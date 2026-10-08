// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// These tests run the launcher without isolate: no_new_privs and seccomp
// need no privileges, so they run in every `go test`. The program the
// launcher executes is the test binary itself, as one of these helpers.

func sys(nr uintptr, args ...uintptr) unix.Errno {
	var a [6]uintptr
	copy(a[:], args)
	_, _, errno := unix.Syscall6(nr, a[0], a[1], a[2], a[3], a[4], a[5])
	return errno
}

// helpers exit 0 when the call came back (allowed), whatever its error.
// Their arguments are invalid on purpose (NULL pointers, bad lengths):
// should the filter ever let a call through, it must do nothing.
var helpers = map[string]func() int{
	"true": func() int { return 0 },
	"env": func() int {
		fmt.Println(strings.Join(os.Environ(), "\n"))
		return 0
	},
	"threads": func() int {
		// Force new OS threads (clone without namespace flags).
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				runtime.LockOSThread()
				_ = unix.Getpid()
			}()
		}
		wg.Wait()
		return 0
	},
	"socket-unix": func() int {
		fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM, 0)
		if err != nil {
			return 1
		}
		unix.Close(fd)
		return 0
	},
	"clone3": func() int {
		// The filter answers ENOSYS (the kernel would say EINVAL).
		if errno := sys(unix.SYS_CLONE3, 0, 0); errno != unix.ENOSYS {
			fmt.Println("clone3:", errno)
			return 1
		}
		return 0
	},
	"personality-query": func() int { sys(unix.SYS_PERSONALITY, 0xffffffff); return 0 },
	"personality-set":   func() int { sys(unix.SYS_PERSONALITY, 0x0040000 /* ADDR_NO_RANDOMIZE */); return 0 },
	"ptrace":            func() int { sys(unix.SYS_PTRACE, unix.PTRACE_TRACEME); return 0 },
	"process_vm_readv":  func() int { sys(unix.SYS_PROCESS_VM_READV, 1); return 0 },
	"mount":             func() int { sys(unix.SYS_MOUNT); return 0 },
	"umount2":           func() int { sys(unix.SYS_UMOUNT2); return 0 },
	"chroot":            func() int { sys(unix.SYS_CHROOT); return 0 },
	"pivot_root":        func() int { sys(unix.SYS_PIVOT_ROOT); return 0 },
	"unshare":           func() int { sys(unix.SYS_UNSHARE, unix.CLONE_NEWUSER); return 0 },
	"setns":             func() int { sys(unix.SYS_SETNS, 0, 0); return 0 },
	// Sockets other than AF_UNIX fail (the JVM probes AF_INET6).
	"socket-inet":       func() int { return socketRefused(unix.AF_INET, unix.SOCK_STREAM) },
	"socket-inet6":      func() int { return socketRefused(unix.AF_INET6, unix.SOCK_STREAM) },
	"socket-netlink":    func() int { return socketRefused(unix.AF_NETLINK, unix.SOCK_RAW) },
	"socket-vsock":      func() int { return socketRefused(unix.AF_VSOCK, unix.SOCK_STREAM) },
	"socket-packet":     func() int { return socketRefused(unix.AF_PACKET, unix.SOCK_RAW) },
	"keyctl":            func() int { sys(unix.SYS_KEYCTL); return 0 },
	"bpf":               func() int { sys(unix.SYS_BPF); return 0 },
	"perf_event_open":   func() int { sys(unix.SYS_PERF_EVENT_OPEN); return 0 },
	"userfaultfd":       func() int { sys(unix.SYS_USERFAULTFD); return 0 },
	"io_uring_setup":    func() int { sys(unix.SYS_IO_URING_SETUP); return 0 },
	"kexec_load":        func() int { sys(unix.SYS_KEXEC_LOAD, 0, 1000 /* > KEXEC_SEGMENT_MAX */); return 0 },
	"init_module":       func() int { sys(unix.SYS_INIT_MODULE); return 0 },
	"reboot":            func() int { sys(unix.SYS_REBOOT); return 0 },
	"settimeofday":      func() int { sys(unix.SYS_SETTIMEOFDAY); return 0 },
	"sethostname":       func() int { sys(unix.SYS_SETHOSTNAME, 0, 1000 /* > __NEW_UTS_LEN */); return 0 },
	"swapon":            func() int { sys(unix.SYS_SWAPON); return 0 },
	"open_by_handle_at": func() int { sys(unix.SYS_OPEN_BY_HANDLE_AT); return 0 },
	"clone-newuser": func() int {
		// If the filter let it through, the child must not run Go code.
		r, _, errno := unix.RawSyscall6(unix.SYS_CLONE, unix.CLONE_NEWUSER|uintptr(unix.SIGCHLD), 0, 0, 0, 0, 0)
		if errno == 0 && r == 0 {
			unix.RawSyscall(unix.SYS_EXIT_GROUP, 0, 0, 0)
		}
		return 0
	},
}

// runLauncher runs `<test binary> sandbox-exec [flags] -- <test binary>`
// as helper name, outside isolate.
func runLauncher(t *testing.T, name string, flags ...string) (*os.ProcessState, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args := append([]string{LauncherCommand}, flags...)
	args = append(args, "--", exe)
	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), helperEnv+"="+name)
	out, _ := cmd.CombinedOutput()
	return cmd.ProcessState, string(out)
}

