// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package web

import (
	"html/template"
	"math"
	"testing"
)

// TestPyFormat compares with Python 3.12's "%" operator (the expected
// strings were printed by Python for the same format and arguments).
func TestPyFormat(t *testing.T) {
	cases := []struct {
		format string
		args   []any
		want   string // "!" means Python raises
	}{
		{"%s", []any{"x"}, "x"},
		{"%s.png", []any{"icon"}, "icon.png"},
		{"a %s b %s", []any{"1", "2"}, "a 1 b 2"},
		{"%d", []any{3}, "3"},
		{"%d", []any{3.9}, "3"},
		{"%d", []any{-3.9}, "-3"},
		{"%5d|%-5d|%05d", []any{42, 42, 42}, "   42|42   |00042"},
		{"%+d % d", []any{5, 5}, "+5  5"},
		{"%x %X %o %#x %#o", []any{255, 255, 8, 255, 8}, "ff FF 10 0xff 0o10"},
		{"%.2f", []any{3.14159}, "3.14"},
		{"%10.3f|", []any{3.14159}, "     3.142|"},
		{"%e", []any{12345.678}, "1.234568e+04"},
		{"%g", []any{1.234e-05}, "1.234e-05"},
		{"%g", []any{123456789.0}, "1.23457e+08"},
		{"%G", []any{1e-10}, "1E-10"},
		{"%.3g", []any{3.14159}, "3.14"},
		{"%r", []any{"it's"}, `"it's"`},
		{"%r", []any{`a"b`}, `'a"b'`},
		{"%5s|%-5s|", []any{"ab", "ab"}, "   ab|ab   |"},
		{"%.2s", []any{"abcdef"}, "ab"},
		{"100%%", nil, "100%"},
		{"%c%c", []any{65, "z"}, "Az"},
		{"%s", []any{true}, "True"},
		{"%s", []any{nil}, "None"},
		{"%s", []any{2.0}, "2.0"},
		{"%s", []any{1e16}, "1e+16"},
		{"%s", []any{1e-5}, "1e-05"},
		{"%s", []any{0.1}, "0.1"},
		{"%s", []any{123456789.125}, "123456789.125"},
		{"%i", []any{7}, "7"},
		{"%*d", []any{5, 1}, "    1"},
		{"%-*d|", []any{4, 1}, "1   |"},
		{"%.*f", []any{2, 1.23456}, "1.23"},
		{"%s %s", []any{"a"}, "!"},
		{"%s", []any{"a", "b"}, "!"},
		{"%d", []any{"x"}, "!"},
		{"%(a)s", []any{"x"}, "!"},
		{"%", nil, "!"},
		{"%z", []any{1}, "!"},
		{"%ld", []any{5}, "5"},
		{"%#g", []any{1.0}, "1.00000"},
		{"%f", []any{math.Inf(1)}, "inf"},
		{"%5.1f%%", []any{99.44}, " 99.4%"},
	}
	for _, c := range cases {
		got, err := PyFormat(c.format, c.args...)
		if c.want == "!" {
			if err == nil {
				t.Errorf("PyFormat(%q, %v) = %q, want an error", c.format, c.args, got)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("PyFormat(%q, %v) = %q, %v; want %q", c.format, c.args, got, err, c.want)
		}
	}
}

type fakeTr map[string]string

func (f fakeTr) T(s string) string {
	if v, ok := f[s]; ok {
		return v
	}
	return s
}

// TestFormatStatusText follows format_status_text of cms/grading.
func TestFormatStatusText(t *testing.T) {
	tr := fakeTr{"Evaluation didn't produce file %s": "No se produjo %s", "N/A": "N/D", "Output is correct": "Correcto"}
	for _, c := range []struct {
		status []string
		tr     Translator
		want   string
	}{
		{[]string{"Output is correct"}, tr, "Correcto"},
		{[]string{"Output is correct"}, nil, "Output is correct"},
		{[]string{"Evaluation didn't produce file %s", "out.txt"}, tr, "No se produjo out.txt"},
		{[]string{"Evaluation didn't produce file %s"}, tr, "N/D"},
		{[]string{"Unknown %s and %s", "a", "b"}, tr, "Unknown a and b"},
		{[]string{"", "x"}, tr, "N/D"},
		{[]string{""}, tr, ""},
		{nil, tr, "N/D"},
		{[]string{"100%% sure"}, nil, "100% sure"},
		{[]string{"%d points", "5"}, nil, "N/A"},
	} {
		if got := FormatStatusText(c.status, c.tr); got != c.want {
			t.Errorf("FormatStatusText(%q) = %q, want %q", c.status, got, c.want)
		}
	}
}

func TestFormatNamedAndHTML(t *testing.T) {
	if got := FormatNamed("Error %(status_code)s: %(n)d%%", "status_code", 404, "n", 3); got != "Error 404: 3%" {
		t.Errorf("FormatNamed: %q", got)
	}
	if got := FormatHTML(template.HTML("<b>%(a)s</b> %(n)d %(h)s"), "a", "<i>", "n", 7, "h", template.HTML("<u>u</u>")); got != "<b>&lt;i&gt;</b> 7 <u>u</u>" {
		t.Errorf("FormatHTML trusted: %q", got)
	}
	if got := FormatHTML("<b>%(a)s</b>", "a", "x"); got != "&lt;b&gt;x&lt;/b&gt;" {
		t.Errorf("FormatHTML plain string: %q", got)
	}
	if got := FormatHTML(template.HTML("%(missing)s %(none)s"), "none", nil); got != "%(missing)s None" {
		t.Errorf("FormatHTML missing: %q", got)
	}
}
