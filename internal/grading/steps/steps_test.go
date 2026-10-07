// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// The cases of TestMergeStats, TestExecutionStats and TestCompile* are
// ported from cmstestsuite/unit_tests/grading/steps (stats_test.py,
// compilation_test.py, trusted_test.py) of CMS:
// Copyright © 2018 Stefano Maggiolo <s.maggiolo@gmail.com>

package steps

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/steps/stepstest"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/sandbox"
)

func testConfig() *Config {
	return &Config{
		Compilation: config.SandboxLimit{MaxProcesses: 1000, MaxTimeSec: 10, MaxMemoryKiB: 512 << 10},
		Trusted:     config.SandboxLimit{MaxProcesses: 900, MaxTimeSec: 7, MaxMemoryKiB: 4 << 20},
		Cgroups:     true,
	}
}

func stats(cpu, wall float64, mem int64, status string) job.Stats {
	return job.Stats{ExecutionTime: &cpu, ExecutionWallClockTime: &wall, ExecutionMemory: &mem, ExitStatus: status}
}

func sig(s job.Stats, signal int) job.Stats { s.Signal = signal; return s }

func out(s job.Stats, stdout, stderr string) job.Stats {
	s.Stdout, s.Stderr = stdout, stderr
	return s
}

func assertStats(t *testing.T, got, want job.Stats) {
	t.Helper()
	near := func(a, b *float64) bool {
		return a == nil && b == nil || a != nil && b != nil && math.Abs(*a-*b) < 1e-9
	}
	if !near(got.ExecutionTime, want.ExecutionTime) || !near(got.ExecutionWallClockTime, want.ExecutionWallClockTime) ||
		!reflect.DeepEqual(got.ExecutionMemory, want.ExecutionMemory) || got.ExitStatus != want.ExitStatus ||
		got.Signal != want.Signal || got.Stdout != want.Stdout || got.Stderr != want.Stderr {
		t.Errorf("stats\n got %s\nwant %s", show(got), show(want))
	}
}

func show(s job.Stats) string {
	f := func(p *float64) string {
		if p == nil {
			return "nil"
		}
		return fmt.Sprint(*p)
	}
	m := "nil"
	if s.ExecutionMemory != nil {
		m = fmt.Sprint(*s.ExecutionMemory)
	}
	return fmt.Sprintf("time=%s wall=%s mem=%s status=%q signal=%d stdout=%q stderr=%q",
		f(s.ExecutionTime), f(s.ExecutionWallClockTime), m, s.ExitStatus, s.Signal, s.Stdout, s.Stderr)
}

func TestExecutionStats(t *testing.T) {
	for _, c := range []struct {
		res  sandbox.Result
		want job.Stats
	}{
		{sandbox.Result{Status: sandbox.StatusOK, CPUTime: 0.1, WallTime: 0.5, MemoryKiB: 1000},
			stats(0.1, 0.5, 1000*1024, job.ExitOK)},
		{sandbox.Result{Status: sandbox.StatusSignal, Signal: 11, CPUTime: 0.1, WallTime: 0.5, MemoryKiB: 1000},
			sig(stats(0.1, 0.5, 1000*1024, job.ExitSignal), 11)},
		{sandbox.Result{Status: sandbox.StatusRuntimeError, ExitCode: 3, Signal: 0},
			stats(0, 0, 0, job.ExitNonzeroReturn)},
		{sandbox.Result{Status: sandbox.StatusTimedOut, Signal: 9, Killed: true}, stats(0, 0, 0, job.ExitTimeout)},
		{sandbox.Result{Status: sandbox.StatusWallTimedOut}, stats(0, 0, 0, job.ExitTimeoutWall)},
		{sandbox.Result{Status: sandbox.StatusMemoryLimit, Signal: 9}, stats(0, 0, 0, job.ExitMemoryLimit)},
		{sandbox.Result{Status: sandbox.StatusSecurityViolation, Signal: 31}, stats(0, 0, 0, job.ExitSecurityViolation)},
		{sandbox.Result{Status: sandbox.StatusInternalError}, stats(0, 0, 0, job.ExitSandboxError)},
	} {
		assertStats(t, ExecutionStats(&c.res), c.want)
	}
	// Every status maps to a valid exit status of the protocol.
	for s := sandbox.StatusOK; s <= sandbox.StatusInternalError; s++ {
		if !job.ValidExitStatus(ExitStatus(s)) || ExitStatus(s) != s.String() {
			t.Errorf("status %v -> %q", s, ExitStatus(s))
		}
	}
}

