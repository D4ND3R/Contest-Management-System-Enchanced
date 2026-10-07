// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package blob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Permissions of the tree: only the cms user may read blobs (test data is
// secret, and sandboxed programs run as other users).
const (
	dirPerm  = 0o700
	tmpName  = "tmp"
	staleTmp = 24 * time.Hour
)

// Local stores blobs in a directory tree root/ab/cd/<digest> (CMS
// FSBackend). Writes go to root/tmp, are fsynced and renamed into place,
// so a blob is either complete or absent, also after a crash, and
// concurrent writers of the same content are harmless.
type Local struct {
	root string
	tmp  string
}

// NewLocal opens (creating it if needed) a store rooted at root, which is
// converted to an absolute path. Temporary files older than a day, left
// by crashed writers, are removed.
func NewLocal(root string) (*Local, error) {
	if root == "" {
		return nil, errors.New("blob: local store: empty path")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("blob: local store: %w", err)
	}
	s := &Local{root: abs, tmp: filepath.Join(abs, tmpName)}
	if err := os.MkdirAll(s.tmp, dirPerm); err != nil {
		return nil, fmt.Errorf("blob: local store: %w", err)
	}
	removeStaleTemps(s.tmp, staleTmp)
	return s, nil
}

// Root returns the absolute root directory.
func (s *Local) Root() string { return s.root }

// path returns where a (valid) digest is stored.
func (s *Local) path(d string) string { return shardPath(s.root, d) }

// Put implements Store.
func (s *Local) Put(ctx context.Context, r io.Reader) (string, int64, error) {
	return s.put(ctx, "", r)
}

func (s *Local) putExpect(ctx context.Context, want string, r io.Reader) (int64, error) {
	_, n, err := s.put(ctx, want, r)
	return n, err
}

// put streams r to a temporary file while hashing it, then commits it.
// A non-empty want is the digest the content must have.
func (s *Local) put(ctx context.Context, want string, r io.Reader) (_ string, _ int64, err error) {
	if err := ctx.Err(); err != nil {
		return "", 0, err
	}
	f, err := os.CreateTemp(s.tmp, "put-*")
	if err != nil {
		return "", 0, fmt.Errorf("blob: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = f.Close()
			_ = os.Remove(f.Name())
		}
	}()
	h := sha256.New()
	n, err := copyBuffered(io.MultiWriter(f, h), ctxReader{ctx, r})
	if err != nil {
		return "", 0, fmt.Errorf("blob: writing: %w", err)
	}
	d := hex.EncodeToString(h.Sum(nil))
	if want != "" && d != want {
		return "", 0, fmt.Errorf("%w: got %s, want %s", ErrDigestMismatch, d, want)
	}
	if err := s.commit(f, d); err != nil {
		return "", 0, err
	}
	committed = true
	return d, n, nil
}

// PutBytes implements Store. Content already present costs one stat.
func (s *Local) PutBytes(ctx context.Context, b []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	d := Digest(b)
	if _, err := os.Stat(s.path(d)); err == nil {
		return d, nil
	}
	f, err := os.CreateTemp(s.tmp, "put-*")
	if err != nil {
		return "", fmt.Errorf("blob: %w", err)
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("blob: writing: %w", err)
	}
	if err := s.commit(f, d); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", err
	}
	return d, nil
}

// commit moves the temporary file f, holding the content of d, into
// place. If the blob already exists the temporary file is just dropped,
// so re-putting known content never pays for an fsync. On success f is
// closed and gone.
func (s *Local) commit(f *os.File, d string) error {
	final := s.path(d)
	if _, err := os.Stat(final); err == nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return nil
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("blob: fsync: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("blob: %w", err)
	}
	if err := makeShardDirs(s.root, d, true); err != nil {
		return err
	}
	// rename is atomic: readers see the old state or the whole file, and
	// concurrent writers of the same digest replace identical content.
	if err := os.Rename(f.Name(), final); err != nil {
		return fmt.Errorf("blob: %w", err)
	}
	// Persist the new directory entry.
	return syncDir(filepath.Dir(final))
}

