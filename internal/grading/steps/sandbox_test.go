//go:build sandbox

// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package steps_test

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	configfiles "github.com/d4nd3r/contest-management-system-enchanced/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/steps"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/steps/stepstest"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/sandbox"
)

// Run with `make test-sandbox` (root, isolate); CMS_SANDBOX_CGROUPS=0
// without cgroups v2.

func TestMain(m *testing.M) {
	// The test binary is the seccomp launcher of the evaluations.
	sandbox.MaybeRunLauncher()
	os.Exit(m.Run())
}

func stepsConfig() *steps.Config {
	c := steps.ConfigFrom(config.Default()) // the real default limits
	c.Cgroups = stepstest.Cgroups()
	return &c
}

// program is a solution of "read two numbers, print their sum" in a
// language: alone (sum.%l), or with a grader (grader.%l calls add in
// sum.%l). The standalone ones fail to compile without -DEVAL where the
// language has it.
type program struct {
	alone          string
	grader, solved string // "" when the language cannot have a grader
	need           []string
}

var programs = map[string]program{
	"C11 / gcc": {
		alone:  "#ifndef EVAL\n#error EVAL is not defined\n#endif\n#include <stdio.h>\nint main(void){long a,b;if(scanf(\"%ld %ld\",&a,&b)!=2)return 1;printf(\"%ld\\n\",a+b);return 0;}\n",
		grader: "#include <stdio.h>\nlong add(long,long);\nint main(void){long a,b;if(scanf(\"%ld %ld\",&a,&b)!=2)return 1;printf(\"%ld\\n\",add(a,b));return 0;}\n",
		solved: "long add(long a,long b){return a+b;}\n",
	},
	"C++": {
		alone:  "#ifndef EVAL\n#error EVAL is not defined\n#endif\n#include <iostream>\nint main(){long a,b;std::cin>>a>>b;std::cout<<a+b<<std::endl;}\n",
		grader: "#include <iostream>\nlong add(long,long);\nint main(){long a,b;std::cin>>a>>b;std::cout<<add(a,b)<<std::endl;}\n",
		solved: "long add(long a,long b){return a+b;}\n",
	},
	"C# / Mono": {
		alone:  "using System;\nclass P{static void Main(){var v=Console.ReadLine().Split(' ');Console.WriteLine(long.Parse(v[0])+long.Parse(v[1]));}}\n",
		grader: "using System;\nclass Grader{static void Main(){var v=Console.ReadLine().Split(' ');Console.WriteLine(Sum.Add(long.Parse(v[0]),long.Parse(v[1])));}}\n",
		solved: "static class Sum{public static long Add(long a,long b){return a+b;}}\n",
	},
	"Haskell / ghc": {
		alone:  "main :: IO ()\nmain = do\n  [a, b] <- fmap (map read . words) getContents :: IO [Integer]\n  print (a + b)\n",
		grader: "import Sum (add)\nmain :: IO ()\nmain = do\n  [a, b] <- fmap (map read . words) getContents :: IO [Integer]\n  print (add a b)\n",
		solved: "module Sum where\nadd :: Integer -> Integer -> Integer\nadd a b = a + b\n",
	},
	"Java / JDK": {
		alone: "import java.util.*;\npublic class sum{public static void main(String[] x){if(!\"true\".equals(System.getProperty(\"eval\")))System.exit(5);" +
			"Scanner s=new Scanner(System.in);System.out.println(s.nextLong()+s.nextLong());}}\n",
		grader: "import java.util.*;\npublic class grader{public static void main(String[] x){Scanner s=new Scanner(System.in);" +
			"System.out.println(sum.add(s.nextLong(),s.nextLong()));}}\n",
		solved: "public class sum{static long add(long a,long b){return a+b;}}\n",
		need:   []string{"/usr/bin/jar"},
	},
	"Kotlin / JDK": {
		alone:  "fun main() {\n  val (a, b) = readLine()!!.split(\" \").map { it.toLong() }\n  println(a + b)\n}\n",
		grader: "fun main() {\n  val (a, b) = readLine()!!.split(\" \").map { it.toLong() }\n  println(add(a, b))\n}\n",
		solved: "fun add(a: Long, b: Long): Long = a + b\n",
		need:   []string{"/usr/bin/java"},
	},
	"Pascal / fpc": {
		alone:  "{$IFNDEF EVAL}{$FATAL EVAL is not defined}{$ENDIF}\nvar a, b: longint;\nbegin\n  readln(a, b);\n  writeln(a + b);\nend.\n",
		grader: "program grader;\nuses sum;\nvar a, b: longint;\nbegin\n  readln(a, b);\n  writeln(add(a, b));\nend.\n",
		solved: "unit sum;\ninterface\nfunction add(a, b: longint): longint;\nimplementation\nfunction add(a, b: longint): longint;\nbegin\n  add := a + b;\nend;\nend.\n",
	},
	"PHP": {
		// Upstream copies the grader over the solution: no grader.
		alone: "<?php\n$a = fscanf(STDIN, \"%d %d\");\necho $a[0] + $a[1], \"\\n\";\n",
	},
	"Python 3": {
		alone:  "a, b = map(int, input().split())\nprint(a + b)\n",
		grader: "import sum\na, b = map(int, input().split())\nprint(sum.add(a, b))\n",
		solved: "def add(a, b):\n    return a + b\n",
		need:   []string{"/usr/bin/zip"},
	},
	"Rust": {
		alone: "use std::io::*;\nfn main(){let mut s=String::new();stdin().read_to_string(&mut s).unwrap();" +
			"let v:Vec<i64>=s.split_whitespace().map(|x|x.parse().unwrap()).collect();println!(\"{}\",v[0]+v[1]);}\n",
		grader: "mod sum;\nuse std::io::*;\nfn main(){let mut s=String::new();stdin().read_to_string(&mut s).unwrap();" +
			"let v:Vec<i64>=s.split_whitespace().map(|x|x.parse().unwrap()).collect();println!(\"{}\",sum::add(v[0],v[1]));}\n",
		solved: "pub fn add(a: i64, b: i64) -> i64 { a + b }\n",
	},
	"Go": {
		alone:  "package main\nimport \"fmt\"\nfunc main(){var a,b int64;fmt.Scan(&a,&b);fmt.Println(a+b)}\n",
		grader: "package main\nimport \"fmt\"\nfunc main(){var a,b int64;fmt.Scan(&a,&b);fmt.Println(add(a,b))}\n",
		solved: "package main\nfunc add(a, b int64) int64 { return a + b }\n",
	},
}

