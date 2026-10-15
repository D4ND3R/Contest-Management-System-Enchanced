// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package web

import (
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/locale"
)

func testLanguages(t testing.TB) *Languages {
	t.Helper()
	ls, err := NewLanguages(LanguagesOptions{Catalogs: locale.FS, Domains: []string{locale.DomainContest, locale.DomainAdmin}})
	if err != nil {
		t.Fatal(err)
	}
	return ls
}

func TestLanguagesLoad(t *testing.T) {
	ls := testLanguages(t)
	var ids []string
	for _, l := range ls.All() {
		ids = append(ids, l.ID)
	}
	want := "en ar bg bs cs de es es-CL et fr he hu it ja ko lt lv nl ro ru sl th uk vi zh-Hans-CN zh-Hant-TW"
	if got := strings.Join(ids, " "); got != want {
		t.Errorf("languages:\n got %s\nwant %s", got, want)
	}
	if ls.Default().ID != "en" || ls.Get("ES-cl").ID != "es-CL" || ls.Get("xx") != nil {
		t.Error("Get/Default")
	}
	es, escl := ls.Get("es"), ls.Get("es-CL")
	if es.Name != "español" || es.String() != "es" {
		t.Errorf("es: %q", es.Name)
	}
	// A CMS string: domain cms. A string only in admin.po: the fallback
	// domain, with the base language for es-CL.
	if es.T("Overview") != "Carátula" {
		t.Errorf("cms domain: %q", es.T("Overview"))
	}
	const adminOnly = "Compilation memory limit exceeded" // internal/grading/steps
	if hasCMS(t, adminOnly) {
		t.Fatalf("%q is in cms.pot: pick another admin-only string", adminOnly)
	}
	if es.T(adminOnly) == adminOnly || escl.T(adminOnly) != es.T(adminOnly) {
		t.Errorf("admin fallback for %q: es %q, es-CL %q", adminOnly, es.T(adminOnly), escl.T(adminOnly))
	}
	// es-CL has its own cms catalog, used before the base language.
	if escl.T("Overview") == "" {
		t.Error("es-CL")
	}
	if es.T("") != "" {
		t.Errorf("empty msgid: %q", es.T(""))
	}
	var nilLang *Lang
	if nilLang.T("x") != "x" || nilLang.Tn("a", "b", 2) != "b" || nilLang.Tc("c", "x") != "x" {
		t.Error("nil Lang must be English")
	}
}

func hasCMS(t *testing.T, msgid string) bool {
	b, err := os.ReadFile("../../locale/cms.pot")
	if err != nil {
		t.Fatal(err)
	}
	return strings.Contains(string(b), "msgid \""+msgid+"\"\n")
}

// From cmstestsuite/unit_tests/locale/locale_test.py of CMS.
func TestFilterLanguageCodes(t *testing.T) {
	for _, c := range []struct{ codes, prefixes, want []string }{
		{[]string{"en-US", "fr-FR", "it-IT"}, []string{"en-US", "it-IT", "de-DE"}, []string{"en-US", "it-IT"}},
		{[]string{"en-US", "fr-FR", "it-IT"}, []string{"en", "it", "de"}, []string{"en-US", "it-IT"}},
		{[]string{"it-IT"}, []string{"fr"}, []string{"en"}},
		{nil, []string{"fr"}, []string{"en"}},
		{[]string{"it-IT"}, nil, []string{"en"}},
		{[]string{"it", "froobar", "fr-FR"}, []string{"fr"}, []string{"fr-FR"}},
		{[]string{"it", "fr-FR"}, []string{"de", "froobar", "fr"}, []string{"fr-FR"}},
		{[]string{"zh-Hant-TW", "zh-Hans-CN"}, []string{"zh-TW"}, []string{"zh-Hant-TW"}},
		{[]string{"es", "es-CL"}, []string{"es_CL", "es-CL"}, []string{"es-CL"}},
	} {
		if got := FilterLanguageCodes(c.codes, c.prefixes); !reflect.DeepEqual(got, c.want) {
			t.Errorf("FilterLanguageCodes(%v, %v) = %v, want %v", c.codes, c.prefixes, got, c.want)
		}
	}
}

// From cmstestsuite/unit_tests/locale/locale_test.py of CMS.
func TestNegotiateLocale(t *testing.T) {
	for _, c := range []struct {
		preferred, available []string
		want                 string
	}{
		{[]string{"it-IT", "fr-FR"}, []string{"fr-FR", "en-US"}, "fr-FR"},
		{[]string{"it", "fr"}, []string{"fr-FR", "en-US"}, "fr-FR"},
		{[]string{"fr-FR", "en-US"}, []string{"fr", "en"}, "fr"},
		{[]string{"fr", "en"}, []string{"it", "de"}, ""},
		{[]string{"fr", "en"}, nil, ""},
		{nil, []string{"it", "de"}, ""},
		{[]string{"it", "froobar", "fr-FR"}, []string{"fr"}, "fr"},
		{[]string{"it", "fr-FR"}, []string{"de", "froobar", "fr"}, "fr"},
	} {
		if got := NegotiateLocale(c.preferred, c.available); got != c.want {
			t.Errorf("NegotiateLocale(%v, %v) = %q, want %q", c.preferred, c.available, got, c.want)
		}
	}
}

