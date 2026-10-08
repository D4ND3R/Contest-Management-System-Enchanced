//go:build sandbox

// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// The tasks and the list of tests are ported from cmstestsuite/tasks/*/
// __init__.py and cmstestsuite/Tests.py of CMS:
// Copyright © 2012 Bernard Blackham <bernard@largestprime.net>
// Copyright © 2013-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2014-2015 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2016 Masaki Hara <ackie.h.gmai@gmail.com>
// Copyright © 2020 Andrey Vihrov <andrey.vihrov@gmail.com>
// The files they use are in testdata (see testdata/NOTICE).

package tasktype

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/steps"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/steps/stepstest"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/sandbox"
)

// Run with `make test-sandbox` (root, isolate); CMS_SANDBOX_CGROUPS=0
// without cgroups v2. -short tests fewer languages.

// The languages of cmstestsuite/Tests.py.
const (
	langCPP    = "C++11 / g++"
	langCPP14  = "C++14 / g++"
	langCPP17  = "C++17 / g++"
	langCPP20  = "C++20 / g++"
	langCC     = "C11 / gcc"
	langHS     = "Haskell / ghc"
	langJava   = "Java / JDK"
	langPascal = "Pascal / fpc"
	langPHP    = "PHP"
	langPy3    = "Python 3 / CPython"
	langPyPy3  = "Python 3 / PyPy"
	langRust   = "Rust"
	langCS     = "C# / Mono"
)

var (
	allLanguages = []string{langCC, langCS, langCPP, langCPP14, langCPP17, langCPP20, langHS, langJava,
		langPascal, langPHP, langPy3, langPyPy3, langRust}
	// managerLanguages support graders; communicationLanguages have the
	// stubs of the communication tasks (upstream MANAGER_LANGUAGES,
	// COMMUNICATION_LANGUAGES).
	managerLanguages       = without(allLanguages, langHS, langPHP, langRust)
	communicationLanguages = without(managerLanguages, langCS)
	// shortLanguages are those of -short.
	shortLanguages = []string{langCC, langCPP, langPy3, langJava}
)

func without(list []string, drop ...string) []string {
	return slices.DeleteFunc(slices.Clone(list), func(s string) bool { return slices.Contains(drop, s) })
}

// testcase is a testcase of a task: files of its data directory.
type testcase struct{ input, output string }

// task is a task of cmstestsuite/tasks.
type task struct {
	dir       string // testdata/tasks/<dir>
	taskType  string
	params    string
	format    []string // submission format
	timeLimit float64
	memoryMiB int64
	managers  []string // files of testdata/tasks/<dir>/code
	// managerPaths replaces some of the managers with other files.
	managerPaths map[string]string
	testcases    []testcase
	points       float64 // per testcase (score type Sum)
}

func tc(pairs ...string) []testcase {
	var t []testcase
	for i := 0; i < len(pairs); i += 2 {
		t = append(t, testcase{pairs[i], pairs[i+1]})
	}
	return t
}

