// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// RunProgram is ported from cms/grading/steps/trusted.py (checker_step)
// and Check from cms/grading/tasktypes/util.py (eval_output) of CMS:
// Copyright © 2010-2015 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2013 Bernard Blackham <bernard@largestprime.net>
// Copyright © 2013-2014 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2014 Fabian Gundlach <320pointsguy@gmail.com>
// Copyright © 2016 Myungwoo Chun <mc.tamaki@gmail.com>
// Copyright © 2016 Amir Keivan Mohtashami <akmohtashami97@gmail.com>

// Package checker decides whether the output of a contestant's program is
// correct: the white diff of CMS, exact and real-number comparisons, and
// checker programs written by the admins, either with the CMS protocol
// (upstream "comparator", also testlib checkers compiled with the
// testlib-cms patch) or plain testlib checkers (exit codes).
//
// The outcome is a number (usually 0 to 1) and texts: one for the
// contestant (a msgid of the "cms" catalogs, translated when shown) and
// one for the admins.
package checker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/steps"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
)

// Kind is a way of checking an output.
type Kind string

// The kinds of checkers.
const (
	// KindDiff is the white diff of CMS (upstream evaluation "diff").
	KindDiff Kind = "diff"
	// KindExact compares byte by byte.
	KindExact Kind = "exact"
	// KindReal compares tokens, numbers within a tolerance.
	KindReal Kind = "real"
	// KindComparator runs the admins' checker with the CMS protocol
	// (upstream evaluation "comparator"): `checker input
	// correct_output user_output [extra...]`, outcome on stdout, text on
	// stderr (ParseManagerOutput).
	KindComparator Kind = "comparator"
	// KindTestlib runs a plain testlib checker: `checker input
	// user_output correct_output`, verdict in the exit code
	// (TestlibOutcome).
	KindTestlib Kind = "testlib"
)

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool {
	switch k {
	case KindDiff, KindExact, KindReal, KindComparator, KindTestlib:
		return true
	}
	return false
}

// IsProgram reports whether k runs a checker program.
func (k Kind) IsProgram() bool { return k == KindComparator || k == KindTestlib }

// Checker is a configured way of checking.
type Checker struct {
	Kind Kind
	// AbsTolerance and RelTolerance are those of KindReal.
	AbsTolerance, RelTolerance float64
}

// Validate checks the configuration.
func (c Checker) Validate() error {
	if !c.Kind.Valid() {
		return fmt.Errorf("checker: unknown kind %q", c.Kind)
	}
	if c.Kind == KindReal {
		return checkTolerance(c.AbsTolerance, c.RelTolerance)
	}
	return nil
}

// Outcome is the result of a check.
type Outcome struct {
	// Outcome is the fraction of the points (usually 0 to 1).
	Outcome float64
	// Text is for the contestant: a msgid and its arguments.
	Text []string
	// AdminText is for the admins (where the output differs, the
	// checker's message).
	AdminText string
}

// Names of the files RunProgram puts in the box (upstream
// CHECKER_INPUT_FILENAME, CHECKER_CORRECT_OUTPUT_FILENAME,
// CHECKER_FILENAME).
const (
	InputFilename         = "input.txt"
	CorrectOutputFilename = "correct_output.txt"
	ProgramFilename       = "checker"
)

// Files are the inputs of a check.
type Files struct {
	// UserOutput is the file of the box with the contestant's output.
	UserOutput string
	// OutputFilename is the name the contestant had to write, for the
	// message when there is no output ("" for the standard output).
	OutputFilename string
	// CorrectOutput is the expected output (every kind).
	CorrectOutput io.Reader
	// Input is the input of the testcase and Program the checker
	// executable (the manager "checker"): checker programs only.
	Input, Program io.Reader
	// ExtraArgs are appended to the arguments of a KindComparator
	// checker (BatchAndOutput passes "batch" or "outputonly").
	ExtraArgs []string
}

