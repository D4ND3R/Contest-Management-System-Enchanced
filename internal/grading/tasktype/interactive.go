// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/grading/tasktypes/Interactive.py and
// interactive_keeper.py of CMS:
// Copyright © 2026 Luca Versari <veluca93@gmail.com>

package tasktype

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/steps"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/sandbox"
)

// Files and protocol of the controller of an Interactive task.
const (
	interactiveController = "controller"
	interactiveInput      = "input.txt"
	// startSolution is the line the controller writes on its standard
	// output to get a new instance of the solution; it then reads on its
	// standard input the descriptors to write to and to read from it.
	startSolution = "START_SOLUTION"
	// maxControllerLine bounds the lines read from the controller.
	maxControllerLine = 4096
	// maxControllerFeedback bounds what is read of its standard error.
	maxControllerFeedback = 1 << 20
	// keeperMargin is added to twice the controller's wall clock limit to
	// bound the whole evaluation (upstream kills its keeper process after
	// twice that limit).
	keeperMargin = 10 * time.Second
)

// Texts of an Interactive evaluation that are not msgids of CMS
// (upstream writes them as they are).
const (
	textControllerFailed = "Controller failed"
	textMayBeCausedBy    = " (may be caused by %s)"
)

// interactiveParams: [process limit, "alone"|"grader", concurrent,
// controller memory limit (MB), controller time limit (s), controller
// wall clock limit (s)].
func interactiveParams() []Param {
	return []Param{
		{Kind: ParamInt, Name: "Process limit", ShortName: "process_limit",
			Description: "Maximum number of solution instances the controller can spawn." +
				"Resource usage is proportional to this limit."},
		compilationParam(),
		{Kind: ParamBool, Name: "Concurrent solutions", ShortName: "concurrent",
			Description: "Whether solutions are assumed to be run concurrently or not"},
		{Kind: ParamFloat, Name: "Controller memory limit (MB)", ShortName: "controller_memory_limit",
			Description: "Maximum memory (in MB) that the controller can use"},
		{Kind: ParamFloat, Name: "Controller time limit (s)", ShortName: "controller_time_limit",
			Description: "Maximum CPU time (in seconds) that the controller can use"},
		{Kind: ParamFloat, Name: "Controller wall time limit (s)", ShortName: "controller_wall_limit",
			Description: "Maximum wall time (in seconds) that the controller can use"},
	}
}

// interactive is the Interactive task type: an admins' program, the
// controller, starts instances of the contestant's program (compiled
// alone or with a grader) as it needs them, and talks with each through a
// pair of pipes.
//
// The controller runs in its own box with input.txt, as a trusted
// program with its own limits. For each line START_SOLUTION it writes on
// its standard output, the worker starts a new instance of the solution
// (at most process_limit), with the time and memory limits of the task
// and the controller's wall clock limit, whose standard input and output
// are pipes to the controller, and answers on the controller's standard
// input with the descriptors "<to solution> <from solution>" the
// controller inherited when it started. At the end the controller writes
// on its standard error the lines "SCORE: <outcome>", "USER_MESSAGE:
// <text>" and "ADMIN_MESSAGE: <text>" (each at most once; nothing else).
//
// The statistics are those of the solutions, merged as concurrent runs
// (memory added, wall clock time the maximum) or not. A failed solution
// scores 0 with its message (after the controller's text, "(may be
// caused by ...)"); a controller that fails scores 0 with "Controller
// failed"; otherwise the outcome and the texts are the controller's.
//
// Differences with upstream: the grader is the entry point of the
// solutions when there is one (the Java class); solutions get the
// processes of the contest (MultithreadedSandbox) instead of one; the
// controller is an auto manager, so user tests work (they end with
// "Execution completed successfully", like Communication); the percent
// signs of the controller's user message are escaped.
type interactive struct {
	processLimit     int
	grader           bool
	concurrent       bool
	controllerMemory int64 // bytes
	controllerTime   float64
	controllerWall   float64
}

