//go:build sandbox

// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Run with `make test-sandbox` (root, isolate >= 2.0). CMS_SANDBOX_CGROUPS=0
// runs isolate without --cg, for machines without cgroups v2; CI runs
// with cgroups (CMS_SANDBOX_CGROUPS=1), where every verdict is exact.

func isolatePath() string {
	if p := os.Getenv("CMS_ISOLATE"); p != "" {
		return p
	}
	return "isolate"
}

// testCgroups tells whether the tests run isolate with --cg.
func testCgroups() bool {
	v := os.Getenv("CMS_SANDBOX_CGROUPS")
	if v == "" {
		return true
	}
	b, err := strconv.ParseBool(v)
	return err != nil || b
}

// testBoxBase keeps the test boxes away from the ids of a worker that
// may run on the same machine (they would coordinate through the box
// locks anyway).
func testBoxBase() int {
	if v, err := strconv.Atoi(os.Getenv("CMS_SANDBOX_TEST_BOX_BASE")); err == nil {
		return v
	}
	return 900
}

func newTestSandbox(t testing.TB, mut func(*Config)) *Sandbox {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("the sandbox tests run as root (make test-sandbox)")
	}
	cfg := Config{
		IsolatePath:    isolatePath(),
		Cgroups:        testCgroups(),
		BoxIDBase:      testBoxBase(),
		BoxCount:       64,
		WorkDir:        t.TempDir(),
		MaxFileSizeKiB: 64 << 10,
		Seccomp:        true,
	}
	if mut != nil {
		mut(&cfg)
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func newTestBox(t testing.TB, s *Sandbox) *Box {
	t.Helper()
	b, err := s.NewBox(context.Background(), t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := b.Close(); err != nil {
			t.Errorf("closing the box: %v", err)
		}
	})
	return b
}

// gcc compiles a C program on the host, once per test binary.
var gccCache = map[string]string{}

func gcc(t *testing.T, src string, flags ...string) string {
	t.Helper()
	key := src + strings.Join(flags, " ")
	if p, ok := gccCache[key]; ok {
		return p
	}
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc not installed")
	}
	dir, err := os.MkdirTemp("", "cms-sandbox-test-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "prog.c"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "prog")
	args := append([]string{"-O2", "-o", exe, filepath.Join(dir, "prog.c")}, flags...)
	if out, err := exec.Command("gcc", args...).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	gccCache[key] = exe
	return exe
}

// putExe copies an executable into the home of a box.
func putExe(t *testing.T, b *Box, name, src string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.CreateFile(name, data, true); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, b *Box, spec RunSpec) *Result {
	t.Helper()
	res, err := b.Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("run %q: %v (%+v)", spec.Argv, err, res)
	}
	return res
}

