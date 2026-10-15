// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from the Translation class of cms/locale/locale.py (CMS):
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
// format_unit and format_list follow babel/units.py and babel/lists.py
// (Babel 2.12.1, BSD-3-Clause, Copyright (c) 2013-2023 by the Babel Team).

package l10n

import (
	"math"
	"strings"
	"time"
)

// CLDR units read by the methods below.
const (
	unitDay    = "duration-day"
	unitHour   = "duration-hour"
	unitMinute = "duration-minute"
	unitSecond = "duration-second"
	unitByte   = "digital-byte"
)

// Unit lengths of FormatDuration.
const (
	LengthLong   = "long"
	LengthShort  = "short"
	LengthNarrow = "narrow"
)

// sizeUnits are the msgids of Translation.SIZE_UNITS. CMS passes them
// through gettext, but they are marked with N_ and never extracted, so no
// catalog translates them (tools/l10n/extract.py checks it).
var sizeUnits = [...]string{"%s KiB", "%s MiB", "%s GiB", "%s TiB"}

// FormatTimedelta returns d to the second, without rounding, as a list of
// days, hours, minutes and seconds with the zero ones left out ("1 hour,
// 1 minute, and 5 seconds"; "0 seconds" for less than a second). The sign
// is ignored, like CMS's format_timedelta.
func (l *Locale) FormatTimedelta(d time.Duration) string {
	secs := absInt(int64(d)) / uint64(time.Second)
	days, secs := secs/86400, secs%86400
	var parts []string
	if days > 0 {
		parts = append(parts, l.formatUnitInt(days, unitDay))
	}
	if secs >= 3600 {
		parts = append(parts, l.formatUnitInt(secs/3600, unitHour))
		secs %= 3600
	}
	if secs >= 60 {
		parts = append(parts, l.formatUnitInt(secs/60, unitMinute))
		secs %= 60
	}
	if secs > 0 || len(parts) == 0 {
		parts = append(parts, l.formatUnitInt(secs, unitSecond))
	}
	return l.formatList(parts)
}

// FormatDuration returns a number of seconds with exactly three decimals
// ("0.123 sec", "1.000 second" in the long form), like CMS's
// format_duration. length is "short" (CMS's default, also used for ""),
// "long" or "narrow". The sign is ignored.
func (l *Locale) FormatDuration(seconds float64, length string) string {
	if length == "" {
		length = LengthShort
	}
	d := math.Abs(seconds)
	return l.formatUnit(l.formatNumber(l.decimal, decimalFromFloat(d), [2]int{3, 3}),
		l.PluralForm(d), unitSecond, length)
}

// FormatSize returns a number of bytes in the largest binary unit that
// leaves at least 1 ("512 bytes", "1.50 MiB", "100 KiB"), with at least
// three significant digits, like CMS's format_size. The sign is ignored.
func (l *Locale) FormatSize(n int64) string {
	u := absInt(n)
	if u < 1024 {
		return l.formatUnitInt(u, unitByte)
	}
	// Dividing by powers of two is exact, so this matches Python's int and
	// float true divisions.
	x := float64(u)
	for _, unit := range sizeUnits {
		x /= 1024
		if x < 1024 {
			// max(0, 2 - floor(log10(x))) for 1 <= x < 1024. The digit
			// count is exact here: x has at most 50 significant bits, so it
			// is never one of the doubles just below 100 that libm's log10
			// rounds up to 2.
			d := 2
			switch {
			case x >= 100:
				d = 0
			case x >= 10:
				d = 1
			}
			return strings.Replace(unit, "%s", l.formatNumber(l.decimal, decimalFromFloat(x), [2]int{d, d}), 1)
		}
	}
	// round(n): x < 2^24 here, so the conversion is exact.
	whole := decimalFromUint(uint64(math.RoundToEven(x)))
	return strings.Replace(sizeUnits[len(sizeUnits)-1], "%s", l.formatNumber(l.decimal, whole, l.decimal.fracPrec), 1)
}

// FormatDecimal formats x with the locale's decimal pattern: grouping and
// up to three decimals ("1,234.568"), like CMS's format_decimal. As in
// Babel, x is rounded half to even from its shortest representation and
// -0 keeps its sign ("-0").
func (l *Locale) FormatDecimal(x float64) string {
	return l.formatNumber(l.decimal, decimalFromFloat(x), l.decimal.fracPrec)
}

// formatUnitInt is babel.units.format_unit(n, unit) for an integer.
func (l *Locale) formatUnitInt(n uint64, unit string) string {
	op := operandsFromUint(n)
	return l.formatUnit(l.formatNumber(l.decimal, decimalFromUint(n), l.decimal.fracPrec),
		l.plural.form(&op), unit, LengthLong)
}

// formatUnit is the end of babel.units.format_unit: the pattern of the
// plural form, or "value name" when the locale lacks it.
func (l *Locale) formatUnit(value, plural, unit, length string) string {
	if p, ok := l.units[unit][length][plural]; ok {
		return strings.ReplaceAll(p, "{0}", value)
	}
	name := l.unitNames[unit][length]
	if name == "" {
		name = unit
	}
	return value + " " + name
}

// formatList is babel.lists.format_list(items) with the standard style.
func (l *Locale) formatList(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return listPattern(l.list[3], items[0], items[1])
	}
	s := listPattern(l.list[0], items[0], items[1])
	for _, it := range items[2 : len(items)-1] {
		s = listPattern(l.list[1], s, it)
	}
	return listPattern(l.list[2], s, items[len(items)-1])
}

// listPattern is pattern.format(a, b) for a pattern whose only fields are
// {0} and {1} (the generator checks it).
func listPattern(pattern, a, b string) string {
	var sb strings.Builder
	sb.Grow(len(pattern) + len(a) + len(b))
	for i := 0; i < len(pattern); i++ {
		if pattern[i] == '{' && i+2 < len(pattern) && pattern[i+2] == '}' {
			switch pattern[i+1] {
			case '0':
				sb.WriteString(a)
				i += 2
				continue
			case '1':
				sb.WriteString(b)
				i += 2
				continue
			}
		}
		sb.WriteByte(pattern[i])
	}
	return sb.String()
}
