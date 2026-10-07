// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package blob

import (
	"context"
	"encoding/binary"
	"io"
	"testing"
)

// benchSize is a typical test case or source file.
const benchSize = 64 << 10

// BenchmarkLocalPut stores new content each time (fsync included).
func BenchmarkLocalPut(b *testing.B) {
	s := newTestLocal(b)
	data := randomBytes(b, benchSize)
	ctx := context.Background()
	b.SetBytes(benchSize)
	b.ReportAllocs()
	var i uint64
	for b.Loop() {
		i++
		binary.LittleEndian.PutUint64(data, i)
		if _, err := s.PutBytes(ctx, data); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLocalPutExisting re-stores known content (one stat).
func BenchmarkLocalPutExisting(b *testing.B) {
	s := newTestLocal(b)
	data := randomBytes(b, benchSize)
	ctx := context.Background()
	if _, err := s.PutBytes(ctx, data); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(benchSize)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := s.PutBytes(ctx, data); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLocalOpen reads and verifies a blob.
func BenchmarkLocalOpen(b *testing.B) {
	s := newTestLocal(b)
	ctx := context.Background()
	d, err := s.PutBytes(ctx, randomBytes(b, benchSize))
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(benchSize)
	b.ReportAllocs()
	for b.Loop() {
		rc, err := s.Open(ctx, d)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := copyBuffered(io.Discard, rc); err != nil {
			b.Fatal(err)
		}
		rc.Close()
	}
}

// BenchmarkCacheHit opens and reads a cached blob (no hashing on hits).
func BenchmarkCacheHit(b *testing.B) {
	ctx := context.Background()
	backend := newTestLocal(b)
	d, err := backend.PutBytes(ctx, randomBytes(b, benchSize))
	if err != nil {
		b.Fatal(err)
	}
	c := newTestCache(b, backend, b.TempDir(), 1<<30)
	readAll(b, c, d)
	b.SetBytes(benchSize)
	b.ReportAllocs()
	for b.Loop() {
		rc, err := c.Open(ctx, d)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := copyBuffered(io.Discard, rc); err != nil {
			b.Fatal(err)
		}
		rc.Close()
	}
}

// BenchmarkCacheFill downloads a 1 MiB blob into the cache each time (two
// blobs alternate in a cache that holds one): the content is hashed once.
func BenchmarkCacheFill(b *testing.B) {
	const size = 1 << 20
	ctx := context.Background()
	backend := newTestLocal(b)
	var ds [2]string
	for i := range ds {
		d, err := backend.PutBytes(ctx, randomBytes(b, size))
		if err != nil {
			b.Fatal(err)
		}
		ds[i] = d
	}
	c := newTestCache(b, backend, b.TempDir(), size)
	b.SetBytes(size)
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		i++
		rc, err := c.Open(ctx, ds[i%2])
		if err != nil {
			b.Fatal(err)
		}
		rc.Close()
	}
	if st := c.Stats(); st.Misses < int64(i) {
		b.Fatalf("%d misses for %d opens: not measuring fills", st.Misses, i)
	}
}

// BenchmarkCacheHitParallel measures contention on the cache index.
func BenchmarkCacheHitParallel(b *testing.B) {
	ctx := context.Background()
	backend := newTestLocal(b)
	d, err := backend.PutBytes(ctx, []byte("small statement"))
	if err != nil {
		b.Fatal(err)
	}
	c := newTestCache(b, backend, b.TempDir(), 1<<30)
	readAll(b, c, d)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			rc, err := c.Open(ctx, d)
			if err != nil {
				b.Error(err)
				return
			}
			rc.Close()
		}
	})
}

func BenchmarkValidDigest(b *testing.B) {
	d := Digest([]byte("x"))
	for b.Loop() {
		if !ValidDigest(d) {
			b.Fatal("invalid")
		}
	}
}

func BenchmarkValidDigestRegexp(b *testing.B) {
	d := Digest([]byte("x"))
	for b.Loop() {
		if !digestRE.MatchString(d) {
			b.Fatal("invalid")
		}
	}
}
