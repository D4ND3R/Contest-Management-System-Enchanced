// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package judging

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
)

func (f *fx) build(ops []Operation) *Built {
	f.t.Helper()
	b, err := f.j.BuildJobs(f.ctx, ops)
	if err != nil {
		f.t.Fatal(err)
	}
	return b
}

func (f *fx) pendingOps(t Targets) []Operation {
	f.t.Helper()
	w, err := f.j.Pending(f.ctx, t)
	if err != nil {
		f.t.Fatal(err)
	}
	return w.Ops
}

// inStore checks that every digest of a job is in the blob store, so a
// worker can fetch it.
func (f *fx) inStore(jb *job.Job) {
	f.t.Helper()
	var digests []string
	for _, m := range []map[string]string{jb.Files, jb.Managers, jb.Executables} {
		digests = slices.AppendSeq(digests, maps.Values(m))
	}
	if jb.Input != "" {
		digests = append(digests, jb.Input)
	}
	if jb.Output != "" {
		digests = append(digests, jb.Output)
	}
	for _, d := range digests {
		if _, err := f.blobs.Stat(f.ctx, d); err != nil {
			f.t.Errorf("%s: %s is not in the blob store: %v", jb.ID, d, err)
		}
	}
}

func TestBuildSubmissionJobs(t *testing.T) {
	f := newFx(t)
	task := f.task(taskOpts{format: []string{"sum.%l"}, dsOpts: dsOpts{
		params:      `["grader", ["input.txt", "output.txt"], "comparator"]`,
		managers:    map[string]string{"grader.cpp": "grader", "checker": "checker", "sum.h": "header"},
		timeLimit:   1.5,
		memoryLimit: 512 << 20,
	}})
	p := f.participant("alice")
	s := f.submit(p, task, time.Minute)

	b := f.build(f.pendingOps(Targets{SubmissionIDs: []int32{s}}))
	if len(b.Jobs) != 1 || len(b.Skipped) != 0 {
		t.Fatalf("jobs %d, skipped %v", len(b.Jobs), b.Skipped)
	}
	jb, op := b.Jobs[0], b.Ops[0]
	want := &job.Job{
		ID:                   fmt.Sprintf("compile/%d/%d@0", s, task.dataset),
		Operation:            job.Operation{Type: job.Compilation, ObjectID: s, DatasetID: task.dataset},
		Attempt:              1,
		TaskType:             "Batch",
		TaskTypeParameters:   json.RawMessage(`["grader", ["input.txt", "output.txt"], "comparator"]`),
		Language:             "C++17 / g++",
		MultithreadedSandbox: true, // Java is a language of the contest
		Files:                map[string]string{"sum.%l": scalar[string](f, `SELECT digest FROM files WHERE submission_id = $1`, s)},
		Managers: map[string]string{"grader.cpp": digestOf("grader"), "checker": digestOf("checker"),
			"sum.h": digestOf("header")},
		Info: fmt.Sprintf("compile submission %d", s),
	}
	gotJSON, _ := json.Marshal(jb)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("compilation job:\n got  %s\n want %s", gotJSON, wantJSON)
	}
	if op.Priority != PriorityHigh || op.Attempt != 1 || op.Retry || jb.DedupKey() != op.DedupKey() {
		t.Errorf("operation %+v", op)
	}
	f.inStore(jb)
	if _, err := jb.Encode(); err != nil {
		t.Error(err)
	}
	// The result row exists now, so the generation of the job is real.
	if f.result(s, task.dataset) == nil {
		t.Error("BuildJobs did not create the result row")
	}

	// Compile, then build the evaluations.
	ch, err := f.j.Apply(f.ctx, []*job.Result{resultOf(jb, job.CompilationOK)})
	if err != nil {
		t.Fatal(err)
	}
	b = f.build(ch.Next)
	if len(b.Jobs) != 3 {
		t.Fatalf("%d evaluation jobs, skipped %v", len(b.Jobs), b.Skipped)
	}
	for i, ej := range b.Jobs {
		code := fmt.Sprintf("00%d", i)
		if ej.Operation.TestcaseCodename != code || ej.Input != digestOf("input "+code) ||
			ej.Output != digestOf("output "+code) || ej.TimeLimit != 1.5 || ej.MemoryLimit != 512<<20 ||
			ej.Attempt != 1 || ej.Generation != 0 || ej.OnlyExecution || ej.GetOutput ||
			ej.Info != fmt.Sprintf("evaluate submission %d on testcase %s", s, code) ||
			!maps.Equal(ej.Executables, map[string]string{"main": digestOf(jb.Operation.Key() + "@0")}) ||
			!maps.Equal(ej.Managers, want.Managers) || !maps.Equal(ej.Files, want.Files) {
			t.Errorf("evaluation job %s: %+v", code, ej)
		}
		if b.Ops[i].Priority != PriorityMedium {
			t.Errorf("%s: priority %d", ej.ID, b.Ops[i].Priority)
		}
		if err := ej.Validate(); err != nil {
			t.Error(err)
		}
	}
}

