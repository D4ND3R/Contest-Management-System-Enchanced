// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package dispatcher

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/queue"
)

// JobPriority maps an operation to a priority of job.JobQueue (R4.1):
// contestant submissions first, then user tests and re-evaluations; the
// order of upstream esoperations within each group (first compilations,
// then first evaluations and compilations that failed once, then
// evaluations that failed before, and other datasets last).
//
//	0  ExtraHigh  first compilation of a contestant submission (upstream HIGH)
//	1  High       its evaluations; compilations that failed once (upstream MEDIUM)
//	2  Medium     user tests, re-evaluations and work found by sweeps
//	3  Low        evaluations that failed before (upstream LOW)
//	4  ExtraLow   datasets that are not active (upstream EXTRA_LOW)
//
// background marks re-evaluation work: operations of results that were
// invalidated (their generation is not 0), found by a sweep, or started by
// an invalidation or a change of dataset. Within a priority, workers take
// the oldest job first and the dispatcher enqueues each batch sorted by
// the time of the submission.
func JobPriority(op judging.Operation, background bool) int {
	switch {
	case op.Priority == judging.PriorityExtraLow:
		return job.PriorityExtraLow
	case op.Priority == judging.PriorityLow:
		return job.PriorityLow
	case background || !op.Type.ForSubmission() || op.Generation > 0:
		return job.PriorityMedium
	case op.Priority == judging.PriorityHigh:
		return job.PriorityExtraHigh
	}
	return job.PriorityHigh
}

// isBackground reports whether op belongs to re-evaluation work this
// leader started (see JobPriority).
func (l *leader) isBackground(op judging.Operation) bool {
	_, ok := l.background[judging.ResultKey{SubmissionID: op.ObjectID, DatasetID: op.DatasetID}]
	return ok
}

// maxBackground bounds the memory of re-evaluation work: beyond it, the
// evaluations of a recompiled submission may get a contestant priority.
const maxBackground = 1 << 20

// markBackground remembers that the work of ops is re-evaluation work, so
// the evaluations that follow their compilations keep a background
// priority.
func (l *leader) markBackground(ops []judging.Operation) {
	for _, op := range ops {
		if op.Type.ForSubmission() && len(l.background) < maxBackground {
			l.background[judging.ResultKey{SubmissionID: op.ObjectID, DatasetID: op.DatasetID}] = struct{}{}
		}
	}
}

// notHeld drops the operations whose job is already queued, running or
// waiting for a retry (the job queue holds their dedup key), without
// building them.
func (l *leader) notHeld(ctx context.Context, ops []judging.Operation) ([]judging.Operation, error) {
	if len(ops) == 0 {
		return nil, nil
	}
	keys := make([]string, len(ops))
	for i, op := range ops {
		keys[i] = op.DedupKey()
	}
	held, err := l.d.jobs.Holding(ctx, keys)
	if err != nil {
		return nil, err
	}
	out := ops[:0:0]
	for i, op := range ops {
		if held[i] {
			l.d.m.held.Inc()
			continue
		}
		out = append(out, op)
	}
	return out, nil
}

// enqueue builds the jobs of ops and adds them to the job queue, each with
// its priority, attempt and dedup key, after a backoff for retries. It
// returns the number of jobs added.
func (l *leader) enqueue(ctx context.Context, ops []judging.Operation, background bool) (int, error) {
	if len(ops) == 0 {
		return 0, nil
	}
	d := l.d
	if err := l.fence(); err != nil {
		return 0, err
	}
	b, err := d.judge.BuildJobs(ctx, ops)
	if err != nil {
		return 0, err
	}
	for _, s := range b.Skipped {
		d.m.skipped.WithLabelValues(string(s.Reason)).Inc()
		if s.Reason == judging.SkipInvalid {
			d.log.Error("cannot build a job: fix the dataset and reevaluate", "operation", s.Operation.Key(), "err", s.Err)
		}
	}
	if len(b.Jobs) == 0 {
		return 0, nil
	}
	if background {
		l.markBackground(b.Ops)
	}
	type entry struct {
		item queue.Item
		op   judging.Operation
	}
	entries := make([]entry, 0, len(b.Jobs))
	for i, jb := range b.Jobs {
		op := b.Ops[i]
		payload, err := jb.Encode()
		if err != nil {
			d.log.Error("cannot encode a job", "operation", op.Key(), "err", err)
			continue
		}
		bg := background || l.isBackground(op)
		var delay time.Duration
		if op.Retry {
			delay = d.jobs.Backoff(jb.Attempt - 1)
		}
		entries = append(entries, entry{op: op, item: queue.Item{
			Priority: JobPriority(op, bg),
			Payload:  payload,
			Options:  queue.EnqueueOptions{DedupKey: jb.DedupKey(), Attempt: jb.Attempt, Delay: delay},
		}})
	}
	// Oldest submission first within each priority (upstream orders the
	// operations of a priority by timestamp).
	slices.SortStableFunc(entries, func(a, b entry) int {
		if a.item.Priority != b.item.Priority {
			return a.item.Priority - b.item.Priority
		}
		if c := a.op.Timestamp.Compare(b.op.Timestamp); c != 0 {
			return c
		}
		return strings.Compare(a.op.Key(), b.op.Key())
	})
	items := make([]queue.Item, len(entries))
	for i := range entries {
		items[i] = entries[i].item
	}
	if err := l.fence(); err != nil {
		return 0, err
	}
	res, err := d.jobs.EnqueueBatch(ctx, items)
	added := 0
	for i, r := range res {
		if r.Added {
			added++
			d.m.enqueued.WithLabelValues(prioLabel(items[i].Priority)).Inc()
		} else {
			d.m.duplicates.Inc()
		}
	}
	if added > 0 {
		l.dirty = true
	}
	return added, err
}
