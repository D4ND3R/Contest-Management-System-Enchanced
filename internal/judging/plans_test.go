// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package judging

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// capture is a q.DBTX that remembers the last query it ran, to EXPLAIN it
// afterwards with the same arguments.
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
	Index    string     `json:"Index Name"`
	Plans    []planNode `json:"Plans"`
}

func (f *fx) explain(c *capture) planNode {
	f.t.Helper()
	if c.sql == "" {
		f.t.Fatal("no query captured")
	}
	var out []byte
	if err := f.pool.QueryRow(f.ctx, "EXPLAIN (FORMAT JSON) "+c.sql, c.args...).Scan(&out); err != nil {
		f.t.Fatal(err)
	}
	var plans []struct {
		Plan planNode `json:"Plan"`
	}
	if err := json.Unmarshal(out, &plans); err != nil || len(plans) != 1 {
		f.t.Fatalf("EXPLAIN: %s, %v", out, err)
	}
	return plans[0].Plan
}

// walk calls fn on every node of the plan.
func (n planNode) walk(fn func(planNode)) {
	fn(n)
	for _, c := range n.Plans {
		c.walk(fn)
	}
}

// TestSweepAndScorePlans checks, on a contest of 300 participants with
// 30 000 submissions and 30 000 user tests judged on two datasets, that
// the queries the dispatcher runs all the time never read a whole table:
// a sweep step reads the results of its window only, through the primary
// key and the partial indexes of migration 0004 (the scored results stay
// out of them), and the task score recomputation reads the submissions of
// one participant on one task.
func TestSweepAndScorePlans(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a large contest")
	}
	f := newFx(t)
	task := f.task(taskOpts{})
	f.dataset(task.id, dsOpts{autojudge: true})
	p := f.participant("alice")
	f.submit(p, task, time.Minute)
	f.exec(`INSERT INTO users (first_name, last_name, username, password)
		SELECT 'u', 'u', 'user' || g, 'plaintext:x' FROM generate_series(1, 299) g`)
	f.exec(`INSERT INTO participations (contest_id, user_id, group_id)
		SELECT $1, u.id, $2 FROM users u WHERE u.username LIKE 'user%'`, f.contest, f.group)
	f.exec(`INSERT INTO submissions (opaque_id, participation_id, task_id, timestamp, language)
		SELECT 1000 + g, p.id, $1, $2::timestamptz + g * interval '1 second', 'C++17 / g++'
		FROM generate_series(1, 100) g, participations p`, task.id, t0)
	f.exec(`INSERT INTO submission_results (submission_id, dataset_id, compilation_outcome, evaluation_outcome,
			score, score_details, public_score, public_score_details, ranking_score_details)
		SELECT s.id, d.id, 'ok', 'ok', 10, '[]', 5, '[]', '{}'
		FROM submissions s JOIN datasets d ON d.task_id = s.task_id`)
	f.exec(`UPDATE submission_results SET score = NULL WHERE submission_id % 1000 = 0`)
	f.exec(`INSERT INTO tokens (submission_id, timestamp) SELECT id, timestamp FROM submissions WHERE id % 10 = 0`)
	f.exec(`INSERT INTO user_tests (participation_id, task_id, timestamp, language, input)
		SELECT p.id, $1, $2, 'C++17 / g++', $3 FROM generate_series(1, 100), participations p`, task.id, t0, digestOf("input"))
	f.exec(`INSERT INTO user_test_results (user_test_id, dataset_id, compilation_outcome, evaluation_outcome)
		SELECT ut.id, d.id, 'ok', 'ok' FROM user_tests ut JOIN datasets d ON d.task_id = ut.task_id`)
	f.exec(`VACUUM ANALYZE`)

	// check fails if the plan reads a whole table (but the small ones),
	// or does not use an index of each group of wantIndexes.
	check := func(name string, c *capture, wantIndexes ...[]string) {
		t.Helper()
		used := map[string]bool{}
		f.explain(c).walk(func(n planNode) {
			if n.NodeType == "Seq Scan" && n.Relation != "tasks" && n.Relation != "datasets" {
				t.Errorf("%s reads the whole %s table", name, n.Relation)
			}
			if n.Index != "" {
				used[n.Index] = true
			}
		})
		for _, group := range wantIndexes {
			if !slices.ContainsFunc(group, func(ix string) bool { return used[ix] }) {
				t.Errorf("%s uses none of %v (uses %v)", name, group, used)
			}
		}
	}

	c := &capture{DBTX: f.pool}
	if _, err := q.New(c).JudgingSweepSubmissions(f.ctx, q.JudgingSweepSubmissionsParams{
		After: 10000, Upto: 15000, MaxRows: 500, MaxCompilationTries: 3, MaxEvaluationTries: 3,
	}); err != nil {
		t.Fatal(err)
	}
	check("a sweep step on submissions", c, []string{"ix_submission_results_unfinished"}, []string{"submission_results_pkey"})

	c = &capture{DBTX: f.pool}
	if _, err := q.New(c).JudgingSweepUserTests(f.ctx, q.JudgingSweepUserTestsParams{
		After: 10000, Upto: 15000, MaxRows: 500, MaxCompilationTries: 3, MaxEvaluationTries: 3,
	}); err != nil {
		t.Fatal(err)
	}
	check("a sweep step on user tests", c, []string{"ix_user_test_results_unfinished"}, []string{"user_test_results_pkey"})

	c = &capture{DBTX: f.pool}
	if _, err := q.New(c).JudgingTaskScoreInputs(f.ctx, q.JudgingTaskScoreInputsParams{
		ParticipationIds: []int32{p, p + 1}, TaskIds: []int32{task.id, task.id},
	}); err != nil {
		t.Fatal(err)
	}
	// The submissions of the participant: by the index on (participation,
	// task) or the unique (participation, opaque id).
	check("the task score inputs", c,
		[]string{"ix_submissions_participation_id_task_id", "participation_opaque_unique"},
		[]string{"submission_results_pkey"}, []string{"tokens_submission_id_key"})

	// A whole sweep stays fast (the time is reported, not checked).
	start := time.Now()
	opts := SweepOptions{ContestID: f.contest}
	steps := 0
	for {
		res, err := f.j.Sweep(f.ctx, opts)
		if err != nil {
			t.Fatal(err)
		}
		steps++
		if res.Done {
			break
		}
		opts.Cursor = res.Next
	}
	t.Logf("a sweep of 30 000 submissions and 30 000 user tests: %d steps in %v", steps, time.Since(start))
}