func TestBuildUserTestJobs(t *testing.T) {
	f := newFx(t)
	p := f.participant("alice")
	cases := []struct {
		name     string
		params   string
		taskType string
		dataset  map[string]string
		user     map[string]string
		// managers of the compilation and of the evaluation
		compile, evaluate []string
		invalid           bool
	}{
		{"batch with grader", `["grader", ["", ""], "diff"]`, "Batch",
			map[string]string{"grader.cpp": "official grader", "sum.h": "header", "checker": "checker"},
			map[string]string{"grader.cpp": "my grader"},
			[]string{"grader.cpp", "sum.h"}, []string{"grader.cpp"}, false},
		{"communication", `[1, "alone", "std_io"]`, "Communication",
			map[string]string{"manager": "manager", "sum.h": "header"}, nil,
			[]string{"manager", "sum.h"}, []string{"manager"}, false},
		{"interactive without controller", `[200, "alone", true, 128.0, 1.0, 5.0]`, "Interactive",
			map[string]string{"other": "x"}, nil, nil, nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			task := f.task(taskOpts{dsOpts: dsOpts{taskType: c.taskType, params: c.params, managers: c.dataset}})
			u := f.userTest(p, task, time.Minute, c.user)
			b := f.build(f.pendingOps(Targets{UserTestIDs: []int32{u}}))
			if c.invalid {
				if len(b.Jobs) != 0 || len(b.Skipped) != 1 || b.Skipped[0].Reason != SkipInvalid || b.Skipped[0].Err == nil {
					t.Fatalf("jobs %v, skipped %+v", b.Jobs, b.Skipped)
				}
				return
			}
			if len(b.Jobs) != 1 {
				t.Fatalf("jobs %v, skipped %+v", b.Jobs, b.Skipped)
			}
			jb := b.Jobs[0]
			if got := slices.Sorted(maps.Keys(jb.Managers)); !slices.Equal(got, c.compile) {
				t.Errorf("compilation managers %v, want %v", got, c.compile)
			}
			if c.user["grader.cpp"] != "" && jb.Managers["grader.cpp"] != digestOf(c.user["grader.cpp"]) {
				t.Error("the grader of the contestant must win")
			}
			if jb.Info != fmt.Sprintf("compile user test %d", u) || jb.Attempt != 1 {
				t.Errorf("job %+v", jb)
			}
			f.inStore(jb)
			ch, err := f.j.Apply(f.ctx, []*job.Result{resultOf(jb, job.CompilationOK)})
			if err != nil {
				t.Fatal(err)
			}
			b = f.build(ch.Next)
			if len(b.Jobs) != 1 {
				t.Fatalf("evaluation: jobs %v, skipped %+v", b.Jobs, b.Skipped)
			}
			ej := b.Jobs[0]
			if got := slices.Sorted(maps.Keys(ej.Managers)); !slices.Equal(got, c.evaluate) {
				t.Errorf("evaluation managers %v, want %v", got, c.evaluate)
			}
			input := scalar[string](f, `SELECT input FROM user_tests WHERE id = $1`, u)
			if ej.Operation.Type != job.UserTestEvaluation || ej.Input != input || ej.Output != "" ||
				!ej.OnlyExecution || !ej.GetOutput || len(ej.Executables) != 1 || ej.TimeLimit != 1 {
				t.Errorf("evaluation job %+v", ej)
			}
			if b.Ops[0].Priority != PriorityMedium {
				t.Errorf("priority %d", b.Ops[0].Priority)
			}
		})
	}
}

