// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package sandbox

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// launcherArgs are the parsed arguments of `cms sandbox-exec`.
type launcherArgs struct {
	policy     string
	nproc      int64 // 0: leave RLIMIT_NPROC alone
	rlimitAS   int64 // KiB; 0: leave RLIMIT_AS alone
	restoreEnv []string
	argv       []string
}

func parseLauncherArgs(args []string) (*launcherArgs, error) {
	la := &launcherArgs{policy: "default"}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			la.argv = args[i+1:]
			break
		}
		name, val, ok := strings.Cut(a, "=")
		if !ok {
			return nil, fmt.Errorf("unexpected argument %q (flags are --name=value, the program goes after --)", a)
		}
		var err error
		switch name {
		case "--policy":
			la.policy = val
		case "--nproc":
			la.nproc, err = strconv.ParseInt(val, 10, 64)
			if err == nil && la.nproc < 1 {
				err = fmt.Errorf("must be at least 1")
			}
		case "--rlimit-as":
			la.rlimitAS, err = strconv.ParseInt(val, 10, 64)
			if err == nil && la.rlimitAS < 1 {
				err = fmt.Errorf("must be at least 1")
			}
		case "--restore-env":
			if val == "" || strings.HasPrefix(val, "=") {
				err = fmt.Errorf("empty variable name")
			}
			la.restoreEnv = append(la.restoreEnv, val)
		default:
			return nil, fmt.Errorf("unknown flag %s", name)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %v", name, err)
		}
	}
	if len(la.argv) == 0 || la.argv[0] == "" {
		return nil, fmt.Errorf("no program given (usage: sandbox-exec [flags] -- program args...)")
	}
	return la, nil
}

// Exit statuses of the launcher when it cannot run the program. They are
// reported as a runtime error like any other exit status (a contestant
// program can exit with them too, so they cannot mean "sandbox error");
// Sandbox.SelfTest catches hosts where the filter cannot be installed.
const (
	launcherSetupFailed = 125
	launcherExecFailed  = 127 // like a shell
)

// LauncherMain implements `cms sandbox-exec`: it sets the resource limits
// the program must get, no_new_privs and the seccomp filter (on every
// thread), and execs the program (no PATH search, exactly like isolate).
// It only returns, with the exit status for the process, when something
// failed; the reason is written to stderr.
//
// The launcher runs inside the box under the limits of the program, so it
// does as little as possible: no goroutines, a few small allocations.
func LauncherMain(args []string) int {
	la, err := parseLauncherArgs(args)
	if err != nil {
		return launcherFail(launcherSetupFailed, err)
	}
	if la.policy != "default" {
		return launcherFail(launcherSetupFailed, fmt.Errorf("unknown policy %q", la.policy))
	}
	pol, err := defaultPolicy()
	if err != nil {
		return launcherFail(launcherSetupFailed, err)
	}
	prog, err := pol.program()
	if err != nil {
		return launcherFail(launcherSetupFailed, err)
	}
	env := restoreEnv(os.Environ(), la.restoreEnv)
	// Everything execve needs is prepared before the limits go down.
	path, err := unix.BytePtrFromString(la.argv[0])
	if err != nil {
		return launcherFail(launcherSetupFailed, fmt.Errorf("program %q: %w", la.argv[0], err))
	}
	argv, err := syscall.SlicePtrFromStrings(la.argv)
	if err != nil {
		return launcherFail(launcherSetupFailed, fmt.Errorf("arguments: %w", err))
	}
	envv, err := syscall.SlicePtrFromStrings(env)
	if err != nil {
		return launcherFail(launcherSetupFailed, fmt.Errorf("environment: %w", err))
	}
	// prctl(PR_SET_NO_NEW_PRIVS), seccomp and execve must happen on the
	// same OS thread.
	runtime.LockOSThread()

	// From here to execve only raw system calls (no Go runtime
	// bookkeeping): during a normal system call the runtime may hand the
	// processor to another thread, creating one if none is idle, and
	// under the program's limits thread creation (and the memory of its
	// stack) fails, so the runtime aborts and a correct program would get
	// a runtime error (this happened under load).
	//
	// Limits the launcher itself could not live with: the Go runtime
	// needs a few threads and reserves address space, so the sandbox
	// grants more and the program gets the real values here. Lowering a
	// limit never fails; both values are set so it cannot be raised back.
	if la.nproc > 0 {
		if err := rawSetrlimit(unix.RLIMIT_NPROC, uint64(la.nproc)); err != nil {
			return launcherFail(launcherSetupFailed, fmt.Errorf("setrlimit(NPROC): %w", err))
		}
	}
	if la.rlimitAS > 0 {
		if err := rawSetrlimit(unix.RLIMIT_AS, uint64(la.rlimitAS)<<10); err != nil {
			return launcherFail(launcherSetupFailed, fmt.Errorf("setrlimit(AS): %w", err))
		}
	}
	if err := installFilter(prog); err != nil {
		return launcherFail(launcherSetupFailed, err)
	}
	_, _, errno := unix.RawSyscall(unix.SYS_EXECVE, uintptr(unsafe.Pointer(path)),
		uintptr(unsafe.Pointer(&argv[0])), uintptr(unsafe.Pointer(&envv[0])))
	runtime.KeepAlive(path)
	runtime.KeepAlive(argv)
	runtime.KeepAlive(envv)
	return launcherFail(launcherExecFailed, fmt.Errorf("execve(%q): %w", la.argv[0], errno))
}

// rawSetrlimit sets both values of a limit of the process with a raw
// system call (see LauncherMain).
func rawSetrlimit(resource int, value uint64) error {
	lim := unix.Rlimit{Cur: value, Max: value}
	_, _, errno := unix.RawSyscall6(unix.SYS_PRLIMIT64, 0, uintptr(resource), uintptr(unsafe.Pointer(&lim)), 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// restoreEnv applies the --restore-env rules: NAME removes the variable,
// NAME=VALUE sets it (the sandbox overrides some variables, such as
// GOMAXPROCS, only for the launcher).
func restoreEnv(env, rules []string) []string {
	if len(rules) == 0 {
		return env
	}
	out := env[:0:0]
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		drop := false
		for _, r := range rules {
			rn, _, _ := strings.Cut(r, "=")
			if rn == name {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, kv)
		}
	}
	for _, r := range rules {
		if strings.Contains(r, "=") {
			out = append(out, r)
		}
	}
	return out
}

func launcherFail(code int, err error) int {
	fmt.Fprintf(os.Stderr, "cms %s: %v\n", LauncherCommand, err)
	return code
}

// checkSeccompSupport tells whether this kernel and architecture can run
// the launcher: the policy must exist for the architecture and the kernel
// must support seccomp filters with SECCOMP_RET_KILL_PROCESS (Linux 4.14).
func checkSeccompSupport() error {
	pol, err := defaultPolicy()
	if err != nil {
		return err
	}
	if _, err := pol.program(); err != nil {
		return err
	}
	action := uint32(unix.SECCOMP_RET_KILL_PROCESS)
	_, _, errno := unix.Syscall(unix.SYS_SECCOMP, unix.SECCOMP_GET_ACTION_AVAIL, 0, uintptr(unsafe.Pointer(&action)))
	if errno != 0 {
		return fmt.Errorf("the kernel does not support seccomp filters that kill the process: %w", errno)
	}
	return nil
}
