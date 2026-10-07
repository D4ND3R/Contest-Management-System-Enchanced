// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cmstestsuite/unit_tests/grading/scoring_test.py of CMS:
// Copyright © 2018 Stefano Maggiolo <s.maggiolo@gmail.com>

package scoring

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/scoretype"
)

// taskFixture is upstream TaskScoreMixin: the submissions of a participant
// on a task with score precision 2.
type taskFixture struct {
	t    *testing.T
	mode string
	subs []Submission
}

var epoch = time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)

func at(seconds int) time.Time { return epoch.Add(time.Duration(seconds) * time.Second) }

type result struct {
	tokened       bool
	details       string
	publicScore   float64
	publicDetails string
}

// add is upstream add_result; a nil score is a result not scored yet.
func (f *taskFixture) add(ts time.Time, score *float64, r result) {
	s := Submission{Timestamp: ts, Tokened: r.tokened, ScoreDetails: json.RawMessage("[]"), PublicScoreDetails: json.RawMessage("[]")}
	if score != nil {
		s.Scored, s.Score, s.PublicScore = true, *score, r.publicScore
		if r.details != "" {
			s.ScoreDetails = json.RawMessage(r.details)
		}
		if r.publicDetails != "" {
			s.PublicScoreDetails = json.RawMessage(r.publicDetails)
		}
	}
	f.subs = append(f.subs, s)
}

func (f *taskFixture) check(view View, want float64, wantPartial bool) {
	f.t.Helper()
	got, partial, err := TaskScore(f.subs, f.mode, 2, view)
	if err != nil {
		f.t.Fatal(err)
	}
	if got != want || partial != wantPartial {
		f.t.Errorf("TaskScore = (%v, %v), want (%v, %v)", got, partial, want, wantPartial)
	}
}

func sc(v float64) *float64 { return &v }

func newFixture(t *testing.T, mode string) *taskFixture { return &taskFixture{t: t, mode: mode} }

// subtask is an item of score details for a subtask, with a score
// precision of 4 as upstream.
func subtask(idx int, maxScore, fraction float64) string {
	return fmt.Sprintf(`{"idx": %d, "max_score": %v, "score_fraction": %v, "score": %v}`,
		idx, maxScore, fraction, scoretype.Round(maxScore*fraction, 4))
}

func details(subtasks ...string) string { return "[" + strings.Join(subtasks, ", ") + "]" }

func TestMaxTokenedLast(t *testing.T) {
	mode := ModeMaxTokenedLast
	t.Run("no submissions", func(t *testing.T) {
		newFixture(t, mode).check(Full, 0, false)
	})
	t.Run("all scored tokened", func(t *testing.T) {
		f := newFixture(t, mode)
		f.add(at(1), sc(44.4), result{tokened: true})
		f.add(at(2), sc(66.6), result{tokened: true})
		f.check(Full, 66.6, false)
	})
	t.Run("all scored tokened best first", func(t *testing.T) {
		f := newFixture(t, mode)
		f.add(at(1), sc(66.6), result{tokened: true})
		f.add(at(2), sc(44.4), result{tokened: true})
		f.check(Full, 66.6, false)
	})
	t.Run("best untokened return next best", func(t *testing.T) {
		f := newFixture(t, mode)
		f.add(at(1), sc(44.4), result{tokened: true})
		f.add(at(2), sc(66.6), result{})
		f.add(at(3), sc(11.1), result{})
		f.check(Full, 44.4, false)
	})
	t.Run("best untokened return last", func(t *testing.T) {
		f := newFixture(t, mode)
		f.add(at(1), sc(44.4), result{tokened: true})
		f.add(at(2), sc(66.6), result{})
		f.add(at(3), sc(55.5), result{})
		f.check(Full, 55.5, false)
	})
	t.Run("no tokened", func(t *testing.T) {
		f := newFixture(t, mode)
		f.add(at(1), sc(66.6), result{})
		f.add(at(2), sc(44.4), result{})
		f.check(Full, 44.4, false)
	})
	t.Run("best last unordered", func(t *testing.T) {
		f := newFixture(t, mode)
		f.add(at(2), sc(66.6), result{})
		f.add(at(1), sc(44.4), result{})
		f.check(Full, 66.6, false)
	})
	t.Run("partial", func(t *testing.T) {
		f := newFixture(t, mode)
		f.add(at(1), nil, result{tokened: true})
		f.add(at(2), sc(66.6), result{})
		f.add(at(3), sc(55.5), result{})
		f.check(Full, 55.5, true)
	})
	t.Run("partial last unscored", func(t *testing.T) {
		f := newFixture(t, mode)
		f.add(at(1), sc(66.6), result{tokened: true})
		f.add(at(2), nil, result{})
		f.check(Full, 66.6, true)
	})
	t.Run("not partial when irrelevant is unscored", func(t *testing.T) {
		// Upstream's name; the score is partial anyway.
		f := newFixture(t, mode)
		f.add(at(1), nil, result{})
		f.add(at(2), sc(66.6), result{})
		f.check(Full, 66.6, true)
	})
	t.Run("all unscored", func(t *testing.T) {
		f := newFixture(t, mode)
		f.add(at(1), nil, result{tokened: true})
		f.add(at(2), nil, result{})
		f.add(at(3), nil, result{})
		f.check(Full, 0, true)
	})
	t.Run("public", func(t *testing.T) {
		f := newFixture(t, mode)
		f.add(at(1), sc(44.4), result{tokened: true, publicScore: 4.4})
		f.add(at(2), sc(66.6), result{publicScore: 66.6})
		f.add(at(3), sc(11.1), result{publicScore: 11.1})
		f.check(Public, 11.1, false)
	})
	t.Run("only tokened", func(t *testing.T) {
		f := newFixture(t, mode)
		f.add(at(1), sc(11.1), result{tokened: true})
		f.add(at(2), sc(66.6), result{})
		f.add(at(3), sc(44.4), result{})
		f.check(OnlyTokened, 11.1, false)
	})
}

