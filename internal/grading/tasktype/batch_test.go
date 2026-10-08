// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Several cases follow cmstestsuite/unit_tests/grading/tasktypes/BatchTest.py
// of CMS (Copyright © 2017-2018 Stefano Maggiolo <s.maggiolo@gmail.com>).

package tasktype

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/steps"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/sandbox"
)

const langC = "C11 / gcc"

// fakeCompiler plays gcc: it writes the executable (after -o) with the
// list of its sources, or fails when a source contains "syntax error".
func fakeCompiler(b *fakeBox, spec sandbox.RunSpec) (*sandbox.Result, error) {
	i := slices.Index(spec.Argv, "-o")
	exe := spec.Argv[i+1]
	var sources []string
	for _, a := range spec.Argv[i+2:] {
		if strings.HasSuffix(a, ".c") {
			sources = append(sources, a)
			if strings.Contains(b.Read(a), "syntax error") {
				_ = b.WriteFile(spec.Stderr, []byte(a+":1: error: expected ';'"))
				return exited(sandbox.StatusRuntimeError, 0.1), nil
			}
		}
	}
	return exited(sandbox.StatusOK, 0.2), b.WriteFile(exe, []byte("exe of "+strings.Join(sources, " ")))
}

// echoProgram plays a contestant's program that copies its input to its
// output (stdin/stdout or the given files).
func echoProgram(b *fakeBox, spec sandbox.RunSpec, in, out string) (*sandbox.Result, error) {
	if spec.Stdin != "" {
		in = spec.Stdin
	}
	if spec.Stdout != "" {
		out = spec.Stdout
	}
	return exited(sandbox.StatusOK, 0.3), b.WriteFile(out, []byte(b.Read(in)))
}

func batchCompileJob(fe *fakeEnv, params string, files map[string]string, managers map[string]string) *job.Job {
	j := newJob(job.Compilation, NameBatch, params)
	j.Language = langC
	for k, v := range files {
		j.Files[k] = fe.put(v)
	}
	for k, v := range managers {
		j.Managers[k] = fe.put(v)
	}
	return j
}

