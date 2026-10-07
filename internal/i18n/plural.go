// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package i18n

import (
	"fmt"
	"strconv"
	"strings"
)

// PluralFunc maps a count to the index of the plural form.
type PluralFunc func(n int64) int

// ParsePluralForms parses a gettext Plural-Forms header such as
// "nplurals=2; plural=(n != 1);". The expression is the C subset gettext
// allows: n, integers, ?:, ||, &&, comparisons, + - * / %, ! and parens.
func ParsePluralForms(header string) (int, PluralFunc, error) {
	nplurals := 0
	expr := ""
	for _, part := range strings.Split(header, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "nplurals":
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil || n < 1 {
				return 0, nil, fmt.Errorf("bad nplurals %q", v)
			}
			nplurals = n
		case "plural":
			expr = strings.TrimSpace(v)
		}
	}
	if nplurals == 0 || expr == "" {
		return 0, nil, fmt.Errorf("incomplete Plural-Forms %q", header)
	}
	p := &pluralParser{src: expr}
	p.next()
	e, err := p.ternary()
	if err != nil {
		return 0, nil, fmt.Errorf("plural expression %q: %w", expr, err)
	}
	if p.tok != "" {
		return 0, nil, fmt.Errorf("plural expression %q: unexpected %q", expr, p.tok)
	}
	np := nplurals
	return nplurals, func(n int64) int {
		if n < 0 {
			n = -n
		}
		i := int(e(n))
		if i < 0 || i >= np {
			return 0
		}
		return i
	}, nil
}

type pexpr func(n int64) int64

type pluralParser struct {
	src string
	pos int
	tok string
}

func (p *pluralParser) next() {
	for p.pos < len(p.src) && (p.src[p.pos] == ' ' || p.src[p.pos] == '\t') {
		p.pos++
	}
	if p.pos >= len(p.src) {
		p.tok = ""
		return
	}
	c := p.src[p.pos]
	if c >= '0' && c <= '9' {
		start := p.pos
		for p.pos < len(p.src) && p.src[p.pos] >= '0' && p.src[p.pos] <= '9' {
			p.pos++
		}
		p.tok = p.src[start:p.pos]
		return
	}
	if p.pos+1 < len(p.src) {
		switch two := p.src[p.pos : p.pos+2]; two {
		case "==", "!=", "<=", ">=", "&&", "||":
			p.pos += 2
			p.tok = two
			return
		}
	}
	p.pos++
	p.tok = string(c)
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func (p *pluralParser) ternary() (pexpr, error) {
	cond, err := p.or()
	if err != nil {
		return nil, err
	}
	if p.tok != "?" {
		return cond, nil
	}
	p.next()
	a, err := p.ternary()
	if err != nil {
		return nil, err
	}
	if p.tok != ":" {
		return nil, fmt.Errorf("expected ':'")
	}
	p.next()
	b, err := p.ternary()
	if err != nil {
		return nil, err
	}
	return func(n int64) int64 {
		if cond(n) != 0 {
			return a(n)
		}
		return b(n)
	}, nil
}

func (p *pluralParser) binary(sub func() (pexpr, error), ops ...string) (pexpr, error) {
	left, err := sub()
	if err != nil {
		return nil, err
	}
	for {
		op := ""
		for _, o := range ops {
			if p.tok == o {
				op = o
			}
		}
		if op == "" {
			return left, nil
		}
		p.next()
		right, err := sub()
		if err != nil {
			return nil, err
		}
		l, r := left, right
		switch op {
		case "||":
			left = func(n int64) int64 { return b2i(l(n) != 0 || r(n) != 0) }
		case "&&":
			left = func(n int64) int64 { return b2i(l(n) != 0 && r(n) != 0) }
		case "==":
			left = func(n int64) int64 { return b2i(l(n) == r(n)) }
		case "!=":
			left = func(n int64) int64 { return b2i(l(n) != r(n)) }
		case "<":
			left = func(n int64) int64 { return b2i(l(n) < r(n)) }
		case ">":
			left = func(n int64) int64 { return b2i(l(n) > r(n)) }
		case "<=":
			left = func(n int64) int64 { return b2i(l(n) <= r(n)) }
		case ">=":
			left = func(n int64) int64 { return b2i(l(n) >= r(n)) }
		case "+":
			left = func(n int64) int64 { return l(n) + r(n) }
		case "-":
			left = func(n int64) int64 { return l(n) - r(n) }
		case "*":
			left = func(n int64) int64 { return l(n) * r(n) }
		case "/":
			left = func(n int64) int64 {
				if d := r(n); d != 0 {
					return l(n) / d
				}
				return 0
			}
		case "%":
			left = func(n int64) int64 {
				if d := r(n); d != 0 {
					return l(n) % d
				}
				return 0
			}
		}
	}
}

func (p *pluralParser) or() (pexpr, error)  { return p.binary(p.and, "||") }
func (p *pluralParser) and() (pexpr, error) { return p.binary(p.eq, "&&") }
func (p *pluralParser) eq() (pexpr, error)  { return p.binary(p.rel, "==", "!=") }
func (p *pluralParser) rel() (pexpr, error) { return p.binary(p.add, "<", ">", "<=", ">=") }
func (p *pluralParser) add() (pexpr, error) { return p.binary(p.mul, "+", "-") }
func (p *pluralParser) mul() (pexpr, error) { return p.binary(p.unary, "*", "/", "%") }

func (p *pluralParser) unary() (pexpr, error) {
	if p.tok == "!" {
		p.next()
		e, err := p.unary()
		if err != nil {
			return nil, err
		}
		return func(n int64) int64 { return b2i(e(n) == 0) }, nil
	}
	return p.primary()
}

func (p *pluralParser) primary() (pexpr, error) {
	switch {
	case p.tok == "n":
		p.next()
		return func(n int64) int64 { return n }, nil
	case p.tok == "(":
		p.next()
		e, err := p.ternary()
		if err != nil {
			return nil, err
		}
		if p.tok != ")" {
			return nil, fmt.Errorf("expected ')'")
		}
		p.next()
		return e, nil
	case p.tok != "" && p.tok[0] >= '0' && p.tok[0] <= '9':
		v, err := strconv.ParseInt(p.tok, 10, 64)
		if err != nil {
			return nil, err
		}
		p.next()
		return func(int64) int64 { return v }, nil
	case p.tok == "":
		return nil, fmt.Errorf("unexpected end")
	default:
		return nil, fmt.Errorf("unexpected %q", p.tok)
	}
}
