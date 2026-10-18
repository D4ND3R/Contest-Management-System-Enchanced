// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

//go:build !unix

package ctl

func tryLockFile(string) (func(), error) { return func() {}, nil }
