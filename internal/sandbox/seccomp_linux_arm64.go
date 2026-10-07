// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package sandbox

import "golang.org/x/sys/unix"

const (
	nativeArch     = unix.AUDIT_ARCH_AARCH64
	nativeX32Check = false
)

// archDenied adds nothing on arm64: iopl, ioperm and the obsolete module
// calls do not exist there.
var archDenied []uint32
