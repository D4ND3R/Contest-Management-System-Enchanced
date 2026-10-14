// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package pkg

import (
	"archive/zip"
	"compress/flate"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
)

// Limits of a zip package. A package is read where it is (nothing is
// extracted), but its content goes to the blob store: these bound what a
// malicious archive can make the server store.
var (
	// MaxArchiveFiles is the largest number of entries of a zip package.
	MaxArchiveFiles = 200000
	// MaxArchiveSize is the largest total uncompressed size of a zip
	// package, as declared by the archive (the reader fails on a member
	// longer than declared).
	MaxArchiveSize int64 = 16 << 30
)

// ErrUnsafeArchive is returned (wrapped) for a zip package with a path
// that could escape its directory when extracted ("zip slip": an absolute
// path, "..", a backslash), a link, or an encrypted member.
var ErrUnsafeArchive = errors.New("unsafe zip archive")

// source is a package: a directory or a zip file. Names are relative to
// the root of the package, with "/" as separator.
type source interface {
	// list returns the regular files and the subdirectories directly in
	// dir ("" is the root), sorted; fs.ErrNotExist if dir does not exist.
	list(dir string) (files, dirs []string, err error)
	// isFile reports whether name is a regular file.
	isFile(name string) bool
	// read returns the content of a small file, failing if it is larger
	// than max bytes.
	read(name string, max int64) ([]byte, error)
	// head returns the first n bytes of a file (fewer if it is shorter).
	head(name string, n int) ([]byte, error)
	// file returns name as a file of the model, read when stored.
	file(name string) *importer.File
	// where names a file of the package in messages.
	where(name string) string
}

// openSource opens the package at p: a directory, or a zip file.
func openSource(p string) (source, error) {
	st, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return dirSource{root: p}, nil
	}
	return openZip(p)
}

// dirSource is a package in a directory.
type dirSource struct{ root string }

func (s dirSource) path(name string) string { return filepath.Join(s.root, filepath.FromSlash(name)) }

func (s dirSource) list(dir string) (files, dirs []string, err error) {
	entries, err := os.ReadDir(s.path(dir))
	if err != nil {
		return nil, nil, err
	}
	for _, e := range entries {
		mode := e.Type()
		if mode&fs.ModeSymlink != 0 {
			st, err := os.Stat(s.path(path.Join(dir, e.Name())))
			if err != nil {
				continue // a broken link: as if missing
			}
			mode = st.Mode()
		}
		switch {
		case mode.IsDir():
			dirs = append(dirs, e.Name())
		case mode.IsRegular():
			files = append(files, e.Name())
		}
	}
	return files, dirs, nil
}

func (s dirSource) isFile(name string) bool {
	st, err := os.Stat(s.path(name))
	return err == nil && st.Mode().IsRegular()
}

func (s dirSource) read(name string, max int64) ([]byte, error) {
	f, err := os.Open(s.path(name))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readMax(f, s.where(name), max)
}

func (s dirSource) head(name string, n int) ([]byte, error) {
	f, err := os.Open(s.path(name))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readHead(f, n)
}

// readHead reads the first n bytes of r.
func readHead(r io.Reader, n int) ([]byte, error) {
	b := make([]byte, n)
	m, err := io.ReadFull(r, b)
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		err = nil
	}
	return b[:m], err
}

func (s dirSource) file(name string) *importer.File { return importer.FileFromPath(s.path(name)) }

func (s dirSource) where(name string) string { return s.path(name) }

// readMax reads at most max bytes.
func readMax(r io.Reader, where string, max int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%s is larger than %d bytes", where, max)
	}
	return data, nil
}

// zipSource is a package in a zip file. The central directory is read
// once; each member is then read straight from the file at its offset
// (so members can be read in parallel and no descriptor stays open
// between reads), checking its size and CRC-32.
type zipSource struct {
	path    string
	root    string // "" or "<dir>/": the package is in the only directory
	entries map[string]*zipEntry
	files   map[string][]string // by directory: the files in it
	dirs    map[string][]string // by directory: its subdirectories
}

