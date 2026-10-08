// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package judging

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/scoring"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
)

// concurrently runs the functions at the same time and returns their
// errors.
func concurrently(fns ...func() error) []error {
	errs := make([]error, len(fns))
	var start, done sync.WaitGroup
	start.Add(1)
	for i, fn := range fns {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			errs[i] = fn()
		}()
	}
	start.Done()
	done.Wait()
	return errs
}

// TestRacingAppliersSameSubmission: two dispatchers apply the results of
// the same submission at the same time (different testcases, and the same
// ones twice): every evaluation is stored once, the evaluation ends, the
// submission is scored once and the task score is right.
func TestRacingAppliersSameSubmission(t *testing.T) {
	f := newFx(t)
	var tcs []tc
	for i := 0; i < 8; i++ {
		tcs = append(tcs, tc{fmt.Sprintf("%03d", i), i == 0})
	}
	task := f.task(taskOpts{dsOpts: dsOpts{testcases: tcs}})
	p := f.participant("alice")
	for round := 0; round < 20; round++ {
		s := f.submit(p, task, time.Duration(round)*time.Minute)
		b := f.build(f.pendingOps(Targets{SubmissionIDs: []int32{s}}))
		ch := f.apply(resultOf(b.Jobs[0], job.CompilationOK))
		b = f.build(ch.Next)
		var results []*job.Result
		for _, jb := range b.Jobs {
			results = append(results, resultOf(jb, "1.0"))
		}
		// Each applier gets half of the results; in odd rounds also a few
		// of the other half (redeliveries), in a different order.
		a1, a2 := results[:4], results[4:]
		redelivered := 0
		if round%2 == 1 {
			a1 = append(append([]*job.Result{}, results[:5]...), results[6])
			a2 = append(append([]*job.Result{}, results[3:]...), results[0])
			redelivered = 4
		}
		var c1, c2 *Changes
		errs := concurrently(
			func() (err error) { c1, err = f.j.Apply(f.ctx, a1); return },
			func() (err error) { c2, err = f.j.Apply(f.ctx, a2); return },
		)
		for _, err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		r := f.result(s, task.dataset)
		if r.Evaluations != 8 || r.status() != StatusScored || *r.Score != 80 {
			t.Fatalf("round %d: result %+v", round, r)
		}
		scored := 0
		for _, c := range []*Changes{c1, c2} {
			for _, sc := range c.Submissions {
				if sc.Status == StatusScored {
					scored++
				}
			}
		}
		if dropped := len(c1.Dropped) + len(c2.Dropped); scored != 1 || dropped != redelivered {
			t.Fatalf("round %d: scored %d times, %d results dropped", round, scored, dropped)
		}
		f.checkCache(p, task, TaskScore{DatasetID: task.dataset, Score: 80, PublicScore: 10})
	}
}

// TestRacingAppliersSameTaskScore: two dispatchers score two submissions
// of the same participant on the same task at the same time; the cached
// task score must see both (the row lock orders the recomputations).
func TestRacingAppliersSameTaskScore(t *testing.T) {
	f := newFx(t)
	task := f.groupTask(scoring.ModeMaxSubtask)
	p := f.participant("alice")
	for round := 0; round < 15; round++ {
		at := time.Duration(round) * time.Hour
		s1, s2 := f.submit(p, task, at), f.submit(p, task, at+time.Minute)
		if _, err := f.j.RefreshTaskScoresOf(f.ctx, []int32{s1, s2}); err != nil {
			t.Fatal(err)
		}
		// Compile both, then race their evaluations: s1 solves subtask 1,
		// s2 subtask 2.
		b := f.build(f.pendingOps(Targets{SubmissionIDs: []int32{s1, s2}}))
		ch := f.apply(resultOf(b.Jobs[0], job.CompilationOK), resultOf(b.Jobs[1], job.CompilationOK))
		b = f.build(ch.Next)
		var r1, r2 []*job.Result
		for _, jb := range b.Jobs {
			v := "0.0"
			code := jb.Operation.TestcaseCodename
			if jb.Operation.ObjectID == s1 && code != "002" || jb.Operation.ObjectID == s2 && code == "002" {
				v = "1.0"
			}
			if jb.Operation.ObjectID == s1 {
				r1 = append(r1, resultOf(jb, v))
			} else {
				r2 = append(r2, resultOf(jb, v))
			}
		}
		for _, err := range concurrently(
			func() error { _, err := f.j.Apply(f.ctx, r1); return err },
			func() error { _, err := f.j.Apply(f.ctx, r2); return err },
		) {
			if err != nil {
				t.Fatal(err)
			}
		}
		f.checkCache(p, task, TaskScore{DatasetID: task.dataset, Score: 100, PublicScore: 40})
	}
}

