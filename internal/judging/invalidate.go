// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from invalidate_submission in cms/service/EvaluationService.py
// and cms/service/ScoringService.py of CMS:
// Copyright © 2010-2014 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2013-2015 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2013 Bernard Blackham <bernard@largestprime.net>
// Copyright © 2014 Artem Iglikov <artem.iglikov@gmail.com>

package judging

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// Level is how much of a result an invalidation throws away (upstream
// invalidate_compilation, invalidate_evaluation, invalidate_score).
type Level string

// The levels.
const (
	// LevelCompilation recompiles: compilation, executables, evaluations
	// and score are dropped.
	LevelCompilation Level = "compilation"
	// LevelEvaluation reevaluates: the compilation is kept, the
	// evaluations (or those of one testcase) and the score are dropped.
	LevelEvaluation Level = "evaluation"
	// LevelScore rescores: only the score is dropped.
	LevelScore Level = "score"
)

// Valid reports whether l is a level.
func (l Level) Valid() bool {
	return l == LevelCompilation || l == LevelEvaluation || l == LevelScore
}

// Scope selects what an invalidation applies to, as the arguments of
// upstream invalidate_submission: the results, on every dataset (or on
// DatasetID), of the submissions that match every field set. At least one
// must be set.
type Scope struct {
	ContestID       int32
	TaskID          int32
	ParticipationID int32
	// UserID selects the submissions of every participation of the user.
	UserID        int32
	SubmissionIDs []int32
	// DatasetID limits the results to a dataset (and the submissions to
	// its task).
	DatasetID int32
	// TestcaseID limits an evaluation-level invalidation to the
	// evaluations of one testcase (and the results to its dataset).
	TestcaseID int32
	// UserTestIDs invalidates user tests too (compilation and evaluation
	// levels), on every dataset or on DatasetID.
	UserTestIDs []int32
}

func (s Scope) selectsSubmissions() bool {
	return s.ContestID != 0 || s.TaskID != 0 || s.ParticipationID != 0 || s.UserID != 0 ||
		len(s.SubmissionIDs) > 0 || s.DatasetID != 0 && len(s.UserTestIDs) == 0 || s.TestcaseID != 0
}

// Errors of Invalidate.
var (
	// ErrEmptyScope: no field of the scope is set (invalidating every
	// result of every contest is refused).
	ErrEmptyScope = errors.New("judging: empty invalidation scope")
	// ErrLevel: unknown level, or a testcase with a level other than
	// evaluation.
	ErrLevel = errors.New("judging: invalid invalidation level")
)

// Invalidation is what Invalidate did.
type Invalidation struct {
	Level Level
	// Submissions are every submission in the scope, with or without
	// results: Pending gives the work to do again (the dispatcher does it
	// when notified with the events of Events).
	Submissions []int32
	// Results are the submission results invalidated.
	Results []ResultKey
	// UserTests and UserTestResults are the user tests in the scope and
	// their results invalidated.
	UserTests       []int32
	UserTestResults []UserTestKey
	// TaskScores are the cached task scores that changed (the
	// invalidated scores are unknown until computed again: partial).
	TaskScores []TaskScoreChange
}

