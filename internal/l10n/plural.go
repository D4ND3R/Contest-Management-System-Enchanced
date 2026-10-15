// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// CLDR plural rules as babel/plural.py evaluates them (Babel 2.12.1,
// BSD-3-Clause, Copyright (c) 2013-2023 by the Babel Team): the operands
// of extract_operands, the in/within relations and Java-style modulo.

package l10n

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// The CLDR plural categories.
const (
	PluralZero  = "zero"
	PluralOne   = "one"
	PluralTwo   = "two"
	PluralFew   = "few"
	PluralMany  = "many"
	PluralOther = "other"
)

// PluralForm returns the CLDR plural category of n in this language
// ("one", "few", "other"...), as Babel's locale.plural_form(n) does for
// a Python float or int. It is the category CMS uses to pick a unit
// pattern ("1 second", "2 seconds").
func (l *Locale) PluralForm(n float64) string {
	op, ok := operandsFromFloat(n)
	if !ok {
		return PluralOther // Babel raises for NaN and infinities
	}
	return l.plural.form(&op)
}

// PluralFormInt is PluralForm for an integer.
func (l *Locale) PluralFormInt(n int64) string {
	op := operandsFromUint(absInt(n))
	return l.plural.form(&op)
}

func absInt(n int64) uint64 {
	if n < 0 {
		return uint64(-(n + 1)) + 1
	}
	return uint64(n)
}

// operands are the values of extract_operands: n (its integer part and
// whether it has a fraction), i, and v, w, f, t computed as Babel does.
type operands struct {
	intDigits string // decimal digits of i
	i         uint64 // i, when it fits
	iBig      bool   // i does not fit in a uint64
	frac      bool   // n != i
	v, w      uint64
	f, t      uint64
}

func operandsFromUint(n uint64) operands {
	return operands{intDigits: strconv.FormatUint(n, 10), i: n}
}

// operandsFromFloat is extract_operands(float): an integral value is an
// int; otherwise Decimal(str(n)), whose fraction digits Babel takes as
// digits[exp:] — the last -exp digits of the coefficient, so 0.05 has
// v = 1 and f = 5 (not 2 and 5).
func operandsFromFloat(x float64) (operands, bool) {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return operands{}, false
	}
	n := math.Abs(x)
	ip := math.Trunc(n)
	op := operands{intDigits: strconv.FormatFloat(ip, 'f', 0, 64)}
	if u, err := strconv.ParseUint(op.intDigits, 10, 64); err == nil {
		op.i = u
	} else {
		op.iBig = true
	}
	if ip == n {
		return op, true
	}
	op.frac = true
	d := decimalFromFloat(n) // never zero or integral here, so exp < 0
	trailing := d.coef
	if len(d.coef)+d.exp > 0 {
		trailing = d.coef[len(d.coef)+d.exp:]
	}
	noTrailing := strings.TrimRight(trailing, "0")
	op.v, op.w = uint64(len(trailing)), uint64(len(noTrailing))
	op.f, _ = strconv.ParseUint(trailing, 10, 64)
	if noTrailing != "" {
		op.t, _ = strconv.ParseUint(noTrailing, 10, 64)
	}
	return op, true
}

type pluralRule struct {
	tag  string
	cond pluralCond
}

// pluralRules are tried in order (Babel sorts them by tag); the first
// match wins and "other" is the default.
type pluralRules []pluralRule

func (r pluralRules) form(op *operands) string {
	for _, rule := range r {
		if rule.cond.eval(op) {
			return rule.tag
		}
	}
	return PluralOther
}

type pluralCond interface {
	eval(op *operands) bool
}

type orCond struct{ a, b pluralCond }

func (c orCond) eval(op *operands) bool { return c.a.eval(op) || c.b.eval(op) }

type andCond struct{ a, b pluralCond }

func (c andCond) eval(op *operands) bool { return c.a.eval(op) && c.b.eval(op) }

// relation is "expr = ranges", "expr != ranges", "expr within ranges" or
// "expr not within ranges", with expr an operand and an optional modulo.
type relation struct {
	operand byte   // n i v w f t c e
	mod     uint64 // 0: none
	within  bool
	negated bool
	ranges  [][2]uint64
}

// value is an operand after the modulo: an integer (huge when big) and
// whether a fraction follows it (only for n).
type value struct {
	u    uint64
	big  bool
	frac bool
}

func (r *relation) value(op *operands) value {
	var v value
	switch r.operand {
	case 'n':
		v = value{u: op.i, big: op.iBig, frac: op.frac}
	case 'i':
		v = value{u: op.i, big: op.iBig}
	case 'v':
		v.u = op.v
	case 'w':
		v.u = op.w
	case 'f':
		v.u = op.f
	case 't':
		v.u = op.t
	}
	// c and e are always 0 in Babel.
	if r.mod != 0 {
		if v.big {
			v.u, v.big = digitsMod(op.intDigits, r.mod), false
		} else {
			v.u %= r.mod
		}
	}
	return v
}