func TestMaxSubtask(t *testing.T) {
	mode := ModeMaxSubtask
	t.Run("no submissions", func(t *testing.T) {
		newFixture(t, mode).check(Full, 0, false)
	})
	t.Run("task not group", func(t *testing.T) {
		f := newFixture(t, mode)
		f.add(at(1), sc(66.6), result{})
		f.add(at(2), sc(44.4), result{})
		f.check(Full, 66.6, false)
	})
	t.Run("all submissions scored", func(t *testing.T) {
		f := newFixture(t, mode)
		f.add(at(1), sc(30*0.2+40*0.5+30*0.1), result{details: details(subtask(3, 30, 0.2), subtask(2, 40, 0.5), subtask(1, 30, 0.1))})
		f.add(at(2), sc(30*0.1+40*0.5+30*0.2), result{details: details(subtask(2, 40, 0.5), subtask(1, 30, 0.2), subtask(3, 30, 0.1))})
		f.check(Full, 30*0.2+40*0.5+30*0.2, false)
	})
	t.Run("compilation error total is zero", func(t *testing.T) {
		// Compilation errors have details=[].
		f := newFixture(t, mode)
		f.add(at(1), sc(0), result{details: "[]"})
		f.add(at(2), sc(0), result{details: details(subtask(3, 30, 0), subtask(2, 40, 0), subtask(1, 30, 0))})
		f.check(Full, 0, false)
	})
	t.Run("compilation error total is positive", func(t *testing.T) {
		f := newFixture(t, mode)
		f.add(at(1), sc(0), result{details: "[]"})
		f.add(at(2), sc(30*0.1), result{details: details(subtask(3, 30, 0.1), subtask(2, 40, 0), subtask(1, 30, 0))})
		f.check(Full, 30*0.1, false)
	})
	t.Run("partial", func(t *testing.T) {
		f := newFixture(t, mode)
		f.add(at(1), sc(30*0.2+40*0.5+30*0.1), result{details: details(subtask(3, 30, 0.2), subtask(2, 40, 0.5), subtask(1, 30, 0.1))})
		f.add(at(2), sc(30*0.1+40*0.5+30*0.2), result{details: details(subtask(3, 30, 0.1), subtask(2, 40, 0.5), subtask(1, 30, 0.2))})
		f.add(at(3), nil, result{})
		f.check(Full, 30*0.2+40*0.5+30*0.2, true)
	})
	t.Run("public", func(t *testing.T) {
		f := newFixture(t, mode)
		f.add(at(1), sc(100), result{
			details:       details(subtask(3, 30, 1), subtask(2, 40, 1), subtask(1, 30, 1)),
			publicScore:   30*0.2 + 40*0.5 + 30*0.1,
			publicDetails: details(subtask(3, 30, 0.2), subtask(2, 40, 0.5), subtask(1, 30, 0.1)),
		})
		f.add(at(2), sc(100), result{
			details:       details(subtask(2, 40, 1), subtask(1, 30, 1), subtask(3, 30, 1)),
			publicScore:   30*0.1 + 40*0.5 + 30*0.2,
			publicDetails: details(subtask(2, 40, 0.5), subtask(1, 30, 0.2), subtask(3, 30, 0.1)),
		})
		f.check(Public, 30*0.2+40*0.5+30*0.2, false)
	})
	t.Run("only tokened", func(t *testing.T) {
		f := newFixture(t, mode)
		f.add(at(1), sc(30*0.2+40*0.5+30*0.1), result{tokened: true, details: details(subtask(3, 30, 0.2), subtask(2, 40, 0.5), subtask(1, 30, 0.1))})
		f.add(at(2), sc(30*0.1+40*0.5+30*0.2), result{tokened: true, details: details(subtask(2, 40, 0.5), subtask(1, 30, 0.2), subtask(3, 30, 0.1))})
		f.add(at(3), sc(100), result{details: details(subtask(2, 40, 1), subtask(1, 30, 1), subtask(3, 30, 1))})
		f.check(OnlyTokened, 30*0.2+40*0.5+30*0.2, false)
	})
}

