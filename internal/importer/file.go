// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package importer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
)

// File is the content of a file of a package (a statement, a testcase, a
// manager...). It is only read when the writer stores it in the blob
// store, streaming: loaders describe files without reading them, so a
// package with gigabytes of testcases never sits in memory. After Upload
// (or Hash, in a dry run) it carries its SHA-256 digest.
//
// A File is used by pointer; the same *File (or two Files with the same
// path) may appear several times in a model, e.g. the input of an
// output-only testcase that is also an attachment, and is stored once.
type File struct {
	name   string
	path   string
	data   []byte
	open   func() (io.ReadCloser, error)
	digest string
	size   int64
	// byDigest: the file was given by digest (FileFromDigest), its
	// content is in the blob store.
	byDigest bool
	// stored: the content is in the blob store (Upload).
	stored bool
}

// FileFromPath returns the file at path on disk.
func FileFromPath(path string) *File {
	return &File{name: path, path: filepath.Clean(path), size: -1}
}

// FileFromBytes returns a file held in memory (small generated files);
// name says where it comes from in messages.
func FileFromBytes(name string, b []byte) *File {
	if b == nil {
		b = []byte{}
	}
	return &File{name: name, data: b, size: int64(len(b))}
}

// FileFromFunc returns a file whose content open returns (a member of an
// archive...); open may be called more than once (once per attempt).
func FileFromFunc(name string, open func() (io.ReadCloser, error)) *File {
	return &File{name: name, open: open, size: -1}
}

// FileFromDigest returns a file already in the blob store (an export of
// this installation imported again, a file shared with another task).
// Upload checks that it exists.
func FileFromDigest(name, digest string) *File {
	return &File{name: name, digest: digest, size: -1, byDigest: true}
}

// Name says where the content comes from (its path, for files on disk).
func (f *File) Name() string { return f.name }

// Path is the path of a file on disk, "" for other files.
func (f *File) Path() string { return f.path }

// Digest is the SHA-256 of the content (lowercase hex) once stored or
// hashed, "" before.
func (f *File) Digest() string { return f.digest }

// Size is the length of the content, -1 if not known yet.
func (f *File) Size() int64 { return f.size }

// Open returns the content. A file known only by digest cannot be opened
// here (read it from the blob store).
func (f *File) Open() (io.ReadCloser, error) {
	switch {
	case f.path != "":
		return os.Open(f.path)
	case f.open != nil:
		return f.open()
	case f.byDigest:
		return nil, fmt.Errorf("%s: only its digest is known", f.name)
	}
	return io.NopCloser(bytes.NewReader(f.data)), nil
}

// ErrMissingFile is returned (wrapped) by Upload when a file on disk does
// not exist, or a file given by digest is not in the blob store.
var ErrMissingFile = errors.New("missing file")

// DefaultUploads is how many files Upload stores at the same time.
const DefaultUploads = 4

// Upload stores the content of files in the blob store (hashing it with
// SHA-256 on the way) and records their digests, at most parallel at a
// time (DefaultUploads if parallel <= 0). Files stored already are not
// read again; files with the same path are read once; nil entries are
// skipped. Content that ends up unused (the transaction that would
// reference it fails) stays in the store until the garbage collector
// removes it, which is harmless: blobs are addressed by content.
func Upload(ctx context.Context, store blob.Store, files []*File, parallel int) error {
	return process(ctx, store, files, parallel, false)
}

// Hash computes the digests of files without storing them (a dry run),
// like Upload otherwise; files given by digest are looked up in store
// (if not nil). A file hashed is still stored by a later Upload.
func Hash(ctx context.Context, store blob.Store, files []*File, parallel int) error {
	return process(ctx, store, files, parallel, true)
}

func process(ctx context.Context, store blob.Store, files []*File, parallel int, hashOnly bool) error {
	if parallel <= 0 {
		parallel = DefaultUploads
	}
	// One read per distinct file: by pointer, then by path.
	seen := make(map[*File]bool, len(files))
	byPath := make(map[string]*File)
	var todo []*File
	var aliases [][2]*File // [copy, original]: same path
	for _, f := range files {
		if f == nil || seen[f] || f.stored || hashOnly && f.digest != "" && !f.byDigest {
			continue
		}
		seen[f] = true
		if f.path != "" {
			if orig, ok := byPath[f.path]; ok {
				aliases = append(aliases, [2]*File{f, orig})
				continue
			}
			byPath[f.path] = f
		}
		todo = append(todo, f)
	}
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(parallel)
	for _, f := range todo {
		g.Go(func() error { return processOne(ctx, store, f, hashOnly) })
	}
	if err := g.Wait(); err != nil {
		return err
	}
	for _, a := range aliases {
		a[0].digest, a[0].size, a[0].stored = a[1].digest, a[1].size, a[1].stored
	}
	return nil
}

func processOne(ctx context.Context, store blob.Store, f *File, hashOnly bool) error {
	if f.byDigest {
		// Known by digest only: it must be in the store already.
		if store == nil {
			return nil
		}
		size, err := store.Stat(ctx, f.digest)
		if errors.Is(err, blob.ErrNotFound) {
			return fmt.Errorf("%s: %w: blob %s is not in the store", f.name, ErrMissingFile, f.digest)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", f.name, err)
		}
		f.size, f.stored = size, true
		return nil
	}
	if f.data != nil && !hashOnly {
		d, err := store.PutBytes(ctx, f.data)
		if err != nil {
			return fmt.Errorf("%s: %w", f.name, err)
		}
		f.digest, f.size, f.stored = d, int64(len(f.data)), true
		return nil
	}
	rc, err := f.Open()
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s: %w", f.name, ErrMissingFile)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", f.name, err)
	}
	defer rc.Close()
	if hashOnly {
		h := sha256.New()
		n, err := io.Copy(h, ctxReader{ctx, rc})
		if err != nil {
			return fmt.Errorf("%s: %w", f.name, err)
		}
		f.digest, f.size = hex.EncodeToString(h.Sum(nil)), n
		return nil
	}
	d, size, err := store.Put(ctx, rc)
	if err != nil {
		return fmt.Errorf("%s: %w", f.name, err)
	}
	f.digest, f.size, f.stored = d, size, true
	return nil
}

// ctxReader stops reading when ctx is done.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// statCache remembers which paths exist, for the validation of large
// packages that name the same file many times.
type statCache struct {
	mu   sync.Mutex
	seen map[string]error
}

func (c *statCache) check(path string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seen == nil {
		c.seen = make(map[string]error)
	}
	if err, ok := c.seen[path]; ok {
		return err
	}
	st, err := os.Stat(path)
	if err == nil && st.IsDir() {
		err = fmt.Errorf("%s is a directory", path)
	}
	c.seen[path] = err
	return err
}
