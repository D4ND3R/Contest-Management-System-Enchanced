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

	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
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
	tries Tries
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
	// Tries are the maximum numbers of failed attempts before giving up
	// (dispatcher.max_compilation_tries and max_evaluation_tries); zero
	// fields take the upstream values. Every process that reads the
	// judging state (dispatcher, admin site, ctl) must use the same ones.
	Tries Tries
}

// Tries are the maximum numbers of failed attempts of each kind of
// operation before the dispatcher gives up (upstream MAX_*_TRIES).
type Tries struct {
	Compilation         int32
	Evaluation          int32
	UserTestCompilation int32
	UserTestEvaluation  int32
}

// TriesFrom returns the tries of the configuration
// (dispatcher.max_compilation_tries and max_evaluation_tries, which apply
// to submissions and user tests alike). Every process that judges or
// reports the judging state uses it.
func TriesFrom(d config.Dispatcher) Tries {
	return Tries{
		Compilation: int32(d.MaxCompilationTries), Evaluation: int32(d.MaxEvaluationTries),
		UserTestCompilation: int32(d.MaxCompilationTries), UserTestEvaluation: int32(d.MaxEvaluationTries),
	}
}

// withDefaults fills the zero fields with the upstream values.
func (t Tries) withDefaults() Tries {
	def := func(v *int32, d int32) {
		if *v <= 0 {
			*v = d
		}
	}
	def(&t.Compilation, MaxCompilationTries)
	def(&t.Evaluation, MaxEvaluationTries)
	def(&t.UserTestCompilation, MaxUserTestCompilationTries)
	def(&t.UserTestEvaluation, MaxUserTestEvaluationTries)
	return t
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
	return &Judge{store: store.New(pool), langs: opts.Languages, log: opts.Logger, now: opts.Now,
		tries: opts.Tries.withDefaults()}
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
