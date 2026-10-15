// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package web

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/httpx"
)

func testSigner() *Signer {
	return NewSigner(config.DeriveKey(bytes.Repeat([]byte{7}, 16), "contest-web/cookie"))
}

func TestSignerTamperAndExpiry(t *testing.T) {
	s := testSigner()
	now := time.Unix(1_800_000_000, 0)
	v := s.Sign("demo_login", []byte(`["alice","",1.5,false]`), now)
	got, err := s.Verify("demo_login", v, time.Hour, now.Add(time.Minute))
	if err != nil || string(got) != `["alice","",1.5,false]` {
		t.Fatalf("round trip: %q %v", got, err)
	}
	if got, err := s.Verify("demo_login", s.Sign("demo_login", nil, now), 0, now); err != nil || len(got) != 0 {
		t.Errorf("empty value: %q %v", got, err)
	}
	parts := strings.Split(v, ".")
	flip := func(s string) string {
		b := []byte(s)
		if b[0] == 'A' {
			b[0] = 'B'
		} else {
			b[0] = 'A'
		}
		return string(b)
	}
	for name, bad := range map[string]string{
		"value":     strings.Join([]string{parts[0], parts[1], flip(parts[2]), parts[3]}, "."),
		"signature": strings.Join([]string{parts[0], parts[1], parts[2], flip(parts[3])}, "."),
		"time":      strings.Join([]string{parts[0], "1800000001", parts[2], parts[3]}, "."),
		"version":   strings.Join([]string{"2", parts[1], parts[2], parts[3]}, "."),
		"truncated": strings.Join(parts[:3], "."),
		"empty":     "",
		"garbage":   "1.x.y.z",
	} {
		if _, err := s.Verify("demo_login", bad, time.Hour, now); !errors.Is(err, ErrBadSignature) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Another name (another contest's cookie) or another key.
	if _, err := s.Verify("other_login", v, time.Hour, now); !errors.Is(err, ErrBadSignature) {
		t.Errorf("other name: %v", err)
	}
	other := NewSigner(config.DeriveKey(bytes.Repeat([]byte{8}, 16), "contest-web/cookie"))
	if _, err := other.Verify("demo_login", v, time.Hour, now); !errors.Is(err, ErrBadSignature) {
		t.Errorf("other key: %v", err)
	}
	// Expiry.
	if _, err := s.Verify("demo_login", v, time.Hour, now.Add(time.Hour+time.Second)); !errors.Is(err, ErrExpired) {
		t.Errorf("expired: %v", err)
	}
	if _, err := s.Verify("demo_login", v, 0, now.Add(1000*time.Hour)); err != nil {
		t.Errorf("no max age: %v", err)
	}
	if _, err := s.Verify("demo_login", v, time.Hour, now.Add(-10*time.Minute)); !errors.Is(err, ErrExpired) {
		t.Errorf("from the future: %v", err)
	}
}

func TestSignedCookie(t *testing.T) {
	s := testSigner()
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/demo", nil)
	s.SetCookie(rec, r, "demo_login", []byte("payload"), 3*time.Hour)
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal(cookies)
	}
	c := cookies[0]
	if c.Name != "demo_login" || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" ||
		c.MaxAge != 10800 || c.Secure {
		t.Errorf("attributes: %+v", c)
	}
	r2 := httptest.NewRequest("GET", "/demo", nil)
	r2.AddCookie(&http.Cookie{Name: "demo_login", Value: "junk"})
	r2.AddCookie(c)
	if got, err := s.Cookie(r2, "demo_login", time.Hour); err != nil || string(got) != "payload" {
		t.Errorf("read back (last cookie wins): %q %v", got, err)
	}
	if _, err := s.Cookie(httptest.NewRequest("GET", "/", nil), "demo_login", time.Hour); !errors.Is(err, http.ErrNoCookie) {
		t.Errorf("absent: %v", err)
	}
	r3 := httptest.NewRequest("GET", "/demo", nil)
	r3.AddCookie(&http.Cookie{Name: "demo_login", Value: c.Value + "x"})
	if _, err := s.Cookie(r3, "demo_login", time.Hour); !errors.Is(err, ErrBadSignature) {
		t.Errorf("tampered: %v", err)
	}
	// Session cookie.
	rec = httptest.NewRecorder()
	s.SetCookie(rec, r, "x", nil, 0)
	if c := rec.Result().Cookies()[0]; c.MaxAge != 0 || !c.Expires.IsZero() {
		t.Errorf("session cookie: %+v", c)
	}
}

func TestCookieSecureBehindProxy(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	setter := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SetCookie(w, r, &http.Cookie{Name: "a", Value: "b"})
	})
	for _, c := range []struct {
		proxies int
		proto   string
		tls     bool
		secure  bool
	}{
		{0, "", false, false},
		{0, "https", false, false}, // not trusted without proxies
		{1, "https", false, true},
		{1, "http", false, false},
		{0, "", true, true},
	} {
		h := httpx.Observe(quiet, nil, c.proxies)(setter)
		target := "http://example.org/"
		if c.tls {
			target = "https://example.org/"
		}
		r := httptest.NewRequest("GET", target, nil)
		if c.proto != "" {
			r.Header.Set("X-Forwarded-Proto", c.proto)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if got := rec.Result().Cookies()[0].Secure; got != c.secure {
			t.Errorf("proxies=%d proto=%q tls=%v: Secure=%v", c.proxies, c.proto, c.tls, got)
		}
	}
}

func TestClearCookie(t *testing.T) {
	rec := httptest.NewRecorder()
	ClearCookie(rec, httptest.NewRequest("GET", "/", nil), "demo_login", "")
	h := rec.Header().Get("Set-Cookie")
	if !strings.Contains(h, "demo_login=;") || !strings.Contains(h, "Max-Age=0") || !strings.Contains(h, "Path=/") ||
		!strings.Contains(h, "Expires=Thu, 01 Jan 1970") {
		t.Errorf("Set-Cookie: %s", h)
	}
}
