// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Package webtest has test helpers for the sites built on internal/web:
// rendering every page of a site and passing its CSRF protection.
package webtest

import (
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/web"
)

// XSRFToken is a valid CSRF token in Tornado's version 2 format.
const XSRFToken = "2|00000000|000102030405060708090a0b0c0d0e0f|0"

// AddXSRF makes an unsafe request pass web.CSRF: it adds the _xsrf
// cookie and the X-XSRFToken header with XSRFToken. Forms can send
// XSRFToken in the _xsrf field instead of the header.
func AddXSRF(r *http.Request) {
	r.AddCookie(&http.Cookie{Name: web.XSRFCookie, Value: XSRFToken})
	r.Header.Set("X-XSRFToken", XSRFToken)
}

// Options configures RenderPages.
type Options struct {
	// Render is the request state; nil renders in English, UTC, at
	// 2026-01-01 00:00:00 UTC, with "." as root.
	Render func(page string) *web.Render
	// Data returns the data of a page; nil renders every page with nil
	// data (the zero-data smoke test).
	Data func(page string) any
	// Strict fails on any template error. Otherwise (the default, meant
	// for zero data) only panics and runtime errors fail: a page may stop
	// at "nil pointer evaluating .Contest.Name" without data, but no
	// function may crash.
	Strict bool
	// Pages restricts the pages rendered; nil renders all of them.
	Pages []string
}

// RenderPages renders the pages of e as described by o and returns the
// output of each one (what was written before an allowed error).
func RenderPages(tb testing.TB, e *web.Engine, o Options) map[string]string {
	tb.Helper()
	pages := o.Pages
	if pages == nil {
		pages = e.Pages()
	}
	out := map[string]string{}
	for _, p := range pages {
		var rc *web.Render
		if o.Render != nil {
			rc = o.Render(p)
		}
		if rc == nil {
			rc = &web.Render{TZ: time.UTC, Now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Root: "."}
		}
		var data any
		if o.Data != nil {
			data = o.Data(p)
		}
		var b strings.Builder
		err := execute(e, &b, p, rc, data)
		out[p] = b.String()
		if err == nil {
			continue
		}
		if o.Strict || isCrash(err) {
			tb.Errorf("%s: %v", p, err)
		}
	}
	return out
}

type panicError struct{ v any }

func (p panicError) Error() string { return fmt.Sprintf("panic: %v", p.v) }

func execute(e *web.Engine, b *strings.Builder, page string, rc *web.Render, data any) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = panicError{r}
		}
	}()
	return e.Execute(b, page, rc, data)
}

// isCrash reports panics, runtime errors inside functions or methods
// (text/template turns their panics into errors) and escaping errors.
func isCrash(err error) bool {
	var pe panicError
	var re runtime.Error
	var te *template.Error
	if errors.As(err, &pe) || errors.As(err, &re) || errors.As(err, &te) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "runtime error") || strings.Contains(msg, "panic")
}
