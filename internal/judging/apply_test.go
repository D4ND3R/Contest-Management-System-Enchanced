// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package judging

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
)

func (f *fx) apply(results ...*job.Result) *Changes {
	f.t.Helper()
	ch, err := f.j.Apply(f.ctx, results)
	if err != nil {
		f.t.Fatal(err)
	}
	return ch
}

// snapshot returns every row of the results, evaluations and executables
// of a submission and its task scores, to compare states.
func (f *fx) snapshot(sub int32) string {
	f.t.Helper()
	return scalar[string](f, `SELECT jsonb_build_array(
		(SELECT jsonb_agg(to_jsonb(sr) ORDER BY dataset_id) FROM submission_results sr WHERE submission_id = $1),
		(SELECT jsonb_agg(to_jsonb(e) - 'id' ORDER BY dataset_id, testcase_id) FROM evaluations e WHERE submission_id = $1),
		(SELECT jsonb_agg(to_jsonb(x) - 'id' ORDER BY dataset_id, filename) FROM executables x WHERE submission_id = $1),
		(SELECT jsonb_agg(to_jsonb(ts) - 'updated_at' ORDER BY task_id) FROM task_scores ts
		 WHERE participation_id = (SELECT participation_id FROM submissions WHERE id = $1)))::text`, sub)
}

func TestApplyJudgesASubmission(t *testing.T) {
	f := newFx(t)
	task := f.task(taskOpts{}) // Sum 10, testcase 000 public
	p := f.participant("alice")
	s := f.submit(p, task, time.Minute)

	all := f.judgeAll(Targets{SubmissionIDs: []int32{s}}, allCorrect)
	r := f.result(s, task.dataset)
	if r.status() != StatusScored || *r.Score != 30 || *r.PublicScore != 10 || r.Evaluations != 3 ||
		r.Executables != 1 || *r.EvaluationOutcome != "ok" || r.CompilationTries != 1 || r.EvaluationTries != 0 {
		t.Fatalf("result %+v", r)
	}
	// What the Changes said along the way.
	var statuses []Status
	var last SubmissionChange
	for _, ch := range all {
		for _, c := range ch.Submissions {
			statuses = append(statuses, c.Status)
			last = c
		}
	}
	if !slices.Equal(statuses, []Status{StatusEvaluating, StatusScored}) {
		t.Errorf("statuses %v", statuses)
	}
	if last.Score != 30 || last.PublicScore != 10 || !last.Active || !last.Official || last.ParticipationID != p ||
		last.TaskID != task.id || last.ContestID != f.contest || !last.Timestamp.Equal(t0.Add(time.Minute)) ||
		last.RankingScoreDetails == nil {
		t.Errorf("last change %+v", last)
	}
	if got := f.cached(p, task); got == nil || *got != (TaskScore{DatasetID: task.dataset, Score: 30, PublicScore: 10}) {
		t.Errorf("task score %+v", got)
	}
	// The stored columns, as CMS stores them.
	row := scalar[string](f, `SELECT to_jsonb(sr) - 'scored_at' FROM submission_results sr WHERE submission_id = $1`, s)
	var cols map[string]any
	_ = json.Unmarshal([]byte(row), &cols)
	for k, want := range map[string]any{
		"compilation_text": []any{"Compilation succeeded"}, "compilation_stdout": "out", "compilation_stderr": "err",
		"compilation_time": 0.25, "compilation_wall_clock_time": 0.5, "compilation_memory": float64(8 << 20),
		"compilation_shard": nil, "ranking_score_details": []any{}, "generation": float64(0),
	} {
		if g, _ := json.Marshal(cols[k]); string(g) != mustJSON(want) {
			t.Errorf("%s = %s, want %s", k, g, mustJSON(want))
		}
	}
	ev := scalar[string](f, `SELECT to_jsonb(e) - 'id' - 'testcase_id' - 'submission_id' - 'dataset_id'
		FROM evaluations e JOIN testcases tc ON tc.id = e.testcase_id WHERE e.submission_id = $1 AND tc.codename = '001'`, s)
	if !sameJSON(ev, `{"text": ["Output is correct"], "outcome": "1.0", "admin_text": "checker said 1.0",
		"execution_time": 0.25, "execution_memory": 8388608, "evaluation_shard": null, "evaluation_sandbox_paths": null,
		"execution_wall_clock_time": 0.5, "evaluation_sandbox_digests": null}`) {
		t.Errorf("evaluation %s", ev)
	}
}

