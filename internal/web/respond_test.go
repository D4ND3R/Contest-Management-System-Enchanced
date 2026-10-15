// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package web

import (
	"bytes"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestWriteAndReadJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := WriteJSON(rec, 403, map[string]any{"error": "Login failed", "x": "<script>"}); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 403 || rec.Header().Get("Content-Type") != "application/json; charset=utf-8" ||
		rec.Body.String() != `{"error":"Login failed","x":"\u003cscript\u003e"}` {
		t.Errorf("%d %v %s", rec.Code, rec.Header(), rec.Body.String())
	}
	var v struct{ A int }
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"A": 3, "B": 1}`))
	if err := ReadJSON(httptest.NewRecorder(), r, 100, &v); err != nil || v.A != 3 {
		t.Errorf("ReadJSON: %v %v", v, err)
	}
	r = httptest.NewRequest("POST", "/", strings.NewReader(`{"A": 3} {}`))
	if err := ReadJSON(httptest.NewRecorder(), r, 100, &v); err == nil {
		t.Error("trailing data accepted")
	}
	r = httptest.NewRequest("POST", "/", strings.NewReader(`{"A": "`+strings.Repeat("x", 200)+`"}`))
	if err := ReadJSON(httptest.NewRecorder(), r, 100, &v); !errors.Is(err, ErrBodyTooLarge) || FormStatus(err) != 413 {
		t.Errorf("large body: %v", err)
	}
}

func TestParseFormLimits(t *testing.T) {
	form := url.Values{"a": {strings.Repeat("x", 1000)}}
	r := httptest.NewRequest("POST", "/", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	err := ParseForm(httptest.NewRecorder(), r, 100, 1<<20)
	if !errors.Is(err, ErrBodyTooLarge) || FormStatus(err) != http.StatusRequestEntityTooLarge {
		t.Errorf("urlencoded over the limit: %v", err)
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("f", "a.c")
	_, _ = fw.Write(bytes.Repeat([]byte("y"), 5000))
	_ = mw.Close()
	r = httptest.NewRequest("POST", "/", bytes.NewReader(body.Bytes()))
	r.Header.Set("Content-Type", mw.FormDataContentType())
	if err := ParseForm(httptest.NewRecorder(), r, 1000, 1<<20); !errors.Is(err, ErrBodyTooLarge) {
		t.Errorf("multipart over the limit: %v", err)
	}
	r = httptest.NewRequest("POST", "/", bytes.NewReader(body.Bytes()))
	r.Header.Set("Content-Type", mw.FormDataContentType())
	if err := ParseForm(httptest.NewRecorder(), r, 1<<20, 100); err != nil || r.MultipartForm.File["f"][0].Size != 5000 {
		t.Errorf("multipart: %v", err)
	}
	r = httptest.NewRequest("POST", "/", strings.NewReader("--x\r\nbroken"))
	r.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	if err := ParseForm(httptest.NewRecorder(), r, 1<<20, 100); err == nil || FormStatus(err) != 400 {
		t.Errorf("malformed: %v", err)
	}
}

func TestArgsLikeTornado(t *testing.T) {
	form := url.Values{"a": {"first", "  last\x01\x1f \t"}, "b": {"ok"}, "n": {"\xff\xfeé"}, "t": {"1"}, "f": {"0"}, "x": {"yes"}}
	r := httptest.NewRequest("POST", "/?q=%20query%20&b=fromquery", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := ParseForm(httptest.NewRecorder(), r, 1<<20, 1<<20); err != nil {
		t.Fatal(err)
	}
	if v, ok := Arg(r, "a"); !ok || v != "last" {
		t.Errorf("a: %q", v)
	}
	if v, _ := Arg(r, "q"); v != "query" {
		t.Errorf("q: %q", v)
	}
	if got := Args(r, "b"); len(got) != 2 || got[0] != "ok" || got[1] != "fromquery" {
		t.Errorf("b: %q", got)
	}
	if v, _ := Arg(r, "n"); v != "�é" {
		t.Errorf("n: %q", v)
	}
	if _, ok := Arg(r, "missing"); ok || ArgDefault(r, "missing", "d") != "d" {
		t.Error("missing")
	}
	for name, want := range map[string]bool{"t": true, "f": false, "missing": true} {
		if v, err := BoolArg(r, name, true); err != nil || v != want {
			t.Errorf("BoolArg(%s) = %v %v", name, v, err)
		}
	}
	if _, err := BoolArg(r, "x", false); err == nil {
		t.Error("BoolArg accepted yes")
	}
	// Without ParseForm only the query is read.
	g := httptest.NewRequest("GET", "/?next=%2Fdemo", nil)
	if v, _ := Arg(g, "next"); v != "/demo" {
		t.Errorf("query only: %q", v)
	}
}
