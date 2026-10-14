// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package judging

import (
	"context"
	"errors"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/scoring"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// TaskScore is a row of the task score cache (table task_scores): the
// score of a participant on a task as upstream scoring.task_score computes
// it. A participant without a row scores 0 and is not partial.
type TaskScore struct {
	// DatasetID is the active dataset the scores were computed on (0:
	// the task has none).
	DatasetID int32
	// Score is task_score(participation, task): admins and rankings.
	Score float64
	// PublicScore is task_score(..., public=True): what the contestant
	// always sees.
	PublicScore float64
	// TokenedScore is task_score(..., only_tokened=True): what the
	// contestant knows of Score.
	TokenedScore float64
	// Partial: some official submission is not scored yet.
	Partial bool
}

// TaskScoreChange is a cached task score that changed.
type TaskScoreChange struct {
	TaskKey
	// ContestID is the contest of the task (0: none), for the live events.
	ContestID int32
	Old, New  TaskScore
}

// taskScoreChunk bounds the participants and tasks recomputed per round of
// queries (the submissions of 2000 participants on a task).
const taskScoreChunk = 2000

// RefreshTaskScores recomputes the cached scores of the given participants
// on the given tasks, inside tx, and returns those that changed. The
// contestant site calls it in the transaction that inserts a token (the
// tokened score and, in the max_tokened_last mode, the score change) or a
// submission (the scores become partial, and the last submission counts
// as 0 until scored in max_tokened_last); otherwise the dispatcher fixes
// the cache when it handles the notification, which the site sends (Notify)
// after tx commits, not inside it. The rows stay locked until tx ends, so
// concurrent refreshes of the same participant and task run one after the
// other.
func RefreshTaskScores(ctx context.Context, tx pgx.Tx, keys []TaskKey) ([]TaskScoreChange, error) {
	return refreshTaskScores(ctx, q.New(tx), keys)
}

func refreshTaskScores(ctx context.Context, qq *q.Queries, keys []TaskKey) ([]TaskScoreChange, error) {
	keys = slices.Clone(keys)
	slices.SortFunc(keys, func(x, y TaskKey) int {
		if x.ParticipationID != y.ParticipationID {
			return int(x.ParticipationID - y.ParticipationID)
		}
		return int(x.TaskID - y.TaskID)
	})
	keys = slices.Compact(keys)
	var changes []TaskScoreChange
	for len(keys) > 0 {
		n := min(len(keys), taskScoreChunk)
		c, err := refreshChunk(ctx, qq, keys[:n])
		if err != nil {
			return nil, err
		}
		changes = append(changes, c...)
		keys = keys[n:]
	}
	return changes, nil
}

// refreshChunk recomputes sorted, distinct keys in four queries.
func refreshChunk(ctx context.Context, qq *q.Queries, keys []TaskKey) ([]TaskScoreChange, error) {
	var parts, tasks []int32
	for _, k := range keys {
		parts = append(parts, k.ParticipationID)
		tasks = append(tasks, k.TaskID)
	}
	if err := qq.JudgingEnsureTaskScores(ctx, q.JudgingEnsureTaskScoresParams{ParticipationIds: parts, TaskIds: tasks}); err != nil {
		return nil, err
	}
	locked, err := qq.JudgingLockTaskScores(ctx, q.JudgingLockTaskScoresParams{ParticipationIds: parts, TaskIds: tasks})
	if err != nil {
		return nil, err
	}
	old := make(map[TaskKey]TaskScore, len(locked))
	for _, r := range locked {
		old[TaskKey{r.ParticipationID, r.TaskID}] = TaskScore{
			DatasetID: orZero(r.DatasetID), Score: r.Score, PublicScore: r.PublicScore,
			TokenedScore: r.TokenedScore, Partial: r.Partial,
		}
	}
	inputs, err := qq.JudgingTaskScoreInputs(ctx, q.JudgingTaskScoreInputsParams{ParticipationIds: parts, TaskIds: tasks})
	if err != nil {
		return nil, err
	}

	var upd q.JudgingUpdateTaskScoresParams
	var changes []TaskScoreChange
	for i := 0; i < len(inputs); {
		first := inputs[i]
		k := TaskKey{first.ParticipationID, first.TaskID}
		var subs []scoring.Submission
		for ; i < len(inputs) && inputs[i].ParticipationID == k.ParticipationID && inputs[i].TaskID == k.TaskID; i++ {
			r := &inputs[i]
			if r.SubmissionID == nil {
				continue // no submissions
			}
			subs = append(subs, scoring.Submission{
				Timestamp:          orZero(r.Timestamp),
				Unofficial:         !orZero(r.Official),
				Tokened:            r.Tokened,
				Scored:             r.Scored,
				Score:              orZero(r.Score),
				ScoreDetails:       r.SubtaskScores,
				PublicScore:        orZero(r.PublicScore),
				PublicScoreDetails: r.PublicSubtaskScores,
			})
		}
		cur, ok := old[k]
		if !ok {
			continue // the participation is gone
		}
		nw, err := computeTaskScore(subs, first.ScoreMode, int(first.ScorePrecision))
		if err != nil {
			return nil, err
		}
		nw.DatasetID = orZero(first.ActiveDatasetID)
		if nw == cur {
			continue
		}
		changes = append(changes, TaskScoreChange{TaskKey: k, ContestID: orZero(first.ContestID), Old: cur, New: nw})
		upd.ParticipationIds = append(upd.ParticipationIds, k.ParticipationID)
		upd.TaskIds = append(upd.TaskIds, k.TaskID)
		upd.DatasetIds = append(upd.DatasetIds, nw.DatasetID)
		upd.Scores = append(upd.Scores, nw.Score)
		upd.PublicScores = append(upd.PublicScores, nw.PublicScore)
		upd.TokenedScores = append(upd.TokenedScores, nw.TokenedScore)
		upd.Partials = append(upd.Partials, nw.Partial)
	}
	if len(changes) > 0 {
		if err := qq.JudgingUpdateTaskScores(ctx, upd); err != nil {
			return nil, err
		}
	}
	return changes, nil
}

// computeTaskScore is upstream task_score with its three views.
func computeTaskScore(subs []scoring.Submission, mode string, precision int) (TaskScore, error) {
	var ts TaskScore
	var err error
	if ts.Score, ts.Partial, err = scoring.TaskScore(subs, mode, precision, scoring.Full); err != nil {
		return ts, err
	}
	if ts.PublicScore, _, err = scoring.TaskScore(subs, mode, precision, scoring.Public); err != nil {
		return ts, err
	}
	if ts.TokenedScore, _, err = scoring.TaskScore(subs, mode, precision, scoring.OnlyTokened); err != nil {
		return ts, err
	}
	return ts, nil
}

// RecomputeTask recomputes, inside tx, the cached scores of every
// participant on a task, and returns those that changed. The
// administration calls it in the transaction that changes the score mode
// or the score precision of the task, or its active dataset (see also
// ActivateDataset).
func RecomputeTask(ctx context.Context, tx pgx.Tx, taskID int32) ([]TaskScoreChange, error) {
	return recomputeTask(ctx, q.New(tx), taskID)
}

func recomputeTask(ctx context.Context, qq *q.Queries, taskID int32) ([]TaskScoreChange, error) {
	parts, err := qq.JudgingTaskParticipations(ctx, taskID)
	if err != nil {
		return nil, err
	}
	keys := make([]TaskKey, len(parts))
	for i, p := range parts {
		keys[i] = TaskKey{p, taskID}
	}
	return refreshTaskScores(ctx, qq, keys)
}

// ErrNotDataset: the dataset does not exist or belongs to another task.
var ErrNotDataset = errors.New("judging: not a dataset of the task")

// ActivateDataset makes a dataset the active one of its task (R4.5) and
// recomputes the cached scores of the task with the results on it, in one
// transaction. It returns the previous active dataset (0: none). The
// submissions not judged on the new dataset yet make the scores partial:
// notify the dispatcher (EventDataset), which judges them.
func (j *Judge) ActivateDataset(ctx context.Context, taskID, datasetID int32) (previous int32, changes []TaskScoreChange, err error) {
	err = j.inTx(ctx, func(_ pgx.Tx, qq *q.Queries) error {
		prev, err := qq.JudgingSetActiveDataset(ctx, q.JudgingSetActiveDatasetParams{TaskID: taskID, DatasetID: datasetID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotDataset
		}
		if err != nil {
			return err
		}
		previous = orZero(prev)
		changes, err = recomputeTask(ctx, qq, taskID)
		return err
	})
	if err != nil {
		return 0, nil, err
	}
	return previous, changes, nil
}

// RefreshTaskScoresOf recomputes the cached scores of the participants and
// tasks of some submissions, each in its own transaction of at most
// taskScoreChunk pairs. The dispatcher calls it for new submissions (in
// case the contestant site did not) and tokens.
func (j *Judge) RefreshTaskScoresOf(ctx context.Context, submissionIDs []int32) ([]TaskScoreChange, error) {
	if len(submissionIDs) == 0 {
		return nil, nil
	}
	rows, err := j.store.Q.JudgingSubmissionTaskKeys(ctx, uniq(submissionIDs))
	if err != nil {
		return nil, err
	}
	keys := make([]TaskKey, len(rows))
	for i, r := range rows {
		keys[i] = TaskKey{r.ParticipationID, r.TaskID}
	}
	return j.refreshInChunks(ctx, keys)
}

// RefreshTaskScoresFor recomputes the cached scores of the given
// participants and tasks, creating the missing rows, each chunk of at most
// taskScoreChunk pairs in its own transaction, and returns those that
// changed. The dispatcher calls it with the MissingTaskScores of each
// sweep step.
func (j *Judge) RefreshTaskScoresFor(ctx context.Context, keys []TaskKey) ([]TaskScoreChange, error) {
	return j.refreshInChunks(ctx, keys)
}

// RepairTaskScores recomputes the cached scores computed on a dataset that
// is not the active one any more (the active dataset changed outside
// ActivateDataset and RecomputeTask, e.g. by a dump import), at most 2000
// per call. The dispatcher calls it at the end of each sweep (the steps
// create the rows that are missing, Sweep).
func (j *Judge) RepairTaskScores(ctx context.Context, contestID int32) ([]TaskScoreChange, error) {
	rows, err := j.store.Q.JudgingStaleTaskScores(ctx, q.JudgingStaleTaskScoresParams{
		ContestID: contestID, MaxRows: taskScoreChunk,
	})
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	keys := make([]TaskKey, len(rows))
	for i, r := range rows {
		keys[i] = TaskKey{r.ParticipationID, r.TaskID}
	}
	return j.refreshInChunks(ctx, keys)
}

func (j *Judge) refreshInChunks(ctx context.Context, keys []TaskKey) ([]TaskScoreChange, error) {
	var all []TaskScoreChange
	for len(keys) > 0 {
		n := min(len(keys), taskScoreChunk)
		chunk := keys[:n]
		keys = keys[n:]
		var changes []TaskScoreChange
		err := j.inTx(ctx, func(_ pgx.Tx, qq *q.Queries) error {
			var err error
			changes, err = refreshTaskScores(ctx, qq, chunk)
			return err
		})
		if err != nil {
			return nil, err
		}
		all = append(all, changes...)
	}
	return all, nil
}
