// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package blob

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
)

func TestOpen(t *testing.T) {
	ctx := context.Background()
	base := config.Default().Storage
	base.Local.Path = t.TempDir()
	base.CacheDir = t.TempDir()

	// local: never cached (it would copy files within the same disk).
	s, err := Open(base, "worker", prometheus.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.(*Local); !ok {
		t.Errorf("local backend is %T", s)
	}

	// remote: cached in cache_dir/worker, metrics registered, uploads
	// spooled in its tmp directory; only the token is needed.
	f := newRemote(t, newTestLocal(t), 0)
	cfg := base
	cfg.Backend = "remote"
	cfg.Remote.URL = f.url
	cfg.Remote.Token = Token(testSecret)
	reg := prometheus.NewRegistry()
	s, err = Open(cfg, "worker", reg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Close(s) })
	c, ok := s.(*Cache)
	if !ok {
		t.Fatalf("remote backend is %T", s)
	}
	if want := filepath.Join(base.CacheDir, "worker"); c.Dir() != want {
		t.Errorf("cache dir %s, want %s", c.Dir(), want)
	}
	client, ok := c.Backend().(*Client)
	if !ok {
		t.Fatalf("cache backend is %T", c.Backend())
	}
	if client.tmpDir != c.tmp {
		t.Errorf("remote uploads spooled in %q, want %q", client.tmpDir, c.tmp)
	}
	d, err := s.PutBytes(ctx, []byte("through the factory"))
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, s, d); string(got) != "through the factory" {
		t.Errorf("got %q", got)
	}
	if err := Check(ctx, s); err != nil {
		t.Error(err)
	}
	if mfs, _ := reg.Gather(); len(mfs) != 4 {
		t.Errorf("%d metric families registered", len(mfs))
	}
	noToken := cfg
	noToken.Remote.Token = ""
	if _, err := Open(noToken, "other", nil); err == nil {
		t.Error("remote store without token")
	}

	// Two processes with the same name cannot share the cache directory
	// (each would fill it to the capacity); another name gets its own.
	if _, err := Open(cfg, "worker", nil); err == nil || !strings.Contains(err.Error(), "in use") {
		t.Errorf("second cache on cache_dir/worker: %v", err)
	}
	s2, err := Open(cfg, "contest-web", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Close(s2); err != nil {
		t.Error(err)
	}
	if err := Close(s); err != nil {
		t.Error(err)
	}
	if s, err = Open(cfg, "worker", nil); err != nil {
		t.Fatalf("reopening after Close: %v", err)
	}
	_ = Close(s)

	// remote without cache.
	cfg.CacheSizeMB = 0
	if s, err = Open(cfg, "worker", nil); err != nil {
		t.Fatal(err)
	}
	if client, ok := s.(*Client); !ok {
		t.Errorf("uncached remote backend is %T", s)
	} else if want := filepath.Join(base.CacheDir, "worker", tmpName); client.tmpDir != want {
		t.Errorf("remote uploads spooled in %q, want %q", client.tmpDir, want)
	}

	// s3: cached, uploads spooled on disk.
	srv := httptest.NewServer(gofakes3.New(s3mem.New(), gofakes3.WithAutoBucket(true)).Server())
	t.Cleanup(srv.Close)
	cfg = base
	cfg.Backend = "s3"
	cfg.S3 = config.S3Blob{Endpoint: srv.URL, Region: "us-east-1", Bucket: "cms", AccessKey: "a", SecretKey: "s"}
	if s, err = Open(cfg, "dispatcher", nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Close(s) })
	if c, ok := s.(*Cache); !ok || c.Backend() == nil {
		t.Errorf("s3 backend is %T", s)
	} else if s3, ok := c.Backend().(*S3); !ok {
		t.Errorf("cache backend is %T", c.Backend())
	} else if s3.tmpDir != c.tmp {
		t.Errorf("s3 uploads spooled in %q, want %q", s3.tmpDir, c.tmp)
	}
	if d, err = s.PutBytes(ctx, []byte("in s3")); err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, s, d); string(got) != "in s3" {
		t.Errorf("got %q", got)
	}
	// Without a cache directory, $TMPDIR as before.
	cfg.CacheDir, cfg.CacheSizeMB = "", 0
	if s, err = OpenBackend(cfg, "gc"); err != nil {
		t.Fatal(err)
	} else if s.(*S3).tmpDir != "" {
		t.Errorf("s3 without cache_dir spools in %q", s.(*S3).tmpDir)
	}

	for _, name := range []string{"", "..", "a/b", "Worker", "-x", "../worker"} {
		if _, err := Open(base, name, nil); err == nil {
			t.Errorf("name %q accepted", name)
		}
	}
	cfg.Backend = "tape"
	if _, err := Open(cfg, "worker", nil); err == nil || !strings.Contains(err.Error(), "tape") {
		t.Errorf("unknown backend: %v", err)
	}
}

// TestOpenSpoolsOnDisk: large uploads to s3 are spooled in the cache
// directory, not in $TMPDIR (often a tmpfs, i.e. memory).
func TestOpenSpoolsOnDisk(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", filepath.Join(tmp, "missing")) // spooling there fails
	srv := httptest.NewServer(gofakes3.New(s3mem.New(), gofakes3.WithAutoBucket(true)).Server())
	t.Cleanup(srv.Close)
	cfg := config.Default().Storage
	cfg.Backend = "s3"
	cfg.CacheDir = filepath.Join(tmp, "cache")
	cfg.S3 = config.S3Blob{Endpoint: srv.URL, Region: "us-east-1", Bucket: "cms", AccessKey: "a", SecretKey: "s"}
	s, err := Open(cfg, "admin-web", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Close(s) })
	data := randomBytes(t, spoolMemLimit+1)
	d, _, err := s.Put(context.Background(), bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Put of %d bytes: %v", len(data), err)
	}
	if got := readAll(t, s, d); !bytes.Equal(got, data) {
		t.Fatal("content differs")
	}
}

// TestNoDatabaseDependency: workers use this package and must never reach
// PostgreSQL (DECISIONS.md D12).
func TestNoDatabaseDependency(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, dep := range strings.Fields(string(out)) {
		if strings.HasPrefix(dep, "github.com/jackc/pgx") || strings.HasSuffix(dep, "/internal/db") {
			t.Errorf("package blob depends on %s", dep)
		}
	}
}
