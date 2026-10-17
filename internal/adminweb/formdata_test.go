// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package adminweb

import (
	"bytes"
	"html"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// Helpers to submit the forms of the pages as a browser would: formOf
// reads the fields of a form of a page with their current values, so a
// test changes only what it is about and the rest round-trips.

var (
	formRe     = regexp.MustCompile(`(?s)<form\b([^>]*)>(.*?)</form>`)
	fieldRe    = regexp.MustCompile(`(?s)<(input|textarea|select)\b([^>]*)>`)
	attrRe     = regexp.MustCompile(`([a-zA-Z_:-]+)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+)))?`)
	optionRe   = regexp.MustCompile(`(?s)<option\b([^>]*)>(.*?)(?:</option>|<option|</select>)`)
	textareaRe = regexp.MustCompile(`(?s)^(.*?)</textarea>`)
	selectRe   = regexp.MustCompile(`(?s)^(.*?)</select>`)
)

// attrs parses the attributes of a tag.
func attrs(s string) map[string]string {
	m := map[string]string{}
	for _, a := range attrRe.FindAllStringSubmatch(s, -1) {
		v := a[2] + a[3] + a[4]
		m[strings.ToLower(a[1])] = html.UnescapeString(v)
	}
	return m
}

// formOf returns the fields of the first form of body whose action ends
// with "/"+action (or is action), with the values a browser would
// send; ok is false when there is no such form.
func formOf(body, action string) (url.Values, bool) {
	for _, m := range formRe.FindAllStringSubmatch(body, -1) {
		a := attrs(m[1])["action"]
		if a != action && !strings.HasSuffix(a, "/"+action) {
			continue
		}
		v := url.Values{}
		inner := m[2]
		for _, idx := range fieldRe.FindAllStringSubmatchIndex(inner, -1) {
			tag := inner[idx[2]:idx[3]]
			at := attrs(inner[idx[4]:idx[5]])
			name := at["name"]
			if name == "" {
				continue
			}
			if _, off := at["disabled"]; off {
				continue
			}
			rest := inner[idx[1]:]
			switch tag {
			case "input":
				switch strings.ToLower(at["type"]) {
				case "checkbox", "radio":
					if _, on := at["checked"]; on {
						val, ok := at["value"]
						if !ok {
							val = "on"
						}
						v.Add(name, val)
					}
				case "submit", "button", "file", "image", "reset":
				default:
					v.Add(name, at["value"])
				}
			case "textarea":
				if t := textareaRe.FindStringSubmatch(rest); t != nil {
					v.Add(name, html.UnescapeString(strings.TrimPrefix(t[1], "\n")))
				}
			case "select":
				sel := selectRe.FindStringSubmatch(rest)
				if sel == nil {
					continue
				}
				first, chosen := "", ""
				found := false
				for i, o := range optionRe.FindAllStringSubmatch(sel[1], -1) {
					oa := attrs(o[1])
					val, ok := oa["value"]
					if !ok {
						val = strings.TrimSpace(html.UnescapeString(o[2]))
					}
					if i == 0 {
						first = val
					}
					if _, s := oa["selected"]; s && !found {
						chosen, found = val, true
					}
				}
				if !found {
					chosen = first
				}
				v.Add(name, chosen)
			}
		}
		return v, true
	}
	return nil, false
}

// mustForm is formOf that fails the test without the form.
func (c *testClient) mustForm(page, action string) url.Values {
	c.T.Helper()
	r := c.Get(page)
	if r.Status != http.StatusOK {
		c.T.Fatalf("GET %s: %d", page, r.Status)
	}
	v, ok := formOf(r.Body, action)
	if !ok {
		c.T.Fatalf("GET %s: no form with action …%s", page, action)
	}
	return v
}

// upload is a file of a multipart form.
type upload struct {
	Field, Name string
	Data        []byte
}

// PostFiles sends a multipart form with the _xsrf token.
func (c *testClient) PostFiles(path string, form url.Values, files ...upload) *testResponse {
	c.T.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	if form == nil {
		form = url.Values{}
	}
	if !form.Has("_xsrf") {
		form.Set("_xsrf", c.XSRF())
	}
	for k, vs := range form {
		for _, s := range vs {
			_ = mw.WriteField(k, s)
		}
	}
	for _, f := range files {
		w, err := mw.CreateFormFile(f.Field, f.Name)
		if err != nil {
			c.T.Fatal(err)
		}
		_, _ = w.Write(f.Data)
	}
	_ = mw.Close()
	req, err := http.NewRequest(http.MethodPost, c.Site.URL+path, &b)
	if err != nil {
		c.T.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return c.Do(req)
}

// wantRedirect fails the test unless r is a redirect (to a location
// ending with suffix, when given).
func wantRedirect(r *testResponse, suffix string) bool {
	return r.Status == http.StatusFound && strings.HasSuffix(r.Location(), suffix)
}
