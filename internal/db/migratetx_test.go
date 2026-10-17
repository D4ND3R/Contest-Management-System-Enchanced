// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/db"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
)

func TestMigrateTx(t *testing.T) {
	pool := testutil.EmptyDB(t)
	ctx := context.Background()
	all, err := db.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	inTx := func(fn func(tx pgx.Tx) error) error {
		return pgx.BeginFunc(ctx, pool, fn)
	}
	applied := func(upTo int) ([]db.Migration, error) {
		var got []db.Migration
		err := inTx(func(tx pgx.Tx) (err error) {
			got, err = db.MigrateTx(ctx, tx, upTo)
			return err
		})
		return got, err
	}
	if got, err := applied(2); err != nil || len(got) != 2 {
		t.Fatalf("up to 2: %d applied, %v", len(got), err)
	}
	// A failing transaction leaves nothing behind.
	err = inTx(func(tx pgx.Tx) error {
		if _, err := db.MigrateTx(ctx, tx, 0); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `SELECT 1/0`)
		return err
	})
	if err == nil {
		t.Fatal("division by zero succeeded")
	}
	if v, _, err := db.Status(ctx, pool); err != nil || v != 2 {
		t.Fatalf("after a rolled back MigrateTx: version %d, %v", v, err)
	}
	if got, err := applied(0); err != nil || len(got) != len(all)-2 {
		t.Fatalf("up to the end: %d applied, %v", len(got), err)
	}
	// Asking for an older version than the database's is a no-op.
	if got, err := applied(1); err != nil || len(got) != 0 {
		t.Fatalf("up to 1 on a migrated database: %d applied, %v", len(got), err)
	}
	if _, err := applied(len(all) + 1); err == nil {
		t.Fatal("an unknown version was accepted")
	}
	if err := db.CheckSchema(ctx, pool); err != nil {
		t.Fatal(err)
	}
	// A database newer than the binary is an error, not a panic.
	if _, err := pool.Exec(ctx, `INSERT INTO schema_migrations (version, name, checksum) VALUES (9999, 'future', 'x')`); err != nil {
		t.Fatal(err)
	}
	if _, err := applied(0); err == nil || !strings.Contains(err.Error(), "newer cms") {
		t.Fatalf("want newer-database error, got %v", err)
	}
}
