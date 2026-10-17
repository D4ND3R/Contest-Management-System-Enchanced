// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package ctl

// The backup archive of `cms ctl backup` (R9.3, D44): a gzip-compressed tar
// with, in this order,
//
//	manifest.json        what the archive holds (format, versions, tables)
//	db/<table>.copy      each table in COPY text format (columns in the manifest)
//	blobs/<digest>       every file of the blob store
//	config/cms.yaml      the configuration file, if there was one
//	SHA256SUMS           the SHA-256 of every other entry (sha256sum format)
//
// Restoring reads the archive twice: first to verify every checksum, then
// to load it, so a damaged archive never changes anything.

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	archiveFormat   = 1
	manifestName    = "manifest.json"
	sumsName        = "SHA256SUMS"
	configEntryName = "config/cms.yaml"
	dbEntryPrefix   = "db/"
	dbEntrySuffix   = ".copy"
	blobEntryPrefix = "blobs/"
	// BackupPrefix and BackupSuffix name the archives (retention only
	// deletes files named like this).
	BackupPrefix = "cms-backup-"
	BackupSuffix = ".tar.gz"
)

// backupManifest is manifest.json.
type backupManifest struct {
	Format    int       `json:"format"`
	CreatedAt time.Time `json:"created_at"`
	// CMSVersion is the version of the binary that wrote the archive.
	CMSVersion string `json:"cms_version"`
	Hostname   string `json:"hostname"`
	// SchemaVersion is the schema of the database and Migrations the
	// checksums of migrations 1..SchemaVersion: a binary restores the
	// archive only if its own migrations match them.
	SchemaVersion int      `json:"schema_version"`
	Migrations    []string `json:"migrations"`
	// Tables are the tables saved, with the columns of their COPY.
	Tables []backupTable `json:"tables"`
	// Derived are tables not saved because they are computed from the
	// others; restore recomputes them.
	Derived []string `json:"derived"`
	// Blobs tells whether blobs/ holds the blob store; BlobBackend is the
	// storage.backend it was read from.
	Blobs       bool   `json:"blobs"`
	BlobBackend string `json:"blob_backend"`
	// ConfigPath is the configuration file archived ("": none).
	ConfigPath string `json:"config_path"`
}

type backupTable struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
}

// archiveWriter writes entries and remembers their checksums.
type archiveWriter struct {
	gz   *gzip.Writer
	tw   *tar.Writer
	sums []string // "<hex>  <name>"
	now  time.Time
}

func newArchiveWriter(w io.Writer) *archiveWriter {
	gz, _ := gzip.NewWriterLevel(w, gzip.BestSpeed)
	return &archiveWriter{gz: gz, tw: tar.NewWriter(gz), now: time.Now()}
}

// add writes an entry of the given size from r.
func (a *archiveWriter) add(name string, size int64, r io.Reader) error {
	hdr := &tar.Header{Name: name, Mode: 0o600, Size: size, ModTime: a.now, Typeflag: tar.TypeReg, Format: tar.FormatPAX}
	if err := a.tw.WriteHeader(hdr); err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(a.tw, io.TeeReader(r, h))
	if err != nil {
		return fmt.Errorf("archiving %s: %w", name, err)
	}
	if n != size {
		return fmt.Errorf("archiving %s: read %d bytes, expected %d", name, n, size)
	}
	a.sums = append(a.sums, hex.EncodeToString(h.Sum(nil))+"  "+name)
	return nil
}

func (a *archiveWriter) addBytes(name string, b []byte) error {
	return a.add(name, int64(len(b)), strings.NewReader(string(b)))
}

// close writes SHA256SUMS and flushes the archive.
func (a *archiveWriter) close() error {
	sums := strings.Join(a.sums, "\n") + "\n"
	hdr := &tar.Header{Name: sumsName, Mode: 0o600, Size: int64(len(sums)), ModTime: a.now, Typeflag: tar.TypeReg, Format: tar.FormatPAX}
	if err := a.tw.WriteHeader(hdr); err != nil {
		return err
	}
	if _, err := io.WriteString(a.tw, sums); err != nil {
		return err
	}
	if err := a.tw.Close(); err != nil {
		return err
	}
	return a.gz.Close()
}

// archiveEntry is one entry met while reading an archive.
type archiveEntry struct {
	Name string
	Size int64
	R    io.Reader
}

