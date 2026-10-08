// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/grading/tasktypes/OutputOnly.py of CMS:
// Copyright © 2010-2012 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2012-2013 Luca Wehrstedt <luca.wehrstedt@gmail.com>

package tasktype

import (
	"context"
	"encoding/json"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/checker"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
)

// outputOnlyParams: [output evaluation].
func outputOnlyParams() []Param { return []Param{outputEvalParam()} }

// outputOnly is the OutputOnly task type: the submission is one output
// file per testcase (UserOutputFilename), checked with a white diff or a
// comparator; files may be missing (partial submissions).
type outputOnly struct {
	checker checker.Checker
}

func newOutputOnly(values []json.RawMessage) (TaskType, error) {
	return &outputOnly{checker: checkerFor(decodeString(values[0]))}, nil
}

func (t *outputOnly) Name() string                  { return NameOutputOnly }
func (t *outputOnly) DisplayName() string           { return "Output only" }
func (t *outputOnly) Testable() bool                { return false }
func (t *outputOnly) AllowPartialSubmission() bool  { return true }
func (t *outputOnly) ReusePreviousSubmission() bool { return true }
func (t *outputOnly) UserManagers() []string        { return []string{} }
func (t *outputOnly) AutoManagers() []string        { return []string{} }

func (t *outputOnly) Managers() []string {
	if t.checker.Kind.IsProgram() {
		return []string{checkerCodename}
	}
	return nil
}

func (t *outputOnly) Boxes() int {
	if t.checker.Kind.IsProgram() {
		return 1
	}
	return 0
}

// CompilationCommands is nil: nothing is compiled.
func (t *outputOnly) CompilationCommands(*language.Registry, []string) map[string][][]string {
	return nil
}

func (t *outputOnly) Compile(ctx context.Context, env *Env, j *job.Job, res *job.Result) error {
	newRun(ctx, env, j, res).noCompilation()
	return nil
}

func (t *outputOnly) Evaluate(ctx context.Context, env *Env, j *job.Job, res *job.Result) error {
	r := newRun(ctx, env, j, res)
	d, ok := j.Files[UserOutputFilename(j.Operation.TestcaseCodename)]
	if !ok {
		// Partial submissions are allowed: a missing file scores 0.
		r.setOutcome(0, []string{textFileNotSubmitted}, "")
		return nil
	}
	o, err := r.checkOutput(t.checker, userOutput{digest: d}, nil)
	if err != nil {
		return err
	}
	r.setOutcome(o.Outcome, o.Text, o.AdminText)
	// Nothing ran: no statistics.
	r.res.Stats = nil
	return nil
}
