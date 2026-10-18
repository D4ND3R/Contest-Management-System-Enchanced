// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package importer

// Packages in archives (new): the loaders read directories, so a contest,
// task or users package given as a zip or tar archive is extracted into a
// temporary directory first. Archives come from anywhere (a download, an
// e-mail): nothing may land outside that directory, and their size is
// bounded.

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Limits of an extracted archive.
var (
	// MaxExtractFiles is the largest number of files and directories.
	MaxExtractFiles = 100000
	// MaxExtractSize is the largest total size of the files, in bytes.
	MaxExtractSize int64 = 8 << 30
)

// ErrUnsafeArchive is returned (wrapped) for an archive with a member
// that would land outside the directory it is extracted to (an absolute
// path, "..", a link pointing out), a device or other special file, or
// over the limits.
var ErrUnsafeArchive = errors.New("unsafe archive")

// archiveSuffixes are the archive formats read, by file name.
var archiveSuffixes = []string{".zip", ".tar", ".tar.gz", ".tgz", ".tar.bz2", ".tbz2"}

// ArchiveFormats lists the archive formats, for messages.
func ArchiveFormats() string { return strings.Join(archiveSuffixes, ", ") }

// IsArchive reports whether the name of p is that of an archive this
// package extracts.
func IsArchive(p string) bool { return archiveSuffix(p) != "" }

func archiveSuffix(p string) string {
	lower := strings.ToLower(p)
	for _, s := range []string{".tar.gz", ".tar.bz2", ".tgz", ".tbz2", ".tar", ".zip"} {
		if strings.HasSuffix(lower, s) {
			return s
		}
	}
	return ""
}

// macOSMetadata is the directory of resource forks the archiver of macOS
// adds to zip files; it is skipped.
const macOSMetadata = "__MACOSX"

// ExtractArchive extracts the archive at src (see IsArchive) into the
// existing directory dir. Links are created last, and only if they point
// inside dir; anything else unsafe is an error wrapping
// ErrUnsafeArchive. On error, dir may hold part of the archive.
func ExtractArchive(src, dir string) error {
	x := &extractor{root: dir}
	var err error
	switch archiveSuffix(src) {
	case ".zip":
		err = x.zip(src)
	case "":
		return fmt.Errorf("%s: not an archive (%s)", src, ArchiveFormats())
	default:
		err = x.tar(src)
	}
	if err != nil {
		return fmt.Errorf("extracting %s: %w", src, err)
	}
	if err := x.links(); err != nil {
		return fmt.Errorf("extracting %s: %w", src, err)
	}
	return nil
}

// extractor writes the members of one archive under root.
type extractor struct {
	root  string
	files int
	size  int64
	// symlinks are created after every file, so that no file is written
	// through one; hardlinks point to files already extracted.
	symlinks []link
}

type link struct{ name, target string }

// memberName returns the cleaned name of a member, "" for one to skip
// (the root itself, the metadata of macOS), or an error if it could land
// outside the root.
func memberName(name string) (string, error) {
	n := strings.TrimSuffix(name, "/")
	for strings.HasPrefix(n, "./") {
		n = n[2:]
	}
	if n == "" || n == "." {
		return "", nil
	}
	if strings.HasPrefix(n, "/") || strings.ContainsAny(n, "\\\x00") || len(n) >= 2 && n[1] == ':' {
		return "", fmt.Errorf("%w: %q", ErrUnsafeArchive, name)
	}
	parts := strings.Split(n, "/")
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return "", fmt.Errorf("%w: %q", ErrUnsafeArchive, name)
		}
	}
	if parts[0] == macOSMetadata {
		return "", nil
	}
	return n, nil
}

func (x *extractor) path(name string) string { return filepath.Join(x.root, filepath.FromSlash(name)) }

// count adds a member to the totals and checks the limits.
func (x *extractor) count(size int64) error {
	x.files++
	if x.files > MaxExtractFiles {
		return fmt.Errorf("%w: more than %d files", ErrUnsafeArchive, MaxExtractFiles)
	}
	if size < 0 {
		return fmt.Errorf("%w: negative size", ErrUnsafeArchive)
	}
	x.size += size
	if x.size > MaxExtractSize {
		return fmt.Errorf("%w: larger than %d bytes", ErrUnsafeArchive, MaxExtractSize)
	}
	return nil
}

func (x *extractor) dir(name string) error {
	if err := x.count(0); err != nil {
		return err
	}
	return os.MkdirAll(x.path(name), 0o755)
}

