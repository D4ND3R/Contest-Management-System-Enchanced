// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/httpx"
)

// Errors of signed values.
var (
	ErrBadSignature = errors.New("web: bad signature")
	ErrExpired      = errors.New("web: signed value expired")
)

// maxClockSkew is how far in the future a signed timestamp may be (other
// replicas' clocks).
const maxClockSkew = 5 * time.Minute

// SetCookie sets a cookie with the defaults of the sites: Path "/",
// HttpOnly, SameSite=Lax, and Secure when the request came over HTTPS
// (directly or, behind num_proxies trusted proxies, per
// X-Forwarded-Proto; see httpx.IsHTTPS). Fields already set in c are
// kept, except that HttpOnly is always on: use the CSRF cookie for
// values JavaScript must read.
func SetCookie(w http.ResponseWriter, r *http.Request, c *http.Cookie) {
	setCookie(w, r, c, true)
}

func setCookie(w http.ResponseWriter, r *http.Request, c *http.Cookie, httpOnly bool) {
	ck := *c
	if ck.Path == "" {
		ck.Path = "/"
	}
	ck.HttpOnly = httpOnly
	if ck.SameSite == 0 { // unset (SameSiteDefaultMode writes a bare "SameSite")
		ck.SameSite = http.SameSiteLaxMode
	}
	if r != nil && httpx.IsHTTPS(r) {
		ck.Secure = true
	}
	http.SetCookie(w, &ck)
}

// ClearCookie deletes a cookie (CMS clear_cookie) at path ("" is "/").
func ClearCookie(w http.ResponseWriter, r *http.Request, name, path string) {
	SetCookie(w, r, &http.Cookie{Name: name, Value: "", Path: path, MaxAge: -1,
		Expires: time.Unix(1, 0)})
}

// lastCookie returns the value of the last cookie named name (as
// Tornado, which keeps the last of duplicates).
func lastCookie(r *http.Request, name string) (string, bool) {
	val, ok := "", false
	for _, c := range r.Cookies() {
		if c.Name == name {
			val, ok = c.Value, true
		}
	}
	return val, ok
}

// Signer signs values bound to a name and a time, as Tornado's secure
// cookies that CMS uses for the login cookie ("<contest>_login") and for
// the API's X-CMS-Authorization header. The key is never secret_key
// itself: derive it with config.DeriveKey and a purpose string (D5), for
// example cfg.DeriveKey("contest-web/cookie").
//
// Format: "1.<unix time>.<base64url value>.<base64url HMAC-SHA256>", the
// HMAC covering the name, the time and the value: a value signed for one
// name (one contest's cookie) is rejected under another.
type Signer struct {
	key []byte
}

// NewSigner returns a signer with key (at least 32 bytes).
func NewSigner(key []byte) *Signer {
	if len(key) < 32 {
		panic("web: signer key shorter than 32 bytes")
	}
	return &Signer{key: append([]byte(nil), key...)}
}

func (s *Signer) mac(name, ts, b64 string) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte("cms-signed/v1\x00"))
	m.Write([]byte(name))
	m.Write([]byte{0})
	m.Write([]byte(ts))
	m.Write([]byte{0})
	m.Write([]byte(b64))
	return m.Sum(nil)
}

// Sign returns value signed for name at time now.
func (s *Signer) Sign(name string, value []byte, now time.Time) string {
	ts := strconv.FormatInt(now.Unix(), 10)
	b64 := base64.RawURLEncoding.EncodeToString(value)
	return "1." + ts + "." + b64 + "." + base64.RawURLEncoding.EncodeToString(s.mac(name, ts, b64))
}

// Verify checks a value signed for name and returns its content. It
// fails with ErrBadSignature if it was tampered with, signed for another
// name or with another key, and with ErrExpired if it was signed more
// than maxAge before now (maxAge <= 0: no limit) or too far in the
// future.
func (s *Signer) Verify(name, signed string, maxAge time.Duration, now time.Time) ([]byte, error) {
	parts := strings.Split(signed, ".")
	if len(parts) != 4 || parts[0] != "1" {
		return nil, ErrBadSignature
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil || !hmac.Equal(sig, s.mac(name, parts[1], parts[2])) {
		return nil, ErrBadSignature
	}
	ts, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return nil, ErrBadSignature
	}
	at := time.Unix(ts, 0)
	if at.After(now.Add(maxClockSkew)) || (maxAge > 0 && at.Before(now.Add(-maxAge))) {
		return nil, ErrExpired
	}
	value, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, ErrBadSignature
	}
	return value, nil
}

// SetCookie sets the cookie name to value, signed (CMS
// set_secure_cookie). maxAge is the cookie lifetime in the browser
// (Max-Age); 0 makes a session cookie. It has the defaults of the
// package-level SetCookie (HttpOnly, SameSite=Lax, Secure over HTTPS).
func (s *Signer) SetCookie(w http.ResponseWriter, r *http.Request, name string, value []byte, maxAge time.Duration) {
	c := &http.Cookie{Name: name, Value: s.Sign(name, value, time.Now())}
	if maxAge > 0 {
		c.MaxAge = int(maxAge / time.Second)
	}
	SetCookie(w, r, c)
}

// Cookie returns the verified content of the signed cookie name (CMS
// get_secure_cookie): http.ErrNoCookie if absent, ErrBadSignature or
// ErrExpired (signed more than maxAge ago; <= 0 means no limit) if it
// cannot be trusted.
func (s *Signer) Cookie(r *http.Request, name string, maxAge time.Duration) ([]byte, error) {
	v, ok := lastCookie(r, name)
	if !ok {
		return nil, http.ErrNoCookie
	}
	return s.Verify(name, v, maxAge, time.Now())
}
