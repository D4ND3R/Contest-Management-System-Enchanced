// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// The cases of TestValidateLogin and TestAuthenticate are those of
// cmstestsuite/unit_tests/server/contest/authentication_test.py of CMS:
// Copyright © 2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>

package contestweb

import (
	"context"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
)

// authFixture tests the authentication functions on the demo contest as
// alice (password "demo").
type authFixture struct {
	t  *testing.T
	ts *testSite
}

func newAuthFixture(t *testing.T, configure func(*config.Config)) *authFixture {
	ts := newTestSite(t, testOptions{Configure: func(c *config.Config) {
		c.ContestWeb.AdminToken = "admin-token"
		if configure != nil {
			configure(c)
		}
	}})
	// A known starting point: password authentication on, no IP rules,
	// the passwords of the users (the dump gives every participation
	// its own).
	ts.Exec(`UPDATE contests SET allow_password_authentication = true, ip_restriction = false,
		ip_autologin = false, block_hidden_participations = false`)
	ts.Exec(`UPDATE participations SET password = NULL`)
	return &authFixture{t: t, ts: ts}
}

func (f *authFixture) contest() *Contest {
	f.t.Helper()
	c, err := f.ts.S.loadContest(context.Background(), "demo")
	if err != nil || c == nil {
		f.t.Fatalf("loading the contest: %v", err)
	}
	return c
}

func addr(s string) netip.Addr { return netip.MustParseAddr(s) }

// login runs ValidateLogin and reports whether it let alice in.
func (f *authFixture) login(username, password, ip, token string) (LoginResult, bool) {
	f.t.Helper()
	res, err := f.ts.S.ValidateLogin(context.Background(), f.contest(), demoNow, username, password, addr(ip), token)
	if err != nil {
		f.t.Fatal(err)
	}
	if (res.Participation == nil) != (res.Data == "") {
		f.t.Fatalf("participation %v but login data %q", res.Participation, res.Data)
	}
	return res, res.Participation != nil
}

func (f *authFixture) loginOK(username, password, ip, token string) string {
	f.t.Helper()
	res, ok := f.login(username, password, ip, token)
	if !ok {
		f.t.Fatalf("login %s/%s from %s (token %q) failed", username, password, ip, token)
	}
	if res.Participation.User.Username != username || res.Participation.ContestID != f.ts.Contest {
		f.t.Fatalf("logged in as %s in contest %d", res.Participation.User.Username, res.Participation.ContestID)
	}
	return res.Data
}

func (f *authFixture) loginFails(username, password, ip, token string) {
	f.t.Helper()
	if _, ok := f.login(username, password, ip, token); ok {
		f.t.Fatalf("login %s/%s from %s (token %q) succeeded", username, password, ip, token)
	}
}

