// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package blob

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
)

// invalidDigests must be rejected by every backend before any I/O.
var invalidDigests = []string{
	"",
	"../../../../etc/passwd",
	"../" + strings.Repeat("a", 61),
	strings.Repeat("a", 62) + "/.",
	"ab/cd/" + strings.Repeat("a", 58),
	strings.Repeat("A", 64), // uppercase
	"E3B0C44298FC1C149AFBF4C8996FB92427AE41E4649B934CA495991B7852B855",
	strings.Repeat("a", 63),
	strings.Repeat("a", 65),
	strings.Repeat("g", 64),
	strings.Repeat("a", 63) + "\x00",
	strings.Repeat("a", 32) + "%2f" + strings.Repeat("a", 29),
	"da39a3ee5e6b4b0d3255bfef95601890afd80709", // SHA-1 (CMS digests)
}

var digestRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

func TestValidDigest(t *testing.T) {
	for _, d := range invalidDigests {
		if ValidDigest(d) {
			t.Errorf("ValidDigest(%q) = true", d)
		}
	}
	for _, d := range []string{Digest(nil), Digest([]byte("x")), strings.Repeat("0", 64), strings.Repeat("f", 64)} {
		if !ValidDigest(d) {
			t.Errorf("ValidDigest(%q) = false", d)
		}
	}
	if Digest(nil) != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Errorf("Digest(nil) = %s", Digest(nil))
	}
}

// FuzzValidDigest checks that the hand-written loop is the regexp.
func FuzzValidDigest(f *testing.F) {
	for _, d := range invalidDigests {
		f.Add(d)
	}
	f.Add(Digest(nil))
	f.Fuzz(func(t *testing.T, d string) {
		if got, want := ValidDigest(d), digestRE.MatchString(d); got != want {
			t.Fatalf("ValidDigest(%q) = %v, regexp says %v", d, got, want)
		}
	})
}

