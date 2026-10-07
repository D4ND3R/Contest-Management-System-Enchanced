// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package buildinfo

import "testing"

func TestSourceURL(t *testing.T) {
	saveV, saveC, saveD, saveDate := Version, Commit, Dirty, Date
	defer func() { Version, Commit, Dirty, Date = saveV, saveC, saveD, saveDate }()

	cases := []struct {
		version, commit string
		dirty           bool
		base, want      string
	}{
		{"v1.2.3", "abc", false, "", DefaultSourceURL + "/tree/v1.2.3"},
		{"dev", "abc123", false, "https://github.com/me/fork/", "https://github.com/me/fork/tree/abc123"},
		{"dev", "abc123", true, "https://github.com/me/fork", "https://github.com/me/fork"},
		{"dev", "", false, "", DefaultSourceURL},
		{"v1.0.0", "abc", false, "https://git.example.org/cms", "https://git.example.org/cms"},
	}
	for _, c := range cases {
		Version, Commit, Dirty = c.version, c.commit, c.dirty
		if got := SourceURL(c.base); got != c.want {
			t.Errorf("SourceURL(%q) with %s/%s/%v = %q, want %q", c.base, c.version, c.commit, c.dirty, got, c.want)
		}
	}
	Version, Commit, Dirty, Date = "v2.0.0", "0123456789abcdef", true, "2026-10-07T00:00:00Z"
	if got := String(); got != "v2.0.0 (0123456789ab-dirty, 2026-10-07T00:00:00Z)" {
		t.Errorf("String() = %q", got)
	}
	Commit = ""
	if got := String(); got != "v2.0.0" {
		t.Errorf("String() without commit = %q", got)
	}
}
