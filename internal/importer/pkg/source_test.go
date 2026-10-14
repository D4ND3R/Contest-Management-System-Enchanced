// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package pkg

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
)

// smallPackage is a valid package without a compiled program.
func smallPackage() map[string]string {
	return with(tests(3), map[string]string{
		"problem.yaml":           "name: z\ntitle: Zip\ntime_limit: 1\nmemory_limit: 64\npublic_tests: 1\nscoring:\n  type: GroupMin\n  subtasks:\n    - {points: 50, tests: 1}\n    - {points: 50, tests: 2-3}\n",
		"statement/en.pdf":       pdf,
		"attachments/sample.txt": "sample",
		"graders/grader.cpp":     "grader",
		"graders/z.h":            "header",
		"solutions/ok.ac.cpp":    "ok",
	})
}

func TestZipPackage(t *testing.T) {
	files := smallPackage()
	want, rep := loadValid(t, pkgDir(t, files))
	noIssues(t, rep)
	for _, prefix := range []string{"", "z/"} {
		entries := zipOf(prefix, files)
		// Directory entries, metadata of macOS and hidden files are
		// skipped.
		entries = append(entries,
			testEntry{name: prefix + "tests/"},
			testEntry{name: "__MACOSX/" + prefix + "._problem.yaml", content: "junk"},
			testEntry{name: prefix + ".DS_Store", content: "junk"},
			testEntry{name: prefix + "tests/.gitkeep"})
		p := writeZip(t, entries)
		if !Detect(p) {
			t.Fatalf("%q: not detected", prefix)
		}
		if l, err := importer.Default.ChooseTask("", p); err != nil || l.Name() != Name {
			t.Errorf("%q: autodetection %v %v", prefix, l, err)
		}
		got, rep := loadValid(t, p)
		noIssues(t, rep)
		sameTask(t, "zip "+prefix, canonical(t, got, nil), canonical(t, want, nil))
		// Files are read from the archive, and name where they are.
		if n := got.Datasets[0].Testcases[0].Input.Name(); n != p+":"+prefix+"tests/01.in" {
			t.Errorf("%q: name %q", prefix, n)
		}
	}
	// Stored members (no compression) too.
	var stored []testEntry
	for _, e := range zipOf("", files) {
		e.method = zip.Store
		stored = append(stored, e)
	}
	got, _ := loadValid(t, writeZip(t, stored))
	sameTask(t, "stored zip", canonical(t, got, nil), canonical(t, want, nil))
}

func TestZipWithoutPackage(t *testing.T) {
	// Two top-level directories: where is the package?
	entries := append(zipOf("a/", smallPackage()), testEntry{name: "b/readme.txt", content: "x"})
	if Detect(writeZip(t, entries)) {
		t.Error("detected a zip without problem.yaml at its root")
	}
	_, err := Load(writeZip(t, entries), nil)
	if err == nil || !strings.Contains(err.Error(), "file missing") {
		t.Errorf("error %v", err)
	}
	notZip := pkgDir(t, map[string]string{"task.zip": "not a zip"})
	if Detect(notZip + "/task.zip") {
		t.Error("detected a file that is not a zip")
	}
	if _, err := Load(notZip+"/task.zip", nil); err == nil {
		t.Error("no error for a file that is not a zip")
	}
}

func TestZipSlip(t *testing.T) {
	good := zipOf("", smallPackage())
	for _, c := range []struct {
		name  string
		entry testEntry
		err   string
	}{
		{"parent", testEntry{name: "../evil.txt", content: "x"}, `entry "../evil.txt"`},
		{"nested parent", testEntry{name: "tests/../../evil.txt", content: "x"}, `entry "tests/../../evil.txt"`},
		{"inner parent", testEntry{name: "tests/../problem2.yaml", content: "x"}, `entry "tests/../problem2.yaml"`},
		{"absolute", testEntry{name: "/etc/evil", content: "x"}, `entry "/etc/evil"`},
		{"backslash", testEntry{name: "tests\\..\\..\\evil", content: "x"}, "entry"},
		{"drive", testEntry{name: "C:/evil", content: "x"}, `entry "C:/evil"`},
		{"dot", testEntry{name: "./tests/x.in", content: "x"}, `entry "./tests/x.in"`},
		{"empty component", testEntry{name: "tests//x.in", content: "x"}, `entry "tests//x.in"`},
		{"symlink", testEntry{name: "tests/link.in", content: "/etc/passwd", mode: fs.ModeSymlink | 0o777}, `"tests/link.in" is a symbolic link`},
		{"duplicate", testEntry{name: "tests/01.in", content: "again"}, `"tests/01.in" appears twice`},
		{"file and directory", testEntry{name: "tests/01.in/x", content: "x"}, `"tests/01.in" is both a file and a directory`},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := writeZip(t, append(slices.Clone(good), c.entry))
			// Detected (no other format reads zips) so that the error is
			// shown.
			if !Detect(p) {
				t.Error("not detected")
			}
			_, err := Load(p, nil)
			if !errors.Is(err, ErrUnsafeArchive) || !strings.Contains(err.Error(), c.err) {
				t.Errorf("error %v, want %q", err, c.err)
			}
		})
	}
}