func TestMax(t *testing.T) {
	mode := ModeMax
	t.Run("no submissions", func(t *testing.T) {
		newFixture(t, mode).check(Full, 0, false)
	})
	t.Run("all submissions scored", func(t *testing.T) {
		f := newFixture(t, mode)
		f.add(at(1), sc(44.4), result{})
		f.add(at(2), sc(66.6), result{})
		f.check(Full, 66.6, false)
	})
	t.Run("all submissions scored best first", func(t *testing.T) {
		f := newFixture(t, mode)
		f.add(at(1), sc(66.6), result{})
		f.add(at(2), sc(44.4), result{})
		f.check(Full, 66.6, false)
	})
	t.Run("best untokened", func(t *testing.T) {
		f := newFixture(t, mode)
		f.add(at(1), sc(44.4), result{tokened: true})
		f.add(at(2), sc(66.6), result{})
		f.add(at(3), sc(11.1), result{})
		f.check(Full, 66.6, false)
	})
	t.Run("partial", func(t *testing.T) {
		f := newFixture(t, mode)
		f.add(at(1), nil, result{})
		f.add(at(2), sc(66.6), result{})
		f.add(at(3), sc(55.5), result{})
		f.check(Full, 66.6, true)
	})
	t.Run("all unscored", func(t *testing.T) {
		f := newFixture(t, mode)
		f.add(at(1), nil, result{})
		f.add(at(2), nil, result{})
		f.check(Full, 0, true)
	})
	t.Run("public", func(t *testing.T) {
		f := newFixture(t, mode)
		f.add(at(1), sc(44.4), result{publicScore: 44.4})
		f.add(at(2), sc(66.6), result{publicScore: 6.6})
		f.check(Public, 44.4, false)
	})
	t.Run("only tokened", func(t *testing.T) {
		f := newFixture(t, mode)
		f.add(at(1), sc(44.4), result{tokened: true})
		f.add(at(2), sc(66.6), result{})
		f.check(OnlyTokened, 44.4, false)
	})
	t.Run("rounded", func(t *testing.T) {
		f := newFixture(t, mode)
		f.add(at(1), sc(44.44444), result{})
		f.add(at(2), sc(44.44443), result{})
		f.check(Full, 44.44, false)
	})
}

func TestTaskScoreInvalid(t *testing.T) {
	if _, _, err := TaskScore(nil, "min", 2, Full); err == nil {
		t.Error("unknown score mode accepted")
	}
	if _, _, err := TaskScore(nil, ModeMax, 2, View(7)); err == nil {
		t.Error("unknown view accepted")
	}
	if !ValidMode(ModeMaxSubtask) || ValidMode("") {
		t.Error("ValidMode")
	}
}

