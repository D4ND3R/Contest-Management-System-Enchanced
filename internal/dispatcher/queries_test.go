// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package dispatcher

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/queue"
)

// manual is a leader driven step by step by a test (no Run).
func (e *env) manual(opts Options) *leader {
	e.t.Helper()
	d, err := New(e.pool, e.kv, e.jobs, e.results, e.judge(), opts)
	if err != nil {
		e.t.Fatal(err)
	}
	if ok, _, err := d.lease.TryAcquire(e.ctx); !ok || err != nil {
		e.t.Fatal("lease", ok, err)
	}
	l := newLeader(d)
	l.recovering = false
	l.readerCtx = e.ctx
	return l
}

// work runs every queued job with v and publishes the results (one
// simulated worker, synchronously). It returns the number of jobs.
func (e *env) work(v verdict) int {
	e.t.Helper()
	n := 0
	for {
		m, err := e.jobs.Consume(e.ctx, "sync", 0)
		if err != nil {
			e.t.Fatal(err)
		}
		if m == nil {
			return n
		}
		jb, err := job.DecodeJob(m.Payload)
		if err != nil {
			e.t.Fatal(err)
		}
		payload, err := resultOf(jb, "sync", v(jb)).Encode()
		if err != nil {
			e.t.Fatal(err)
		}
		if _, err := e.jobs.AckAndEnqueue(e.ctx, m, e.results, 0, payload, queue.EnqueueOptions{}); err != nil {
			e.t.Fatal(err)
		}
		n++
	}
}

// TestConstantQueries checks N2 for the dispatcher: the database round
// trips to take in new submissions, store their compilations and
// evaluations (with scoring and the task score cache) and sweep do not
// depend on the number of submissions.
func TestConstantQueries(t *testing.T) {
	type counts struct{ intake, compiled, evaluated, sweep int64 }
	measure := func(n int) counts {
		e := newEnv(t)
		task, ds := e.task(5)
		var subs []int32
		for i := range n {
			subs = append(subs, e.submit(e.participant(fmt.Sprint("user", i)), task, 1)...)
		}
		opts := e.options("a")
		opts.ResultBatch = 1000
		l := e.manual(opts)
		var c counts
		e.notify(judging.Event{Kind: judging.EventSubmission, SubmissionIDs: subs})
		msgs, err := judging.ReadEvents(e.ctx, e.kv, EventsConsumer, 1000, 0, false)
		if err != nil || len(msgs) != 1 {
			t.Fatal(msgs, err)
		}
		e.qc.Reset()
		if err := l.handleEvents(e.ctx, msgs); err != nil {
			t.Fatal(err)
		}
		c.intake = e.qc.Count()
		if got := e.work(allCorrect); got != n {
			t.Fatalf("%d compilations for %d submissions", got, n)
		}
		e.qc.Reset()
		if k, _, _, err := l.consumeResults(e.ctx, 0, time.Time{}); err != nil || k != n {
			t.Fatal(k, err)
		}
		c.compiled = e.qc.Count()
		if got := e.work(allCorrect); got != 5*n {
			t.Fatalf("%d evaluations for %d submissions", got, n)
		}
		e.qc.Reset()
		if k, _, _, err := l.consumeResults(e.ctx, 0, time.Time{}); err != nil || k != 5*n {
			t.Fatal(k, err)
		}
		c.evaluated = e.qc.Count()
		if e.scored(ds) != n {
			t.Fatalf("%d scored of %d", e.scored(ds), n)
		}
		e.qc.Reset()
		l.sweep.request(0)
		for l.sweep.due(time.Now()) && (l.sweep.active || len(l.sweep.queued) > 0) {
			if err := l.sweepStep(e.ctx); err != nil {
				t.Fatal(err)
			}
		}
		c.sweep = e.qc.Count()
		return c
	}
	one, many := measure(1), measure(40)
	t.Logf("queries for 1 and 40 submissions: %+v %+v", one, many)
	if one != many {
		t.Errorf("the queries depend on the number of submissions: %+v for 1, %+v for 40", one, many)
	}
}

func TestJobPriority(t *testing.T) {
	op := func(kind job.Kind, prio int, gen int64) judging.Operation {
		return judging.Operation{Operation: job.Operation{Type: kind}, Priority: prio, Generation: gen}
	}
	cases := []struct {
		op         judging.Operation
		background bool
		want       int
	}{
		{op(job.Compilation, judging.PriorityHigh, 0), false, job.PriorityExtraHigh},
		{op(job.Evaluation, judging.PriorityMedium, 0), false, job.PriorityHigh},
		{op(job.Compilation, judging.PriorityMedium, 0), false, job.PriorityHigh},
		{op(job.Evaluation, judging.PriorityLow, 0), false, job.PriorityLow},
		{op(job.Compilation, judging.PriorityExtraLow, 0), false, job.PriorityExtraLow},
		// User tests, re-evaluations and background work.
		{op(job.UserTestCompilation, judging.PriorityHigh, 0), false, job.PriorityMedium},
		{op(job.UserTestEvaluation, judging.PriorityMedium, 0), false, job.PriorityMedium},
		{op(job.Compilation, judging.PriorityHigh, 2), false, job.PriorityMedium},
		{op(job.Evaluation, judging.PriorityMedium, 0), true, job.PriorityMedium},
		{op(job.Evaluation, judging.PriorityLow, 1), true, job.PriorityLow},
		{op(job.UserTestEvaluation, judging.PriorityLow, 0), false, job.PriorityLow},
		{op(job.Evaluation, judging.PriorityExtraLow, 3), true, job.PriorityExtraLow},
	}
	for _, c := range cases {
		if got := JobPriority(c.op, c.background); got != c.want {
			t.Errorf("JobPriority(%s %d gen %d, %v) = %d, want %d", c.op.Type, c.op.Priority, c.op.Generation,
				c.background, got, c.want)
		}
	}
}

// TestEnqueueOldestFirst: the jobs of one batch enter their priority in
// the order of their submissions, oldest first, whatever the order of the
// operations.
func TestEnqueueOldestFirst(t *testing.T) {
	e := newEnv(t)
	task, ds := e.task(1)
	p := e.participant("ann")
	subs := e.submit(p, task, 4)
	for i, s := range subs {
		// The last one is the oldest.
		e.exec(`UPDATE submissions SET timestamp = $1 WHERE id = $2`, t0.Add(-time.Duration(i)*time.Minute), s)
	}
	l := e.manual(e.options("a"))
	w, err := l.d.judge.Pending(e.ctx, judging.Targets{SubmissionIDs: subs})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := l.schedule(e.ctx, w, false); err != nil || n != 4 {
		t.Fatal(n, err)
	}
	var got []int32
	for range 4 {
		m, err := e.jobs.Consume(e.ctx, "x", 0)
		if err != nil || m == nil {
			t.Fatal(m, err)
		}
		jb, _ := job.DecodeJob(m.Payload)
		got = append(got, jb.Operation.ObjectID)
		if m.Priority != job.PriorityExtraHigh || jb.Operation.DatasetID != ds {
			t.Errorf("job %+v at priority %d", jb.Operation, m.Priority)
		}
	}
	want := slices.Clone(subs)
	slices.Reverse(want)
	if !slices.Equal(got, want) {
		t.Errorf("order %v, want %v", got, want)
	}
	// Scheduling the same work again adds nothing: the jobs are queued.
	if n, err := l.schedule(e.ctx, w, false); err != nil || n != 0 {
		t.Errorf("rescheduled: %d %v", n, err)
	}
}
