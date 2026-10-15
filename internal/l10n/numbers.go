// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Number formatting: a port of parse_pattern and NumberPattern.apply of
// babel/numbers.py (Babel 2.12.1, BSD-3-Clause, Copyright (c) 2013-2023 by
// the Babel Team), limited to the plain decimal patterns CLDR gives every
// locale (no scientific, significant-digit or currency forms).

package l10n

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// numberPattern is a parsed CLDR number pattern such as "#,##0.###".
type numberPattern struct {
	prefix, suffix [2]string // positive, negative
	grouping       [2]int    // primary, secondary group sizes
	intPrec        [2]int    // min, max integer digits
	fracPrec       [2]int    // min, max fraction digits
	hasNumber      bool      // the pattern has a number part
	scale          int       // 2 for percent, 3 for per mille
}

// splitNumberPattern is babel's number_re: a prefix of quoted text and
// characters other than [0-9@#.,], the number ([0-9@#.,E+]*) and a suffix.
func splitNumberPattern(s string) (prefix, number, suffix string) {
	i := 0
	for i < len(s) {
		if s[i] == '\'' {
			if j := strings.IndexByte(s[i+1:], '\''); j >= 0 {
				i += j + 2
				continue
			}
		} else if strings.IndexByte("0123456789@#.,", s[i]) >= 0 {
			break
		}
		i++
	}
	j := i
	for j < len(s) && strings.IndexByte("0123456789@#.,E+", s[j]) >= 0 {
		j++
	}
	return s[:i], s[i:j], s[j:]
}

// parseNumberPattern is babel.numbers.parse_pattern.
func parseNumberPattern(pattern string) (*numberPattern, error) {
	p := &numberPattern{}
	var number string
	if pos, neg, ok := strings.Cut(pattern, ";"); ok {
		p.prefix[0], number, p.suffix[0] = splitNumberPattern(pos)
		p.prefix[1], _, p.suffix[1] = splitNumberPattern(neg)
	} else {
		p.prefix[0], number, p.suffix[0] = splitNumberPattern(pattern)
		p.prefix[1], p.suffix[1] = "-"+p.prefix[0], p.suffix[0]
	}
	if strings.ContainsAny(number, "E@") {
		return nil, fmt.Errorf("number pattern %q: scientific and significant-digit patterns are not implemented", pattern)
	}
	integer, fraction := number, ""
	if i := strings.LastIndexByte(number, '.'); i >= 0 {
		integer, fraction = number[:i], number[i+1:]
	}
	p.intPrec = parsePrecision(integer)
	p.fracPrec = parsePrecision(fraction)
	p.grouping = parseGrouping(integer)
	p.hasNumber = number != ""
	affixes := p.prefix[0] + p.prefix[1] + p.suffix[0] + p.suffix[1]
	if strings.Contains(affixes, "%") {
		p.scale = 2
	} else if strings.Contains(affixes, "‰") {
		p.scale = 3
	}
	return p, nil
}

// parsePrecision counts the minimum ('0', '@') and maximum ('#' too)
// digits of a pattern part, up to the first other character but ','.
func parsePrecision(s string) [2]int {
	var prec [2]int
	for _, c := range s {
		switch c {
		case '@', '0':
			prec[0]++
			prec[1]++
		case '#':
			prec[1]++
		case ',':
		default:
			return prec
		}
	}
	return prec
}

// parseGrouping is babel.numbers.parse_grouping.
func parseGrouping(p string) [2]int {
	width := len(p)
	g1 := strings.LastIndexByte(p, ',')
	if g1 == -1 {
		return [2]int{1000, 1000}
	}
	g1 = width - g1 - 1
	g2 := strings.LastIndexByte(p[:width-g1-1], ',')
	if g2 == -1 {
		return [2]int{g1, g1}
	}
	return [2]int{g1, width - g1 - g2 - 2}
}

// decimal is the value Babel formats: decimal.Decimal(str(x)). A finite
// value is coef × 10^exp with coef free of leading and trailing zeros
// ("0" for zero), which is what Decimal.normalize leaves.
type decimal struct {
	neg  bool
	inf  bool
	nan  bool
	coef string
	exp  int
}

// decimalFromFloat converts through the shortest representation that
// round-trips, the digits of Python's repr(float).
func decimalFromFloat(x float64) decimal {
	switch {
	case math.IsNaN(x):
		return decimal{nan: true} // str(nan) is "nan", never signed
	case math.IsInf(x, 0):
		return decimal{neg: x < 0, inf: true}
	}
	d := decimal{neg: math.Signbit(x)}
	s := strconv.FormatFloat(math.Abs(x), 'e', -1, 64) // "1.2345e+03"
	mant, e, _ := strings.Cut(s, "e")
	exp, _ := strconv.Atoi(e)
	digits := strings.Replace(mant, ".", "", 1)
	d.coef, d.exp = digits, exp-(len(digits)-1)
	d.normalize()
	return d
}

// decimalFromUint is Decimal(str(n)) for an integer.
func decimalFromUint(n uint64) decimal {
	d := decimal{coef: strconv.FormatUint(n, 10)}
	d.normalize()
	return d
}

