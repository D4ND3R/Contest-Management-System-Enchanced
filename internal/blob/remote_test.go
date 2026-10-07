// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package blob

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
)

var testSecret = []byte("0123456789abcdef0123456789abcdef")

type remoteFixture struct {
	backend *Local
	server  *Server
	url     string
	client  *Client
	// requests counts requests by method.
	mu       sync.Mutex
	requests map[string]int
	// fail503, if positive, makes that many next requests fail with 503.
	fail503 atomic.Int32
}

func (f *remoteFixture) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[method]
}

func newRemote(t testing.TB, backend Store, maxSize int64) *remoteFixture {
	t.Helper()
	f := &remoteFixture{requests: map[string]int{}}
	if l, ok := backend.(*Local); ok {
		f.backend = l
	}
	srv, err := NewServer(backend, testSecret, maxSize, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.server = srv
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests[r.Method]++
		f.mu.Unlock()
		if f.fail503.Add(-1) >= 0 {
			http.Error(w, "restarting", http.StatusServiceUnavailable)
			return
		}
		srv.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	f.url = ts.URL
	c, err := NewClient(ts.URL+"/", Token(testSecret), WithClientTempDir(t.TempDir()), WithRetries(3, time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	f.client = c
	return f
}

func TestRemote(t *testing.T) {
	f := newRemote(t, newTestLocal(t), 0)
	testStore(t, f.client, storeOptions{noDelete: true, noWalk: true})
	if err := f.client.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// TestRemoteOverCache serves a cache (hits are *os.File, sent with
// sendfile) to a remote client.
func TestRemoteOverCache(t *testing.T) {
	cache, err := NewCache(newTestLocal(t), t.TempDir(), 64<<20, nil)
	if err != nil {
		t.Fatal(err)
	}
	f := newRemote(t, cache, 0)
	testStore(t, f.client, storeOptions{noDelete: true, noWalk: true})
}

func TestToken(t *testing.T) {
	// Pinned: changing it invalidates the storage.remote.token of every
	// judge. hex(config.DeriveKey(testSecret, "blob-token/v1")), computed
	// independently.
	const want = "29b23ff282ad16b4d98ad716ac2fa8b801358f3fae162dd1c7954550ff1bde99"
	if got := Token(testSecret); got != hex.EncodeToString(config.DeriveKey(testSecret, "blob-token/v1")) {
		t.Fatalf("Token = %s, not derived with config.DeriveKey", got)
	} else if got != want {
		t.Errorf("Token = %s, want %s", got, want)
	}
	if !ValidDigest(Token(testSecret)) {
		t.Error("token is not 64 lowercase hex characters")
	}
	if Token([]byte("other key 0123456789")) == Token(testSecret) {
		t.Fatal("token does not depend on the key")
	}
	// Not the raw HMAC of a label: another derivation of secret_key over
	// user-chosen text (a contest name...) cannot produce it.
	for _, label := range []string{"blob-token/v1", "blob-token", "cms-blob-v1"} {
		m := hmac.New(sha256.New, testSecret)
		m.Write([]byte(label))
		if hex.EncodeToString(m.Sum(nil)) == Token(testSecret) {
			t.Errorf("token is HMAC(secret_key, %q)", label)
		}
	}
}

func rawRequest(t *testing.T, method, url, auth string, body io.Reader) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestRemoteAuth(t *testing.T) {
	f := newRemote(t, newTestLocal(t), 0)
	ctx := context.Background()
	d, err := f.backend.PutBytes(ctx, []byte("secret test data"))
	if err != nil {
		t.Fatal(err)
	}
	u := f.url + "/blobs/" + d
	good := "Bearer " + Token(testSecret)
	for _, tc := range []struct {
		method, auth string
		want         int
	}{
		{"GET", "", http.StatusUnauthorized},
		{"HEAD", "", http.StatusUnauthorized},
		{"PUT", "", http.StatusUnauthorized},
		{"DELETE", "", http.StatusUnauthorized},
		{"GET", "Basic dXNlcjpwYXNz", http.StatusUnauthorized},
		{"GET", "Bearer", http.StatusUnauthorized},
		{"GET", "Bearer " + Token([]byte("wrong key 0123456789")), http.StatusForbidden},
		{"GET", "Bearer " + strings.ToUpper(Token(testSecret)), http.StatusForbidden},
		{"GET", good, http.StatusOK},
		{"GET", "bearer " + Token(testSecret), http.StatusOK},
		{"HEAD", good, http.StatusOK},
		{"DELETE", good, http.StatusMethodNotAllowed},
		{"POST", good, http.StatusMethodNotAllowed},
	} {
		resp := rawRequest(t, tc.method, u, tc.auth, nil)
		if resp.StatusCode != tc.want {
			t.Errorf("%s with %q: %d, want %d", tc.method, tc.auth, resp.StatusCode, tc.want)
		}
		if tc.want == http.StatusUnauthorized && !strings.HasPrefix(resp.Header.Get("WWW-Authenticate"), "Bearer") {
			t.Errorf("401 without WWW-Authenticate: %v", resp.Header)
		}
	}
	// The blob survived the DELETE attempt.
	if _, err := f.backend.Stat(ctx, d); err != nil {
		t.Fatal(err)
	}
	// A client with the token of another secret_key, or with secret_key
	// itself instead of the token, is refused clearly.
	for _, tok := range []string{Token([]byte("another key 0123456789")), hex.EncodeToString(testSecret)} {
		bad, err := NewClient(f.url, tok)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := bad.Open(ctx, d); !errors.Is(err, ErrUnauthorized) {
			t.Errorf("Open with token %s…: %v", tok[:8], err)
		}
	}
	bad, err := NewClient(f.url, Token([]byte("another key 0123456789")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bad.PutBytes(ctx, []byte("x")); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("PutBytes: %v", err)
	}
	if err := bad.Check(ctx); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("Check: %v", err)
	}
}

func TestRemotePutVerifiesDigest(t *testing.T) {
	f := newRemote(t, newTestLocal(t), 0)
	auth := "Bearer " + Token(testSecret)
	data := []byte("the real content")
	for _, body := range []io.Reader{
		bytes.NewReader([]byte("something else")),
		io.MultiReader(bytes.NewReader([]byte("something else"))), // unknown length: chunked
	} {
		resp := rawRequest(t, "PUT", f.url+"/blobs/"+Digest(data), auth, body)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("mismatching PUT: %d", resp.StatusCode)
		}
	}
	if got := walkAll(t, f.backend); len(got) != 0 {
		t.Fatalf("mismatching content stored: %v", got)
	}
	resp := rawRequest(t, "PUT", f.url+"/blobs/"+Digest(data), auth, bytes.NewReader(data))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("PUT: %d", resp.StatusCode)
	}
	// Again: already there, nothing to do.
	resp = rawRequest(t, "PUT", f.url+"/blobs/"+Digest(data), auth, bytes.NewReader(data))
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("second PUT: %d", resp.StatusCode)
	}
}

func TestRemoteInvalidDigests(t *testing.T) {
	f := newRemote(t, newTestLocal(t), 0)
	auth := "Bearer " + Token(testSecret)
	for _, path := range []string{
		"/blobs/" + strings.Repeat("A", 64),
		"/blobs/" + strings.Repeat("a", 63),
		"/blobs/..%2f..%2f..%2fetc%2fpasswd",
		"/blobs/%2e%2e",
		"/blobs/" + strings.Repeat("a", 32) + "%2f" + strings.Repeat("a", 29),
	} {
		for _, m := range []string{"GET", "PUT"} {
			resp := rawRequest(t, m, f.url+path, auth, strings.NewReader("x"))
			if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
				t.Errorf("%s %s: %d", m, path, resp.StatusCode)
			}
		}
	}
	if got := walkAll(t, f.backend); len(got) != 0 {
		t.Fatalf("something was stored: %v", got)
	}
}