var (
	twoTestcases = tc("1.in", "1.out", "2.in", "2.out")
	graders      = []string{"grader.c", "grader.cpp", "grader.pas", "grader.py", "grader.java"}

	batchStdio = &task{dir: "batch_stdio", taskType: NameBatch, params: `["alone", ["", ""], "diff"]`,
		format: []string{"batchstdio.%l"}, timeLimit: 0.5, memoryMiB: 128, testcases: twoTestcases, points: 50}
	batchFileio = &task{dir: "batch_fileio", taskType: NameBatch, params: `["alone", ["input.txt", "output.txt"], "diff"]`,
		format: []string{"batchfileio.%l"}, timeLimit: 0.5, memoryMiB: 128, testcases: twoTestcases, points: 50}
	batchFileioManaged = &task{dir: "batch_fileio_managed", taskType: NameBatch,
		params: `["grader", ["input.txt", "output.txt"], "comparator"]`,
		format: []string{"batchfileiomanaged.%l"}, timeLimit: 0.5, memoryMiB: 128,
		managers:  append([]string{"task.h", "grader.cs", "checker"}, graders...),
		testcases: twoTestcases, points: 50}
	batchAndOutputTask = &task{dir: "batch_and_output", taskType: NameBatchAndOutput,
		params:    `["alone", ["", ""], "comparator", "000,001"]`,
		format:    []string{"batch_and_output.%l", "output_000.txt", "output_001.txt", "output_002.txt"},
		timeLimit: 0.5, memoryMiB: 128, managers: []string{"checker"},
		testcases: tc("empty", "empty", "empty", "empty", "empty", "empty", "empty", "empty"), points: 25}
	outputOnlyTask = &task{dir: "outputonly", taskType: NameOutputOnly, params: `["diff"]`,
		format: []string{"output_000.txt", "output_001.txt"}, timeLimit: 0.5, memoryMiB: 128,
		testcases: tc("input_000.txt", "output_000.txt", "input_001.txt", "output_001.txt"), points: 50}
	outputOnlyComparator = &task{dir: "outputonly_comparator", taskType: NameOutputOnly, params: `["comparator"]`,
		format: []string{"output_000.txt", "output_001.txt"}, timeLimit: 0.5, memoryMiB: 128,
		managers:  []string{"checker"},
		testcases: tc("input_000.txt", "output_000.txt", "input_001.txt", "output_001.txt"), points: 50}
	communicationStdio = &task{dir: "communication_stdio", taskType: NameCommunication, params: `[1, "alone", "std_io"]`,
		format: []string{"communication.%l"}, timeLimit: 1, memoryMiB: 128, managers: []string{"manager"},
		testcases: twoTestcases, points: 50}
	communicationStdioStubbed = &task{dir: "communication_stdio_stubbed", taskType: NameCommunication,
		params: `[1, "grader", "std_io"]`, format: []string{"communication.%l"}, timeLimit: 1, memoryMiB: 128,
		managers: append([]string{"manager"}, graders...), testcases: twoTestcases, points: 50}
	communicationFifoioStubbed = &task{dir: "communication_fifoio_stubbed", taskType: NameCommunication,
		params: `[1, "grader", "fifo_io"]`, format: []string{"communication.%l"}, timeLimit: 1, memoryMiB: 128,
		managers: append([]string{"manager"}, graders...), testcases: twoTestcases, points: 50}
	communicationManyFifoioStubbed = &task{dir: "communication_many_fifoio_stubbed", taskType: NameCommunication,
		params: `[2, "grader", "fifo_io"]`, format: []string{"user1.%l", "user2.%l"}, timeLimit: 1, memoryMiB: 128,
		managers: append([]string{"manager"}, graders...), testcases: twoTestcases, points: 50}
	communicationManyStdioStubbed = &task{dir: "communication_many_stdio_stubbed", taskType: NameCommunication,
		params: `[2, "grader", "std_io"]`, format: []string{"user1.%l", "user2.%l"}, timeLimit: 1, memoryMiB: 128,
		managers: append([]string{"manager"}, graders...), testcases: twoTestcases, points: 50}
	interactiveTask = &task{dir: "interactive", taskType: NameInteractive,
		params: `[200, "grader", true, 128.0, 1.0, 5.0]`, format: []string{"interactive.%l"}, timeLimit: 1,
		memoryMiB: 128, managers: []string{"controller", "grader.cpp", "grader.py"},
		testcases: tc("input0.txt", "input0.out", "input1.txt", "input1.out"), points: 50}
	interactiveMany = &task{dir: "interactive_many", taskType: NameInteractive,
		params: `[200, "alone", true, 512.0, 1.0, 5.0]`, format: []string{"interactivemany.%l"}, timeLimit: 1,
		memoryMiB: 128, managers: []string{"controller"}, testcases: tc("input0.txt", "input0.out"), points: 100}
	twoStepsTask = &task{dir: "twosteps", taskType: NameTwoSteps, params: `["diff"]`,
		format: []string{"first.%l", "second.%l"}, timeLimit: 1, memoryMiB: 128,
		managers: []string{"manager.c", "manager.h", "first.h", "second.h"}, testcases: twoTestcases, points: 50}
	twoStepsComparator = &task{dir: "twosteps_comparator", taskType: NameTwoSteps, params: `["comparator"]`,
		format: []string{"first.%l", "second.%l"}, timeLimit: 1, memoryMiB: 128,
		managers:  []string{"checker", "manager.c", "manager.h", "first.h", "second.h"},
		testcases: twoTestcases, points: 50}
)

// result is what happened to a submission.
type result struct {
	lang        *language.Language
	compilation *job.Result
	evaluations []*job.Result // one per testcase, unless compilation failed
	score       float64
}

func (r *result) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "compilation %s %q", r.compilation.CompilationOutcome, r.compilation.Text)
	if st := r.compilation.Stats; st != nil && r.compilation.CompilationOutcome != job.CompilationOK {
		fmt.Fprintf(&b, "\nstdout: %s\nstderr: %s", st.Stdout, st.Stderr)
	}
	for i, e := range r.evaluations {
		fmt.Fprintf(&b, "\ntestcase %d: %s %q (admin %q)", i, e.Outcome, e.Text, e.AdminText)
		if st := e.Stats; st != nil {
			fmt.Fprintf(&b, " %s", st.ExitStatus)
			if st.ExecutionTime != nil && st.ExecutionWallClockTime != nil && st.ExecutionMemory != nil {
				fmt.Fprintf(&b, " cpu %.3f s wall %.3f s %d KiB", *st.ExecutionTime, *st.ExecutionWallClockTime, *st.ExecutionMemory>>10)
			}
		}
	}
	return b.String()
}

