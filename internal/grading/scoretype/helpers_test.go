// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cmstestsuite/unit_tests/grading/scoretypes/scoretypetestutils.py
// of CMS:
// Copyright © 2018 Stefano Maggiolo <s.maggiolo@gmail.com>

package scoretype

import (
	"encoding/json"
	"math"
	"reflect"
	"sort"
	"testing"
)

// public7 are the testcases of the group tests of CMS.
func public7() map[string]bool {
	return map[string]bool{
		"0_0": true, "1_0": true, "1_1": true, "2_0": true,
		"2_1": false, "3_0": false, "3_1": false,
	}
}

func ptr[T any](v T) *T { return &v }

// submissionResult has every testcase correct; the evaluations are in
// reverse order to make sure the score type does not depend on it.
func submissionResult(public map[string]bool) *SubmissionResult {
	codenames := make([]string, 0, len(public))
	for c := range public {
		codenames = append(codenames, c)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(codenames)))
	sr := &SubmissionResult{Evaluated: true, TimeLimit: ptr(1.0)}
	for _, c := range codenames {
		sr.Evaluations = append(sr.Evaluations, Evaluation{
			Codename: c, Outcome: 1, Text: []string{"Nothing to report"},
			ExecutionTime: ptr(0.5), ExecutionMemory: ptr(int64(100)),
		})
	}
	return sr
}

func setOutcome(t *testing.T, sr *SubmissionResult, codename string, outcome float64) {
	t.Helper()
	for i := range sr.Evaluations {
		if sr.Evaluations[i].Codename == codename {
			sr.Evaluations[i].Outcome = outcome
			return
		}
	}
	t.Fatalf("setOutcome called for non-existing codename %s", codename)
}

func mustNew(t *testing.T, name, params string, public map[string]bool, precision int) ScoreType {
	t.Helper()
	st, err := New(name, json.RawMessage(params), public, precision)
	if err != nil {
		t.Fatalf("New(%s, %s): %v", name, params, err)
	}
	return st
}

func mustFail(t *testing.T, name, params string, public map[string]bool, precision int) {
	t.Helper()
	if _, err := New(name, json.RawMessage(params), public, precision); err == nil {
		t.Errorf("New(%s, %s) succeeded", name, params)
	}
}

func compute(t *testing.T, st ScoreType, sr *SubmissionResult) *Result {
	t.Helper()
	r, err := st.ComputeScore(sr)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func almostEqual(a, b float64) bool { return math.Abs(a-b) < 1e-7 }

// assertComputeScore is upstream assertComputeScore: total and public
// scores, ranking details and the idx of each detail.
func assertComputeScore(t *testing.T, r *Result, total, public float64, ranking []string, idx []any) {
	t.Helper()
	if !almostEqual(r.Score, total) {
		t.Errorf("score %v, want %v", r.Score, total)
	}
	if !almostEqual(r.PublicScore, public) {
		t.Errorf("public score %v, want %v", r.PublicScore, public)
	}
	if !reflect.DeepEqual(r.RankingDetails, ranking) {
		t.Errorf("ranking details %q, want %q", r.RankingDetails, ranking)
	}
	var got []any
	if r.Details.Grouped {
		for _, s := range r.Details.Subtasks {
			got = append(got, s.Idx)
		}
	} else {
		for _, tc := range r.Details.Testcases {
			got = append(got, tc.Idx)
		}
	}
	if !reflect.DeepEqual(got, idx) {
		t.Errorf("details idx %v, want %v", got, idx)
	}
}

func checkMaxScores(t *testing.T, st ScoreType, max, public float64, headers []string) {
	t.Helper()
	if st.MaxScore() != max || st.MaxPublicScore() != public || !reflect.DeepEqual(st.RankingHeaders(), headers) {
		t.Errorf("max scores (%v, %v, %q), want (%v, %v, %q)",
			st.MaxScore(), st.MaxPublicScore(), st.RankingHeaders(), max, public, headers)
	}
}