func TestBatchCompile(t *testing.T) {
	fe := newFakeEnv(t, func(name string, b *fakeBox, spec sandbox.RunSpec) (*sandbox.Result, error) {
		if name != "compile" || argv0(spec) != "gcc" {
			t.Errorf("unexpected run %s %q", name, spec.Argv)
		}
		return fakeCompiler(b, spec)
	})

	// Alone: only the source (upstream test_alone_success).
	j := batchCompileJob(fe, `["alone", ["", ""], "diff"]`, map[string]string{"foo.%l": "int main;"},
		map[string]string{"grader.c": "grader", "checker": "chk"})
	res := fe.mustExecute(j)
	if res.CompilationOutcome != job.CompilationOK || len(res.Executables) != 1 {
		t.Fatalf("result %+v", res)
	}
	equalText(t, "alone", res.Text, []string{"Compilation succeeded"})
	if got := fe.get(res.Executables["foo"]); got != "exe of foo.c" {
		t.Errorf("executable %q", got)
	}
	if res.Stats == nil || res.Stats.ExitStatus != job.ExitOK || *res.Stats.ExecutionTime != 0.2 {
		t.Errorf("stats %+v", res.Stats)
	}
	// Managers that are files of the language are copied, even alone
	// (upstream is_manager_for_compilation).
	box := fe.box(0)
	if !box.Exists("grader.c") || box.Exists("checker") || !box.Exists("foo.c") {
		t.Error("alone: wrong files in the box")
	}
	if len(res.Sandboxes) != 1 || res.Sandboxes[0] != box.Dir() {
		t.Errorf("sandboxes %v", res.Sandboxes)
	}

	// Two files (upstream test_alone_success_two_files).
	j = batchCompileJob(fe, `["alone", ["", ""], "diff"]`, map[string]string{"foo.%l": "a", "bar.%l": "b"}, nil)
	res = fe.mustExecute(j)
	if got := fe.get(res.Executables["bar_foo"]); got != "exe of bar.c foo.c" {
		t.Errorf("executable %q (%v)", got, res.Executables)
	}

	// Grader: the grader first, headers copied, other graders not
	// (upstream test_grader_success).
	j = batchCompileJob(fe, `["grader", ["", ""], "diff"]`, map[string]string{"foo.%l": "f"},
		map[string]string{"grader.c": "g", "grader.cpp": "g++", "grader.h": "h", "task.o": "obj"})
	res = fe.mustExecute(j)
	if got := fe.get(res.Executables["foo"]); got != "exe of grader.c foo.c" {
		t.Errorf("executable %q", got)
	}
	box = fe.box(2)
	for name, want := range map[string]bool{"grader.c": true, "grader.h": true, "task.o": true, "foo.c": true, "grader.cpp": false} {
		if box.Exists(name) != want {
			t.Errorf("grader: %s in the box: %v", name, !want)
		}
	}

	// Compilation failure: the contestant's fault (upstream
	// test_alone_compilation_failure).
	j = batchCompileJob(fe, `["alone", ["", ""], "diff"]`, map[string]string{"foo.%l": "syntax error"}, nil)
	res = fe.mustExecute(j)
	if res.CompilationOutcome != job.CompilationFail || res.Executables != nil {
		t.Fatalf("result %+v", res)
	}
	equalText(t, "failure", res.Text, []string{"Compilation failed"})
	if !strings.Contains(res.Stats.Stderr, "expected ';'") || res.Stats.ExitStatus != job.ExitNonzeroReturn {
		t.Errorf("stats %+v", res.Stats)
	}
	fe.allClosed()

	// Configuration errors: no box (upstream test_alone_failure_missing_file,
	// test_grader_failure_missing_grader).
	n := len(fe.names())
	for _, j := range []*job.Job{
		batchCompileJob(fe, `["alone", ["", ""], "diff"]`, nil, nil),
		batchCompileJob(fe, `["grader", ["", ""], "diff"]`, map[string]string{"foo.%l": "f"}, map[string]string{"grader.cpp": "g"}),
	} {
		res, err := fe.execute(j)
		if !errors.Is(err, ErrConfiguration) || res.Success || res.CompilationOutcome != "" || res.Text != nil {
			t.Errorf("%v: %v %+v", j.Files, err, res)
		}
	}
	if len(fe.names()) != n {
		t.Error("a box was created for a misconfigured job")
	}
	// An unknown language.
	j = batchCompileJob(fe, `["alone", ["", ""], "diff"]`, map[string]string{"foo.%l": "f"}, nil)
	j.Language = "Brainfuck"
	if _, err := fe.execute(j); !errors.Is(err, ErrConfiguration) {
		t.Errorf("unknown language: %v", err)
	}
}

// batchEvalJob is an evaluation of Batch with the executable "foo".
func batchEvalJob(fe *fakeEnv, params, input, output string) *job.Job {
	j := newJob(job.Evaluation, NameBatch, params)
	j.Language = langC
	j.Executables["foo"] = fe.put("exe of foo.c")
	j.Input = fe.put(input)
	j.Output = fe.put(output)
	j.TimeLimit = 2.5
	j.MemoryLimit = 123 << 20
	j.MultithreadedSandbox = true
	return j
}

