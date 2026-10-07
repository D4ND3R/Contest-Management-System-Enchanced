// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package ctl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/sandbox"
)

// fakeHost builds a /sys, /proc and /run tree under a temporary root.
type fakeHost struct {
	t     *testing.T
	root  string
	files map[string]string
}

func newFakeHost(t *testing.T) *fakeHost {
	return &fakeHost{t: t, root: t.TempDir(), files: map[string]string{}}
}

func (f *fakeHost) set(path, content string) { f.files[path] = content }

func (f *fakeHost) write() {
	for p, c := range f.files {
		full := filepath.Join(f.root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			f.t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			f.t.Fatal(err)
		}
	}
}

const goodCheckEnvironment = `Using cgroup root: /sys/fs/cgroup/isolate.slice/isolate.service
Checking for cgroup support for memory.max ... PASS
Checking for swap ... PASS
Checking for simultaneous multithreading ... PASS
Checking for CPU frequency scaling ... SKIPPED (not detected)
Checking for kernel address space randomisation ... FAIL
WARNING: address space randomisation is enabled.
echo 0 > /proc/sys/kernel/randomize_va_space
Checking for transparent hugepage support ... FAIL
WARNING: transparent hugepages are enabled.
echo never > /sys/kernel/mm/transparent_hugepage/enabled
WARNING: transparent hugepage defrag is enabled.
echo never > /sys/kernel/mm/transparent_hugepage/defrag
`

// goodHost is a well configured judging machine with 4 CPUs.
func goodHost(t *testing.T) *fakeHost {
	f := newFakeHost(t)
	f.set("sys/fs/cgroup/cgroup.controllers", "cpuset cpu io memory hugetlb pids rdma misc")
	f.set("proc/sys/kernel/osrelease", "6.8.0-45-generic")
	f.set("run/isolate/cgroup", "/sys/fs/cgroup/isolate.slice/isolate.service")
	f.set("sys/fs/cgroup/isolate.slice/isolate.service/cgroup.controllers", "cpuset cpu memory pids")
	f.set("proc/swaps", "Filename\t\t\t\tType\t\tSize\t\tUsed\t\tPriority")
	f.set("sys/devices/system/cpu/online", "0-3")
	for c := 0; c < 4; c++ {
		f.set(fmt.Sprintf("sys/devices/system/cpu/cpu%d/topology/thread_siblings_list", c), fmt.Sprint(c))
		f.set(fmt.Sprintf("sys/devices/system/cpu/cpu%d/cpufreq/scaling_governor", c), "performance")
	}
	f.set("proc/sys/fs/protected_hardlinks", "1")
	return f
}

type fakeCommands struct {
	version, checkConfig, checkEnv string
	versionErr                     error
	haveCheckEnv                   bool
}

func (f *fakeHost) checker(cfg *config.Config, cmds fakeCommands) *hostChecker {
	f.write()
	h := newHostChecker(cfg)
	h.root = f.root
	h.lockDir = filepath.Join(f.root, "locks")
	if err := os.MkdirAll(h.lockDir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	h.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		switch {
		case name == "isolate" && slices.Equal(args, []string{"--version"}):
			return []byte(cmds.version), cmds.versionErr
		case name == "isolate" && slices.Contains(args, "--check-config"):
			return []byte(cmds.checkConfig), nil
		case strings.HasSuffix(name, "isolate-check-environment"):
			return []byte(cmds.checkEnv), nil
		}
		return nil, fmt.Errorf("unexpected command %s %q", name, args)
	}
	h.lookPath = func(name string) (string, error) {
		switch {
		case name == "isolate":
			return "/usr/bin/isolate", nil
		case strings.HasSuffix(name, "isolate-check-environment") && cmds.haveCheckEnv:
			return "/usr/bin/isolate-check-environment", nil
		}
		return "", errors.New("not found")
	}
	h.seccomp = func() error { return nil }
	h.selfTest = func(context.Context) error { return nil }
	return h
}

var goodCommands = fakeCommands{
	version: "The process isolator 2.7\n(c) 2012--2026 Martin Mares and Bernard Blackham\n",
	checkConfig: "Config: box_root=/var/lib/isolate lock_root=/run/isolate/locks cg_root=auto:/run/isolate/cgroup\n" +
		"Config: first_uids=165536 first_gid=165536 num_boxes=65536\nConfig: restricted_init=0 syscall_flags=65535\n",
	checkEnv:     goodCheckEnvironment,
	haveCheckEnv: true,
}

