// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package importer

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
)

// texts runs a query whose rows are one text column.
func texts(t testing.TB, pool *pgxpool.Pool, sql string, args ...any) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), sql, args...)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return out
}

// text runs a query that returns one text value.
func text(t testing.TB, pool *pgxpool.Pool, sql string, args ...any) string {
	t.Helper()
	out := texts(t, pool, sql, args...)
	if len(out) != 1 {
		t.Fatalf("%s: %d rows, want 1: %q", sql, len(out), out)
	}
	return out[0]
}

// count runs a count query.
func count(t testing.TB, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

// wantRows compares the rows of a query with want.
func wantRows(t testing.TB, pool *pgxpool.Pool, want []string, sql string, args ...any) {
	t.Helper()
	if got := texts(t, pool, sql, args...); !slices.Equal(got, want) {
		t.Errorf("%s:\n got %q\nwant %q", strings.Join(strings.Fields(sql), " "), got, want)
	}
}

// contestWithGroup creates an empty contest with a group "main".
func contestWithGroup(t testing.TB, w *Writer, name string) int32 {
	t.Helper()
	c := NewContest(name)
	c.Groups = []Group{NewGroup("main")}
	res, err := w.WriteContest(context.Background(), c, nil, ContestOptions{})
	if err != nil {
		t.Fatalf("WriteContest %s: %v", name, err)
	}
	return res.ContestID
}

// addScored gives username (created if needed) a participation in the
// contest and a submission to the task scored score on the dataset.
func addScored(t testing.TB, pool *pgxpool.Pool, contestID, taskID, datasetID int32, username string, score float64) int32 {
	t.Helper()
	ctx := context.Background()
	var userID, partID, subID int32
	err := pool.QueryRow(ctx, `INSERT INTO users (first_name, last_name, username, password)
		VALUES ('U', 'U', $1, 'plaintext:x')
		ON CONFLICT (username) DO UPDATE SET first_name = users.first_name RETURNING id`, username).Scan(&userID)
	if err == nil {
		err = pool.QueryRow(ctx, `INSERT INTO participations (contest_id, user_id, group_id)
			SELECT id, $2, main_group_id FROM contests WHERE id = $1 RETURNING id`, contestID, userID).Scan(&partID)
	}
	if err == nil {
		err = pool.QueryRow(ctx, `INSERT INTO submissions (opaque_id, participation_id, task_id, timestamp, language)
			VALUES (1, $1, $2, now(), 'C++17 / g++') RETURNING id`, partID, taskID).Scan(&subID)
	}
	if err == nil {
		_, err = pool.Exec(ctx, `INSERT INTO submission_results (submission_id, dataset_id, compilation_outcome,
			evaluation_outcome, score, score_details, public_score, public_score_details, ranking_score_details, scored_at)
			VALUES ($1, $2, 'ok', 'ok', $3, '[]', 0, '[]', '{}', now())`, subID, datasetID, score)
	}
	if err != nil {
		t.Fatal(err)
	}
	return subID
}

// activeDataset returns the id of the active dataset of a task.
func activeDataset(t testing.TB, pool *pgxpool.Pool, taskID int32) int32 {
	t.Helper()
	var id int32
	if err := pool.QueryRow(context.Background(), `SELECT active_dataset_id FROM tasks WHERE id = $1`, taskID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// change returns the first change of that kind and name, or a zero one.
func change(r *Result, kind, name string) Change {
	for _, c := range r.Changes {
		if c.Kind == kind && c.Name == name {
			return c
		}
	}
	return Change{}
}

// changes prints the changes of a result, for test messages.
func changes(r *Result) string {
	var b strings.Builder
	for _, c := range r.Changes {
		b.WriteString(c.String())
		b.WriteByte('\n')
	}
	return b.String()
}

// sameEvents compares the events of a result.
func sameEvents(got []judging.Event, want ...judging.Event) bool {
	return reflect.DeepEqual(got, want)
}
