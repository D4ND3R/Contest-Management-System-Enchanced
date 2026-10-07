// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package ctl

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/cli"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/sandbox"
)

// verifyHostCommand is `cms ctl verify-host` (SPEC R2.5; CMS had no
// equivalent: it automates part of the IOI technical checklist).
func verifyHostCommand() *cli.Command {
	var cpus string
	var noRun bool
	return &cli.Command{
		Name:  "verify-host",
		Short: "check that this machine can judge reliably (cgroups v2, isolate, SMT, swap, CPU frequency)",
		Long: `Checks the judging host and prints one PASS, WARN or FAIL line per check:
cgroups v2 with the memory, cpu and pids controllers, isolate >= 2.0 and its
configuration (cgroup root, number of boxes), the box ids of this worker not
used by another one, swap off (or at least accounted for, so that boxes
cannot swap),
SMT off and the "performance" CPU governor on the judging CPUs, hard link
protection, seccomp support, the results of isolate-check-environment when
installed, and finally a run of a trivial program in a box.

It changes nothing (run "isolate-check-environment --execute" to apply its
recommendations). The exit status is non-zero when a check FAILs.`,
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&cpus, "cpus", "", "CPUs reserved for judging, e.g. 2-5 (default: all online CPUs)")
			fs.BoolVar(&noRun, "no-run", false, "skip the run in a box (the only check that touches isolate boxes)")
		},
		Run: func(ctx context.Context, env *cli.Env, args []string) error {
			if err := noArgs(args); err != nil {
				return err
			}
			cfg, _, err := config.Load(env.ConfigPath)
			if err != nil {
				return err
			}
			h := newHostChecker(cfg)
			if cpus != "" {
				if h.cpus, err = parseCPUList(cpus); err != nil {
					return cli.UsageError("--cpus: %v", err)
				}
			}
			if noRun {
				h.selfTest = nil
			}
			return h.report(ctx, env.Stdout)
		},
	}
}

// Check outcomes.
const (
	checkPass = "PASS"
	checkWarn = "WARN"
	checkFail = "FAIL"
)

type checkResult struct {
	level, name, msg string
}

// hostChecker runs the checks. Every path is read under root and every
// command goes through run, so that tests use a fake /sys and /proc.
type hostChecker struct {
	cfg      *config.Config
	root     string
	cpus     []int // judging CPUs; nil: all online
	lockDir  string
	run      func(ctx context.Context, name string, args ...string) ([]byte, error)
	lookPath func(string) (string, error)
	seccomp  func() error
	selfTest func(ctx context.Context) error // nil: skipped

	cgRoot  string // isolate's cgroup root, once checkIsolate found it
	results []checkResult
}

func newHostChecker(cfg *config.Config) *hostChecker {
	return &hostChecker{
		cfg:     cfg,
		root:    "/",
		lockDir: sandbox.DefaultLockDir,
		run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			cmd := exec.CommandContext(ctx, name, args...)
			cmd.Env = append(os.Environ(), "TERM=dumb") // no colors from isolate-check-environment
			return cmd.CombinedOutput()
		},
		lookPath: exec.LookPath,
		seccomp:  sandbox.CheckSeccomp,
		selfTest: func(ctx context.Context) error {
			sc := sandbox.ConfigFrom(cfg)
			s, err := sandbox.New(sc)
			if err != nil {
				return err
			}
			return s.SelfTest(ctx)
		},
	}
}

func (h *hostChecker) path(p string) string { return filepath.Join(h.root, p) }

func (h *hostChecker) read(p string) (string, error) {
	b, err := os.ReadFile(h.path(p))
	return strings.TrimSpace(string(b)), err
}

func (h *hostChecker) add(level, name, format string, a ...any) {
	h.results = append(h.results, checkResult{level, name, fmt.Sprintf(format, a...)})
}