func TestMergeStats(t *testing.T) {
	ok := job.ExitOK
	// Concurrent: times added, wall clock max'd, memory added.
	assertStats(t, MergeStats(job.Ptr(stats(1.0, 2.0, 300, ok)), stats(0.1, 0.2, 3, ok), true),
		stats(1.1, 2.0, 303, ok))
	// Sequential: memory max'd, wall clock added.
	assertStats(t, MergeStats(job.Ptr(stats(1.0, 2.0, 300, ok)), stats(0.1, 0.2, 3, ok), false),
		stats(1.1, 2.2, 300, ok))
	// The first status that is not OK wins.
	zero := func(s string) job.Stats { return stats(0, 0, 0, s) }
	assertStats(t, MergeStats(job.Ptr(zero(ok)), zero(job.ExitTimeout), true), zero(job.ExitTimeout))
	assertStats(t, MergeStats(job.Ptr(zero(ok)), sig(zero(job.ExitSignal), 11), true), sig(zero(job.ExitSignal), 11))
	assertStats(t, MergeStats(job.Ptr(zero(job.ExitTimeout)), sig(zero(job.ExitSignal), 11), true), zero(job.ExitTimeout))
	assertStats(t, MergeStats(job.Ptr(sig(zero(job.ExitSignal), 9)), sig(zero(job.ExitSignal), 11), true), sig(zero(job.ExitSignal), 9))
	assertStats(t, MergeStats(job.Ptr(sig(zero(job.ExitSignal), 9)), zero(ok), true), sig(zero(job.ExitSignal), 9))

	// The inputs are not modified, the result does not alias them.
	r0, r1 := stats(1.0, 2.0, 300, ok), sig(stats(0.1, 0.2, 3, job.ExitSignal), 11)
	m := MergeStats(&r0, r1, true)
	assertStats(t, m, sig(stats(1.1, 2.0, 303, job.ExitSignal), 11))
	assertStats(t, r0, stats(1.0, 2.0, 300, ok))
	assertStats(t, r1, sig(stats(0.1, 0.2, 3, job.ExitSignal), 11))
	m2 := MergeStats(nil, r1, true)
	assertStats(t, m2, r1)
	if m2.ExecutionTime == r1.ExecutionTime {
		t.Error("MergeStats(nil, s) aliases s")
	}

	// Unknown values (nil) are ignored.
	var none job.Stats
	none.ExitStatus = ok
	assertStats(t, MergeStats(&none, stats(1, 2, 3, ok), true), stats(1, 2, 3, ok))

	// Outputs.
	assertStats(t, MergeStats(job.Ptr(out(zero(ok), "o1", "e1")), out(zero(ok), "o2", "e2"), true),
		out(zero(ok), "o1\n===\no2", "e1\n===\ne2"))
	assertStats(t, MergeStats(job.Ptr(out(zero(ok), "o1", "")), out(zero(ok), "", "e2"), true),
		out(zero(ok), "o1\n===\n", "\n===\ne2"))
	assertStats(t, MergeStats(job.Ptr(zero(ok)), zero(ok), true), zero(ok))
}

func TestMessages(t *testing.T) {
	var keys []string
	for _, m := range EvaluationMessages.All() {
		keys = append(keys, m.Shorthand)
	}
	want := []string{"success", "partial", "wrong", "nooutput", "timeout", "walltimeout", "memorylimit", "signal", "returncode", "security"}
	if !slices.Equal(keys, want) {
		t.Errorf("evaluation messages %q", keys)
	}
	keys = keys[:0]
	for _, m := range CompilationMessages.All() {
		keys = append(keys, m.Shorthand)
	}
	if want := []string{"success", "fail", "timeout", "memorylimit", "signal"}; !slices.Equal(keys, want) {
		t.Errorf("compilation messages %q", keys)
	}
	if _, ok := EvaluationMessages.Lookup("nope"); ok {
		t.Error("Lookup(nope)")
	}
	defer func() {
		if recover() == nil {
			t.Error("Get of an unknown message did not panic")
		}
	}()
	EvaluationMessages.Get("nope")
}