func programFor(name string) (program, bool) {
	if p, ok := programs[name]; ok {
		return p, true
	}
	for prefix, p := range programs {
		if strings.HasPrefix(name, prefix) {
			return p, true
		}
	}
	return program{}, false
}

// installed reports whether every program the language runs exists.
func installed(l *language.Language, p program) (bool, string) {
	cmds, _ := l.CompilationCommands([]string{"a" + l.SourceExtension()}, "a", true)
	cmds = append(cmds, l.EvaluationCommands("a", "", nil)...)
	need := slices.Clone(p.need)
	for _, c := range cmds {
		if strings.HasPrefix(c[0], "/") {
			need = append(need, c[0])
		}
	}
	for _, n := range need {
		if _, err := os.Stat(n); err != nil {
			return false, n
		}
	}
	return true, ""
}

// goStdDir is the -pkgdir of the Go language (see go.yaml).
const goStdDir = "/var/lib/cms/go-std"

// goLanguage returns the Go language, with the standard library prepared
// in a temporary directory when the host has not prepared goStdDir:
// otherwise the compilation would exceed the time limit.
func goLanguage(t *testing.T, l *language.Language) *language.Language {
	t.Helper()
	if _, err := os.Stat(goStdDir); err == nil {
		return l
	}
	// Not under /tmp, where the boxes mount their home.
	dir, err := os.MkdirTemp("/var/tmp", "cms-go-std-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/usr/bin/go", "install", "-pkgdir="+dir, "std")
	cmd.Dir = t.TempDir() // outside the module of CMS, which needs a newer Go
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir(), "GOCACHE=" + t.TempDir(),
		"GOTOOLCHAIN=local", "CGO_ENABLED=0", "GODEBUG=installgoroot=all"}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("preparing the Go standard library: %v\n%s", err, out)
	}
	src, err := fs.ReadFile(configfiles.Languages(), "go.yaml")
	if err != nil {
		t.Fatal(err)
	}
	l, err = language.Parse([]byte(strings.ReplaceAll(string(src), goStdDir, dir)), "go.yaml")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s does not exist: standard library prepared in %s", goStdDir, dir)
	return l
}

