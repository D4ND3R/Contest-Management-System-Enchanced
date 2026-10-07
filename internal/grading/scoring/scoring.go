// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/grading/scoring.py of CMS:
// Copyright © 2010-2015 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2013 Bernard Blackham <bernard@largestprime.net>
// Copyright © 2013-2014 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2014 Fabian Gundlach <320pointsguy@gmail.com>
// Copyright © 2016 Myungwoo Chun <mc.tamaki@gmail.com>
// Copyright © 2016 Amir Keivan Mohtashami <akmohtashami97@gmail.com>

// Package scoring computes the score of a participant on a task from the
// scores of their submissions, with the score modes of CMS (max,
// max_subtask, max_tokened_last), and compares the scores of a submission
// on two datasets. It is pure: callers load the submissions (in one query,
// no N+1) and pass them in.
package scoring

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/scoretype"
)

// Score modes (tasks.score_mode).
const (
	// ModeMax: the best score among all submissions (IOI 2013-2016).
	ModeMax = "max"
	// ModeMaxSubtask: the sum over the subtasks of the best score of each
	// subtask among all submissions (IOI since 2017).
	ModeMaxSubtask = "max_subtask"
	// ModeMaxTokenedLast: the best score among the tokened submissions and
	// the last one (IOI 2010-2012).
	ModeMaxTokenedLast = "max_tokened_last"
)

// ValidMode reports whether mode is a score mode.
func ValidMode(mode string) bool {
	return mode == ModeMax || mode == ModeMaxSubtask || mode == ModeMaxTokenedLast
}

// View is which score of each submission counts.
type View int

const (
	// Full counts the scores (score, score_details): what admins and the
	// rankings see.
	Full View = iota
	// Public counts the public scores (public_score,
	// public_score_details): what a contestant always sees.
	Public
	// OnlyTokened counts the scores of the tokened submissions only, as if
	// the others had not been scored yet: what a contestant knows of their
	// full score (upstream only_tokened).
	OnlyTokened
)

// Submission is an official submission of a participant on a task, with
// its result on the active dataset of the task.
type Submission struct {
	// Timestamp orders the submissions; for equal timestamps the order of
	// the slice counts.
	Timestamp time.Time
	// Unofficial submissions (sent during analysis mode) do not count.
	Unofficial bool
	// Tokened reports whether a token was played on the submission.
	Tokened bool
	// Scored reports whether the submission has a scored result on the
	// dataset (upstream SubmissionResult.scored()); the scores below are
	// meaningful only then. Submissions not scored yet count as 0 and make
	// the task score partial.
	Scored             bool
	Score              float64
	ScoreDetails       json.RawMessage
	PublicScore        float64
	PublicScoreDetails json.RawMessage
}

// TaskScore returns the score of a participant on a task given their
// submissions on it, the score mode and the score precision of the task
// (upstream task_score). partial is true if some submission is not scored
// yet.
func TaskScore(subs []Submission, mode string, precision int, view View) (score float64, partial bool, err error) {
	if !ValidMode(mode) {
		return 0, false, fmt.Errorf("unknown score mode %q", mode)
	}
	if view != Full && view != Public && view != OnlyTokened {
		return 0, false, fmt.Errorf("unknown view %d", view)
	}
	official := make([]*Submission, 0, len(subs))
	for i := range subs {
		if !subs[i].Unofficial {
			official = append(official, &subs[i])
		}
	}
	if len(official) == 0 {
		return 0, false, nil
	}
	slices.SortStableFunc(official, func(a, b *Submission) int { return a.Timestamp.Compare(b.Timestamp) })
	for _, s := range official {
		if !s.Scored {
			partial = true
			break
		}
	}
	switch mode {
	case ModeMax:
		score = taskScoreMax(official, view)
	case ModeMaxSubtask:
		score = taskScoreMaxSubtask(official, view)
	case ModeMaxTokenedLast:
		score = taskScoreMaxTokenedLast(official, view)
	}
	// Subtask scores are rounded, but their float sum may not be.
	return scoretype.Round(score, precision), partial, nil
}