func (d *decimal) normalize() {
	if strings.Trim(d.coef, "0") == "" {
		d.coef, d.exp = "0", 0
		return
	}
	d.coef = strings.TrimLeft(d.coef, "0")
	t := strings.TrimRight(d.coef, "0")
	d.exp += len(d.coef) - len(t)
	d.coef = t
}

// quantize is Decimal.quantize(Decimal(10) ** -q) with ROUND_HALF_EVEN,
// formatted with "f" and split at the point: the integer digits and
// exactly q fraction digits. Unlike Python, it never fails for lack of
// precision (Python's context keeps 28 digits).
func (d decimal) quantize(q int) (integer, fraction string) {
	coef := d.coef
	if d.exp >= -q {
		coef += strings.Repeat("0", d.exp+q)
	} else {
		drop := -q - d.exp
		var kept, dropped string
		if drop >= len(coef) {
			dropped = strings.Repeat("0", drop-len(coef)) + coef
		} else {
			kept, dropped = coef[:len(coef)-drop], coef[len(coef)-drop:]
		}
		up := dropped[0] > '5'
		if dropped[0] == '5' {
			if strings.TrimRight(dropped[1:], "0") != "" {
				up = true
			} else if kept != "" {
				up = (kept[len(kept)-1]-'0')%2 == 1
			}
		}
		if up {
			kept = incrementDigits(kept)
		}
		if kept == "" {
			kept = "0"
		}
		coef = kept
	}
	if len(coef) < q+1 {
		coef = strings.Repeat("0", q+1-len(coef)) + coef
	}
	return coef[:len(coef)-q], coef[len(coef)-q:]
}

// incrementDigits adds one to a string of decimal digits ("" is zero).
func incrementDigits(s string) string {
	b := []byte(s)
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] < '9' {
			b[i]++
			return string(b)
		}
		b[i] = '0'
	}
	return "1" + string(b)
}

// formatNumber is NumberPattern.apply(value, locale) with decimal
// quantization and the fraction precision frac (Babel's frac_prec, which
// CMS overrides for durations and sizes).
func (l *Locale) formatNumber(p *numberPattern, d decimal, frac [2]int) string {
	if !d.nan && !d.inf && p.scale != 0 && d.coef != "0" {
		d.exp += p.scale
	}
	var number string
	switch {
	case d.inf:
		number = l.infSym
	case d.nan:
		// Python formats Decimal("NaN") as "NaN" (no fraction part) and
		// the integer and fraction steps run on it as on any number.
		number = l.formatInt("NaN", p) + l.formatFrac("0", frac)
	default:
		integer, fraction := d.quantize(frac[1])
		if fraction == "" {
			fraction = "0"
		}
		number = l.formatInt(integer, p) + l.formatFrac(fraction, frac)
	}
	neg := 0
	if d.neg {
		neg = 1
	}
	var b strings.Builder
	b.Grow(len(p.prefix[neg]) + len(number) + len(p.suffix[neg]))
	b.WriteString(p.prefix[neg])
	if p.hasNumber {
		b.WriteString(number)
	}
	b.WriteString(p.suffix[neg])
	return unquoteNumber(b.String())
}

// formatInt is NumberPattern._format_int: zero padding to the minimum
// integer digits and grouping.
func (l *Locale) formatInt(value string, p *numberPattern) string {
	if len(value) < p.intPrec[0] {
		value = strings.Repeat("0", p.intPrec[0]-len(value)) + value
	}
	gsize := p.grouping[0]
	if len(value) <= gsize {
		return value
	}
	var groups []string
	for len(value) > gsize {
		groups = append(groups, value[len(value)-gsize:])
		value = value[:len(value)-gsize]
		gsize = p.grouping[1]
	}
	var b strings.Builder
	b.WriteString(value)
	for i := len(groups) - 1; i >= 0; i-- {
		b.WriteString(l.groupSym)
		b.WriteString(groups[i])
	}
	return b.String()
}

// formatFrac is NumberPattern._format_frac.
func (l *Locale) formatFrac(value string, frac [2]int) string {
	lo, hi := frac[0], frac[1]
	if len(value) < lo {
		value += strings.Repeat("0", lo-len(value))
	}
	if hi == 0 || (lo == 0 && strings.Trim(value, "0") == "") {
		return ""
	}
	for len(value) > lo && value[len(value)-1] == '0' {
		value = value[:len(value)-1]
	}
	return l.decSym + value
}

// unquoteNumber is the final re.sub(r"'([^']*)'", ...) of apply: quoted
// text loses its quotes and a doubled quote becomes one.
func unquoteNumber(s string) string {
	if !strings.Contains(s, "'") {
		return s
	}
	var b strings.Builder
	for {
		i := strings.IndexByte(s, '\'')
		if i < 0 {
			break
		}
		j := strings.IndexByte(s[i+1:], '\'')
		if j < 0 {
			break
		}
		b.WriteString(s[:i])
		if j == 0 {
			b.WriteByte('\'')
		} else {
			b.WriteString(s[i+1 : i+1+j])
		}
		s = s[i+j+2:]
	}
	b.WriteString(s)
	return b.String()
}
