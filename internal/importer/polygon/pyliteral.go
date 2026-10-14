// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package polygon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Upstream executes files/cms_conf.py as a Python module and reads its
// variables general and datasets. Executing code of a package is not an
// option here (and there is no Python), so the file is read as a small
// subset of Python that covers what these files hold: assignments
// (`name = value`, `name["key"] = value`) of literals (numbers, strings,
// True, False, None, lists, tuples, dictionaries), arithmetic on numbers
// (+ - * / // % **), names assigned before, timedelta(...) and
// datetime.timedelta(...), dict(...), float(...) and int(...); import
// lines are ignored. Anything else is an error that names the line.

type pyKind uint8

const (
	pyNone pyKind = iota
	pyBool
	pyInt
	pyFloat
	pyStr
	pyList
	pyTuple
	pyDict
	pyTimedelta
	pyFunc
	pyModule
)

var pyKindNames = [...]string{"None", "bool", "int", "float", "str", "list", "tuple", "dict", "timedelta", "function", "module"}

func (k pyKind) String() string { return pyKindNames[k] }

// pyValue is a Python value.
type pyValue struct {
	kind pyKind
	// i is an int, a bool (0 or 1), or the microseconds of a timedelta.
	i int64
	f float64
	// s is a str, or the name of a function or module.
	s string
	// items are those of a list or tuple, or the keys of a dict.
	items []pyValue
	// vals are the values of a dict, in the order of items.
	vals []pyValue
}

var (
	pyNoneValue  = pyValue{kind: pyNone}
	pyTrueValue  = pyValue{kind: pyBool, i: 1}
	pyFalseValue = pyValue{kind: pyBool}
)

func pyStrValue(s string) pyValue { return pyValue{kind: pyStr, s: s} }

// isNumber: bool, int or float (bool is an int in Python).
func (v pyValue) isNumber() bool { return v.kind == pyBool || v.kind == pyInt || v.kind == pyFloat }

func (v pyValue) float() float64 {
	if v.kind == pyFloat {
		return v.f
	}
	return float64(v.i)
}

// get looks a key up in a dict.
func (v pyValue) get(key pyValue) (pyValue, bool) {
	for i, k := range v.items {
		if pyEqual(k, key) {
			return v.vals[i], true
		}
	}
	return pyValue{}, false
}

// set sets a key of a dict (in place: dicts are shared, as in Python).
func (v *pyValue) set(key, val pyValue) {
	for i, k := range v.items {
		if pyEqual(k, key) {
			v.vals[i] = val
			return
		}
	}
	v.items = append(v.items, key)
	v.vals = append(v.vals, val)
}

// pyEqual compares keys: numbers by value (1 == 1.0 == True), strings by
// content.
func pyEqual(a, b pyValue) bool {
	switch {
	case a.isNumber() && b.isNumber():
		if a.kind == pyFloat || b.kind == pyFloat {
			return a.float() == b.float()
		}
		return a.i == b.i
	case a.kind != b.kind:
		return false
	case a.kind == pyStr:
		return a.s == b.s
	case a.kind == pyNone:
		return true
	case a.kind == pyTuple:
		if len(a.items) != len(b.items) {
			return false
		}
		for i := range a.items {
			if !pyEqual(a.items[i], b.items[i]) {
				return false
			}
		}
		return true
	}
	return false
}

// hashable reports whether v can be a dict key.
func hashable(v pyValue) bool {
	switch v.kind {
	case pyList, pyDict:
		return false
	case pyTuple:
		for _, it := range v.items {
			if !hashable(it) {
				return false
			}
		}
	}
	return true
}

// pyError is an error at a line of the file.
type pyError struct {
	line int
	msg  string
}

func (e *pyError) Error() string { return fmt.Sprintf("line %d: %s", e.line, e.msg) }

// ---- Tokens ----

type tokKind uint8

const (
	tokEOF tokKind = iota
	tokNewline
	tokName
	tokNumber
	tokString
	tokOp
)

type token struct {
	kind tokKind
	text string // name, operator, number literal
	val  string // decoded string
	line int
}

