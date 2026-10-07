//go:build sandbox

// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package sandbox

import (
	"bytes"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The malicious battery (SPEC R2.4, N4): every program gets the verdict
// it deserves and leaves the host as it was. With cgroups every verdict
// is exact; without them (development machines) the memory limit is an
// address space limit, so a memory hog fails to allocate instead of
// being killed, and that is what the tests expect there.

// boxUID returns the user the programs of a box run as.
func boxUID(t *testing.T, b *Box) int {
	t.Helper()
	res := run(t, b, RunSpec{Argv: []string{"/usr/bin/id", "-u"}, Stdout: "uid.txt"})
	out, _, err := b.ReadFile("uid.txt", 100)
	if err != nil || res.Status != StatusOK {
		t.Fatalf("id -u: %+v %v", res, err)
	}
	uid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || uid == 0 {
		t.Fatalf("box uid %q", out)
	}
	if err := b.Remove("uid.txt"); err != nil {
		t.Fatal(err)
	}
	return uid
}

// assertNoProcesses checks that nothing runs as the box user once the
// run is over (isolate kills the whole box).
func assertNoProcesses(t *testing.T, uid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		left := processesOf(uid)
		if len(left) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("processes of the box user %d survived the run: %v", uid, left)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func processesOf(uid int) []string {
	entries, _ := os.ReadDir("/proc")
	var pids []string
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/proc", e.Name(), "status"))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if f := strings.Fields(line); len(f) > 1 && f[0] == "Uid:" && f[1] == strconv.Itoa(uid) {
				pids = append(pids, e.Name())
			}
		}
	}
	return pids
}

// battery runs a C program in a fresh box and checks the verdict and the
// processes left behind.
func battery(t *testing.T, s *Sandbox, src string, spec RunSpec) (*Box, *Result) {
	t.Helper()
	exe := gcc(t, src)
	b := newTestBox(t, s)
	uid := boxUID(t, b)
	putExe(t, b, "prog", exe)
	spec.Argv = append([]string{"./prog"}, spec.Argv...)
	if spec.CPUTime == 0 {
		spec.CPUTime = 1
	}
	if spec.WallTime == 0 {
		spec.WallTime = 5
	}
	if spec.MemoryKiB == 0 {
		spec.MemoryKiB = 64 << 10
	}
	res := run(t, b, spec)
	assertNoProcesses(t, uid)
	return b, res
}

func wantStatus(t *testing.T, res *Result, want ...Status) {
	t.Helper()
	for _, w := range want {
		if res.Status == w {
			return
		}
	}
	t.Errorf("status %s, want %v (%+v)", res.Status, want, res)
}

const okProgram = `
#include <stdio.h>
int main(void) {
	long a, b;
	if (scanf("%ld %ld", &a, &b) != 2) return 1;
	printf("%ld\n", a + b);
	return 0;
}`

func TestBatteryNormalProgram(t *testing.T) {
	s := newTestSandbox(t, nil)
	for _, seccomp := range []bool{false, true} {
		exe := gcc(t, okProgram)
		b := newTestBox(t, s)
		putExe(t, b, "prog", exe)
		if err := b.CreateFile("input.txt", []byte("40 2\n"), false); err != nil {
			t.Fatal(err)
		}
		res := run(t, b, RunSpec{
			Argv: []string{"./prog"}, Stdin: "input.txt", Stdout: "output.txt", Stderr: "stderr.txt",
			CPUTime: 1, WallTime: 3, MemoryKiB: 64 << 10, Seccomp: seccomp, RestrictWrites: true,
		})
		wantStatus(t, res, StatusOK)
		out, _, _ := b.ReadFile("output.txt", 100)
		if string(out) != "42\n" {
			t.Errorf("seccomp=%v: output %q", seccomp, out)
		}
		if res.CPUTime > 0.5 || res.WallTime > 2 {
			t.Errorf("seccomp=%v: slow trivial run: %+v", seccomp, res)
		}
	}
}

