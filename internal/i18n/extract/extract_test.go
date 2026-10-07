// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package extract

import (
	"bytes"
	"strings"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/i18n"
)

func TestExtractGoAndTemplates(t *testing.T) {
	c := NewCollector()
	goSrc := `package x
func f(cat interface{ T(string) string }) {
	_ = cat.T("Submit")
	_ = T("Submit")
	_ = Tn("%(n)d file", "%(n)d files", 3)
	_ = Tc("button", "Open")
	_ = N("Later")
	_ = T(variable) // not extracted
}`
	if err := c.Go("site/handlers.go", []byte(goSrc)); err != nil {
		t.Fatal(err)
	}
	tmpl := `{{define "page"}}<h1>{{T "Submit"}}</h1>
{{if .X}}{{printf "%s" (T "Nested in a pipeline")}}{{else}}{{Tn "one task" "%(n)d tasks" .N}}{{end}}
{{range .Items}}{{Tc "noun" "Open"}}{{end}}{{/* {{T "In a comment"}} */}}{{end}}`
	if err := c.Template("site/templates/page.html", []byte(tmpl)); err != nil {
		t.Fatal(err)
	}
	msgs := c.Messages()
	byID := map[string]Message{}
	for _, m := range msgs {
		byID[m.Context+"|"+m.ID] = m
	}
	if m := byID["|Submit"]; len(m.Refs) != 3 || m.Refs[2] != "site/templates/page.html:1" {
		t.Errorf("Submit refs %v", m.Refs)
	}
	if m := byID["|%(n)d file"]; m.Plural != "%(n)d files" {
		t.Errorf("plural %+v", m)
	}
	for _, k := range []string{"button|Open", "noun|Open", "|Later", "|Nested in a pipeline", "|one task"} {
		if _, ok := byID[k]; !ok {
			t.Errorf("missing %q in %v", k, msgs)
		}
	}
	if _, ok := byID["|In a comment"]; ok {
		t.Error("extracted a string from a comment")
	}
	if m := byID["|Nested in a pipeline"]; m.Refs[0] != "site/templates/page.html:2" {
		t.Errorf("line number %v", m.Refs)
	}

	var buf bytes.Buffer
	if err := WritePOT(&buf, "test", msgs); err != nil {
		t.Fatal(err)
	}
	// The output is a valid catalog with every message.
	f, err := i18n.ParsePO(&buf)
	if err != nil {
		t.Fatalf("written .pot does not parse: %v\n%s", err, buf.String())
	}
	if len(f.Entries) != len(msgs) {
		t.Errorf("round trip: %d entries, want %d", len(f.Entries), len(msgs))
	}
}

func TestConflictingPlural(t *testing.T) {
	c := NewCollector()
	err := c.Go("a.go", []byte(`package a
func f() { Tn("x", "xs", 1); Tn("x", "xes", 2) }`))
	if err == nil || !strings.Contains(err.Error(), "different plural") {
		t.Fatalf("got %v", err)
	}
}
