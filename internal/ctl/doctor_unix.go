// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

//go:build unix

package ctl

import (
	"os"
	"strconv"
	"syscall"
)

// fileOwner is the uid of a file ("" if unknown).
func fileOwner(fi os.FileInfo) string {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return strconv.FormatUint(uint64(st.Uid), 10)
	}
	return ""
}

// freeBytes is the space available to unprivileged users on path's file
// system.
func freeBytes(path string) (uint64, bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, false
	}
	return st.Bavail * uint64(st.Bsize), true
}