// TestValidateLogin ports the tests of validate_login.
func TestValidateLogin(t *testing.T) {
	t.Parallel()
	f := newAuthFixture(t, nil)
	ts := f.ts

	// test_successful_login, test_no_user
	f.loginOK("alice", "demo", "127.0.0.1", "")
	f.loginFails("nobody", "demo", "127.0.0.1", "")
	f.loginFails("alice", "wrong", "127.0.0.1", "")
	f.loginFails("alice", "", "127.0.0.1", "")
	// test_no_participation_for_user_in_contest
	ts.Exec(`INSERT INTO users (first_name, last_name, username, password) VALUES ('D', 'D', 'dave', 'plaintext:demo')`)
	f.loginFails("dave", "demo", "127.0.0.1", "")

	// test_participation_specific_password
	ts.Exec(`UPDATE participations SET password = 'plaintext:other' WHERE id = $1`, ts.Participation("alice"))
	f.loginFails("alice", "demo", "127.0.0.1", "")
	f.loginOK("alice", "other", "127.0.0.1", "")
	ts.Exec(`UPDATE participations SET password = NULL`)

	// bcrypt passwords (CMS hash_password).
	hashed, err := HashPassword("secret")
	if err != nil {
		t.Fatal(err)
	}
	ts.Exec(`UPDATE users SET password = $1 WHERE username = 'bob'`, hashed)
	f.loginOK("bob", "secret", "127.0.0.1", "")
	f.loginFails("bob", "demo", "127.0.0.1", "")

	// test_invalid_password_stored_in_user / _in_participation: never
	// a match, never an error.
	ts.Exec(`UPDATE users SET password = 'demo' WHERE username = 'carol'`)
	f.loginFails("carol", "demo", "127.0.0.1", "")
	ts.Exec(`UPDATE users SET password = 'plaintext:demo' WHERE username = 'carol'`)
	ts.Exec(`UPDATE participations SET password = 'other' WHERE id = $1`, ts.Participation("carol"))
	f.loginFails("carol", "other", "127.0.0.1", "")
	ts.Exec(`UPDATE participations SET password = NULL`)

	// test_unallowed_password_authentication
	ts.Exec(`UPDATE contests SET allow_password_authentication = false`)
	f.loginFails("alice", "demo", "127.0.0.1", "")
	// test_impersonation_overrides_unallowed_password_authentication
	f.loginOK("alice", "", "127.0.0.1", "admin-token")
	ts.Exec(`UPDATE contests SET allow_password_authentication = true`)

	// test_unallowed_hidden_participation and its impersonation.
	ts.Exec(`UPDATE contests SET block_hidden_participations = true`)
	ts.Exec(`UPDATE participations SET hidden = true WHERE id = $1`, ts.Participation("alice"))
	f.loginFails("alice", "demo", "127.0.0.1", "")
	f.loginOK("alice", "", "127.0.0.1", "admin-token")
	ts.Exec(`UPDATE contests SET block_hidden_participations = false`)
	f.loginOK("alice", "demo", "127.0.0.1", "")
	ts.Exec(`UPDATE participations SET hidden = false`)

	// test_ip_lock
	ts.Exec(`UPDATE contests SET ip_restriction = true`)
	setIP := func(ips string) {
		if ips == "NULL" {
			ts.Exec(`UPDATE participations SET ip = NULL WHERE id = $1`, ts.Participation("alice"))
			return
		}
		ts.Exec(`UPDATE participations SET ip = $2::cidr[] WHERE id = $1`, ts.Participation("alice"), ips)
	}
	setIP("{10.0.0.0/24}")
	f.loginOK("alice", "demo", "10.0.0.1", "")
	f.loginFails("alice", "wrongpass", "10.0.0.1", "")
	f.loginFails("alice", "demo", "10.0.1.1", "")
	// test_impersonation_overrides_ip_lock
	f.loginOK("alice", "demo", "10.0.0.1", "admin-token")
	f.loginOK("alice", "demo", "10.0.1.1", "admin-token")
	setIP("{10.9.0.0/24,127.0.0.1/32}")
	f.loginOK("alice", "demo", "127.0.0.1", "")
	f.loginFails("alice", "demo", "127.0.0.0", "")
	f.loginOK("alice", "demo", "10.9.0.7", "")
	setIP("{}")
	f.loginFails("alice", "demo", "10.0.0.1", "")
	setIP("NULL")
	f.loginOK("alice", "demo", "10.0.0.1", "")
	// test_deactivated_ip_lock
	setIP("{10.0.0.0/24}")
	ts.Exec(`UPDATE contests SET ip_restriction = false`)
	f.loginOK("alice", "demo", "10.0.1.1", "")

	// test_unsuccessful_impersonation
	f.loginFails("alice", "", "127.0.0.1", "bad-admin-token")
	f.loginFails("nobody", "", "127.0.0.1", "admin-token")
}

