// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package web

import (
	"bytes"
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
)

// seekStore serves blobs as io.ReadSeekers (as the cache does with its
// files); blob.Local serves forward-only verified streams.
type seekStore struct{ blob.Store }

func (s seekStore) Open(ctx context.Context, d string) (io.ReadCloser, error) {
	rc, err := s.Store.Open(ctx, d)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}
	return struct {
		*bytes.Reader
		io.Closer
	}{bytes.NewReader(b), io.NopCloser(nil)}, nil
}

func TestTombstoneIsJobs(t *testing.T) {
	if tombstone != job.TombstoneDigest {
		t.Fatal("tombstone digest differs from job.TombstoneDigest")
	}
}

// TestServeBlob follows cmstestsuite/unit_tests/server/file_middleware_test.py
// of CMS, for a seekable and a forward-only store.
func TestServeBlob(t *testing.T) {
	local, err := blob.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	content := make([]byte, 1024)
	for i := range content {
		content[i] = byte(i * 7)
	}
	digest, err := local.PutBytes(context.Background(), content)
	if err != nil {
		t.Fatal(err)
	}
	for name, store := range map[string]blob.Store{"forward-only": local, "seekable": seekStore{local}} {
		t.Run(name, func(t *testing.T) {
			serve := func(d Download, hdr ...string) *httptest.ResponseRecorder {
				r := httptest.NewRequest("GET", "/demo/tasks/a/attachments/x", nil)
				for i := 0; i+1 < len(hdr); i += 2 {
					r.Header.Set(hdr[i], hdr[i+1])
				}
				rec := httptest.NewRecorder()
				if err := ServeBlob(rec, r, store, d); err != nil {
					t.Errorf("ServeBlob: %v", err)
				}
				return rec
			}
			d := Download{Digest: digest, Filename: "statement.pdf"}
			rec := serve(d)
			h := rec.Result().Header
			if rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), content) {
				t.Fatalf("success: %d %d bytes", rec.Code, rec.Body.Len())
			}
			if h.Get("Content-Type") != "application/pdf" || h.Get("Content-Disposition") != "attachment; filename=statement.pdf" ||
				h.Get("Etag") != `"`+digest+`"` || h.Get("Cache-Control") != "no-cache, private" ||
				h.Get("Accept-Ranges") != "bytes" || h.Get("Content-Length") != "1024" {
				t.Errorf("headers: %v", h)
			}
			if rec := serve(Download{Digest: digest}); rec.Result().Header.Get("Content-Disposition") != "" ||
				rec.Result().Header.Get("Content-Type") != "application/octet-stream" {
				t.Errorf("no filename: %v", rec.Result().Header)
			}
			if rec := serve(Download{Digest: digest, Filename: "a", ContentType: "text/plain", Disposition: "inline"}); rec.Result().Header.Get("Content-Type") != "text/plain" ||
				rec.Result().Header.Get("Content-Disposition") != "inline; filename=a" {
				t.Errorf("explicit type: %v", rec.Result().Header)
			}
			if rec := serve(d, "If-None-Match", `"`+digest+`"`); rec.Code != 304 || rec.Body.Len() != 0 {
				t.Errorf("conditional: %d", rec.Code)
			}
			if rec := serve(d, "If-None-Match", `"not the etag"`); rec.Code != 200 || rec.Body.Len() != 1024 {
				t.Errorf("conditional no match: %d", rec.Code)
			}
			rec = serve(d, "Range", "bytes=256-767")
			if rec.Code != 206 || !bytes.Equal(rec.Body.Bytes(), content[256:768]) ||
				rec.Result().Header.Get("Content-Range") != "bytes 256-767/1024" {
				t.Errorf("range: %d %q", rec.Code, rec.Result().Header.Get("Content-Range"))
			}
			rec = serve(d, "Range", "bytes=256-2047")
			if rec.Code != 206 || !bytes.Equal(rec.Body.Bytes(), content[256:]) {
				t.Errorf("range end overflows: %d", rec.Code)
			}
			if rec := serve(d, "Range", "bytes=1025-"); rec.Code != 416 {
				t.Errorf("range start overflows: %d", rec.Code)
			}
			rec = serve(d, "Range", "bytes=0-9,100-109")
			if name == "seekable" {
				ct, params, _ := mime.ParseMediaType(rec.Result().Header.Get("Content-Type"))
				if rec.Code != 206 || ct != "multipart/byteranges" {
					t.Fatalf("multiple ranges: %d %s", rec.Code, ct)
				}
				mr := multipart.NewReader(rec.Body, params["boundary"])
				for _, want := range [][]byte{content[0:10], content[100:110]} {
					p, err := mr.NextPart()
					if err != nil {
						t.Fatal(err)
					}
					if got, _ := io.ReadAll(p); !bytes.Equal(got, want) {
						t.Error("multipart range content")
					}
				}
			} else if rec.Code != 200 || rec.Body.Len() != 1024 {
				// A forward-only stream answers multiple ranges with the whole file.
				t.Errorf("multiple ranges, forward-only: %d", rec.Code)
			}
			hr := httptest.NewRequest("HEAD", "/x", nil)
			hrec := httptest.NewRecorder()
			_ = ServeBlob(hrec, hr, store, d)
			if hrec.Code != 200 || hrec.Body.Len() != 0 || hrec.Result().Header.Get("Content-Length") != "1024" {
				t.Errorf("HEAD: %d %d", hrec.Code, hrec.Body.Len())
			}
			if rec := serve(Download{Digest: strings.Repeat("ab", 32)}); rec.Code != 404 {
				t.Errorf("not found: %d", rec.Code)
			}
			if rec := serve(Download{Digest: "../../etc/passwd"}); rec.Code != 404 {
				t.Errorf("invalid digest: %d", rec.Code)
			}
			if rec := serve(Download{Digest: "x"}); rec.Code != 503 {
				t.Errorf("tombstone: %d", rec.Code)
			}
		})
	}
}

