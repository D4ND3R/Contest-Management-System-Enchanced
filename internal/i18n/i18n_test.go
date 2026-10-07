// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package i18n

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestPluralForms(t *testing.T) {
	cases := []struct {
		header string
		want   map[int64]int
	}{
		{"nplurals=1; plural=0;", map[int64]int{0: 0, 1: 0, 7: 0}},
		{"nplurals=2; plural=(n != 1);", map[int64]int{0: 1, 1: 0, 2: 1}},
		{"nplurals=2; plural=n > 1;", map[int64]int{0: 0, 1: 0, 2: 1}},
		// Russian/Ukrainian (upstream ru.po).
		{"nplurals=3; plural=(n%10==1 && n%100!=11 ? 0 : n%10>=2 && n%10<=4 && (n%100<10 || n%100>=20) ? 1 : 2);",
			map[int64]int{1: 0, 2: 1, 5: 2, 11: 2, 21: 0, 22: 1, 112: 2}},
		// Arabic (upstream ar.po).
		{"nplurals=6; plural=n==0 ? 0 : n==1 ? 1 : n==2 ? 2 : n%100>=3 && n%100<=10 ? 3 : n%100>=11 ? 4 : 5;",
			map[int64]int{0: 0, 1: 1, 2: 2, 3: 3, 11: 4, 100: 5, 102: 5}},
		// Slovenian (upstream sl.po).
		{"nplurals=4; plural=(n%100==1 ? 1 : n%100==2 ? 2 : n%100==3 || n%100==4 ? 3 : 0);",
			map[int64]int{1: 1, 2: 2, 3: 3, 5: 0, 101: 1}},
		{"nplurals=2; plural=!(n == 1);", map[int64]int{1: 0, 3: 1}},
	}
	for _, c := range cases {
		_, fn, err := ParsePluralForms(c.header)
		if err != nil {
			t.Errorf("%s: %v", c.header, err)
			continue
		}
		for n, want := range c.want {
			if got := fn(n); got != want {
				t.Errorf("%s: n=%d got %d want %d", c.header, n, got, want)
			}
		}
	}
	for _, bad := range []string{"nplurals=2;", "plural=n;", "nplurals=2; plural=(n != 1;", "nplurals=2; plural=n ? 1;"} {
		if _, _, err := ParsePluralForms(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

const samplePO = `# A comment
msgid ""
msgstr ""
"Language: xx\n"
"Plural-Forms: nplurals=2; plural=(n != 1);\n"

#: cms/server/contest/templates/overview.html:10
msgid "Correct"
msgstr "Bien"

msgid "Line one\n"
"line two \"quoted\""
msgstr ""
"Linea uno\n"
"linea dos"

#, fuzzy
msgid "Unsure"
msgstr "Dudoso"

msgctxt "verb"
msgid "Submit"
msgstr "Enviar"

#, python-format
msgid "%(n)d file"
msgid_plural "%(n)d files"
msgstr[0] "%(n)d archivo"
msgstr[1] "%(n)d archivos"

msgid "Untranslated"
msgstr ""

#~ msgid "Obsolete"
#~ msgstr "Viejo"
`

func TestParseAndCatalog(t *testing.T) {
	f, err := ParsePO(strings.NewReader(samplePO))
	if err != nil {
		t.Fatal(err)
	}
	if f.Header["Language"] != "xx" {
		t.Errorf("header %v", f.Header)
	}
	if len(f.Entries) != 6 {
		t.Fatalf("got %d entries: %+v", len(f.Entries), f.Entries)
	}
	if f.Entries[0].Refs[0] != "cms/server/contest/templates/overview.html:10" {
		t.Errorf("refs %v", f.Entries[0].Refs)
	}
	if !f.Entries[2].Fuzzy || f.Entries[3].Fuzzy {
		t.Error("fuzzy flag misassigned")
	}
	c, err := NewCatalog("xx", f)
	if err != nil {
		t.Fatal(err)
	}
	checks := [][2]string{
		{c.T("Correct"), "Bien"},
		{c.T("Line one\nline two \"quoted\""), "Linea uno\nlinea dos"},
		{c.T("Unsure"), "Unsure"}, // fuzzy ignored
		{c.T("Untranslated"), "Untranslated"},
		{c.T("Obsolete"), "Obsolete"},
		{c.Tc("verb", "Submit"), "Enviar"},
		{c.T("Submit"), "Submit"}, // context matters
		{c.Tn("%(n)d file", "%(n)d files", 1), "%(n)d archivo"},
		{c.Tn("%(n)d file", "%(n)d files", 3), "%(n)d archivos"},
		{c.Tn("missing", "missings", 1), "missing"},
		{c.Tn("missing", "missings", 2), "missings"},
		{(*Catalog)(nil).T("Correct"), "Correct"},
		{(*Catalog)(nil).Tn("a", "b", 0), "b"},
		{(*Catalog)(nil).Tc("verb", "Submit"), "Submit"},
	}
	for i, c := range checks {
		if got, want := c[0], c[1]; got != want {
			t.Errorf("case %d: got %q want %q", i, got, want)
		}
	}
}

func TestFormat(t *testing.T) {
	args := map[string]any{"name": "Ana", "n": 3, "x": 30.0, "y": 0.1 + 0.2, "s": 1.23456}
	cases := map[string]string{
		"Hello %(name)s":         "Hello Ana",
		"%(n)d tokens":           "3 tokens",
		"every %(x)g minutes":    "every 30 minutes",
		"%(y)g":                  "0.3",
		"%(s).2f s":              "1.23 s",
		"100%% sure":             "100% sure",
		"%(missing)s stays":      "%(missing)s stays",
		"no placeholders":        "no placeholders",
		"broken %(name":          "broken %(name",
		"trailing %":             "trailing %",
		"%(name)s and %(name)s!": "Ana and Ana!",
	}
	for in, want := range cases {
		if got := Format(in, args); got != want {
			t.Errorf("Format(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBundle(t *testing.T) {
	po := func(lang, correct string) *fstest.MapFile {
		return &fstest.MapFile{Data: []byte("msgid \"\"\nmsgstr \"\"\n\"Language: " + lang + "\\n\"\n\nmsgid \"Correct\"\nmsgstr \"" + correct + "\"\n")}
	}
	fsys := fstest.MapFS{
		"es/LC_MESSAGES/cms.po":    po("es", "Correcto"),
		"es_CL/LC_MESSAGES/cms.po": po("es_CL", "Correcto po"),
		"it/LC_MESSAGES/cms.po":    po("it", "Corretto"),
		"it/LC_MESSAGES/admin.po":  po("it", "x"),
	}
	b, err := LoadBundle(fsys, "cms")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(b.Languages(), ","); got != "en,es,es_CL,it" {
		t.Errorf("languages %s", got)
	}
	if b.Catalog("es-cl").T("Correct") != "Correcto po" || b.Catalog("es-MX").T("Correct") != "Correcto" ||
		b.Catalog("fr").T("Correct") != "Correct" {
		t.Error("catalog fallback")
	}
	match := map[string]string{
		"es-CL,es;q=0.9,en;q=0.8": "es_CL",
		"es-MX,en;q=0.5":          "es",
		"fr, it;q=0.3":            "it",
		"de":                      "en",
		"":                        "en",
		"en;q=0.1, it;q=0.9":      "it",
	}
	for h, want := range match {
		if got := b.Match(h, nil); got != want {
			t.Errorf("Match(%q) = %s, want %s", h, got, want)
		}
	}
	if got := b.Match("it", []string{"es"}); got != "es" && got != "en" {
		t.Errorf("allowed list ignored: %s", got)
	}
	if got := b.Match("es", []string{"es_CL"}); got != "es_CL" {
		t.Errorf("regional fallback: %s", got)
	}
}

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{"es-cl": "es_CL", "ZH_tw": "zh_TW", "IT": "it", " pt-br ": "pt_BR"} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q)=%q want %q", in, got, want)
		}
	}
}
