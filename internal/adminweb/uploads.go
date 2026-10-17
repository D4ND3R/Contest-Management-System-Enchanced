// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package adminweb

// Uploads. Routes with Upload: true accept bodies up to MaxBody; the
// multipart form is parsed with formMemory bytes of memory, so larger
// files are written to temporary files by net/http (and removed after
// the request), never held whole in memory. Handlers then stream them
// into the blob store with PutUpload, or open them (Open) to read a zip
// in place.

import (
	"io"
	"mime/multipart"
)

// Files returns the files sent in the field name (CMS
// self.request.files[name]); nil if none.
func (c *Ctx) Files(name string) []*multipart.FileHeader {
	if c.R.MultipartForm == nil {
		return nil
	}
	return c.R.MultipartForm.File[name]
}

// File returns the first file of the field name, nil if none.
func (c *Ctx) File(name string) *multipart.FileHeader {
	if fs := c.Files(name); len(fs) > 0 {
		return fs[0]
	}
	return nil
}

// PutUpload streams an uploaded file into the blob store and returns its
// digest and size.
func (c *Ctx) PutUpload(fh *multipart.FileHeader) (digest string, size int64, err error) {
	f, err := fh.Open()
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	return c.S.blobs.Put(c.Context(), f)
}

// OpenUpload opens an uploaded file for reading (and io.ReaderAt, for
// zip.NewReader with fh.Size).
func OpenUpload(fh *multipart.FileHeader) (multipart.File, error) { return fh.Open() }

// ReadUpload reads an uploaded file whole if it is at most limit bytes
// (small files: a statement in Markdown, a checker source); ok is false
// when it is larger.
func ReadUpload(fh *multipart.FileHeader, limit int64) (data []byte, ok bool, err error) {
	if fh.Size > limit {
		return nil, false, nil
	}
	f, err := fh.Open()
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	data, err = io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(data)) > limit {
		return nil, false, nil
	}
	return data, true, nil
}
