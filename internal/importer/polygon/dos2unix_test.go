// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package polygon

import (
	"bytes"
	"errors"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
)

func convert(t *testing.T, content []byte) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	rc, err := unixFile(path).Open()
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	out, err := io.ReadAll(iotestOneByte(rc))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// iotestOneByte reads with small buffers half of the time, to cross every
// boundary.
func iotestOneByte(r io.Reader) io.Reader {
	if rand.IntN(2) == 0 {
		return r
	}
	return readerFunc(func(p []byte) (int, error) {
		if len(p) > 3 {
			p = p[:1+rand.IntN(3)]
		}
		return r.Read(p)
	})
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

// The expected outputs are those of dos2unix 7.5.1 -q.
func TestDos2Unix(t *testing.T) {
	cases := []struct{ in, want string }{
		{"x\r\ny\r\n", "x\ny\n"},
		{"\xef\xbb\xbfx\r\n", "x\n"},
		{"\xef\xbb\xbfx\n", "x\n"},
		{"x\ry\r", "x\ry\r"},
		{"x\r\r\ny", "x\r\ny"},
		{"x\r\n\x0cy\r\n", "x\n\x0cy\n"},
		{"x\r\n\x7fy\r\n", "x\n\x7fy\n"},
		{"x\r\n\ty\r\n", "x\n\ty\n"},
		{"x\r\n\x00y\r\n", "x\r\n\x00y\r\n"}, // binary
		{"x\r\n\x1ay\r\n", "x\r\n\x1ay\r\n"},
		{"\x1b[x\r\n", "\x1b[x\r\n"},
		{"x\r\n\x0by", "x\r\n\x0by"},
		{"x\r\n\x08", "x\r\n\x08"},
		{"plain\n", "plain\n"},
		{"", ""},
		{"\r", "\r"},
		{"\r\n", "\n"},
		{"\xef\xbb", "\xef\xbb"},
		{"caf\xc3\xa9\r\n\xff\r\n", "caf\xc3\xa9\n\xff\n"},
	}
	for _, c := range cases {
		if got := convert(t, []byte(c.in)); string(got) != c.want {
			t.Errorf("%q: got %q, want %q", c.in, got, c.want)
		}
	}
	// Long files: CR LF across the buffers.
	big := bytes.Repeat([]byte("1 2 3\r\n"), 100000)
	if got := convert(t, big); !bytes.Equal(got, bytes.ReplaceAll(big, []byte("\r\n"), []byte("\n"))) {
		t.Error("long file not converted")
	}
	// A file that needs nothing is the file itself.
	path := filepath.Join(t.TempDir(), "f")
	os.WriteFile(path, []byte("ok\n"), 0o644)
	rc, err := openUnix(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rc.(*os.File); !ok {
		t.Errorf("%T, want *os.File", rc)
	}
	rc.Close()
	// A missing file is reported as missing (the writer says so).
	if _, err := unixFile(filepath.Join(t.TempDir(), "none")).Open(); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing file: %v", err)
	}
	f := unixFile(path)
	if f.Path() != "" || f.Name() != path {
		t.Errorf("file %q %q", f.Path(), f.Name())
	}
	var _ *importer.File = f
}

// TestDos2UnixLikeTheProgram compares the conversion with dos2unix on
// random files, when it is installed.
func TestDos2UnixLikeTheProgram(t *testing.T) {
	prog, err := exec.LookPath("dos2unix")
	if err != nil {
		t.Skip("dos2unix is not installed")
	}
	alphabet := []byte("ab \t\r\r\r\n\n\x0c\xef\xbb\xbf\xff")
	dir := t.TempDir()
	for i := 0; i < 300; i++ {
		n := rand.IntN(40)
		if i%50 == 0 {
			n = 70000 + rand.IntN(70000) // more than one buffer
		}
		b := make([]byte, n)
		for j := range b {
			b[j] = alphabet[rand.IntN(len(alphabet))]
		}
		if i%3 == 0 && n > 3 {
			copy(b, utf8BOM)
		}
		if i%17 == 0 && n > 0 {
			b[rand.IntN(n)] = byte(rand.IntN(32))
		}
		want := filepath.Join(dir, "want")
		if err := os.WriteFile(want, b, 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(prog, "-q", want).CombinedOutput(); err != nil {
			t.Fatalf("dos2unix: %v: %s", err, out)
		}
		wb, _ := os.ReadFile(want)
		if got := convert(t, b); !bytes.Equal(got, wb) {
			t.Fatalf("input %q:\n got %q\nwant %q", trim(b), trim(got), trim(wb))
		}
	}
}

func trim(b []byte) string {
	if len(b) > 80 {
		return string(b[:80]) + "..."
	}
	return strings.Clone(string(b))
}
