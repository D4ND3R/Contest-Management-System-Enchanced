// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

//go:build unix

package ctl

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
)

// fixBlobOwnership gives a blob restored by root into the local store (and
// its shard directories, root/ab/cd/digest) the owner of the store's root
// directory, so that the services, which run as that user, can use it.
func fixBlobOwnership(cfg config.Storage, digest string) {
	if cfg.Backend != "local" || os.Geteuid() != 0 || len(digest) < 4 {
		return
	}
	root := cfg.Local.Path
	st, err := os.Stat(root)
	if err != nil {
		return
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok || sys.Uid == 0 {
		return
	}
	uid, gid := int(sys.Uid), int(sys.Gid)
	a := filepath.Join(root, digest[:2])
	b := filepath.Join(a, digest[2:4])
	for _, p := range []string{a, b, filepath.Join(b, digest)} {
		_ = os.Lchown(p, uid, gid)
	}
}

// tryLockFile takes an exclusive, non-blocking lock on path (created if
// needed) and returns its release.
func tryLockFile(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("locking %s: %w", path, err)
	}
	return func() { f.Close() }, nil
}
