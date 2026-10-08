// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/grading/tasktypes/Communication.py of CMS:
// Copyright © 2010-2012 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2012-2014 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2016 Masaki Hara <ackie.h.gmai@gmail.com>

package tasktype

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/checker"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/steps"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/sandbox"
)

// Files of the manager of a Communication task.
const (
	// communicationManager is the admins' program.
	communicationManager = "manager"
	// communicationInput is the input in the manager's box, also its
	// standard input.
	communicationInput = "input.txt"
	// communicationOutput is where the manager may write what users see
	// of their user tests.
	communicationOutput = "output.txt"
	// communicationStdout is the standard output of the manager, the
	// outcome (standard manager output).
	communicationStdout = "stdout.txt"
)

// Values of the "user_io" parameter.
const (
	userIOStd   = "std_io"
	userIOFIFOs = "fifo_io"
)

// MaxProcesses bounds the number of user processes of a Communication
// task and the process limit of an Interactive task: each one needs a
// sandbox box.
const MaxProcesses = 1000

// communicationParams: [number of processes, "alone"|"grader",
// "std_io"|"fifo_io"].
func communicationParams() []Param {
	return []Param{
		{Kind: ParamInt, Name: "Number of Processes", ShortName: "num_processes"},
		compilationParam(),
		{
			Kind: ParamChoice, Name: "User I/O", ShortName: "user_io",
			Choices: []Choice{
				{userIOStd, "User processes read from stdin and write to stdout"},
				{userIOFIFOs, "User processes read from and write to fifos, whose paths are given as arguments"},
			},
		},
	}
}

// communication is the Communication task type: an admins' program, the
// manager, runs together with one or more instances of the contestant's
// program (compiled alone or with a grader), talking with each through a
// pair of FIFOs. The manager reads the input on its standard input, gets
// the FIFOs as arguments (from and to the first process, from and to the
// second...), and writes the outcome on its standard output and the text
// on its standard error (standard manager output); it may write
// output.txt, which users see for their user tests. Each user process
// gets its FIFOs as arguments (to and from the manager) or as its
// standard input and output, plus its 0-based index when there are
// several.
//
// The users' statistics are merged as concurrent runs, and their total
// CPU time must stay under the time limit. When a user process fails,
// the outcome is 0 with its message whatever the manager does (it is
// stopped: it may be blocked on the FIFOs of a dead process, or die of a
// broken pipe); upstream fails the job in those cases, and also answers
// "Execution completed successfully" to a user test whose program failed.
type communication struct {
	numProcesses int
	grader       bool
	fifos        bool
}

func newCommunication(values []json.RawMessage) (TaskType, error) {
	n, _ := decodeInt(values[0])
	if n < 1 || n > MaxProcesses {
		return nil, fmt.Errorf("the number of processes must be between 1 and %d, got %d", MaxProcesses, n)
	}
	return &communication{
		numProcesses: int(n),
		grader:       decodeString(values[1]) == compilationGrader,
		fifos:        decodeString(values[2]) == userIOFIFOs,
	}, nil
}

func (t *communication) Name() string                  { return NameCommunication }
func (t *communication) DisplayName() string           { return "Communication" }
func (t *communication) Testable() bool                { return true }
func (t *communication) AllowPartialSubmission() bool  { return false }
func (t *communication) ReusePreviousSubmission() bool { return true }
func (t *communication) AutoManagers() []string        { return []string{communicationManager} }
func (t *communication) Boxes() int                    { return t.numProcesses + 1 }

func (t *communication) UserManagers() []string {
	if t.grader {
		return []string{graderBasename + ".%l"}
	}
	return []string{}
}

func (t *communication) Managers() []string {
	m := []string{communicationManager}
	if t.grader {
		m = append(m, graderBasename+".%l")
	}
	return m
}

func (t *communication) CompilationCommands(langs *language.Registry, submissionFormat []string) map[string][][]string {
	var codenames []string
	if t.grader {
		codenames = append(codenames, graderBasename+".%l")
	}
	codenames = append(codenames, submissionFormat...)
	return compilationCommands(langs, codenames, func(l *language.Language) string {
		return executableName(submissionFormat, l, false)
	})
}

