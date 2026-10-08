// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Unlimited, as RunSpec.Processes or RunSpec.OpenFiles, removes the limit.
const Unlimited = -1

// DefaultPath is the PATH of sandboxed programs unless RunSpec.Env sets
// one (isolate itself passes no PATH).
const DefaultPath = "/usr/local/bin:/usr/bin:/bin"

// RunSpec describes one execution in a box (the attributes CMS sets on
// its Sandbox object before execute_without_std).
//
// Paths inside the box (Argv[0], Stdin, Stdout, Stderr, Chdir, Writable)
// are relative to HomeDir or absolute inner paths. Paths on the host
// (Dir.Outside) must be absolute.
type RunSpec struct {
	// Argv is the command; Argv[0] is not searched in PATH.
	Argv []string
	// Stdin, Stdout and Stderr redirect the standard streams to files
	// (or FIFOs) inside the box. Empty means /dev/null.
	Stdin, Stdout, Stderr string
	// StdinFile and StdoutFile, when set, are the standard input and
	// output of the program instead of Stdin and Stdout: open files of
	// the worker, usually pipes (the controller of an Interactive task
	// and its solutions talk to the worker and to each other this way).
	StdinFile, StdoutFile *os.File
	// ExtraFiles are passed to the program as the file descriptors 3, 4,
	// ... (isolate --inherit-fds). Only for trusted programs: isolate
	// then leaves its own descriptors open in the box too, so it is
	// refused together with Seccomp.
	ExtraFiles []*os.File
	// Env holds NAME=VALUE pairs. The program gets only these plus
	// HOME=HomeDir and PATH=DefaultPath (unless Env overrides them):
	// the environment of the worker, which may hold credentials, never
	// reaches a box.
	Env []string
	// Dirs are mounted besides the defaults (isolate's /bin, /dev, /lib,
	// /lib64, /proc, /usr; the home at HomeDir; /etc/alternatives,
	// /opt/pypy3 and /etc/mono when they exist, as CMS does).
	Dirs []Dir
	// Chdir is the working directory inside the box (default HomeDir).
	Chdir string

	// CPUTime, WallTime and ExtraTime are in seconds; 0 means no limit.
	// ExtraTime lets a program that went over CPUTime run a little
	// longer so that its real time is reported (isolate --extra-time).
	CPUTime, WallTime, ExtraTime float64
	// MemoryKiB limits memory: the whole cgroup with cgroups, the address
	// space of each process without them. 0 means no limit.
	MemoryKiB int64
	// Processes limits processes and threads together (RLIMIT_NPROC of
	// the box user). 0 and 1 mean a single thread (the CMS default);
	// Unlimited removes the limit (fork bombs are then only stopped by
	// the memory limit).
	Processes int
	// FileSizeKiB bounds every file written (0: sandbox.max_file_size_kib).
	FileSizeKiB int64
	// StackKiB bounds the stack (0: unlimited, like CMS).
	StackKiB int64
	// OpenFiles bounds open file descriptors (0: isolate's default, 1024;
	// Unlimited: none).
	OpenFiles int

	// Seccomp runs the program under the seccomp launcher: forbidden
	// system calls kill it with StatusSecurityViolation. Only for
	// contestant programs (evaluations and user tests), never for
	// compilers, checkers, managers or generators. Ignored when
	// worker.seccomp is off.
	Seccomp bool

	// AllowFileLocks lets the program lock files (flock, fcntl locks),
	// which recent isolate versions forbid by default because locks are
	// shared across boxes (isolate --syscalls; versions without it reject
	// the run). Some compilers need them (the Go build cache).
	AllowFileLocks bool

	// RestrictWrites makes the home read-only for the program except the
	// files in Writable and the Stdout and Stderr redirections, which are
	// created empty when missing (CMS allow_writing_only, used for
	// evaluations). Otherwise the program may create and change anything
	// in the home (CMS allow_writing_all: compilations, trusted programs).
	RestrictWrites bool
	Writable       []string
}

// Dir is an extra directory visible inside the box (isolate --dir).
type Dir struct {
	// Inside is the absolute path inside the box.
	Inside string
	// Outside is the absolute path on the host ("" means Inside).
	Outside string
	// Writable mounts it read-write (default read-only).
	Writable bool
	// NoExec forbids running programs from it.
	NoExec bool
	// Optional skips the mount when Outside does not exist.
	Optional bool
}

