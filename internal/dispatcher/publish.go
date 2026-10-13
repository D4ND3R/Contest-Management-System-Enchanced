// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package dispatcher

import (
	"context"
	"slices"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
)

// ScoreSink receives, after each commit, what changed for the public
// scoreboard: the hook of upstream ProxyService (submission_scored,
// submission_tokened, dataset_updated). Phase F7 implements it (the push
// to ranking-web); until then the dispatcher uses NopScoreSink.
//
// Push is called by the main loop of the active dispatcher, so it must not
// block: an implementation queues the update and sends it in the
// background. Updates are not durable (a crash after the commit loses the
// last ones), so the implementation must also resend everything
// periodically or when the ranking restarts, as upstream does.
type ScoreSink interface {
	Push(ctx context.Context, u ScoreUpdate)
}

// ScoreUpdate is what changed for the scoreboard. Slices may be empty but
// an update is only pushed when something is set.
type ScoreUpdate struct {
	// Submissions are official submissions whose result on the active
	// dataset of their task got a score (Scored) or lost it (an
	// invalidation, then Scored is false).
	Submissions []judging.SubmissionChange
	// Tokens are submissions on which a token was played.
	Tokens []int32
	// Tasks are tasks whose active dataset changed or was edited: every
	// score of the task must be sent again.
	Tasks []int32
	// TaskScores are the cached task scores that changed.
	TaskScores []judging.TaskScoreChange
}

func (u *ScoreUpdate) empty() bool {
	return len(u.Submissions) == 0 && len(u.Tokens) == 0 && len(u.Tasks) == 0 && len(u.TaskScores) == 0
}

// NopScoreSink drops every update (until F7).
type NopScoreSink struct{}

// Push implements ScoreSink.
func (NopScoreSink) Push(context.Context, ScoreUpdate) {}

// published turns committed changes into live events and a scoreboard
// update. It runs in the main loop after the transaction committed.
func (l *leader) published(ctx context.Context, ch *judging.Changes, extra ScoreUpdate) {
	d := l.d
	var evs []live.Event
	upd := extra
	now := time.Now()
	for _, s := range ch.Submissions {
		evs = append(evs, live.Submission{
			Contest: int64(s.ContestID), Participation: int64(s.ParticipationID), Task: int64(s.TaskID),
			Dataset: int64(s.DatasetID), ID: int64(s.SubmissionID), Status: string(s.Status),
			ActiveDataset: s.Active,
		}.Event())
		if s.Active && s.Official {
			upd.Submissions = append(upd.Submissions, s)
		}
		done := s.Scored || s.Status == judging.StatusCompilationFailed
		if s.Active && done {
			if _, ok := l.fresh[s.SubmissionID]; ok {
				delete(l.fresh, s.SubmissionID)
				d.m.latency.Observe(now.Sub(s.Timestamp).Seconds())
			}
		}
		if done {
			delete(l.background, judging.ResultKey{SubmissionID: s.SubmissionID, DatasetID: s.DatasetID})
		}
	}
	for _, u := range ch.UserTests {
		if !u.Active {
			continue // user tests on other datasets interest nobody
		}
		evs = append(evs, live.UserTest{
			Contest: int64(u.ContestID), Participation: int64(u.ParticipationID), Task: int64(u.TaskID),
			ID: int64(u.UserTestID), Status: string(u.Status),
		}.Event())
	}
	evs = append(evs, taskScoreEvents(ch.TaskScores)...)
	upd.TaskScores = append(upd.TaskScores, ch.TaskScores...)
	if len(evs) > 0 {
		d.live.Publish(evs...)
	}
	if !upd.empty() {
		d.m.scoreUpdates.Inc()
		d.scores.Push(ctx, upd)
	}
	for _, g := range ch.GaveUp {
		d.m.gaveUp.WithLabelValues(string(g.Type)).Inc()
	}
	d.m.scoreErrors.Add(float64(len(ch.ScoreErrors)))
	if len(ch.Submissions)+len(ch.UserTests)+len(ch.TaskScores) > 0 {
		l.dirty = true
	}
}

// taskScoreEvents returns the events of task score changes: one
// task_score per contestant and task, and one scoreboard event per contest
// listing the contestants whose score changed (the publisher merges them
// to at most one per second).
func taskScoreEvents(changes []judging.TaskScoreChange) []live.Event {
	if len(changes) == 0 {
		return nil
	}
	var evs []live.Event
	board := map[int32][]int64{}
	for _, c := range changes {
		evs = append(evs, live.TaskScore{
			Contest: int64(c.ContestID), Participation: int64(c.ParticipationID), Task: int64(c.TaskID),
		}.Event())
		if c.ContestID != 0 && (c.Old.Score != c.New.Score || c.Old.Partial != c.New.Partial) {
			board[c.ContestID] = append(board[c.ContestID], int64(c.ParticipationID))
		}
	}
	contests := make([]int32, 0, len(board))
	for c := range board {
		contests = append(contests, c)
	}
	slices.Sort(contests)
	for _, c := range contests {
		evs = append(evs, live.ScoreboardChange{Contest: int64(c), Participations: board[c]}.Event())
	}
	return evs
}
