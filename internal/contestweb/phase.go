// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/server/contest/phase_management.py and Group.phase
// (cms/db/user.py) of CMS:
// Copyright © 2010-2014 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2012-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>

package contestweb

import (
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// The actual phases of a participant (CMS compute_actual_phase).
const (
	// PhaseBeforeContest: the contest has not started yet.
	PhaseBeforeContest = -2
	// PhaseBeforeWindow: the contest started but the participant's time
	// frame did not (USACO-like contests waiting for "Start!", or a
	// delayed start).
	PhaseBeforeWindow = -1
	// PhaseCompeting: the participant can compete.
	PhaseCompeting = 0
	// PhaseAfterWindow: the participant's time frame ended but the
	// contest did not.
	PhaseAfterWindow = 1
	// PhaseAfterContest: the contest ended and the analysis mode has not
	// started.
	PhaseAfterContest = 2
	// PhaseAnalysis: the analysis mode is running.
	PhaseAnalysis = 3
	// PhaseOver: the contest ended, and the analysis mode too or it is
	// disabled.
	PhaseOver = 4
)

// ActualPhase is the phase of the contest for one participant and the
// intervals around it (CMS compute_actual_phase's result).
type ActualPhase struct {
	// Phase is one of the Phase… constants.
	Phase int
	// Begin and End bound the current phase; nil means minus and plus
	// infinity.
	Begin, End *time.Time
	// ValidBegin and ValidEnd bound phase 0 (the time frame in which the
	// participant competes), nil if it is not known yet (USACO-like
	// contest not started).
	ValidBegin, ValidEnd *time.Time
}

func tp(t time.Time) *time.Time { return &t }

// ComputeActualPhase returns the actual phase of a participant at now
// and its boundaries, exactly as CMS compute_actual_phase: contestStart
// and contestStop are the times of the participant's group, analysisStart
// and analysisStop are nil when the analysis mode is disabled,
// perUserTime is nil for traditional contests, startingTime is nil while
// a USACO-like participant has not started.
func ComputeActualPhase(now, contestStart, contestStop time.Time,
	analysisStart, analysisStop *time.Time, perUserTime *time.Duration,
	startingTime *time.Time, delay, extra time.Duration) ActualPhase {
	var res ActualPhase
	var actualStop *time.Time

	if perUserTime != nil && startingTime == nil {
		// "USACO-like" contest, but we still don't know when the user
		// started/will start.
		switch {
		case !now.Before(contestStart) && !now.After(contestStop):
			res.Phase = PhaseBeforeWindow
			res.Begin, res.End = tp(contestStart), tp(contestStop)
		case now.Before(contestStart):
			res.Phase = PhaseBeforeContest
			res.End = tp(contestStart)
		default:
			res.Phase = PhaseAfterContest
			res.Begin = tp(contestStop)
		}
	} else {
		var intendedStart, intendedStop time.Time
		if perUserTime == nil {
			// "Traditional" contest.
			intendedStart, intendedStop = contestStart, contestStop
		} else {
			// "USACO-like" contest, and we already know when the user
			// started/will start. Both values are bounded to the contest,
			// in case the starting time was set before its start or
			// after its stop.
			intendedStart = clamp(*startingTime, contestStart, contestStop)
			intendedStop = clamp(startingTime.Add(*perUserTime), contestStart, contestStop)
		}
		actualStart := intendedStart.Add(delay)
		stop := intendedStop.Add(delay).Add(extra)
		actualStop = &stop
		res.ValidBegin, res.ValidEnd = tp(actualStart), tp(stop)

		switch {
		case !now.Before(actualStart) && !now.After(stop):
			res.Phase = PhaseCompeting
			res.Begin, res.End = tp(actualStart), tp(stop)
		case !now.Before(contestStart) && now.Before(actualStart):
			// This also includes a corner case: the user's start is
			// known but in the future (set by an admin, or delayed).
			res.Phase = PhaseBeforeWindow
			res.Begin, res.End = tp(contestStart), tp(actualStart)
		case now.Before(contestStart):
			res.Phase = PhaseBeforeContest
			res.End = tp(contestStart)
		case now.After(stop) && !now.After(contestStop):
			res.Phase = PhaseAfterWindow
			res.Begin, res.End = tp(stop), tp(contestStop)
		default: // contestStop < now
			res.Phase = PhaseAfterContest
			res.Begin = tp(latest(contestStop, stop))
		}
	}

	if res.Phase == PhaseAfterContest {
		if analysisStart != nil && analysisStop != nil {
			switch {
			case now.Before(*analysisStart):
				res.End = tp(*analysisStart)
			case !now.After(*analysisStop):
				// actual_stop may come after analysis_start with extra
				// time or delay time.
				begin := *analysisStart
				if actualStop != nil {
					begin = latest(begin, *actualStop)
				}
				res.Phase = PhaseAnalysis
				res.Begin, res.End = tp(begin), tp(*analysisStop)
			default:
				begin := *analysisStop
				if actualStop != nil {
					begin = latest(begin, *actualStop)
				}
				res.Phase = PhaseOver
				res.Begin, res.End = tp(begin), nil
			}
		} else {
			res.Phase = PhaseOver
		}
	}
	return res
}

// GroupPhase is CMS Group.phase: -1 before the start, 0 during the
// contest, 1 after it before the analysis mode, 2 during the analysis
// mode, 3 when everything is over.
func GroupPhase(now time.Time, g *q.Group) int {
	switch {
	case now.Before(g.Start):
		return -1
	case !now.After(g.Stop):
		return 0
	}
	if g.AnalysisEnabled {
		if now.Before(g.AnalysisStart) {
			return 1
		} else if !now.After(g.AnalysisStop) {
			return 2
		}
	}
	return 3
}

// ParticipationPhase computes the actual phase of a participation in its
// group (CMS ContestHandler.render_params).
func ParticipationPhase(now time.Time, g *q.Group, p *q.Participation) ActualPhase {
	var as, ae *time.Time
	if g.AnalysisEnabled {
		as, ae = tp(g.AnalysisStart), tp(g.AnalysisStop)
	}
	return ComputeActualPhase(now, g.Start, g.Stop, as, ae, g.PerUserTime,
		p.StartingTime, p.DelayTime, p.ExtraTime)
}

func clamp(t, lo, hi time.Time) time.Time {
	if t.Before(lo) {
		t = lo
	}
	if t.After(hi) {
		t = hi
	}
	return t
}

func latest(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
