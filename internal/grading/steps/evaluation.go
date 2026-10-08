// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/grading/steps/evaluation.py of CMS:
// Copyright © 2010-2015 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2013 Bernard Blackham <bernard@largestprime.net>
// Copyright © 2013-2014 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2014 Fabian Gundlach <320pointsguy@gmail.com>
// Copyright © 2016 Myungwoo Chun <mc.tamaki@gmail.com>
// Copyright © 2016 Amir Keivan Mohtashami <akmohtashami97@gmail.com>

package steps

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/sandbox"
)

// MultiprocessLimit is the process limit of the programs of contests
// with a language that requires multithreading (upstream
// set_multiprocess: enough for any runtime, bounded to limit fork bombs).
const MultiprocessLimit = 1000

// EvaluationStderr is where the standard error of evaluated programs
// goes (upstream "stderr.txt").
const EvaluationStderr = "stderr.txt"

// Evaluation describes the run of a contestant program (upstream
// evaluation_step arguments).
type Evaluation struct {
	// Commands run one after the other (from
	// lang.EvaluationCommands); the statistics are those of the last.
	Commands [][]string
	// Language of the program: its directories, environment, minimum
	// number of threads. May be nil.
	Language *language.Language

	// TimeLimit is the CPU time limit in seconds of each command (0: none).
	TimeLimit float64
	// WallTimeLimit is the wall clock limit in seconds; 0 means
	// 2×TimeLimit+1 (none without a time limit).
	WallTimeLimit float64
	// MemoryLimit is the memory limit in bytes (0: none).
	MemoryLimit int64

	// Dirs are mounted besides those of the language (FIFOs of
	// Communication tasks...); Env is added to the language's.
	Dirs []sandbox.Dir
	Env  []string
	// Writable are the only files of the home the program may write,
	// besides Stdout and the standard error (EvaluationStderr), which
	// are created empty (upstream allow_writing_only).
	Writable []string
	// Stdin and Stdout redirect the standard streams to files or FIFOs
	// ("": /dev/null).
	Stdin, Stdout string
	// StdinFile and StdoutFile replace Stdin and Stdout with open files
	// of the worker (pipes: the Interactive task type), and ExtraFiles
	// are passed as the descriptors 3, 4... (trusted programs only, which
	// then get no limit on open files). See sandbox.RunSpec.
	StdinFile, StdoutFile *os.File
	ExtraFiles            []*os.File

	// Multiprocess allows MultiprocessLimit processes (job
	// MultithreadedSandbox, a language of the contest needs threads);
	// so does a language that requires multithreading.
	Multiprocess bool
	// Trusted marks an admin program (a manager run alongside the
	// contestant's): it runs without the seccomp filter.
	Trusted bool
}

// spec builds the sandbox run of a command.
func (c *Config) evaluationSpec(ev *Evaluation, argv []string) (sandbox.RunSpec, error) {
	if ev.TimeLimit < 0 || ev.WallTimeLimit < 0 || ev.MemoryLimit < 0 {
		return sandbox.RunSpec{}, fmt.Errorf("evaluation: negative limit (time %g, wall %g, memory %d)",
			ev.TimeLimit, ev.WallTimeLimit, ev.MemoryLimit)
	}
	spec := sandbox.RunSpec{
		Argv:           argv,
		Stdin:          ev.Stdin,
		Stdout:         ev.Stdout,
		Stderr:         EvaluationStderr,
		CPUTime:        ev.TimeLimit,
		WallTime:       ev.WallTimeLimit,
		MemoryKiB:      c.memoryKiB(ev.MemoryLimit, ev.Language),
		Processes:      1,
		Seccomp:        !ev.Trusted,
		RestrictWrites: true,
		Writable:       ev.Writable,
		StdinFile:      ev.StdinFile,
		StdoutFile:     ev.StdoutFile,
		ExtraFiles:     ev.ExtraFiles,
	}
	if len(ev.ExtraFiles) > 0 {
		if !ev.Trusted {
			return sandbox.RunSpec{}, errors.New("evaluation: only trusted programs inherit file descriptors")
		}
		spec.OpenFiles = sandbox.Unlimited
	}
	if spec.WallTime == 0 {
		spec.WallTime = wallLimit(ev.TimeLimit)
	}
	if l := ev.Language; l != nil {
		spec.Dirs = mounts(l.Evaluation.Dirs)
		spec.Env = append(spec.Env, l.Evaluation.Env...)
		spec.Processes = max(spec.Processes, l.Evaluation.Processes)
		if l.RequiresMultithreading {
			spec.Processes = MultiprocessLimit
		}
	}
	if ev.Multiprocess {
		spec.Processes = MultiprocessLimit
	}
	spec.Dirs = append(spec.Dirs, ev.Dirs...)
	spec.Env = append(spec.Env, ev.Env...)
	return spec, nil
}

