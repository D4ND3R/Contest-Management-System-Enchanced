// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package blob

import (
	"bytes"
	"context"
	"crypto/hmac"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
)

// tokenPurpose names the key of the blob server among those derived from
// secret_key (config.DeriveKey). Changing it invalidates every judge's
// token, so it carries a version.
const tokenPurpose = "blob-token/v1"

const (
	// DefaultMaxUpload bounds the blobs a remote client may upload
	// (compiled executables and outputs are far smaller).
	DefaultMaxUpload = 1 << 30
	// maxConcurrentUploads bounds the PUTs the server processes at once:
	// each one may spool up to DefaultMaxUpload on disk (s3) or write a
	// temporary file (local) before committing it. Others wait their turn.
	maxConcurrentUploads = 4
)

// Token returns the bearer token of the blob server for secret_key:
// hex(config.DeriveKey(secret_key, "blob-token/v1")). It is all a judge on
// another machine gets (storage.remote.token, printed by `cms ctl
// blob-token`): it lets its holder read and add blobs, and nothing else.
// secret_key, which signs administrator sessions, never leaves the
// machines of the trusted services.
func Token(secretKey []byte) string {
	return hex.EncodeToString(config.DeriveKey(secretKey, tokenPurpose))
}

// Server serves a Store over HTTP to judges on other machines, which have
// no database access:
//
//	GET  /blobs/{digest}   content (HEAD: just the size)
//	PUT  /blobs/{digest}   store; the body must hash to {digest}
//
// Every request needs "Authorization: Bearer <Token(secret_key)>" (the
// storage.remote.token of the judges). HEAD answers for the backing store,
// so clients use it to decide whether to upload. There is
// no DELETE and no listing: a compromised judge can neither destroy nor
// enumerate test data. The dispatcher mounts it on its judge listener.
type Server struct {
	store   Store
	token   []byte
	maxSize int64
	log     *slog.Logger
	mux     *http.ServeMux
	// uploads holds one slot per PUT being processed.
	uploads chan struct{}
}