func TestUnofficialSubmissions(t *testing.T) {
	f := newFixture(t, ModeMaxTokenedLast)
	f.add(at(1), sc(30), result{})
	f.add(at(2), sc(10), result{})
	f.add(at(3), nil, result{})
	f.subs[2].Unofficial = true // sent in analysis mode
	f.check(Full, 10, false)
	f.subs[1].Unofficial = true
	f.check(Full, 30, false)
	f.subs[0].Unofficial = true
	f.check(Full, 0, false)
}

func TestEqualTimestampsKeepOrder(t *testing.T) {
	// Python's sort is stable: the last of equal timestamps is the one
	// given last.
	f := newFixture(t, ModeMaxTokenedLast)
	f.add(at(1), sc(30), result{})
	f.add(at(1), sc(10), result{})
	f.check(Full, 10, false)
	if f.subs[0].Score != 30 {
		t.Error("TaskScore reordered the caller's slice")
	}
}

func TestMaxSubtaskShapes(t *testing.T) {
	for _, c := range []struct {
		name    string
		details []string
		scores  []float64
		want    float64
	}{
		// Sum details (no "score" in the testcases): a single subtask.
		{"sum", []string{`[{"idx": "0", "outcome": "Correct"}]`, `[{"idx": "0"}]`}, []float64{40, 30}, 40},
		// Public details of a subtask with private testcases have no
		// score: the whole public score counts as subtask 1, which is also
		// the idx of the second subtask of the other submission, so CMS
		// gives max(10, 7) + 0.
		{"public mixed", []string{
			`[{"idx": 0, "score": 10, "max_score": 10, "testcases": []}, {"idx": 1, "testcases": []}]`,
			`[{"idx": 0, "score": 0, "max_score": 10}, {"idx": 1, "score": 7}]`,
		}, []float64{10, 7}, 10},
		{"fallback collides with idx 1", []string{`[{"idx": 1, "score": 3}]`, `{"not": "a list"}`}, []float64{3, 5}, 5},
		// Python dict keys: 1, 1.0 and true are the same subtask.
		{"numeric keys", []string{`[{"idx": 1, "score": 3}, {"idx": "1", "score": 4}]`, `[{"idx": 1.0, "score": 5}, {"idx": true, "score": 6}]`}, []float64{7, 11}, 6 + 4},
		// A repeated idx keeps the last score.
		{"repeated idx", []string{`[{"idx": 0, "score": 9}, {"idx": 0, "score": 2}]`}, []float64{11}, 2},
		{"null idx", []string{`[{"idx": null, "score": 2}]`, `[{"idx": null, "score": 3}]`}, []float64{2, 3}, 3},
		{"unhashable idx", []string{`[{"idx": [0], "score": 2}]`}, []float64{8}, 8},
		{"score not a number", []string{`[{"idx": 0, "score": "2"}]`}, []float64{8}, 8},
		{"not objects", []string{`[1, 2]`, `[null]`}, []float64{8, 9}, 9},
		{"empty details with a score", []string{`[]`}, []float64{5}, 5},
		{"negative scores count as 0", []string{`[{"idx": 0, "score": -5}]`}, []float64{-5}, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t, ModeMaxSubtask)
			for i, d := range c.details {
				f.add(at(i), sc(c.scores[i]), result{details: d})
			}
			f.check(Full, c.want, false)
		})
	}
}

