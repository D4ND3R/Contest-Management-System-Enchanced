// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cmstestsuite/unit_tests/server/contest/submission/
// file_matching_test.py of CMS:
// Copyright © 2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>

package submission

import (
	"maps"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
)

func makeLanguage(name string, exts ...string) *language.Language {
	return &language.Language{Name: name, SourceExtensions: exts}
}

var (
	cLang     = makeLanguage("C", ".c")
	cppLang   = makeLanguage("C++", ".cpp", ".cxx", ".cc")
	pascal    = makeLanguage("Pascal", "lib.pas")
	py2Lang   = makeLanguage("Py2", ".py")
	py3Lang   = makeLanguage("Py3", ".py")
	longOver  = makeLanguage("LongOverlap", ".suf.fix")
	shortOver = makeLanguage("ShortOverlap", ".fix")
	selfOver  = makeLanguage("SelfOverlap", ".suf.fix", ".fix")
)

// fakeLangs is a registry of languages for the tests.
type fakeLangs []*language.Language

func (f fakeLangs) Lookup(name string) (*language.Language, bool) {
	for _, l := range f {
		if l.Name == name {
			return l, true
		}
	}
	return nil, false
}

func (f fakeLangs) All() []*language.Language { return f }

const (
	fooContent  = "this is the content of a file"
	barContent  = "this is the content of another file"
	bazContent  = "this is the content of a third file"
	spamContent = "this is the content of a fourth file"
	hamContent  = "this is the content of a fifth file"
	eggsContent = "this is the content of a sixth file"
)

func rf(codename, filename, content string) ReceivedFile {
	return NewFile(codename, filename, []byte(content))
}

func ptr(s string) *string { return &s }

type matchCall struct {
	files   []ReceivedFile
	lang    *string
	format  []string
	allowed []string // nil with any: no limit
	any     bool
}

func runMatch(t *testing.T, langs fakeLangs, c matchCall) (map[string]string, *language.Language, error) {
	t.Helper()
	files, l, err := MatchFilesAndLanguage(c.files, c.lang, c.format, c.allowed, c.any, langs)
	if err != nil {
		return nil, nil, err
	}
	out := map[string]string{}
	for k, f := range files {
		b, err := f.Content()
		if err != nil {
			t.Fatal(err)
		}
		out[k] = string(b)
	}
	return out, l, nil
}

