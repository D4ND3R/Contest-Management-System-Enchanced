// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package importer

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// Writer stores models in PostgreSQL and their files in the blob store.
// Each Write… call validates the model (nothing is written if it has
// errors), stores the files first (hashing them with SHA-256) and then
// writes every row in one transaction: all or nothing. Rows are written
// with a bounded number of statements per task, contest or list of
// people, whatever the number of testcases, users or participations.
type Writer struct {
	Pool  *pgxpool.Pool
	Blobs blob.Store
	// Languages are the programming languages of the installation (nil:
	// the built-in ones).
	Languages *language.Registry
	// Uploads is how many files are stored at the same time
	// (DefaultUploads if 0).
	Uploads int
	// DryRun validates, hashes the files without storing them and runs
	// the transaction, but rolls it back: the result says what the import
	// would do (the preview of the administration, --dry-run), and has no
	// Events.
	DryRun bool
}

// NewWriter returns a writer on a database and a blob store.
func NewWriter(pool *pgxpool.Pool, blobs blob.Store, langs *language.Registry) *Writer {
	if langs == nil {
		langs = language.Builtin()
	}
	return &Writer{Pool: pool, Blobs: blobs, Languages: langs}
}

func (w *Writer) langs() *language.Registry {
	if w.Languages == nil {
		return language.Builtin()
	}
	return w.Languages
}

// Action is what an import did to an object.
type Action string

// The actions.
const (
	Created   Action = "created"
	Updated   Action = "updated"
	Unchanged Action = "unchanged"
	// Skipped: the object exists and the options did not ask to update
	// it.
	Skipped Action = "skipped"
	Deleted Action = "deleted"
)

// Change is one thing an import did.
type Change struct {
	Action Action `json:"action"`
	// Kind is "contest", "group", "task", "dataset", "user", "team" or
	// "participation".
	Kind string `json:"kind"`
	Name string `json:"name"`
	ID   int32  `json:"id,omitempty"`
	// Details says what changed ("4 testcases added").
	Details string `json:"details,omitempty"`
}

func (c Change) String() string {
	s := fmt.Sprintf("%s %s %s", c.Action, c.Kind, c.Name)
	if c.ID != 0 {
		s += fmt.Sprintf(" (id %d)", c.ID)
	}
	if c.Details != "" {
		s += ": " + c.Details
	}
	return s
}

// Result is what an import did.
type Result struct {
	// Report has the warnings of the load, the validation and the write.
	Report *Report
	// Changes lists what was created, updated, left alone...
	Changes []Change
	// ContestID is the contest imported, or the contest a task was
	// imported into (0: none).
	ContestID int32
	// TaskIDs are the ids of the tasks written, by name.
	TaskIDs map[string]int32
	// Events are the notifications for the dispatcher (judging.Notify),
	// to send once the import committed: a sweep of the contest, the
	// tasks updated (new datasets to judge, data for the scoreboard).
	Events []judging.Event
}

func newResult(rep *Report) *Result {
	if rep == nil {
		rep = &Report{}
	}
	return &Result{Report: rep, TaskIDs: map[string]int32{}}
}

func (r *Result) add(a Action, kind, name string, id int32, details string) {
	r.Changes = append(r.Changes, Change{Action: a, Kind: kind, Name: name, ID: id, Details: details})
}

// Count returns the number of changes of a kind with an action.
func (r *Result) Count(kind string, a Action) int {
	n := 0
	for _, c := range r.Changes {
		if c.Kind == kind && c.Action == a {
			n++
		}
	}
	return n
}

// inTx runs fn in a transaction (READ COMMITTED, run again on a
// serialization failure or a deadlock, like store.Store.RunTx: fn must
// start from scratch every time).
func (w *Writer) inTx(ctx context.Context, fn func(tx pgx.Tx, qq *q.Queries) error) error {
	err := store.New(w.Pool).RunTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := fn(tx, q.New(tx)); err != nil {
			return err
		}
		if w.DryRun {
			return errDryRun
		}
		return nil
	})
	if errors.Is(err, errDryRun) {
		return nil
	}
	return err
}

// errDryRun rolls back the transaction of a dry run.
var errDryRun = errors.New("importer: dry run")

// done takes what the transaction did into res.
func (w *Writer) done(res, out *Result) {
	res.merge(out)
	if w.DryRun {
		res.Events = nil
	}
}

// upload stores the files of the tasks.
func (w *Writer) upload(ctx context.Context, tasks []*Task, skipStatements bool) error {
	var files []*File
	for _, t := range tasks {
		all := t.Files()
		if skipStatements {
			// The statements must not be touched (Files lists them first).
			all = all[len(t.Statements):]
		}
		files = append(files, all...)
	}
	if w.DryRun {
		return Hash(ctx, w.Blobs, files, w.Uploads)
	}
	return Upload(ctx, w.Blobs, files, w.Uploads)
}

// plural returns "n thing" or "n things".
func plural(n int, thing string) string {
	if n == 1 {
		return "1 " + thing
	}
	return fmt.Sprintf("%d %ss", n, thing)
}

// nonZero is plural, or "" for 0.
func nonZero(n int, thing string) string {
	if n == 0 {
		return ""
	}
	return plural(n, thing)
}

// delta counts the rows a sync added, changed and removed.
type delta struct{ added, changed, removed int }

func (d delta) zero() bool { return d == delta{} }

func (d delta) describe(thing string) string {
	var parts []string
	if d.added > 0 {
		parts = append(parts, plural(d.added, thing)+" added")
	}
	if d.changed > 0 {
		parts = append(parts, plural(d.changed, thing)+" changed")
	}
	if d.removed > 0 {
		parts = append(parts, plural(d.removed, thing)+" removed")
	}
	return strings.Join(parts, ", ")
}

// joinDetails joins the non-empty parts.
func joinDetails(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "; ")
}