func TestIsolateIsUsable(t *testing.T) {
	ctx := context.Background()
	major, minor, err := IsolateVersion(ctx, isolatePath())
	if err != nil {
		t.Fatal(err)
	}
	if major < 2 {
		t.Fatalf("isolate %d.%d found, 2.0 or newer is required", major, minor)
	}
	s := newTestSandbox(t, nil)
	if err := s.SelfTest(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestRelativeDirsAreRejected(t *testing.T) {
	s := newTestSandbox(t, nil)
	b := newTestBox(t, s)
	for _, d := range []Dir{
		{Inside: "/data", Outside: "relative/path"},
		{Inside: "/data", Outside: "./box"},
		{Inside: "data", Outside: "/tmp"},
		{Inside: "/data", Outside: "/tmp/a:rw"},
	} {
		if _, err := b.Run(context.Background(), RunSpec{Argv: []string{"/bin/true"}, Dirs: []Dir{d}}); err == nil ||
			!strings.Contains(err.Error(), "sandbox:") {
			t.Errorf("%+v accepted (%v)", d, err)
		}
	}
	// And isolate itself refuses a relative --dir, which is why the
	// sandbox checks first.
	out, err := exec.Command(isolatePath(), "--box-id="+itoa(b.ID()), "--dir=/x=relative/path", "--run", "--", "/bin/true").CombinedOutput()
	if s.cfg.Cgroups {
		out, err = exec.Command(isolatePath(), "--cg", "--box-id="+itoa(b.ID()), "--dir=/x=relative/path", "--run", "--", "/bin/true").CombinedOutput()
	}
	if err == nil {
		t.Errorf("isolate accepted a relative --dir:\n%s", out)
	}
}

func TestRunBasics(t *testing.T) {
	s := newTestSandbox(t, nil)
	b := newTestBox(t, s)
	if err := b.CreateFile("input.txt", []byte("3 4\n"), false); err != nil {
		t.Fatal(err)
	}
	for _, seccomp := range []bool{false, true} {
		res := run(t, b, RunSpec{
			Argv:           []string{"/bin/sh", "-c", `read a b; echo $((a+b)); echo "$HOME $PWD $FOO" >&2`},
			Stdin:          "input.txt",
			Stdout:         "output.txt",
			Stderr:         "stderr.txt",
			Env:            []string{"FOO=bar"},
			CPUTime:        2,
			WallTime:       5,
			MemoryKiB:      256 << 10,
			Processes:      Unlimited,
			Seccomp:        seccomp,
			RestrictWrites: true,
		})
		if res.Status != StatusOK || res.ExitCode != 0 {
			t.Fatalf("seccomp=%v: %+v", seccomp, res)
		}
		out, trunc, err := b.ReadFile("output.txt", 100)
		if err != nil || trunc || string(out) != "7\n" {
			t.Errorf("seccomp=%v: output %q %v %v", seccomp, out, trunc, err)
		}
		out, _, _ = b.ReadFile("stderr.txt", 100)
		if string(out) != "/tmp /tmp bar\n" {
			t.Errorf("seccomp=%v: environment %q", seccomp, out)
		}
		// isolate reports milliseconds: a run this short can take 0.000 s.
		if res.CPUTime < 0 || res.WallTime < 0 || res.MemoryKiB <= 0 {
			t.Errorf("seccomp=%v: stats %+v", seccomp, res)
		}
	}
	// The worker's environment never reaches the box.
	t.Setenv("CMS_SECRET_KEY", "do-not-leak")
	res := run(t, b, RunSpec{Argv: []string{"/usr/bin/env"}, Stdout: "env.txt"})
	env, _, _ := b.ReadFile("env.txt", 4096)
	if res.Status != StatusOK || strings.Contains(string(env), "do-not-leak") || !strings.Contains(string(env), "PATH="+DefaultPath) {
		t.Errorf("environment of the box: %+v\n%s", res, env)
	}
}

func TestRestrictWrites(t *testing.T) {
	s := newTestSandbox(t, nil)
	b := newTestBox(t, s)
	if err := b.CreateFile("input.txt", []byte("data"), false); err != nil {
		t.Fatal(err)
	}
	res := run(t, b, RunSpec{
		Argv:           []string{"/bin/sh", "-c", `echo x > out.txt || exit 3; echo y > new.txt && exit 4; echo z >> input.txt && exit 5; exit 0`},
		RestrictWrites: true, Writable: []string{"out.txt"},
	})
	if res.Status != StatusOK {
		t.Errorf("restricted writes: %+v", res)
	}
	res = run(t, b, RunSpec{Argv: []string{"/bin/sh", "-c", `echo y > new.txt && echo z >> input.txt`}})
	if res.Status != StatusOK {
		t.Errorf("unrestricted writes: %+v", res)
	}
	data, _, _ := b.ReadFile("input.txt", 100)
	if string(data) != "dataz\n" {
		t.Errorf("input.txt = %q", data)
	}
}

func TestStartKillAndClose(t *testing.T) {
	s := newTestSandbox(t, nil)
	b, err := s.NewBox(context.Background(), "kill")
	if err != nil {
		t.Fatal(err)
	}
	p, err := b.Start(context.Background(), RunSpec{Argv: []string{"/bin/sleep", "60"}})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	p.Kill()
	res, err := p.Wait()
	if err != nil || res.Status != StatusSignal {
		t.Errorf("killed run: %+v %v", res, err)
	}

	// A cancelled context interrupts the run and is not a verdict.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	if res, err := b.Run(ctx, RunSpec{Argv: []string{"/bin/sleep", "60"}}); err == nil {
		t.Errorf("cancelled run returned %+v", res)
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("cancellation took %v", time.Since(start))
	}

	// Close kills what is still running and deletes everything.
	if _, err := b.Start(context.Background(), RunSpec{Argv: []string{"/bin/sleep", "60"}}); err != nil {
		t.Fatal(err)
	}
	dir := b.Dir()
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("box directory left behind: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if _, err := b.Run(context.Background(), RunSpec{Argv: []string{"/bin/true"}}); err == nil {
		t.Error("run in a closed box")
	}
}

func TestKeepSandbox(t *testing.T) {
	s := newTestSandbox(t, func(c *Config) { c.KeepSandbox = true })
	b, err := s.NewBox(context.Background(), "keep")
	if err != nil {
		t.Fatal(err)
	}
	run(t, b, RunSpec{Argv: []string{"/bin/sh", "-c", "echo kept > kept.txt"}})
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(b.Home(), "kept.txt"))
	if err != nil || string(data) != "kept\n" {
		t.Errorf("kept file: %q %v", data, err)
	}
	if log, err := os.ReadFile(filepath.Join(b.Dir(), "commands.log")); err != nil || !strings.Contains(string(log), "--run") {
		t.Errorf("commands.log: %q %v", log, err)
	}
}