func wantMatch(t *testing.T, langs fakeLangs, c matchCall, want map[string]string, wantLang *language.Language) {
	t.Helper()
	got, l, err := runMatch(t, langs, c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !maps.Equal(got, want) {
		t.Errorf("files: got %v, want %v", got, want)
	}
	if l != wantLang {
		t.Errorf("language: got %v, want %v", l, wantLang)
	}
}

func wantInvalid(t *testing.T, langs fakeLangs, c matchCall) {
	t.Helper()
	_, _, err := runMatch(t, langs, c)
	if _, ok := err.(*InvalidFilesOrLanguage); !ok {
		t.Errorf("want InvalidFilesOrLanguage, got %v", err)
	}
}

func TestMatchSuccessLanguageRequired(t *testing.T) {
	langs := fakeLangs{cLang, cppLang}
	wantMatch(t, langs, matchCall{
		files: []ReceivedFile{rf("foo.%l", "my_name.cpp", fooContent), rf("bar.%l", "", barContent),
			rf("", "baz.cc", bazContent), rf("spam.txt", "my_other_name", spamContent),
			rf("eggs.zip", "", hamContent), rf("", "ham", eggsContent)},
		format: []string{"foo.%l", "bar.%l", "baz.%l", "spam.txt", "eggs.zip", "ham", "superfluous"},
		any:    true,
	}, map[string]string{"foo.%l": fooContent, "bar.%l": barContent, "baz.%l": bazContent,
		"spam.txt": spamContent, "eggs.zip": hamContent, "ham": eggsContent}, cppLang)
}

func TestMatchSuccessLanguageAgnostic(t *testing.T) {
	langs := fakeLangs{cLang, cppLang}
	wantMatch(t, langs, matchCall{
		files:  []ReceivedFile{rf("foo.txt", "my_name", fooContent), rf("bar.zip", "", barContent), rf("", "baz", bazContent)},
		format: []string{"foo.txt", "bar.zip", "baz", "superfluous"},
		any:    true,
	}, map[string]string{"foo.txt": fooContent, "bar.zip": barContent, "baz": bazContent}, nil)
}

func TestMatchLanguageAgnosticAlwaysPossible(t *testing.T) {
	langs := fakeLangs{cLang, cppLang}
	wantInvalid(t, langs, matchCall{files: []ReceivedFile{rf("foo.txt", "", fooContent)}, lang: ptr("C"),
		format: []string{"foo.txt", "bar.zip"}, any: true})
	wantMatch(t, langs, matchCall{files: []ReceivedFile{rf("foo.txt", "", fooContent)},
		format: []string{"foo.txt", "bar.zip"}, allowed: []string{"C++"}},
		map[string]string{"foo.txt": fooContent}, nil)
}

func TestMatchBadFile(t *testing.T) {
	langs := fakeLangs{cLang}
	wantInvalid(t, langs, matchCall{files: []ReceivedFile{rf("foo.%l", "", fooContent)}, lang: ptr("C"), format: []string{"bar.%l"}, any: true})
	wantInvalid(t, langs, matchCall{files: []ReceivedFile{rf("", "foo.c", fooContent)}, lang: ptr("C"), format: []string{"bar.%l"}, any: true})
	wantInvalid(t, langs, matchCall{files: []ReceivedFile{rf("foo.txt", "", fooContent)}, format: []string{"bar.txt"}, any: true})
	wantInvalid(t, langs, matchCall{files: []ReceivedFile{rf("", "foo.txt", fooContent)}, format: []string{"bar.txt"}, any: true})
}

func TestMatchBadExtension(t *testing.T) {
	wantInvalid(t, fakeLangs{cLang}, matchCall{files: []ReceivedFile{rf("foo.%l", "foo.cpp", fooContent)}, lang: ptr("C"), format: []string{"foo.%l"}, any: true})
}

func TestMatchExtensionWithoutLeadingPeriod(t *testing.T) {
	langs := fakeLangs{pascal}
	wantMatch(t, langs, matchCall{files: []ReceivedFile{rf("", "foolib.pas", fooContent)}, format: []string{"foo.%l"}, any: true},
		map[string]string{"foo.%l": fooContent}, pascal)
	wantInvalid(t, langs, matchCall{files: []ReceivedFile{rf("", "foo.lib.pas", fooContent)}, format: []string{"foo.%l"}, any: true})
	wantMatch(t, langs, matchCall{files: []ReceivedFile{rf("foo.%l", "foolib.pas", fooContent)}, format: []string{"foo.%l"}, any: true},
		map[string]string{"foo.%l": fooContent}, pascal)
}

func TestMatchDuplicateFiles(t *testing.T) {
	wantInvalid(t, fakeLangs{cLang}, matchCall{files: []ReceivedFile{rf("foo.%l", "bar.c", fooContent), rf("", "foo.c", barContent)},
		format: []string{"foo.%l"}, any: true})
}

func TestMatchAmbiguousFile(t *testing.T) {
	langs := fakeLangs{cLang, cppLang}
	wantInvalid(t, langs, matchCall{files: []ReceivedFile{rf("", "foo.c", fooContent)}, lang: ptr("C"), format: []string{"foo.%l", "foo.c"}, any: true})
	wantMatch(t, langs, matchCall{files: []ReceivedFile{rf("", "foo.c", fooContent)}, format: []string{"foo.%l", "foo.c"}, any: true},
		map[string]string{"foo.c": fooContent}, cppLang)
	wantInvalid(t, langs, matchCall{files: []ReceivedFile{rf("foo.%l", "bar.c", fooContent), rf("", "foo.c", fooContent)},
		lang: ptr("C"), format: []string{"foo.%l", "foo.c"}, any: true})
}

func TestMatchAmbiguousFile2(t *testing.T) {
	langs := fakeLangs{selfOver, longOver, shortOver}
	files := []ReceivedFile{rf("", "foo.suf.fix", fooContent)}
	wantInvalid(t, langs, matchCall{files: files, lang: ptr("SelfOverlap"), format: []string{"foo.%l", "foo.suf.%l"}, any: true})
	wantInvalid(t, langs, matchCall{files: files, format: []string{"foo.%l", "foo.suf.%l"}, any: true})
	wantInvalid(t, langs, matchCall{files: files, format: []string{"foo.%l"}, any: true})
}

func TestMatchForbiddenLanguage(t *testing.T) {
	langs := fakeLangs{cLang, cppLang}
	files := []ReceivedFile{rf("foo.%l", "foo.c", fooContent)}
	wantInvalid(t, langs, matchCall{files: files, format: []string{"foo.%l"}, allowed: []string{"C++", "Py2"}})
	wantInvalid(t, langs, matchCall{files: files, lang: ptr("C"), format: []string{"foo.%l"}, allowed: []string{"C++", "Py2"}})
}

func TestMatchMissingExtensions(t *testing.T) {
	langs := fakeLangs{cLang, cppLang}
	files := []ReceivedFile{rf("foo.%l", "", fooContent)}
	format := []string{"foo.%l"}
	wantInvalid(t, langs, matchCall{files: files, format: format, any: true})
	wantMatch(t, langs, matchCall{files: files, lang: ptr("C"), format: format, any: true}, map[string]string{"foo.%l": fooContent}, cLang)
	wantMatch(t, langs, matchCall{files: files, format: format, allowed: []string{"C++"}}, map[string]string{"foo.%l": fooContent}, cppLang)
}

func TestMatchAmbiguousExtensions(t *testing.T) {
	langs := fakeLangs{py2Lang, py3Lang}
	files := []ReceivedFile{rf("foo.%l", "foo.py", fooContent)}
	format := []string{"foo.%l"}
	wantInvalid(t, langs, matchCall{files: files, format: format, any: true})
	wantMatch(t, langs, matchCall{files: files, lang: ptr("Py2"), format: format, any: true}, map[string]string{"foo.%l": fooContent}, py2Lang)
	wantMatch(t, langs, matchCall{files: files, format: format, allowed: []string{"Py3"}}, map[string]string{"foo.%l": fooContent}, py3Lang)
}

func TestMatchOverlappingExtensions(t *testing.T) {
	langs := fakeLangs{longOver, shortOver}
	files := []ReceivedFile{rf("", "foo.suf.fix", fooContent)}
	format := []string{"foo.%l", "foo.suf.%l"}
	wantInvalid(t, langs, matchCall{files: files, format: format, any: true})
	wantMatch(t, langs, matchCall{files: files, lang: ptr("LongOverlap"), format: format, any: true}, map[string]string{"foo.%l": fooContent}, longOver)
	wantMatch(t, langs, matchCall{files: files, format: format, allowed: []string{"ShortOverlap"}}, map[string]string{"foo.suf.%l": fooContent}, shortOver)
}

func TestMatchNeitherCodenameNorFilename(t *testing.T) {
	langs := fakeLangs{cLang}
	wantInvalid(t, langs, matchCall{files: []ReceivedFile{rf("", "", fooContent)}, lang: ptr("C"), format: []string{"foo.%l"}, any: true})
	wantInvalid(t, langs, matchCall{files: []ReceivedFile{rf("", "", fooContent)}, format: []string{"foo.txt"}, any: true})
}

func TestMatchNonexistingGivenLanguages(t *testing.T) {
	wantInvalid(t, fakeLangs{cLang, cppLang}, matchCall{files: []ReceivedFile{rf("foo.%l", "foo.c", fooContent)},
		lang: ptr("BadLang"), format: []string{"foo.%l"}, any: true})
}

func TestMatchNonexistingAllowedLanguages(t *testing.T) {
	langs := fakeLangs{cLang, cppLang}
	files := []ReceivedFile{rf("foo.%l", "foo.c", fooContent)}
	wantMatch(t, langs, matchCall{files: files, format: []string{"foo.%l"}, allowed: []string{"C", "BadLang"}},
		map[string]string{"foo.%l": fooContent}, cLang)
	wantMatch(t, langs, matchCall{files: files, lang: ptr("C"), format: []string{"foo.%l"}, allowed: []string{"C", "BadLang"}},
		map[string]string{"foo.%l": fooContent}, cLang)
}

func TestMatchGivenFilesEmpty(t *testing.T) {
	langs := fakeLangs{cLang, cppLang}
	wantInvalid(t, langs, matchCall{format: []string{"foo.%l"}, any: true})
	wantInvalid(t, langs, matchCall{lang: ptr("C"), format: []string{"foo.%l"}, any: true})
	wantInvalid(t, langs, matchCall{format: []string{"foo.txt"}, any: true})
}

func TestMatchSubmissionFormatEmpty(t *testing.T) {
	langs := fakeLangs{cLang, cppLang}
	wantInvalid(t, langs, matchCall{files: []ReceivedFile{rf("foo.%l", "foo.c", fooContent)}, lang: ptr("C"), any: true})
	wantInvalid(t, langs, matchCall{files: []ReceivedFile{rf("foo.txt", "foo.txt", fooContent)}, any: true})
	wantInvalid(t, langs, matchCall{any: true})
}

func TestMatchAllowedLanguagesEmpty(t *testing.T) {
	langs := fakeLangs{cLang}
	files := []ReceivedFile{rf("foo.%l", "foo.c", fooContent)}
	wantInvalid(t, langs, matchCall{files: files, lang: ptr("C"), format: []string{"foo.%l"}, allowed: []string{}})
	wantInvalid(t, langs, matchCall{files: files, lang: ptr("C"), format: []string{"foo.%l"}, allowed: []string{"BadLang"}})
	wantInvalid(t, langs, matchCall{files: files, format: []string{"foo.%l"}, allowed: []string{}})
	wantInvalid(t, langs, matchCall{files: files, format: []string{"foo.%l"}, allowed: []string{"BadLang"}})
	txt := []ReceivedFile{rf("foo.txt", "foo.txt", fooContent)}
	wantMatch(t, langs, matchCall{files: txt, format: []string{"foo.txt"}, allowed: []string{}}, map[string]string{"foo.txt": fooContent}, nil)
	wantMatch(t, langs, matchCall{files: txt, format: []string{"foo.txt"}, allowed: []string{"BadLang"}}, map[string]string{"foo.txt": fooContent}, nil)
}

func TestPySplitextBase(t *testing.T) {
	for in, want := range map[string]string{
		"foo.%l": "foo", "foo.suf.%l": "foo.suf", ".%l": ".%l", "..%l": "..%l", "a/.%l": "a/.%l",
		"foo": "foo", "dir.x/foo": "dir.x/foo", "x..%l": "x.",
	} {
		if got := pySplitextBase(in); got != want {
			t.Errorf("pySplitextBase(%q) = %q, want %q", in, got, want)
		}
	}
}
