// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// The cases port cmstestsuite/unit_tests/service/esoperations_test.py of
// CMS (Copyright © 2016 Stefano Maggiolo <s.maggiolo@gmail.com>).

package judging

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
)

// esFx is the contest of upstream esoperations_test: two tasks, each with
// three datasets of three testcases. The first is active with autojudge
// unset (operations on the active dataset are scheduled regardless), the
// second has autojudge set, the third is neither (never judged).
type esFx struct {
	*fx
	p     int32
	tasks []tk
	ds    [][3]int32
}

func newESFx(t *testing.T) *esFx {
	f := &esFx{fx: newFx(t)}
	f.p = f.participant("alice")
	for i := 0; i < 2; i++ {
		task := f.task(taskOpts{})
		ds := [3]int32{task.dataset,
			f.dataset(task.id, dsOpts{autojudge: true}),
			f.dataset(task.id, dsOpts{})}
		f.tasks = append(f.tasks, task)
		f.ds = append(f.ds, ds)
	}
	return f
}

// withResults adds a submission to task 0 with a result on each dataset
// of the task, with the given compilation outcome ("" for none).
func (f *esFx) withResults(outcome string) int32 {
	f.t.Helper()
	s := f.submit(f.p, f.tasks[0], time.Minute)
	for _, ds := range f.ds[0] {
		var o *string
		if outcome != "" {
			o = &outcome
		}
		f.exec(`INSERT INTO submission_results (submission_id, dataset_id, compilation_outcome) VALUES ($1, $2, $3)`, s, ds, o)
	}
	return s
}

func (f *esFx) evaluate(sub, ds int32, codename string) {
	f.t.Helper()
	f.exec(`INSERT INTO evaluations (submission_id, dataset_id, testcase_id, outcome)
		SELECT $1, $2, id, '1.0' FROM testcases WHERE dataset_id = $2 AND codename = $3`, sub, ds, codename)
}

// compileOps are the expected compilations of sub on the datasets to
// judge of task 0 (upstream submission_compilation_operation).
func (f *esFx) compileOps(sub int32, tries int) []string {
	prio := PriorityHigh
	if tries > 0 {
		prio = PriorityMedium
	}
	retry := ""
	if tries > 0 {
		retry = " retry"
	}
	return []string{
		fmt.Sprintf("compile/%d/%d p%d a%d%s", sub, f.ds[0][0], prio, tries+1, retry),
		fmt.Sprintf("compile/%d/%d p%d a%d%s", sub, f.ds[0][1], PriorityExtraLow, tries+1, retry),
	}
}

// evalOps are the expected evaluations (upstream
// submission_evaluation_operation), skipping the given codenames.
func (f *esFx) evalOps(sub int32, tries int, skip ...string) []string {
	prio := PriorityMedium
	retry := ""
	if tries > 0 {
		prio, retry = PriorityLow, " retry"
	}
	var out []string
	for i, ds := range f.ds[0][:2] {
		p := prio
		if i > 0 {
			p = PriorityExtraLow
		}
		for _, c := range []string{"000", "001", "002"} {
			if !slices.Contains(skip, c) {
				out = append(out, fmt.Sprintf("evaluate/%d/%d/%s p%d a%d%s", sub, ds, c, p, tries+1, retry))
			}
		}
	}
	return out
}

func (f *esFx) pending(subs ...int32) *Work {
	f.t.Helper()
	w, err := f.j.Pending(f.ctx, Targets{SubmissionIDs: subs})
	if err != nil {
		f.t.Fatal(err)
	}
	return w
}

// sweep runs a whole sweep of the contest in small steps.
func (f *esFx) sweep() *Work {
	f.t.Helper()
	w := &Work{}
	opts := SweepOptions{ContestID: f.contest, Window: 3, Limit: 2}
	for i := 0; ; i++ {
		if i > 1000 {
			f.t.Fatal("the sweep does not end")
		}
		res, err := f.j.Sweep(f.ctx, opts)
		if err != nil {
			f.t.Fatal(err)
		}
		w.add(&res.Work)
		if res.Done {
			return w
		}
		if res.Next == opts.Cursor {
			f.t.Fatalf("the sweep does not advance from %+v", opts.Cursor)
		}
		opts.Cursor = res.Next
	}
}