// readArchive calls fn for every regular entry of the archive at path, in
// order. fn may leave part of the entry unread.
func readArchive(file string, fn func(e archiveEntry) error) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(bufio.NewReaderSize(f, 1<<20))
	if err != nil {
		return fmt.Errorf("%s is not a cms backup (gzip): %w", file, err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading %s: %w", file, err)
		}
		if hdr.Typeflag != tar.TypeReg {
			return fmt.Errorf("%s: unexpected entry %q (not a regular file)", file, hdr.Name)
		}
		if name := path.Clean(hdr.Name); name != hdr.Name || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "..") {
			return fmt.Errorf("%s: invalid entry name %q", file, hdr.Name)
		}
		if err := fn(archiveEntry{Name: hdr.Name, Size: hdr.Size, R: tr}); err != nil {
			return err
		}
	}
}

// verifyArchive reads the whole archive, checks every entry against
// SHA256SUMS (which must be the last entry and list every other one) and
// returns the manifest.
func verifyArchive(file string) (*backupManifest, error) {
	got := map[string]string{}
	var order []string
	var sums []byte
	var m *backupManifest
	err := readArchive(file, func(e archiveEntry) error {
		if sums != nil {
			return fmt.Errorf("%s: entry %q after %s", file, e.Name, sumsName)
		}
		if e.Name == sumsName {
			b, err := readAllLimited(e.R, 1<<30)
			sums = b
			if sums == nil {
				sums = []byte{}
			}
			return err
		}
		if _, dup := got[e.Name]; dup {
			return fmt.Errorf("%s: duplicate entry %q", file, e.Name)
		}
		var h hash.Hash = sha256.New()
		var r io.Reader = io.TeeReader(e.R, h)
		if e.Name == manifestName {
			if len(order) != 0 {
				return fmt.Errorf("%s: %s is not the first entry", file, manifestName)
			}
			b, err := readAllLimited(r, 64<<20)
			if err != nil {
				return err
			}
			m = &backupManifest{}
			if err := json.Unmarshal(b, m); err != nil {
				return fmt.Errorf("%s: %s: %w", file, manifestName, err)
			}
		} else if _, err := io.Copy(io.Discard, r); err != nil {
			return fmt.Errorf("%s: %s: %w", file, e.Name, err)
		}
		got[e.Name] = hex.EncodeToString(h.Sum(nil))
		order = append(order, e.Name)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, fmt.Errorf("%s: no %s: not a cms backup", file, manifestName)
	}
	if sums == nil {
		return nil, fmt.Errorf("%s: no %s: the archive is truncated", file, sumsName)
	}
	if m.Format != archiveFormat {
		return nil, fmt.Errorf("%s: archive format %d is not supported (this binary reads %d)", file, m.Format, archiveFormat)
	}
	listed := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(sums)), "\n") {
		sum, name, ok := strings.Cut(line, "  ")
		if !ok {
			return nil, fmt.Errorf("%s: malformed %s line %q", file, sumsName, line)
		}
		have, ok := got[name]
		if !ok {
			return nil, fmt.Errorf("%s: %s is listed in %s but missing", file, name, sumsName)
		}
		if have != sum {
			return nil, fmt.Errorf("%s: checksum mismatch for %s: the archive is damaged", file, name)
		}
		listed[name] = true
	}
	for _, name := range order {
		if !listed[name] {
			return nil, fmt.Errorf("%s: %s is not listed in %s", file, name, sumsName)
		}
	}
	return m, nil
}

// readAllLimited reads r up to limit bytes; more is an error.
func readAllLimited(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("more than %d bytes", limit)
	}
	return b, nil
}

// pruneBackups keeps the newest keep archives of dir (by name, which starts
// with the UTC time) and deletes the others. It returns the deleted names.
func pruneBackups(dir string, keep int) ([]string, error) {
	if keep <= 0 {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if e.Type().IsRegular() && strings.HasPrefix(n, BackupPrefix) && strings.HasSuffix(n, BackupSuffix) {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	if len(names) <= keep {
		return nil, nil
	}
	old := names[:len(names)-keep]
	for _, n := range old {
		if err := os.Remove(filepath.Join(dir, n)); err != nil {
			return nil, err
		}
	}
	return old, nil
}