// NewServer returns a server for store. maxSize bounds uploads (<= 0 means
// DefaultMaxUpload); log may be nil.
func NewServer(store Store, secretKey []byte, maxSize int64, log *slog.Logger) (*Server, error) {
	if len(secretKey) == 0 {
		return nil, errors.New("blob: the blob server needs secret_key")
	}
	if maxSize <= 0 {
		maxSize = DefaultMaxUpload
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	s := &Server{
		store:   store,
		token:   []byte(Token(secretKey)),
		maxSize: maxSize,
		log:     log,
		uploads: make(chan struct{}, maxConcurrentUploads),
	}
	s.mux = http.NewServeMux()
	s.mux.HandleFunc("GET /blobs/{digest}", s.get) // also HEAD
	s.mux.HandleFunc("PUT /blobs/{digest}", s.put)
	return s, nil
}

// ServeHTTP implements http.Handler. Authentication comes first, so
// unauthenticated clients learn nothing (not even which methods exist).
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	tok, ok := bearer(r.Header.Get("Authorization"))
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer realm="cms-blobs"`)
		http.Error(w, "missing bearer token", http.StatusUnauthorized)
		return
	}
	if !hmac.Equal([]byte(tok), s.token) {
		http.Error(w, "invalid token", http.StatusForbidden)
		return
	}
	s.mux.ServeHTTP(w, r)
}

// bearer extracts the token of an "Authorization: Bearer <token>" header.
func bearer(h string) (string, bool) {
	const scheme = "Bearer "
	if len(h) <= len(scheme) || !strings.EqualFold(h[:len(scheme)], scheme) {
		return "", false
	}
	return strings.TrimSpace(h[len(scheme):]), true
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	d := r.PathValue("digest")
	if !ValidDigest(d) {
		http.Error(w, "invalid digest", http.StatusBadRequest)
		return
	}
	if r.Method == http.MethodHead {
		size, err := s.store.Stat(r.Context(), d)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		setBlobHeaders(w.Header(), d, size)
		return
	}
	rc, err := s.store.Open(r.Context(), d)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer rc.Close()
	setBlobHeaders(w.Header(), d, knownSize(rc))
	// When rc is an *os.File (cache, local store behind a cache) this
	// becomes sendfile.
	if _, err := io.Copy(w, rc); err != nil {
		if !errors.Is(err, context.Canceled) {
			s.log.Error("blob server: sending", "digest", d, "err", err)
		}
		// Send what was read (the client verifies the digest and reports
		// ErrCorrupt), then cut the connection: a truncated body must not
		// pass for a complete one when the length was unknown.
		_ = http.NewResponseController(w).Flush()
		panic(http.ErrAbortHandler)
	}
}

func setBlobHeaders(h http.Header, d string, size int64) {
	h.Set("Content-Type", "application/octet-stream")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("ETag", `"`+d+`"`)
	if size >= 0 {
		h.Set("Content-Length", strconv.FormatInt(size, 10))
	}
}

func (s *Server) put(w http.ResponseWriter, r *http.Request) {
	d := r.PathValue("digest")
	if !ValidDigest(d) {
		http.Error(w, "invalid digest", http.StatusBadRequest)
		return
	}
	if r.ContentLength > s.maxSize {
		http.Error(w, "blob too large", http.StatusRequestEntityTooLarge)
		return
	}
	// Known content: no need to read the body. This must be the answer of
	// the backing store, not of a cache in front of it: Cache.Stat asks
	// its backend.
	if _, err := s.store.Stat(r.Context(), d); err == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// The body is not read until a slot is free: the client just waits.
	select {
	case s.uploads <- struct{}{}:
		defer func() { <-s.uploads }()
	case <-r.Context().Done():
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	body := http.MaxBytesReader(w, r.Body, s.maxSize)
	if _, err := putExpect(r.Context(), s.store, d, body); err != nil {
		var tooLarge *http.MaxBytesError
		switch {
		case errors.As(err, &tooLarge) || errors.Is(err, ErrTooLarge):
			http.Error(w, "blob too large", http.StatusRequestEntityTooLarge)
		case errors.Is(err, ErrDigestMismatch):
			http.Error(w, "content does not hash to the digest in the URL", http.StatusUnprocessableEntity)
		default:
			s.fail(w, r, err)
		}
		return
	}
	w.WriteHeader(http.StatusCreated)
}

// fail maps store errors to HTTP statuses; details of internal errors stay
// in the log.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		http.Error(w, "not found", http.StatusNotFound)
	case errors.Is(err, ErrInvalidDigest):
		http.Error(w, "invalid digest", http.StatusBadRequest)
	case r.Context().Err() != nil:
		// The client went away; nobody reads the answer.
		w.WriteHeader(http.StatusServiceUnavailable)
	default:
		s.log.Error("blob server", "method", r.Method, "path", r.URL.Path, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// Client is the Store of judges on other machines: it talks to a Server
// (storage.remote.url) with the token storage.remote.token. Downloads are
// verified against their digest; uploads are spooled to learn their digest
// first. Delete and Walk return ErrUnsupported: judges never delete or
// enumerate blobs.
type Client struct {
	base    string // URL prefix ending in "/blobs/"
	auth    string
	hc      *http.Client
	tmpDir  string
	retries int
	backoff time.Duration
}

// ClientOption customises NewClient.
type ClientOption func(*Client)

// WithHTTPClient sets the HTTP client (tests, custom CAs).
func WithHTTPClient(hc *http.Client) ClientOption {
	return func(c *Client) { c.hc = hc }
}

// WithClientTempDir sets where uploads larger than a few MiB are spooled
// (default os.TempDir()).
func WithClientTempDir(dir string) ClientOption {
	return func(c *Client) { c.tmpDir = dir }
}

// WithRetries sets how many times a request failing with a network error
// or a 502/503/504 is retried (default 3), waiting backoff, 2·backoff...
func WithRetries(n int, backoff time.Duration) ClientOption {
	return func(c *Client) { c.retries, c.backoff = n, backoff }
}

// NewClient returns a client of the blob server at baseURL (for example
// "http://10.0.0.1:8891"; blobs live under baseURL/blobs/) that
// authenticates with token, the output of Token for the secret_key of the
// server.
func NewClient(baseURL, token string, opts ...ClientOption) (*Client, error) {
	if token == "" {
		return nil, errors.New("blob: the remote store needs storage.remote.token " +
			"(print it with `cms ctl blob-token` on a machine that has secret_key)")
	}
	// A token has the shape of a digest: 64 lowercase hex characters.
	if !ValidDigest(token) {
		return nil, errors.New("blob: storage.remote.token must be 64 lowercase hexadecimal characters")
	}
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("blob: storage.remote.url %q is not an http(s) URL", baseURL)
	}
	c := &Client{
		base:    strings.TrimRight(baseURL, "/") + "/blobs/",
		auth:    "Bearer " + token,
		retries: 3,
		backoff: 200 * time.Millisecond,
		hc: &http.Client{Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConnsPerHost:   16,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: time.Minute,
		}},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// smallUpload is the size under which Put skips the HEAD request: sending
// the content costs about as much as asking whether it is there.
const smallUpload = 64 << 10

// do sends a request built by mk, retrying network errors and 502/503/504
// with exponential backoff. mk is called again for every attempt (bodies
// are rebuilt).
func (c *Client) do(ctx context.Context, mk func() (*http.Request, error)) (*http.Response, error) {
	wait := c.backoff
	for attempt := 0; ; attempt++ {
		req, err := mk()
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", c.auth)
		resp, err := c.hc.Do(req)
		retry := false
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			retry = true
		case resp.StatusCode == http.StatusBadGateway ||
			resp.StatusCode == http.StatusServiceUnavailable ||
			resp.StatusCode == http.StatusGatewayTimeout:
			retry = true
		}
		if !retry || attempt >= c.retries {
			if err != nil {
				return nil, fmt.Errorf("blob: remote: %w", err)
			}
			return resp, nil
		}
		if resp != nil {
			drain(resp)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
		wait *= 2
	}
}

// drain discards a small remaining body so the connection can be reused.
func drain(resp *http.Response) {
	_, _ = io.CopyN(io.Discard, resp.Body, 4<<10)
	_ = resp.Body.Close()
}

// statusError converts an unexpected response to an error.
func statusError(resp *http.Response, d string) error {
	defer drain(resp)
	switch resp.StatusCode {
	case http.StatusNotFound:
		return notFound(d)
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrUnauthorized
	case http.StatusRequestEntityTooLarge:
		return fmt.Errorf("%w: %s rejected by the blob server", ErrTooLarge, d)
	case http.StatusUnprocessableEntity:
		return fmt.Errorf("%w: %s", ErrDigestMismatch, d)
	case http.StatusBadRequest:
		return fmt.Errorf("%w: %s rejected by the blob server", ErrInvalidDigest, d)
	}
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("blob: remote: %s: %s", resp.Status, bytes.TrimSpace(msg))
}

func (c *Client) request(ctx context.Context, method, d string, body func() io.Reader, size int64) func() (*http.Request, error) {
	return func() (*http.Request, error) {
		var rd io.Reader
		if body != nil {
			rd = body()
		}
		req, err := http.NewRequestWithContext(ctx, method, c.base+d, rd)
		if err != nil {
			return nil, err
		}
		if body != nil {
			req.ContentLength = size
			req.Header.Set("Content-Type", "application/octet-stream")
		}
		return req, nil
	}
}

// Stat implements Store.
func (c *Client) Stat(ctx context.Context, d string) (int64, error) {
	if err := checkDigest(d); err != nil {
		return 0, err
	}
	resp, err := c.do(ctx, c.request(ctx, http.MethodHead, d, nil, 0))
	if err != nil {
		return 0, err
	}
	if resp.StatusCode != http.StatusOK {
		return 0, statusError(resp, d)
	}
	_ = resp.Body.Close()
	if resp.ContentLength < 0 {
		return 0, fmt.Errorf("blob: remote: no Content-Length for %s", d)
	}
	return resp.ContentLength, nil
}

// Open implements Store.
func (c *Client) Open(ctx context.Context, d string) (io.ReadCloser, error) {
	if err := checkDigest(d); err != nil {
		return nil, err
	}
	resp, err := c.do(ctx, c.request(ctx, http.MethodGet, d, nil, 0))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp, d)
	}
	return newVerifier(resp.Body, d, resp.ContentLength), nil
}

// upload sends size bytes (from body) as the content of d, skipping the
// transfer when the server already has it.
func (c *Client) upload(ctx context.Context, d string, body func() io.Reader, size int64) error {
	if size > smallUpload {
		switch _, err := c.Stat(ctx, d); {
		case err == nil:
			return nil
		case !errors.Is(err, ErrNotFound):
			return err
		}
	}
	resp, err := c.do(ctx, c.request(ctx, http.MethodPut, d, body, size))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		return statusError(resp, d)
	}
	drain(resp)
	return nil
}

// Put implements Store.
func (c *Client) Put(ctx context.Context, r io.Reader) (string, int64, error) {
	sp, err := spoolReader(ctx, r, c.tmpDir, -1)
	if err != nil {
		return "", 0, err
	}
	defer sp.Close()
	if err := c.upload(ctx, sp.digest, func() io.Reader { return sp.reader() }, sp.size); err != nil {
		return "", 0, err
	}
	return sp.digest, sp.size, nil
}

// PutBytes implements Store.
func (c *Client) PutBytes(ctx context.Context, b []byte) (string, error) {
	d := Digest(b)
	if err := c.upload(ctx, d, func() io.Reader { return bytes.NewReader(b) }, int64(len(b))); err != nil {
		return "", err
	}
	return d, nil
}

// Delete returns ErrUnsupported: the blob server has no DELETE.
func (c *Client) Delete(context.Context, string) error {
	return fmt.Errorf("blob: remote store: delete: %w", ErrUnsupported)
}

// Walk returns ErrUnsupported: the blob server has no listing.
func (c *Client) Walk(context.Context, func(string, int64) error) error {
	return fmt.Errorf("blob: remote store: walk: %w", ErrUnsupported)
}

// probeDigest is the digest of the empty content, used by Check.
var probeDigest = Digest(nil)

// Check implements Checker: the server must answer and accept the token.
func (c *Client) Check(ctx context.Context) error {
	_, err := c.Stat(ctx, probeDigest)
	if err == nil || errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}
