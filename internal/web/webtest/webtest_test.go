// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package webtest

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/web"
)

type page struct {
	Contest *struct{ Name string }
	Items   []int
}

type recorder struct {
	testing.TB
	errs []string
}

func (r *recorder) Errorf(format string, args ...any) { r.errs = append(r.errs, format) }
func (r *recorder) Helper()                           {}

func TestRenderPages(t *testing.T) {
	fsys := fstest.MapFS{
		"ok.html":     {Data: []byte(`<p>{{len .Items}}</p>`)},
		"nilptr.html": {Data: []byte(`<p>{{.Contest.Name}}</p>`)},
		"crash.html":  {Data: []byte(`<p>{{boom}}</p>`)},
	}
	e, err := web.NewEngine(web.EngineOptions{Templates: fsys, Funcs: func(func() *web.Render) template.FuncMap {
		return template.FuncMap{"boom": func() int { var m map[string]*int; return *m["x"] }}
	}})
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{TB: t}
	out := RenderPages(rec, e, Options{Data: func(string) any { return page{} }})
	if len(rec.errs) != 1 {
		t.Errorf("zero data: want only the crash reported, got %d errors", len(rec.errs))
	}
	if out["ok.html"] != "<p>0</p>" {
		t.Errorf("ok.html: %q", out["ok.html"])
	}
	rec = &recorder{TB: t}
	RenderPages(rec, e, Options{Strict: true, Pages: []string{"nilptr.html"}, Data: func(string) any { return page{} }})
	if len(rec.errs) != 1 {
		t.Errorf("strict: %d errors", len(rec.errs))
	}
}

func TestAddXSRF(t *testing.T) {
	c := web.NewCSRF(web.CSRFOptions{})
	r := httptest.NewRequest("POST", "/x", strings.NewReader(""))
	AddXSRF(r)
	if err := c.Check(r); err != nil {
		t.Error(err)
	}
	r = httptest.NewRequest("POST", "/x", strings.NewReader("_xsrf="+XSRFToken))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: web.XSRFCookie, Value: XSRFToken})
	if err := c.Check(r); err != nil {
		t.Error(err)
	}
}
