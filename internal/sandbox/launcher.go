// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package sandbox

import (
	"os"
)

// LauncherCommand is the hidden subcommand of the cms binary that starts a
// contestant program under the seccomp filter:
//
//	cms sandbox-exec [--policy=default] [--nproc=N] [--rlimit-as=KiB]
//	                 [--restore-env=NAME[=VALUE]]... -- program args...
//
// It is not meant to be typed by people: the sandbox puts the binary in
// the box (read-only, at LauncherDir) and prepends this command to the
// argv of programs run with RunSpec.Seccomp.
const LauncherCommand = "sandbox-exec"

// LauncherDir is where the directory holding the launcher binary is
// mounted inside the box.
const LauncherDir = "/cms-launcher"

// launcherBinary is the name of the launcher inside LauncherDir.
const launcherBinary = "cms"

// MaybeRunLauncher turns the current process into the launcher when it
// was invoked as `<binary> sandbox-exec ...`, and never returns in that
// case. The cms binary calls it first thing in main. Tests that run
// programs with RunSpec.Seccomp use the test binary as the launcher, so
// their TestMain must call it first:
//
//	func TestMain(m *testing.M) {
//		sandbox.MaybeRunLauncher()
//		os.Exit(m.Run())
//	}
func MaybeRunLauncher() {
	if len(os.Args) > 1 && os.Args[1] == LauncherCommand {
		os.Exit(LauncherMain(os.Args[2:]))
	}
}