func (t *communication) Compile(ctx context.Context, env *Env, j *job.Job, res *job.Result) error {
	r := newRun(ctx, env, j, res)
	if _, err := r.language(); err != nil {
		return err
	}
	if err := r.checkFiles(1, true); err != nil {
		return err
	}
	return r.compileSubmission(t.grader, false)
}

func (t *communication) Evaluate(ctx context.Context, env *Env, j *job.Job, res *job.Result) error {
	r := newRun(ctx, env, j, res)
	exe, exeDigest, err := r.executable()
	if err != nil {
		return err
	}
	managerDigest, err := r.manager(communicationManager)
	if err != nil {
		return err
	}
	lang, err := r.language()
	if err != nil {
		return err
	}
	n := t.numProcesses

	// The manager's box, with a directory of FIFOs per user process
	// (/fifo<i>/u<i>_to_m and /fifo<i>/m_to_u<i>).
	mgrBox, err := r.newSandboxBox("manager_evaluate")
	if err != nil {
		return err
	}
	defer r.closeBox(mgrBox)
	fifoDirs := make([]sandbox.Dir, n)
	toManager := make([]string, n)
	fromManager := make([]string, n)
	for i := range n {
		inside := fmt.Sprintf("/fifo%d", i)
		u2m, m2u := fmt.Sprintf("u%d_to_m", i), fmt.Sprintf("m_to_u%d", i)
		fd, err := mgrBox.NewFIFODir(u2m, m2u)
		if err != nil {
			return err
		}
		defer fd.Close()
		fifoDirs[i] = fd.Mount(inside)
		toManager[i], fromManager[i] = inside+"/"+u2m, inside+"/"+m2u
	}
	if err := r.put(mgrBox, communicationManager, managerDigest, true); err != nil {
		return err
	}
	if err := r.put(mgrBox, communicationInput, r.j.Input, false); err != nil {
		return err
	}

	// The users' boxes.
	userBoxes := make([]*sandbox.Box, n)
	for i := range n {
		b, err := r.newSandboxBox(fmt.Sprintf("user_evaluate_%d", i))
		if err != nil {
			return err
		}
		defer r.closeBox(b)
		if err := r.put(b, exe, exeDigest, true); err != nil {
			return err
		}
		userBoxes[i] = b
	}

	// The manager: trusted, but run as an evaluation to run it alongside
	// the users. Its limits are generous enough that users cannot make
	// it time out: its wall clock limit exceeds the sum of theirs, and
	// its CPU limit the total time the users may take (n times the time
	// limit, since that total is checked only at the end).
	mgrCmd := []string{"./" + communicationManager}
	for i := range n {
		mgrCmd = append(mgrCmd, toManager[i], fromManager[i])
	}
	trusted := r.env.Steps.Trusted
	mgrProc, err := r.env.Steps.StartEvaluation(r.ctx, mgrBox, steps.Evaluation{
		Commands:     [][]string{mgrCmd},
		TimeLimit:    max(float64(n)*(r.j.TimeLimit+1), trusted.MaxTimeSec),
		MemoryLimit:  trusted.MaxMemoryKiB * 1024,
		Dirs:         fifoDirs,
		Writable:     []string{communicationOutput},
		Stdin:        communicationInput,
		Stdout:       communicationStdout,
		Multiprocess: r.j.MultithreadedSandbox,
		Trusted:      true,
	})
	if err != nil {
		return err
	}

	// The users, with the grader as entry point when there is one.
	main := ""
	if t.grader {
		main = graderBasename
	}
	userProcs := make([]*sandbox.Process, n)
	for i := range n {
		ev := steps.Evaluation{
			Language:     lang,
			TimeLimit:    r.j.TimeLimit,
			MemoryLimit:  r.j.MemoryLimit,
			Dirs:         []sandbox.Dir{fifoDirs[i]},
			Multiprocess: r.j.MultithreadedSandbox,
		}
		var args []string
		if t.fifos {
			args = append(args, fromManager[i], toManager[i])
		} else {
			ev.Stdin, ev.Stdout = fromManager[i], toManager[i]
		}
		if n != 1 {
			args = append(args, strconv.Itoa(i))
		}
		cmds := lang.EvaluationCommands(exe, main, args)
		// The commands before the last set things up (upstream runs
		// them as trusted, ignoring how they end).
		if len(cmds) > 1 {
			if _, err := r.env.Steps.RunTrusted(r.ctx, userBoxes[i], cmds[:len(cmds)-1]); err != nil {
				return err
			}
		}
		ev.Commands = cmds[len(cmds)-1:]
		if userProcs[i], err = r.env.Steps.StartEvaluation(r.ctx, userBoxes[i], ev); err != nil {
			return err
		}
	}

	// Wait for the users, then for the manager.
	var errs []error
	var userStats *job.Stats
	userOK := true
	for i := range n {
		st, err := steps.WaitEvaluation(userProcs[i])
		if err != nil {
			errs = append(errs, fmt.Errorf("user process %d: %w", i, err))
			continue
		}
		userOK = userOK && steps.EvaluationSucceeded(st)
		merged := steps.MergeStats(userStats, *st, true)
		userStats = &merged
	}
	if len(errs) > 0 {
		return errors.Join(errs...) // closing the box stops the manager
	}
	// The time is the sum over the user processes, but each box can only
	// enforce its own.
	if userOK && r.j.TimeLimit > 0 && userStats.ExecutionTime != nil && *userStats.ExecutionTime >= r.j.TimeLimit {
		userOK = false
		userStats.ExitStatus, userStats.Signal = job.ExitTimeout, 0
	}
	if !userOK {
		// The outcome is the users' failure, whatever the manager does:
		// it may be blocked opening the FIFOs of a user that died
		// before opening them, or fail writing to one that died later
		// (a broken pipe). Unlike upstream, which waits for the manager
		// and then fails the job (retried, never judged), stop it.
		mgrProc.Kill()
	}
	mgrStats, err := steps.WaitEvaluation(mgrProc)
	if err != nil {
		return fmt.Errorf("manager: %w", err)
	}
	r.res.Stats = userStats

	switch {
	case !userOK:
		// Unlike upstream (which says "Execution completed
		// successfully" to a user test whose program failed), the
		// failure comes first, as in the other task types.
		r.setOutcome(0, steps.EvaluationText(userStats), "")
	case !steps.EvaluationSucceeded(mgrStats):
		return configError("the manager ended with %s (signal %d)", mgrStats.ExitStatus, mgrStats.Signal)
	case r.j.OnlyExecution:
		r.setOutcome(0, []string{steps.ExecutionCompleted}, "")
	default:
		o, err := managerOutcome(mgrBox, communicationStdout, steps.EvaluationStderr)
		if err != nil {
			return err
		}
		r.setOutcome(o.Outcome, o.Text, o.AdminText)
	}
	return r.storeUserOutput(mgrBox, communicationOutput)
}