func TestBatteryForkBomb(t *testing.T) {
	s := newTestSandbox(t, nil)
	src := `
#include <unistd.h>
int main(void) { for (;;) fork(); }`
	for _, procs := range []int{0, 50} {
		for _, seccomp := range []bool{false, true} {
			_, res := battery(t, s, src, RunSpec{Processes: procs, Seccomp: seccomp, WallTime: 3})
			// The process limit stops the bomb; the CPU limit stops
			// the processes spinning on fork. Without cgroups isolate
			// only watches the CPU time of the main process while it
			// runs, so the wall clock limit may stop them first.
			if s.cfg.Cgroups || procs <= 1 {
				wantStatus(t, res, StatusTimedOut)
			} else {
				wantStatus(t, res, StatusTimedOut, StatusWallTimedOut)
			}
		}
	}
}

func TestBatteryMemoryHog(t *testing.T) {
	s := newTestSandbox(t, nil)
	src := `
#include <stdlib.h>
#include <string.h>
int main(void) {
	for (;;) {
		char *p = malloc(1 << 20);
		if (!p) return 3;
		memset(p, 1, 1 << 20);
	}
}`
	for _, seccomp := range []bool{false, true} {
		_, res := battery(t, s, src, RunSpec{MemoryKiB: 64 << 10, Seccomp: seccomp})
		if s.cfg.Cgroups {
			wantStatus(t, res, StatusMemoryLimit)
		} else {
			// Address space limit: malloc fails.
			wantStatus(t, res, StatusRuntimeError)
			if res.ExitCode != 3 {
				t.Errorf("exit code %d, want 3 (malloc failed)", res.ExitCode)
			}
		}
		if res.MemoryKiB > 64<<10+4096 {
			t.Errorf("memory %d KiB above the limit", res.MemoryKiB)
		}
	}
}

func TestBatteryStackOverflow(t *testing.T) {
	s := newTestSandbox(t, nil)
	src := `
#include <stdio.h>
int depth(int n) { volatile char pad[1024]; pad[0] = n; return depth(n + 1) + pad[0]; }
int main(void) { printf("%d\n", depth(0)); return 0; }`
	for _, seccomp := range []bool{false, true} {
		_, res := battery(t, s, src, RunSpec{StackKiB: 8 << 10, MemoryKiB: 256 << 10, Seccomp: seccomp})
		wantStatus(t, res, StatusSignal)
		if res.Signal != int(syscall.SIGSEGV) {
			t.Errorf("signal %d, want SIGSEGV", res.Signal)
		}
	}
}

