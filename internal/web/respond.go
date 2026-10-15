// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// WriteJSON sends v as JSON with status, as CMS's json() helper
// (Content-Type: application/json; charset=utf-8). The pages never
// cache these answers.
func WriteJSON(w http.ResponseWriter, status int, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	if h.Get("Cache-Control") == "" {
		h.Set("Cache-Control", "no-cache, must-revalidate")
	}
	h.Set("Content-Length", strconv.Itoa(len(b)))
	w.WriteHeader(status)
	_, err = w.Write(b)
	return err
}

// ReadJSON decodes a JSON body of at most maxBody bytes into v; unknown
// fields are allowed, trailing data is not.
func ReadJSON(w http.ResponseWriter, r *http.Request, maxBody int64, v any) error {
	body := http.MaxBytesReader(w, r.Body, maxBody)
	dec := json.NewDecoder(body)
	if err := dec.Decode(v); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return ErrBodyTooLarge
		}
		return fmt.Errorf("web: bad JSON body: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("web: bad JSON body: trailing data")
	}
	return nil
}

// ParseForm parses the query and the body (urlencoded or multipart) of
// r once: the body is capped to maxBody bytes (ErrBodyTooLarge, for a
// 413) and multipart parts beyond maxMemory bytes go to temporary files.
// A form already parsed (by CSRF.Protect, under the site-wide body
// limit) is left as it is, so handlers check their own per-file limits
// on r.MultipartForm. Any other failure is a malformed request (400).
func ParseForm(w http.ResponseWriter, r *http.Request, maxBody, maxMemory int64) error {
	if r.Form != nil && (r.MultipartForm != nil || !isMultipart(r)) {
		return nil
	}
	if maxBody > 0 && r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	}
	err := parseBody(r, maxMemory)
	if errors.Is(err, ErrXSRFFormat) {
		return errors.New("web: malformed form")
	}
	return err
}

// FormStatus maps an error of ParseForm or ReadJSON to a status code.
func FormStatus(err error) int {
	if errors.Is(err, ErrBodyTooLarge) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}

// Args returns every value of an argument of the query or the body
// (Tornado get_arguments), cleaned as Arg does. The form must be parsed
// (ParseForm) for body arguments; otherwise only the query is read.
func Args(r *http.Request, name string) []string {
	var vs []string
	if r.Form != nil {
		vs = r.Form[name]
	} else {
		vs = r.URL.Query()[name]
	}
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = cleanArg(v)
	}
	return out
}

// Arg returns the last value of an argument (Tornado get_argument):
// control characters other than tab and newlines become spaces and the
// surrounding whitespace is stripped. ok is false if it is absent.
func Arg(r *http.Request, name string) (string, bool) {
	vs := Args(r, name)
	if len(vs) == 0 {
		return "", false
	}
	return vs[len(vs)-1], true
}

// ArgDefault returns Arg or def when the argument is absent.
func ArgDefault(r *http.Request, name, def string) string {
	if v, ok := Arg(r, name); ok {
		return v
	}
	return def
}

// BoolArg parses a Boolean argument as CMS's get_boolean_argument:
// absent or empty gives def, "0" false, "1" true, anything else an
// error.
func BoolArg(r *http.Request, name string, def bool) (bool, error) {
	switch v := ArgDefault(r, name, ""); v {
	case "":
		return def, nil
	case "0":
		return false, nil
	case "1":
		return true, nil
	default:
		return false, fmt.Errorf("cannot parse boolean argument %s", name)
	}
}

// cleanArg is Tornado's argument cleaning: invalid UTF-8 replaced,
// [\x00-\x08\x0e-\x1f] turned into spaces, whitespace stripped.
func cleanArg(v string) string {
	if !isCleanASCII(v) {
		v = strings.ToValidUTF8(v, "�")
		v = strings.Map(func(c rune) rune {
			if c <= 0x08 || (c >= 0x0e && c <= 0x1f) {
				return ' '
			}
			return c
		}, v)
	}
	return strings.TrimSpace(v)
}

func isCleanASCII(v string) bool {
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c >= 0x80 || c <= 0x08 || (c >= 0x0e && c <= 0x1f) {
			return false
		}
	}
	return true
}
