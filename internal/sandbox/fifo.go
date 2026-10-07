// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package sandbox

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// FIFODir is a host directory of named pipes that several boxes mount at
// the same time: the Communication task type connects the manager and
// each user process this way (CMS creates one such directory per user
// process, mounted at /fifo<i>).
//
// Each box runs as its own user, so the directory is 0755 and the pipes
// 0666, like in CMS. Mount it read-write in every box that uses it.
type FIFODir struct {
	dir  string
	keep bool
}

// NewFIFODir creates a directory of named pipes (plain names, no slashes)
// in the directory of the box, typically the manager's: it goes away with
// the box (or earlier with FIFODir.Close), also after a crash.
func (b *Box) NewFIFODir(names ...string) (*FIFODir, error) {
	dir, err := os.MkdirTemp(b.outer, "fifo-")
	if err != nil {
		return nil, fmt.Errorf("sandbox: %w", err)
	}
	f := &FIFODir{dir: dir, keep: b.s.cfg.KeepSandbox}
	fail := func(err error) (*FIFODir, error) {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("sandbox: %w", err)
	}
	if err := checkMountPath(dir); err != nil {
		return fail(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		return fail(err)
	}
	for _, n := range names {
		if n == "" || n == "." || n == ".." || strings.ContainsAny(n, "/\x00") {
			return fail(fmt.Errorf("invalid FIFO name %q", n))
		}
		p := filepath.Join(dir, n)
		if err := unix.Mkfifo(p, 0o666); err != nil {
			return fail(fmt.Errorf("mkfifo %s: %w", p, err))
		}
		if err := os.Chmod(p, 0o666); err != nil { // the umask removed bits
			return fail(err)
		}
	}
	return f, nil
}

// Path is the host path of the directory.
func (f *FIFODir) Path() string { return f.dir }

// Mount returns the Dir that makes the directory visible at inside (an
// absolute path such as /fifo0) in a box.
func (f *FIFODir) Mount(inside string) Dir {
	return Dir{Inside: path.Clean(inside), Outside: f.dir, Writable: true}
}

// Close deletes the directory unless keep_sandbox is set.
func (f *FIFODir) Close() error {
	if f.keep {
		return nil
	}
	return os.RemoveAll(f.dir)
}
