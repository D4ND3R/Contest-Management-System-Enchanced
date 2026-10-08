// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package sandbox

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
)

func TestParseMetaAndClassify(t *testing.T) {
	cases := []struct {
		name    string
		meta    string
		exit    int
		seccomp bool
		want    Status
		wantErr bool
		check   func(*Result) bool
	}{
		{name: "ok", meta: "time:0.012\ntime-wall:0.020\nmax-rss:1500\ncsw-voluntary:1\ncsw-forced:0\nexitcode:0\n", want: StatusOK,
			check: func(r *Result) bool { return r.CPUTime == 0.012 && r.WallTime == 0.02 && r.MemoryKiB == 1500 }},
		{name: "cg-mem wins over max-rss", meta: "time:0.1\nmax-rss:1500\ncg-mem:4096\nexitcode:0\n", want: StatusOK,
			check: func(r *Result) bool { return r.MemoryKiB == 4096 }},
		{name: "runtime error", meta: "exitcode:3\nstatus:RE\nmessage:Exited with error status 3\n", exit: 1, want: StatusRuntimeError,
			check: func(r *Result) bool { return r.ExitCode == 3 }},
		{name: "signal", meta: "exitsig:11\nstatus:SG\nmessage:Caught fatal signal 11\n", exit: 1, want: StatusSignal,
			check: func(r *Result) bool { return r.Signal == 11 }},
		{name: "SIGSYS without seccomp", meta: "exitsig:31\nstatus:SG\n", exit: 1, want: StatusSignal},
		{name: "SIGSYS with seccomp", meta: "exitsig:31\nstatus:SG\n", exit: 1, seccomp: true, want: StatusSecurityViolation},
		{name: "other signal with seccomp", meta: "exitsig:9\nstatus:SG\n", exit: 1, seccomp: true, want: StatusSignal},
		{name: "timeout", meta: "time:1.004\nkilled:1\nstatus:TO\nmessage:Time limit exceeded\n", exit: 1, want: StatusTimedOut,
			check: func(r *Result) bool { return r.Killed }},
		{name: "wall timeout", meta: "status:TO\nmessage:Time limit exceeded (wall clock)\nkilled:1\n", exit: 1, want: StatusWallTimedOut},
		{name: "timeout beats OOM", meta: "status:TO\ncg-oom-killed:1\nmessage:Time limit exceeded\n", exit: 1, want: StatusTimedOut},
		{name: "OOM killed the program", meta: "exitsig:9\nstatus:SG\ncg-oom-killed:1\ncg-mem:65536\n", exit: 1, want: StatusMemoryLimit},
		{name: "OOM killed a child", meta: "exitcode:1\nstatus:RE\ncg-oom-killed:1\n", exit: 1, want: StatusMemoryLimit},
		{name: "OOM beats seccomp", meta: "exitsig:31\nstatus:SG\ncg-oom-killed:1\n", exit: 1, seccomp: true, want: StatusMemoryLimit},
		{name: "sandbox error", meta: "status:XX\nmessage:execve(\"/x\"): No such file or directory\n", exit: 2, want: StatusInternalError, wantErr: true},
		{name: "exit 2 without status", meta: "time:0.0\n", exit: 2, want: StatusInternalError, wantErr: true},
		{name: "isolate killed", meta: "", exit: -1, want: StatusInternalError, wantErr: true},
		{name: "exit 1 without status", meta: "time:0.0\n", exit: 1, want: StatusInternalError, wantErr: true},
		{name: "unknown status", meta: "status:ZZ\n", exit: 1, want: StatusInternalError, wantErr: true},
		{name: "garbage number", meta: "time:fast\n", want: StatusInternalError, wantErr: true},
	}
	for _, tc := range cases {
		m, err := parseMeta([]byte(tc.meta))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		res, err := classify(m, tc.exit, tc.seccomp)
		if (err != nil) != tc.wantErr || res == nil || res.Status != tc.want {
			t.Errorf("%s: got %+v, %v; want %s (error %v)", tc.name, res, err, tc.want, tc.wantErr)
			continue
		}
		if tc.check != nil && !tc.check(res) {
			t.Errorf("%s: %+v", tc.name, res)
		}
	}
	if _, err := parseMeta([]byte("no colon here\n")); err == nil {
		t.Error("malformed meta accepted")
	}
	if m, _ := parseMeta([]byte("status:RE\nstatus:SG\n")); len(m["status"]) != 2 {
		t.Errorf("repeated keys: %v", m)
	}
}