// TestImpersonationNotConfigured ports test_impersonation_with_no_config.
func TestImpersonationNotConfigured(t *testing.T) {
	t.Parallel()
	f := newAuthFixture(t, func(c *config.Config) { c.ContestWeb.AdminToken = "" })
	f.loginFails("alice", "", "127.0.0.1", "admin-token")
	f.loginFails("alice", "", "127.0.0.1", "")
	f.loginOK("alice", "demo", "127.0.0.1", "")
}

// authCheck is one call of Authenticate.
type authCheck struct {
	f      *authFixture
	at     time.Time
	cookie string
	header string
	ip     string
}

func (f *authFixture) auth(cookie string) *authCheck {
	return &authCheck{f: f, at: demoNow, cookie: cookie, ip: "10.0.0.1"}
}

func (a *authCheck) run() AuthResult {
	a.f.t.Helper()
	res, err := a.f.ts.S.Authenticate(context.Background(), a.f.contest(), a.at, a.cookie, a.header, addr(a.ip))
	if err != nil {
		a.f.t.Fatal(err)
	}
	return res
}

// refreshed: alice is authenticated and gets new login data.
func (a *authCheck) refreshed() string {
	a.f.t.Helper()
	res := a.run()
	if res.Participation == nil || res.Participation.User.Username != "alice" || res.Impersonated || res.Data == "" {
		a.f.t.Fatalf("authentication at %s from %s: %+v, want alice with new login data", a.at, a.ip, res)
	}
	return res.Data
}

// cleared: alice is authenticated (by address) and the cookie goes.
func (a *authCheck) cleared() {
	a.f.t.Helper()
	res := a.run()
	if res.Participation == nil || res.Participation.User.Username != "alice" || res.Data != "" {
		a.f.t.Fatalf("authentication from %s: %+v, want alice without login data", a.ip, res)
	}
}

func (a *authCheck) impersonated() {
	a.f.t.Helper()
	res := a.run()
	if res.Participation == nil || res.Participation.User.Username != "alice" || !res.Impersonated || res.Data == "" {
		a.f.t.Fatalf("authentication from %s: %+v, want alice impersonated", a.ip, res)
	}
}

func (a *authCheck) fails() {
	a.f.t.Helper()
	res := a.run()
	if res.Participation != nil || res.Data != "" || res.Impersonated {
		a.f.t.Fatalf("authentication at %s from %s (cookie %.20q): %+v, want a failure", a.at, a.ip, a.cookie, res)
	}
}

