// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// The language choice is ported from cms/locale/locale.py
// (filter_language_codes, choose_language_code) and
// cms/server/contest/handlers/base.py (setup_locale) of CMS:
// Copyright © 2012-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2013-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2014 Artem Iglikov <artem.iglikov@gmail.com>
// with the Accept-Language parsing of Werkzeug 3.1 and
// babel.core.negotiate_locale of Babel 2.12 (BSD licensed).

package web

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/i18n"
)

// LanguageCookie is the cookie set by the language selector of the
// contestant site (cws_utils.js switch_lang) with the identifier of the
// chosen language.
const LanguageCookie = "language"

// Lang is one interface language: its message catalogs and its formats.
type Lang struct {
	// ID is the identifier CMS uses ("es-CL", "zh-Hant-TW").
	ID string
	// Code is the catalog directory ("es_CL"); "en" for English.
	Code string
	// Name is the name of the language in itself, for the selector.
	Name string
	// Locale formats dates, sizes and numbers.
	Locale Locale

	cats []*i18n.Catalog // by domain priority; nil entries are skipped
}

// T translates msgid: the first catalog (domain) that has it wins, so the
// contestant site uses the 25 catalogs of CMS ("cms") and falls back to
// "admin" for the strings CMS does not have.
func (l *Lang) T(msgid string) string {
	if l != nil {
		for _, c := range l.cats {
			if c.Has("", msgid) {
				return c.T(msgid)
			}
		}
	}
	return msgid
}

// Tc translates msgid in a context (msgctxt).
func (l *Lang) Tc(ctx, msgid string) string {
	if l != nil {
		for _, c := range l.cats {
			if c.Has(ctx, msgid) {
				return c.Tc(ctx, msgid)
			}
		}
	}
	return msgid
}

// Tn translates a message with plural forms chosen by n.
func (l *Lang) Tn(msgid, plural string, n int) string {
	if l != nil {
		for _, c := range l.cats {
			if c.Has("", msgid) {
				return c.Tn(msgid, plural, n)
			}
		}
	}
	if n == 1 {
		return msgid
	}
	return plural
}

// String returns the identifier.
func (l *Lang) String() string { return l.ID }

// LanguagesOptions configures NewLanguages.
type LanguagesOptions struct {
	// Catalogs holds <lang>/LC_MESSAGES/<domain>.po (locale.FS).
	Catalogs fs.FS
	// Domains in priority order, for example {"cms", "admin"} for the
	// contestant site and {"admin", "cms"} for the administration. The
	// interface languages are English plus those of the first domain.
	Domains []string
	// Locale returns the formats of a language given its catalog
	// directory ("es_CL"; "en" for English). Nil uses BasicLocale.
	Locale func(code string) Locale
}

// Languages is the set of interface languages of a site.
type Languages struct {
	all  []*Lang // English first, then by identifier (case-insensitive)
	byID map[string]*Lang
}

// NewLanguages loads the catalogs of the given domains.
func NewLanguages(o LanguagesOptions) (*Languages, error) {
	if len(o.Domains) == 0 {
		return nil, errors.New("web: no translation domains")
	}
	newLocale := o.Locale
	if newLocale == nil {
		newLocale = func(code string) Locale { return BasicLocale{Code: code} }
	}
	bundles := make([]*i18n.Bundle, len(o.Domains))
	for i, d := range o.Domains {
		b, err := i18n.LoadBundle(o.Catalogs, d)
		if err != nil {
			return nil, fmt.Errorf("web: catalogs of %q: %w", d, err)
		}
		bundles[i] = b
	}
	ls := &Languages{byID: map[string]*Lang{}}
	for _, code := range bundles[0].Languages() {
		loc := newLocale(code)
		l := &Lang{ID: loc.Identifier(), Code: code, Name: loc.Name(), Locale: loc}
		if code != i18n.SourceLanguage {
			for i, b := range bundles {
				c := b.Catalog(code)
				// The first domain is used as gettext in CMS: its exact
				// catalog only. The fallback domains may lend the base
				// language (es for es_CL), better than English for the
				// strings CMS does not have.
				if c != nil && (i > 0 || i18n.Normalize(c.Lang) == i18n.Normalize(code)) {
					l.cats = append(l.cats, c)
				}
			}
		}
		if _, dup := ls.byID[strings.ToLower(l.ID)]; dup {
			return nil, fmt.Errorf("web: two catalogs for language %s", l.ID)
		}
		ls.byID[strings.ToLower(l.ID)] = l
		ls.all = append(ls.all, l)
	}
	sort.SliceStable(ls.all, func(i, j int) bool {
		a, b := ls.all[i], ls.all[j]
		if (a.Code == i18n.SourceLanguage) != (b.Code == i18n.SourceLanguage) {
			return a.Code == i18n.SourceLanguage
		}
		return strings.ToLower(a.ID) < strings.ToLower(b.ID)
	})
	return ls, nil
}

