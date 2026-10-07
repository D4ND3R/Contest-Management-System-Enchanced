// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/grading/scoretypes/abc.py (ScoreTypeGroup), GroupMin.py,
// GroupMul.py and GroupThreshold.py of CMS:
// Copyright © 2010-2012 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2013-2016 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2015 wafrelka <wafrelka@gmail.com>

package scoretype

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
)

// subtask is a subtask of a group score type, resolved against the
// testcases of the dataset.
type subtask struct {
	maxScore   float64
	threshold  float64
	alwaysShow bool
	// target are the codenames of its testcases, and allPublic whether
	// they are all public (then the subtask counts for the public score).
	target    []string
	allPublic bool
}

// group implements GroupMin, GroupMul and GroupThreshold (upstream
// ScoreTypeGroup and its subclasses): the testcases are split in subtasks
// and each subtask scores a fraction of its max score.
type group struct {
	name           string
	subtasks       []subtask
	public         map[string]bool
	precision      int
	maxScore       float64
	maxPublicScore float64
	headers        []string
	maxTarget      int
}

func newGroup(name string, raw json.RawMessage, public map[string]bool, codenames []string, precision int) (*group, error) {
	params, err := parseGroupParams(raw, name == NameGroupThreshold)
	if err != nil {
		return nil, err
	}
	tgts, err := targets(params, public, codenames)
	if err != nil {
		return nil, err
	}
	g := &group{
		name:      name,
		subtasks:  make([]subtask, len(params)),
		public:    public,
		precision: precision,
		headers:   make([]string, len(params)),
	}
	// Upstream max_scores.
	for i, p := range params {
		if Round(p.maxScore, precision) != p.maxScore {
			return nil, fmt.Errorf("subtask %d: the max score for a subtask has more precision than the task allows", i)
		}
		st := subtask{maxScore: p.maxScore, threshold: p.threshold, alwaysShow: p.alwaysShow, target: tgts[i], allPublic: true}
		for _, c := range st.target {
			st.allPublic = st.allPublic && public[c]
		}
		g.maxScore += st.maxScore
		if st.allPublic {
			g.maxPublicScore += st.maxScore
		}
		g.headers[i] = "Subtask " + strconv.Itoa(i) + " (" + FormatG(st.maxScore) + ")"
		g.maxTarget = max(g.maxTarget, len(st.target))
		g.subtasks[i] = st
	}
	return g, nil
}

func (g *group) Name() string             { return g.name }
func (g *group) MaxScore() float64        { return g.maxScore }
func (g *group) MaxPublicScore() float64  { return g.maxPublicScore }
func (g *group) RankingHeaders() []string { return slices.Clone(g.headers) }
func (g *group) Grouped() bool            { return true }

func (g *group) JSONDetails(details json.RawMessage, level FeedbackLevel) (*Details, error) {
	return jsonDetails(details, level, true)
}

// publicOutcome is the outcome shown for a testcase (upstream
// get_public_outcome).
func (g *group) publicOutcome(outcome float64, st *subtask) string {
	if g.name == NameGroupThreshold {
		if 0 < outcome && outcome <= st.threshold {
			return OutcomeCorrect
		}
		return OutcomeNotCorrect
	}
	switch {
	case outcome <= 0:
		return OutcomeNotCorrect
	case outcome >= 1:
		return OutcomeCorrect
	}
	return OutcomePartiallyCorrect
}

// reduce is the fraction of the max score of a subtask given the outcomes
// of its testcases (upstream reduce): the minimum (GroupMin), the product
// from the left (GroupMul), or 1 if all outcomes are in (0, threshold]
// (GroupThreshold).
func (g *group) reduce(outcomes []float64, st *subtask) float64 {
	switch g.name {
	case NameGroupMin:
		// Python's min keeps the first of equal values (0.0 before -0.0),
		// unlike Go's.
		m := outcomes[0]
		for _, o := range outcomes[1:] {
			if o < m {
				m = o
			}
		}
		return m
	case NameGroupMul:
		p := outcomes[0]
		for _, o := range outcomes[1:] {
			p *= o
		}
		return p
	}
	for _, o := range outcomes {
		if !(0 < o && o <= st.threshold) {
			return 0
		}
	}
	return 1
}