// file writes a regular file of exactly size bytes read from r.
func (x *extractor) file(name string, size int64, executable bool, r io.Reader) error {
	if err := x.count(size); err != nil {
		return err
	}
	dest := x.path(name)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if executable {
		mode = 0o755
	}
	// O_EXCL: a member given twice is an error rather than an overwrite.
	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(r, size+1))
	if err == nil && n != size {
		err = fmt.Errorf("%w: %q is not %d bytes long as declared", ErrUnsafeArchive, name, size)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// symlink records a symbolic link, created by links.
func (x *extractor) symlink(name, target string) error {
	if err := x.count(0); err != nil {
		return err
	}
	if target == "" || path.IsAbs(target) || strings.ContainsAny(target, "\\\x00") {
		return fmt.Errorf("%w: link %q points to %q", ErrUnsafeArchive, name, target)
	}
	if t := path.Join(path.Dir(name), target); t == ".." || strings.HasPrefix(t, "../") {
		return fmt.Errorf("%w: link %q points outside the archive (%q)", ErrUnsafeArchive, name, target)
	}
	x.symlinks = append(x.symlinks, link{name, target})
	return nil
}

// hardlink makes name another name of the regular file target, already
// extracted.
func (x *extractor) hardlink(name, target string) error {
	t, err := memberName(target)
	if err != nil || t == "" {
		return fmt.Errorf("%w: link %q points to %q", ErrUnsafeArchive, name, target)
	}
	if st, err := os.Lstat(x.path(t)); err != nil || !st.Mode().IsRegular() {
		return fmt.Errorf("%w: link %q points to %q, which is not a file before it", ErrUnsafeArchive, name, target)
	}
	if err := x.count(0); err != nil {
		return err
	}
	dest := x.path(name)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return os.Link(x.path(t), dest)
}

// links creates the symbolic links, then checks where they lead: a link
// through other links can still point outside the root.
func (x *extractor) links() error {
	for _, l := range x.symlinks {
		dest := x.path(l.name)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if err := os.Symlink(filepath.FromSlash(l.target), dest); err != nil {
			return err
		}
	}
	if len(x.symlinks) == 0 {
		return nil
	}
	root, err := filepath.EvalSymlinks(x.root)
	if err != nil {
		return err
	}
	for _, l := range x.symlinks {
		real, err := filepath.EvalSymlinks(x.path(l.name))
		if errors.Is(err, fs.ErrNotExist) {
			continue // a dangling link: as if missing
		}
		if err != nil {
			return fmt.Errorf("%w: link %q: %v", ErrUnsafeArchive, l.name, err)
		}
		if rel, err := filepath.Rel(root, real); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("%w: link %q points outside the archive", ErrUnsafeArchive, l.name)
		}
	}
	return nil
}

func (x *extractor) zip(src string) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer zr.Close()
	if len(zr.File) > MaxExtractFiles {
		return fmt.Errorf("%w: more than %d files", ErrUnsafeArchive, MaxExtractFiles)
	}
	for _, f := range zr.File {
		name, err := memberName(f.Name)
		if err != nil {
			return err
		}
		if name == "" {
			continue
		}
		mode := f.Mode()
		switch {
		case mode.IsDir() || strings.HasSuffix(f.Name, "/"):
			err = x.dir(name)
		case mode&fs.ModeSymlink != 0:
			err = x.zipSymlink(name, f)
		case mode.IsRegular():
			if f.UncompressedSize64 > uint64(MaxExtractSize) {
				return fmt.Errorf("%w: larger than %d bytes", ErrUnsafeArchive, MaxExtractSize)
			}
			var rc io.ReadCloser
			if rc, err = f.Open(); err == nil {
				err = x.file(name, int64(f.UncompressedSize64), mode&0o111 != 0, rc)
				rc.Close()
			}
		default:
			return fmt.Errorf("%w: %q is not a regular file, a directory or a link", ErrUnsafeArchive, f.Name)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (x *extractor) zipSymlink(name string, f *zip.File) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	target, err := io.ReadAll(io.LimitReader(rc, 4096))
	if err != nil {
		return err
	}
	return x.symlink(name, string(target))
}

func (x *extractor) tar(src string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	var r io.Reader = f
	switch archiveSuffix(src) {
	case ".tar.gz", ".tgz":
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gz.Close()
		r = gz
	case ".tar.bz2", ".tbz2":
		r = bzip2.NewReader(f)
	}
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name, err := memberName(h.Name)
		if err != nil {
			return err
		}
		if name == "" {
			continue
		}
		switch h.Typeflag {
		case tar.TypeDir:
			err = x.dir(name)
		case tar.TypeReg:
			err = x.file(name, h.Size, h.Mode&0o111 != 0, tr)
		case tar.TypeSymlink:
			err = x.symlink(name, h.Linkname)
		case tar.TypeLink:
			err = x.hardlink(name, h.Linkname)
		default:
			return fmt.Errorf("%w: %q is not a regular file, a directory or a link", ErrUnsafeArchive, h.Name)
		}
		if err != nil {
			return err
		}
	}
}
