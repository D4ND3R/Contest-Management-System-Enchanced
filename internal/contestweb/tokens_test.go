// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cmstestsuite/unit_tests/server/contest/tokening_test.py of
// CMS:
// Copyright © 2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>

package contestweb

import (
	"fmt"
	"math"
	"testing"
	"time"
)

// tokenTest is the fixture of TestTokensAvailable of CMS: a contest
// started at base, a task (1) and another task (2).
type tokenTest struct {
	t       *testing.T
	base    time.Time
	contest TokenRules
	task    TokenRules
	start   time.Time
	played  []TokenPlayed
}

func newTokenTest(t *testing.T) *tokenTest {
	base := time.Date(2026, 3, 1, 10, 0, 0, 123456000, time.UTC)
	return &tokenTest{t: t, base: base, start: base,
		contest: TokenRules{Mode: TokenModeInfinite}, task: TokenRules{Mode: TokenModeInfinite}}
}

func (tt *tokenTest) at(s float64) time.Time {
	return tt.base.Add(time.Duration(s * float64(time.Second)))
}

func (tt *tokenTest) atp(s float64) *time.Time { t := tt.at(s); return &t }

func (tt *tokenTest) call(s float64) TokensInfo {
	return TokensAvailable(tt.contest, tt.task, 1, tt.start, tt.played, tt.at(s))
}

func (tt *tokenTest) tokenToContest(s float64) {
	tt.played = append(tt.played, TokenPlayed{tt.at(s), 2})
}

func (tt *tokenTest) tokenToTask(s float64) {
	tt.played = append(tt.played, TokenPlayed{tt.at(s), 1})
}

func finite(initial, number int32, interval float64, max *int32) TokenRules {
	return TokenRules{Mode: TokenModeFinite, GenInitial: initial, GenNumber: number,
		GenInterval: time.Duration(interval * float64(time.Second)), GenMax: max}
}

func constrain(r *TokenRules, maxNumber *int32, minInterval float64) {
	r.MaxNumber = maxNumber
	r.MinInterval = time.Duration(minInterval * float64(time.Second))
}

func i32p(v int32) *int32 { return &v }

func (tt *tokenTest) want(s float64, avail float64, gen, exp *time.Time) {
	tt.t.Helper()
	got := tt.call(s)
	str := func(p *time.Time) string {
		if p == nil {
			return "None"
		}
		return p.Sub(tt.base).String()
	}
	if got.Available != avail || str(got.NextGen) != str(gen) || str(got.Expiration) != str(exp) {
		tt.t.Errorf("at %v: got (%v, %s, %s), want (%v, %s, %s)", s, got.Available, str(got.NextGen),
			str(got.Expiration), avail, str(gen), str(exp))
	}
}

func TestTokensInfinite(t *testing.T) {
	tt := newTokenTest(t)
	tt.want(10, -1, nil, nil)
}

func TestTokensDisabled(t *testing.T) {
	tt := newTokenTest(t)
	tt.task.Mode = TokenModeDisabled
	tt.want(10, 0, nil, nil)
	tt.contest.Mode, tt.task.Mode = TokenModeDisabled, TokenModeInfinite
	tt.want(10, 0, nil, nil)
}

func TestTokensFiniteOnContest(t *testing.T) {
	tt := newTokenTest(t)
	tt.contest = finite(3, 2, 4, i32p(6))
	tt.want(3, 3, tt.atp(4), nil)
	tt.want(6, 5, tt.atp(8), nil)
	tt.want(10, 6, nil, nil)
	tt.tokenToContest(9)
	tt.want(10, 5, tt.atp(12), nil)
}

func TestTokensFiniteOnTask(t *testing.T) {
	tt := newTokenTest(t)
	tt.task = finite(3, 2, 4, i32p(6))
	tt.want(3, 3, tt.atp(4), nil)
	tt.want(6, 5, tt.atp(8), nil)
	tt.want(10, 6, nil, nil)
	tt.tokenToTask(9)
	tt.want(10, 5, tt.atp(12), nil)
	// Tokens on other tasks have no effect.
	tt.tokenToContest(9)
	tt.want(10, 5, tt.atp(12), nil)
}

func TestTokensFiniteOnBoth(t *testing.T) {
	tt := newTokenTest(t)
	tt.contest = finite(1, 2, 5, nil)
	tt.task = finite(2, 1, 4, i32p(5))
	tt.want(3, 1, tt.atp(5), nil)
	tt.want(7, 3, tt.atp(10), nil)
	tt.want(11, 4, tt.atp(12), nil)
	tt.want(13, 5, nil, nil)
	tt.want(16, 5, nil, nil)
	tt.want(101, 5, nil, nil)
	tt.tokenToTask(102)
	tt.want(103, 4, tt.atp(104), nil)
	tt.want(105, 5, nil, nil)
}

