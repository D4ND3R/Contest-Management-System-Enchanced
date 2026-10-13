// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package dispatcher

import (
	"fmt"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
)

// BenchmarkJudge measures the dispatcher's work per job (SPEC N1: under
// 50ms): b.N submissions of 20 testcases go through a running dispatcher
// with simulated workers that answer at once. The time per operation is
// that of one submission (21 jobs); ms/job is reported.
func BenchmarkJudge(b *testing.B) {
	e := newEnv(b)
	task, ds := e.task(20)
	var parts []int32
	for i := range 50 {
		parts = append(parts, e.participant(fmt.Sprint("user", i)))
	}
	opts := e.options("bench")
	opts.IdleWait = 100 * time.Millisecond
	e.start(opts)
	e.startWorkers(8, allCorrect)
	var subs []int32
	for i := range b.N {
		subs = append(subs, e.submit(parts[i%len(parts)], task, 1)...)
	}
	b.ResetTimer()
	for i := 0; i < len(subs); i += 20 {
		e.notify(judging.Event{Kind: judging.EventSubmission, SubmissionIDs: subs[i:min(i+20, len(subs))]})
	}
	e.waitScored(ds, b.N, 10*time.Minute)
	b.StopTimer()
	perJob := float64(b.Elapsed().Microseconds()) / 1000 / float64(21*b.N)
	b.ReportMetric(perJob, "ms/job")
	b.ReportMetric(1000/perJob, "jobs/s")
}
