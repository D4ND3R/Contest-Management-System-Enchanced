// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package polygon

import (
	"math"
	"strings"
	"testing"
)

// evalX runs src and returns json.dumps(x).
func evalX(t *testing.T, src string) (string, error) {
	t.Helper()
	vars, err := pyModuleVars(src)
	if err != nil {
		return "", err
	}
	x, ok := vars["x"]
	if !ok {
		t.Fatalf("%q: no x", src)
	}
	b, err := pyJSON(x)
	return string(b), err
}

// The expected values are what CPython 3.12 prints for
// json.dumps(x) after exec(src).
func TestPyLiterals(t *testing.T) {
	cases := []struct{ src, want string }{
		{"x = 1 + 2 * 3", "7"},
		{"x = 2 ** 3 ** 2", "512"},
		{"x = -2 ** 2", "-4"},
		{"x = 7 // 2, ", "[3]"},
		{"x = 1, 2", "[1, 2]"},
		{"x = -7 // 2", "-4"},
		{"x = -7 % 3", "2"},
		{"x = 7 % -3", "-2"},
		{"x = 7.5 // 2", "3.0"},
		{"x = -7.5 % 2", "0.5"},
		{"x = 1 / 4", "0.25"},
		{"x = 100 / 3", "33.333333333333336"},
		{"x = 10 / 5", "2.0"},
		{"x = 256 * 1024 * 1024", "268435456"},
		{"x = 1e16", "1e+16"},
		{"x = 1e-5", "1e-05"},
		{"x = 0.0001", "0.0001"},
		{"x = 123456789012345678.0", "1.2345678901234568e+17"},
		{"x = 0x1F + 0o17 + 0b101 + 1_000", "1051"},
		{"x = .5 + 5.", "5.5"},
		{`x = 'a' 'b' "c"`, `"abc"`},
		{"x = '''multi\nline'''", `"multi\nline"`},
		{`x = r'\d+' + '\x41\u00e9\n'`, `"\\d+Aé\n"`},
		{`x = "tab\tquote\"\101"`, `"tab\tquote\"A"`},
		{"x = [1, (2, 3), {'a': None, 'b': True}]", `[1, [2, 3], {"a": null, "b": true}]`},
		{"x = {1: 'a', 1.0: 'b', True: 'c'}", `{"1": "c"}`},
		{"x = {None: 1, 2.5: 2, False: 3}", `{"null": 1, "2.5": 2, "false": 3}`},
		{"x = dict(a=1, b=[2])", `{"a": 1, "b": [2]}`},
		{"x = dict({'a': 1}, b=2)", `{"a": 1, "b": 2}`},
		{"x = int('42') + int(3.9) + int(-3.9)", "42"},
		{"x = float('1.5') + float(2)", "3.5"},
		{"x = [1, 2,]", "[1, 2]"},
		{"x = (1)", "1"},
		{"x = ()", "[]"},
		{"d = {'a': {'b': 1}}\nd['a']['b'] = 2\nd['c'] = 3\nx = d", `{"a": {"b": 2}, "c": 3}`},
		{"a = [1, 2]\nb = a\nb[0] = 9\nx = a", "[9, 2]"},
		{"x = {'a': 1}['a']", "1"},
		{"x = [[0, '0[1-2]'], [40, '1-.*']][1][-1]", `"1-.*"`},
		{"x = 2 ** -1", "0.5"},
		{"# comment\nimport datetime  # ignored\nfrom datetime import timedelta\n\nx = [\n  1,  # one\n  2,\n]\n", "[1, 2]"},
		{"x = 1 + \\\n  2; y = 3", "3"},
		{"\uFEFFx = 1\r\n", "1"},
		{"x = 'é' + \"\\N\"", `"é\\N"`},
		{"x = -(-3)", "3"},
		{"x = +True", "1"},
		{"x = [1] + [2]", "[1, 2]"},
	}
	for _, c := range cases {
		got, err := evalX(t, c.src)
		if err != nil {
			t.Errorf("%q: %v", c.src, err)
			continue
		}
		if canonicalJSON(t, got) != canonicalJSON(t, c.want) {
			t.Errorf("%q = %s, want %s", c.src, got, c.want)
		}
	}
}

func TestPyTimedelta(t *testing.T) {
	// Microseconds, as CPython 3.12 computes them.
	cases := map[string]int64{
		"timedelta(seconds=30)":                     30000000,
		"timedelta(minutes=1.5, milliseconds=1)":    90001000,
		"timedelta(1, 2, 3)":                        86402000003,
		"timedelta(hours=-1)":                       -3600000000,
		"timedelta(seconds=0.0000005)":              0,
		"timedelta(weeks=1)*2":                      1209600000000,
		"timedelta(seconds=10)/4":                   2500000,
		"datetime.timedelta(days=1) - timedelta(1)": 0,
		"2 * timedelta(seconds=1) + timedelta(0)":   2000000,
	}
	for src, want := range cases {
		vars, err := pyModuleVars("x = " + src)
		if err != nil {
			t.Errorf("%s: %v", src, err)
			continue
		}
		if x := vars["x"]; x.kind != pyTimedelta || x.i != want {
			t.Errorf("%s = %v, want %d µs", src, x, want)
		}
	}
	if _, err := pyJSON(pyValue{kind: pyTimedelta}); err == nil {
		t.Error("a timedelta encoded as JSON")
	}
}

