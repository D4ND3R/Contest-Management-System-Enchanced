// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/server/contest/tokening.py of CMS:
// Copyright © 2010-2014 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2012-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2014 Fabian Gundlach <320pointsguy@gmail.com>

package contestweb

import (
	"math"
	"strconv"
	"time"
)

// TokenPlayed is a token the participant played: when, and on a
// submission to which task.
type TokenPlayed struct {
	Timestamp time.Time
	TaskID    int32
}

// TokensInfo is what CMS's tokens_available returns: how many tokens the
// participant can play on a task (Available; -1 means infinite), when the
// next one is generated (NextGen, nil: never) and the first time they
// may play one (Expiration, nil: now, or never if there are none and
// none will be generated).
type TokensInfo struct {
	Available  float64
	NextGen    *time.Time
	Expiration *time.Time
	// availFloat: CMS computes Available as a Python float in most cases
	// and the page prints it as such ("3.0").
	availFloat bool
}

// String is Available as CMS prints it: Python's str of an int or of a
// float ("5", "3.0").
func (t TokensInfo) String() string { return pyNumber{t.Available, t.availFloat}.String() }

// CanPlay reports whether the participant has a token to play, maybe
// after waiting (CMS: tokens_info[0] > 0 or tokens_info[0] == -1).
func (t TokensInfo) CanPlay() bool { return t.Available > 0 || t.Available == -1 }

// pyNumber is a number of tokens with its Python type: CMS's arithmetic
// mixes ints and floats and the pages show the difference.
type pyNumber struct {
	v       float64
	isFloat bool
}

func (n pyNumber) String() string {
	if !n.isFloat {
		return strconv.FormatInt(int64(n.v), 10)
	}
	return pyFloatRepr(n.v)
}

// pyFloatRepr is Python's repr of a float.
func pyFloatRepr(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	case math.IsNaN(f):
		return "nan"
	}
	abs := math.Abs(f)
	if abs != 0 && (abs < 1e-4 || abs >= 1e16) {
		s := strconv.FormatFloat(f, 'e', -1, 64)
		// Python writes at least two exponent digits: 1e+16.
		return s
	}
	s := strconv.FormatFloat(f, 'f', -1, 64)
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			return s
		}
	}
	return s + ".0"
}

// pyFloorDiv is Python's float floor division (float_floor_div in
// CPython): it differs from math.Floor(a/b) when the quotient rounds up
// to an integer.
func pyFloorDiv(vx, wx float64) float64 {
	mod := math.Mod(vx, wx)
	div := (vx - mod) / wx
	if mod != 0 {
		if (wx < 0) != (mod < 0) {
			div -= 1.0
		}
	}
	if div != 0 {
		floordiv := math.Floor(div)
		if div-floordiv > 0.5 {
			floordiv += 1.0
		}
		return floordiv
	}
	return math.Copysign(0, vx/wx)
}

// totalSeconds is Python's timedelta.total_seconds() (microseconds over
// 10**6, correctly rounded).
func totalSeconds(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1e6
}

// tokensAvailableOne is CMS's _tokens_available: the tokens of one set
// of rules (the contest's or the task's) given the tokens played under
// them (history, oldest first, none after now) and the time tokens start
// being generated.
func tokensAvailableOne(r TokenRules, start time.Time, history []time.Time, now time.Time) (pyNumber, *time.Time, *time.Time) {
	switch r.Mode {
	case TokenModeDisabled:
		return pyNumber{0, false}, nil, nil
	case TokenModeInfinite:
		return pyNumber{-1, false}, nil, nil
	}
	interval := totalSeconds(r.GenInterval)
	// How many tokens are generated in [begin, end).
	generate := func(begin, end time.Time) float64 {
		before := pyFloorDiv(totalSeconds(begin.Sub(start)), interval)
		upTo := pyFloorDiv(totalSeconds(end.Sub(start)), interval)
		return float64(r.GenNumber) * (upTo - before)
	}
	// min(avail, gen_max) as Python: the first argument unless the
	// second is smaller (then an int).
	capMax := func(n pyNumber) pyNumber {
		if r.GenMax != nil && float64(*r.GenMax) < n.v {
			return pyNumber{float64(*r.GenMax), false}
		}
		return n
	}
	avail := pyNumber{float64(r.GenInitial), false}
	prev := start
	for _, next := range history {
		avail = pyNumber{avail.v + generate(prev, next), true}
		avail = capMax(avail)
		avail.v--
		prev = next
	}
	avail = pyNumber{avail.v + generate(prev, now), true}
	avail = capMax(avail)

	var nextGen *time.Time
	if r.GenNumber > 0 && (r.GenMax == nil || avail.v < float64(*r.GenMax)) {
		periods := pyFloorDiv(totalSeconds(now.Sub(start)), interval)
		t := start.Add(r.GenInterval * time.Duration(periods+1))
		nextGen = &t
	}
	if r.MaxNumber != nil && avail.v >= float64(int(*r.MaxNumber)-len(history)) {
		avail = pyNumber{float64(int(*r.MaxNumber) - len(history)), false}
		nextGen = nil
	}
	expiration := start
	if len(history) > 0 {
		expiration = history[len(history)-1].Add(r.MinInterval)
	}
	if !expiration.After(now) || avail.v == 0 && nextGen == nil {
		return avail, nextGen, nil
	}
	return avail, nextGen, &expiration
}

