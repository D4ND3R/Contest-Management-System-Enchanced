// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package adminweb

import (
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/web"
)

// formCtx is a Ctx with the arguments of form, in loc.
func formCtx(t *testing.T, form url.Values, loc *time.Location) *Ctx {
	t.Helper()
	r := httptest.NewRequest("POST", "/x", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := r.ParseForm(); err != nil {
		t.Fatal(err)
	}
	return &Ctx{R: r, RC: &web.Render{TZ: loc}}
}

// TestForm checks the argument readers of CMS: absent keeps, empty
// stores the empty value, invalid values are errors with CMS's messages.
func TestForm(t *testing.T) {
	rome, _ := time.LoadLocation("Europe/Rome")
	c := formCtx(t, url.Values{
		"s": {"  text "}, "empty": {""}, "list": {" a, ,b ,"}, "n": {"42"}, "bad_n": {"4x"},
		"secs": {"1.5"}, "mins": {"2"}, "when": {"2026-07-01 09:00:00"}, "when_us": {"2026-07-01 09:00:00.25"},
		"bad_when": {"July"}, "ips": {"10.0.0.0/8, 192.0.2.1, ::1"}, "bad_ips": {"10.0.0.1/8"}, "check": {"on"},
		"f": {"0.25"},
	}, rome)
	f := c.Form()
	s, keep := "old", "kept"
	f.String(&s, "s")
	f.String(&keep, "absent")
	var opt *string
	x := "x"
	opt = &x
	f.OptString(&opt, "empty")
	var list []string
	f.StringList(&list, "list")
	var n int32
	f.Int32(&n, "n")
	var optN *int32
	f.OptInt32(&optN, "empty")
	var d time.Duration
	f.Duration(&d, "secs")
	var dm time.Duration
	f.DurationMin(&dm, "mins")
	var od *time.Duration
	f.OptDuration(&od, "empty")
	var when, whenUS time.Time
	f.Datetime(&when, "when")
	f.Datetime(&whenUS, "when_us")
	var ips []netip.Prefix
	f.IPNetworks(&ips, "ips")
	var check, unchecked bool
	unchecked = true
	f.Bool(&check, "check")
	f.Bool(&unchecked, "absent")
	var fl float64
	f.Float(&fl, "f")
	if err := f.Err(); err != nil {
		t.Fatal(err)
	}
	if s != "text" || keep != "kept" || opt != nil || strings.Join(list, "|") != "a|b" || n != 42 || optN != nil ||
		d != 1500*time.Millisecond || dm != 2*time.Minute || od != nil || !check || unchecked || fl != 0.25 {
		t.Errorf("values: %q %q %v %v %d %v %v %v %v %v %v %v", s, keep, opt, list, n, optN, d, dm, od, check, unchecked, fl)
	}
	if !when.Equal(time.Date(2026, 7, 1, 7, 0, 0, 0, time.UTC)) || whenUS.Sub(when) != 250*time.Millisecond {
		t.Errorf("times in the server zone: %v %v", when, whenUS)
	}
	if len(ips) != 3 || ips[1].String() != "192.0.2.1/32" || ips[2].String() != "::1/128" {
		t.Errorf("networks: %v", ips)
	}
	if got := FormDatetime(whenUS, rome); got != "2026-07-01 09:00:00.250000" {
		t.Errorf("FormDatetime: %q", got)
	}
	if got := FormDatetime(when, rome); got != "2026-07-01 09:00:00" {
		t.Errorf("FormDatetime: %q", got)
	}
	if FormSeconds(90*time.Second) != "90.0" || FormSeconds(1500*time.Millisecond) != "1.5" {
		t.Errorf("FormSeconds: %s %s", FormSeconds(90*time.Second), FormSeconds(1500*time.Millisecond))
	}

	f = c.Form()
	f.Int32(&n, "bad_n")
	f.Datetime(&when, "bad_when")
	f.IPNetworks(&ips, "bad_ips")
	f.Int32(&n, "empty")
	f.Required("empty", "username")
	err := f.Err()
	for _, want := range []string{"Can't cast 4x to int.", "Can't cast July to datetime.",
		"Can't cast 10.0.0.1/8 to an IP network.", "Can't cast  to int.", "No username specified."} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("errors lack %q: %v", want, err)
		}
	}
}

// TestFormPassword checks CMS's get_password: a new password is hashed
// with the method; an empty one keeps a hashed password, unsets it when
// allowed, or stores the empty password.
func TestFormPassword(t *testing.T) {
	web.PasswordCost = bcrypt.MinCost
	cases := []struct {
		password, method, old string
		allowUnset            bool
		want                  string // prefix; "keep" or "nil"
	}{
		{"pw", "bcrypt", "", false, "bcrypt:$2"},
		{"pw", "plaintext", "bcrypt:x", false, "plaintext:pw"},
		{"", "bcrypt", "bcrypt:$2a$old", false, "keep"},
		{"", "plaintext", "bcrypt:$2a$old", true, "nil"},
		{"", "plaintext", "plaintext:old", false, "plaintext:"},
		{"", "bcrypt", "", true, "nil"},
	}
	for _, tc := range cases {
		c := formCtx(t, url.Values{"password": {tc.password}, "method": {tc.method}}, time.UTC)
		old := "unchanged"
		dst := &old
		f := c.Form()
		f.Password(&dst, tc.old, tc.allowUnset)
		if err := f.Err(); err != nil {
			t.Fatal(err)
		}
		switch tc.want {
		case "keep":
			if dst == nil || *dst != "unchanged" {
				t.Errorf("%+v: %v", tc, dst)
			}
		case "nil":
			if dst != nil {
				t.Errorf("%+v: %v", tc, *dst)
			}
		default:
			if dst == nil || !strings.HasPrefix(*dst, tc.want) || (tc.want == "plaintext:" && *dst != "plaintext:") {
				t.Errorf("%+v: %v", tc, dst)
			}
		}
	}
}

// TestPager checks the pages of CMS's selector (macro/pages.html).
func TestPager(t *testing.T) {
	r := httptest.NewRequest("GET", "/contest/1/submissions?page=3&task=2", nil)
	c := &Ctx{R: r, RC: &web.Render{}}
	p := c.Pager(1000, 50)
	if p.Pages != 20 || p.Page != 3 || p.Offset() != 150 || p.Many() || len(p.Range()) != 20 || !p.Range()[3].Current {
		t.Fatalf("pager: %+v", p)
	}
	if u := p.URL(0); u != "submissions?page=0&task=2" {
		t.Errorf("URL: %q", u)
	}
	p = c.Pager(5000, 50)
	rg := p.Range()
	if !p.Many() || p.Last() != 99 || rg[0].Label != 2 || len(rg) != 99 {
		t.Errorf("many pages: first %d, %d entries", rg[0].Label, len(rg))
	}
	c.R = httptest.NewRequest("GET", "/x?page=999", nil)
	if p := c.Pager(10, 50); p.Page != 0 || p.Pages != 1 {
		t.Errorf("clamped: %+v", p)
	}
	c.R = httptest.NewRequest("GET", "/x?page=-4", nil)
	if p := c.Pager(0, 0); p.Page != 0 || p.Pages != 0 || p.Size != DefaultPageSize {
		t.Errorf("empty: %+v", p)
	}
}
