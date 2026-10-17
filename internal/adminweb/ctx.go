// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/server/admin/handlers/base.py (BaseHandler) of CMS:
// Copyright © 2010-2013 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2016 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2012-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>

package adminweb

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/httpx"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/i18n"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/web"
)

// formMemory bounds the memory used to parse a multipart body: larger
// file parts go to temporary files (removed by net/http after the
// request).
const formMemory = 1 << 20

// Ctx is the state of a request (CMS BaseHandler after prepare): what
// every handler and template needs.
type Ctx struct {
	S *Server
	W http.ResponseWriter
	R *http.Request
	// Now is the time of the request (CMS make_datetime()).
	Now time.Time
	// Admin is the logged-in admin; nil only on PermPublic routes.
	Admin *Admin
	// Contest is the contest of the page (CMS self.contest): set by
	// ContestFromPath or LoadContest, it switches the sidebar of
	// base.html to the contest menu. Nil on global pages.
	Contest *Contest
	// Lang is the interface language (Accept-Language and the
	// "language" cookie; English or Spanish).
	Lang *web.Lang
	// RC is the render state of the templates.
	RC *web.Render
	// Route is the route being served.
	Route Route
	// LiveSince is the id of the last live event before the page read
	// its data (the page's EventSource starts from it).
	LiveSince string
}

// Context returns the context of the request.
func (c *Ctx) Context() context.Context { return c.R.Context() }

// T translates a message (admin catalogs, then CMS's).
func (c *Ctx) T(msgid string) string { return c.Lang.T(msgid) }

// Tn translates a message with plural forms.
func (c *Ctx) Tn(msgid, plural string, n int) string { return c.Lang.Tn(msgid, plural, n) }

// Arg returns the last value of a query or form argument (Tornado
// get_argument, cleaned and stripped): ok is false if absent.
func (c *Ctx) Arg(name string) (string, bool) { return web.Arg(c.R, name) }

// ArgDefault returns Arg or def.
func (c *Ctx) ArgDefault(name, def string) string { return web.ArgDefault(c.R, name, def) }

// URL returns the relative URL of a page of the site (CMS url(...)):
// c.URL("contest", 3, "users") is "../../contest/3/users" from
// /contest/3/questions.
func (c *Ctx) URL(components ...any) string { return c.RC.Root.Join(components...) }

// Can reports whether the admin has the permission p (perm.go).
func (c *Ctx) Can(p Perm) bool { return c.Admin.Can(p) }

// ClientAddr is the client's address as seen through num_proxies trusted
// proxies (CMS request.remote_ip); the zero Addr if it cannot be parsed.
func (c *Ctx) ClientAddr() netip.Addr { return clientAddr(c.R) }

func clientAddr(r *http.Request) netip.Addr {
	a, err := netip.ParseAddr(httpx.ClientIP(r))
	if err != nil {
		return netip.Addr{}
	}
	return a.Unmap().WithZone("")
}

// PathID returns the path value name as an id. Route paths validate ids
// before the handler runs ([0-9]+ fitting in 32 bits), so it only fails
// for a name the route does not have, which is a bug: 404.
func (c *Ctx) PathID(name string) (int32, bool) {
	n, err := strconv.ParseInt(c.R.PathValue(name), 10, 32)
	if err != nil {
		return 0, false
	}
	return int32(n), true
}

// Render sends a page with status 200: data embeds *Page (Ctx.Page).
// Template failures become the error page (500).
func (c *Ctx) Render(name string, data any) { c.RenderStatus(http.StatusOK, name, data) }

// RenderStatus sends a page with status.
func (c *Ctx) RenderStatus(status int, name string, data any) {
	c.S.site.Page(c.W, c.R, c.RC, status, name, data)
}

// errorData is the data of error.html.
type errorData struct {
	*Page
	StatusCode int
}

// Error sends the error page with status (CMS HTTPError + write_error,
// which renders error.html with the base parameters of the page).
func (c *Ctx) Error(status int) {
	p, err := c.Page()
	if err != nil {
		c.S.log.Error("loading the error page", "path", c.R.URL.Path, "err", err)
		web.PlainError(c.W, status)
		return
	}
	if err := c.S.site.Engine.Render(c.W, c.R, status, "error.html", c.RC, errorData{Page: p, StatusCode: status}); err != nil {
		c.S.log.Error("rendering the error page", "path", c.R.URL.Path, "err", err)
		web.PlainError(c.W, status)
	}
}

// NotFound sends the error page with 404 (CMS safe_get_item).
func (c *Ctx) NotFound() { c.Error(http.StatusNotFound) }

// Forbidden sends the error page with 403 ("Admin is not authorized").
func (c *Ctx) Forbidden() { c.Error(http.StatusForbidden) }

// BadRequest sends the error page with 400 (Tornado MissingArgumentError).
func (c *Ctx) BadRequest() { c.Error(http.StatusBadRequest) }

// NotImplemented answers the routes whose handlers are not written yet.
func (c *Ctx) NotImplemented() { c.Error(http.StatusNotImplemented) }

// Fail logs an unexpected error and sends the error page with 500 (or
// nothing if the client went away).
func (c *Ctx) Fail(err error) {
	if c.Context().Err() != nil {
		return
	}
	c.S.log.Error("admin page failed", "method", c.R.Method, "path", c.R.URL.Path, "err", err)
	c.Error(http.StatusInternalServerError)
}

// Redirect answers 302 with a relative Location (Tornado redirect):
// c.Redirect(c.URL("contest", id)).
func (c *Ctx) Redirect(url string) {
	web.Redirect(c.W, url, http.StatusFound)
}