// pick returns the score and details of s that count for the view; ok is
// false if s counts as not scored.
func pick(s *Submission, view View) (score float64, details json.RawMessage, ok bool) {
	switch {
	case !s.Scored:
		return 0, nil, false
	case view == Public:
		return s.PublicScore, s.PublicScoreDetails, true
	case view == OnlyTokened && !s.Tokened:
		// The score mode must ignore it: send what a submission not
		// scored yet would send.
		return 0, nil, false
	}
	return s.Score, s.ScoreDetails, true
}

// pyMax is Python's max(a, b): a unless b is greater (so max(0.0, -0.0) is
// 0.0, unlike Go's max).
func pyMax(a, b float64) float64 {
	if b > a {
		return b
	}
	return a
}

// taskScoreMax: the maximum score among all submissions (not yet scored
// ones count as 0).
func taskScoreMax(subs []*Submission, view View) float64 {
	best := 0.0
	for _, s := range subs {
		if score, _, ok := pick(s, view); ok {
			best = pyMax(best, score)
		}
	}
	return best
}

// taskScoreMaxTokenedLast: the maximum score among the tokened submissions
// and the last submission (not yet scored ones count as 0).
func taskScoreMaxTokenedLast(subs []*Submission, view View) float64 {
	last, _, _ := pick(subs[len(subs)-1], view)
	bestTokened := 0.0
	for _, s := range subs {
		if score, _, ok := pick(s, view); ok && s.Tokened {
			bestTokened = pyMax(bestTokened, score)
		}
	}
	return pyMax(last, bestTokened)
}

// taskScoreMaxSubtask: the sum over the subtasks of the maximum score of
// the subtask among all submissions (not yet scored ones count as 0). The
// subtasks come from the details of group score types; details of another
// shape count as a single subtask.
func taskScoreMaxSubtask(subs []*Submission, view View) float64 {
	// The best score of each subtask, in the order the subtasks first
	// appear (the order of Python's dict, which is the order of the sum).
	var best subtaskScores
	for _, s := range subs {
		score, details, ok := pick(s, view)
		if !ok {
			continue
		}
		if score == 0 && isEmptyList(details) {
			// The submission did not compile: ignore it.
			continue
		}
		st, ok := parseSubtaskScores(details)
		if !ok || len(st.keys) == 0 {
			// Not a group score type: a single subtask.
			st = subtaskScores{keys: []subtaskKey{{kind: kindNumber, num: 1}}, scores: []float64{score}}
		}
		for i, k := range st.keys {
			best.max(k, st.scores[i])
		}
	}
	return pySum(best.scores)
}

// pySum adds like Python's sum() of floats since 3.12 (Neumaier's
// compensated summation), the Python of the default CMS image: the result
// does not depend on the order of the subtasks as much as a plain sum.
func pySum(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	f, c := 0.0, 0.0
	f += xs[0] // sum starts from the int 0
	for _, x := range xs[1:] {
		t := f + x
		if math.Abs(f) >= math.Abs(x) {
			c += (f - t) + x
		} else {
			c += (x - t) + f
		}
		f = t
	}
	// Keep the sign of a negative zero result, and do not turn an
	// overflowed sum into NaN.
	if c != 0 && !math.IsInf(c, 0) && !math.IsNaN(c) {
		f += c
	}
	return f
}

// subtaskKey is the idx of a subtask as a Python dict key: numbers (and
// booleans) are equal by value, so 1, 1.0 and true are the same key.
type subtaskKey struct {
	kind byte
	num  float64
	str  string
}

const (
	kindNumber byte = iota + 1
	kindString
	kindNull
)

// subtaskScores is an insertion-ordered map from subtask to score.
type subtaskScores struct {
	keys   []subtaskKey
	scores []float64
}

