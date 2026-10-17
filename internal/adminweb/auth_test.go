// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package adminweb

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
)

// TestLoginMatrix checks the login of CMS's LoginHandler: right and wrong
// passwords (bcrypt and plaintext), unknown and disabled admins, the next
// argument, the "already logged in" page and the logout.
func TestLoginMatrix(t *testing.T) {
	ts := newTestSite(t, testOptions{})
	cases := []struct {
		name, user, password, next string
		ok                         bool
		location                   string
	}{
		{"bcrypt", "admin", "admin", "", true, "."},
		{"plaintext", "ro", "ro", "", true, "."},
		{"wrong password", "admin", "nope", "", false, "./login?login_error=true"},
		{"unknown admin", "nobody", "x", "", false, "./login?login_error=true"},
		{"disabled", "off", "off", "", false, "./login?login_error=true"},
		{"empty", "", "", "", false, "./login?login_error=true"},
		{"next", "msg", "msg", "/contest/1/questions?x=1", true, "./contest/1/questions?x=1"},
		{"next kept on error", "msg", "bad", "/admins", false, "./login?login_error=true&next=%2Fadmins"},
		{"external next", "msg", "msg", "https://evil.example/", true, "."},
		{"schemeless next", "msg", "msg", "//evil.example/x", true, "."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := ts.Client()
			form := url.Values{"username": {tc.user}, "password": {tc.password}}
			if tc.next != "" {
				form.Set("next", tc.next)
			}
			r := c.Post("/login", form)
			if r.Status != http.StatusFound || r.Location() != tc.location {
				t.Fatalf("login: %d %q, want 302 %q", r.Status, r.Location(), tc.location)
			}
			got := c.Get("/admins")
			if tc.ok != (got.Status == http.StatusOK) {
				t.Fatalf("after login: GET /admins %d", got.Status)
			}
			if !tc.ok && (got.Status != http.StatusFound || got.Location() != "./login?next=%2Fadmins") {
				t.Fatalf("not logged in: %d %q", got.Status, got.Location())
			}
		})
	}

	c := ts.Client()
	if r := c.Get("/login"); r.Status != 200 || !strings.Contains(r.Body, `name="username"`) {
		t.Fatalf("login page: %d", r.Status)
	}
	if r := c.Get("/login?login_error=true&next=/x"); !strings.Contains(r.Body, "Failed to log in.") ||
		!strings.Contains(r.Body, `<input type="hidden" name="next" value="/x">`) {
		t.Fatalf("login page with an error: %s", r.Body)
	}
	c.Login("admin")
	if r := c.Get("/login"); !strings.Contains(r.Body, "You are already logged in.") {
		t.Fatalf("login page when logged in: %s", r.Body)
	}
	if r := c.Post("/logout", nil); r.Status != http.StatusFound || r.Location() != "." {
		t.Fatalf("logout: %d %q", r.Status, r.Location())
	}
	if r := c.Get("/"); r.Status != http.StatusFound {
		t.Fatalf("after logout: %d", r.Status)
	}
}

// TestLoginCookie checks the session cookie: flags, refresh, expiry, a
// tampered value, and the end of sessions of disabled admins and changed
// passwords.
func TestLoginCookie(t *testing.T) {
	ts := newTestSite(t, testOptions{Configure: func(c *config.Config) {
		c.AdminWeb.CookieDuration = config.Duration(time.Hour)
	}})
	c := ts.Client()
	r := c.Post("/login", url.Values{"username": {"ro"}, "password": {"ro"}})
	var ck *http.Cookie
	for _, x := range (&http.Response{Header: r.Header}).Cookies() {
		if x.Name == cookieName {
			ck = x
		}
	}
	if ck == nil || !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode || ck.MaxAge != 3600 || ck.Path != "/" {
		t.Fatalf("login cookie: %+v", ck)
	}
	if strings.Contains(ck.Value, "plaintext") || strings.Contains(ck.Value, ":ro") {
		t.Fatalf("the cookie leaks the password: %s", ck.Value)
	}

	// Each page refreshes the session; polling ones do not.
	ts.Clock.Add(50 * time.Minute)
	if r := c.Get("/admins"); r.Status != 200 || r.Header.Get("Set-Cookie") == "" {
		t.Fatalf("page: %d, cookie %q", r.Status, r.Header.Get("Set-Cookie"))
	}
	if r := c.RPC("EvaluationService", "workers_status", map[string]any{}); r.Header.Get("Set-Cookie") != "" {
		t.Fatalf("rpc refreshed the session")
	}
	ts.Clock.Add(50 * time.Minute)
	if r := c.Get("/admins"); r.Status != 200 {
		t.Fatalf("after a refresh: %d", r.Status)
	}
	ts.Clock.Add(61 * time.Minute)
	if r := c.Get("/admins"); r.Status != http.StatusFound {
		t.Fatalf("expired session: %d", r.Status)
	}

	// Tampering.
	c = ts.Client()
	c.Login("ro")
	u, _ := url.Parse(ts.URL)
	v := c.Cookie(cookieName)
	c.HTTP.Jar.SetCookies(u, []*http.Cookie{{Name: cookieName, Value: strings.Replace(v, "1.", "1.9", 1), Path: "/"}})
	if r := c.Get("/admins"); r.Status != http.StatusFound {
		t.Fatalf("tampered cookie: %d", r.Status)
	}

	// Disabling the admin or changing the password ends the session.
	c = ts.Client()
	c.Login("ro")
	ts.Exec(`UPDATE admins SET enabled = false WHERE username = 'ro'`)
	if r := c.Get("/admins"); r.Status != http.StatusFound {
		t.Fatalf("disabled admin: %d", r.Status)
	}
	ts.Exec(`UPDATE admins SET enabled = true WHERE username = 'ro'`)
	c.Login("ro")
	ts.Exec(`UPDATE admins SET authentication = 'plaintext:new' WHERE username = 'ro'`)
	if r := c.Get("/admins"); r.Status != http.StatusFound {
		t.Fatalf("changed password: %d", r.Status)
	}
}

// TestLoginLimits checks the limit per user name: after 10 failures the
// next attempt gets 429, and other users are not affected.
func TestLoginLimits(t *testing.T) {
	ts := newTestSite(t, testOptions{})
	c := ts.Client()
	for i := range 10 {
		if r := c.Post("/login", url.Values{"username": {"msg"}, "password": {"bad"}}); r.Status != http.StatusFound {
			t.Fatalf("attempt %d: %d", i, r.Status)
		}
	}
	if r := c.Post("/login", url.Values{"username": {"msg"}, "password": {"msg"}}); r.Status != http.StatusTooManyRequests ||
		r.Header.Get("Retry-After") == "" {
		t.Fatalf("attempt 11: %d", r.Status)
	}
	c.Login("ro")
}
