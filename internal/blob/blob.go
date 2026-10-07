// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Package blob is the content-addressed file store (CMS FileCacher). Every
// file (statements, attachments, test cases, managers, submissions,
// executables, outputs) is stored once under the lowercase hex SHA-256 of
// its content; the database only keeps digests.
//
// Backends:
//
//   - Local: a directory tree root/ab/cd/<digest> with atomic writes;
//   - S3: any S3-compatible service (objects prefix + ab/<digest>);
//   - Client: the HTTP blob server of the dispatcher (Server), used by
//     judges on other machines, which have no database access. They
//     authenticate with storage.remote.token (Token), never with
//     secret_key.
//
// Cache wraps any of them with an on-disk LRU cache bounded in bytes, for
// workers and web servers. Open builds the store described by cms.yaml.
//
// Digests come from users (URLs, uploaded dumps) and are used to build
// paths and object keys, so every backend rejects anything that is not a
// valid digest before touching the filesystem or the network. Readers
// returned by Open verify the content against its digest and fail with
// ErrCorrupt at the end of the stream if it does not match.
package blob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"sync"
)

// DigestLen is the length of a digest: hex SHA-256.
const DigestLen = 2 * sha256.Size

// Errors returned by every backend. Callers test them with errors.Is.
var (
	// ErrNotFound: no blob has that digest.
	ErrNotFound = errors.New("blob: not found")
	// ErrCorrupt: the stored content does not hash to its digest.
	ErrCorrupt = errors.New("blob: stored content does not match its digest")
	// ErrInvalidDigest: the string is not a lowercase hex SHA-256.
	ErrInvalidDigest = errors.New("blob: invalid digest")
	// ErrDigestMismatch: uploaded content does not hash to the digest it
	// was announced with (remote PUT); nothing was stored.
	ErrDigestMismatch = errors.New("blob: content does not hash to the announced digest")
	// ErrTooLarge: the content exceeds the size accepted by the store.
	ErrTooLarge = errors.New("blob: content too large")
	// ErrUnauthorized: the blob server rejected storage.remote.token (it
	// must be the output of `cms ctl blob-token` for the secret_key of the
	// dispatcher).
	ErrUnauthorized = errors.New("blob: the blob server rejected storage.remote.token " +
		"(it must be the output of `cms ctl blob-token` with the secret_key of the dispatcher)")
	// ErrUnsupported: the backend does not implement the operation (for
	// example Walk and Delete on the remote client).
	ErrUnsupported = errors.ErrUnsupported
)

// Store is a content-addressed blob store. Implementations are safe for
// concurrent use, including concurrent writers of the same content.
type Store interface {
	// Put stores the content read from r and returns its digest and size.
	// The content is streamed: it is never held in memory as a whole.
	Put(ctx context.Context, r io.Reader) (digest string, size int64, err error)
	// PutBytes stores b and returns its digest.
	PutBytes(ctx context.Context, b []byte) (string, error)
	// Open returns the content of a blob, or ErrNotFound. The reader
	// verifies the content and returns ErrCorrupt instead of io.EOF if it
	// does not hash to digest, so callers must read to the end before
	// trusting the data (io.Copy, io.ReadAll do).
	Open(ctx context.Context, digest string) (io.ReadCloser, error)
	// Stat returns the size of a blob, or ErrNotFound.
	Stat(ctx context.Context, digest string) (size int64, err error)
	// Delete removes a blob. Deleting a missing blob is not an error.
	Delete(ctx context.Context, digest string) error
	// Walk calls fn for every stored blob, in no particular order, until
	// fn returns an error (which Walk returns). Blobs added or deleted
	// during the walk may or may not be reported. Used by the garbage
	// collector.
	Walk(ctx context.Context, fn func(digest string, size int64) error) error
}

// Checker is implemented by stores that can report whether their backing
// service is usable (health checks).
type Checker interface {
	Check(ctx context.Context) error
}

// Check runs the health check of s, if it has one.
func Check(ctx context.Context, s Store) error {
	if c, ok := s.(Checker); ok {
		return c.Check(ctx)
	}
	return nil
}