func TestRemoteMaxSize(t *testing.T) {
	f := newRemote(t, newTestLocal(t), 1000)
	ctx := context.Background()
	if _, err := f.client.PutBytes(ctx, make([]byte, 1000)); err != nil {
		t.Fatalf("upload at the limit: %v", err)
	}
	big := randomBytes(t, 1001)
	if _, err := f.client.PutBytes(ctx, big); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("PutBytes over the limit: %v", err)
	}
	// Without Content-Length the limit is enforced while reading.
	resp := rawRequest(t, "PUT", f.url+"/blobs/"+Digest(big), "Bearer "+Token(testSecret), io.MultiReader(bytes.NewReader(big)))
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("chunked PUT over the limit: %d", resp.StatusCode)
	}
	if got := walkAll(t, f.backend); len(got) != 1 {
		t.Fatalf("stored: %v", got)
	}
}

func TestRemoteCorruption(t *testing.T) {
	f := newRemote(t, newTestLocal(t), 0)
	data := randomBytes(t, 200<<10)
	d, err := f.backend.PutBytes(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 1
	if err := os.WriteFile(f.backend.path(d), data, 0o600); err != nil {
		t.Fatal(err)
	}
	expectCorrupt(t, f.client, d)
}

func TestRemoteSkipsKnownUploads(t *testing.T) {
	f := newRemote(t, newTestLocal(t), 0)
	ctx := context.Background()
	big := randomBytes(t, smallUpload+1)
	if _, _, err := f.client.Put(ctx, bytes.NewReader(big)); err != nil {
		t.Fatal(err)
	}
	puts := f.count("PUT")
	if _, _, err := f.client.Put(ctx, bytes.NewReader(big)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.client.PutBytes(ctx, big); err != nil {
		t.Fatal(err)
	}
	if f.count("PUT") != puts {
		t.Errorf("known content uploaded again: %d PUTs, want %d", f.count("PUT"), puts)
	}
}

func TestRemoteRetries(t *testing.T) {
	f := newRemote(t, newTestLocal(t), 0)
	ctx := context.Background()
	f.fail503.Store(2)
	d, err := f.client.PutBytes(ctx, []byte("after a restart"))
	if err != nil {
		t.Fatalf("PutBytes with 2 failures: %v", err)
	}
	f.fail503.Store(2)
	if got := readAll(t, f.client, d); string(got) != "after a restart" {
		t.Fatalf("got %q", got)
	}
	f.fail503.Store(10)
	if _, err := f.client.Stat(ctx, d); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("Stat with too many failures: %v", err)
	}
	f.fail503.Store(0)
	// A cancelled context stops the retries at once.
	f.fail503.Store(100)
	cctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	slow, err := NewClient(f.url, Token(testSecret), WithRetries(100, time.Second))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := slow.Stat(cctx, d); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stat: %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("retries ignored the context")
	}
}

