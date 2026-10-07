// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package sandbox

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

// The seccomp filter is a small classic-BPF program built here instead of
// with a filter library: the policy is a deny list of a few dozen system
// calls plus three argument checks, and keeping it in one readable place
// makes it easy to audit (see DECISIONS.md).
//
// Layout of the program (every jump goes forward, as BPF requires):
//
//	load arch; arch != native             -> KILL
//	load nr;   [amd64] nr >= x32 bit      -> KILL
//	nr == socket/clone/personality        -> argument check
//	nr == clone3                          -> ERRNO(ENOSYS)
//	nr in the deny list                   -> KILL
//	                                         ALLOW
//
// KILL is SECCOMP_RET_KILL_PROCESS: the whole program dies with SIGSYS,
// which the sandbox reports as StatusSecurityViolation.

// Offsets inside struct seccomp_data (linux/seccomp.h). The loads read
// 32-bit words in host byte order; only little-endian architectures are
// supported (amd64, arm64), so the low half of args[0] is at offset 16.
const (
	offNr       = 0
	offArch     = 4
	offArg0Low  = 16
	errnoENOSYS = 38
)

// namespaceCloneFlags are the clone(2) flags that create namespaces:
// unshare(2) by another name.
const namespaceCloneFlags = unix.CLONE_NEWNS | unix.CLONE_NEWUTS | unix.CLONE_NEWIPC |
	unix.CLONE_NEWUSER | unix.CLONE_NEWPID | unix.CLONE_NEWNET | unix.CLONE_NEWCGROUP |
	unix.CLONE_NEWTIME

// commonDenied are the system calls that kill the program on every
// architecture. They are what a contest program never needs and what an
// attack on the host, the sandbox or other submissions would: debugging
// other processes, filesystems and namespaces, kernel modules, clocks,
// keyrings, eBPF, perf, userfaultfd, io_uring, file handles.
var commonDenied = []struct {
	name string
	nr   uint32
}{
	{"ptrace", unix.SYS_PTRACE},
	{"process_vm_readv", unix.SYS_PROCESS_VM_READV},
	{"process_vm_writev", unix.SYS_PROCESS_VM_WRITEV},
	{"pidfd_getfd", unix.SYS_PIDFD_GETFD},
	{"process_madvise", unix.SYS_PROCESS_MADVISE},
	{"mount", unix.SYS_MOUNT},
	{"umount2", unix.SYS_UMOUNT2},
	{"pivot_root", unix.SYS_PIVOT_ROOT},
	{"chroot", unix.SYS_CHROOT},
	{"open_tree", unix.SYS_OPEN_TREE},
	{"move_mount", unix.SYS_MOVE_MOUNT},
	{"fsopen", unix.SYS_FSOPEN},
	{"fsconfig", unix.SYS_FSCONFIG},
	{"fsmount", unix.SYS_FSMOUNT},
	{"fspick", unix.SYS_FSPICK},
	{"mount_setattr", unix.SYS_MOUNT_SETATTR},
	{"swapon", unix.SYS_SWAPON},
	{"swapoff", unix.SYS_SWAPOFF},
	{"reboot", unix.SYS_REBOOT},
	{"kexec_load", unix.SYS_KEXEC_LOAD},
	{"kexec_file_load", unix.SYS_KEXEC_FILE_LOAD},
	{"init_module", unix.SYS_INIT_MODULE},
	{"finit_module", unix.SYS_FINIT_MODULE},
	{"delete_module", unix.SYS_DELETE_MODULE},
	{"acct", unix.SYS_ACCT},
	{"settimeofday", unix.SYS_SETTIMEOFDAY},
	{"clock_settime", unix.SYS_CLOCK_SETTIME},
	{"clock_adjtime", unix.SYS_CLOCK_ADJTIME},
	{"adjtimex", unix.SYS_ADJTIMEX},
	{"sethostname", unix.SYS_SETHOSTNAME},
	{"setdomainname", unix.SYS_SETDOMAINNAME},
	{"unshare", unix.SYS_UNSHARE},
	{"setns", unix.SYS_SETNS},
	{"bpf", unix.SYS_BPF},
	{"perf_event_open", unix.SYS_PERF_EVENT_OPEN},
	{"keyctl", unix.SYS_KEYCTL},
	{"add_key", unix.SYS_ADD_KEY},
	{"request_key", unix.SYS_REQUEST_KEY},
	{"userfaultfd", unix.SYS_USERFAULTFD},
	{"quotactl", unix.SYS_QUOTACTL},
	{"quotactl_fd", unix.SYS_QUOTACTL_FD},
	{"open_by_handle_at", unix.SYS_OPEN_BY_HANDLE_AT},
	{"name_to_handle_at", unix.SYS_NAME_TO_HANDLE_AT},
	{"fanotify_init", unix.SYS_FANOTIFY_INIT},
	{"io_uring_setup", unix.SYS_IO_URING_SETUP},
	{"io_uring_enter", unix.SYS_IO_URING_ENTER},
	{"io_uring_register", unix.SYS_IO_URING_REGISTER},
	{"lookup_dcookie", unix.SYS_LOOKUP_DCOOKIE},
	{"vhangup", unix.SYS_VHANGUP},
	{"syslog", unix.SYS_SYSLOG},
}