// defaultDirs are mounted in every box, like CMS does: Debian and Ubuntu
// reach some interpreters through /etc/alternatives, Arch installs PyPy
// in /opt, and Mono reads its DllMap from /etc/mono.
var defaultDirs = []Dir{
	{Inside: "/etc/alternatives", Optional: true},
	{Inside: "/opt/pypy3", Optional: true},
	{Inside: "/etc/mono", NoExec: true, Optional: true},
}

// checkMountPath rejects host paths isolate cannot take: relative ones
// (isolate refuses them, or resolves "./" against its own box directory)
// and those with ':' (isolate's option separator) or NUL.
func checkMountPath(p string) error {
	if !filepath.IsAbs(p) {
		return fmt.Errorf("%q is not an absolute path (isolate rejects relative directories)", p)
	}
	if strings.ContainsAny(p, ":\x00") {
		return fmt.Errorf("%q contains ':' or NUL, which isolate cannot mount", p)
	}
	return nil
}

func (d Dir) arg() (string, error) {
	if !path.IsAbs(d.Inside) || strings.ContainsAny(d.Inside, ":=\x00") {
		return "", fmt.Errorf("sandbox: inner directory %q must be absolute, without ':' or '='", d.Inside)
	}
	inside := path.Clean(d.Inside)
	if inside == "/" {
		return "", errors.New("sandbox: cannot mount over the root of the box")
	}
	outside := d.Outside
	if outside == "" {
		outside = inside
	}
	if err := checkMountPath(outside); err != nil {
		return "", fmt.Errorf("sandbox: directory for %s: %w", inside, err)
	}
	s := inside + "=" + filepath.Clean(outside)
	if d.Writable {
		s += ":rw"
	}
	if d.NoExec {
		s += ":noexec"
	}
	if d.Optional {
		s += ":maybe"
	}
	return "--dir=" + s, nil
}

// innerPath makes a path inside the box absolute (relative to HomeDir).
func innerPath(p string) (string, error) {
	if p == "" || strings.ContainsRune(p, 0) {
		return "", fmt.Errorf("sandbox: invalid path %q", p)
	}
	if !path.IsAbs(p) {
		p = path.Join(HomeDir, p)
	}
	return path.Clean(p), nil
}

// The system call restrictions of isolate (syscall_flags, "System call
// restrictions" in its manual): every bit set is a restriction in place;
// the default, 65535, includes those of future versions.
const (
	isolateSyscallsAll   = 0xffff
	isolateSyscallsLocks = 4
)

