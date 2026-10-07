// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package db_test

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/db"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
)

func TestMigrationsAreWellFormed(t *testing.T) {
	migs, err := db.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migs) == 0 {
		t.Fatal("no migrations embedded")
	}
	createIndex := regexp.MustCompile(`(?i)^\s*CREATE\s+(UNIQUE\s+)?INDEX`)
	for _, m := range migs {
		lines := strings.Split(m.SQL, "\n")
		for i, l := range lines {
			if !createIndex.MatchString(l) {
				continue
			}
			// Every index costs writes: the line above must say why it exists.
			if i == 0 || !strings.HasPrefix(strings.TrimSpace(lines[i-1]), "--") {
				t.Errorf("%04d_%s line %d: index without a justifying comment on the line above", m.Version, m.Name, i+1)
			}
		}
		if strings.Contains(strings.ToUpper(m.SQL), "DROP TABLE") && !strings.Contains(m.SQL, "-- destructive:") {
			t.Errorf("%04d_%s drops a table without a '-- destructive:' note explaining the data migration", m.Version, m.Name)
		}
	}
}

func TestMigrateFromScratchIsIdempotent(t *testing.T) {
	pool := testutil.EmptyDB(t)
	ctx := context.Background()

	if err := db.CheckSchema(ctx, pool); !errors.Is(err, db.ErrSchemaOutdated) {
		t.Fatalf("empty database: want ErrSchemaOutdated, got %v", err)
	}
	applied, err := db.Migrate(ctx, pool, nil)
	if err != nil {
		t.Fatal(err)
	}
	all, _ := db.Migrations()
	if len(applied) != len(all) {
		t.Fatalf("applied %d of %d", len(applied), len(all))
	}
	if err := db.CheckSchema(ctx, pool); err != nil {
		t.Fatal(err)
	}
	again, err := db.Migrate(ctx, pool, nil)
	if err != nil || len(again) != 0 {
		t.Fatalf("second run applied %d (%v)", len(again), err)
	}
	v, pending, err := db.Status(ctx, pool)
	if err != nil || v != len(all) || len(pending) != 0 {
		t.Fatalf("status v=%d pending=%d err=%v", v, len(pending), err)
	}
}

func TestConcurrentMigratorsApplyOnce(t *testing.T) {
	pool := testutil.EmptyDB(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	counts := make([]int, 4)
	errs := make([]error, 4)
	for i := range counts {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			a, err := db.Migrate(ctx, pool, nil)
			counts[i], errs[i] = len(a), err
		}(i)
	}
	wg.Wait()
	total := 0
	for i := range counts {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		total += counts[i]
	}
	all, _ := db.Migrations()
	if total != len(all) {
		t.Errorf("migrations applied %d times in total, want %d", total, len(all))
	}
}

func TestEditedMigrationIsRejected(t *testing.T) {
	pool := testutil.DB(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE schema_migrations SET checksum = 'tampered' WHERE version = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Migrate(ctx, pool, nil); err == nil || !strings.Contains(err.Error(), "modified after being applied") {
		t.Fatalf("want modified-migration error, got %v", err)
	}
}

func TestNewerDatabaseIsRejected(t *testing.T) {
	pool := testutil.DB(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO schema_migrations (version, name, checksum) VALUES (9999, 'future', 'x')`); err != nil {
		t.Fatal(err)
	}
	if err := db.CheckSchema(ctx, pool); err == nil || !strings.Contains(err.Error(), "newer cms") {
		t.Fatalf("want newer-database error, got %v", err)
	}
}

func TestServerSettingsTable(t *testing.T) {
	pool := testutil.DB(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO server_settings (key, value) VALUES ('timezone', '"America/Mexico_City"')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO server_settings (key, value) VALUES ('Bad Key', '1')`); err == nil {
		t.Error("invalid key accepted")
	}
	var tz string
	if err := pool.QueryRow(ctx, `SELECT value #>> '{}' FROM server_settings WHERE key = 'timezone'`).Scan(&tz); err != nil || tz != "America/Mexico_City" {
		t.Errorf("tz=%q err=%v", tz, err)
	}
}

func TestDatabasesAreIsolated(t *testing.T) {
	a, b := testutil.DB(t), testutil.DB(t)
	ctx := context.Background()
	if _, err := a.Exec(ctx, `INSERT INTO server_settings (key, value) VALUES ('only_in_a', 'true')`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := b.QueryRow(ctx, `SELECT count(*) FROM server_settings`).Scan(&n); err != nil || n != 0 {
		t.Errorf("database b sees %d rows (%v)", n, err)
	}
}
