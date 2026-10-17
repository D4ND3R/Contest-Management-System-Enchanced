// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/server/admin/authentication.py (AWSAuthMiddleware),
// cms/server/admin/handlers/main.py (LoginHandler, LogoutHandler) and
// cms/server/admin/handlers/base.py (get_current_user,
// require_permission) of CMS:
// Copyright © 2010-2013 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2017 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2012-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>

package adminweb

// Authentication: the "awslogin" cookie holds the admin's id and a
// fingerprint of their authentication string, signed with a key derived
// from secret_key (D5); it lasts admin_web.cookie_duration after the last
// request (CMS refreshes its timestamp on every request but the polling
// ones). It is HttpOnly and SameSite=Lax (CMS: HttpOnly only). A disabled
// admin, a deleted one or a changed password ends the session at the next
// request (CMS checks enabled; the fingerprint is new).
//
// Login attempts go through the limits of D42 (per address, per user
// name) and a queue of bcrypt checks with one check per core, as the
// contestant site.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/web"
)

// cookieName is CMS's AWSAuthMiddleware.COOKIE.
const cookieName = "awslogin"

// cookieDuration is admin_web.cookie_duration (at least a minute).
func (s *Server) cookieDuration() time.Duration {
	return max(s.cfg.AdminWeb.CookieDuration.D(), time.Minute)
}

// fingerprint identifies the authentication string of an admin in the
// cookie without revealing it.
func (s *Server) fingerprint(authentication string) string {
	m := hmac.New(sha256.New, s.authKey)
	m.Write([]byte(authentication))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:18])
}

// setLoginCookie logs the admin in (or refreshes the session).
func (s *Server) setLoginCookie(w http.ResponseWriter, r *http.Request, a *Admin, now time.Time) {
	value := strconv.Itoa(int(a.ID)) + ":" + s.fingerprint(a.Authentication)
	web.SetCookie(w, r, &http.Cookie{Name: cookieName, Value: s.signer.Sign(cookieName, []byte(value), now),
		Path: "/", MaxAge: int(s.cookieDuration() / time.Second)})
}

// clearLoginCookie logs out.
func (s *Server) clearLoginCookie(w http.ResponseWriter, r *http.Request) {
	web.ClearCookie(w, r, cookieName, "/")
}