// check verifies a result (upstream Check*).
type check func(t *testing.T, r *result)

// score: the score of the submission (upstream CheckOverallScore).
func score(want float64) check {
	return func(t *testing.T, r *result) {
		t.Helper()
		if r.compilation.CompilationOutcome != job.CompilationOK || r.score != want {
			t.Errorf("score %v, want %v\n%s", r.score, want, r)
		}
	}
}

// compilationFailed: upstream CheckCompilationFail.
func compilationFailed(t *testing.T, r *result) {
	t.Helper()
	if r.compilation.CompilationOutcome != job.CompilationFail || r.compilation.Executables != nil {
		t.Errorf("the compilation did not fail\n%s", r)
	}
}

// failed: every evaluation scored 0 with a text containing text
// (upstream CheckAbstractEvaluationFailure).
func failed(text string) check {
	return func(t *testing.T, r *result) {
		t.Helper()
		if r.compilation.CompilationOutcome != job.CompilationOK || len(r.evaluations) == 0 {
			t.Fatalf("not evaluated\n%s", r)
		}
		for _, e := range r.evaluations {
			if e.Outcome != "0.0" || !strings.Contains(strings.Join(e.Text, " "), text) {
				t.Errorf("want 0 and %q\n%s", text, r)
				return
			}
		}
	}
}

var (
	timedOut     = failed("Execution timed out")
	wallTimedOut = failed("Execution timed out (wall clock limit exceeded)")
	nonzero      = failed("Execution failed because the return code was nonzero")
)

// memoryLimit: upstream CheckMemoryLimit. The verdict needs cgroups:
// without them memory is limited as address space (the allocation
// fails, a runtime error or a signal follows), and runtimes with a
// large address space run without a limit.
func memoryLimit(t *testing.T, r *result) {
	t.Helper()
	switch {
	case stepstest.Cgroups():
		failed("Memory limit exceeded")(t, r)
	case r.lang.LargeAddressSpace:
		t.Logf("no memory limit without cgroups for %s", r.lang.Name)
	default:
		score(0)(t, r)
	}
}

// test is a test of cmstestsuite/Tests.py: a submission of the files in
// testdata/code (by submission format entry, "" for none, ".%l" replaced
// with the extension of the language) to a task in some languages.
type test struct {
	name      string
	task      *task
	files     []string
	altFiles  map[string][]string
	languages []string // nil: no language (OutputOnly)
	checks    []check
	// userTest also runs a user test with the first input, with the
	// given managers of the task as the user's managers ("grader.%l").
	userTest     bool
	userManagers []string
}

