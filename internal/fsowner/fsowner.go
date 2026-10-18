// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Package fsowner keeps the files that root creates in the directories of
// an installation usable by the services. The services run as their own
// user (cms), and the administration commands are often run with sudo
// (`sudo cmsctl import-contest ...`): a blob, a shard directory or a
// backup created by root would otherwise belong to root, and the
// services, which keep their data readable only by themselves, could not
// read it. As root, the files and directories created under a directory
// owned by another user are given to that user.
package fsowner

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// Owner is the user and group that files created by root are given.
type Owner struct {
	UID, GID int
}

// Apply gives paths to o (links themselves, not their targets). A nil
// Owner does nothing: the process is not root, or the directory belongs
// to root.
func (o *Owner) Apply(paths ...string) error {
	if o == nil {
		return nil
	}
	for _, p := range paths {
		if err := os.Lchown(p, o.UID, o.GID); err != nil {
			return err
		}
	}
	return nil
}

// ApplyTree gives root and everything under it to o (nil: nothing).
func (o *Owner) ApplyTree(root string) error {
	if o == nil {
		return nil
	}
	return filepath.WalkDir(root, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return o.Apply(p)
	})
}

// Of returns the owner to give what this process creates under dir: the
// owner of dir when the process runs as root and dir belongs to another
// user, else nil (files get the owner of the process, as usual).
func Of(dir string) *Owner {
	if geteuid() != 0 {
		return nil
	}
	uid, gid, ok := ownerOf(dir)
	if !ok || uid == 0 {
		return nil
	}
	return &Owner{UID: uid, GID: gid}
}

// MkdirAll is os.MkdirAll, except that as root the directories it
// creates are given to the owner of the closest directory that existed
// (see Of): `sudo cmsctl backup` creating /var/lib/cms/backups leaves it
// to the user of /var/lib/cms.
func MkdirAll(path string, perm fs.FileMode) error {
	if geteuid() != 0 {
		return os.MkdirAll(path, perm)
	}
	path = filepath.Clean(path)
	// The directories to create, deepest first.
	var missing []string
	existing := path
	for {
		if _, err := os.Stat(existing); err == nil {
			break
		} else if !errors.Is(err, fs.ErrNotExist) {
			break // let MkdirAll report it
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			break
		}
		missing = append(missing, existing)
		existing = parent
	}
	if err := os.MkdirAll(path, perm); err != nil {
		return err
	}
	o := Of(existing)
	for i := len(missing) - 1; i >= 0; i-- {
		if err := o.Apply(missing[i]); err != nil {
			return err
		}
	}
	return nil
}