func TestBatchEvaluate(t *testing.T) {
	var program func(b *fakeBox, spec sandbox.RunSpec) (*sandbox.Result, error)
	fe := newFakeEnv(t, func(name string, b *fakeBox, spec sandbox.RunSpec) (*sandbox.Result, error) {
		if name != "evaluate" {
			t.Errorf("unexpected run in %s: %q", name, spec.Argv)
		}
		return program(b, spec)
	})

	// stdio + diff (upstream test_stdio_diff_success).
	program = func(b *fakeBox, spec sandbox.RunSpec) (*sandbox.Result, error) {
		if !slices.Equal(spec.Argv, []string{"./foo"}) || spec.Stdin != "input.txt" || spec.Stdout != "output.txt" ||
			len(spec.Writable) != 0 || spec.CPUTime != 2.5 || spec.MemoryKiB != 123<<10 ||
			spec.Processes != steps.MultiprocessLimit || !spec.Seccomp || !spec.RestrictWrites {
			t.Errorf("spec %+v", spec)
		}
		if st, err := b.Stat("foo"); err != nil || st.Mode()&0o100 == 0 {
			t.Errorf("the executable is not executable: %v", err)
		}
		return echoProgram(b, spec, "", "")
	}
	j := batchEvalJob(fe, `["alone", ["", ""], "diff"]`, "1 2\n", "1  2")
	res := fe.mustExecute(j)
	if res.Outcome != "1.0" || res.UserOutput != "" || res.AdminText != "" {
		t.Errorf("result %+v", res)
	}
	equalText(t, "stdio", res.Text, []string{"Output is correct"})
	if res.Stats == nil || res.Stats.ExitStatus != job.ExitOK || *res.Stats.ExecutionTime != 0.3 ||
		*res.Stats.ExecutionMemory != 1<<20 {
		t.Errorf("stats %+v", res.Stats)
	}
	if len(fe.names()) != 1 {
		t.Errorf("boxes %v", fe.names())
	}

	// Wrong answer.
	j = batchEvalJob(fe, `["alone", ["", ""], "diff"]`, "1 2\n", "1 3\n")
	res = fe.mustExecute(j)
	if res.Outcome != "0.0" || res.AdminText == "" {
		t.Errorf("result %+v", res)
	}
	equalText(t, "wrong", res.Text, []string{"Output isn't correct"})

	// File I/O (upstream test_fileio_diff_success).
	program = func(b *fakeBox, spec sandbox.RunSpec) (*sandbox.Result, error) {
		if spec.Stdin != "" || spec.Stdout != "" || !slices.Equal(spec.Writable, []string{"myout"}) {
			t.Errorf("spec %+v", spec)
		}
		return echoProgram(b, spec, "myin", "myout")
	}
	j = batchEvalJob(fe, `["alone", ["myin", "myout"], "exact"]`, "42\n", "42\n")
	res = fe.mustExecute(j)
	if res.Outcome != "1.0" {
		t.Errorf("result %+v", res)
	}

	// No output file.
	program = func(b *fakeBox, spec sandbox.RunSpec) (*sandbox.Result, error) {
		return exited(sandbox.StatusOK, 0.1), nil
	}
	res = fe.mustExecute(batchEvalJob(fe, `["alone", ["myin", "myout"], "diff"]`, "1", "1"))
	if res.Outcome != "0.0" {
		t.Errorf("result %+v", res)
	}
	equalText(t, "no output", res.Text, []string{"Evaluation didn't produce file %s", "myout"})

	// A link instead of the output: no output (upstream eval_output).
	program = func(b *fakeBox, spec sandbox.RunSpec) (*sandbox.Result, error) {
		return exited(sandbox.StatusOK, 0.1), symlink(b, "input.txt", "output.txt")
	}
	res = fe.mustExecute(batchEvalJob(fe, `["alone", ["", ""], "diff"]`, "1", "1"))
	equalText(t, "link", res.Text, []string{"Evaluation didn't produce file %s", ""})

	// The contestant's failures (upstream
	// test_stdio_diff_evaluation_step_submission_failure_).
	for st, want := range map[sandbox.Status]string{
		sandbox.StatusRuntimeError:      "Execution failed because the return code was nonzero",
		sandbox.StatusTimedOut:          "Execution timed out",
		sandbox.StatusWallTimedOut:      "Execution timed out (wall clock limit exceeded)",
		sandbox.StatusMemoryLimit:       "Memory limit exceeded",
		sandbox.StatusSignal:            "Execution killed by signal",
		sandbox.StatusSecurityViolation: "Execution killed because of a forbidden system call",
	} {
		program = func(b *fakeBox, spec sandbox.RunSpec) (*sandbox.Result, error) {
			_, _ = echoProgram(b, spec, "", "")
			return exited(st, 2.6), nil
		}
		res = fe.mustExecute(batchEvalJob(fe, `["alone", ["", ""], "diff"]`, "1", "1"))
		if res.Outcome != "0.0" || res.Stats.ExitStatus != steps.ExitStatus(st) {
			t.Errorf("%v: result %+v %+v", st, res, res.Stats)
		}
		equalText(t, st.String(), res.Text, []string{want})
	}

	// Sandbox failure: the job fails (upstream
	// test_stdio_diff_evaluation_step_sandbox_failure_).
	program = func(b *fakeBox, spec sandbox.RunSpec) (*sandbox.Result, error) {
		return &sandbox.Result{Status: sandbox.StatusInternalError, Message: "isolate broke"}, nil
	}
	res, err := fe.execute(batchEvalJob(fe, `["alone", ["", ""], "diff"]`, "1", "1"))
	if err == nil || res.Success || res.Outcome != "" || res.Stats != nil || res.Text != nil {
		t.Errorf("sandbox failure: %v %+v", err, res)
	}

	// Wrong number of executables: no box (upstream
	// test_stdio_diff_failure_missing_file, _two_files).
	n := len(fe.names())
	for _, exes := range []map[string]string{{}, {"foo": fe.put("a"), "foo2": fe.put("b")}} {
		j := batchEvalJob(fe, `["alone", ["", ""], "diff"]`, "1", "1")
		j.Executables = exes
		if _, err := fe.execute(j); !errors.Is(err, ErrConfiguration) {
			t.Errorf("%d executables: %v", len(exes), err)
		}
	}
	if len(fe.names()) != n {
		t.Error("a box was created for a misconfigured job")
	}
	fe.allClosed()

	// A tombstone: the job fails with Tombstone set.
	j = batchEvalJob(fe, `["alone", ["", ""], "diff"]`, "1", "1")
	j.Input = job.TombstoneDigest
	res, err = fe.execute(j)
	if !errors.Is(err, ErrTombstone) || !res.Tombstone || res.Success {
		t.Errorf("tombstone: %v %+v", err, res)
	}
	j = batchEvalJob(fe, `["alone", ["", ""], "diff"]`, "1", "1")
	j.Executables = map[string]string{"foo": job.TombstoneDigest}
	if res, err = fe.execute(j); !errors.Is(err, ErrTombstone) || !res.Tombstone {
		t.Errorf("tombstone executable: %v %+v", err, res)
	}
}

