// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package blob

import (
	"bytes"
	"container/list"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/sync/singleflight"
)

const (
	// fillTimeout bounds a download into the cache. It does not depend on
	// the context of the caller that started it: other callers may be
	// waiting for the same blob.
	fillTimeout = 15 * time.Minute
	// cacheStaleTmp: temporary files older than this are leftovers.
	cacheStaleTmp = time.Hour
	// touchEvery limits how often a hit updates the file mtime, which
	// carries the LRU order across restarts.
	touchEvery = time.Minute
	// openAttempts bounds the retries of Open when concurrent fills keep
	// evicting the blob it just fetched (capacity far too small); after
	// that the content is streamed straight from the backend.
	openAttempts = 3
)

// Cache is an on-disk LRU cache in front of another Store, bounded in
// bytes. Workers and web servers use it so test cases, managers and
// statements are downloaded once per machine.
//
//   - Concurrent Opens of the same missing blob download it once
//     (singleflight); the others wait for that download.
//   - Content is verified against its digest while it is downloaded, and
//     blobs found on disk at startup are verified on first use; hits then
//     return the file directly, without hashing it again.
//   - The index is rebuilt at startup by scanning the directory; file
//     mtimes carry the LRU order across restarts.
//   - When over capacity, least recently used blobs are deleted. A blob
//     larger than the whole capacity is still served (it is evicted by the
//     next fill). Files already open stay readable after eviction.
//
// A directory belongs to one Cache at a time: NewCache locks it (flock on
// dir/lock, released by Close or when the process exits) and fails if
// another process holds it. The capacity is enforced by the index of the
// owner, so two processes sharing a directory would each fill it up to the
// capacity and delete each other's files; Open gives every service its own
// subdirectory instead. A file deleted behind the cache's back (by an
// operator) is just downloaded again.
//
// Writes (Put, PutBytes, Delete) go to the backend; Delete also drops the
// cached copy. Stat asks the backend too: the cache may still hold blobs
// the backend no longer has.
type Cache struct {
	backend  Store
	dir      string
	tmp      string
	capacity int64

	lock      *os.File // holds the flock on dir/lock
	closeOnce sync.Once

	mu      sync.Mutex
	entries map[string]*cacheEntry
	lru     list.List // of *cacheEntry, most recently used first
	bytes   int64

	fills singleflight.Group

	hits, misses, evictions atomic.Int64

	// onJoin, if set (tests), is called when an Open starts waiting for a
	// download.
	onJoin func()
}

type cacheEntry struct {
	digest   string
	size     int64
	verified bool
	touched  time.Time
	elem     *list.Element
}

// CacheStats is a snapshot of the cache counters.
type CacheStats struct {
	Hits, Misses, Evictions int64
	Bytes                   int64
	Entries                 int
}

// NewCache returns a cache of backend in dir holding at most capacity
// bytes. dir is created if needed, converted to an absolute path and
// locked (it fails if another Cache, in this process or another one, holds
// it); the blobs already there are indexed. With reg non-nil, the metrics
// cms_blob_cache_{hits,misses,evictions}_total and cms_blob_cache_bytes are
// registered on it. Close releases the directory.
func NewCache(backend Store, dir string, capacity int64, reg prometheus.Registerer) (*Cache, error) {
	if capacity <= 0 {
		return nil, fmt.Errorf("blob: cache capacity must be positive, got %d", capacity)
	}
	if dir == "" {
		return nil, errors.New("blob: cache: empty directory")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("blob: cache: %w", err)
	}
	c := &Cache{
		backend:  backend,
		dir:      abs,
		tmp:      filepath.Join(abs, tmpName),
		capacity: capacity,
		entries:  map[string]*cacheEntry{},
	}
	if err := os.MkdirAll(c.tmp, dirPerm); err != nil {
		return nil, fmt.Errorf("blob: cache: %w", err)
	}
	lock, err := lockDir(abs)
	if err != nil {
		return nil, err
	}
	c.lock = lock
	removeStaleTemps(c.tmp, cacheStaleTmp)
	if err := c.rebuild(); err != nil {
		_ = c.Close()
		return nil, err
	}
	if reg != nil {
		if err := c.register(reg); err != nil {
			_ = c.Close()
			return nil, err
		}
	}
	return c, nil
}

