// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package sandbox

import (
	"debug/elf"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

// The launcher executable (see launcher.go) is built here, byte by byte:
//
//	ELF header, 2 program headers   (PT_LOAD of the whole file, read+exec;
//	                                 PT_GNU_STACK, read+write)
//	machine code                    (launcherCode)
//	messages                        (launcherMessages)
//	zero padding to 8 bytes
//	struct sock_fprog               {len, pointer to the filter}
//	struct sock_filter[len]         (the BPF program of the policy)
//
// The code is position-dependent only through the filter pointer of
// struct sock_fprog: the file is an ET_EXEC, which the kernel always maps
// at its address, launcherBase. The code does, in order:
//
//	argc < 2                                    -> "no program to run", exit 125
//	prctl(PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0)      -> on error "cannot install the seccomp filter", exit 125
//	seccomp(SECCOMP_SET_MODE_FILTER, 0, &fprog) -> on error the same
//	execve(argv[1], &argv[1], envp)             -> on error "cannot execute the program", exit 127
//
// so the program gets the arguments that follow the launcher's name and
// the environment unchanged. The launcher has a single thread, so the
// filter needs no TSYNC. The machine code below is the output of an
// assembler for the source in launcher_linux_test.go
// (TestLauncherCodeMatchesSource checks it when llvm-mc or GNU as is
// installed); each line shows its instruction.

// launcherBase is the address the launcher is loaded at (the usual one of
// non-PIE executables).
const launcherBase = 0x400000

// Sizes of the ELF64 structures and of the seccomp structures (64-bit).
const (
	elfHeaderSize   = 64
	elfProgHdrSize  = 56
	launcherCodeOff = elfHeaderSize + 2*elfProgHdrSize
	sockFprogSize   = 16
	sockFilterSize  = 8
	bpfMaxInsns     = 4096 // BPF_MAXINSNS: the kernel rejects longer filters
	// launcherAlign aligns the segment for every page size of the
	// supported architectures (arm64 kernels may use 64 KiB pages).
	launcherAlign = 0x10000
)

// launcherMessages follow the machine code, in this order. The code
// addresses them by position and length: they cannot change without
// reassembling it.
var launcherMessages = []string{
	"cms seccomp launcher: no program to run\n",
	"cms seccomp launcher: cannot install the seccomp filter\n",
	"cms seccomp launcher: cannot execute the program\n",
}

type launcherArch struct {
	machine elf.Machine
	code    []string // instructions, as hexadecimal bytes in memory order
}

// launcherCode is the machine code of the launcher for each supported
// architecture (those of defaultPolicy).
var launcherCode = map[string]launcherArch{
	"amd64": {elf.EM_X86_64, []string{
		"48 8b 0c 24",          // _start:       movq (%rsp), %rcx                       argc
		"48 83 f9 02",          //               cmpq $2, %rcx
		"72 63",                //               jb usage
		"b8 9d 00 00 00",       //               movl $157, %eax                         prctl
		"bf 26 00 00 00",       //               movl $38, %edi                          PR_SET_NO_NEW_PRIVS
		"be 01 00 00 00",       //               movl $1, %esi
		"31 d2",                //               xorl %edx, %edx
		"45 31 d2",             //               xorl %r10d, %r10d
		"45 31 c0",             //               xorl %r8d, %r8d
		"0f 05",                //               syscall
		"48 85 c0",             //               testq %rax, %rax
		"75 58",                //               jnz setup_failed
		"b8 3d 01 00 00",       //               movl $317, %eax                         seccomp
		"bf 01 00 00 00",       //               movl $1, %edi                           SECCOMP_SET_MODE_FILTER
		"31 f6",                //               xorl %esi, %esi                         no flags
		"48 8d 15 05 01 00 00", //               leaq fprog(%rip), %rdx
		"0f 05",                //               syscall
		"48 85 c0",             //               testq %rax, %rax
		"75 3e",                //               jnz setup_failed
		"48 8b 0c 24",          //               movq (%rsp), %rcx                       argc (syscall clobbers rcx)
		"48 8d 74 24 10",       //               leaq 16(%rsp), %rsi                     &argv[1]
		"48 8b 3e",             //               movq (%rsi), %rdi                       argv[1]
		"48 8d 54 cc 10",       //               leaq 16(%rsp,%rcx,8), %rdx              envp = &argv[argc+1]
		"b8 3b 00 00 00",       //               movl $59, %eax                          execve
		"0f 05",                //               syscall
		"48 8d 35 a7 00 00 00", //               leaq exec_msg(%rip), %rsi
		"ba 31 00 00 00",       //               movl $(exec_msg_end - exec_msg), %edx
		"bb 7f 00 00 00",       //               movl $127, %ebx
		"eb 24",                //               jmp fail
		"48 8d 35 34 00 00 00", // usage:        leaq usage_msg(%rip), %rsi
		"ba 28 00 00 00",       //               movl $(usage_msg_end - usage_msg), %edx
		"bb 7d 00 00 00",       //               movl $125, %ebx
		"eb 11",                //               jmp fail
		"48 8d 35 49 00 00 00", // setup_failed: leaq setup_msg(%rip), %rsi
		"ba 38 00 00 00",       //               movl $(setup_msg_end - setup_msg), %edx
		"bb 7d 00 00 00",       //               movl $125, %ebx
		"b8 01 00 00 00",       // fail:         movl $1, %eax                           write
		"bf 02 00 00 00",       //               movl $2, %edi                           stderr
		"0f 05",                //               syscall
		"b8 e7 00 00 00",       //               movl $231, %eax                         exit_group
		"89 df",                //               movl %ebx, %edi
		"0f 05",                //               syscall
		"0f 0b",                //               ud2
	}},
	"arm64": {elf.EM_AARCH64, []string{
		"e9 03 40 f9", // _start:       ldr x9, [sp]                            argc
		"3f 09 00 f1", //               cmp x9, #2
		"03 03 00 54", //               b.lo usage
		"e8 14 80 d2", //               mov x8, #167                            prctl
		"c0 04 80 d2", //               mov x0, #38                             PR_SET_NO_NEW_PRIVS
		"21 00 80 d2", //               mov x1, #1
		"02 00 80 d2", //               mov x2, #0
		"03 00 80 d2", //               mov x3, #0
		"04 00 80 d2", //               mov x4, #0
		"01 00 00 d4", //               svc #0
		"80 02 00 b5", //               cbnz x0, setup_failed
		"a8 22 80 d2", //               mov x8, #277                            seccomp
		"20 00 80 d2", //               mov x0, #1                              SECCOMP_SET_MODE_FILTER
		"01 00 80 d2", //               mov x1, #0                              no flags
		"02 08 00 10", //               adr x2, fprog
		"01 00 00 d4", //               svc #0
		"c0 01 00 b5", //               cbnz x0, setup_failed
		"e1 43 00 91", //               add x1, sp, #16                         &argv[1]
		"20 00 40 f9", //               ldr x0, [x1]                            argv[1]
		"22 0c 09 8b", //               add x2, x1, x9, lsl #3                  envp = &argv[argc+1]
		"a8 1b 80 d2", //               mov x8, #221                            execve
		"01 00 00 d4", //               svc #0
		"41 05 00 10", //               adr x1, exec_msg
		"22 06 80 d2", //               mov x2, #(exec_msg_end - exec_msg)
		"f3 0f 80 d2", //               mov x19, #127
		"08 00 00 14", //               b fail
		"c1 01 00 10", // usage:        adr x1, usage_msg
		"02 05 80 d2", //               mov x2, #(usage_msg_end - usage_msg)
		"b3 0f 80 d2", //               mov x19, #125
		"04 00 00 14", //               b fail
		"81 02 00 10", // setup_failed: adr x1, setup_msg
		"02 07 80 d2", //               mov x2, #(setup_msg_end - setup_msg)
		"b3 0f 80 d2", //               mov x19, #125
		"40 00 80 d2", // fail:         mov x0, #2                              stderr
		"08 08 80 d2", //               mov x8, #64                             write
		"01 00 00 d4", //               svc #0
		"e0 03 13 aa", //               mov x0, x19
		"c8 0b 80 d2", //               mov x8, #94                             exit_group
		"01 00 00 d4", //               svc #0
		"00 00 00 00", //               udf #0
	}},
}

// launcherText returns the code and messages of the launcher for arch,
// padded to 8 bytes: what precedes struct sock_fprog.
func launcherText(arch string) (elf.Machine, []byte, error) {
	a, ok := launcherCode[arch]
	if !ok {
		return 0, nil, fmt.Errorf("the seccomp launcher does not support %s", arch)
	}
	var text []byte
	for _, ins := range a.code {
		b, err := hex.DecodeString(strings.ReplaceAll(ins, " ", ""))
		if err != nil {
			return 0, nil, fmt.Errorf("seccomp launcher code for %s: %q: %w", arch, ins, err)
		}
		text = append(text, b...)
	}
	text = append(text, strings.Join(launcherMessages, "")...)
	for len(text)%8 != 0 {
		text = append(text, 0)
	}
	return a.machine, text, nil
}

// buildLauncher returns the launcher executable for arch (a GOARCH) that
// installs the filter prog.
func buildLauncher(arch string, prog []unix.SockFilter) ([]byte, error) {
	machine, text, err := launcherText(arch)
	if err != nil {
		return nil, err
	}
	if len(prog) == 0 || len(prog) > bpfMaxInsns {
		return nil, fmt.Errorf("seccomp launcher: %d BPF instructions (1 to %d)", len(prog), bpfMaxInsns)
	}
	filterOff := launcherCodeOff + len(text) + sockFprogSize
	size := filterOff + sockFilterSize*len(prog)
	le := binary.LittleEndian // amd64 and arm64
	b := make([]byte, 0, size)

	// ELF header.
	b = append(b, elf.ELFMAG...)
	b = append(b, byte(elf.ELFCLASS64), byte(elf.ELFDATA2LSB), byte(elf.EV_CURRENT), byte(elf.ELFOSABI_NONE))
	b = append(b, make([]byte, elf.EI_NIDENT-len(b))...)
	b = le.AppendUint16(b, uint16(elf.ET_EXEC))
	b = le.AppendUint16(b, uint16(machine))
	b = le.AppendUint32(b, uint32(elf.EV_CURRENT))
	b = le.AppendUint64(b, launcherBase+launcherCodeOff) // e_entry
	b = le.AppendUint64(b, elfHeaderSize)                // e_phoff
	b = le.AppendUint64(b, 0)                            // e_shoff: no sections
	b = le.AppendUint32(b, 0)                            // e_flags
	b = le.AppendUint16(b, elfHeaderSize)
	b = le.AppendUint16(b, elfProgHdrSize)
	b = le.AppendUint16(b, 2) // e_phnum
	b = le.AppendUint16(b, 0) // e_shentsize
	b = le.AppendUint16(b, 0) // e_shnum
	b = le.AppendUint16(b, 0) // e_shstrndx

	// Program headers: the whole file, mapped read-only and executable;
	// a non-executable stack.
	phdr := func(typ elf.ProgType, flags elf.ProgFlag, vaddr, size, align uint64) {
		b = le.AppendUint32(b, uint32(typ))
		b = le.AppendUint32(b, uint32(flags))
		b = le.AppendUint64(b, 0)     // p_offset
		b = le.AppendUint64(b, vaddr) // p_vaddr
		b = le.AppendUint64(b, vaddr) // p_paddr
		b = le.AppendUint64(b, size)  // p_filesz
		b = le.AppendUint64(b, size)  // p_memsz
		b = le.AppendUint64(b, align)
	}
	phdr(elf.PT_LOAD, elf.PF_R|elf.PF_X, launcherBase, uint64(size), launcherAlign)
	phdr(elf.PT_GNU_STACK, elf.PF_R|elf.PF_W, 0, 0, 16)

	b = append(b, text...)

	// struct sock_fprog {unsigned short len; struct sock_filter *filter;}
	b = le.AppendUint16(b, uint16(len(prog)))
	b = append(b, make([]byte, 6)...)
	b = le.AppendUint64(b, launcherBase+uint64(filterOff))

	// struct sock_filter {__u16 code; __u8 jt; __u8 jf; __u32 k;}
	for _, ins := range prog {
		b = le.AppendUint16(b, ins.Code)
		b = append(b, ins.Jt, ins.Jf)
		b = le.AppendUint32(b, ins.K)
	}
	if len(b) != size {
		return nil, fmt.Errorf("seccomp launcher: built %d bytes, want %d", len(b), size)
	}
	return b, nil
}

// launcherImage returns the launcher for this machine with the default
// policy.
func launcherImage() ([]byte, error) {
	pol, err := defaultPolicy()
	if err != nil {
		return nil, err
	}
	prog, err := pol.program()
	if err != nil {
		return nil, err
	}
	return buildLauncher(runtime.GOARCH, prog)
}

// checkSeccompSupport tells whether this kernel and architecture can run
// the launcher: the policy and the launcher must exist for the
// architecture and the kernel must support seccomp filters with
// SECCOMP_RET_KILL_PROCESS (Linux 4.14).
func checkSeccompSupport() error {
	if _, err := launcherImage(); err != nil {
		return err
	}
	action := uint32(unix.SECCOMP_RET_KILL_PROCESS)
	_, _, errno := unix.Syscall(unix.SYS_SECCOMP, unix.SECCOMP_GET_ACTION_AVAIL, 0, uintptr(unsafe.Pointer(&action)))
	if errno != 0 {
		return fmt.Errorf("the kernel does not support seccomp filters that kill the process: %w", errno)
	}
	return nil
}
