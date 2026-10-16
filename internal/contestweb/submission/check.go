// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/server/contest/submission/check.py of CMS:
// Copyright © 2010-2014 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2012-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>

package submission

import "time"

// The checks of CMS on how many submissions (or user tests) a contestant
// sent and when. CMS queries the database in each check; here the site
// reads the counts and the last times once (Stats) and the checks are
// pure.

// Stats are the submissions (or user tests) a participant already sent:
// how many and the last time, on the task and on every task of the
// contest (CMS get_submission_count, get_latest_submission). A nil time
// means none.
type Stats struct {
	TaskCount, ContestCount int64
	TaskLast, ContestLast   *time.Time
}

// CheckMaxNumber reports whether a participant who sent count
// submissions may send another one with the limit max (nil: no limit;
// CMS check_max_number). Unrestricted participants always may.
func CheckMaxNumber(max *int32, count int64, unrestricted bool) bool {
	if max == nil || unrestricted {
		return true
	}
	return count < int64(*max)
}

// CheckMinInterval reports whether at least min (nil: no limit) passed
// since the last submission, at now (CMS check_min_interval).
// Unrestricted participants and participants with no submissions always
// may submit.
func CheckMinInterval(min *time.Duration, now time.Time, last *time.Time, unrestricted bool) bool {
	if min == nil || unrestricted || last == nil {
		return true
	}
	return now.Sub(*last) >= *min
}

// IsLastMinutes reports whether a participant whose time ends at end is
// in the grace period at the end of the contest, when the minimum
// interval between submissions does not apply (CMS is_last_minutes):
// at most grace (nil: no grace period) before end. end is the stop of
// the participant's group (or their starting time plus the group's
// per_user_time) plus their delay and extra time (EndTime).
// Unrestricted participants are never in it.
func IsLastMinutes(now, end time.Time, grace *time.Duration, unrestricted bool) bool {
	if unrestricted || grace == nil {
		return false
	}
	return end.Sub(now) <= *grace
}

// EndTime is the end of a participant's time as is_last_minutes
// computes it: the stop of the group, or for USACO-like contests
// (perUserTime not nil) the starting time plus the per-user time, then
// plus the delay and the extra time. A USACO-like participant who has
// not started has no end (ok is false).
func EndTime(groupStop time.Time, perUserTime *time.Duration, startingTime *time.Time, delay, extra time.Duration) (end time.Time, ok bool) {
	end = groupStop
	if perUserTime != nil {
		if startingTime == nil {
			return time.Time{}, false
		}
		end = startingTime.Add(*perUserTime)
	}
	return end.Add(delay).Add(extra), true
}
