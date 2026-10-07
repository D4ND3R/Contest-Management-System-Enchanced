// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package sandbox

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
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
	// prctl(PR_SET_NO_NEW_PRIVS), seccomp and execve must happen on the
	// same OS thread.
	runtime.LockOSThread()
	// Limits the launcher itself could not live with: the Go runtime
	// needs a few threads and reserves address space, so the sandbox
	// grants more and the program gets the real values here. Lowering a
	// limit never fails; both values are set so it cannot be raised back.
	if la.nproc > 0 {
		lim := unix.Rlimit{Cur: uint64(la.nproc), Max: uint64(la.nproc)}
		if err := unix.Setrlimit(unix.RLIMIT_NPROC, &lim); err != nil {
			return launcherFail(launcherSetupFailed, fmt.Errorf("setrlimit(NPROC): %w", err))
		}
	}
	if la.rlimitAS > 0 {
		lim := unix.Rlimit{Cur: uint64(la.rlimitAS) << 10, Max: uint64(la.rlimitAS) << 10}
		if err := unix.Setrlimit(unix.RLIMIT_AS, &lim); err != nil {
			return launcherFail(launcherSetupFailed, fmt.Errorf("setrlimit(AS): %w", err))
		}
	}
	env := restoreEnv(os.Environ(), la.restoreEnv)
	if err := installFilter(prog); err != nil {
		return launcherFail(launcherSetupFailed, err)
	}
	err = unix.Exec(la.argv[0], la.argv, env)
	return launcherFail(launcherExecFailed, fmt.Errorf("execve(%q): %w", la.argv[0], err))
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
