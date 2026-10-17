// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package adminweb

// Small helpers of the task, dataset and preparation pages for files of
// the blob store.

import (
	"errors"
	"io"
)

// errTooLarge: a file is larger than it said or than allowed.
var errTooLarge = errors.New("file too large")

// limitedReader reads at most n bytes and fails (instead of truncating)
// when r has more: the declared size of a zip member must be its size.
type limitedReader struct {
	r io.Reader
	n int64
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if l.n <= 0 {
		var one [1]byte
		if n, _ := l.r.Read(one[:]); n > 0 {
			return 0, errTooLarge
		}
		return 0, io.EOF
	}
	if int64(len(p)) > l.n {
		p = p[:l.n]
	}
	n, err := l.r.Read(p)
	l.n -= int64(n)
	return n, err
}

// copyBlob copies a blob of the store to w.
func (s *Server) copyBlob(c *Ctx, w io.Writer, digest string) error {
	rc, err := s.blobs.Open(c.Context(), digest)
	if err != nil {
		return err
	}
	defer rc.Close()
	_, err = io.Copy(w, rc)
	return err
}

// readBlob reads a blob whole if it is at most limit bytes; truncated is
// set (and data holds the first limit bytes) when it is larger.
func (s *Server) readBlob(c *Ctx, digest string, limit int64) (data []byte, truncated bool, err error) {
	rc, err := s.blobs.Open(c.Context(), digest)
	if err != nil {
		return nil, false, err
	}
	defer rc.Close()
	data, err = io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(data)) > limit {
		return data[:limit], true, nil
	}
	return data, false, nil
}