func levels(h *hostChecker) map[string]string {
	m := map[string]string{}
	for _, r := range h.results {
		if prev, ok := m[r.name]; !ok || prev == checkPass || r.level == checkFail {
			m[r.name] = r.level
		}
	}
	return m
}

func TestVerifyHostGood(t *testing.T) {
	h := goodHost(t).checker(config.Default(), goodCommands)
	var out bytes.Buffer
	if err := h.report(context.Background(), &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	got := levels(h)
	for name, want := range map[string]string{
		"cgroups v2": checkPass, "isolate": checkPass, "isolate config": checkPass, "isolate cgroup": checkPass,
		"box ids": checkPass, "swap": checkPass, "SMT": checkPass, "CPU frequency": checkPass,
		"hard links": checkPass, "seccomp": checkPass, "sandbox run": checkPass,
		// Its address space randomisation and hugepage advice.
		"isolate-check-environment": checkWarn,
	} {
		if got[name] != want {
			t.Errorf("%s: %s, want %s\n%s", name, got[name], want, out.String())
		}
	}
	for _, line := range []string{
		"PASS  cgroups v2: controllers memory, cpu, pids available",
		"PASS  isolate: version 2.7",
		"PASS  isolate cgroup: /sys/fs/cgroup/isolate.slice/isolate.service",
		"PASS  box ids: 0-99 not used by another worker",
		"PASS  SMT: off on CPUs 0-3",
		"WARN  isolate-check-environment: kernel address space randomisation: address space randomisation is enabled. " +
			"(fix: echo 0 > /proc/sys/kernel/randomize_va_space)",
		"PASS  isolate-check-environment: CPU frequency scaling (not detected)",
		"0 failed",
	} {
		if !strings.Contains(out.String(), line) {
			t.Errorf("missing %q in:\n%s", line, out.String())
		}
	}
}

func TestVerifyHostBad(t *testing.T) {
	f := goodHost(t)
	delete(f.files, "sys/fs/cgroup/cgroup.controllers")
	f.set("sys/fs/cgroup/memory/memory.limit_in_bytes", "9223372036854771712") // cgroups v1
	f.set("proc/swaps", "Filename\tType\tSize\tUsed\tPriority\n/swap.img\tfile\t8388604\t0\t-2")
	f.set("sys/devices/system/cpu/cpu1/topology/thread_siblings_list", "1,3")
	f.set("sys/devices/system/cpu/cpu3/topology/thread_siblings_list", "1,3")
	f.set("sys/devices/system/cpu/cpu2/cpufreq/scaling_governor", "powersave")
	f.set("proc/sys/fs/protected_hardlinks", "0")
	cfg := config.Default()
	cfg.Worker.BoxIDBase = 950
	cmds := goodCommands
	cmds.checkConfig = strings.Replace(cmds.checkConfig, "num_boxes=65536", "num_boxes=1000", 1)
	cmds.haveCheckEnv = false
	h := f.checker(cfg, cmds)
	h.seccomp = func() error { return errors.New("no seccomp here") }
	selfTestRan := false
	h.selfTest = func(context.Context) error { selfTestRan = true; return errors.New("box failed") }
	// Another worker holds box 960.
	held, err := os.OpenFile(sandbox.BoxLockPath(h.lockDir, 960), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := lockFile(held); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err = h.report(context.Background(), &out)
	if err == nil {
		t.Fatalf("bad host passed:\n%s", out.String())
	}
	got := levels(h)
	for name, want := range map[string]string{
		"cgroups v2": checkFail, "isolate config": checkFail, "swap": checkFail, "hard links": checkFail,
		"seccomp": checkFail, "sandbox run": checkFail,
		"box ids": checkWarn, "SMT": checkWarn, "CPU frequency": checkWarn, "isolate-check-environment": checkWarn,
	} {
		if got[name] != want {
			t.Errorf("%s: %s, want %s\n%s", name, got[name], want, out.String())
		}
	}
	if !selfTestRan {
		t.Error("the box run was skipped")
	}
	for _, line := range []string{
		"only cgroups v1 is mounted",
		"worker box ids 950-1049, but isolate has only 1000 boxes",
		"960 in use by a running process",
		"CPUs 1,3 share their core",
		"cpu2=powersave",
	} {
		if !strings.Contains(out.String(), line) {
			t.Errorf("missing %q in:\n%s", line, out.String())
		}
	}
}

func TestVerifyHostIsolate(t *testing.T) {
	// isolate.service not running.
	f := goodHost(t)
	delete(f.files, "run/isolate/cgroup")
	h := f.checker(config.Default(), goodCommands)
	h.checkAll(context.Background())
	if got := levels(h); got["isolate cgroup"] != checkFail || got["sandbox run"] != checkWarn {
		t.Errorf("isolate.service down: %v", h.results)
	}

	// Too old, or missing.
	for _, cmds := range []fakeCommands{
		{version: "The process isolator 1.10.1\n"},
		{versionErr: errors.New("exec: \"isolate\": executable file not found in $PATH")},
	} {
		h := goodHost(t).checker(config.Default(), cmds)
		h.checkAll(context.Background())
		if got := levels(h); got["isolate"] != checkFail {
			t.Errorf("%+v: %v", cmds, h.results)
		}
	}

	// Missing controllers; an old kernel.
	f = goodHost(t)
	f.set("sys/fs/cgroup/cgroup.controllers", "cpu io")
	h = f.checker(config.Default(), goodCommands)
	h.checkCgroups(true)
	if len(h.results) != 1 || h.results[0].level != checkFail || !strings.Contains(h.results[0].msg, "memory, pids") {
		t.Errorf("missing controllers: %v", h.results)
	}
	f = goodHost(t)
	f.set("proc/sys/kernel/osrelease", "5.15.0-100-generic")
	h = f.checker(config.Default(), goodCommands)
	h.checkCgroups(true)
	if got := levels(h); got["kernel"] != checkWarn {
		t.Errorf("old kernel: %v", h.results)
	}

	// Swap accounted for in the cgroup of isolate only warns.
	f = goodHost(t)
	f.set("proc/swaps", "Filename\tType\n/swap\tfile")
	f.set("sys/fs/cgroup/isolate.slice/isolate.service/memory.swap.max", "max")
	h = f.checker(config.Default(), goodCommands)
	h.checkAll(context.Background())
	if got := levels(h); got["swap"] != checkWarn {
		t.Errorf("accounted swap: %v", h.results)
	}

	// Development machines (sandbox.cgroups false): cgroups and swap
	// only warn.
	f = goodHost(t)
	delete(f.files, "sys/fs/cgroup/cgroup.controllers")
	f.set("proc/swaps", "Filename\tType\n/swap\tfile")
	cfg := config.Default()
	cfg.Sandbox.Cgroups = false
	h = f.checker(cfg, goodCommands)
	var out bytes.Buffer
	if err := h.report(context.Background(), &out); err != nil {
		t.Errorf("development host failed: %v\n%s", err, out.String())
	}
	if got := levels(h); got["cgroups v2"] != checkWarn || got["swap"] != checkWarn || got["sandbox.cgroups"] != checkWarn {
		t.Errorf("development host: %v", h.results)
	}

	// --cpus restricts the CPU checks to the judging CPUs.
	f = goodHost(t)
	f.set("sys/devices/system/cpu/cpu0/topology/thread_siblings_list", "0,1")
	f.set("sys/devices/system/cpu/cpu1/topology/thread_siblings_list", "0,1")
	f.set("sys/devices/system/cpu/cpu0/cpufreq/scaling_governor", "ondemand")
	h = f.checker(config.Default(), goodCommands)
	h.cpus = []int{2, 3}
	h.checkAll(context.Background())
	if got := levels(h); got["SMT"] != checkPass || got["CPU frequency"] != checkPass {
		t.Errorf("--cpus 2-3: %v", h.results)
	}
}

func TestCPUList(t *testing.T) {
	for in, want := range map[string][]int{"0": {0}, "0-3": {0, 1, 2, 3}, "0-1,4,6-7\n": {0, 1, 4, 6, 7}} {
		got, err := parseCPUList(in)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("%q: %v %v", in, got, err)
		}
		if f := formatCPUList(got); f != strings.TrimSpace(in) {
			t.Errorf("format %v: %q", got, f)
		}
	}
	for _, bad := range []string{"", "x", "3-1", "-1", "1-"} {
		if _, err := parseCPUList(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func lockFile(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) }