// report runs every check, prints the results and fails if one failed.
func (h *hostChecker) report(ctx context.Context, w io.Writer) error {
	h.checkAll(ctx)
	fails, warns := 0, 0
	for _, r := range h.results {
		fmt.Fprintf(w, "%s  %s: %s\n", r.level, r.name, r.msg)
		switch r.level {
		case checkFail:
			fails++
		case checkWarn:
			warns++
		}
	}
	fmt.Fprintf(w, "\n%d checks: %d failed, %d warnings\n", len(h.results), fails, warns)
	if fails > 0 {
		return fmt.Errorf("%d host checks failed", fails)
	}
	return nil
}

func (h *hostChecker) checkAll(ctx context.Context) {
	cg := h.cfg.Sandbox.Cgroups
	h.checkCgroups(cg)
	isolateOK := h.checkIsolate(ctx, cg)
	h.checkBoxes()
	h.checkSwap(cg)
	cpus := h.judgingCPUs()
	h.checkSMT(cpus)
	h.checkGovernor(cpus)
	h.checkHardlinks()
	h.checkSeccomp()
	h.checkEnvironment(ctx)
	switch {
	case h.selfTest == nil:
	case !isolateOK:
		h.add(checkWarn, "sandbox run", "skipped: isolate is not usable")
	default:
		ctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		if err := h.selfTest(ctx); err != nil {
			h.add(checkFail, "sandbox run", "%v", err)
		} else {
			what := "a program ran in a box"
			if h.cfg.Worker.Seccomp {
				what += ", also under the seccomp filter"
			}
			h.add(checkPass, "sandbox run", "%s", what)
		}
	}
}

var cgroupControllers = []string{"memory", "cpu", "pids"}

func (h *hostChecker) checkCgroups(enabled bool) {
	const name = "cgroups v2"
	fail := checkFail
	if !enabled {
		fail = checkWarn
		h.add(checkWarn, "sandbox.cgroups", "false: development only (memory is limited per process as address "+
			"space, \"Memory limit exceeded\" is not detected, child processes are not timed while they run)")
	}
	data, err := h.read("sys/fs/cgroup/cgroup.controllers")
	if err != nil {
		if _, v1 := os.Stat(h.path("sys/fs/cgroup/memory")); v1 == nil {
			h.add(fail, name, "only cgroups v1 is mounted on /sys/fs/cgroup; isolate needs the unified hierarchy "+
				"(boot with systemd.unified_cgroup_hierarchy=1)")
		} else {
			h.add(fail, name, "not mounted on /sys/fs/cgroup")
		}
		return
	}
	have := strings.Fields(data)
	var missing []string
	for _, c := range cgroupControllers {
		if !slices.Contains(have, c) {
			missing = append(missing, c)
		}
	}
	if len(missing) > 0 {
		h.add(fail, name, "controllers missing: %s (available: %s)", strings.Join(missing, ", "), data)
		return
	}
	h.add(checkPass, name, "controllers %s available", strings.Join(cgroupControllers, ", "))
	// isolate reports memory use from memory.peak (Linux 5.19).
	if rel, err := h.read("proc/sys/kernel/osrelease"); err == nil {
		if maj, min, ok := majorMinor(rel); ok && (maj < 5 || maj == 5 && min < 19) {
			h.add(checkWarn, "kernel", "Linux %s: isolate needs 5.19 or newer to report memory use", rel)
		}
	}
}

var versionRE = regexp.MustCompile(`(\d+)\.(\d+)`)

// majorMinor finds the first "major.minor" version in s.
func majorMinor(s string) (int, int, bool) {
	m := versionRE.FindStringSubmatch(s)
	if m == nil {
		return 0, 0, false
	}
	maj, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	return maj, min, true
}

// isolateConfig is what `isolate -v -v --check-config` prints.
type isolateConfig struct {
	boxRoot, lockRoot, cgRoot string
	numBoxes                  int
}

var isolateConfigRE = regexp.MustCompile(`(\w+)=(\S+)`)

