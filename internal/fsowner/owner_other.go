// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

//go:build !unix

package fsowner

// Without Unix owners there is nothing to fix.
func geteuid() int { return -1 }

func ownerOf(string) (uid, gid int, ok bool) { return 0, 0, false }
