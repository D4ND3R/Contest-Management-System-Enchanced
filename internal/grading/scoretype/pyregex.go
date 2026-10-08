// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package scoretype

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// maxRepeat is the largest count of a repetition RE2 accepts.
const maxRepeat = 1000

// compileCodenameRegex compiles the regular expression that selects the
// testcases of a subtask. Upstream uses Python's re.match: the expression
// is anchored at the start of the codename, and written in Python's
// syntax. Go's regexp (RE2) has most of it; pyToRE2 translates the rest
// and refuses what RE2 cannot do (lookarounds, backreferences,
// conditionals, atomic groups, possessive repetitions, verbose mode), so
// that such parameters are rejected when the dataset is saved instead of
// selecting other testcases than in CMS.
func compileCodenameRegex(pattern string) (*regexp.Regexp, error) {
	re2, err := pyToRE2(pattern)
	if err != nil {
		return nil, err
	}
	return regexp.Compile(`^(?:` + re2 + `)`)
}

// pyToRE2 translates a Python regular expression to RE2. Codenames are
// ASCII ([A-Za-z0-9_-]+), so the differences of Unicode classes (\d, \w,
// \s, case folding) and of $ before a final newline do not matter, and
// the flags a (ASCII) and u (Unicode) are dropped.
func pyToRE2(p string) (string, error) {
	t := &pyTranslator{p: p}
	if err := t.run(); err != nil {
		return "", err
	}
	return t.out.String(), nil
}

type pyTranslator struct {
	p     string
	i     int
	depth int // of the groups open
	out   strings.Builder
	// flagsEnd is the length of the output when it only holds global
	// flags, which must come first.
	flagsEnd int
}

func (t *pyTranslator) more() bool { return t.i < len(t.p) }

// peek returns the next byte, 0 at the end.
func (t *pyTranslator) peek() byte {
	if t.i < len(t.p) {
		return t.p[t.i]
	}
	return 0
}

func (t *pyTranslator) run() error {
	for t.more() {
		c := t.p[t.i]
		switch c {
		case ')':
			// RE2 would take "a)|(b" as two alternatives, the second
			// one not anchored.
			if t.depth == 0 {
				return errors.New("unbalanced parenthesis")
			}
			t.depth--
			t.i++
			t.out.WriteByte(')')
		case '\\':
			if err := t.escape(); err != nil {
				return err
			}
		case '[':
			if err := t.class(); err != nil {
				return err
			}
		case '(':
			if err := t.group(); err != nil {
				return err
			}
		case '*', '+', '?':
			t.i++
			t.out.WriteByte(c)
			if err := t.repeatSuffix(); err != nil {
				return err
			}
		case '{':
			if err := t.brace(); err != nil {
				return err
			}
		case ']', '}':
			t.i++
			t.literal(rune(c))
		default: // . ^ $ | and literals
			r, size := utf8.DecodeRuneInString(t.p[t.i:])
			if r == utf8.RuneError && size == 1 {
				return errors.New("invalid UTF-8")
			}
			t.i += size
			if strings.ContainsRune(".^$|", r) {
				t.out.WriteRune(r)
			} else {
				t.literal(r)
			}
		}
	}
	return nil
}

// literal writes a character that matches itself.
func (t *pyTranslator) literal(r rune) {
	switch {
	case r < utf8.RuneSelf && isAlnum(byte(r)):
		t.out.WriteRune(r)
	case r > ' ' && r < utf8.RuneSelf:
		t.out.WriteByte('\\')
		t.out.WriteRune(r)
	default:
		fmt.Fprintf(&t.out, `\x{%x}`, r)
	}
}

