// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/grading/tasktypes/Batch.py of CMS:
// Copyright © 2010-2015 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2012-2014 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2017 Myungwoo Chun <mc.tamaki@gmail.com>

package tasktype

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/checker"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/steps"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
)

// Default input and output files of Batch when the program uses the
// standard streams (the files the worker redirects them to).
const (
	batchDefaultInput  = "input.txt"
	batchDefaultOutput = "output.txt"
)

// batchParams are the parameters of Batch (upstream ACCEPTED_PARAMETERS):
// ["alone"|"grader", [input file, output file], output evaluation]. Empty
// file names mean the standard input and output.
func batchParams() []Param {
	return []Param{
		compilationParam(),
		{
			Kind: ParamCollection, Name: "I/O (blank for stdin/stdout)", ShortName: "io",
			Params: []Param{
				{Kind: ParamString, Name: "Input file", ShortName: "inputfile"},
				{Kind: ParamString, Name: "Output file", ShortName: "outputfile"},
			},
		},
		outputEvalParam(),
	}
}

// batch is the Batch task type: one standalone program, compiled alone or
// with a grader, reading the input from the standard input or a file and
// writing the output to the standard output or a file, checked with a
// white diff or a comparator ("checker" manager, run as `checker input
// correct_output user_output`).
type batch struct {
	grader                        bool
	inputFilename, outputFilename string
	// actualInput and actualOutput are the files of the box holding the
	// input and the output, whether redirected or not.
	actualInput, actualOutput string
	outputEval                string
	checker                   checker.Checker
}

func newBatch(values []json.RawMessage) (TaskType, error) {
	return parseBatch(values)
}

func parseBatch(values []json.RawMessage) (*batch, error) {
	io := decodeStrings(values[1])
	t := &batch{
		grader:         decodeString(values[0]) == compilationGrader,
		inputFilename:  io[0],
		outputFilename: io[1],
		outputEval:     decodeString(values[2]),
	}
	for _, f := range io {
		if f != "" && !validFilename(f) {
			return nil, fmt.Errorf("invalid I/O file name %q: a file name without directories", f)
		}
	}
	t.checker = checkerFor(t.outputEval)
	t.actualInput, t.actualOutput = t.inputFilename, t.outputFilename
	if t.actualInput == "" {
		t.actualInput = batchDefaultInput
	}
	if t.actualOutput == "" {
		t.actualOutput = batchDefaultOutput
	}
	return t, nil
}

// validFilename accepts a plain file name of the box.
func validFilename(s string) bool {
	return s != "." && s != ".." && len(s) <= 255 && !strings.ContainsAny(s, "/\x00")
}

func (t *batch) Name() string                  { return NameBatch }
func (t *batch) DisplayName() string           { return "Batch" }
func (t *batch) Testable() bool                { return true }
func (t *batch) AllowPartialSubmission() bool  { return false }
func (t *batch) ReusePreviousSubmission() bool { return true }
func (t *batch) AutoManagers() []string        { return []string{} }

// UserManagers: with a grader, contestants provide their own grader
// (usually a simplified one given by the admins) with user tests.
func (t *batch) UserManagers() []string {
	if t.grader {
		return []string{graderBasename + ".%l"}
	}
	return []string{}
}

func (t *batch) Managers() []string {
	var m []string
	if t.grader {
		m = append(m, graderBasename+".%l")
	}
	if t.checker.Kind.IsProgram() {
		m = append(m, checkerCodename)
	}
	return m
}

func (t *batch) Boxes() int {
	if t.checker.Kind.IsProgram() {
		return 2 // the program's box holds the output while the checker runs
	}
	return 1
}

func (t *batch) CompilationCommands(langs *language.Registry, submissionFormat []string) map[string][][]string {
	var codenames []string
	if t.grader {
		codenames = append(codenames, graderBasename+".%l")
	}
	// For Batch every codename ends with ".%l"; BatchAndOutput also has
	// output files.
	for _, c := range submissionFormat {
		if strings.HasSuffix(c, ".%l") {
			codenames = append(codenames, c)
		}
	}
	return compilationCommands(langs, codenames, func(l *language.Language) string {
		return executableName(submissionFormat, l, true)
	})
}

