// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package contestweb

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// TestStatements serves the statements by their content: PDF inline (as
// CMS), LaTeX source as a download, Markdown rendered in a page of the
// site without raw HTML (R5.2).
func TestStatements(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{})
	carol := ts.Client()
	carol.Login("carol", "demo")
	put := func(content string) {
		t.Helper()
		d, err := ts.Blobs.PutBytes(context.Background(), []byte(content))
		if err != nil {
			t.Fatal(err)
		}
		ts.Exec(`UPDATE statements SET digest = $1 WHERE language = 'it'
			AND task_id = (SELECT id FROM tasks WHERE name = 'sum')`, d)
	}
	const path = "/demo/tasks/sum/statements/it/sum.it.pdf"

	r := carol.Get(path)
	if r.Status != http.StatusOK || r.Header.Get("Content-Type") != "application/pdf" ||
		r.Header.Get("Content-Disposition") != "inline; filename=sum.it.pdf" || !strings.HasPrefix(r.Body, "%PDF") {
		t.Errorf("PDF: %d %q %q", r.Status, r.Header.Get("Content-Type"), r.Header.Get("Content-Disposition"))
	}

	put("\\documentclass{article}\n\\begin{document}\nSum $a+b$.\n\\end{document}\n")
	r = carol.Get(path)
	if r.Status != http.StatusOK || r.Header.Get("Content-Type") != "text/x-tex; charset=utf-8" ||
		r.Header.Get("Content-Disposition") != "attachment; filename=sum.it.tex" || !strings.Contains(r.Body, "Sum $a+b$") {
		t.Errorf("LaTeX: %d %q %q", r.Status, r.Header.Get("Content-Type"), r.Header.Get("Content-Disposition"))
	}

	put("# Somma\n\nCalcola *a + b*.\n\n<script>alert(1)</script>\n\n[x](javascript:alert(1))\n")
	r = carol.Get(path)
	if r.Status != http.StatusOK || !strings.HasPrefix(r.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("Markdown: %d %q", r.Status, r.Header.Get("Content-Type"))
	}
	for _, want := range []string{`<div class="statement markdown_statement" lang="it">`, "<h1>Somma</h1>",
		"<em>a + b</em>", `<li class="active">`, "A plus B (sum) <small>description</small>"} {
		if !strings.Contains(r.Body, want) {
			t.Errorf("Markdown: no %q", want)
		}
	}
	for _, bad := range []string{"<script>alert", `href="javascript:`} {
		if strings.Contains(r.Body, bad) {
			t.Errorf("Markdown: %q in the page", bad)
		}
	}

	// Unknown language or task: 404; no login: the login form.
	if r := carol.Get("/demo/tasks/sum/statements/xx"); r.Status != http.StatusNotFound {
		t.Errorf("unknown language: %d", r.Status)
	}
	if r := carol.Get("/demo/tasks/nosuch/statements/it"); r.Status != http.StatusNotFound {
		t.Errorf("unknown task: %d", r.Status)
	}
	// An empty language reaches the handler, as CMS's statements/(.*):
	// the error page of the contest, not Tornado's plain 404.
	if r := carol.Get("/demo/tasks/sum/statements/"); r.Status != http.StatusNotFound || !strings.Contains(r.Body, "<title>Error 404</title>") {
		t.Errorf("empty language: %d\n%s", r.Status, r.Body)
	}
	if r := ts.Client().Get("/demo/tasks/sum/statements/"); r.Status != http.StatusFound {
		t.Errorf("empty language, anonymous: %d", r.Status)
	}
	ts.Exec(`INSERT INTO statements (task_id, language, digest) SELECT task_id, '', digest FROM statements WHERE language = 'it'`)
	if r := carol.Get("/demo/tasks/sum/statements/"); r.Status != http.StatusOK {
		t.Errorf("statement with an empty language: %d", r.Status)
	}
	if r := ts.Client().Get(path); r.Status != http.StatusFound {
		t.Errorf("anonymous: %d", r.Status)
	}
}

// TestTaskPagesPhases: the task pages wait for the contest to start
// (CMS actual_phase_required(0, 1, 2, 3, 4)), unless the participation
// is unrestricted.
func TestTaskPagesPhases(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{})
	ts.Exec(`UPDATE groups SET start = '2027-01-01', stop = '2028-01-01'`)
	carol := ts.Client()
	carol.Login("carol", "demo")
	for _, p := range []string{"/demo/tasks/sum/description", "/demo/tasks/sum/submissions",
		"/demo/tasks/quiz/attachments/input_000.txt", "/demo/tasks/sum/statements/it"} {
		if r := carol.Get(p); r.Status != http.StatusFound || !strings.HasSuffix(r.Location(), "/demo") {
			t.Errorf("before the contest, GET %s: %d %s", p, r.Status, r.Location())
		}
	}
	ts.Exec(`UPDATE participations SET unrestricted = true`)
	if r := carol.Get("/demo/tasks/sum/description"); r.Status != http.StatusOK {
		t.Errorf("unrestricted: %d", r.Status)
	}
	// Attachments by name only.
	ts.Exec(`UPDATE groups SET start = '2025-01-01', stop = '2030-01-01'`)
	for _, p := range []string{"/demo/tasks/quiz/attachments/../input_000.txt", "/demo/tasks/quiz/attachments/input_000.txt%00",
		"/demo/tasks/sum/attachments/input_000.txt"} {
		if r := carol.Get(p); r.Status != http.StatusNotFound && r.Status != http.StatusTemporaryRedirect {
			t.Errorf("GET %s: %d", p, r.Status)
		}
	}
	r := carol.Get("/demo/tasks/quiz/attachments/input_000.txt")
	if r.Status != http.StatusOK || r.Header.Get("Content-Disposition") != "attachment; filename=input_000.txt" {
		t.Errorf("attachment: %d %q", r.Status, r.Header.Get("Content-Disposition"))
	}
}