func newInteractive(values []json.RawMessage) (TaskType, error) {
	n, _ := decodeInt(values[0])
	mem, _ := decodeFloat(values[3])
	cpu, _ := decodeFloat(values[4])
	wall, _ := decodeFloat(values[5])
	switch {
	case n < 1 || n > MaxProcesses:
		return nil, fmt.Errorf("the process limit must be between 1 and %d, got %d", MaxProcesses, n)
	case mem <= 0 || mem > job.MaxMemoryLimit>>20:
		return nil, fmt.Errorf("the controller memory limit must be positive (MB), got %g", mem)
	case cpu <= 0 || cpu > job.MaxTimeLimit || wall <= 0 || wall > job.MaxTimeLimit:
		return nil, fmt.Errorf("the controller time limits must be between 0 and %g seconds, got %g and %g",
			job.MaxTimeLimit, cpu, wall)
	}
	return &interactive{
		processLimit: int(n),
		grader:       decodeString(values[1]) == compilationGrader,
		concurrent:   string(bytes.TrimSpace(values[2])) == "true",
		// Upstream: the parameter times 2**20, in bytes.
		controllerMemory: int64(mem * (1 << 20)),
		controllerTime:   cpu,
		controllerWall:   wall,
	}, nil
}

func (t *interactive) Name() string                  { return NameInteractive }
func (t *interactive) DisplayName() string           { return "Interactive" }
func (t *interactive) Testable() bool                { return true }
func (t *interactive) AllowPartialSubmission() bool  { return false }
func (t *interactive) ReusePreviousSubmission() bool { return true }
func (t *interactive) Boxes() int                    { return t.processLimit + 1 }

// AutoManagers: unlike upstream (none), the controller, without which a
// user test cannot run.
func (t *interactive) AutoManagers() []string { return []string{interactiveController} }

func (t *interactive) UserManagers() []string {
	if t.grader {
		return []string{graderBasename + ".%l"}
	}
	return []string{}
}

func (t *interactive) Managers() []string {
	m := []string{interactiveController}
	if t.grader {
		m = append(m, graderBasename+".%l")
	}
	return m
}

func (t *interactive) CompilationCommands(langs *language.Registry, submissionFormat []string) map[string][][]string {
	var codenames []string
	if t.grader {
		codenames = append(codenames, graderBasename+".%l")
	}
	for _, c := range submissionFormat {
		if strings.HasSuffix(c, ".%l") {
			codenames = append(codenames, c)
		}
	}
	return compilationCommands(langs, codenames, func(l *language.Language) string {
		return executableName(submissionFormat, l, false)
	})
}

func (t *interactive) Compile(ctx context.Context, env *Env, j *job.Job, res *job.Result) error {
	return newRun(ctx, env, j, res).compileSubmission(t.grader, false)
}

// solution is a running instance of the contestant's program.
type solution struct {
	box   *sandbox.Box
	proc  *sandbox.Process
	done  chan struct{}
	stats *job.Stats
	err   error
}

// pipe is a pipe between the controller and a solution.
type pipe struct{ r, w *os.File }

