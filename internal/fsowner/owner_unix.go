// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

//go:build unix

package fsowner

import (
	"os"
	"syscall"
)

// geteuid is os.Geteuid; tests replace it.
var geteuid = os.Geteuid

// ownerOf returns the user and group owning path.
func ownerOf(path string) (uid, gid int, ok bool) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, 0, false
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return int(sys.Uid), int(sys.Gid), true
}
