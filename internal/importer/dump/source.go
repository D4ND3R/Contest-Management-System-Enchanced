// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package dump

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// A dump of CMS (cmsDumpExporter) is a directory, or a zip or tar archive
// (plain, gzip or bzip2) holding one directory, with:
//
//	contest.json         the objects of the data model (see decode.go)
//	files/<sha1>         the content of every file, named by its SHA-1
//	descriptions/<sha1>  a description of each file (not needed here)
//
// Directories and zip files are read in any order (the files a dump needs
// are read in parallel and the others are never opened); tar archives are
// read once, in order, without extracting them.

// modelName is the name of the file with the objects.
const modelName = "contest.json"

// sha1Name matches the name of a file of the dump.
var sha1Name = regexp.MustCompile(`^[0-9a-f]{40}$`)

// entry is a file of files/.
type entry struct {
	open func() (io.ReadCloser, error)
	size int64
}

// randomSource is a dump whose files can be opened in any order.
type randomSource interface {
	// model opens contest.json.
	model() (io.ReadCloser, error)
	// files lists the files of files/ by name (their SHA-1); others lists
	// the names in files/ that are not digests.
	files() (files map[string]entry, others []string, err error)
	Close() error
}

// streamSource is a dump read once, in order (a tar archive).
type streamSource interface {
	// scan calls model with the content of contest.json and file with
	// each file of files/ (named by its SHA-1, or other for names that are
	// not digests), in the order of the archive. The readers are valid
	// until the callback returns.
	scan(model func(io.Reader) error, file func(name string, r io.Reader) error, other func(name string)) error
	Close() error
}

// ErrNotDump is returned (wrapped) when a path is not a dump of CMS.
var ErrNotDump = errors.New("not a dump of CMS")

// openSource opens a dump: a directory with contest.json (or holding one
// directory with it, an extracted archive), a zip file or a tar archive.
// It returns a randomSource or a streamSource.
func openSource(p string) (any, error) {
	st, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return openDir(p)
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	head := make([]byte, 512)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		f.Close()
		return nil, err
	}
	head = head[:n]
	switch {
	case bytes.HasPrefix(head, []byte("PK\x03\x04")) || bytes.HasPrefix(head, []byte("PK\x05\x06")):
		f.Close()
		return openZip(p)
	case bytes.HasPrefix(head, []byte{0x1f, 0x8b}):
		return &tarSource{path: p, f: f, compression: "gzip"}, nil
	case bytes.HasPrefix(head, []byte("BZh")):
		return &tarSource{path: p, f: f, compression: "bzip2"}, nil
	case len(head) >= 262 && string(head[257:262]) == "ustar":
		return &tarSource{path: p, f: f}, nil
	}
	f.Close()
	return nil, fmt.Errorf("%s: %w (expected a directory, a zip file or a tar, tar.gz or tar.bz2 archive)", p, ErrNotDump)
}

// dirSource is a dump in a directory.
type dirSource struct{ root string }

func openDir(p string) (*dirSource, error) {
	if _, err := os.Stat(filepath.Join(p, modelName)); err == nil {
		return &dirSource{root: p}, nil
	}
	// An extracted archive: the directory holds only the dump directory.
	ents, err := os.ReadDir(p)
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, e := range ents {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	if len(dirs) == 1 {
		sub := filepath.Join(p, dirs[0])
		if _, err := os.Stat(filepath.Join(sub, modelName)); err == nil {
			return &dirSource{root: sub}, nil
		}
	}
	return nil, fmt.Errorf("%s: %w: no %s", p, ErrNotDump, modelName)
}

func (d *dirSource) model() (io.ReadCloser, error) {
	return os.Open(filepath.Join(d.root, modelName))
}

func (d *dirSource) files() (map[string]entry, []string, error) {
	dir := filepath.Join(d.root, "files")
	ents, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]entry{}, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	out := make(map[string]entry, len(ents))
	var others []string
	for _, e := range ents {
		name := e.Name()
		if !e.Type().IsRegular() || !sha1Name.MatchString(name) {
			others = append(others, name)
			continue
		}
		full := filepath.Join(dir, name)
		out[name] = entry{open: func() (io.ReadCloser, error) { return os.Open(full) }, size: -1}
	}
	return out, others, nil
}

func (d *dirSource) Close() error { return nil }

// zipSource is a dump in a zip file.
type zipSource struct {
	zr   *zip.ReadCloser
	root string // "" or "<dir>/"
}