func (t *interactive) Evaluate(ctx context.Context, env *Env, j *job.Job, res *job.Result) error {
	// Upstream's keeper is killed after twice the controller's wall
	// clock limit: the evaluation then fails.
	ctx, cancel := context.WithTimeout(ctx, time.Duration(2*t.controllerWall*float64(time.Second))+keeperMargin)
	defer cancel()
	r := newRun(ctx, env, j, res)
	exe, exeDigest, err := r.executable()
	if err != nil {
		return err
	}
	controllerDigest, err := r.manager(interactiveController)
	if err != nil {
		return err
	}
	lang, err := r.language()
	if err != nil {
		return err
	}
	// Unlike upstream, the grader is the entry point when there is one,
	// as in the other task types (the Java class).
	main := ""
	if t.grader {
		main = graderBasename
	}
	solutionCmds := lang.EvaluationCommands(exe, main, nil)

	var files []*os.File
	defer func() {
		for _, f := range files {
			f.Close() // closing twice is harmless
		}
	}()
	newPipe := func() (pipe, error) {
		pr, pw, err := os.Pipe()
		if err != nil {
			return pipe{}, fmt.Errorf("interactive: %w", err)
		}
		files = append(files, pr, pw)
		return pipe{pr, pw}, nil
	}
	// All the pipes exist before the controller starts: it inherits its
	// ends of each pair as the descriptors 3+2i (to solution i) and 4+2i
	// (from solution i).
	toSol := make([]pipe, t.processLimit)
	fromSol := make([]pipe, t.processLimit)
	var inherited []*os.File
	for i := range t.processLimit {
		if toSol[i], err = newPipe(); err != nil {
			return err
		}
		if fromSol[i], err = newPipe(); err != nil {
			return err
		}
		inherited = append(inherited, toSol[i].w, fromSol[i].r)
	}
	ctrlIn, err := newPipe()
	if err != nil {
		return err
	}
	ctrlOut, err := newPipe()
	if err != nil {
		return err
	}

	ctrlBox, err := r.newSandboxBox("controller")
	if err != nil {
		return err
	}
	defer r.closeBox(ctrlBox)
	if err := r.put(ctrlBox, interactiveController, controllerDigest, true); err != nil {
		return err
	}
	if err := r.put(ctrlBox, interactiveInput, r.j.Input, false); err != nil {
		return err
	}

	var sols []*solution
	defer func() {
		// After an error: stop what still runs.
		for _, s := range sols {
			s.box.Close()
		}
		for _, s := range sols {
			<-s.done
		}
	}()

	ctrlProc, err := r.env.Steps.StartEvaluation(ctx, ctrlBox, steps.Evaluation{
		Commands:      [][]string{{"./" + interactiveController}},
		TimeLimit:     t.controllerTime,
		WallTimeLimit: t.controllerWall,
		MemoryLimit:   t.controllerMemory,
		Multiprocess:  true,
		Trusted:       true,
		StdinFile:     ctrlIn.r,
		StdoutFile:    ctrlOut.w,
		ExtraFiles:    inherited,
	})
	if err != nil {
		return err
	}
	// The worker keeps only its ends.
	ctrlIn.r.Close()
	ctrlOut.w.Close()
	for _, f := range inherited {
		f.Close()
	}

	startSol := func(i int) error {
		box, err := r.newSandboxBox(fmt.Sprintf("solution_%d", i))
		if err != nil {
			return err
		}
		s := &solution{box: box, done: make(chan struct{})}
		if err := r.put(box, exe, exeDigest, true); err != nil {
			r.closeBox(box)
			return err
		}
		if len(solutionCmds) > 1 {
			// Set-up commands, as in Communication.
			if _, err := r.env.Steps.RunTrusted(ctx, box, solutionCmds[:len(solutionCmds)-1]); err != nil {
				r.closeBox(box)
				return err
			}
		}
		s.proc, err = r.env.Steps.StartEvaluation(ctx, box, steps.Evaluation{
			Commands: solutionCmds[len(solutionCmds)-1:],
			Language: lang,
			// The wall clock limit only stops stuck solutions: the
			// controller's (it would die first with a longer one).
			TimeLimit:     r.j.TimeLimit,
			WallTimeLimit: t.controllerWall,
			MemoryLimit:   r.j.MemoryLimit,
			StdinFile:     toSol[i].r,
			StdoutFile:    fromSol[i].w,
			Multiprocess:  r.j.MultithreadedSandbox,
		})
		if err != nil {
			r.closeBox(box)
			return err
		}
		toSol[i].r.Close()
		fromSol[i].w.Close()
		sols = append(sols, s)
		go func() {
			defer close(s.done)
			s.stats, s.err = steps.WaitEvaluation(s.proc)
			// Release the box at once: the controller may start many
			// solutions one after the other.
			r.closeBox(box)
		}()
		return nil
	}

	// Serve the controller.
	lines := bufio.NewReaderSize(ctrlOut.r, maxControllerLine)
	next := 0
	var protocolErr error
serve:
	for {
		line, err := readControllerLine(lines)
		if err == io.EOF {
			break
		}
		if err != nil {
			protocolErr = err
			break
		}
		if pyStrip(line) != startSolution {
			protocolErr = configError("Invalid command from controller: %s", pyStrip(line))
			break
		}
		if next >= t.processLimit {
			break serve
		}
		if err := startSol(next); err != nil {
			return err
		}
		// A controller that already died does not read the answer.
		_, _ = fmt.Fprintf(ctrlIn.w, "%d %d\n", 3+2*next, 4+2*next)
		next++
	}
	ctrlIn.w.Close()
	ctrlOut.r.Close()
	if protocolErr != nil {
		return protocolErr
	}

	ctrlStats, ctrlErr := steps.WaitEvaluation(ctrlProc)
	var errs []error
	if ctrlErr != nil {
		errs = append(errs, fmt.Errorf("controller: %w", ctrlErr))
	}
	var stats *job.Stats
	solOK := true
	for i, s := range sols {
		<-s.done
		if s.err != nil {
			errs = append(errs, fmt.Errorf("solution %d: %w", i, s.err))
			continue
		}
		solOK = solOK && steps.EvaluationSucceeded(s.stats)
		merged := steps.MergeStats(stats, *s.stats, t.concurrent)
		stats = &merged
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	if stats == nil {
		stats = &job.Stats{
			ExecutionTime: job.Ptr(0.0), ExecutionWallClockTime: job.Ptr(0.0),
			ExecutionMemory: job.Ptr(int64(0)), ExitStatus: job.ExitOK,
		}
	}

	feedback, _, err := ctrlBox.ReadFile(steps.EvaluationStderr, maxControllerFeedback)
	if err != nil {
		return fmt.Errorf("reading the feedback of the controller: %w", err)
	}
	score, text, admin, err := parseControllerFeedback(feedback)
	if err != nil {
		return configError("Internal error: %v", err)
	}
	r.res.Stats = stats
	switch {
	case !solOK:
		msg := steps.EvaluationText(stats)
		if len(text) > 0 && len(msg) > 0 {
			msg = append([]string{text[0] + fmt.Sprintf(textMayBeCausedBy, msg[0])},
				append(text[1:], msg[1:]...)...)
		}
		r.setOutcome(0, msg, admin)
	case !steps.EvaluationSucceeded(ctrlStats):
		r.setOutcome(0, append([]string{textControllerFailed}, text...), admin)
	case r.j.OnlyExecution:
		// A user test: like Communication, the verdict of the admins'
		// program is not shown (upstream has no user tests here).
		r.setOutcome(0, []string{steps.ExecutionCompleted}, "")
	default:
		r.setOutcome(score, text, admin)
	}
	return nil
}

// readControllerLine reads a line of the controller (a last line without
// newline counts), refusing long lines and invalid UTF-8.
func readControllerLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadSlice('\n')
	switch {
	case errors.Is(err, bufio.ErrBufferFull):
		return "", configError("the controller wrote a line longer than %d bytes", maxControllerLine)
	case err == io.EOF && len(line) > 0:
		err = nil
	case err != nil:
		if errors.Is(err, os.ErrClosed) {
			return "", io.EOF
		}
		return "", err
	}
	if !utf8.Valid(line) {
		return "", configError("the controller wrote invalid UTF-8")
	}
	return string(line), nil
}

