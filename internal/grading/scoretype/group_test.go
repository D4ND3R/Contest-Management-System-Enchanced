// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cmstestsuite/unit_tests/grading/scoretypes/GroupMinTest.py,
// GroupMulTest.py and GroupThresholdTest.py of CMS:
// Copyright © 2018 Stefano Maggiolo <s.maggiolo@gmail.com>

package scoretype

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
)

const s1, s2, s3 = 10.5, 30.5, 59.0

var headers4 = []string{"Subtask 0 (0)", "Subtask 1 (10.5)", "Subtask 2 (30.5)", "Subtask 3 (59)"}

var idx4 = []any{0, 1, 2, 3}

// withThreshold adds a threshold to list parameters for GroupThreshold.
func groupParams(name, params string, thresholds ...string) string {
	if name != NameGroupThreshold {
		return params
	}
	for _, th := range thresholds {
		params = strings.Replace(params, "]", ", "+th+"#", 1)
	}
	return strings.ReplaceAll(params, "#", "]")
}

func TestGroupParametersCorrect(t *testing.T) {
	for _, name := range []string{NameGroupMin, NameGroupMul, NameGroupThreshold} {
		t.Run(name, func(t *testing.T) {
			mustNew(t, name, `[]`, public7(), 2)
			mustNew(t, name, groupParams(name, `[[40, 2], [60.0, 2]]`, "500", "1000"), public7(), 2)
			mustNew(t, name, groupParams(name, `[[40, "1_*"], [60.0, "2_*"]]`, "500.5", "1000"), public7(), 2)
		})
	}
}

func TestGroupParametersInvalid(t *testing.T) {
	for _, name := range []string{NameGroupMin, NameGroupMul, NameGroupThreshold} {
		t.Run(name, func(t *testing.T) {
			// th completes a subtask given as a list for GroupThreshold.
			th := ""
			if name == NameGroupThreshold {
				th = ", 1000"
			}
			for _, p := range []string{
				// Upstream tests.
				`[1]`, `1`, `[[]]`, `[[1]]`,
				`[["a", 10` + th + `]]`,
				`[[100, 1.5` + th + `]]`,
				`[[100, {}` + th + `]]`,
				`[[40, 10` + th + `], [40, "1_*"` + th + `]]`,
				`[[100, "9_*"` + th + `]]`,
				// Skipped upstream ("not yet detected"): too many testcases.
				`[[100, 20` + th + `]]`,
				// More.
				`null`, `""`, `{}`, `[[100, 2` + th + `], 5]`, `[{"testcases": 2}]`, `[{"max_score": 1}]`,
				`[[true, 2` + th + `]]`,
				`[[null, 2` + th + `]]`,
				`[[100, true` + th + `]]`,
				`[[100, -1` + th + `]]`,
				`[[100, 0` + th + `]]`,
				`[[100, []` + th + `]]`,
				`[[100, ["0_0", 1]` + th + `]]`,
				`[[100, ["9_9"]` + th + `]]`,
				`[[100, "("` + th + `]]`,
				`[[100, "(?=1)"` + th + `]]`,
				`[[10.555, 2` + th + `]]`,
				`[[50, 3` + th + `], [50, 5` + th + `]]`,
				`[{"max_score": 1, "testcases": 2, "threshold": 1, "always_show_testcases": "yes"}]`,
			} {
				mustFail(t, name, p, public7(), 2)
			}
		})
	}
	// Skipped upstream ("not yet detected"): GroupThreshold needs a
	// threshold, and it must be a number.
	for _, p := range []string{`[[1, 2]]`, `[[100, 1, "1000"]]`, `[[100, 1, null]]`, `[{"max_score": 1, "testcases": 2}]`} {
		mustFail(t, NameGroupThreshold, p, public7(), 2)
	}
	mustFail(t, NameGroupMin, `[[1, 2]]`, public7(), -1)
}

func TestGroupParametersExtraItems(t *testing.T) {
	// CMS ignores extra items (upstream skips the test that expects an
	// error); old datasets may have them.
	mustNew(t, NameGroupMin, `[[1, 2, 3]]`, public7(), 2)
	mustNew(t, NameGroupMul, `[[1, 2, 3, "x"]]`, public7(), 2)
	mustNew(t, NameGroupMin, `[{"max_score": 1, "testcases": 2, "extra": true}]`, public7(), 2)
}