func TestPendingNoOperations(t *testing.T) {
	f := newESFx(t)
	// A submission of another contest.
	other := f.id(`INSERT INTO contests (name, description) VALUES ('other', 'Other') RETURNING id`)
	f.exec(`UPDATE tasks SET contest_id = $1 WHERE id = $2`, other, f.tasks[1].id)
	f.submit(f.p, f.tasks[1], time.Minute)

	// A submission that failed compilation: only scoring is left.
	failed := f.withResults(job.CompilationFail)
	// A submission completely evaluated: only its evaluation outcome is
	// missing (upstream finalizes it in submission_enqueue_operations).
	complete := f.withResults(job.CompilationOK)
	for _, ds := range f.ds[0] {
		for _, c := range []string{"000", "001", "002"} {
			f.evaluate(complete, ds, c)
		}
	}
	// Submissions that reached the maximum tries.
	maxComp := f.withResults("")
	f.exec(`UPDATE submission_results SET compilation_tries = 25 WHERE submission_id = $1`, maxComp)
	maxEval := f.withResults(job.CompilationOK)
	f.exec(`UPDATE submission_results SET evaluation_tries = 25 WHERE submission_id = $1`, maxEval)

	sw := f.sweep()
	if len(sw.Ops) != 0 {
		t.Errorf("sweep: unexpected operations %v", opKeys(sw.Ops))
	}
	w := f.pending(failed, complete, maxComp, maxEval)
	if len(w.Ops) != 0 {
		t.Errorf("pending: unexpected operations %v", opKeys(w.Ops))
	}
	want := []ResultKey{{failed, f.ds[0][0]}, {failed, f.ds[0][1]}, {complete, f.ds[0][0]}, {complete, f.ds[0][1]}}
	for _, got := range [][]ResultKey{w.ToScore, sw.ToScore} {
		if !sameResultKeys(got, want) {
			t.Errorf("to score: got %v, want %v", got, want)
		}
	}
	wantGaveUp := []GaveUp{
		{job.Compilation, maxComp, f.ds[0][0], 25}, {job.Compilation, maxComp, f.ds[0][1], 25},
		{job.Evaluation, maxEval, f.ds[0][0], 25}, {job.Evaluation, maxEval, f.ds[0][1], 25},
	}
	if !slices.Equal(w.GaveUp, wantGaveUp) {
		t.Errorf("gave up: got %v, want %v", w.GaveUp, wantGaveUp)
	}
	// A submission that gave up shows what CMS shows: compiling or
	// evaluating, forever.
	if s := f.result(maxComp, f.ds[0][0]).status(); s != StatusCompiling {
		t.Errorf("gave up compiling: status %s", s)
	}
	if s := f.result(maxEval, f.ds[0][0]).status(); s != StatusEvaluating {
		t.Errorf("gave up evaluating: status %s", s)
	}
}

func sameResultKeys(got, want []ResultKey) bool {
	sorted := func(s []ResultKey) []ResultKey {
		s = slices.Clone(s)
		slices.SortFunc(s, func(a, b ResultKey) int {
			if a.SubmissionID != b.SubmissionID {
				return int(a.SubmissionID - b.SubmissionID)
			}
			return int(a.DatasetID - b.DatasetID)
		})
		return s
	}
	return slices.Equal(sorted(got), sorted(want))
}

func TestPendingSubmissionStates(t *testing.T) {
	cases := []struct {
		name  string
		setup func(f *esFx) int32
		want  func(f *esFx, s int32) []string
	}{
		{"without results", func(f *esFx) int32 { return f.submit(f.p, f.tasks[0], time.Minute) },
			func(f *esFx, s int32) []string { return f.compileOps(s, 0) }},
		{"with results", func(f *esFx) int32 { return f.withResults("") },
			func(f *esFx, s int32) []string { return f.compileOps(s, 0) }},
		{"with results, second try", func(f *esFx) int32 {
			s := f.withResults("")
			f.exec(`UPDATE submission_results SET compilation_tries = 1 WHERE submission_id = $1`, s)
			return s
		}, func(f *esFx, s int32) []string { return f.compileOps(s, 1) }},
		{"to evaluate", func(f *esFx) int32 { return f.withResults(job.CompilationOK) },
			func(f *esFx, s int32) []string { return f.evalOps(s, 0) }},
		{"to evaluate, second try", func(f *esFx) int32 {
			s := f.withResults(job.CompilationOK)
			f.exec(`UPDATE submission_results SET evaluation_tries = 1 WHERE submission_id = $1`, s)
			return s
		}, func(f *esFx, s int32) []string { return f.evalOps(s, 1) }},
		{"partially evaluated", func(f *esFx) int32 {
			s := f.withResults(job.CompilationOK)
			for _, ds := range f.ds[0] {
				f.evaluate(s, ds, "001")
			}
			return s
		}, func(f *esFx, s int32) []string { return f.evalOps(s, 0, "001") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newESFx(t)
			s := c.setup(f)
			want := c.want(f, s)
			w := f.pending(s)
			sameKeys(t, "pending", opKeys(w.Ops), want)
			if len(w.ToScore) != 0 || len(w.GaveUp) != 0 {
				t.Errorf("to score %v, gave up %v", w.ToScore, w.GaveUp)
			}
			sameKeys(t, "sweep", opKeys(f.sweep().Ops), want)
			for _, o := range w.Ops {
				if !o.Timestamp.Equal(t0.Add(time.Minute)) {
					t.Errorf("%s: timestamp %v", o.Key(), o.Timestamp)
				}
			}
		})
	}
}