func openZip(p string) (*zipSource, error) {
	zr, err := zip.OpenReader(p)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	root, err := findRoot(zipNames(zr))
	if err != nil {
		zr.Close()
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	return &zipSource{zr: zr, root: root}, nil
}

func zipNames(zr *zip.ReadCloser) []string {
	names := make([]string, len(zr.File))
	for i, f := range zr.File {
		names[i] = f.Name
	}
	return names
}

// findRoot returns the directory of contest.json in an archive ("" or
// "<dir>/"): at the top, or in the one directory of the archive (what
// cmsDumpExporter writes).
func findRoot(names []string) (string, error) {
	var roots []string
	for _, n := range names {
		n = strings.TrimPrefix(n, "./")
		switch {
		case n == modelName:
			roots = append(roots, "")
		case path.Base(n) == modelName && strings.Count(n, "/") == 1:
			roots = append(roots, path.Dir(n)+"/")
		}
	}
	switch len(roots) {
	case 0:
		return "", fmt.Errorf("%w: no %s in the archive", ErrNotDump, modelName)
	case 1:
		return roots[0], nil
	}
	sort.Strings(roots)
	return "", fmt.Errorf("%w: the archive holds several dumps (%s)", ErrNotDump, strings.Join(roots, ", "))
}

func (z *zipSource) member(name string) *zip.File {
	for _, f := range z.zr.File {
		if strings.TrimPrefix(f.Name, "./") == z.root+name {
			return f
		}
	}
	return nil
}

func (z *zipSource) model() (io.ReadCloser, error) {
	f := z.member(modelName)
	if f == nil {
		return nil, fmt.Errorf("%w: no %s", ErrNotDump, modelName)
	}
	return f.Open()
}

func (z *zipSource) files() (map[string]entry, []string, error) {
	out := make(map[string]entry)
	var others []string
	prefix := z.root + "files/"
	for _, f := range z.zr.File {
		n := strings.TrimPrefix(f.Name, "./")
		name, ok := strings.CutPrefix(n, prefix)
		if !ok || name == "" || f.FileInfo().IsDir() {
			continue
		}
		if !sha1Name.MatchString(name) {
			others = append(others, name)
			continue
		}
		out[name] = entry{open: f.Open, size: int64(f.UncompressedSize64)}
	}
	return out, others, nil
}

func (z *zipSource) Close() error { return z.zr.Close() }

// tarSource is a dump in a tar archive, read once.
type tarSource struct {
	path        string
	f           *os.File
	compression string // "", "gzip" or "bzip2"
}

func (t *tarSource) scan(model func(io.Reader) error, file func(string, io.Reader) error, other func(string)) error {
	if _, err := t.f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	var r io.Reader = bufio.NewReaderSize(t.f, 1<<20)
	switch t.compression {
	case "gzip":
		gz, err := gzip.NewReader(r)
		if err != nil {
			return fmt.Errorf("%s: %w", t.path, err)
		}
		defer gz.Close()
		r = gz
	case "bzip2":
		r = bzip2.NewReader(r)
	}
	tr := tar.NewReader(r)
	// The dump is at the top of the archive or in its one directory
	// (cmsDumpExporter writes "<name>/contest.json", "<name>/files/<sha1>").
	root, rootKnown := "", false
	several := fmt.Errorf("%s: %w: the archive holds several dumps", t.path, ErrNotDump)
	seenModel := false
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("%s: %w", t.path, err)
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		n := strings.TrimPrefix(h.Name, "./")
		dir, rest := "", n
		if n != modelName && !strings.HasPrefix(n, "files/") {
			i := strings.IndexByte(n, '/')
			if i < 0 {
				continue
			}
			dir, rest = n[:i+1], n[i+1:]
		}
		name, isFile := strings.CutPrefix(rest, "files/")
		if rest != modelName && (!isFile || name == "" || strings.Contains(name, "/")) {
			continue // descriptions/ and anything else
		}
		if rootKnown && dir != root {
			return several
		}
		root, rootKnown = dir, true
		if rest == modelName {
			if seenModel {
				return several
			}
			seenModel = true
			if err := model(tr); err != nil {
				return err
			}
			continue
		}
		if !sha1Name.MatchString(name) {
			other(name)
			continue
		}
		if err := file(name, tr); err != nil {
			return err
		}
	}
	if !seenModel {
		return fmt.Errorf("%s: %w: no %s in the archive", t.path, ErrNotDump, modelName)
	}
	return nil
}

func (t *tarSource) Close() error { return t.f.Close() }
