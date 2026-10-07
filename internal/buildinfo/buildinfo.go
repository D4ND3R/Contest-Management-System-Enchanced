// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Package buildinfo exposes the version of the running binary and the URL
// of its source code. Every web site links to that URL in its footer, as
// required by the AGPL for software used over a network.
package buildinfo

import (
	"runtime/debug"
	"strings"
)

// DefaultSourceURL is the repository this binary is built from. Forks that
// modify the code must point source_url (in cms.yaml) to their own copy.
const DefaultSourceURL = "https://github.com/D4ND3R/Contest-Management-System-Enchanced"

// These are set at link time by the Makefile and GoReleaser:
//
//	-X .../internal/buildinfo.Version=v1.2.3
//	-X .../internal/buildinfo.Commit=<sha>
//	-X .../internal/buildinfo.Date=<rfc3339>
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

// Dirty reports whether the binary was built from a tree with uncommitted
// changes (only known when the Go toolchain embedded VCS information).
var Dirty bool

func init() {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			if Commit == "" {
				Commit = s.Value
			}
		case "vcs.time":
			if Date == "" {
				Date = s.Value
			}
		case "vcs.modified":
			Dirty = s.Value == "true"
		}
	}
}

// String returns a one-line human description of the build.
func String() string {
	var b strings.Builder
	b.WriteString(Version)
	if Commit != "" {
		b.WriteString(" (")
		b.WriteString(shortCommit())
		if Dirty {
			b.WriteString("-dirty")
		}
		if Date != "" {
			b.WriteString(", ")
			b.WriteString(Date)
		}
		b.WriteString(")")
	}
	return b.String()
}

func shortCommit() string {
	if len(Commit) > 12 {
		return Commit[:12]
	}
	return Commit
}

// SourceURL returns the link to the exact source of the running version,
// given the configured repository base URL (empty means DefaultSourceURL).
// Release builds point at their tag, other builds at their commit.
func SourceURL(base string) string {
	if base == "" {
		base = DefaultSourceURL
	}
	base = strings.TrimRight(base, "/")
	if !strings.HasPrefix(base, "https://github.com/") && !strings.HasPrefix(base, "https://gitlab.com/") {
		// Unknown forge: we do not know its URL scheme for refs.
		return base
	}
	switch {
	case strings.HasPrefix(Version, "v") && !Dirty:
		return base + "/tree/" + Version
	case Commit != "" && !Dirty:
		return base + "/tree/" + Commit
	default:
		return base
	}
}