// TestAuthenticate ports the tests of authenticate_request.
func TestAuthenticate(t *testing.T) {
	t.Parallel()
	t.Run("cookie_contains_timestamp", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, func(c *config.Config) { c.ContestWeb.CookieDuration = config.Duration(time.Minute) })
		cookie := f.loginOK("alice", "demo", "10.0.0.1", "")
		f.auth(cookie).refreshed()
		a := f.auth(cookie)
		a.at = demoNow.Add(50 * time.Second)
		fresh := a.refreshed()
		a.at = demoNow.Add(70 * time.Second)
		a.fails()
		a.cookie = fresh
		a.refreshed()
	})
	t.Run("cookie_contains_password", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, nil)
		cookie := f.loginOK("alice", "demo", "10.0.0.1", "")
		f.ts.Exec(`UPDATE contests SET allow_password_authentication = false`)
		f.auth(cookie).fails()
		f.ts.Exec(`UPDATE contests SET allow_password_authentication = true`)
		f.ts.Exec(`UPDATE users SET password = 'plaintext:newpass' WHERE username = 'alice'`)
		f.auth(cookie).fails()
		f.ts.Exec(`UPDATE participations SET password = 'plaintext:demo' WHERE id = $1`, f.ts.Participation("alice"))
		f.auth(cookie).refreshed()
		f.ts.Exec(`UPDATE users SET password = 'plaintext:demo' WHERE username = 'alice'`)
		f.ts.Exec(`UPDATE participations SET password = 'plaintext:newpass' WHERE id = $1`, f.ts.Participation("alice"))
		f.auth(cookie).fails()
	})
	t.Run("ip_autologin", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, nil)
		f.ts.Exec(`UPDATE contests SET ip_autologin = true, allow_password_authentication = false`)
		f.ts.Exec(`UPDATE participations SET ip = '{10.0.0.1/32}' WHERE id = $1`, f.ts.Participation("alice"))
		f.auth("").cleared()
		a := f.auth("")
		a.ip = "10.1.0.1"
		a.fails()
		f.ts.Exec(`UPDATE participations SET ip = '{10.0.0.0/24}' WHERE id = $1`, f.ts.Participation("alice"))
		f.auth("").fails()
	})
	t.Run("ip_autologin_with_ambiguous_addresses", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, nil)
		cookie := f.loginOK("alice", "demo", "10.0.0.1", "")
		f.ts.Exec(`UPDATE contests SET ip_autologin = true, allow_password_authentication = false`)
		f.ts.Exec(`UPDATE participations SET ip = '{10.0.0.1/32}' WHERE id IN ($1, $2)`,
			f.ts.Participation("alice"), f.ts.Participation("bob"))
		f.auth(cookie).fails()
		// No fallback to the cookie either.
		f.ts.Exec(`UPDATE contests SET allow_password_authentication = true`)
		f.auth(cookie).fails()
		// Without IP autologin the addresses do not matter.
		f.ts.Exec(`UPDATE contests SET ip_autologin = false`)
		f.auth(cookie).refreshed()
		// Ambiguity among hidden participations does not count when they
		// are blocked.
		f.ts.Exec(`UPDATE contests SET ip_autologin = true, block_hidden_participations = true`)
		f.ts.Exec(`UPDATE participations SET hidden = true WHERE id = $1`, f.ts.Participation("bob"))
		f.auth("").cleared()
		f.ts.Exec(`UPDATE contests SET block_hidden_participations = false`)
		f.auth("").fails()
	})
	t.Run("invalid_password_in_database", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, nil)
		cookie := f.loginOK("alice", "demo", "10.0.0.1", "")
		f.ts.Exec(`UPDATE users SET password = 'not a valid password' WHERE username = 'alice'`)
		f.auth(cookie).fails()
		f.ts.Exec(`UPDATE users SET password = 'plaintext:demo' WHERE username = 'alice'`)
		f.ts.Exec(`UPDATE participations SET password = 'not a valid password' WHERE id = $1`, f.ts.Participation("alice"))
		f.auth(cookie).fails()
	})
	t.Run("invalid_cookie", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, nil)
		cookie := f.loginOK("alice", "demo", "10.0.0.1", "")
		f.auth("").fails()
		f.auth("not a valid cookie").fails()
		f.auth(cookie[:len(cookie)-2] + "xx").fails()
		// Login data of another contest (signed with its cookie name).
		other := f.ts.S.signLogin(&Contest{}, loginData{Username: "alice", Password: f.ts.S.fingerprint("plaintext:demo")}, demoNow)
		f.auth(other).fails()
		// Login data from the future (clock skew between replicas, or a
		// forged one) is not accepted either.
		a := f.auth(cookie)
		a.at = demoNow.Add(-time.Hour)
		a.fails()
	})
	t.Run("authorization_header", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, nil)
		data := f.loginOK("alice", "demo", "10.0.0.1", "")
		a := f.auth("")
		a.header = data
		a.refreshed()
		// The header wins over the cookie.
		a.cookie = "not a valid cookie"
		a.refreshed()
		a.header, a.cookie = "not valid", data
		a.fails()
	})
	t.Run("no_participation_for_user_in_contest", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, nil)
		cookie := f.loginOK("alice", "demo", "10.0.0.1", "")
		f.ts.Exec(`DELETE FROM participations WHERE id = $1`, f.ts.Participation("alice"))
		f.auth(cookie).fails()
	})
	t.Run("hidden_user", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, nil)
		cookie := f.loginOK("alice", "demo", "10.0.0.1", "")
		f.ts.Exec(`UPDATE contests SET ip_autologin = true, block_hidden_participations = true`)
		f.ts.Exec(`UPDATE participations SET hidden = true, ip = '{10.0.0.1/32}' WHERE id = $1`, f.ts.Participation("alice"))
		f.auth(cookie).fails()
	})
	t.Run("ip_lock", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, nil)
		cookie := f.loginOK("alice", "demo", "10.0.0.1", "")
		f.ts.Exec(`UPDATE contests SET ip_autologin = true, ip_restriction = true`)
		f.ts.Exec(`UPDATE participations SET ip = '{10.0.0.0/24,127.0.0.1/32}' WHERE id = $1`, f.ts.Participation("alice"))
		a := f.auth(cookie)
		a.ip = "127.0.0.1"
		a.cleared()
		a.ip = "10.0.0.1"
		a.refreshed()
		a.ip = "10.1.0.1"
		a.fails()
		f.ts.Exec(`UPDATE contests SET ip_restriction = false`)
		a.refreshed()
		f.ts.Exec(`UPDATE contests SET ip_restriction = true`)
		f.ts.Exec(`UPDATE participations SET ip = '{}' WHERE id = $1`, f.ts.Participation("alice"))
		a.fails()
		f.ts.Exec(`UPDATE participations SET ip = NULL WHERE id = $1`, f.ts.Participation("alice"))
		a.refreshed()
	})
	t.Run("impersonate", func(t *testing.T) {
		t.Parallel()
		f := newAuthFixture(t, nil)
		data := f.loginOK("alice", "", "10.0.0.1", "admin-token")
		f.ts.Exec(`UPDATE contests SET allow_password_authentication = false`)
		f.auth(data).impersonated()
		// test_impersonation_overrides_unallowed_hidden_participation
		f.ts.Exec(`UPDATE contests SET block_hidden_participations = true`)
		f.ts.Exec(`UPDATE participations SET hidden = true WHERE id = $1`, f.ts.Participation("alice"))
		f.auth(data).impersonated()
		// test_impersonation_overrides_ip_lock
		f.ts.Exec(`UPDATE contests SET ip_restriction = true, block_hidden_participations = false`)
		f.ts.Exec(`UPDATE participations SET ip = '{10.0.0.0/24}', hidden = false WHERE id = $1`, f.ts.Participation("alice"))
		a := f.auth(data)
		a.impersonated()
		a.ip = "10.0.1.1"
		a.impersonated()
		// The password of the participant does not matter.
		f.ts.Exec(`UPDATE users SET password = 'plaintext:changed' WHERE username = 'alice'`)
		a.impersonated()
		// test_impersonate_overridden_by_ip_autologin
		f.ts.Exec(`UPDATE contests SET ip_autologin = true, ip_restriction = false`)
		f.ts.Exec(`UPDATE participations SET ip = '{10.0.0.1/32}' WHERE id = $1`, f.ts.Participation("alice"))
		f.auth(data).cleared()
	})
}

