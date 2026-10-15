// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package web

import (
	"net/http/httptest"
	"testing"
)

// From cmstestsuite/unit_tests/server/util_test.py of CMS.
func TestURLLikeCMS(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{URL("/").Join(), "/"},
		{URL("/").Join("foo"), "/foo"},
		{URL("/").Join("foo", "bar"), "/foo/bar"},
		{URL("/").Join("foo", 42), "/foo/42"},
		{URL("/prefix").Join(), "/prefix"},
		{URL("/prefix").Join("foo", 42), "/prefix/foo/42"},
		{URL("..").Join(), ".."},
		{URL("..").Join("foo", "bar"), "../foo/bar"},
		{URL("/").Sub("foo").Join(), "/foo"},
		{URL("/").Sub(42).Join("bar"), "/42/bar"},
		{URL("/").Sub("foo").Sub(42).Join("bar"), "/foo/42/bar"},
		{URL("/prefix").Sub("foo").Sub(42).Join("bar"), "/prefix/foo/42/bar"},
		{URL("..").Sub("foo").Sub(42).Join("bar"), "../foo/42/bar"},
		{URL("/foo").Sub("bar").JoinQuery([]any{"baz"}, "a", "b"), "/foo/bar/baz?a=b"},
		{URL("/foo").Sub("bar").JoinQuery([]any{"baz"}, "a", "b", "b", "a"), "/foo/bar/baz?a=b&b=a"},
		{URL("/").Join("foo/bar"), "/foo%2Fbar"},
		{URL("/").Join("foo?bar"), "/foo%3Fbar"},
		{URL("/").Join("foo&bar"), "/foo%26bar"},
		{URL("/").Join("foo#bar"), "/foo%23bar"},
		{URL("/").JoinQuery(nil, "?foo", "/bar#", "f=o?o", "?b=a&r"), "/?%3Ffoo=%2Fbar%23&f%3Do%3Fo=%3Fb%3Da%26r"},
		{URL(".").Join("a b", "ü~"), "./a%20b/%C3%BC~"},
		{QueryString("q", "a b+c"), "q=a+b%2Bc"},
		{URL("..").String(), ".."},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
	for p, want := range map[string]URL{"/": ".", "/x": ".", "/x/": "..", "/x/y": "..", "/x/y/": "../..", "/a/b/c/d": "../../.."} {
		if got := URLRoot(p); got != want {
			t.Errorf("URLRoot(%q) = %q, want %q", p, got, want)
		}
	}
}

// TestNormalizeNextPage compares with CMS's normalize_login_next_page
// (Python 3.12 urlsplit) for Url("..") and the default "DEFAULT": only
// local paths are accepted and they come back relative to the site.
func TestNormalizeNextPage(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ".."},
		{"/", ".."},
		{"/demo/tasks", "../demo/tasks"},
		{"/demo/tasks?x=1&y=%2F", "../demo/tasks?x=1&y=%2F"},
		{"?x=1", "..?x=1"},
		{"//evil.com/x", "DEFAULT"},
		{"http://evil.com", "DEFAULT"},
		{"https:evil", "DEFAULT"},
		{"javascript:alert(1)", "DEFAULT"},
		{"demo/tasks", "DEFAULT"},
		{"/\\evil.com", "../%5Cevil.com"},
		{"\\\\evil.com", "DEFAULT"},
		{" /x", "../x"},
		{"\t//evil", "DEFAULT"},
		{"/a/../b", "../a/../b"},
		{"/a//b", "../a/b"},
		{"/%2F%2Fevil", "../%252F%252Fevil"},
		{"/x#frag", "../x"},
		{"/x?y#z", "../x?y"},
		{"///evil.com", "../evil.com"},
		{"/demo/a b", "../demo/a%20b"},
		{"//[::1", "DEFAULT"}, // CMS raises ValueError (500)
		{"/ü", "../%C3%BC"},
		{"\x00/x", "../x"},
		{"/x\r\n/y", "../x/y"},
		{"HTTP://x/y", "DEFAULT"},
		{"mailto:a@b", "DEFAULT"},
		{"/demo/", "../demo"},
		{"//", ".."},
		{"/?", ".."},
		{"#x", ".."},
		{"/a;b", "../a%3Bb"},
		{"x:/y", "DEFAULT"},
		{"1:/y", "DEFAULT"},
		{"/a?b?c", "../a?b?c"},
	}
	for _, c := range cases {
		if got := NormalizeNextPage(c.in, true, "..", "DEFAULT"); got != c.want {
			t.Errorf("NormalizeNextPage(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := NormalizeNextPage("", false, "..", "DEFAULT"); got != "DEFAULT" {
		t.Errorf("absent: %q", got)
	}
}

func TestRedirect(t *testing.T) {
	rec := httptest.NewRecorder()
	Redirect(rec, "../demo?login_error=true&next=%2Fdemo%2Ftasks\r\nSet-Cookie: x=1", 302)
	if rec.Code != 302 || rec.Header().Get("Location") != "../demo?login_error=true&next=%2Fdemo%2FtasksSet-Cookie: x=1" {
		t.Errorf("%d %q", rec.Code, rec.Header().Get("Location"))
	}
}