func TestTokensConstraints(t *testing.T) {
	tt := newTokenTest(t)
	tt.contest = finite(3, 1, 2, i32p(6))
	tt.task = finite(1, 2, 2, nil)
	constrain(&tt.contest, i32p(3), 2)
	constrain(&tt.task, nil, 4)
	tt.tokenToContest(2)
	tt.want(3, 2, nil, tt.atp(4))
	tt.tokenToTask(1)
	tt.want(3, 1, nil, tt.atp(5))
	tt.tokenToContest(6)
	tt.want(7, 0, nil, nil)
}

func TestTokensConstraintsHaveNoEffectIfInfinite(t *testing.T) {
	tt := newTokenTest(t)
	tt.tokenToTask(5)
	constrain(&tt.contest, i32p(1), 10)
	tt.want(10, -1, nil, nil)
	constrain(&tt.contest, nil, 0)
	constrain(&tt.task, i32p(1), 10)
	tt.want(10, -1, nil, nil)
	constrain(&tt.contest, i32p(1), 10)
	tt.want(10, -1, nil, nil)
}

func TestTokensUsacoLike(t *testing.T) {
	tt := newTokenTest(t)
	tt.start = tt.at(1000) // the participant's starting time
	tt.contest = finite(2, 1, 10, i32p(10))
	tt.task = finite(10, 0, 1, nil)
	tt.want(1000, 2, tt.atp(1010), nil)
	tt.want(1011, 3, tt.atp(1020), nil)
	tt.want(1081, 10, nil, nil)
	tt.tokenToTask(1050)
	tt.want(1051, 6, tt.atp(1060), nil)
	tt.want(1081, 9, nil, nil)
}

// TestTokensFutureIgnored: tokens after now do not count (CMS filters
// Token.timestamp <= timestamp).
func TestTokensFutureIgnored(t *testing.T) {
	tt := newTokenTest(t)
	tt.task = finite(3, 0, 4, nil)
	tt.tokenToTask(20)
	tt.want(10, 3, nil, nil)
	tt.want(30, 2, nil, nil)
}

// TestTokensPythonTypes checks the number CMS prints: a Python float
// after generation ("3.0"), an int when capped.
func TestTokensPythonTypes(t *testing.T) {
	tt := newTokenTest(t)
	tt.contest = finite(2, 1, 30, i32p(5))
	constrain(&tt.contest, i32p(10), 0)
	for _, c := range []struct {
		at   float64
		want string
	}{{31, "3.0"}, {10, "2.0"}, {1000, "5"}} {
		if got := tt.call(c.at).String(); got != c.want {
			t.Errorf("at %v: %s, want %s", c.at, got, c.want)
		}
	}
	tt.contest = finite(2, 0, 30, nil)
	if got := tt.call(10).String(); got != "2.0" {
		t.Errorf("no generation: %s", got)
	}
	tt.contest = finite(2, 1, 30, nil)
	constrain(&tt.contest, i32p(3), 0)
	tt.tokenToContest(5)
	if got := tt.call(100).String(); got != "2" {
		t.Errorf("max number: %s", got)
	}
	tt = newTokenTest(t)
	if got := tt.call(1).String(); got != "-1" {
		t.Errorf("infinite: %s", got)
	}
}

func TestPyFloorDiv(t *testing.T) {
	for _, c := range []struct{ a, b, want float64 }{
		{7, 2, 3}, {-7, 2, -4}, {7, -2, -4}, {0, 3, 0}, {6, 3, 2}, {0.3, 0.1, 2}, {1, 0.1, 9}, {-0.5, 30, -1},
	} {
		if got := pyFloorDiv(c.a, c.b); got != c.want {
			t.Errorf("%v // %v = %v, want %v", c.a, c.b, got, c.want)
		}
	}
	if s := fmt.Sprintln(pyFloatRepr(3), pyFloatRepr(2.5), pyFloatRepr(1e16), pyFloatRepr(math.Copysign(0, -1)), pyFloatRepr(math.Inf(1))); s != "3.0 2.5 1e+16 -0.0 inf\n" {
		t.Errorf("repr: %s", s)
	}
}

func TestCheckToken(t *testing.T) {
	now := time.Now()
	if err := checkToken(TokensInfo{Available: -1}, false); err != nil {
		t.Error(err)
	}
	if err := checkToken(TokensInfo{Available: 1}, false); err != nil {
		t.Error(err)
	}
	if err := checkToken(TokensInfo{Available: 0}, false); err != errTokenUnacceptable {
		t.Error(err)
	}
	if err := checkToken(TokensInfo{Available: 1, Expiration: &now}, false); err != errTokenUnacceptable {
		t.Error(err)
	}
	if err := checkToken(TokensInfo{Available: -1}, true); err != errTokenAlreadyPlayed {
		t.Error(err)
	}
}
