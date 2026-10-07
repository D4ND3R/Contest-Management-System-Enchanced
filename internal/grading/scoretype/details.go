// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/grading/scoretypes/abc.py and Sum.py of CMS
// (get_json_details):
// Copyright © 2010-2012 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2013-2016 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2015 wafrelka <wafrelka@gmail.com>

package scoretype

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"unicode/utf8"
)

// keys records which keys a details object has: CMS stores dicts whose
// keys vary (a hidden testcase only has "idx", details written by older
// CMS versions lack "score" or "time_limit"...) and the templates test
// them ("outcome" in tc).
type keys uint16

const (
	kIdx keys = 1 << iota
	kOutcome
	kText
	kTime
	kTimeLimit
	kTimeLimitWasExceeded
	kMemory
	kShowRestricted
	kShowOIRestricted
	kScoreFraction
	kScore
	kMaxScore
	kTestcases
)

var keyNames = map[string]keys{
	"idx":                            kIdx,
	"outcome":                        kOutcome,
	"text":                           kText,
	"time":                           kTime,
	"time_limit":                     kTimeLimit,
	"time_limit_was_exceeded":        kTimeLimitWasExceeded,
	"memory":                         kMemory,
	"show_in_restricted_feedback":    kShowRestricted,
	"show_in_oi_restricted_feedback": kShowOIRestricted,
	"score_fraction":                 kScoreFraction,
	"score":                          kScore,
	"max_score":                      kMaxScore,
	"testcases":                      kTestcases,
}

const (
	// Keys of the testcases computed by the group score types and by Sum.
	groupTestcaseKeys = kIdx | kOutcome | kText | kTime | kTimeLimit | kTimeLimitWasExceeded |
		kMemory | kShowRestricted | kShowOIRestricted
	sumTestcaseKeys = kIdx | kOutcome | kText | kTime | kMemory
	// Keys of a subtask with a score, and of a subtask of the public
	// details that has private testcases.
	subtaskKeys        = kIdx | kScoreFraction | kScore | kMaxScore | kTestcases
	hiddenSubtaskKeys  = kIdx | kTestcases
	filteredScoreKeys  = kScoreFraction | kScore | kMaxScore
	fullFeedbackTcKeys = kTime | kTimeLimit | kTimeLimitWasExceeded | kMemory
)

// TestcaseDetails are the details of a testcase. A testcase hidden from
// the viewer only has Idx; use Has to tell which keys are set.
type TestcaseDetails struct {
	// Idx is the codename of the testcase.
	Idx string
	// Outcome is OutcomeCorrect, OutcomeNotCorrect or
	// OutcomePartiallyCorrect.
	Outcome string
	// Text is the message of the evaluation: a msgid and its arguments.
	Text []string
	// Time in seconds and Memory in bytes; nil if unknown.
	Time   *float64
	Memory *int64
	// TimeLimit of the dataset (group score types), and whether the
	// testcase exceeded it.
	TimeLimit            *float64
	TimeLimitWasExceeded bool
	// ShowInRestrictedFeedback and ShowInOIRestrictedFeedback tell whether
	// the testcase is shown with those feedback levels (group score types).
	ShowInRestrictedFeedback   bool
	ShowInOIRestrictedFeedback bool

	has keys
}

// SubtaskDetails are the details of a subtask (group score types). A
// subtask of the public details with private testcases has no score: only
// Idx and Testcases.
type SubtaskDetails struct {
	// Idx is the index of the subtask, from 0.
	Idx int
	// ScoreFraction is the fraction of MaxScore obtained, and Score that
	// score rounded to the score precision of the task.
	ScoreFraction float64
	Score         float64
	MaxScore      float64
	Testcases     []TestcaseDetails

	has keys
}

// Details are the score details of a submission: a list of subtasks for
// the group score types, a list of testcases for Sum. As JSON they are the
// list itself, like in CMS.
type Details struct {
	// Grouped is true for the group score types (Subtasks), false for Sum
	// (Testcases).
	Grouped   bool
	Subtasks  []SubtaskDetails
	Testcases []TestcaseDetails
}

// Has reports whether the testcase has the JSON key ("outcome", "time"...),
// like `key in tc` in the templates of CMS.
func (t *TestcaseDetails) Has(key string) bool { return t.has&keyNames[key] != 0 }