// tokenize splits src into tokens: newlines inside brackets and after a
// backslash are dropped, as are comments.
func tokenize(src string) ([]token, error) {
	var toks []token
	line, depth := 1, 0
	i := 0
	errf := func(format string, args ...any) error {
		return &pyError{line, fmt.Sprintf(format, args...)}
	}
	if strings.HasPrefix(src, "\uFEFF") {
		i = len("\uFEFF")
	}
	for i < len(src) {
		c := src[i]
		switch {
		case c == '\n':
			if depth == 0 && len(toks) > 0 && toks[len(toks)-1].kind != tokNewline {
				toks = append(toks, token{kind: tokNewline, line: line})
			}
			line++
			i++
		case c == ' ' || c == '\t' || c == '\r' || c == '\f':
			i++
		case c == '#':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '\\':
			if i+1 < len(src) && src[i+1] == '\n' {
				i += 2
				line++
			} else if i+2 < len(src) && src[i+1] == '\r' && src[i+2] == '\n' {
				i += 3
				line++
			} else {
				return nil, errf("unexpected character after line continuation")
			}
		case c == ';':
			if depth == 0 {
				toks = append(toks, token{kind: tokNewline, line: line})
			} else {
				return nil, errf("invalid syntax: ';'")
			}
			i++
		case isNameStart(src[i:]):
			j := i
			for j < len(src) {
				r, size := utf8.DecodeRuneInString(src[j:])
				if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
					break
				}
				j += size
			}
			name := src[i:j]
			// String prefixes.
			if j < len(src) && (src[j] == '\'' || src[j] == '"') {
				p := strings.ToLower(name)
				if p == "r" || p == "u" {
					s, n, nl, err := readString(src[j:], p == "r")
					if err != nil {
						return nil, errf("%v", err)
					}
					toks = append(toks, token{kind: tokString, val: s, line: line})
					line += nl
					i = j + n
					continue
				}
				if strings.ContainsAny(p, "bf") && len(p) <= 2 {
					return nil, errf("%s-strings are not supported", name)
				}
			}
			toks = append(toks, token{kind: tokName, text: name, line: line})
			i = j
		case c >= '0' && c <= '9' || c == '.' && i+1 < len(src) && src[i+1] >= '0' && src[i+1] <= '9':
			j := i
			for j < len(src) && (isAlnum(src[j]) || src[j] == '.' || src[j] == '_' ||
				(src[j] == '+' || src[j] == '-') && (src[j-1] == 'e' || src[j-1] == 'E') && !strings.HasPrefix(strings.ToLower(src[i:j]), "0x")) {
				j++
			}
			toks = append(toks, token{kind: tokNumber, text: src[i:j], line: line})
			i = j
		case c == '\'' || c == '"':
			s, n, nl, err := readString(src[i:], false)
			if err != nil {
				return nil, errf("%v", err)
			}
			toks = append(toks, token{kind: tokString, val: s, line: line})
			line += nl
			i += n
		default:
			op := ""
			for _, o := range []string{"**", "//", "{", "}", "[", "]", "(", ")", ",", ":", "=", "+", "-", "*", "/", "%", "."} {
				if strings.HasPrefix(src[i:], o) {
					op = o
					break
				}
			}
			if op == "" || strings.HasPrefix(src[i:], "==") {
				r, _ := utf8.DecodeRuneInString(src[i:])
				return nil, errf("unsupported character %q", r)
			}
			switch op {
			case "{", "[", "(":
				depth++
			case "}", "]", ")":
				if depth == 0 {
					return nil, errf("unmatched %q", op)
				}
				depth--
			}
			toks = append(toks, token{kind: tokOp, text: op, line: line})
			i += len(op)
		}
	}
	if depth != 0 {
		return nil, &pyError{line, "unexpected end of file: a bracket is not closed"}
	}
	toks = append(toks, token{kind: tokNewline, line: line}, token{kind: tokEOF, line: line})
	return toks, nil
}

