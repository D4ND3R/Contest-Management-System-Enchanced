// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package judging

import (
	"context"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// DatasetStatus counts the submissions (or user tests) of a task by the
// status of their result on one dataset to judge.
type DatasetStatus struct {
	TaskID             int32
	TaskName           string
	DatasetID          int32
	DatasetDescription string
	Active             bool
	Total              int64
	Compiling          int64
	CompilationGaveUp  int64
	CompilationFailed  int64
	Evaluating         int64
	EvaluationGaveUp   int64
	// Scoring and Scored for submissions; Evaluated for user tests.
	Scoring   int64
	Scored    int64
	Evaluated int64
}

// StatusReport is the judging status of a contest.
type StatusReport struct {
	Submissions []DatasetStatus
	UserTests   []DatasetStatus
}

// Status counts the submissions and user tests of a contest (0: every
// contest) by status on each dataset to judge (`cms ctl judging-status`).
func (j *Judge) Status(ctx context.Context, contestID int32) (*StatusReport, error) {
	subs, err := j.store.Q.JudgingSubmissionStatusCounts(ctx, q.JudgingSubmissionStatusCountsParams{
		MaxCompilationTries: j.tries.Compilation, MaxEvaluationTries: j.tries.Evaluation, ContestID: contestID,
	})
	if err != nil {
		return nil, err
	}
	uts, err := j.store.Q.JudgingUserTestStatusCounts(ctx, q.JudgingUserTestStatusCountsParams{
		MaxCompilationTries: j.tries.UserTestCompilation, MaxEvaluationTries: j.tries.UserTestEvaluation, ContestID: contestID,
	})
	if err != nil {
		return nil, err
	}
	r := &StatusReport{}
	for _, s := range subs {
		r.Submissions = append(r.Submissions, DatasetStatus{
			TaskID: s.TaskID, TaskName: s.TaskName, DatasetID: s.DatasetID, DatasetDescription: s.DatasetDescription,
			Active: s.Active, Total: s.Submissions, Compiling: s.Compiling, CompilationGaveUp: s.CompilationGaveUp,
			CompilationFailed: s.CompilationFailed, Evaluating: s.Evaluating, EvaluationGaveUp: s.EvaluationGaveUp,
			Scoring: s.Scoring, Scored: s.Scored,
		})
	}
	for _, s := range uts {
		r.UserTests = append(r.UserTests, DatasetStatus{
			TaskID: s.TaskID, TaskName: s.TaskName, DatasetID: s.DatasetID, DatasetDescription: s.DatasetDescription,
			Active: s.Active, Total: s.UserTests, Compiling: s.Compiling, CompilationGaveUp: s.CompilationGaveUp,
			CompilationFailed: s.CompilationFailed, Evaluating: s.Evaluating, EvaluationGaveUp: s.EvaluationGaveUp,
			Evaluated: s.Evaluated,
		})
	}
	return r, nil
}

// Progress counts the unfinished submission results of a contest on its
// datasets to judge (the live counters of the admin pages).
type Progress struct {
	// ContestID is 0 for tasks without a contest.
	ContestID int32
	// Compiling, Evaluating and Scoring are the results waiting for a
	// compilation, for evaluations and for their score; those that gave
	// up are not counted.
	Compiling  int64
	Evaluating int64
	Scoring    int64
}

// Progress returns the unfinished results of every contest that has some,
// in one query that reads only unfinished results (the dispatcher calls
// it at most once a second while judging).
func (j *Judge) Progress(ctx context.Context) ([]Progress, error) {
	rows, err := j.store.Q.JudgingProgress(ctx, q.JudgingProgressParams{
		MaxCompilationTries: j.tries.Compilation, MaxEvaluationTries: j.tries.Evaluation,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Progress, len(rows))
	for i, r := range rows {
		out[i] = Progress{ContestID: r.ContestID, Compiling: r.Compiling, Evaluating: r.Evaluating, Scoring: r.Scoring}
	}
	return out, nil
}
