// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from ScoringExecutor.execute in cms/service/ScoringService.py of
// CMS:
// Copyright © 2010-2014 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2013-2015 Luca Wehrstedt <luca.wehrstedt@gmail.com>

package judging

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/scoretype"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// Score scores the given results in one transaction (upstream
// ScoringService): those whose compilation failed or whose evaluation is
// complete. A result that has every evaluation but no evaluation outcome
// yet gets it first (upstream finalization in
// submission_enqueue_operations). Results not ready, already scored or
// gone are left alone. The task score cache is updated and the changes
// returned, as in Apply.
func (j *Judge) Score(ctx context.Context, keys []ResultKey) (*Changes, error) {
	if len(keys) == 0 {
		return &Changes{}, nil
	}
	var ch *Changes
	err := j.inTx(ctx, func(_ pgx.Tx, qq *q.Queries) error {
		ch = &Changes{}
		a := &applier{j: j, qq: qq, ch: ch, now: j.now()}
		_, list, err := a.lockSubmissions(ctx, append([]ResultKey(nil), keys...))
		if err != nil {
			return err
		}
		return a.finishSubmissions(ctx, list)
	})
	if err != nil {
		return nil, err
	}
	j.logChanges(ch)
	return ch, nil
}

// score computes and stores the scores of locked results that are ready
// (upstream ScoringExecutor.execute), in a constant number of queries:
// the testcases of their datasets, their evaluations, and one batch of
// updates.
func (a *applier) score(ctx context.Context, list []*subState) error {
	if len(list) == 0 {
		return nil
	}
	var datasets []int32
	var evaluated q.JudgingEvaluationsToScoreParams
	for _, st := range list {
		datasets = append(datasets, st.DatasetID)
		if st.EvaluationOutcome != nil {
			evaluated.SubmissionIds = append(evaluated.SubmissionIds, st.SubmissionID)
			evaluated.DatasetIds = append(evaluated.DatasetIds, st.DatasetID)
		}
	}
	tcRows, err := a.qq.JudgingTestcaseVisibility(ctx, uniq(datasets))
	if err != nil {
		return err
	}
	public := map[int32]map[string]bool{}
	for _, t := range tcRows {
		if public[t.DatasetID] == nil {
			public[t.DatasetID] = map[string]bool{}
		}
		public[t.DatasetID][t.Codename] = t.Public
	}
	evals := map[ResultKey][]q.JudgingEvaluationsToScoreRow{}
	if len(evaluated.SubmissionIds) > 0 {
		rows, err := a.qq.JudgingEvaluationsToScore(ctx, evaluated)
		if err != nil {
			return err
		}
		for _, e := range rows {
			k := ResultKey{e.SubmissionID, e.DatasetID}
			evals[k] = append(evals[k], e)
		}
	}

	// One score type per dataset (and precision, which belongs to the task).
	type stKey struct {
		dataset   int32
		precision int32
	}
	types := map[stKey]scoretype.ScoreType{}
	typeErrs := map[stKey]error{}
	var params []q.JudgingSetScoreParams
	var scored []*subState
	for _, st := range list {
		k := st.key()
		tk := stKey{st.DatasetID, st.ScorePrecision}
		typ, ok := types[tk]
		if !ok && typeErrs[tk] == nil {
			pub := public[st.DatasetID]
			if pub == nil {
				pub = map[string]bool{}
			}
			typ, err = scoretype.New(st.ScoreType, st.ScoreTypeParameters, pub, int(st.ScorePrecision))
			if err != nil {
				typeErrs[tk] = err
			} else {
				types[tk] = typ
			}
		}
		if err := typeErrs[tk]; err != nil {
			a.ch.ScoreErrors = append(a.ch.ScoreErrors, ScoreError{k, err})
			continue
		}
		sr := &scoretype.SubmissionResult{Evaluated: st.EvaluationOutcome != nil, TimeLimit: st.TimeLimit}
		var bad error
		for _, e := range evals[k] {
			o, err := parseOutcome(e.Outcome)
			if err != nil {
				bad = fmt.Errorf("testcase %s: %w", e.Codename, err)
				break
			}
			sr.Evaluations = append(sr.Evaluations, scoretype.Evaluation{
				Codename: e.Codename, Outcome: o, Text: e.Text,
				ExecutionTime: e.ExecutionTime, ExecutionMemory: e.ExecutionMemory,
			})
		}
		if bad != nil {
			a.ch.ScoreErrors = append(a.ch.ScoreErrors, ScoreError{k, bad})
			continue
		}
		res, err := typ.ComputeScore(sr)
		if err != nil {
			a.ch.ScoreErrors = append(a.ch.ScoreErrors, ScoreError{k, err})
			continue
		}
		details, err := json.Marshal(res.Details)
		if err != nil {
			return err
		}
		publicDetails, err := json.Marshal(res.PublicDetails)
		if err != nil {
			return err
		}
		ranking := res.RankingDetails
		if ranking == nil {
			ranking = []string{}
		}
		params = append(params, q.JudgingSetScoreParams{
			Score: res.Score, ScoreDetails: details, PublicScore: res.PublicScore,
			PublicScoreDetails: publicDetails, RankingScoreDetails: ranking, ScoredAt: a.now,
			SubmissionID: st.SubmissionID, DatasetID: st.DatasetID, Generation: st.Generation,
		})
		st.score, st.publicScore, st.ranking = res.Score, res.PublicScore, ranking
		scored = append(scored, st)
	}
	if len(params) == 0 {
		return nil
	}
	var be batchErr
	a.qq.JudgingSetScore(ctx, params).QueryRow(func(i int, _ int32, err error) {
		if be.ok(err) {
			scored[i].Scored, scored[i].scoreChanged = true, true
		}
	})
	return be.err
}

// parseOutcome reads an evaluation outcome as upstream float(): spaces
// around are allowed; a missing outcome cannot be scored.
func parseOutcome(s *string) (float64, error) {
	if s == nil {
		return 0, fmt.Errorf("the evaluation has no outcome")
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(*s), 64)
	if err != nil {
		return 0, fmt.Errorf("the outcome %q is not a number", *s)
	}
	return v, nil
}