func TestPendingMixed(t *testing.T) {
	f := newESFx(t)
	var want []string
	f.withResults(job.CompilationFail)
	s := f.submit(f.p, f.tasks[0], time.Minute)
	want = append(want, f.compileOps(s, 0)...)
	s = f.withResults("")
	want = append(want, f.compileOps(s, 0)...)
	s = f.withResults(job.CompilationOK)
	want = append(want, f.evalOps(s, 0)...)
	sameKeys(t, "sweep", opKeys(f.sweep().Ops), want)
}

func TestPendingUserTests(t *testing.T) {
	type state struct {
		compilation  string // "", ok, fail
		compileTries int
		evaluated    bool
		evalTries    int
	}
	cases := []struct {
		name    string
		results *state // nil: no result rows
		want    func(f *esFx, u int32) []string
		gaveUp  job.Kind
	}{
		{"without results", nil, func(f *esFx, u int32) []string {
			return []string{fmt.Sprintf("compile_test/%d/%d p%d a1", u, f.ds[0][0], PriorityHigh),
				fmt.Sprintf("compile_test/%d/%d p%d a1", u, f.ds[0][1], PriorityExtraLow)}
		}, ""},
		{"with results", &state{}, func(f *esFx, u int32) []string {
			return []string{fmt.Sprintf("compile_test/%d/%d p%d a1", u, f.ds[0][0], PriorityHigh),
				fmt.Sprintf("compile_test/%d/%d p%d a1", u, f.ds[0][1], PriorityExtraLow)}
		}, ""},
		{"second try", &state{compileTries: 1}, func(f *esFx, u int32) []string {
			return []string{fmt.Sprintf("compile_test/%d/%d p%d a2 retry", u, f.ds[0][0], PriorityMedium),
				fmt.Sprintf("compile_test/%d/%d p%d a2 retry", u, f.ds[0][1], PriorityExtraLow)}
		}, ""},
		{"to evaluate", &state{compilation: "ok"}, func(f *esFx, u int32) []string {
			return []string{fmt.Sprintf("evaluate_test/%d/%d p%d a1", u, f.ds[0][0], PriorityMedium),
				fmt.Sprintf("evaluate_test/%d/%d p%d a1", u, f.ds[0][1], PriorityExtraLow)}
		}, ""},
		{"to evaluate, second try", &state{compilation: "ok", evalTries: 1}, func(f *esFx, u int32) []string {
			return []string{fmt.Sprintf("evaluate_test/%d/%d p%d a2 retry", u, f.ds[0][0], PriorityLow),
				fmt.Sprintf("evaluate_test/%d/%d p%d a2 retry", u, f.ds[0][1], PriorityExtraLow)}
		}, ""},
		{"compilation failed", &state{compilation: "fail"}, nil, ""},
		{"evaluated", &state{compilation: "ok", evaluated: true}, nil, ""},
		{"max compilation tries", &state{compileTries: 25}, nil, job.UserTestCompilation},
		{"max evaluation tries", &state{compilation: "ok", evalTries: 25}, nil, job.UserTestEvaluation},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newESFx(t)
			u := f.userTest(f.p, f.tasks[0], time.Minute, nil)
			if s := c.results; s != nil {
				for _, ds := range f.ds[0] {
					var outcome, evaluated *string
					if s.compilation != "" {
						outcome = &s.compilation
					}
					if s.evaluated {
						evaluated = job.Ptr("ok")
					}
					f.exec(`INSERT INTO user_test_results (user_test_id, dataset_id, compilation_outcome, compilation_tries,
							evaluation_outcome, evaluation_tries) VALUES ($1, $2, $3, $4, $5, $6)`,
						u, ds, outcome, s.compileTries, evaluated, s.evalTries)
				}
			}
			var want []string
			if c.want != nil {
				want = c.want(f, u)
			}
			w, err := f.j.Pending(f.ctx, Targets{UserTestIDs: []int32{u}})
			if err != nil {
				t.Fatal(err)
			}
			sameKeys(t, "pending", opKeys(w.Ops), want)
			sameKeys(t, "sweep", opKeys(f.sweep().Ops), want)
			if c.gaveUp != "" && (len(w.GaveUp) != 2 || w.GaveUp[0].Type != c.gaveUp) {
				t.Errorf("gave up: %v", w.GaveUp)
			}
		})
	}
}

