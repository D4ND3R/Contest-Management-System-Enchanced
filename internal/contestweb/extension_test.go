// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package contestweb

import (
	"regexp"
	"testing"
)

// replaceExtensionRegexp is CMS's re.sub(r'%s$' % extension, '.%l',
// filename), the reference of replaceExtension.
func replaceExtensionRegexp(filename, ext string) string {
	re, err := regexp.Compile("(?:" + ext + ")$")
	if err != nil {
		return filename
	}
	return re.ReplaceAllLiteralString(filename, ".%l")
}

func TestReplaceExtension(t *testing.T) {
	for _, c := range []struct{ name, ext, want string }{
		{"sum.cpp", ".cpp", "sum.%l"},
		{"sumxcpp", ".cpp", "sum.%l"}, // the dot matches any character
		{"sum€cpp", ".cpp", "sum.%l"}, // a whole character
		{"sum\ncpp", ".cpp", "sum\ncpp"},
		{"a.py", ".cpp", "a.py"},
		{"x.c", ".c", "x.%l"},
		{"x.cc", ".c", "x..%l"}, // as CMS
		{".c", ".c", ".%l"},
		{"c", ".c", "c"},
		{"", ".c", ""},
		{"x.java", ".java", "x.%l"},
		{"x.hs", "", "x.hs.%l"},
		{"x.c++", ".c\\+\\+", "x.%l"}, // regular expression syntax
		{"x.ccx", ".c+x", "x.%l"},
	} {
		if got := replaceExtension(c.name, c.ext); got != c.want {
			t.Errorf("replaceExtension(%q, %q) = %q, want %q", c.name, c.ext, got, c.want)
		}
		if ref := replaceExtensionRegexp(c.name, c.ext); ref != c.want {
			t.Errorf("reference (%q, %q) = %q, want %q", c.name, c.ext, ref, c.want)
		}
	}
	// No regular expression is compiled per download (it took 25
	// allocations, ≈ 2 µs).
	if n := testing.AllocsPerRun(100, func() { _ = replaceExtension("sum.cpp", ".cpp") }); n > 1 {
		t.Errorf("%v allocations per call", n)
	}
}

func BenchmarkReplaceExtension(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_ = replaceExtension("sum.cpp", ".cpp")
	}
}
