// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from format_datetime, format_time and format_datetime_smart of
// cms/locale/locale.py (CMS):
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
// on top of a port of the pattern engine of babel/dates.py (Babel 2.12.1,
// BSD-3-Clause, Copyright (c) 2013-2023 by the Babel Team).

package l10n

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// FormatDatetime returns the medium date and time of t in tz ("Oct 14,
// 2026, 9:05:03 AM"), like CMS's format_datetime. A nil tz means UTC.
func (l *Locale) FormatDatetime(t time.Time, tz *time.Location) string {
	lt := inZone(t, tz)
	// Babel: datetime_format.replace("'", "").replace("{0}", time).replace("{1}", date).
	s := strings.ReplaceAll(l.datetimeFmt, "{0}", l.formatPattern(l.timeFmt, lt))
	return strings.ReplaceAll(s, "{1}", l.formatPattern(l.dateFmt, lt))
}

// FormatTime returns the medium time of t in tz ("9:05:03 AM").
func (l *Locale) FormatTime(t time.Time, tz *time.Location) string {
	return l.formatPattern(l.timeFmt, inZone(t, tz))
}

// FormatDatetimeSmart returns only the time when t falls on the same day
// as now in tz, the date and time otherwise.
func (l *Locale) FormatDatetimeSmart(t, now time.Time, tz *time.Location) string {
	y1, m1, d1 := inZone(t, tz).Date()
	y2, m2, d2 := inZone(now, tz).Date()
	if y1 == y2 && m1 == m2 && d1 == d2 {
		return l.FormatTime(t, tz)
	}
	return l.FormatDatetime(t, tz)
}

func inZone(t time.Time, tz *time.Location) time.Time {
	if tz == nil {
		tz = time.UTC
	}
	return t.In(tz)
}

// dtToken is a literal (field == 0) or a field repeated n times.
type dtToken struct {
	lit   string
	field rune
	n     int
}

// patternChars are Babel's PATTERN_CHARS: the letters that are fields,
// with their valid lengths (nil: any).
var patternChars = map[rune][]int{
	'G': {1, 2, 3, 4, 5}, 'y': nil, 'Y': nil, 'u': nil,
	'Q': {1, 2, 3, 4, 5}, 'q': {1, 2, 3, 4, 5},
	'M': {1, 2, 3, 4, 5}, 'L': {1, 2, 3, 4, 5},
	'w': {1, 2}, 'W': {1},
	'd': {1, 2}, 'D': {1, 2, 3}, 'F': {1}, 'g': nil,
	'E': {1, 2, 3, 4, 5, 6}, 'e': {1, 2, 3, 4, 5, 6}, 'c': {1, 3, 4, 5, 6},
	'a': {1, 2, 3, 4, 5}, 'b': {1, 2, 3, 4, 5}, 'B': {1, 2, 3, 4, 5},
	'h': {1, 2}, 'H': {1, 2}, 'K': {1, 2}, 'k': {1, 2},
	'm': {1, 2},
	's': {1, 2}, 'S': nil, 'A': nil,
	'z': {1, 2, 3, 4}, 'Z': {1, 2, 3, 4, 5}, 'O': {1, 4}, 'v': {1, 4},
	'V': {1, 2, 3, 4}, 'x': {1, 2, 3, 4, 5}, 'X': {1, 2, 3, 4, 5},
}

// supportedFields are the fields formatField implements: everything the
// medium patterns of CLDR use, without eras, quarters, weeks, fractional
// seconds or time zones.
const supportedFields = "yYuMLdDFEabBhHKkms"

// tokenizeDatePattern is babel.dates.tokenize_pattern: quoted text is
// literal, a doubled quote is a quote, runs of one pattern letter are fields.
func tokenizeDatePattern(pattern string) []dtToken {
	var (
		out       []dtToken
		charbuf   []rune
		quotebuf  []rune
		inQuote   bool
		fieldchar rune
		fieldnum  int
	)
	appendChars := func() {
		out = append(out, dtToken{lit: strings.ReplaceAll(string(charbuf), "\x00", "'")})
		charbuf = charbuf[:0]
	}
	appendField := func() {
		out = append(out, dtToken{field: fieldchar, n: fieldnum})
		fieldchar, fieldnum = 0, 0
	}
	for _, ch := range strings.ReplaceAll(pattern, "''", "\x00") {
		if inQuote {
			if ch == '\'' {
				charbuf = append(charbuf, quotebuf...)
				inQuote = false
			} else {
				quotebuf = append(quotebuf, ch)
			}
			continue
		}
		if ch == '\'' {
			if fieldchar != 0 {
				appendField()
			} else if len(charbuf) > 0 {
				appendChars()
			}
			inQuote, quotebuf = true, nil
		} else if _, ok := patternChars[ch]; ok {
			if len(charbuf) > 0 {
				appendChars()
			}
			if ch == fieldchar {
				fieldnum++
			} else {
				if fieldchar != 0 {
					appendField()
				}
				fieldchar, fieldnum = ch, 1
			}
		} else {
			if fieldchar != 0 {
				appendField()
			}
			charbuf = append(charbuf, ch)
		}
	}
	if fieldchar != 0 {
		appendField()
	} else if len(charbuf) > 0 {
		appendChars()
	}
	return out
}

