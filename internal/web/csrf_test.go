// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package web

import (
	"bytes"
	"encoding/hex"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// A token of the original CMS (Tornado 4.5), as its login page sends it.
const tornadoToken = "2|4cf14561|34407eb103d38f64093be61db61a7b20|1792008492"

func TestDecodeXSRFLikeTornado(t *testing.T) {
	tok, ts, ok := decodeXSRF(tornadoToken)
	if !ok || hex.EncodeToString(tok) != "78b13bd04f22ca0545caa37cfaeb3e41" || ts != 1792008492 {
		t.Fatalf("decode: %x %d %v", tok, ts, ok)
	}
	// Re-masking keeps the token.
	again, _, _ := decodeXSRF(encodeXSRF(tok, ts))
	if !bytes.Equal(again, tok) {
		t.Error("re-masked token differs")
	}
	// Version 1: the hex token itself.
	if v1, _, ok := decodeXSRF("78b13bd04f22ca0545caa37cfaeb3e41"); !ok || !bytes.Equal(v1, tok) {
		t.Error("version 1")
	}
	for _, bad := range []string{"", "3|a|b|1", "2|4cf14561|zz|1", "2|4cf1|3440|1", "2|4cf14561|3440", "2|4cf14561||1", "2|4cf14561|34|x"} {
		if _, _, ok := decodeXSRF(bad); ok {
			t.Errorf("accepted %q", bad)
		}
	}
}

func post(form url.Values, cookie string, mod func(*http.Request)) *http.Request {
	r := httptest.NewRequest("POST", "/demo/login", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: XSRFCookie, Value: cookie})
	}
	if mod != nil {
		mod(r)
	}
	return r
}

func TestCSRFCheck(t *testing.T) {
	c := NewCSRF(CSRFOptions{})
	// The page: the cookie is created once per request, every form gets a
	// differently masked copy of the same token.
	rec := httptest.NewRecorder()
	page := httptest.NewRequest("GET", "/demo", nil)
	f1, f2 := c.FormToken(rec, page), c.FormToken(rec, page)
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != XSRFCookie || cookies[0].HttpOnly || cookies[0].Path != "/" ||
		cookies[0].SameSite != http.SameSiteLaxMode || cookies[0].Secure {
		t.Fatalf("cookie: %+v", cookies)
	}
	if f1 == f2 {
		t.Error("the form tokens should be masked differently")
	}
	ck := cookies[0].Value
	ok := func(name string, r *http.Request) {
		t.Helper()
		if err := c.Check(r); err != nil {
			t.Errorf("%s: rejected: %v", name, err)
		}
	}
	fail := func(name string, r *http.Request, want error) {
		t.Helper()
		if err := c.Check(r); !errors.Is(err, want) {
			t.Errorf("%s: got %v, want %v", name, err, want)
		}
	}
	ok("GET", httptest.NewRequest("GET", "/x", nil))
	ok("HEAD", httptest.NewRequest("HEAD", "/x", nil))
	ok("form field", post(url.Values{"_xsrf": {f1}}, ck, nil))
	ok("other masking", post(url.Values{"_xsrf": {f2}}, ck, nil))
	ok("cookie value itself", post(url.Values{"_xsrf": {ck}}, ck, nil))
	ok("X-XSRFToken", post(nil, ck, func(r *http.Request) { r.Header.Set("X-XSRFToken", f1) }))
	ok("X-CSRFToken", post(nil, ck, func(r *http.Request) { r.Header.Set("X-CSRFToken", f1) }))
	ok("query argument", post(nil, ck, func(r *http.Request) { r.URL.RawQuery = "_xsrf=" + url.QueryEscape(f1) }))
	ok("same-origin", post(url.Values{"_xsrf": {f1}}, ck, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "same-origin") }))
	fail("missing", post(url.Values{"username": {"a"}}, ck, nil), ErrXSRFMissing)
	fail("empty", post(url.Values{"_xsrf": {""}}, ck, nil), ErrXSRFMissing)
	fail("bad format", post(url.Values{"_xsrf": {"2|zz|zz|1"}}, ck, nil), ErrXSRFFormat)
	fail("no cookie", post(url.Values{"_xsrf": {f1}}, "", nil), ErrXSRFMismatch)
	noCookie := post(url.Values{"_xsrf": {f1}}, "", nil)
	_ = c.Check(noCookie)
	if noCookie.Form != nil {
		t.Error("the body was parsed for a request that cannot match")
	}
	fail("no cookie, header", post(nil, "", func(r *http.Request) { r.Header.Set("X-XSRFToken", f1) }), ErrXSRFMismatch)
	fail("other token", post(url.Values{"_xsrf": {tornadoToken}}, ck, nil), ErrXSRFMismatch)
	fail("cookie tampered", post(url.Values{"_xsrf": {f1}}, tornadoToken, nil), ErrXSRFMismatch)
	fail("cross-site", post(url.Values{"_xsrf": {f1}}, ck, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }), ErrCrossSite)
	fail("same-site", post(url.Values{"_xsrf": {f1}}, ck, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "same-site") }), ErrCrossSite)
	ok("same-site allowed", func() *http.Request {
		c2 := NewCSRF(CSRFOptions{AllowSameSite: true})
		r := post(url.Values{"_xsrf": {f1}}, ck, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "same-site") })
		if err := c2.Check(r); err != nil {
			t.Errorf("AllowSameSite: %v", err)
		}
		return post(url.Values{"_xsrf": {f1}}, ck, nil)
	}())
	// The last _xsrf of the form wins, as Tornado's get_argument.
	ok("last value", post(url.Values{"_xsrf": {"junk", f1}}, ck, nil))
	// Multipart forms (submissions).
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("_xsrf", f1)
	fw, _ := mw.CreateFormFile("file", "a.c")
	_, _ = fw.Write([]byte("int main(){}"))
	_ = mw.Close()
	mr := httptest.NewRequest("POST", "/demo/tasks/a/submit", &body)
	mr.Header.Set("Content-Type", mw.FormDataContentType())
	mr.AddCookie(&http.Cookie{Name: XSRFCookie, Value: ck})
	ok("multipart", mr)
	if mr.MultipartForm == nil || len(mr.MultipartForm.File["file"]) != 1 {
		t.Error("the multipart form should stay parsed for the handler")
	}
	// Exempt requests (CMS: X-CMS-Authorization).
	ex := NewCSRF(CSRFOptions{Exempt: func(r *http.Request) bool { return r.Header.Get("X-CMS-Authorization") != "" }})
	if err := ex.Check(post(nil, "", func(r *http.Request) { r.Header.Set("X-CMS-Authorization", "x") })); err != nil {
		t.Errorf("exempt: %v", err)
	}
}

