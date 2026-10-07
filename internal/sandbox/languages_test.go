//go:build sandbox

// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package sandbox

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestLanguagesUnderSeccomp compiles a small program in every language
// installed on the host (in a box, without the filter, like a compilation)
// and runs it under the seccomp filter (like an evaluation): the policy
// must not break any runtime. The commands follow the CMS languages
// (cms/grading/languages); missing compilers are skipped.
func TestLanguagesUnderSeccomp(t *testing.T) {
	s := newTestSandbox(t, nil)
	type lang struct {
		name, src, code string
		need            []string // binaries that must exist
		compile         [][]string
		run             []string
		procs           int   // threads the runtime needs (0: one)
		memKiB          int64 // 0: 256 MiB
		env             []string
		locks           bool // the compiler locks files
	}
	const in = "20 22\n"
	langs := []lang{
		{name: "C11 / gcc", src: "prog.c", need: []string{"/usr/bin/gcc"},
			code:    `#include <stdio.h>` + "\n" + `int main(void){long a,b;scanf("%ld %ld",&a,&b);printf("%ld\n",a+b);return 0;}`,
			compile: [][]string{{"/usr/bin/gcc", "-DEVAL", "-std=gnu11", "-O2", "-pipe", "-static", "-s", "-o", "prog", "prog.c", "-lm"}},
			run:     []string{"./prog"}},
		{name: "C++17 / g++", src: "prog.cpp", need: []string{"/usr/bin/g++"},
			code:    "#include <iostream>\nint main(){long a,b;std::cin>>a>>b;std::cout<<a+b<<std::endl;}",
			compile: [][]string{{"/usr/bin/g++", "-DEVAL", "-std=gnu++17", "-O2", "-pipe", "-static", "-s", "-o", "prog", "prog.cpp"}},
			run:     []string{"./prog"}},
		{name: "Pascal / fpc", src: "prog.pas", need: []string{"/usr/bin/fpc"},
			code:    "var a, b: longint;\nbegin\n  readln(a, b);\n  writeln(a + b);\nend.\n",
			compile: [][]string{{"/usr/bin/fpc", "-dEVAL", "-XS", "-O2", "-oprog", "prog.pas"}},
			run:     []string{"./prog"}},
		{name: "Rust", src: "prog.rs", need: []string{"/usr/bin/rustc"},
			code: "use std::io::*;\nfn main(){let mut s=String::new();stdin().read_to_string(&mut s).unwrap();" +
				"let v:Vec<i64>=s.split_whitespace().map(|x|x.parse().unwrap()).collect();println!(\"{}\",v[0]+v[1]);}",
			compile: [][]string{{"/usr/bin/rustc", "--edition", "2021", "-O", "-o", "prog", "prog.rs"}},
			run:     []string{"./prog"}},
		{name: "Haskell / ghc", src: "prog.hs", need: []string{"/usr/bin/ghc"},
			code:    "main :: IO ()\nmain = do\n  [a, b] <- fmap (map read . words) getContents :: IO [Integer]\n  print (a + b)\n",
			compile: [][]string{{"/usr/bin/ghc", "-static", "-O2", "-o", "prog", "prog.hs"}},
			run:     []string{"./prog"}, procs: 4}, // the RTS ticker is a thread
		{name: "Python 3 / CPython", src: "prog.py", need: []string{"/usr/bin/python3"},
			code: "a, b = map(int, input().split())\nprint(a + b)\n",
			run:  []string{"/usr/bin/python3", "prog.py"}},
		{name: "Python 3 / PyPy", src: "prog.py", need: []string{"/usr/bin/pypy3"},
			code: "a, b = map(int, input().split())\nprint(a + b)\n",
			run:  []string{"/usr/bin/pypy3", "prog.py"}},
		{name: "PHP", src: "prog.php", need: []string{"/usr/bin/php"},
			code: "<?php\n$a = fscanf(STDIN, \"%d %d\");\necho $a[0] + $a[1], \"\\n\";\n",
			run:  []string{"/usr/bin/php", "prog.php"}},
		{name: "C# / Mono", src: "prog.cs", need: []string{"/usr/bin/mcs", "/usr/bin/mono"},
			code: "using System;\nclass P{static void Main(){var v=Console.ReadLine().Split(' ');" +
				"Console.WriteLine(long.Parse(v[0])+long.Parse(v[1]));}}",
			compile: [][]string{{"/usr/bin/mcs", "-out:prog.exe", "-optimize+", "prog.cs"}},
			run:     []string{"/usr/bin/mono", "prog.exe"}, procs: 100, memKiB: 1 << 20},
		{name: "Java / JDK", src: "Main.java", need: []string{"/usr/bin/javac", "/usr/bin/java"},
			code: "import java.util.*;\npublic class Main{public static void main(String[] x){Scanner s=new Scanner(System.in);" +
				"System.out.println(s.nextLong()+s.nextLong());}}",
			compile: [][]string{{"/usr/bin/javac", "Main.java"}},
			run:     []string{"/usr/bin/java", "-Deval=true", "-Xmx512M", "-Xss64M", "-cp", ".", "Main"}, procs: 100, memKiB: 2 << 20},
		{name: "Kotlin", src: "prog.kt", need: []string{"/usr/bin/kotlinc", "/usr/bin/java"},
			code:    "fun main() { val (a, b) = readLine()!!.split(\" \").map { it.toLong() }\n println(a + b) }\n",
			compile: [][]string{{"/usr/bin/kotlinc", "prog.kt", "-include-runtime", "-d", "prog.jar"}},
			run:     []string{"/usr/bin/java", "-Xmx512M", "-Xss64M", "-jar", "prog.jar"}, procs: 100, memKiB: 2 << 20},
		{name: "Go", src: "prog.go", need: []string{goBinary()},
			code:    "package main\nimport \"fmt\"\nfunc main(){var a,b int64;fmt.Scan(&a,&b);fmt.Println(a+b)}\n",
			compile: [][]string{{goBinary(), "build", "-o", "prog", "prog.go"}},
			env:     []string{"GOCACHE=/tmp/.gocache", "GOPATH=/tmp/.gopath", "GOTOOLCHAIN=local", "CGO_ENABLED=0"},
			run:     []string{"./prog"}, procs: 100, memKiB: 256 << 10, locks: true},
	}
	for _, l := range langs {
		t.Run(l.name, func(t *testing.T) {
			for _, n := range l.need {
				if _, err := os.Stat(n); err != nil {
					t.Skipf("%s not installed", n)
				}
			}
			b := newTestBox(t, s)
			if err := b.CreateFile(l.src, []byte(l.code), false); err != nil {
				t.Fatal(err)
			}
			if err := b.CreateFile("input.txt", []byte(in), false); err != nil {
				t.Fatal(err)
			}
			// Without cgroups memory is limited as address space, which
			// virtual machines (JVM, Mono) and the Go runtime reserve by
			// the gigabyte: those languages (memKiB set) run unlimited.
			compileMem, mem := int64(4<<20), l.memKiB
			if mem == 0 {
				mem = 256 << 10
			}
			if !s.cfg.Cgroups {
				compileMem = 0
				if l.memKiB != 0 {
					mem = 0
				}
			}
			// Compilation: what the compilers of CMS get (/etc, many
			// processes, generous limits), never the filter.
			for _, cmd := range l.compile {
				res, err := b.Run(context.Background(), RunSpec{
					Argv: cmd, Stdout: "compile.out", Stderr: "compile.err", Env: l.env,
					Dirs:    []Dir{{Inside: "/etc"}, {Inside: "/var/lib/ghc", Optional: true}},
					CPUTime: 60, WallTime: 120, MemoryKiB: compileMem, Processes: 1000, AllowFileLocks: l.locks,
				})
				if err != nil || res.Status != StatusOK {
					msg, _, _ := b.ReadFile("compile.err", 4096)
					out, _, _ := b.ReadFile("compile.out", 4096)
					t.Fatalf("compilation %q: %+v %v\n%s\n%s", cmd, res, err, out, msg)
				}
			}
			res, err := b.Run(context.Background(), RunSpec{
				Argv: l.run, Stdin: "input.txt", Stdout: "output.txt", Stderr: "stderr.txt",
				CPUTime: 10, WallTime: 20, MemoryKiB: mem, Processes: l.procs,
				Seccomp: true, RestrictWrites: true,
			})
			out, _, _ := b.ReadFile("output.txt", 4096)
			if err != nil || res.Status != StatusOK || strings.TrimSpace(string(out)) != "42" {
				msg, _, _ := b.ReadFile("stderr.txt", 4096)
				t.Fatalf("run %q: %+v %v\noutput %q\n%s", l.run, res, err, out, msg)
			}
			t.Logf("%s: %.3f s, %d KiB", l.name, res.CPUTime, res.MemoryKiB)
		})
	}
}

// goBinary is the Go toolchain of the distribution (golang-go), or the
// official one.
func goBinary() string {
	for _, p := range []string{"/usr/bin/go", "/usr/local/go/bin/go"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "/usr/bin/go"
}
