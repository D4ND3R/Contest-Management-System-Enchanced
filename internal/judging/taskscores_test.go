// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package judging

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/scoring"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// groupTask is a task with two subtasks: 40 points for testcases 000 and
// 001 (public: the subtask counts for the public score), 60 points for
// 002 (private).
func (f *fx) groupTask(mode string) tk {
	return f.task(taskOpts{scoreMode: mode, precision: 2, dsOpts: dsOpts{
		scoreType: "GroupMin", scoreParams: `[[40, "00[01]"], [60, "002"]]`,
		testcases: []tc{{"000", true}, {"001", true}, {"002", false}},
	}})
}

// outcomes returns a verdict that gives each testcase the outcome in m
// (default "0.0").
func outcomes(m map[string]string) verdict {
	return func(j *job.Job) string {
		if j.Operation.Type.IsCompilation() {
			return job.CompilationOK
		}
		if o, ok := m[j.Operation.TestcaseCodename]; ok {
			return o
		}
		return "0.0"
	}
}

// expected computes the task score of p from the database the slow way:
// every submission with its full details, through upstream task_score.
func (f *fx) expected(p int32, task tk) TaskScore {
	f.t.Helper()
	rows, err := f.pool.Query(f.ctx, `SELECT s.timestamp, s.official, tk.id IS NOT NULL,
			sr.score IS NOT NULL, coalesce(sr.score, 0), sr.score_details, coalesce(sr.public_score, 0), sr.public_score_details,
			t.score_mode, t.score_precision, coalesce(t.active_dataset_id, 0)
		FROM submissions s JOIN tasks t ON t.id = s.task_id
		LEFT JOIN submission_results sr ON sr.submission_id = s.id AND sr.dataset_id = t.active_dataset_id
		LEFT JOIN tokens tk ON tk.submission_id = s.id
		WHERE s.participation_id = $1 AND s.task_id = $2 ORDER BY s.timestamp, s.id`, p, task.id)
	if err != nil {
		f.t.Fatal(err)
	}
	var subs []scoring.Submission
	var mode string
	var precision, ds int32
	for rows.Next() {
		var s scoring.Submission
		var official bool
		if err := rows.Scan(&s.Timestamp, &official, &s.Tokened, &s.Scored, &s.Score, &s.ScoreDetails,
			&s.PublicScore, &s.PublicScoreDetails, &mode, &precision, &ds); err != nil {
			f.t.Fatal(err)
		}
		s.Unofficial = !official
		subs = append(subs, s)
	}
	if err := rows.Err(); err != nil {
		f.t.Fatal(err)
	}
	if len(subs) == 0 {
		return TaskScore{DatasetID: task.dataset}
	}
	ts, err := computeTaskScore(subs, mode, int(precision))
	if err != nil {
		f.t.Fatal(err)
	}
	ts.DatasetID = ds
	return ts
}

func (f *fx) checkCache(p int32, task tk, want TaskScore) {
	f.t.Helper()
	got := f.cached(p, task)
	if got == nil {
		f.t.Errorf("no task score, want %+v", want)
		return
	}
	if *got != want {
		f.t.Errorf("task score %+v, want %+v", *got, want)
	}
	if slow := f.expected(p, task); slow != *got {
		f.t.Errorf("task score %+v, but upstream task_score gives %+v", *got, slow)
	}
}

