// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package blob

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestLocal(t testing.TB) *Local {
	t.Helper()
	s, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestLocal(t *testing.T) {
	testStore(t, newTestLocal(t), storeOptions{})
}

func TestLocalLayout(t *testing.T) {
	s := newTestLocal(t)
	d, err := s.PutBytes(context.Background(), []byte("layout"))
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(s.Root(), d[:2], d[2:4], d)
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatalf("blob not at %s: %v", p, err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		t.Errorf("blob readable by others: %v", fi.Mode())
	}
	if di, _ := os.Stat(filepath.Dir(p)); di.Mode().Perm() != dirPerm {
		t.Errorf("shard directory mode %v", di.Mode())
	}
	// No temporary files are left behind.
	if entries, _ := os.ReadDir(filepath.Join(s.Root(), tmpName)); len(entries) != 0 {
		t.Errorf("leftover temporary files: %v", entries)
	}
}

func TestLocalRootIsAbsolute(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	s, err := NewLocal("blobs")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "blobs"); s.Root() != want {
		t.Errorf("root %q, want %q", s.Root(), want)
	}
	if _, err := NewLocal(""); err == nil {
		t.Error("empty root accepted")
	}
}

func TestLocalCorruption(t *testing.T) {
	s := newTestLocal(t)
	data := randomBytes(t, 10_000)
	d, err := s.PutBytes(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	p := s.path(d)
	data[5000] ^= 0x01 // flip one bit on disk
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	expectCorrupt(t, s, d)
	// Truncation is corruption too.
	if err := os.WriteFile(p, data[:100], 0o600); err != nil {
		t.Fatal(err)
	}
	expectCorrupt(t, s, d)
}

func TestLocalRePutIsCheap(t *testing.T) {
	s := newTestLocal(t)
	ctx := context.Background()
	data := []byte("same content")
	d, err := s.PutBytes(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(s.path(d), old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutBytes(ctx, data); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Put(ctx, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	// The existing file was not rewritten.
	if fi, _ := os.Stat(s.path(d)); !fi.ModTime().Equal(old) {
		t.Errorf("existing blob rewritten: mtime %v", fi.ModTime())
	}
}

func TestLocalIgnoresStrayFiles(t *testing.T) {
	s := newTestLocal(t)
	d, err := s.PutBytes(context.Background(), []byte("real"))
	if err != nil {
		t.Fatal(err)
	}
	// Junk an operator (or a crash) may leave around.
	stray := []string{
		filepath.Join(s.Root(), "README"),
		filepath.Join(s.Root(), d[:2], "notes.txt"),
		filepath.Join(s.Root(), d[:2], d[2:4], "x"),
		filepath.Join(s.Root(), "zz", "zz", strings.Repeat("a", 64)),              // not hex shard
		filepath.Join(s.Root(), "00", "00", Digest([]byte("in the wrong shard"))), // wrong shard
		filepath.Join(s.Root(), tmpName, "put-123"),
	}
	for _, p := range stray {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("junk"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got := walkAll(t, s)
	if len(got) != 1 || got[d] != 4 {
		t.Errorf("Walk = %v", got)
	}
}

func TestLocalRemovesStaleTemps(t *testing.T) {
	dir := t.TempDir()
	s, err := NewLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(s.tmp, "put-stale")
	fresh := filepath.Join(s.tmp, "put-fresh")
	for _, p := range []string{stale, fresh} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * staleTmp)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := NewLocal(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Error("stale temporary file kept")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("fresh temporary file (maybe another process writing) removed")
	}
}

func TestLocalCancelledPut(t *testing.T) {
	s := newTestLocal(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := s.Put(ctx, bytes.NewReader([]byte("x"))); !errors.Is(err, context.Canceled) {
		t.Fatalf("Put: %v", err)
	}
	if _, err := s.PutBytes(ctx, []byte("x")); !errors.Is(err, context.Canceled) {
		t.Fatalf("PutBytes: %v", err)
	}
	if entries, _ := os.ReadDir(s.tmp); len(entries) != 0 {
		t.Errorf("leftover temporary files: %v", entries)
	}
	if err := s.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
}
