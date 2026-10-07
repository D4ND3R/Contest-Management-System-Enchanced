// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// The semantics of boxes, runs and exit statuses are ported from
// cms/grading/Sandbox.py (IsolateSandbox) of CMS:
// Copyright © 2010-2015 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2014 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2026 Luca Versari <veluca93@gmail.com>

// Package sandbox runs untrusted programs with isolate (the IsolateSandbox
// of CMS, cms/grading/Sandbox.py) plus an optional seccomp launcher that
// yields the "Security violation" verdict.
//
// A Sandbox is created once per worker from the configuration. Each job
// takes boxes from it (NewBox): a box is an isolate box id plus a
// directory on the host, its home, that the programs see as /tmp (their
// working directory), exactly like CMS. Files go in and out of the home
// with the Box helpers, which never follow symbolic links out of it.
// Programs run with Box.Run (or Start and Wait for programs that must run
// at the same time, like the manager and the user processes of a
// Communication task). Every box must be closed: Close kills what is
// still running, releases the isolate box and deletes the home unless
// worker.keep_sandbox is set.
//
// Every directory handed to isolate is absolute: isolate rejects relative
// --dir values, so relative paths are refused before calling it.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
)

// Config configures a Sandbox. ConfigFrom builds it from cms.yaml.
type Config struct {
	// IsolatePath is the isolate binary (a name searched in PATH or an
	// absolute path).
	IsolatePath string
	// Cgroups runs isolate with --cg (cgroups v2). Without it memory is
	// limited per process as address space and "Memory limit exceeded"
	// cannot be told apart from other crashes; only for development.
	Cgroups bool
	// Boxes [BoxIDBase, BoxIDBase+BoxCount) are used.
	BoxIDBase, BoxCount int
	// WorkDir holds the homes of the boxes; it is made absolute.
	WorkDir string
	// KeepSandbox leaves the homes on disk after Close (debugging).
	KeepSandbox bool
	// MaxFileSizeKiB is the default --fsize of every run.
	MaxFileSizeKiB int64
	// Seccomp enables the seccomp launcher for runs that ask for it
	// (RunSpec.Seccomp). When false those runs go without the filter.
	Seccomp bool
	// LauncherPath is a binary implementing `sandbox-exec` (see
	// MaybeRunLauncher). Empty means the running executable, which is
	// the cms binary in production and the test binary in tests.
	LauncherPath string
	// LockDir holds one lock file per box id, shared by every worker of
	// the machine so that two workers never use the same box. Empty
	// means DefaultLockDir.
	LockDir string
	// Logger receives debug logs of every isolate command (nil: none).
	Logger *slog.Logger
}

// DefaultLockDir is the default Config.LockDir.
const DefaultLockDir = "/run/lock/cms-sandbox"

// ConfigFrom extracts the sandbox settings of an installation.
func ConfigFrom(c *config.Config) Config {
	return Config{
		IsolatePath:    c.Worker.IsolatePath,
		Cgroups:        c.Sandbox.Cgroups,
		BoxIDBase:      c.Worker.BoxIDBase,
		BoxCount:       c.Worker.BoxCount,
		WorkDir:        c.Worker.WorkDir,
		KeepSandbox:    c.Worker.KeepSandbox,
		MaxFileSizeKiB: c.Sandbox.MaxFileSizeKiB,
		Seccomp:        c.Worker.Seccomp,
	}
}

// Sandbox creates boxes. It is safe for concurrent use.
type Sandbox struct {
	cfg         Config
	pool        *pool
	launcherDir string // host directory mounted at LauncherDir ("" without seccomp)
	log         *slog.Logger
}

// New checks the configuration, prepares the work directory and, when
// seccomp is enabled, the launcher. It does not run isolate: SelfTest
// does.
func New(cfg Config) (*Sandbox, error) {
	if cfg.IsolatePath == "" {
		cfg.IsolatePath = "isolate"
	}
	if cfg.BoxCount < 1 || cfg.BoxIDBase < 0 {
		return nil, fmt.Errorf("sandbox: invalid box id range [%d, %d)", cfg.BoxIDBase, cfg.BoxIDBase+cfg.BoxCount)
	}
	if cfg.WorkDir == "" {
		return nil, errors.New("sandbox: no work directory")
	}
	if cfg.MaxFileSizeKiB < 1 {
		return nil, errors.New("sandbox: the maximum file size must be positive")
	}
	abs, err := filepath.Abs(cfg.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("sandbox: work directory: %w", err)
	}
	cfg.WorkDir = abs
	if err := checkMountPath(cfg.WorkDir); err != nil {
		return nil, fmt.Errorf("sandbox: work directory: %w", err)
	}
	if err := os.MkdirAll(cfg.WorkDir, 0o755); err != nil {
		return nil, fmt.Errorf("sandbox: %w", err)
	}
	if cfg.LockDir == "" {
		cfg.LockDir = DefaultLockDir
	}
	if err := os.MkdirAll(cfg.LockDir, 0o755); err != nil {
		return nil, fmt.Errorf("sandbox: box locks: %w", err)
	}
	log := cfg.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	s := &Sandbox{
		cfg:  cfg,
		pool: newPool(cfg.BoxIDBase, cfg.BoxCount, cfg.LockDir),
		log:  log,
	}
	if cfg.Seccomp {
		if err := checkSeccompSupport(); err != nil {
			return nil, fmt.Errorf("sandbox: worker.seccomp is on but %w", err)
		}
		if s.launcherDir, err = installLauncher(cfg.LauncherPath, cfg.WorkDir); err != nil {
			return nil, fmt.Errorf("sandbox: seccomp launcher: %w", err)
		}
	}
	return s, nil
}

