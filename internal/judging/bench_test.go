// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package judging

import (
	"fmt"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/scoring"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
)

// BenchmarkJudgeSubmission measures the database work of the dispatcher
// for one submission of a task with 30 testcases in 5 subtasks, judged as
// a dispatcher does: build the compilation job, apply its result, build
// the 30 evaluation jobs, apply their results in batches of 10 (the last
// batch scores the submission and updates the task score of a participant
// with 20 earlier submissions). The time per job is the overhead of the
// dispatcher (N1: below 50 ms).
func BenchmarkJudgeSubmission(b *testing.B) {
	f := newFx(b)
	var tcs []tc
	for i := 0; i < 30; i++ {
		tcs = append(tcs, tc{fmt.Sprintf("%d_%02d", i/6, i), i%6 == 0})
	}
	task := f.task(taskOpts{scoreMode: scoring.ModeMaxSubtask, dsOpts: dsOpts{testcases: tcs, scoreType: "GroupMin",
		scoreParams: `[[20, "0_.*"], [20, "1_.*"], [20, "2_.*"], [20, "3_.*"], [20, "4_.*"]]`}})
	p := f.participant("alice")
	var old []int32
	for i := 0; i < 20; i++ {
		old = append(old, f.submit(p, task, time.Duration(i)*time.Second))
	}
	f.judgeAll(Targets{SubmissionIDs: old}, allCorrect)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		s := f.submit(p, task, time.Hour+time.Duration(i)*time.Second)
		ops := f.pendingOps(Targets{SubmissionIDs: []int32{s}})
		b.StartTimer()
		built, err := f.j.BuildJobs(f.ctx, ops)
		if err != nil {
			b.Fatal(err)
		}
		ch, err := f.j.Apply(f.ctx, []*job.Result{resultOf(built.Jobs[0], job.CompilationOK)})
		if err != nil {
			b.Fatal(err)
		}
		built, err = f.j.BuildJobs(f.ctx, ch.Next)
		if err != nil {
			b.Fatal(err)
		}
		for k := 0; k < len(built.Jobs); k += 10 {
			var results []*job.Result
			for _, jb := range built.Jobs[k:min(k+10, len(built.Jobs))] {
				results = append(results, resultOf(jb, "1.0"))
			}
			if _, err := f.j.Apply(f.ctx, results); err != nil {
				b.Fatal(err)
			}
		}
	}
	b.ReportMetric(float64(b.Elapsed().Microseconds())/float64(b.N)/31/1000, "ms/job")
}
