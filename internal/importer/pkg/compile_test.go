// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package pkg

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
)

// testlibChecker accepts an output equal to the expected one.
const testlibChecker = `#include "testlib.h"
int main(int argc, char* argv[]) {
    registerTestlibCmd(argc, argv);
    int got = ouf.readInt(), want = ans.readInt();
    if (got == want) quitf(_ok, "same");
    quitf(_wa, "expected %d, got %d", want, got);
}
`

// runProgram writes a compiled program and runs it with args (files
// created from contents), returning its outputs and exit code.
func runProgram(t *testing.T, f *importer.File, contents ...string) (stdout, stderr string, code int) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "program")
	if err := os.WriteFile(bin, []byte(content(t, f)), 0o755); err != nil {
		t.Fatal(err)
	}
	var args []string
	for i, c := range contents {
		p := filepath.Join(dir, "arg"+string(rune('0'+i)))
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
		args = append(args, p)
	}
	cmd := exec.Command(bin, args...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return out.String(), errOut.String(), code
}

func managerOf(t *testing.T, d *importer.Dataset, name string) *importer.File {
	t.Helper()
	for _, m := range d.Managers {
		if m.Filename == name {
			return m.File
		}
	}
	t.Fatalf("no manager %s in %v", name, managerNames(d))
	return nil
}

func TestCompileCheckers(t *testing.T) {
	needCompiler(t)
	base := with(tests(1), map[string]string{"statement/en.pdf": pdf, "solutions/main.ac.cpp": ""})
	yaml := "name: c\ntitle: C\ntime_limit: 1\nmemory_limit: 64\n"

	// A comparator with testlib.h speaks the CMS protocol (-DCMS, the
	// testlib.h of CMS replacing the package's): input, expected output,
	// contestant output; the outcome on stdout, the message on stderr.
	tk, rep := loadValid(t, pkgDir(t, with(base, map[string]string{
		"problem.yaml":        yaml + "checker: comparator\n",
		"graders/checker.cpp": testlibChecker,
		"graders/testlib.h":   "#error the package's testlib.h is not used for a comparator\n",
	})))
	noIssues(t, rep)
	d := tk.Datasets[0]
	if m := managerNames(d); !slices.Equal(m, []string{"checker"}) {
		t.Fatalf("managers %v", m)
	}
	checker := managerOf(t, d, "checker")
	if !strings.HasPrefix(content(t, checker), "\x7fELF") {
		t.Fatal("the checker is not an executable")
	}
	if out, errOut, code := runProgram(t, checker, "in", "42\n", "42\n"); code != 0 || strings.TrimSpace(out) != "1.0" ||
		!strings.Contains(errOut, "translate:success") {
		t.Errorf("same output: %q %q %d", out, errOut, code)
	}
	if out, errOut, _ := runProgram(t, checker, "in", "42\n", "41\n"); strings.TrimSpace(out) != "0.0" ||
		!strings.Contains(errOut, "translate:wrong") {
		t.Errorf("other output: %q %q", out, errOut)
	}

	// A testlib checker is a plain one (exit codes): input, contestant
	// output, expected output; the package's testlib.h is used if any.
	tk, _ = loadValid(t, pkgDir(t, with(base, map[string]string{
		"problem.yaml":       yaml + "checker: testlib\n",
		"graders/checker.cc": testlibChecker,
	})))
	checker = managerOf(t, tk.Datasets[0], "checker")
	if _, _, code := runProgram(t, checker, "in", "7\n", "7\n"); code != 0 {
		t.Errorf("same output: exit code %d", code)
	}
	if _, errOut, code := runProgram(t, checker, "in", "6\n", "7\n"); code != 1 || !strings.Contains(errOut, "expected 7, got 6") {
		t.Errorf("other output: exit code %d, %q", code, errOut)
	}

	// The same package from a zip compiles the same way (from the cache).
	z := writeZip(t, zipOf("", with(base, map[string]string{
		"problem.yaml": yaml + "checker: testlib\n", "graders/checker.cc": testlibChecker,
	})))
	tz, _ := loadValid(t, z)
	if content(t, managerOf(t, tz.Datasets[0], "checker")) != content(t, checker) {
		t.Error("the checker compiled from the zip differs")
	}
}