// Invalidate throws away part of the results in the scope, in one
// transaction (upstream invalidate_submission of EvaluationService for the
// compilation and evaluation levels, of ScoringService for the score
// level). The compilation and evaluation levels increment the generation
// of the results, so the results of the jobs in flight for them are
// dropped when they arrive. The task score cache is updated. Notify the
// dispatcher with inv.Events() afterwards: it computes and enqueues the
// work (Pending) and scores again.
func (j *Judge) Invalidate(ctx context.Context, scope Scope, level Level) (*Invalidation, error) {
	if !level.Valid() || scope.TestcaseID != 0 && level != LevelEvaluation {
		return nil, fmt.Errorf("%w: %q", ErrLevel, level)
	}
	if !scope.selectsSubmissions() && len(scope.UserTestIDs) == 0 {
		return nil, ErrEmptyScope
	}
	var inv *Invalidation
	err := j.inTx(ctx, func(tx pgx.Tx, qq *q.Queries) error {
		inv = &Invalidation{Level: level}
		if scope.selectsSubmissions() {
			if err := invalidateSubmissions(ctx, tx, qq, scope, level, inv); err != nil {
				return err
			}
		}
		if len(scope.UserTestIDs) > 0 && level != LevelScore {
			if err := invalidateUserTests(ctx, qq, scope, level, inv); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	j.log.Info("results invalidated", "level", string(level), "submissions", len(inv.Submissions),
		"results", len(inv.Results), "user_tests", len(inv.UserTests))
	return inv, nil
}

// scopeSQL is the WHERE condition of the submissions s (joined to their
// task t) in the scope, and of their results sr.
func scopeSQL(s Scope, args *[]any) (subConds, resultConds []string) {
	arg := func(v any) string {
		*args = append(*args, v)
		return "$" + strconv.Itoa(len(*args))
	}
	if s.ContestID != 0 {
		subConds = append(subConds, "t.contest_id = "+arg(s.ContestID))
	}
	if s.TaskID != 0 {
		subConds = append(subConds, "s.task_id = "+arg(s.TaskID))
	}
	if s.ParticipationID != 0 {
		subConds = append(subConds, "s.participation_id = "+arg(s.ParticipationID))
	}
	if s.UserID != 0 {
		subConds = append(subConds, "s.participation_id IN (SELECT p.id FROM participations p WHERE p.user_id = "+arg(s.UserID)+")")
	}
	if len(s.SubmissionIDs) > 0 {
		subConds = append(subConds, "s.id = ANY("+arg(uniq(s.SubmissionIDs))+"::integer[])")
	}
	if s.DatasetID != 0 {
		p := arg(s.DatasetID)
		subConds = append(subConds, "s.task_id = (SELECT d.task_id FROM datasets d WHERE d.id = "+p+")")
		resultConds = append(resultConds, "sr.dataset_id = "+p)
	}
	if s.TestcaseID != 0 {
		p := arg(s.TestcaseID)
		subConds = append(subConds, "s.task_id = (SELECT d.task_id FROM testcases tc JOIN datasets d ON d.id = tc.dataset_id WHERE tc.id = "+p+")")
		resultConds = append(resultConds, "sr.dataset_id = (SELECT tc.dataset_id FROM testcases tc WHERE tc.id = "+p+")")
	}
	return subConds, resultConds
}

func invalidateSubmissions(ctx context.Context, tx pgx.Tx, qq *q.Queries, scope Scope, level Level, inv *Invalidation) error {
	var args []any
	subConds, resultConds := scopeSQL(scope, &args)
	where := " WHERE " + strings.Join(subConds, " AND ")
	rows, err := tx.Query(ctx, "SELECT s.id FROM submissions s JOIN tasks t ON t.id = s.task_id"+where+" ORDER BY s.id", args...)
	if err != nil {
		return err
	}
	inv.Submissions, err = pgx.CollectRows(rows, pgx.RowTo[int32])
	if err != nil {
		return err
	}
	if len(inv.Submissions) == 0 {
		return nil
	}
	// Lock the results in a fixed order, and share-lock their tasks, as
	// Apply does.
	type locked struct {
		key             ResultKey
		participationID int32
		taskID          int32
		active          bool
	}
	rows, err = tx.Query(ctx, `SELECT sr.submission_id, sr.dataset_id, s.participation_id, s.task_id,
			coalesce(sr.dataset_id = t.active_dataset_id, false)
		FROM submission_results sr
		JOIN submissions s ON s.id = sr.submission_id
		JOIN tasks t ON t.id = s.task_id`+
		" WHERE "+strings.Join(append(subConds, resultConds...), " AND ")+
		" ORDER BY sr.submission_id, sr.dataset_id FOR UPDATE OF sr FOR SHARE OF t", args...)
	if err != nil {
		return err
	}
	results, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (locked, error) {
		var l locked
		err := r.Scan(&l.key.SubmissionID, &l.key.DatasetID, &l.participationID, &l.taskID, &l.active)
		return l, err
	})
	if err != nil {
		return err
	}
	if len(results) == 0 {
		return nil
	}
	var sids, dids []int32
	for _, r := range results {
		sids = append(sids, r.key.SubmissionID)
		dids = append(dids, r.key.DatasetID)
	}
	type keyRow struct{ SubmissionID, DatasetID int32 }
	var done []keyRow
	switch level {
	case LevelCompilation:
		rows, err := qq.JudgingInvalidateCompilations(ctx, q.JudgingInvalidateCompilationsParams{SubmissionIds: sids, DatasetIds: dids})
		if err != nil {
			return err
		}
		for _, r := range rows {
			done = append(done, keyRow{r.SubmissionID, r.DatasetID})
		}
	case LevelEvaluation:
		rows, err := qq.JudgingInvalidateEvaluations(ctx, q.JudgingInvalidateEvaluationsParams{
			SubmissionIds: sids, DatasetIds: dids, TestcaseID: scope.TestcaseID,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			done = append(done, keyRow{r.SubmissionID, r.DatasetID})
		}
	case LevelScore:
		rows, err := qq.JudgingInvalidateScores(ctx, q.JudgingInvalidateScoresParams{SubmissionIds: sids, DatasetIds: dids})
		if err != nil {
			return err
		}
		for _, r := range rows {
			done = append(done, keyRow{r.SubmissionID, r.DatasetID})
		}
	}
	changed := make(map[ResultKey]bool, len(done))
	for _, d := range done {
		k := ResultKey{d.SubmissionID, d.DatasetID}
		changed[k] = true
		inv.Results = append(inv.Results, k)
	}
	var keys []TaskKey
	for _, r := range results {
		if r.active && changed[r.key] {
			keys = append(keys, TaskKey{r.participationID, r.taskID})
		}
	}
	if len(keys) > 0 {
		inv.TaskScores, err = refreshTaskScores(ctx, qq, keys)
	}
	return err
}

func invalidateUserTests(ctx context.Context, qq *q.Queries, scope Scope, level Level, inv *Invalidation) error {
	inv.UserTests = uniq(scope.UserTestIDs)
	rows, err := qq.JudgingLockUserTestResultsByID(ctx, q.JudgingLockUserTestResultsByIDParams{
		UserTestIds: inv.UserTests, DatasetID: scope.DatasetID,
	})
	if err != nil || len(rows) == 0 {
		return err
	}
	var ids, dids []int32
	for _, r := range rows {
		ids = append(ids, r.UserTestID)
		dids = append(dids, r.DatasetID)
	}
	if level == LevelCompilation {
		done, err := qq.JudgingInvalidateUserTestCompilations(ctx, q.JudgingInvalidateUserTestCompilationsParams{UserTestIds: ids, DatasetIds: dids})
		if err != nil {
			return err
		}
		for _, d := range done {
			inv.UserTestResults = append(inv.UserTestResults, UserTestKey{d.UserTestID, d.DatasetID})
		}
		return nil
	}
	done, err := qq.JudgingInvalidateUserTestEvaluations(ctx, q.JudgingInvalidateUserTestEvaluationsParams{UserTestIds: ids, DatasetIds: dids})
	if err != nil {
		return err
	}
	for _, d := range done {
		inv.UserTestResults = append(inv.UserTestResults, UserTestKey{d.UserTestID, d.DatasetID})
	}
	return nil
}
