// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cmstestsuite/unit_tests/grading/scoretypes/SumTest.py of CMS:
// Copyright © 2018 Stefano Maggiolo <s.maggiolo@gmail.com>

package scoretype

import (
	"encoding/json"
	"testing"
)

func public4() map[string]bool {
	return map[string]bool{"0": false, "1": true, "2": false, "3": false}
}

var sumIdx = []any{"0", "1", "2", "3"}

func TestSumParameters(t *testing.T) {
	mustNew(t, NameSum, `100`, public4(), 2)
	mustNew(t, NameSum, `1.5`, public4(), 2)
	// Like CMS, Sum does not check the precision of its parameter.
	mustNew(t, NameSum, `33.333333333333336`, public4(), 0)
	for _, p := range []string{`[]`, `"1"`, `true`, `null`, `{}`, ``} {
		mustFail(t, NameSum, p, public4(), 2)
	}
	// Even without testcases (CMS only noticed when adding them).
	mustFail(t, NameSum, `[]`, map[string]bool{}, 2)
}

func TestSumMaxScores(t *testing.T) {
	checkMaxScores(t, mustNew(t, NameSum, `10.5`, public4(), 2), 10.5*4, 10.5, []string{})
	checkMaxScores(t, mustNew(t, NameSum, `10.5`, map[string]bool{}, 2), 0, 0, []string{})
}

func TestSumComputeScore(t *testing.T) {
	const tcScore = 10.5
	st := mustNew(t, NameSum, `10.5`, public4(), 2)
	sr := submissionResult(public4())

	// All correct.
	assertComputeScore(t, compute(t, st, sr), tcScore*4, tcScore, []string{}, sumIdx)

	// Some non-public testcase is incorrect.
	setOutcome(t, sr, "3", 0)
	assertComputeScore(t, compute(t, st, sr), tcScore*3, tcScore, []string{}, sumIdx)

	// Also the public testcase is incorrect.
	setOutcome(t, sr, "1", 0)
	assertComputeScore(t, compute(t, st, sr), tcScore*2, 0, []string{}, sumIdx)

	// Now the public testcase has a partial score.
	setOutcome(t, sr, "1", 0.2)
	r := compute(t, st, sr)
	assertComputeScore(t, r, tcScore*2.2, tcScore*0.2, []string{}, sumIdx)
	if o := r.Details.Testcases[1].Outcome; o != OutcomePartiallyCorrect {
		t.Errorf("outcome %q", o)
	}
	if b, _ := json.Marshal(r.PublicDetails); string(b) != `[{"idx":"0"},{"idx":"1","outcome":"Partially correct",`+
		`"text":["Nothing to report"],"time":0.5,"memory":100},{"idx":"2"},{"idx":"3"}]` {
		t.Errorf("public details %s", b)
	}
	if b, _ := json.Marshal(r.RankingDetails); string(b) != "[]" {
		t.Errorf("ranking details %s", b)
	}
}

func TestSumNotEvaluated(t *testing.T) {
	r := compute(t, mustNew(t, NameSum, `5`, public4(), 2), &SubmissionResult{})
	assertComputeScore(t, r, 0, 0, []string{}, nil)
	for _, d := range []Details{r.Details, r.PublicDetails} {
		if b, _ := json.Marshal(d); string(b) != "[]" {
			t.Errorf("details %s", b)
		}
	}
	sr := submissionResult(public4())
	sr.Evaluations = sr.Evaluations[:3]
	if _, err := mustNew(t, NameSum, `5`, public4(), 2).ComputeScore(sr); err == nil {
		t.Error("missing evaluation accepted")
	}
}

func TestSumJSONDetails(t *testing.T) {
	st := mustNew(t, NameSum, `10`, public4(), 2)
	r := compute(t, st, submissionResult(public4()))
	raw, _ := json.Marshal(r.Details)

	full, err := st.JSONDetails(raw, FeedbackFull)
	if err != nil {
		t.Fatal(err)
	}
	if len(full.Testcases) != 4 || !full.Testcases[0].Has("time") || !full.Testcases[0].Has("memory") || full.Grouped {
		t.Errorf("full details %+v", full)
	}
	restricted, err := st.JSONDetails(raw, FeedbackRestricted)
	if err != nil {
		t.Fatal(err)
	}
	if len(restricted.Testcases) != 4 || restricted.Testcases[0].Has("time") || restricted.Testcases[0].Has("memory") {
		t.Errorf("restricted details %+v", restricted)
	}

	// Upstream test_get_html_details: the time and memory columns appear
	// only with full feedback.
	if !full.ShowTiming() || restricted.ShowTiming() {
		t.Error("ShowTiming")
	}
}