func TestPendingArchiveSandbox(t *testing.T) {
	f := newESFx(t)
	s := f.submit(f.p, f.tasks[0], 0)
	w, err := f.j.Pending(f.ctx, Targets{SubmissionIDs: []int32{s}, ArchiveSandbox: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range w.Ops {
		if !o.ArchiveSandbox {
			t.Errorf("%s: archive_sandbox not set", o.Key())
		}
	}
}

func TestSweepKeyset(t *testing.T) {
	f := newESFx(t)
	other := f.id(`INSERT INTO contests (name, description) VALUES ('other', 'Other') RETURNING id`)
	otherTask := f.task(taskOpts{})
	f.exec(`UPDATE tasks SET contest_id = $1 WHERE id = $2`, other, otherTask.id)

	var subs, uts []int32
	for i := 0; i < 20; i++ {
		s := f.submit(f.p, f.tasks[i%2], time.Duration(i)*time.Minute)
		subs = append(subs, s)
		f.submit(f.p, otherTask, time.Minute) // never swept with ContestID
		if i%5 == 0 {
			uts = append(uts, f.userTest(f.p, f.tasks[0], time.Minute, nil))
		}
	}
	// Judge some of them completely: the sweep must not return them.
	f.judgeAll(Targets{SubmissionIDs: subs[:6]}, allCorrect)

	w, err := f.j.Pending(f.ctx, Targets{SubmissionIDs: subs, UserTestIDs: uts})
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Ops) != 2*14+2*len(uts) {
		t.Fatalf("pending: %d operations: %v", len(w.Ops), opKeys(w.Ops))
	}
	sameKeys(t, "sweep", opKeys(f.sweep().Ops), opKeys(w.Ops))

	// Every contest, in one step.
	res, err := f.j.Sweep(f.ctx, SweepOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Done || len(res.Ops) != 2*14+20 { // the other task has one dataset
		t.Errorf("sweep of every contest, first step: done=%v, %d operations", res.Done, len(res.Ops))
	}
	res, err = f.j.Sweep(f.ctx, SweepOptions{Cursor: res.Next})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Done || len(res.Ops) != 2*len(uts) {
		t.Errorf("sweep of every contest, second step: done=%v, %d operations", res.Done, len(res.Ops))
	}

	// An empty database is swept at once.
	empty := newFx(t)
	res, err = empty.j.Sweep(empty.ctx, SweepOptions{})
	if err != nil || !res.Done || len(res.Ops) != 0 {
		t.Errorf("empty sweep: %+v, %v", res, err)
	}
}

// TestPendingConstantQueries: the work of 1 or 50 submissions and user
// tests, in every state, takes the same number of queries, as a sweep step
// does (N2).
func TestPendingConstantQueries(t *testing.T) {
	count := func(n int) (pending, sweep int64) {
		f := newESFx(t)
		var subs, uts []int32
		for i := 0; i < n; i++ {
			subs = append(subs, f.submit(f.p, f.tasks[0], time.Minute), f.withResults(""),
				f.withResults(job.CompilationFail))
			s := f.withResults(job.CompilationOK)
			f.evaluate(s, f.ds[0][0], "001")
			subs = append(subs, s)
			uts = append(uts, f.userTest(f.p, f.tasks[0], time.Minute, nil))
		}
		f.qc.Reset()
		w, err := f.j.Pending(f.ctx, Targets{SubmissionIDs: subs, UserTestIDs: uts})
		if err != nil {
			t.Fatal(err)
		}
		pending = f.qc.Count()
		if len(w.Ops) != n*(2+2+5+2) || len(w.ToScore) != 2*n {
			t.Fatalf("n=%d: %d operations, %d to score", n, len(w.Ops), len(w.ToScore))
		}
		f.qc.Reset()
		// The first step: every submission (the user tests come next).
		res, err := f.j.Sweep(f.ctx, SweepOptions{ContestID: f.contest})
		if err != nil || len(res.Ops) != n*(2+2+5) || len(res.ToScore) != 2*n {
			t.Fatalf("n=%d: sweep step %+v, %v", n, res, err)
		}
		sweep = f.qc.Count()
		return
	}
	p1, s1 := count(1)
	p50, s50 := count(50)
	if p1 != p50 || s1 != s50 {
		t.Errorf("queries for 1 and 50: pending %d/%d, sweep step %d/%d", p1, p50, s1, s50)
	}
	t.Logf("queries: pending %d, sweep step %d", p1, s1)
}
