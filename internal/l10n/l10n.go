// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package l10n

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

//go:embed data/*.json
var dataFS embed.FS

// DefaultCode is the code of the source language (CMS's DEFAULT_TRANSLATION).
const DefaultCode = "en"

// Locale holds the CLDR data of one language. It is immutable and safe for
// concurrent use.
type Locale struct {
	code       string
	identifier string
	name       string

	datetimeFmt string // glue pattern ("{1}, {0}") without quotes, as Babel uses it
	dateFmt     []dtToken
	timeFmt     []dtToken

	months      map[string]map[string][]string // context -> width -> 12 names
	days        map[string][]string            // width -> 7 names from Monday (format context)
	periods     map[string]map[string]map[string]string
	periodRules []periodRule

	decimal  *numberPattern
	decSym   string
	groupSym string
	infSym   string

	plural pluralRules

	units     map[string]map[string]map[string]string // unit -> length -> plural form -> pattern
	unitNames map[string]map[string]string            // unit -> length -> name
	list      [4]string                               // start, middle, end, 2

	// Display names, aligned with the key lists of global.json.
	names [4][]string // languages, territories, scripts, variants
}

// Code returns the code of the catalog directory ("es_CL", "zh_TW").
func (l *Locale) Code() string { return l.code }

// Identifier returns CMS's identifier of the language, the value of
// <html lang> and of the language cookie ("es-CL", "zh-Hant-TW").
func (l *Locale) Identifier() string { return l.identifier }

// Name returns the name of the language in itself ("español (Chile)").
func (l *Locale) Name() string { return l.name }

type localeEntry struct {
	code       string
	identifier string
	once       sync.Once
	loc        *Locale
}

func (e *localeEntry) get() *Locale {
	// The data of a language is parsed on first use.
	e.once.Do(func() {
		loc, err := loadLocale(e.code)
		if err != nil {
			// The data is generated and covered by the tests.
			panic(fmt.Sprintf("l10n: data/%s.json: %v", e.code, err))
		}
		e.loc = loc
	})
	return e.loc
}

type registryData struct {
	codes   []string                // sorted
	entries map[string]*localeEntry // code -> entry
	exact   map[string]string       // code or identifier as is -> code
	keys    map[string]string       // normalized code or identifier -> code
}

var (
	registryOnce sync.Once
	registry     registryData
)

func loadRegistry() *registryData {
	registryOnce.Do(func() {
		raw, err := dataFS.ReadFile("data/locales.json")
		if err != nil {
			panic(fmt.Sprintf("l10n: %v", err))
		}
		var index map[string]string
		if err := json.Unmarshal(raw, &index); err != nil {
			panic(fmt.Sprintf("l10n: data/locales.json: %v", err))
		}
		registry.entries = map[string]*localeEntry{}
		registry.exact = map[string]string{}
		registry.keys = map[string]string{}
		for code, ident := range index {
			registry.codes = append(registry.codes, code)
			registry.entries[code] = &localeEntry{code: code, identifier: ident}
			registry.exact[code] = code
			registry.exact[ident] = code
			registry.keys[normKey(code)] = code
			registry.keys[normKey(ident)] = code
		}
		sort.Strings(registry.codes)
	})
	return &registry
}

func normKey(s string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), "-", "_"))
}

// Available returns the codes of every language, sorted.
func Available() []string {
	return append([]string(nil), loadRegistry().codes...)
}

// Get returns the locale for a language code or identifier ("es_CL",
// "es-CL", "zh-Hant-TW"), ignoring case. Other languages fall back to the
// locale with the same language and script, the script being the likely
// one when it is not given (zh, zh-SG -> zh_CN; zh-HK, zh-Hant -> zh_TW),
// then to the base language (es-MX -> es) and finally to English. Get
// never returns nil.
func Get(lang string) *Locale {
	r := loadRegistry()
	return r.entries[r.resolve(lang)].get()
}

