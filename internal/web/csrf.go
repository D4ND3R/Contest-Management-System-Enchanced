// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// CSRF errors (Tornado's messages).
var (
	ErrXSRFMissing   = errors.New("'_xsrf' argument missing from POST")
	ErrXSRFFormat    = errors.New("'_xsrf' argument has invalid format")
	ErrXSRFMismatch  = errors.New("XSRF cookie does not match POST argument")
	ErrCrossSite     = errors.New("cross-site request")
	ErrBodyTooLarge  = errors.New("request body too large")
	errBadXSRFCookie = errors.New("bad _xsrf cookie")
)

// XSRFCookie is the name of the cookie and of the form field.
const XSRFCookie = "_xsrf"

// CSRFOptions configures NewCSRF.
type CSRFOptions struct {
	// Path of the cookie; default "/".
	Path string
	// MaxMemory bounds the memory used to parse a multipart body to find
	// the _xsrf field (larger files go to temporary files); default
	// 8 MiB. The body size itself is capped by httpx.LimitBody.
	MaxMemory int64
	// Exempt skips the check for the requests it returns true for: CMS
	// skips it when X-CMS-Authorization is present (a custom header no
	// form can send) and on the API login.
	Exempt func(*http.Request) bool
	// AllowSameSite accepts unsafe requests whose Sec-Fetch-Site is
	// "same-site" (another port or subdomain of the same site); they
	// are rejected by default, which also stops cookie tossing from a
	// sibling subdomain.
	AllowSameSite bool
	// OnFailure answers rejected requests; nil sends the critical error
	// text of CMS with status 403 (413 for a body over the limit).
	OnFailure func(w http.ResponseWriter, r *http.Request, err error)
}

// CSRF protects forms as Tornado's xsrf_cookies, which the CMS
// templates and scripts rely on: a random token lives in the _xsrf
// cookie (readable by JavaScript: the admin scripts copy it into the
// X-XSRFToken header) and every POST must carry the same token in the
// _xsrf field ({{xsrf_form_html}}) or in the X-XSRFToken or X-CSRFToken
// header (the header is checked first, Tornado reads the field first:
// they only differ if a request sends two different tokens). Tokens use
// Tornado's version 2 format, "2|mask|masked|time":
// every page gets a newly masked copy (BREACH), and tokens are compared
// in constant time. On top of that, browsers' Sec-Fetch-Site must not
// say the request comes from another site.
type CSRF struct {
	o CSRFOptions
}

// NewCSRF returns the protection with the options o.
func NewCSRF(o CSRFOptions) *CSRF {
	if o.Path == "" {
		o.Path = "/"
	}
	if o.MaxMemory <= 0 {
		o.MaxMemory = 8 << 20
	}
	return &CSRF{o: o}
}

// FormToken returns a freshly masked token for the forms of a page. If
// the browser has no valid _xsrf cookie, a new token is created and the
// cookie is set on w (and added to r, so later calls in the same request
// return the same token).
func (c *CSRF) FormToken(w http.ResponseWriter, r *http.Request) string {
	token, ts, err := c.cookieToken(r)
	if err != nil {
		token = make([]byte, 16)
		_, _ = rand.Read(token)
		ts = time.Now().Unix()
		value := encodeXSRF(token, ts)
		ck := &http.Cookie{Name: XSRFCookie, Value: value, Path: c.o.Path}
		setCookie(w, r, ck, false)
		// The last _xsrf cookie wins (as in Tornado): later calls in this
		// request see this one.
		r.AddCookie(&http.Cookie{Name: XSRFCookie, Value: value})
	}
	return encodeXSRF(token, ts)
}

func (c *CSRF) cookieToken(r *http.Request) ([]byte, int64, error) {
	v, ok := lastCookie(r, XSRFCookie)
	if !ok {
		return nil, 0, errBadXSRFCookie
	}
	token, ts, ok := decodeXSRF(v)
	if !ok {
		return nil, 0, errBadXSRFCookie
	}
	return token, ts, nil
}