type zipEntry struct {
	name         string // in the archive
	offset       int64
	method       uint16
	csize, usize uint64
	crc          uint32
}

// safeZipName reports whether an entry name stays inside the directory it
// is extracted to: relative, without "." or ".." components, backslashes,
// drive letters or NUL bytes.
func safeZipName(name string) bool {
	if name == "" || strings.HasPrefix(name, "/") || strings.ContainsAny(name, "\\\x00") ||
		len(name) >= 2 && name[1] == ':' {
		return false
	}
	for _, part := range strings.Split(strings.TrimSuffix(name, "/"), "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

// ignoredZipEntry: metadata some archivers add (macOS).
func ignoredZipEntry(name string) bool {
	return strings.HasPrefix(name, "__MACOSX/") || path.Base(name) == ".DS_Store"
}

// openZip reads the central directory of a zip package and checks every
// entry before anything is read.
func openZip(p string) (*zipSource, error) {
	r, err := zip.OpenReader(p)
	if err != nil && !errors.Is(err, zip.ErrInsecurePath) {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	if r == nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	defer r.Close()
	if len(r.File) > MaxArchiveFiles {
		return nil, fmt.Errorf("%s: %d entries, more than %d", p, len(r.File), MaxArchiveFiles)
	}
	z := &zipSource{path: p, entries: map[string]*zipEntry{}}
	var total uint64
	names := map[string]bool{}
	for _, f := range r.File {
		name := f.Name
		if ignoredZipEntry(name) {
			continue
		}
		if !safeZipName(name) {
			return nil, fmt.Errorf("%s: %w: entry %q", p, ErrUnsafeArchive, name)
		}
		if f.Mode()&fs.ModeSymlink != 0 {
			return nil, fmt.Errorf("%s: %w: %q is a symbolic link", p, ErrUnsafeArchive, name)
		}
		if f.Flags&0x1 != 0 {
			return nil, fmt.Errorf("%s: %w: %q is encrypted", p, ErrUnsafeArchive, name)
		}
		clean := strings.TrimSuffix(name, "/")
		if names[clean] {
			return nil, fmt.Errorf("%s: %w: %q appears twice", p, ErrUnsafeArchive, clean)
		}
		names[clean] = true
		if strings.HasSuffix(name, "/") || f.Mode().IsDir() {
			continue
		}
		if f.Method != zip.Store && f.Method != zip.Deflate {
			return nil, fmt.Errorf("%s: %q: unsupported compression method %d (use deflate)", p, name, f.Method)
		}
		total += f.UncompressedSize64
		if total > uint64(MaxArchiveSize) {
			return nil, fmt.Errorf("%s: the files add up to more than %d bytes", p, MaxArchiveSize)
		}
		off, err := f.DataOffset()
		if err != nil {
			return nil, fmt.Errorf("%s: %q: %w", p, name, err)
		}
		z.entries[name] = &zipEntry{name: name, offset: off, method: f.Method,
			csize: f.CompressedSize64, usize: f.UncompressedSize64, crc: f.CRC32}
	}
	// A directory entry and a file with the same name are caught above;
	// a file and an implicit directory are caught here.
	for name := range z.entries {
		for d := path.Dir(name); d != "."; d = path.Dir(d) {
			if _, ok := z.entries[d]; ok {
				return nil, fmt.Errorf("%s: %w: %q is both a file and a directory", p, ErrUnsafeArchive, d)
			}
		}
	}
	z.root = zipRoot(z.entries)
	z.index()
	return z, nil
}

// zipRoot finds the package in an archive: problem.yaml at the root, or
// in the only top-level directory.
func zipRoot(entries map[string]*zipEntry) string {
	if _, ok := entries["problem.yaml"]; ok {
		return ""
	}
	top := map[string]bool{}
	for name := range entries {
		first, _, _ := strings.Cut(name, "/")
		top[first] = true
	}
	if len(top) == 1 {
		for d := range top {
			if _, ok := entries[d+"/problem.yaml"]; ok {
				return d + "/"
			}
		}
	}
	return ""
}

// index lists the files and directories under the root.
func (z *zipSource) index() {
	z.files, z.dirs = map[string][]string{}, map[string][]string{}
	seenDir := map[string]bool{}
	for name := range z.entries {
		rel, ok := strings.CutPrefix(name, z.root)
		if !ok {
			continue
		}
		dir, base := path.Split(rel)
		dir = strings.TrimSuffix(dir, "/")
		z.files[dir] = append(z.files[dir], base)
		// Every ancestor is a directory of its parent.
		for d := dir; d != ""; {
			parent, b := path.Split(d)
			parent = strings.TrimSuffix(parent, "/")
			if !seenDir[d] {
				seenDir[d] = true
				z.dirs[parent] = append(z.dirs[parent], b)
			}
			d = parent
		}
	}
	for _, m := range []map[string][]string{z.files, z.dirs} {
		for _, l := range m {
			sort.Strings(l)
		}
	}
}

func (z *zipSource) list(dir string) (files, dirs []string, err error) {
	f, okF := z.files[dir]
	d, okD := z.dirs[dir]
	if !okF && !okD && dir != "" {
		return nil, nil, fs.ErrNotExist
	}
	return f, d, nil
}

func (z *zipSource) isFile(name string) bool {
	_, ok := z.entries[z.root+name]
	return ok
}

func (z *zipSource) read(name string, max int64) ([]byte, error) {
	e, ok := z.entries[z.root+name]
	if !ok {
		return nil, fmt.Errorf("%s: %w", z.where(name), fs.ErrNotExist)
	}
	rc, err := z.open(e)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return readMax(rc, z.where(name), max)
}

func (z *zipSource) head(name string, n int) ([]byte, error) {
	e, ok := z.entries[z.root+name]
	if !ok {
		return nil, fmt.Errorf("%s: %w", z.where(name), fs.ErrNotExist)
	}
	rc, err := z.open(e)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return readHead(rc, n)
}

func (z *zipSource) file(name string) *importer.File {
	e := z.entries[z.root+name]
	return importer.FileFromFunc(z.where(name), func() (io.ReadCloser, error) {
		if e == nil {
			return nil, fmt.Errorf("%s: %w", z.where(name), fs.ErrNotExist)
		}
		return z.open(e)
	})
}

func (z *zipSource) where(name string) string { return z.path + ":" + z.root + name }

// open returns the content of a member, checked against its size and
// CRC-32 when the end is reached.
func (z *zipSource) open(e *zipEntry) (io.ReadCloser, error) {
	f, err := os.Open(z.path)
	if err != nil {
		return nil, err
	}
	m := &zipMember{f: f, crc: crc32.NewIEEE(), entry: e, where: z.path + ":" + e.name}
	sr := io.NewSectionReader(f, e.offset, int64(e.csize))
	m.r = sr
	if e.method == zip.Deflate {
		m.fr = flate.NewReader(sr)
		m.r = m.fr
	}
	return m, nil
}

type zipMember struct {
	f     *os.File
	r     io.Reader
	fr    io.ReadCloser
	crc   hash.Hash32
	n     uint64
	entry *zipEntry
	where string
}

func (m *zipMember) Read(p []byte) (int, error) {
	n, err := m.r.Read(p)
	m.crc.Write(p[:n])
	m.n += uint64(n)
	if m.n > m.entry.usize {
		return n, fmt.Errorf("%s: %w: longer than declared", m.where, zip.ErrFormat)
	}
	if err == io.EOF {
		if m.n != m.entry.usize {
			return n, fmt.Errorf("%s: %w: shorter than declared", m.where, zip.ErrFormat)
		}
		if m.crc.Sum32() != m.entry.crc {
			return n, fmt.Errorf("%s: %w", m.where, zip.ErrChecksum)
		}
	} else if err != nil {
		return n, fmt.Errorf("%s: %w", m.where, err)
	}
	return n, err
}

func (m *zipMember) Close() error {
	if m.fr != nil {
		m.fr.Close()
	}
	return m.f.Close()
}
