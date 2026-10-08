// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package judging

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/multitracer"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/db"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
)

// TestMain checks, after a full run, that every query of
// internal/store/queries/judging_*.sql ran at least once against
// PostgreSQL (the store tests leave them to this package).
func TestMain(m *testing.M) {
	code := m.Run()
	if code == 0 && fullRun() {
		missing, err := queriesNotRun()
		if err != nil {
			fmt.Fprintln(os.Stderr, "checking that every judging query ran:", err)
			code = 1
		} else if len(missing) > 0 {
			fmt.Fprintf(os.Stderr, "FAIL: these judging queries never ran in the tests: %s\n", strings.Join(missing, ", "))
			code = 1
		}
	}
	os.Exit(code)
}

func fullRun() bool {
	if os.Getenv(testutil.EnvDatabaseURL) == "" {
		return false
	}
	for _, name := range []string{"test.run", "test.skip"} {
		if f := flag.Lookup(name); f != nil && f.Value.String() != "" {
			return false
		}
	}
	return true
}

// recorder notes the sqlc queries that run ("-- name: <Name> :kind").
type recorder struct {
	mu   sync.Mutex
	seen map[string]bool
}

var executed = &recorder{seen: map[string]bool{}}

var sqlcName = regexp.MustCompile(`^-- name: (\w+) :`)

func (r *recorder) noteSQL(sql string) {
	if m := sqlcName.FindStringSubmatch(sql); m != nil {
		r.mu.Lock()
		r.seen[m[1]] = true
		r.mu.Unlock()
	}
}

func (r *recorder) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	r.noteSQL(d.SQL)
	return ctx
}
func (r *recorder) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
func (r *recorder) TraceBatchStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchStartData) context.Context {
	return ctx
}
func (r *recorder) TraceBatchQuery(_ context.Context, _ *pgx.Conn, d pgx.TraceBatchQueryData) {
	r.noteSQL(d.SQL)
}
func (r *recorder) TraceBatchEnd(context.Context, *pgx.Conn, pgx.TraceBatchEndData) {}

var queryHeader = regexp.MustCompile(`(?m)^-- name: (\w+) :`)

func queriesNotRun() ([]string, error) {
	files, err := filepath.Glob(filepath.Join("..", "store", "queries", "judging*.sql"))
	if err != nil || len(files) == 0 {
		return nil, fmt.Errorf("no query files found: %v", err)
	}
	executed.mu.Lock()
	defer executed.mu.Unlock()
	var missing []string
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		for _, m := range queryHeader.FindAllStringSubmatch(string(data), -1) {
			if !executed.seen[m[1]] {
				missing = append(missing, m[1])
			}
		}
	}
	return missing, nil
}

// testDB returns a migrated database whose queries are recorded, and a
// counter of its round trips.
func testDB(t testing.TB) (*pgxpool.Pool, *db.QueryCounter) {
	t.Helper()
	qc := new(db.QueryCounter)
	pool := testutil.DBWithConfig(t, func(cfg *pgxpool.Config) {
		cfg.ConnConfig.Tracer = multitracer.New(executed, qc)
	})
	return pool, qc
}
