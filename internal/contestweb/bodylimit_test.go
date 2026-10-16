// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package contestweb

import (
	"bytes"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestFormBodyLimit: bodies of requests that are not uploads are capped
// at MaxFormBody (a urlencoded form is read whole into memory, before
// any login: 64 MiB per anonymous request was accepted); uploads keep
// MaxBody.
func TestFormBodyLimit(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{})
	c := ts.Client()
	big := strings.Repeat("a", MaxFormBody+1)

	if r := c.Post("/demo/login", url.Values{"username": {"alice"}, "password": {"x"}, "x": {big}}); r.Status != http.StatusRequestEntityTooLarge {
		t.Errorf("anonymous urlencoded login: %d", r.Status)
	}
	// No CSRF check there: the form is parsed by the route.
	if r := c.PostRaw("/demo/api/login", url.Values{"username": {"alice"}, "x": {big}}); r.Status != http.StatusRequestEntityTooLarge ||
		r.Body != `{"error":"Request Entity Too Large"}` {
		t.Errorf("API login: %d %s", r.Status, r.Body)
	}
	if r := c.PostFiles("/demo/login", url.Values{"username": {"alice"}}, upload{"f", "f", []byte(big)}); r.Status != http.StatusRequestEntityTooLarge {
		t.Errorf("multipart login: %d", r.Status)
	}
	// A form within the limit still works.
	c.Login("alice", "demo")
	if r := c.Post("/demo/question", url.Values{"question_subject": {"s"}, "question_text": {big}}); r.Status != http.StatusRequestEntityTooLarge {
		t.Errorf("question: %d", r.Status)
	}

	// Uploads may be larger (the handler then applies
	// max_submission_length).
	carol := ts.Client()
	carol.Login("carol", "demo")
	r := carol.PostFiles("/demo/tasks/sum/submit", url.Values{"language": {"C++17 / g++"}},
		upload{"sum.%l", "a.cpp", bytes.Repeat([]byte("/"), 2*MaxFormBody)})
	if r.Status != http.StatusFound {
		t.Errorf("large submission: %d", r.Status)
	}
	if n := carol.notes(); len(n) != 1 || !strings.Contains(n[0], "too big") {
		t.Errorf("large submission: %q", n)
	}
}
