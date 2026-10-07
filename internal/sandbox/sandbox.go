// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Package sandbox runs untrusted programs: a wrapper of isolate (cgroups
// v2) plus a launcher with a seccomp filter that yields the "Security
// Violation" verdict. Every directory handed to isolate is absolute:
// isolate rejects relative --dir values.
//
// Phase F0 only provides the host check used by `make test-sandbox` and
// CI; the wrapper arrives in F2.
package sandbox

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

var versionRE = regexp.MustCompile(`(\d+)\.(\d+)`)

// IsolateVersion returns the major and minor version of the isolate binary.
func IsolateVersion(ctx context.Context, path string) (int, int, error) {
	out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if err != nil {
		return 0, 0, fmt.Errorf("running %s --version: %w (%s)", path, err, strings.TrimSpace(string(out)))
	}
	m := versionRE.FindStringSubmatch(string(out))
	if m == nil {
		return 0, 0, fmt.Errorf("cannot read the version of isolate from %q", out)
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return major, minor, nil
}