func symlink(b *fakeBox, target, name string) error {
	p := filepath.Join(b.FakeBox.Dir, name)
	_ = os.Remove(p)
	return os.Symlink(target, p)
}

func TestBatchUserTest(t *testing.T) {
	ran := 0
	fe := newFakeEnv(t, func(name string, b *fakeBox, spec sandbox.RunSpec) (*sandbox.Result, error) {
		ran++
		return echoProgram(b, spec, "", "")
	})
	// Only executed, the output is returned (upstream
	// test_stdio_diff_only_execution_success, _get_output_success).
	j := batchEvalJob(fe, `["alone", ["", ""], "comparator"]`, strings.Repeat("x", maxUserOutput+10), "")
	j.Operation = job.Operation{Type: job.UserTestEvaluation, ObjectID: 3, DatasetID: 1}
	j.Output, j.OnlyExecution, j.GetOutput = "", true, true
	res := fe.mustExecute(j)
	if ran != 1 || res.Outcome != "0.0" || res.UserOutput == "" {
		t.Fatalf("result %+v", res)
	}
	equalText(t, "user test", res.Text, []string{"Execution completed successfully"})
	if got := fe.get(res.UserOutput); got != strings.Repeat("x", maxUserOutput) {
		t.Errorf("user output of %d bytes, want the first %d", len(got), maxUserOutput)
	}
	// A failed run returns no output.
	fe.run = func(name string, b *fakeBox, spec sandbox.RunSpec) (*sandbox.Result, error) {
		_, _ = echoProgram(b, spec, "", "")
		return exited(sandbox.StatusTimedOut, 3), nil
	}
	res = fe.mustExecute(j)
	if res.UserOutput != "" {
		t.Errorf("result %+v", res)
	}
	equalText(t, "user test TLE", res.Text, []string{"Execution timed out"})
}

// comparator plays a checker program with the CMS protocol: it reads its
// arguments and answers with outcome and text.
func comparator(t *testing.T, outcome, text string, wantExtra ...string) func(b *fakeBox, spec sandbox.RunSpec) (*sandbox.Result, error) {
	return func(b *fakeBox, spec sandbox.RunSpec) (*sandbox.Result, error) {
		want := append([]string{"./checker", "input.txt", "correct_output.txt", "user_output.txt"}, wantExtra...)
		if !slices.Equal(spec.Argv, want) || spec.Seccomp {
			t.Errorf("checker run %q, want %q", spec.Argv, want)
		}
		if b.Read("checker") != "checker program" || b.Read("user_output.txt") == "" {
			t.Errorf("checker box: checker %q, output %q", b.Read("checker"), b.Read("user_output.txt"))
		}
		_ = b.WriteFile(spec.Stdout, []byte(outcome+"\n"))
		_ = b.WriteFile(spec.Stderr, []byte(text+"\nADMIN_MESSAGE: input "+b.Read("input.txt")+"\n"))
		return exited(sandbox.StatusOK, 0.01), nil
	}
}

