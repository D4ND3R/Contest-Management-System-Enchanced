// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/grading/scoretypes/abc.py and __init__.py of CMS:
// Copyright © 2010-2012 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2013 Bernard Blackham <bernard@largestprime.net>
// Copyright © 2013-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2015 wafrelka <wafrelka@gmail.com>

// Package scoretype computes the score of a submission from the outcomes
// of its testcases: the score types of CMS (Sum, GroupMin, GroupMul,
// GroupThreshold) with the same parameters, rounding and score details.
//
// The details are stored in the score_details and public_score_details
// columns and rendered later by the templates ported from CMS, so their
// JSON has the keys and nesting of CMS. The package is pure: no database,
// no sandbox; a ScoreType is immutable and safe for concurrent use.
package scoretype

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
)

// Names of the score types (datasets.score_type).
const (
	NameSum            = "Sum"
	NameGroupMin       = "GroupMin"
	NameGroupMul       = "GroupMul"
	NameGroupThreshold = "GroupThreshold"
)

// Names returns the names of the score types in the order CMS lists them.
func Names() []string {
	return []string{NameSum, NameGroupMin, NameGroupMul, NameGroupThreshold}
}

// Valid reports whether name is a score type.
func Valid(name string) bool {
	switch name {
	case NameSum, NameGroupMin, NameGroupMul, NameGroupThreshold:
		return true
	}
	return false
}

// FeedbackLevel is how much of the score details a contestant sees
// (tasks.feedback_level).
type FeedbackLevel string

// The feedback levels of CMS.
const (
	// FeedbackFull shows every testcase with its time and memory.
	FeedbackFull FeedbackLevel = "full"
	// FeedbackRestricted shows, in a subtask that is not fully solved, the
	// public testcases up to the first one with the lowest outcome.
	FeedbackRestricted FeedbackLevel = "restricted"
	// FeedbackOIRestricted shows only that testcase.
	FeedbackOIRestricted FeedbackLevel = "oi_restricted"
)

// Valid reports whether l is a feedback level.
func (l FeedbackLevel) Valid() bool {
	return l == FeedbackFull || l == FeedbackRestricted || l == FeedbackOIRestricted
}

// Public outcomes of a testcase: the msgids of the "cms" catalogs that the
// templates translate.
const (
	OutcomeCorrect          = "Correct"
	OutcomeNotCorrect       = "Not correct"
	OutcomePartiallyCorrect = "Partially correct"
)

// timeoutText is the text of an evaluation that exceeded the CPU time limit
// (EVALUATION_MESSAGES "timeout" in cms/grading/steps/evaluation.py).
const timeoutText = "Execution timed out"

// Evaluation is the result of a submission on one testcase.
type Evaluation struct {
	// Codename of the testcase.
	Codename string
	// Outcome as a number (upstream float(evaluation.outcome)).
	Outcome float64
	// Text is the message for the contestant: a msgid and its arguments.
	Text []string
	// ExecutionTime in seconds and ExecutionMemory in bytes; nil if
	// unknown.
	ExecutionTime   *float64
	ExecutionMemory *int64
}

// SubmissionResult is what ComputeScore needs of a submission result.
type SubmissionResult struct {
	// Evaluated reports whether the result has an evaluation outcome
	// (upstream SubmissionResult.evaluated()). A submission that did not
	// compile is not evaluated and scores zero.
	Evaluated bool
	// Evaluations of the submission on the dataset, in any order.
	Evaluations []Evaluation
	// TimeLimit of the dataset in seconds (nil: none), shown next to the
	// testcases that exceeded it.
	TimeLimit *float64
}

// Result is the score of a submission (upstream: the tuple returned by
// compute_score).
type Result struct {
	// Score and Details are what the contestant sees after playing a
	// token or once the contest is over (score, score_details).
	Score   float64
	Details Details
	// PublicScore and PublicDetails only use the public testcases
	// (public_score, public_score_details).
	PublicScore   float64
	PublicDetails Details
	// RankingDetails are the subtask scores sent to the scoreboard
	// (ranking_score_details), formatted with FormatG.
	RankingDetails []string
}

// ScoreType computes the scores of the submissions on a dataset.
type ScoreType interface {
	// Name of the score type.
	Name() string
	// MaxScore is the maximum score of a submission, and MaxPublicScore
	// the maximum score counting only the public testcases.
	MaxScore() float64
	MaxPublicScore() float64
	// RankingHeaders are the headers of the columns of RankingDetails in
	// the scoreboard (one per subtask, "Subtask 0 (40)"...).
	RankingHeaders() []string
	// Grouped reports whether the details are a list of subtasks (group
	// score types) rather than a list of testcases (Sum); the two are
	// rendered by different templates.
	Grouped() bool
	// ComputeScore scores a submission result (upstream compute_score).
	ComputeScore(sr *SubmissionResult) (*Result, error)
	// JSONDetails decodes stored score details (score_details or
	// public_score_details) and filters them for the feedback level
	// (upstream get_json_details). NULL details give nil.
	JSONDetails(details json.RawMessage, level FeedbackLevel) (*Details, error)
}

// New returns the score type name with its parameters (the JSON of
// datasets.score_type_parameters), the testcases of the dataset (codename
// -> public) and the score_precision of the task. Invalid parameters are
// an error, as in CMS ("Unable to instantiate score type").
func New(name string, parameters json.RawMessage, publicTestcases map[string]bool, scorePrecision int) (ScoreType, error) {
	if scorePrecision < 0 {
		return nil, fmt.Errorf("score type %s: negative score precision %d", name, scorePrecision)
	}
	// Lexicographical order by codename, as CMS.
	codenames := make([]string, 0, len(publicTestcases))
	for c := range publicTestcases {
		codenames = append(codenames, c)
	}
	sort.Strings(codenames)

	var (
		st  ScoreType
		err error
	)
	switch name {
	case NameSum:
		st, err = newSum(parameters, publicTestcases, codenames)
	case NameGroupMin, NameGroupMul, NameGroupThreshold:
		st, err = newGroup(name, parameters, publicTestcases, codenames, scorePrecision)
	default:
		return nil, fmt.Errorf("score type %q not found", name)
	}
	if err != nil {
		return nil, fmt.Errorf("unable to instantiate score type %s (probably due to invalid values for the score type parameters): %w", name, err)
	}
	return st, nil
}

// evaluationsByCodename indexes the evaluations; the last one wins, as in
// CMS.
func evaluationsByCodename(evs []Evaluation) (map[string]*Evaluation, error) {
	m := make(map[string]*Evaluation, len(evs))
	for i := range evs {
		e := &evs[i]
		if math.IsNaN(e.Outcome) || math.IsInf(e.Outcome, 0) {
			return nil, fmt.Errorf("testcase %q: outcome %v is not a finite number", e.Codename, e.Outcome)
		}
		m[e.Codename] = e
	}
	return m, nil
}

// text returns the text of an evaluation, never nil (it is a JSON list).
func text(t []string) []string {
	if t == nil {
		return []string{}
	}
	return t
}

// finite checks the totals of a result before they are stored as JSON,
// which has no NaN or infinity.
func finite(r *Result) error {
	for _, v := range [...]float64{r.Score, r.PublicScore} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("the score %v is not a finite number", v)
		}
	}
	return nil
}