// Has reports whether the subtask has the JSON key ("score_fraction",
// "score"...).
func (s *SubtaskDetails) Has(key string) bool { return s.has&keyNames[key] != 0 }

// ShowTiming reports whether the columns of time and memory are shown: some
// testcase has the key "time" or "memory".
func (s *SubtaskDetails) ShowTiming() bool { return showTiming(s.Testcases) }

// ShowTiming reports whether the columns of time and memory are shown (Sum).
func (d *Details) ShowTiming() bool { return showTiming(d.Testcases) }

func showTiming(tcs []TestcaseDetails) bool {
	for i := range tcs {
		if tcs[i].has&(kTime|kMemory) != 0 {
			return true
		}
	}
	return false
}

// MarshalJSON encodes the details as the JSON list CMS stores.
func (d Details) MarshalJSON() ([]byte, error) { return d.AppendJSON(nil), nil }

// AppendJSON appends the JSON of the details to b.
func (d Details) AppendJSON(b []byte) []byte {
	b = append(b, '[')
	if d.Grouped {
		for i := range d.Subtasks {
			if i > 0 {
				b = append(b, ',')
			}
			b = d.Subtasks[i].appendJSON(b)
		}
	} else {
		for i := range d.Testcases {
			if i > 0 {
				b = append(b, ',')
			}
			b = d.Testcases[i].appendJSON(b)
		}
	}
	return append(b, ']')
}

// MarshalJSON encodes the subtask with the keys it has, in the order of CMS.
func (s SubtaskDetails) MarshalJSON() ([]byte, error) { return s.appendJSON(nil), nil }

func (s *SubtaskDetails) appendJSON(b []byte) []byte {
	b = append(b, `{"idx":`...)
	b = strconv.AppendInt(b, int64(s.Idx), 10)
	if s.has&kScoreFraction != 0 {
		b = appendFloat(append(b, `,"score_fraction":`...), s.ScoreFraction)
	}
	if s.has&kScore != 0 {
		b = appendFloat(append(b, `,"score":`...), s.Score)
	}
	if s.has&kMaxScore != 0 {
		b = appendFloat(append(b, `,"max_score":`...), s.MaxScore)
	}
	if s.has&kTestcases != 0 {
		b = append(b, `,"testcases":[`...)
		for i := range s.Testcases {
			if i > 0 {
				b = append(b, ',')
			}
			b = s.Testcases[i].appendJSON(b)
		}
		b = append(b, ']')
	}
	return append(b, '}')
}

// MarshalJSON encodes the testcase with the keys it has, in the order of
// CMS.
func (t TestcaseDetails) MarshalJSON() ([]byte, error) { return t.appendJSON(nil), nil }

func (t *TestcaseDetails) appendJSON(b []byte) []byte {
	// "idx" is always there; CMS writes null for a testcase without one.
	b = append(b, `{"idx":`...)
	if t.has&kIdx != 0 {
		b = appendString(b, t.Idx)
	} else {
		b = append(b, "null"...)
	}
	if t.has&kOutcome != 0 {
		b = appendString(append(b, `,"outcome":`...), t.Outcome)
	}
	if t.has&kText != 0 {
		b = append(b, `,"text":`...)
		if t.Text == nil {
			b = append(b, "null"...)
		} else {
			b = append(b, '[')
			for i, s := range t.Text {
				if i > 0 {
					b = append(b, ',')
				}
				b = appendString(b, s)
			}
			b = append(b, ']')
		}
	}
	if t.has&kTime != 0 {
		b = appendOptFloat(append(b, `,"time":`...), t.Time)
	}
	if t.has&kTimeLimit != 0 {
		b = appendOptFloat(append(b, `,"time_limit":`...), t.TimeLimit)
	}
	if t.has&kTimeLimitWasExceeded != 0 {
		b = strconv.AppendBool(append(b, `,"time_limit_was_exceeded":`...), t.TimeLimitWasExceeded)
	}
	if t.has&kMemory != 0 {
		b = append(b, `,"memory":`...)
		if t.Memory == nil {
			b = append(b, "null"...)
		} else {
			b = strconv.AppendInt(b, *t.Memory, 10)
		}
	}
	if t.has&kShowRestricted != 0 {
		b = strconv.AppendBool(append(b, `,"show_in_restricted_feedback":`...), t.ShowInRestrictedFeedback)
	}
	if t.has&kShowOIRestricted != 0 {
		b = strconv.AppendBool(append(b, `,"show_in_oi_restricted_feedback":`...), t.ShowInOIRestrictedFeedback)
	}
	return append(b, '}')
}