func TestBatteryNetwork(t *testing.T) {
	s := newTestSandbox(t, nil)
	// A listener on the host loopback: the box has its own network
	// namespace and must not reach it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan struct{}, 1)
	go func() {
		if c, err := ln.Accept(); err == nil {
			accepted <- struct{}{}
			c.Close()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	src := `
#include <arpa/inet.h>
#include <netinet/in.h>
#include <stdlib.h>
#include <sys/socket.h>
int main(int argc, char **argv) {
	int fd = socket(AF_INET, SOCK_STREAM, 0);
	if (fd < 0) return 0;
	struct sockaddr_in a = {0};
	a.sin_family = AF_INET;
	a.sin_port = htons(atoi(argv[1]));
	a.sin_addr.s_addr = htonl(0x7f000001);
	if (connect(fd, (struct sockaddr *)&a, sizeof a) == 0) return 42;
	a.sin_addr.s_addr = htonl(0x01010101); /* 1.1.1.1 */
	a.sin_port = htons(53);
	if (connect(fd, (struct sockaddr *)&a, sizeof a) == 0) return 43;
	return 0;
}`
	_, res := battery(t, s, src, RunSpec{Argv: []string{strconv.Itoa(port)}})
	wantStatus(t, res, StatusOK) // the socket exists, but goes nowhere
	_, res = battery(t, s, src, RunSpec{Argv: []string{strconv.Itoa(port)}, Seccomp: true})
	wantStatus(t, res, StatusSecurityViolation)
	select {
	case <-accepted:
		t.Error("a sandboxed program connected to the host")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestBatteryWriteOutside(t *testing.T) {
	s := newTestSandbox(t, nil)
	victim := filepath.Join(t.TempDir(), "victim")
	src := `
#include <stdio.h>
int main(int argc, char **argv) {
	const char *paths[] = {"/usr/pwned", "/bin/pwned", "/lib/pwned", "/pwned", "/proc/pwned",
		"/dev/pwned", "/etc/pwned", "/box/pwned", "new-file-in-home", argv[1], 0};
	int escaped = 0;
	for (int i = 0; paths[i]; i++) {
		FILE *f = fopen(paths[i], "w");
		if (f) { fprintf(stderr, "wrote %s\n", paths[i]); fclose(f); escaped = 1; }
	}
	return escaped;
}`
	for _, seccomp := range []bool{false, true} {
		b, res := battery(t, s, src, RunSpec{
			Argv: []string{victim}, Stderr: "stderr.txt", Seccomp: seccomp, RestrictWrites: true,
		})
		msg, _, _ := b.ReadFile("stderr.txt", 4096)
		wantStatus(t, res, StatusOK)
		if res.Status != StatusOK {
			t.Logf("stderr: %s", msg)
		}
		for _, p := range []string{"/usr/pwned", "/bin/pwned", "/lib/pwned", "/pwned", victim} {
			if _, err := os.Stat(p); err == nil {
				os.Remove(p)
				t.Errorf("the program wrote %s on the host", p)
			}
		}
	}
}

func TestBatteryHugeFile(t *testing.T) {
	s := newTestSandbox(t, nil)
	src := `
#include <stdio.h>
#include <string.h>
int main(void) {
	static char buf[1 << 16];
	memset(buf, 'x', sizeof buf);
	FILE *f = fopen("big.txt", "w");
	if (!f) return 2;
	for (int i = 0; i < 1024; i++) fwrite(buf, 1, sizeof buf, f); /* 64 MiB */
	fclose(f);
	return 0;
}`
	for _, seccomp := range []bool{false, true} {
		b, res := battery(t, s, src, RunSpec{FileSizeKiB: 1024, Seccomp: seccomp, RestrictWrites: true, Writable: []string{"big.txt"}})
		wantStatus(t, res, StatusSignal)
		if res.Signal != int(syscall.SIGXFSZ) {
			t.Errorf("signal %d, want SIGXFSZ", res.Signal)
		}
		if st, err := b.Stat("big.txt"); err != nil || st.Size() > 1024<<10 {
			t.Errorf("big.txt: %v %v", st, err)
		}
	}
}

func TestBatteryOutputFlood(t *testing.T) {
	s := newTestSandbox(t, nil)
	src := `
#include <stdio.h>
int main(void) { for (;;) { fputs("flood flood flood flood flood flood flood flood\n", stdout); fputs("err\n", stderr); } }`
	// Into files: the file size limit stops it.
	b, res := battery(t, s, src, RunSpec{Stdout: "out.txt", Stderr: "err.txt", FileSizeKiB: 2048, Seccomp: true, RestrictWrites: true})
	wantStatus(t, res, StatusSignal)
	if st, err := b.Stat("out.txt"); err != nil || st.Size() > 2048<<10 {
		t.Errorf("out.txt: %v %v", st, err)
	}
	// Into /dev/null (no redirection): the time limit stops it, and
	// nothing reaches the worker.
	_, res = battery(t, s, src, RunSpec{Seccomp: true})
	wantStatus(t, res, StatusTimedOut)
}

func TestBatterySignals(t *testing.T) {
	s := newTestSandbox(t, nil)
	// A process of the host that must survive.
	sentinel := exec.Command("/bin/sleep", "60")
	if err := sentinel.Start(); err != nil {
		t.Fatal(err)
	}
	defer sentinel.Process.Kill()
	src := `
#include <signal.h>
#include <stdlib.h>
#include <unistd.h>
int main(int argc, char **argv) {
	kill(atoi(argv[1]), SIGKILL); /* a host pid: not even visible */
	kill(1, SIGKILL);             /* init of the box: ignored */
	kill(getppid(), SIGKILL);
	kill(-1, SIGKILL);            /* every process it may signal but itself */
	write(1, "alive\n", 6);
	kill(getpid(), SIGKILL);
	return 0;
}`
	for _, seccomp := range []bool{false, true} {
		b, res := battery(t, s, src, RunSpec{Argv: []string{strconv.Itoa(sentinel.Process.Pid)}, Stdout: "out.txt", Seccomp: seccomp})
		wantStatus(t, res, StatusSignal)
		if out, _, _ := b.ReadFile("out.txt", 100); string(out) != "alive\n" {
			t.Errorf("the program did not survive its own kill(-1): %q", out)
		}
		if res.Signal != int(syscall.SIGKILL) {
			t.Errorf("signal %d, want SIGKILL", res.Signal)
		}
	}
	if err := sentinel.Process.Signal(syscall.Signal(0)); err != nil {
		t.Errorf("the host process died: %v", err)
	}
	// A program raising SIGSYS itself without the filter is a plain
	// signal, not a security violation.
	_, res := battery(t, s, "#include <signal.h>\nint main(void) { raise(SIGSYS); return 0; }", RunSpec{})
	wantStatus(t, res, StatusSignal)
}

func TestBatteryForbiddenSyscalls(t *testing.T) {
	s := newTestSandbox(t, nil)
	for name, call := range map[string]string{
		"ptrace":   `ptrace(PTRACE_TRACEME, 0, 0, 0);`,
		"unshare":  `unshare(CLONE_NEWUSER | CLONE_NEWNS);`,
		"mount":    `mount("none", "/tmp", "tmpfs", 0, "");`,
		"bpf":      `syscall(SYS_bpf, 0, 0, 0);`,
		"keyctl":   `syscall(SYS_keyctl, 0, 0, 0);`,
		"io_uring": `syscall(SYS_io_uring_setup, 1, 0);`,
	} {
		t.Run(name, func(t *testing.T) {
			src := `
#define _GNU_SOURCE
#include <sched.h>
#include <sys/mount.h>
#include <sys/ptrace.h>
#include <sys/syscall.h>
#include <unistd.h>
int main(void) { ` + call + ` return 0; }`
			_, res := battery(t, s, src, RunSpec{Seccomp: true})
			wantStatus(t, res, StatusSecurityViolation)
			if res.Signal != int(syscall.SIGSYS) {
				t.Errorf("signal %d, want SIGSYS", res.Signal)
			}
		})
	}
}

func TestBatteryCPULoop(t *testing.T) {
	s := newTestSandbox(t, nil)
	src := `int main(void) { volatile unsigned long x = 0; for (;;) x++; }`
	for _, seccomp := range []bool{false, true} {
		_, res := battery(t, s, src, RunSpec{CPUTime: 0.5, WallTime: 5, Seccomp: seccomp})
		wantStatus(t, res, StatusTimedOut)
		if res.CPUTime < 0.5 || !res.Killed {
			t.Errorf("CPU time %.3f, killed %v", res.CPUTime, res.Killed)
		}
	}
}

func TestBatterySleep(t *testing.T) {
	s := newTestSandbox(t, nil)
	src := `#include <unistd.h>
int main(void) { sleep(30); return 0; }`
	for _, seccomp := range []bool{false, true} {
		start := time.Now()
		_, res := battery(t, s, src, RunSpec{CPUTime: 1, WallTime: 0.5, Seccomp: seccomp})
		wantStatus(t, res, StatusWallTimedOut)
		if time.Since(start) > 10*time.Second {
			t.Errorf("the wall limit took %v", time.Since(start))
		}
	}
}

// TestBatteryReadSymlinkOut: files a program leaves in the home are read
// by the worker without following links out of it, and FIFOs do not
// block it.
func TestBatteryReadSymlinkOut(t *testing.T) {
	s := newTestSandbox(t, nil)
	b := newTestBox(t, s)
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("host secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := run(t, b, RunSpec{Processes: Unlimited, Argv: []string{"/bin/sh", "-c",
		"ln -s " + secret + " output.txt && ln -s /etc/passwd passwd.txt && mkdir d && ln -s / d/root && mkfifo fifo.txt"}})
	wantStatus(t, res, StatusOK)
	for _, name := range []string{"output.txt", "passwd.txt", "d/root/etc/passwd", "fifo.txt"} {
		done := make(chan struct{})
		go func() {
			defer close(done)
			if data, _, err := b.ReadFile(name, 1024); err == nil {
				t.Errorf("%s read through a link or special file: %q", name, bytes.TrimSpace(data))
			}
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("reading %s blocked", name)
		}
	}
}

// TestBatteryNoLeakAcrossRuns: a run cannot leave a process behind that
// would spy on the next one (CMS reuses boxes between steps).
func TestBatteryNoLeakAcrossRuns(t *testing.T) {
	s := newTestSandbox(t, nil)
	src := `
#include <unistd.h>
int main(void) { if (fork() == 0) { setsid(); for (;;) sleep(1); } return 0; }`
	for _, seccomp := range []bool{false, true} {
		_, res := battery(t, s, src, RunSpec{Processes: 4, Seccomp: seccomp})
		wantStatus(t, res, StatusOK)
	}
}