// lockName is the lock file of a cache directory; walkTree ignores it.
const lockName = "lock"

// lockDir takes the exclusive lock of the cache directory dir.
func lockDir(dir string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(dir, lockName), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("blob: cache: %w", err)
	}
	if err := tryLock(f); err != nil {
		_ = f.Close()
		if errors.Is(err, errLocked) {
			return nil, fmt.Errorf("blob: cache: %s is in use by another process; every process "+
				"needs its own cache directory (give a second instance of a service on this "+
				"machine another storage.cache_dir)", dir)
		}
		return nil, fmt.Errorf("blob: cache: locking %s: %w", dir, err)
	}
	return f, nil
}

// Close releases the cache directory. The cache must not be used
// afterwards; files already opened stay readable.
func (c *Cache) Close() error {
	var err error
	c.closeOnce.Do(func() {
		if c.lock != nil {
			err = c.lock.Close() // closing the descriptor drops the flock
		}
	})
	return err
}

// rebuild indexes the blobs on disk, oldest first, and enforces the
// capacity (it may have shrunk since the last run).
func (c *Cache) rebuild() error {
	type found struct {
		d     string
		size  int64
		mtime time.Time
	}
	var all []found
	err := walkTree(context.Background(), c.dir, func(d string, fi fs.FileInfo) error {
		all = append(all, found{d, fi.Size(), fi.ModTime()})
		return nil
	})
	if err != nil {
		return fmt.Errorf("blob: cache: scanning %s: %w", c.dir, err)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].mtime.Before(all[j].mtime) })
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, f := range all {
		e := &cacheEntry{digest: f.d, size: f.size, touched: f.mtime}
		e.elem = c.lru.PushFront(e)
		c.entries[f.d] = e
		c.bytes += f.size
	}
	c.evictLocked(nil)
	return nil
}

func (c *Cache) register(reg prometheus.Registerer) error {
	for _, col := range []prometheus.Collector{
		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "cms_blob_cache_hits_total",
			Help: "Blob opens served from the local cache.",
		}, func() float64 { return float64(c.hits.Load()) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "cms_blob_cache_misses_total",
			Help: "Blob opens that had to wait for a download (or a verification).",
		}, func() float64 { return float64(c.misses.Load()) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "cms_blob_cache_evictions_total",
			Help: "Blobs deleted from the local cache to stay under its capacity.",
		}, func() float64 { return float64(c.evictions.Load()) }),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "cms_blob_cache_bytes",
			Help: "Bytes held by the local blob cache.",
		}, func() float64 {
			c.mu.Lock()
			defer c.mu.Unlock()
			return float64(c.bytes)
		}),
	} {
		if err := reg.Register(col); err != nil {
			return fmt.Errorf("blob: cache metrics: %w", err)
		}
	}
	return nil
}

// Backend returns the store behind the cache.
func (c *Cache) Backend() Store { return c.backend }

// Dir returns the absolute cache directory.
func (c *Cache) Dir() string { return c.dir }

// Stats returns the current counters.
func (c *Cache) Stats() CacheStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return CacheStats{
		Hits:      c.hits.Load(),
		Misses:    c.misses.Load(),
		Evictions: c.evictions.Load(),
		Bytes:     c.bytes,
		Entries:   len(c.entries),
	}
}

func (c *Cache) path(d string) string { return shardPath(c.dir, d) }

// errChurn: the blob was evicted again before it could be opened.
var errChurn = errors.New("blob: cache: evicted while opening")

