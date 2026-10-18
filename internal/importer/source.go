// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package importer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Source is a package opened for a loader: a directory as given, a file
// a loader reads itself (the zip of the problem_yaml format), or an
// archive extracted into a temporary directory. Close removes that
// directory.
type Source struct {
	// Path is what the loader reads.
	Path string
	// Loader is the loader of the package.
	Loader Loader
	// Archive is the archive Path was extracted from ("" if none).
	Archive string
	tmp     string
}

// Close removes the extracted files, if any.
func (s *Source) Close() error {
	if s == nil || s.tmp == "" {
		return nil
	}
	err := os.RemoveAll(s.tmp)
	s.tmp = ""
	return err
}

// Open opens the package at p as kind with the loader called name (""
// detects it, see Choose). p is a directory, a file the loader reads
// (detected by it, or the only one given with -L), or an archive (see
// IsArchive), which is extracted into a temporary directory under
// $TMPDIR: if its root is not a package but holds a single directory
// (archives are often made of the directory), that directory is the
// package. The caller must Close the source.
func (r *Registry) Open(kind Kind, name, p string) (*Source, error) {
	st, err := os.Stat(p)
	if err != nil {
		// A loader may read something that is not a file (tests' loaders
		// in memory): as before archives existed, it decides.
		l, cerr := r.Choose(kind, name, p)
		if cerr != nil {
			return nil, fmt.Errorf("%w (%v)", cerr, err)
		}
		return &Source{Path: p, Loader: l}, nil
	}
	if st.IsDir() {
		l, err := r.Choose(kind, name, p)
		if err != nil {
			return nil, err
		}
		return &Source{Path: p, Loader: l}, nil
	}
	// A loader that reads the file itself (the zip of problem_yaml).
	if l, err := r.Choose(kind, name, p); err == nil && l.Detect(p, kind) {
		return &Source{Path: p, Loader: l}, nil
	}
	if !IsArchive(p) {
		return nil, fmt.Errorf("%s is neither a directory nor an archive (%s)", p, ArchiveFormats())
	}
	tmp, err := os.MkdirTemp("", "cms-import-")
	if err != nil {
		return nil, err
	}
	src := &Source{Archive: p, tmp: tmp}
	// The directory is named after the archive, as if the user had
	// extracted it (some formats name the package after its directory).
	dir := filepath.Join(tmp, archiveBase(p))
	if err := os.Mkdir(dir, 0o755); err != nil {
		src.Close()
		return nil, err
	}
	if err := ExtractArchive(p, dir); err != nil {
		src.Close()
		return nil, err
	}
	dir = r.packageRoot(kind, dir)
	l, err := r.Choose(kind, name, dir)
	if err != nil {
		src.Close()
		return nil, fmt.Errorf("%w (extracted from %s)", err, p)
	}
	src.Path, src.Loader = dir, l
	return src, nil
}

// archiveBase is the name of an archive without its extension.
func archiveBase(p string) string {
	base := filepath.Base(p)
	base = base[:len(base)-len(archiveSuffix(base))]
	if base == "" || base == "." || base == ".." {
		return "package"
	}
	return base
}

// packageRoot returns dir, or its only subdirectory when no loader
// detects a package of kind in dir itself.
func (r *Registry) packageRoot(kind Kind, dir string) string {
	for _, l := range r.Loaders() {
		if Supports(l, kind) && l.Detect(dir, kind) {
			return dir
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return dir
	}
	var only string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue // .DS_Store and the like
		}
		if !e.IsDir() || only != "" {
			return dir
		}
		only = e.Name()
	}
	if only == "" {
		return dir
	}
	return filepath.Join(dir, only)
}