// compileAndRun compiles sources (name -> content, the entry point first
// in order) in a box, moves the executable to a new box like the worker
// does, and runs it on "20 22".
func compileAndRun(t *testing.T, s *sandbox.Sandbox, c *steps.Config, l *language.Language, order []string, files map[string]string, main string) {
	t.Helper()
	ctx := context.Background()
	exe := "sum" + l.ExecutableExtension
	cbox := stepstest.Box(t, s)
	for name, content := range files {
		if err := cbox.CreateFile(name, []byte(content), false); err != nil {
			t.Fatal(err)
		}
	}
	cmds, err := l.CompilationCommands(order, exe, true)
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.Compile(ctx, cbox, l, cmds)
	if err != nil {
		t.Fatal(err)
	}
	if !r.OK {
		t.Fatalf("compilation %q: %q %s\nstdout: %s\nstderr: %s", cmds, r.Text, r.Stats.ExitStatus, r.Stats.Stdout, r.Stats.Stderr)
	}
	if r.Text[0] != "Compilation succeeded" || r.Stats.ExecutionTime == nil || r.Stats.ExecutionMemory == nil {
		t.Errorf("compilation result %+v", r)
	}
	data, _, err := cbox.ReadFile(exe, -1)
	if err != nil {
		t.Fatalf("executable: %v", err)
	}

	ebox := stepstest.Box(t, s)
	if err := ebox.CreateFile(exe, data, true); err != nil {
		t.Fatal(err)
	}
	if err := ebox.CreateFile("input.txt", []byte("20 22\n"), false); err != nil {
		t.Fatal(err)
	}
	st, err := c.Evaluate(ctx, ebox, steps.Evaluation{
		Commands:    l.EvaluationCommands(exe, main, nil),
		Language:    l,
		TimeLimit:   5,
		MemoryLimit: 256 << 20,
		Stdin:       "input.txt",
		Stdout:      "output.txt",
	})
	if err != nil {
		t.Fatal(err)
	}
	out, _, _ := ebox.ReadFile("output.txt", 4096)
	if !steps.EvaluationSucceeded(st) || strings.TrimSpace(string(out)) != "42" {
		errOut, _, _ := ebox.ReadFile(steps.EvaluationStderr, 4096)
		t.Fatalf("evaluation: %s (signal %d), output %q\nstderr: %s", st.ExitStatus, st.Signal, out, errOut)
	}
	t.Logf("compilation %.2f s CPU, %d MiB; evaluation %.3f s, %d MiB", *r.Stats.ExecutionTime, *r.Stats.ExecutionMemory>>20,
		*st.ExecutionTime, *st.ExecutionMemory>>20)
}

