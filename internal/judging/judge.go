// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package judging

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// Judge is the judging core on a database. It is safe for concurrent use,
// also from several processes: every write locks the rows it changes.
type Judge struct {
	store *store.Store
	langs *language.Registry
	log   *slog.Logger
	now   func() time.Time
}

// Options configures a Judge. Zero fields take defaults.
type Options struct {
	// Languages is the language registry of the installation (header
	// extensions and source extensions for user tests, languages that need
	// threads); default language.Builtin().
	Languages *language.Registry
	// Logger receives what upstream logs (gave up, scoring errors...);
	// default slog.Default().
	Logger *slog.Logger
	// Now is the clock (scored_at); default time.Now.
	Now func() time.Time
}

// New returns a Judge on pool.
func New(pool *pgxpool.Pool, opts Options) *Judge {
	if opts.Languages == nil {
		opts.Languages = language.Builtin()
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Judge{store: store.New(pool), langs: opts.Languages, log: opts.Logger, now: opts.Now}
}

// inTx runs fn in a READ COMMITTED transaction, retried on serialization
// failures and deadlocks (store.RunTx).
func (j *Judge) inTx(ctx context.Context, fn func(tx pgx.Tx, qq *q.Queries) error) error {
	return j.store.RunTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return fn(tx, j.store.Q.WithTx(tx))
	})
}

// batchErr collects the first unexpected error of a sqlc batch; a
// pgx.ErrNoRows means the statement changed nothing.
type batchErr struct {
	once sync.Once
	err  error
}

// ok reports whether err is nil; it records any error but pgx.ErrNoRows.
func (b *batchErr) ok(err error) bool {
	if err == nil {
		return true
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		b.once.Do(func() { b.err = err })
	}
	return false
}

// uniq returns the distinct values of s, sorted as they come (s is
// usually sorted already).
func uniq[T comparable](s []T) []T {
	seen := make(map[T]bool, len(s))
	out := make([]T, 0, len(s))
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