// compileDatePattern tokenizes a pattern and checks its fields, like
// babel.dates.parse_pattern plus the fields this package implements.
func compileDatePattern(pattern string) ([]dtToken, error) {
	toks := tokenizeDatePattern(pattern)
	for _, t := range toks {
		if t.field == 0 {
			continue
		}
		if limit := patternChars[t.field]; limit != nil && !containsInt(limit, t.n) {
			return nil, fmt.Errorf("pattern %q: invalid length for field %q", pattern, strings.Repeat(string(t.field), t.n))
		}
		if !strings.ContainsRune(supportedFields, t.field) {
			return nil, fmt.Errorf("pattern %q: field %q is not implemented", pattern, string(t.field))
		}
	}
	return toks, nil
}

func containsInt(list []int, n int) bool {
	for _, v := range list {
		if v == n {
			return true
		}
	}
	return false
}

func (l *Locale) formatPattern(toks []dtToken, t time.Time) string {
	var b strings.Builder
	for _, tk := range toks {
		if tk.field == 0 {
			b.WriteString(tk.lit)
		} else {
			b.WriteString(l.formatField(tk.field, tk.n, t))
		}
	}
	return b.String()
}

var nameWidths = map[int]string{3: "abbreviated", 4: "wide", 5: "narrow", 6: "short"}

// formatField is DateTimeFormat.__getitem__ for the supported fields.
func (l *Locale) formatField(ch rune, n int, t time.Time) string {
	switch ch {
	case 'y', 'u', 'Y':
		year := t.Year()
		if ch == 'Y' {
			year, _ = t.ISOWeek()
		}
		s := zeroPad(year, n)
		if n == 2 {
			s = s[len(s)-2:]
		}
		return s
	case 'M', 'L':
		if n <= 2 {
			return zeroPad(int(t.Month()), n)
		}
		ctx := "format"
		if ch == 'L' {
			ctx = "stand-alone"
		}
		return l.months[ctx][nameWidths[n]][t.Month()-1]
	case 'd':
		return zeroPad(t.Day(), n)
	case 'D':
		return zeroPad(t.YearDay(), n)
	case 'F':
		return strconv.Itoa((t.Day()-1)/7 + 1)
	case 'E':
		if n < 3 {
			n = 3
		}
		return l.days[nameWidths[n]][(int(t.Weekday())+6)%7]
	case 'a', 'b', 'B':
		return l.formatPeriod(ch, n, t)
	case 'h':
		h := t.Hour() % 12
		if h == 0 {
			h = 12
		}
		return zeroPad(h, n)
	case 'H':
		return zeroPad(t.Hour(), n)
	case 'K':
		return zeroPad(t.Hour()%12, n)
	case 'k':
		h := t.Hour()
		if h == 0 {
			h = 24
		}
		return zeroPad(h, n)
	case 'm':
		return zeroPad(t.Minute(), n)
	case 's':
		return zeroPad(t.Second(), n)
	}
	// compileDatePattern rejects every other field.
	panic(fmt.Sprintf("l10n: unsupported date field %q", string(ch)))
}

// zeroPad is Python's '%0*d' % (width, v).
func zeroPad(v, width int) string {
	s := strconv.Itoa(v)
	if len(s) >= width {
		return s
	}
	if v < 0 {
		return "-" + strings.Repeat("0", width-len(s)) + s[1:]
	}
	return strings.Repeat("0", width-len(s)) + s
}

// formatPeriod is DateTimeFormat.format_period: a = AM/PM, B = flexible
// day period ("in the morning"), b = its stand-alone form.
func (l *Locale) formatPeriod(ch rune, n int, t time.Time) string {
	first := nameWidths[max(3, n)]
	var period, ctx string
	if ch == 'a' {
		period, ctx = "am", "format"
		if t.Hour() >= 12 {
			period = "pm"
		}
	} else {
		period, ctx = l.periodID(t), "stand-alone"
		if ch == 'B' {
			ctx = "format"
		}
	}
	for _, w := range [...]string{first, "wide", "narrow", "abbreviated"} {
		if name, ok := l.periods[ctx][w][period]; ok {
			return name
		}
	}
	return period // Babel raises; every CLDR locale names its periods
}

type periodCond struct {
	at, from, before, to             int
	hasAt, hasFrom, hasBefore, hasTo bool
}

type periodRule struct {
	id    string
	conds []periodCond
}

func newPeriodCond(m map[string]int) periodCond {
	var c periodCond
	c.at, c.hasAt = m["at"]
	c.from, c.hasFrom = m["from"]
	c.before, c.hasBefore = m["before"]
	c.to, c.hasTo = m["to"]
	return c
}

// periodID is babel.dates.get_period_id: "at" rules first, then the
// ranges, then plain am/pm.
func (l *Locale) periodID(t time.Time) string {
	secs := t.Hour()*3600 + t.Minute()*60 + t.Second()
	for _, r := range l.periodRules {
		for _, c := range r.conds {
			if c.hasAt && c.at == secs {
				return r.id
			}
		}
	}
	for _, r := range l.periodRules {
		for _, c := range r.conds {
			if c.hasFrom && c.hasBefore {
				if c.from < c.before {
					if c.from <= secs && secs < c.before {
						return r.id
					}
				} else if (c.from <= secs && secs < 86400) || (0 <= secs && secs < c.before) {
					return r.id
				}
			}
			startOK := c.hasFrom && secs >= c.from
			endOK := (c.hasTo && secs <= c.to) || (c.hasBefore && secs < c.before)
			if startOK && endOK {
				return r.id
			}
		}
	}
	if secs < 43200 {
		return "am"
	}
	return "pm"
}
