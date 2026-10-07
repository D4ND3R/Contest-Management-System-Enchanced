// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly)

package blob

import (
	"errors"
	"os"
)

var errLocked = errors.New("blob: locked")

// tryLock does nothing where flock is not available (CMS runs on Linux;
// this keeps the package building elsewhere for development).
func tryLock(*os.File) error { return nil }