// TestLanguages compiles and runs a program in every built-in language
// whose tools are installed, alone and with a grader, through the steps
// (compilation limits of the default configuration, evaluation under the
// seccomp filter).
func TestLanguages(t *testing.T) {
	s := stepstest.Sandbox(t, true)
	c := stepsConfig()
	for _, l := range language.Builtin().All() {
		t.Run(l.Name, func(t *testing.T) {
			p, ok := programFor(l.Name)
			if !ok {
				t.Fatalf("no test program for %s", l.Name)
			}
			if ok, missing := installed(l, p); !ok {
				t.Skipf("%s is not installed", missing)
			}
			if l.Name == "Go" {
				l = goLanguage(t, l)
			}
			ext := l.SourceExtension()
			t.Run("alone", func(t *testing.T) {
				compileAndRun(t, s, c, l, []string{"sum" + ext}, map[string]string{"sum" + ext: p.alone}, "")
			})
			if p.grader == "" {
				return
			}
			t.Run("grader", func(t *testing.T) {
				compileAndRun(t, s, c, l, []string{"grader" + ext, "sum" + ext},
					map[string]string{"grader" + ext: p.grader, "sum" + ext: p.solved}, "grader")
			})
		})
	}
}

// TestCompilationErrors checks the messages of failed compilations and
// that compilers do not see the configuration of the host.
func TestCompilationErrors(t *testing.T) {
	if _, err := os.Stat("/usr/bin/gcc"); err != nil {
		t.Skip("gcc not installed")
	}
	s := stepstest.Sandbox(t, false)
	c := stepsConfig()
	l, _ := language.Builtin().Get("C11 / gcc")
	for _, tc := range []struct {
		name, src, want, stderr string
	}{
		{"syntax error", "int main(void) { return 0 }\n", "Compilation failed", "error"},
		// /etc is not visible: no secret of the host can be included.
		{"host file", "#include \"/etc/hostname\"\nint main(void){return 0;}\n", "Compilation failed", "No such file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			box := stepstest.Box(t, s)
			if err := box.CreateFile("sum.c", []byte(tc.src), false); err != nil {
				t.Fatal(err)
			}
			cmds, _ := l.CompilationCommands([]string{"sum.c"}, "sum", true)
			r, err := c.Compile(context.Background(), box, l, cmds)
			if err != nil {
				t.Fatal(err)
			}
			if r.OK || r.Text[0] != tc.want || !strings.Contains(r.Stats.Stderr, tc.stderr) || r.Stats.ExitStatus != job.ExitNonzeroReturn {
				t.Errorf("result %q %s, stderr %q", r.Text, r.Stats.ExitStatus, r.Stats.Stderr)
			}
			if len(r.Stats.Stderr) > steps.MaxCompilerOutput {
				t.Errorf("stderr of %d bytes", len(r.Stats.Stderr))
			}
		})
	}
	t.Run("timeout", func(t *testing.T) {
		c := *c
		c.Compilation.MaxTimeSec = 0.5
		box := stepstest.Box(t, s)
		r, err := c.Compile(context.Background(), box, nil, [][]string{{"/bin/sh", "-c", "while :; do :; done"}})
		if err != nil {
			t.Fatal(err)
		}
		if r.OK || !slices.Equal(r.Text, []string{"Compilation timed out"}) {
			t.Errorf("result %q %s", r.Text, r.Stats.ExitStatus)
		}
	})
}

