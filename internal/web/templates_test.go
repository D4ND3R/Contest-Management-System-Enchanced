// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package web

import (
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

func mapFS(files map[string]string) fstest.MapFS {
	m := fstest.MapFS{}
	for k, v := range files {
		m[k] = &fstest.MapFile{Data: []byte(v)}
	}
	return m
}

func newEngine(t *testing.T, files map[string]string, o EngineOptions) *Engine {
	t.Helper()
	o.Templates = mapFS(files)
	e, err := NewEngine(o)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func render(t *testing.T, e *Engine, name string, rc *Render, data any) string {
	t.Helper()
	var b strings.Builder
	if err := e.Execute(&b, name, rc, data); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return b.String()
}

// The same templates in Jinja (rendered by Jinja2 3.1 with CMS's
// trim_blocks and lstrip_blocks) give jinjaTrue and jinjaFalse.
const trimBase = `<!DOCTYPE html>
<html>
    <head>
        <title>{{block "title" .}}{{end}}</title>
        {{block "js" .}}{{end}}
    </head>
    <body>
        {{block "body" .}}{{end}}
    </body>
</html>
`

const trimChild = `{{/* extends "base.html" */}}
{{define "title"}}
    Title
{{end}}
{{define "body"}}
<ul>
    {{range .Items}}
    <li>{{.}}</li>
    {{end}}
</ul>
{{if .A}}
  yes
{{else}}
  no
{{end}}
<p>{{if .A}}inline{{end}}</p>
    {{/* comment */}}
<div>
    {{$y := 1}}
    {{$y}}
</div>
    {{if .A}}
        {{if .B}}
        nested
        {{end}}
    {{else if .C}}
    elif
    {{end}}
<span>{{"a"}}</span>   {{if .A}}x{{end}}
{{end}}
`

const (
	jinjaTrue  = "<!DOCTYPE html>\n<html>\n    <head>\n        <title>    Title\n</title>\n    </head>\n    <body>\n<ul>\n    <li>1</li>\n    <li>2</li>\n</ul>\n  yes\n<p>inline</p>\n<div>\n    1\n</div>\n        nested\n<span>a</span>   x    </body>\n</html>"
	jinjaFalse = "<!DOCTYPE html>\n<html>\n    <head>\n        <title>    Title\n</title>\n    </head>\n    <body>\n<ul>\n    <li>1</li>\n    <li>2</li>\n</ul>\n  no\n<p></p>\n<div>\n    1\n</div>\n    elif\n<span>a</span>       </body>\n</html>"
)

func TestTrimBlocksLikeJinja(t *testing.T) {
	e := newEngine(t, map[string]string{"base.html": trimBase, "child.html": trimChild}, EngineOptions{})
	type data struct {
		Items   []int
		A, B, C bool
	}
	// As Jinja (keep_trailing_newline off), the final newline goes.
	if got := render(t, e, "child.html", nil, data{[]int{1, 2}, true, true, true}); got != jinjaTrue {
		t.Errorf("a=true\n got %q\nwant %q", got, jinjaTrue)
	}
	if got := render(t, e, "child.html", nil, data{[]int{1, 2}, false, true, true}); got != jinjaFalse {
		t.Errorf("a=false\n got %q\nwant %q", got, jinjaFalse)
	}
}

var siteFiles = map[string]string{
	"base.html": `<html lang="{{lang}}"><title>{{block "title" .}}{{end}}</title>` +
		`<script nonce="{{nonce}}">{{block "js" .}}{{end}}</script>{{block "body" .}}{{end}}</html>`,
	"contest.html": `{{/* extends "base.html" */}}` +
		`{{define "js"}}var x = 1;{{block "additional_js" .}}var def = 1;{{end}}{{end}}` +
		`{{define "body"}}<nav>{{T "Overview"}}</nav>{{block "core" .}}{{end}}{{end}}`,
	"overview.html": `{{/* extends "contest.html" */}}` +
		`{{define "title"}}{{.Title}}{{end}}` +
		`{{define "core"}}<main>{{template "submission.rows" (dict "n" 2)}}{{template "row.html" .}}</main>{{end}}`,
	"empty_js.html": `{{/* extends "contest.html" */}}{{define "additional_js"}}{{end}}`,
	"row.html":      `<p>row {{.Title}}</p>`,
	"macro/submission.html": `{{define "submission.rows"}}<table>{{range $i := .n}}{{template "submission.row" $i}}{{end}}</table>{{end}}` +
		`{{define "submission.row"}}<tr>{{.}}</tr>{{end}}`,
	"error.html": `{{/* extends "base.html" */}}{{define "body"}}{{Format (T "Error %(status_code)s") "status_code" .StatusCode}}{{end}}`,
}

func TestInheritanceAndMacros(t *testing.T) {
	e := newEngine(t, siteFiles, EngineOptions{})
	got := render(t, e, "overview.html", &Render{Nonce: "N0nce"}, map[string]any{"Title": "<Demo>"})
	want := `<html lang="en"><title>&lt;Demo&gt;</title><script nonce="N0nce">var x = 1;var def = 1;</script>` +
		`<nav>Overview</nav><main><table><tr>0</tr><tr>1</tr></table><p>row &lt;Demo&gt;</p></main></html>`
	if got != want {
		t.Errorf("overview\n got %s\nwant %s", got, want)
	}
	// An empty block in a child empties the parent's default, as in Jinja.
	got = render(t, e, "empty_js.html", nil, nil)
	if !strings.Contains(got, `<script nonce="">var x = 1;</script>`) {
		t.Errorf("empty block: %s", got)
	}
	pages := strings.Join(e.Pages(), ",")
	if pages != "base.html,contest.html,empty_js.html,error.html,overview.html,row.html" {
		t.Errorf("pages: %s", pages)
	}
	if e.Has("macro/submission.html") {
		t.Error("library file listed as a page")
	}
	if err := e.Execute(&strings.Builder{}, "nope.html", nil, nil); !errors.Is(err, ErrNoTemplate) {
		t.Errorf("missing page: %v", err)
	}
}

func TestEngineErrors(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"missing parent":   {"a.html": `{{/* extends "nope.html" */}}`},
		"loop":             {"a.html": `{{/* extends "b.html" */}}`, "b.html": `{{/* extends "a.html" */}}`},
		"missing template": {"a.html": `{{template "nope" .}}`},
		"unknown function": {"a.html": `{{nosuchfunc}}`},
		"syntax":           {"a.html": `{{if}}`},
		"escaping":         {"a.html": `<script>{{if .X}}"{{end}}</script>`},
		"duplicate macro": {"a.html": `{{template "m" .}}`, "macro/x.html": `{{define "m"}}1{{end}}`,
			"macro/y.html": `{{define "m"}}2{{end}}`},
	} {
		_, err := NewEngine(EngineOptions{Templates: mapFS(files)})
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestRenderConcurrent(t *testing.T) {
	langs := testLanguages(t)
	e := newEngine(t, siteFiles, EngineOptions{MaxIdle: 2})
	es, en := langs.Get("es"), langs.Get("en")
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			lang, word := en, "Overview"
			if i%2 == 0 {
				lang, word = es, "Carátula"
			}
			nonce := fmt.Sprintf("n%d", i)
			var b strings.Builder
			if err := e.Execute(&b, "overview.html", &Render{Lang: lang, Nonce: nonce}, map[string]any{"Title": i}); err != nil {
				errs <- err
				return
			}
			out := b.String()
			if !strings.Contains(out, `nonce="`+nonce+`"`) || !strings.Contains(out, word) ||
				!strings.Contains(out, fmt.Sprintf("<title>%d</title>", i)) {
				errs <- fmt.Errorf("request %d got another request's state: %s", i, out)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestEngineRenderHTTP(t *testing.T) {
	e := newEngine(t, siteFiles, EngineOptions{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	if err := e.Render(rec, req, http.StatusOK, "row.html", nil, map[string]any{"Title": "t"}); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || rec.Body.String() != "<p>row t</p>" {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
	h := rec.Result().Header
	if h.Get("Content-Type") != "text/html; charset=UTF-8" || h.Get("Cache-Control") != "no-cache, must-revalidate" {
		t.Errorf("headers %v", h)
	}
	etag := h.Get("Etag")
	if len(etag) != 42 {
		t.Errorf("etag %q", etag)
	}
	// Conditional request: 304 without body.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("If-None-Match", etag)
	_ = e.Render(rec, req, http.StatusOK, "row.html", nil, map[string]any{"Title": "t"})
	if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
		t.Errorf("conditional: %d %q", rec.Code, rec.Body.String())
	}
	// HEAD: headers only.
	rec = httptest.NewRecorder()
	_ = e.Render(rec, httptest.NewRequest("HEAD", "/x", nil), http.StatusOK, "row.html", nil, map[string]any{"Title": "t"})
	if rec.Code != 200 || rec.Body.Len() != 0 || rec.Result().Header.Get("Content-Length") != "12" {
		t.Errorf("HEAD: %d %q %v", rec.Code, rec.Body.String(), rec.Result().Header)
	}
	// Errors do not write anything.
	rec = httptest.NewRecorder()
	if err := e.Render(rec, req, 200, "nope.html", nil, nil); err == nil || rec.Body.Len() != 0 || rec.Code != 200 {
		t.Errorf("failed render wrote: %v %d %q", err, rec.Code, rec.Body.String())
	}
}

func TestSitePageAndError(t *testing.T) {
	langs := testLanguages(t)
	e := newEngine(t, siteFiles, EngineOptions{})
	s := &Site{Engine: e, Languages: langs, ErrorPage: "error.html",
		Clock: func() time.Time { return time.Unix(0, 0) }}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/demo/tasks/x", nil)
	rc := s.NewRender(rec, req, langs.Get("es"))
	if rc.Root != "../.." || rc.Lang.ID != "es" || rec.Header().Get("Content-Language") != "es" {
		t.Errorf("render state %+v", rc)
	}
	s.Error(rec, req, rc, 404)
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), "Error 404") {
		t.Errorf("error page: %d %s", rec.Code, rec.Body.String())
	}
	// A failing page becomes the error page with 500.
	rec = httptest.NewRecorder()
	s.Page(rec, req, rc, 200, "overview.html", 42)
	if rec.Code != 500 || !strings.Contains(rec.Body.String(), "Error 500") {
		t.Errorf("failing page: %d %s", rec.Code, rec.Body.String())
	}
	// Without state: the critical error text of CMS.
	rec = httptest.NewRecorder()
	s.Error(rec, req, nil, 403)
	if rec.Code != 403 || rec.Body.String() != CriticalError {
		t.Errorf("plain error: %d %q", rec.Code, rec.Body.String())
	}
}

func TestRequestRoot(t *testing.T) {
	for path, want := range map[string]URL{
		"/":                     ".",
		"/demo":                 ".",
		"/demo/":                "..",
		"/demo/tasks/a%2Fb/sub": "../../..",
		"/demo/tasks/x?y=/z/w":  "../..",
	} {
		req := httptest.NewRequest("GET", path, nil)
		if got := RequestRoot(req); got != want {
			t.Errorf("%s: %q, want %q", path, got, want)
		}
	}
	// StripPrefix does not change the root: the browser sees the full path.
	req := httptest.NewRequest("GET", "/static/css/x.css", nil)
	var got URL
	http.StripPrefix("/static", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = RequestRoot(r)
	})).ServeHTTP(httptest.NewRecorder(), req)
	if got != "../.." {
		t.Errorf("after StripPrefix: %q", got)
	}
}

func TestSiteFuncs(t *testing.T) {
	langs := testLanguages(t)
	st, err := NewStatic(SharedStatic)
	if err != nil {
		t.Fatal(err)
	}
	// Every function is checked through small templates.
	tz := time.FixedZone("X", 2*3600)
	now := time.Date(2026, 10, 14, 23, 30, 0, 0, time.UTC)
	rc := &Render{Lang: langs.Get("es"), TZ: tz, Now: now, Root: "..", Nonce: "abc+/="}
	cases := []struct{ tmpl, want string }{
		{`{{T "Overview"}}`, "Carátula"},
		{`{{Tc "x" "Overview"}}`, "Overview"},
		{`{{Format (Tn "Every minute " "Every %(gen_interval)g minutes " 2) "gen_interval" 2}}`, "Cada 2 minutos "},
		{`{{Format (Tn "Every minute " "Every %(gen_interval)g minutes " 1) "gen_interval" 1}}`, "Cada minuto "},
		{`{{Format (T "Error %(status_code)s") "status_code" 404}}`, "Error 404"},
		{`{{Format (T "Logged in as <strong>%(first_name)s %(last_name)s</strong> <em>(%(username)s)</em>") "first_name" "<b>" "last_name" "B" "username" "u"}}`,
			"Acceso permitido a <strong>&lt;b&gt; B</strong> <em>(u)</em>"},
		{`{{Format "<i>%(a)s</i>" "a" "x"}}`, "&lt;i&gt;x&lt;/i&gt;"},
		{`{{lang}}`, "es"},
		{`{{(translation).Name}}`, "español"},
		{`{{url}}|{{url "tasks" "a b" 3}}`, "..|../tasks/a%20b/3"},
		{`{{static "css" "nope.css"}}`, "../static/css/nope.css"},
		{`{{static "jq/jquery-3.6.0.min.js"}}`, "../static/jq/jquery-3.6.0.min.js?h=" + st.Hash("jq/jquery-3.6.0.min.js")},
		{`<script nonce="{{nonce}}"></script>`, `<script nonce="abc&#43;/="></script>`},
		{`{{xsrf_form_html}}`, ""},
		// BasicLocale is Babel's English: U+202F before AM/PM (CLDR 42).
		{`{{format_datetime .T}}`, "Oct 15, 2026, 1:30:00\u202fAM"},
		{`{{format_time .T}}`, "1:30:00\u202fAM"},
		{`{{format_datetime_smart .T}}`, "1:30:00\u202fAM"},
		{`{{format_datetime_smart .Old}}`, "Oct 13, 2026, 1:30:00\u202fAM"},
		{`{{format_timedelta .D}}`, "1 hour and 1 second"},
		{`{{format_duration 1.5}}|{{format_duration 1 "long"}}`, "1.500 sec|1.000 second"},
		{`{{format_size 1536}}`, "1.50 KiB"},
		{`{{format_decimal 1234.5}}`, "1,234.5"},
		{`{{format_status_text .S}}`, "La evaluación no generó el archivo out.txt"},
		{`{{format_status_text .Bad}}`, "N/A"},
		{`{{today .T}}|{{today .Old}}`, "true|false"},
		{`{{all .Times "today"}}|{{any .Times "today"}}`, "false|true"},
		{`{{all .Bools}}|{{any .Bools}}|{{all .Empty}}|{{any .Empty}}`, "false|true|true|false"},
		{`{{range $k, $v := dictselect .M "in" .Allowed}}{{$k}}={{$v}};{{end}}`, "c=3;cpp=1;"},
		{`{{range $k, $v := dictselect .M "eq" 2 "by=value"}}{{$k}}={{$v}};{{end}}`, "py=2;"},
		{`{{all (list "a.c" "b.c") "endswith" ".c"}}|{{any (list "ab") "contains" "b"}}`, "true|true"},
		{`{{markdown "*x* <b>"}}`, "<p><em>x</em> &lt;b&gt;</p>\n"},
		{`{{make_timestamp .T}}|{{make_timestamp .Micro}}`, "1792020600.0|1792020600.000123"},
		{`<script>var t = {{make_timestamp .T}};</script>`, "<script>var t =  1792020600.0 ;</script>"},
		{`{{mimetype "a.pdf"}}|{{mime_icon (mimetype "a.pdf")}}|{{mime_name "application/pdf"}}`,
			"application/pdf|x-office-document|documento PDF"},
		{`{{mimetype "nothing"}}|{{mime_icon "foo/bar"}}`, "|foo-x-generic"},
		{`{{with dict "a" 1 "b" "x"}}{{.a}}{{.b}}{{end}}|{{len (list 1 2 3)}}`, "1x|3"},
		{`{{add 1 2}} {{sub 1 2}} {{mul 2 3}} {{div 3 2}} {{add 1 0.5}}`, "3 -1 6 1.5 1.5"},
		{`{{join ", " (list "a" 1 2.0)}}`, "a, 1, 2.0"},
		{`{{replace ".%l" ".cpp" "sol.%l"}}`, "sol.cpp"},
		{`{{default "x" .Nil}}|{{default "x" "y"}}`, "x|y"},
		{`<script>var v = {{tojson .M}};</script>`, `<script>var v = {"c":3,"cpp":1,"py":2};</script>`},
		{`{{format "%s.png" "icon"}}|{{format "%05.1f" 3.14159}}`, "icon.png|003.1"},
		{`{{(now).Year}}|{{timezone}}`, "2026|X"},
		{`{{contest_url "tasks"}}`, "../demo/tasks"},
		// contest.html builds CMS.CWSUtils with URLs and timestamps in JS.
		{`<script>var u = new X("{{url}}", "{{contest_url}}", {{make_timestamp .T}}, {{T "Overview"}});</script>`,
			`<script>var u = new X("..", "..\/demo",  1792020600.0 , "Carátula");</script>`},
		{`<a href="{{url "tasks" "a b"}}" title="{{T "Overview"}}">`, `<a href="../tasks/a%20b" title="Carátula">`},
	}
	type data struct {
		T, Old, Micro time.Time
		D             time.Duration
		S, Bad        []string
		Times         []time.Time
		Bools, Empty  []bool
		M             map[string]int
		Allowed       []string
		Nil           *int
	}
	d := data{
		T: now, Old: now.Add(-48 * time.Hour), Micro: now.Add(123456 * time.Nanosecond), D: time.Hour + time.Second,
		S:     []string{"Evaluation didn't produce file %s", "out.txt"},
		Bad:   []string{"Evaluation didn't produce file %s"},
		Times: []time.Time{now, now.Add(-48 * time.Hour)},
		Bools: []bool{true, false},
		M:     map[string]int{"cpp": 1, "py": 2, "c": 3}, Allowed: []string{"cpp", "c"},
	}
	for _, c := range cases {
		files := map[string]string{"t.html": c.tmpl}
		e := newEngine(t, files, EngineOptions{Static: st, Funcs: func(cur func() *Render) template.FuncMap {
			return template.FuncMap{"contest_url": func(c ...any) string { return cur().Root.Sub("demo").Join(c...) }}
		}})
		var b strings.Builder
		if err := e.Execute(&b, "t.html", rc, d); err != nil {
			t.Errorf("%s: %v", c.tmpl, err)
			continue
		}
		if b.String() != c.want {
			t.Errorf("%s\n got %q\nwant %q", c.tmpl, b.String(), c.want)
		}
	}
}

// BenchmarkRender renders a page shaped like the contest overview (a
// layout chain, a macro, 30 rows with translations and formats) through
// the instance pool, in parallel.
func BenchmarkRender(b *testing.B) {
	langs := testLanguages(b)
	files := map[string]string{
		"base.html":    siteFiles["base.html"],
		"contest.html": siteFiles["contest.html"],
		"macro/row.html": `{{define "row"}}<tr><td>{{.Name}}</td><td>{{format_datetime_smart .At}}</td>` +
			`<td>{{format_size .Size}}</td><td>{{T "Submissions"}}</td><td><a href="{{url "tasks" .Name}}">{{.Name}}</a></td></tr>{{end}}`,
		"overview.html": `{{/* extends "contest.html" */}}{{define "title"}}{{.Title}}{{end}}` +
			`{{define "core"}}<table>{{range .Rows}}{{template "row" .}}{{end}}</table>{{xsrf_form_html}}{{end}}`,
	}
	e, err := NewEngine(EngineOptions{Templates: mapFS(files)})
	if err != nil {
		b.Fatal(err)
	}
	type row struct {
		Name string
		At   time.Time
		Size int64
	}
	data := struct {
		Title string
		Rows  []row
	}{Title: "Demo"}
	for i := 0; i < 30; i++ {
		data.Rows = append(data.Rows, row{fmt.Sprintf("task%d", i), time.Unix(int64(1_790_000_000+i*600), 0), int64(i * 1000)})
	}
	es := langs.Get("es")
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/demo/tasks", nil)
		for pb.Next() {
			rec.Body.Reset()
			rc := &Render{Lang: es, TZ: time.UTC, Now: time.Unix(1_790_000_000, 0), Root: "..", Nonce: "n"}
			if err := e.Render(rec, req, 200, "overview.html", rc, data); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func TestExtendsDirective(t *testing.T) {
	for src, want := range map[string]string{
		`{{/* extends "a.html" */}}`:              "a.html",
		"\n  {{- /* extends \"a.html\" */ -}}\nx": "a.html",
		"{{/*\nSPDX-License-Identifier: AGPL-3.0-or-later\nPorted from cms/server/contest/templates/x.html\n*/}}\n{{/* extends \"contest.html\" */}}": "contest.html",
		`{{/* license */}}<p>{{/* extends "a.html" */}}`: "",
		`<p>{{/* extends "a.html" */}}`:                  "",
		`{{/* extend "a.html" */}}`:                      "",
		``:                                               "",
	} {
		if got := extendsOf([]byte(src)); got != want {
			t.Errorf("extendsOf(%q) = %q, want %q", src, got, want)
		}
	}
	// A license header before the directive, trimmed from the output.
	e := newEngine(t, map[string]string{
		"base.html": "{{/* Ported from base.html */}}\n<!DOCTYPE html>\n<b>{{block \"x\" .}}{{end}}</b>\n",
		"page.html": "{{/* license */}}\n{{/* extends \"base.html\" */}}\n{{define \"x\"}}X{{end}}\n",
	}, EngineOptions{})
	if got := render(t, e, "page.html", nil, nil); got != "<!DOCTYPE html>\n<b>X</b>" {
		t.Errorf("got %q", got)
	}
}

var staticHash = regexp.MustCompile(`\?h=[0-9a-f]{24}`)

// TestCMSPageBytes renders base.html and the login branch of contest.html
// of CMS, ported line by line (testdata/cms), and compares with the page
// the original CMS sent for the same contest (testdata/cms_login.golden):
// the same bytes, except the hashes of static files this package does not
// ship and html/template's escaping of strings inside <script>.
func TestCMSPageBytes(t *testing.T) {
	st, err := NewStatic(SharedStatic, fstest.MapFS{
		"css/bootstrap.css": {Data: []byte("x")}, "cws_style.css": {Data: []byte("x")},
		"js/bootstrap.js": {Data: []byte("x")}, "cws_utils.js": {Data: []byte("x")},
	})
	if err != nil {
		t.Fatal(err)
	}
	sub, _ := fs.Sub(os.DirFS("testdata"), "cms")
	e, err := NewEngine(EngineOptions{Templates: sub, Static: st,
		Funcs: func(cur func() *Render) template.FuncMap {
			return template.FuncMap{"contest_url": func(c ...any) string { return cur().Root.Sub("demo").Join(c...) }}
		}})
	if err != nil {
		t.Fatal(err)
	}
	langs := testLanguages(t)
	ch := langs.Choose(httptest.NewRequest("GET", "/demo", nil), langs.All())
	type lang struct {
		Name       string
		Extensions []string
	}
	data := map[string]any{
		"Contest": map[string]any{"Description": "Demo Contest", "Name": "demo", "AllowRegistration": false},
		"Languages": []lang{{"C++17 / g++", []string{".cpp", ".cc", ".cxx", ".c++", ".C"}}, {"C11 / gcc", []string{".c"}},
			{"Python 3 / CPython", []string{".py"}}, {"Java / JDK", []string{".java"}}},
		"Participation": nil, "Choice": ch, "LoginError": false, "Next": "",
	}
	rc := &Render{Lang: ch.Lang, Root: "."}
	rc.xsrf = func() string { return "2|4cf14561|34407eb103d38f64093be61db61a7b20|1792008492" }
	got := render(t, e, "contest.html", rc, data)
	want, err := os.ReadFile("testdata/cms_login.golden")
	if err != nil {
		t.Fatal(err)
	}
	norm := func(s string) string {
		s = staticHash.ReplaceAllString(s, "?h=HASH")
		return strings.NewReplacer(`\u002b`, "+", `\/`, "/").Replace(s)
	}
	if norm(got) != norm(string(want)) {
		gl, wl := strings.Split(norm(got), "\n"), strings.Split(norm(string(want)), "\n")
		for i := 0; i < len(gl) || i < len(wl); i++ {
			var g, w string
			if i < len(gl) {
				g = gl[i]
			}
			if i < len(wl) {
				w = wl[i]
			}
			if g != w {
				t.Fatalf("line %d differs:\n got %q\nwant %q", i+1, g, w)
			}
		}
	}
}
