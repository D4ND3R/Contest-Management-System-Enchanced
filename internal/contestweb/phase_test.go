// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// The cases are those of cmstestsuite/unit_tests/server/contest/
// phase_management_test.py of CMS:
// Copyright © 2010-2013 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2015 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2012-2017 Luca Wehrstedt <luca.wehrstedt@gmail.com>

package contestweb

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// phaseCase is a call of upstream's test(): the arguments of
// compute_actual_phase as "HH[:MM[:SS]]" ("" for None) and the partition
// of the time they induce: times at even positions, the phase between
// them at odd ones; before the first time the phase is -2, after the last
// one +4.
type phaseCase struct {
	contestStart, contestStop, analysisStart, analysisStop string
	perUserTime, startingTime, delay, extra                string
	intervals                                              []any
}

var phaseCases = []phaseCase{
	{"4", "12", "", "", "", "", "0", "0", []any{"4", 0, "12"}},
	{"4", "12", "", "", "", "", "0", "2", []any{"4", 0, "14"}},
	{"4", "12", "", "", "", "", "2", "0", []any{"4", -1, "6", 0, "14"}},
	{"4", "12", "", "", "", "", "2", "2", []any{"4", -1, "6", 0, "16"}},
	{"4", "8", "", "", "", "", "5", "0", []any{"4", -1, "9", 0, "13"}},
	{"4", "12", "", "", "", "7", "0", "0", []any{"4", 0, "12"}},
	{"4", "12", "", "", "", "7", "0", "2", []any{"4", 0, "14"}},
	{"4", "12", "", "", "", "7", "2", "0", []any{"4", -1, "6", 0, "14"}},
	{"4", "12", "", "", "", "7", "2", "2", []any{"4", -1, "6", 0, "16"}},
	{"4", "8", "", "", "", "7", "5", "0", []any{"4", -1, "9", 0, "13"}},
	{"4", "12", "17", "20", "", "", "0", "0", []any{"4", 0, "12", 2, "17", 3, "20"}},
	{"4", "12", "17", "20", "", "", "0", "2", []any{"4", 0, "14", 2, "17", 3, "20"}},
	{"4", "12", "17", "20", "", "", "2", "0", []any{"4", -1, "6", 0, "14", 2, "17", 3, "20"}},
	{"4", "12", "17", "20", "", "", "2", "2", []any{"4", -1, "6", 0, "16", 2, "17", 3, "20"}},
	{"4", "8", "17", "20", "", "", "5", "0", []any{"4", -1, "9", 0, "13", 2, "17", 3, "20"}},
	{"4", "12", "17", "20", "", "7", "0", "0", []any{"4", 0, "12", 2, "17", 3, "20"}},
	{"4", "12", "17", "20", "", "7", "0", "2", []any{"4", 0, "14", 2, "17", 3, "20"}},
	{"4", "12", "17", "20", "", "7", "2", "0", []any{"4", -1, "6", 0, "14", 2, "17", 3, "20"}},
	{"4", "12", "17", "20", "", "7", "2", "2", []any{"4", -1, "6", 0, "16", 2, "17", 3, "20"}},
	{"4", "8", "17", "20", "", "7", "5", "0", []any{"4", -1, "9", 0, "13", 2, "17", 3, "20"}},
	{"4", "12", "12", "20", "", "", "2", "0", []any{"4", -1, "6", 0, "14", 3, "20"}},
	{"4", "12", "12", "20", "", "", "0", "2", []any{"4", 0, "14", 3, "20"}},
	{"4", "12", "12", "20", "", "", "1", "1", []any{"4", -1, "5", 0, "14", 3, "20"}},
	{"4", "8", "8", "12", "", "", "0", "5", []any{"4", 0, "13"}},
	{"4", "8", "8", "12", "", "", "5", "0", []any{"4", -1, "9", 0, "13"}},
	{"4", "8", "8", "12", "", "", "9", "0", []any{"4", -1, "13", 0, "17"}},
	{"4", "8", "8", "16", "", "", "5", "1", []any{"4", -1, "9", 0, "14", 3, "16"}},
	{"6", "18", "", "", "6", "9", "0", "0", []any{"6", -1, "9", 0, "15", 1, "18"}},
	{"6", "18", "", "", "6", "3", "0", "0", []any{"6", 0, "9", 1, "18"}},
	{"6", "18", "", "", "6", "15", "0", "0", []any{"6", -1, "15", 0, "18"}},
	{"6", "18", "", "", "6", "9", "0", "1", []any{"6", -1, "9", 0, "16", 1, "18"}},
	{"6", "18", "", "", "6", "3", "0", "1", []any{"6", 0, "10", 1, "18"}},
	{"6", "18", "", "", "6", "15", "0", "1", []any{"6", -1, "15", 0, "19"}},
	{"6", "18", "", "", "6", "9", "1", "0", []any{"6", -1, "10", 0, "16", 1, "18"}},
	{"6", "18", "", "", "6", "3", "1", "0", []any{"6", -1, "7", 0, "10", 1, "18"}},
	{"6", "18", "", "", "6", "15", "1", "0", []any{"6", -1, "16", 0, "19"}},
	{"6", "18", "", "", "6", "9", "1", "1", []any{"6", -1, "10", 0, "17", 1, "18"}},
	{"6", "18", "", "", "6", "3", "1", "1", []any{"6", -1, "7", 0, "11", 1, "18"}},
	{"6", "18", "", "", "6", "15", "1", "1", []any{"6", -1, "16", 0, "20"}},
	{"6", "18", "", "", "6", "", "0", "0", []any{"6", -1, "18"}},
	{"6", "18", "", "", "6", "", "0", "1", []any{"6", -1, "18"}},
	{"6", "18", "", "", "6", "", "1", "0", []any{"6", -1, "18"}},
	{"6", "18", "", "", "6", "", "1", "1", []any{"6", -1, "18"}},
	{"6", "18", "", "", "3", "2", "0", "0", []any{"6", 0, "6", 1, "18"}},
	{"6", "18", "", "", "3", "2", "0", "1", []any{"6", 0, "7", 1, "18"}},
	{"6", "18", "", "", "3", "2", "1", "0", []any{"6", -1, "7", 0, "7", 1, "18"}},
	{"6", "18", "", "", "3", "2", "1", "1", []any{"6", -1, "7", 0, "8", 1, "18"}},
	{"6", "18", "", "", "3", "19", "0", "0", []any{"6", -1, "18", 0, "18"}},
	{"6", "18", "", "", "3", "19", "0", "1", []any{"6", -1, "18", 0, "19"}},
	{"6", "18", "", "", "3", "19", "1", "0", []any{"6", -1, "19", 0, "19"}},
	{"6", "18", "", "", "3", "19", "1", "1", []any{"6", -1, "19", 0, "20"}},
	{"6", "18", "21", "23", "6", "9", "0", "0", []any{"6", -1, "9", 0, "15", 1, "18", 2, "21", 3, "23"}},
	{"6", "18", "21", "23", "6", "3", "0", "0", []any{"6", 0, "9", 1, "18", 2, "21", 3, "23"}},
	{"6", "18", "21", "23", "6", "15", "0", "0", []any{"6", -1, "15", 0, "18", 2, "21", 3, "23"}},
	{"6", "18", "21", "23", "6", "9", "0", "1", []any{"6", -1, "9", 0, "16", 1, "18", 2, "21", 3, "23"}},
	{"6", "18", "21", "23", "6", "3", "0", "1", []any{"6", 0, "10", 1, "18", 2, "21", 3, "23"}},
	{"6", "18", "21", "23", "6", "15", "0", "1", []any{"6", -1, "15", 0, "19", 2, "21", 3, "23"}},
	{"6", "18", "21", "23", "6", "9", "1", "0", []any{"6", -1, "10", 0, "16", 1, "18", 2, "21", 3, "23"}},
	{"6", "18", "21", "23", "6", "3", "1", "0", []any{"6", -1, "7", 0, "10", 1, "18", 2, "21", 3, "23"}},
	{"6", "18", "21", "23", "6", "15", "1", "0", []any{"6", -1, "16", 0, "19", 2, "21", 3, "23"}},
	{"6", "18", "21", "23", "6", "9", "1", "1", []any{"6", -1, "10", 0, "17", 1, "18", 2, "21", 3, "23"}},
	{"6", "18", "21", "23", "6", "3", "1", "1", []any{"6", -1, "7", 0, "11", 1, "18", 2, "21", 3, "23"}},
	{"6", "18", "21", "23", "6", "15", "1", "1", []any{"6", -1, "16", 0, "20", 2, "21", 3, "23"}},
	{"6", "18", "21", "23", "6", "", "0", "0", []any{"6", -1, "18", 2, "21", 3, "23"}},
	{"6", "18", "21", "23", "6", "", "0", "1", []any{"6", -1, "18", 2, "21", 3, "23"}},
	{"6", "18", "21", "23", "6", "", "1", "0", []any{"6", -1, "18", 2, "21", 3, "23"}},
	{"6", "18", "21", "23", "6", "", "1", "1", []any{"6", -1, "18", 2, "21", 3, "23"}},
	{"6", "18", "21", "23", "3", "2", "0", "0", []any{"6", 0, "6", 1, "18", 2, "21", 3, "23"}},
	{"6", "18", "21", "23", "3", "2", "0", "1", []any{"6", 0, "7", 1, "18", 2, "21", 3, "23"}},
	{"6", "18", "21", "23", "3", "2", "1", "0", []any{"6", -1, "7", 0, "7", 1, "18", 2, "21", 3, "23"}},
	{"6", "18", "21", "23", "3", "2", "1", "1", []any{"6", -1, "7", 0, "8", 1, "18", 2, "21", 3, "23"}},
	{"6", "18", "21", "23", "3", "19", "0", "0", []any{"6", -1, "18", 0, "18", 2, "21", 3, "23"}},
	{"6", "18", "21", "23", "3", "19", "0", "1", []any{"6", -1, "18", 0, "19", 2, "21", 3, "23"}},
	{"6", "18", "21", "23", "3", "19", "1", "0", []any{"6", -1, "19", 0, "19", 2, "21", 3, "23"}},
	{"6", "18", "21", "23", "3", "19", "1", "1", []any{"6", -1, "19", 0, "20", 2, "21", 3, "23"}},
}