// cProgram compiles a C program on the host.
func cProgram(t *testing.T, src string) []byte {
	t.Helper()
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc not installed")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "p.c"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("gcc", "-O2", "-static", "-o", filepath.Join(dir, "p"), filepath.Join(dir, "p.c")).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	data, err := os.ReadFile(filepath.Join(dir, "p"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestEvaluationVerdicts runs failing programs through the evaluation
// step: each verdict and its message.
func TestEvaluationVerdicts(t *testing.T) {
	s := stepstest.Sandbox(t, true)
	c := stepsConfig()
	for _, tc := range []struct {
		name, src string
		status    string
		text      string
		wall      float64
		cgroups   bool // the exact verdict needs cgroups
	}{
		{"correct", "#include <stdio.h>\nint main(void){puts(\"ok\");return 0;}\n", job.ExitOK, "", 0, false},
		{"nonzero return", "int main(void){return 3;}\n", job.ExitNonzeroReturn,
			"Execution failed because the return code was nonzero", 0, false},
		{"signal", "#include <stdlib.h>\nint main(void){abort();}\n", job.ExitSignal, "Execution killed by signal", 0, false},
		{"timeout", "int main(void){volatile unsigned long i=0;for(;;)i++;}\n", job.ExitTimeout, "Execution timed out", 0, false},
		{"wall timeout", "#include <unistd.h>\nint main(void){sleep(30);return 0;}\n", job.ExitTimeoutWall,
			"Execution timed out (wall clock limit exceeded)", 2, false},
		{"forbidden system call", "#include <sys/ptrace.h>\nint main(void){return ptrace(PTRACE_TRACEME,0,0,0)<0;}\n",
			job.ExitSecurityViolation, "Execution killed because of a forbidden system call", 0, false},
		{"memory limit", "#include <stdlib.h>\n#include <string.h>\nint main(void){for(;;){char*p=malloc(1<<20);if(!p)return 1;memset(p,1,1<<20);}}\n",
			job.ExitMemoryLimit, "Memory limit exceeded", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.cgroups && !stepstest.Cgroups() {
				t.Skip("the memory limit verdict needs cgroups")
			}
			box := stepstest.Box(t, s)
			if err := box.CreateFile("prog", cProgram(t, tc.src), true); err != nil {
				t.Fatal(err)
			}
			st, err := c.Evaluate(context.Background(), box, steps.Evaluation{
				Commands:      [][]string{{"./prog"}},
				TimeLimit:     1,
				WallTimeLimit: tc.wall,
				MemoryLimit:   64 << 20,
				Stdout:        "output.txt",
			})
			if err != nil {
				t.Fatal(err)
			}
			text := strings.Join(steps.EvaluationText(st), "")
			if st.ExitStatus != tc.status || text != tc.text {
				t.Errorf("%s %q (signal %d), want %s %q", st.ExitStatus, text, st.Signal, tc.status, tc.text)
			}
		})
	}
}

// TestStartEvaluation runs two programs at the same time, connected by
// FIFOs (like Communication), and merges their statistics.
func TestStartEvaluation(t *testing.T) {
	s := stepstest.Sandbox(t, true)
	c := stepsConfig()
	writer := cProgram(t, "#include <stdio.h>\nint main(void){printf(\"20 22\\n\");return 0;}\n")
	reader := cProgram(t, "#include <stdio.h>\nint main(void){long a,b;if(scanf(\"%ld %ld\",&a,&b)!=2)return 1;printf(\"%ld\\n\",a+b);return 0;}\n")
	b1, b2 := stepstest.Box(t, s), stepstest.Box(t, s)
	fifos, err := b1.NewFIFODir("pipe")
	if err != nil {
		t.Fatal(err)
	}
	defer fifos.Close()
	_ = b1.CreateFile("prog", writer, true)
	_ = b2.CreateFile("prog", reader, true)
	dir := fifos.Mount("/fifo")
	ctx := context.Background()
	p1, err := c.StartEvaluation(ctx, b1, steps.Evaluation{Commands: [][]string{{"./prog"}}, TimeLimit: 2, Stdout: "/fifo/pipe",
		Dirs: []sandbox.Dir{dir}})
	if err != nil {
		t.Fatal(err)
	}
	p2, err := c.StartEvaluation(ctx, b2, steps.Evaluation{Commands: [][]string{{"./prog"}}, TimeLimit: 2, Stdin: "/fifo/pipe",
		Stdout: "output.txt", Dirs: []sandbox.Dir{dir}})
	if err != nil {
		t.Fatal(err)
	}
	st1, err1 := steps.WaitEvaluation(p1)
	st2, err2 := steps.WaitEvaluation(p2)
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	merged := steps.MergeStats(st1, *st2, true)
	out, _, _ := b2.ReadFile("output.txt", 100)
	if merged.ExitStatus != job.ExitOK || strings.TrimSpace(string(out)) != "42" {
		t.Errorf("%s %s, output %q", st1.ExitStatus, st2.ExitStatus, out)
	}
	if _, err := c.StartEvaluation(ctx, b1, steps.Evaluation{Commands: [][]string{{"./a"}, {"./b"}}}); err == nil {
		t.Error("two commands: want an error")
	}
}