func TestTaskScoreModes(t *testing.T) {
	type step struct {
		max, maxSubtask, maxTokenedLast TaskScore
	}
	f := newFx(t)
	p := f.participant("alice")
	tasks := map[string]tk{}
	for _, m := range []string{scoring.ModeMax, scoring.ModeMaxSubtask, scoring.ModeMaxTokenedLast} {
		tasks[m] = f.groupTask(m)
	}
	check := func(what string, want step) {
		t.Helper()
		for m, w := range map[string]TaskScore{scoring.ModeMax: want.max, scoring.ModeMaxSubtask: want.maxSubtask,
			scoring.ModeMaxTokenedLast: want.maxTokenedLast} {
			w.DatasetID = tasks[m].dataset
			f.t = t
			t.Run(what+"/"+m, func(t *testing.T) {
				f.t = t
				f.checkCache(p, tasks[m], w)
			})
			f.t = t
		}
	}
	subs := map[string][]int32{}
	submit := func(at time.Duration, v verdict) {
		for m, task := range tasks {
			s := f.submit(p, task, at)
			subs[m] = append(subs[m], s)
			if v != nil {
				f.judgeAll(Targets{SubmissionIDs: []int32{s}}, v)
			} else if _, err := f.j.RefreshTaskScoresOf(f.ctx, []int32{s}); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Subtask 1 only (40, public 40: its public testcase is right).
	submit(time.Minute, outcomes(map[string]string{"000": "1.0", "001": "1.0"}))
	check("first", step{
		max:            TaskScore{Score: 40, PublicScore: 40},
		maxSubtask:     TaskScore{Score: 40, PublicScore: 40},
		maxTokenedLast: TaskScore{Score: 40, PublicScore: 40},
	})
	// Subtask 2 only (60, public 0).
	submit(2*time.Minute, outcomes(map[string]string{"002": "1.0"}))
	check("second", step{
		max:            TaskScore{Score: 60, PublicScore: 40},
		maxSubtask:     TaskScore{Score: 100, PublicScore: 40},
		maxTokenedLast: TaskScore{Score: 60, PublicScore: 0},
	})
	// A token on the first submission.
	for _, m := range []string{scoring.ModeMax, scoring.ModeMaxSubtask, scoring.ModeMaxTokenedLast} {
		if ch := f.token(subs[m][0], 3*time.Minute); len(ch) != 1 || ch[0].New.TokenedScore != 40 {
			t.Errorf("%s: token changes %+v", m, ch)
		}
	}
	check("token", step{
		max:            TaskScore{Score: 60, PublicScore: 40, TokenedScore: 40},
		maxSubtask:     TaskScore{Score: 100, PublicScore: 40, TokenedScore: 40},
		maxTokenedLast: TaskScore{Score: 60, PublicScore: 40, TokenedScore: 40},
	})
	// A new submission, not judged yet: partial, and in max_tokened_last
	// the last submission counts as 0.
	submit(4*time.Minute, nil)
	check("pending", step{
		max:            TaskScore{Score: 60, PublicScore: 40, TokenedScore: 40, Partial: true},
		maxSubtask:     TaskScore{Score: 100, PublicScore: 40, TokenedScore: 40, Partial: true},
		maxTokenedLast: TaskScore{Score: 40, PublicScore: 40, TokenedScore: 40, Partial: true},
	})
	// It is judged: half of the first subtask (GroupMin takes the lowest
	// outcome: 20 points, public), nothing of the second.
	for m := range tasks {
		f.judgeAll(Targets{SubmissionIDs: subs[m][2:]}, outcomes(map[string]string{"000": "1.0", "001": "0.5"}))
	}
	check("judged", step{
		max:            TaskScore{Score: 60, PublicScore: 40, TokenedScore: 40},
		maxSubtask:     TaskScore{Score: 100, PublicScore: 40, TokenedScore: 40},
		maxTokenedLast: TaskScore{Score: 40, PublicScore: 40, TokenedScore: 40},
	})
	// An unofficial submission (analysis mode) does not count.
	for m, task := range tasks {
		s := f.submit(p, task, 5*time.Minute)
		f.exec(`UPDATE submissions SET official = false WHERE id = $1`, s)
		if _, err := f.j.RefreshTaskScoresOf(f.ctx, []int32{s}); err != nil {
			t.Fatal(err)
		}
		subs[m] = append(subs[m], s)
	}
	check("unofficial", step{
		max:            TaskScore{Score: 60, PublicScore: 40, TokenedScore: 40},
		maxSubtask:     TaskScore{Score: 100, PublicScore: 40, TokenedScore: 40},
		maxTokenedLast: TaskScore{Score: 40, PublicScore: 40, TokenedScore: 40},
	})
}

func TestTaskScorePrecision(t *testing.T) {
	f := newFx(t)
	task := f.task(taskOpts{precision: 1, dsOpts: dsOpts{scoreParams: "33.3333",
		testcases: []tc{{"000", true}, {"001", true}, {"002", true}}}})
	p := f.participant("alice")
	s := f.submit(p, task, time.Minute)
	f.judgeAll(Targets{SubmissionIDs: []int32{s}}, allCorrect)
	f.checkCache(p, task, TaskScore{DatasetID: task.dataset, Score: 100, PublicScore: 100})
}

func TestActivateDataset(t *testing.T) {
	f := newFx(t)
	task := f.task(taskOpts{}) // Sum 10 x 3
	other := f.dataset(task.id, dsOpts{autojudge: true, scoreParams: "20"})
	empty := f.dataset(task.id, dsOpts{})
	p := f.participant("alice")
	s := f.submit(p, task, time.Minute)
	f.judgeAll(Targets{SubmissionIDs: []int32{s}}, outcomes(map[string]string{"000": "1.0", "001": "1.0"}))
	f.checkCache(p, task, TaskScore{DatasetID: task.dataset, Score: 20, PublicScore: 10})
	if r := f.result(s, other); r.status() != StatusScored || *r.Score != 40 {
		t.Fatalf("autojudge dataset: %+v", r)
	}

	prev, ch, err := f.j.ActivateDataset(f.ctx, task.id, other)
	if err != nil || prev != task.dataset || len(ch) != 1 || ch[0].New.Score != 40 || ch[0].Old.Score != 20 {
		t.Fatalf("activate: %d %+v %v", prev, ch, err)
	}
	task.dataset = other
	f.checkCache(p, task, TaskScore{DatasetID: other, Score: 40, PublicScore: 20})

	// A dataset never judged: partial until the dispatcher judges it.
	if _, _, err = f.j.ActivateDataset(f.ctx, task.id, empty); err != nil {
		t.Fatal(err)
	}
	task.dataset = empty
	f.checkCache(p, task, TaskScore{DatasetID: empty, Partial: true})
	f.judgeAll(Targets{SubmissionIDs: []int32{s}}, allCorrect)
	f.checkCache(p, task, TaskScore{DatasetID: empty, Score: 30, PublicScore: 10})

	// Not a dataset of the task.
	if _, _, err := f.j.ActivateDataset(f.ctx, task.id, f.task(taskOpts{}).dataset); !errors.Is(err, ErrNotDataset) {
		t.Errorf("dataset of another task: %v", err)
	}
	if got := scalar[int32](f, `SELECT active_dataset_id FROM tasks WHERE id = $1`, task.id); got != empty {
		t.Errorf("active dataset %d", got)
	}
}

func TestRepairAndRecomputeTaskScores(t *testing.T) {
	f := newFx(t)
	task := f.task(taskOpts{scoreMode: scoring.ModeMaxTokenedLast})
	other := f.dataset(task.id, dsOpts{autojudge: true, scoreParams: "20"})
	var parts []int32
	for i := 0; i < 3; i++ {
		p := f.participant(fmt.Sprintf("user%d", i))
		parts = append(parts, p)
		s := f.submit(p, task, time.Minute)
		f.judgeAll(Targets{SubmissionIDs: []int32{s}}, allCorrect)
		f.submit(p, task, 2*time.Minute) // pending, last: score 0 in max_tokened_last
		if _, err := f.j.RefreshTaskScoresOf(f.ctx, []int32{s + 1}); err != nil {
			t.Fatal(err)
		}
	}
	f.checkCache(parts[0], task, TaskScore{DatasetID: task.dataset, Partial: true})

	// The active dataset is changed behind the back of the cache.
	f.exec(`UPDATE tasks SET active_dataset_id = $1 WHERE id = $2`, other, task.id)
	ch, err := f.j.RepairTaskScores(f.ctx, f.contest)
	if err != nil || len(ch) != 3 {
		t.Fatalf("repair: %+v %v", ch, err)
	}
	task.dataset = other
	f.checkCache(parts[0], task, TaskScore{DatasetID: other, Partial: true})
	if ch, err := f.j.RepairTaskScores(f.ctx, 0); err != nil || len(ch) != 0 {
		t.Errorf("second repair: %+v %v", ch, err)
	}

	// The score mode is changed by an admin, who recomputes in the same
	// transaction.
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(f.ctx, `UPDATE tasks SET score_mode = 'max' WHERE id = $1`, task.id); err != nil {
		t.Fatal(err)
	}
	ch, err = RecomputeTask(f.ctx, tx, task.id)
	if err != nil || len(ch) != 3 {
		t.Fatalf("recompute: %+v %v", ch, err)
	}
	if err := tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	f.checkCache(parts[2], task, TaskScore{DatasetID: other, Score: 60, PublicScore: 20, Partial: true})
}

// TestTaskScoreReads covers the queries the pages use to read the cache.
func TestTaskScoreReads(t *testing.T) {
	f := newFx(t)
	t1, t2 := f.task(taskOpts{}), f.task(taskOpts{})
	alice, bob := f.participant("alice"), f.participant("bob")
	for _, p := range []int32{alice, bob} {
		for _, task := range []tk{t1, t2} {
			f.judgeAll(Targets{SubmissionIDs: []int32{f.submit(p, task, time.Minute)}}, allCorrect)
		}
	}
	qq := q.New(f.pool)
	mine, err := qq.ListTaskScoresByParticipation(f.ctx, alice)
	if err != nil || len(mine) != 2 || mine[0].TaskID != t1.id || mine[1].Score != 30 {
		t.Errorf("by participation: %+v %v", mine, err)
	}
	one, err := qq.GetTaskScore(f.ctx, q.GetTaskScoreParams{ParticipationID: bob, TaskID: t2.id})
	if err != nil || one.Score != 30 || one.PublicScore != 10 || one.Partial || one.DatasetID == nil || *one.DatasetID != t2.dataset {
		t.Errorf("one: %+v %v", one, err)
	}
	if _, err := qq.GetTaskScore(f.ctx, q.GetTaskScoreParams{ParticipationID: bob, TaskID: f.task(taskOpts{}).id}); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("no row: %v", err)
	}
	all, err := qq.ListTaskScoresByContest(f.ctx, &f.contest)
	if err != nil || len(all) != 4 || all[0].ParticipationID != alice || all[3].ParticipationID != bob {
		t.Errorf("by contest: %+v %v", all, err)
	}
}

// TestSubtaskScoresFunction: the max_subtask mode gives the same score on
// the details compacted by cms_subtask_scores (what the cache reads) as
// on the full details.
func TestSubtaskScoresFunction(t *testing.T) {
	f := newFx(t)
	details := []string{
		`[]`,
		`[{"idx": 1, "score": 40.5, "max_score": 50, "testcases": [{"idx": "a"}]}, {"idx": 2, "score": 0, "testcases": []}]`,
		`[{"idx": "x", "score": 10}, {"idx": 1.0, "score": 5}, {"idx": true, "score": 7}]`,
		`[{"idx": 1, "score": 1}, {"score": 2}]`, // not every subtask has an idx
		`[{"idx": 1, "score": "1"}]`,             // a score that is not a number
		`[{"idx": [1], "score": 1}]`,             // an unhashable idx
		`[{"idx": null, "score": 3}, {"idx": null, "score": 4}]`,
		`[{"idx": "0", "outcome": "Correct"}, {"idx": "1"}]`, // Sum
		`[1, 2]`,
		`{"a": 1}`,
		`"text"`,
	}
	for i, d := range details {
		var compact json.RawMessage
		if err := f.pool.QueryRow(f.ctx, `SELECT cms_subtask_scores($1::jsonb)`, d).Scan(&compact); err != nil {
			t.Fatalf("%s: %v", d, err)
		}
		// The details of two submissions, one of them this one.
		full := []scoring.Submission{
			{Timestamp: t0, Scored: true, Score: 12, ScoreDetails: json.RawMessage(d)},
			{Timestamp: t0.Add(time.Second), Scored: true, Score: 3,
				ScoreDetails: json.RawMessage(`[{"idx": 1, "score": 3}, {"idx": 2, "score": 30}]`)},
		}
		var c json.RawMessage
		if err := f.pool.QueryRow(f.ctx, `SELECT cms_subtask_scores($1::jsonb)`, string(full[1].ScoreDetails)).Scan(&c); err != nil {
			t.Fatal(err)
		}
		compacted := []scoring.Submission{full[0], full[1]}
		compacted[0].ScoreDetails, compacted[1].ScoreDetails = compact, c
		want, _, err1 := scoring.TaskScore(full, scoring.ModeMaxSubtask, 2, scoring.Full)
		got, _, err2 := scoring.TaskScore(compacted, scoring.ModeMaxSubtask, 2, scoring.Full)
		if err1 != nil || err2 != nil || got != want {
			t.Errorf("case %d %s: compact %s gives %v, full gives %v (%v %v)", i, d, compact, got, want, err1, err2)
		}
	}
	var null *string
	if err := f.pool.QueryRow(f.ctx, `SELECT cms_subtask_scores(NULL)::text`).Scan(&null); err != nil || null != nil {
		t.Errorf("NULL: %v %v", null, err)
	}
}

// TestRefreshTaskScoresConstantQueries: recomputing the scores of 1 or 50
// participants takes the same number of queries.
func TestRefreshTaskScoresConstantQueries(t *testing.T) {
	count := func(n int) int64 {
		f := newFx(t)
		task := f.groupTask(scoring.ModeMaxSubtask)
		var subs []int32
		for i := 0; i < n; i++ {
			p := f.participant(fmt.Sprintf("user%d", i))
			for k := 0; k < 3; k++ {
				subs = append(subs, f.submit(p, task, time.Duration(k)*time.Minute))
			}
		}
		f.judgeAll(Targets{SubmissionIDs: subs}, allCorrect)
		f.exec(`UPDATE task_scores SET score = -1`)
		f.qc.Reset()
		ch, err := f.j.RefreshTaskScoresOf(f.ctx, subs)
		if err != nil || len(ch) != n {
			t.Fatalf("n=%d: %d changes, %v", n, len(ch), err)
		}
		return f.qc.Count()
	}
	if c1, c50 := count(1), count(50); c1 != c50 {
		t.Errorf("queries for 1 and 50 participants: %d, %d", c1, c50)
	}
}
