// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package tasktype

import (
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/checker"
)

// Values of the "output_eval" parameter of Batch, BatchAndOutput,
// OutputOnly and TwoSteps. "diff" and "comparator" are those of CMS; the
// others are extensions of this fork (R3.2), which CMS does not accept.
const (
	// OutputEvalDiff compares with the white diff of CMS.
	OutputEvalDiff = "diff"
	// OutputEvalComparator runs the "checker" manager with the CMS
	// protocol (also testlib checkers compiled with the testlib-cms
	// patch).
	OutputEvalComparator = "comparator"
	// OutputEvalExact compares byte by byte.
	OutputEvalExact = "exact"
	// OutputEvalReal compares token by token, numbers within
	// RealTolerance (absolute or relative).
	OutputEvalReal = "real"
	// OutputEvalTestlib runs the "checker" manager as a plain testlib
	// checker (verdict in the exit code).
	OutputEvalTestlib = "testlib"
)

// RealTolerance is the absolute and relative tolerance of
// OutputEvalReal, that of testlib's standard checker rcmp6.
const RealTolerance = 1e-6

// outputEvalParam is the "output_eval" parameter (upstream _EVALUATION).
func outputEvalParam() Param {
	return Param{
		Kind: ParamChoice, Name: "Output evaluation", ShortName: "output_eval",
		Choices: []Choice{
			{OutputEvalDiff, "Outputs compared with white diff"},
			{OutputEvalComparator, "Outputs are compared by a comparator"},
			{OutputEvalExact, "Outputs compared byte by byte"},
			{OutputEvalReal, "Outputs compared token by token, numbers with a tolerance of 1e-6"},
			{OutputEvalTestlib, "Outputs are checked by a testlib checker"},
		},
	}
}

// checkerFor returns the checker of an "output_eval" value.
func checkerFor(outputEval string) checker.Checker {
	switch outputEval {
	case OutputEvalComparator:
		return checker.Checker{Kind: checker.KindComparator}
	case OutputEvalExact:
		return checker.Checker{Kind: checker.KindExact}
	case OutputEvalReal:
		return checker.Checker{Kind: checker.KindReal, AbsTolerance: RealTolerance, RelTolerance: RealTolerance}
	case OutputEvalTestlib:
		return checker.Checker{Kind: checker.KindTestlib}
	}
	return checker.Checker{Kind: checker.KindDiff}
}

// compilationParam is the "compilation" parameter of Batch,
// Communication and Interactive.
func compilationParam() Param {
	return Param{
		Kind: ParamChoice, Name: "Compilation", ShortName: "compilation",
		Choices: []Choice{
			{compilationAlone, "Submissions are self-sufficient"},
			{compilationGrader, "Submissions are compiled with a grader"},
		},
	}
}

// Values of the "compilation" parameter.
const (
	compilationAlone  = "alone"
	compilationGrader = "grader"
)