func isAlnum(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isNameStart(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return r == '_' || unicode.IsLetter(r)
}

// readString decodes the string literal at the start of s (a quote or a
// triple quote): its value, its length in s and the newlines in it.
func readString(s string, raw bool) (string, int, int, error) {
	q := s[:1]
	if strings.HasPrefix(s, q+q+q) {
		q = q + q + q
	}
	var b strings.Builder
	i := len(q)
	lines := 0
	for {
		if i >= len(s) {
			return "", 0, 0, fmt.Errorf("unterminated string literal")
		}
		if strings.HasPrefix(s[i:], q) {
			return b.String(), i + len(q), lines, nil
		}
		c := s[i]
		if c == '\n' {
			if len(q) == 1 {
				return "", 0, 0, fmt.Errorf("unterminated string literal")
			}
			lines++
		}
		if c != '\\' {
			b.WriteByte(c)
			i++
			continue
		}
		if i+1 >= len(s) {
			return "", 0, 0, fmt.Errorf("unterminated string literal")
		}
		e := s[i+1]
		if raw {
			// The backslash stays; it only keeps a quote from ending the
			// string.
			b.WriteByte('\\')
			b.WriteByte(e)
			if e == '\n' {
				lines++
			}
			i += 2
			continue
		}
		i += 2
		switch e {
		case '\n':
			lines++ // line continuation
		case '\\', '\'', '"':
			b.WriteByte(e)
		case 'a':
			b.WriteByte('\a')
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'v':
			b.WriteByte('\v')
		case 'x', 'u', 'U':
			n := map[byte]int{'x': 2, 'u': 4, 'U': 8}[e]
			if i+n > len(s) {
				return "", 0, 0, fmt.Errorf("truncated \\%c escape", e)
			}
			code, err := strconv.ParseUint(s[i:i+n], 16, 32)
			if err != nil || code > unicode.MaxRune {
				return "", 0, 0, fmt.Errorf("invalid \\%c escape", e)
			}
			b.WriteRune(rune(code))
			i += n
		case '0', '1', '2', '3', '4', '5', '6', '7':
			j := i - 1
			for j < i+2 && j < len(s) && s[j] >= '0' && s[j] <= '7' {
				j++
			}
			code, _ := strconv.ParseUint(s[i-1:j], 8, 32)
			b.WriteRune(rune(code))
			i = j
		default:
			// Unknown escapes keep the backslash.
			b.WriteByte('\\')
			b.WriteByte(e)
		}
	}
}

// ---- Parser and evaluator ----

// pyModuleVars runs the assignments of src and returns the variables.
func pyModuleVars(src string) (map[string]pyValue, error) {
	toks, err := tokenize(src)
	if err != nil {
		return nil, err
	}
	p := &pyParser{toks: toks, vars: map[string]pyValue{}}
	for p.peek().kind != tokEOF {
		if err := p.statement(); err != nil {
			return nil, err
		}
	}
	return p.vars, nil
}

type pyParser struct {
	toks []token
	pos  int
	vars map[string]pyValue
}

func (p *pyParser) peek() token { return p.toks[p.pos] }

func (p *pyParser) next() token {
	t := p.toks[p.pos]
	if t.kind != tokEOF {
		p.pos++
	}
	return t
}

func (p *pyParser) isOp(op string) bool {
	t := p.peek()
	return t.kind == tokOp && t.text == op
}

func (p *pyParser) errf(format string, args ...any) error {
	return &pyError{p.peek().line, fmt.Sprintf(format, args...)}
}

func (p *pyParser) expect(op string) error {
	if !p.isOp(op) {
		return p.errf("expected %q", op)
	}
	p.next()
	return nil
}

func (p *pyParser) statement() error {
	t := p.peek()
	if t.kind == tokNewline {
		p.next()
		return nil
	}
	if t.kind == tokName && (t.text == "import" || t.text == "from") {
		// import datetime, from datetime import timedelta...: the names
		// that matter are built in.
		for p.peek().kind != tokNewline {
			p.next()
		}
		return nil
	}
	if t.kind != tokName || isKeyword(t.text) {
		return p.errf("unsupported statement: only assignments of values are read")
	}
	// target: NAME ('[' expr ']')*
	name := p.next().text
	var keys []pyValue
	for p.isOp("[") {
		p.next()
		k, err := p.expr()
		if err != nil {
			return err
		}
		if err := p.expect("]"); err != nil {
			return err
		}
		keys = append(keys, k)
	}
	if !p.isOp("=") {
		return p.errf("unsupported statement: only assignments of values are read")
	}
	p.next()
	val, err := p.expr()
	if err != nil {
		return err
	}
	if p.isOp(",") { // x = 1, 2 is a tuple
		items := []pyValue{val}
		for p.isOp(",") {
			p.next()
			if p.peek().kind == tokNewline {
				break
			}
			v, err := p.expr()
			if err != nil {
				return err
			}
			items = append(items, v)
		}
		val = pyValue{kind: pyTuple, items: items}
	}
	if p.peek().kind != tokNewline {
		return p.errf("invalid syntax")
	}
	line := t.line
	p.next()
	if len(keys) == 0 {
		p.vars[name] = val
		return nil
	}
	target, ok := p.vars[name]
	if !ok {
		return &pyError{line, fmt.Sprintf("name %q is not defined", name)}
	}
	// Walk to the container of the last key. Dicts and lists are shared
	// values in Python: the slices here are too, as long as they are not
	// appended to by another name (a new key appends, see below).
	ptr := &target
	for i, k := range keys {
		last := i == len(keys)-1
		switch ptr.kind {
		case pyDict:
			if !hashable(k) {
				return &pyError{line, fmt.Sprintf("unhashable key of type %s", k.kind)}
			}
			if last {
				ptr.set(k, val)
				break
			}
			idx := -1
			for j, kk := range ptr.items {
				if pyEqual(kk, k) {
					idx = j
				}
			}
			if idx < 0 {
				return &pyError{line, fmt.Sprintf("KeyError: %s", pyRepr(k))}
			}
			ptr = &ptr.vals[idx]
		case pyList:
			idx, err := listIndex(*ptr, k)
			if err != nil {
				return &pyError{line, err.Error()}
			}
			if last {
				ptr.items[idx] = val
				break
			}
			ptr = &ptr.items[idx]
		default:
			return &pyError{line, fmt.Sprintf("'%s' object does not support item assignment", ptr.kind)}
		}
	}
	p.vars[name] = target
	return nil
}

func isKeyword(s string) bool {
	switch s {
	case "if", "else", "elif", "for", "while", "def", "class", "return", "with", "try", "except",
		"lambda", "del", "global", "nonlocal", "assert", "pass", "raise", "yield", "and", "or", "not", "in", "is":
		return true
	}
	return false
}

// expr: term (('+'|'-') term)*
func (p *pyParser) expr() (pyValue, error) {
	v, err := p.term()
	if err != nil {
		return v, err
	}
	for p.isOp("+") || p.isOp("-") {
		op := p.next()
		w, err := p.term()
		if err != nil {
			return v, err
		}
		if v, err = binary(op.text, v, w); err != nil {
			return v, &pyError{op.line, err.Error()}
		}
	}
	return v, nil
}

// term: factor (('*'|'/'|'//'|'%') factor)*
func (p *pyParser) term() (pyValue, error) {
	v, err := p.factor()
	if err != nil {
		return v, err
	}
	for p.isOp("*") || p.isOp("/") || p.isOp("//") || p.isOp("%") {
		op := p.next()
		w, err := p.factor()
		if err != nil {
			return v, err
		}
		if v, err = binary(op.text, v, w); err != nil {
			return v, &pyError{op.line, err.Error()}
		}
	}
	return v, nil
}

// factor: ('+'|'-') factor | power
func (p *pyParser) factor() (pyValue, error) {
	if p.isOp("-") || p.isOp("+") {
		op := p.next()
		v, err := p.factor()
		if err != nil {
			return v, err
		}
		switch {
		case op.text == "+" && (v.isNumber() || v.kind == pyTimedelta):
			if v.kind == pyBool {
				v.kind = pyInt
			}
			return v, nil
		case v.kind == pyFloat:
			return pyValue{kind: pyFloat, f: -v.f}, nil
		case v.kind == pyInt || v.kind == pyBool || v.kind == pyTimedelta:
			if v.i == math.MinInt64 {
				return v, &pyError{op.line, "integer too large"}
			}
			k := v.kind
			if k == pyBool {
				k = pyInt
			}
			return pyValue{kind: k, i: -v.i}, nil
		}
		return v, &pyError{op.line, fmt.Sprintf("bad operand type for unary %s: '%s'", op.text, v.kind)}
	}
	return p.power()
}

// power: primary ['**' factor]
func (p *pyParser) power() (pyValue, error) {
	v, err := p.primary()
	if err != nil {
		return v, err
	}
	if p.isOp("**") {
		op := p.next()
		w, err := p.factor()
		if err != nil {
			return v, err
		}
		if v, err = binary("**", v, w); err != nil {
			return v, &pyError{op.line, err.Error()}
		}
	}
	return v, nil
}

// primary: atom trailer*
func (p *pyParser) primary() (pyValue, error) {
	v, err := p.atom()
	if err != nil {
		return v, err
	}
	for {
		switch {
		case p.isOp("."):
			p.next()
			t := p.next()
			if t.kind != tokName {
				return v, &pyError{t.line, "invalid syntax after '.'"}
			}
			if v.kind != pyModule || v.s != "datetime" || t.text != "timedelta" {
				return v, &pyError{t.line, fmt.Sprintf("unsupported attribute %q", t.text)}
			}
			v = pyValue{kind: pyFunc, s: "timedelta"}
		case p.isOp("("):
			line := p.next().line
			args, kwargs, err := p.callArgs()
			if err != nil {
				return v, err
			}
			if v, err = call(v, args, kwargs); err != nil {
				return v, &pyError{line, err.Error()}
			}
		case p.isOp("["):
			line := p.next().line
			k, err := p.expr()
			if err != nil {
				return v, err
			}
			if err := p.expect("]"); err != nil {
				return v, err
			}
			switch v.kind {
			case pyDict:
				got, ok := v.get(k)
				if !ok {
					return v, &pyError{line, fmt.Sprintf("KeyError: %s", pyRepr(k))}
				}
				v = got
			case pyList, pyTuple:
				idx, err := listIndex(v, k)
				if err != nil {
					return v, &pyError{line, err.Error()}
				}
				v = v.items[idx]
			default:
				return v, &pyError{line, fmt.Sprintf("'%s' object is not subscriptable", v.kind)}
			}
		default:
			return v, nil
		}
	}
}

func listIndex(v, k pyValue) (int, error) {
	if k.kind != pyInt && k.kind != pyBool {
		return 0, fmt.Errorf("%s indices must be integers, not %s", v.kind, k.kind)
	}
	idx := k.i
	if idx < 0 {
		idx += int64(len(v.items))
	}
	if idx < 0 || idx >= int64(len(v.items)) {
		return 0, fmt.Errorf("%s index out of range", v.kind)
	}
	return int(idx), nil
}

type kwarg struct {
	name string
	val  pyValue
}

// callArgs reads the arguments of a call, after "(".
func (p *pyParser) callArgs() ([]pyValue, []kwarg, error) {
	var args []pyValue
	var kwargs []kwarg
	for !p.isOp(")") {
		if t := p.peek(); t.kind == tokName && p.toks[p.pos+1].kind == tokOp && p.toks[p.pos+1].text == "=" {
			p.next()
			p.next()
			v, err := p.expr()
			if err != nil {
				return nil, nil, err
			}
			kwargs = append(kwargs, kwarg{t.text, v})
		} else {
			if len(kwargs) > 0 {
				return nil, nil, p.errf("positional argument follows keyword argument")
			}
			v, err := p.expr()
			if err != nil {
				return nil, nil, err
			}
			args = append(args, v)
		}
		if !p.isOp(",") {
			break
		}
		p.next()
	}
	return args, kwargs, p.expect(")")
}

func (p *pyParser) atom() (pyValue, error) {
	t := p.next()
	switch t.kind {
	case tokNumber:
		v, err := parseNumber(t.text)
		if err != nil {
			return v, &pyError{t.line, err.Error()}
		}
		return v, nil
	case tokString:
		s := t.val
		for p.peek().kind == tokString { // "a" "b" is "ab"
			s += p.next().val
		}
		return pyStrValue(s), nil
	case tokName:
		switch t.text {
		case "True":
			return pyTrueValue, nil
		case "False":
			return pyFalseValue, nil
		case "None":
			return pyNoneValue, nil
		}
		if v, ok := p.vars[t.text]; ok {
			return v, nil
		}
		switch t.text {
		case "timedelta", "dict", "float", "int":
			return pyValue{kind: pyFunc, s: t.text}, nil
		case "datetime":
			return pyValue{kind: pyModule, s: t.text}, nil
		}
		if isKeyword(t.text) {
			return pyValue{}, &pyError{t.line, fmt.Sprintf("unsupported syntax %q", t.text)}
		}
		return pyValue{}, &pyError{t.line, fmt.Sprintf("name %q is not defined", t.text)}
	case tokOp:
		switch t.text {
		case "(":
			if p.isOp(")") {
				p.next()
				return pyValue{kind: pyTuple}, nil
			}
			first, err := p.expr()
			if err != nil {
				return first, err
			}
			if p.isOp(")") {
				p.next()
				return first, nil
			}
			items := []pyValue{first}
			for p.isOp(",") {
				p.next()
				if p.isOp(")") {
					break
				}
				v, err := p.expr()
				if err != nil {
					return v, err
				}
				items = append(items, v)
			}
			return pyValue{kind: pyTuple, items: items}, p.expect(")")
		case "[":
			var items []pyValue
			for !p.isOp("]") {
				v, err := p.expr()
				if err != nil {
					return v, err
				}
				items = append(items, v)
				if !p.isOp(",") {
					break
				}
				p.next()
			}
			return pyValue{kind: pyList, items: items}, p.expect("]")
		case "{":
			d := pyValue{kind: pyDict}
			for !p.isOp("}") {
				line := p.peek().line
				k, err := p.expr()
				if err != nil {
					return k, err
				}
				if !p.isOp(":") {
					return k, p.errf("sets are not supported (expected ':')")
				}
				p.next()
				v, err := p.expr()
				if err != nil {
					return v, err
				}
				if !hashable(k) {
					return k, &pyError{line, fmt.Sprintf("unhashable key of type %s", k.kind)}
				}
				d.set(k, v)
				if !p.isOp(",") {
					break
				}
				p.next()
			}
			return d, p.expect("}")
		}
	case tokNewline, tokEOF:
		return pyValue{}, &pyError{t.line, "unexpected end of line"}
	}
	return pyValue{}, &pyError{t.line, fmt.Sprintf("invalid syntax at %q", t.text)}
}

// parseNumber parses a Python int or float literal.
func parseNumber(s string) (pyValue, error) {
	clean := strings.ReplaceAll(s, "_", "")
	if strings.Contains(s, "__") || strings.HasSuffix(s, "_") {
		return pyValue{}, fmt.Errorf("invalid number %q", s)
	}
	lower := strings.ToLower(clean)
	if len(lower) > 1 && lower[0] == '0' && strings.ContainsRune("xob", rune(lower[1])) {
		base := map[byte]int{'x': 16, 'o': 8, 'b': 2}[lower[1]]
		n, err := strconv.ParseInt(lower[2:], base, 64)
		if err != nil {
			return pyValue{}, fmt.Errorf("invalid or too large integer %q", s)
		}
		return pyValue{kind: pyInt, i: n}, nil
	}
	if strings.ContainsAny(lower, ".e") || strings.HasSuffix(lower, "j") {
		if strings.HasSuffix(lower, "j") {
			return pyValue{}, fmt.Errorf("complex numbers are not supported")
		}
		f, err := strconv.ParseFloat(lower, 64)
		if err != nil && !strings.Contains(err.Error(), "range") {
			return pyValue{}, fmt.Errorf("invalid number %q", s)
		}
		return pyValue{kind: pyFloat, f: f}, nil
	}
	if len(lower) > 1 && lower[0] == '0' && strings.Trim(lower, "0") != "" {
		return pyValue{}, fmt.Errorf("leading zeros in decimal integer literals are not permitted: %q", s)
	}
	n, err := strconv.ParseInt(lower, 10, 64)
	if err != nil {
		return pyValue{}, fmt.Errorf("invalid or too large integer %q", s)
	}
	return pyValue{kind: pyInt, i: n}, nil
}

// binary applies an arithmetic operator.
func binary(op string, a, b pyValue) (pyValue, error) {
	bad := func() (pyValue, error) {
		return pyValue{}, fmt.Errorf("unsupported operand types for %s: '%s' and '%s'", op, a.kind, b.kind)
	}
	switch {
	case a.isNumber() && b.isNumber():
		return arith(op, a, b)
	case op == "+" && a.kind == b.kind && a.kind == pyStr:
		return pyStrValue(a.s + b.s), nil
	case op == "+" && a.kind == b.kind && (a.kind == pyList || a.kind == pyTuple):
		items := append(append([]pyValue(nil), a.items...), b.items...)
		return pyValue{kind: a.kind, items: items}, nil
	case a.kind == pyTimedelta && b.kind == pyTimedelta && (op == "+" || op == "-"):
		r, ok := addInt(a.i, b.i, op == "-")
		if !ok {
			return pyValue{}, fmt.Errorf("timedelta overflow")
		}
		return pyValue{kind: pyTimedelta, i: r}, nil
	case op == "*" && a.kind == pyTimedelta && b.isNumber():
		return scaleTimedelta(a, b.float())
	case op == "*" && b.kind == pyTimedelta && a.isNumber():
		return scaleTimedelta(b, a.float())
	case op == "/" && a.kind == pyTimedelta && b.isNumber():
		if b.float() == 0 {
			return pyValue{}, fmt.Errorf("division by zero")
		}
		return scaleTimedelta(a, 1/b.float())
	}
	return bad()
}

func scaleTimedelta(td pyValue, k float64) (pyValue, error) {
	us := math.RoundToEven(float64(td.i) * k)
	if math.IsNaN(us) || math.Abs(us) > maxTimedeltaMicro {
		return pyValue{}, fmt.Errorf("timedelta overflow")
	}
	return pyValue{kind: pyTimedelta, i: int64(us)}, nil
}

func addInt(a, b int64, sub bool) (int64, bool) {
	if sub {
		if b == math.MinInt64 {
			return 0, false
		}
		b = -b
	}
	r := a + b
	if (a > 0 && b > 0 && r < 0) || (a < 0 && b < 0 && r >= 0) {
		return 0, false
	}
	return r, true
}

// arith applies an operator to numbers with Python's semantics: ints stay
// ints (but /), floats win.
func arith(op string, a, b pyValue) (pyValue, error) {
	if a.kind != pyFloat && b.kind != pyFloat && op != "/" {
		x, y := big.NewInt(a.i), big.NewInt(b.i)
		r := new(big.Int)
		switch op {
		case "+":
			r.Add(x, y)
		case "-":
			r.Sub(x, y)
		case "*":
			r.Mul(x, y)
		case "//", "%":
			if y.Sign() == 0 {
				return pyValue{}, fmt.Errorf("integer division or modulo by zero")
			}
			// Floor division and a modulo with the sign of the divisor.
			q, m := new(big.Int).DivMod(x, y, new(big.Int))
			if m.Sign() != 0 && y.Sign() < 0 {
				q.Add(q, big.NewInt(1))
				m.Add(m, y)
			}
			if op == "//" {
				r = q
			} else {
				r = m
			}
		case "**":
			if y.Sign() < 0 {
				return pyValue{kind: pyFloat, f: math.Pow(float64(a.i), float64(b.i))}, nil
			}
			if y.Cmp(big.NewInt(256)) > 0 && x.CmpAbs(big.NewInt(1)) > 0 {
				return pyValue{}, fmt.Errorf("integer too large")
			}
			r.Exp(x, y, nil)
		}
		if !r.IsInt64() {
			return pyValue{}, fmt.Errorf("integer too large")
		}
		return pyValue{kind: pyInt, i: r.Int64()}, nil
	}
	x, y := a.float(), b.float()
	var r float64
	switch op {
	case "+":
		r = x + y
	case "-":
		r = x - y
	case "*":
		r = x * y
	case "/":
		if y == 0 {
			return pyValue{}, fmt.Errorf("division by zero")
		}
		r = x / y
	case "//":
		if y == 0 {
			return pyValue{}, fmt.Errorf("float floor division by zero")
		}
		r = math.Floor(x / y)
	case "%":
		if y == 0 {
			return pyValue{}, fmt.Errorf("float modulo")
		}
		r = math.Mod(x, y)
		if r != 0 && (r < 0) != (y < 0) {
			r += y
		}
	case "**":
		r = math.Pow(x, y)
	}
	if math.IsInf(r, 0) || math.IsNaN(r) {
		return pyValue{}, fmt.Errorf("numerical result out of range")
	}
	return pyValue{kind: pyFloat, f: r}, nil
}

// maxTimedeltaMicro is the largest timedelta of Python (999999999 days).
const maxTimedeltaMicro = 999999999 * 86400 * 1e6

// call runs one of the supported functions.
func call(fn pyValue, args []pyValue, kwargs []kwarg) (pyValue, error) {
	if fn.kind != pyFunc {
		return pyValue{}, fmt.Errorf("'%s' object is not callable", fn.kind)
	}
	switch fn.s {
	case "timedelta":
		// timedelta(days, seconds, microseconds, milliseconds, minutes,
		// hours, weeks), in microseconds.
		names := []string{"days", "seconds", "microseconds", "milliseconds", "minutes", "hours", "weeks"}
		scale := []float64{86400e6, 1e6, 1, 1e3, 60e6, 3600e6, 7 * 86400e6}
		vals := make([]*pyValue, len(names))
		if len(args) > len(names) {
			return pyValue{}, fmt.Errorf("timedelta takes at most 7 arguments")
		}
		for i := range args {
			vals[i] = &args[i]
		}
		for _, kw := range kwargs {
			i := indexOf(names, kw.name)
			if i < 0 {
				return pyValue{}, fmt.Errorf("'%s' is an invalid keyword argument for timedelta()", kw.name)
			}
			if vals[i] != nil {
				return pyValue{}, fmt.Errorf("timedelta() got multiple values for argument '%s'", kw.name)
			}
			vals[i] = &kw.val
		}
		var us float64
		for i, v := range vals {
			if v == nil {
				continue
			}
			if !v.isNumber() {
				return pyValue{}, fmt.Errorf("unsupported type for timedelta %s component: %s", names[i], v.kind)
			}
			us += v.float() * scale[i]
		}
		us = math.RoundToEven(us)
		if math.Abs(us) > maxTimedeltaMicro {
			return pyValue{}, fmt.Errorf("timedelta overflow")
		}
		return pyValue{kind: pyTimedelta, i: int64(us)}, nil
	case "dict":
		d := pyValue{kind: pyDict}
		if len(args) > 1 {
			return pyValue{}, fmt.Errorf("dict expected at most 1 argument, got %d", len(args))
		}
		if len(args) == 1 {
			if args[0].kind != pyDict {
				return pyValue{}, fmt.Errorf("dict() of a %s is not supported", args[0].kind)
			}
			for i := range args[0].items {
				d.set(args[0].items[i], args[0].vals[i])
			}
		}
		for _, kw := range kwargs {
			d.set(pyStrValue(kw.name), kw.val)
		}
		return d, nil
	case "float", "int":
		if len(kwargs) > 0 || len(args) != 1 {
			return pyValue{}, fmt.Errorf("%s() takes exactly one argument", fn.s)
		}
		a := args[0]
		var f float64
		switch {
		case a.isNumber():
			if fn.s == "int" && a.kind != pyFloat {
				return pyValue{kind: pyInt, i: a.i}, nil
			}
			f = a.float()
		case a.kind == pyStr:
			s := strings.TrimSpace(a.s)
			if fn.s == "int" {
				n, err := strconv.ParseInt(strings.ReplaceAll(s, "_", ""), 10, 64)
				if err != nil {
					return pyValue{}, fmt.Errorf("invalid literal for int() with base 10: %s", pyRepr(a))
				}
				return pyValue{kind: pyInt, i: n}, nil
			}
			var err error
			if f, err = strconv.ParseFloat(strings.ReplaceAll(s, "_", ""), 64); err != nil {
				return pyValue{}, fmt.Errorf("could not convert string to float: %s", pyRepr(a))
			}
		default:
			return pyValue{}, fmt.Errorf("%s() argument must be a string or a number, not '%s'", fn.s, a.kind)
		}
		if fn.s == "float" {
			return pyValue{kind: pyFloat, f: f}, nil
		}
		t := math.Trunc(f)
		if math.IsNaN(t) || math.IsInf(t, 0) || math.Abs(t) >= 1<<63 {
			return pyValue{}, fmt.Errorf("cannot convert %s to integer", pyFloatRepr(f))
		}
		return pyValue{kind: pyInt, i: int64(t)}, nil
	}
	return pyValue{}, fmt.Errorf("unsupported function %s", fn.s)
}

func indexOf(list []string, s string) int {
	for i, x := range list {
		if x == s {
			return i
		}
	}
	return -1
}

// ---- Output ----

// pyFloatRepr formats a float as Python's repr: the shortest digits that
// read back the same, in fixed notation between 1e-4 and 1e16 (always
// with a decimal point), in scientific notation otherwise.
func pyFloatRepr(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	case math.IsNaN(f):
		return "nan"
	}
	if f == 0 {
		if math.Signbit(f) {
			return "-0.0"
		}
		return "0.0"
	}
	e := strconv.FormatFloat(f, 'e', -1, 64) // d.ddde±XX
	mant, expStr, _ := strings.Cut(e, "e")
	exp, _ := strconv.Atoi(expStr)
	if exp < -4 || exp >= 16 {
		sign := "+"
		if exp < 0 {
			sign, exp = "-", -exp
		}
		return fmt.Sprintf("%se%s%02d", mant, sign, exp)
	}
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}