// Check checks the contestant's output (upstream eval_output). A missing
// output (or a symbolic link in its place) gets the outcome 0 and
// "Evaluation didn't produce file %s". Comparisons run in the worker;
// checker programs run in the box with the trusted limits (RunProgram):
// the box should hold nothing but the user output (upstream copies it
// into a new sandbox), since the program writes its files there.
//
// The error is non-nil when the check could not be carried out (sandbox
// failure, broken checker program): retry or fix, never a verdict.
func (c Checker) Check(ctx context.Context, cfg *steps.Config, box steps.Box, f Files) (*Outcome, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	st, err := box.Stat(f.UserOutput)
	if errors.Is(err, fs.ErrNotExist) || err == nil && !st.Mode().IsRegular() {
		return &Outcome{Text: []string{steps.EvaluationMessages.Get(steps.EvaluationNoOutput).Message, f.OutputFilename}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("checker: %w", err)
	}
	if c.Kind.IsProgram() {
		return RunProgram(ctx, cfg, box, c.Kind, f)
	}
	if f.CorrectOutput == nil {
		return nil, errors.New("checker: no correct output")
	}
	out, err := box.Open(f.UserOutput)
	if err != nil {
		return nil, fmt.Errorf("checker: %w", err)
	}
	defer out.Close()
	return c.Compare(out, f.CorrectOutput)
}

// Compare runs a comparison (KindDiff, KindExact, KindReal): outcome 1
// and "Output is correct", or 0 and "Output isn't correct" with the
// difference in the admin text (upstream white_diff_fobj_step).
func (c Checker) Compare(output, correct io.Reader) (*Outcome, error) {
	var (
		equal bool
		admin string
		err   error
	)
	switch c.Kind {
	case KindDiff:
		equal, admin, err = WhiteDiff(output, correct)
	case KindExact:
		equal, admin, err = Exact(output, correct)
	case KindReal:
		equal, admin, err = RealNumbers(output, correct, c.AbsTolerance, c.RelTolerance)
	default:
		return nil, fmt.Errorf("checker: %q is not a comparison", c.Kind)
	}
	if err != nil {
		return nil, fmt.Errorf("checker: reading the outputs: %w", err)
	}
	if equal {
		return &Outcome{Outcome: 1, Text: stock(steps.EvaluationSuccess)}, nil
	}
	return &Outcome{Outcome: 0, Text: stock(steps.EvaluationWrong), AdminText: admin}, nil
}

// maxCheckerOutput bounds what is read of the outputs of a checker
// program.
const maxCheckerOutput = 1 << 20

// RunProgram runs a checker program (upstream checker_step): it puts the
// program, the input and the correct output in the box (which must not
// have them yet), runs it with the trusted limits and reads its verdict:
// the standard manager output for KindComparator, the exit code for
// KindTestlib. The user output must already be in the box.
//
// The error is non-nil when the checker could not run or broke its
// protocol (the admins must fix it), or the sandbox failed.
func RunProgram(ctx context.Context, cfg *steps.Config, box steps.Box, kind Kind, f Files) (*Outcome, error) {
	if !kind.IsProgram() {
		return nil, fmt.Errorf("checker: %q is not a checker program", kind)
	}
	for _, name := range []string{InputFilename, CorrectOutputFilename, ProgramFilename} {
		if box.Exists(name) {
			return nil, fmt.Errorf("checker: file %s already in the sandbox for the checker", name)
		}
	}
	if f.Program == nil {
		return nil, errors.New("checker: configuration error: missing checker in task managers")
	}
	if f.Input == nil || f.CorrectOutput == nil {
		return nil, errors.New("checker: the input and the correct output are needed")
	}
	if err := box.CreateFileFrom(ProgramFilename, f.Program, true); err != nil {
		return nil, fmt.Errorf("checker: %w", err)
	}
	if err := box.CreateFileFrom(InputFilename, f.Input, false); err != nil {
		return nil, fmt.Errorf("checker: %w", err)
	}
	if err := box.CreateFileFrom(CorrectOutputFilename, f.CorrectOutput, false); err != nil {
		return nil, fmt.Errorf("checker: %w", err)
	}
	cmd := []string{"./" + ProgramFilename, InputFilename, CorrectOutputFilename, f.UserOutput}
	if kind == KindTestlib {
		// testlib's order; a fourth argument would be its result file.
		cmd = []string{"./" + ProgramFilename, InputFilename, f.UserOutput, CorrectOutputFilename}
	} else {
		cmd = append(cmd, f.ExtraArgs...)
	}
	res, err := cfg.RunTrusted(ctx, box, [][]string{cmd})
	if err != nil {
		return nil, fmt.Errorf("checker: %w", err)
	}
	stderr, _, err := box.ReadFile(res.Stderr, maxCheckerOutput)
	if err != nil {
		return nil, fmt.Errorf("checker: missing stderr: %w", err)
	}
	if kind == KindTestlib {
		if !res.OK && res.Stats.ExitStatus != job.ExitNonzeroReturn {
			return nil, fmt.Errorf("checker: the testlib checker ended with %s (signal %d)", res.Stats.ExitStatus, res.Stats.Signal)
		}
		return TestlibOutcome(res.ExitCode, stderr)
	}
	if !res.OK {
		return nil, fmt.Errorf("checker: the checker ended with %s (signal %d): %s",
			res.Stats.ExitStatus, res.Stats.Signal, pyStrip(firstLine(stderr)))
	}
	stdout, _, err := box.ReadFile(res.Stdout, maxCheckerOutput)
	if err != nil {
		return nil, fmt.Errorf("checker: missing stdout: %w", err)
	}
	o, err := ParseManagerOutput(stdout, stderr)
	if err != nil {
		return nil, fmt.Errorf("checker: invalid output from checker: %w", err)
	}
	return o, nil
}