func (m *subtaskScores) index(k subtaskKey) int {
	for i := range m.keys {
		if m.keys[i] == k {
			return i
		}
	}
	return -1
}

// set stores v for k: a repeated key keeps its position and takes the new
// value, as in a Python dict.
func (m *subtaskScores) set(k subtaskKey, v float64) {
	if i := m.index(k); i >= 0 {
		m.scores[i] = v
		return
	}
	m.keys = append(m.keys, k)
	m.scores = append(m.scores, v)
}

// max stores the maximum of v and the current value of k (0 if none).
func (m *subtaskScores) max(k subtaskKey, v float64) {
	if i := m.index(k); i >= 0 {
		m.scores[i] = pyMax(m.scores[i], v)
		return
	}
	m.keys = append(m.keys, k)
	m.scores = append(m.scores, pyMax(0, v))
}

func isEmptyList(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) < 2 || raw[0] != '[' || raw[len(raw)-1] != ']' {
		return false
	}
	return len(bytes.TrimSpace(raw[1:len(raw)-1])) == 0
}

func parseKey(raw json.RawMessage) (subtaskKey, bool) {
	raw = bytes.TrimSpace(raw)
	switch {
	case len(raw) == 0:
		return subtaskKey{}, false
	case raw[0] == '"':
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return subtaskKey{}, false
		}
		return subtaskKey{kind: kindString, str: s}, true
	case string(raw) == "null":
		return subtaskKey{kind: kindNull}, true
	case string(raw) == "true":
		return subtaskKey{kind: kindNumber, num: 1}, true
	case string(raw) == "false":
		return subtaskKey{kind: kindNumber, num: 0}, true
	}
	// Lists and objects are not hashable in Python.
	f, err := strconv.ParseFloat(string(raw), 64)
	if err != nil {
		return subtaskKey{}, false
	}
	return subtaskKey{kind: kindNumber, num: f}, true
}

// ResultScore is the score of a submission result on a dataset. Nil fields
// are NULL columns (not scored yet); a nil *ResultScore is a missing
// result.
type ResultScore struct {
	Score               *float64
	PublicScore         *float64
	RankingScoreDetails []string
}

// ScoreChange is how the score of a submission changes between two
// datasets (upstream SubmissionScoreDelta). The old and new values of a
// field that does not change are left empty.
type ScoreChange struct {
	ScoreChanged           bool
	OldScore, NewScore     *float64
	PublicScoreChanged     bool
	OldPublicScore         *float64
	NewPublicScore         *float64
	RankingChanged         bool
	OldRankingScoreDetails []string
	NewRankingScoreDetails []string
}

// CompareResults compares the results of a submission on two datasets of a
// task, typically the active one and another (upstream
// compute_changes_for_dataset, for one submission); changed is false if
// they score the same.
func CompareResults(old, cur *ResultScore) (c ScoreChange, changed bool) {
	var o, n ResultScore
	if old != nil {
		o = *old
	}
	if cur != nil {
		n = *cur
	}
	if !sameFloat(o.Score, n.Score) {
		c.ScoreChanged, c.OldScore, c.NewScore = true, o.Score, n.Score
	}
	if !sameFloat(o.PublicScore, n.PublicScore) {
		c.PublicScoreChanged, c.OldPublicScore, c.NewPublicScore = true, o.PublicScore, n.PublicScore
	}
	// NULL and an empty list differ, as None and [] in Python.
	if (o.RankingScoreDetails == nil) != (n.RankingScoreDetails == nil) ||
		!slices.Equal(o.RankingScoreDetails, n.RankingScoreDetails) {
		c.RankingChanged, c.OldRankingScoreDetails, c.NewRankingScoreDetails = true, o.RankingScoreDetails, n.RankingScoreDetails
	}
	return c, c.ScoreChanged || c.PublicScoreChanged || c.RankingChanged
}

func sameFloat(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
