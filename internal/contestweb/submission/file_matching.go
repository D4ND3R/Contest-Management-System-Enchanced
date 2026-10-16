// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/server/contest/submission/file_matching.py of CMS:
// Copyright © 2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>

package submission

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
)

// Languages finds the programming languages (*language.Registry
// implements it).
type Languages interface {
	Lookup(name string) (*language.Language, bool)
	All() []*language.Language
}

// InvalidFilesOrLanguage is returned when the files sent do not fit the
// submission format, or their language cannot be told (CMS
// InvalidFilesOrLanguage). Reason explains why, for the logs.
type InvalidFilesOrLanguage struct{ Reason string }

func (e *InvalidFilesOrLanguage) Error() string { return "submission: " + e.Reason }

func invalid(format string, args ...any) error {
	return &InvalidFilesOrLanguage{Reason: fmt.Sprintf(format, args...)}
}

// langSuffix is the placeholder of the source extension in a submission
// format ("foo.%l").
const langSuffix = ".%l"

// pySplitextBase is os.path.splitext(name)[0]: name without its last
// extension; leading dots do not start an extension (".%l" has none).
func pySplitextBase(name string) string {
	slash := strings.LastIndexByte(name, '/')
	dot := strings.LastIndexByte(name, '.')
	if dot <= slash+1 {
		return name
	}
	// Leading dots of the last component are not extensions.
	if strings.Trim(name[slash+1:dot], ".") == "" {
		return name
	}
	return name[:dot]
}

// matchFilename reports whether filename is the element of the format
// for the language (CMS _match_filename): equal, or with the trailing
// ".%l" replaced by a source extension of the language.
func matchFilename(filename string, lang *language.Language, element string) bool {
	if !strings.HasSuffix(element, langSuffix) {
		return filename == element
	}
	base := pySplitextBase(element)
	for _, ext := range lang.SourceExtensions {
		if filename == base+ext {
			return true
		}
	}
	return false
}

// matchExtension reports whether filename (if given) has a source
// extension of the language when element ends with ".%l" (CMS
// _match_extension).
func matchExtension(filename string, lang *language.Language, element string) bool {
	if filename == "" || !strings.HasSuffix(element, langSuffix) {
		return true
	}
	for _, ext := range lang.SourceExtensions {
		if strings.HasSuffix(filename, ext) {
			return true
		}
	}
	return false
}

// matchFile finds the element of the format a file is for (CMS
// _match_file): without codename, the only element its file name
// matches; with one, the codename itself if the file name's extension
// fits.
func matchFile(f *ReceivedFile, lang *language.Language, format []string) (string, error) {
	if f.Codename == "" && f.Filename == "" {
		return "", invalid("need at least one of codename and filename")
	}
	if f.Codename == "" {
		var found []string
		for _, e := range format {
			if (lang != nil || !strings.HasSuffix(e, langSuffix)) && matchFilename(f.Filename, lang, e) {
				found = append(found, e)
			}
		}
		if len(found) == 1 {
			return found[0], nil
		}
	} else if slices.Contains(format, f.Codename) &&
		(lang != nil || !strings.HasSuffix(f.Codename, langSuffix)) &&
		matchExtension(f.Filename, lang, f.Codename) {
		return f.Codename, nil
	}
	return "", invalid("file %q/%q doesn't unambiguously match the submission format %q", f.Codename, f.Filename, format)
}

// matchFiles fits the files into the format for a language (CMS
// _match_files).
func matchFiles(given []ReceivedFile, lang *language.Language, format []string) (map[string]*ReceivedFile, error) {
	files := make(map[string]*ReceivedFile, len(given))
	for i := range given {
		codename, err := matchFile(&given[i], lang, format)
		if err != nil {
			return nil, err
		}
		if _, dup := files[codename]; dup {
			return nil, invalid("two files match the same element %q of the submission format", codename)
		}
		files[codename] = &given[i]
	}
	return files, nil
}

// MatchFilesAndLanguage tells which element of the submission format each
// file is for and which language they are in (CMS
// match_files_and_language). language is the language the contestant
// chose (nil: guess it among the allowed ones); allowed are the
// languages of the task, unless anyLanguage (CMS None: no limit). A
// format without ".%l" elements has no language (the result is nil, and
// choosing one is an error). Exactly one language must fit all the files.
func MatchFilesAndLanguage(given []ReceivedFile, lang *string, format []string, allowed []string, anyLanguage bool, langs Languages) (map[string]*ReceivedFile, *language.Language, error) {
	if len(given) == 0 {
		return nil, nil, invalid("no files given")
	}
	// Python sets: duplicates in the format do not matter.
	format = uniq(format)

	var candidates []*language.Language
	needsLanguage := slices.ContainsFunc(format, func(e string) bool { return strings.HasSuffix(e, langSuffix) })
	switch {
	case !needsLanguage:
		if lang != nil {
			return nil, nil, invalid("a language %q is given when not needed", *lang)
		}
		candidates = []*language.Language{nil}
	case lang != nil:
		l, ok := langs.Lookup(*lang)
		if !ok {
			return nil, nil, invalid("the given language %q isn't a language", *lang)
		}
		if !anyLanguage && !slices.Contains(allowed, l.Name) {
			return nil, nil, invalid("the given language %q isn't allowed", *lang)
		}
		candidates = []*language.Language{l}
	case anyLanguage:
		candidates = langs.All()
	default:
		for _, name := range allowed {
			if l, ok := langs.Lookup(name); ok && !slices.Contains(candidates, l) {
				candidates = append(candidates, l)
			}
		}
	}

	var (
		matched     map[string]*ReceivedFile
		matchedLang *language.Language
		matches     []string
		reasons     []string
	)
	for _, l := range candidates {
		files, err := matchFiles(given, l, format)
		if err != nil {
			reasons = append(reasons, fmt.Sprintf("%s: %v", langName(l), err))
			continue
		}
		matched, matchedLang = files, l
		matches = append(matches, langName(l))
	}
	switch len(matches) {
	case 0:
		return nil, nil, invalid("there isn't any language that matches all the files:\n%s", strings.Join(reasons, ";\n"))
	case 1:
		return matched, matchedLang, nil
	}
	sort.Strings(matches)
	return nil, nil, invalid("there is more than one language that matches all the files: %q", matches)
}

func langName(l *language.Language) string {
	if l == nil {
		return "None"
	}
	return fmt.Sprintf("%q", l.Name)
}

func uniq(s []string) []string {
	out := make([]string, 0, len(s))
	for _, x := range s {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}
