// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package checker

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// errNotFloat is the error of PyFloat.
var errNotFloat = errors.New("not a float")

// PyFloat parses s like Python's float(s), which upstream applies to the
// numbers written by managers (the outcome of the standard manager
// output, the score of an Interactive controller), so that the same
// outputs are accepted and refused:
//
//   - white space around the number is ignored;
//   - decimal digits of any script count (Unicode category Nd: "١" is 1);
//   - an underscore may separate two digits ("1_000");
//   - the number is decimal, with an optional sign, fraction and exponent
//     ("1.", ".5", "1e-3"); hexadecimal ("0x1p0") is refused;
//   - "inf", "infinity" and "nan" in any case, with an optional sign, are
//     infinities and NaN, and so are numbers too large ("1e400"): callers
//     that need a finite number must check it.
func PyFloat(s string) (float64, error) {
	// Python first turns non-ASCII white space into spaces and decimal
	// digits into ASCII ones; anything else non-ASCII is an error.
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 127 {
			ascii = false
			break
		}
	}
	if !ascii {
		var b strings.Builder
		b.Grow(len(s))
		for _, r := range s {
			switch {
			case r < 127:
				b.WriteByte(byte(r))
			case unicode.IsSpace(r):
				b.WriteByte(' ')
			default:
				d, ok := decimalValue(r)
				if !ok {
					return 0, errNotFloat
				}
				b.WriteByte('0' + d)
			}
		}
		s = b.String()
	}
	s = strings.Trim(s, " \t\n\v\f\r")

	// Underscores only between digits, and then dropped.
	if strings.IndexByte(s, '_') >= 0 {
		for i := 0; i < len(s); i++ {
			if s[i] == '_' && (i == 0 || !isDigit(s[i-1]) || i+1 == len(s) || !isDigit(s[i+1])) {
				return 0, errNotFloat
			}
		}
		s = strings.ReplaceAll(s, "_", "")
	}

	body := s
	neg := false
	if body != "" && (body[0] == '+' || body[0] == '-') {
		neg = body[0] == '-'
		body = body[1:]
	}
	switch strings.ToLower(body) {
	case "inf", "infinity":
		if neg {
			return math.Inf(-1), nil
		}
		return math.Inf(1), nil
	case "nan":
		return math.NaN(), nil
	}
	if !decimalSyntax(body) {
		return 0, errNotFloat
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return 0, errNotFloat
	}
	return v, nil // ±Inf when too large, as Python
}

// decimalSyntax reports whether s is digits with an optional fraction and
// exponent, at least one digit before or after the point.
func decimalSyntax(s string) bool {
	i, digits := 0, 0
	for i < len(s) && isDigit(s[i]) {
		i, digits = i+1, digits+1
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && isDigit(s[i]) {
			i, digits = i+1, digits+1
		}
	}
	if digits == 0 {
		return false
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		start := i
		for i < len(s) && isDigit(s[i]) {
			i++
		}
		if i == start {
			return false
		}
	}
	return i == len(s)
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// decimalValue is the value of a decimal digit of any script. Unicode
// assigns them in runs of ten, from 0 to 9, sometimes several runs in a
// row (the mathematical digits).
func decimalValue(r rune) (byte, bool) {
	if !unicode.IsDigit(r) {
		return 0, false
	}
	start := r
	for unicode.IsDigit(start - 1) {
		start--
	}
	return byte((r - start) % 10), true
}
