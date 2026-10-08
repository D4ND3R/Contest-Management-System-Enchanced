// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package checker

import (
	"math"
	"strconv"
	"testing"
)

// TestPyFloat compares PyFloat with Python 3.12's float(): the expected
// values are what float() returns (its repr), "error" for a ValueError.
func TestPyFloat(t *testing.T) {
	cases := []struct{ in, want string }{
		{"1", "1.0"},
		{"0", "0.0"},
		{"-0", "-0.0"},
		{"+1.5", "1.5"},
		{"1.", "1.0"},
		{".5", "0.5"},
		{".", "error"},
		{"", "error"},
		{" 1 ", "1.0"},
		{"1e3", "1000.0"},
		{"1E-3", "0.001"},
		{"1e", "error"},
		{"1e+", "error"},
		{"e3", "error"},
		{"1.5e+2_0", "1.5e+20"},
		{"0x1p0", "error"},
		{"0X10", "error"},
		{"0b1", "error"},
		{"0o7", "error"},
		{"1_000", "1000.0"},
		{"1__000", "error"},
		{"_1", "error"},
		{"1_", "error"},
		{"1_.5", "error"},
		{"1._5", "error"},
		{"1.5_5", "1.55"},
		{"1e1_0", "10000000000.0"},
		{"1e_10", "error"},
		{"inf", "inf"},
		{"-Infinity", "-inf"},
		{"+iNf", "inf"},
		{"nan", "nan"},
		{"-nan", "nan"},
		{"NaN", "nan"},
		{"infinit", "error"},
		{"in_f", "error"},
		{"1e400", "inf"},
		{"-1e400", "-inf"},
		{"1e-400", "0.0"},
		{"4.9e-324", "5e-324"},
		{"\u0661", "1.0"},
		{"\u0661\u066b\u0665", "error"},
		{"\u0661.\u0665", "1.5"},
		{"\u0e53.\u0e51\u0e54", "3.14"},
		{"\U0001d7d9\U0001d7da", "12.0"},
		{"\U0001d7d8.\U0001d7dd", "0.5"},
		{"\u00a01\u00a0", "1.0"},
		{"1\u2003", "1.0"},
		{"1\x1c", "error"},
		{"\u00b2", "error"},
		{"\u00bd", "error"},
		{"1 2", "error"},
		{"1,5", "error"},
		{"--1", "error"},
		{"+-1", "error"},
		{"0.1", "0.1"},
		{"0.30000000000000004", "0.30000000000000004"},
		{"123456789012345678901234567890", "1.2345678901234568e+29"},
		{"1e-5", "1e-05"},
		{"7e22", "7e+22"},
		{"1.7976931348623157e308", "1.7976931348623157e+308"},
		{"1.7976931348623159e308", "inf"},
		{"\t-2.5\n", "-2.5"},
		{"1\x7f", "error"},
		{"1\u0085", "1.0"},
		{"0001.2500", "1.25"},
		{"1.2.3", "error"},
		{"1e5.5", "error"},
		{"1d5", "error"},
		{"1f", "error"},
	}
	for _, c := range cases {
		v, err := PyFloat(c.in)
		switch c.want {
		case "error":
			if err == nil {
				t.Errorf("PyFloat(%q) = %v, want an error", c.in, v)
			}
			continue
		case "nan":
			if err != nil || !math.IsNaN(v) {
				t.Errorf("PyFloat(%q) = %v, %v; want NaN", c.in, v, err)
			}
			continue
		case "inf", "-inf":
			if err != nil || !math.IsInf(v, map[string]int{"inf": 1, "-inf": -1}[c.want]) {
				t.Errorf("PyFloat(%q) = %v, %v; want %s", c.in, v, err, c.want)
			}
			continue
		}
		want, perr := strconv.ParseFloat(c.want, 64)
		if perr != nil {
			t.Fatal(perr)
		}
		if err != nil || v != want || math.Signbit(v) != math.Signbit(want) {
			t.Errorf("PyFloat(%q) = %v, %v; want %s", c.in, v, err, c.want)
		}
	}
}

// TestManagerOutcomeSyntax: the outcome of a manager is read as Python
// reads it; a hexadecimal number is a broken manager (upstream "Outcome is
// not a float."), digits of other scripts are numbers.
func TestManagerOutcomeSyntax(t *testing.T) {
	for in, want := range map[string]float64{"1_0": 10, "١": 1, " .5 ": 0.5, "1e-400": 0} {
		o, err := ParseManagerOutput([]byte(in+"\n"), nil)
		if err != nil || o.Outcome != want {
			t.Errorf("outcome %q: %+v, %v; want %v", in, o, err, want)
		}
	}
	for _, in := range []string{"0x1p0", "0x10", "1e400", "inf", "nan", "1__0"} {
		if o, err := ParseManagerOutput([]byte(in+"\n"), nil); err == nil {
			t.Errorf("outcome %q: %+v, want an error", in, o)
		}
	}
}