// maxManagerOutput bounds what is read of the outputs of a manager.
const maxManagerOutput = 1 << 20

// managerOutcome reads the standard manager output a manager left in its
// box (upstream extract_outcome_and_text).
func managerOutcome(box Box, stdout, stderr string) (*checker.Outcome, error) {
	out, _, err := box.ReadFile(stdout, maxManagerOutput)
	if err != nil {
		return nil, fmt.Errorf("reading the outcome of the manager: %w", err)
	}
	errOut, _, err := box.ReadFile(stderr, maxManagerOutput)
	if err != nil {
		return nil, fmt.Errorf("reading the text of the manager: %w", err)
	}
	o, err := checker.ParseManagerOutput(out, errOut)
	if err != nil {
		return nil, configError("invalid output from the manager: %v", err)
	}
	return o, nil
}

// newSandboxBox creates a box that can run programs in the background,
// which only real sandbox boxes do.
func (r *run) newSandboxBox(name string) (*sandbox.Box, error) {
	b, err := r.newBox(name)
	if err != nil {
		return nil, err
	}
	sb, ok := b.(*sandbox.Box)
	if !ok {
		r.closeBox(b)
		return nil, fmt.Errorf("the task type needs real sandbox boxes, got %T", b)
	}
	return sb, nil
}
