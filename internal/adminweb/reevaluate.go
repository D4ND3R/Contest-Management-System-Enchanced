// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from invalidate_submission in cms/service/EvaluationService.py and
// cms/service/ScoringService.py of CMS (the RPC methods that the
// reevaluation buttons of the administration call):
// Copyright © 2010-2014 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2013-2015 Luca Wehrstedt <luca.wehrstedt@gmail.com>

package adminweb

// Reevaluation (R4.4, R6.3): the RPC invalidate_submission of
// EvaluationService (recompile, reevaluate: levels "compilation" and
// "evaluation") and of ScoringService (rescore), which the C, E and S
// buttons of macro/reevaluation_buttons.html call for a submission on a
// dataset, a dataset, a participation, a task, a user or a contest, and
// the "Rerun and archive" buttons of the submission page (one testcase,
// archive_sandbox). judging.Invalidate does the work in its own
// transaction; its audit entry is a separate Write right after; then the
// dispatcher is notified. As in CMS the methods need permission_all (CMS
// rpc_authorization: invalidate_submission is not allowed to the other
// roles); the problem preparer cannot reevaluate contestants'
// submissions.

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// invalidateArgs are the arguments of invalidate_submission.
type invalidateArgs struct {
	ContestID       *int32 `json:"contest_id"`
	SubmissionID    *int32 `json:"submission_id"`
	DatasetID       *int32 `json:"dataset_id"`
	TestcaseID      *int32 `json:"testcase_id"`
	ParticipationID *int32 `json:"participation_id"`
	TaskID          *int32 `json:"task_id"`
	// UserID (new) selects every participation of a user.
	UserID         *int32  `json:"user_id"`
	Level          *string `json:"level"`
	ArchiveSandbox bool    `json:"archive_sandbox"`
}

func val(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}

// rpcInvalidateSubmission is EvaluationService.invalidate_submission and
// ScoringService.invalidate_submission.
func (s *Server) rpcInvalidateSubmission(c *Ctx, raw json.RawMessage) (any, error) {
	var a invalidateArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	service := c.R.PathValue("service")
	var level judging.Level
	if service == "ScoringService" {
		level = judging.LevelScore
	} else {
		level = judging.LevelCompilation
		if a.Level != nil {
			level = judging.Level(*a.Level)
		}
		if level != judging.LevelCompilation && level != judging.LevelEvaluation {
			return nil, fmt.Errorf("Unexpected invalidation level `%s'.", level)
		}
	}
	scope := judging.Scope{
		TaskID: val(a.TaskID), ParticipationID: val(a.ParticipationID), UserID: val(a.UserID),
		DatasetID: val(a.DatasetID), TestcaseID: val(a.TestcaseID),
	}
	if a.SubmissionID != nil {
		scope.SubmissionIDs = []int32{*a.SubmissionID}
	}
	// EvaluationService uses the contest only when nothing narrower is
	// given; ScoringService filters on everything given.
	if level == judging.LevelScore || scope.TaskID == 0 && scope.ParticipationID == 0 && scope.UserID == 0 &&
		len(scope.SubmissionIDs) == 0 && scope.DatasetID == 0 && scope.TestcaseID == 0 {
		scope.ContestID = val(a.ContestID)
	}
	inv, err := s.judge.Invalidate(c.Context(), scope, level)
	switch {
	case errors.Is(err, judging.ErrEmptyScope):
		return nil, errors.New("Nothing to invalidate: give a submission, dataset, participation, task or contest.")
	case errors.Is(err, judging.ErrLevel):
		return nil, fmt.Errorf("Unexpected invalidation level `%s'.", level)
	case err != nil:
		return nil, err
	}

	// The audit entry (Invalidate committed its own transaction).
	target, targetID := "contest", int64(scope.ContestID)
	switch {
	case len(scope.SubmissionIDs) > 0:
		target, targetID = "submission", int64(scope.SubmissionIDs[0])
	case scope.TestcaseID != 0:
		target, targetID = "testcase", int64(scope.TestcaseID)
	case scope.DatasetID != 0:
		target, targetID = "dataset", int64(scope.DatasetID)
	case scope.ParticipationID != 0:
		target, targetID = "participation", int64(scope.ParticipationID)
	case scope.TaskID != 0:
		target, targetID = "task", int64(scope.TaskID)
	case scope.UserID != 0:
		target, targetID = "user", int64(scope.UserID)
	}
	action := map[judging.Level]string{
		judging.LevelCompilation: "submission.recompile",
		judging.LevelEvaluation:  "submission.reevaluate",
		judging.LevelScore:       "submission.rescore",
	}[level]
	details := map[string]any{"level": string(level), "submissions": len(inv.Submissions),
		"results": len(inv.Results), "task_scores": len(inv.TaskScores)}
	for k, v := range map[string]*int32{"contest_id": a.ContestID, "submission_id": a.SubmissionID,
		"dataset_id": a.DatasetID, "testcase_id": a.TestcaseID, "participation_id": a.ParticipationID,
		"task_id": a.TaskID, "user_id": a.UserID} {
		if v != nil {
			details[k] = *v
		}
	}
	if a.ArchiveSandbox {
		details["archive_sandbox"] = true
	}
	err = c.Write(func(tx *Tx) error {
		cid, err := tx.Q.AdminScopeContest(tx.Ctx, q.AdminScopeContestParams{SubmissionID: a.SubmissionID,
			ParticipationID: a.ParticipationID, DatasetID: a.DatasetID, TaskID: a.TaskID, ContestID: a.ContestID})
		if err != nil {
			return err
		}
		tx.Publish(taskScoreLiveEvents(inv.TaskScores)...)
		return tx.Audit(AuditEntry{Action: action, TargetType: target, TargetID: targetID, ContestID: cid, Details: details})
	})
	if err != nil {
		// The results are invalidated already: log, and go on to notify.
		s.log.Error("cannot record the audit entry of an invalidation", "err", err)
	}
	s.log.Info("results invalidated", "admin", c.Admin.Username, "level", string(level),
		"submissions", len(inv.Submissions), "results", len(inv.Results))
	if events := inv.Events(a.ArchiveSandbox); len(events) > 0 {
		if err := judging.Notify(c.Context(), s.kv, events...); err != nil {
			// The sweep of the dispatcher finds the work on the datasets
			// it judges; rescoring other datasets needs the notification.
			s.log.Warn("cannot notify the dispatcher of an invalidation; it finds the work at its next sweep", "err", err)
		}
	}
	return nil, nil
}