// Evaluate runs a contestant program in the box, which already holds the
// executable and the input (upstream evaluation_step): limits as given,
// seccomp filter on, the home read-only but for the outputs. Every
// command runs (only a sandbox failure stops), and the statistics are
// those of the last command. Check EvaluationSucceeded, and use
// EvaluationText for the message of a failed run.
//
// The error is non-nil only when the sandbox failed (retry the job).
func (c *Config) Evaluate(ctx context.Context, box Box, ev Evaluation) (*job.Stats, error) {
	if len(ev.Commands) == 0 {
		return nil, errors.New("evaluation: no commands")
	}
	var last *sandbox.Result
	for i, argv := range ev.Commands {
		spec, err := c.evaluationSpec(&ev, argv)
		if err != nil {
			return nil, err
		}
		res, err := box.Run(ctx, spec)
		if err == nil && (res == nil || res.Status == sandbox.StatusInternalError) {
			err = errors.New("sandbox error")
		}
		if err != nil {
			return nil, fmt.Errorf("evaluation, command %d of %d (%s): %w", i+1, len(ev.Commands), argv[0], err)
		}
		c.log().Debug("evaluation command", "argv", argv, "status", res.Status.String(),
			"cpu", res.CPUTime, "wall", res.WallTime, "memory_kib", res.MemoryKiB)
		last = res
	}
	st := ExecutionStats(last)
	return &st, nil
}

// StartEvaluation starts the single command of ev without waiting
// (upstream evaluation_step_before_run with wait=False), for programs that
// must run at the same time (Communication). Finish it with
// WaitEvaluation.
func (c *Config) StartEvaluation(ctx context.Context, box *sandbox.Box, ev Evaluation) (*sandbox.Process, error) {
	if len(ev.Commands) != 1 {
		return nil, fmt.Errorf("evaluation: %d commands, a started evaluation runs exactly one", len(ev.Commands))
	}
	spec, err := c.evaluationSpec(&ev, ev.Commands[0])
	if err != nil {
		return nil, err
	}
	return box.Start(ctx, spec)
}

// WaitEvaluation waits for a program started with StartEvaluation and
// returns its statistics (upstream evaluation_step_after_run). Merge the
// statistics of programs that ran together with MergeStats (concurrent).
func WaitEvaluation(p *sandbox.Process) (*job.Stats, error) {
	res, err := p.Wait()
	if err == nil && (res == nil || res.Status == sandbox.StatusInternalError) {
		err = errors.New("sandbox error")
	}
	if err != nil {
		return nil, fmt.Errorf("evaluation: %w", err)
	}
	st := ExecutionStats(res)
	return &st, nil
}

// EvaluationSucceeded reports whether the program ended normally, so
// that its output can be checked (upstream evaluation_success). When it
// did not, the outcome is 0 and the text EvaluationText.
func EvaluationSucceeded(st *job.Stats) bool { return st.ExitStatus == job.ExitOK }