// allowedPersonalities are the personality(2) arguments that runtimes may
// use: querying the current value and the default Linux personality.
// Anything else (disabling address randomization, READ_IMPLIES_EXEC...)
// kills the program. None of the supported runtimes calls personality.
var allowedPersonalities = []uint32{0xffffffff, 0 /* PER_LINUX */}

// policy describes a seccomp policy for one architecture.
type policy struct {
	arch      uint32 // AUDIT_ARCH_* of the native architecture
	x32Check  bool   // kill syscall numbers with the x32 ABI bit (amd64)
	denied    []uint32
	socketNr  uint32
	cloneNr   uint32
	clone3Nr  uint32
	personaNr uint32
}

// defaultPolicy returns the "default" policy for the running architecture.
func defaultPolicy() (*policy, error) {
	if nativeArch == 0 {
		return nil, fmt.Errorf("the seccomp launcher does not support this architecture")
	}
	p := &policy{
		arch:      nativeArch,
		x32Check:  nativeX32Check,
		socketNr:  unix.SYS_SOCKET,
		cloneNr:   unix.SYS_CLONE,
		clone3Nr:  unix.SYS_CLONE3,
		personaNr: unix.SYS_PERSONALITY,
	}
	for _, d := range commonDenied {
		p.denied = append(p.denied, d.nr)
	}
	p.denied = append(p.denied, archDenied...)
	return p, nil
}

// x32SyscallBit marks system calls of the x32 ABI on amd64. They share
// AUDIT_ARCH_X86_64 but have their own numbers, so a deny list keyed on
// numbers must reject them all.
const x32SyscallBit = 0x40000000

// bpfAsm is a minimal assembler with forward labels.
type bpfAsm struct {
	ins    []unix.SockFilter
	fixups []fixup
	labels map[string]int
}

type fixup struct {
	at     int
	jt, jf string
}

func (a *bpfAsm) stmt(code uint16, k uint32) {
	a.ins = append(a.ins, unix.SockFilter{Code: code, K: k})
}

// jump appends a conditional jump; an empty label means "next instruction".
func (a *bpfAsm) jump(code uint16, k uint32, jt, jf string) {
	a.fixups = append(a.fixups, fixup{at: len(a.ins), jt: jt, jf: jf})
	a.ins = append(a.ins, unix.SockFilter{Code: code, K: k})
}

func (a *bpfAsm) label(name string) {
	if a.labels == nil {
		a.labels = map[string]int{}
	}
	a.labels[name] = len(a.ins)
}