// All returns every language, English first.
func (ls *Languages) All() []*Lang { return ls.all }

// Default returns English.
func (ls *Languages) Default() *Lang { return ls.all[0] }

// Get returns the language with the given identifier (case-insensitive),
// or nil.
func (ls *Languages) Get(id string) *Lang { return ls.byID[strings.ToLower(id)] }

// Filter keeps the languages matching one of the prefixes, typically the
// allowed_localizations of a contest (FilterLanguageCodes), in the order
// of All. No prefixes means every language (CMS only filters when the
// contest restricts the languages); if nothing matches, only English is
// left, as in CMS.
func (ls *Languages) Filter(prefixes []string) []*Lang {
	if len(prefixes) == 0 {
		return ls.all
	}
	ids := make([]string, len(ls.all))
	for i, l := range ls.all {
		ids[i] = l.ID
	}
	var out []*Lang
	for _, id := range FilterLanguageCodes(ids, prefixes) {
		if l := ls.Get(id); l != nil {
			out = append(out, l)
		}
	}
	if len(out) == 0 {
		return []*Lang{ls.Default()}
	}
	return out
}

// FilterLanguageCodes is CMS's filter_language_codes: the codes (BCP 47
// identifiers, "-" separated) that are at least as specific as one of
// the prefixes, in their order: every component of the code (language,
// territory, script, variant) must equal the prefix's, and components
// the prefix leaves out match anything ("en" matches "en-US", "zh-TW"
// matches "zh-Hant-TW"). Invalid codes and prefixes are ignored ("es_CL"
// is not a valid prefix). When nothing matches it returns ["en"].
func FilterLanguageCodes(codes, prefixes []string) []string {
	var parsed []locParts
	for _, p := range prefixes {
		if lp, ok := parseLocale(p); ok {
			parsed = append(parsed, lp)
		}
	}
	var out []string
	for _, c := range codes {
		lp, ok := parseLocale(c)
		if !ok {
			continue
		}
		for _, p := range parsed {
			if p.matches(lp) {
				out = append(out, lp.String())
				break
			}
		}
	}
	if len(out) == 0 {
		return []string{i18n.SourceLanguage}
	}
	return out
}

// Choice is the outcome of the language choice of a request.
type Choice struct {
	// Lang is the language of the page.
	Lang *Lang
	// Automatic is the best match of Accept-Language ("Automatic (…)" in
	// the selector).
	Automatic *Lang
	// Cookie is the language picked in the selector, nil if none (or if
	// it is not available).
	Cookie *Lang
	// Available are the languages of the selector, in order.
	Available []*Lang
}

// ByID returns the available languages by identifier: ranging over the
// map in a template visits them sorted by identifier, as the language
// selector of CMS (available_translations|dictsort(by="key")).
func (c Choice) ByID() map[string]*Lang {
	m := make(map[string]*Lang, len(c.Available))
	for _, l := range c.Available {
		m[l.ID] = l
	}
	return m
}

