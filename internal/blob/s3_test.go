// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package blob

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
	"github.com/minio/minio-go/v7"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
)

// newTestS3 starts an in-memory S3 server (gofakes3) with a bucket and
// returns a store on it, plus the raw client to tamper with objects.
func newTestS3(t testing.TB, prefix string) (*S3, *minio.Client) {
	t.Helper()
	srv := httptest.NewServer(gofakes3.New(s3mem.New()).Server())
	t.Cleanup(srv.Close)
	cfg := config.S3Blob{
		Endpoint:  srv.URL, // http://127.0.0.1:port, agrees with use_ssl=false
		Region:    "us-east-1",
		Bucket:    "cms-test",
		Prefix:    prefix,
		AccessKey: "access",
		SecretKey: "secret",
		UseSSL:    false,
	}
	s, err := NewS3(cfg, WithS3TempDir(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Check(context.Background()); err == nil {
		t.Fatal("Check passed before the bucket exists")
	}
	if err := s.mc.MakeBucket(context.Background(), cfg.Bucket, minio.MakeBucketOptions{Region: cfg.Region}); err != nil {
		t.Fatal(err)
	}
	if err := s.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s, s.mc
}

func TestS3(t *testing.T) {
	s, _ := newTestS3(t, "cms")
	if s.prefix != "cms/" {
		t.Errorf("prefix %q", s.prefix)
	}
	testStore(t, s, storeOptions{})
}

func TestS3Layout(t *testing.T) {
	s, mc := newTestS3(t, "contest/")
	ctx := context.Background()
	d, err := s.PutBytes(ctx, []byte("layout"))
	if err != nil {
		t.Fatal(err)
	}
	key := "contest/" + d[:2] + "/" + d
	if _, err := mc.StatObject(ctx, "cms-test", key, minio.StatObjectOptions{}); err != nil {
		t.Fatalf("object not at %s: %v", key, err)
	}
	// Foreign objects in the bucket are not blobs.
	for _, k := range []string{"contest/README", "contest/ab/" + strings.Repeat("a", 64), "other/" + d[:2] + "/" + d} {
		if _, err := mc.PutObject(ctx, "cms-test", k, bytes.NewReader([]byte("x")), 1, minio.PutObjectOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	got := walkAll(t, s)
	if len(got) != 1 || got[d] != int64(len("layout")) {
		t.Errorf("Walk = %v", got)
	}
}

func TestS3Corruption(t *testing.T) {
	s, mc := newTestS3(t, "")
	ctx := context.Background()
	data := randomBytes(t, 10_000)
	d, err := s.PutBytes(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	data[1234] ^= 0x80
	if _, err := mc.PutObject(ctx, "cms-test", s.key(d), bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{}); err != nil {
		t.Fatal(err)
	}
	expectCorrupt(t, s, d)
}

func TestS3PutExpect(t *testing.T) {
	s, _ := newTestS3(t, "")
	ctx := context.Background()
	if _, err := putExpect(ctx, s, Digest([]byte("b")), bytes.NewReader([]byte("a"))); err == nil {
		t.Fatal("mismatch accepted")
	}
	if got := walkAll(t, s); len(got) != 0 {
		t.Fatalf("mismatching content stored: %v", got)
	}
}

func TestNewS3Config(t *testing.T) {
	ok := []config.S3Blob{
		{Bucket: "b", UseSSL: true},
		{Bucket: "b", Endpoint: "minio:9000"},
		{Bucket: "b", Endpoint: "https://s3.example.org", UseSSL: true},
		{Bucket: "b", Endpoint: "http://minio:9000/", UseSSL: false},
	}
	for _, cfg := range ok {
		if _, err := NewS3(cfg); err != nil {
			t.Errorf("%+v: %v", cfg, err)
		}
	}
	bad := []config.S3Blob{
		{}, // no bucket
		{Bucket: "b", Endpoint: "https://s3.example.org", UseSSL: false},
		{Bucket: "b", Endpoint: "http://minio:9000", UseSSL: true},
		{Bucket: "b", Endpoint: "ftp://minio:9000", UseSSL: false},
		{Bucket: "b", Endpoint: "https://s3.example.org/bucket", UseSSL: true},
	}
	for _, cfg := range bad {
		if _, err := NewS3(cfg); err == nil {
			t.Errorf("%+v accepted", cfg)
		}
	}
}

// TestS3MissingBucket: a wrong or deleted bucket is a storage failure, not
// a missing blob (judges would report missing test data) nor a successful
// delete (the garbage collector would believe it freed space).
func TestS3MissingBucket(t *testing.T) {
	srv := httptest.NewServer(gofakes3.New(s3mem.New()).Server())
	t.Cleanup(srv.Close)
	s, err := NewS3(config.S3Blob{
		Endpoint: srv.URL, Region: "us-east-1", Bucket: "missing", AccessKey: "a", SecretKey: "s",
	}, WithS3TempDir(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	d := Digest([]byte("test case"))
	if _, err := s.Open(ctx, d); err == nil || errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "bucket") {
		t.Errorf("Open: %v", err)
	}
	if err := s.Delete(ctx, d); err == nil {
		t.Error("Delete in a missing bucket succeeded")
	}
	if _, err := s.PutBytes(ctx, []byte("test case")); err == nil {
		t.Error("PutBytes in a missing bucket succeeded")
	}
	if err := s.Walk(ctx, func(string, int64) error { return nil }); err == nil {
		t.Error("Walk of a missing bucket succeeded")
	}
	if err := s.Check(ctx); err == nil {
		t.Error("Check passed")
	}
	// A missing object in an existing bucket is still ErrNotFound.
	s2, _ := newTestS3(t, "")
	if _, err := s2.Open(ctx, d); !errors.Is(err, ErrNotFound) {
		t.Errorf("Open of a missing object: %v", err)
	}
	if _, err := s2.Stat(ctx, d); !errors.Is(err, ErrNotFound) {
		t.Errorf("Stat of a missing object: %v", err)
	}
	if err := s2.Delete(ctx, d); err != nil {
		t.Errorf("Delete of a missing object: %v", err)
	}
}
