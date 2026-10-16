// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cmstestsuite/unit_tests/server/contest/submission/
// check_test.py of CMS (the parts that do not query the database: the
// counts are Stats here):
// Copyright © 2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>

package submission

import (
	"testing"
	"time"
)

func i32(v int32) *int32 { return &v }

func dur(d time.Duration) *time.Duration { return &d }

func TestCheckMaxNumber(t *testing.T) {
	cases := []struct {
		max          *int32
		count        int64
		unrestricted bool
		want         bool
	}{
		{nil, 100, false, true},
		{i32(3), 0, false, true},
		{i32(3), 2, false, true},
		{i32(3), 3, false, false},
		{i32(3), 4, false, false},
		{i32(3), 4, true, true},
		{i32(0), 0, false, false},
	}
	for _, c := range cases {
		if got := CheckMaxNumber(c.max, c.count, c.unrestricted); got != c.want {
			t.Errorf("CheckMaxNumber(%v, %d, %v) = %v", c.max, c.count, c.unrestricted, got)
		}
	}
}

func TestCheckMinInterval(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	tp := func(d time.Duration) *time.Time { t := now.Add(-d); return &t }
	cases := []struct {
		min          *time.Duration
		last         *time.Time
		unrestricted bool
		want         bool
	}{
		{nil, tp(0), false, true},
		{dur(10 * time.Second), nil, false, true},
		{dur(10 * time.Second), tp(9 * time.Second), false, false},
		{dur(10 * time.Second), tp(10 * time.Second), false, true},
		{dur(10 * time.Second), tp(11 * time.Second), false, true},
		{dur(10 * time.Second), tp(1 * time.Second), true, true},
	}
	for i, c := range cases {
		if got := CheckMinInterval(c.min, now, c.last, c.unrestricted); got != c.want {
			t.Errorf("case %d: got %v", i, got)
		}
	}
}

// TestIsLastMinutes ports TestIsLastMinutes of CMS.
func TestIsLastMinutes(t *testing.T) {
	ts := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	min := func(m int) *time.Duration { return dur(time.Duration(m) * time.Minute) }
	end := func(perUser *time.Duration, start *time.Time, delay, extra time.Duration) time.Time {
		e, ok := EndTime(ts, perUser, start, delay, extra)
		if !ok {
			t.Fatal("no end")
		}
		return e
	}
	noUser := end(nil, nil, 0, 0)
	// Unconfigured grace period.
	if IsLastMinutes(ts, noUser, nil, false) {
		t.Error("unconfigured")
	}
	if !IsLastMinutes(ts.Add(-15*time.Minute), noUser, min(15), false) {
		t.Error("no per-user time, last minutes")
	}
	if IsLastMinutes(ts.Add(-15*time.Minute), noUser, min(10), false) {
		t.Error("no per-user time, not last minutes")
	}
	started := ts.Add(-5 * time.Hour)
	perUser := end(dur(5*time.Hour), &started, 0, 0)
	if !IsLastMinutes(ts.Add(-15*time.Minute), perUser, min(15), false) {
		t.Error("per-user time, last minutes")
	}
	if IsLastMinutes(ts.Add(-15*time.Minute), perUser, min(10), false) {
		t.Error("per-user time, not last minutes")
	}
	if IsLastMinutes(ts.Add(-15*time.Minute), end(nil, nil, 0, time.Second), min(15), false) {
		t.Error("extra time ignored")
	}
	if IsLastMinutes(ts.Add(-15*time.Minute), end(nil, nil, time.Second, 0), min(15), false) {
		t.Error("delay ignored")
	}
	if IsLastMinutes(ts, noUser, min(15), true) {
		t.Error("unrestricted")
	}
	if _, ok := EndTime(ts, dur(time.Hour), nil, 0, 0); ok {
		t.Error("a USACO-like participant who did not start has no end")
	}
}