// Prefixes of the lines of the controller's feedback.
const (
	feedbackScore = "SCORE: "
	feedbackUser  = "USER_MESSAGE: "
	feedbackAdmin = "ADMIN_MESSAGE: "
)

// parseControllerFeedback reads the standard error of the controller
// (upstream get_controller_text): each line, trimmed, is "SCORE: <float>",
// "USER_MESSAGE: <text>" or "ADMIN_MESSAGE: <text>", each at most once.
// The score defaults to 0. Percent signs of the user message are doubled
// (texts are formatted when shown).
func parseControllerFeedback(data []byte) (score float64, text []string, admin string, err error) {
	if !utf8.Valid(data) {
		return 0, nil, "", errors.New("the feedback of the controller is not valid UTF-8")
	}
	s := strings.ReplaceAll(strings.ReplaceAll(string(data), "\r\n", "\n"), "\r", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return 0, nil, "", nil
	}
	var haveScore, haveAdmin bool
	for _, line := range strings.Split(s, "\n") {
		line = pyStrip(line)
		if strings.ContainsRune(line, 0) {
			return 0, nil, "", errors.New("NUL in the feedback of the controller")
		}
		switch {
		case strings.HasPrefix(line, feedbackScore):
			if haveScore {
				return 0, nil, "", errors.New("two scores in the feedback of the controller")
			}
			v, err := strconv.ParseFloat(pyStrip(line[len(feedbackScore):]), 64)
			if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
				return 0, nil, "", fmt.Errorf("invalid score %q from the controller", line[len(feedbackScore):])
			}
			score, haveScore = v, true
		case strings.HasPrefix(line, feedbackUser):
			if text != nil {
				return 0, nil, "", errors.New("two user messages in the feedback of the controller")
			}
			msg := pyStrip(line[len(feedbackUser):])
			text = []string{strings.ReplaceAll(msg, "%", "%%")}
		case strings.HasPrefix(line, feedbackAdmin):
			if haveAdmin {
				return 0, nil, "", errors.New("two admin messages in the feedback of the controller")
			}
			admin, haveAdmin = pyStrip(line[len(feedbackAdmin):]), true
		default:
			return 0, nil, "", fmt.Errorf("Unknown controller feedback command %q", job.TruncateUTF8(line, 100))
		}
	}
	return score, text, admin, nil
}

// pyStrip trims white space like Python's str.strip().
func pyStrip(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || r >= 0x1c && r <= 0x1f
	})
}