func TestSeccompPolicy(t *testing.T) {
	if err := checkSeccompSupport(); err != nil {
		t.Skipf("seccomp not supported here: %v", err)
	}
	allowed := []string{"true", "threads", "socket-unix", "clone3", "personality-query",
		"socket-inet", "socket-inet6", "socket-netlink", "socket-vsock", "socket-packet"}
	denied := []string{
		"personality-set", "ptrace", "process_vm_readv", "mount", "umount2", "chroot", "pivot_root",
		"unshare", "setns", "keyctl", "bpf",
		"perf_event_open", "userfaultfd", "io_uring_setup", "kexec_load", "init_module", "reboot",
		"settimeofday", "sethostname", "swapon", "open_by_handle_at", "clone-newuser",
	}
	denied = append(denied, archDeniedHelpers...)
	for _, name := range allowed {
		t.Run("allow/"+name, func(t *testing.T) {
			t.Parallel()
			st, out := runLauncher(t, name)
			if !st.Success() {
				t.Errorf("%s: %v\n%s", name, st, out)
			}
		})
	}
	for _, name := range denied {
		t.Run("deny/"+name, func(t *testing.T) {
			t.Parallel()
			st, out := runLauncher(t, name)
			ws := st.Sys().(syscall.WaitStatus)
			if !ws.Signaled() || ws.Signal() != syscall.SIGSYS {
				t.Errorf("%s: want SIGSYS, got %v\n%s", name, st, out)
			}
		})
	}
}

// TestSeccompHelpersWithoutFilter is the control: without the launcher
// the same helpers are not killed, so the SIGSYS above is the filter's.
func TestSeccompHelpersWithoutFilter(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ptrace", "personality-set", "keyctl"} {
		cmd := exec.Command(exe)
		cmd.Env = append(os.Environ(), helperEnv+"="+name)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s without the filter: %v\n%s", name, err, out)
		}
	}
}

func TestLauncherLimitsAndEnv(t *testing.T) {
	if err := checkSeccompSupport(); err != nil {
		t.Skipf("seccomp not supported here: %v", err)
	}
	t.Setenv("GOGC", "off")
	t.Setenv("GOMAXPROCS", "1")
	st, out := runLauncher(t, "env", "--restore-env=GOGC=50", "--restore-env=GOMAXPROCS")
	if !st.Success() {
		t.Fatalf("%v\n%s", st, out)
	}
	vars := map[string]bool{}
	for _, l := range strings.Split(out, "\n") {
		vars[l] = true
		if strings.HasPrefix(l, "GOMAXPROCS=") {
			t.Errorf("GOMAXPROCS not removed: %s", l)
		}
	}
	if !vars["GOGC=50"] || !vars[helperEnv+"=env"] {
		t.Errorf("environment not restored:\n%s", out)
	}

	// The limits (an external program: the race detector of the test
	// binary cannot live with an address space limit).
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, LauncherCommand, "--nproc=7", "--rlimit-as=1048576", "--", "/bin/cat", "/proc/self/limits")
	out2, err := cmd.CombinedOutput()
	limits := strings.Join(strings.Fields(string(out2)), " ")
	if err != nil || !strings.Contains(limits, "Max processes 7 7 processes") ||
		!strings.Contains(limits, "Max address space 1073741824 1073741824 bytes") {
		t.Errorf("limits not applied: %v\n%s", err, out2)
	}
}

func TestLauncherFailures(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args []string
		code int
		msg  string
	}{
		{[]string{"--"}, launcherSetupFailed, "no program"},
		{[]string{"--policy=strict", "--", "/bin/true"}, launcherSetupFailed, "unknown policy"},
		{[]string{"--nproc=0", "--", "/bin/true"}, launcherSetupFailed, "--nproc"},
		{[]string{"--bogus=1", "--", "/bin/true"}, launcherSetupFailed, "unknown flag"},
		{[]string{"/bin/true"}, launcherSetupFailed, "unexpected argument"},
		{[]string{"--", "/nonexistent/program"}, launcherExecFailed, "execve"},
	} {
		cmd := exec.Command(exe, append([]string{LauncherCommand}, tc.args...)...)
		out, _ := cmd.CombinedOutput()
		if cmd.ProcessState.ExitCode() != tc.code || !strings.Contains(string(out), tc.msg) {
			t.Errorf("%q: exit %d, want %d with %q:\n%s", tc.args, cmd.ProcessState.ExitCode(), tc.code, tc.msg, out)
		}
	}
}

// socketRefused creates a socket and reports success (0) when the filter
// refused it with EAFNOSUPPORT.
func socketRefused(family, typ int) int {
	r, _, errno := unix.RawSyscall(unix.SYS_SOCKET, uintptr(family), uintptr(typ), 0)
	if errno == unix.EAFNOSUPPORT {
		return 0
	}
	if errno == 0 {
		unix.Close(int(r))
	}
	fmt.Println("socket:", errno)
	return 1
}