func TestBatchComparator(t *testing.T) {
	check := comparator(t, "0.5", "translate:partial")
	fe := newFakeEnv(t, func(name string, b *fakeBox, spec sandbox.RunSpec) (*sandbox.Result, error) {
		switch name {
		case "evaluate":
			return echoProgram(b, spec, "", "")
		case "check":
			return check(b, spec)
		}
		t.Errorf("unexpected box %s", name)
		return nil, nil
	})
	j := batchEvalJob(fe, `["alone", ["", ""], "comparator"]`, "in", "out")
	j.Managers["checker"] = fe.put("checker program")
	res := fe.mustExecute(j)
	if res.Outcome != "0.5" || res.AdminText != "input in" {
		t.Errorf("result %+v", res)
	}
	equalText(t, "comparator", res.Text, []string{"Output is partially correct"})
	// The statistics are those of the contestant's program.
	if *res.Stats.ExecutionTime != 0.3 {
		t.Errorf("stats %+v", res.Stats)
	}
	if got := fe.names(); !slices.Equal(got, []string{"evaluate", "check"}) || len(res.Sandboxes) != 2 {
		t.Errorf("boxes %v, sandboxes %v", got, res.Sandboxes)
	}
	fe.allClosed()

	// A broken checker fails the job.
	check = func(b *fakeBox, spec sandbox.RunSpec) (*sandbox.Result, error) {
		return exited(sandbox.StatusRuntimeError, 0.01), nil
	}
	if _, err := fe.execute(j); err == nil {
		t.Error("a failing checker gave a result")
	}
	check = func(b *fakeBox, spec sandbox.RunSpec) (*sandbox.Result, error) {
		_ = b.WriteFile(spec.Stdout, []byte("lots\n"))
		return exited(sandbox.StatusOK, 0.01), nil
	}
	if _, err := fe.execute(j); err == nil {
		t.Error("a checker that breaks the protocol gave a result")
	}

	// The checker is missing.
	delete(j.Managers, "checker")
	if _, err := fe.execute(j); !errors.Is(err, ErrConfiguration) {
		t.Errorf("missing checker: %v", err)
	}

	// testlib: verdict in the exit code, arguments in testlib's order.
	check = func(b *fakeBox, spec sandbox.RunSpec) (*sandbox.Result, error) {
		if !slices.Equal(spec.Argv, []string{"./checker", "input.txt", "user_output.txt", "correct_output.txt"}) {
			t.Errorf("testlib run %q", spec.Argv)
		}
		_ = b.WriteFile(spec.Stderr, []byte("wrong answer 1st numbers differ"))
		r := exited(sandbox.StatusRuntimeError, 0.01)
		r.ExitCode = 1
		return r, nil
	}
	j = batchEvalJob(fe, `["alone", ["", ""], "testlib"]`, "in", "out")
	j.Managers["checker"] = fe.put("checker program")
	res = fe.mustExecute(j)
	if res.Outcome != "0.0" || !strings.Contains(res.AdminText, "1st numbers differ") {
		t.Errorf("testlib: %+v", res)
	}
}

func TestBatchRealNumbers(t *testing.T) {
	fe := newFakeEnv(t, func(name string, b *fakeBox, spec sandbox.RunSpec) (*sandbox.Result, error) {
		return echoProgram(b, spec, "", "")
	})
	res := fe.mustExecute(batchEvalJob(fe, `["alone", ["", ""], "real"]`, "3.1415926 x", "3.14159265 x"))
	if res.Outcome != "1.0" {
		t.Errorf("result %+v", res)
	}
	res = fe.mustExecute(batchEvalJob(fe, `["alone", ["", ""], "real"]`, "3.1416 x", "3.14159265 x"))
	if res.Outcome != "0.0" {
		t.Errorf("result %+v", res)
	}
}