func appendOptFloat(b []byte, f *float64) []byte {
	if f == nil {
		return append(b, "null"...)
	}
	return appendFloat(b, *f)
}

// appendFloat formats f like encoding/json. Non-finite values cannot be
// JSON; they are written as null (ComputeScore rejects them before).
func appendFloat(b []byte, f float64) []byte {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return append(b, "null"...)
	}
	abs := math.Abs(f)
	format := byte('f')
	if abs != 0 && (abs < 1e-6 || abs >= 1e21) {
		format = 'e'
	}
	b = strconv.AppendFloat(b, f, format, -1, 64)
	if format == 'e' {
		// e-09 -> e-9, as encoding/json.
		n := len(b)
		if n >= 4 && b[n-4] == 'e' && b[n-3] == '-' && b[n-2] == '0' {
			b[n-2] = b[n-1]
			b = b[:n-1]
		}
	}
	return b
}

// appendString appends s as a JSON string, escaping like encoding/json
// (without the HTML escapes): invalid UTF-8 becomes U+FFFD.
func appendString(b []byte, s string) []byte {
	const hex = "0123456789abcdef"
	b = append(b, '"')
	start := 0
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			if c >= 0x20 && c != '"' && c != '\\' {
				i++
				continue
			}
			b = append(b, s[start:i]...)
			switch c {
			case '"', '\\':
				b = append(b, '\\', c)
			case '\n':
				b = append(b, '\\', 'n')
			case '\r':
				b = append(b, '\\', 'r')
			case '\t':
				b = append(b, '\\', 't')
			default:
				b = append(b, '\\', 'u', '0', '0', hex[c>>4], hex[c&0xF])
			}
			i++
			start = i
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			b = append(b, s[start:i]...)
			b = append(b, "\ufffd"...)
			i += size
			start = i
			continue
		}
		if r == '\u2028' || r == '\u2029' {
			b = append(b, s[start:i]...)
			b = append(b, '\\', 'u', '2', '0', '2', hex[r&0xF])
			i += size
			start = i
			continue
		}
		i += size
	}
	b = append(b, s[start:]...)
	return append(b, '"')
}

