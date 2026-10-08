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
		MaxCompilationTries: MaxCompilationTries, MaxEvaluationTries: MaxEvaluationTries, ContestID: contestID,
	})
	if err != nil {
		return nil, err
	}
	uts, err := j.store.Q.JudgingUserTestStatusCounts(ctx, q.JudgingUserTestStatusCountsParams{
		MaxCompilationTries: MaxUserTestCompilationTries, MaxEvaluationTries: MaxUserTestEvaluationTries, ContestID: contestID,
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
