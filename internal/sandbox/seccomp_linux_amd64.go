// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package sandbox

import "golang.org/x/sys/unix"

const (
	nativeArch     = unix.AUDIT_ARCH_X86_64
	nativeX32Check = true
)

// archDenied adds the amd64-only system calls: port I/O and obsolete
// calls that load code into the kernel or the address space.
var archDenied = []uint32{
	unix.SYS_IOPL,
	unix.SYS_IOPERM,
	unix.SYS_USELIB,
	unix.SYS_CREATE_MODULE,
	unix.SYS__SYSCTL,
}