// Close releases what s holds (the lock on a cache directory), if
// anything; s must not be used afterwards. Exiting the process releases it
// too.
func Close(s Store) error {
	if c, ok := s.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// ValidDigest reports whether d is a lowercase hex SHA-256: it matches
// ^[0-9a-f]{64}$ (the loop is equivalent to that regexp, see the tests, and
// several times faster on this hot path).
func ValidDigest(d string) bool {
	if len(d) != DigestLen {
		return false
	}
	for i := 0; i < len(d); i++ {
		c := d[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// Digest returns the digest of b.
func Digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// checkDigest returns ErrInvalidDigest (with a printable excerpt of d) when
// d is not a valid digest.
func checkDigest(d string) error {
	if ValidDigest(d) {
		return nil
	}
	if len(d) > DigestLen+8 {
		d = d[:DigestLen+8] + "…"
	}
	return fmt.Errorf("%w: %q", ErrInvalidDigest, d)
}

func notFound(d string) error { return fmt.Errorf("%w: %s", ErrNotFound, d) }

func corrupt(d string) error { return fmt.Errorf("%w: %s", ErrCorrupt, d) }

// shardDir returns the directory of a digest under root: root/ab/cd.
func shardDir(root, d string) string {
	return root + string(os.PathSeparator) + d[:2] + string(os.PathSeparator) + d[2:4]
}

// shardPath returns the path of a digest under root: root/ab/cd/<digest>.
func shardPath(root, d string) string {
	return shardDir(root, d) + string(os.PathSeparator) + d
}

// newHash returns the hash of digests. It is a variable so tests can count
// how many times content is hashed.
var newHash = sha256.New

// verifier hashes what is read through it and turns the final io.EOF into
// ErrCorrupt when the content does not match the expected digest.
type verifier struct {
	rc     io.ReadCloser
	h      hash.Hash
	digest string
	want   [sha256.Size]byte
	size   int64 // -1 if unknown
	err    error // sticky final error
}

func newVerifier(rc io.ReadCloser, digest string, size int64) *verifier {
	v := &verifier{rc: rc, h: newHash(), digest: digest, size: size}
	// digest was validated by the caller, so this cannot fail.
	_, _ = hex.Decode(v.want[:], []byte(digest))
	return v
}

func (v *verifier) Read(p []byte) (int, error) {
	if v.err != nil {
		return 0, v.err
	}
	n, err := v.rc.Read(p)
	if n > 0 {
		v.h.Write(p[:n])
	}
	switch {
	case err == io.EOF:
		var sum [sha256.Size]byte
		if !bytes.Equal(v.h.Sum(sum[:0]), v.want[:]) {
			v.err = corrupt(v.digest)
		} else {
			v.err = io.EOF
		}
		return n, v.err
	case err != nil:
		v.err = err
	}
	return n, err
}

func (v *verifier) Close() error { return v.rc.Close() }

// Size returns the length of the content, or -1 if unknown.
func (v *verifier) Size() int64 { return v.size }

// verifies reports whether reading rc to the end checks it against digest
// d: every backend of this package returns such a verifier from Open.
func verifies(rc io.Reader, d string) bool {
	v, ok := rc.(*verifier)
	return ok && v.digest == d
}

// sizer is implemented by readers that know the length of their content.
type sizer interface{ Size() int64 }

// knownSize returns the length of the content behind rc, or -1.
func knownSize(rc io.Reader) int64 {
	switch r := rc.(type) {
	case sizer:
		return r.Size()
	case interface{ Stat() (fs.FileInfo, error) }:
		if fi, err := r.Stat(); err == nil {
			return fi.Size()
		}
	}
	return -1
}

// ctxReader stops reading once ctx is done, so long copies honour
// cancellation.
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

// copyBufSize is large enough to amortise syscalls and small enough to pool.
const copyBufSize = 256 << 10

var copyBufs = sync.Pool{New: func() any { b := make([]byte, copyBufSize); return &b }}

// copyBuffered copies with a pooled buffer.
func copyBuffered(dst io.Writer, src io.Reader) (int64, error) {
	bp := copyBufs.Get().(*[]byte)
	defer copyBufs.Put(bp)
	return io.CopyBuffer(writerOnly{dst}, readerOnly{src}, *bp)
}

// writerOnly and readerOnly hide ReadFrom/WriteTo so io.CopyBuffer really
// uses the pooled buffer instead of allocating its own.
type writerOnly struct{ io.Writer }

type readerOnly struct{ io.Reader }

// expectPutter is implemented by stores that can check the digest of an
// upload before committing it (used by the blob server).
type expectPutter interface {
	putExpect(ctx context.Context, want string, r io.Reader) (int64, error)
}

// putExpect stores the content of r, which must hash to want: on mismatch
// it returns ErrDigestMismatch and nothing is stored.
func putExpect(ctx context.Context, s Store, want string, r io.Reader) (int64, error) {
	if err := checkDigest(want); err != nil {
		return 0, err
	}
	if p, ok := s.(expectPutter); ok {
		return p.putExpect(ctx, want, r)
	}
	sp, err := spoolReader(ctx, r, "", -1)
	if err != nil {
		return 0, err
	}
	defer sp.Close()
	if sp.digest != want {
		return 0, fmt.Errorf("%w: got %s, want %s", ErrDigestMismatch, sp.digest, want)
	}
	if _, _, err := s.Put(ctx, sp.reader()); err != nil {
		return 0, err
	}
	return sp.size, nil
}
