// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package web

import (
	"encoding/json"
	"os"
	"testing"
)

// TestMimeGolden compares with the outputs of CMS's own functions
// (get_type_for_file_name, get_icon_for_type, translate_mimetype) over
// shared-mime-info, recorded by mimedata/extract.py: a list of file names
// plus one per known extension and literal name.
func TestMimeGolden(t *testing.T) {
	b, err := os.ReadFile("testdata/mime_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Name     *string           `json:"name"`
		Type     *string           `json:"type"`
		Icon     string            `json:"icon"`
		Comments map[string]string `json:"comments"`
	}
	if err := json.Unmarshal(b, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) < 1000 {
		t.Fatalf("only %d rows", len(rows))
	}
	for _, r := range rows {
		typ := ""
		if r.Type != nil {
			typ = *r.Type
		}
		if r.Name != nil {
			if got := MimeType(*r.Name); got != typ {
				t.Errorf("MimeType(%q) = %q, want %q", *r.Name, got, typ)
			}
		}
		if typ == "" {
			continue
		}
		if got := MimeIcon(typ); got != r.Icon {
			t.Errorf("MimeIcon(%q) = %q, want %q", typ, got, r.Icon)
		}
		for lang, want := range r.Comments {
			if got := MimeComment(typ, lang); got != want {
				t.Errorf("MimeComment(%q, %q) = %q, want %q", typ, lang, got, want)
			}
		}
	}
}

func TestMimeIconsExist(t *testing.T) {
	// The contestant pages show static/img/mimetypes/<icon>.png; CMS ships
	// icons for the usual types of statements and attachments.
	for name, icon := range map[string]string{
		"a.pdf": "x-office-document", "a.zip": "package-x-generic", "a.txt": "text-x-generic",
		"a.png": "image-x-generic", "a.cpp": "text-x-generic", "a.odt": "x-office-document",
	} {
		if got := MimeIcon(MimeType(name)); got != icon {
			t.Errorf("%s: %q, want %q", name, got, icon)
		}
	}
}
