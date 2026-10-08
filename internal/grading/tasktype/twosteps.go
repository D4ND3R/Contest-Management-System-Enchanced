// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/grading/tasktypes/TwoSteps.py of CMS:
// Copyright © 2010-2012 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2012-2013 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2016 Petar Veličković <pv273@cam.ac.uk>

package tasktype

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/checker"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/steps"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/sandbox"
)

// Files of a TwoSteps evaluation.
const (
	twoStepsManager = "manager"
	twoStepsInput   = "input.txt"
	twoStepsOutput  = "output.txt"
	// twoStepsFirstStdout is where the standard output of the first
	// step goes (upstream's default redirection).
	twoStepsFirstStdout = "stdout.txt"
)

// twoStepsParams: [output evaluation].
func twoStepsParams() []Param { return []Param{outputEvalParam()} }

// twoSteps is the TwoSteps task type (deprecated in CMS): contestants
// submit two files with a function each; the first computes some data
// from the input and the second must recover something from it. The
// admins provide manager.%l, compiled with both sources (and headers
// named after the files, manager.h, first.h... where the language has
// headers), which runs twice at the same time: `./manager 0 /fifo/fifo`
// with the input on its standard input, and `./manager 1 /fifo/fifo`,
// whose standard output is checked.
type twoSteps struct {
	checker checker.Checker
}

func newTwoSteps(values []json.RawMessage) (TaskType, error) {
	return &twoSteps{checker: checkerFor(decodeString(values[0]))}, nil
}

func (t *twoSteps) Name() string                  { return NameTwoSteps }
func (t *twoSteps) DisplayName() string           { return "Two steps" }
func (t *twoSteps) Testable() bool                { return true }
func (t *twoSteps) AllowPartialSubmission() bool  { return false }
func (t *twoSteps) ReusePreviousSubmission() bool { return true }
func (t *twoSteps) UserManagers() []string        { return []string{twoStepsManager + ".%l"} }
func (t *twoSteps) AutoManagers() []string        { return []string{} }

// Boxes: both steps; the first box is released before the checker runs.
func (t *twoSteps) Boxes() int { return 2 }

func (t *twoSteps) Managers() []string {
	m := []string{twoStepsManager + ".%l"}
	if t.checker.Kind.IsProgram() {
		m = append(m, checkerCodename)
	}
	return m
}

// sources lists the files compiled for a language: the manager, then each
// submitted file, each followed by its header when the language has
// headers.
func (t *twoSteps) sources(codenames []string, lang *language.Language) []string {
	src, hdr := lang.SourceExtension(), lang.HeaderExtension()
	names := []string{twoStepsManager + src}
	if hdr != "" {
		names = append(names, twoStepsManager+hdr)
	}
	for _, c := range codenames {
		names = append(names, replaceExt(c, src))
		if hdr != "" {
			names = append(names, replaceExt(c, hdr))
		}
	}
	return names
}

func (t *twoSteps) CompilationCommands(langs *language.Registry, submissionFormat []string) map[string][][]string {
	res := map[string][][]string{}
	for _, l := range langs.All() {
		cmds, err := l.CompilationCommands(t.sources(submissionFormat, l), twoStepsManager+l.ExecutableExtension, true)
		if err == nil {
			res[l.Name] = cmds
		}
	}
	return res
}

func (t *twoSteps) Compile(ctx context.Context, env *Env, j *job.Job, res *job.Result) error {
	r := newRun(ctx, env, j, res)
	lang, err := r.language()
	if err != nil {
		return err
	}
	if err := r.checkFiles(2, false); err != nil {
		return err
	}
	codenames := sortedKeys(j.Files)
	sources := t.sources(codenames, lang)
	files := map[string]string{}
	// The manager and the headers come from the managers, the sources
	// from the submission.
	for _, name := range sources {
		if d, ok := j.Managers[name]; ok {
			files[name] = d
		}
	}
	for _, c := range codenames {
		files[replaceExt(c, lang.SourceExtension())] = j.Files[c]
	}
	for _, name := range sources {
		if _, ok := files[name]; !ok {
			return configError("dataset is missing manager '%s'.", name)
		}
	}
	return r.compileFiles(lang, files, sources, twoStepsManager+lang.ExecutableExtension)
}

// replaceExt replaces ".%l" in a codename with an extension.
func replaceExt(codename, ext string) string {
	return strings.ReplaceAll(codename, ".%l", ext)
}

func (t *twoSteps) Evaluate(ctx context.Context, env *Env, j *job.Job, res *job.Result) error {
	r := newRun(ctx, env, j, res)
	exe, exeDigest, err := r.executable()
	if err != nil {
		return err
	}
	lang, err := r.language()
	if err != nil {
		return err
	}
	first, err := r.newSandboxBox("first_evaluate")
	if err != nil {
		return err
	}
	defer r.closeBox(first)
	second, err := r.newSandboxBox("second_evaluate")
	if err != nil {
		return err
	}
	defer r.closeBox(second)
	fifo, err := first.NewFIFODir("fifo")
	if err != nil {
		return err
	}
	defer fifo.Close()
	mount := fifo.Mount("/fifo")

	for _, b := range []*sandbox.Box{first, second} {
		if err := r.put(b, exe, exeDigest, true); err != nil {
			return err
		}
	}
	if err := r.put(first, twoStepsInput, r.j.Input, false); err != nil {
		return err
	}
	ev := func(step string) steps.Evaluation {
		return steps.Evaluation{
			Commands:     [][]string{{"./" + exe, step, "/fifo/fifo"}},
			Language:     lang,
			TimeLimit:    r.j.TimeLimit,
			MemoryLimit:  r.j.MemoryLimit,
			Dirs:         []sandbox.Dir{mount},
			Multiprocess: r.j.MultithreadedSandbox,
		}
	}
	ev1 := ev("0")
	ev1.Stdin, ev1.Stdout = twoStepsInput, twoStepsFirstStdout
	ev2 := ev("1")
	ev2.Stdout = twoStepsOutput
	p1, err := r.env.Steps.StartEvaluation(r.ctx, first, ev1)
	if err != nil {
		return err
	}
	p2, err := r.env.Steps.StartEvaluation(r.ctx, second, ev2)
	if err != nil {
		return err
	}
	st2, err2 := steps.WaitEvaluation(p2)
	st1, err1 := steps.WaitEvaluation(p1)
	if err := errors.Join(err1, err2); err != nil {
		return fmt.Errorf("two steps: %w", err)
	}
	stats := steps.MergeStats(st1, *st2, true)
	r.res.Stats = &stats
	r.closeBox(first)

	switch {
	case !steps.EvaluationSucceeded(&stats):
		r.setOutcome(0, steps.EvaluationText(&stats), "")
	case !second.Exists(twoStepsOutput):
		r.setOutcome(0, []string{steps.EvaluationMessages.Get(steps.EvaluationNoOutput).Message, twoStepsOutput}, "")
	default:
		if err := r.storeUserOutput(second, twoStepsOutput); err != nil {
			return err
		}
		if r.j.OnlyExecution {
			r.setOutcome(0, []string{steps.ExecutionCompleted}, "")
			return nil
		}
		o, err := r.checkOutput(t.checker, userOutput{box: second, name: twoStepsOutput}, nil)
		if err != nil {
			return err
		}
		r.setOutcome(o.Outcome, o.Text, o.AdminText)
	}
	return nil
}