// phaseTime is upstream's parse_datetime: a time of 2000-01-01.
func phaseTime(t *testing.T, s string) *time.Time {
	t.Helper()
	if s == "" {
		return nil
	}
	d := phaseDuration(t, s)
	v := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC).Add(*d)
	return &v
}

// phaseDuration is upstream's parse_timedelta.
func phaseDuration(t *testing.T, s string) *time.Duration {
	t.Helper()
	if s == "" {
		return nil
	}
	var d time.Duration
	for i, part := range strings.Split(s, ":") {
		n, err := strconv.Atoi(part)
		if err != nil {
			t.Fatal(err)
		}
		d += time.Duration(n) * []time.Duration{time.Hour, time.Minute, time.Second}[i]
	}
	return &d
}

// phaseSteps are upstream's TEST_STEPS: distances from the boundaries of
// every interval at which the phase is checked.
var phaseSteps = []time.Duration{
	time.Second, 5 * time.Second, 15 * time.Second, 30 * time.Second,
	time.Minute, 5 * time.Minute, 15 * time.Minute, 30 * time.Minute,
	time.Hour, 3 * time.Hour, 6 * time.Hour, 12 * time.Hour, 24 * time.Hour,
}

func fmtTime(t *time.Time) string {
	if t == nil {
		return "None"
	}
	return t.Format("15:04:05")
}