// compilationCommands returns the commands of every language compiling
// codenames into the executable named by exe.
func compilationCommands(langs *language.Registry, codenames []string, exe func(*language.Language) string) map[string][][]string {
	res := map[string][][]string{}
	for _, l := range langs.All() {
		sources := make([]string, len(codenames))
		for i, c := range codenames {
			sources[i] = sourceFilename(c, l)
		}
		cmds, err := l.CompilationCommands(sources, exe(l), true)
		if err == nil {
			res[l.Name] = cmds
		}
	}
	return res
}

func (t *batch) Compile(ctx context.Context, env *Env, j *job.Job, res *job.Result) error {
	r := newRun(ctx, env, j, res)
	if err := r.checkFiles(1, true); err != nil {
		return err
	}
	return r.compileSubmission(t.grader, true)
}

func (t *batch) Evaluate(ctx context.Context, env *Env, j *job.Job, res *job.Result) error {
	r := newRun(ctx, env, j, res)
	if _, _, err := r.executable(); err != nil {
		return err
	}
	box, out, err := t.execute(r)
	if box != nil {
		defer r.closeBox(box)
	}
	if err != nil || out == nil {
		return err
	}
	return t.check(r, *out, nil)
}

// execute runs the program on the input (upstream _execution_step). It
// returns the box (to close) and, when the output has to be checked,
// where it is; otherwise the outcome is already set.
func (t *batch) execute(r *run) (Box, *userOutput, error) {
	exe, digest, err := r.executable()
	if err != nil {
		return nil, nil, err
	}
	lang, err := r.language()
	if err != nil {
		return nil, nil, err
	}
	main := "" // the executable without extension
	if t.grader {
		main = graderBasename
	}
	ev := steps.Evaluation{
		Commands:     lang.EvaluationCommands(exe, main, nil),
		Language:     lang,
		TimeLimit:    r.j.TimeLimit,
		MemoryLimit:  r.j.MemoryLimit,
		Multiprocess: r.j.MultithreadedSandbox,
	}
	// Redirections, or the output file the program may write.
	if t.inputFilename == "" {
		ev.Stdin = t.actualInput
	}
	if t.outputFilename == "" {
		ev.Stdout = t.actualOutput
	} else {
		ev.Writable = []string{t.actualOutput}
	}

	box, err := r.newBox("evaluate")
	if err != nil {
		return nil, nil, err
	}
	if err := r.put(box, exe, digest, true); err != nil {
		return box, nil, err
	}
	if err := r.put(box, t.actualInput, r.j.Input, false); err != nil {
		return box, nil, err
	}
	st, err := r.env.Steps.Evaluate(r.ctx, box, ev)
	if err != nil {
		return box, nil, err
	}
	r.res.Stats = st

	switch {
	case !steps.EvaluationSucceeded(st):
		// The contestant's error.
		r.setOutcome(0, steps.EvaluationText(st), "")
	case !box.Exists(t.actualOutput):
		r.setOutcome(0, []string{steps.EvaluationMessages.Get(steps.EvaluationNoOutput).Message, t.actualOutput}, "")
	default:
		if err := r.storeUserOutput(box, t.actualOutput); err != nil {
			return box, nil, err
		}
		if r.j.OnlyExecution {
			r.setOutcome(0, []string{steps.ExecutionCompleted}, "")
			break
		}
		return box, &userOutput{box: box, name: t.actualOutput, filename: t.outputFilename}, nil
	}
	return box, nil, nil
}

// check checks the output and sets the outcome (upstream _evaluate_step).
func (t *batch) check(r *run, out userOutput, extraArgs []string) error {
	o, err := r.checkOutput(t.checker, out, extraArgs)
	if err != nil {
		return err
	}
	r.setOutcome(o.Outcome, o.Text, o.AdminText)
	return nil
}
