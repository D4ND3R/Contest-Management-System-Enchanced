// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package i18n

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
)

// SourceLanguage is the language of the msgids.
const SourceLanguage = "en"

// Catalog holds the translations of one language for one domain. A nil
// *Catalog is valid and returns the source (English) text.
type Catalog struct {
	Lang     string
	msgs     map[string][]string
	nplurals int
	plural   PluralFloatFunc
}

func key(ctx, id string) string {
	if ctx == "" {
		return id
	}
	return ctx + "\x04" + id
}

// NewCatalog builds a catalog from a parsed .po file as Babel's
// compile_catalog builds the .mo files of CMS: fuzzy entries and empty
// translations are left out, so the English text is shown instead of a
// possibly wrong one; but plural entries stay even when some (or all) of
// their forms are empty, the first form being msgid and the others
// msgid_plural. That is why CMS, in a language whose first form is not
// "one" (Arabic: zero; Chinese, Japanese: every number), shows the
// singular English text of an untranslated plural message for other
// counts; the contestant site shows the same.
func NewCatalog(lang string, f *File) (*Catalog, error) {
	c := &Catalog{Lang: lang, msgs: map[string][]string{}, nplurals: 2,
		plural: func(n float64) int {
			if n != 1 {
				return 1
			}
			return 0
		}}
	if pf := f.Header["Plural-Forms"]; pf != "" {
		n, fn, err := ParsePluralFormsFloat(pf)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", lang, err)
		}
		c.nplurals, c.plural = n, fn
	}
	for _, e := range f.Entries {
		if e.Fuzzy || len(e.Str) == 0 {
			continue
		}
		if e.Plural == "" {
			if e.Str[0] != "" {
				c.msgs[key(e.Context, e.ID)] = e.Str
			}
			continue
		}
		forms := make([]string, len(e.Str))
		for i, s := range e.Str {
			switch {
			case s != "":
				forms[i] = s
			case i == 0:
				forms[i] = e.ID
			default:
				forms[i] = e.Plural
			}
		}
		c.msgs[key(e.Context, e.ID)] = forms
	}
	return c, nil
}

// T translates msgid.
func (c *Catalog) T(msgid string) string {
	if c == nil {
		return msgid
	}
	if s, ok := c.msgs[msgid]; ok {
		return s[0]
	}
	return msgid
}

// Tc translates msgid in a disambiguating context (msgctxt).
func (c *Catalog) Tc(ctx, msgid string) string {
	if c == nil {
		return msgid
	}
	if s, ok := c.msgs[key(ctx, msgid)]; ok {
		return s[0]
	}
	return msgid
}

// Tn translates a message with a plural form chosen by n.
func (c *Catalog) Tn(msgid, plural string, n int) string {
	if n < 0 {
		n = -n
	}
	return c.TnFloat(msgid, plural, float64(n))
}

// TnFloat translates a message with a plural form chosen by a count that
// may have a fraction, as Python's ngettext with a float (see
// PluralFloatFunc): "Every 0.5 minutes".
func (c *Catalog) TnFloat(msgid, plural string, n float64) string {
	if c != nil {
		if s, ok := c.msgs[msgid]; ok {
			if i := c.plural(n); i < len(s) {
				return s[i]
			}
		}
	}
	if n == 1 {
		return msgid
	}
	return plural
}

// Has reports whether msgid has a usable translation.
func (c *Catalog) Has(ctx, msgid string) bool {
	if c == nil {
		return false
	}
	_, ok := c.msgs[key(ctx, msgid)]
	return ok
}

// Bundle holds every language of one domain ("cms" for the contestant
// and ranking sites, "admin" for the administration).
type Bundle struct {
	Domain string
	cats   map[string]*Catalog
}

// LoadBundle reads <lang>/LC_MESSAGES/<domain>.po for every language
// directory in fsys.
func LoadBundle(fsys fs.FS, domain string) (*Bundle, error) {
	b := &Bundle{Domain: domain, cats: map[string]*Catalog{}}
	dirs, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		p := path.Join(d.Name(), "LC_MESSAGES", domain+".po")
		fh, err := fsys.Open(p)
		if err != nil {
			continue
		}
		f, err := ParsePO(fh)
		_ = fh.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		c, err := NewCatalog(d.Name(), f)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		b.cats[d.Name()] = c
	}
	return b, nil
}

// Languages returns the available languages, source language included.
func (b *Bundle) Languages() []string {
	out := []string{SourceLanguage}
	for l := range b.cats {
		out = append(out, l)
	}
	sort.Strings(out[1:])
	return out
}

// Normalize turns "es-cl", "es_CL" or "ES-CL" into the directory form "es_CL".
func Normalize(lang string) string {
	lang = strings.ReplaceAll(strings.TrimSpace(lang), "-", "_")
	base, region, ok := strings.Cut(lang, "_")
	base = strings.ToLower(base)
	if !ok {
		return base
	}
	return base + "_" + strings.ToUpper(region)
}

// Catalog returns the catalog for lang: an exact match, else the base
// language (es_CL -> es), else nil (English).
func (b *Bundle) Catalog(lang string) *Catalog {
	lang = Normalize(lang)
	if c, ok := b.cats[lang]; ok {
		return c
	}
	if base, _, ok := strings.Cut(lang, "_"); ok {
		if c, ok := b.cats[base]; ok {
			return c
		}
	}
	return nil
}

// Match picks the best language for an Accept-Language header among the
// allowed ones (empty allowed means every available language). It
// returns SourceLanguage when nothing matches.
func (b *Bundle) Match(acceptLanguage string, allowed []string) string {
	avail := map[string]bool{SourceLanguage: true}
	for l := range b.cats {
		avail[l] = true
	}
	if len(allowed) > 0 {
		filtered := map[string]bool{}
		for _, l := range allowed {
			if l = Normalize(l); avail[l] {
				filtered[l] = true
			}
		}
		if len(filtered) > 0 {
			avail = filtered
		}
	}
	type pref struct {
		lang string
		q    float64
		pos  int
	}
	var prefs []pref
	for i, part := range strings.Split(acceptLanguage, ",") {
		tag, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if tag == "" || tag == "*" {
			continue
		}
		q := 1.0
		if v, ok := strings.CutPrefix(strings.TrimSpace(params), "q="); ok {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				q = f
			}
		}
		if q > 0 {
			prefs = append(prefs, pref{Normalize(tag), q, i})
		}
	}
	sort.SliceStable(prefs, func(i, j int) bool { return prefs[i].q > prefs[j].q })
	for _, p := range prefs {
		if avail[p.lang] {
			return p.lang
		}
		base, _, _ := strings.Cut(p.lang, "_")
		if avail[base] {
			return base
		}
		// "es" requested and only "es_CL" offered: still a better match
		// than English.
		for _, l := range sorted(avail) {
			if strings.HasPrefix(l, base+"_") {
				return l
			}
		}
	}
	if avail[SourceLanguage] {
		return SourceLanguage
	}
	return sorted(avail)[0]
}

func sorted(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