func TestGroupMaxScores(t *testing.T) {
	for _, name := range []string{NameGroupMin, NameGroupMul, NameGroupThreshold} {
		for _, params := range []string{
			groupParams(name, fmt.Sprintf(`[[0, "0_*"], [%v, "1_*"], [%v, "2_*"], [%v, "3_*"]]`, s1, s2, s3), "0", "10", "20", "30"),
			groupParams(name, fmt.Sprintf(`[[0, 1], [%v, 2], [%v, 2], [%v, 2]]`, s1, s2, s3), "0", "10", "20", "30"),
		} {
			t.Run(name+params, func(t *testing.T) {
				// Only group 1_* is public.
				public := public7()
				checkMaxScores(t, mustNew(t, name, params, public, 2), s1+s2+s3, s1, headers4)
				// All groups are public.
				for c := range public {
					public[c] = true
				}
				checkMaxScores(t, mustNew(t, name, params, public, 2), s1+s2+s3, s1+s2+s3, headers4)
				// No groups are public.
				for c := range public {
					public[c] = false
				}
				checkMaxScores(t, mustNew(t, name, params, public, 2), s1+s2+s3, 0, headers4)
			})
		}
	}
}

func groupOf4(name string) string {
	return groupParams(name, fmt.Sprintf(`[[0, "0_*"], [%v, "1_*"], [%v, "2_*"], [%v, "3_*"]]`, s1, s2, s3),
		"0", "10", "20", "30")
}

func TestGroupMinComputeScore(t *testing.T) {
	st := mustNew(t, NameGroupMin, groupOf4(NameGroupMin), public7(), 2)
	sr := submissionResult(public7())

	// All correct.
	assertComputeScore(t, compute(t, st, sr), s1+s2+s3, s1, []string{"0", "10.5", "30.5", "59"}, idx4)

	// Some non-public subtask is incorrect.
	setOutcome(t, sr, "3_1", 0)
	assertComputeScore(t, compute(t, st, sr), s1+s2, s1, []string{"0", "10.5", "30.5", "0"}, idx4)

	// Also the public subtask is incorrect.
	setOutcome(t, sr, "1_0", 0)
	setOutcome(t, sr, "1_1", 0)
	assertComputeScore(t, compute(t, st, sr), s2, 0, []string{"0", "0", "30.5", "0"}, idx4)

	// Some partial results.
	setOutcome(t, sr, "3_0", 0.5)
	setOutcome(t, sr, "3_1", 0.1)
	assertComputeScore(t, compute(t, st, sr), s2+s3*0.1, 0, []string{"0", "0", "30.5", "5.9"}, idx4)
}

func TestGroupMulComputeScore(t *testing.T) {
	st := mustNew(t, NameGroupMul, groupOf4(NameGroupMul), public7(), 2)
	sr := submissionResult(public7())

	assertComputeScore(t, compute(t, st, sr), s1+s2+s3, s1, []string{"0", "10.5", "30.5", "59"}, idx4)

	setOutcome(t, sr, "3_1", 0)
	assertComputeScore(t, compute(t, st, sr), s1+s2, s1, []string{"0", "10.5", "30.5", "0"}, idx4)

	setOutcome(t, sr, "1_0", 0)
	setOutcome(t, sr, "1_1", 0)
	assertComputeScore(t, compute(t, st, sr), s2, 0, []string{"0", "0", "30.5", "0"}, idx4)

	setOutcome(t, sr, "3_0", 0.5)
	setOutcome(t, sr, "3_1", 0.1)
	assertComputeScore(t, compute(t, st, sr), s2+s3*0.5*0.1, 0, []string{"0", "0", "30.5", "2.95"}, idx4)
}

func TestGroupThresholdComputeScore(t *testing.T) {
	st := mustNew(t, NameGroupThreshold, groupOf4(NameGroupThreshold), public7(), 2)
	sr := submissionResult(public7())

	// All correct (below threshold).
	for i := range sr.Evaluations {
		sr.Evaluations[i].Outcome = 5.5
	}
	assertComputeScore(t, compute(t, st, sr), s1+s2+s3, s1, []string{"0", "10.5", "30.5", "59"}, idx4)

	// Some non-public subtask is incorrect.
	setOutcome(t, sr, "3_1", 100.5)
	assertComputeScore(t, compute(t, st, sr), s1+s2, s1, []string{"0", "10.5", "30.5", "0"}, idx4)

	// Also the public subtask is incorrect.
	setOutcome(t, sr, "1_0", 12.5)
	setOutcome(t, sr, "1_1", 12.5)
	assertComputeScore(t, compute(t, st, sr), s2, 0, []string{"0", "0", "30.5", "0"}, idx4)

	// Outcome equal to 0 is special and treated as error even if it is
	// below the threshold.
	setOutcome(t, sr, "1_0", 0)
	setOutcome(t, sr, "1_1", 0)
	assertComputeScore(t, compute(t, st, sr), s2, 0, []string{"0", "0", "30.5", "0"}, idx4)
}