// TokensAvailable is CMS's tokens_available: the tokens a participant
// may play on the task taskID at now, combining the rules of the contest
// and of the task. played are the participant's tokens on every task
// (any order; those after now are ignored); start is when tokens start
// being generated: the participant's starting time in USACO-like
// contests, the start of their group otherwise.
func TokensAvailable(contest, task TokenRules, taskID int32, start time.Time, played []TokenPlayed, now time.Time) TokensInfo {
	var contestHistory, taskHistory []time.Time
	for _, p := range sortedTokens(played) {
		if p.Timestamp.After(now) {
			continue
		}
		contestHistory = append(contestHistory, p.Timestamp)
		if p.TaskID == taskID {
			taskHistory = append(taskHistory, p.Timestamp)
		}
	}
	cAvail, cGen, cExp := tokensAvailableOne(contest, start, contestHistory, now)
	tAvail, tGen, tExp := tokensAvailableOne(task, start, taskHistory, now)

	expiration := tExp
	switch {
	case cExp == nil:
	case tExp == nil:
		expiration = cExp
	case cExp.After(*tExp):
		expiration = cExp
	}

	if cAvail.v == -1 && tAvail.v == -1 {
		return TokensInfo{Available: -1, Expiration: expiration}
	}
	// Infinite tokens of one kind are as one more than the other kind.
	if cAvail.v == -1 {
		cAvail = pyNumber{tAvail.v + 1, tAvail.isFloat}
	}
	if tAvail.v == -1 {
		tAvail = pyNumber{cAvail.v + 1, cAvail.isFloat}
	}
	res := TokensInfo{Expiration: expiration}
	switch {
	case cAvail.v < tAvail.v:
		res.Available, res.availFloat, res.NextGen = cAvail.v, cAvail.isFloat, cGen
	case tAvail.v < cAvail.v:
		res.Available, res.availFloat, res.NextGen = tAvail.v, tAvail.isFloat, tGen
	default:
		res.Available, res.availFloat = tAvail.v, tAvail.isFloat
		if cGen != nil && tGen != nil {
			g := *cGen
			if tGen.After(g) {
				g = *tGen
			}
			res.NextGen = &g
		}
	}
	return res
}

// sortedTokens returns the tokens oldest first (stable for equal times).
func sortedTokens(played []TokenPlayed) []TokenPlayed {
	out := make([]TokenPlayed, len(played))
	copy(out, played)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Timestamp.Before(out[j-1].Timestamp); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// tokenStart is when the participant starts accumulating tokens (CMS
// tokens_available): their starting time in USACO-like contests, the
// start of their group otherwise (also when they have not started yet).
func tokenStart(p *Participation) time.Time {
	if p.Group.PerUserTime != nil && p.StartingTime != nil {
		return *p.StartingTime
	}
	return p.Group.Start
}

// The messages of the tokens (CMS tokening.py and UseTokenHandler).
const (
	msgTokenDiscarded     = "Token request discarded"
	msgNoTokens           = "Your request has been discarded because you have no tokens available."
	msgTokenAlreadyPlayed = "Your request has been discarded because you already used a token on that submission."
	msgTokenReceived      = "Token request received"
	msgTokenReceivedText  = "Your request has been received and applied to the submission."
	errTokenUnacceptable  = tokenError("unacceptable")
	errTokenAlreadyPlayed = tokenError("already played")
)

// tokenError is why a token request is refused (CMS UnacceptableToken,
// TokenAlreadyPlayed).
type tokenError string

func (e tokenError) Error() string { return "token: " + string(e) }

// checkToken is CMS's accept_token without the insertion: the token is
// refused if no token is available now (or one must wait), then if the
// submission already has one.
func checkToken(info TokensInfo, alreadyPlayed bool) error {
	if info.Available == 0 || info.Expiration != nil {
		return errTokenUnacceptable
	}
	if alreadyPlayed {
		return errTokenAlreadyPlayed
	}
	return nil
}
