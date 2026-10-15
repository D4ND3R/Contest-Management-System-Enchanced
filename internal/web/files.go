// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/server/file_middleware.py (FileServerMiddleware) and
// cms/server/util.py (FileHandlerMixin) of CMS:
// Copyright © 2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2021 Manuel Gundlach <manuel.gundlach@gmail.com>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>

package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
)

// tombstone is the digest of a file whose content is gone (CMS
// TombstoneError, job.TombstoneDigest, D19).
const tombstone = "x"

// Download describes a file sent from the blob store (CMS fetch()).
type Download struct {
	// Digest is the SHA-256 of the content.
	Digest string
	// Filename is the name the browser saves the file as; "" sends no
	// Content-Disposition.
	Filename string
	// ContentType defaults to the type of Filename (MimeType) or
	// application/octet-stream, as the handlers of CMS do.
	ContentType string
	// Disposition is "attachment" (the default) or "inline".
	Disposition string
}

// ServeBlob streams a file from the blob store as CMS's
// FileServerMiddleware: 404 if the blob does not exist, 503 for the
// tombstone digest, the digest as strong ETag, Cache-Control: no-cache,
// private, and conditional (If-None-Match, 304) and partial (Range,
// 206/416) requests, HEAD included. The file is streamed, never held in
// memory. The error is the one of the store, for logging; the response
// has already been sent.
func ServeBlob(w http.ResponseWriter, r *http.Request, store blob.Store, d Download) error {
	if d.Digest == tombstone {
		http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		return nil
	}
	if !blob.ValidDigest(d.Digest) {
		http.NotFound(w, r)
		return nil
	}
	rc, err := store.Open(r.Context(), d.Digest)
	if err != nil {
		return blobError(w, r, err)
	}
	defer rc.Close()
	var content io.ReadSeeker
	var fwd *forwardSeeker
	if rs, ok := rc.(io.ReadSeeker); ok {
		content = rs
	} else {
		size := int64(-1)
		if sz, ok := rc.(interface{ Size() int64 }); ok {
			size = sz.Size()
		}
		if size < 0 {
			if size, err = store.Stat(r.Context(), d.Digest); err != nil {
				return blobError(w, r, err)
			}
		}
		if strings.Contains(r.Header.Get("Range"), ",") {
			// A forward-only stream cannot serve ranges in any order:
			// answer with the whole file (allowed by RFC 9110).
			r.Header.Del("Range")
		}
		fwd = &forwardSeeker{r: rc, size: size}
		content = fwd
	}

	ctype := d.ContentType
	if ctype == "" {
		if ctype = MimeType(d.Filename); ctype == "" {
			ctype = "application/octet-stream"
		}
	}
	h := w.Header()
	h.Set("Content-Type", ctype)
	if d.Filename != "" {
		h.Set("Content-Disposition", ContentDisposition(d.Disposition, d.Filename))
	}
	h.Set("Etag", `"`+d.Digest+`"`)
	h.Set("Cache-Control", "no-cache, private")
	http.ServeContent(w, r, "", time.Time{}, content)
	if fwd != nil && fwd.err != nil {
		return fmt.Errorf("web: blob %s: %w", d.Digest, fwd.err)
	}
	return nil
}

func blobError(w http.ResponseWriter, r *http.Request, err error) error {
	if errors.Is(err, blob.ErrNotFound) || errors.Is(err, blob.ErrInvalidDigest) {
		http.NotFound(w, r)
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return nil
	}
	http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
	return fmt.Errorf("web: blob: %w", err)
}

// forwardSeeker adapts a stream of known size to the seeks
// http.ServeContent makes: the size query (SeekEnd) and forward seeks
// to the start of a range, which skip bytes.
type forwardSeeker struct {
	r    io.Reader
	size int64
	pos  int64 // logical position
	read int64 // bytes consumed from r
	err  error
}

func (f *forwardSeeker) Seek(offset int64, whence int) (int64, error) {
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = f.pos + offset
	case io.SeekEnd:
		abs = f.size + offset
	default:
		return 0, errors.New("web: bad whence")
	}
	if abs < 0 {
		return 0, errors.New("web: negative position")
	}
	f.pos = abs
	return abs, nil
}

func (f *forwardSeeker) Read(p []byte) (int, error) {
	if f.pos < f.read {
		f.err = errors.New("web: cannot seek backwards in a blob stream")
		return 0, f.err
	}
	if f.pos > f.read {
		n, err := io.CopyN(io.Discard, f.r, f.pos-f.read)
		f.read += n
		if err != nil {
			f.err = err
			return 0, err
		}
	}
	n, err := f.r.Read(p)
	f.read += int64(n)
	f.pos = f.read
	if err != nil && err != io.EOF {
		f.err = err
	}
	return n, err
}

// ContentDisposition formats the header as Werkzeug does for CMS
// (dump_options_header): the filename bare if it only has token
// characters, else quoted with \ and " escaped. Names with non-ASCII
// characters also get an RFC 8187 filename* parameter, which browsers
// prefer, and an ASCII fallback in filename. Control characters are
// dropped.
func ContentDisposition(disposition, filename string) string {
	if disposition == "" {
		disposition = "attachment"
	}
	var clean strings.Builder
	ascii := true
	for _, c := range filename {
		if c < 0x20 || c == 0x7f || c == utf8.RuneError {
			continue
		}
		if c >= 0x80 {
			ascii = false
		}
		clean.WriteRune(c)
	}
	name := clean.String()
	if ascii {
		return disposition + "; filename=" + quoteHeaderValue(name)
	}
	var fallback strings.Builder
	for _, c := range name {
		if c < 0x80 {
			fallback.WriteRune(c)
		} else {
			fallback.WriteByte('_')
		}
	}
	return disposition + "; filename=" + quoteHeaderValue(fallback.String()) +
		"; filename*=UTF-8''" + rfc8187(name)
}

// isTokenChar is Werkzeug's _token_chars.
func isTokenChar(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' ||
		strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0
}

// quoteHeaderValue is Werkzeug's quote_header_value.
func quoteHeaderValue(s string) string {
	if s == "" {
		return `""`
	}
	token := true
	for i := 0; i < len(s); i++ {
		if !isTokenChar(s[i]) {
			token = false
			break
		}
	}
	if token {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// rfc8187 percent-encodes s for a ext-value (attr-char kept).
func rfc8187(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' ||
			strings.IndexByte("!#$&+-.^_`|~", c) >= 0 {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&15])
	}
	return b.String()
}
