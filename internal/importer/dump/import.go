// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package dump

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store"
)

// Options say what to import (the flags of cmsDumpImporter).
type Options struct {
	// Drop deletes the contest data of the database first (contests,
	// tasks, users, teams and everything under them; not the admins), in
	// the same transaction: cmsDumpImporter -d. Without it, a dump with a
	// contest, task, user or team whose name is already in the database is
	// refused (ConflictError). A dry run with Drop empties nothing, and
	// takes no lock that would block the installation: it writes into
	// empty temporary tables that hide those of the database.
	Drop bool
	// ReuseUsers uses the users and teams of the database that have the
	// username or code of one of the dump (keeping them as they are)
	// instead of refusing the dump: to import, one by one, dumps of
	// contests that share their contestants.
	ReuseUsers bool
	// SkipGenerated leaves out the results of submissions and user tests
	// (they are judged again), SkipSubmissions the submissions,
	// SkipUserTests the user tests, and SkipUsers the users, teams,
	// participations and everything under them (-G, -S, -U and -X of
	// cmsDumpImporter).
	SkipGenerated   bool
	SkipSubmissions bool
	SkipUserTests   bool
	SkipUsers       bool
	// Report collects the warnings and errors (a new one if nil).
	Report *importer.Report
}

// Import imports the dump of CMS at path (a directory, or a zip or tar
// archive) with the database, blob store, languages, parallelism and dry
// run of w. The files are checked against their names and stored first,
// then every row is written in one transaction: nothing is written if
// anything is wrong. Errors in the dump are a *importer.ValidationError
// (its report lists them); names already in the database a
// *ConflictError. The result lists what was created and has an
// EventSweep for each contest, to send to the dispatcher once the import
// committed: it judges what the dump did not have judged.
func Import(ctx context.Context, w *importer.Writer, path string, opts Options) (*importer.Result, error) {
	rep := opts.Report
	if rep == nil {
		rep = &importer.Report{}
	}
	res := &importer.Result{Report: rep, TaskIDs: map[string]int32{}}
	src, err := openSource(path)
	if err != nil {
		return res, err
	}
	langs := w.Languages
	if langs == nil {
		langs = language.Builtin()
	}
	var blobs blob.Store
	if !w.DryRun {
		blobs = w.Blobs
	}
	h := newHashes()
	var d *Dump
	switch s := src.(type) {
	case randomSource:
		defer s.Close()
		rc, err := s.model()
		if err != nil {
			return res, err
		}
		d, err = read(rc, &opts, langs, rep)
		rc.Close()
		if err != nil {
			return res, err
		}
		files, others, err := s.files()
		if err != nil {
			return res, err
		}
		if len(others) > 0 {
			rep.Warnf("files", "%s not named by a SHA-1 digest ignored", plural(len(others), "file"))
		}
		all := make([]string, 0, len(files))
		for name := range files {
			all = append(all, name)
		}
		d.resolveFiles(func(name string) bool { _, ok := files[name]; return ok }, all)
		if err := rep.Err(); err != nil {
			return res, err
		}
		if err := h.storeAll(ctx, blobs, files, d.need, w.Uploads); err != nil {
			return res, err
		}
	case streamSource:
		defer s.Close()
		var all []string
		others := 0
		err := s.scan(func(r io.Reader) error {
			var err error
			if d, err = read(r, &opts, langs, rep); err != nil {
				return err
			}
			// Nothing is stored for a dump that cannot be imported.
			return rep.Err()
		}, func(name string, r io.Reader) error {
			all = append(all, name)
			if d != nil && d.need[name] == nil {
				return nil
			}
			return h.storeFile(ctx, blobs, name, r)
		}, func(string) { others++ })
		if err != nil {
			return res, err
		}
		if others > 0 {
			rep.Warnf("files", "%s not named by a SHA-1 digest ignored", plural(others, "file"))
		}
		present := make(map[string]bool, len(all))
		for _, name := range all {
			present[name] = true
		}
		d.resolveFiles(func(name string) bool { return present[name] }, all)
	default:
		return res, fmt.Errorf("%s: %w", path, ErrNotDump)
	}
	h.report(rep)
	if err := rep.Err(); err != nil {
		return res, err
	}

	dw := &dbWriter{ctx: ctx, d: d, h: h, opts: &opts, dryRun: w.DryRun, res: res}
	run := func(tx pgx.Tx) error {
		dw.tx = tx
		if err := dw.write(); err != nil {
			return err
		}
		if w.DryRun {
			return errRollback
		}
		return nil
	}
	if w.DryRun && opts.Drop {
		err = isolated(ctx, w.Pool, run)
	} else {
		err = store.New(w.Pool).RunTx(ctx, pgx.TxOptions{}, run)
	}
	if errors.Is(err, errRollback) {
		res.Events = nil
		return res, nil
	}
	if err != nil {
		res.Changes, res.Events, res.ContestID, res.TaskIDs = nil, nil, 0, map[string]int32{}
		return res, err
	}
	return res, nil
}

// isolated runs fn in a transaction on a connection taken out of the pool
// and closed afterwards, for a dry run with Drop: its temporary tables hide
// tables of the database by name (dbWriter.shadow), so the statements that
// the connection prepared before must not be used (they name the tables of
// the database), nor those it prepares during the dry run afterwards.
func isolated(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	pc, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	conn := pc.Hijack()
	defer func() {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = conn.Close(cctx)
	}()
	if err := conn.DeallocateAll(ctx); err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, conn, fn)
}

// read decodes contest.json, updates it to the current data model,
// converts it and checks it; errors go to rep (see Report.Err), err is a
// problem reading it at all.
func read(r io.Reader, opts *Options, langs *language.Registry, rep *importer.Report) (*Dump, error) {
	raw, err := readRaw(r)
	if err != nil {
		return nil, err
	}
	d := newDump(raw.version, raw.roots, rep, opts)
	if raw.version < Version && raw.version >= OldestVersion {
		rep.Warnf(modelName, "the dump was written with the data model version %d of CMS: it was updated to version %d",
			raw.version, Version)
	}
	skip := opts.skipClasses()
	warn := func(format string, args ...any) { rep.Warnf(modelName, format, args...) }
	if err := raw.decodeObjects(warn, func(key string, o *object) error {
		d.add(key, o, skip)
		return nil
	}); err != nil {
		return nil, err
	}
	for _, k := range raw.roots {
		if i, ok := d.index[k]; !ok || d.class[i] == "" {
			rep.Warnf(modelName, "_objects names object %s, which is not in the dump", k)
		}
	}
	d.check(langs)
	return d, nil
}
