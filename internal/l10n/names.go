// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Locale display names, ported from format_locale of cms/locale/locale.py
// (CMS):
// Copyright © 2010-2014 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2015 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2012-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2013 Bernard Blackham <bernard@largestprime.net>
// Copyright © 2014 Artem Iglikov <artem.iglikov@gmail.com>
// Copyright © 2014 Fabian Gundlach <320pointsguy@gmail.com>
// Copyright © 2015 William Di Luigi <williamdiluigi@gmail.com>
// Copyright © 2016 Myungwoo Chun <mc.tamaki@gmail.com>
// Copyright © 2018 Edoardo Morassutto <edoardo.morassutto@gmail.com>
//
// on top of a port of parse_locale, Locale.parse (with its likely-subtag
// fallback) and Locale.get_display_name of babel/core.py (Babel 2.12.1,
// BSD-3-Clause, Copyright (c) 2013-2023 by the Babel Team).

package l10n

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// FormatLocale names a locale identifier in this language: "Italian" for
// "it", "Spanish (Chile)" for "es_CL", "Chinese (Simplified, China)" for
// "zh_CN", like CMS's format_locale. The separator is "_" ("es-CL" is not
// parsed); codes Babel cannot parse or has no data for are returned as
// they are. Where CMS would print None (Babel has data but no name, as for
// "root"), the code is returned too.
func (l *Locale) FormatLocale(code string) string {
	p, ok := parseLocale(code, '_')
	if !ok {
		return code
	}
	g := loadGlobal()
	p, ok = g.resolve(p)
	if !ok {
		return code
	}
	if name := l.displayName(p); name != "" {
		return name
	}
	return code
}

// localeParts are the components of a locale identifier.
type localeParts struct {
	lang, terr, script, variant, modifier string
}

// id is babel.core.get_locale_identifier: lang_Script_TERR_VARIANT@mod.
func (p localeParts) id() string {
	s := p.lang
	for _, part := range [...]string{p.script, p.terr, p.variant} {
		if part != "" {
			if s != "" {
				s += "_"
			}
			s += part
		}
	}
	if p.modifier != "" {
		s += "@" + p.modifier
	}
	return s
}

// parseLocale is babel.core.parse_locale.
func parseLocale(identifier string, sep byte) (localeParts, bool) {
	var p localeParts
	identifier, p.modifier, _ = strings.Cut(identifier, "@")
	identifier, _, _ = strings.Cut(identifier, ".") // the charset
	parts := strings.Split(identifier, string(sep))
	// Python's lower() maps U+0130 to "i̇", whose combining dot is not
	// alphabetic: such a language never parses.
	if strings.ContainsRune(parts[0], 'İ') {
		return p, false
	}
	p.lang = strings.ToLower(parts[0])
	parts = parts[1:]
	if !isAlpha(p.lang) {
		return p, false
	}
	if len(parts) > 0 && utf8.RuneCountInString(parts[0]) == 4 && isAlpha(parts[0]) {
		p.script = titleCase(parts[0])
		parts = parts[1:]
	}
	if len(parts) > 0 {
		switch n := utf8.RuneCountInString(parts[0]); {
		case n == 2 && isAlpha(parts[0]):
			p.terr = strings.ToUpper(parts[0])
			parts = parts[1:]
		case n == 3 && isDigits(parts[0]):
			p.terr = parts[0]
			parts = parts[1:]
		}
	}
	if len(parts) > 0 {
		first, _ := utf8.DecodeRuneInString(parts[0])
		n := utf8.RuneCountInString(parts[0])
		if (n == 4 && unicode.IsDigit(first)) || (n >= 5 && unicode.IsLetter(first)) {
			// Babel pops the last part, which is the first one whenever
			// the identifier is valid.
			p.variant = strings.ToUpper(parts[len(parts)-1])
			parts = parts[:len(parts)-1]
		}
	}
	return p, len(parts) == 0
}

// isAlpha is Python's str.isalpha.
func isAlpha(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

func isDigits(s string) bool {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return s != ""
}

// titleCase is Python's str.title for a single word.
func titleCase(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToTitle(r)) + strings.ToLower(s[size:])
}

