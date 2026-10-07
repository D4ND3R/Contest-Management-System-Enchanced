// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package checker

import (
	_ "embed"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/steps"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
)

// TestlibHeader is the testlib.h CMS ships for Polygon checkers
// (cmscontrib/loaders/polygon/testlib.h: testlib 0.9.21 by Mike
// Mirzayanov with the testlib-cms patch, whose notices it keeps).
// Compiled with -DCMS, a testlib checker takes the arguments in the CMS
// order and writes the standard manager output (KindComparator); without
// it, it is a plain testlib checker (KindTestlib). Upstream's Polygon
// loader compiles check.cpp with it: g++ -x c++ -O2 -static -DCMS.
//
//go:embed testlib/testlib.h
var TestlibHeader []byte

// The exit codes of plain testlib checkers.
const (
	TestlibOK            = 0
	TestlibWrongAnswer   = 1
	TestlibPresentation  = 2
	TestlibFail          = 3
	TestlibDirt          = 4 // old testlib: wrong output format
	TestlibPoints        = 7
	TestlibUnexpectedEOF = 8
)

// TestlibOutcome interprets the result of a plain testlib checker (run as
// `checker input output answer`): exit code 0 is correct; 1 (wrong
// answer), 2 (presentation error), 4 (wrong output format) and 8
// (unexpected end of file) are wrong; 7 gives the points written after
// "points" on stderr (quitp) as the outcome, like the testlib-cms patch;
// 3 (FAIL: the checker itself failed) and any other code are errors. The
// texts are the stock messages, like the patch does without
// CMS_VERBOSE_FEEDBACK; the message of the checker becomes the admin text.
func TestlibOutcome(exitCode int, stderr []byte) (*Outcome, error) {
	msg := pyStrip(firstLine(stderr))
	if !utf8.ValidString(msg) {
		msg = strings.ToValidUTF8(msg, "�")
	}
	msg = strings.ReplaceAll(job.TruncateUTF8(msg, maxMessage), "\x00", `\x00`)
	o := &Outcome{AdminText: msg}
	switch exitCode {
	case TestlibOK:
		o.Outcome, o.Text = 1, stock(steps.EvaluationSuccess)
	case TestlibWrongAnswer, TestlibPresentation, TestlibDirt, TestlibUnexpectedEOF:
		o.Outcome, o.Text = 0, stock(steps.EvaluationWrong)
	case TestlibPoints:
		f := strings.Fields(msg)
		if len(f) < 2 || f[0] != "points" {
			return nil, fmt.Errorf("testlib checker exited with the points code but wrote %q", msg)
		}
		v, err := strconv.ParseFloat(f[1], 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, fmt.Errorf("testlib checker: invalid points %q", f[1])
		}
		o.Outcome, o.Text = v, stock(steps.EvaluationPartial)
	case TestlibFail:
		return nil, fmt.Errorf("testlib checker failed: %s", msg)
	default:
		return nil, fmt.Errorf("testlib checker: unexpected exit code %d: %s", exitCode, msg)
	}
	return o, nil
}

func stock(key string) []string { return []string{steps.EvaluationMessages.Get(key).Message} }

func firstLine(b []byte) string {
	s, _, _ := strings.Cut(normalizeNewlines(b), "\n")
	return s
}
