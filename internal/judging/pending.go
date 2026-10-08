// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from submission_get_operations, user_test_get_operations,
// get_submissions_operations and get_user_tests_operations in
// cms/service/esoperations.py, submission_enqueue_operations in
// cms/service/EvaluationService.py and get_operations in
// cms/service/scoringoperations.py of CMS:
// Copyright © 2010-2014 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2016 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2013-2015 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2013 Bernard Blackham <bernard@largestprime.net>
// Copyright © 2014 Artem Iglikov <artem.iglikov@gmail.com>

package judging

import (
	"context"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// Targets selects the submissions and user tests Pending looks at.
type Targets struct {
	SubmissionIDs []int32
	UserTestIDs   []int32
	// ArchiveSandbox asks the workers to archive the sandboxes of the
	// operations (the "Rerun and archive" buttons of the admin).
	ArchiveSandbox bool
}

// Work is what is left to do for some submissions and user tests.
type Work struct {
	// Ops are the operations for the workers, by submission (or user
	// test), dataset and testcase.
	Ops []Operation
	// ToScore are results that only need the database: their compilation
	// failed or every testcase is evaluated, and they are not scored yet.
	// Pass them to Score.
	ToScore []ResultKey
	// GaveUp are results that reached the maximum number of tries.
	GaveUp []GaveUp
}

func (w *Work) add(o *Work) {
	w.Ops = append(w.Ops, o.Ops...)
	w.ToScore = append(w.ToScore, o.ToScore...)
	w.GaveUp = append(w.GaveUp, o.GaveUp...)
}

// Pending returns the work left for the given submissions and user tests
// on each of their datasets to judge (upstream
// submission_enqueue_operations and user_test_enqueue_operations, without
// the enqueueing). It only reads, in at most three queries whatever the
// number of submissions.
func (j *Judge) Pending(ctx context.Context, t Targets) (*Work, error) {
	w := &Work{}
	if len(t.SubmissionIDs) > 0 {
		rows, err := j.store.Q.JudgingSubmissionStates(ctx, uniq(t.SubmissionIDs))
		if err != nil {
			return nil, err
		}
		sw, err := submissionWork(ctx, j.store.Q, rows, t.ArchiveSandbox)
		if err != nil {
			return nil, err
		}
		w.add(sw)
	}
	if len(t.UserTestIDs) > 0 {
		rows, err := j.store.Q.JudgingUserTestStates(ctx, uniq(t.UserTestIDs))
		if err != nil {
			return nil, err
		}
		w.add(userTestWork(rows, t.ArchiveSandbox))
	}
	return w, nil
}

// evalTarget is a result to evaluate: its missing testcases become
// evaluation operations.
type evalTarget struct {
	key        ResultKey
	timestamp  time.Time
	active     bool
	tries      int32
	generation int64
}

// submissionWork turns the states of submission results into work
// (upstream submission_get_operations, plus the finalization of
// submission_enqueue_operations and the scoring sweep of ScoringService).
func submissionWork(ctx context.Context, qq *q.Queries, rows []q.JudgingSubmissionStatesRow, archive bool) (*Work, error) {
	w := &Work{}
	var toEval []evalTarget
	for _, r := range rows {
		key := ResultKey{r.SubmissionID, r.DatasetID}
		switch {
		case !r.HasResult || r.CompilationOutcome == nil && r.CompilationTries < MaxCompilationTries:
			// submission_to_compile.
			w.Ops = append(w.Ops, Operation{
				Operation: job.Operation{Type: job.Compilation, ObjectID: r.SubmissionID, DatasetID: r.DatasetID,
					ArchiveSandbox: archive},
				Priority:   priority(true, r.Active, r.CompilationTries),
				Timestamp:  r.Timestamp,
				Generation: r.Generation,
				Attempt:    int(r.CompilationTries) + 1,
				Retry:      r.CompilationTries > 0,
			})
		case r.CompilationOutcome == nil:
			w.GaveUp = append(w.GaveUp, GaveUp{job.Compilation, r.SubmissionID, r.DatasetID, r.CompilationTries})
		case *r.CompilationOutcome == job.CompilationOK && r.EvaluationOutcome == nil:
			if r.EvaluationTries >= MaxEvaluationTries {
				w.GaveUp = append(w.GaveUp, GaveUp{job.Evaluation, r.SubmissionID, r.DatasetID, r.EvaluationTries})
				continue
			}
			// submission_to_evaluate.
			toEval = append(toEval, evalTarget{key, r.Timestamp, r.Active, r.EvaluationTries, r.Generation})
		case !r.Scored:
			// Compilation failed, or evaluated: needs scoring.
			w.ToScore = append(w.ToScore, key)
		}
	}
	if len(toEval) == 0 {
		return w, nil
	}
	ops, finished, err := evaluationOps(ctx, qq, toEval, archive, nil)
	if err != nil {
		return nil, err
	}
	w.Ops = append(w.Ops, ops...)
	// Every evaluation is there: only the outcome is missing (upstream
	// "finalizing it" in submission_enqueue_operations); Score sets it.
	w.ToScore = append(w.ToScore, finished...)
	return w, nil
}

// evaluationOps returns the evaluation operations of the testcases that
// targets have no evaluation for, and the targets that have them all. If
// only is not nil, an operation is returned only if only reports true
// for it (retries of the testcases that failed).
func evaluationOps(ctx context.Context, qq *q.Queries, targets []evalTarget, archive bool,
	only func(ResultKey, string) bool) (ops []Operation, finished []ResultKey, err error) {
	params := q.JudgingMissingEvaluationsParams{
		SubmissionIds: make([]int32, len(targets)),
		DatasetIds:    make([]int32, len(targets)),
	}
	byKey := make(map[ResultKey]*evalTarget, len(targets))
	for i := range targets {
		t := &targets[i]
		params.SubmissionIds[i], params.DatasetIds[i] = t.key.SubmissionID, t.key.DatasetID
		byKey[t.key] = t
	}
	missing, err := qq.JudgingMissingEvaluations(ctx, params)
	if err != nil {
		return nil, nil, err
	}
	hasMissing := make(map[ResultKey]bool, len(targets))
	for _, m := range missing {
		key := ResultKey{m.SubmissionID, m.DatasetID}
		hasMissing[key] = true
		t := byKey[key]
		if t == nil || only != nil && !only(key, m.Codename) {
			continue
		}
		ops = append(ops, Operation{
			Operation: job.Operation{Type: job.Evaluation, ObjectID: key.SubmissionID, DatasetID: key.DatasetID,
				TestcaseCodename: m.Codename, ArchiveSandbox: archive},
			Priority:   priority(false, t.active, t.tries),
			Timestamp:  t.timestamp,
			Generation: t.generation,
			Attempt:    int(t.tries) + 1,
			Retry:      t.tries > 0,
		})
	}
	for _, t := range targets {
		if !hasMissing[t.key] {
			finished = append(finished, t.key)
		}
	}
	return ops, finished, nil
}

// userTestWork turns the states of user test results into operations
// (upstream user_test_get_operations).
func userTestWork(rows []q.JudgingUserTestStatesRow, archive bool) *Work {
	w := &Work{}
	for _, r := range rows {
		op := Operation{
			Operation:  job.Operation{ObjectID: r.UserTestID, DatasetID: r.DatasetID, ArchiveSandbox: archive},
			Timestamp:  r.Timestamp,
			Generation: r.Generation,
		}
		switch {
		case !r.HasResult || r.CompilationOutcome == nil && r.CompilationTries < MaxUserTestCompilationTries:
			op.Type = job.UserTestCompilation
			op.Priority = priority(true, r.Active, r.CompilationTries)
			op.Attempt, op.Retry = int(r.CompilationTries)+1, r.CompilationTries > 0
		case r.CompilationOutcome == nil:
			w.GaveUp = append(w.GaveUp, GaveUp{job.UserTestCompilation, r.UserTestID, r.DatasetID, r.CompilationTries})
			continue
		case *r.CompilationOutcome == job.CompilationOK && r.EvaluationOutcome == nil:
			if r.EvaluationTries >= MaxUserTestEvaluationTries {
				w.GaveUp = append(w.GaveUp, GaveUp{job.UserTestEvaluation, r.UserTestID, r.DatasetID, r.EvaluationTries})
				continue
			}
			op.Type = job.UserTestEvaluation
			op.Priority = priority(false, r.Active, r.EvaluationTries)
			op.Attempt, op.Retry = int(r.EvaluationTries)+1, r.EvaluationTries > 0
		default:
			continue
		}
		w.Ops = append(w.Ops, op)
	}
	return w
}

// SweepCursor is the position of a sweep: the last submission and user
// test ids looked at. The zero value starts a sweep.
type SweepCursor struct {
	SubmissionID int32
	UserTestID   int32
}

// SweepOptions configures one step of a sweep.
type SweepOptions struct {
	// ContestID limits the sweep to a contest (0: every contest).
	ContestID int32
	// Cursor is where the previous step ended (zero: the beginning).
	Cursor SweepCursor
	// Window is how many ids a step looks at (default 5000): it bounds
	// the rows a step reads when there is nothing to do.
	Window int32
	// Limit is the largest number of submissions (or user tests) with
	// work a step returns (default 500).
	Limit int32
	// ArchiveSandbox is passed to the operations.
	ArchiveSandbox bool
}

// SweepResult is the outcome of a step.
type SweepResult struct {
	Work
	// Next is the cursor of the next step; Done is set once every
	// submission and user test that existed at the start of the step has
	// been looked at.
	Next SweepCursor
	Done bool
}

// Sweep is one step of a keyset sweep over the submissions, then the user
// tests, of a contest (upstream _missing_operations of EvaluationService
// and ScoringService): the work of the next submissions that have some,
// found without reading the finished results (migration 0004). Call it
// with the returned cursor until Done; the dispatcher runs a sweep at
// start and periodically, so lost notifications only delay work.
func (j *Judge) Sweep(ctx context.Context, opts SweepOptions) (*SweepResult, error) {
	if opts.Window <= 0 {
		opts.Window = 5000
	}
	if opts.Limit <= 0 {
		opts.Limit = 500
	}
	max, err := j.store.Q.JudgingMaxIDs(ctx)
	if err != nil {
		return nil, err
	}
	res := &SweepResult{Next: opts.Cursor}
	upto := func(after, max int32) int32 {
		if after >= max-opts.Window {
			return max
		}
		return after + opts.Window
	}
	switch {
	case opts.Cursor.SubmissionID < max.MaxSubmissionID:
		after := opts.Cursor.SubmissionID
		to := upto(after, max.MaxSubmissionID)
		ids, err := j.store.Q.JudgingSweepSubmissions(ctx, q.JudgingSweepSubmissionsParams{
			After: after, Upto: to, ContestID: opts.ContestID, MaxRows: opts.Limit,
			MaxCompilationTries: MaxCompilationTries, MaxEvaluationTries: MaxEvaluationTries,
		})
		if err != nil {
			return nil, err
		}
		res.Next.SubmissionID = to
		if int32(len(ids)) == opts.Limit {
			// More may be left in the window: continue after the last.
			res.Next.SubmissionID = ids[len(ids)-1]
		}
		if len(ids) > 0 {
			w, err := j.Pending(ctx, Targets{SubmissionIDs: ids, ArchiveSandbox: opts.ArchiveSandbox})
			if err != nil {
				return nil, err
			}
			res.Work = *w
		}
	case opts.Cursor.UserTestID < max.MaxUserTestID:
		after := opts.Cursor.UserTestID
		to := upto(after, max.MaxUserTestID)
		ids, err := j.store.Q.JudgingSweepUserTests(ctx, q.JudgingSweepUserTestsParams{
			After: after, Upto: to, ContestID: opts.ContestID, MaxRows: opts.Limit,
			MaxCompilationTries: MaxUserTestCompilationTries, MaxEvaluationTries: MaxUserTestEvaluationTries,
		})
		if err != nil {
			return nil, err
		}
		res.Next.UserTestID = to
		if int32(len(ids)) == opts.Limit {
			res.Next.UserTestID = ids[len(ids)-1]
		}
		if len(ids) > 0 {
			w, err := j.Pending(ctx, Targets{UserTestIDs: ids, ArchiveSandbox: opts.ArchiveSandbox})
			if err != nil {
				return nil, err
			}
			res.Work = *w
		}
	}
	res.Done = res.Next.SubmissionID >= max.MaxSubmissionID && res.Next.UserTestID >= max.MaxUserTestID
	return res, nil
}