func TestZipLimits(t *testing.T) {
	entries := zipOf("", smallPackage())
	p := writeZip(t, entries)

	oldFiles, oldSize := MaxArchiveFiles, MaxArchiveSize
	defer func() { MaxArchiveFiles, MaxArchiveSize = oldFiles, oldSize }()
	MaxArchiveFiles = len(entries) - 1
	if _, err := Load(p, nil); err == nil || !strings.Contains(err.Error(), "entries, more than") {
		t.Errorf("too many files: %v", err)
	}
	MaxArchiveFiles, MaxArchiveSize = oldFiles, 100
	if _, err := Load(p, nil); err == nil || !strings.Contains(err.Error(), "add up to more than 100 bytes") {
		t.Errorf("too large: %v", err)
	}
	MaxArchiveSize = oldSize

	// Encrypted members and unknown compression methods.
	enc := writeZip(t, append(slices.Clone(entries), testEntry{name: "secret.txt", content: "x"}))
	setFlag(t, enc, "secret.txt", 0x1)
	if _, err := Load(enc, nil); !errors.Is(err, ErrUnsafeArchive) || !strings.Contains(err.Error(), "encrypted") {
		t.Errorf("encrypted: %v", err)
	}
	other := writeZipMethod(t, append(slices.Clone(entries), testEntry{name: "odd.txt", content: "x", method: 99}))
	if _, err := Load(other, nil); err == nil || !strings.Contains(err.Error(), "unsupported compression method 99") {
		t.Errorf("method: %v", err)
	}
}

// TestZipCorrupt checks that a member is checked against its size and
// CRC-32 when read (a zip bomb declares a small size).
func TestZipCorrupt(t *testing.T) {
	read := func(p, name string) error {
		pk, err := Load(p, nil)
		if err != nil {
			return err
		}
		for _, tc := range pk.Task.Datasets[0].Testcases {
			if tc.Codename == name {
				rc, err := tc.Input.Open()
				if err != nil {
					return err
				}
				defer rc.Close()
				_, err = io.ReadAll(rc)
				return err
			}
		}
		t.Fatalf("no test %s", name)
		return nil
	}
	stored := func() string {
		var entries []testEntry
		for _, e := range zipOf("", smallPackage()) {
			if e.name == "tests/02.in" {
				e.content, e.method = "0123456789abcdef", zip.Store
			}
			entries = append(entries, e)
		}
		return writeZip(t, entries)
	}

	p := stored()
	if err := read(p, "02"); err != nil {
		t.Fatalf("intact: %v", err)
	}
	data, _ := os.ReadFile(p)
	i := bytes.Index(data, []byte("0123456789abcdef"))
	data[i+3] = 'X'
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := read(p, "02"); !errors.Is(err, zip.ErrChecksum) {
		t.Errorf("changed content: %v", err)
	}

	p = stored()
	patchCentral(t, p, "tests/02.in", func(h []byte) { binary.LittleEndian.PutUint32(h[24:], 8) })
	if err := read(p, "02"); !errors.Is(err, zip.ErrFormat) || !strings.Contains(err.Error(), "longer than declared") {
		t.Errorf("longer: %v", err)
	}
	p = stored()
	patchCentral(t, p, "tests/02.in", func(h []byte) { binary.LittleEndian.PutUint32(h[24:], 32) })
	if err := read(p, "02"); !errors.Is(err, zip.ErrFormat) || !strings.Contains(err.Error(), "shorter than declared") {
		t.Errorf("shorter: %v", err)
	}
}

// patchCentral changes the central directory header of a member.
func patchCentral(t testing.TB, p, name string, patch func(header []byte)) {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	sig := []byte{0x50, 0x4b, 0x01, 0x02}
	for i := 0; ; {
		j := bytes.Index(data[i:], sig)
		if j < 0 {
			t.Fatalf("%s: no central header for %s", p, name)
		}
		h := data[i+j:]
		n := int(binary.LittleEndian.Uint16(h[28:]))
		if string(h[46:46+n]) == name {
			patch(h)
			break
		}
		i += j + 4
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// setFlag sets a general purpose flag of a member (central directory).
func setFlag(t testing.TB, p, name string, flag uint16) {
	patchCentral(t, p, name, func(h []byte) {
		binary.LittleEndian.PutUint16(h[8:], binary.LittleEndian.Uint16(h[8:])|flag)
	})
}

// writeZipMethod is writeZip with a compressor registered for the
// unknown method 99 (deflate under another number).
func writeZipMethod(t testing.TB, entries []testEntry) string {
	t.Helper()
	p := t.TempDir() + "/task.zip"
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	zw.RegisterCompressor(99, func(w io.Writer) (io.WriteCloser, error) { return flate.NewWriter(w, 5) })
	for _, e := range entries {
		m := e.method
		if m == 0 {
			m = zip.Deflate
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: e.name, Method: m})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, e.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSafeZipName(t *testing.T) {
	for name, want := range map[string]bool{
		"a": true, "a/b.txt": true, "a/b/": true, "a..b": true, "..a": true,
		"": false, "/a": false, "../a": false, "a/..": false, "a/../b": false, "a\\b": false,
		"C:/a": false, "C:": false, "a//b": false, "./a": false, "a/./b": false, "a\x00b": false,
	} {
		if got := safeZipName(name); got != want {
			t.Errorf("safeZipName(%q) = %v", name, got)
		}
	}
}

func TestDirSourceLinks(t *testing.T) {
	dir := pkgDir(t, smallPackage())
	// A link to a file counts as the file; a broken link as missing.
	if err := os.Symlink(dir+"/tests/01.in", dir+"/tests/04.in"); err != nil {
		t.Skip(err)
	}
	if err := os.Symlink(dir+"/tests/01.out", dir+"/tests/04.out"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dir+"/missing", dir+"/tests/05.in"); err != nil {
		t.Fatal(err)
	}
	pk, rep := load(t, dir)
	if got := testcaseNames(pk.Task.Datasets[0]); !slices.Equal(got, []string{"01*", "02", "03", "04"}) {
		t.Errorf("testcases %v\n%s", got, issues(rep))
	}
}