// args builds the isolate command line of a run.
func (b *Box) args(spec *RunSpec, metaPath string) ([]string, error) {
	if len(spec.Argv) == 0 || spec.Argv[0] == "" {
		return nil, errors.New("sandbox: empty command")
	}
	for _, a := range spec.Argv {
		if strings.ContainsRune(a, 0) {
			return nil, errors.New("sandbox: NUL in the command")
		}
	}
	seccomp := spec.Seccomp && b.s.cfg.Seccomp
	if len(spec.ExtraFiles) > 0 && spec.Seccomp {
		return nil, errors.New("sandbox: extra file descriptors are only for trusted programs (no seccomp)")
	}
	a := make([]string, 0, 32+len(spec.Argv)+len(spec.Dirs)+len(spec.Env))
	if b.s.cfg.Cgroups {
		a = append(a, "--cg")
	}
	a = append(a, "--box-id="+itoa(b.id), "--silent", "--meta="+metaPath)

	chdir := HomeDir
	if spec.Chdir != "" {
		var err error
		if chdir, err = innerPath(spec.Chdir); err != nil {
			return nil, err
		}
	}
	a = append(a, "--chdir="+chdir)

	homeDir, err := Dir{Inside: HomeDir, Outside: b.home, Writable: true}.arg()
	if err != nil {
		return nil, err
	}
	// isolate's own /box is not used, but isolate needs it (it starts in
	// it): read-only, so that the only writable places are the home
	// (whose writable files the run chooses) and the private /dev/shm,
	// and a program cannot fill the disk with files of the maximum size.
	a = append(a, homeDir, "--dir=/box=./box")
	for _, list := range [][]Dir{defaultDirs, spec.Dirs} {
		for _, d := range list {
			s, err := d.arg()
			if err != nil {
				return nil, err
			}
			a = append(a, s)
		}
	}
	if seccomp {
		a = append(a, "--dir="+LauncherDir+"="+b.s.launcherDir)
	}

	// Environment: fixed values first so that Env can override them.
	env := map[string]string{"HOME": HomeDir, "PATH": DefaultPath}
	order := []string{"HOME", "PATH"}
	for _, kv := range spec.Env {
		name, val, ok := strings.Cut(kv, "=")
		if !ok || name == "" || strings.ContainsRune(kv, 0) {
			return nil, fmt.Errorf("sandbox: invalid environment entry %q", kv)
		}
		if _, seen := env[name]; !seen {
			order = append(order, name)
		}
		env[name] = val
	}
	for _, name := range order {
		a = append(a, "--env="+name+"="+env[name])
	}

	if spec.AllowFileLocks {
		a = append(a, "--syscalls="+itoa(isolateSyscallsAll&^isolateSyscallsLocks))
	}

	// Limits.
	fsize := spec.FileSizeKiB
	if fsize <= 0 {
		fsize = b.s.cfg.MaxFileSizeKiB
	}
	a = append(a, "--fsize="+strconv.FormatInt(fsize, 10))
	if spec.StackKiB > 0 {
		a = append(a, "--stack="+strconv.FormatInt(spec.StackKiB, 10))
	}
	switch {
	case spec.OpenFiles == Unlimited:
		a = append(a, "--open-files=0")
	case spec.OpenFiles > 0:
		a = append(a, "--open-files="+itoa(spec.OpenFiles))
	}
	procs := spec.Processes
	if procs == 0 {
		procs = 1
	}
	switch {
	case procs == Unlimited:
		a = append(a, "--processes")
	case procs < 0:
		return nil, fmt.Errorf("sandbox: invalid process limit %d", procs)
	default:
		a = append(a, "--processes="+itoa(procs))
	}
	for _, t := range []struct {
		flag string
		v    float64
	}{{"--time=", spec.CPUTime}, {"--wall-time=", spec.WallTime}, {"--extra-time=", spec.ExtraTime}} {
		if t.v < 0 {
			return nil, fmt.Errorf("sandbox: negative time limit %s%g", t.flag, t.v)
		}
		if t.v > 0 {
			a = append(a, t.flag+strconv.FormatFloat(t.v, 'f', 3, 64))
		}
	}
	if spec.MemoryKiB < 0 {
		return nil, fmt.Errorf("sandbox: negative memory limit %d", spec.MemoryKiB)
	}
	if spec.MemoryKiB > 0 {
		if b.s.cfg.Cgroups {
			a = append(a, "--cg-mem="+strconv.FormatInt(spec.MemoryKiB, 10))
		} else {
			a = append(a, "--mem="+strconv.FormatInt(spec.MemoryKiB, 10))
		}
	}

	if len(spec.ExtraFiles) > 0 {
		a = append(a, "--inherit-fds")
	}

	// Redirections (CMS always redirects; empty means /dev/null). Without
	// --stdin or --stdout the program inherits those of isolate, which
	// are StdinFile and StdoutFile.
	for _, r := range []struct {
		flag, p string
		file    *os.File
	}{{"--stdin=", spec.Stdin, spec.StdinFile}, {"--stdout=", spec.Stdout, spec.StdoutFile}, {"--stderr=", spec.Stderr, nil}} {
		if r.file != nil {
			continue
		}
		p := "/dev/null"
		if r.p != "" {
			if p, err = innerPath(r.p); err != nil {
				return nil, err
			}
		}
		a = append(a, r.flag+p)
	}

	a = append(a, "--run", "--")
	if seccomp {
		// The launcher runs the program with the same environment and
		// limits (see launcher.go).
		a = append(a, LauncherDir+"/"+launcherBinary)
	}
	return append(a, spec.Argv...), nil
}

// Process is a program started with Box.Start.
type Process struct {
	box      *Box
	ctx      context.Context
	cmd      *exec.Cmd
	seccomp  bool
	metaPath string
	stderr   limitedBuffer // isolate's own messages
	done     chan struct{}
	waitErr  error

	killOnce  sync.Once
	cancelled atomic.Bool // killed because the context of Start ended

	resOnce sync.Once
	res     *Result
	err     error
}

