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
	"syscall"
	"time"

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
// CPU time must stay under the time limit. A manager that does not end
// normally fails the job (the admins must fix it), except when a user
// process failed first, or crashed by itself (a signal other than
// SIGPIPE, the CPU time or memory limit, a forbidden system call) at most
// crashGrace after the manager ended, since the ends of processes that
// die together are noticed in any order: then the outcome is 0 with the
// users' message whatever the manager does (it is stopped: it may be
// blocked on the FIFOs of a dead process, or die of a broken pipe). Upstream fails the
// job in that case too, and also answers "Execution completed
// successfully" to a user test whose program failed.
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
	// Each process is waited for from the moment it starts, noting when
	// it ended: which of the manager and the users failed first decides
	// whose fault the failure is.
	mgr := waitEnd(mgrProc)

	// The users, with the grader as entry point when there is one.
	main := ""
	if t.grader {
		main = graderBasename
	}
	userProcs := make([]*sandbox.Process, n)
	users := make([]*processEnd, n)
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
		users[i] = waitEnd(userProcs[i])
	}

	// Wait for the users. A manager that fails by itself before any
	// user process failed is the admins' error (upstream fails the job
	// whenever the manager fails): the users, which may stay blocked on
	// the FIFOs until their wall clock limit, are not waited for.
	usersDone := make(chan struct{})
	go func() {
		for _, u := range users {
			<-u.done
		}
		close(usersDone)
	}()
	select {
	case <-usersDone:
	case <-mgr.done:
		if mgr.err != nil {
			return fmt.Errorf("manager: %w", mgr.err)
		}
		if mgr.failedByItself() && !failedBefore(users, mgr.at) {
			// The workers notice the ends of processes that die together
			// in any order: give a user that crashed by itself a moment
			// to be noticed before blaming the manager.
			select {
			case <-usersDone:
			case <-time.After(crashGrace):
			}
			if !userCrashedNear(users, mgr.at) {
				return mgr.failure()
			}
		}
		<-usersDone
	}
	var errs []error
	var userStats *job.Stats
	var failedAt, lastAt time.Time // when the first user that failed, the last user ended
	for i, u := range users {
		if u.err != nil {
			errs = append(errs, fmt.Errorf("user process %d: %w", i, u.err))
			continue
		}
		if !steps.EvaluationSucceeded(u.stats) && (failedAt.IsZero() || u.at.Before(failedAt)) {
			failedAt = u.at
		}
		if u.at.After(lastAt) {
			lastAt = u.at
		}
		merged := steps.MergeStats(userStats, *u.stats, true)
		userStats = &merged
	}
	if len(errs) > 0 {
		return errors.Join(errs...) // closing the box stops the manager
	}
	// The time is the sum over the user processes, but each box can only
	// enforce its own: the users exceed it when the last one ends.
	if failedAt.IsZero() && r.j.TimeLimit > 0 && userStats.ExecutionTime != nil && *userStats.ExecutionTime >= r.j.TimeLimit {
		userStats.ExitStatus, userStats.Signal = job.ExitTimeout, 0
		failedAt = lastAt
	}

	if !failedAt.IsZero() {
		// The users failed. If the manager had already failed by
		// itself, the failure is the manager's (upstream fails the job
		// whenever the manager fails). Otherwise the outcome is the
		// users' failure, whatever the manager does: it may be blocked
		// opening the FIFOs of a user that died before opening them, or
		// fail writing to one that died later (a broken pipe). Unlike
		// upstream, which waits for the manager and then fails the job
		// (retried, never judged), stop it.
		select {
		case <-mgr.done:
		default:
			mgrProc.Kill()
		}
		<-mgr.done
		if mgr.err != nil {
			return fmt.Errorf("manager: %w", mgr.err)
		}
		if mgr.failedByItself() && mgr.at.Before(failedAt) && !userCrashedNear(users, mgr.at) {
			return mgr.failure()
		}
		r.res.Stats = userStats
		// Unlike upstream (which says "Execution completed
		// successfully" to a user test whose program failed), the
		// failure comes first, as in the other task types.
		r.setOutcome(0, steps.EvaluationText(userStats), "")
		return r.storeUserOutput(mgrBox, communicationOutput)
	}

	// The users ended normally: the manager must end normally too.
	<-mgr.done
	if mgr.err != nil {
		return fmt.Errorf("manager: %w", mgr.err)
	}
	if !steps.EvaluationSucceeded(mgr.stats) {
		return mgr.failure()
	}
	r.res.Stats = userStats
	switch {
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

// processEnd is how and when a program running in the background ended.
type processEnd struct {
	done  chan struct{} // closed when the other fields are set
	stats *job.Stats
	err   error
	at    time.Time
}

// waitEnd waits for p in the background.
func waitEnd(p *sandbox.Process) *processEnd {
	e := &processEnd{done: make(chan struct{})}
	go func() {
		e.stats, e.err = steps.WaitEvaluation(p)
		e.at = time.Now()
		close(e.done)
	}()
	return e
}

// failedByItself reports whether a manager that ended did not end
// normally, other than by a broken pipe: SIGPIPE means that a user
// process closed its end of a FIFO, so the user failed first.
func (e *processEnd) failedByItself() bool {
	return !steps.EvaluationSucceeded(e.stats) &&
		!(e.stats.ExitStatus == job.ExitSignal && e.stats.Signal == int(syscall.SIGPIPE))
}

// failure is the error of a job whose manager failed.
func (e *processEnd) failure() error {
	return configError("the manager ended with %s (signal %d)", e.stats.ExitStatus, e.stats.Signal)
}

// crashGrace bounds how much later than the manager a user process that
// crashed by itself may be noticed for the crash to count as the cause of
// the manager's failure.
const crashGrace = 250 * time.Millisecond

// crashedByItself reports whether a user process failed in a way that a
// manager that died cannot cause: killed by a signal other than SIGPIPE,
// over its CPU time or memory limit, or a forbidden system call. A
// nonzero exit, a broken pipe or the wall clock limit can follow from a
// dead manager (an end of file, a closed FIFO, a read that never
// returns), so for those the order of the ends decides.
func crashedByItself(s *job.Stats) bool {
	switch s.ExitStatus {
	case job.ExitSignal:
		return s.Signal != int(syscall.SIGPIPE)
	case job.ExitTimeout, job.ExitMemoryLimit, job.ExitSecurityViolation:
		return true
	}
	return false
}

// userCrashedNear reports whether a user process that already ended
// crashed by itself no later than crashGrace after t, the end of the
// manager: then the manager failed because of it (a manager failing on
// a broken pipe), even if its end was noticed first.
func userCrashedNear(users []*processEnd, t time.Time) bool {
	for _, u := range users {
		select {
		case <-u.done:
			if u.err == nil && crashedByItself(u.stats) && !u.at.After(t.Add(crashGrace)) {
				return true
			}
		default:
		}
	}
	return false
}

// failedBefore reports whether one of the processes that already ended
// failed before t.
func failedBefore(procs []*processEnd, t time.Time) bool {
	for _, p := range procs {
		select {
		case <-p.done:
			if p.err == nil && !steps.EvaluationSucceeded(p.stats) && p.at.Before(t) {
				return true
			}
		default:
		}
	}
	return false
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
