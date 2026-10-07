// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/grading/languagemanager.py of CMS:
// Copyright © 2016-2017 Stefano Maggiolo <s.maggiolo@gmail.com>

package language

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	configfiles "github.com/d4nd3r/contest-management-system-enchanced/config"
)

// Registry is a set of languages, looked up by name or by file extension
// (upstream languagemanager). It is immutable: safe for concurrent use.
type Registry struct {
	langs  []*Language // sorted by name
	byName map[string]*Language
}

// ErrUnknown is returned (wrapped) for a name that is not a language.
var ErrUnknown = errors.New("language not supported")

// InvalidExtension replaces ".%l" in the file names of submissions whose
// language is unknown (upstream safe_get_lang_filename).
const InvalidExtension = ".invalid_language"

// New builds a registry; names must be unique.
func New(langs ...*Language) (*Registry, error) {
	r := &Registry{byName: make(map[string]*Language, len(langs))}
	for _, l := range langs {
		if l.digest == "" {
			return nil, fmt.Errorf("language %q was not loaded with Parse", l.Name)
		}
		if prev, ok := r.byName[l.Name]; ok {
			return nil, fmt.Errorf("language %q is defined twice (%s and %s)", l.Name, prev.File, l.File)
		}
		r.byName[l.Name] = l
		r.langs = append(r.langs, l)
	}
	sort.Slice(r.langs, func(i, j int) bool { return r.langs[i].Name < r.langs[j].Name })
	return r, nil
}

var builtin = sync.OnceValues(func() (*Registry, error) {
	langs, err := loadFS(configfiles.Languages(), "config/languages")
	if err != nil {
		return nil, err
	}
	return New(langs...)
})

// Builtin returns the languages shipped with CMS (config/languages). It
// panics if they are invalid, which the tests of this package rule out.
func Builtin() *Registry {
	r, err := builtin()
	if err != nil {
		panic("built-in languages: " + err.Error())
	}
	return r
}

// Load returns the built-in languages plus those of dir (*.yaml and
// *.yml files; cms.yaml languages_dir), which replace the built-in
// language with the same name. An empty dir means the built-in ones only;
// a dir that does not exist is an error.
func Load(dir string) (*Registry, error) {
	base, err := builtin()
	if err != nil {
		return nil, fmt.Errorf("built-in languages: %w", err)
	}
	if dir == "" {
		return base, nil
	}
	st, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("languages_dir: %w", err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("languages_dir: %s is not a directory", dir)
	}
	extra, err := loadFS(os.DirFS(dir), dir)
	if err != nil {
		return nil, err
	}
	return base.With(extra...)
}

// With returns a registry with langs added, replacing the languages with
// the same names. Names in langs must be unique.
func (r *Registry) With(langs ...*Language) (*Registry, error) {
	seen := make(map[string]*Language, len(langs))
	for _, l := range langs {
		if prev, ok := seen[l.Name]; ok {
			return nil, fmt.Errorf("language %q is defined twice (%s and %s)", l.Name, prev.File, l.File)
		}
		seen[l.Name] = l
	}
	all := slices.Clone(langs)
	for _, l := range r.langs {
		if _, ok := seen[l.Name]; !ok {
			all = append(all, l)
		}
	}
	return New(all...)
}

// loadFS parses every *.yaml and *.yml file at the root of fsys; prefix
// names the directory in messages.
func loadFS(fsys fs.FS, prefix string) ([]*Language, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", prefix, err)
	}
	var langs []*Language
	var errs []error
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") || (path.Ext(name) != ".yaml" && path.Ext(name) != ".yml") {
			continue
		}
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		l, err := Parse(data, filepath.Join(prefix, name))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		langs = append(langs, l)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return langs, nil
}

// Get returns the language with the given name (upstream get_language).
func (r *Registry) Get(name string) (*Language, error) {
	if l, ok := r.byName[name]; ok {
		return l, nil
	}
	return nil, fmt.Errorf("language `%s': %w", name, ErrUnknown)
}

// Lookup returns the language with the given name, if any.
func (r *Registry) Lookup(name string) (*Language, bool) {
	l, ok := r.byName[name]
	return l, ok
}

// All returns the languages sorted by name. The slice is shared: do not
// modify it.
func (r *Registry) All() []*Language { return r.langs }

// Names returns the names of the languages, sorted.
func (r *Registry) Names() []string {
	out := make([]string, len(r.langs))
	for i, l := range r.langs {
		out[i] = l.Name
	}
	return out
}

// FilenameToLanguage returns the language of a source file from its
// extension: among the languages named in allowed (all of them when
// allowed is nil) whose source extensions include it, the one with the
// smallest name, so the choice is arbitrary but deterministic (upstream
// filename_to_language). nil if none matches.
func (r *Registry) FilenameToLanguage(filename string, allowed []string) *Language {
	dot := strings.LastIndexByte(filename, '.')
	if dot < 0 {
		return nil
	}
	ext := filename[dot:]
	for _, l := range r.langs { // sorted by name
		if allowed != nil && !slices.Contains(allowed, l.Name) {
			continue
		}
		if slices.Contains(l.SourceExtensions, ext) {
			return l
		}
	}
	return nil
}

// LangFilename replaces ".%l" in a file name template with the source
// extension of the language (upstream safe_get_lang_filename): unchanged
// for an empty language, InvalidExtension for an unknown one.
func (r *Registry) LangFilename(lang, filename string) string {
	if lang == "" {
		return filename
	}
	ext := InvalidExtension
	if l, ok := r.byName[lang]; ok {
		ext = l.SourceExtension()
	}
	return strings.ReplaceAll(filename, ".%l", ext)
}

// RequiresMultithreading reports whether any of the named languages
// requires multithreading: then every program of the contest may use
// many threads (job.Job.MultithreadedSandbox). Unknown names are ignored.
func (r *Registry) RequiresMultithreading(names []string) bool {
	for _, n := range names {
		if l, ok := r.byName[n]; ok && l.RequiresMultithreading {
			return true
		}
	}
	return false
}

// SourceExtensions, HeaderExtensions and ObjectExtensions return the
// extensions of all the languages, sorted and without duplicates
// (upstream SOURCE_EXTS, HEADER_EXTS, OBJECT_EXTS).
func (r *Registry) SourceExtensions() []string {
	return r.collect(func(l *Language) []string { return l.SourceExtensions })
}

// HeaderExtensions: see SourceExtensions.
func (r *Registry) HeaderExtensions() []string {
	return r.collect(func(l *Language) []string { return l.HeaderExtensions })
}

// ObjectExtensions: see SourceExtensions.
func (r *Registry) ObjectExtensions() []string {
	return r.collect(func(l *Language) []string { return l.ObjectExtensions })
}

func (r *Registry) collect(f func(*Language) []string) []string {
	var out []string
	for _, l := range r.langs {
		out = append(out, f(l)...)
	}
	slices.Sort(out)
	return slices.Compact(out)
}