// ParseDetails decodes stored score details: subtasks if grouped,
// testcases otherwise. NULL (or empty) details give nil. Details that do
// not have the expected shape are an error (CMS then shows "Score details
// temporarily unavailable."); keys missing from an object are fine.
func ParseDetails(raw json.RawMessage, grouped bool) (*Details, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || isNull(raw) {
		return nil, nil
	}
	if raw[0] != '[' {
		return nil, errors.New("score details: not a list")
	}
	d := &Details{Grouped: grouped}
	var err error
	if grouped {
		err = json.Unmarshal(raw, &d.Subtasks)
		if d.Subtasks == nil {
			d.Subtasks = []SubtaskDetails{}
		}
	} else {
		err = json.Unmarshal(raw, &d.Testcases)
		if d.Testcases == nil {
			d.Testcases = []TestcaseDetails{}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("score details: %w", err)
	}
	return d, nil
}

func isNull(raw []byte) bool { return string(raw) == "null" }

type rawSubtask struct {
	Idx           json.RawMessage `json:"idx"`
	ScoreFraction json.RawMessage `json:"score_fraction"`
	Score         json.RawMessage `json:"score"`
	MaxScore      json.RawMessage `json:"max_score"`
	Testcases     json.RawMessage `json:"testcases"`
}

// UnmarshalJSON decodes a subtask, remembering which keys it has.
func (s *SubtaskDetails) UnmarshalJSON(b []byte) error {
	var r rawSubtask
	if isNull(bytes.TrimSpace(b)) {
		return errors.New("a subtask is null")
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}
	*s = SubtaskDetails{}
	if r.Idx == nil || isNull(r.Idx) {
		return errors.New("a subtask has no idx")
	}
	if err := json.Unmarshal(r.Idx, &s.Idx); err != nil {
		return fmt.Errorf("subtask idx: %w", err)
	}
	s.has = kIdx
	for _, f := range [...]struct {
		raw json.RawMessage
		dst *float64
		k   keys
	}{{r.ScoreFraction, &s.ScoreFraction, kScoreFraction}, {r.Score, &s.Score, kScore}, {r.MaxScore, &s.MaxScore, kMaxScore}} {
		if f.raw == nil {
			continue
		}
		if err := decodeNumber(f.raw, f.dst); err != nil {
			return fmt.Errorf("subtask %d: %w", s.Idx, err)
		}
		s.has |= f.k
	}
	if r.Testcases != nil {
		if isNull(r.Testcases) {
			return fmt.Errorf("subtask %d: testcases is null", s.Idx)
		}
		if err := json.Unmarshal(r.Testcases, &s.Testcases); err != nil {
			return fmt.Errorf("subtask %d: %w", s.Idx, err)
		}
	}
	if s.Testcases == nil {
		s.Testcases = []TestcaseDetails{}
	}
	// The filtered details always have "testcases" (st.get("testcases", [])).
	s.has |= kTestcases
	return nil
}

type rawTestcase struct {
	Idx                        json.RawMessage `json:"idx"`
	Outcome                    json.RawMessage `json:"outcome"`
	Text                       json.RawMessage `json:"text"`
	Time                       json.RawMessage `json:"time"`
	TimeLimit                  json.RawMessage `json:"time_limit"`
	TimeLimitWasExceeded       json.RawMessage `json:"time_limit_was_exceeded"`
	Memory                     json.RawMessage `json:"memory"`
	ShowInRestrictedFeedback   json.RawMessage `json:"show_in_restricted_feedback"`
	ShowInOIRestrictedFeedback json.RawMessage `json:"show_in_oi_restricted_feedback"`
}

// UnmarshalJSON decodes a testcase, remembering which keys it has.
func (t *TestcaseDetails) UnmarshalJSON(b []byte) error {
	var r rawTestcase
	if isNull(bytes.TrimSpace(b)) {
		return errors.New("a testcase is null")
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}
	*t = TestcaseDetails{}
	if r.Idx != nil && !isNull(r.Idx) {
		if err := json.Unmarshal(r.Idx, &t.Idx); err != nil {
			return fmt.Errorf("testcase idx: %w", err)
		}
		t.has |= kIdx
	}
	if r.Outcome != nil {
		if err := json.Unmarshal(r.Outcome, &t.Outcome); err != nil || isNull(r.Outcome) {
			return fmt.Errorf("testcase %q: invalid outcome %s", t.Idx, r.Outcome)
		}
		t.has |= kOutcome
	}
	if r.Text != nil {
		if err := json.Unmarshal(r.Text, &t.Text); err != nil {
			return fmt.Errorf("testcase %q: text: %w", t.Idx, err)
		}
		t.has |= kText
	}
	for _, f := range [...]struct {
		raw json.RawMessage
		dst **float64
		k   keys
	}{{r.Time, &t.Time, kTime}, {r.TimeLimit, &t.TimeLimit, kTimeLimit}} {
		if f.raw == nil {
			continue
		}
		t.has |= f.k
		if isNull(f.raw) {
			continue
		}
		v := new(float64)
		if err := decodeNumber(f.raw, v); err != nil {
			return fmt.Errorf("testcase %q: %w", t.Idx, err)
		}
		*f.dst = v
	}
	if r.Memory != nil {
		t.has |= kMemory
		if !isNull(r.Memory) {
			m, err := decodeInt(r.Memory)
			if err != nil {
				return fmt.Errorf("testcase %q: memory: %w", t.Idx, err)
			}
			t.Memory = &m
		}
	}
	for _, f := range [...]struct {
		raw json.RawMessage
		dst *bool
		k   keys
	}{
		{r.TimeLimitWasExceeded, &t.TimeLimitWasExceeded, kTimeLimitWasExceeded},
		{r.ShowInRestrictedFeedback, &t.ShowInRestrictedFeedback, kShowRestricted},
		{r.ShowInOIRestrictedFeedback, &t.ShowInOIRestrictedFeedback, kShowOIRestricted},
	} {
		if f.raw == nil {
			continue
		}
		// null is false, as in the templates.
		if err := json.Unmarshal(f.raw, f.dst); err != nil {
			return fmt.Errorf("testcase %q: %w", t.Idx, err)
		}
		t.has |= f.k
	}
	return nil
}

// decodeNumber decodes a JSON number (not null).
func decodeNumber(raw json.RawMessage, dst *float64) error {
	if isNull(raw) {
		return errors.New("a number is null")
	}
	return json.Unmarshal(raw, dst)
}

// decodeInt decodes a JSON number that is an integer (possibly written as
// 1.0).
func decodeInt(raw json.RawMessage) (int64, error) {
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, err
	}
	if i, err := n.Int64(); err == nil {
		return i, nil
	}
	f, err := n.Float64()
	if err != nil || f != math.Trunc(f) || math.Abs(f) > 1<<63 {
		return 0, fmt.Errorf("%s is not an integer", n)
	}
	return int64(f), nil
}