// currentAdmin returns the admin of the login cookie (CMS
// get_current_user): nil without a valid cookie or if the admin is gone
// or disabled. hadCookie says whether the browser sent one.
func (s *Server) currentAdmin(ctx context.Context, r *http.Request, now time.Time) (a *Admin, hadCookie bool, err error) {
	ck, err := r.Cookie(cookieName)
	if err != nil {
		return nil, false, nil
	}
	value, err := s.signer.Verify(cookieName, ck.Value, s.cookieDuration(), now)
	if err != nil {
		return nil, true, nil
	}
	idStr, fp, ok := strings.Cut(string(value), ":")
	if !ok {
		return nil, true, nil
	}
	id, err := strconv.ParseInt(idStr, 10, 32)
	if err != nil {
		return nil, true, nil
	}
	row, err := s.st.Q.GetAdmin(ctx, int32(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, true, nil
	}
	if err != nil {
		return nil, true, err
	}
	if !row.Enabled || !hmac.Equal([]byte(fp), []byte(s.fingerprint(row.Authentication))) {
		return nil, true, nil
	}
	return &Admin{Admin: row}, true, nil
}

// route adapts a handler: path values, body limit, CSRF, login and
// permission, in this order.
func (s *Server) route(rt Route) http.HandlerFunc {
	params := pathParams(rt.Path)
	limit := int64(MaxFormBody)
	if rt.Upload {
		limit = MaxBody
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if !validPath(r, params) {
			notFound(w, r)
			return
		}
		if r.Body != nil && r.Body != http.NoBody {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			// Every unsafe request carries the _xsrf token (the form
			// field or the X-XSRFToken header of the scripts).
			if err := s.csrf.Check(r); err != nil {
				status := http.StatusForbidden
				if errors.Is(err, web.ErrBodyTooLarge) {
					status = http.StatusRequestEntityTooLarge
				}
				s.log.Info("request refused by the CSRF check", "method", r.Method, "path", r.URL.Path, "err", err)
				web.PlainError(w, status)
				return
			}
		}
		c, ok := s.newCtx(w, r, rt)
		if !ok {
			return
		}
		if r.Method == http.MethodHead {
			// CMS's handlers do not implement HEAD: Tornado answers 405.
			c.Error(http.StatusMethodNotAllowed)
			return
		}
		if r.Method != http.MethodGet {
			if err := web.ParseForm(w, r, limit, formMemory); err != nil {
				c.Error(web.FormStatus(err))
				return
			}
		}
		if !c.authorized() {
			return
		}
		rt.Handler(c)
	}
}

// authorized applies the route's permission (CMS require_permission):
// without a login, GET goes to the login page with ?next= and other
// methods get 403; a logged-in admin without the permission gets 403,
// unless the route is SelfAllowed and about themselves.
func (c *Ctx) authorized() bool {
	p := c.Route.Perm
	if p == PermPublic {
		return true
	}
	if c.Admin == nil {
		if c.R.Method == http.MethodGet {
			c.Redirect(c.URL("login") + "?" + web.QueryString("next", c.R.RequestURI))
			return false
		}
		c.Forbidden()
		return false
	}
	if c.Admin.Can(p) {
		return true
	}
	if c.Route.SelfAllowed {
		if id, ok := c.PathID("admin_id"); ok && id == c.Admin.ID {
			return true
		}
	}
	c.S.log.Info("admin is not authorized", "admin", c.Admin.Username, "method", c.R.Method,
		"path", c.R.URL.Path, "requires", p.String())
	c.Forbidden()
	return false
}

// newCtx prepares a request (CMS BaseHandler.prepare and
// get_current_user): it answers itself (500) and returns false when the
// request cannot go on.
func (s *Server) newCtx(w http.ResponseWriter, r *http.Request, rt Route) (*Ctx, bool) {
	c := &Ctx{S: s, W: w, R: r, Now: s.now(), Route: rt}
	if s.hub != nil {
		// Before reading anything: what is published from now on is
		// replayed to the page's stream.
		c.LiveSince = s.hub.LastID()
	}
	c.Lang = s.ui.Choose(r, s.ui.All()).Lang
	c.RC = s.site.NewRender(w, r, c.Lang)
	c.RC.Now = c.Now
	c.RC.Extra = c
	w.Header().Set("Cache-Control", "no-cache, must-revalidate")

	a, hadCookie, err := s.currentAdmin(r.Context(), r, c.Now)
	if err != nil {
		s.log.Error("loading the admin of the session", "err", err)
		web.PlainError(w, http.StatusInternalServerError)
		return nil, false
	}
	switch {
	case a == nil && hadCookie:
		s.clearLoginCookie(w, r)
	case a != nil && !rt.NoRefresh:
		s.setLoginCookie(w, r, a, c.Now)
	}
	c.Admin = a
	return c, true
}

// loginPage is GET /login (CMS SimpleHandler("login.html")).
func (s *Server) loginPage(c *Ctx) {
	next, hasNext := c.Arg("next")
	data := loginData{Admin: c.Admin, LoginError: c.ArgDefault("login_error", "") != ""}
	if hasNext {
		data.Next = &next
	}
	c.Render("login.html", data)
}

// loginData is the data of login.html.
type loginData struct {
	// Admin is the logged-in admin ("You are already logged in").
	Admin      *Admin
	LoginError bool
	// Next is the next argument, nil if absent.
	Next *string
}

// loginPost is POST /login (CMS LoginHandler.post): on success it goes
// to next (a local path) or the overview; on failure back to the login
// form with login_error.
func (s *Server) loginPost(c *Ctx) {
	next, hasNext := c.Arg("next")
	errArgs := []string{"login_error", "true"}
	if hasNext {
		errArgs = append(errArgs, "next", next)
	}
	errorPage := c.URL("login") + "?" + web.QueryString(errArgs...)
	target := web.NormalizeNextPage(next, hasNext, c.RC.Root, c.URL())

	username := c.ArgDefault("username", "")
	password := c.ArgDefault("password", "")
	addr := c.ClientAddr()
	if ok, wait := s.loginByIP.Allow(web.AddrKey(addr)); !ok {
		s.log.Warn("too many admin login attempts from the address", "ip", addr.String())
		web.TooManyRequests(c.W, wait)
		return
	}
	userKey := "u\x00" + clip(username)
	if ok, wait := s.loginByUser.Allow(userKey); !ok {
		s.log.Warn("too many admin login attempts for the user", "username", clip(username), "ip", addr.String())
		web.TooManyRequests(c.W, wait)
		return
	}
	row, err := s.st.Q.GetAdminByUsername(c.Context(), username)
	if errors.Is(err, pgx.ErrNoRows) {
		s.log.Warn("nonexistent admin account", "username", clip(username), "ip", addr.String())
		c.Redirect(errorPage)
		return
	}
	if err != nil {
		c.Fail(err)
		return
	}
	var allowed bool
	var verr error
	check := func() { allowed, verr = web.ValidatePassword(row.Authentication, password) }
	if strings.HasPrefix(row.Authentication, "bcrypt:") {
		if err := s.bcrypt.Do(c.Context(), web.AddrKey(addr), check); err != nil {
			s.loginByUser.Refund(userKey)
			if errors.Is(err, web.ErrBcryptBusy) {
				web.TooManyRequests(c.W, 5*time.Second)
			}
			return
		}
	} else {
		check()
	}
	if verr != nil {
		s.log.Warn("unable to validate the password of an admin", "username", row.Username, "err", verr)
		allowed = false
	}
	if !allowed || !row.Enabled {
		if !allowed {
			s.log.Info("admin login error", "username", row.Username, "ip", addr.String())
		} else {
			s.loginByUser.Refund(userKey)
			s.log.Info("admin login successful, but the account is disabled", "username", row.Username, "ip", addr.String())
		}
		c.Redirect(errorPage)
		return
	}
	s.loginByUser.Refund(userKey)
	s.log.Info("admin logged in", "username", row.Username, "ip", addr.String())
	s.setLoginCookie(c.W, c.R, &Admin{Admin: row}, c.Now)
	c.Redirect(target)
}

// logoutPost is POST /logout (CMS LogoutHandler).
func (s *Server) logoutPost(c *Ctx) {
	s.clearLoginCookie(c.W, c.R)
	c.Redirect(c.URL())
}

// clip shortens a client's text for the logs and the limiter keys.
func clip(s string) string {
	const n = 64
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