// TestPanicReleasesBox: a panic in the middle of a job still releases
// the box when Close is deferred (the worker always does).
func TestPanicReleasesBox(t *testing.T) {
	s := newTestSandbox(t, func(c *Config) { c.BoxCount = 1 })
	var dir string
	func() {
		defer func() { _ = recover() }()
		b, err := s.NewBox(context.Background(), "panic")
		if err != nil {
			t.Fatal(err)
		}
		defer b.Close()
		dir = b.Dir()
		if _, err := b.Start(context.Background(), RunSpec{Argv: []string{"/bin/sleep", "60"}}); err != nil {
			t.Fatal(err)
		}
		panic("job failed")
	}()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("box directory left behind: %v", err)
	}
	// The only id is free again.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b, err := s.NewBox(ctx, "after-panic")
	if err != nil {
		t.Fatal(err)
	}
	b.Close()
}

func TestCommunicationFIFOs(t *testing.T) {
	s := newTestSandbox(t, nil)
	mgr := newTestBox(t, s)
	user := newTestBox(t, s)
	fifos, err := mgr.NewFIFODir("u0_to_m", "m_to_u0")
	if err != nil {
		t.Fatal(err)
	}
	defer fifos.Close()
	ctx := context.Background()
	// The manager sends a number and expects its double back.
	pm, err := mgr.Start(ctx, RunSpec{
		Argv:     []string{"/bin/sh", "-c", `echo 21 > /fifo0/m_to_u0; read x < /fifo0/u0_to_m; echo "$x" > result.txt`},
		Dirs:     []Dir{fifos.Mount("/fifo0")},
		CPUTime:  2,
		WallTime: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	pu, err := user.Start(ctx, RunSpec{
		Argv:           []string{"/bin/sh", "-c", `read x; echo $((x*2))`},
		Stdin:          "/fifo0/m_to_u0",
		Stdout:         "/fifo0/u0_to_m",
		Dirs:           []Dir{fifos.Mount("/fifo0")},
		CPUTime:        2,
		WallTime:       10,
		Seccomp:        true,
		RestrictWrites: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ru, err := pu.Wait()
	if err != nil || ru.Status != StatusOK {
		t.Fatalf("user: %+v %v", ru, err)
	}
	rm, err := pm.Wait()
	if err != nil || rm.Status != StatusOK {
		t.Fatalf("manager: %+v %v", rm, err)
	}
	data, _, _ := mgr.ReadFile("result.txt", 100)
	if string(data) != "42\n" {
		t.Errorf("result %q", data)
	}
}

func TestBoxPoolConcurrency(t *testing.T) {
	s := newTestSandbox(t, func(c *Config) { c.BoxCount = 3 })
	ctx := context.Background()
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			b, err := s.NewBox(ctx, "pool")
			if err != nil {
				errs <- err
				return
			}
			defer b.Close()
			res, err := b.Run(ctx, RunSpec{Argv: []string{"/bin/true"}})
			if err == nil && res.Status != StatusOK {
				err = os.ErrInvalid
			}
			errs <- err
		}()
	}
	for i := 0; i < 8; i++ {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
}

// TestSeccompLauncherCost: the launcher costs the program nothing it could
// notice. Its peak memory is the program's (the kernel and the cgroup keep
// the peak across execve, so a launcher with a runtime would raise it), it
// runs under memory limits the program alone fits in, and it adds well
// under a millisecond of CPU time (the cms binary as launcher added about
// 8 ms and 14 MiB).
func TestSeccompLauncherCost(t *testing.T) {
	s := newTestSandbox(t, nil)
	b := newTestBox(t, s)
	putExe(t, b, "prog", gcc(t, "int main(void) { volatile int a = 3, b = 4; return a + b == 7 ? 0 : 1; }\n", "-static"))
	const runs = 10
	measure := func(seccomp bool, memKiB int64) (mem int64, cpu float64) {
		mem, cpu = -1, -1
		for range runs {
			res := run(t, b, RunSpec{Argv: []string{"./prog"}, CPUTime: 1, WallTime: 5, MemoryKiB: memKiB, Seccomp: seccomp})
			if res.Status != StatusOK {
				t.Fatalf("seccomp=%v, memory limit %d KiB: %+v", seccomp, memKiB, res)
			}
			if mem < 0 || res.MemoryKiB < mem {
				mem = res.MemoryKiB
			}
			if cpu < 0 || res.CPUTime < cpu {
				cpu = res.CPUTime
			}
		}
		return mem, cpu
	}
	memPlain, cpuPlain := measure(false, 64<<10)
	memSeccomp, cpuSeccomp := measure(true, 64<<10)
	t.Logf("static C program (best of %d): %d KiB, %.3f s; under the launcher: %d KiB, %.3f s",
		runs, memPlain, cpuPlain, memSeccomp, cpuSeccomp)
	if memSeccomp > memPlain+256 {
		t.Errorf("the launcher raises the peak memory from %d to %d KiB", memPlain, memSeccomp)
	}
	if cpuSeccomp > cpuPlain+0.002 {
		t.Errorf("the launcher raises the CPU time from %.3f to %.3f s", cpuPlain, cpuSeccomp)
	}
	// The smallest memory limit the program runs in (of a few) is enough
	// for the launcher too.
	ctx := context.Background()
	for _, memKiB := range []int64{2 << 10, 4 << 10, 8 << 10} {
		spec := RunSpec{Argv: []string{"./prog"}, CPUTime: 1, WallTime: 5, MemoryKiB: memKiB}
		if res, err := b.Run(ctx, spec); err != nil || res.Status != StatusOK {
			continue
		}
		spec.Seccomp = true
		if res, err := b.Run(ctx, spec); err != nil || res.Status != StatusOK {
			t.Errorf("the program runs with %d KiB but not under the launcher: %+v %v", memKiB, res, err)
		}
		return
	}
	t.Log("the program does not run with 8 MiB")
}