// Open implements Store. A hit returns the cached *os.File (seekable;
// copying it to another file uses copy_file_range, serving it over HTTP
// uses sendfile).
func (c *Cache) Open(ctx context.Context, d string) (io.ReadCloser, error) {
	if err := checkDigest(d); err != nil {
		return nil, err
	}
	f, err := c.openFile(ctx, d)
	if errors.Is(err, errChurn) {
		return c.backend.Open(ctx, d)
	}
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (c *Cache) openFile(ctx context.Context, d string) (*os.File, error) {
	for attempt := 0; attempt < openAttempts; attempt++ {
		f, err := c.lookup(d)
		if err != nil {
			return nil, err
		}
		if f != nil {
			if attempt == 0 {
				c.hits.Add(1)
			}
			return f, nil
		}
		if attempt == 0 {
			c.misses.Add(1)
		}
		if err := c.fill(ctx, d); err != nil {
			return nil, err
		}
	}
	return nil, errChurn
}

// lookup opens the cached file of d if it is indexed and verified (nil,
// nil otherwise). The file is opened under the lock, so eviction cannot
// delete it in between.
func (c *Cache) lookup(d string) (*os.File, error) {
	c.mu.Lock()
	e := c.entries[d]
	if e == nil || !e.verified {
		c.mu.Unlock()
		return nil, nil
	}
	f, err := os.Open(c.path(d))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Deleted behind our back (by an operator): forget it and
			// download it again.
			c.removeLocked(e, false)
			err = nil
		} else {
			err = fmt.Errorf("blob: cache: %w", err)
		}
		c.mu.Unlock()
		return nil, err
	}
	c.lru.MoveToFront(e.elem)
	now := time.Now()
	touch := now.Sub(e.touched) >= touchEvery
	if touch {
		e.touched = now
	}
	c.mu.Unlock()
	if touch {
		// Persist the recency for the next startup; atime is left alone.
		_ = os.Chtimes(f.Name(), time.Time{}, now)
	}
	return f, nil
}

// fill makes sure a verified copy of d is in the cache. Concurrent calls
// for the same digest share one download; a caller whose ctx ends stops
// waiting but the download goes on for the others.
func (c *Cache) fill(ctx context.Context, d string) error {
	ch := c.fills.DoChan(d, func() (any, error) {
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), fillTimeout)
		defer cancel()
		return nil, c.fetch(fctx, d)
	})
	if c.onJoin != nil {
		c.onJoin()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case res := <-ch:
		return res.Err
	}
}

// fetch verifies the copy found at startup or downloads d.
func (c *Cache) fetch(ctx context.Context, d string) error {
	c.mu.Lock()
	e := c.entries[d]
	c.mu.Unlock()
	if e != nil {
		if e.verified {
			return nil
		}
		ok := verifyFile(c.path(d), d)
		c.mu.Lock()
		if c.entries[d] == e {
			if ok {
				e.verified = true
			} else {
				c.removeLocked(e, true)
			}
		}
		c.mu.Unlock()
		if ok {
			return nil
		}
	}
	return c.download(ctx, d)
}

// verifyFile reports whether the file at path hashes to d.
func verifyFile(path, d string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	h := newHash()
	if _, err := copyBuffered(h, f); err != nil {
		return false
	}
	want, _ := hex.DecodeString(d)
	return bytes.Equal(h.Sum(nil), want)
}

// download copies d from the backend into the cache, verifying it. The
// readers of this package's backends verify the content themselves (and
// fail with ErrCorrupt at the end); only those of other Store
// implementations are hashed here.
func (c *Cache) download(ctx context.Context, d string) (err error) {
	rc, err := c.backend.Open(ctx, d)
	if err != nil {
		return err
	}
	defer rc.Close()
	f, err := os.CreateTemp(c.tmp, "fill-*")
	if err != nil {
		return fmt.Errorf("blob: cache: %w", err)
	}
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = os.Remove(f.Name())
		}
	}()
	dst := io.Writer(f)
	var h hash.Hash
	if !verifies(rc, d) {
		h = newHash()
		dst = io.MultiWriter(f, h)
	}
	n, err := copyBuffered(dst, ctxReader{ctx, rc})
	if errors.Is(err, ErrCorrupt) {
		return err
	}
	if err != nil {
		return fmt.Errorf("blob: cache: downloading %s: %w", d, err)
	}
	if h != nil {
		want, _ := hex.DecodeString(d)
		if !bytes.Equal(h.Sum(nil), want) {
			return corrupt(d)
		}
	}
	// No fsync: after a crash a damaged file fails verification on first
	// use and is downloaded again.
	if err := f.Close(); err != nil {
		return fmt.Errorf("blob: cache: %w", err)
	}
	if err := makeShardDirs(c.dir, d, false); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), c.path(d)); err != nil {
		return fmt.Errorf("blob: cache: %w", err)
	}
	c.insert(d, n)
	return nil
}

