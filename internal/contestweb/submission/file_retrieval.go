// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/server/contest/submission/file_retrieval.py and
// open_archive (cmscommon/archive.py) of CMS:
// Copyright © 2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2018 Stefano Maggiolo <s.maggiolo@gmail.com>

package submission

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
	"mime/multipart"
	"slices"
	"sort"
	"strings"
)

// ReceivedFile is a file sent by a contestant (CMS ReceivedFile): the
// name of its form field (the element of the submission format it is
// for, "foo.%l"), the name it had on the contestant's computer, and its
// content. "" stands for CMS's None: archive members have no codename
// (and the HTML forms cannot send an empty field name or file name).
type ReceivedFile struct {
	Codename string
	Filename string
	// Size is the length of the content.
	Size int64

	content []byte
	open    func() (io.ReadCloser, error)
}

// NewFile returns a file held in memory.
func NewFile(codename, filename string, content []byte) ReceivedFile {
	return ReceivedFile{Codename: codename, Filename: filename, Size: int64(len(content)), content: content}
}

// Content reads the content of the file (from memory, or from the
// request's temporary file). Callers check Size against their limits
// before reading.
func (f *ReceivedFile) Content() ([]byte, error) {
	if f.content != nil || f.open == nil {
		return f.content, nil
	}
	rc, err := f.open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, f.Size+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) != f.Size {
		return nil, fmt.Errorf("submission: file %q has %d bytes, %d expected", f.Filename, len(b), f.Size)
	}
	f.content = b
	return b, nil
}

// InvalidArchive is returned when the archive sent by a contestant cannot
// be read (CMS InvalidArchive): TooBig when its files exceed the size
// limit once decompressed, TooManyFiles when it has more files than
// allowed.
type InvalidArchive struct {
	TooBig       bool
	TooManyFiles bool
	err          error
}

func (e *InvalidArchive) Error() string {
	switch {
	case e.TooBig:
		return "submission: the archive is too big"
	case e.TooManyFiles:
		return "submission: the archive has too many files"
	case e.err != nil:
		return "submission: invalid archive: " + e.err.Error()
	}
	return "submission: invalid archive"
}

func (e *InvalidArchive) Unwrap() error { return e.err }

// Archive formats are recognized by their content, as CMS's open_archive
// does with Python's tarfile and zipfile: tar (uncompressed, gzip or
// bzip2) first, then zip.
var (
	gzipMagic  = []byte{0x1f, 0x8b}
	bzip2Magic = []byte("BZh")
)

// ExtractFilesFromArchive returns the regular files of an archive (zip,
// tar, tar.gz or tar.bz2) with their base names as file names and no
// codename (CMS extract_files_from_archive): the directory structure is
// ignored, even if two files end up with the same name. maxSize (> 0)
// bounds the total decompressed size and maxFiles (> 0) the number of
// files, both checked on the sizes the archive declares before anything
// is decompressed (zip bombs), and the content is never read past the
// declared size. Any problem is an *InvalidArchive.
func ExtractFilesFromArchive(data []byte, maxSize int64, maxFiles int) ([]ReceivedFile, error) {
	files, err := extractArchive(data, maxSize, maxFiles)
	if err != nil {
		var ia *InvalidArchive
		if errors.As(err, &ia) {
			return nil, ia
		}
		return nil, &InvalidArchive{err: err}
	}
	return files, nil
}

func extractArchive(data []byte, maxSize int64, maxFiles int) ([]ReceivedFile, error) {
	if tr, ok := openTar(data); ok {
		return extractTar(tr, maxSize, maxFiles)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errors.New("not a known archive format")
	}
	return extractZip(zr, maxSize, maxFiles)
}

// limits counts the files of an archive against the limits (in the
// order of CMS: the size first).
type limits struct {
	maxSize  int64
	maxFiles int
	total    int64
	n        int
}

func (l *limits) add(size int64) error {
	if size < 0 {
		return errors.New("negative file size")
	}
	l.total += size
	if l.maxSize > 0 && (l.total > l.maxSize || l.total < 0) {
		return &InvalidArchive{TooBig: true}
	}
	if l.maxFiles > 0 && l.n+1 > l.maxFiles {
		return &InvalidArchive{TooManyFiles: true}
	}
	l.n++
	return nil
}

// openTar recognizes a tar archive, compressed or not (Python's
// tarfile.is_tarfile and tarfile.open with transparent compression): it
// returns a reader positioned at the start when the first header can be
// read.
func openTar(data []byte) (*tar.Reader, bool) {
	newStream := func() (io.Reader, bool) {
		switch {
		case bytes.HasPrefix(data, gzipMagic):
			zr, err := gzip.NewReader(bytes.NewReader(data))
			if err != nil {
				return nil, false
			}
			return zr, true
		case bytes.HasPrefix(data, bzip2Magic):
			return bzip2.NewReader(bytes.NewReader(data)), true
		}
		return bytes.NewReader(data), true
	}
	probe, ok := newStream()
	if !ok {
		return nil, false
	}
	if _, err := tar.NewReader(bufio.NewReader(probe)).Next(); err != nil {
		return nil, false
	}
	r, _ := newStream()
	return tar.NewReader(r), true
}

