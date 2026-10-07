// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package blob

import (
	"bytes"
	"context"
	"errors"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// countingStore counts the Opens reaching the backend and can hold them
// until released.
type countingStore struct {
	Store
	opens atomic.Int64
	gate  chan struct{} // if non-nil, Open waits for it to be closed
}

func (s *countingStore) Open(ctx context.Context, d string) (io.ReadCloser, error) {
	s.opens.Add(1)
	if s.gate != nil {
		select {
		case <-s.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return s.Store.Open(ctx, d)
}

func newTestCache(t testing.TB, backend Store, dir string, capacity int64) *Cache {
	t.Helper()
	c, err := NewCache(backend, dir, capacity, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestCacheConformance(t *testing.T) {
	testStore(t, newTestCache(t, newTestLocal(t), t.TempDir(), 64<<20), storeOptions{})
}

// TestCacheSmallCapacity runs the suite with a cache that evicts all the
// time: results must stay correct.
func TestCacheSmallCapacity(t *testing.T) {
	testStore(t, newTestCache(t, newTestLocal(t), t.TempDir(), 4<<10), storeOptions{})
}

func TestCacheHitMiss(t *testing.T) {
	ctx := context.Background()
	backend := &countingStore{Store: newTestLocal(t)}
	c := newTestCache(t, backend, t.TempDir(), 1<<20)
	data := []byte("statement.pdf")
	d, err := c.PutBytes(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if got := readAll(t, c, d); !bytes.Equal(got, data) {
			t.Fatalf("got %q", got)
		}
	}
	st := c.Stats()
	if backend.opens.Load() != 1 || st.Misses != 1 || st.Hits != 2 {
		t.Errorf("backend opens %d, stats %+v; want 1 open, 1 miss, 2 hits", backend.opens.Load(), st)
	}
	if st.Bytes != int64(len(data)) || st.Entries != 1 {
		t.Errorf("stats %+v", st)
	}
	// Hits are plain files (seekable, sendfile/copy_file_range friendly).
	rc, err := c.Open(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	if _, ok := rc.(*os.File); !ok {
		t.Errorf("hit returned %T", rc)
	}
	if size, err := c.Stat(ctx, d); err != nil || size != int64(len(data)) {
		t.Errorf("Stat = %d, %v", size, err)
	}
	if _, err := c.Open(ctx, Digest([]byte("missing"))); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing blob: %v", err)
	}
}

func TestCacheEviction(t *testing.T) {
	ctx := context.Background()
	backend := &countingStore{Store: newTestLocal(t)}
	c := newTestCache(t, backend, t.TempDir(), 3000)
	var ds []string
	for i := 0; i < 4; i++ {
		d, err := backend.PutBytes(ctx, bytes.Repeat([]byte{byte('a' + i)}, 1000))
		if err != nil {
			t.Fatal(err)
		}
		ds = append(ds, d)
	}
	a, b, cc, dd := ds[0], ds[1], ds[2], ds[3]
	for _, d := range []string{a, b, cc} {
		readAll(t, c, d)
	}
	readAll(t, c, a) // a is now the most recently used; b the least
	readAll(t, c, dd)
	st := c.Stats()
	if st.Evictions != 1 || st.Bytes != 3000 || st.Entries != 3 {
		t.Fatalf("stats %+v", st)
	}
	if _, err := os.Stat(c.path(b)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("least recently used blob not evicted: %v", err)
	}
	for _, d := range []string{a, cc, dd} {
		if _, err := os.Stat(c.path(d)); err != nil {
			t.Errorf("recent blob evicted: %v", err)
		}
	}
	// b comes back from the backend.
	opens := backend.opens.Load()
	readAll(t, c, b)
	if backend.opens.Load() != opens+1 {
		t.Error("evicted blob not downloaded again")
	}
	if c.Stats().Bytes > 3000 {
		t.Errorf("over capacity: %+v", c.Stats())
	}
}

func TestCacheOversizedBlob(t *testing.T) {
	ctx := context.Background()
	backend := newTestLocal(t)
	c := newTestCache(t, backend, t.TempDir(), 100)
	small, _ := backend.PutBytes(ctx, []byte("small"))
	big := randomBytes(t, 1000)
	dbig, _ := backend.PutBytes(ctx, big)
	readAll(t, c, small)
	if got := readAll(t, c, dbig); !bytes.Equal(got, big) {
		t.Fatal("oversized blob content")
	}
	// The big one evicted the small one and stays alone until the next
	// fill.
	if st := c.Stats(); st.Entries != 1 || st.Bytes != 1000 {
		t.Errorf("stats %+v", st)
	}
	readAll(t, c, small)
	if st := c.Stats(); st.Entries != 1 || st.Bytes != 5 {
		t.Errorf("stats %+v", st)
	}
}

func TestCacheRebuild(t *testing.T) {
	ctx := context.Background()
	backend := &countingStore{Store: newTestLocal(t)}
	dir := t.TempDir()
	c := newTestCache(t, backend, dir, 1<<20)
	var ds []string
	for i := 0; i < 5; i++ {
		d, err := backend.PutBytes(ctx, bytes.Repeat([]byte{byte('0' + i)}, 100*(i+1)))
		if err != nil {
			t.Fatal(err)
		}
		readAll(t, c, d)
		// Distinct mtimes: d[0] oldest.
		at := time.Now().Add(time.Duration(i-10) * time.Minute)
		if err := os.Chtimes(c.path(d), at, at); err != nil {
			t.Fatal(err)
		}
		ds = append(ds, d)
	}
	before := c.Stats()
	// Leftovers of a crashed download.
	stale := filepath.Join(dir, tmpName, "fill-crashed")
	if err := os.WriteFile(stale, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * cacheStaleTmp)
	_ = os.Chtimes(stale, old, old)

	// Restart.
	opens := backend.opens.Load()
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	c2 := newTestCache(t, backend, dir, 1<<20)
	if st := c2.Stats(); st.Bytes != before.Bytes || st.Entries != 5 {
		t.Fatalf("rebuilt stats %+v, before %+v", st, before)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Error("stale temporary file kept")
	}
	for _, d := range ds {
		if size, err := c2.Stat(ctx, d); err != nil || size <= 0 {
			t.Errorf("Stat after restart: %d, %v", size, err)
		}
		readAll(t, c2, d)
	}
	if backend.opens.Load() != opens {
		t.Errorf("blobs on disk downloaded again after a restart (%d opens)", backend.opens.Load()-opens)
	}

	// Restart with a smaller capacity: the oldest (by mtime) go first.
	for i, d := range ds {
		at := time.Now().Add(time.Duration(i-10) * time.Minute)
		_ = os.Chtimes(c.path(d), at, at)
	}
	_ = c2.Close()
	c3 := newTestCache(t, backend, dir, 900) // fits ds[3] (400) + ds[4] (500)
	st := c3.Stats()
	if st.Entries != 2 || st.Bytes != 900 || st.Evictions != 3 {
		t.Fatalf("stats after shrinking %+v", st)
	}
	for i, d := range ds {
		_, err := os.Stat(c3.path(d))
		if (i >= 3) != (err == nil) {
			t.Errorf("blob %d (mtime rank %d): stat %v", i, i, err)
		}
	}
}

func TestCacheCorruptFileAfterRestart(t *testing.T) {
	ctx := context.Background()
	backend := &countingStore{Store: newTestLocal(t)}
	dir := t.TempDir()
	c := newTestCache(t, backend, dir, 1<<20)
	data := randomBytes(t, 5000)
	d, _ := backend.PutBytes(ctx, data)
	readAll(t, c, d)
	// Damage the cached copy (crash without fsync, disk error...).
	bad := append([]byte(nil), data...)
	bad[10] ^= 0xff
	if err := os.WriteFile(c.path(d), bad, 0o600); err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	c2 := newTestCache(t, backend, dir, 1<<20)
	opens := backend.opens.Load()
	if got := readAll(t, c2, d); !bytes.Equal(got, data) {
		t.Fatal("corrupt cached copy served")
	}
	if backend.opens.Load() != opens+1 {
		t.Error("corrupt cached copy not downloaded again")
	}
	if on, _ := os.ReadFile(c2.path(d)); !bytes.Equal(on, data) {
		t.Error("cached copy not repaired")
	}
}

func TestCacheCorruptBackend(t *testing.T) {
	ctx := context.Background()
	backend := newTestLocal(t)
	c := newTestCache(t, backend, t.TempDir(), 1<<20)
	data := randomBytes(t, 5000)
	d, _ := backend.PutBytes(ctx, data)
	data[0] ^= 1
	if err := os.WriteFile(backend.path(d), data, 0o600); err != nil {
		t.Fatal(err)
	}
	// A backend without its own verification still gets caught.
	c2 := newTestCache(t, rawStore{backend}, t.TempDir(), 1<<20)
	for _, s := range []Store{c, c2} {
		if _, err := s.Open(ctx, d); !errors.Is(err, ErrCorrupt) {
			t.Errorf("Open of corrupt content: %v", err)
		}
	}
	for _, s := range []*Cache{c, c2} {
		if st := s.Stats(); st.Entries != 0 {
			t.Errorf("corrupt content cached: %+v", st)
		}
		if entries, _ := os.ReadDir(s.tmp); len(entries) != 0 {
			t.Errorf("leftover temporary files: %v", entries)
		}
	}
}

// rawStore opens files of a Local store without verifying them.
type rawStore struct{ *Local }

func (s rawStore) Open(_ context.Context, d string) (io.ReadCloser, error) {
	return os.Open(s.path(d))
}

func TestCacheSingleflight(t *testing.T) {
	ctx := context.Background()
	local := newTestLocal(t)
	data := randomBytes(t, 50_000)
	d, _ := local.PutBytes(ctx, data)
	backend := &countingStore{Store: local, gate: make(chan struct{})}
	c := newTestCache(t, backend, t.TempDir(), 1<<20)
	const n = 16
	var joined sync.WaitGroup
	joined.Add(n)
	c.onJoin = joined.Done
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rc, err := c.Open(ctx, d)
			if err == nil {
				var got []byte
				got, err = io.ReadAll(rc)
				rc.Close()
				if err == nil && !bytes.Equal(got, data) {
					err = errors.New("content differs")
				}
			}
			errs <- err
		}()
	}
	joined.Wait() // everyone waits for the same download
	close(backend.gate)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := backend.opens.Load(); got != 1 {
		t.Errorf("%d backend opens for %d concurrent misses, want 1", got, n)
	}
	if st := c.Stats(); st.Misses != n {
		t.Errorf("stats %+v", st)
	}
}

func TestCacheWaiterCancel(t *testing.T) {
	local := newTestLocal(t)
	d, _ := local.PutBytes(context.Background(), []byte("slow"))
	backend := &countingStore{Store: local, gate: make(chan struct{})}
	c := newTestCache(t, backend, t.TempDir(), 1<<20)
	ctx, cancel := context.WithCancel(context.Background())
	joined := make(chan struct{})
	c.onJoin = func() { close(joined) }
	done := make(chan error)
	go func() {
		_, err := c.Open(ctx, d)
		done <- err
	}()
	<-joined
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Open: %v", err)
	}
	c.onJoin = nil
	// The download goes on and serves the next caller.
	close(backend.gate)
	if got := readAll(t, c, d); string(got) != "slow" {
		t.Fatalf("got %q", got)
	}
	if backend.opens.Load() != 1 {
		t.Errorf("%d backend opens", backend.opens.Load())
	}
}

func TestCacheDelete(t *testing.T) {
	ctx := context.Background()
	backend := newTestLocal(t)
	c := newTestCache(t, backend, t.TempDir(), 1<<20)
	d, _ := c.PutBytes(ctx, []byte("bye"))
	readAll(t, c, d)
	if err := c.Delete(ctx, d); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(c.path(d)); !errors.Is(err, os.ErrNotExist) {
		t.Error("cached copy kept after Delete")
	}
	if _, err := c.Open(ctx, d); !errors.Is(err, ErrNotFound) {
		t.Errorf("Open after Delete: %v", err)
	}
	if st := c.Stats(); st.Entries != 0 || st.Bytes != 0 {
		t.Errorf("stats %+v", st)
	}
}

// TestCacheFileDeletedBehindItsBack: an operator deletes a cached file;
// the cache downloads it again instead of failing.
func TestCacheFileDeletedBehindItsBack(t *testing.T) {
	ctx := context.Background()
	backend := &countingStore{Store: newTestLocal(t)}
	c := newTestCache(t, backend, t.TempDir(), 1<<20)
	d, _ := backend.PutBytes(ctx, []byte("shared"))
	readAll(t, c, d)
	if err := os.Remove(c.path(d)); err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, c, d); string(got) != "shared" {
		t.Fatalf("got %q", got)
	}
	if backend.opens.Load() != 2 {
		t.Errorf("%d backend opens", backend.opens.Load())
	}
}

// TestCacheDirectoryLock: a directory belongs to one cache at a time, so
// several caches cannot fill it to their capacity each (three caches of
// 1 MiB on one directory used to hold 2.7 MiB) nor evict each other's
// files.
func TestCacheDirectoryLock(t *testing.T) {
	ctx := context.Background()
	backend := newTestLocal(t)
	dir := t.TempDir()
	c := newTestCache(t, backend, dir, 1<<20)
	for i := 0; i < 3; i++ {
		_, err := NewCache(backend, dir, 1<<20, nil)
		if err == nil || !strings.Contains(err.Error(), "in use by another process") {
			t.Fatalf("second cache on the same directory: %v", err)
		}
	}
	// A relative spelling of the same directory is the same directory.
	t.Chdir(filepath.Dir(dir))
	if _, err := NewCache(backend, filepath.Base(dir), 1<<20, nil); err == nil {
		t.Fatal("same directory through a relative path accepted")
	}
	var ds []string
	for i := 0; i < 3; i++ {
		d, err := backend.PutBytes(ctx, randomBytes(t, 900<<10))
		if err != nil {
			t.Fatal(err)
		}
		readAll(t, c, d)
		ds = append(ds, d)
	}
	var onDisk int64
	if err := walkTree(ctx, dir, func(_ string, fi fs.FileInfo) error {
		onDisk += fi.Size()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if onDisk > 1<<20 {
		t.Errorf("%d bytes in the directory, capacity %d", onDisk, 1<<20)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	c2 := newTestCache(t, backend, dir, 1<<20)
	if got := readAll(t, c2, ds[2]); len(got) != 900<<10 {
		t.Errorf("reopened cache: %d bytes", len(got))
	}
}

// TestCacheStatAsksBackend: the garbage collector deletes blobs from the
// backend directly; a cached copy must not make Stat claim they are still
// stored, or a re-upload of the same content would be skipped.
func TestCacheStatAsksBackend(t *testing.T) {
	ctx := context.Background()
	backend := newTestLocal(t)
	c := newTestCache(t, backend, t.TempDir(), 1<<20)
	data := []byte("collected")
	d, err := c.PutBytes(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	readAll(t, c, d)
	if err := backend.Delete(ctx, d); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Stat(ctx, d); !errors.Is(err, ErrNotFound) {
		t.Errorf("Stat of a blob gone from the backend: %v", err)
	}
	// Reads may still be served by the verified cached copy.
	if got := readAll(t, c, d); !bytes.Equal(got, data) {
		t.Errorf("got %q", got)
	}
	if _, err := c.PutBytes(ctx, data); err != nil {
		t.Fatal(err)
	}
	if size, err := backend.Stat(ctx, d); err != nil || size != int64(len(data)) {
		t.Errorf("backend after the re-upload: %d, %v", size, err)
	}
}

// TestCacheHashesOnce: a fill hashes the content once. The verifier of the
// backend checks it; the cache hashes only for backends that do not.
func TestCacheHashesOnce(t *testing.T) {
	ctx := context.Background()
	var hashes atomic.Int64
	orig := newHash
	newHash = func() hash.Hash { hashes.Add(1); return orig() }
	t.Cleanup(func() { newHash = orig })
	backend := newTestLocal(t)
	data := randomBytes(t, 100<<10)
	d, err := backend.PutBytes(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		backend Store
	}{
		{"verifying backend", backend},
		{"backend without verification", rawStore{backend}},
	} {
		c := newTestCache(t, tc.backend, t.TempDir(), 1<<20)
		hashes.Store(0)
		if got := readAll(t, c, d); !bytes.Equal(got, data) {
			t.Fatalf("%s: content differs", tc.name)
		}
		if n := hashes.Load(); n != 1 {
			t.Errorf("%s: content hashed %d times during a fill, want 1", tc.name, n)
		}
		// Hits are not hashed at all.
		readAll(t, c, d)
		if n := hashes.Load(); n != 1 {
			t.Errorf("%s: a hit hashed the content", tc.name)
		}
	}
}

func TestCacheMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	c, err := NewCache(newTestLocal(t), t.TempDir(), 1<<20, reg)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := c.PutBytes(context.Background(), []byte("metric"))
	readAll(t, c, d)
	readAll(t, c, d)
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]float64{}
	for _, mf := range mfs {
		m := mf.GetMetric()[0]
		if m.GetCounter() != nil {
			got[mf.GetName()] = m.GetCounter().GetValue()
		} else {
			got[mf.GetName()] = m.GetGauge().GetValue()
		}
	}
	want := map[string]float64{
		"cms_blob_cache_hits_total":      1,
		"cms_blob_cache_misses_total":    1,
		"cms_blob_cache_evictions_total": 0,
		"cms_blob_cache_bytes":           6,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v (all: %v)", k, got[k], v, got)
		}
	}
	// A second cache on the same registry is a configuration error.
	if _, err := NewCache(newTestLocal(t), t.TempDir(), 1<<20, reg); err == nil {
		t.Error("duplicate metrics accepted")
	}
}

func TestCacheConstructor(t *testing.T) {
	if _, err := NewCache(newTestLocal(t), t.TempDir(), 0, nil); err == nil {
		t.Error("zero capacity accepted")
	}
	if _, err := NewCache(newTestLocal(t), "", 10, nil); err == nil {
		t.Error("empty directory accepted")
	}
	dir := t.TempDir()
	t.Chdir(dir)
	c, err := NewCache(newTestLocal(t), "cache", 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(c.Dir()) || !strings.HasPrefix(c.Dir(), dir) {
		t.Errorf("cache dir %q", c.Dir())
	}
	if err := Check(context.Background(), c); err != nil {
		t.Error(err)
	}
}
