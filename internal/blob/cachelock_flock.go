// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package blob

import (
	"errors"
	"os"
	"syscall"
)

// errLocked: another open file description holds the lock.
var errLocked = errors.New("blob: locked")

// tryLock takes an exclusive flock on f without waiting. flock locks
// belong to the open file description, so they also exclude other Caches
// of the same process, and the kernel drops them when the process dies.
// Go opens files with O_CLOEXEC: sandboxed children never inherit them.
func tryLock(f *os.File) error {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, syscall.EINTR):
			continue
		case errors.Is(err, syscall.EWOULDBLOCK):
			return errLocked
		default:
			return err
		}
	}
}
