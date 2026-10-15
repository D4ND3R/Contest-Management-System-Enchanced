// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package web

import (
	"math"
	"strconv"
	"strings"
	"time"
)

// Locale formats dates, durations, sizes and numbers for one language,
// like the methods of CMS's Translation (cms/locale/locale.py, CLDR data
// through Babel). Package internal/l10n implements it; the sites pass
// l10n.Get through LanguagesOptions.Locale. Formatting messages that
// need a translation ("%s KiB") are the implementation's business.
type Locale interface {
	// Identifier is the BCP 47 identifier CMS uses for the language
	// ("en", "es-CL", "zh-Hant-TW"): <html lang>, the language cookie and
	// the selector options.
	Identifier() string
	// Name is the name of the language in itself ("español (Chile)").
	Name() string
	FormatDatetime(t time.Time, tz *time.Location) string
	FormatTime(t time.Time, tz *time.Location) string
	FormatDatetimeSmart(t, now time.Time, tz *time.Location) string
	FormatTimedelta(d time.Duration) string
	// FormatDuration formats seconds with millisecond precision; length
	// is "short" (the default in CMS) or "long"/"narrow".
	FormatDuration(seconds float64, length string) string
	FormatSize(n int64) string
	FormatDecimal(x float64) string
	// FormatLocale names a locale identifier in this language
	// ("Italian" for "it").
	FormatLocale(code string) string
}

// BasicLocale is the Locale used when the site provides none (tests,
// tools): English formats close to Babel's "en" for the 26 interface
// languages, with their CMS identifiers and native names. The real
// formats come from internal/l10n.
type BasicLocale struct {
	Code string // catalog directory name: "es_CL"
}

// basicLocales maps catalog directories to the identifier and native name
// Babel 2.12 gives CMS (cms.locale.get_translations).
var basicLocales = map[string][2]string{
	"ar": {"ar", "العربية"}, "bg": {"bg", "български"}, "bs": {"bs", "bosanski"},
	"cs": {"cs", "čeština"}, "de": {"de", "Deutsch"}, "en": {"en", "English"},
	"es": {"es", "español"}, "es_CL": {"es-CL", "español (Chile)"}, "et": {"et", "eesti"},
	"fr": {"fr", "français"}, "he": {"he", "עברית"}, "hu": {"hu", "magyar"},
	"it": {"it", "italiano"}, "ja": {"ja", "日本語"}, "ko": {"ko", "한국어"},
	"lt": {"lt", "lietuvių"}, "lv": {"lv", "latviešu"}, "nl": {"nl", "Nederlands"},
	"ro": {"ro", "română"}, "ru": {"ru", "русский"}, "sl": {"sl", "slovenščina"},
	"th": {"th", "ไทย"}, "uk": {"uk", "українська"}, "vi": {"vi", "Tiếng Việt"},
	"zh_CN": {"zh-Hans-CN", "中文 (简体, 中国)"}, "zh_TW": {"zh-Hant-TW", "中文 (繁體, 台灣)"},
}

// Identifier implements Locale.
func (l BasicLocale) Identifier() string {
	if v, ok := basicLocales[l.Code]; ok {
		return v[0]
	}
	return strings.ReplaceAll(l.Code, "_", "-")
}

// Name implements Locale.
func (l BasicLocale) Name() string {
	if v, ok := basicLocales[l.Code]; ok {
		return v[1]
	}
	return l.Code
}

func in(t time.Time, tz *time.Location) time.Time {
	if tz == nil {
		tz = time.UTC
	}
	return t.In(tz)
}

// FormatDatetime implements Locale (Babel en, medium: "Oct 14, 2026, 3:04:05 PM").
func (BasicLocale) FormatDatetime(t time.Time, tz *time.Location) string {
	return in(t, tz).Format("Jan 2, 2006, 3:04:05\u202fPM")
}

// FormatTime implements Locale (Babel en, medium: "3:04:05 PM").
func (BasicLocale) FormatTime(t time.Time, tz *time.Location) string {
	return in(t, tz).Format("3:04:05\u202fPM")
}

// FormatDatetimeSmart implements Locale: the time alone when t is on the
// same day as now in tz.
func (l BasicLocale) FormatDatetimeSmart(t, now time.Time, tz *time.Location) string {
	a, b := in(t, tz), in(now, tz)
	if a.Year() == b.Year() && a.YearDay() == b.YearDay() {
		return l.FormatTime(t, tz)
	}
	return l.FormatDatetime(t, tz)
}

// FormatTimedelta implements Locale ("1 day, 2 hours, 3 minutes, and 4 seconds").
func (BasicLocale) FormatTimedelta(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	secs := int64(d / time.Second)
	var parts []string
	unit := func(n int64, one string) {
		if n == 1 {
			parts = append(parts, "1 "+one)
		} else {
			parts = append(parts, strconv.FormatInt(n, 10)+" "+one+"s")
		}
	}
	if days := secs / 86400; days > 0 {
		unit(days, "day")
	}
	secs %= 86400
	if secs >= 3600 {
		unit(secs/3600, "hour")
		secs %= 3600
	}
	if secs >= 60 {
		unit(secs/60, "minute")
		secs %= 60
	}
	if secs > 0 || len(parts) == 0 {
		unit(secs, "second")
	}
	switch len(parts) {
	case 1:
		return parts[0]
	case 2:
		return parts[0] + " and " + parts[1]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + ", and " + parts[len(parts)-1]
}

// FormatDuration implements Locale ("1,234.568 sec", "1.000 second").
func (BasicLocale) FormatDuration(seconds float64, length string) string {
	n := groupDigits(strconv.FormatFloat(math.Abs(seconds), 'f', 3, 64))
	if length == "long" {
		if n == "1.000" {
			return n + " second"
		}
		return n + " seconds"
	}
	return n + " sec"
}

// FormatSize implements Locale as CMS's format_size: bytes below 1 KiB,
// then the largest binary unit with three significant digits ("1.50
// KiB", "999 KiB", "1,000 KiB", "5,165 TiB").
func (BasicLocale) FormatSize(n int64) string {
	if n < 0 {
		n = -n
	}
	if n < 1024 {
		if n == 1 {
			return "1 byte"
		}
		return groupDigits(strconv.FormatInt(n, 10)) + " bytes"
	}
	f := float64(n)
	for _, u := range []string{"KiB", "MiB", "GiB", "TiB"} {
		f /= 1024
		if f < 1024 {
			d := 2 - int(math.Floor(math.Log10(f)))
			if d < 0 {
				d = 0
			}
			return groupDigits(strconv.FormatFloat(f, 'f', d, 64)) + " " + u
		}
	}
	return groupDigits(strconv.FormatFloat(math.RoundToEven(f), 'f', 0, 64)) + " TiB"
}

// FormatDecimal implements Locale (up to three decimals, thousands
// separated by commas: Babel's "#,##0.###").
func (BasicLocale) FormatDecimal(x float64) string {
	s := strconv.FormatFloat(x, 'f', 3, 64)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "-0" {
		s = "0"
	}
	return groupDigits(s)
}

// groupDigits puts commas between the thousands of the integral part of
// a decimal number ("-1234.5" -> "-1,234.5").
func groupDigits(s string) string {
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	intPart, frac, hasFrac := strings.Cut(s, ".")
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	for i, c := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if hasFrac {
		b.WriteByte('.')
		b.WriteString(frac)
	}
	return b.String()
}

// FormatLocale implements Locale (the identifier itself).
func (BasicLocale) FormatLocale(code string) string { return code }