func TestPyErrors(t *testing.T) {
	cases := map[string]string{
		"import os\nos.system('rm -rf /')":     "line 2: unsupported statement",
		"x = open('/etc/passwd').read()":       `name "open" is not defined`,
		"def f():\n  pass":                     "line 1: unsupported statement",
		"for i in range(3): pass":              "unsupported statement",
		"x = [i for i in y]":                   `name "i" is not defined`,
		"x = 1 if True else 2":                 "invalid syntax",
		"x = lambda: 1":                        "unsupported syntax",
		"x = {1, 2}":                           "sets are not supported",
		"x = {[1]: 2}":                         "unhashable",
		"x = f'{y}'":                           "f-strings are not supported",
		"x = b'a'":                             "b-strings are not supported",
		"x = 'abc":                             "unterminated string",
		"x = [1,\n2":                           "bracket is not closed",
		"x = 1)":                               `unmatched ")"`,
		"x = y":                                `name "y" is not defined`,
		"x = 1\ny[0] = 2":                      `line 2: name "y" is not defined`,
		"x = 1\nx['a'] = 2":                    "does not support item assignment",
		"x = {}\nx['a']['b'] = 2":              "KeyError",
		"x = [1]\nx[1] = 2":                    "index out of range",
		"x = 1 / 0":                            "division by zero",
		"x = 9223372036854775807 + 1":          "integer too large",
		"x = 99999999999999999999":             "too large",
		"x = 012":                              "leading zeros",
		"x = 1j":                               "complex",
		"x = 'a' - 'b'":                        "unsupported operand types for -",
		"x = timedelta(fortnights=1)":          "invalid keyword argument",
		"x = timedelta('1')":                   "unsupported type for timedelta",
		"x = int('x')":                         "invalid literal for int()",
		"x = dict(1)":                          "dict() of a int",
		"x == 1":                               "unsupported character",
		"x = 1 2":                              "invalid syntax",
		"x = 1e400 * 10":                       "out of range",
		"x = 2 ** 100":                         "integer too large",
		"x = {'a': 1}['b']":                    "KeyError",
		"x = None[0]":                          "not subscriptable",
		"x = 1(2)":                             "not callable",
		"x = timedelta(days=1e12)":             "timedelta overflow",
		"x = 'a'\\ y":                          "after line continuation",
		"x = float('nan')\ny = 1":              "",
		"x = timedelta(1, seconds=1, days=2)":  "multiple values",
		"x = dict(a=1, 2)":                     "positional argument follows keyword argument",
		"x = [1][1.0]":                         "indices must be integers",
		"x = -'a'":                             "bad operand type for unary -",
		"x = 1 ; ; y = 2":                      "",
		"general = {'a': 1}; general.update()": "unsupported statement",
		"x = datetime.date(2020, 1, 1)":        `unsupported attribute "date"`,
	}
	for src, want := range cases {
		_, err := pyModuleVars(src)
		switch {
		case want == "" && err != nil:
			t.Errorf("%q: %v", src, err)
		case want != "" && (err == nil || !strings.Contains(err.Error(), want)):
			t.Errorf("%q: error %v, want %q", src, err, want)
		}
	}
	// NaN is read but is not JSON.
	if _, err := evalX(t, "x = float('nan')"); err == nil {
		t.Error("NaN encoded as JSON")
	}
}

func TestPyFloatRepr(t *testing.T) {
	// repr() of CPython.
	cases := map[float64]string{
		0.1: "0.1", 100.0 / 3: "33.333333333333336", 1e16: "1e+16", 1e15: "1000000000000000.0",
		123456789.123: "123456789.123", 1e-4: "0.0001", 9.99e-5: "9.99e-05", 1e22: "1e+22",
		2.5e-7: "2.5e-07", 5e-324: "5e-324", math.MaxFloat64: "1.7976931348623157e+308",
		100: "100.0", 25: "25.0", 1.0 / 7: "0.14285714285714285", -2.5: "-2.5", 0: "0.0",
		math.Copysign(0, -1): "-0.0", math.Inf(1): "inf", math.Inf(-1): "-inf", math.NaN(): "nan",
	}
	for f, want := range cases {
		if got := pyFloatRepr(f); got != want {
			t.Errorf("pyFloatRepr(%v) = %s, want %s", f, got, want)
		}
	}
}
