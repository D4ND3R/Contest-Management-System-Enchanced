// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/grading/scoretypes/Sum.py of CMS:
// Copyright © 2010-2012 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>

package scoretype

import (
	"encoding/json"
	"fmt"
)

// sum is the Sum score type: each testcase is worth the parameter times
// its outcome. Scores are not rounded (the task score is).
type sum struct {
	param          float64
	codenames      []string
	public         map[string]bool
	maxScore       float64
	maxPublicScore float64
}

func newSum(raw json.RawMessage, public map[string]bool, codenames []string) (*sum, error) {
	param, _, ok := number(raw)
	if !ok {
		return nil, fmt.Errorf("the parameter must be a number, got %s", raw)
	}
	s := &sum{param: param, codenames: codenames, public: public}
	// Upstream max_scores adds the parameter once per testcase.
	for _, c := range codenames {
		if public[c] {
			s.maxPublicScore += param
		}
		s.maxScore += param
	}
	return s, nil
}

func (s *sum) Name() string             { return NameSum }
func (s *sum) MaxScore() float64        { return s.maxScore }
func (s *sum) MaxPublicScore() float64  { return s.maxPublicScore }
func (s *sum) RankingHeaders() []string { return []string{} }
func (s *sum) Grouped() bool            { return false }

func (s *sum) JSONDetails(details json.RawMessage, level FeedbackLevel) (*Details, error) {
	return jsonDetails(details, level, false)
}

// publicOutcome is the outcome shown for a testcase given its score.
func (s *sum) publicOutcome(score float64) string {
	switch {
	case score <= 0:
		return OutcomeNotCorrect
	case score >= s.param:
		return OutcomeCorrect
	}
	return OutcomePartiallyCorrect
}

// ComputeScore implements ScoreType (upstream Sum.compute_score).
func (s *sum) ComputeScore(sr *SubmissionResult) (*Result, error) {
	// Not evaluated means it did not even compile.
	if !sr.Evaluated {
		return &Result{
			Details:        Details{Testcases: []TestcaseDetails{}},
			PublicDetails:  Details{Testcases: []TestcaseDetails{}},
			RankingDetails: []string{},
		}, nil
	}
	evals, err := evaluationsByCodename(sr.Evaluations)
	if err != nil {
		return nil, err
	}
	r := &Result{
		Details:        Details{Testcases: make([]TestcaseDetails, len(s.codenames))},
		PublicDetails:  Details{Testcases: make([]TestcaseDetails, len(s.codenames))},
		RankingDetails: []string{},
	}
	for i, c := range s.codenames {
		ev, ok := evals[c]
		if !ok {
			return nil, fmt.Errorf("no evaluation for testcase %q", c)
		}
		score := ev.Outcome * s.param
		r.Score += score
		r.Details.Testcases[i] = TestcaseDetails{
			Idx:     c,
			Outcome: s.publicOutcome(score),
			Text:    text(ev.Text),
			Time:    ev.ExecutionTime,
			Memory:  ev.ExecutionMemory,
			has:     sumTestcaseKeys,
		}
		if s.public[c] {
			r.PublicScore += score
			r.PublicDetails.Testcases[i] = r.Details.Testcases[i]
		} else {
			r.PublicDetails.Testcases[i] = TestcaseDetails{Idx: c, has: kIdx}
		}
	}
	if err := finite(r); err != nil {
		return nil, err
	}
	return r, nil
}