func (a *bpfAsm) assemble() ([]unix.SockFilter, error) {
	off := func(from int, name string) (uint8, error) {
		if name == "" {
			return 0, nil
		}
		to, ok := a.labels[name]
		if !ok {
			return 0, fmt.Errorf("bpf: undefined label %q", name)
		}
		d := to - from - 1
		if d < 0 || d > 255 {
			return 0, fmt.Errorf("bpf: jump to %q out of range (%d)", name, d)
		}
		return uint8(d), nil
	}
	for _, f := range a.fixups {
		jt, err := off(f.at, f.jt)
		if err != nil {
			return nil, err
		}
		jf, err := off(f.at, f.jf)
		if err != nil {
			return nil, err
		}
		a.ins[f.at].Jt, a.ins[f.at].Jf = jt, jf
	}
	return a.ins, nil
}

const (
	ldAbs  = unix.BPF_LD | unix.BPF_W | unix.BPF_ABS
	jeqK   = unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K
	jgeK   = unix.BPF_JMP | unix.BPF_JGE | unix.BPF_K
	jsetK  = unix.BPF_JMP | unix.BPF_JSET | unix.BPF_K
	retK   = unix.BPF_RET | unix.BPF_K
	kill   = unix.SECCOMP_RET_KILL_PROCESS
	allow  = unix.SECCOMP_RET_ALLOW
	enosys = unix.SECCOMP_RET_ERRNO | errnoENOSYS
)

// program compiles the policy to classic BPF.
func (p *policy) program() ([]unix.SockFilter, error) {
	var a bpfAsm
	a.stmt(ldAbs, offArch)
	a.jump(jeqK, p.arch, "", "kill")
	a.stmt(ldAbs, offNr)
	if p.x32Check {
		a.jump(jgeK, x32SyscallBit, "kill", "")
	}
	a.jump(jeqK, p.socketNr, "socket", "")
	a.jump(jeqK, p.cloneNr, "clone", "")
	a.jump(jeqK, p.personaNr, "personality", "")
	a.jump(jeqK, p.clone3Nr, "enosys", "")
	for _, nr := range p.denied {
		a.jump(jeqK, nr, "kill", "")
	}
	a.stmt(retK, allow)

	// socket(2): only AF_UNIX (runtimes talk to nscd that way; abstract
	// sockets are confined to the network namespace of the box).
	a.label("socket")
	a.stmt(ldAbs, offArg0Low)
	a.jump(jeqK, unix.AF_UNIX, "allow", "kill")

	// clone(2): threads and processes, but no new namespaces. The flags
	// are the first argument on every supported architecture.
	a.label("clone")
	a.stmt(ldAbs, offArg0Low)
	a.jump(jsetK, namespaceCloneFlags, "kill", "allow")

	a.label("personality")
	a.stmt(ldAbs, offArg0Low)
	for _, v := range allowedPersonalities {
		a.jump(jeqK, v, "allow", "")
	}
	a.stmt(retK, kill)

	// clone3(2) passes its flags in memory, which BPF cannot inspect:
	// answer ENOSYS so that libc falls back to clone(2), which is checked.
	a.label("enosys")
	a.stmt(retK, enosys)

	a.label("allow")
	a.stmt(retK, allow)
	a.label("kill")
	a.stmt(retK, kill)
	return a.assemble()
}

// installFilter sets no_new_privs and loads the filter on every thread of
// the process (TSYNC), so that nothing runs unfiltered after execve.
func installFilter(prog []unix.SockFilter) error {
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("prctl(PR_SET_NO_NEW_PRIVS): %w", err)
	}
	fprog := unix.SockFprog{Len: uint16(len(prog)), Filter: &prog[0]}
	r, _, errno := unix.Syscall(unix.SYS_SECCOMP, unix.SECCOMP_SET_MODE_FILTER,
		unix.SECCOMP_FILTER_FLAG_TSYNC, uintptr(unsafe.Pointer(&fprog)))
	if errno != 0 {
		return fmt.Errorf("seccomp(SECCOMP_SET_MODE_FILTER): %w", errno)
	}
	if r != 0 {
		return fmt.Errorf("seccomp: thread %d could not be synchronized", r)
	}
	return nil
}
