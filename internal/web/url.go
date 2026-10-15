// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/server/util.py of CMS (get_url_root, Url,
// normalize_login_next_page):
// Copyright © 2010-2014 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2012-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2014 Artem Iglikov <artem.iglikov@gmail.com>

package web

import (
	"fmt"
	"net/http"
	"strings"
)

// URL builds relative URLs piece by piece, like CMS's Url. Every page
// links to the others relative to the root of the site computed from the
// request path (URLRoot), so the sites work unchanged behind a reverse
// proxy that mounts them under a sub-path.
type URL string

// URLRoot returns the relative URL of the root of the site from
// requestPath (CMS get_url_root): "." for "/" or "/x", ".." for "/x/",
// "../.." for "/x/y/z"...
func URLRoot(requestPath string) URL {
	depth := strings.Count(requestPath, "/") - 1
	if depth <= 0 {
		return "."
	}
	return URL(strings.Repeat("../", depth-1) + "..")
}

// Join returns the URL of the given path components below u. Components
// are formatted with fmt.Sprint and percent-encoded completely (a "/"
// inside a component becomes %2F), as Python's quote(c, safe="").
func (u URL) Join(components ...any) string {
	s := string(u)
	for _, c := range components {
		if !strings.HasSuffix(s, "/") {
			s += "/"
		}
		s += Quote(fmt.Sprint(c))
	}
	return s
}

// JoinQuery is Join plus a query string built from key/value pairs in
// their order (CMS Url(..., **kwargs)).
func (u URL) JoinQuery(components []any, pairs ...string) string {
	s := u.Join(components...)
	if len(pairs) > 0 {
		s += "?" + QueryString(pairs...)
	}
	return s
}

// Sub returns the URL builder of a sub-path (CMS url[component]).
func (u URL) Sub(components ...any) URL { return URL(u.Join(components...)) }

// String returns the URL itself (CMS url()).
func (u URL) String() string { return string(u) }

// Quote percent-encodes every byte of s except the unreserved characters
// A-Z a-z 0-9 _ . - ~ (Python's urllib.parse.quote(s, safe="")).
func Quote(s string) string {
	return quote(s, false)
}

// QueryString encodes key/value pairs as Python's urlencode does
// (quote_plus: spaces become "+"), keeping their order. An odd trailing
// key gets an empty value.
func QueryString(pairs ...string) string {
	var b strings.Builder
	for i := 0; i < len(pairs); i += 2 {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(quote(pairs[i], true))
		b.WriteByte('=')
		if i+1 < len(pairs) {
			b.WriteString(quote(pairs[i+1], true))
		}
	}
	return b.String()
}

func quote(s string, plus bool) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9',
			c == '_', c == '.', c == '-', c == '~':
			b.WriteByte(c)
		case c == ' ' && plus:
			b.WriteByte('+')
		default:
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String()
}

// NormalizeNextPage validates the "next" parameter of a login form (CMS
// normalize_login_next_page): only a local absolute path, with an
// optional query, is accepted, and it is rebuilt through u so the result
// is always relative to the site (no open redirects to other hosts or
// schemes). present is false when the parameter was not sent; then, and
// for anything else, def is returned. "" and a query alone mean "/".
func NormalizeNextPage(next string, present bool, u URL, def string) string {
	if !present {
		return def
	}
	scheme, netloc, p, query, ok := urlSplit(next)
	if !ok {
		return def
	}
	if p == "" {
		p = "/"
	}
	if scheme != "" || netloc != "" || !strings.HasPrefix(p, "/") {
		return def
	}
	var out string
	if p != "/" {
		parts := strings.Split(strings.Trim(p, "/"), "/")
		comps := make([]any, len(parts))
		for i, s := range parts {
			comps[i] = s
		}
		out = u.Join(comps...)
	} else {
		out = u.String()
	}
	if query != "" {
		out += "?" + query
	}
	return out
}

// urlSplit is Python's urllib.parse.urlsplit (3.11+, with fragments),
// returning ok=false where Python raises ValueError.
func urlSplit(s string) (scheme, netloc, path, query string, ok bool) {
	// Leading C0 controls and spaces are stripped; tabs and newlines are
	// removed anywhere (WHATWG).
	s = strings.TrimLeft(s, "\x00\x01\x02\x03\x04\x05\x06\x07\x08\t\n\x0b\x0c\r\x0e\x0f"+
		"\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f ")
	s = strings.NewReplacer("\t", "", "\r", "", "\n", "").Replace(s)
	if i := strings.IndexByte(s, ':'); i > 0 && isASCIILetter(s[0]) {
		isScheme := true
		for j := 0; j < i; j++ {
			c := s[j]
			if !isASCIILetter(c) && !('0' <= c && c <= '9') && c != '+' && c != '-' && c != '.' {
				isScheme = false
				break
			}
		}
		if isScheme {
			scheme, s = strings.ToLower(s[:i]), s[i+1:]
		}
	}
	if strings.HasPrefix(s, "//") {
		end := len(s)
		for _, d := range "/?#" {
			if k := strings.IndexRune(s[2:], d); k >= 0 && k+2 < end {
				end = k + 2
			}
		}
		netloc, s = s[2:end], s[end:]
		if strings.Contains(netloc, "[") != strings.Contains(netloc, "]") {
			return "", "", "", "", false
		}
	}
	if i := strings.IndexByte(s, '#'); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, '?'); i >= 0 {
		s, query = s[:i], s[i+1:]
	}
	return scheme, netloc, s, query, true
}

func isASCIILetter(c byte) bool { return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' }

// Redirect sends a redirection to target, which is written to Location
// as it is: relative targets stay relative (unlike http.Redirect, which
// makes them absolute and breaks sites mounted under a sub-path behind a
// proxy). code is usually http.StatusFound, as Tornado's redirect.
func Redirect(w http.ResponseWriter, target string, code int) {
	// A Location header cannot carry line breaks (header injection).
	target = strings.NewReplacer("\r", "", "\n", "").Replace(target)
	w.Header().Set("Location", target)
	w.WriteHeader(code)
}
