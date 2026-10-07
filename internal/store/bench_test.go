// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package store_test

import (
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/store"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// Size of the contest of the benchmarks: the end of a large contest.
const (
	benchParticipants       = 300
	benchTasks              = 3
	benchSubmissionsPerTask = 50 // per participant: 45,000 submissions
	benchUserTestsPerTask   = 10 // per participant: 9,000 user tests
)

// bigContest fills a contest with benchParticipants participants who sent
// benchSubmissionsPerTask submissions to each of benchTasks tasks, every
// one scored on the active dataset, a token on one in ten, and user
// tests with results; then it updates the planner statistics.
func bigContest(tb testing.TB) (*fixture, q.Contest, []q.Task) {
	f, _ := newFixture(tb)
	c, g := f.contest("big")
	var tasks []q.Task
	for i := range benchTasks {
		tk, _ := f.task(c, int32(i), "t"+string(rune('a'+i)))
		tasks = append(tasks, tk)
	}
	for _, step := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO users (first_name, last_name, username, password)
SELECT 'First', 'Last', 'user' || i, 'plaintext:x' FROM generate_series(1, $1::integer) i`,
			[]any{benchParticipants}},
		{`INSERT INTO participations (contest_id, user_id, group_id) SELECT $1, u.id, $2 FROM users u`,
			[]any{c.ID, g.ID}},
		{`INSERT INTO submissions (opaque_id, participation_id, task_id, timestamp, language, official)
SELECT i * 100 + t.num, p.id, t.id, $2::timestamptz + (i * 10000 + p.id * 10 + t.num) * interval '1 millisecond',
       'C++20 / g++', true
FROM participations p CROSS JOIN tasks t CROSS JOIN generate_series(1, $3::integer) i
WHERE p.contest_id = $1 AND t.contest_id = $1`,
			[]any{c.ID, t0, benchSubmissionsPerTask}},
		{`INSERT INTO submission_results (submission_id, dataset_id, compilation_outcome, compilation_tries,
    evaluation_outcome, evaluation_tries, score, score_details, public_score, public_score_details,
    ranking_score_details, scored_at)
SELECT s.id, t.active_dataset_id, 'ok', 1, 'ok', 1, 100, '[]', 100, '[]', '{}', s.timestamp
FROM submissions s JOIN tasks t ON t.id = s.task_id`, nil},
		{`INSERT INTO tokens (submission_id, timestamp) SELECT id, timestamp FROM submissions WHERE id % 10 = 0`, nil},
		{`INSERT INTO user_tests (participation_id, task_id, timestamp, language, input)
SELECT p.id, t.id, $2::timestamptz + (i * 10000 + p.id * 10 + t.num) * interval '1 millisecond',
       'C++20 / g++', repeat('0', 64)
FROM participations p CROSS JOIN tasks t CROSS JOIN generate_series(1, $3::integer) i
WHERE p.contest_id = $1 AND t.contest_id = $1`,
			[]any{c.ID, t0, benchUserTestsPerTask}},
		{`INSERT INTO user_test_results (user_test_id, dataset_id, compilation_outcome, evaluation_outcome)
SELECT ut.id, t.active_dataset_id, 'ok', 'ok' FROM user_tests ut JOIN tasks t ON t.id = ut.task_id`, nil},
		{`ANALYZE`, nil},
	} {
		if _, err := f.s.Pool.Exec(f.ctx, step.sql, step.args...); err != nil {
			tb.Fatalf("seeding the contest: %v\n%s", err, step.sql)
		}
	}
	return f, c, tasks
}

// BenchmarkAdminPages measures the first page of the admin lists of a
// large contest (the pages admins reload during the contest).
func BenchmarkAdminPages(b *testing.B) {
	f, c, tasks := bigContest(b)
	for _, bc := range []struct {
		name   string
		filter store.SubmissionFilter
	}{
		{"submissions/contest", store.SubmissionFilter{ContestID: c.ID}},
		{"submissions/task", store.SubmissionFilter{TaskID: tasks[1].ID}},
		{"submissions/contest-scored", store.SubmissionFilter{ContestID: c.ID, Status: store.StatusScored}},
	} {
		b.Run(bc.name, func(b *testing.B) {
			for b.Loop() {
				rows, err := f.s.ListSubmissions(f.ctx, bc.filter)
				if err != nil || len(rows) != store.DefaultPageSize {
					b.Fatalf("%d rows, error %v", len(rows), err)
				}
			}
		})
	}
	b.Run("user-tests/contest", func(b *testing.B) {
		for b.Loop() {
			rows, err := f.q.ListUserTestsByContest(f.ctx, q.ListUserTestsByContestParams{ContestID: c.ID, PageSize: store.DefaultPageSize})
			if err != nil || len(rows) != store.DefaultPageSize {
				b.Fatalf("%d rows, error %v", len(rows), err)
			}
		}
	})
}
