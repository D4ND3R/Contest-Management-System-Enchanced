// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/grading/tasktypes/BatchAndOutput.py of CMS:
// Copyright © 2010-2015 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2012-2014 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2017 Myungwoo Chun <mc.tamaki@gmail.com>

package tasktype

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
)

// UserOutputFilename is the file a contestant submits with the output of
// a testcase in OutputOnly and BatchAndOutput tasks (upstream
// USER_OUTPUT_FILENAME_TEMPLATE, "output_%s.txt"): the submission format
// of those tasks lists one per testcase.
func UserOutputFilename(codename string) string { return "output_" + codename + ".txt" }

// batchAndOutputParams are the parameters of Batch plus the
// comma-separated codenames of the testcases that must be submitted as
// outputs.
func batchAndOutputParams() []Param {
	return append(batchParams(), Param{
		Kind: ParamString, Name: "Comma-separated list of output only testcases",
		ShortName: "output_only_testcases",
	})
}

// batchAndOutput is the BatchAndOutput task type, a Batch task where the
// output of a testcase may also be submitted (output_<codename>.txt). The
// testcases listed in the last parameter are output only; the others use
// the submitted output if there is one and the program otherwise. A
// comparator gets a fourth argument: "outputonly" for the output-only
// testcases, "batch" for the others.
type batchAndOutput struct {
	*batch
	outputOnly map[string]bool
}

func newBatchAndOutput(values []json.RawMessage) (TaskType, error) {
	b, err := parseBatch(values[:3])
	if err != nil {
		return nil, err
	}
	t := &batchAndOutput{batch: b, outputOnly: map[string]bool{}}
	// Like upstream, the codenames are not trimmed. Empty items (an empty
	// parameter, "a,,b") name no testcase; upstream keeps them in its set
	// but no codename is empty, and user tests (codename "" here, None
	// upstream) must not match them.
	for _, c := range strings.Split(decodeString(values[3]), ",") {
		if c != "" {
			t.outputOnly[c] = true
		}
	}
	return t, nil
}

func (t *batchAndOutput) Name() string                  { return NameBatchAndOutput }
func (t *batchAndOutput) DisplayName() string           { return "BatchAndOutput" }
func (t *batchAndOutput) AllowPartialSubmission() bool  { return true }
func (t *batchAndOutput) ReusePreviousSubmission() bool { return false }

func (t *batchAndOutput) Compile(ctx context.Context, env *Env, j *job.Job, res *job.Result) error {
	r := newRun(ctx, env, j, res)
	sources := 0
	for c := range j.Files {
		if strings.HasSuffix(c, ".%l") {
			sources++
		}
	}
	if sources == 0 {
		// Only outputs were submitted.
		r.noCompilation()
		return nil
	}
	return r.compileSubmission(t.grader, true)
}

func (t *batchAndOutput) Evaluate(ctx context.Context, env *Env, j *job.Job, res *job.Result) error {
	r := newRun(ctx, env, j, res)
	codename := j.Operation.TestcaseCodename
	var out *userOutput
	ran := false
	if d, ok := j.Files[UserOutputFilename(codename)]; ok {
		out = &userOutput{digest: d}
	} else if !t.outputOnly[codename] && len(j.Executables) > 0 {
		box, o, err := t.execute(r)
		if box != nil {
			defer r.closeBox(box)
		}
		if err != nil {
			return err
		}
		out, ran = o, true
	}
	if out == nil && !ran {
		r.setOutcome(0, []string{textFileNotSubmitted}, "")
		r.res.Stats = nil
		return nil
	}
	if out == nil {
		return nil // the outcome of the execution is set
	}
	extra := "batch"
	if t.outputOnly[codename] {
		extra = "outputonly"
	}
	return t.check(r, *out, []string{extra})
}