func TestEvaluationText(t *testing.T) {
	for status, want := range map[string][]string{
		job.ExitOK:                nil,
		job.ExitSandboxError:      nil,
		job.ExitTimeout:           {"Execution timed out"},
		job.ExitTimeoutWall:       {"Execution timed out (wall clock limit exceeded)"},
		job.ExitSignal:            {"Execution killed by signal"},
		job.ExitMemoryLimit:       {"Memory limit exceeded"},
		job.ExitNonzeroReturn:     {"Execution failed because the return code was nonzero"},
		job.ExitSecurityViolation: {"Execution killed because of a forbidden system call"},
	} {
		st := stats(0, 0, 0, status)
		if got := EvaluationText(&st); !slices.Equal(got, want) {
			t.Errorf("%s: %q, want %q", status, got, want)
		}
	}
}

// scripted returns a RunFunc playing the given results in order, writing
// out and errOut as the outputs of each.
func scripted(results []*sandbox.Result, outs, errOuts []string) stepstest.RunFunc {
	i := 0
	return func(b *stepstest.FakeBox, spec sandbox.RunSpec) (*sandbox.Result, error) {
		defer func() { i++ }()
		if i < len(outs) {
			_ = b.WriteFile(spec.Stdout, []byte(outs[i]))
		}
		if i < len(errOuts) {
			_ = b.WriteFile(spec.Stderr, []byte(errOuts[i]))
		}
		if i >= len(results) {
			return stepstest.OK(), nil
		}
		return results[i], nil
	}
}

func res(status sandbox.Status, cpu, wall float64, memKiB int64) *sandbox.Result {
	return &sandbox.Result{Status: status, CPUTime: cpu, WallTime: wall, MemoryKiB: memKiB}
}

var oneCommand = [][]string{{"/test", "command"}}
var twoCommands = [][]string{{"/test", "command", "1"}, {"/command", "2"}}

func TestCompile(t *testing.T) {
	c := testConfig()
	for _, tc := range []struct {
		name   string
		result *sandbox.Result
		ok     bool
		text   []string
	}{
		{"success", res(sandbox.StatusOK, 0.1, 0.5, 1000), true, []string{"Compilation succeeded"}},
		{"nonzero return", res(sandbox.StatusRuntimeError, 0.1, 0.5, 1000), false, []string{"Compilation failed"}},
		{"timeout", res(sandbox.StatusTimedOut, 0.1, 0.5, 1000), false, []string{"Compilation timed out"}},
		{"wall timeout", res(sandbox.StatusWallTimedOut, 0.1, 0.5, 1000), false, []string{"Compilation timed out"}},
		{"memory limit", res(sandbox.StatusMemoryLimit, 0.1, 0.5, 1000), false, []string{"Compilation memory limit exceeded"}},
		{"signal", &sandbox.Result{Status: sandbox.StatusSignal, Signal: 11, CPUTime: 0.1, WallTime: 0.5, MemoryKiB: 1000},
			false, []string{"Compilation killed with signal %s", "11"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			box := stepstest.New(t, scripted([]*sandbox.Result{tc.result}, []string{"o"}, []string{"你好"}))
			r, err := c.Compile(context.Background(), box, nil, oneCommand)
			if err != nil {
				t.Fatal(err)
			}
			if r.OK != tc.ok || !slices.Equal(r.Text, tc.text) {
				t.Errorf("ok %v text %q, want %v %q", r.OK, r.Text, tc.ok, tc.text)
			}
			want := out(ExecutionStats(tc.result), "o", "你好")
			assertStats(t, r.Stats, want)
			specs := box.Specs()
			if len(specs) != 1 {
				t.Fatalf("%d runs", len(specs))
			}
			s := specs[0]
			if !slices.Equal(s.Argv, oneCommand[0]) || s.Stdout != "compilation_stdout_0.txt" || s.Stderr != "compilation_stderr_0.txt" ||
				s.CPUTime != 10 || s.WallTime != 21 || s.MemoryKiB != 512<<10 || s.Processes != 1000 ||
				s.Seccomp || s.RestrictWrites || len(s.Dirs) != 0 || len(s.Env) != 0 {
				t.Errorf("run spec %+v", s)
			}
		})
	}
}

