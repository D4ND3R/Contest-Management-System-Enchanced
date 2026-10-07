// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package language

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// scope tells which placeholders an argument may use.
type scope int

const (
	scopeCompile    scope = iota // compilation commands
	scopeEachSource              // compilation commands with for_each
	scopeEvaluate                // evaluation commands
)

// Placeholder names.
const (
	phExecutable   = "executable"
	phSources      = "sources"
	phMainSource   = "main_source"
	phOtherSources = "other_sources"
	phSource       = "source"
	phMain         = "main"
	phArgs         = "args"
)

// condPrefix starts the {eval:TEXT} conditional argument.
const condPrefix = "{eval:"

func (s scope) scalar(name string) bool {
	switch name {
	case phExecutable:
		return true
	case phMainSource:
		return s == scopeCompile || s == scopeEachSource
	case phSource:
		return s == scopeEachSource
	case phMain:
		return s == scopeEvaluate
	}
	return false
}

func (s scope) list(name string) bool {
	switch name {
	case phSources, phOtherSources:
		return s == scopeCompile || s == scopeEachSource
	case phArgs:
		return s == scopeEvaluate
	}
	return false
}

// modifiers are the transformations a placeholder can apply ("{x|stem}").
var modifiers = map[string]func(string) string{
	"base":       path.Base,
	"stem":       stem,
	"capitalize": capitalize,
	"quote":      shellQuote,
}

// part is a literal (name == "") or a placeholder of an argument.
type part struct {
	lit  string
	name string
	mods []string
}

// argTemplate is a parsed argument of a command.
type argTemplate struct {
	// cond is set for {eval:TEXT}: the argument is condText or nothing.
	cond     bool
	condText string
	parts    []part
	// list is the index in parts of the list placeholder, or -1.
	list int
}

// values are what the placeholders of a command expand to.
type values struct {
	scalars       map[string]string
	lists         map[string][]string
	forEvaluation bool
}

// parseArg parses an argument of a command of the given scope.
func parseArg(raw string, sc scope) (argTemplate, error) {
	t := argTemplate{list: -1}
	if strings.ContainsRune(raw, 0) {
		return t, errors.New("NUL in an argument")
	}
	if strings.HasPrefix(raw, condPrefix) {
		if sc == scopeEvaluate {
			return t, fmt.Errorf("%q: {eval:...} is only for compilation commands", raw)
		}
		text, ok := strings.CutSuffix(raw[len(condPrefix):], "}")
		if !ok || text == "" || strings.ContainsAny(text, "{}") {
			return t, fmt.Errorf("%q: {eval:TEXT} must be a whole argument with a non-empty TEXT without braces", raw)
		}
		t.cond, t.condText = true, text
		return t, nil
	}
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			t.parts = append(t.parts, part{lit: lit.String()})
			lit.Reset()
		}
	}
	placeholders := 0
	for i := 0; i < len(raw); {
		switch {
		case strings.HasPrefix(raw[i:], "{{"):
			lit.WriteByte('{')
			i += 2
		case strings.HasPrefix(raw[i:], "}}"):
			lit.WriteByte('}')
			i += 2
		case raw[i] == '}':
			return t, fmt.Errorf("%q: unbalanced \"}\" (write \"}}\" for a literal brace)", raw)
		case raw[i] == '{':
			end := strings.IndexByte(raw[i:], '}')
			if end < 0 {
				return t, fmt.Errorf("%q: unclosed \"{\" (write \"{{\" for a literal brace)", raw)
			}
			inner := raw[i+1 : i+end]
			fields := strings.Split(inner, "|")
			p := part{name: fields[0], mods: fields[1:]}
			switch {
			case strings.HasPrefix(inner, "eval:"):
				return t, fmt.Errorf("%q: {eval:TEXT} must be the whole argument", raw)
			case sc.list(p.name):
				if t.list >= 0 {
					return t, fmt.Errorf("%q: more than one list placeholder", raw)
				}
				flush()
				t.list = len(t.parts)
			case sc.scalar(p.name):
				flush()
			default:
				return t, fmt.Errorf("%q: unknown placeholder {%s} here", raw, p.name)
			}
			for _, m := range p.mods {
				if _, ok := modifiers[m]; !ok {
					return t, fmt.Errorf("%q: unknown modifier %q (base, stem, capitalize, quote)", raw, m)
				}
			}
			t.parts = append(t.parts, p)
			placeholders++
			i += end + 1
		default:
			lit.WriteByte(raw[i])
			i++
		}
	}
	flush()
	if t.list >= 0 && placeholders > 1 {
		return t, fmt.Errorf("%q: a list placeholder must be the only placeholder of its argument", raw)
	}
	return t, nil
}

// expand appends the arguments t stands for to dst.
func (t *argTemplate) expand(dst []string, v *values) []string {
	if t.cond {
		if v.forEvaluation {
			dst = append(dst, t.condText)
		}
		return dst
	}
	if t.list < 0 {
		var sb strings.Builder
		for _, p := range t.parts {
			if p.name == "" {
				sb.WriteString(p.lit)
			} else {
				sb.WriteString(applyMods(v.scalars[p.name], p.mods))
			}
		}
		return append(dst, sb.String())
	}
	// Only literals around the list placeholder.
	var prefix, suffix strings.Builder
	for i, p := range t.parts {
		switch {
		case i < t.list:
			prefix.WriteString(p.lit)
		case i > t.list:
			suffix.WriteString(p.lit)
		}
	}
	lp := t.parts[t.list]
	for _, item := range v.lists[lp.name] {
		dst = append(dst, prefix.String()+applyMods(item, lp.mods)+suffix.String())
	}
	return dst
}

func applyMods(s string, mods []string) string {
	for _, m := range mods {
		s = modifiers[m](s)
	}
	return s
}

// stem is the base name without its extension, like
// os.path.splitext(os.path.basename(p))[0] (leading dots do not start an
// extension: ".bashrc" has none).
func stem(p string) string {
	b := path.Base(p)
	dot := strings.LastIndexByte(b, '.')
	if dot <= 0 || strings.Trim(b[:dot], ".") == "" {
		return b
	}
	return b[:dot]
}

// capitalize puts the first letter of the base name in upper case, keeping
// the directories (upstream HaskellGhc._capitalize).
func capitalize(p string) string {
	dir, base := path.Split(p)
	r, n := utf8.DecodeRuneInString(base)
	if n == 0 {
		return p
	}
	return dir + string(unicode.ToUpper(r)) + base[n:]
}

// unsafeShell matches what Python's shlex.quote considers unsafe.
var unsafeShell = regexp.MustCompile(`[^A-Za-z0-9_@%+=:,./-]`)

// shellQuote quotes s for a POSIX shell exactly like Python's shlex.quote.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !unsafeShell.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}
