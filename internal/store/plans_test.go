// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package store_test

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/store"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// capture is a q.DBTX that remembers the last query it ran, to EXPLAIN
// it afterwards with the same arguments.
type capture struct {
	q.DBTX
	sql  string
	args []any
}

func (c *capture) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	c.sql, c.args = sql, args
	return c.DBTX.Query(ctx, sql, args...)
}

// planNode is a node of EXPLAIN (FORMAT JSON).
type planNode struct {
	NodeType string     `json:"Node Type"`
	Relation string     `json:"Relation Name"`
	Plans    []planNode `json:"Plans"`
}

// explain returns the plan of the query c captured.
func explain(t *testing.T, f *fixture, c *capture) planNode {
	t.Helper()
	if c.sql == "" {
		t.Fatal("no query captured")
	}
	var out []byte
	check(t, f.s.Pool.QueryRow(f.ctx, "EXPLAIN (FORMAT JSON) "+c.sql, c.args...).Scan(&out))
	var plans []struct {
		Plan planNode `json:"Plan"`
	}
	check(t, json.Unmarshal(out, &plans))
	if len(plans) != 1 {
		t.Fatalf("EXPLAIN returned %d plans", len(plans))
	}
	return plans[0].Plan
}

// pageTables returns the tables that the plan reads below a Limit node:
// before the page is cut, where the planner may read them for every
// candidate row (a hash join over the whole contest) rather than for
// the rows of the page.
func pageTables(n planNode, underLimit bool) []string {
	var out []string
	if underLimit && n.Relation != "" {
		out = append(out, n.Relation)
	}
	underLimit = underLimit || n.NodeType == "Limit"
	for _, c := range n.Plans {
		out = append(out, pageTables(c, underLimit)...)
	}
	sort.Strings(out)
	return slices.Compact(out)
}

// limitsAbove returns the fewest Limit nodes above a scan of table in
// the plan (-1 if the plan does not read it).
func limitsAbove(n planNode, table string, limits int) int {
	if n.NodeType == "Limit" {
		limits++
	}
	best := -1
	if n.Relation == table {
		best = limits
	}
	for _, c := range n.Plans {
		if l := limitsAbove(c, table, limits); l >= 0 && (best < 0 || l < best) {
			best = l
		}
	}
	return best
}

// TestPagesJoinAfterTheLimit checks the shape of the admin list pages.
// The page is chosen on the submissions (user tests) and their tasks
// only, and the participant, the result and the token are joined to the
// rows of the page (deferred join); the submissions are read task by
// task, each stopping at the page size (a Limit below the Limit of the
// page). A contest-wide page used to hash every result and token of the
// contest and sort all its submissions to keep 50 rows
// (BenchmarkAdminPages).
func TestPagesJoinAfterTheLimit(t *testing.T) {
	f, _ := newFixture(t)
	x := newSubmissionsFixture(f)
	cur := store.SubmissionCursor{Timestamp: t0.Add(4 * time.Minute), ID: x.s4.ID}

	for _, tc := range []struct {
		name   string
		filter store.SubmissionFilter
		// tables the page may read for every candidate row
		allowed []string
	}{
		{"contest", store.SubmissionFilter{ContestID: x.c.ID}, []string{"submissions", "tasks"}},
		{"contest, next page", store.SubmissionFilter{ContestID: x.c.ID, Before: &cur}, []string{"submissions", "tasks"}},
		{"task", store.SubmissionFilter{TaskID: x.a.ID}, []string{"submissions", "tasks"}},
		{"participation", store.SubmissionFilter{ParticipationID: x.bob.ID}, []string{"submissions", "tasks"}},
		{"dataset", store.SubmissionFilter{DatasetID: x.dsA2.ID}, []string{"datasets", "submissions", "tasks"}},
		// The status filter reads the result of every candidate.
		{"status", store.SubmissionFilter{ContestID: x.c.ID, Status: store.StatusScored},
			[]string{"submission_results", "submissions", "tasks"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &capture{DBTX: f.s.Pool}
			_ = must[[]store.SubmissionRow](t)(store.ListSubmissions(f.ctx, c, tc.filter))
			plan := explain(t, f, c)
			for _, table := range pageTables(plan, false) {
				if !slices.Contains(tc.allowed, table) {
					t.Errorf("the page reads %s before the LIMIT; want only %v", table, tc.allowed)
				}
			}
			if l := limitsAbove(plan, "submissions", 0); l < 2 {
				t.Errorf("the submissions are read under %d Limit nodes, want 2 (per task, then the page)", l)
			}
		})
	}

	t.Run("user tests", func(t *testing.T) {
		c := &capture{DBTX: f.s.Pool}
		_ = must[[]q.ListUserTestsByContestRow](t)(q.New(c).ListUserTestsByContest(f.ctx,
			q.ListUserTestsByContestParams{ContestID: x.c.ID, PageSize: store.DefaultPageSize}))
		for _, table := range pageTables(explain(t, f, c), false) {
			if table != "user_tests" && table != "tasks" {
				t.Errorf("the page reads %s before the LIMIT; want only user_tests and tasks", table)
			}
		}
	})
}