func TestBuildJobsSkips(t *testing.T) {
	f := newFx(t)
	task := f.task(taskOpts{})
	p := f.participant("alice")
	s := f.submit(p, task, time.Minute)
	compile := f.pendingOps(Targets{SubmissionIDs: []int32{s}})

	// Compiled meanwhile: the compilation is done.
	b := f.build(compile)
	f.judge(f.pendingOps(Targets{SubmissionIDs: []int32{s}}), func(j *job.Job) string {
		if j.Operation.Type == job.Compilation {
			return job.CompilationOK
		}
		return "" // evaluations fail
	})
	b = f.build(compile)
	if len(b.Jobs) != 0 || len(b.Skipped) != 1 || b.Skipped[0].Reason != SkipDone {
		t.Errorf("compiled: %+v", b.Skipped)
	}
	// Evaluations gave up after three failures.
	if r := f.result(s, task.dataset); r.EvaluationTries != MaxEvaluationTries || r.status() != StatusEvaluating {
		t.Errorf("result %+v", r)
	}
	evals := f.pendingOps(Targets{SubmissionIDs: []int32{s}})
	if len(evals) != 0 {
		t.Errorf("operations after giving up: %v", opKeys(evals))
	}
	evals = []Operation{{Operation: job.Operation{Type: job.Evaluation, ObjectID: s, DatasetID: task.dataset, TestcaseCodename: "000"}}}
	if b := f.build(evals); len(b.Skipped) != 1 || b.Skipped[0].Reason != SkipDone {
		t.Errorf("gave up: %+v", b.Skipped)
	}

	// A testcase that does not exist, a submission that is gone, an
	// operation of the wrong kind.
	f.exec(`UPDATE submission_results SET evaluation_tries = 0 WHERE submission_id = $1`, s)
	gone := f.submit(p, task, time.Minute)
	ops := []Operation{
		{Operation: job.Operation{Type: job.Evaluation, ObjectID: s, DatasetID: task.dataset, TestcaseCodename: "nope"}},
		{Operation: job.Operation{Type: job.Compilation, ObjectID: gone, DatasetID: task.dataset}},
		{Operation: job.Operation{Type: job.Compilation, ObjectID: s, DatasetID: f.task(taskOpts{}).dataset}},
	}
	f.exec(`DELETE FROM submissions WHERE id = $1`, gone)
	b = f.build(ops)
	if len(b.Jobs) != 0 || len(b.Skipped) != 3 {
		t.Fatalf("jobs %v, skipped %+v", b.Jobs, b.Skipped)
	}
	for _, sk := range b.Skipped {
		if sk.Reason != SkipGone {
			t.Errorf("%s: %s", sk.Operation.Key(), sk.Reason)
		}
	}

	// A dataset whose task type the protocol rejects.
	bad := f.task(taskOpts{dsOpts: dsOpts{taskType: "No such", params: "[]"}})
	s2 := f.submit(p, bad, time.Minute)
	b = f.build(f.pendingOps(Targets{SubmissionIDs: []int32{s2}}))
	if len(b.Skipped) != 1 || b.Skipped[0].Reason != SkipInvalid || b.Skipped[0].Err == nil {
		t.Errorf("invalid task type: %+v", b.Skipped)
	}
}