// insert indexes a freshly downloaded blob and evicts what no longer fits.
func (c *Cache) insert(d string, size int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if old := c.entries[d]; old != nil {
		c.removeLocked(old, false) // same file, replaced by the rename
	}
	e := &cacheEntry{digest: d, size: size, verified: true, touched: time.Now()}
	e.elem = c.lru.PushFront(e)
	c.entries[d] = e
	c.bytes += size
	c.evictLocked(e)
}

// evictLocked deletes least recently used blobs, never keep, until the
// cache fits its capacity.
func (c *Cache) evictLocked(keep *cacheEntry) {
	for c.bytes > c.capacity {
		back := c.lru.Back()
		if back == nil {
			return
		}
		e := back.Value.(*cacheEntry)
		if e == keep {
			return // keep is the only entry left
		}
		c.removeLocked(e, true)
		c.evictions.Add(1)
	}
}

// removeLocked forgets e and, with unlink, deletes its file. Deleting
// under the lock keeps a concurrent download of the same digest from
// losing its freshly renamed file.
func (c *Cache) removeLocked(e *cacheEntry, unlink bool) {
	c.lru.Remove(e.elem)
	delete(c.entries, e.digest)
	c.bytes -= e.size
	if unlink {
		_ = os.Remove(c.path(e.digest))
	}
}

// Stat implements Store by asking the backend, even for cached blobs. The
// cache may hold blobs the backend no longer has (the garbage collector
// deletes them from the backend directly), and Stat decides whether
// content must be uploaded (blob server PUT and HEAD, Client.Put): a
// cached copy answering for the backend would let a re-upload be skipped
// and the blob be lost once evicted.
func (c *Cache) Stat(ctx context.Context, d string) (int64, error) {
	if err := checkDigest(d); err != nil {
		return 0, err
	}
	return c.backend.Stat(ctx, d)
}

// Put implements Store (written to the backend only).
func (c *Cache) Put(ctx context.Context, r io.Reader) (string, int64, error) {
	return c.backend.Put(ctx, r)
}

// PutBytes implements Store (written to the backend only).
func (c *Cache) PutBytes(ctx context.Context, b []byte) (string, error) {
	return c.backend.PutBytes(ctx, b)
}

func (c *Cache) putExpect(ctx context.Context, want string, r io.Reader) (int64, error) {
	return putExpect(ctx, c.backend, want, r)
}

// Delete implements Store: the blob is deleted from the backend and from
// the cache.
func (c *Cache) Delete(ctx context.Context, d string) error {
	if err := checkDigest(d); err != nil {
		return err
	}
	err := c.backend.Delete(ctx, d)
	c.mu.Lock()
	if e := c.entries[d]; e != nil {
		c.removeLocked(e, true)
	}
	c.mu.Unlock()
	return err
}

// Walk implements Store by walking the backend.
func (c *Cache) Walk(ctx context.Context, fn func(digest string, size int64) error) error {
	return c.backend.Walk(ctx, fn)
}

// Check implements Checker: the backend must be healthy and the cache
// directory usable.
func (c *Cache) Check(ctx context.Context) error {
	if _, err := os.Stat(c.tmp); err != nil {
		return fmt.Errorf("blob: cache: %w", err)
	}
	return Check(ctx, c.backend)
}