// sameJSON compares two JSON documents as values.
func sameJSON(a, b string) bool {
	var x, y any
	if json.Unmarshal([]byte(a), &x) != nil || json.Unmarshal([]byte(b), &y) != nil {
		return false
	}
	return mustJSON(x) == mustJSON(y)
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestApplyRedeliveryIsNoOp(t *testing.T) {
	f := newFx(t)
	task := f.task(taskOpts{})
	p := f.participant("alice")
	s := f.submit(p, task, time.Minute)
	b := f.build(f.pendingOps(Targets{SubmissionIDs: []int32{s}}))
	comp := resultOf(b.Jobs[0], job.CompilationOK)
	ch := f.apply(comp)
	before := f.snapshot(s)
	// The same compilation again, alone and twice in one batch.
	ch2 := f.apply(comp, comp)
	if len(ch2.Dropped) != 2 || ch2.Dropped[0].Reason != DropDuplicate || len(ch2.Next) != 0 || len(ch2.Submissions) != 0 {
		t.Errorf("repeated compilation: %+v", ch2)
	}
	if after := f.snapshot(s); after != before {
		t.Errorf("a repeated compilation changed the database:\n%s\n%s", before, after)
	}
	// A different compilation outcome for the same job: the first wins.
	if ch := f.apply(resultOf(b.Jobs[0], job.CompilationFail)); len(ch.Dropped) != 1 || f.snapshot(s) != before {
		t.Error("a second outcome overwrote the first")
	}

	// The evaluations, twice in the same batch and then again.
	b = f.build(ch.Next)
	var evals []*job.Result
	for _, jb := range b.Jobs {
		evals = append(evals, resultOf(jb, "1.0"))
	}
	ch = f.apply(append(slices.Clone(evals), evals...)...)
	if len(ch.Dropped) != 3 {
		t.Errorf("duplicates in a batch: %+v", ch.Dropped)
	}
	before = f.snapshot(s)
	ch = f.apply(evals...)
	if len(ch.Dropped) != 3 || len(ch.Submissions) != 0 || len(ch.TaskScores) != 0 || f.snapshot(s) != before {
		t.Errorf("repeated evaluations: %+v", ch)
	}
	if r := f.result(s, task.dataset); r.status() != StatusScored || *r.Score != 30 {
		t.Errorf("result %+v", r)
	}
}

func TestApplyDropsStaleResults(t *testing.T) {
	f := newFx(t)
	task := f.task(taskOpts{})
	p := f.participant("alice")
	s := f.submit(p, task, time.Minute)
	b := f.build(f.pendingOps(Targets{SubmissionIDs: []int32{s}}))
	old := b.Jobs[0]
	// Invalidated while the job is in flight.
	if _, err := f.j.Invalidate(f.ctx, Scope{SubmissionIDs: []int32{s}}, LevelCompilation); err != nil {
		t.Fatal(err)
	}
	ch := f.apply(resultOf(old, job.CompilationOK))
	if len(ch.Dropped) != 1 || ch.Dropped[0].Reason != DropStale || len(ch.Next) != 0 {
		t.Fatalf("stale result: %+v", ch)
	}
	if r := f.result(s, task.dataset); r.CompilationOutcome != nil || r.Generation != 1 || r.Executables != 0 {
		t.Fatalf("a stale result was stored: %+v", r)
	}
	// The new job is applied.
	b = f.build(f.pendingOps(Targets{SubmissionIDs: []int32{s}}))
	if b.Jobs[0].Generation != 1 {
		t.Fatalf("new job of generation %d", b.Jobs[0].Generation)
	}
	ch = f.apply(resultOf(b.Jobs[0], job.CompilationOK))
	if len(ch.Dropped) != 0 || len(ch.Next) != 3 {
		t.Fatalf("new result: %+v", ch)
	}
	// Evaluations in flight during a reevaluation are dropped too.
	b = f.build(ch.Next)
	if _, err := f.j.Invalidate(f.ctx, Scope{SubmissionIDs: []int32{s}}, LevelEvaluation); err != nil {
		t.Fatal(err)
	}
	ch = f.apply(resultOf(b.Jobs[0], "1.0"), resultOf(b.Jobs[1], ""))
	if len(ch.Dropped) != 2 || ch.Dropped[0].Reason != DropStale || ch.Dropped[1].Reason != DropStale {
		t.Fatalf("stale evaluations: %+v", ch.Dropped)
	}
	if r := f.result(s, task.dataset); r.Evaluations != 0 || r.EvaluationTries != 0 || r.Generation != 2 {
		t.Fatalf("result %+v", r)
	}
}

func TestApplyCountsFailures(t *testing.T) {
	f := newFx(t)
	task := f.task(taskOpts{})
	p := f.participant("alice")
	s := f.submit(p, task, time.Minute)
	ops := f.pendingOps(Targets{SubmissionIDs: []int32{s}})
	for attempt := 1; attempt <= MaxCompilationTries; attempt++ {
		b := f.build(ops)
		if len(b.Jobs) != 1 || b.Jobs[0].Attempt != attempt {
			t.Fatalf("attempt %d: %+v", attempt, b.Jobs)
		}
		fail := resultOf(b.Jobs[0], "")
		ch := f.apply(fail, fail) // the failure is delivered twice
		if len(ch.Dropped) != 1 || ch.Dropped[0].Reason != DropDuplicate {
			t.Errorf("attempt %d: repeated failure %+v", attempt, ch.Dropped)
		}
		if r := f.result(s, task.dataset); r.CompilationTries != int32(attempt) || r.status() != StatusCompiling {
			t.Errorf("attempt %d: %+v", attempt, r)
		}
		if attempt < MaxCompilationTries {
			sameKeys(t, "retry", opKeys(ch.Next), []string{
				fmt.Sprintf("compile/%d/%d p%d a%d retry", s, task.dataset, PriorityMedium, attempt+1)})
		} else if len(ch.Next) != 0 || len(ch.GaveUp) != 1 || ch.GaveUp[0].Tries != MaxCompilationTries {
			t.Errorf("last attempt: next %v, gave up %v", opKeys(ch.Next), ch.GaveUp)
		}
		ops = ch.Next
	}
	if w, _ := f.j.Pending(f.ctx, Targets{SubmissionIDs: []int32{s}}); len(w.Ops) != 0 || len(w.GaveUp) != 1 {
		t.Errorf("after giving up: %+v", w)
	}

	// Evaluations: a failing testcase is retried alone, at low priority.
	s = f.submit(p, task, 2*time.Minute)
	b := f.build(f.pendingOps(Targets{SubmissionIDs: []int32{s}}))
	ch := f.apply(resultOf(b.Jobs[0], job.CompilationOK))
	b = f.build(ch.Next)
	ch = f.apply(resultOf(b.Jobs[0], "1.0"), resultOf(b.Jobs[1], ""), resultOf(b.Jobs[2], ""))
	if r := f.result(s, task.dataset); r.EvaluationTries != 1 || r.Evaluations != 1 {
		t.Fatalf("result %+v", r)
	}
	sameKeys(t, "evaluation retries", opKeys(ch.Next), []string{
		fmt.Sprintf("evaluate/%d/%d/001 p%d a2 retry", s, task.dataset, PriorityLow),
		fmt.Sprintf("evaluate/%d/%d/002 p%d a2 retry", s, task.dataset, PriorityLow),
	})
	// The retries succeed: the submission is scored.
	b = f.build(ch.Next)
	if b.Jobs[0].Attempt != 2 {
		t.Errorf("retry attempt %d", b.Jobs[0].Attempt)
	}
	ch = f.apply(resultOf(b.Jobs[0], "1.0"), resultOf(b.Jobs[1], "0.0"))
	if r := f.result(s, task.dataset); r.status() != StatusScored || *r.Score != 20 || r.EvaluationTries != 1 {
		t.Errorf("result %+v", r)
	}
	if len(ch.Next) != 0 {
		t.Errorf("next %v", opKeys(ch.Next))
	}
}

func TestApplyCompilationFailedAndEmptyDatasets(t *testing.T) {
	f := newFx(t)
	task := f.task(taskOpts{})
	empty := f.task(taskOpts{dsOpts: dsOpts{testcases: []tc{}}})
	p := f.participant("alice")
	s1 := f.submit(p, task, time.Minute)
	s2 := f.submit(p, empty, time.Minute)
	f.judgeAll(Targets{SubmissionIDs: []int32{s1}}, func(*job.Job) string { return job.CompilationFail })
	f.judgeAll(Targets{SubmissionIDs: []int32{s2}}, allCorrect)
	r := f.result(s1, task.dataset)
	if r.status() != StatusCompilationFailed || *r.Score != 0 || r.Evaluations != 0 || r.Executables != 0 {
		t.Errorf("compilation failed: %+v", r)
	}
	details := scalar[string](f, `SELECT jsonb_build_array(score_details, public_score_details, ranking_score_details)::text
		FROM submission_results WHERE submission_id = $1`, s1)
	if details != `[[], [], []]` {
		t.Errorf("details of a submission that did not compile: %s", details)
	}
	if got := f.cached(p, task); got == nil || got.Partial || got.Score != 0 {
		t.Errorf("task score %+v", got)
	}
	// A dataset without testcases is evaluated as soon as it compiles.
	if r := f.result(s2, empty.dataset); r.status() != StatusScored || *r.Score != 0 || *r.EvaluationOutcome != "ok" {
		t.Errorf("no testcases: %+v", r)
	}
}

func TestApplyTombstone(t *testing.T) {
	f := newFx(t)
	task := f.task(taskOpts{})
	p := f.participant("alice")
	s := f.submit(p, task, time.Minute)
	b := f.build(f.pendingOps(Targets{SubmissionIDs: []int32{s}}))
	ch := f.apply(resultOf(b.Jobs[0], job.CompilationOK))
	b = f.build(ch.Next)

	tomb := func(jb *job.Job) *job.Result {
		r := resultOf(jb, "")
		r.Tombstone = true
		return r
	}
	// The testcase input is gone: a failure like any other.
	ch = f.apply(tomb(b.Jobs[0]))
	if r := f.result(s, task.dataset); r.EvaluationTries != 1 || r.Generation != 0 {
		t.Fatalf("result %+v", r)
	}
	// The executable is the tombstone (cleaned files): recompile.
	f.exec(`UPDATE executables SET digest = 'x' WHERE submission_id = $1`, s)
	ch = f.apply(tomb(b.Jobs[1]), resultOf(b.Jobs[2], "1.0"))
	r := f.result(s, task.dataset)
	if r.CompilationOutcome != nil || r.Generation != 1 || r.Executables != 0 || r.Evaluations != 0 || r.EvaluationTries != 0 {
		t.Fatalf("after a tombstone executable: %+v", r)
	}
	sameKeys(t, "recompilation", opKeys(ch.Next), []string{fmt.Sprintf("compile/%d/%d p%d a1", s, task.dataset, PriorityHigh)})
	if len(ch.Dropped) != 1 || ch.Dropped[0].Reason != DropStale {
		t.Errorf("the evaluation after the recompilation must be dropped: %+v", ch.Dropped)
	}
	if len(ch.Submissions) != 1 || ch.Submissions[0].Status != StatusCompiling {
		t.Errorf("changes %+v", ch.Submissions)
	}
	f.judge(ch.Next, allCorrect)
	if r := f.result(s, task.dataset); r.status() != StatusScored || *r.Score != 30 {
		t.Errorf("rejudged: %+v", r)
	}
}

func TestApplyGoneAndInvalid(t *testing.T) {
	f := newFx(t)
	task := f.task(taskOpts{})
	p := f.participant("alice")
	s := f.submit(p, task, time.Minute)
	u := f.userTest(p, task, time.Minute, nil)
	b := f.build(f.pendingOps(Targets{SubmissionIDs: []int32{s}, UserTestIDs: []int32{u}}))
	f.exec(`DELETE FROM submissions WHERE id = $1`, s)
	f.exec(`DELETE FROM user_tests WHERE id = $1`, u)
	bad := resultOf(b.Jobs[0], job.CompilationOK)
	bad.CompilationOutcome = "maybe"
	ch := f.apply(resultOf(b.Jobs[0], job.CompilationOK), resultOf(b.Jobs[1], job.CompilationOK), bad, nil)
	var reasons []DropReason
	for _, d := range ch.Dropped {
		reasons = append(reasons, d.Reason)
	}
	if !slices.Equal(reasons, []DropReason{DropInvalid, DropGone, DropGone}) || !errors.Is(ch.Dropped[0].Err, job.ErrInvalid) {
		t.Errorf("dropped %+v", ch.Dropped)
	}
}

func TestApplyUserTests(t *testing.T) {
	f := newFx(t)
	task := f.task(taskOpts{})
	auto := f.dataset(task.id, dsOpts{autojudge: true})
	p := f.participant("alice")
	u := f.userTest(p, task, time.Minute, nil)

	// Compilation fails twice, then works; the execution fails once.
	compiles, evals := 0, 0
	all := f.judge(f.pendingOps(Targets{UserTestIDs: []int32{u}}), func(j *job.Job) string {
		if j.Operation.Type == job.UserTestCompilation {
			if compiles++; compiles <= 2 {
				return ""
			}
			return job.CompilationOK
		}
		if evals++; evals == 1 {
			return ""
		}
		return "ok"
	})
	var statuses []UserTestStatus
	var next []string
	for _, ch := range all {
		for _, c := range ch.UserTests {
			statuses = append(statuses, c.Status)
		}
		next = append(next, opKeys(ch.Next)...)
	}
	if !slices.Equal(statuses, []UserTestStatus{UserTestEvaluating, UserTestEvaluating, UserTestEvaluated, UserTestEvaluated}) {
		t.Errorf("statuses %v", statuses)
	}
	sameKeys(t, "operations", next, []string{
		fmt.Sprintf("compile_test/%d/%d p%d a2 retry", u, task.dataset, PriorityMedium),
		fmt.Sprintf("compile_test/%d/%d p%d a2 retry", u, auto, PriorityExtraLow),
		fmt.Sprintf("evaluate_test/%d/%d p%d a1", u, task.dataset, PriorityMedium),
		fmt.Sprintf("evaluate_test/%d/%d p%d a1", u, auto, PriorityExtraLow),
		fmt.Sprintf("evaluate_test/%d/%d p%d a2 retry", u, task.dataset, PriorityLow),
	})
	row := scalar[string](f, `SELECT jsonb_build_array(compilation_outcome, compilation_tries, evaluation_outcome,
			evaluation_tries, evaluation_text, output, execution_time,
			(SELECT count(*) FROM user_test_executables e WHERE e.user_test_id = r.user_test_id AND e.dataset_id = r.dataset_id))::text
		FROM user_test_results r WHERE user_test_id = $1 AND dataset_id = $2`, u, auto)
	want := mustJSON([]any{"ok", 2, "ok", 1, []string{"Execution completed successfully"},
		digestOf(fmt.Sprintf("output of evaluate_test/%d/%d", u, auto)), 0.25, 1})
	if !sameJSON(row, want) {
		t.Errorf("user test result %s, want %s", row, want)
	}
	// Repeated results change nothing.
	bj := f.build([]Operation{{Operation: job.Operation{Type: job.UserTestEvaluation, ObjectID: u, DatasetID: auto}}})
	if len(bj.Skipped) != 1 || bj.Skipped[0].Reason != SkipDone {
		t.Errorf("evaluated user test: %+v", bj)
	}
}

func TestApplyNotJudgedDataset(t *testing.T) {
	f := newFx(t)
	task := f.task(taskOpts{})
	auto := f.dataset(task.id, dsOpts{autojudge: true})
	p := f.participant("alice")
	s := f.submit(p, task, time.Minute)
	b := f.build(f.pendingOps(Targets{SubmissionIDs: []int32{s}}))
	// Autojudge is switched off while the job is in flight: the result is
	// kept, but nothing more is scheduled on the dataset (upstream only
	// enqueues for datasets to judge).
	f.exec(`UPDATE datasets SET autojudge = false WHERE id = $1`, auto)
	ch := f.apply(resultOf(b.Jobs[0], job.CompilationOK), resultOf(b.Jobs[1], job.CompilationOK))
	for _, o := range ch.Next {
		if o.DatasetID == auto {
			t.Errorf("operation on a dataset not judged: %s", o.Key())
		}
	}
	if r := f.result(s, auto); r == nil || r.CompilationOutcome == nil {
		t.Errorf("the result on the dataset was not stored: %+v", r)
	}
	if len(ch.Next) != 3 {
		t.Errorf("next %v", opKeys(ch.Next))
	}
}

// TestApplyConstantQueries: applying 1 or 50 results takes the same number
// of queries, compilations and evaluations that complete and get scored
// included (N2).
func TestApplyConstantQueries(t *testing.T) {
	count := func(n int) (compile, evaluate int64) {
		f := newFx(t)
		task := f.task(taskOpts{scoreMode: "max_subtask", dsOpts: dsOpts{scoreType: "GroupMin",
			scoreParams: `[[40, "00[01]"], [60, "002"]]`}})
		var subs []int32
		for i := 0; i < n; i++ {
			p := f.participant(fmt.Sprintf("user%d", i))
			subs = append(subs, f.submit(p, task, time.Duration(i)*time.Second))
		}
		b := f.build(f.pendingOps(Targets{SubmissionIDs: subs}))
		results := make([]*job.Result, len(b.Jobs))
		for i, jb := range b.Jobs {
			results[i] = resultOf(jb, job.CompilationOK)
		}
		f.qc.Reset()
		ch := f.apply(results...)
		compile = f.qc.Count()
		b = f.build(ch.Next)
		results = results[:0]
		for _, jb := range b.Jobs {
			results = append(results, resultOf(jb, "1.0"))
		}
		f.qc.Reset()
		ch = f.apply(results...)
		evaluate = f.qc.Count()
		if len(ch.TaskScores) != n || len(ch.Submissions) != n {
			t.Fatalf("n=%d: %d task scores, %d submissions changed", n, len(ch.TaskScores), len(ch.Submissions))
		}
		return
	}
	c1, e1 := count(1)
	c50, e50 := count(50)
	if c1 != c50 || e1 != e50 {
		t.Errorf("queries for 1 and 50 results: compilations %d/%d, evaluations %d/%d", c1, c50, e1, e50)
	}
	t.Logf("queries per Apply: compilations %d, evaluations with scoring %d", c1, e1)
}