// globalData is data/global.json: the locales Babel has data for and the
// tables Locale.parse uses, plus the code lists the display names of each
// locale are aligned with.
type globalData struct {
	Babel            string            `json:"babel"`
	CLDR             string            `json:"cldr"`
	Locales          []string          `json:"locales"`
	LanguageAliases  map[string]string `json:"languageAliases"`
	TerritoryAliases map[string]string `json:"territoryAliases"`
	ScriptAliases    map[string]string `json:"scriptAliases"`
	VariantAliases   map[string]string `json:"variantAliases"`
	LikelySubtags    map[string]string `json:"likelySubtags"`
	Languages        []string          `json:"languages"`
	Territories      []string          `json:"territories"`
	Scripts          []string          `json:"scripts"`
	Variants         []string          `json:"variants"`

	exists map[string]bool   // lower-cased locale identifiers
	index  [4]map[string]int // languages, territories, scripts, variants
}

var (
	globalOnce sync.Once
	global     *globalData
)

func loadGlobal() *globalData {
	globalOnce.Do(func() {
		raw, err := dataFS.ReadFile("data/global.json")
		if err != nil {
			panic(fmt.Sprintf("l10n: %v", err))
		}
		g := &globalData{}
		if err := json.Unmarshal(raw, g); err != nil {
			panic(fmt.Sprintf("l10n: data/global.json: %v", err))
		}
		g.exists = make(map[string]bool, len(g.Locales))
		for _, id := range g.Locales {
			g.exists[strings.ToLower(id)] = true
		}
		for i, keys := range [...][]string{g.Languages, g.Territories, g.Scripts, g.Variants} {
			g.index[i] = make(map[string]int, len(keys))
			for j, k := range keys {
				g.index[i][k] = j
			}
		}
		global = g
	})
	return global
}

// load is the Locale constructor: it succeeds when Babel has data for
// the identifier without its modifier (localedata.exists, which ignores
// case).
func (g *globalData) load(p localeParts) (localeParts, bool) {
	mod := p.modifier
	p.modifier = ""
	ok := g.exists[strings.ToLower(p.id())]
	p.modifier = mod
	return p, ok
}

func (g *globalData) loadReducing(p localeParts) (localeParts, bool) {
	if q, ok := g.load(p); ok {
		return q, true
	}
	return g.load(localeParts{lang: p.lang, terr: p.terr})
}

// resolve is Locale.parse after parse_locale: the locale itself, else the
// likely subtags of the (aliased) identifier, else those of its language
// with its territory.
func (g *globalData) resolve(p localeParts) (localeParts, bool) {
	if q, ok := g.load(p); ok {
		return q, true
	}
	if a, ok := g.LanguageAliases[p.lang]; ok {
		p.lang = a
	}
	if a, ok := g.TerritoryAliases[p.terr]; ok && p.terr != "" {
		p.terr = a
	}
	if a, ok := g.ScriptAliases[p.script]; ok && p.script != "" {
		p.script = a
	}
	if a, ok := g.VariantAliases[p.variant]; ok && p.variant != "" {
		p.variant = a
	}
	if p.terr == "ZZ" {
		p.terr = ""
	}
	if p.script == "Zzzz" {
		p.script = ""
	}
	if likely, ok := g.LikelySubtags[p.id()]; ok {
		if lp, ok := parseLocale(likely, '_'); ok {
			if q, ok := g.loadReducing(lp); ok {
				return q, true
			}
		}
	}
	if likely, ok := g.LikelySubtags[p.lang]; ok {
		if lp, ok := parseLocale(likely, '_'); ok {
			lp.terr = p.terr
			if q, ok := g.loadReducing(lp); ok {
				return q, true
			}
		}
	}
	return p, false
}

// name returns the display name of a code in this locale ("" if none):
// kind 0 languages, 1 territories, 2 scripts, 3 variants.
func (l *Locale) nameOf(kind int, code string) string {
	i, ok := loadGlobal().index[kind][code]
	if !ok || i >= len(l.names[kind]) {
		return ""
	}
	return l.names[kind][i]
}

// displayName is Locale.get_display_name(l). Like Babel, it adds the
// parentheses whenever the locale has a territory, script or variant,
// even if none of them has a name here.
func (l *Locale) displayName(p localeParts) string {
	name := l.nameOf(0, p.lang)
	if name == "" || (p.terr == "" && p.script == "" && p.variant == "") {
		return name
	}
	var details []string
	if p.script != "" {
		details = append(details, l.nameOf(2, p.script))
	}
	if p.terr != "" {
		details = append(details, l.nameOf(1, p.terr))
	}
	if p.variant != "" {
		details = append(details, l.nameOf(3, p.variant))
	}
	if p.modifier != "" {
		details = append(details, p.modifier)
	}
	kept := details[:0]
	for _, d := range details {
		if d != "" {
			kept = append(kept, d)
		}
	}
	return name + " (" + strings.Join(kept, ", ") + ")"
}