// ComputeScore implements ScoreType (upstream ScoreTypeGroup.compute_score).
func (g *group) ComputeScore(sr *SubmissionResult) (*Result, error) {
	// Not evaluated means it did not even compile.
	if !sr.Evaluated {
		ranking := make([]string, len(g.subtasks))
		for i := range ranking {
			ranking[i] = "0"
		}
		return &Result{
			Details:        Details{Grouped: true, Subtasks: []SubtaskDetails{}},
			PublicDetails:  Details{Grouped: true, Subtasks: []SubtaskDetails{}},
			RankingDetails: ranking,
		}, nil
	}
	evals, err := evaluationsByCodename(sr.Evaluations)
	if err != nil {
		return nil, err
	}

	r := &Result{
		Details:        Details{Grouped: true, Subtasks: make([]SubtaskDetails, len(g.subtasks))},
		PublicDetails:  Details{Grouped: true, Subtasks: make([]SubtaskDetails, len(g.subtasks))},
		RankingDetails: make([]string, len(g.subtasks)),
	}
	outcomes := make([]float64, g.maxTarget)
	for i := range g.subtasks {
		st := &g.subtasks[i]
		tcs := make([]TestcaseDetails, len(st.target))
		// In "restricted" feedback the public testcases are shown up to the
		// first one with the lowest outcome, in "oi_restricted" only that
		// one.
		firstLowest := -1
		for j, c := range st.target {
			ev, ok := evals[c]
			if !ok {
				return nil, fmt.Errorf("no evaluation for testcase %q", c)
			}
			o := ev.Outcome
			outcomes[j] = o
			txt := text(ev.Text)
			tcs[j] = TestcaseDetails{
				Idx:                        c,
				Outcome:                    g.publicOutcome(o, st),
				Text:                       txt,
				Time:                       ev.ExecutionTime,
				TimeLimit:                  sr.TimeLimit,
				TimeLimitWasExceeded:       len(txt) == 1 && txt[0] == timeoutText,
				Memory:                     ev.ExecutionMemory,
				ShowInRestrictedFeedback:   g.public[c],
				ShowInOIRestrictedFeedback: g.public[c],
				has:                        groupTestcaseKeys,
			}
			if g.public[c] && (firstLowest < 0 || o < outcomes[firstLowest]) {
				firstLowest = j
			}
		}
		fraction := g.reduce(outcomes[:len(st.target)], st)
		stScore := fraction * st.maxScore
		if math.IsNaN(stScore) || math.IsInf(stScore, 0) {
			return nil, fmt.Errorf("subtask %d: the score %v is not a finite number", i, stScore)
		}
		rounded := Round(stScore, g.precision)

		if firstLowest >= 0 && fraction < 1 && !st.alwaysShow {
			// Codenames compare as strings, as in CMS.
			lowest := st.target[firstLowest]
			for j := range tcs {
				if !g.public[tcs[j].Idx] {
					continue
				}
				tcs[j].ShowInRestrictedFeedback = tcs[j].Idx <= lowest
				tcs[j].ShowInOIRestrictedFeedback = tcs[j].Idx == lowest
			}
		}

		r.Score += rounded
		// The fraction lets an "example" subtask with a max score of zero be
		// shown as correct or not; the rounded score is for display.
		r.Details.Subtasks[i] = SubtaskDetails{
			Idx:           i,
			ScoreFraction: fraction,
			Score:         rounded,
			MaxScore:      st.maxScore,
			Testcases:     tcs,
			has:           subtaskKeys,
		}
		if st.allPublic {
			r.PublicScore += rounded
			r.PublicDetails.Subtasks[i] = r.Details.Subtasks[i]
		} else {
			// CMS shares the dicts of the public testcases between the two
			// details, so they carry the final show_in_* flags.
			ptcs := make([]TestcaseDetails, len(tcs))
			for j := range tcs {
				if g.public[tcs[j].Idx] {
					ptcs[j] = tcs[j]
				} else {
					ptcs[j] = TestcaseDetails{Idx: tcs[j].Idx, has: kIdx}
				}
			}
			r.PublicDetails.Subtasks[i] = SubtaskDetails{Idx: i, Testcases: ptcs, has: hiddenSubtaskKeys}
		}
		r.RankingDetails[i] = FormatG(rounded)
	}
	// Subtask scores are rounded, but their float sum may not be: round
	// again (the public score is not, as in CMS).
	r.Score = Round(r.Score, g.precision)
	if err := finite(r); err != nil {
		return nil, err
	}
	return r, nil
}