// Choose picks the language of a request among available (CMS
// setup_locale): the best match of Accept-Language, else the first
// available language; a "language" cookie naming an available language
// overrides it. available must not be empty (use Filter or All).
func (ls *Languages) Choose(r *http.Request, available []*Lang) Choice {
	if len(available) == 0 {
		available = ls.all
	}
	ids := make([]string, len(available))
	byID := make(map[string]*Lang, len(available))
	for i, l := range available {
		ids[i] = l.ID
		byID[strings.ToLower(l.ID)] = l
	}
	auto := byID[strings.ToLower(NegotiateLocale(ParseAcceptLanguage(r.Header.Get("Accept-Language")), ids))]
	if auto == nil {
		auto = available[0]
	}
	ch := Choice{Lang: auto, Automatic: auto, Available: available}
	if c, err := r.Cookie(LanguageCookie); err == nil {
		chosen := NegotiateLocale([]string{c.Value, auto.ID}, ids)
		if l := byID[strings.ToLower(chosen)]; l != nil {
			ch.Lang = l
			// CMS compares the negotiated string with the cookie itself.
			if chosen == c.Value {
				ch.Cookie = l
			}
		}
	}
	return ch
}

// qValue is Werkzeug's _q_value_re.
var qValue = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)

// ParseAcceptLanguage returns the language ranges of an Accept-Language
// header in Werkzeug's order (LanguageAccept.values()): by quality, the
// wildcard last, ties in header order. Items with an invalid quality
// (not a number, or outside [0, 1]) are dropped; q=0 items are kept, as
// Werkzeug does.
func ParseAcceptLanguage(h string) []string {
	type item struct {
		v string
		q float64
	}
	var items []item
	for _, part := range splitHeaderList(h) {
		v, params, _ := strings.Cut(part, ";")
		v = strings.TrimSpace(v)
		q := 1.0
		var extra []string
		for _, p := range strings.Split(params, ";") {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			k, val, _ := strings.Cut(p, "=")
			k = strings.ToLower(strings.TrimSpace(k))
			val = strings.Trim(strings.TrimSpace(val), `"`)
			if k == "q" {
				if !qValue.MatchString(val) {
					q = -1
					break
				}
				q, _ = strconv.ParseFloat(val, 64)
				if q < 0 || q > 1 {
					q = -1
					break
				}
				continue
			}
			extra = append(extra, k+"="+val)
		}
		if q < 0 {
			continue
		}
		if len(extra) > 0 {
			// Werkzeug rebuilds "en; a=b": it never matches a language.
			v += "; " + strings.Join(extra, "; ")
		}
		items = append(items, item{v, q})
	}
	sort.SliceStable(items, func(i, j int) bool {
		wi, wj := items[i].v != "*", items[j].v != "*"
		if wi != wj {
			return wi
		}
		return items[i].q > items[j].q
	})
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.v
	}
	return out
}