func TestStatusStrings(t *testing.T) {
	for s, want := range map[Status]string{
		StatusOK: "ok", StatusRuntimeError: "nonzero return", StatusSignal: "signal",
		StatusTimedOut: "timeout", StatusWallTimedOut: "wall timeout", StatusMemoryLimit: "memory limit exceeded",
		StatusSecurityViolation: "security violation", StatusInternalError: "sandbox error",
	} {
		if s.String() != want {
			t.Errorf("%d: %q, want %q", s, s.String(), want)
		}
	}
}

// fakeBox is a box whose isolate was never called, for argument and file
// tests.
func fakeBox(t *testing.T, cfg Config) *Box {
	t.Helper()
	if cfg.MaxFileSizeKiB == 0 {
		cfg.MaxFileSizeKiB = 1 << 20
	}
	if cfg.IsolatePath == "" {
		cfg.IsolatePath = "isolate"
	}
	outer := t.TempDir()
	home := filepath.Join(outer, "home")
	if err := os.Mkdir(home, 0o777); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	s := &Sandbox{cfg: cfg, launcherDir: "/var/lib/cms/worker/launcher"}
	return &Box{s: s, id: 7, outer: outer, home: home, root: root, running: map[*Process]struct{}{}}
}

func flagValue(args []string, flag string) []string {
	var out []string
	for _, a := range args {
		if a == "--run" {
			break
		}
		if v, ok := strings.CutPrefix(a, flag+"="); ok {
			out = append(out, v)
		} else if a == flag {
			out = append(out, "")
		}
	}
	return out
}

func TestArgs(t *testing.T) {
	b := fakeBox(t, Config{Cgroups: true, Seccomp: true, MaxFileSizeKiB: 1024})
	args, err := b.args(&RunSpec{
		Argv: []string{"./prog", "a b"}, Stdin: "input.txt", Stdout: "/fifo0/out", Env: []string{"FOO=1", "PATH=/bin"},
		Dirs:    []Dir{{Inside: "/fifo0", Outside: "/var/lib/cms/fifo-1", Writable: true}, {Inside: "/opt/x", NoExec: true, Optional: true}},
		CPUTime: 1.5, WallTime: 4, ExtraTime: 0.25, MemoryKiB: 65536, StackKiB: 8192, OpenFiles: 64,
		Chdir: "sub",
	}, "/meta")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"--cg":         {""},
		"--box-id":     {"7"},
		"--meta":       {"/meta"},
		"--chdir":      {"/tmp/sub"},
		"--fsize":      {"1024"},
		"--stack":      {"8192"},
		"--open-files": {"64"},
		"--processes":  {"1"},
		"--time":       {"1.500"},
		"--wall-time":  {"4.000"},
		"--extra-time": {"0.250"},
		"--cg-mem":     {"65536"},
		"--mem":        nil,
		"--stdin":      {"/tmp/input.txt"},
		"--stdout":     {"/fifo0/out"},
		"--stderr":     {"/dev/null"},
		"--env":        {"HOME=/tmp", "PATH=/bin", "FOO=1"},
		"--dir": {"/tmp=" + b.home + ":rw", "/box=./box", "/etc/alternatives=/etc/alternatives:maybe", "/opt/pypy3=/opt/pypy3:maybe",
			"/etc/mono=/etc/mono:noexec:maybe", "/fifo0=/var/lib/cms/fifo-1:rw", "/opt/x=/opt/x:noexec:maybe"},
		"--syscalls": nil,
	}
	for flag, w := range want {
		if got := flagValue(args, flag); !slices.Equal(got, w) {
			t.Errorf("%s: %q, want %q", flag, got, w)
		}
	}
	if tail := args[len(args)-4:]; !slices.Equal(tail, []string{"--run", "--", "./prog", "a b"}) {
		t.Errorf("command: %q", tail)
	}

	// Seccomp puts the launcher in front of the command and changes
	// nothing else: the program gets its limits and environment from
	// isolate.
	b = fakeBox(t, Config{Cgroups: false, Seccomp: true})
	spec := RunSpec{Argv: []string{"./prog", "x"}, Env: []string{"GOGC=50"}, MemoryKiB: 1000, Processes: 3, Seccomp: true, AllowFileLocks: true}
	args, err = b.args(&spec, "/meta")
	if err != nil {
		t.Fatal(err)
	}
	for flag, w := range map[string][]string{
		"--cg":        nil,
		"--mem":       {"1000"},
		"--cg-mem":    nil,
		"--processes": {"3"},
		"--syscalls":  {"65531"},
		"--env":       {"HOME=/tmp", "PATH=" + DefaultPath, "GOGC=50"},
	} {
		if got := flagValue(args, flag); !slices.Equal(got, w) {
			t.Errorf("seccomp %s: %q, want %q", flag, got, w)
		}
	}
	if !slices.Contains(args, "--dir=/cms-launcher=/var/lib/cms/worker/launcher") {
		t.Errorf("launcher not mounted: %q", args)
	}
	i := slices.Index(args, "--run")
	wantCmd := []string{"--run", "--", "/cms-launcher/seccomp-exec", "./prog", "x"}
	if got := args[i:]; !slices.Equal(got, wantCmd) {
		t.Errorf("seccomp command:\n%q\nwant\n%q", got, wantCmd)
	}
	spec.Seccomp = false
	plain, err := b.args(&spec, "/meta")
	if err != nil {
		t.Fatal(err)
	}
	if want := slices.DeleteFunc(slices.Clone(args), func(a string) bool {
		return strings.HasPrefix(a, "--dir=/cms-launcher=") || a == "/cms-launcher/seccomp-exec"
	}); !slices.Equal(plain, want) {
		t.Errorf("seccomp changes more than the command:\n%q\n%q", args, plain)
	}

	// Without worker.seccomp the flag is ignored.
	b = fakeBox(t, Config{Cgroups: true, Seccomp: false})
	args, _ = b.args(&RunSpec{Argv: []string{"./prog"}, Seccomp: true, Processes: Unlimited}, "/meta")
	if slices.Contains(args, "/cms-launcher/seccomp-exec") || !slices.Equal(flagValue(args, "--processes"), []string{""}) {
		t.Errorf("seccomp off: %q", args)
	}
}

