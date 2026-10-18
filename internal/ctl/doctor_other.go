// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

//go:build !unix

package ctl

import "os"

func fileOwner(os.FileInfo) string { return "" }

func freeBytes(string) (uint64, bool) { return 0, false }