var cmsTests = []test{
	// Correct solutions to batch tasks.
	{name: "correct-stdio", task: batchStdio, files: []string{"correct-stdio.%l"},
		altFiles: map[string][]string{
			langCPP14: {"correct-stdio-cxx14.%l"}, langCPP17: {"correct-stdio-cxx17.%l"}, langCPP20: {"correct-stdio-cxx20.%l"},
		},
		languages: allLanguages, checks: []check{score(100)}},
	{name: "interactive-correct", task: interactiveTask, files: []string{"interactive-correct.%l"},
		languages: []string{langCPP, langCPP14, langCPP17, langCPP20, langPy3, langPyPy3}, checks: []check{score(100)},
		userTest: true, userManagers: []string{"grader.%l"}},
	{name: "interactive-wrong", task: interactiveTask, files: []string{"interactive-wrong.%l"},
		languages: []string{langCPP}, checks: []check{score(0)}},
	{name: "interactive-sleep", task: interactiveTask, files: []string{"interactive-sleep.%l"},
		languages: []string{langCPP}, checks: []check{score(0), wallTimedOut}},
	{name: "interactive-tle", task: interactiveTask, files: []string{"interactive-tle.%l"},
		languages: []string{langCPP}, checks: []check{score(0), timedOut}},
	{name: "interactive-crash", task: interactiveTask, files: []string{"interactive-crash.%l"},
		languages: []string{langCPP}, checks: []check{score(0), nonzero}},
	{name: "interactive-wrong-protocol", task: interactiveTask, files: []string{"interactive-wrong-protocol.%l"},
		languages: []string{langCPP}, checks: []check{score(0)}},
	{name: "interactive-many-correct", task: interactiveMany, files: []string{"interactive-many-correct.%l"},
		languages: []string{langCPP, langCPP14, langCPP17, langCPP20}, checks: []check{score(100)}},
	{name: "interactive-many-wrong", task: interactiveMany, files: []string{"interactive-many-wrong.%l"},
		languages: []string{langCPP}, checks: []check{score(0)}},
	{name: "interactive-many-early-wa", task: interactiveMany, files: []string{"interactive-many-early-wa.%l"},
		languages: []string{langCPP}, checks: []check{score(0)}},
	{name: "interactive-many-sleep", task: interactiveMany, files: []string{"interactive-many-sleep.%l"},
		languages: []string{langCPP}, checks: []check{score(0), wallTimedOut}},
	{name: "interactive-many-tle", task: interactiveMany, files: []string{"interactive-many-tle.%l"},
		languages: []string{langCPP}, checks: []check{score(0), timedOut}},
	{name: "interactive-many-crash", task: interactiveMany, files: []string{"interactive-many-crash.%l"},
		languages: []string{langCPP}, checks: []check{score(0), nonzero}},
	{name: "correct-freopen", task: batchFileio, files: []string{"correct-freopen.%l"},
		languages: []string{langCC}, checks: []check{score(100)}, userTest: true},
	{name: "correct-stdio-inner-class", task: batchStdio, files: []string{"correct-stdio-inner-class.%l"},
		languages: []string{langJava, langCS}, checks: []check{score(100)}},
	{name: "correct-fileio", task: batchFileio, files: []string{"correct-fileio.%l"},
		languages: allLanguages, checks: []check{score(100)}},

	// Various incorrect solutions to batch tasks.
	{name: "incorrect-stdio", task: batchStdio, files: []string{"incorrect-stdio.%l"},
		languages: allLanguages, checks: []check{score(0)}},
	{name: "half-correct-stdio", task: batchStdio, files: []string{"half-correct-stdio.%l"},
		languages: allLanguages, checks: []check{score(50)}},
	{name: "incorrect-fileio", task: batchFileio, files: []string{"incorrect-fileio.%l"},
		languages: allLanguages, checks: []check{score(0)}},
	{name: "half-correct-fileio", task: batchFileio, files: []string{"half-correct-fileio.%l"},
		languages: allLanguages, checks: []check{score(50)}},
	{name: "incorrect-fileio-nooutput", task: batchFileio, files: []string{"incorrect-fileio-nooutput.%l"},
		languages: []string{langCC}, checks: []check{score(0)}},
	{name: "incorrect-fileio-emptyoutput", task: batchFileio, files: []string{"incorrect-fileio-emptyoutput.%l"},
		languages: []string{langCC}, checks: []check{score(0)}},
	{name: "incorrect-fileio-with-stdio", task: batchFileio, files: []string{"incorrect-fileio-with-stdio.%l"},
		languages: allLanguages, checks: []check{score(0)}},

	// OutputOnly tasks.
	{name: "correct-outputonly", task: outputOnlyTask,
		files: []string{"correct-outputonly-000.txt", "correct-outputonly-001.txt"}, checks: []check{score(100)}},
	{name: "incorrect-outputonly", task: outputOnlyTask,
		files: []string{"incorrect-outputonly-000.txt", "incorrect-outputonly-001.txt"}, checks: []check{score(0)}},
	{name: "partial-correct-outputonly", task: outputOnlyTask,
		files: []string{"correct-outputonly-000.txt", "incorrect-outputonly-001.txt"}, checks: []check{score(50)}},
	{name: "correct-outputonly-comparator", task: outputOnlyComparator,
		files: []string{"correct-outputonly-000.txt", "correct-outputonly-001.txt"}, checks: []check{score(100)}},
	{name: "incorrect-outputonly-comparator", task: outputOnlyComparator,
		files: []string{"incorrect-outputonly-000.txt", "incorrect-outputonly-001.txt"}, checks: []check{score(0)}},
	{name: "partial-correct-outputonly-comparator", task: outputOnlyComparator,
		files: []string{"correct-outputonly-000.txt", "incorrect-outputonly-001.txt"}, checks: []check{score(50)}},

	// Failed compilation (PHP's compilation cannot fail).
	{name: "compile-fail", task: batchFileio, files: []string{"compile-fail.%l"},
		languages: without(allLanguages, langPHP), checks: []check{compilationFailed}},
	{name: "compile-timeout", task: batchFileio, files: []string{"compile-timeout.%l"},
		languages: []string{langCPP}, checks: []check{compilationFailed}},
	{name: "compile-memory-limit", task: batchFileio, files: []string{"compile-memory-limit.%l"},
		languages: []string{langCPP}, checks: []check{compilationFailed}},

	// Various timeout conditions.
	{name: "timeout-cputime", task: batchStdio, files: []string{"timeout-cputime.%l"},
		languages: allLanguages, checks: []check{score(0), timedOut}},
	{name: "timeout-pause", task: batchStdio, files: []string{"timeout-pause.%l"},
		languages: []string{langCPP}, checks: []check{score(0), wallTimedOut}},
	{name: "timeout-sleep", task: batchStdio, files: []string{"timeout-sleep.%l"},
		languages: []string{langCPP}, checks: []check{score(0), timedOut}},
	{name: "timeout-sigstop", task: batchStdio, files: []string{"timeout-sigstop.%l"},
		languages: []string{langCPP}, checks: []check{score(0), timedOut}},
	{name: "timeout-select", task: batchStdio, files: []string{"timeout-select.%l"},
		languages: []string{langCPP}, checks: []check{score(0), timedOut}},

	// Nonzero return status.
	{name: "nonzero-return-stdio", task: batchStdio, files: []string{"nonzero-return-stdio.%l"},
		languages: allLanguages, checks: []check{score(0), nonzero}},
	{name: "nonzero-return-fileio", task: batchFileio, files: []string{"nonzero-return-fileio.%l"},
		languages: allLanguages, checks: []check{score(0), nonzero}},

	// OOM problems.
	{name: "oom-static", task: batchStdio, files: []string{"oom-static.%l"},
		languages: []string{langCC, langCPP, langCPP14, langCPP17, langCPP20, langPascal}, checks: []check{memoryLimit}},
	{name: "oom-heap", task: batchStdio, files: []string{"oom-heap.%l"},
		languages: allLanguages, checks: []check{memoryLimit}},

	// Tasks with graders.
	{name: "managed-correct", task: batchFileioManaged, files: []string{"managed-correct.%l"},
		languages: managerLanguages, checks: []check{score(100)},
		userTest: true, userManagers: []string{"grader.%l"}},
	{name: "managed-incorrect", task: batchFileioManaged, files: []string{"managed-incorrect.%l"},
		languages: managerLanguages, checks: []check{score(0)}},

	// Communication tasks.
	{name: "communication-fifoio-correct", task: communicationFifoioStubbed, files: []string{"communication-stubbed-correct.%l"},
		languages: communicationLanguages, checks: []check{score(100)}},
	{name: "communication-fifoio-incorrect", task: communicationFifoioStubbed, files: []string{"communication-stubbed-incorrect.%l"},
		languages: communicationLanguages, checks: []check{score(0)}},
	{name: "communication-stdio-correct", task: communicationStdioStubbed, files: []string{"communication-stubbed-correct.%l"},
		languages: communicationLanguages, checks: []check{score(100)},
		userTest: true, userManagers: []string{"grader.%l"}},
	{name: "communication-stdio-incorrect", task: communicationStdioStubbed, files: []string{"communication-stubbed-incorrect.%l"},
		languages: communicationLanguages, checks: []check{score(0)}},
	{name: "communication-stdio-unstubbed-correct", task: communicationStdio, files: []string{"communication-stdio-correct.%l"},
		languages: communicationLanguages, checks: []check{score(100)}},
	{name: "communication-stdio-unstubbed-incorrect", task: communicationStdio, files: []string{"communication-stdio-incorrect.%l"},
		languages: communicationLanguages, checks: []check{score(0)}},

	// Communication tasks with two processes.
	{name: "communication-many-fifoio-correct", task: communicationManyFifoioStubbed,
		files:     []string{"communication-many-correct-user1.%l", "communication-many-correct-user2.%l"},
		languages: communicationLanguages, checks: []check{score(100)}},
	{name: "communication-many-fifoio-incorrect", task: communicationManyFifoioStubbed,
		files:     []string{"communication-many-incorrect-user1.%l", "communication-many-incorrect-user2.%l"},
		languages: communicationLanguages, checks: []check{score(0)}},
	{name: "communication-many-stdio-correct", task: communicationManyStdioStubbed,
		files:     []string{"communication-many-correct-user1.%l", "communication-many-correct-user2.%l"},
		languages: communicationLanguages, checks: []check{score(100)}},
	{name: "communication-many-stdio-incorrect", task: communicationManyStdioStubbed,
		files:     []string{"communication-many-incorrect-user1.%l", "communication-many-incorrect-user2.%l"},
		languages: communicationLanguages, checks: []check{score(0)}},

	// TwoSteps.
	{name: "twosteps-correct", task: twoStepsTask, files: []string{"twosteps-correct-first.%l", "twosteps-correct-second.%l"},
		languages: []string{langCC}, checks: []check{score(100)}, userTest: true, userManagers: []string{"manager.%l"}},
	{name: "twosteps-half-correct", task: twoStepsTask, files: []string{"twosteps-half-correct-first.%l", "twosteps-correct-second.%l"},
		languages: []string{langCC}, checks: []check{score(50)}},
	{name: "twosteps-incorrect", task: twoStepsTask, files: []string{"twosteps-incorrect-first.%l", "twosteps-correct-second.%l"},
		languages: []string{langCC}, checks: []check{score(0)}},
	{name: "twosteps-comparator-correct", task: twoStepsComparator, files: []string{"twosteps-correct-first.%l", "twosteps-correct-second.%l"},
		languages: []string{langCC}, checks: []check{score(100)}},
	{name: "twosteps-comparator-half-correct", task: twoStepsComparator,
		files:     []string{"twosteps-half-correct-first.%l", "twosteps-correct-second.%l"},
		languages: []string{langCC}, checks: []check{score(50)}},
	{name: "twosteps-comparator-incorrect", task: twoStepsComparator,
		files:     []string{"twosteps-incorrect-first.%l", "twosteps-correct-second.%l"},
		languages: []string{langCC}, checks: []check{score(0)}},

	// BatchAndOutput.
	{name: "batchandoutput-batchonly-correct", task: batchAndOutputTask, files: []string{"output-0-stdio.%l", "", "", ""},
		languages: []string{langCPP}, checks: []check{score(50)}},
	{name: "batchandoutput-batchonly-incorrect", task: batchAndOutputTask, files: []string{"output-1-stdio.%l", "", "", ""},
		languages: []string{langCPP}, checks: []check{score(0)}},
	{name: "batchandoutput-outputonly-correct", task: batchAndOutputTask,
		files: []string{"", "outputonly-1.txt", "outputonly-1.txt", "outputonly-0.txt"}, languages: []string{langCPP},
		checks: []check{score(75)}},
	{name: "batchandoutput-outputonly-incorrect", task: batchAndOutputTask,
		files: []string{"", "outputonly-0.txt", "outputonly-0.txt", "outputonly-1.txt"}, languages: []string{langCPP},
		checks: []check{score(0)}},
	{name: "batchandoutput-mixed-correct", task: batchAndOutputTask,
		files: []string{"output-0-stdio.%l", "outputonly-1.txt", "outputonly-1.txt", "outputonly-0.txt"}, languages: []string{langCPP},
		checks: []check{score(100)}},
	{name: "batchandoutput-mixed-incorrect", task: batchAndOutputTask,
		files: []string{"output-1-stdio.%l", "outputonly-0.txt", "outputonly-0.txt", "outputonly-1.txt"}, languages: []string{langCPP},
		checks: []check{score(0)}},
	{name: "batchandoutput-tle", task: batchAndOutputTask,
		files: []string{"timeout-cputime.%l", "outputonly-1.txt", "outputonly-1.txt", "outputonly-0.txt"}, languages: []string{langCPP},
		checks: []check{score(75)}},
	{name: "batchandoutput-compile-fail", task: batchAndOutputTask,
		files: []string{"compile-fail.%l", "outputonly-1.txt", "outputonly-1.txt", "outputonly-0.txt"}, languages: []string{langCPP},
		checks: []check{compilationFailed}},

	// Writing to files not allowed: the programs write the correct
	// output only if they could write where they should not.
	{name: "write-forbidden-fileio", task: batchFileio, files: []string{"write-forbidden-fileio.%l"},
		languages: []string{langCC}, checks: []check{score(0)}},
	{name: "write-forbidden-stdio", task: batchStdio, files: []string{"write-forbidden-stdio.%l"},
		languages: []string{langCC}, checks: []check{score(0)}},
	{name: "write-forbidden-managed", task: batchFileioManaged, files: []string{"write-forbidden-managed.%l"},
		languages: []string{langCC}, checks: []check{score(0)}},
	{name: "write-forbidden-communication", task: communicationFifoioStubbed, files: []string{"write-forbidden-communication.%l"},
		languages: []string{langCC}, checks: []check{score(0)}},
	// Correct only if it cannot execute output.txt.
	{name: "executing-output", task: batchFileio, files: []string{"executing-output.%l"},
		languages: []string{langCC}, checks: []check{score(100)}},
	// Rewriting the input does not fool the checker.
	{name: "rewrite-input", task: batchFileioManaged, files: []string{"rewrite-input.%l"},
		languages: []string{langCC}, checks: []check{score(0)}},
	{name: "delete-write-input", task: batchFileioManaged, files: []string{"delete-write-input.%l"},
		languages: []string{langCC}, checks: []check{score(0)}},
	// A huge file.
	{name: "write-big-fileio", task: batchFileio, files: []string{"write-big-fileio.%l"},
		languages: []string{langCC}, checks: []check{score(0)}},
}