func randomBytes(t testing.TB, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

func readAll(t testing.TB, s Store, d string) []byte {
	t.Helper()
	rc, err := s.Open(context.Background(), d)
	if err != nil {
		t.Fatalf("Open(%s): %v", d, err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("reading %s: %v", d, err)
	}
	return b
}

func walkAll(t testing.TB, s Store) map[string]int64 {
	t.Helper()
	got := map[string]int64{}
	if err := s.Walk(context.Background(), func(d string, size int64) error {
		got[d] = size
		return nil
	}); err != nil {
		t.Fatalf("Walk: %v", err)
	}
	return got
}

// storeOptions describe what a backend under test supports.
type storeOptions struct {
	noDelete, noWalk bool
}

// testStore is the conformance suite every backend passes.
func testStore(t *testing.T, s Store, opt storeOptions) {
	ctx := context.Background()

	t.Run("RoundTrip", func(t *testing.T) {
		for _, n := range []int{0, 1, 1000, 300 << 10, spoolMemLimit + 12345} {
			data := randomBytes(t, n)
			// iotest.HalfReader exercises short reads.
			d, size, err := s.Put(ctx, iotest.HalfReader(bytes.NewReader(data)))
			if err != nil {
				t.Fatalf("Put(%d bytes): %v", n, err)
			}
			if d != Digest(data) || size != int64(n) {
				t.Fatalf("Put(%d bytes) = %s, %d; want %s", n, d, size, Digest(data))
			}
			if got := readAll(t, s, d); !bytes.Equal(got, data) {
				t.Fatalf("content of %d bytes differs after a round trip", n)
			}
			if got, err := s.Stat(ctx, d); err != nil || got != int64(n) {
				t.Fatalf("Stat = %d, %v; want %d", got, err, n)
			}
			// Again with PutBytes: same digest, nothing breaks.
			d2, err := s.PutBytes(ctx, data)
			if err != nil || d2 != d {
				t.Fatalf("PutBytes = %s, %v; want %s", d2, err, d)
			}
		}
	})

	t.Run("PutBytes", func(t *testing.T) {
		data := []byte("int main() { return 0; }\n")
		d, err := s.PutBytes(ctx, data)
		if err != nil || d != Digest(data) {
			t.Fatalf("PutBytes = %s, %v", d, err)
		}
		if got := readAll(t, s, d); !bytes.Equal(got, data) {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		d := Digest([]byte("never stored " + t.Name()))
		if _, err := s.Open(ctx, d); !errors.Is(err, ErrNotFound) {
			t.Errorf("Open: %v, want ErrNotFound", err)
		}
		if _, err := s.Stat(ctx, d); !errors.Is(err, ErrNotFound) {
			t.Errorf("Stat: %v, want ErrNotFound", err)
		}
	})

	t.Run("InvalidDigests", func(t *testing.T) {
		for _, d := range invalidDigests {
			if _, err := s.Open(ctx, d); !errors.Is(err, ErrInvalidDigest) {
				t.Errorf("Open(%q): %v", d, err)
			}
			if _, err := s.Stat(ctx, d); !errors.Is(err, ErrInvalidDigest) {
				t.Errorf("Stat(%q): %v", d, err)
			}
			if err := s.Delete(ctx, d); !opt.noDelete && !errors.Is(err, ErrInvalidDigest) {
				t.Errorf("Delete(%q): %v", d, err)
			}
		}
	})

	t.Run("ConcurrentSameContent", func(t *testing.T) {
		data := randomBytes(t, 100<<10)
		var wg sync.WaitGroup
		errs := make(chan error, 32)
		for i := 0; i < 32; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				var d string
				var err error
				if i%2 == 0 {
					d, _, err = s.Put(ctx, bytes.NewReader(data))
				} else {
					d, err = s.PutBytes(ctx, data)
				}
				if err == nil && d != Digest(data) {
					err = fmt.Errorf("digest %s", d)
				}
				errs <- err
			}(i)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		if got := readAll(t, s, Digest(data)); !bytes.Equal(got, data) {
			t.Fatal("content differs after concurrent puts")
		}
	})

	t.Run("ConcurrentDifferentContent", func(t *testing.T) {
		const n = 32
		blobs := make([][]byte, n)
		for i := range blobs {
			blobs[i] = randomBytes(t, 1000+i*517)
		}
		var wg sync.WaitGroup
		errs := make(chan error, n)
		for i := range blobs {
			wg.Add(1)
			go func(b []byte) {
				defer wg.Done()
				d, _, err := s.Put(ctx, bytes.NewReader(b))
				if err == nil && d != Digest(b) {
					err = fmt.Errorf("digest %s", d)
				}
				if err == nil {
					// Read back while others write.
					var rc io.ReadCloser
					if rc, err = s.Open(ctx, d); err == nil {
						var got []byte
						got, err = io.ReadAll(rc)
						rc.Close()
						if err == nil && !bytes.Equal(got, b) {
							err = errors.New("content differs")
						}
					}
				}
				errs <- err
			}(blobs[i])
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
	})

	t.Run("DeleteIdempotent", func(t *testing.T) {
		if opt.noDelete {
			if err := s.Delete(ctx, Digest(nil)); !errors.Is(err, ErrUnsupported) {
				t.Fatalf("Delete: %v, want ErrUnsupported", err)
			}
			return
		}
		d, err := s.PutBytes(ctx, []byte("to be deleted "+t.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			if err := s.Delete(ctx, d); err != nil {
				t.Fatalf("Delete #%d: %v", i+1, err)
			}
		}
		if _, err := s.Stat(ctx, d); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Stat after Delete: %v", err)
		}
		if _, err := s.Open(ctx, d); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Open after Delete: %v", err)
		}
		// Never stored at all.
		if err := s.Delete(ctx, Digest([]byte("never "+t.Name()))); err != nil {
			t.Fatalf("Delete of a missing blob: %v", err)
		}
	})

	t.Run("Walk", func(t *testing.T) {
		if opt.noWalk {
			if err := s.Walk(ctx, func(string, int64) error { return nil }); !errors.Is(err, ErrUnsupported) {
				t.Fatalf("Walk: %v, want ErrUnsupported", err)
			}
			return
		}
		want := map[string]int64{}
		for i := 0; i < 10; i++ {
			b := []byte(strings.Repeat("w", i) + t.Name())
			d, err := s.PutBytes(ctx, b)
			if err != nil {
				t.Fatal(err)
			}
			want[d] = int64(len(b))
		}
		got := walkAll(t, s)
		for d, size := range want {
			if got[d] != size {
				t.Errorf("Walk: %s has size %d, want %d (present: %v)", d, got[d], size, hasKey(got, d))
			}
		}
		for d, size := range got {
			if !ValidDigest(d) {
				t.Errorf("Walk reported %q", d)
			}
			if st, err := s.Stat(ctx, d); err != nil || st != size {
				t.Errorf("Walk said %s has %d bytes, Stat says %d, %v", d, size, st, err)
			}
		}
		// fn errors stop the walk and are returned.
		stop := errors.New("stop")
		calls := 0
		err := s.Walk(ctx, func(string, int64) error { calls++; return stop })
		if !errors.Is(err, stop) || calls != 1 {
			t.Errorf("Walk with a failing fn: %v after %d calls", err, calls)
		}
	})
}

func hasKey(m map[string]int64, k string) bool { _, ok := m[k]; return ok }

func sortedKeys(m map[string]int64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// expectCorrupt reads d from s and requires ErrCorrupt.
func expectCorrupt(t *testing.T, s Store, d string) {
	t.Helper()
	rc, err := s.Open(context.Background(), d)
	if err != nil {
		if errors.Is(err, ErrCorrupt) {
			return
		}
		t.Fatalf("Open: %v", err)
	}
	defer rc.Close()
	if _, err := io.ReadAll(rc); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("reading a corrupt blob: %v, want ErrCorrupt", err)
	}
}

func TestPutExpect(t *testing.T) {
	ctx := context.Background()
	for name, mk := range map[string]func(t *testing.T) Store{
		"local": func(t *testing.T) Store { return newTestLocal(t) },
		// A store without its own putExpect uses the generic spool.
		"generic": func(t *testing.T) Store { return plainStore{newTestLocal(t)} },
	} {
		t.Run(name, func(t *testing.T) {
			s := mk(t)
			data := []byte("expected content")
			if _, err := putExpect(ctx, s, Digest([]byte("other")), bytes.NewReader(data)); !errors.Is(err, ErrDigestMismatch) {
				t.Fatalf("mismatch: %v", err)
			}
			if got := walkAll(t, s); len(got) != 0 {
				t.Fatalf("a mismatching upload was stored: %v", sortedKeys(got))
			}
			n, err := putExpect(ctx, s, Digest(data), bytes.NewReader(data))
			if err != nil || n != int64(len(data)) {
				t.Fatalf("putExpect = %d, %v", n, err)
			}
			if _, err := putExpect(ctx, s, "../x", bytes.NewReader(data)); !errors.Is(err, ErrInvalidDigest) {
				t.Fatalf("invalid digest: %v", err)
			}
		})
	}
}

// plainStore hides the optional interfaces of a Store.
type plainStore struct{ Store }

func TestSpoolLimits(t *testing.T) {
	ctx := context.Background()
	data := randomBytes(t, spoolMemLimit+10)
	sp, err := spoolReader(ctx, bytes.NewReader(data), t.TempDir(), -1)
	if err != nil {
		t.Fatal(err)
	}
	defer sp.Close()
	if sp.file == nil || sp.size != int64(len(data)) || sp.digest != Digest(data) {
		t.Fatalf("large spool: file=%v size=%d", sp.file != nil, sp.size)
	}
	// Readers can be taken several times (retries).
	for i := 0; i < 2; i++ {
		got, err := io.ReadAll(sp.reader())
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("reader #%d: %v", i, err)
		}
	}
	if _, err := spoolReader(ctx, bytes.NewReader(data), t.TempDir(), 100); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("limit: %v", err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := spoolReader(cctx, bytes.NewReader(data), t.TempDir(), -1); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
}