// The expectations without filename* are Werkzeug 3.1's output for CMS.
func TestContentDisposition(t *testing.T) {
	for _, c := range []struct{ disp, name, want string }{
		{"", "a.txt", "attachment; filename=a.txt"},
		{"attachment", "my file.c", `attachment; filename="my file.c"`},
		{"attachment", `a"b.c`, `attachment; filename="a\"b.c"`},
		{"attachment", "x;y.cpp", `attachment; filename="x;y.cpp"`},
		{"attachment", `a\b`, `attachment; filename="a\\b"`},
		{"inline", "", `inline; filename=""`},
		{"attachment", "a\r\nSet-Cookie: x=1.c", `attachment; filename="aSet-Cookie: x=1.c"`},
		{"attachment", "ñandú.cpp", `attachment; filename=_and_.cpp; filename*=UTF-8''%C3%B1and%C3%BA.cpp`},
		{"attachment", "统计 1.pdf", `attachment; filename="__ 1.pdf"; filename*=UTF-8''%E7%BB%9F%E8%AE%A1%201.pdf`},
	} {
		if got := ContentDisposition(c.disp, c.name); got != c.want {
			t.Errorf("ContentDisposition(%q, %q) = %q, want %q", c.disp, c.name, got, c.want)
		}
	}
}

func TestForwardSeeker(t *testing.T) {
	f := &forwardSeeker{r: strings.NewReader("0123456789"), size: 10}
	if n, _ := f.Seek(0, io.SeekEnd); n != 10 {
		t.Error("size")
	}
	_, _ = f.Seek(3, io.SeekStart)
	b := make([]byte, 2)
	if _, err := io.ReadFull(f, b); err != nil || string(b) != "34" {
		t.Errorf("read after seek: %q %v", b, err)
	}
	_, _ = f.Seek(1, io.SeekStart)
	if _, err := f.Read(b); err == nil {
		t.Error("backwards seek must fail")
	}
	if _, err := f.Seek(-1, io.SeekStart); err == nil {
		t.Error("negative position")
	}
}