// TestApplyRacingInvalidation: results applied while the submission is
// recompiled end in a consistent state whatever the order: the
// invalidation wins, and a late result is dropped as stale.
func TestApplyRacingInvalidation(t *testing.T) {
	f := newFx(t)
	task := f.task(taskOpts{})
	p := f.participant("alice")
	for round := 0; round < 10; round++ {
		s := f.submit(p, task, time.Duration(round)*time.Minute)
		b := f.build(f.pendingOps(Targets{SubmissionIDs: []int32{s}}))
		ch := f.apply(resultOf(b.Jobs[0], job.CompilationOK))
		b = f.build(ch.Next)
		var results []*job.Result
		for _, jb := range b.Jobs {
			results = append(results, resultOf(jb, "1.0"))
		}
		var applied *Changes
		for _, err := range concurrently(
			func() (err error) { applied, err = f.j.Apply(f.ctx, results); return },
			func() error {
				_, err := f.j.Invalidate(f.ctx, Scope{SubmissionIDs: []int32{s}}, LevelCompilation)
				return err
			},
		) {
			if err != nil {
				t.Fatal(err)
			}
		}
		r := f.result(s, task.dataset)
		if r.status() != StatusCompiling || r.Generation != 1 || r.Evaluations != 0 || r.Executables != 0 {
			t.Fatalf("round %d: %+v", round, r)
		}
		if n := len(applied.Dropped); n != 0 && n != 3 {
			t.Errorf("round %d: %d results dropped", round, n)
		}
		want := TaskScore{DatasetID: task.dataset, Partial: true}
		if round > 0 { // the previous submissions are judged
			want.Score, want.PublicScore = 30, 10
		}
		f.checkCache(p, task, want)
		// Judge it again for the next round.
		f.judgeAll(Targets{SubmissionIDs: []int32{s}}, allCorrect)
	}
}

// TestApplyRacingActivateDataset: the results that complete the judging of
// an autojudge dataset arrive while an admin makes it the active one. The
// cached task scores must end as task_score computes them on the new
// dataset whatever the interleaving: a result scored on the dataset must
// not miss both the recomputation of ActivateDataset (which ran before it
// committed) and its own (which saw the dataset still inactive).
func TestApplyRacingActivateDataset(t *testing.T) {
	f := newFx(t)
	task := f.task(taskOpts{})
	auto := f.dataset(task.id, dsOpts{autojudge: true, scoreParams: "20"})
	main := task.dataset
	var parts []int32
	for i := 0; i < 20; i++ {
		parts = append(parts, f.participant(fmt.Sprintf("user%d", i)))
	}
	for round := 0; round < 15; round++ {
		if _, _, err := f.j.ActivateDataset(f.ctx, task.id, main); err != nil {
			t.Fatal(err)
		}
		var subs []int32
		for _, p := range parts {
			subs = append(subs, f.submit(p, task, time.Duration(round)*time.Minute))
		}
		if _, err := f.j.RefreshTaskScoresOf(f.ctx, subs); err != nil {
			t.Fatal(err)
		}
		// Judge everything but the last testcase on the autojudge dataset.
		b := f.build(f.pendingOps(Targets{SubmissionIDs: subs}))
		var results []*job.Result
		for _, jb := range b.Jobs {
			results = append(results, resultOf(jb, job.CompilationOK))
		}
		b = f.build(f.apply(results...).Next)
		var held []*job.Result
		results = results[:0]
		for _, jb := range b.Jobs {
			if jb.Operation.DatasetID == auto && jb.Operation.TestcaseCodename == "002" {
				held = append(held, resultOf(jb, "1.0"))
			} else {
				results = append(results, resultOf(jb, "1.0"))
			}
		}
		f.apply(results...)
		for _, err := range concurrently(
			func() error { _, err := f.j.Apply(f.ctx, held); return err },
			func() error { _, _, err := f.j.ActivateDataset(f.ctx, task.id, auto); return err },
		) {
			if err != nil {
				t.Fatal(err)
			}
		}
		for _, p := range parts {
			want := TaskScore{DatasetID: auto, Score: 60, PublicScore: 20}
			if got := f.cached(p, tk{task.id, auto}); got == nil || *got != want {
				t.Fatalf("round %d, participant %d: task score %+v, want %+v", round, p, got, want)
			}
		}
	}
}