func TestCheckSubmission(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	recent := now.Add(-5 * time.Second)
	cases := []struct {
		name                     string
		r                        Rules
		st                       Stats
		overrideMax, overrideMin bool
		subject, text            string
		formatted                string
	}{
		{name: "no limits", st: Stats{TaskCount: 100, ContestCount: 1000, TaskLast: &recent, ContestLast: &recent}},
		{name: "contest max", r: Rules{ContestMaxNumber: i32(3), TaskMaxNumber: i32(1)}, st: Stats{ContestCount: 3, TaskCount: 1},
			subject: MsgTooManySubmissions, text: MsgMaxSubmissionsContest,
			formatted: "You have reached the maximum limit of at most 3 submissions among all tasks."},
		{name: "task max", r: Rules{ContestMaxNumber: i32(3), TaskMaxNumber: i32(1)}, st: Stats{ContestCount: 2, TaskCount: 1},
			subject: MsgTooManySubmissions, text: MsgMaxSubmissionsTask,
			formatted: "You have reached the maximum limit of at most 1 submissions on this task."},
		{name: "max overridden", r: Rules{ContestMaxNumber: i32(3)}, st: Stats{ContestCount: 3}, overrideMax: true},
		{name: "unrestricted", r: Rules{ContestMaxNumber: i32(3), ContestMinInterval: dur(time.Minute), Unrestricted: true},
			st: Stats{ContestCount: 3, ContestLast: &recent}},
		{name: "contest interval", r: Rules{ContestMinInterval: dur(30 * time.Second), TaskMinInterval: dur(time.Minute)},
			st: Stats{ContestLast: &recent, TaskLast: &recent}, subject: MsgSubmissionsTooFrequent, text: MsgMinIntervalContest,
			formatted: "Among all tasks, you can submit again after 30 seconds from last submission."},
		{name: "task interval", r: Rules{ContestMinInterval: dur(2 * time.Second), TaskMinInterval: dur(1500 * time.Millisecond * 10)},
			st: Stats{ContestLast: &recent, TaskLast: &recent}, subject: MsgSubmissionsTooFrequent, text: MsgMinIntervalTask,
			formatted: "For this task, you can submit again after 15 seconds from last submission."},
		{name: "interval in the last minutes", r: Rules{ContestMinInterval: dur(time.Minute), LastMinutes: true},
			st: Stats{ContestLast: &recent}},
		{name: "interval overridden", r: Rules{TaskMinInterval: dur(time.Minute)}, st: Stats{TaskLast: &recent}, overrideMin: true},
	}
	for _, c := range cases {
		err := CheckSubmission(c.r, c.st, now, c.overrideMax, c.overrideMin)
		if c.subject == "" {
			if err != nil {
				t.Errorf("%s: %v", c.name, err)
			}
			continue
		}
		if err == nil || err.Subject != c.subject || err.Text != c.text || err.FormattedText() != c.formatted {
			t.Errorf("%s: got %v", c.name, err)
		}
	}
}

func TestCheckUserTest(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	recent := now.Add(-5 * time.Second)
	// The grace period does not apply to user tests.
	r := Rules{ContestMinInterval: dur(time.Minute), LastMinutes: true}
	if err := CheckUserTest(r, Stats{ContestLast: &recent}, now); err == nil || err.Subject != MsgTestsTooFrequent ||
		err.FormattedText() != "Among all tasks, you can test again after 60 seconds from last test." {
		t.Errorf("got %v", err)
	}
	if err := CheckUserTest(Rules{TaskMaxNumber: i32(2)}, Stats{TaskCount: 2}, now); err == nil || err.Text != MsgMaxTestsTask {
		t.Errorf("got %v", err)
	}
	if err := CheckUserTest(Rules{ContestMaxNumber: i32(2)}, Stats{ContestCount: 2}, now); err == nil || err.Text != MsgMaxTestsContest {
		t.Errorf("got %v", err)
	}
	if err := CheckUserTest(Rules{TaskMinInterval: dur(time.Minute)}, Stats{TaskLast: &recent}, now); err == nil || err.Text != MsgTestMinIntervalTask {
		t.Errorf("got %v", err)
	}
	if err := CheckUserTest(Rules{}, Stats{TaskCount: 9}, now); err != nil {
		t.Errorf("got %v", err)
	}
}

func TestPyPercent(t *testing.T) {
	for _, c := range []struct {
		msg  string
		args []any
		want string
	}{
		{"at most %d bytes", []any{int64(100000)}, "at most 100000 bytes"},
		{"after %d seconds", []any{12.9}, "after 12 seconds"},
		{"%s and %d%%", []any{"x", int32(3)}, "x and 3%"},
		{"no args %d", nil, "no args %d"},
	} {
		if got := pyPercent(c.msg, c.args...); got != c.want {
			t.Errorf("pyPercent(%q) = %q, want %q", c.msg, got, c.want)
		}
	}
}