// safeMethod reports the methods Tornado does not check.
func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// Check validates a request: nil for GET, HEAD and OPTIONS, for exempt
// requests, and for unsafe requests carrying the token of their cookie
// in the X-XSRFToken or X-CSRFToken header or else in the _xsrf
// argument, for which it parses the form (multipart included; not when
// the request has no valid _xsrf cookie, which could never match).
func (c *CSRF) Check(r *http.Request) error {
	if safeMethod(r.Method) || (c.o.Exempt != nil && c.o.Exempt(r)) {
		return nil
	}
	switch strings.ToLower(r.Header.Get("Sec-Fetch-Site")) {
	case "cross-site":
		return ErrCrossSite
	case "same-site":
		if !c.o.AllowSameSite {
			return ErrCrossSite
		}
	}
	expected, _, cookieErr := c.cookieToken(r)
	sent := r.Header.Get("X-Xsrftoken")
	if sent == "" {
		sent = r.Header.Get("X-Csrftoken")
	}
	if sent == "" {
		if cookieErr != nil {
			// Nothing can match: do not even parse the body.
			return ErrXSRFMismatch
		}
		var err error
		if sent, err = c.formToken(r); err != nil {
			return err
		}
	}
	if sent == "" {
		return ErrXSRFMissing
	}
	token, _, ok := decodeXSRF(sent)
	if !ok {
		return ErrXSRFFormat
	}
	if cookieErr != nil {
		// Tornado compares with a new random token: always a mismatch.
		return ErrXSRFMismatch
	}
	if subtle.ConstantTimeCompare(token, expected) != 1 {
		return ErrXSRFMismatch
	}
	return nil
}

// formToken returns the _xsrf argument (body or query, the last one, as
// Tornado's get_argument).
func (c *CSRF) formToken(r *http.Request) (string, error) {
	if err := parseBody(r, c.o.MaxMemory); err != nil {
		return "", err
	}
	if vs := r.Form[XSRFCookie]; len(vs) > 0 {
		return strings.TrimSpace(vs[len(vs)-1]), nil
	}
	return "", nil
}

// parseBody parses the query and a urlencoded or multipart body once.
func parseBody(r *http.Request, maxMemory int64) error {
	if r.Form != nil && (r.MultipartForm != nil || !isMultipart(r)) {
		return nil
	}
	var err error
	if isMultipart(r) {
		err = r.ParseMultipartForm(maxMemory)
	} else {
		err = r.ParseForm()
	}
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return ErrBodyTooLarge
	}
	if err != nil && !errors.Is(err, http.ErrNotMultipart) {
		return ErrXSRFFormat
	}
	return nil
}

func isMultipart(r *http.Request) bool {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return mt == "multipart/form-data"
}

// Protect rejects unsafe requests that fail Check.
func (c *CSRF) Protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := c.Check(r); err != nil {
			if c.o.OnFailure != nil {
				c.o.OnFailure(w, r, err)
				return
			}
			status := http.StatusForbidden
			if errors.Is(err, ErrBodyTooLarge) {
				status = http.StatusRequestEntityTooLarge
			}
			PlainError(w, status)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// encodeXSRF is Tornado's version 2 token: "2|<mask>|<token^mask>|<ts>".
func encodeXSRF(token []byte, ts int64) string {
	var mask [4]byte
	_, _ = rand.Read(mask[:])
	masked := make([]byte, len(token))
	for i := range token {
		masked[i] = token[i] ^ mask[i%4]
	}
	return "2|" + hex.EncodeToString(mask[:]) + "|" + hex.EncodeToString(masked) + "|" + strconv.FormatInt(ts, 10)
}

// decodeXSRF is Tornado's _decode_xsrf_token: version 2 tokens are
// unmasked; anything without a version is a version 1 token (hex, or
// the raw text).
func decodeXSRF(s string) (token []byte, ts int64, ok bool) {
	if s == "" {
		return nil, 0, false
	}
	if v, rest, found := strings.Cut(s, "|"); found && v != "" && v[0] >= '1' && v[0] <= '9' && isDigits(v) {
		if v != "2" {
			return nil, 0, false
		}
		parts := strings.Split(rest, "|")
		if len(parts) != 3 {
			return nil, 0, false
		}
		mask, err1 := hex.DecodeString(parts[0])
		masked, err2 := hex.DecodeString(parts[1])
		t, err3 := strconv.ParseInt(parts[2], 10, 64)
		if err1 != nil || err2 != nil || err3 != nil || len(mask) != 4 || len(masked) == 0 {
			return nil, 0, false
		}
		for i := range masked {
			masked[i] ^= mask[i%4]
		}
		return masked, t, true
	}
	if b, err := hex.DecodeString(s); err == nil {
		return b, time.Now().Unix(), true
	}
	return []byte(s), time.Now().Unix(), true
}
