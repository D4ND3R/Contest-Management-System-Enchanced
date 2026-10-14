// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package italy

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
)

// demoDir is the demo contest of the fixtures (testdata/contests/demo).
const demoDir = "../../../testdata/contests/demo"

// copyDemo copies the demo contest to a temporary directory. The
// comparator of "half" is compiled by build.sh and not committed: a
// placeholder stands for it when it is missing (real reports whether the
// compiled one is there).
func copyDemo(t testing.TB) (dir string, real bool) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), "demo")
	err := filepath.WalkDir(demoDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(demoDir, p)
		dst := filepath.Join(dir, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	checker := filepath.Join(dir, "half", "check", "checker")
	if _, err := os.Stat(checker); err == nil {
		return dir, true
	}
	if err := os.WriteFile(checker, []byte("#!/bin/sh\necho 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, false
}

// writeFiles writes files (path relative to dir: content) and returns dir.
func writeFiles(t testing.TB, dir string, files map[string]string) string {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// sha1Of returns the SHA-1 of a file of the model (the digests of CMS).
func sha1Of(t testing.TB, f *importer.File) string {
	t.Helper()
	if f == nil {
		t.Fatal("no file")
	}
	rc, err := f.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	h := sha1.New()
	if _, err := io.Copy(h, rc); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// sameJSON compares two JSON texts by value.
func sameJSON(t testing.TB, got json.RawMessage, want string) bool {
	t.Helper()
	var a, b any
	if err := json.Unmarshal(got, &a); err != nil {
		t.Fatalf("invalid JSON %s: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &b); err != nil {
		t.Fatalf("invalid JSON %s: %v", want, err)
	}
	return reflect.DeepEqual(a, b)
}

// load returns the options of a load and its report.
func load() (*importer.Options, *importer.Report) {
	rep := &importer.Report{}
	return &importer.Options{Report: rep}, rep
}

// issues returns the issues of a report, one per line.
func issues(rep *importer.Report) string {
	var b strings.Builder
	_, _ = rep.WriteTo(&b)
	return b.String()
}

// hasIssue reports whether the report has an issue of that severity
// containing text.
func hasIssue(rep *importer.Report, s importer.Severity, text string) bool {
	for _, i := range rep.Issues() {
		if i.Severity == s && strings.Contains(i.Message, text) {
			return true
		}
	}
	return false
}

// loadTask loads the task at dir and fails on an error of the loader.
func loadTask(t testing.TB, dir string) (*importer.Task, *importer.Report) {
	t.Helper()
	opts, rep := load()
	tk, err := Loader{}.LoadTask(dir, opts)
	if err != nil {
		t.Fatal(err)
	}
	return tk, rep
}

// managers returns the file names of the managers of a dataset.
func managers(d *importer.Dataset) []string {
	out := []string{}
	for _, m := range d.Managers {
		out = append(out, m.Filename)
	}
	return out
}
