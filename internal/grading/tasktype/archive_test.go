// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package tasktype

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/sandbox"
	"golang.org/x/sys/unix"
)

// readArchive lists the entries of an archive (name -> content or link).
func readArchive(t *testing.T, fe *fakeEnv, digest string) map[string]string {
	t.Helper()
	gz, err := gzip.NewReader(strings.NewReader(fe.get(digest)))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	out := map[string]string{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(tr)
		out[h.Name] = string(data) + h.Linkname
	}
}

func TestArchiveAndKeep(t *testing.T) {
	fe := newFakeEnv(t, func(name string, b *fakeBox, spec sandbox.RunSpec) (*sandbox.Result, error) {
		if err := unix.Mkfifo(filepath.Join(b.Dir(), "fifo"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("/etc/passwd", filepath.Join(b.Dir(), "link")); err != nil {
			t.Fatal(err)
		}
		return echoProgram(b, spec, "", "")
	})
	j := batchEvalJob(fe, `["alone", ["", ""], "diff"]`, "42\n", "42\n")
	j.Operation.ArchiveSandbox = true
	j.KeepSandbox = true
	res := fe.mustExecute(j)
	box := fe.box(0)
	if !box.kept || !box.isClosed() {
		t.Errorf("kept %v, closed %v", box.kept, box.isClosed())
	}
	d, ok := res.SandboxDigests[box.Dir()]
	if !ok || len(res.SandboxDigests) != 1 {
		t.Fatalf("archives %v", res.SandboxDigests)
	}
	entries := readArchive(t, fe, d)
	base := filepath.Base(box.Dir())
	var names []string
	for n := range entries {
		names = append(names, n)
	}
	slices.Sort(names)
	want := []string{base + "/", base + "/foo", base + "/input.txt", base + "/link", base + "/output.txt", base + "/stderr.txt"}
	if !slices.Equal(names, want) {
		t.Errorf("entries %q, want %q", names, want)
	}
	if entries[base+"/output.txt"] != "42\n" || entries[base+"/link"] != "/etc/passwd" {
		t.Errorf("entries %q", entries)
	}

	// Without the flags: nothing archived, nothing kept; closing twice
	// is harmless.
	j = batchEvalJob(fe, `["alone", ["", ""], "diff"]`, "42\n", "42\n")
	res = fe.mustExecute(j)
	if res.SandboxDigests != nil || fe.box(1).kept {
		t.Errorf("archived or kept: %v", res.SandboxDigests)
	}
	r := newRun(t.Context(), fe.env, j, res)
	r.closeBox(fe.box(1))
	r.closeBox(fe.box(1))
}