// TestChoose reproduces BaseHandler.setup_locale and the contest filter
// of CMS: the expectations were computed by CMS itself (werkzeug 3.1
// parse_accept_header, babel 2.12 negotiate_locale) for the 26 interface
// languages. Where CMS returns the Accept-Language spelling ("ES") and
// then fails looking it up (500), the Go side picks the language.
func TestChoose(t *testing.T) {
	ls := testLanguages(t)
	for _, c := range []struct {
		accept, cookie       string
		hasCookie            bool
		allowed              []string
		lang, auto, cookieID string
		available            int
	}{
		{"", "", false, nil, "en", "en", "", 26},
		{"es-CL,es;q=0.9", "", false, nil, "es-CL", "es-CL", "", 26},
		{"es-AR,es;q=0.8,en;q=0.5", "", false, nil, "es", "es", "", 26},
		{"fr-CA", "", false, nil, "fr", "fr", "", 26},
		{"zh-TW", "", false, nil, "en", "en", "", 26},
		{"zh-tw,zh;q=0.5", "", false, nil, "en", "en", "", 26},
		{"it", "", false, nil, "it", "it", "", 26},
		{"*", "", false, nil, "en", "en", "", 26},
		{"de;q=0, it;q=0.5", "", false, nil, "it", "it", "", 26},
		{"de;q=0", "", false, nil, "de", "de", "", 26},
		{"en-US,en;q=0.9", "", false, nil, "en", "en", "", 26},
		{"es", "it", true, nil, "it", "es", "it", 26},
		{"es", "xx", true, nil, "es", "es", "", 26},
		{"es", "es-AR", true, nil, "es", "es", "", 26},
		{"es", "", true, nil, "es", "es", "", 26},
		{"ES", "", false, nil, "es", "es", "", 26},
		{"fr", "", false, []string{"it", "es"}, "es", "es", "", 3},
		{"es", "", false, []string{"xx"}, "en", "en", "", 1},
		{"", "", false, []string{"zh-TW"}, "zh-Hant-TW", "zh-Hant-TW", "", 1},
		{"de", "de", true, []string{"en", "es"}, "en", "en", "", 3},
		{"pt-BR, pt;q=0.9", "", false, nil, "en", "en", "", 26},
		{"sl", "", false, nil, "sl", "sl", "", 26},
		{"nl-BE", "", false, nil, "nl", "nl", "", 26},
		{"he-IL", "", false, nil, "he", "he", "", 26},
		{"en;q=0.1, ru;q=0.9", "", false, nil, "ru", "ru", "", 26},
		{"ru;q=1.5, it", "", false, nil, "it", "it", "", 26},
		{"ru;q=abc, it", "", false, nil, "it", "it", "", 26},
		{"ja, *;q=0.1", "", false, nil, "ja", "ja", "", 26},
		{"x-pig-latin, uk", "", false, nil, "uk", "uk", "", 26},
		{"es-cl", "", false, nil, "es-CL", "es-CL", "", 26},
		{"", "es-CL", true, []string{"es"}, "es-CL", "es", "es-CL", 2},
		{"zh-Hant-TW", "", false, nil, "zh-Hant-TW", "zh-Hant-TW", "", 26},
		{"zh-CN", "", false, nil, "en", "en", "", 26},
		{"bs-BA", "", false, nil, "bs", "bs", "", 26},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		if c.accept != "" {
			r.Header.Set("Accept-Language", c.accept)
		}
		if c.hasCookie {
			r.Header.Set("Cookie", LanguageCookie+"="+c.cookie)
		}
		avail := ls.All()
		if c.allowed != nil {
			avail = ls.Filter(c.allowed)
		}
		ch := ls.Choose(r, avail)
		cookieID := ""
		if ch.Cookie != nil {
			cookieID = ch.Cookie.ID
		}
		if ch.Lang.ID != c.lang || ch.Automatic.ID != c.auto || cookieID != c.cookieID || len(ch.Available) != c.available {
			t.Errorf("accept %q cookie %q allowed %v: got %s/%s/%q (%d), want %s/%s/%q (%d)",
				c.accept, c.cookie, c.allowed, ch.Lang.ID, ch.Automatic.ID, cookieID, len(ch.Available),
				c.lang, c.auto, c.cookieID, c.available)
		}
	}
	// The selector lists the languages sorted by identifier.
	ch := ls.Choose(httptest.NewRequest("GET", "/", nil), ls.Filter([]string{"es", "en"}))
	if m := ch.ByID(); len(m) != 3 || m["es-CL"] == nil || m["en"] == nil {
		t.Errorf("ByID: %v", m)
	}
}

// The expectations are werkzeug 3.1's parse_accept_header(h,
// LanguageAccept).values(), which CMS uses.
func TestParseAcceptLanguage(t *testing.T) {
	for in, want := range map[string][]string{
		"":                          nil,
		"da, en-gb;q=0.8, en;q=0.7": {"da", "en-gb", "en"},
		"en;q=0.5, *, fr":           {"fr", "en", "*"},
		"a;q=0.5, b;q=0.5, c":       {"c", "a", "b"},
		"a;q=2, b;q=-1, c;q=x, d":   {"d"},
		"a;level=1, b":              {"a; level=1", "b"},
		`a;q="0.3", b`:              {"b", "a"},
		"a,,b,":                     {"a", "", "b"},
	} {
		got := ParseAcceptLanguage(in)
		if len(got) == 0 && len(want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("ParseAcceptLanguage(%q) = %q, want %q", in, got, want)
		}
	}
}
