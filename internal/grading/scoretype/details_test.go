// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package scoretype

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func filtered(t *testing.T, st ScoreType, raw string, level FeedbackLevel) string {
	t.Helper()
	d, err := st.JSONDetails(json.RawMessage(raw), level)
	if err != nil {
		t.Fatalf("JSONDetails(%s): %v", raw, err)
	}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestJSONDetailsOldFormats(t *testing.T) {
	st := mustNew(t, NameGroupMin, `[[30, 2], [70, 5]]`, public7(), 2)
	// Written by CMS 1.4 and converted by the dump updaters: no score, no
	// time_limit, no show_in_oi_restricted_feedback; a public subtask with
	// private testcases.
	raw := `[{"idx": 1, "score_fraction": 1.0, "max_score": 30, "testcases": [
	   {"idx": "0_0", "outcome": "Correct", "text": ["Output is correct"], "time": 0.1, "memory": 1024,
	    "show_in_restricted_feedback": true}]},
	  {"idx": 2, "testcases": [{"idx": "2_1"}, {"idx": "2_0", "outcome": "Not correct",
	    "text": ["Output isn't correct"], "time": null, "memory": null, "show_in_restricted_feedback": true}]}]`
	for level, want := range map[FeedbackLevel]string{
		FeedbackFull: `[{"idx":1,"score_fraction":1,"max_score":30,"testcases":[{"idx":"0_0","outcome":"Correct",` +
			`"text":["Output is correct"],"time":0.1,"memory":1024}]},{"idx":2,"testcases":[{"idx":"2_1"},` +
			`{"idx":"2_0","outcome":"Not correct","text":["Output isn't correct"],"time":null,"memory":null}]}]`,
		FeedbackRestricted: `[{"idx":1,"score_fraction":1,"max_score":30,"testcases":[{"idx":"0_0","outcome":"Correct",` +
			`"text":["Output is correct"]}]},{"idx":2,"testcases":[{"idx":"2_1"},` +
			`{"idx":"2_0","outcome":"Not correct","text":["Output isn't correct"]}]}]`,
		FeedbackOIRestricted: `[{"idx":1,"score_fraction":1,"max_score":30,"testcases":[]},{"idx":2,"testcases":[]}]`,
	} {
		if got := filtered(t, st, raw, level); got != want {
			t.Errorf("%s:\n got %s\nwant %s", level, got, want)
		}
	}

	d, err := st.JSONDetails(json.RawMessage(raw), FeedbackFull)
	if err != nil {
		t.Fatal(err)
	}
	s0, s1 := &d.Subtasks[0], &d.Subtasks[1]
	if !s0.Has("score_fraction") || s0.Has("score") || !s0.Has("max_score") || s1.Has("score_fraction") ||
		!s0.ShowTiming() || !s1.ShowTiming() || s1.Testcases[0].Has("outcome") || s1.Testcases[1].Time != nil ||
		s0.Testcases[0].Has("time_limit") || !s0.Testcases[0].Has("text") || s0.Testcases[0].Has("nonsense") {
		t.Errorf("parsed %+v", d)
	}
}

func TestJSONDetailsTextNull(t *testing.T) {
	// A shown testcase always has "text" (tc.get("text") in CMS).
	st := mustNew(t, NameGroupMin, `[[100, 1]]`, map[string]bool{"a": true}, 0)
	raw := `[{"idx":0,"score_fraction":0,"score":0,"max_score":100,"testcases":[{"idx":"a","outcome":"Not correct",` +
		`"show_in_restricted_feedback":true}]}]`
	want := `[{"idx":0,"score_fraction":0,"score":0,"max_score":100,"testcases":[{"idx":"a","outcome":"Not correct","text":null}]}]`
	if got := filtered(t, st, raw, FeedbackRestricted); got != want {
		t.Errorf("got %s", got)
	}
	// Sum only shows testcases with both outcome and text.
	sum := mustNew(t, NameSum, `1`, map[string]bool{"a": true}, 0)
	if got := filtered(t, sum, `[{"idx":"a","outcome":"Correct"}]`, FeedbackFull); got != `[{"idx":"a"}]` {
		t.Errorf("sum got %s", got)
	}
}

func TestParseDetailsErrors(t *testing.T) {
	for _, raw := range []string{
		`{}`, `"x"`, `1`, `[1]`, `[null]`, `[{"testcases": []}]`, `[{"idx": "0"}]`, `[{"idx": 1.5}]`,
		`[{"idx": 0, "score": "1"}]`, `[{"idx": 0, "score": null}]`, `[{"idx": 0, "testcases": null}]`,
		`[{"idx": 0, "testcases": [1]}]`, `[{"idx": 0, "testcases": [{"idx": 1}]}]`,
		`[{"idx": 0, "testcases": [{"idx": "a", "outcome": null}]}]`,
		`[{"idx": 0, "testcases": [{"idx": "a", "text": "Output is correct"}]}]`,
		`[{"idx": 0, "testcases": [{"idx": "a", "time": "1"}]}]`,
		`[{"idx": 0, "testcases": [{"idx": "a", "memory": 1.5}]}]`,
		`[{"idx": 0, "testcases": [{"idx": "a", "show_in_restricted_feedback": 1}]}]`,
		`[`,
	} {
		if d, err := ParseDetails(json.RawMessage(raw), true); err == nil {
			t.Errorf("ParseDetails(%s) = %+v", raw, d)
		}
	}
	for _, raw := range []string{`[{"idx": 0}]`, `[null]`, `[[]]`, `{"a": 1}`} {
		if d, err := ParseDetails(json.RawMessage(raw), false); err == nil {
			t.Errorf("ParseDetails(%s, sum) = %+v", raw, d)
		}
	}
	for _, raw := range []string{``, ` `, `null`, ` null `} {
		if d, err := ParseDetails(json.RawMessage(raw), true); d != nil || err != nil {
			t.Errorf("ParseDetails(%q) = %v, %v", raw, d, err)
		}
	}
	// Empty lists, integral memory written as a float, null booleans.
	d, err := ParseDetails(json.RawMessage(`[{"idx":0,"testcases":[{"idx":"a","memory":2048.0,"time_limit_was_exceeded":null}]}]`), true)
	if err != nil || *d.Subtasks[0].Testcases[0].Memory != 2048 || d.Subtasks[0].Testcases[0].TimeLimitWasExceeded {
		t.Errorf("ParseDetails = %+v, %v", d, err)
	}
	if d, err := ParseDetails(json.RawMessage(`[]`), false); err != nil || d.Testcases == nil || d.Grouped {
		t.Errorf("ParseDetails([]) = %+v, %v", d, err)
	}
}

func TestDetailsRoundTrip(t *testing.T) {
	st := mustNew(t, NameGroupThreshold, `[[0, "0_*", 0], {"max_score": 40, "testcases": "1_.*", "threshold": 2},
		{"max_score": 60, "testcases": "[23]_.*", "threshold": 5, "always_show_testcases": true}]`, public7(), 2)
	sr := submissionResult(public7())
	setOutcome(t, sr, "2_0", 7)
	setOutcome(t, sr, "1_1", 3)
	r := compute(t, st, sr)
	for _, d := range []Details{r.Details, r.PublicDetails} {
		b1, _ := json.Marshal(d)
		p, err := ParseDetails(b1, true)
		if err != nil {
			t.Fatal(err)
		}
		b2, _ := json.Marshal(p)
		if string(b1) != string(b2) {
			t.Errorf("round trip:\n%s\n%s", b1, b2)
		}
	}
	// always_show_testcases keeps every public testcase of a failed subtask
	// visible.
	for _, tc := range r.Details.Subtasks[2].Testcases {
		if public7()[tc.Idx] != tc.ShowInRestrictedFeedback || public7()[tc.Idx] != tc.ShowInOIRestrictedFeedback {
			t.Errorf("testcase %+v", tc)
		}
	}
	if r.Details.Subtasks[1].Testcases[1].Outcome != OutcomeNotCorrect || r.Score != 0 {
		t.Errorf("result %+v", r)
	}
}

func TestAppendString(t *testing.T) {
	for _, s := range []string{
		"", "plain", `quote " backslash \ slash /`, "\x00\x01\x1f\t\n\r\b\f", "ñandú 漢字 🎉",
		"\u2028\u2029", "bad \xff\xfe utf8", "<html> & </html>", strings.Repeat("x", 100),
	} {
		b := appendString(nil, s)
		var got string
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatalf("%q -> %s: %v", s, b, err)
		}
		want, _ := json.Marshal(s)
		var w string
		_ = json.Unmarshal(want, &w)
		if got != w {
			t.Errorf("%q -> %s decodes to %q, want %q", s, b, got, w)
		}
	}
}

func TestAppendFloat(t *testing.T) {
	for _, f := range []float64{0, 1, -1, 0.1, 1e-7, 1.5e-9, 1e21, 123456789.125, 5e-324, math.MaxFloat64} {
		want, _ := json.Marshal(f)
		if got := appendFloat(nil, f); string(got) != string(want) {
			t.Errorf("appendFloat(%v) = %s, want %s", f, got, want)
		}
	}
	if got := appendFloat(nil, math.NaN()); string(got) != "null" {
		t.Errorf("NaN = %s", got)
	}
}

func BenchmarkJSONDetails(b *testing.B) {
	public := map[string]bool{}
	for c := range public7() {
		public[c] = true
	}
	st, err := New(NameGroupMin, json.RawMessage(`[[30, 3], [70, 4]]`), public, 2)
	if err != nil {
		b.Fatal(err)
	}
	r, err := st.ComputeScore(submissionResult(public))
	if err != nil {
		b.Fatal(err)
	}
	raw, _ := json.Marshal(r.Details)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := st.JSONDetails(raw, FeedbackRestricted); err != nil {
			b.Fatal(err)
		}
	}
}