// WriteText sends a plain body: CMS's DELETE handlers answer the page to
// go to next ("../../contests"), which ajax_delete opens.
func (c *Ctx) WriteText(s string) {
	h := c.W.Header()
	h.Set("Content-Type", "text/html; charset=UTF-8")
	c.W.WriteHeader(http.StatusOK)
	_, _ = c.W.Write([]byte(s))
}

// JSON sends v as JSON (CMS json()).
func (c *Ctx) JSON(status int, v any) {
	_ = web.WriteJSON(c.W, status, v)
}

// Contest is a contest with what base.html shows of it: the times of its
// main group (the countdown of the sidebar) and the number of unanswered
// questions (the badge of the menu).
type Contest struct {
	q.Contest
	// MainGroup is nil when the contest has none.
	MainGroup *q.Group
	// Unanswered counts the questions neither answered nor ignored.
	Unanswered int64
}

// LoadContest returns the contest id with its main group and unanswered
// questions (one query), nil if it does not exist. It does not set
// c.Contest.
func (c *Ctx) LoadContest(id int32) (*Contest, error) {
	row, err := c.S.st.Q.AdminCoreContest(c.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ct := &Contest{Contest: row.Contest, Unanswered: row.Unanswered}
	if row.MainStart != nil {
		ct.MainGroup = &q.Group{
			ID:              *row.Contest.MainGroupID,
			Name:            deref(row.MainName),
			Start:           *row.MainStart,
			Stop:            *row.MainStop,
			AnalysisEnabled: *row.MainAnalysisEnabled,
			AnalysisStart:   *row.MainAnalysisStart,
			AnalysisStop:    *row.MainAnalysisStop,
			PerUserTime:     row.MainPerUserTime,
			ContestID:       row.Contest.ID,
		}
	}
	return ct, nil
}

// ContestFromPath loads the contest of the path value {contest_id} and
// makes it the contest of the page (c.Contest). It answers 404 (or 500)
// itself and returns false when the handler must stop.
func (c *Ctx) ContestFromPath() bool {
	id, ok := c.PathID("contest_id")
	if !ok {
		c.NotFound()
		return false
	}
	return c.SetContest(id)
}

// SetContest loads contest id as the contest of the page (CMS
// self.contest = ...), for pages of an object of a contest (a task, a
// submission): 404 if it does not exist.
func (c *Ctx) SetContest(id int32) bool {
	ct, err := c.LoadContest(id)
	if err != nil {
		c.Fail(err)
		return false
	}
	if ct == nil {
		c.NotFound()
		return false
	}
	c.Contest = ct
	return true
}

func deref[T any](p *T) T {
	var z T
	if p == nil {
		return z
	}
	return *p
}

// Notification levels; CMS's admin shows every notification alike
// ("notification"), the level only changes the logs.
const (
	levelSuccess = "success"
	levelError   = "error"
)

// Notify queues a notification for the admin, shown by the next poll of
// /notifications (CMS add_notification): subject and text are msgids
// translated now, text formatted with params ("%(name)s" placeholders).
func (c *Ctx) Notify(subject, text string, params map[string]any) {
	c.notify(levelSuccess, c.T(subject), c.format(text, params))
}

// NotifyError queues a notification about a failure; detail is shown
// as it is (an error message, CMS repr(error)).
func (c *Ctx) NotifyError(subject, detail string) {
	c.notify(levelError, c.T(subject), detail)
}

func (c *Ctx) format(text string, params map[string]any) string {
	if text == "" {
		return ""
	}
	msg := c.T(text)
	if params != nil {
		msg = i18n.Format(msg, params)
	}
	return msg
}

func (c *Ctx) notify(level, subject, text string) {
	if c.Admin == nil {
		return
	}
	c.S.notes.push(c.Context(), c.Admin.ID, notification{
		Timestamp: web.Timestamp(float64(c.Now.UnixMicro()) / 1e6),
		Subject:   subject,
		Text:      text,
		Level:     level,
	})
}

// Commit reports the outcome of a Write as CMS's try_commit: the
// notification "Operation successful." or "Operation failed." (with the
// database's message for constraint violations), and true on success.
// Errors that are not the request's fault are logged.
func (c *Ctx) Commit(err error) bool {
	if err == nil {
		c.Notify(N("Operation successful."), "", nil)
		return true
	}
	var pe *pgconn.PgError
	var fe *FieldError
	switch {
	case errors.As(err, &fe):
		c.NotifyError(N("Operation failed."), fe.Error())
	case errors.As(err, &pe) && (pe.Code[:2] == "23" || pe.Code[:2] == "22"):
		// Integrity (23...) and data (22...) errors: the admin's input.
		c.NotifyError(N("Operation failed."), pe.Message)
	default:
		c.S.log.Error("admin write failed", "path", c.R.URL.Path, "err", err)
		c.NotifyError(N("Operation failed."), c.T("Internal error: see the logs of the server."))
	}
	return false
}

// FieldError is an invalid value in a form (CMS's "Invalid field(s)"
// notification). Handlers return it from Write to refuse a change.
type FieldError struct{ Msg string }

func (e *FieldError) Error() string { return e.Msg }

// Invalid returns a FieldError with a formatted message (English, as
// CMS's repr(error)).
func Invalid(format string, args ...any) error {
	return &FieldError{Msg: fmt.Sprintf(format, args...)}
}

// N marks a msgid translated later (Notify translates it).
func N(msgid string) string { return msgid }
