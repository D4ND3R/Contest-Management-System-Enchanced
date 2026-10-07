// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package blob

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
)

// defaultS3Endpoint is used when storage.s3.endpoint is empty.
const defaultS3Endpoint = "s3.amazonaws.com"

// S3 stores blobs in an S3-compatible bucket (AWS, MinIO, Garage, Ceph...)
// under the keys <prefix>ab/<digest>.
type S3 struct {
	mc     *minio.Client
	bucket string
	prefix string
	tmpDir string
}

// S3Option customises NewS3.
type S3Option func(*s3Options)

type s3Options struct {
	transport http.RoundTripper
	tmpDir    string
}

// WithS3Transport sets the HTTP transport (tests, custom CAs).
func WithS3Transport(rt http.RoundTripper) S3Option {
	return func(o *s3Options) { o.transport = rt }
}

// WithS3TempDir sets where uploads larger than a few MiB are spooled while
// their digest is computed (default os.TempDir()).
func WithS3TempDir(dir string) S3Option {
	return func(o *s3Options) { o.tmpDir = dir }
}

// NewS3 returns a store on the bucket of cfg. It does not contact the
// service; Check does. The endpoint is host[:port]; a scheme, if given,
// must agree with use_ssl. A non-empty prefix gets a trailing "/".
func NewS3(cfg config.S3Blob, opts ...S3Option) (*S3, error) {
	var o s3Options
	for _, opt := range opts {
		opt(&o)
	}
	if cfg.Bucket == "" {
		return nil, errors.New("blob: storage.s3.bucket is not set")
	}
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = defaultS3Endpoint
	}
	if strings.Contains(endpoint, "://") {
		u, err := url.Parse(endpoint)
		if err != nil || u.Host == "" || (u.Path != "" && u.Path != "/") {
			return nil, fmt.Errorf("blob: storage.s3.endpoint %q: want host[:port]", cfg.Endpoint)
		}
		if (u.Scheme == "https") != cfg.UseSSL || (u.Scheme != "https" && u.Scheme != "http") {
			return nil, fmt.Errorf("blob: storage.s3.endpoint %q does not agree with storage.s3.use_ssl=%v",
				cfg.Endpoint, cfg.UseSSL)
		}
		endpoint = u.Host
	}
	mc, err := minio.New(endpoint, &minio.Options{
		Creds:     credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure:    cfg.UseSSL,
		Region:    cfg.Region,
		Transport: o.transport,
		// The callers (workers, dispatcher) retry whole jobs; a few quick
		// retries here are enough.
		MaxRetries: 3,
	})
	if err != nil {
		return nil, fmt.Errorf("blob: s3: %w", err)
	}
	prefix := cfg.Prefix
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return &S3{mc: mc, bucket: cfg.Bucket, prefix: prefix, tmpDir: o.tmpDir}, nil
}

// key returns the object key of a (valid) digest.
func (s *S3) key(d string) string { return s.prefix + d[:2] + "/" + d }

// isNotFound reports whether err means the object does not exist. Only
// NoSuchKey counts: other 404s, NoSuchBucket above all (wrong or deleted
// bucket), are storage failures, not missing blobs. A HEAD has no body to
// tell them apart, so minio-go reports NoSuchKey for any 404 on an object
// unless the server sends the code in a header (MinIO does): Stat and the
// existence check of uploads may still see a missing bucket as a missing
// object, which only makes the following upload fail with NoSuchBucket.
func isNotFound(err error) bool {
	return minio.ToErrorResponse(err).Code == minio.NoSuchKey
}

// exists reports whether the object of d is already stored.
func (s *S3) exists(ctx context.Context, d string) (bool, error) {
	_, err := s.mc.StatObject(ctx, s.bucket, s.key(d), minio.StatObjectOptions{})
	switch {
	case err == nil:
		return true, nil
	case isNotFound(err):
		return false, nil
	default:
		return false, fmt.Errorf("blob: s3: %w", err)
	}
}

