// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package judging

import (
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
)

func TestStatus(t *testing.T) {
	f := newFx(t)
	task := f.task(taskOpts{})
	auto := f.dataset(task.id, dsOpts{autojudge: true})
	f.dataset(task.id, dsOpts{}) // not judged: not counted
	p := f.participant("alice")
	var subs []int32
	for i := 0; i < 6; i++ {
		subs = append(subs, f.submit(p, task, time.Duration(i)*time.Minute))
	}
	// 0: scored; 1: compilation failed; 2: gave up compiling; 3: gave up
	// evaluating; 4: scoring (complete, not scored); 5: compiling.
	f.judgeAll(Targets{SubmissionIDs: subs[:1]}, allCorrect)
	f.judgeAll(Targets{SubmissionIDs: subs[1:2]}, func(*job.Job) string { return job.CompilationFail })
	f.judgeAll(Targets{SubmissionIDs: subs[2:3]}, func(*job.Job) string { return "" })
	f.judgeAll(Targets{SubmissionIDs: subs[3:4]}, func(j *job.Job) string {
		if j.Operation.Type == job.Compilation {
			return job.CompilationOK
		}
		return ""
	})
	f.judgeAll(Targets{SubmissionIDs: subs[4:5]}, allCorrect)
	f.exec(`UPDATE submission_results SET score = NULL WHERE submission_id = $1`, subs[4])
	u := f.userTest(p, task, time.Minute, nil)
	f.judgeAll(Targets{UserTestIDs: []int32{u}}, func(j *job.Job) string {
		if j.Operation.Type.IsCompilation() {
			return job.CompilationOK
		}
		return "ok"
	})
	f.userTest(p, task, time.Minute, nil)

	r, err := f.j.Status(f.ctx, f.contest)
	if err != nil {
		t.Fatal(err)
	}
	want := DatasetStatus{TaskID: task.id, TaskName: "task1", DatasetID: task.dataset, DatasetDescription: "default",
		Active: true, Total: 6, Compiling: 1, CompilationGaveUp: 1, CompilationFailed: 1, Evaluating: 0,
		EvaluationGaveUp: 1, Scoring: 1, Scored: 1}
	if len(r.Submissions) != 2 || r.Submissions[0] != want || r.Submissions[1].DatasetID != auto || r.Submissions[1].Active {
		t.Errorf("submissions:\n got  %+v\n want %+v", r.Submissions, want)
	}
	wantUT := DatasetStatus{TaskID: task.id, TaskName: "task1", DatasetID: task.dataset, DatasetDescription: "default",
		Active: true, Total: 2, Compiling: 1, Evaluated: 1}
	if len(r.UserTests) != 2 || r.UserTests[0] != wantUT {
		t.Errorf("user tests:\n got  %+v\n want %+v", r.UserTests, wantUT)
	}
	if r, err := f.j.Status(f.ctx, f.contest+1); err != nil || len(r.Submissions) != 0 {
		t.Errorf("another contest: %+v %v", r, err)
	}
	if r, err := f.j.Status(f.ctx, 0); err != nil || len(r.Submissions) != 2 {
		t.Errorf("every contest: %+v %v", r, err)
	}
}

func TestProgress(t *testing.T) {
	f := newFx(t)
	task := f.task(taskOpts{})
	f.dataset(task.id, dsOpts{autojudge: true})
	other := f.dataset(task.id, dsOpts{}) // not judged: not counted
	p := f.participant("alice")
	var subs []int32
	for i := 0; i < 5; i++ {
		subs = append(subs, f.submit(p, task, time.Duration(i)*time.Minute))
	}
	// 0: scored; 1: evaluating; 2: compiling (jobs built); 3: scoring; 4:
	// gave up compiling. Each on the active and the autojudge dataset.
	f.judgeAll(Targets{SubmissionIDs: subs[:1]}, allCorrect)
	w, err := f.j.Pending(f.ctx, Targets{SubmissionIDs: subs[1:3]})
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.j.BuildJobs(f.ctx, w.Ops)
	if err != nil {
		t.Fatal(err)
	}
	var compiled []*job.Result
	for _, jb := range b.Jobs {
		if jb.Operation.ObjectID == subs[1] {
			compiled = append(compiled, resultOf(jb, job.CompilationOK))
		}
	}
	if _, err := f.j.Apply(f.ctx, compiled); err != nil {
		t.Fatal(err)
	}
	f.judgeAll(Targets{SubmissionIDs: subs[3:4]}, allCorrect)
	f.exec(`UPDATE submission_results SET score = NULL WHERE submission_id = $1`, subs[3])
	f.judgeAll(Targets{SubmissionIDs: subs[4:5]}, func(*job.Job) string { return "" })
	f.exec(`INSERT INTO submission_results (submission_id, dataset_id) VALUES ($1, $2)`, subs[0], other)

	got, err := f.j.Progress(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []Progress{{ContestID: f.contest, Compiling: 2, Evaluating: 2, Scoring: 2}}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("progress:\n got  %+v\n want %+v", got, want)
	}
	// Tries above the default: the result that gave up compiles again.
	j := New(f.pool, Options{Tries: Tries{Compilation: 5}})
	if got, err := j.Progress(f.ctx); err != nil || len(got) != 1 || got[0].Compiling != 4 {
		t.Errorf("progress with 5 compilation tries: %+v %v", got, err)
	}
}