// judge runs jobs in real boxes, like a worker.
type judge struct {
	env   *Env
	store blob.Store
	langs *language.Registry
	// archive asks for the archives of the boxes of evaluations.
	archive bool
}

func newJudge(t *testing.T) *judge {
	t.Helper()
	s := stepstest.Sandbox(t, true)
	if dir := os.Getenv("CMS_TEST_KEEP_SANDBOX"); dir != "" {
		// Debugging: the boxes stay in dir.
		cfg := s.Config()
		cfg.KeepSandbox, cfg.WorkDir = true, dir
		var err error
		if s, err = sandbox.New(cfg); err != nil {
			t.Fatal(err)
		}
	}
	store, err := blob.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := steps.ConfigFrom(config.Default()) // the real default limits
	cfg.Cgroups = stepstest.Cgroups()
	langs := language.Builtin()
	return &judge{
		env:   &Env{Boxes: SandboxBoxes(s), Store: store, Languages: langs, Steps: &cfg},
		store: store,
		langs: langs,
	}
}

func (j *judge) put(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	d, err := j.store.PutBytes(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func (j *judge) read(t *testing.T, digest string) string {
	t.Helper()
	rc, err := j.store.Open(context.Background(), digest)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// execute runs a job; it must succeed.
func (j *judge) execute(t *testing.T, jb *job.Job) *job.Result {
	t.Helper()
	if err := jb.Validate(); err != nil {
		t.Fatal(err)
	}
	res := jb.NewResult("sandbox-test")
	if err := Execute(context.Background(), j.env, jb, res); err != nil {
		t.Fatalf("%s: %v (sandboxes %v)", jb.Describe(), err, res.Sandboxes)
	}
	if err := res.Validate(); err != nil {
		t.Fatal(err)
	}
	return res
}

// newTaskJob builds a job of a submission of the files in testdata/code
// to a task in a language (nil for none).
func (j *judge) newTaskJob(t *testing.T, tk *task, lang *language.Language, files []string, kind job.Kind) *job.Job {
	t.Helper()
	jb := newJob(kind, tk.taskType, tk.params)
	if lang != nil {
		jb.Language = lang.Name
	}
	for i, f := range files {
		if f == "" {
			continue
		}
		jb.Files[tk.format[i]] = j.put(t, filepath.Join("testdata", "code", f))
	}
	for _, m := range tk.managers {
		path := filepath.Join("testdata", "tasks", tk.dir, "code", m)
		if p, ok := tk.managerPaths[m]; ok {
			path = p
		}
		jb.Managers[m] = j.put(t, path)
	}
	return jb
}

// evaluationJob builds the evaluation job of testcase i of a submission
// that compiled.
func (j *judge) evaluationJob(t *testing.T, tk *task, lang *language.Language, files []string,
	compilation *job.Result, i int) *job.Job {
	t.Helper()
	c := tk.testcases[i]
	jb := j.newTaskJob(t, tk, lang, files, job.Evaluation)
	jb.Operation.TestcaseCodename = fmt.Sprintf("%03d", i)
	jb.Executables = compilation.Executables
	if jb.Executables == nil {
		jb.Executables = map[string]string{}
	}
	jb.Input = j.put(t, filepath.Join("testdata", "tasks", tk.dir, "data", c.input))
	jb.Output = j.put(t, filepath.Join("testdata", "tasks", tk.dir, "data", c.output))
	jb.TimeLimit = tk.timeLimit
	jb.MemoryLimit = tk.memoryMiB << 20
	jb.Operation.ArchiveSandbox = j.archive
	return jb
}

// submit compiles and evaluates a submission of the files of a test
// (testdata/code) in a language (nil for none).
func (j *judge) submit(t *testing.T, tk *task, lang *language.Language, files []string) *result {
	t.Helper()
	r := &result{lang: lang, compilation: j.execute(t, j.newTaskJob(t, tk, lang, files, job.Compilation))}
	if r.compilation.CompilationOutcome != job.CompilationOK {
		return r
	}
	for i := range tk.testcases {
		e := j.execute(t, j.evaluationJob(t, tk, lang, files, r.compilation, i))
		r.evaluations = append(r.evaluations, e)
		v, _, err := e.OutcomeValue()
		if err != nil {
			t.Fatal(err)
		}
		r.score += v * tk.points
	}
	return r
}

// userTest compiles and runs a user test of the files with the first
// input of the task; userManagers are taken from the task. Like
// upstream (Job.from_user_test), the managers are the user's, the auto
// managers of the task type and the headers of the dataset.
func (j *judge) userTest(t *testing.T, tk *task, lang *language.Language, files, userManagers []string) *job.Result {
	t.Helper()
	tt, err := Get(tk.taskType, []byte(tk.params))
	if err != nil {
		t.Fatal(err)
	}
	managers := map[string]string{}
	codeDir := filepath.Join("testdata", "tasks", tk.dir, "code")
	for _, m := range userManagers {
		m = strings.ReplaceAll(m, ".%l", lang.SourceExtension())
		managers[m] = j.put(t, filepath.Join(codeDir, m))
	}
	for _, m := range tt.AutoManagers() {
		m = strings.ReplaceAll(m, ".%l", lang.SourceExtension())
		managers[m] = j.put(t, filepath.Join(codeDir, m))
	}
	for _, m := range tk.managers {
		for _, h := range lang.HeaderExtensions {
			if strings.HasSuffix(m, h) {
				managers[m] = j.put(t, filepath.Join(codeDir, m))
			}
		}
	}
	mk := func(kind job.Kind) *job.Job {
		jb := newJob(kind, tk.taskType, tk.params)
		jb.Language = lang.Name
		for i, f := range files {
			jb.Files[tk.format[i]] = j.put(t, filepath.Join("testdata", "code", f))
		}
		jb.Managers = managers
		return jb
	}
	c := j.execute(t, mk(job.UserTestCompilation))
	if c.CompilationOutcome != job.CompilationOK {
		t.Fatalf("user test compilation: %q\n%s", c.Text, c.Stats.Stderr)
	}
	jb := mk(job.UserTestEvaluation)
	jb.Executables = c.Executables
	jb.Input = j.put(t, filepath.Join("testdata", "tasks", tk.dir, "data", tk.testcases[0].input))
	jb.TimeLimit = tk.timeLimit
	jb.MemoryLimit = tk.memoryMiB << 20
	return j.execute(t, jb)
}

// filesFor returns the files of a test in a language.
func (tst *test) filesFor(lang *language.Language) []string {
	files := tst.files
	if lang == nil {
		return files
	}
	if alt, ok := tst.altFiles[lang.Name]; ok {
		files = alt
	}
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = strings.ReplaceAll(f, ".%l", lang.SourceExtension())
	}
	return out
}

// installedLanguage reports whether the programs a language runs exist.
func installedLanguage(l *language.Language) (bool, string) {
	cmds, _ := l.CompilationCommands([]string{"a" + l.SourceExtension()}, "a", true)
	cmds = append(cmds, l.EvaluationCommands("a", "", nil)...)
	var need []string
	for _, c := range cmds {
		for _, a := range c {
			if strings.HasPrefix(a, "/usr/") {
				need = append(need, a)
			}
		}
	}
	if l.Name == langJava {
		need = append(need, "/usr/bin/jar")
	}
	for _, n := range need {
		if _, err := os.Stat(n); err != nil {
			return false, n
		}
	}
	return true, ""
}

// TestCMSTestSuite runs the functional tests of CMS (cmstestsuite/Tests.py)
// on the task types, in real boxes.
func TestCMSTestSuite(t *testing.T) {
	j := newJudge(t)
	for i := range cmsTests {
		tst := &cmsTests[i]
		langs := []string{""}
		if tst.languages != nil {
			langs = tst.languages
		}
		for _, name := range langs {
			var lang *language.Language
			label := tst.name
			if name != "" {
				label += "/" + name
				l, err := j.langs.Get(name)
				if err != nil {
					t.Fatal(err)
				}
				lang = l
			}
			t.Run(label, func(t *testing.T) {
				if lang != nil {
					if testing.Short() && !slices.Contains(shortLanguages, lang.Name) {
						t.Skip("-short")
					}
					if ok, missing := installedLanguage(lang); !ok {
						t.Skipf("%s is not installed", missing)
					}
				}
				t.Parallel()
				start := time.Now()
				r := j.submit(t, tst.task, lang, tst.filesFor(lang))
				for _, c := range tst.checks {
					c(t, r)
				}
				if tst.userTest && !t.Failed() {
					var files []string
					for _, f := range tst.filesFor(lang) {
						files = append(files, f)
					}
					res := j.userTest(t, tst.task, lang, files, tst.userManagers)
					// upstream CheckUserTestEvaluated
					if res.Outcome != "0.0" || !slices.Equal(res.Text, []string{"Execution completed successfully"}) {
						t.Errorf("user test: %q %s %+v", res.Text, res.Outcome, res.Stats)
					}
					if res.UserOutput != "" {
						t.Logf("user test output: %q", j.read(t, res.UserOutput))
					}
				}
				t.Logf("%s in %v", r, time.Since(start).Round(time.Millisecond))
			})
		}
	}
}