// Open implements Store.
func (s *Local) Open(ctx context.Context, d string) (io.ReadCloser, error) {
	if err := checkDigest(d); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.Open(s.path(d))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, notFound(d)
		}
		return nil, fmt.Errorf("blob: %w", err)
	}
	size := int64(-1)
	if fi, err := f.Stat(); err == nil {
		size = fi.Size()
	}
	return newVerifier(f, d, size), nil
}

// Stat implements Store.
func (s *Local) Stat(ctx context.Context, d string) (int64, error) {
	if err := checkDigest(d); err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	fi, err := os.Stat(s.path(d))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, notFound(d)
		}
		return 0, fmt.Errorf("blob: %w", err)
	}
	return fi.Size(), nil
}

// Delete implements Store. Empty shard directories are kept: removing
// them would race with concurrent writers.
func (s *Local) Delete(ctx context.Context, d string) error {
	if err := checkDigest(d); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Remove(s.path(d)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("blob: %w", err)
	}
	return nil
}

// Walk implements Store, in lexical order of digests.
func (s *Local) Walk(ctx context.Context, fn func(digest string, size int64) error) error {
	return walkTree(ctx, s.root, func(d string, fi fs.FileInfo) error {
		return fn(d, fi.Size())
	})
}

// Check implements Checker: the root must still be a directory.
func (s *Local) Check(context.Context) error {
	fi, err := os.Stat(s.tmp)
	if err != nil {
		return fmt.Errorf("blob: local store: %w", err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("blob: local store: %s is not a directory", s.tmp)
	}
	return nil
}

// makeShardDirs creates root/ab and root/ab/cd if missing. With durable
// set, the parent of every directory it creates is fsynced so the new
// entries survive a crash.
func makeShardDirs(root, d string, durable bool) error {
	d1 := filepath.Join(root, d[:2])
	d2 := filepath.Join(d1, d[2:4])
	for _, dir := range [2]string{d1, d2} {
		err := os.Mkdir(dir, dirPerm)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("blob: %w", err)
		}
		if durable {
			if err := syncDir(filepath.Dir(dir)); err != nil {
				return err
			}
		}
	}
	return nil
}

// syncDir fsyncs a directory (its entries).
func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("blob: %w", err)
	}
	defer f.Close()
	if err := f.Sync(); err != nil {
		return fmt.Errorf("blob: fsync %s: %w", dir, err)
	}
	return nil
}

// removeStaleTemps deletes files in dir older than age. Younger files may
// belong to writers of another process sharing the directory.
func removeStaleTemps(dir string, age time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-age)
	for _, e := range entries {
		if fi, err := e.Info(); err == nil && fi.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// isHexName reports whether name is n lowercase hex characters.
func isHexName(name string, n int) bool {
	if len(name) != n {
		return false
	}
	for i := 0; i < n; i++ {
		c := name[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// walkTree calls fn for every blob file under root (layout ab/cd/<digest>),
// ignoring anything else (the tmp directory, stray files, digests in the
// wrong shard).
func walkTree(ctx context.Context, root string, fn func(d string, fi fs.FileInfo) error) error {
	level1, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("blob: %w", err)
	}
	for _, e1 := range level1 {
		if !e1.IsDir() || !isHexName(e1.Name(), 2) {
			continue
		}
		p1 := filepath.Join(root, e1.Name())
		level2, err := os.ReadDir(p1)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return fmt.Errorf("blob: %w", err)
		}
		for _, e2 := range level2 {
			if !e2.IsDir() || !isHexName(e2.Name(), 2) {
				continue
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			p2 := filepath.Join(p1, e2.Name())
			files, err := os.ReadDir(p2)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				return fmt.Errorf("blob: %w", err)
			}
			prefix := e1.Name() + e2.Name()
			for _, f := range files {
				d := f.Name()
				if !f.Type().IsRegular() || !ValidDigest(d) || d[:4] != prefix {
					continue
				}
				fi, err := f.Info()
				if err != nil {
					continue // deleted meanwhile
				}
				if err := fn(d, fi); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
