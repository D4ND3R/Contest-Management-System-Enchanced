// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

//go:build linux && !amd64 && !arm64

package sandbox

// The seccomp policy is only defined for amd64 and arm64; on other
// architectures defaultPolicy fails and worker.seccomp must be false.
const (
	nativeArch     = 0
	nativeX32Check = false
)

var archDenied []uint32
