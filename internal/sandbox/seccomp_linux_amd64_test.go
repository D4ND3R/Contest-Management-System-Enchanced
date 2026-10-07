// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package sandbox

import "golang.org/x/sys/unix"

// archDeniedHelpers are the amd64-only denied calls TestSeccompPolicy
// checks: the x32 ABI and port I/O.
var archDeniedHelpers = []string{"x32", "iopl"}

func init() {
	helpers["x32"] = func() int { sys(unix.SYS_GETPID | x32SyscallBit); return 0 }
	helpers["iopl"] = func() int { sys(unix.SYS_IOPL, 3); return 0 }
}