func TestRemoteConstructors(t *testing.T) {
	if _, err := NewServer(newTestLocal(t), nil, 0, nil); err == nil {
		t.Error("server without secret_key")
	}
	if _, err := NewClient("http://x", ""); err == nil || !strings.Contains(err.Error(), "storage.remote.token") {
		t.Errorf("client without token: %v", err)
	}
	// Malformed tokens (a 16-byte secret_key pasted by mistake...). A
	// 32-byte secret_key has the shape of a token: the server rejects it
	// (TestRemoteAuth).
	for _, tok := range []string{hex.EncodeToString(testSecret[:16]), strings.ToUpper(Token(testSecret)), Token(testSecret)[:63]} {
		if _, err := NewClient("http://x", tok); err == nil {
			t.Errorf("client with token %q", tok)
		}
	}
	for _, u := range []string{"", "10.0.0.1:8891", "ftp://x", "http://", "::"} {
		if _, err := NewClient(u, Token(testSecret)); err == nil {
			t.Errorf("client with URL %q", u)
		}
	}
}

// TestRemoteReuploadAfterBackendDelete: the dispatcher serves judges
// through a cache, and the garbage collector deletes a blob from the
// backend while the cache still holds it. A judge uploading the same
// content again must store it in the backend, not be told it is there.
func TestRemoteReuploadAfterBackendDelete(t *testing.T) {
	ctx := context.Background()
	backend := newTestLocal(t)
	cache := newTestCache(t, backend, t.TempDir(), 64<<20)
	f := newRemote(t, cache, 0)
	small := []byte("deterministic executable")
	big := randomBytes(t, smallUpload+1) // the client asks with a HEAD first
	for _, data := range [][]byte{small, big} {
		d, err := f.client.PutBytes(ctx, data)
		if err != nil {
			t.Fatal(err)
		}
		readAll(t, cache, d) // now cached on the dispatcher
		if err := backend.Delete(ctx, d); err != nil {
			t.Fatal(err)
		}
		if _, err := f.client.Stat(ctx, d); !errors.Is(err, ErrNotFound) {
			t.Errorf("HEAD of a blob deleted from the backend: %v", err)
		}
		if _, err := f.client.PutBytes(ctx, data); err != nil {
			t.Fatal(err)
		}
		if _, err := backend.Stat(ctx, d); err != nil {
			t.Errorf("%d bytes: re-upload not stored in the backend: %v", len(data), err)
		}
		// Also with Put (spooled upload).
		if err := backend.Delete(ctx, d); err != nil {
			t.Fatal(err)
		}
		if _, _, err := f.client.Put(ctx, bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
		if _, err := backend.Stat(ctx, d); err != nil {
			t.Errorf("%d bytes: Put not stored in the backend: %v", len(data), err)
		}
	}
}

// slowPutStore records how many Puts run at once and holds each one until
// release is closed.
type slowPutStore struct {
	Store
	mu            sync.Mutex
	running, peak int
	entered       chan struct{}
	release       chan struct{}
}

func (s *slowPutStore) Put(ctx context.Context, r io.Reader) (string, int64, error) {
	s.mu.Lock()
	s.running++
	s.peak = max(s.peak, s.running)
	s.mu.Unlock()
	s.entered <- struct{}{}
	<-s.release
	defer func() {
		s.mu.Lock()
		s.running--
		s.mu.Unlock()
	}()
	return s.Store.Put(ctx, r)
}

// TestRemoteUploadSlots: the server processes a bounded number of uploads
// at once; the others wait instead of failing.
func TestRemoteUploadSlots(t *testing.T) {
	ctx := context.Background()
	store := &slowPutStore{Store: newTestLocal(t), entered: make(chan struct{}, 64), release: make(chan struct{})}
	f := newRemote(t, store, 0)
	// Registered after the server's Close, so it runs first: a failing
	// test does not leave handlers blocked.
	var once sync.Once
	release := func() { once.Do(func() { close(store.release) }) }
	t.Cleanup(release)
	const n = maxConcurrentUploads + 3
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			_, err := f.client.PutBytes(ctx, []byte{byte(i)})
			errs <- err
		}()
	}
	for i := 0; i < maxConcurrentUploads; i++ {
		<-store.entered
	}
	select {
	case <-store.entered:
		t.Fatal("more uploads than slots processed at once")
	case <-time.After(100 * time.Millisecond):
	}
	release()
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.peak != maxConcurrentUploads {
		t.Errorf("peak of %d concurrent uploads, want %d", store.peak, maxConcurrentUploads)
	}
}