func (r *registryData) resolve(lang string) string {
	if code, ok := r.exact[lang]; ok {
		return code
	}
	k := normKey(lang)
	if code, ok := r.keys[k]; ok {
		return code
	}
	p, ok := parseLocale(k, '_')
	if !ok {
		return DefaultCode
	}
	if p.terr != "" {
		if code, ok := r.keys[normKey(p.lang+"_"+p.terr)]; ok {
			return code
		}
	}
	script := p.script
	if script == "" {
		g := loadGlobal()
		likely, ok := g.LikelySubtags[localeParts{lang: p.lang, terr: p.terr}.id()]
		if !ok {
			likely, ok = g.LikelySubtags[p.lang]
		}
		if lp, valid := parseLocale(likely, '_'); ok && valid {
			script = lp.script
		}
	}
	if script != "" {
		prefix := normKey(p.lang + "_" + script + "_")
		for _, code := range r.codes {
			if strings.HasPrefix(normKey(r.entries[code].identifier), prefix) {
				return code
			}
		}
	}
	if code, ok := r.keys[p.lang]; ok {
		return code
	}
	for _, code := range r.codes {
		if strings.HasPrefix(normKey(code), p.lang+"_") {
			return code
		}
	}
	return DefaultCode
}

// localeJSON is the shape of data/<code>.json (tools/l10n/extract.py).
type localeJSON struct {
	Code           string                                  `json:"code"`
	Identifier     string                                  `json:"identifier"`
	Name           string                                  `json:"name"`
	DatetimeFormat string                                  `json:"datetimeFormat"`
	DateFormat     string                                  `json:"dateFormat"`
	TimeFormat     string                                  `json:"timeFormat"`
	Months         map[string]map[string][]string          `json:"months"`
	Days           map[string]map[string][]string          `json:"days"`
	Periods        map[string]map[string]map[string]string `json:"periods"`
	PeriodRules    [][2]json.RawMessage                    `json:"periodRules"`
	DecimalFormat  string                                  `json:"decimalFormat"`
	Symbols        struct {
		Decimal  string `json:"decimal"`
		Group    string `json:"group"`
		Infinity string `json:"infinity"`
	} `json:"symbols"`
	Plural      [][2]string                             `json:"plural"`
	Units       map[string]map[string]map[string]string `json:"units"`
	UnitNames   map[string]map[string]string            `json:"unitNames"`
	List        map[string]string                       `json:"list"`
	Languages   []*string                               `json:"languages"`
	Territories []*string                               `json:"territories"`
	Scripts     []*string                               `json:"scripts"`
	Variants    []*string                               `json:"variants"`
}

func loadLocale(code string) (*Locale, error) {
	raw, err := dataFS.ReadFile("data/" + code + ".json")
	if err != nil {
		return nil, err
	}
	var d localeJSON
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, err
	}
	l := &Locale{
		code:        d.Code,
		identifier:  d.Identifier,
		name:        d.Name,
		datetimeFmt: strings.ReplaceAll(d.DatetimeFormat, "'", ""),
		months:      d.Months,
		days:        d.Days["format"],
		periods:     d.Periods,
		decSym:      d.Symbols.Decimal,
		groupSym:    d.Symbols.Group,
		infSym:      d.Symbols.Infinity,
		units:       d.Units,
		unitNames:   d.UnitNames,
		list:        [4]string{d.List["start"], d.List["middle"], d.List["end"], d.List["2"]},
	}
	if l.dateFmt, err = compileDatePattern(d.DateFormat); err != nil {
		return nil, err
	}
	if l.timeFmt, err = compileDatePattern(d.TimeFormat); err != nil {
		return nil, err
	}
	for _, pr := range d.PeriodRules {
		var r periodRule
		var conds []map[string]int
		if err := json.Unmarshal(pr[0], &r.id); err != nil {
			return nil, fmt.Errorf("periodRules: %w", err)
		}
		if err := json.Unmarshal(pr[1], &conds); err != nil {
			return nil, fmt.Errorf("periodRules: %w", err)
		}
		for _, c := range conds {
			r.conds = append(r.conds, newPeriodCond(c))
		}
		l.periodRules = append(l.periodRules, r)
	}
	if l.decimal, err = parseNumberPattern(d.DecimalFormat); err != nil {
		return nil, err
	}
	for _, p := range d.Plural {
		rule, err := parsePluralRule(p[1])
		if err != nil {
			return nil, fmt.Errorf("plural %s %q: %w", p[0], p[1], err)
		}
		l.plural = append(l.plural, pluralRule{tag: p[0], cond: rule})
	}
	for i, list := range [][]*string{d.Languages, d.Territories, d.Scripts, d.Variants} {
		names := make([]string, len(list))
		for j, s := range list {
			if s != nil {
				names[j] = *s
			}
		}
		l.names[i] = names
	}
	return l, nil
}