func extractTar(tr *tar.Reader, maxSize int64, maxFiles int) ([]ReceivedFile, error) {
	l := limits{maxSize: maxSize, maxFiles: maxFiles}
	var out []ReceivedFile
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		// Python's TarInfo.isfile(): regular and contiguous files.
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA && h.Typeflag != tar.TypeCont {
			continue
		}
		if err := l.add(h.Size); err != nil {
			return nil, err
		}
		content, err := readExactly(tr, h.Size)
		if err != nil {
			return nil, err
		}
		out = append(out, NewFile("", baseName(h.Name), content))
	}
}

func extractZip(zr *zip.Reader, maxSize int64, maxFiles int) ([]ReceivedFile, error) {
	l := limits{maxSize: maxSize, maxFiles: maxFiles}
	var out []ReceivedFile
	for _, f := range zr.File {
		// Python's ZipInfo.is_dir(): a name ending with a slash.
		if strings.HasSuffix(f.Name, "/") {
			continue
		}
		size := f.UncompressedSize64
		if size > 1<<62 {
			return nil, &InvalidArchive{TooBig: true}
		}
		if err := l.add(int64(size)); err != nil {
			return nil, err
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		content, err := readExactly(rc, int64(size))
		rc.Close()
		if err != nil {
			return nil, err
		}
		// zipfile cuts names at the first NUL.
		name, _, _ := strings.Cut(f.Name, "\x00")
		out = append(out, NewFile("", baseName(name), content))
	}
	return out, nil
}

// readExactly reads size bytes, never more.
func readExactly(r io.Reader, size int64) ([]byte, error) {
	b := make([]byte, size)
	if _, err := io.ReadFull(r, b); err != nil {
		return nil, err
	}
	return b, nil
}

// baseName is pathlib.PurePosixPath(p).name: the last component, with
// repeated slashes collapsed ("foo//bar" -> "bar", "/foo/bar" -> "bar").
func baseName(p string) string {
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		p = p[i+1:]
	}
	if p == "." {
		return ""
	}
	return p
}

// ArchiveField is the form field of a submission sent as an archive
// (the submission.zip form of the pages).
const ArchiveField = "submission"

// ExtractFilesFromForm returns the files of a multipart form in CMS's
// format (CMS extract_files_from_tornado): one ReceivedFile per file,
// with the field name as codename, or the content of the archive if the
// form holds only one file, in the field "submission". Fields are taken
// in the order given by order (the submission format: the order of the
// fields in the pages), then the others by name, so that the comment
// listing the file names is the one CMS writes. Contents are read only
// when needed (ReceivedFile.Content), except for an archive, read to be
// extracted with ExtractFilesFromArchive's limits.
func ExtractFilesFromForm(form map[string][]*multipart.FileHeader, order []string, maxSize int64, maxFiles int) ([]ReceivedFile, error) {
	fields := make([]string, 0, len(form))
	for k, v := range form {
		if len(v) > 0 {
			fields = append(fields, k)
		}
	}
	if len(fields) == 1 && fields[0] == ArchiveField && len(form[ArchiveField]) == 1 {
		fh := form[ArchiveField][0]
		f := fileFromHeader(ArchiveField, fh)
		data, err := f.Content()
		if err != nil {
			return nil, &InvalidArchive{err: err}
		}
		return ExtractFilesFromArchive(data, maxSize, maxFiles)
	}
	rank := func(k string) int {
		if i := slices.Index(order, k); i >= 0 {
			return i
		}
		return len(order)
	}
	sort.SliceStable(fields, func(i, j int) bool {
		ri, rj := rank(fields[i]), rank(fields[j])
		if ri != rj {
			return ri < rj
		}
		return fields[i] < fields[j]
	})
	var out []ReceivedFile
	for _, k := range fields {
		for _, fh := range form[k] {
			out = append(out, fileFromHeader(k, fh))
		}
	}
	return out, nil
}

func fileFromHeader(field string, fh *multipart.FileHeader) ReceivedFile {
	return ReceivedFile{
		Codename: field,
		// Go keeps only the last element of the name the browser sent
		// (Tornado keeps it whole; browsers send base names anyway).
		Filename: fh.Filename,
		Size:     fh.Size,
		open: func() (io.ReadCloser, error) {
			return fh.Open()
		},
	}
}
