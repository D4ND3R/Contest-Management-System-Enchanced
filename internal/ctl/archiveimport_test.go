// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package ctl

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
)

// packArchive writes the files of dir into an archive at dst (.zip or
// .tar.gz), under prefix ("" or "name/").
func packArchive(t *testing.T, dir, dst, prefix string) {
	t.Helper()
	out, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	var add func(name string, data []byte) error
	var closeAll func() error
	if strings.HasSuffix(dst, ".zip") {
		zw := zip.NewWriter(out)
		add = func(name string, data []byte) error {
			w, err := zw.Create(name)
			if err != nil {
				return err
			}
			_, err = w.Write(data)
			return err
		}
		closeAll = zw.Close
	} else {
		gz := gzip.NewWriter(out)
		tw := tar.NewWriter(gz)
		add = func(name string, data []byte) error {
			if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
				return err
			}
			_, err := tw.Write(data)
			return err
		}
		closeAll = func() error {
			if err := tw.Close(); err != nil {
				return err
			}
			return gz.Close()
		}
	}
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return add(prefix+filepath.ToSlash(rel), data)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := closeAll(); err != nil {
		t.Fatal(err)
	}
}

// TestOpenArchives: a contest given as a zip or tar.gz, with its files at
// the root or inside one directory, is detected and extracted, and the
// extraction is removed on Close.
func TestOpenArchives(t *testing.T) {
	demo := filepath.Join("..", "..", "testdata", "contests", "demo")
	for _, tc := range []struct{ name, prefix string }{
		{"flat.zip", ""}, {"nested.zip", "D4OI2026/"}, {"flat.tar.gz", ""}, {"nested.tgz", "demo/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			arc := filepath.Join(t.TempDir(), tc.name)
			packArchive(t, demo, arc, tc.prefix)
			src, err := importer.Default.Open(importer.KindContest, "", arc)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			if src.Loader.Name() != "italy_yaml" {
				t.Errorf("loader %s", src.Loader.Name())
			}
			if _, err := os.Stat(filepath.Join(src.Path, "contest.yaml")); err != nil {
				t.Errorf("contest.yaml not at the package root: %v", err)
			}
			path := src.Path
			if err := src.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("the extraction was not removed: %v", err)
			}
		})
	}
}

// TestOpenArchiveRejectsEscapes: members that would land outside the
// extraction directory are refused, and nothing is left behind.
func TestOpenArchiveRejectsEscapes(t *testing.T) {
	dir := t.TempDir()
	arc := filepath.Join(dir, "evil.zip")
	f, err := os.Create(arc)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("../../outside.txt")
	_, _ = io.WriteString(w, "x")
	_ = zw.Close()
	_ = f.Close()
	if src, err := importer.Default.Open(importer.KindContest, "", arc); err == nil {
		src.Close()
		t.Fatal("an archive escaping its directory was accepted")
	}
	if _, err := os.Stat(filepath.Join(dir, "..", "outside.txt")); !os.IsNotExist(err) {
		t.Fatal("a file was written outside the extraction directory")
	}
}

// TestOpenExplainsWhatIsExpected: a directory that is no package says what
// each loader looks for.
func TestOpenExplainsWhatIsExpected(t *testing.T) {
	_, err := importer.Default.Open(importer.KindContest, "", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "contest.yaml") || !strings.Contains(err.Error(), "contest.xml") {
		t.Fatalf("error %v does not say what is expected", err)
	}
}