func isAlnum(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isOctal(c byte) bool { return c >= '0' && c <= '7' }

// repeatSuffix handles what may follow a repetition: ? (lazy) or +
// (possessive, which RE2 does not have).
func (t *pyTranslator) repeatSuffix() error {
	switch t.peek() {
	case '?':
		t.i++
		t.out.WriteByte('?')
	case '+':
		return errors.New("possessive repetitions (*+, ++, ?+, {m,n}+) are not supported")
	}
	return nil
}

// brace handles {: a repetition {m}, {m,}, {,n}, {m,n} or {,}, or else a
// literal brace (Python's rules; RE2 takes {,n} as literal text).
func (t *pyTranslator) brace() error {
	j := t.i + 1
	digits := func() string {
		k := j
		for j < len(t.p) && t.p[j] >= '0' && t.p[j] <= '9' {
			j++
		}
		return t.p[k:j]
	}
	if j < len(t.p) && t.p[j] == '}' {
		t.i++
		t.literal('{')
		return nil
	}
	lo := digits()
	hi, comma := lo, false
	if j < len(t.p) && t.p[j] == ',' {
		j++
		comma = true
		hi = digits()
	}
	if j >= len(t.p) || t.p[j] != '}' {
		t.i++
		t.literal('{')
		return nil
	}
	t.i = j + 1
	for _, n := range []string{lo, hi} {
		if v, err := strconv.Atoi(n); n != "" && (err != nil || v > maxRepeat) {
			return fmt.Errorf("repetition counts over %d are not supported", maxRepeat)
		}
	}
	if lo == "" {
		lo = "0"
	}
	if comma {
		fmt.Fprintf(&t.out, "{%s,%s}", lo, hi)
	} else {
		fmt.Fprintf(&t.out, "{%s}", lo)
	}
	return t.repeatSuffix()
}

// group handles ( and the extensions (?...).
func (t *pyTranslator) group() error {
	rest := t.p[t.i:]
	if !strings.HasPrefix(rest, "(?") {
		t.i++
		t.out.WriteByte('(')
		t.depth++
		return nil
	}
	switch {
	case strings.HasPrefix(rest, "(?:"):
		t.i += 3
		t.out.WriteString("(?:")
		t.depth++
		return nil
	case strings.HasPrefix(rest, "(?P<"):
		// Names do not matter without backreferences, and Python
		// accepts names that RE2 does not.
		end := strings.IndexByte(rest, '>')
		if end < 0 {
			return errors.New("missing >, unterminated name")
		}
		t.i += end + 1
		t.out.WriteByte('(')
		t.depth++
		return nil
	case strings.HasPrefix(rest, "(?P="):
		return errors.New("backreferences (?P=name) are not supported")
	case strings.HasPrefix(rest, "(?#"):
		end := strings.IndexByte(rest, ')')
		if end < 0 {
			return errors.New("missing ), unterminated comment")
		}
		t.i += end + 1
		return nil
	case strings.HasPrefix(rest, "(?="), strings.HasPrefix(rest, "(?!"):
		return fmt.Errorf("lookahead assertions %s…) are not supported", rest[:3])
	case strings.HasPrefix(rest, "(?<="), strings.HasPrefix(rest, "(?<!"):
		return fmt.Errorf("lookbehind assertions %s…) are not supported", rest[:4])
	case strings.HasPrefix(rest, "(?("):
		return errors.New("conditional groups (?(…)…) are not supported")
	case strings.HasPrefix(rest, "(?>"):
		return errors.New("atomic groups (?>…) are not supported")
	}
	return t.flags()
}

// flags handles (?flags), (?flags:...) and (?on-off:...).
func (t *pyTranslator) flags() error {
	var on, off strings.Builder
	cur := &on
	j := t.i + 2
	for ; j < len(t.p) && t.p[j] != ')' && t.p[j] != ':'; j++ {
		switch c := t.p[j]; c {
		case 'i', 'm', 's':
			cur.WriteByte(c)
		case 'a', 'u': // ASCII and Unicode: the same on codenames
		case 'x':
			return errors.New("verbose mode (?x) is not supported")
		case '-':
			if cur == &off {
				return errors.New("bad inline flags")
			}
			cur = &off
		default:
			return fmt.Errorf("unknown extension ?%c", c)
		}
	}
	if j >= len(t.p) || j == t.i+2 {
		return errors.New("missing flag")
	}
	f := on.String()
	if off.Len() > 0 {
		f += "-" + off.String()
	}
	if t.p[j] == ':' {
		t.out.WriteString("(?" + f + ":")
		t.depth++
	} else {
		// Python 3.11+ refuses global flags elsewhere (older versions
		// applied them to the whole expression); RE2 would apply them
		// from there on.
		if t.out.Len() != t.flagsEnd || off.Len() > 0 {
			return errors.New("global flags not at the start of the expression")
		}
		if f != "" {
			t.out.WriteString("(?" + f + ")")
		}
		t.flagsEnd = t.out.Len()
	}
	t.i = j + 1
	return nil
}

// escape handles a backslash outside a class (Python _escape).
func (t *pyTranslator) escape() error {
	if t.i+1 >= len(t.p) {
		return errors.New(`bad escape (end of pattern)`)
	}
	c := t.p[t.i+1]
	switch c {
	case 'A', 'b', 'B', 'd', 'D', 's', 'S', 'w', 'W', 'f', 'n', 'r', 't', 'v':
		t.out.WriteString(t.p[t.i : t.i+2])
		t.i += 2
		return nil
	case 'Z':
		t.out.WriteString(`\z`)
		t.i += 2
		return nil
	}
	if c >= '1' && c <= '9' {
		// Three octal digits are a character, else a backreference.
		if t.i+3 < len(t.p) && isOctal(c) && isOctal(t.p[t.i+2]) && isOctal(t.p[t.i+3]) {
			r, err := t.code(t.i+1, 3, 8)
			if err != nil {
				return err
			}
			t.literal(r)
			return nil
		}
		return fmt.Errorf(`backreferences (\%c) are not supported`, c)
	}
	r, ok, err := t.codeEscape()
	if err != nil {
		return err
	}
	if !ok {
		r, err = t.plainEscape()
		if err != nil {
			return err
		}
	}
	t.literal(r)
	return nil
}

// codeEscape reads the escapes that give a character by its code, common
// to classes: \a, \x, \u, \U, \N and octal \0.
func (t *pyTranslator) codeEscape() (rune, bool, error) {
	c := t.p[t.i+1]
	var r rune
	var err error
	switch c {
	case 'a':
		r, t.i = 7, t.i+2
	case 'x':
		r, err = t.code(t.i+2, 2, 16)
	case 'u':
		r, err = t.code(t.i+2, 4, 16)
	case 'U':
		r, err = t.code(t.i+2, 8, 16)
	case 'N':
		return 0, false, errors.New(`named characters \N{…} are not supported`)
	case '0':
		n := 1
		for n < 3 && t.i+1+n < len(t.p) && isOctal(t.p[t.i+1+n]) {
			n++
		}
		r, err = t.code(t.i+1, n, 8)
	default:
		return 0, false, nil
	}
	return r, true, err
}

// code reads the n digits in base at position j as a character and moves
// past them.
func (t *pyTranslator) code(j, n, base int) (rune, error) {
	if j+n > len(t.p) {
		return 0, fmt.Errorf("incomplete escape %s", t.p[t.i:])
	}
	v, err := strconv.ParseUint(t.p[j:j+n], base, 32)
	if err != nil {
		return 0, fmt.Errorf("incomplete escape %s", t.p[t.i:j+n])
	}
	if base == 8 && v > 0o377 || !utf8.ValidRune(rune(v)) {
		return 0, fmt.Errorf("bad escape %s", t.p[t.i:j+n])
	}
	t.i = j + n
	return rune(v), nil
}

// plainEscape reads a backslash before a character that matches itself;
// a letter or digit with no meaning is an error.
func (t *pyTranslator) plainEscape() (rune, error) {
	r, size := utf8.DecodeRuneInString(t.p[t.i+1:])
	if r < utf8.RuneSelf && isAlnum(byte(r)) {
		return 0, fmt.Errorf(`bad escape \%c`, r)
	}
	t.i += 1 + size
	return r, nil
}

// class handles a character class (Python's rules: a ] first is literal,
// a - first or last is literal, escapes of _class_escape).
func (t *pyTranslator) class() error {
	t.i++
	t.out.WriteByte('[')
	if t.peek() == '^' {
		t.i++
		t.out.WriteByte('^')
	}
	empty := true
	for {
		if !t.more() {
			return errors.New("unterminated character set")
		}
		if t.p[t.i] == ']' && !empty {
			t.i++
			t.out.WriteByte(']')
			return nil
		}
		empty = false
		lo, cat, err := t.classItem()
		if err != nil {
			return err
		}
		if t.peek() != '-' {
			t.classWrite(lo, cat)
			continue
		}
		t.i++
		if !t.more() {
			return errors.New("unterminated character set")
		}
		if t.p[t.i] == ']' {
			t.classWrite(lo, cat)
			t.literal('-')
			continue // the ] ends the class
		}
		hi, cat2, err := t.classItem()
		if err != nil {
			return err
		}
		if cat != "" || cat2 != "" || hi < lo {
			return errors.New("bad character range")
		}
		t.literal(lo)
		t.out.WriteByte('-')
		t.literal(hi)
	}
}

// classItem reads a character of a class, or a category (\d...).
func (t *pyTranslator) classItem() (rune, string, error) {
	if t.p[t.i] != '\\' {
		r, size := utf8.DecodeRuneInString(t.p[t.i:])
		if r == utf8.RuneError && size == 1 {
			return 0, "", errors.New("invalid UTF-8")
		}
		t.i += size
		return r, "", nil
	}
	if t.i+1 >= len(t.p) {
		return 0, "", errors.New(`bad escape (end of pattern)`)
	}
	c := t.p[t.i+1]
	switch c {
	case 'd', 'D', 's', 'S', 'w', 'W':
		t.i += 2
		return 0, t.p[t.i-2 : t.i], nil
	case 'b':
		t.i += 2
		return '\b', "", nil
	case 'f', 'n', 'r', 't', 'v':
		t.i += 2
		return rune(map[byte]byte{'f': '\f', 'n': '\n', 'r': '\r', 't': '\t', 'v': '\v'}[c]), "", nil
	}
	if isOctal(c) {
		n := 1
		for n < 3 && t.i+1+n < len(t.p) && isOctal(t.p[t.i+1+n]) {
			n++
		}
		r, err := t.code(t.i+1, n, 8)
		return r, "", err
	}
	r, ok, err := t.codeEscape()
	if err != nil || ok {
		return r, "", err
	}
	r, err = t.plainEscape()
	return r, "", err
}

func (t *pyTranslator) classWrite(r rune, cat string) {
	if cat != "" {
		t.out.WriteString(cat)
		return
	}
	t.literal(r)
}