// pyRepr formats a value for messages.
func pyRepr(v pyValue) string {
	switch v.kind {
	case pyNone:
		return "None"
	case pyBool:
		if v.i != 0 {
			return "True"
		}
		return "False"
	case pyInt:
		return strconv.FormatInt(v.i, 10)
	case pyFloat:
		return pyFloatRepr(v.f)
	case pyStr:
		return strconv.Quote(v.s)
	case pyTimedelta:
		return fmt.Sprintf("timedelta(microseconds=%d)", v.i)
	case pyList, pyTuple:
		parts := make([]string, len(v.items))
		for i, it := range v.items {
			parts[i] = pyRepr(it)
		}
		if v.kind == pyList {
			return "[" + strings.Join(parts, ", ") + "]"
		}
		return "(" + strings.Join(parts, ", ") + ")"
	case pyDict:
		parts := make([]string, len(v.items))
		for i := range v.items {
			parts[i] = pyRepr(v.items[i]) + ": " + pyRepr(v.vals[i])
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return v.s
}

// pyJSON encodes a value as Python's json.dumps does (and as CMS stores
// task and score type parameters): floats keep their decimal point,
// tuples are arrays, dict keys become strings.
func pyJSON(v pyValue) (json.RawMessage, error) {
	var b bytes.Buffer
	if err := writeJSON(&b, v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func writeJSON(b *bytes.Buffer, v pyValue) error {
	switch v.kind {
	case pyNone:
		b.WriteString("null")
	case pyBool:
		if v.i != 0 {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case pyInt:
		b.WriteString(strconv.FormatInt(v.i, 10))
	case pyFloat:
		if math.IsInf(v.f, 0) || math.IsNaN(v.f) {
			return fmt.Errorf("%s is not valid JSON", pyFloatRepr(v.f))
		}
		b.WriteString(pyFloatRepr(v.f))
	case pyStr:
		s, _ := json.Marshal(v.s)
		b.Write(s)
	case pyList, pyTuple:
		b.WriteByte('[')
		for i, it := range v.items {
			if i > 0 {
				b.WriteString(", ")
			}
			if err := writeJSON(b, it); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case pyDict:
		b.WriteByte('{')
		for i, k := range v.items {
			if i > 0 {
				b.WriteString(", ")
			}
			var key string
			switch k.kind {
			case pyStr:
				key = k.s
			case pyInt, pyFloat, pyBool, pyNone:
				var kb bytes.Buffer
				_ = writeJSON(&kb, k)
				key = kb.String()
			default:
				return fmt.Errorf("keys must be str, int, float, bool or None, not %s", k.kind)
			}
			s, _ := json.Marshal(key)
			b.Write(s)
			b.WriteString(": ")
			if err := writeJSON(b, v.vals[i]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	default:
		return fmt.Errorf("object of type %s is not JSON serializable", v.kind)
	}
	return nil
}
