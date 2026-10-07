// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package db

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationLockID is the pg_advisory_lock key that serialises concurrent
// migrators (two services upgraded at once, parallel test packages).
const migrationLockID int64 = 0x636d735f6d6967 // "cms_mig"

// Migration is one forward-only schema change.
type Migration struct {
	Version  int
	Name     string
	SQL      string
	Checksum string
}

var migrationName = regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.sql$`)

// Migrations returns the embedded migrations in order.
func Migrations() ([]Migration, error) {
	return loadMigrations(migrationFiles, "migrations")
}

func loadMigrations(fsys fs.FS, dir string) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	var out []Migration
	for _, e := range entries {
		m := migrationName.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("migration %q: name must be NNNN_snake_case.sql", e.Name())
		}
		data, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		v, _ := strconv.Atoi(m[1])
		sum := sha256.Sum256(data)
		out = append(out, Migration{Version: v, Name: m[2], SQL: string(data), Checksum: hex.EncodeToString(sum[:])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	for i, m := range out {
		if m.Version != i+1 {
			return nil, fmt.Errorf("migrations must be numbered 1, 2, 3... without gaps; found %04d at position %d", m.Version, i+1)
		}
	}
	return out, nil
}

const createTracking = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    integer PRIMARY KEY,
    name       text NOT NULL,
    checksum   text NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT now()
)`

type applied struct {
	version  int
	name     string
	checksum string
}

func readApplied(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}) ([]applied, error) {
	rows, err := q.Query(ctx, `SELECT version, name, checksum FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (applied, error) {
		var a applied
		err := r.Scan(&a.version, &a.name, &a.checksum)
		return a, err
	})
}

// compare matches the applied migrations against the embedded ones and
// returns the pending ones. Editing an applied migration, or running a
// binary older than the database, is an error: migrations are forward-only.
func compare(done []applied, all []Migration) ([]Migration, error) {
	for i, a := range done {
		if i >= len(all) {
			return nil, fmt.Errorf("the database is at schema version %d but this binary only knows %d: "+
				"run a newer cms (downgrades are not supported; restore a backup instead)", done[len(done)-1].version, len(all))
		}
		m := all[i]
		if a.version != m.Version {
			return nil, fmt.Errorf("schema_migrations has version %d where %d was expected", a.version, m.Version)
		}
		if a.checksum != m.Checksum {
			return nil, fmt.Errorf("migration %04d_%s was modified after being applied; "+
				"never edit an applied migration, add a new one", m.Version, m.Name)
		}
	}
	return all[len(done):], nil
}

// Migrate applies the pending migrations, each in its own transaction,
// holding an advisory lock so concurrent callers apply them only once.
func Migrate(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) ([]Migration, error) {
	all, err := Migrations()
	if err != nil {
		return nil, err
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockID); err != nil {
		return nil, fmt.Errorf("taking the migration lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, migrationLockID)
	}()

	if _, err := conn.Exec(ctx, createTracking); err != nil {
		return nil, fmt.Errorf("creating schema_migrations: %w", err)
	}
	done, err := readApplied(ctx, conn)
	if err != nil {
		return nil, err
	}
	pending, err := compare(done, all)
	if err != nil {
		return nil, err
	}
	for _, m := range pending {
		start := time.Now()
		err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, m.SQL); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version, name, checksum) VALUES ($1, $2, $3)`,
				m.Version, m.Name, m.Checksum)
			return err
		})
		if err != nil {
			return nil, fmt.Errorf("migration %04d_%s: %w", m.Version, m.Name, err)
		}
		if log != nil {
			log.Info("migration applied", "version", m.Version, "name", m.Name, "dur_ms", time.Since(start).Milliseconds())
		}
	}
	return pending, nil
}

// Status returns the current schema version and the pending migrations.
func Status(ctx context.Context, pool *pgxpool.Pool) (int, []Migration, error) {
	all, err := Migrations()
	if err != nil {
		return 0, nil, err
	}
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('schema_migrations') IS NOT NULL`).Scan(&exists); err != nil {
		return 0, nil, err
	}
	if !exists {
		return 0, all, nil
	}
	done, err := readApplied(ctx, pool)
	if err != nil {
		return 0, nil, err
	}
	pending, err := compare(done, all)
	if err != nil {
		return 0, nil, err
	}
	return len(done), pending, nil
}

// ErrSchemaOutdated means migrations are pending.
var ErrSchemaOutdated = errors.New("the database schema is outdated: run `cms migrate`")

// CheckSchema fails unless the database is exactly at the version of this
// binary. Services call it at startup so that an upgrade never runs new
// code against an old schema (or the other way round).
func CheckSchema(ctx context.Context, pool *pgxpool.Pool) error {
	_, pending, err := Status(ctx, pool)
	if err != nil {
		return err
	}
	if len(pending) > 0 {
		return fmt.Errorf("%w (%d pending, next %04d_%s)", ErrSchemaOutdated, len(pending), pending[0].Version, pending[0].Name)
	}
	return nil
}