// TestPasswords checks the password strings of CMS.
func TestPasswords(t *testing.T) {
	t.Parallel()
	h, err := HashPassword("pw")
	if err != nil || !strings.HasPrefix(h, "bcrypt:$2a$04$") {
		t.Fatalf("HashPassword: %q, %v", h, err)
	}
	// The site hashes at the cost of CMS (the tests lower it).
	if BcryptCost != 12 {
		t.Errorf("BcryptCost = %d, CMS uses 12", BcryptCost)
	}
	// Python's bcrypt (CMS) reads at most 72 bytes of the password.
	long, err := HashPassword(strings.Repeat("a", 80))
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := ValidatePassword(long, strings.Repeat("a", 72)); !ok {
		t.Error("the bytes after the 72nd count")
	}
	for _, c := range []struct {
		stored, pw string
		ok, err    bool
	}{
		{h, "pw", true, false},
		{h, "pw2", false, false},
		{"plaintext:pw", "pw", true, false},
		{"plaintext:pw", "PW", false, false},
		{"plaintext:", "", true, false},
		{"pw", "pw", false, true},
		{"md5:abc", "abc", false, true},
	} {
		ok, err := ValidatePassword(c.stored, c.pw)
		if ok != c.ok || (err != nil) != c.err {
			t.Errorf("ValidatePassword(%q, %.10q) = %v, %v", c.stored, c.pw, ok, err)
		}
	}
}

