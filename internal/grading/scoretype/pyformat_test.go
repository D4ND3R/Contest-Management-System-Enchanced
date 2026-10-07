// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package scoretype

import (
	"math"
	"testing"
)

// The expected values are the output of Python 3.12 (round(x, n) and
// "%g" % x). A wider comparison (9 000 values, precisions 0 to 7) was run
// against Python while writing the port.
func TestRound(t *testing.T) {
	for _, c := range []struct {
		x    float64
		n    int
		want float64
	}{
		{2.675, 2, 2.67}, // 2.67499999…
		{0.125, 2, 0.12}, // exact tie: to even
		{0.375, 2, 0.38},
		{0.5, 0, 0},
		{1.5, 0, 2},
		{2.5, 0, 2},
		{-2.5, 0, -2},
		{0.005, 2, 0.01}, // 0.005000000000000000104…
		{0.015, 2, 0.01}, // 0.01499999…
		{0.335, 2, 0.34},
		{0.30000000000000004, 2, 0.3},
		{44.44444, 2, 44.44},
		{6.000000000000001, 4, 6},
		{33.333333333333336, 0, 33},
		{1e300, 2, 1e300},
		{5e-324, 2, 0},
		{1.25, -1, 1}, // negative precision counts as 0
		{0.1, 400, 0.1},
	} {
		if got := Round(c.x, c.n); got != c.want {
			t.Errorf("Round(%v, %d) = %v, want %v", c.x, c.n, got, c.want)
		}
	}
	if got := Round(-0.001, 2); got != 0 || !math.Signbit(got) {
		t.Errorf("Round(-0.001, 2) = %v, want -0", got)
	}
	if !math.IsNaN(Round(math.NaN(), 2)) || !math.IsInf(Round(math.Inf(1), 2), 1) {
		t.Error("non-finite values")
	}
}

func TestFormatG(t *testing.T) {
	for _, c := range []struct {
		x    float64
		want string
	}{
		{0, "0"},
		{math.Copysign(0, -1), "-0"},
		{10.5, "10.5"},
		{59, "59"},
		{2.95, "2.95"},
		{100, "100"},
		{123456, "123456"},
		{1e6, "1e+06"},
		{1234567, "1.23457e+06"},
		{1234565, "1.23456e+06"}, // exact tie: to even
		{999999.5, "1e+06"},
		{0.0001, "0.0001"},
		{1e-05, "1e-05"},
		{1e16, "1e+16"},
		{33.333333333333336, "33.3333"},
		{-7.25, "-7.25"},
		{math.Inf(1), "inf"},
		{math.Inf(-1), "-inf"},
		{math.NaN(), "nan"},
	} {
		if got := FormatG(c.x); got != c.want {
			t.Errorf("FormatG(%v) = %q, want %q", c.x, got, c.want)
		}
	}
}
