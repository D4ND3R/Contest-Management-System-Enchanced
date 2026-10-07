// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

//go:build !linux

package sandbox

import (
	"fmt"
	"os"
)

// LauncherMain fails outside Linux: seccomp and isolate are Linux-only.
func LauncherMain(args []string) int {
	fmt.Fprintf(os.Stderr, "cms %s: only supported on Linux\n", LauncherCommand)
	return 125
}

func checkSeccompSupport() error {
	return fmt.Errorf("seccomp is only supported on Linux")
}