func TestGroupMinJSONDetails(t *testing.T) {
	st := mustNew(t, NameGroupMin, `[[10.0, "1_*"], [20.0, "2_*"]]`, public7(), 2)
	sr := submissionResult(public7())
	setOutcome(t, sr, "1_0", 0)
	setOutcome(t, sr, "1_1", 0)
	raw, err := json.Marshal(compute(t, st, sr).Details)
	if err != nil {
		t.Fatal(err)
	}

	// Full feedback includes all testcases and times/memory.
	full, err := st.JSONDetails(raw, FeedbackFull)
	if err != nil {
		t.Fatal(err)
	}
	tc := &full.Subtasks[0].Testcases[0]
	if len(full.Subtasks) != 2 || len(full.Subtasks[0].Testcases) != 2 ||
		!tc.Has("time") || !tc.Has("memory") || !tc.Has("outcome") || !full.Subtasks[0].ShowTiming() {
		t.Errorf("full details: %+v", full)
	}

	// Restricted feedback strips time and keeps placeholders for hidden
	// testcases: 1_1 is hidden because 1_0 was the lowest.
	restricted, err := st.JSONDetails(raw, FeedbackRestricted)
	if err != nil {
		t.Fatal(err)
	}
	r0 := restricted.Subtasks[0].Testcases
	if len(restricted.Subtasks) != 2 || r0[0].Has("time") || r0[0].Has("memory") ||
		r0[0].Outcome != OutcomeNotCorrect || r0[1].Has("outcome") || r0[1].Idx != "1_1" ||
		restricted.Subtasks[0].ShowTiming() {
		t.Errorf("restricted details: %+v", restricted)
	}

	// OI restricted omits the testcases not shown.
	oi, err := st.JSONDetails(raw, FeedbackOIRestricted)
	if err != nil {
		t.Fatal(err)
	}
	if len(oi.Subtasks[0].Testcases) != 1 || oi.Subtasks[0].Testcases[0].Idx != "1_0" {
		t.Errorf("oi restricted details: %+v", oi)
	}

	if _, err := st.JSONDetails(raw, "everything"); err == nil {
		t.Error("invalid feedback level accepted")
	}
	if d, err := st.JSONDetails(nil, FeedbackFull); d != nil || err != nil {
		t.Errorf("NULL details: %v, %v", d, err)
	}
}

func TestGroupNotEvaluated(t *testing.T) {
	st := mustNew(t, NameGroupMin, `[[30, 3], [70, 4]]`, public7(), 2)
	r := compute(t, st, &SubmissionResult{})
	assertComputeScore(t, r, 0, 0, []string{"0", "0"}, nil)
	for _, d := range []Details{r.Details, r.PublicDetails} {
		if b, _ := json.Marshal(d); string(b) != "[]" {
			t.Errorf("details %s, want []", b)
		}
	}
}

func TestGroupMissingEvaluation(t *testing.T) {
	st := mustNew(t, NameGroupMin, `[[30, 3], [70, 4]]`, public7(), 2)
	sr := submissionResult(public7())
	sr.Evaluations = sr.Evaluations[1:]
	if _, err := st.ComputeScore(sr); err == nil {
		t.Error("missing evaluation accepted")
	}
	sr = submissionResult(public7())
	setOutcome(t, sr, "1_0", nan())
	if _, err := st.ComputeScore(sr); err == nil {
		t.Error("NaN outcome accepted")
	}
}

func nan() float64 {
	var zero float64
	return zero / zero
}