func TestCompileManagerWithHeaders(t *testing.T) {
	needCompiler(t)
	tk, rep := loadValid(t, pkgDir(t, with(tests(1), map[string]string{
		"problem.yaml":        "name: m\ntitle: M\ntime_limit: 1\nmemory_limit: 64\ntype: communication\n",
		"statement/en.pdf":    pdf,
		"solutions/a.ac.cpp":  "",
		"graders/manager.cpp": "#include \"common.hpp\"\n#include <cstdio>\nint main() { std::printf(\"%d\\n\", ANSWER); }\n",
		"graders/common.hpp":  "#define ANSWER 42\n",
	})))
	noIssues(t, rep)
	d := tk.Datasets[0]
	// The header is also a manager (for the contestants' sources).
	if m := managerNames(d); !slices.Equal(m, []string{"common.hpp", "manager"}) {
		t.Errorf("managers %v", m)
	}
	if out, _, code := runProgram(t, managerOf(t, d, "manager")); out != "42\n" || code != 0 {
		t.Errorf("manager: %q %d", out, code)
	}
}

func TestCompileErrors(t *testing.T) {
	needCompiler(t)
	base := with(tests(1), map[string]string{"statement/en.pdf": pdf, "solutions/main.ac.cpp": "",
		"problem.yaml": "name: c\ntitle: C\ntime_limit: 1\nmemory_limit: 64\ntype: interactive\n" +
			"controller_memory_limit: 64\ncontroller_time_limit: 1\ncontroller_wall_time_limit: 2\n"})
	dir := pkgDir(t, with(base, map[string]string{"graders/controller.cpp": "int main() { return undefined_name; }\n"}))
	_, rep := load(t, dir)
	wantIssue(t, rep, importer.SeverityError, "could not compile controller.cpp")
	wantIssue(t, rep, importer.SeverityError, "undefined_name")
	for _, i := range rep.Errors() {
		if strings.Contains(i.Message, "could not compile") && i.Where != filepath.Join(dir, "graders", "controller.cpp") {
			t.Errorf("where: %q", i.Where)
		}
	}

	old := Compiler
	Compiler = "cms-test-no-such-compiler"
	defer func() { Compiler = old }()
	_, rep = load(t, pkgDir(t, with(base, map[string]string{"graders/controller.cpp": "int main() { return 3; }\n"})))
	wantIssue(t, rep, importer.SeverityError, "the compiler cms-test-no-such-compiler is not installed: compile graders/controller.cpp yourself")
}

func TestCompileCacheKey(t *testing.T) {
	inputs := map[string][]byte{"a.cpp": []byte("x"), "a.h": []byte("y")}
	k := cacheKey("g++", compileFlags, "a.cpp", inputs)
	if k != cacheKey("g++", compileFlags, "a.cpp", map[string][]byte{"a.h": []byte("y"), "a.cpp": []byte("x")}) {
		t.Error("the key depends on the order of the inputs")
	}
	for _, other := range [][32]byte{
		cacheKey("clang++", compileFlags, "a.cpp", inputs),
		cacheKey("g++", append(slices.Clone(compileFlags), "-DCMS"), "a.cpp", inputs),
		cacheKey("g++", compileFlags, "a.h", inputs),
		cacheKey("g++", compileFlags, "a.cpp", map[string][]byte{"a.cpp": []byte("x"), "a.h": []byte("z")}),
		cacheKey("g++", compileFlags, "a.cpp", map[string][]byte{"a.cpp": []byte("xa"), ".h": []byte("y")}),
	} {
		if other == k {
			t.Error("two compilations share a key")
		}
	}
	c := &binCache{bins: map[[32]byte][]byte{}}
	for i := range cachedBinaries + 2 {
		c.put([32]byte{byte(i)}, []byte{byte(i)})
	}
	if _, ok := c.get([32]byte{0}); ok {
		t.Error("the oldest binary is still cached")
	}
	if b, ok := c.get([32]byte{cachedBinaries + 1}); !ok || b[0] != cachedBinaries+1 {
		t.Error("the newest binary is not cached")
	}
}
