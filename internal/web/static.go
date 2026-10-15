// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// The ?h=<hash> scheme is the one of StaticFileHasher in
// cms/io/web_service.py of CMS:
// Copyright © 2010-2013 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2015 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2013-2016 Luca Wehrstedt <luca.wehrstedt@gmail.com>

package web

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
)

// shared holds cms/server/static of CMS (jQuery, jQuery Migrate, the
// favicon and the loading animation), served by every site, and live.js
// (the live updates of the three sites, R5.8).
//
//go:embed static/favicon.ico static/loading.gif static/jq static/live.js
var shared embed.FS

// SharedStatic is the static directory common to the CMS sites
// (cms/server/static): favicon.ico, loading.gif, jq/jquery-3.6.0.min.js
// and jq/jquery-migrate-3.3.2.min.js, plus this fork's live.js (live
// updates, see its header and internal/live). A site passes it first to
// NewStatic and its own directory after it.
var SharedStatic fs.FS = mustSub(shared, "static")

func mustSub(f fs.FS, dir string) fs.FS {
	s, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return s
}

// startTime is the Last-Modified of embedded files (they have no
// modification time).
var startTime = time.Now().UTC().Truncate(time.Second)

// staticMaxAge is the cache lifetime of hashed URLs (one year, as CMS).
const staticMaxAge = 365 * 24 * time.Hour

// Static serves the static files of a site, all held in memory, with
// the cache scheme of CMS: page templates link them with {{static
// "css/bootstrap.css"}}, which appends ?h=<the first 24 hex digits of
// their SHA-256>; a request carrying the current hash is cacheable for a
// year (immutable), any other one must revalidate (ETag, Last-Modified).
// Text files are gzipped once at startup and sent compressed to clients
// that accept it. Mount it with
//
//	mux.Handle("GET /static/", http.StripPrefix("/static", st))
type Static struct {
	files map[string]*staticFile
	// NotFound handles missing files; nil sends a plain 404.
	NotFound http.Handler
}

type staticFile struct {
	data  []byte
	gz    []byte
	hash  string // 24 hex digits
	ctype string
}

// NewStatic loads every file of the layers; a file of a later layer
// replaces the one with the same path in an earlier layer (CMS serves
// cms/server/static and then the site's static directory over it).
func NewStatic(layers ...fs.FS) (*Static, error) {
	s := &Static{files: map[string]*staticFile{}}
	for _, l := range layers {
		err := fs.WalkDir(l, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, err := fs.ReadFile(l, p)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			f := &staticFile{data: data, hash: hex.EncodeToString(sum[:])[:24], ctype: staticType(p)}
			if compressible(f.ctype) && len(data) > 256 {
				var buf bytes.Buffer
				zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
				_, _ = zw.Write(data)
				_ = zw.Close()
				if buf.Len() < len(data)*9/10 {
					f.gz = buf.Bytes()
				}
			}
			s.files[p] = f
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("web: static files: %w", err)
		}
	}
	return s, nil
}

// staticTypes are the types of the files the sites ship (Python's
// mimetypes, as Werkzeug's SharedDataMiddleware uses them, with a UTF-8
// charset for text).
var staticTypes = map[string]string{
	".css":   "text/css; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".mjs":   "text/javascript; charset=utf-8",
	".json":  "application/json",
	".map":   "application/json",
	".html":  "text/html; charset=utf-8",
	".htm":   "text/html; charset=utf-8",
	".txt":   "text/plain; charset=utf-8",
	".svg":   "image/svg+xml",
	".png":   "image/png",
	".gif":   "image/gif",
	".jpg":   "image/jpeg",
	".jpeg":  "image/jpeg",
	".ico":   "image/vnd.microsoft.icon",
	".webp":  "image/webp",
	".woff":  "font/woff",
	".woff2": "font/woff2",
	".ttf":   "font/ttf",
	".otf":   "font/otf",
	".eot":   "application/vnd.ms-fontobject",
	".pdf":   "application/pdf",
}

func staticType(p string) string {
	ext := strings.ToLower(path.Ext(p))
	if t, ok := staticTypes[ext]; ok {
		return t
	}
	if t := mime.TypeByExtension(ext); t != "" {
		return t
	}
	return "application/octet-stream"
}

func compressible(ctype string) bool {
	return strings.HasPrefix(ctype, "text/") || strings.HasPrefix(ctype, "application/json") ||
		strings.HasPrefix(ctype, "image/svg") || ctype == "image/vnd.microsoft.icon" ||
		ctype == "application/vnd.ms-fontobject" || ctype == "font/ttf" || ctype == "font/otf"
}

// Has reports whether p (relative to the static root, "css/x.css") is a
// static file.
func (s *Static) Has(p string) bool {
	return s != nil && s.files[strings.TrimPrefix(p, "/")] != nil
}

// Hash returns the version of a file used in its URL (the first 24 hex
// digits of its SHA-256), or "" if there is no such file.
func (s *Static) Hash(p string) string {
	if s == nil {
		return ""
	}
	if f := s.files[strings.TrimPrefix(p, "/")]; f != nil {
		return f.hash
	}
	return ""
}

// URL returns the URL of a static file relative to root, with its hash
// (CMS static_url("static", ...)): "../static/css/bootstrap.css?h=...".
// An unknown file gets no hash, as in CMS.
func (s *Static) URL(root URL, p string) string {
	p = strings.TrimPrefix(p, "/")
	comps := []any{"static"}
	for _, c := range strings.Split(p, "/") {
		comps = append(comps, c)
	}
	u := root.Join(comps...)
	if h := s.Hash(p); h != "" {
		u += "?h=" + h
	}
	return u
}

// ServeHTTP serves the file at r.URL.Path (relative to the static root;
// see the type documentation for the mount).
func (s *Static) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	p := strings.TrimPrefix(r.URL.Path, "/")
	var f *staticFile
	if s != nil && fs.ValidPath(p) {
		f = s.files[p]
	}
	if f == nil {
		if s != nil && s.NotFound != nil {
			s.NotFound.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", f.ctype)
	if r.URL.Query().Get("h") == f.hash {
		h.Set("Cache-Control", "public, max-age="+strconv.Itoa(int(staticMaxAge/time.Second))+", immutable")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	body, etag := f.data, `"`+f.hash+`"`
	if f.gz != nil {
		h.Add("Vary", "Accept-Encoding")
		// Ranges are served from the identity encoding.
		if r.Header.Get("Range") == "" && acceptsGzip(r.Header.Get("Accept-Encoding")) {
			body, etag = f.gz, `"`+f.hash+`-gz"`
			h.Set("Content-Encoding", "gzip")
		}
	}
	h.Set("Etag", etag)
	http.ServeContent(w, r, "", startTime, bytes.NewReader(body))
}

// acceptsGzip reports whether an Accept-Encoding header allows gzip
// (explicitly or through "*", with a non-zero quality).
func acceptsGzip(h string) bool {
	star := false
	for _, part := range strings.Split(h, ",") {
		coding, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		coding = strings.ToLower(strings.TrimSpace(coding))
		q := 1.0
		for _, p := range strings.Split(params, ";") {
			k, v, ok := strings.Cut(strings.TrimSpace(p), "=")
			if ok && strings.EqualFold(strings.TrimSpace(k), "q") {
				if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
					q = f
				}
			}
		}
		switch coding {
		case "gzip", "x-gzip":
			return q > 0
		case "*":
			star = q > 0
		}
	}
	return star
}