func TestCompileMultipleCommands(t *testing.T) {
	c := testConfig()
	// Both run; the outputs are joined like upstream (empty ones too).
	box := stepstest.New(t, scripted(nil, []string{"o1", ""}, []string{"", "\xc3\x28e2\n"}))
	r, err := c.Compile(context.Background(), box, nil, twoCommands)
	if err != nil {
		t.Fatal(err)
	}
	if !r.OK || len(box.Specs()) != 2 || box.Specs()[1].Stdout != "compilation_stdout_1.txt" {
		t.Fatalf("result %+v, %d runs", r, len(box.Specs()))
	}
	if r.Stats.Stdout != "o1\n===\n" || r.Stats.Stderr != "\n===\n�(e2" {
		t.Errorf("outputs %q %q", r.Stats.Stdout, r.Stats.Stderr)
	}
	// The first failure stops the step.
	box = stepstest.New(t, scripted([]*sandbox.Result{res(sandbox.StatusRuntimeError, 1, 2, 10)}, []string{"x"}, []string{"error"}))
	r, err = c.Compile(context.Background(), box, nil, twoCommands)
	if err != nil {
		t.Fatal(err)
	}
	if r.OK || len(box.Specs()) != 1 || r.Stats.Stderr != "error" || r.Text[0] != "Compilation failed" {
		t.Errorf("result %+v after %d runs", r, len(box.Specs()))
	}
	// Times add up, memory is the maximum.
	box = stepstest.New(t, scripted([]*sandbox.Result{res(sandbox.StatusOK, 1, 2, 10), res(sandbox.StatusOK, 0.5, 1, 30)}, nil, nil))
	r, _ = c.Compile(context.Background(), box, nil, twoCommands)
	// Empty outputs are joined too (upstream shows "===" for them).
	assertStats(t, r.Stats, out(stats(1.5, 3, 30*1024, job.ExitOK), "\n===\n", "\n===\n"))
}

func TestCompileOutputs(t *testing.T) {
	c := testConfig()
	long := strings.Repeat("é", 600) // 1200 bytes: cut inside a character
	box := stepstest.New(t, func(b *stepstest.FakeBox, spec sandbox.RunSpec) (*sandbox.Result, error) {
		_ = b.WriteFile(spec.Stdout, []byte(" "+long))
		// A compile-time program removed its standard error.
		_ = os.Remove(filepath.Join(b.Dir, spec.Stderr))
		return stepstest.OK(), nil
	})
	r, err := c.Compile(context.Background(), box, nil, oneCommand)
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Repeat("é", 511) + "�"; r.Stats.Stdout != want || r.Stats.Stderr != "" {
		t.Errorf("stdout %q (%d bytes), stderr %q", r.Stats.Stdout, len(r.Stats.Stdout), r.Stats.Stderr)
	}
}

func TestCompileSandboxError(t *testing.T) {
	c := testConfig()
	for _, run := range []stepstest.RunFunc{
		func(*stepstest.FakeBox, sandbox.RunSpec) (*sandbox.Result, error) {
			return &sandbox.Result{Status: sandbox.StatusInternalError}, errors.New("isolate broke")
		},
		func(*stepstest.FakeBox, sandbox.RunSpec) (*sandbox.Result, error) {
			return &sandbox.Result{Status: sandbox.StatusInternalError}, nil
		},
	} {
		box := stepstest.New(t, run)
		if r, err := c.Compile(context.Background(), box, nil, twoCommands); err == nil || r != nil {
			t.Errorf("sandbox failure: %+v, %v", r, err)
		}
		if len(box.Specs()) != 1 {
			t.Errorf("%d runs after a sandbox failure", len(box.Specs()))
		}
	}
	if _, err := c.Compile(context.Background(), stepstest.New(t, nil), nil, nil); err == nil {
		t.Error("no commands: want an error")
	}
}