func TestArgsRejectBadPaths(t *testing.T) {
	b := fakeBox(t, Config{})
	for name, spec := range map[string]RunSpec{
		"empty argv":           {},
		"relative outside dir": {Argv: []string{"x"}, Dirs: []Dir{{Inside: "/d", Outside: "relative/dir"}}},
		"dot-slash outside":    {Argv: []string{"x"}, Dirs: []Dir{{Inside: "/d", Outside: "./box"}}},
		"relative inside dir":  {Argv: []string{"x"}, Dirs: []Dir{{Inside: "d", Outside: "/d"}}},
		"colon in dir":         {Argv: []string{"x"}, Dirs: []Dir{{Inside: "/d", Outside: "/a:rw"}}},
		"equals in inside":     {Argv: []string{"x"}, Dirs: []Dir{{Inside: "/a=b", Outside: "/d"}}},
		"mount over root":      {Argv: []string{"x"}, Dirs: []Dir{{Inside: "/", Outside: "/d"}}},
		"bad env":              {Argv: []string{"x"}, Env: []string{"NOEQUALS"}},
		"negative time":        {Argv: []string{"x"}, CPUTime: -1},
		"negative memory":      {Argv: []string{"x"}, MemoryKiB: -1},
		"bad processes":        {Argv: []string{"x"}, Processes: -5},
		"NUL in argv":          {Argv: []string{"x\x00y"}},
	} {
		if _, err := b.args(&spec, "/meta"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestBoxFiles(t *testing.T) {
	b := fakeBox(t, Config{})
	if err := b.CreateFile("in/input.txt", []byte("hello"), false); err != nil {
		t.Fatal(err)
	}
	if err := b.CreateFile("prog", []byte("#!/bin/sh\n"), true); err != nil {
		t.Fatal(err)
	}
	if err := b.CreateFile("prog", nil, false); err == nil {
		t.Error("CreateFile overwrote a file")
	}
	for _, bad := range []string{"", "/etc/passwd", "../x", "a/../../x", "."} {
		if err := b.CreateFile(bad, nil, false); err == nil {
			t.Errorf("CreateFile(%q) accepted", bad)
		}
	}
	if st, err := b.Stat("prog"); err != nil || st.Mode().Perm() != 0o755 {
		t.Errorf("executable mode: %v %v", st, err)
	}
	if st, err := b.Stat("in/input.txt"); err != nil || st.Mode().Perm() != 0o644 {
		t.Errorf("file mode: %v %v", st, err)
	}
	data, trunc, err := b.ReadFile("in/input.txt", 3)
	if err != nil || !trunc || string(data) != "hel" {
		t.Errorf("truncated read: %q %v %v", data, trunc, err)
	}
	data, trunc, err = b.ReadFile("in/input.txt", 5)
	if err != nil || trunc || string(data) != "hello" {
		t.Errorf("exact read: %q %v %v", data, trunc, err)
	}
	if !b.Exists("prog") || b.Exists("nope") {
		t.Error("Exists")
	}
	if err := b.Remove("prog"); err != nil || b.Exists("prog") {
		t.Errorf("Remove: %v", err)
	}

	// What an untrusted program may leave behind.
	secret := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(secret, []byte("secret"), 0o644)
	os.Symlink(secret, filepath.Join(b.home, "abs-link"))
	os.Symlink("in/input.txt", filepath.Join(b.home, "rel-link"))
	os.Symlink("/", filepath.Join(b.home, "root"))
	unix.Mkfifo(filepath.Join(b.home, "fifo"), 0o666)
	for _, name := range []string{"abs-link", "rel-link", "root/etc/passwd", "fifo", "in"} {
		if _, _, err := b.ReadFile(name, 100); err == nil {
			t.Errorf("ReadFile(%q) followed a link or opened a special file", name)
		}
	}
}

func TestAllowWriting(t *testing.T) {
	b := fakeBox(t, Config{})
	b.CreateFile("input.txt", []byte("x"), false)
	b.CreateFile("prog", []byte("x"), true)
	mode := func(name string) os.FileMode {
		st, err := os.Lstat(filepath.Join(b.home, name))
		if err != nil {
			t.Fatal(err)
		}
		return st.Mode().Perm()
	}
	if err := b.allowWriting(true, []string{"output.txt", "/tmp/sub/res.txt", "/fifo0/in", "../escape"}); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]os.FileMode{".": 0o755, "input.txt": 0o755, "prog": 0o755, "output.txt": 0o722, "sub/res.txt": 0o722} {
		if got := mode(name); got != want {
			t.Errorf("restricted %s: %o, want %o", name, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(b.home), "escape")); err == nil {
		t.Error("a writable path outside the home was created")
	}
	if err := b.allowWriting(false, nil); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".", "input.txt", "output.txt"} {
		if got := mode(name); got != 0o777 {
			t.Errorf("unrestricted %s: %o", name, got)
		}
	}
}

func TestPool(t *testing.T) {
	dir := t.TempDir()
	p := newPool(10, 3, dir)
	ctx := context.Background()
	var ids []int
	for i := 0; i < 3; i++ {
		id, err := p.acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if !slices.Equal(ids, []int{10, 11, 12}) {
		t.Errorf("ids %v", ids)
	}
	// Another process (here another pool, same lock files) cannot take
	// them, and sees them in use.
	other := newPool(10, 3, dir)
	short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	if _, err := other.acquire(short); err == nil {
		t.Error("an id used by another pool was handed out")
	}
	cancel()
	if inUse, err := BoxInUse(dir, 11); err != nil || !inUse {
		t.Errorf("BoxInUse(11) = %v, %v", inUse, err)
	}
	if inUse, err := BoxInUse(dir, 99); err != nil || inUse {
		t.Errorf("BoxInUse(99) = %v, %v", inUse, err)
	}
	// A waiter gets the next released id at once.
	got := make(chan int)
	go func() {
		id, err := p.acquire(ctx)
		if err != nil {
			t.Error(err)
		}
		got <- id
	}()
	time.Sleep(20 * time.Millisecond)
	p.release(11)
	select {
	case id := <-got:
		if id != 11 {
			t.Errorf("got %d after releasing 11", id)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter not woken")
	}
	// Round robin: the next free id after the last one handed out.
	p.release(10)
	p.release(12)
	if id, _ := p.acquire(ctx); id != 12 {
		t.Errorf("round robin gave %d, want 12", id)
	}
	if inUse, _ := BoxInUse(dir, 10); inUse {
		t.Error("released id still locked")
	}

	// Concurrent users never share an id.
	q := newPool(0, 4, t.TempDir())
	var mu sync.Mutex
	inUse := map[int]bool{}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := q.acquire(ctx)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			if inUse[id] {
				t.Errorf("id %d handed out twice", id)
			}
			inUse[id] = true
			mu.Unlock()
			time.Sleep(time.Millisecond)
			mu.Lock()
			inUse[id] = false
			mu.Unlock()
			q.release(id)
		}()
	}
	wg.Wait()
}

func TestNewChecksConfig(t *testing.T) {
	base := Config{BoxIDBase: 0, BoxCount: 10, WorkDir: t.TempDir(), MaxFileSizeKiB: 1024, LockDir: t.TempDir()}
	for name, mut := range map[string]func(*Config){
		"no boxes":       func(c *Config) { c.BoxCount = 0 },
		"negative base":  func(c *Config) { c.BoxIDBase = -1 },
		"no work dir":    func(c *Config) { c.WorkDir = "" },
		"colon work dir": func(c *Config) { c.WorkDir = filepath.Join(c.WorkDir, "a:b") },
		"no file size":   func(c *Config) { c.MaxFileSizeKiB = 0 },
	} {
		c := base
		mut(&c)
		if _, err := New(c); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A relative work directory becomes absolute.
	t.Chdir(t.TempDir())
	c := base
	c.WorkDir = "work"
	s, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(s.Config().WorkDir) {
		t.Errorf("work dir %q", s.Config().WorkDir)
	}
}

func TestInstallLauncher(t *testing.T) {
	if err := checkSeccompSupport(); err != nil {
		t.Skip(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	exeBefore, err := os.Stat(exe)
	if err != nil {
		t.Fatal(err)
	}
	// The box user runs the launcher whatever the umask of the worker.
	old := unix.Umask(0o077)
	defer unix.Umask(old)
	c := Config{BoxCount: 1, WorkDir: t.TempDir(), MaxFileSizeKiB: 1024, LockDir: t.TempDir(), Seccomp: true}
	s, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.launcherDir, launcherBinary)
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0o755 {
		t.Fatalf("launcher: %v %v", st, err)
	}
	if dst, err := os.Stat(s.launcherDir); err != nil || dst.Mode().Perm() != 0o755 {
		t.Errorf("launcher dir: %v %v", dst, err)
	}
	img, _ := launcherImage()
	if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, img) {
		t.Errorf("launcher content: %d bytes, %v", len(data), err)
	}
	// It is a file of its own: the binary of the worker is neither
	// linked nor changed (its permissions are the admin's).
	if os.SameFile(st, exeBefore) {
		t.Error("the launcher is the binary of the worker")
	}
	if exeAfter, err := os.Stat(exe); err != nil || exeAfter.Mode() != exeBefore.Mode() {
		t.Errorf("the binary of the worker changed: %v -> %v (%v)", exeBefore.Mode(), exeAfter.Mode(), err)
	}
	// Installing again (another worker, a restart) replaces it and leaves
	// no temporary file.
	if _, err := New(c); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(s.launcherDir); err != nil || len(entries) != 1 {
		t.Errorf("launcher dir: %v %v", entries, err)
	}
}

func TestFIFODir(t *testing.T) {
	b := fakeBox(t, Config{})
	f, err := b.NewFIFODir("u0_to_m", "m_to_u0")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"u0_to_m", "m_to_u0"} {
		st, err := os.Stat(filepath.Join(f.Path(), n))
		if err != nil || st.Mode()&os.ModeNamedPipe == 0 || st.Mode().Perm() != 0o666 {
			t.Errorf("%s: %v %v", n, st, err)
		}
	}
	if d := f.Mount("/fifo0/"); d.Inside != "/fifo0" || d.Outside != f.Path() || !d.Writable {
		t.Errorf("mount %+v", d)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.Path()); !os.IsNotExist(err) {
		t.Error("FIFO directory left behind")
	}
	if _, err := b.NewFIFODir("../x"); err == nil {
		t.Error("bad FIFO name accepted")
	}
}

func TestLimitedBuffer(t *testing.T) {
	b := limitedBuffer{max: 5}
	b.Write([]byte("abc"))
	b.Write([]byte("defgh"))
	b.Write([]byte("ij"))
	if b.String() != "abcde... (5 more bytes)" {
		t.Errorf("%q", b.String())
	}
}

func TestConfigFrom(t *testing.T) {
	c := config.Default()
	sc := ConfigFrom(c)
	if sc.BoxCount != 100 || !sc.Cgroups || !sc.Seccomp || sc.WorkDir != c.Worker.WorkDir ||
		sc.MaxFileSizeKiB != c.Sandbox.MaxFileSizeKiB || sc.IsolatePath != "isolate" {
		t.Errorf("%+v", sc)
	}
}

func TestBPFProgram(t *testing.T) {
	p, err := defaultPolicy()
	if err != nil {
		t.Skip(err)
	}
	prog, err := p.program()
	if err != nil {
		t.Fatal(err)
	}
	if len(prog) > 255 {
		t.Errorf("program too long: %d instructions", len(prog))
	}
	// Every path ends in a return: the last instruction is one.
	if last := prog[len(prog)-1]; last.Code != unix.BPF_RET|unix.BPF_K {
		t.Errorf("last instruction %+v", last)
	}
	var a bpfAsm
	a.jump(jeqK, 1, "missing", "")
	if _, err := a.assemble(); err == nil {
		t.Error("undefined label accepted")
	}
}