func TestGroupRegexMatchesPrefix(t *testing.T) {
	// Python's re.match anchors only at the start: "1_*" is "1" followed by
	// any number of "_", so it also matches 10_0.
	public := map[string]bool{"1_0": true, "10_0": true, "2_0": true}
	st := mustNew(t, NameGroupMin, `[[50, "1_*"], [50, "2"]]`, public, 0)
	r := compute(t, st, &SubmissionResult{Evaluated: true, Evaluations: []Evaluation{
		{Codename: "1_0", Outcome: 1}, {Codename: "10_0", Outcome: 0}, {Codename: "2_0", Outcome: 1},
	}})
	if got := len(r.Details.Subtasks[0].Testcases); got != 2 || r.Score != 50 {
		t.Errorf("subtask 0 has %d testcases, score %v", got, r.Score)
	}
	// An alternation stays anchored.
	st = mustNew(t, NameGroupMin, `[[100, "x|2"]]`, public, 0)
	if r := compute(t, st, &SubmissionResult{Evaluated: true, Evaluations: []Evaluation{{Codename: "2_0", Outcome: 1}}}); len(r.Details.Subtasks[0].Testcases) != 1 {
		t.Errorf("alternation: %+v", r.Details)
	}
}

func TestGroupMinNegativeZero(t *testing.T) {
	// Python's min(0.0, -0.0) is 0.0: the ranking shows "0", not "-0".
	public := map[string]bool{"a": true, "b": true}
	st := mustNew(t, NameGroupMin, `[[10, 2]]`, public, 0)
	r := compute(t, st, &SubmissionResult{Evaluated: true, Evaluations: []Evaluation{
		{Codename: "a", Outcome: 0}, {Codename: "b", Outcome: -1 * 0.0},
	}})
	if r.RankingDetails[0] != "0" {
		t.Errorf("ranking %q", r.RankingDetails)
	}
}

func TestGroupTimeLimitExceeded(t *testing.T) {
	public := map[string]bool{"a": true, "b": true, "c": true}
	st := mustNew(t, NameGroupMin, `[[10, 3]]`, public, 0)
	r := compute(t, st, &SubmissionResult{Evaluated: true, TimeLimit: ptr(2.0), Evaluations: []Evaluation{
		{Codename: "a", Outcome: 0, Text: []string{timeoutText}},
		{Codename: "b", Outcome: 0, Text: []string{"Execution timed out (wall clock limit exceeded)"}},
		{Codename: "c", Outcome: 0, Text: []string{timeoutText, "extra"}},
	}})
	tcs := r.Details.Subtasks[0].Testcases
	if !tcs[0].TimeLimitWasExceeded || tcs[1].TimeLimitWasExceeded || tcs[2].TimeLimitWasExceeded || *tcs[0].TimeLimit != 2 {
		t.Errorf("testcases %+v", tcs)
	}
	// No text is an empty list, not null.
	r = compute(t, st, &SubmissionResult{Evaluated: true, Evaluations: []Evaluation{{Codename: "a"}, {Codename: "b"}, {Codename: "c"}}})
	b, _ := json.Marshal(r.Details)
	if !strings.Contains(string(b), `"text":[]`) || !strings.Contains(string(b), `"time_limit":null`) {
		t.Errorf("details %s", b)
	}
}

func TestGroupConcurrentUse(t *testing.T) {
	st := mustNew(t, NameGroupMul, groupOf4(NameGroupMul), public7(), 2)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sr := submissionResult(public7())
			sr.Evaluations[i%len(sr.Evaluations)].Outcome = 0.5
			if _, err := st.ComputeScore(sr); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

func TestNewUnknown(t *testing.T) {
	if _, err := New("Nope", json.RawMessage(`1`), nil, 0); err == nil {
		t.Error("unknown score type accepted")
	}
	if !Valid(NameSum) || Valid("Nope") || len(Names()) != 4 || Names()[0] != NameSum {
		t.Errorf("names %q", Names())
	}
	if !FeedbackRestricted.Valid() || FeedbackLevel("x").Valid() {
		t.Error("feedback level validity")
	}
}

func BenchmarkGroupMinComputeScore(b *testing.B) {
	public := map[string]bool{}
	for i := range 100 {
		public[fmt.Sprintf("%d_%02d", i/20, i%20)] = i%3 == 0
	}
	st, err := New(NameGroupMin, json.RawMessage(`[[10, 20], [20, 20], [20, 20], [25, 20], [25, 20]]`), public, 2)
	if err != nil {
		b.Fatal(err)
	}
	sr := submissionResult(public)
	sr.Evaluations[7].Outcome = 0.5
	b.ReportAllocs()
	var buf []byte
	for b.Loop() {
		r, err := st.ComputeScore(sr)
		if err != nil {
			b.Fatal(err)
		}
		buf = r.Details.AppendJSON(buf[:0])
		buf = r.PublicDetails.AppendJSON(buf[:0])
	}
}
