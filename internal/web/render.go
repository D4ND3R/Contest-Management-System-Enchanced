// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package web

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/httpx"
)

// Render is the request state the template functions read (what CMS
// puts in the Jinja2 context of every page: translation, now, timezone,
// url, xsrf_form_html), so templates call {{T "Logout"}} or
// {{format_datetime .Timestamp}} without passing it around, macros
// included. Build it with Site.NewRender; the zero value renders in
// English, UTC, with "." as root.
type Render struct {
	// Lang is the interface language (nil: English).
	Lang *Lang
	// TZ is the server time zone for dates (D6); nil means UTC.
	TZ *time.Location
	// Now is the time of the request (CMS's self.timestamp).
	Now time.Time
	// Root is the relative URL of the root of the site (CMS url()).
	Root URL
	// Nonce is the CSP nonce of the page ({{nonce}}).
	Nonce string
	// Extra is free for the site's own functions (contest URL...).
	Extra any

	xsrf      func() string
	xsrfToken string
}

// XSRFToken returns the masked CSRF token for the forms of the page,
// setting the _xsrf cookie on first use if the browser has none; ""
// when the Render has no CSRF protection.
func (rc *Render) XSRFToken() string {
	if rc == nil || rc.xsrf == nil {
		return ""
	}
	if rc.xsrfToken == "" {
		rc.xsrfToken = rc.xsrf()
	}
	return rc.xsrfToken
}

// locale returns the formats of the page language.
func (rc *Render) locale() Locale {
	if rc != nil && rc.Lang != nil && rc.Lang.Locale != nil {
		return rc.Lang.Locale
	}
	return BasicLocale{Code: "en"}
}

func (rc *Render) tz() *time.Location {
	if rc != nil && rc.TZ != nil {
		return rc.TZ
	}
	return time.UTC
}

func (rc *Render) lang() *Lang {
	if rc == nil {
		return nil
	}
	return rc.Lang
}

func (rc *Render) root() URL {
	if rc == nil || rc.Root == "" {
		return "."
	}
	return rc.Root
}

// zeroRender is used when a page is executed without request state.
func zeroRender() *Render {
	return &Render{TZ: time.UTC, Now: time.Now(), Root: "."}
}

// RequestRoot returns the relative root URL of a request (CMS
// get_url_root(request.path)), computed from the path as the browser
// sent it: http.StripPrefix and the decoding of %2F do not change it.
func RequestRoot(r *http.Request) URL {
	p := r.RequestURI
	if !strings.HasPrefix(p, "/") {
		p = r.URL.EscapedPath()
	}
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}
	return URLRoot(p)
}

// Site groups what every page of a site needs: the templates, the
// languages, CSRF protection and the server time zone.
type Site struct {
	Engine    *Engine
	Languages *Languages
	CSRF      *CSRF
	// Location returns the server time zone (D6); nil means UTC.
	Location func() *time.Location
	// Clock returns the current time; nil means time.Now.
	Clock func() time.Time
	// Log receives template errors; nil discards them.
	Log *slog.Logger
	// ErrorPage is the template of error pages (CMS "error.html"),
	// rendered with ErrorData; "" sends the plain critical error text.
	ErrorPage string
}

// ErrorData is the data of error pages (CMS error.html: status_code).
type ErrorData struct {
	StatusCode int
}

// CriticalError is the text CMS sends when not even the error page can
// be rendered (and on CSRF failures, which happen before the page state
// exists).
const CriticalError = "A critical error has occurred :-("

// NewRender returns the render state of a request in lang (nil:
// English) and sets Content-Language as CMS does. The CSRF token, and
// its cookie, are only created if the page uses them.
func (s *Site) NewRender(w http.ResponseWriter, r *http.Request, lang *Lang) *Render {
	rc := &Render{
		Lang:  lang,
		TZ:    time.UTC,
		Now:   time.Now(),
		Root:  RequestRoot(r),
		Nonce: httpx.Nonce(r.Context()),
	}
	if s.Location != nil {
		if loc := s.Location(); loc != nil {
			rc.TZ = loc
		}
	}
	if s.Clock != nil {
		rc.Now = s.Clock()
	}
	if s.CSRF != nil {
		rc.xsrf = func() string { return s.CSRF.FormToken(w, r) }
	}
	id := "en"
	if lang != nil {
		id = lang.ID
	}
	w.Header().Set("Content-Language", id)
	return rc
}

// Page renders the page name with status. If the template fails, the
// error is logged and the error page is sent with status 500 instead.
func (s *Site) Page(w http.ResponseWriter, r *http.Request, rc *Render, status int, name string, data any) {
	err := s.Engine.Render(w, r, status, name, rc, data)
	if err == nil {
		return
	}
	s.logError(r, "rendering page", name, err)
	s.Error(w, r, rc, http.StatusInternalServerError)
}

// Error sends the error page with status (CMS write_error); without a
// Render or an error template, or if that fails too, it sends the
// critical error text.
func (s *Site) Error(w http.ResponseWriter, r *http.Request, rc *Render, status int) {
	if rc != nil && s.ErrorPage != "" && s.Engine != nil {
		err := s.Engine.Render(w, r, status, s.ErrorPage, rc, ErrorData{StatusCode: status})
		if err == nil {
			return
		}
		s.logError(r, "rendering error page", s.ErrorPage, err)
	}
	PlainError(w, status)
}

// PlainError sends the critical error text of CMS with status.
func PlainError(w http.ResponseWriter, status int) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=UTF-8")
	h.Set("Cache-Control", "no-cache, must-revalidate")
	h.Del("Content-Length")
	h.Del("Etag")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(CriticalError))
}

func (s *Site) logError(r *http.Request, msg, name string, err error) {
	if s.Log == nil {
		return
	}
	s.Log.Error(msg, "template", name, "path", r.URL.Path, "err", err)
}
