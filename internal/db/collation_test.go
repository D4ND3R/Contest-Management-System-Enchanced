// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package db_test

import (
	"context"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
)

// TestIdentifiersSortByCodePoint checks migration 0003: identifiers sort by
// code point whatever the locale of the database (CI and real
// installations use en_US, which ignores punctuation).
func TestIdentifiersSortByCodePoint(t *testing.T) {
	pool := testutil.DB(t)
	ctx := context.Background()
	rows, err := pool.Query(ctx, `
		SELECT table_name, column_name, coalesce(collation_name, '')
		FROM information_schema.columns
		WHERE table_schema = 'public' AND (table_name, column_name) IN (
			('contests','name'), ('groups','name'), ('tasks','name'), ('users','username'),
			('admins','username'), ('teams','code'), ('testcases','codename'),
			('statements','language'), ('attachments','filename'), ('managers','filename'),
			('files','filename'), ('executables','filename'), ('user_test_files','filename'),
			('user_test_managers','filename'), ('user_test_executables','filename'),
			('server_settings','key'))`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var table, column, coll string
		if err := rows.Scan(&table, &column, &coll); err != nil {
			t.Fatal(err)
		}
		n++
		if coll != "C" {
			t.Errorf("%s.%s has collation %q, want C", table, column, coll)
		}
	}
	if n != 16 {
		t.Errorf("found %d of the 16 identifier columns", n)
	}
	var sorted []string
	r2, err := pool.Query(ctx, `SELECT x COLLATE "C" FROM unnest(ARRAY['aaa.h','aaa.%l','B','a']) x
		ORDER BY x COLLATE "C"`)
	if err != nil {
		t.Fatal(err)
	}
	for r2.Next() {
		var s string
		_ = r2.Scan(&s)
		sorted = append(sorted, s)
	}
	if want := []string{"B", "a", "aaa.%l", "aaa.h"}; len(sorted) != 4 || sorted[0] != want[0] || sorted[2] != want[2] {
		t.Errorf("code point order %v, want %v", sorted, want)
	}
}