// TestLoginForm drives the login form as a browser: cookies, redirects,
// CSRF, logout and the rate limit.
func TestLoginForm(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{})
	c := ts.Client()

	r := c.Get("/demo")
	if r.Status != http.StatusOK || !strings.Contains(r.Body, `action="./demo/login"`) || strings.Contains(r.Body, "Failed to log in.") {
		t.Fatalf("login form: %d\n%s", r.Status, r.Body)
	}
	// Failed login: back to the form with the error and the next page.
	r = c.Post("/demo/login", url.Values{"username": {"alice"}, "password": {"nope"}, "next": {"/demo/documentation"}})
	if r.Status != http.StatusFound || r.Location() != "../demo?login_error=true&next=%2Fdemo%2Fdocumentation" {
		t.Fatalf("failed login: %d %q", r.Status, r.Location())
	}
	r = c.Get("/demo?login_error=true&next=%2Fdemo%2Fdocumentation")
	if !strings.Contains(r.Body, "Failed to log in.") || !strings.Contains(r.Body, `<input type="hidden" name="next" value="/demo/documentation">`) {
		t.Fatalf("login error page:\n%s", r.Body)
	}
	if c.Cookie("demo_login") != "" {
		t.Fatal("a failed login set the login cookie")
	}
	// Without the CSRF token: 403, nothing happens.
	r = c.PostRaw("/demo/login", url.Values{"username": {"alice"}, "password": {"demo"}})
	if r.Status != http.StatusForbidden || c.Cookie("demo_login") != "" {
		t.Fatalf("login without _xsrf: %d", r.Status)
	}

	// Successful login: the next page, normalized.
	r = c.Post("/demo/login", url.Values{"username": {"alice"}, "password": {"demo"}, "next": {"/demo/documentation"}})
	if r.Status != http.StatusFound || r.Location() != "../demo/documentation" {
		t.Fatalf("login: %d %q", r.Status, r.Location())
	}
	setCookie := r.Header.Get("Set-Cookie")
	for _, want := range []string{"demo_login=", "Path=/", "Max-Age=10800", "HttpOnly", "SameSite=Lax"} {
		if !strings.Contains(setCookie, want) {
			t.Errorf("login cookie %q lacks %q", setCookie, want)
		}
	}
	// No open redirects.
	r = c.Post("/demo/login", url.Values{"username": {"alice"}, "password": {"demo"}, "next": {"https://evil.example/x"}})
	if r.Status != http.StatusFound || r.Location() != "../demo" {
		t.Fatalf("login with a foreign next: %d %q", r.Status, r.Location())
	}

	r = c.Get("/demo")
	if !strings.Contains(r.Body, "Logged in as <strong>Alice Liddell</strong> <em>(alice)</em>") {
		t.Fatalf("overview after login:\n%s", r.Body)
	}
	// Every page refreshes the cookie, the asynchronous requests do not
	// (CMS refresh_cookie = False).
	if !strings.Contains(r.Header.Get("Set-Cookie"), "demo_login=") {
		t.Error("a page did not refresh the login cookie")
	}
	r = c.Get("/demo/notifications")
	if r.Status != http.StatusOK || strings.Contains(r.Header.Get("Set-Cookie"), "demo_login=") {
		t.Errorf("notifications: %d, Set-Cookie %q", r.Status, r.Header.Get("Set-Cookie"))
	}

	// Logout.
	r = c.Post("/demo/logout", nil)
	if r.Status != http.StatusFound || r.Location() != "../demo" || c.Cookie("demo_login") != "" {
		t.Fatalf("logout: %d %q, cookie %q", r.Status, r.Location(), c.Cookie("demo_login"))
	}
	if r = c.Get("/demo"); !strings.Contains(r.Body, "Please log in") {
		t.Fatal("still logged in after the logout")
	}

	// The cookie expires after cookie_duration without requests.
	c.Login("alice", "demo")
	ts.Clock.Add(3*time.Hour + time.Second)
	if r = c.Get("/demo"); !strings.Contains(r.Body, "Please log in") {
		t.Fatal("the login cookie did not expire")
	}
	c.Login("alice", "demo")
	ts.Clock.Add(2 * time.Hour)
	c.Get("/demo") // refreshes it
	ts.Clock.Add(2 * time.Hour)
	if r = c.Get("/demo"); !strings.Contains(r.Body, "Logged in as") {
		t.Fatal("the refreshed login cookie expired")
	}
}