// filterGroup filters the details of a group score type for the feedback
// level (upstream ScoreTypeGroup.get_json_details). Unlike CMS, a subtask
// without a score (public details of a subtask with private testcases)
// keeps its keys out instead of failing: the template shows it as N/A.
func filterGroup(src []SubtaskDetails, level FeedbackLevel) []SubtaskDetails {
	out := make([]SubtaskDetails, len(src))
	for i := range src {
		st := &src[i]
		f := SubtaskDetails{
			Idx:           st.Idx,
			ScoreFraction: st.ScoreFraction,
			Score:         st.Score,
			MaxScore:      st.MaxScore,
			has:           kIdx | st.has&filteredScoreKeys | kTestcases,
		}
		tcs := make([]TestcaseDetails, 0, len(st.Testcases))
		for j := range st.Testcases {
			tc := &st.Testcases[j]
			var show bool
			switch level {
			case FeedbackFull:
				show = true
			case FeedbackRestricted:
				show = tc.ShowInRestrictedFeedback
			case FeedbackOIRestricted:
				show = tc.ShowInOIRestrictedFeedback
			}
			if show && tc.has&kOutcome != 0 {
				ft := TestcaseDetails{Idx: tc.Idx, Outcome: tc.Outcome, Text: tc.Text, has: tc.has&kIdx | kOutcome | kText}
				if level == FeedbackFull {
					ft.Time, ft.TimeLimit, ft.TimeLimitWasExceeded, ft.Memory = tc.Time, tc.TimeLimit, tc.TimeLimitWasExceeded, tc.Memory
					ft.has |= tc.has & fullFeedbackTcKeys
				}
				tcs = append(tcs, ft)
			} else if level != FeedbackOIRestricted {
				tcs = append(tcs, TestcaseDetails{Idx: tc.Idx, has: tc.has & kIdx})
			}
		}
		f.Testcases = tcs
		out[i] = f
	}
	return out
}

// filterSum filters the details of Sum (upstream Sum.get_json_details):
// every testcase with an outcome is shown, with time and memory only with
// full feedback.
func filterSum(src []TestcaseDetails, level FeedbackLevel) []TestcaseDetails {
	out := make([]TestcaseDetails, len(src))
	for i := range src {
		tc := &src[i]
		if tc.has&kOutcome != 0 && tc.has&kText != 0 {
			ft := TestcaseDetails{Idx: tc.Idx, Outcome: tc.Outcome, Text: tc.Text, has: tc.has&kIdx | kOutcome | kText}
			if level == FeedbackFull {
				ft.Time, ft.Memory = tc.Time, tc.Memory
				ft.has |= tc.has & (kTime | kMemory)
			}
			out[i] = ft
		} else {
			out[i] = TestcaseDetails{Idx: tc.Idx, has: tc.has & kIdx}
		}
	}
	return out
}

// jsonDetails implements ScoreType.JSONDetails.
func jsonDetails(raw json.RawMessage, level FeedbackLevel, grouped bool) (*Details, error) {
	if !level.Valid() {
		return nil, fmt.Errorf("invalid feedback level %q", level)
	}
	d, err := ParseDetails(raw, grouped)
	if d == nil || err != nil {
		return nil, err
	}
	if grouped {
		d.Subtasks = filterGroup(d.Subtasks, level)
	} else {
		d.Testcases = filterSum(d.Testcases, level)
	}
	return d, nil
}
