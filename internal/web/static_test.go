// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package web

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func testStatic(t *testing.T) (*Static, []byte) {
	t.Helper()
	css := []byte(strings.Repeat("body { color: red; }\n", 200))
	site := fstest.MapFS{
		"cws_style.css": {Data: css},
		"favicon.ico":   {Data: []byte("site icon")},
		"img/x.png":     {Data: []byte("\x89PNG....")},
	}
	st, err := NewStatic(SharedStatic, site)
	if err != nil {
		t.Fatal(err)
	}
	return st, css
}

func get(h http.Handler, target string, hdr ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", target, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestStaticHashAndURL(t *testing.T) {
	st, css := testStatic(t)
	sum := sha256.Sum256(css)
	if h := st.Hash("cws_style.css"); h != hex.EncodeToString(sum[:])[:24] {
		t.Errorf("hash %q", h)
	}
	if got := st.URL("..", "cws_style.css"); got != "../static/cws_style.css?h="+st.Hash("cws_style.css") {
		t.Errorf("URL %q", got)
	}
	if got := st.URL(".", "/jq/jquery-3.6.0.min.js"); !strings.HasPrefix(got, "./static/jq/jquery-3.6.0.min.js?h=") {
		t.Errorf("URL %q", got)
	}
	if got := st.URL(".", "missing.js"); got != "./static/missing.js" {
		t.Errorf("missing file URL %q", got)
	}
	// A later layer replaces a file of an earlier one.
	h := http.StripPrefix("/static", st)
	if rec := get(h, "/static/favicon.ico"); rec.Body.String() != "site icon" {
		t.Errorf("override: %q", rec.Body.String())
	}
	if !st.Has("loading.gif") || st.Has("nope") {
		t.Error("Has")
	}
	var nilStatic *Static
	if nilStatic.URL(".", "a.css") != "./static/a.css" || nilStatic.Hash("a") != "" {
		t.Error("nil Static")
	}
	// The shared files are those of CMS's cms/server/static, plus live.js.
	var names []string
	_ = fs.WalkDir(SharedStatic, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			names = append(names, p)
		}
		return err
	})
	if got := strings.Join(names, " "); got != "favicon.ico jq/jquery-3.6.0.min.js jq/jquery-migrate-3.3.2.min.js live.js loading.gif" {
		t.Errorf("shared files: %s", got)
	}
}

func TestStaticServe(t *testing.T) {
	st, css := testStatic(t)
	h := http.StripPrefix("/static", st)
	hash := st.Hash("cws_style.css")

	rec := get(h, "/static/cws_style.css?h="+hash)
	hd := rec.Result().Header
	if rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), css) {
		t.Fatalf("GET: %d", rec.Code)
	}
	if hd.Get("Cache-Control") != "public, max-age=31536000, immutable" || hd.Get("Content-Type") != "text/css; charset=utf-8" ||
		hd.Get("Etag") != `"`+hash+`"` || hd.Get("Last-Modified") == "" || hd.Get("Vary") != "Accept-Encoding" {
		t.Errorf("headers: %v", hd)
	}
	// No hash, or an old one: revalidate.
	for _, q := range []string{"", "?h=0123456789abcdef01234567"} {
		if cc := get(h, "/static/cws_style.css"+q).Result().Header.Get("Cache-Control"); cc != "no-cache" {
			t.Errorf("%q: Cache-Control %q", q, cc)
		}
	}
	// Conditional requests.
	if rec := get(h, "/static/cws_style.css", "If-None-Match", `"`+hash+`"`); rec.Code != 304 || rec.Body.Len() != 0 {
		t.Errorf("If-None-Match: %d", rec.Code)
	}
	lm := hd.Get("Last-Modified")
	if rec := get(h, "/static/cws_style.css", "If-Modified-Since", lm); rec.Code != 304 {
		t.Errorf("If-Modified-Since: %d", rec.Code)
	}
	// Compression.
	rec = get(h, "/static/cws_style.css", "Accept-Encoding", "br, gzip;q=0.8")
	if rec.Result().Header.Get("Content-Encoding") != "gzip" || rec.Result().Header.Get("Etag") != `"`+hash+`-gz"` {
		t.Fatalf("gzip headers: %v", rec.Result().Header)
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	if plain, _ := io.ReadAll(zr); !bytes.Equal(plain, css) || rec.Body.Len() >= len(css) {
		t.Error("gzip body")
	}
	if rec := get(h, "/static/cws_style.css", "Accept-Encoding", "gzip;q=0"); rec.Result().Header.Get("Content-Encoding") != "" {
		t.Error("gzip;q=0 must not be compressed")
	}
	if rec := get(h, "/static/cws_style.css", "Accept-Encoding", "gzip", "If-None-Match", `"`+hash+`-gz"`); rec.Code != 304 {
		t.Errorf("gzip conditional: %d", rec.Code)
	}
	// Images are not compressed.
	if rec := get(h, "/static/img/x.png", "Accept-Encoding", "gzip"); rec.Result().Header.Get("Content-Encoding") != "" ||
		rec.Result().Header.Get("Content-Type") != "image/png" {
		t.Errorf("png: %v", rec.Result().Header)
	}
	// Ranges come from the identity encoding.
	rec = get(h, "/static/cws_style.css", "Range", "bytes=5-9", "Accept-Encoding", "gzip")
	if rec.Code != 206 || rec.Body.String() != string(css[5:10]) || rec.Result().Header.Get("Content-Encoding") != "" {
		t.Errorf("range: %d %q", rec.Code, rec.Body.String())
	}
	// HEAD, missing files, traversal, methods.
	hr := httptest.NewRequest("HEAD", "/static/cws_style.css", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, hr)
	if rec.Code != 200 || rec.Body.Len() != 0 {
		t.Errorf("HEAD: %d %d", rec.Code, rec.Body.Len())
	}
	for _, p := range []string{"/static/nope.css", "/static/../web/static.go", "/static/jq", "/static/", "/static/./favicon.ico"} {
		if rec := get(h, p); rec.Code != 404 {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
	pr := httptest.NewRequest("POST", "/static/cws_style.css", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, pr)
	if rec.Code != 405 {
		t.Errorf("POST: %d", rec.Code)
	}
	st.NotFound = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(410) })
	if rec := get(h, "/static/nope"); rec.Code != 410 {
		t.Errorf("NotFound handler: %d", rec.Code)
	}
}

func TestAcceptsGzip(t *testing.T) {
	for h, want := range map[string]bool{
		"":                  false,
		"gzip":              true,
		"GZIP":              true,
		"deflate, gzip":     true,
		"gzip;q=0":          false,
		"gzip; q=0.5":       true,
		"*":                 true,
		"*;q=0":             false,
		"br":                false,
		"x-gzip":            true,
		"identity, *;q=0.1": true,
	} {
		if got := acceptsGzip(h); got != want {
			t.Errorf("acceptsGzip(%q) = %v", h, got)
		}
	}
}