// Config returns the configuration in use (WorkDir is absolute).
func (s *Sandbox) Config() Config { return s.cfg }

// installLauncher puts the launcher binary in its own directory under
// workDir, which the boxes mount read-only at LauncherDir. A hard link
// (or a copy when linking is not possible) instead of mounting the
// directory of the binary: the box sees nothing else, the directory has
// known permissions, and replacing the binary during an upgrade does not
// change what running workers execute.
func installLauncher(path, workDir string) (string, error) {
	if path == "" {
		exe, err := os.Executable()
		if err != nil {
			return "", err
		}
		path = exe
	}
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(path) {
		if path, err = filepath.Abs(path); err != nil {
			return "", err
		}
	}
	dir := filepath.Join(workDir, "launcher")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	// MkdirAll keeps the mode of an existing directory: the box user
	// must be able to enter it.
	if err := os.Chmod(dir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, launcherBinary)
	tmp := dst + ".new-" + strconv.Itoa(os.Getpid())
	_ = os.Remove(tmp)
	if err := os.Link(path, tmp); err != nil {
		if err := copyFile(path, tmp, 0o755); err != nil {
			_ = os.Remove(tmp)
			return "", err
		}
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	// Atomic: boxes of other workers sharing workDir may be starting it.
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return dir, nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// isolate runs an isolate command that is not a --run (init, cleanup)
// and returns its trimmed standard output.
func (s *Sandbox) isolate(ctx context.Context, cg bool, args ...string) (string, error) {
	if cg {
		args = append([]string{"--cg"}, args...)
	}
	cmd := exec.CommandContext(ctx, s.cfg.IsolatePath, args...)
	var stdout, stderr limitedBuffer
	stdout.max, stderr.max = 4096, 4096
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	s.log.Debug("isolate", "args", args)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s %s: %w: %s", s.cfg.IsolatePath, strings.Join(args, " "), err,
			strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// CheckSeccomp tells whether the seccomp launcher works on this kernel and
// architecture (worker.seccomp needs it).
func CheckSeccomp() error { return checkSeccompSupport() }

// SelfTest runs a trivial program in a box, and also under the seccomp
// launcher when it is enabled, and checks the results. The worker calls
// it at startup so that a broken host fails loudly instead of failing
// every job.
func (s *Sandbox) SelfTest(ctx context.Context) error {
	b, err := s.NewBox(ctx, "selftest")
	if err != nil {
		return err
	}
	defer b.Close()
	specs := []RunSpec{{Argv: []string{"/bin/true"}, CPUTime: 5, WallTime: 10, MemoryKiB: 256 << 10}}
	if s.cfg.Seccomp {
		specs = append(specs, RunSpec{Argv: []string{"/bin/true"}, CPUTime: 5, WallTime: 10, MemoryKiB: 256 << 10, Seccomp: true})
	}
	for _, spec := range specs {
		res, err := b.Run(ctx, spec)
		if err != nil {
			return fmt.Errorf("sandbox self-test (seccomp %v): %w", spec.Seccomp, err)
		}
		if res.Status != StatusOK {
			return fmt.Errorf("sandbox self-test (seccomp %v): /bin/true ended with %s (%s)", spec.Seccomp, res.Status, res.Message)
		}
	}
	return nil
}

var versionRE = regexp.MustCompile(`(\d+)\.(\d+)`)

// IsolateVersion returns the major and minor version of the isolate binary.
func IsolateVersion(ctx context.Context, path string) (int, int, error) {
	out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if err != nil {
		return 0, 0, fmt.Errorf("running %s --version: %w (%s)", path, err, strings.TrimSpace(string(out)))
	}
	m := versionRE.FindStringSubmatch(string(out))
	if m == nil {
		return 0, 0, fmt.Errorf("cannot read the version of isolate from %q", out)
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return major, minor, nil
}

// limitedBuffer keeps the first max bytes written to it and discards the
// rest, so that a flood on a pipe is drained without using memory.
type limitedBuffer struct {
	max       int
	buf       []byte
	discarded int64
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if room := b.max - len(b.buf); room > 0 {
		if len(p) > room {
			b.discarded += int64(len(p) - room)
			p = p[:room]
		}
		b.buf = append(b.buf, p...)
	} else {
		b.discarded += int64(len(p))
	}
	return n, nil
}

func (b *limitedBuffer) String() string {
	if b.discarded > 0 {
		return string(b.buf) + fmt.Sprintf("... (%d more bytes)", b.discarded)
	}
	return string(b.buf)
}

func itoa(i int) string { return strconv.Itoa(i) }