func (a ActualPhase) String() string {
	return fmt.Sprintf("(%d, %s, %s, %s, %s)", a.Phase, fmtTime(a.Begin), fmtTime(a.End),
		fmtTime(a.ValidBegin), fmtTime(a.ValidEnd))
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

// TestComputeActualPhase runs the cases of CMS's phase_management_test.py
// (test_traditional and test_usaco_like) exactly as upstream checks them.
func TestComputeActualPhase(t *testing.T) {
	t.Parallel()
	for i, c := range phaseCases {
		cs, ce := phaseTime(t, c.contestStart), phaseTime(t, c.contestStop)
		as, ae := phaseTime(t, c.analysisStart), phaseTime(t, c.analysisStop)
		put, st := phaseDuration(t, c.perUserTime), phaseTime(t, c.startingTime)
		delay, extra := *phaseDuration(t, c.delay), *phaseDuration(t, c.extra)

		type interval struct {
			phase      int
			begin, end *time.Time
		}
		iv := c.intervals
		var parts []interval
		var validBegin, validEnd *time.Time
		parts = append(parts, interval{-2, nil, phaseTime(t, iv[0].(string))})
		for j := 1; j < len(iv); j += 2 {
			p := iv[j].(int)
			b, e := phaseTime(t, iv[j-1].(string)), phaseTime(t, iv[j+1].(string))
			parts = append(parts, interval{p, b, e})
			if p == 0 {
				validBegin, validEnd = b, e
			}
		}
		parts = append(parts, interval{4, phaseTime(t, iv[len(iv)-1].(string)), nil})

		check := func(at time.Time, want interval) {
			t.Helper()
			got := ComputeActualPhase(at, *cs, *ce, as, ae, put, st, delay, extra)
			w := ActualPhase{Phase: want.phase, Begin: want.begin, End: want.end, ValidBegin: validBegin, ValidEnd: validEnd}
			if got.Phase != w.Phase || !sameTime(got.Begin, w.Begin) || !sameTime(got.End, w.End) ||
				!sameTime(got.ValidBegin, w.ValidBegin) || !sameTime(got.ValidEnd, w.ValidEnd) {
				t.Errorf("case %d (%+v) at %s: got %s, want %s", i, c, at.Format("15:04:05"), got, w)
			}
		}
		for _, p := range parts {
			for _, step := range phaseSteps {
				switch {
				case p.begin == nil:
					check(p.end.Add(-step), p)
				case p.end == nil:
					check(p.begin.Add(step), p)
				default:
					if p.begin.Add(step).After(p.end.Add(-step)) {
						break
					}
					check(p.begin.Add(step), p)
					check(p.end.Add(-step), p)
				}
			}
		}
	}
}

// TestGroupPhase checks CMS Group.phase at and around the boundaries.
func TestGroupPhase(t *testing.T) {
	t.Parallel()
	at := func(h int) time.Time { return time.Date(2000, 1, 1, h, 0, 0, 0, time.UTC) }
	g := q.Group{Start: at(4), Stop: at(8), AnalysisEnabled: true, AnalysisStart: at(10), AnalysisStop: at(12)}
	for _, c := range []struct {
		t    time.Time
		want int
	}{
		{at(3), -1}, {at(4).Add(-time.Microsecond), -1}, {at(4), 0}, {at(8), 0},
		{at(8).Add(time.Microsecond), 1}, {at(10), 2}, {at(12), 2}, {at(12).Add(time.Microsecond), 3},
	} {
		if got := GroupPhase(c.t, &g); got != c.want {
			t.Errorf("analysis enabled, %s: %d, want %d", c.t.Format("15:04:05.000000"), got, c.want)
		}
	}
	g.AnalysisEnabled = false
	if got := GroupPhase(at(9), &g); got != 3 {
		t.Errorf("analysis disabled, after the stop: %d, want 3", got)
	}
}