func TestCSRFNewCookieWhenInvalid(t *testing.T) {
	c := NewCSRF(CSRFOptions{})
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: XSRFCookie, Value: "2|bad"})
	form := c.FormToken(rec, r)
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected a new cookie, got %v", cookies)
	}
	if err := c.Check(post(url.Values{"_xsrf": {form}}, cookies[0].Value, nil)); err != nil {
		t.Error(err)
	}
	// A valid cookie is kept.
	rec = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: XSRFCookie, Value: tornadoToken})
	form = c.FormToken(rec, r)
	if len(rec.Result().Cookies()) != 0 {
		t.Error("valid cookie replaced")
	}
	if err := c.Check(post(url.Values{"_xsrf": {form}}, tornadoToken, nil)); err != nil {
		t.Error(err)
	}
}

func TestCSRFProtect(t *testing.T) {
	c := NewCSRF(CSRFOptions{})
	called := false
	h := c.Protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, post(url.Values{"username": {"a"}}, tornadoToken, nil))
	if called || rec.Code != 403 || rec.Body.String() != CriticalError {
		t.Errorf("rejected request: called=%v %d %q", called, rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, post(url.Values{"_xsrf": {tornadoToken}}, tornadoToken, nil))
	if !called || rec.Code != 200 {
		t.Errorf("valid request: called=%v %d", called, rec.Code)
	}
	// A body over the limit is 413, not 403.
	called = false
	big := post(url.Values{"_xsrf": {strings.Repeat("x", 4096)}}, tornadoToken, nil)
	rec = httptest.NewRecorder()
	big.Body = http.MaxBytesReader(rec, big.Body, 100)
	h.ServeHTTP(rec, big)
	if called || rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("large body: called=%v %d", called, rec.Code)
	}
	// OnFailure.
	var seen error
	c2 := NewCSRF(CSRFOptions{OnFailure: func(w http.ResponseWriter, r *http.Request, err error) {
		seen = err
		w.WriteHeader(418)
	}})
	rec = httptest.NewRecorder()
	c2.Protect(http.NotFoundHandler()).ServeHTTP(rec, post(nil, tornadoToken, nil))
	if rec.Code != 418 || !errors.Is(seen, ErrXSRFMissing) {
		t.Errorf("OnFailure: %d %v", rec.Code, seen)
	}
}

func TestXSRFFormHTMLLikeTornado(t *testing.T) {
	e := newEngine(t, map[string]string{"f.html": `<form>{{xsrf_form_html}}</form><i data-t="{{xsrf_token}}"></i>`}, EngineOptions{})
	c := NewCSRF(CSRFOptions{})
	s := &Site{Engine: e, CSRF: c}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/demo", nil)
	rc := s.NewRender(rec, req, nil)
	out := render(t, e, "f.html", rc, nil)
	tok := rc.XSRFToken()
	want := `<form><input type="hidden" name="_xsrf" value="` + tok + `"/></form><i data-t="` + tok + `"></i>`
	if out != want {
		t.Errorf("got %s\nwant %s", out, want)
	}
	if n := len(rec.Result().Cookies()); n != 1 {
		t.Errorf("%d cookies", n)
	}
}