// Run starts a program and waits for it.
func (b *Box) Run(ctx context.Context, spec RunSpec) (*Result, error) {
	p, err := b.Start(ctx, spec)
	if err != nil {
		return nil, err
	}
	return p.Wait()
}

// Start starts a program in the box and returns at once. Wait must be
// called. Cancelling ctx kills the program (and Wait then returns the
// context's error, never a verdict).
func (b *Box) Start(ctx context.Context, spec RunSpec) (*Process, error) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, errors.New("sandbox: box is closed")
	}
	n := b.execNum
	b.execNum++
	b.mu.Unlock()

	writable := append([]string{}, spec.Writable...)
	for _, w := range []string{spec.Stdout, spec.Stderr} {
		if w != "" {
			writable = append(writable, w)
		}
	}
	if err := b.allowWriting(spec.RestrictWrites, writable); err != nil {
		return nil, err
	}
	metaPath := filepath.Join(b.outer, "run.meta."+itoa(n))
	args, err := b.args(&spec, metaPath)
	if err != nil {
		return nil, err
	}
	b.logCommand(args)

	p := &Process{
		box: b, ctx: ctx, seccomp: spec.Seccomp && b.s.cfg.Seccomp, metaPath: metaPath,
		done: make(chan struct{}),
	}
	p.stderr.max = 4096
	p.cmd = exec.Command(b.s.cfg.IsolatePath, args...)
	// isolate's stdout is empty with --silent; its stderr has its
	// fatal errors. The program's streams are always redirected.
	p.cmd.Stderr = &p.stderr
	p.cmd.Stdout = &p.stderr
	if spec.StdinFile != nil {
		p.cmd.Stdin = spec.StdinFile
	}
	if spec.StdoutFile != nil {
		// With --silent isolate writes nothing on its standard output.
		p.cmd.Stdout = spec.StdoutFile
	}
	p.cmd.ExtraFiles = spec.ExtraFiles
	// If isolate dies without its box (SIGKILL), the box processes may
	// still hold the pipes: stop waiting for them after a while.
	p.cmd.WaitDelay = 5 * time.Second
	b.s.log.Debug("isolate run", "box", b.id, "args", args)

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, errors.New("sandbox: box is closed")
	}
	if err := p.cmd.Start(); err != nil {
		b.mu.Unlock()
		return nil, fmt.Errorf("sandbox: starting isolate: %w", err)
	}
	b.running[p] = struct{}{}
	b.mu.Unlock()

	go func() {
		p.waitErr = p.cmd.Wait()
		close(p.done)
		b.mu.Lock()
		delete(b.running, p)
		b.mu.Unlock()
	}()
	if ctx.Done() != nil {
		go func() {
			select {
			case <-ctx.Done():
				p.cancelled.Store(true)
				p.Kill()
			case <-p.done:
			}
		}()
	}
	return p, nil
}

// Kill stops the program: isolate is interrupted (it kills the box and
// still writes the meta file), and killed outright if it does not exit
// within a few seconds. Wait reports what isolate saw: usually a
// StatusSignal result with Signal 15.
func (p *Process) Kill() {
	p.killOnce.Do(func() {
		_ = p.cmd.Process.Signal(syscall.SIGTERM)
		go func() {
			t := time.NewTimer(5 * time.Second)
			defer t.Stop()
			select {
			case <-p.done:
			case <-t.C:
				_ = p.cmd.Process.Kill()
			}
		}()
	})
}

// Wait waits for the program and returns its result. It returns an
// error, and possibly a Result with StatusInternalError, when the sandbox
// failed (the job must be retried, it is never the contestant's fault),
// or when the context of Start was cancelled.
func (p *Process) Wait() (*Result, error) {
	<-p.done
	p.resOnce.Do(func() { p.res, p.err = p.result() })
	return p.res, p.err
}

// logCommand appends the isolate command line to commands.log in the box
// directory (CMS does the same), for whoever debugs a kept box.
func (b *Box) logCommand(args []string) {
	f, err := os.OpenFile(filepath.Join(b.outer, "commands.log"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	var sb strings.Builder
	sb.WriteString(shellQuote(b.s.cfg.IsolatePath))
	for _, a := range args {
		sb.WriteByte(' ')
		sb.WriteString(shellQuote(a))
	}
	sb.WriteByte('\n')
	_, _ = f.WriteString(sb.String())
}

// shellQuote quotes s for a POSIX shell when needed.
func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_=+/.,:%@") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