func TestCompileLanguage(t *testing.T) {
	host := filepath.Join(t.TempDir(), "fpc.cfg")
	if err := os.WriteFile(host, []byte("-Fu/units"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirs := t.TempDir()
	for _, d := range []string{"java-17-openjdk", "java-21-openjdk", "other"} {
		if err := os.Mkdir(filepath.Join(dirs, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Patterns match directories only.
	if err := os.WriteFile(filepath.Join(dirs, "java-file-openjdk"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := language.Parse([]byte(`
name: Test
source_extensions: [.t]
large_address_space: true
compilation:
  commands: [[/c, "{sources}"]]
  dirs: [`+dirs+`/java-*-openjdk, /var/lib/ghc]
  env: [A=1, B=two words]
  files: {.cfg: `+host+`, missing: /nonexistent/file}
  file_locks: true
evaluation:
  commands: [["./{executable}"]]
`), "test.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, cgroups := range []bool{true, false} {
		c := testConfig()
		c.Cgroups = cgroups
		box := stepstest.New(t, nil)
		if _, err := c.Compile(context.Background(), box, l, oneCommand); err != nil {
			t.Fatal(err)
		}
		s := box.Specs()[0]
		wantDirs := []sandbox.Dir{
			{Inside: dirs + "/java-17-openjdk", Optional: true},
			{Inside: dirs + "/java-21-openjdk", Optional: true},
			{Inside: "/var/lib/ghc", Optional: true},
		}
		if !reflect.DeepEqual(s.Dirs, wantDirs) || !slices.Equal(s.Env, []string{"A=1", "B=two words"}) || !s.AllowFileLocks {
			t.Errorf("run spec %+v", s)
		}
		if wantMem := map[bool]int64{true: 512 << 10, false: 0}[cgroups]; s.MemoryKiB != wantMem {
			t.Errorf("cgroups %v: memory %d KiB, want %d", cgroups, s.MemoryKiB, wantMem)
		}
		if box.Read(".cfg") != "-Fu/units" || box.Exists("missing") {
			t.Error("compilation files not copied")
		}
	}
	// A file of the language clashing with one in the box is an error.
	box := stepstest.New(t, nil)
	_ = box.CreateFile(".cfg", nil, false)
	if _, err := testConfig().Compile(context.Background(), box, l, oneCommand); err == nil {
		t.Error("clashing file: want an error")
	}
}

func TestEvaluate(t *testing.T) {
	c := testConfig()
	box := stepstest.New(t, scripted([]*sandbox.Result{
		res(sandbox.StatusRuntimeError, 0.2, 0.3, 10),
		{Status: sandbox.StatusSignal, Signal: 11, CPUTime: 1.5, WallTime: 2, MemoryKiB: 64},
	}, nil, nil))
	st, err := c.Evaluate(context.Background(), box, Evaluation{
		Commands:    [][]string{{"/bin/unpack"}, {"./sol"}},
		TimeLimit:   1.5,
		MemoryLimit: 256 << 20,
		Stdin:       "input.txt",
		Stdout:      "output.txt",
		Writable:    []string{"extra.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Every command runs; the statistics are those of the last.
	assertStats(t, *st, sig(stats(1.5, 2, 64*1024, job.ExitSignal), 11))
	if EvaluationSucceeded(st) || !slices.Equal(EvaluationText(st), []string{"Execution killed by signal"}) {
		t.Errorf("succeeded %v, text %q", EvaluationSucceeded(st), EvaluationText(st))
	}
	specs := box.Specs()
	if len(specs) != 2 {
		t.Fatalf("%d runs", len(specs))
	}
	s := specs[1]
	want := sandbox.RunSpec{
		Argv: []string{"./sol"}, Stdin: "input.txt", Stdout: "output.txt", Stderr: "stderr.txt",
		CPUTime: 1.5, WallTime: 4, MemoryKiB: 256 << 10, Processes: 1,
		Seccomp: true, RestrictWrites: true, Writable: []string{"extra.txt"},
	}
	if !reflect.DeepEqual(s, want) {
		t.Errorf("run spec\n got %+v\nwant %+v", s, want)
	}
}

func TestEvaluationSpec(t *testing.T) {
	r := language.Builtin()
	java, _ := r.Get("Java / JDK")
	hs, _ := r.Get("Haskell / ghc")
	cpp, _ := r.Get("C++17 / g++")
	for _, tc := range []struct {
		name    string
		cgroups bool
		ev      Evaluation
		procs   int
		memKiB  int64
		wall    float64
		seccomp bool
	}{
		{"no limits", true, Evaluation{}, 1, 0, 0, true},
		{"explicit wall", true, Evaluation{TimeLimit: 1, WallTimeLimit: 10}, 1, 0, 10, true},
		{"multiprocess", true, Evaluation{Multiprocess: true, TimeLimit: 2}, MultiprocessLimit, 0, 5, true},
		{"haskell", true, Evaluation{Language: hs, MemoryLimit: 64 << 20}, 2, 64 << 10, 0, true},
		{"java", true, Evaluation{Language: java, MemoryLimit: 64 << 20}, MultiprocessLimit, 64 << 10, 0, true},
		{"java without cgroups", false, Evaluation{Language: java, MemoryLimit: 64 << 20}, MultiprocessLimit, 0, 0, true},
		{"c++ without cgroups", false, Evaluation{Language: cpp, MemoryLimit: 64 << 20}, 1, 64 << 10, 0, true},
		{"trusted manager", true, Evaluation{Trusted: true}, 1, 0, 0, false},
	} {
		c := testConfig()
		c.Cgroups = tc.cgroups
		s, err := c.evaluationSpec(&tc.ev, []string{"./x"})
		if err != nil {
			t.Fatal(err)
		}
		if s.Processes != tc.procs || s.MemoryKiB != tc.memKiB || s.WallTime != tc.wall || s.Seccomp != tc.seccomp {
			t.Errorf("%s: processes %d memory %d wall %g seccomp %v", tc.name, s.Processes, s.MemoryKiB, s.WallTime, s.Seccomp)
		}
	}
	// The language's directories come first, then the step's.
	c := testConfig()
	s, _ := c.evaluationSpec(&Evaluation{Language: hs, Dirs: []sandbox.Dir{{Inside: "/fifo0", Outside: "/tmp/x", Writable: true}},
		Env: []string{"X=1"}}, []string{"./x"})
	if len(s.Dirs) != 1 || s.Dirs[0].Inside != "/fifo0" || !slices.Equal(s.Env, []string{"X=1"}) {
		t.Errorf("dirs %+v env %q", s.Dirs, s.Env)
	}
	for _, ev := range []Evaluation{{TimeLimit: -1}, {WallTimeLimit: -1}, {MemoryLimit: -1}} {
		if _, err := c.evaluationSpec(&ev, []string{"./x"}); err == nil {
			t.Errorf("%+v: want an error", ev)
		}
	}
	if _, err := c.Evaluate(context.Background(), stepstest.New(t, nil), Evaluation{}); err == nil {
		t.Error("no commands: want an error")
	}
	box := stepstest.New(t, func(*stepstest.FakeBox, sandbox.RunSpec) (*sandbox.Result, error) {
		return nil, context.Canceled
	})
	if _, err := c.Evaluate(context.Background(), box, Evaluation{Commands: [][]string{{"./x"}}}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled run: %v", err)
	}
}

func TestRunTrusted(t *testing.T) {
	c := testConfig()
	box := stepstest.New(t, scripted([]*sandbox.Result{{Status: sandbox.StatusOK, CPUTime: 0.1}}, []string{"1.0\n"}, []string{"ok\n"}))
	r, err := c.RunTrusted(context.Background(), box, oneCommand)
	if err != nil {
		t.Fatal(err)
	}
	if !r.OK || r.Stdout != "trusted_stdout_0.txt" || r.Stderr != "trusted_stderr_0.txt" || box.Read(r.Stdout) != "1.0\n" ||
		r.Stats.Stdout != "" {
		t.Errorf("result %+v", r)
	}
	s := box.Specs()[0]
	if s.CPUTime != 7 || s.WallTime != 15 || s.MemoryKiB != 4<<20 || s.Processes != 900 || s.Seccomp || s.RestrictWrites {
		t.Errorf("run spec %+v", s)
	}
	for _, st := range []sandbox.Status{sandbox.StatusRuntimeError, sandbox.StatusTimedOut, sandbox.StatusWallTimedOut,
		sandbox.StatusSignal, sandbox.StatusMemoryLimit} {
		box := stepstest.New(t, scripted([]*sandbox.Result{{Status: st, ExitCode: 3}}, nil, nil))
		r, err := c.RunTrusted(context.Background(), box, twoCommands)
		if err != nil || r.OK || r.ExitCode != 3 || len(box.Specs()) != 1 {
			t.Errorf("%v: %+v %v", st, r, err)
		}
	}
	box = stepstest.New(t, scripted([]*sandbox.Result{{Status: sandbox.StatusInternalError}}, nil, nil))
	if _, err := c.RunTrusted(context.Background(), box, oneCommand); err == nil {
		t.Error("sandbox failure: want an error")
	}
}

func TestConfigFrom(t *testing.T) {
	cfg := config.Default()
	c := ConfigFrom(cfg)
	if c.Compilation != cfg.Sandbox.Compilation || c.Trusted != cfg.Sandbox.Trusted || c.Cgroups != cfg.Sandbox.Cgroups {
		t.Errorf("%+v", c)
	}
}