func parseIsolateConfig(out string) isolateConfig {
	var c isolateConfig
	for _, m := range isolateConfigRE.FindAllStringSubmatch(out, -1) {
		switch m[1] {
		case "box_root":
			c.boxRoot = m[2]
		case "lock_root":
			c.lockRoot = m[2]
		case "cg_root":
			c.cgRoot = m[2]
		case "num_boxes":
			c.numBoxes, _ = strconv.Atoi(m[2])
		}
	}
	return c
}

// checkIsolate checks the binary, its configuration and its cgroup root;
// it returns whether isolate can be run at all.
func (h *hostChecker) checkIsolate(ctx context.Context, cg bool) bool {
	bin := h.cfg.Worker.IsolatePath
	out, err := h.run(ctx, bin, "--version")
	if err != nil {
		h.add(checkFail, "isolate", "cannot run %s: %v (install isolate >= 2.0; the worker must be root or in the isolate group)",
			bin, firstLine(out, err))
		return false
	}
	maj, min, ok := majorMinor(string(out))
	switch {
	case !ok:
		h.add(checkFail, "isolate", "cannot read the version from %q", firstLine(out, nil))
		return false
	case maj < 2:
		h.add(checkFail, "isolate", "version %d.%d found, 2.0 or newer is required", maj, min)
		return false
	}
	h.add(checkPass, "isolate", "version %d.%d", maj, min)

	out, err = h.run(ctx, bin, "-v", "-v", "--check-config")
	if err != nil {
		h.add(checkFail, "isolate config", "%s", firstLine(out, err))
		return false
	}
	ic := parseIsolateConfig(string(out))
	base, count := h.cfg.Worker.BoxIDBase, h.cfg.Worker.BoxCount
	if ic.numBoxes > 0 && base+count > ic.numBoxes {
		h.add(checkFail, "isolate config", "worker box ids %d-%d, but isolate has only %d boxes (num_boxes)",
			base, base+count-1, ic.numBoxes)
	} else {
		h.add(checkPass, "isolate config", "box root %s, %d boxes", ic.boxRoot, ic.numBoxes)
	}
	if !cg {
		return true
	}
	cgRoot := ic.cgRoot
	if f, ok := strings.CutPrefix(cgRoot, "auto:"); ok {
		data, err := h.read(f)
		if err != nil {
			h.add(checkFail, "isolate cgroup", "%s is missing: isolate.service is not running "+
				"(systemctl enable --now isolate.service)", f)
			return false
		}
		cgRoot = data
	}
	ctrl, err := h.read(filepath.Join(cgRoot, "cgroup.controllers"))
	if err != nil {
		h.add(checkFail, "isolate cgroup", "cgroup root %s is not usable: %v", cgRoot, err)
		return false
	}
	if !slices.Contains(strings.Fields(ctrl), "memory") {
		h.add(checkFail, "isolate cgroup", "the memory controller is not delegated to %s (has: %s)", cgRoot, ctrl)
		return false
	}
	h.cgRoot = cgRoot
	h.add(checkPass, "isolate cgroup", "%s", cgRoot)
	return true
}

func firstLine(out []byte, err error) string {
	s := strings.TrimSpace(string(out))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if s == "" && err != nil {
		return err.Error()
	}
	return s
}

// checkBoxes looks for box ids of this worker held by a running process.
func (h *hostChecker) checkBoxes() {
	base, count := h.cfg.Worker.BoxIDBase, h.cfg.Worker.BoxCount
	var busy []int
	for id := base; id < base+count; id++ {
		inUse, err := sandbox.BoxInUse(h.lockDir, id)
		if err != nil {
			h.add(checkWarn, "box ids", "cannot check the lock of box %d: %v", id, err)
			return
		}
		if inUse {
			busy = append(busy, id)
		}
	}
	if len(busy) > 0 {
		h.add(checkWarn, "box ids", "%s in use by a running process: another worker on this machine with an "+
			"overlapping range (worker.box_id_base, worker.box_count), or this worker is running; "+
			"overlapping workers wait for each other's boxes", formatCPUList(busy))
		return
	}
	h.add(checkPass, "box ids", "%d-%d not used by another worker", base, base+count-1)
}

