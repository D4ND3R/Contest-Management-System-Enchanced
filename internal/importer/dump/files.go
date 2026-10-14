// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package dump

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
)

// The files of a dump are named by the SHA-1 of their content, and every
// digest of the dump is a SHA-1. Here digests are SHA-256 (D19): each file
// is stored in the blob store (only hashed in a dry run) and checked
// against its name on the way, and the digest columns are rewritten with
// the new digests when the rows are written.

// hashes maps the SHA-1 of the files of the dump to their SHA-256.
type hashes struct {
	mu sync.Mutex
	m  map[string]string
	// mismatched lists the files whose content does not match their
	// name.
	mismatched []string
	stored     int
	bytes      int64
}

func newHashes() *hashes { return &hashes{m: map[string]string{}} }

// storeFile stores (or, without a store, only hashes) one file of the
// dump named name (its SHA-1) and checks its content against its name.
func (h *hashes) storeFile(ctx context.Context, blobs blob.Store, name string, r io.Reader) error {
	if h.has(name) {
		return nil
	}
	sum1 := sha1.New()
	tr := io.TeeReader(ctxReader{ctx, r}, sum1)
	var digest string
	var size int64
	if blobs == nil {
		sum := sha256.New()
		n, err := io.Copy(sum, tr)
		if err != nil {
			return fmt.Errorf("files/%s: %w", name, err)
		}
		digest, size = hex.EncodeToString(sum.Sum(nil)), n
	} else {
		var err error
		if digest, size, err = blobs.Put(ctx, tr); err != nil {
			return fmt.Errorf("files/%s: %w", name, err)
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if hex.EncodeToString(sum1.Sum(nil)) != name {
		// The content stored (if any) is unused: the garbage collector
		// removes it.
		h.mismatched = append(h.mismatched, name)
		return nil
	}
	h.m[name] = digest
	h.stored++
	h.bytes += size
	return nil
}

func (h *hashes) has(name string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.m[name]
	return ok
}

// storeAll stores the files the dump needs from a source that can open
// them in any order, parallel at a time.
func (h *hashes) storeAll(ctx context.Context, blobs blob.Store, files map[string]entry, need map[string]*use, parallel int) error {
	if parallel <= 0 {
		parallel = importer.DefaultUploads
	}
	names := make([]string, 0, len(need))
	for name := range need {
		if _, ok := files[name]; ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(parallel)
	for _, name := range names {
		e := files[name]
		g.Go(func() error {
			rc, err := e.open()
			if err != nil {
				return fmt.Errorf("files/%s: %w", name, err)
			}
			defer rc.Close()
			return h.storeFile(ctx, blobs, name, rc)
		})
	}
	return g.Wait()
}

// report notes the files whose content does not match their name.
func (h *hashes) report(rep *importer.Report) {
	sort.Strings(h.mismatched)
	for _, name := range h.mismatched {
		rep.Errorf("files/"+name, "the content of the file does not match its name (its SHA-1 digest): the dump is corrupted")
	}
}

// resolveFiles decides what to do with the files the rows need, knowing
// which files the dump has: a missing required file is an error, a
// missing generated file becomes the tombstone and a missing optional one
// is dropped, and a missing file of a task whose files cmsDumpExporter
// does not export (outside) becomes the tombstone too. Files of the dump
// that nothing needs are reported. A dump without any of the files it
// needs (exported with cmsDumpExporter -F) is one error.
func (d *Dump) resolveFiles(has func(string) bool, all []string) {
	var missing []string
	tombstoned, dropped, needed, present := 0, 0, 0, 0
	outsideTasks := map[int32]int{}
	for name, u := range d.need {
		if u.kind != optional {
			needed++
		}
		if has(name) {
			present++
			continue
		}
		switch u.kind {
		case required:
			missing = append(missing, name)
		case outside:
			outsideTasks[u.task]++
		case generated:
			tombstoned++
		case optional:
			dropped++
		}
	}
	if len(missing) > 0 && present == 0 {
		// cmsDumpImporter of CMS imports such a dump (the rows name the
		// files by their SHA-1, which the files of an installation of CMS
		// can match later); here a digest is the SHA-256 of the content.
		d.rep.Errorf(modelName, "the dump has none of the %s its objects need: it was exported without files "+
			"(cmsDumpExporter -F), and such a dump cannot be imported: each file is stored under its SHA-256, which "+
			"only its content gives (a dump names its files by their SHA-1); export it again without -F",
			plural(needed, "file"))
		return
	}
	contestName := make(map[int32]string, len(d.contests))
	for _, c := range d.contests {
		contestName[c.key] = c.name
	}
	for _, t := range d.tasks {
		n := outsideTasks[t.key]
		if n == 0 {
			continue
		}
		why, fix := "the task is in no contest", "import the task again from its package (import-task -u)"
		if t.contest != none {
			why = fmt.Sprintf("the task is in the contest %q, which is in the dump only because its contestants also "+
				"took part in an exported contest (the dump was written with cmsDumpExporter -c)", contestName[t.contest])
			fix = "export that contest too (add its id to -c) to keep them, or import the task again from its " +
				"package (import-task -u)"
		}
		d.rep.Warnf(d.where(t.key, t.name), "%s, and cmsDumpExporter exports only the files of the contests it exports: "+
			"%s of the task, its submissions and user tests are imported as deleted (tombstones); %s",
			why, plural(n, "file"), fix)
	}
	sort.Slice(missing, func(i, j int) bool { return d.need[missing[i]].where < d.need[missing[j]].where })
	const listed = 10
	for i, name := range missing {
		if i == listed {
			if n := len(missing) - listed; n == 1 {
				d.rep.Errorf(modelName, "1 more file is not in the dump")
			} else {
				d.rep.Errorf(modelName, "%d more files are not in the dump", n)
			}
			break
		}
		d.rep.Errorf(d.need[name].where, "the file %s is not in the dump", name)
	}
	if tombstoned > 0 {
		d.rep.Warnf(modelName, "%s are not in the dump: they are imported as deleted (tombstones) and compiled again when needed",
			plural(tombstoned, "executable or user test output"))
	}
	if dropped > 0 {
		d.rep.Warnf(modelName, "the archived sandboxes of the results are not in the dump (CMS does not export them): "+
			"%s dropped", plural(dropped, "reference"))
	}
	unused := 0
	for _, name := range all {
		if d.need[name] == nil {
			unused++
		}
	}
	if unused > 0 {
		d.rep.Warnf(modelName, "%s of the dump are not used by its objects: not imported", plural(unused, "file"))
	}
}

// digestOf returns the SHA-256 digest of a file of the dump named by its
// SHA-1: the tombstone stays, a missing generated file becomes the
// tombstone, and "" means a missing optional file.
func (d *Dump) digestOf(h *hashes, sha1 string) string {
	if sha1 == tombstone || sha1 == "" {
		return sha1
	}
	if s, ok := h.m[sha1]; ok {
		return s
	}
	if u := d.need[sha1]; u != nil && (u.kind == generated || u.kind == outside) {
		return tombstone
	}
	return ""
}

// ctxReader stops reading when ctx is done.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
