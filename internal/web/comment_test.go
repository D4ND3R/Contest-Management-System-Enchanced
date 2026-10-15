// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package web

import (
	"strings"
	"testing"
	"testing/fstest"
)

// TestHTMLComment: html/template drops the comments of the template text;
// {{comment}} writes them as CMS's pages have them.
func TestHTMLComment(t *testing.T) {
	e, err := NewEngine(EngineOptions{Templates: fstest.MapFS{
		"p.html": {Data: []byte("<div>\n<!-- dropped -->\n    {{comment \" kept \"}}\n</div>\n")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := e.Execute(&b, "p.html", nil, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := b.String(), "<div>\n\n    <!-- kept -->\n</div>"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := HTMLComment("a --> b"); got != "<!--a - -> b-->" {
		t.Errorf("HTMLComment did not neutralize the end: %q", got)
	}
}