func (h *hostChecker) checkSwap(cg bool) {
	data, err := h.read("proc/swaps")
	if err != nil {
		h.add(checkWarn, "swap", "cannot read /proc/swaps: %v", err)
		return
	}
	lines := strings.Split(data, "\n")
	if len(lines) <= 1 {
		h.add(checkPass, "swap", "off")
		return
	}
	devices := len(lines) - 1
	switch {
	case !cg:
		h.add(checkWarn, "swap", "on (%d devices): run swapoff -a", devices)
	case h.cgRoot != "" && fileExists(h.path(filepath.Join(h.cgRoot, "memory.swap.max"))):
		// isolate sets memory.swap.max to 0 for boxes with a memory
		// limit, so programs cannot swap; the rest of the host can.
		h.add(checkWarn, "swap", "on (%d devices): boxes cannot swap (isolate limits memory.swap.max), "+
			"but swapping elsewhere slows judging down; run swapoff -a", devices)
	default:
		h.add(checkFail, "swap", "on (%d devices) without swap accounting in the cgroup of isolate: "+
			"memory limits do not cover swapped-out memory; run swapoff -a", devices)
	}
}

// judgingCPUs returns the CPUs to check: --cpus, or every online CPU.
func (h *hostChecker) judgingCPUs() []int {
	if h.cpus != nil {
		return h.cpus
	}
	data, err := h.read("sys/devices/system/cpu/online")
	if err != nil {
		return nil
	}
	cpus, _ := parseCPUList(data)
	return cpus
}

func (h *hostChecker) checkSMT(cpus []int) {
	if len(cpus) == 0 {
		h.add(checkWarn, "SMT", "cannot list the CPUs")
		return
	}
	var shared []int
	for _, c := range cpus {
		data, err := h.read(fmt.Sprintf("sys/devices/system/cpu/cpu%d/topology/thread_siblings_list", c))
		if err != nil {
			continue
		}
		if sib, err := parseCPUList(data); err == nil && len(sib) > 1 {
			shared = append(shared, c)
		}
	}
	if len(shared) > 0 {
		h.add(checkWarn, "SMT", "CPUs %s share their core with another hardware thread: times are noisy; "+
			"echo off > /sys/devices/system/cpu/smt/control", formatCPUList(shared))
		return
	}
	h.add(checkPass, "SMT", "off on CPUs %s", formatCPUList(cpus))
}

func (h *hostChecker) checkGovernor(cpus []int) {
	var wrong []string
	found := false
	for _, c := range cpus {
		g, err := h.read(fmt.Sprintf("sys/devices/system/cpu/cpu%d/cpufreq/scaling_governor", c))
		if err != nil {
			continue
		}
		found = true
		if g != "performance" {
			wrong = append(wrong, fmt.Sprintf("cpu%d=%s", c, g))
		}
	}
	switch {
	case !found:
		h.add(checkPass, "CPU frequency", "no frequency scaling (fixed frequency or a virtual machine)")
	case len(wrong) > 0:
		h.add(checkWarn, "CPU frequency", "governor is not \"performance\" (%s): times vary with the load; "+
			"echo performance | tee /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor", strings.Join(wrong, ", "))
	default:
		h.add(checkPass, "CPU frequency", "governor \"performance\"")
	}
}

func (h *hostChecker) checkHardlinks() {
	v, err := h.read("proc/sys/fs/protected_hardlinks")
	switch {
	case err != nil:
		h.add(checkWarn, "hard links", "cannot read fs.protected_hardlinks: %v", err)
	case v != "1":
		h.add(checkFail, "hard links", "fs.protected_hardlinks is %s: a sandboxed program could make isolate "+
			"change the owner of unrelated files; sysctl fs.protected_hardlinks=1", v)
	default:
		h.add(checkPass, "hard links", "protected")
	}
}

