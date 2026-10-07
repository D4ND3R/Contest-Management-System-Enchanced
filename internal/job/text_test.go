// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package job_test

import (
	"math"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
)

// The expected strings are Python 3.12's str(float).
func TestFormatOutcome(t *testing.T) {
	for _, c := range []struct {
		v    float64
		want string
	}{
		{0, "0.0"}, {math.Copysign(0, -1), "-0.0"}, {1, "1.0"}, {0.5, "0.5"},
		{1.0 / 3, "0.3333333333333333"}, {2.0 / 3, "0.6666666666666666"},
		{0.30000000000000004, "0.30000000000000004"}, {1e-4, "0.0001"}, {1e-5, "1e-05"},
		{0.00012345, "0.00012345"}, {123456789, "123456789.0"}, {1e15, "1000000000000000.0"},
		{1e16, "1e+16"}, {1.5e16, "1.5e+16"}, {1e22, "1e+22"}, {-2.5, "-2.5"}, {1e-300, "1e-300"},
		{5e-324, "5e-324"}, {100, "100.0"}, {12345678901234567, "1.2345678901234568e+16"},
		{math.Inf(1), "inf"}, {math.Inf(-1), "-inf"}, {math.NaN(), "nan"},
	} {
		got := job.FormatOutcome(c.v)
		if got != c.want {
			t.Errorf("FormatOutcome(%v) = %q, want %q", c.v, got, c.want)
		}
		if f, err := strconv.ParseFloat(got, 64); !math.IsNaN(c.v) && (err != nil || f != c.v) {
			t.Errorf("%q does not parse back to %v: %v %v", got, c.v, f, err)
		}
	}
}

// The expected strings come from upstream's safe_get_str run on Python
// 3.12.
func TestSanitizeOutput(t *testing.T) {
	for in, want := range map[string]string{
		"  hello\n":                  "hello",
		"a\x00b\x07c\td\re\n":        "a�b�c\td\re",
		"\xff\xfeok":                 "��ok",
		"café © ‘q’ ¿":               "café � ‘q’ �",
		"\x1b[31mred\x1b[0m":         "�[31mred�[0m",
		"\n\n  \t":                   "",
		"\xc3":                       "�",
		"sum.cpp:1:5: error: x\r\n ": "sum.cpp:1:5: error: x",
	} {
		if got := job.SanitizeOutput([]byte(in)); got != want {
			t.Errorf("SanitizeOutput(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTruncateUTF8(t *testing.T) {
	s := "aé€😀b" // 1 + 2 + 3 + 4 + 1 bytes
	for n, want := range map[int]string{
		-1: "", 0: "", 1: "a", 2: "a", 3: "aé", 4: "aé", 5: "aé", 6: "aé€",
		7: "aé€", 8: "aé€", 9: "aé€", 10: "aé€😀", 11: s, 100: s,
	} {
		got := job.TruncateUTF8(s, n)
		if got != want || !utf8.ValidString(got) || len(got) > max(n, 0) {
			t.Errorf("TruncateUTF8(%q, %d) = %q, want %q", s, n, got, want)
		}
	}
	long := strings.Repeat("€", job.MaxOutputLen)
	if got := job.TruncateUTF8(long, job.MaxOutputLen); len(got) > job.MaxOutputLen || !utf8.ValidString(got) {
		t.Errorf("long: %d bytes", len(got))
	}
}
