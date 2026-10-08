// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package sandbox

// The seccomp launcher puts contestant programs under the seccomp filter
// (RunSpec.Seccomp): instead of the program, the box runs
//
//	/cms-launcher/seccomp-exec program args...
//
// which sets no_new_privs, installs the filter and executes the program
// (no PATH search, exactly like isolate) with the same arguments and
// environment.
//
// The launcher is a tiny static ELF executable that the worker writes at
// startup (launcherImage, installLauncher): a few dozen machine
// instructions for the native architecture followed by the BPF program of
// the policy, about 1 KiB in all. Nothing else runs in the box before the
// program, so the program pays practically nothing for it: no runtime
// start-up, no memory, no threads. The program gets the limits of its run
// straight from isolate, like a run without the filter.

// LauncherDir is where the directory holding the launcher is mounted
// inside the box (read-only).
const LauncherDir = "/cms-launcher"

// launcherBinary is the name of the launcher inside LauncherDir.
const launcherBinary = "seccomp-exec"

// Exit statuses of the launcher when it cannot run the program, with a
// message on the standard error. They are reported as a runtime error
// like any other exit status (a contestant program can exit with them
// too, so they cannot mean "sandbox error"); Sandbox.SelfTest catches
// hosts where the filter cannot be installed.
const (
	launcherSetupFailed = 125
	launcherExecFailed  = 127 // like a shell
)
