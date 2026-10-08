// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package sandbox

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// launcherAsm is the assembler source of launcherCode (GNU syntax; the
// messages and the label fprog follow, see launcherSource). To change the
// launcher, edit it, assemble it (llvm-mc -triple=<arch>-linux-gnu
// -filetype=obj, or GNU as) and copy the bytes of .text into
// launcherCode: TestLauncherCodeMatchesSource checks that they match.
var launcherAsm = map[string]string{
	"amd64": `
	.text
_start:
	movq	(%rsp), %rcx
	cmpq	$2, %rcx
	jb	usage
	movl	$157, %eax
	movl	$38, %edi
	movl	$1, %esi
	xorl	%edx, %edx
	xorl	%r10d, %r10d
	xorl	%r8d, %r8d
	syscall
	testq	%rax, %rax
	jnz	setup_failed
	movl	$317, %eax
	movl	$1, %edi
	xorl	%esi, %esi
	leaq	fprog(%rip), %rdx
	syscall
	testq	%rax, %rax
	jnz	setup_failed
	movq	(%rsp), %rcx
	leaq	16(%rsp), %rsi
	movq	(%rsi), %rdi
	leaq	16(%rsp,%rcx,8), %rdx
	movl	$59, %eax
	syscall
	leaq	exec_msg(%rip), %rsi
	movl	$(exec_msg_end - exec_msg), %edx
	movl	$127, %ebx
	jmp	fail
usage:
	leaq	usage_msg(%rip), %rsi
	movl	$(usage_msg_end - usage_msg), %edx
	movl	$125, %ebx
	jmp	fail
setup_failed:
	leaq	setup_msg(%rip), %rsi
	movl	$(setup_msg_end - setup_msg), %edx
	movl	$125, %ebx
fail:
	movl	$1, %eax
	movl	$2, %edi
	syscall
	movl	$231, %eax
	movl	%ebx, %edi
	syscall
	ud2
`,
	"arm64": `
	.text
_start:
	ldr	x9, [sp]
	cmp	x9, #2
	b.lo	usage
	mov	x8, #167
	mov	x0, #38
	mov	x1, #1
	mov	x2, #0
	mov	x3, #0
	mov	x4, #0
	svc	#0
	cbnz	x0, setup_failed
	mov	x8, #277
	mov	x0, #1
	mov	x1, #0
	adr	x2, fprog
	svc	#0
	cbnz	x0, setup_failed
	add	x1, sp, #16
	ldr	x0, [x1]
	add	x2, x1, x9, lsl #3
	mov	x8, #221
	svc	#0
	adr	x1, exec_msg
	mov	x2, #(exec_msg_end - exec_msg)
	mov	x19, #127
	b	fail
usage:
	adr	x1, usage_msg
	mov	x2, #(usage_msg_end - usage_msg)
	mov	x19, #125
	b	fail
setup_failed:
	adr	x1, setup_msg
	mov	x2, #(setup_msg_end - setup_msg)
	mov	x19, #125
fail:
	mov	x0, #2
	mov	x8, #64
	svc	#0
	mov	x0, x19
	mov	x8, #94
	svc	#0
	udf	#0
`,
}

// launcherSource is the complete source of the launcher text for arch:
// launcherAsm, the messages, zeros up to a multiple of 8 bytes and the
// label fprog (struct sock_fprog follows the text).
func launcherSource(arch string) string {
	src := launcherAsm[arch]
	n := 0
	for _, ins := range launcherCode[arch].code {
		n += len(strings.ReplaceAll(ins, " ", "")) / 2
	}
	for i, label := range []string{"usage_msg", "setup_msg", "exec_msg"} {
		src += label + ":\n\t.ascii\t" + strconv.Quote(launcherMessages[i]) + "\n" + label + "_end:\n"
		n += len(launcherMessages[i])
	}
	// Not .p2align: assemblers pad code sections with no-ops.
	if pad := (8 - n%8) % 8; pad > 0 {
		src += "\t.zero\t" + strconv.Itoa(pad) + "\n"
	}
	return src + "fprog:\n"
}

// assemblers lists the commands that assemble an object file for arch.
func assemblers(arch string, src, obj string) [][]string {
	triple := map[string]string{"amd64": "x86_64-linux-gnu", "arm64": "aarch64-linux-gnu"}[arch]
	cmds := [][]string{{"llvm-mc", "-triple=" + triple, "-filetype=obj", "-o", obj, src}}
	if arch == runtime.GOARCH {
		cmds = append(cmds, []string{"as", "-o", obj, src})
	}
	return append(cmds, []string{triple + "-as", "-o", obj, src})
}