func digitsMod(digits string, m uint64) uint64 {
	var r uint64
	for i := 0; i < len(digits); i++ {
		r = (r*10 + uint64(digits[i]-'0')) % m
	}
	return r
}

// eval is in_range_list (the value is an integer in a range) or
// within_range_list (lo <= value <= hi, fraction included).
func (r relation) eval(op *operands) bool {
	v := r.value(op)
	ok := false
	if r.within || !v.frac {
		for _, rg := range r.ranges {
			lo, hi := rg[0], rg[1]
			geLo := v.big || v.u >= lo
			leHi := !v.big && (v.u < hi || (v.u == hi && !v.frac))
			if geLo && leHi {
				ok = true
				break
			}
		}
	}
	return ok != r.negated
}

// parsePluralRule parses the rules tools/l10n/extract.py writes:
//
//	condition = and ("or" and)*
//	and       = relation ("and" relation)*
//	relation  = expr ("=" | "!=" | "within" | "not within") ranges
//	expr      = operand ("%" value)?
//	ranges    = value (".." value)? ("," value (".." value)?)*
func parsePluralRule(s string) (pluralCond, error) {
	p := &ruleParser{toks: tokenizeRule(s)}
	c, err := p.condition()
	if err != nil {
		return nil, err
	}
	if p.pos < len(p.toks) {
		return nil, fmt.Errorf("unexpected %q", p.toks[p.pos])
	}
	return c, nil
}

func tokenizeRule(s string) []string {
	var toks []string
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ':
			i++
		case c >= '0' && c <= '9':
			j := i
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				j++
			}
			toks = append(toks, s[i:j])
			i = j
		case c >= 'a' && c <= 'z':
			j := i
			for j < len(s) && s[j] >= 'a' && s[j] <= 'z' {
				j++
			}
			toks = append(toks, s[i:j])
			i = j
		case strings.HasPrefix(s[i:], ".."), strings.HasPrefix(s[i:], "!="):
			toks = append(toks, s[i:i+2])
			i += 2
		default:
			toks = append(toks, s[i:i+1])
			i++
		}
	}
	return toks
}

type ruleParser struct {
	toks []string
	pos  int
}

func (p *ruleParser) peek() string {
	if p.pos < len(p.toks) {
		return p.toks[p.pos]
	}
	return ""
}

func (p *ruleParser) skip(tok string) bool {
	if p.peek() == tok {
		p.pos++
		return true
	}
	return false
}

func (p *ruleParser) condition() (pluralCond, error) {
	c, err := p.and()
	for err == nil && p.skip("or") {
		var r pluralCond
		if r, err = p.and(); err == nil {
			c = orCond{c, r}
		}
	}
	return c, err
}

func (p *ruleParser) and() (pluralCond, error) {
	c, err := p.relation()
	for err == nil && p.skip("and") {
		var r pluralCond
		if r, err = p.relation(); err == nil {
			c = andCond{c, r}
		}
	}
	return c, err
}

func (p *ruleParser) relation() (pluralCond, error) {
	var r relation
	op := p.peek()
	if len(op) != 1 || !strings.Contains("nivwftce", op) {
		return nil, fmt.Errorf("expected an operand, got %q", op)
	}
	p.pos++
	r.operand = op[0]
	if p.skip("%") {
		m, err := p.value()
		if err != nil {
			return nil, err
		}
		if m == 0 {
			return nil, fmt.Errorf("modulo by zero")
		}
		r.mod = m
	}
	switch {
	case p.skip("="):
	case p.skip("!="):
		r.negated = true
	case p.skip("within"):
		r.within = true
	case p.skip("not"):
		if !p.skip("within") {
			return nil, fmt.Errorf("expected within after not, got %q", p.peek())
		}
		r.within, r.negated = true, true
	default:
		return nil, fmt.Errorf("expected a relation, got %q", p.peek())
	}
	for {
		lo, err := p.value()
		if err != nil {
			return nil, err
		}
		hi := lo
		if p.skip("..") {
			if hi, err = p.value(); err != nil {
				return nil, err
			}
		}
		r.ranges = append(r.ranges, [2]uint64{lo, hi})
		if !p.skip(",") {
			return r, nil
		}
	}
}

func (p *ruleParser) value() (uint64, error) {
	v, err := strconv.ParseUint(p.peek(), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("expected a number, got %q", p.peek())
	}
	p.pos++
	return v, nil
}
