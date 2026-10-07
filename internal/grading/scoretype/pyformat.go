// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package scoretype

import (
	"math"
	"strconv"
)

// maxRoundDigits is Python's NDIGITS_MAX for doubles: with more digits
// round(x, n) returns x unchanged.
const maxRoundDigits = 323

// Round rounds x to ndigits digits after the decimal point exactly like
// Python's round(x, ndigits), which CMS uses for score_precision: the exact
// binary value of x is rounded half to even, so Round(2.675, 2) is 2.67
// (2.675 is really 2.67499999…) and Round(0.125, 2) is 0.12. A negative
// ndigits counts as 0 (score_precision cannot be negative).
func Round(x float64, ndigits int) float64 {
	if ndigits < 0 {
		ndigits = 0
	}
	if x == 0 || math.IsNaN(x) || math.IsInf(x, 0) || ndigits > maxRoundDigits {
		return x
	}
	// strconv formats with the exact decimal expansion and rounds half to
	// even on it, as Python's dtoa does; parsing gives the nearest double.
	var buf [64]byte
	s := strconv.AppendFloat(buf[:0], x, 'f', ndigits, 64)
	r, err := strconv.ParseFloat(string(s), 64)
	if err != nil {
		return x
	}
	return r
}

// FormatG formats x like Python's "%g" % x: six significant digits,
// trailing zeros removed, exponent notation below 1e-4 and from 1e6
// ("10.5", "59", "1e+06", "1.23457e+06", "1e-05"). CMS uses it for the
// ranking details and headers sent to the scoreboard.
func FormatG(x float64) string {
	switch {
	case math.IsNaN(x):
		return "nan"
	case math.IsInf(x, 1):
		return "inf"
	case math.IsInf(x, -1):
		return "-inf"
	}
	return strconv.FormatFloat(x, 'g', 6, 64)
}