// splitHeaderList splits a comma-separated header, keeping quoted strings
// together.
func splitHeaderList(h string) []string {
	var out []string
	var cur strings.Builder
	quoted := false
	for i := 0; i < len(h); i++ {
		c := h[i]
		switch {
		case c == '"':
			quoted = !quoted
			cur.WriteByte(c)
		case c == '\\' && quoted && i+1 < len(h):
			cur.WriteByte(c)
			cur.WriteByte(h[i+1])
			i++
		case c == ',' && !quoted:
			out = append(out, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	// As urllib's parse_http_list: a trailing empty item is dropped.
	if cur.Len() > 0 {
		out = append(out, strings.TrimSpace(cur.String()))
	}
	return out
}

// localeAliases is babel.core.LOCALE_ALIASES.
var localeAliases = map[string]string{
	"ar": "ar_SY", "bg": "bg_BG", "bs": "bs_BA", "ca": "ca_ES", "cs": "cs_CZ", "da": "da_DK",
	"de": "de_DE", "el": "el_GR", "en": "en_US", "es": "es_ES", "et": "et_EE", "fa": "fa_IR",
	"fi": "fi_FI", "fr": "fr_FR", "gl": "gl_ES", "he": "he_IL", "hu": "hu_HU", "id": "id_ID",
	"is": "is_IS", "it": "it_IT", "ja": "ja_JP", "km": "km_KH", "ko": "ko_KR", "lt": "lt_LT",
	"lv": "lv_LV", "mk": "mk_MK", "nl": "nl_NL", "nn": "nn_NO", "no": "nb_NO", "pl": "pl_PL",
	"pt": "pt_PT", "ro": "ro_RO", "ru": "ru_RU", "sk": "sk_SK", "sl": "sl_SI", "sv": "sv_SE",
	"th": "th_TH", "tr": "tr_TR", "uk": "uk_UA",
}

// NegotiateLocale is Babel's negotiate_locale with "-" as separator (CMS
// choose_language_code): the first preferred range that is available
// (case-insensitive), or whose alias is, or whose language part alone is.
// It returns "" when nothing matches. Note that "zh-TW" does not match
// "zh-Hant-TW", as in CMS.
func NegotiateLocale(preferred, available []string) string {
	avail := map[string]bool{}
	for _, a := range available {
		if a != "" {
			avail[strings.ToLower(a)] = true
		}
	}
	for _, p := range preferred {
		ll := strings.ToLower(p)
		if avail[ll] {
			return p
		}
		if alias, ok := localeAliases[ll]; ok {
			alias = strings.ReplaceAll(alias, "_", "-")
			if avail[strings.ToLower(alias)] {
				return alias
			}
		}
		if parts := strings.Split(p, "-"); len(parts) > 1 && avail[strings.ToLower(parts[0])] {
			return parts[0]
		}
	}
	return ""
}

// locParts are the components of a locale identifier (Babel parse_locale).
type locParts struct{ lang, territory, script, variant string }

// String is Babel's get_locale_identifier(parts, sep="-").
func (p locParts) String() string {
	out := p.lang
	for _, c := range []string{p.script, p.territory, p.variant} {
		if c != "" {
			out += "-" + c
		}
	}
	return out
}

func (p locParts) matches(l locParts) bool {
	return l.lang == p.lang &&
		(p.territory == "" || l.territory == p.territory) &&
		(p.script == "" || l.script == p.script) &&
		(p.variant == "" || l.variant == p.variant)
}

func isAlpha(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isASCIILetter(s[i]) {
			return false
		}
	}
	return s != ""
}

func isDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

// parseLocale is Babel's parse_locale(identifier, sep="-").
func parseLocale(id string) (locParts, bool) {
	if i := strings.IndexByte(id, '.'); i >= 0 {
		id = id[:i] // charset
	}
	if i := strings.IndexByte(id, '@'); i >= 0 {
		id = id[:i] // modifier
	}
	parts := strings.Split(id, "-")
	var p locParts
	p.lang = strings.ToLower(parts[0])
	parts = parts[1:]
	if !isAlpha(p.lang) {
		return p, false
	}
	if len(parts) > 0 && len(parts[0]) == 4 && isAlpha(parts[0]) {
		p.script = strings.ToUpper(parts[0][:1]) + strings.ToLower(parts[0][1:])
		parts = parts[1:]
	}
	if len(parts) > 0 {
		if len(parts[0]) == 2 && isAlpha(parts[0]) {
			p.territory = strings.ToUpper(parts[0])
			parts = parts[1:]
		} else if len(parts[0]) == 3 && isDigits(parts[0]) {
			p.territory = parts[0]
			parts = parts[1:]
		}
	}
	if len(parts) > 0 {
		v := parts[0]
		if (len(v) == 4 && v[0] >= '0' && v[0] <= '9') || (len(v) >= 5 && isASCIILetter(v[0])) {
			p.variant = strings.ToUpper(parts[len(parts)-1])
			parts = parts[:len(parts)-1]
		}
	}
	if len(parts) > 0 {
		return p, false
	}
	return p, true
}