func TestBuildJobsUsesCurrentGeneration(t *testing.T) {
	f := newFx(t)
	task := f.task(taskOpts{})
	p := f.participant("alice")
	s := f.submit(p, task, time.Minute)
	f.judgeAll(Targets{SubmissionIDs: []int32{s}}, allCorrect)
	// An operation computed before an invalidation gets the generation
	// and attempt of the row as it is now.
	stale := Operation{Operation: job.Operation{Type: job.Compilation, ObjectID: s, DatasetID: task.dataset}, Generation: 0, Attempt: 7}
	if _, err := f.j.Invalidate(f.ctx, Scope{SubmissionIDs: []int32{s}}, LevelCompilation); err != nil {
		t.Fatal(err)
	}
	b := f.build([]Operation{stale, stale})
	if len(b.Jobs) != 1 || b.Jobs[0].Generation != 1 || b.Jobs[0].Attempt != 1 || b.Ops[0].Generation != 1 ||
		b.Jobs[0].ID != fmt.Sprintf("compile/%d/%d@1", s, task.dataset) {
		t.Fatalf("jobs %+v", b.Jobs)
	}
}

// TestBuildJobsConstantQueries: building the jobs of 1 or 50 submissions
// (and of 1 or 50 testcases) takes the same number of queries (N2).
func TestBuildJobsConstantQueries(t *testing.T) {
	count := func(n int) (compile, evaluate, userTests int64) {
		f := newFx(t)
		var tcs []tc
		for i := 0; i < n; i++ {
			tcs = append(tcs, tc{fmt.Sprintf("%03d", i), i%2 == 0})
		}
		task := f.task(taskOpts{dsOpts: dsOpts{testcases: tcs,
			managers: map[string]string{"checker": "c", "a.h": "h"}}})
		auto := f.dataset(task.id, dsOpts{autojudge: true, testcases: tcs})
		_ = auto
		var subs, uts []int32
		for i := 0; i < n; i++ {
			p := f.participant(fmt.Sprintf("user%d", i))
			subs = append(subs, f.submit(p, task, time.Duration(i)*time.Second))
			uts = append(uts, f.userTest(p, task, time.Duration(i)*time.Second, map[string]string{"x.h": "x"}))
		}
		ops := f.pendingOps(Targets{SubmissionIDs: subs})
		f.qc.Reset()
		b := f.build(ops)
		compile = f.qc.Count()
		if len(b.Jobs) != 2*n {
			t.Fatalf("n=%d: %d compilation jobs", n, len(b.Jobs))
		}
		results := make([]*job.Result, len(b.Jobs))
		for i, jb := range b.Jobs {
			results[i] = resultOf(jb, job.CompilationOK)
		}
		ch, err := f.j.Apply(f.ctx, results)
		if err != nil {
			t.Fatal(err)
		}
		f.qc.Reset()
		b = f.build(ch.Next)
		evaluate = f.qc.Count()
		if len(b.Jobs) != 2*n*n {
			t.Fatalf("n=%d: %d evaluation jobs", n, len(b.Jobs))
		}
		ops = f.pendingOps(Targets{UserTestIDs: uts})
		f.qc.Reset()
		b = f.build(ops)
		userTests = f.qc.Count()
		if len(b.Jobs) != 2*n {
			t.Fatalf("n=%d: %d user test jobs", n, len(b.Jobs))
		}
		return
	}
	c1, e1, u1 := count(1)
	c50, e50, u50 := count(50)
	if c1 != c50 || e1 != e50 || u1 != u50 {
		t.Errorf("queries for 1 and 50: compilations %d/%d, evaluations %d/%d, user tests %d/%d", c1, c50, e1, e50, u1, u50)
	}
	t.Logf("queries: compilations %d, evaluations %d, user tests %d", c1, e1, u1)
}