// upload stores size bytes of r under d unless already there.
func (s *S3) upload(ctx context.Context, d string, r io.Reader, size int64) error {
	if ok, err := s.exists(ctx, d); err != nil || ok {
		return err
	}
	_, err := s.mc.PutObject(ctx, s.bucket, s.key(d), r, size, minio.PutObjectOptions{
		ContentType: "application/octet-stream",
		// Over plain HTTP minio-go signs payloads with aws-chunked
		// streaming, which it sends without Content-Length when the
		// payload is empty; some servers reject that. An empty payload
		// has nothing to protect anyway (and reads verify the digest).
		DisableContentSha256: size == 0,
	})
	if err != nil {
		return fmt.Errorf("blob: s3: %w", err)
	}
	return nil
}

// Put implements Store. The content is spooled (memory or a temporary
// file) to learn its digest, which names the object, before uploading it.
func (s *S3) Put(ctx context.Context, r io.Reader) (string, int64, error) {
	sp, err := spoolReader(ctx, r, s.tmpDir, -1)
	if err != nil {
		return "", 0, err
	}
	defer sp.Close()
	if err := s.upload(ctx, sp.digest, sp.reader(), sp.size); err != nil {
		return "", 0, err
	}
	return sp.digest, sp.size, nil
}

func (s *S3) putExpect(ctx context.Context, want string, r io.Reader) (int64, error) {
	sp, err := spoolReader(ctx, r, s.tmpDir, -1)
	if err != nil {
		return 0, err
	}
	defer sp.Close()
	if sp.digest != want {
		return 0, fmt.Errorf("%w: got %s, want %s", ErrDigestMismatch, sp.digest, want)
	}
	return sp.size, s.upload(ctx, sp.digest, sp.reader(), sp.size)
}

// PutBytes implements Store.
func (s *S3) PutBytes(ctx context.Context, b []byte) (string, error) {
	d := Digest(b)
	if err := s.upload(ctx, d, bytes.NewReader(b), int64(len(b))); err != nil {
		return "", err
	}
	return d, nil
}

// Open implements Store with a single GET (minio.Client.GetObject would
// send a HEAD first): its error body tells a missing object (ErrNotFound)
// from a missing bucket or any other failure.
func (s *S3) Open(ctx context.Context, d string) (io.ReadCloser, error) {
	if err := checkDigest(d); err != nil {
		return nil, err
	}
	body, info, _, err := minio.Core{Client: s.mc}.GetObject(ctx, s.bucket, s.key(d), minio.GetObjectOptions{})
	if err != nil {
		if isNotFound(err) {
			return nil, notFound(d)
		}
		return nil, fmt.Errorf("blob: s3: %w", err)
	}
	return newVerifier(body, d, info.Size), nil
}

// Stat implements Store.
func (s *S3) Stat(ctx context.Context, d string) (int64, error) {
	if err := checkDigest(d); err != nil {
		return 0, err
	}
	info, err := s.mc.StatObject(ctx, s.bucket, s.key(d), minio.StatObjectOptions{})
	if err != nil {
		if isNotFound(err) {
			return 0, notFound(d)
		}
		return 0, fmt.Errorf("blob: s3: %w", err)
	}
	return info.Size, nil
}

// Delete implements Store (S3 deletes are idempotent).
func (s *S3) Delete(ctx context.Context, d string) error {
	if err := checkDigest(d); err != nil {
		return err
	}
	err := s.mc.RemoveObject(ctx, s.bucket, s.key(d), minio.RemoveObjectOptions{})
	if err != nil && !isNotFound(err) {
		return fmt.Errorf("blob: s3: %w", err)
	}
	return nil
}

// Walk implements Store. Objects under the prefix that do not follow the
// ab/<digest> layout are ignored.
func (s *S3) Walk(ctx context.Context, fn func(digest string, size int64) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // stops the listing goroutine if fn fails
	for obj := range s.mc.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{
		Prefix:    s.prefix,
		Recursive: true,
	}) {
		if obj.Err != nil {
			return fmt.Errorf("blob: s3: listing: %w", obj.Err)
		}
		rest := strings.TrimPrefix(obj.Key, s.prefix)
		shard, d, ok := strings.Cut(rest, "/")
		if !ok || !ValidDigest(d) || shard != d[:2] {
			continue
		}
		if err := fn(d, obj.Size); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// Check implements Checker: the bucket must exist and be accessible.
func (s *S3) Check(ctx context.Context) error {
	ok, err := s.mc.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("blob: s3: %w", err)
	}
	if !ok {
		return fmt.Errorf("blob: s3: bucket %q does not exist", s.bucket)
	}
	return nil
}
