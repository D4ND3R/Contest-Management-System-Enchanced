// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package blob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// spoolMemLimit is the largest content a spool keeps in memory; longer
// content goes to an unlinked temporary file.
const spoolMemLimit = 4 << 20

// spool holds content whose digest must be known before it is uploaded
// (S3 object keys and remote PUT URLs contain the digest).
type spool struct {
	mem    []byte
	file   *os.File // unlinked: nothing is left behind after a crash
	size   int64
	digest string
}

// spoolReader reads r to the end, hashing it. Content longer than
// spoolMemLimit is written to a temporary file in dir (os.TempDir() if
// empty). maxSize < 0 means no limit; above it ErrTooLarge is returned.
func spoolReader(ctx context.Context, r io.Reader, dir string, maxSize int64) (_ *spool, err error) {
	h := sha256.New()
	sp := &spool{}
	defer func() {
		if err != nil {
			sp.Close()
		}
	}()
	src := io.Reader(ctxReader{ctx, r})
	if maxSize >= 0 {
		// Read one byte more than allowed to detect oversized content.
		src = io.LimitReader(src, maxSize+1)
	}
	src = io.TeeReader(src, h)
	// Up to spoolMemLimit in memory.
	var buf bytes.Buffer
	n, err := io.Copy(&buf, io.LimitReader(src, spoolMemLimit+1))
	if err != nil {
		return nil, err
	}
	if n <= spoolMemLimit {
		sp.mem = buf.Bytes()
		sp.size = n
	} else {
		f, err := os.CreateTemp(dir, "cms-blob-spool-*")
		if err != nil {
			return nil, fmt.Errorf("blob: spooling upload: %w", err)
		}
		_ = os.Remove(f.Name())
		sp.file = f
		if _, err := f.Write(buf.Bytes()); err != nil {
			return nil, fmt.Errorf("blob: spooling upload: %w", err)
		}
		m, err := copyBuffered(f, src)
		if err != nil {
			return nil, fmt.Errorf("blob: spooling upload: %w", err)
		}
		sp.size = n + m
	}
	if maxSize >= 0 && sp.size > maxSize {
		return nil, fmt.Errorf("%w: more than %d bytes", ErrTooLarge, maxSize)
	}
	sp.digest = hex.EncodeToString(h.Sum(nil))
	return sp, nil
}

// reader returns a new reader over the spooled content; it can be called
// several times (request retries).
func (s *spool) reader() io.ReadSeeker {
	if s.file != nil {
		return io.NewSectionReader(s.file, 0, s.size)
	}
	return bytes.NewReader(s.mem)
}

// Close releases the temporary file, if any.
func (s *spool) Close() {
	if s.file != nil {
		_ = s.file.Close()
		s.file = nil
	}
	s.mem = nil
}
