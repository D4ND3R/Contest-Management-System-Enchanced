// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from delete_sandbox in cms/grading/tasktypes/util.py and
// Sandbox.archive in cms/grading/Sandbox.py of CMS:
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2012-2014 Luca Wehrstedt <luca.wehrstedt@gmail.com>

package tasktype

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"syscall"
)

// maxArchive bounds the content of an archived box: the files were
// written by the contestant's program (and the compilers).
const maxArchive = 256 << 20

// keeper is implemented by boxes that can stay on disk after Close
// (sandbox.Box.Keep).
type keeper interface{ Keep() }

// closeBox releases a box (upstream delete_sandbox): it archives it first
// when the operation asks for it (Result.SandboxDigests), and keeps it on
// disk when the job asks for it (sandbox.keep_sandbox keeps every box).
// Neither a failed archive nor a box that cannot be deleted fails the
// job: they are logged. Closing a box twice is harmless.
//
// Unlike upstream, the boxes of a job that failed are not kept (set
// keep_sandbox to debug failures): the next job would reuse their ids.
func (r *run) closeBox(b Box) {
	r.mu.Lock()
	if r.closed[b] {
		r.mu.Unlock()
		return
	}
	if r.closed == nil {
		r.closed = map[Box]bool{}
	}
	r.closed[b] = true
	r.mu.Unlock()

	if r.j.Operation.ArchiveSandbox {
		d, err := r.archive(b)
		if err != nil {
			r.env.log().Warn("cannot archive a sandbox", "dir", b.Dir(), "err", err, "job", r.j.Describe())
		} else {
			r.mu.Lock()
			if r.res.SandboxDigests == nil {
				r.res.SandboxDigests = map[string]string{}
			}
			r.res.SandboxDigests[b.Dir()] = d
			r.mu.Unlock()
		}
	}
	if r.j.KeepSandbox {
		if k, ok := b.(keeper); ok {
			k.Keep()
		}
	}
	if err := b.Close(); err != nil {
		r.env.log().Warn("cannot release a sandbox", "dir", b.Dir(), "err", err, "job", r.j.Describe())
	}
}

// archive stores a gzipped tar of the directory of a box (its home, the
// meta files and the log of the commands) and returns its digest
// (upstream Sandbox.archive). Its programs must have ended.
func (r *run) archive(b Box) (string, error) {
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(writeArchive(pw, b.Dir())) }()
	d, _, err := r.env.Store.Put(r.ctx, pr)
	pr.CloseWithError(errors.New("archive: the store stopped reading"))
	if err != nil {
		return "", fmt.Errorf("archiving %s: %w", b.Dir(), err)
	}
	return d, nil
}

// writeArchive writes dir as a gzipped tar whose entries start with the
// base name of dir, like upstream. Links are stored as links, never
// followed; FIFOs and other special files are left out.
func writeArchive(w io.Writer, dir string) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	base := filepath.Base(dir)
	var total int64
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		link := ""
		switch mode := info.Mode(); {
		case mode&fs.ModeSymlink != 0:
			if link, err = os.Readlink(p); err != nil {
				return err
			}
		case !mode.IsRegular() && !mode.IsDir():
			return nil // FIFOs (Communication), sockets, devices
		}
		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		hdr.Name = path.Join(base, filepath.ToSlash(rel))
		if info.IsDir() {
			hdr.Name += "/"
		}
		if total += hdr.Size; total > maxArchive {
			return fmt.Errorf("more than %d bytes", maxArchive)
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.CopyN(tw, f, hdr.Size)
		return err
	})
	if err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}