// TestLoginRateLimit: after 10 failed attempts a user name gets 429.
func TestLoginRateLimit(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{})
	c := ts.Client()
	for i := 0; i < 10; i++ {
		if r := c.Post("/demo/login", url.Values{"username": {"bob"}, "password": {"x"}}); r.Status != http.StatusFound {
			t.Fatalf("attempt %d: %d", i, r.Status)
		}
	}
	r := c.Post("/demo/login", url.Values{"username": {"bob"}, "password": {"demo"}})
	if r.Status != http.StatusTooManyRequests || r.Header.Get("Retry-After") == "" {
		t.Fatalf("11th attempt: %d, Retry-After %q", r.Status, r.Header.Get("Retry-After"))
	}
	// Other users are not affected.
	c.Login("alice", "demo")
}

// TestIPRules checks the address rules through the proxy headers.
func TestIPRules(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{Configure: func(c *config.Config) { c.ContestWeb.NumProxies = 1 }})
	ts.Exec(`UPDATE contests SET ip_restriction = true`)
	ts.Exec(`UPDATE participations SET ip = '{10.0.0.0/24}' WHERE id = $1`, ts.Participation("alice"))

	inside := ts.Client()
	inside.Header.Set("X-Forwarded-For", "10.0.0.7")
	inside.Login("alice", "demo")
	if r := inside.Get("/demo"); !strings.Contains(r.Body, "Logged in as") {
		t.Fatal("not logged in from an allowed address")
	}
	// The same cookie from another address does not work.
	inside.Header.Set("X-Forwarded-For", "10.0.1.7")
	if r := inside.Get("/demo"); !strings.Contains(r.Body, "Please log in") {
		t.Fatal("logged in from a forbidden address")
	}
	outside := ts.Client()
	outside.Header.Set("X-Forwarded-For", "10.0.1.7")
	if r := outside.Post("/demo/login", url.Values{"username": {"alice"}, "password": {"demo"}}); !strings.Contains(r.Location(), "login_error=true") {
		t.Fatalf("login from a forbidden address: %d %q", r.Status, r.Location())
	}

	// IP autologin: no form, no cookie.
	ts.Exec(`UPDATE contests SET ip_autologin = true, ip_restriction = false`)
	ts.Exec(`UPDATE participations SET ip = '{10.0.0.9/32}' WHERE id = $1`, ts.Participation("bob"))
	auto := ts.Client()
	auto.Header.Set("X-Forwarded-For", "10.0.0.9")
	if r := auto.Get("/demo"); !strings.Contains(r.Body, "Logged in as <strong>Bob Builder</strong>") {
		t.Fatalf("IP autologin:\n%s", r.Body)
	}
	if auto.Cookie("demo_login") != "" {
		t.Error("IP autologin set the login cookie")
	}
}