func TestLauncherCodeMatchesSource(t *testing.T) {
	for arch := range launcherCode {
		t.Run(arch, func(t *testing.T) {
			_, text, err := launcherText(arch)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			src, obj := filepath.Join(dir, "launcher.s"), filepath.Join(dir, "launcher.o")
			if err := os.WriteFile(src, []byte(launcherSource(arch)), 0o644); err != nil {
				t.Fatal(err)
			}
			assembled := false
			for _, cmd := range assemblers(arch, src, obj) {
				if _, err := exec.LookPath(cmd[0]); err != nil {
					continue
				}
				if out, err := exec.Command(cmd[0], cmd[1:]...).CombinedOutput(); err != nil {
					t.Fatalf("%q: %v\n%s", cmd, err, out)
				}
				assembled = true
				break
			}
			if !assembled {
				msg := "no assembler for " + arch + " (llvm-mc or GNU as)"
				if os.Getenv("CMS_TEST_REQUIRE_ASSEMBLER") != "" {
					t.Fatal(msg)
				}
				t.Skip(msg)
			}
			f, err := elf.Open(obj)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if rels := f.Section(".rela.text"); rels != nil && rels.Size > 0 {
				t.Errorf("the code needs relocations")
			}
			want, err := f.Section(".text").Data()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(text, want) {
				t.Errorf("launcherCode does not match its source:\n got %x\nwant %x", text, want)
			}
		})
	}
}

// TestLauncherELF checks the executable of every architecture (it cannot
// run them all): headers, entry point and the filter where the code
// expects it.
func TestLauncherELF(t *testing.T) {
	prog := []unix.SockFilter{{Code: ldAbs, K: offArch}, {Code: retK, K: allow}, {Code: jeqK, Jt: 1, Jf: 2, K: 0xdeadbeef}}
	for arch, code := range launcherCode {
		t.Run(arch, func(t *testing.T) {
			img, err := buildLauncher(arch, prog)
			if err != nil {
				t.Fatal(err)
			}
			f, err := elf.NewFile(bytes.NewReader(img))
			if err != nil {
				t.Fatal(err)
			}
			if f.Class != elf.ELFCLASS64 || f.Data != elf.ELFDATA2LSB || f.Type != elf.ET_EXEC ||
				f.Machine != code.machine || f.Entry != launcherBase+launcherCodeOff {
				t.Errorf("header %+v", f.FileHeader)
			}
			if len(f.Progs) != 2 {
				t.Fatalf("%d program headers", len(f.Progs))
			}
			load, stack := f.Progs[0], f.Progs[1]
			if load.Type != elf.PT_LOAD || load.Flags != elf.PF_R|elf.PF_X || load.Off != 0 ||
				load.Vaddr != launcherBase || load.Filesz != uint64(len(img)) || load.Memsz != load.Filesz {
				t.Errorf("PT_LOAD %+v", load.ProgHeader)
			}
			if stack.Type != elf.PT_GNU_STACK || stack.Flags != elf.PF_R|elf.PF_W {
				t.Errorf("PT_GNU_STACK %+v", stack.ProgHeader)
			}
			_, text, _ := launcherText(arch)
			if !bytes.Equal(img[launcherCodeOff:launcherCodeOff+len(text)], text) {
				t.Error("code not at the entry point")
			}
			// struct sock_fprog, then the filter it points to.
			le := binary.LittleEndian
			fprog := img[launcherCodeOff+len(text):]
			n, ptr := int(le.Uint16(fprog)), le.Uint64(fprog[8:])
			if n != len(prog) || ptr != launcherBase+uint64(launcherCodeOff+len(text)+sockFprogSize) {
				t.Fatalf("sock_fprog {%d, %#x}", n, ptr)
			}
			filter := img[ptr-launcherBase:]
			if len(filter) != sockFilterSize*n {
				t.Fatalf("filter of %d bytes", len(filter))
			}
			for i, ins := range prog {
				b := filter[i*sockFilterSize:]
				got := unix.SockFilter{Code: le.Uint16(b), Jt: b[2], Jf: b[3], K: le.Uint32(b[4:])}
				if got != ins {
					t.Errorf("instruction %d: %+v, want %+v", i, got, ins)
				}
			}
		})
	}
	if _, err := buildLauncher("mips", prog); err == nil {
		t.Error("unknown architecture accepted")
	}
	if _, err := buildLauncher("amd64", nil); err == nil {
		t.Error("empty filter accepted")
	}
	if _, err := buildLauncher("amd64", make([]unix.SockFilter, bpfMaxInsns+1)); err == nil {
		t.Error("filter too long accepted")
	}
}

// BenchmarkLauncher compares the cost of starting /bin/true with and
// without the launcher (the peak memory of these runs is that of the test
// binary: os/exec starts processes with vfork; TestSeccompLauncherCost
// measures it through isolate).
func BenchmarkLauncher(b *testing.B) {
	if err := checkSeccompSupport(); err != nil {
		b.Skip(err)
	}
	img, err := launcherImage()
	if err != nil {
		b.Fatal(err)
	}
	launcher := filepath.Join(b.TempDir(), launcherBinary)
	if err := os.WriteFile(launcher, img, 0o755); err != nil {
		b.Fatal(err)
	}
	for _, bc := range []struct {
		name string
		argv []string
	}{{"direct", []string{"/bin/true"}}, {"launcher", []string{launcher, "/bin/true"}}} {
		b.Run(bc.name, func(b *testing.B) {
			var cpu time.Duration
			for b.Loop() {
				cmd := exec.Command(bc.argv[0], bc.argv[1:]...)
				if err := cmd.Run(); err != nil {
					b.Fatal(err)
				}
				cpu += cmd.ProcessState.UserTime() + cmd.ProcessState.SystemTime()
			}
			b.ReportMetric(float64(cpu.Microseconds())/float64(b.N), "cpu-us/op")
		})
	}
}
