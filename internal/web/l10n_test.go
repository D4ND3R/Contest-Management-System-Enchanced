// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package web

import (
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/l10n"
	"github.com/d4nd3r/contest-management-system-enchanced/locale"
)

// internal/l10n provides the real formats.
var _ Locale = (*l10n.Locale)(nil)

func TestL10nLocales(t *testing.T) {
	ls, err := NewLanguages(LanguagesOptions{
		Catalogs: locale.FS,
		Domains:  []string{locale.DomainContest, locale.DomainAdmin},
		Locale:   func(code string) Locale { return l10n.Get(code) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ls.All()) != 26 {
		t.Fatalf("%d languages", len(ls.All()))
	}
	for _, l := range ls.All() {
		b := BasicLocale{Code: l.Code}
		if l.ID != b.Identifier() || l.Name != b.Name() {
			t.Errorf("%s: l10n %q %q, BasicLocale %q %q", l.Code, l.ID, l.Name, b.Identifier(), b.Name())
		}
	}
	// English formats agree with the fallback.
	en, b := ls.Get("en").Locale, BasicLocale{Code: "en"}
	at := time.Date(2026, 10, 14, 13, 4, 5, 0, time.UTC)
	for _, c := range []struct{ got, want string }{
		{en.FormatDatetime(at, time.UTC), b.FormatDatetime(at, time.UTC)},
		{en.FormatSize(5_678_900_000_000_000), b.FormatSize(5_678_900_000_000_000)},
		{en.FormatDuration(1234.56789, "short"), b.FormatDuration(1234.56789, "short")},
		{en.FormatTimedelta(25*time.Hour + time.Second), b.FormatTimedelta(25*time.Hour + time.Second)},
		{en.FormatDecimal(1234.5), b.FormatDecimal(1234.5)},
	} {
		if c.got != c.want {
			t.Errorf("l10n %q, BasicLocale %q", c.got, c.want)
		}
	}
}
