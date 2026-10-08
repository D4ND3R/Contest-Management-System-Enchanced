// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// writeLauncher writes a launcher with the filter prog in dir and returns
// its path. It must not run while other goroutines start processes: a
// child holding the file open for writing between fork and exec would make
// running it fail with ETXTBSY.
func writeLauncher(t *testing.T, dir string, prog []unix.SockFilter) string {
	t.Helper()
	img, err := buildLauncher(runtime.GOARCH, prog)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, launcherBinary)
	if err := os.WriteFile(path, img, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// defaultLauncher writes the launcher of the default policy in a
// temporary directory.
func defaultLauncher(t *testing.T) string {
	t.Helper()
	if err := checkSeccompSupport(); err != nil {
		t.Skipf("seccomp not supported here: %v", err)
	}
	img, err := launcherImage()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), launcherBinary)
	if err := os.WriteFile(path, img, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// runLauncher runs `launcher <test binary>` as helper name, outside
// isolate.
func runLauncher(t *testing.T, launcher, name string) (*os.ProcessState, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(launcher, exe)
	cmd.Env = append(os.Environ(), helperEnv+"="+name)
	out, _ := cmd.CombinedOutput()
	return cmd.ProcessState, string(out)
}

func TestSeccompPolicy(t *testing.T) {
	launcher := defaultLauncher(t)
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
			st, out := runLauncher(t, launcher, name)
			if !st.Success() {
				t.Errorf("%s: %v\n%s", name, st, out)
			}
		})
	}
	for _, name := range denied {
		t.Run("deny/"+name, func(t *testing.T) {
			t.Parallel()
			st, out := runLauncher(t, launcher, name)
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

// TestLauncherPassesArgsAndEnv: the program gets the arguments after the
// launcher's own name, argv[0] included, and the environment unchanged.
func TestLauncherPassesArgsAndEnv(t *testing.T) {
	launcher := defaultLauncher(t)
	cmd := exec.Command(launcher, "/bin/sh", "-c", `printf '%s|' "$0" "$@" "$FOO"; printf '%s\n' "$(cat /proc/self/status | grep -c Seccomp:.2)"`,
		"zero", "one", "two words", "")
	cmd.Env = []string{"FOO=bar baz", "PATH=/usr/bin:/bin"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	// $0 of sh -c is the first argument after the script; the shell and
	// its children run under the filter (Seccomp: 2).
	if want := "zero|one|two words||bar baz|1\n"; string(out) != want {
		t.Errorf("output %q, want %q", out, want)
	}
}

func TestLauncherFailures(t *testing.T) {
	launcher := defaultLauncher(t)
	// A filter the kernel rejects (it does not end with a return).
	badFilter := writeLauncher(t, t.TempDir(), []unix.SockFilter{{Code: ldAbs, K: offNr}})
	for _, tc := range []struct {
		argv []string
		code int
		msg  string
	}{
		{[]string{launcher}, launcherSetupFailed, launcherMessages[0]},
		{[]string{badFilter, "/bin/true"}, launcherSetupFailed, launcherMessages[1]},
		{[]string{launcher, "/nonexistent/program"}, launcherExecFailed, launcherMessages[2]},
		{[]string{launcher, "relative/program"}, launcherExecFailed, launcherMessages[2]},
	} {
		cmd := exec.Command(tc.argv[0], tc.argv[1:]...)
		out, _ := cmd.CombinedOutput()
		if cmd.ProcessState.ExitCode() != tc.code || string(out) != tc.msg {
			t.Errorf("%q: exit %d, want %d with %q; output %q", tc.argv[1:], cmd.ProcessState.ExitCode(), tc.code, tc.msg, out)
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