func (h *hostChecker) checkSeccomp() {
	if !h.cfg.Worker.Seccomp {
		h.add(checkWarn, "seccomp", "worker.seccomp is false: forbidden system calls are not reported as security violations")
		return
	}
	if err := h.seccomp(); err != nil {
		h.add(checkFail, "seccomp", "%v (set worker.seccomp: false to judge without it)", err)
		return
	}
	h.add(checkPass, "seccomp", "filters supported")
}

// checkEnvironment reports the results of isolate-check-environment (from
// the isolate package), without --execute.
func (h *hostChecker) checkEnvironment(ctx context.Context) {
	const bin = "isolate-check-environment"
	// Next to isolate first (it is not always in PATH), then in PATH.
	candidates := []string{bin}
	if p, err := h.lookPath(h.cfg.Worker.IsolatePath); err == nil {
		candidates = append([]string{filepath.Join(filepath.Dir(p), bin)}, candidates...)
	}
	found := ""
	for _, c := range candidates {
		if p, err := h.lookPath(c); err == nil {
			found = p
			break
		}
	}
	if found == "" {
		h.add(checkWarn, bin, "not installed (it comes with isolate)")
		return
	}
	out, _ := h.run(ctx, found)
	h.results = append(h.results, parseCheckEnvironment(string(out))...)
}

var checkingRE = regexp.MustCompile(`^Checking for (.*?) \.\.\. (PASS|FAIL|CAUTION|SKIPPED.*)$`)

// parseCheckEnvironment turns the output of isolate-check-environment into
// results: its failures are WARN here (they hurt reproducibility; the
// checks above FAIL what breaks judging), with its advice appended.
func parseCheckEnvironment(out string) []checkResult {
	var res []checkResult
	cur, detailed := -1, false
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if m := checkingRE.FindStringSubmatch(line); m != nil {
			r := checkResult{level: checkPass, name: "isolate-check-environment", msg: m[1]}
			switch {
			case m[2] == "FAIL" || m[2] == "CAUTION":
				r.level = checkWarn
			case strings.HasPrefix(m[2], "SKIPPED"):
				r.msg += " (not detected)"
			}
			res = append(res, r)
			cur, detailed = len(res)-1, false
			continue
		}
		if line == "" || cur < 0 || res[cur].level == checkPass {
			continue
		}
		if w, ok := strings.CutPrefix(line, "WARNING: "); ok {
			if detailed {
				res[cur].msg += "; " + w
			} else {
				res[cur].msg += ": " + w
				detailed = true
			}
		} else {
			res[cur].msg += " (fix: " + strings.TrimPrefix(line, "+ ") + ")"
		}
	}
	return res
}

// parseCPUList parses the kernel's CPU list format ("0-3,8,10-11").
func parseCPUList(s string) ([]int, error) {
	var out []int
	for _, part := range strings.Split(strings.TrimSpace(s), ",") {
		if part == "" {
			continue
		}
		lo, hi, isRange := strings.Cut(part, "-")
		a, err := strconv.Atoi(lo)
		if err != nil || a < 0 {
			return nil, fmt.Errorf("invalid CPU list %q", s)
		}
		b := a
		if isRange {
			if b, err = strconv.Atoi(hi); err != nil || b < a {
				return nil, fmt.Errorf("invalid CPU list %q", s)
			}
		}
		for i := a; i <= b; i++ {
			out = append(out, i)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("empty CPU list")
	}
	return out, nil
}

// formatCPUList is the inverse of parseCPUList for sorted lists.
func formatCPUList(ids []int) string {
	var parts []string
	for i := 0; i < len(ids); {
		j := i
		for j+1 < len(ids) && ids[j+1] == ids[j]+1 {
			j++
		}
		if i == j {
			parts = append(parts, strconv.Itoa(ids[i]))
		} else {
			parts = append(parts, fmt.Sprintf("%d-%d", ids[i], ids[j]))
		}
		i = j + 1
	}
	return strings.Join(parts, ",")
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