func TestMaxSubtaskSum(t *testing.T) {
	// The subtasks are added like Python's sum() (compensated): 0.6 in
	// either order, where a plain sum of 0.1 + 0.2 + 0.3 gives
	// 0.6000000000000001. Precision 17 keeps the difference.
	subs := []Submission{
		{Timestamp: at(1), Scored: true, Score: 0.6, ScoreDetails: json.RawMessage(`[{"idx": 0, "score": 0.1}, {"idx": 1, "score": 0.2}, {"idx": 2, "score": 0.3}]`)},
		{Timestamp: at(2), Scored: true, Score: 0.6, ScoreDetails: json.RawMessage(`[{"idx": 2, "score": 0.3}, {"idx": 1, "score": 0.2}, {"idx": 0, "score": 0.1}]`)},
	}
	got, _, err := TaskScore(subs, ModeMaxSubtask, 17, Full)
	if err != nil {
		t.Fatal(err)
	}
	if got != 0.6 {
		t.Errorf("TaskScore = %v, want 0.6", got)
	}
	// Python 3.13: sum([0.1]*10), sum([1e100, 1.0, -1e100, 1.0]),
	// sum([-0.0]), sum([1e308, 1e308, -1e308]), sum([2.0**53, 1.0, 1.0]).
	tenth := make([]float64, 10)
	for i := range tenth {
		tenth[i] = 0.1
	}
	for _, c := range []struct {
		xs   []float64
		want float64
	}{
		{tenth, 1},
		{[]float64{1e100, 1, -1e100, 1}, 2},
		{[]float64{math.Copysign(0, -1)}, 0},
		{[]float64{1e308, 1e308, -1e308}, math.Inf(1)},
		{[]float64{1 << 53, 1, 1}, 1<<53 + 2},
		{nil, 0},
	} {
		if got := pySum(c.xs); got != c.want || math.Signbit(got) {
			t.Errorf("pySum(%v) = %v, want %v", c.xs, got, c.want)
		}
	}
}

func TestCompareResults(t *testing.T) {
	r := func(score, public *float64, ranking []string) *ResultScore {
		return &ResultScore{Score: score, PublicScore: public, RankingScoreDetails: ranking}
	}
	same := r(sc(10), sc(5), []string{"5", "5"})
	if _, changed := CompareResults(same, r(sc(10), sc(5), []string{"5", "5"})); changed {
		t.Error("equal results changed")
	}
	if _, changed := CompareResults(nil, &ResultScore{}); changed {
		t.Error("a missing result and an unscored one differ")
	}
	c, changed := CompareResults(same, r(sc(20), sc(5), []string{"15", "5"}))
	if !changed || !c.ScoreChanged || *c.OldScore != 10 || *c.NewScore != 20 || c.PublicScoreChanged ||
		c.OldPublicScore != nil || !c.RankingChanged || c.NewRankingScoreDetails[0] != "15" {
		t.Errorf("change %+v", c)
	}
	c, changed = CompareResults(same, nil)
	if !changed || !c.ScoreChanged || c.NewScore != nil || !c.PublicScoreChanged || !c.RankingChanged {
		t.Errorf("removed result %+v", c)
	}
	// NULL and [] differ.
	if c, changed := CompareResults(r(sc(0), sc(0), nil), r(sc(0), sc(0), []string{})); !changed || !c.RankingChanged || c.ScoreChanged {
		t.Errorf("NULL vs [] %+v", c)
	}
}

func BenchmarkTaskScoreMaxSubtask(b *testing.B) {
	// 50 scored submissions of a task with 5 subtasks of 20 testcases.
	var tcs []string
	for i := range 20 {
		tcs = append(tcs, fmt.Sprintf(`{"idx": "%02d", "outcome": "Correct", "text": ["Output is correct"], "time": 0.25, `+
			`"time_limit": 1.0, "time_limit_was_exceeded": false, "memory": 1048576, "show_in_restricted_feedback": true, `+
			`"show_in_oi_restricted_feedback": false}`, i))
	}
	var sts []string
	for i := range 5 {
		sts = append(sts, fmt.Sprintf(`{"idx": %d, "score_fraction": 1.0, "score": 20.0, "max_score": 20, "testcases": [%s]}`,
			i, strings.Join(tcs, ", ")))
	}
	d := json.RawMessage("[" + strings.Join(sts, ", ") + "]")
	subs := make([]Submission, 50)
	for i := range subs {
		subs[i] = Submission{Timestamp: at(i), Scored: true, Score: 100, ScoreDetails: d}
	}
	b.SetBytes(int64(len(d) * len(subs)))
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := TaskScore(subs, ModeMaxSubtask, 2, Full); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTaskScoreMax(b *testing.B) {
	subs := make([]Submission, 50)
	for i := range subs {
		subs[i] = Submission{Timestamp: at(50 - i), Scored: true, Score: float64(i)}
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := TaskScore(subs, ModeMax, 2, Full); err != nil {
			b.Fatal(err)
		}
	}
}
