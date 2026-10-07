// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package language

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// upstreamNames are the languages of CMS, with their exact names.
var upstreamNames = []string{
	"C# / Mono", "C++11 / g++", "C++14 / g++", "C++17 / g++", "C++20 / g++",
	"C11 / gcc", "Haskell / ghc", "Java / JDK", "PHP", "Pascal / fpc",
	"Python 3 / CPython", "Python 3 / PyPy", "Rust",
}

func TestBuiltinLanguages(t *testing.T) {
	r := Builtin()
	want := append(slices.Clone(upstreamNames), "Go", "Kotlin / JDK")
	slices.Sort(want)
	if got := r.Names(); !slices.Equal(got, want) {
		t.Fatalf("built-in languages:\n got %q\nwant %q", got, want)
	}
	for _, l := range r.All() {
		if l.Digest() == "" || len(l.Digest()) != 64 {
			t.Errorf("%s: digest %q", l.Name, l.Digest())
		}
		if !strings.HasPrefix(l.File, "config/languages/") {
			t.Errorf("%s: file %q", l.Name, l.File)
		}
		// Every definition must give commands for a typical case.
		cmds, err := l.CompilationCommands([]string{"grader" + l.SourceExtension(), "sol" + l.SourceExtension()},
			"sol"+l.ExecutableExtension, true)
		if err != nil || len(cmds) == 0 {
			t.Errorf("%s: compilation commands %q, %v", l.Name, cmds, err)
		}
		if ev := l.EvaluationCommands("sol"+l.ExecutableExtension, "", nil); len(ev) == 0 {
			t.Errorf("%s: no evaluation commands", l.Name)
		}
	}
	// The digests tell the languages apart.
	seen := map[string]string{}
	for _, l := range r.All() {
		if other, ok := seen[l.Digest()]; ok {
			t.Errorf("%s and %s have the same digest", l.Name, other)
		}
		seen[l.Digest()] = l.Name
	}
}

type goldenLanguage struct {
	SourceExtensions       []string `json:"source_extensions"`
	HeaderExtensions       []string `json:"header_extensions"`
	ObjectExtensions       []string `json:"object_extensions"`
	ExecutableExtension    string   `json:"executable_extension"`
	RequiresMultithreading bool     `json:"requires_multithreading"`
	Cases                  struct {
		Compile []struct {
			Sources       []string   `json:"sources"`
			Executable    string     `json:"executable"`
			ForEvaluation bool       `json:"for_evaluation"`
			Commands      [][]string `json:"commands"`
		} `json:"compile"`
		Evaluate []struct {
			Executable string     `json:"executable"`
			Main       *string    `json:"main"`
			Args       []string   `json:"args"`
			Commands   [][]string `json:"commands"`
		} `json:"evaluate"`
	} `json:"cases"`
}

// TestUpstreamCommands compares the YAML definitions with the commands of
// the Python classes of CMS (testdata/gen_golden.py).
func TestUpstreamCommands(t *testing.T) {
	data, err := os.ReadFile("testdata/upstream_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden map[string]goldenLanguage
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatal(err)
	}
	var names []string
	for n := range golden {
		names = append(names, n)
	}
	slices.Sort(names)
	if !slices.Equal(names, upstreamNames) {
		t.Fatalf("golden languages %q", names)
	}
	r := Builtin()
	for _, name := range names {
		g := golden[name]
		t.Run(name, func(t *testing.T) {
			l, err := r.Get(name)
			if err != nil {
				t.Fatal(err)
			}
			eq := func(what string, got, want any) {
				t.Helper()
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s:\n got %q\nwant %q", what, got, want)
				}
			}
			eq("source extensions", l.SourceExtensions, g.SourceExtensions)
			eq("header extensions", nonNil(l.HeaderExtensions), g.HeaderExtensions)
			eq("object extensions", nonNil(l.ObjectExtensions), g.ObjectExtensions)
			eq("executable extension", l.ExecutableExtension, g.ExecutableExtension)
			eq("requires multithreading", l.RequiresMultithreading, g.RequiresMultithreading)
			for _, c := range g.Cases.Compile {
				got, err := l.CompilationCommands(c.Sources, c.Executable, c.ForEvaluation)
				if err != nil {
					t.Fatal(err)
				}
				eq("compile "+strings.Join(c.Sources, ",")+" -> "+c.Executable, got, c.Commands)
			}
			for _, c := range g.Cases.Evaluate {
				main := ""
				if c.Main != nil {
					main = *c.Main
				}
				want := c.Commands
				if name == "C# / Mono" {
					// Upstream drops the arguments of C# programs; this
					// fork passes them (see csharp_mono.yaml).
					want = [][]string{append(slices.Clone(want[0]), c.Args...)}
				}
				if main == "" && name == "Java / JDK" {
					// Upstream passes main=None to java; callers
					// always give it, and "" means the stem.
					want = [][]string{slices.Clone(want[0])}
					for i, a := range want[0] {
						if a == "" {
							want[0][i] = stem(c.Executable)
						}
					}
				}
				eq("evaluate "+c.Executable, l.EvaluationCommands(c.Executable, main, c.Args), want)
			}
		})
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func TestCompilationCommandsErrors(t *testing.T) {
	l := Builtin().byName["C11 / gcc"]
	if _, err := l.CompilationCommands(nil, "x", true); err == nil {
		t.Fatal("no sources: want an error")
	}
}

func TestModifiers(t *testing.T) {
	for _, c := range []struct{ fn, in, want string }{
		{"stem", "a.py", "a"},
		{"stem", "dir/a.b.py", "a.b"},
		{"stem", ".bashrc", ".bashrc"},
		{"stem", "..a", "..a"},
		{"stem", "a..b", "a."},
		{"stem", ".a.b", ".a"},
		{"stem", "noext", "noext"},
		{"base", "dir/sub/a.py", "a.py"},
		{"capitalize", "foo.hs", "Foo.hs"},
		{"capitalize", "dir/foo.hs", "dir/Foo.hs"},
		{"capitalize", "éa.hs", "Éa.hs"},
		{"capitalize", "", ""},
		{"quote", "", "''"},
		{"quote", "foo.jar", "foo.jar"},
		{"quote", "a b.jar", "'a b.jar'"},
		{"quote", "it's", `'it'"'"'s'`},
		{"quote", "x;rm -rf /", "'x;rm -rf /'"},
		{"quote", "@%+=:,./-_", "@%+=:,./-_"},
	} {
		if got := modifiers[c.fn](c.in); got != c.want {
			t.Errorf("%s(%q) = %q, want %q", c.fn, c.in, got, c.want)
		}
	}
}

func TestTemplates(t *testing.T) {
	const head = "name: Test\nsource_extensions: [.t]\n"
	l, err := Parse([]byte(head+`
compilation:
  commands:
    - [/c, "{eval:-DEVAL}", "x{{y}}z", "-o{executable}", "{sources|base}", "-I{other_sources|stem}.d"]
    - for_each: other_sources
      command: [/bin/ln, "{source}", "{source|base|capitalize}", "{main_source|stem}"]
evaluation:
  commands:
    - ["./{executable}", "{main}", "--", "{args|quote}"]
`), "test.yaml")
	if err != nil {
		t.Fatal(err)
	}
	got, err := l.CompilationCommands([]string{"d/m.t", "a.t", "b.t"}, "exe", true)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"/c", "-DEVAL", "x{y}z", "-oexe", "m.t", "a.t", "b.t", "-Ia.d", "-Ib.d"},
		{"/bin/ln", "a.t", "A.t", "m"},
		{"/bin/ln", "b.t", "B.t", "m"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("compilation:\n got %q\nwant %q", got, want)
	}
	got, _ = l.CompilationCommands([]string{"m.t"}, "exe", false)
	want = [][]string{{"/c", "x{y}z", "-oexe", "m.t"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("single source, not for evaluation:\n got %q\nwant %q", got, want)
	}
	ev := l.EvaluationCommands("exe.bin", "", []string{"a b", "c"})
	if w := [][]string{{"./exe.bin", "exe", "--", "'a b'", "c"}}; !reflect.DeepEqual(ev, w) {
		t.Errorf("evaluation:\n got %q\nwant %q", ev, w)
	}
	// The templates are not changed by expansion.
	ev2 := l.EvaluationCommands("other", "M", nil)
	if w := [][]string{{"./other", "M", "--"}}; !reflect.DeepEqual(ev2, w) {
		t.Errorf("evaluation:\n got %q\nwant %q", ev2, w)
	}
}

func TestParseErrors(t *testing.T) {
	const ok = "name: T\nsource_extensions: [.t]\ncompilation:\n  commands: [[/c, \"{sources}\"]]\n" +
		"evaluation:\n  commands: [[\"./{executable}\"]]\n"
	if _, err := Parse([]byte(ok), "ok.yaml"); err != nil {
		t.Fatalf("valid definition: %v", err)
	}
	for _, c := range []struct{ name, yaml, want string }{
		{"unknown key", ok + "colour: blue\n", "colour"},
		{"unknown command key", "name: T\nsource_extensions: [.t]\ncompilation:\n  commands: [{for_each: other_sources, cmd: [/a]}]\n" +
			"evaluation:\n  commands: [[/x]]\n", "cmd"},
		{"no name", strings.Replace(ok, "name: T", "name: ''", 1), "invalid name"},
		{"name with spaces", strings.Replace(ok, "name: T", "name: ' T'", 1), "invalid name"},
		{"no sources", strings.Replace(ok, "source_extensions: [.t]", "source_extensions: []", 1), "source_extensions is empty"},
		{"bad extension", strings.Replace(ok, "[.t]", "[.t, a/b]", 1), "invalid extension"},
		{"no compilation", strings.Replace(ok, "commands: [[/c, \"{sources}\"]]", "commands: []", 1), "compilation.commands is empty"},
		{"interpreted with commands", ok + "interpreted: true\n", "interpreted"},
		{"no evaluation", strings.Replace(ok, "commands: [[\"./{executable}\"]]", "commands: []", 1), "evaluation.commands is empty"},
		{"unknown placeholder", strings.Replace(ok, "{sources}", "{source}", 1), "unknown placeholder {source}"},
		{"list in evaluation", strings.Replace(ok, "[\"./{executable}\"]", "[\"./{executable}\", \"{sources}\"]", 1), "unknown placeholder {sources}"},
		{"two lists", strings.Replace(ok, "{sources}", "{sources}{other_sources}", 1), "more than one list"},
		{"list and scalar", strings.Replace(ok, "{sources}", "{sources}{executable}", 1), "only placeholder"},
		{"bad modifier", strings.Replace(ok, "{sources}", "{sources|upper}", 1), "unknown modifier"},
		{"unclosed brace", strings.Replace(ok, "{sources}", "{sources", 1), "unclosed"},
		{"lone closing brace", strings.Replace(ok, "{sources}", "a}b", 1), "unbalanced"},
		{"eval inside", strings.Replace(ok, "{sources}", "-x{eval:-D}", 1), "whole argument"},
		{"empty eval", strings.Replace(ok, "{sources}", "{eval:}", 1), "non-empty TEXT"},
		{"eval in evaluation", strings.Replace(ok, "[\"./{executable}\"]", "[\"./{executable}\", \"{eval:-x}\"]", 1), "only for compilation"},
		{"relative program", strings.Replace(ok, "[/c,", "[gcc,", 1), "absolute path"},
		{"program placeholder", strings.Replace(ok, "[/c,", "[\"/{main_source}\",", 1), "absolute path"},
		{"bad for_each", "name: T\nsource_extensions: [.t]\ncompilation:\n  commands: [{for_each: sources, command: [/a]}]\n" +
			"evaluation:\n  commands: [[/x]]\n", "for_each"},
		{"for_each in evaluation", "name: T\nsource_extensions: [.t]\ncompilation:\n  commands: [[/a]]\n" +
			"evaluation:\n  commands: [{for_each: other_sources, command: [/x]}]\n", "only for compilation"},
		{"source outside for_each", strings.Replace(ok, "{sources}", "{source}", 1), "unknown placeholder"},
		{"negative processes", ok + "  processes: -1\n", "processes"},
		{"relative dir", ok + "  dirs: [etc/x]\n", "absolute"},
		{"all of etc", ok + "  dirs: [/etc]\n", "configuration of CMS"},
		{"root", ok + "  dirs: [/]\n", "cannot be mounted"},
		{"colon in dir", ok + "  dirs: [/a:b]\n", "isolate separators"},
		{"bad glob", ok + "  dirs: [\"/a[\"]\n", "syntax error"},
		{"bad env", ok + "  env: [NOEQUALS]\n", "NAME=VALUE"},
		{"bad env name", ok + "  env: [\"1A=b\"]\n", "NAME=VALUE"},
		{"bad file name", strings.Replace(ok, "compilation:\n", "compilation:\n  files: {a/b: /x}\n", 1), "plain file name"},
		{"relative host file", strings.Replace(ok, "compilation:\n", "compilation:\n  files: {a: x}\n", 1), "absolute"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse([]byte(c.yaml), "bad.yaml")
			if err == nil || !strings.Contains(err.Error(), c.want) || !strings.HasPrefix(err.Error(), "bad.yaml: ") {
				t.Fatalf("error %v, want one mentioning %q", err, c.want)
			}
		})
	}
}

func TestRegistryLookups(t *testing.T) {
	r := Builtin()
	if _, err := r.Get("Brainfuck"); !errors.Is(err, ErrUnknown) {
		t.Errorf("unknown language: %v", err)
	}
	if l, ok := r.Lookup("Rust"); !ok || l.Name != "Rust" {
		t.Error("Lookup(Rust)")
	}
	for _, c := range []struct {
		file    string
		allowed []string
		want    string
	}{
		{"sol.cpp", nil, "C++11 / g++"}, // the smallest name, like upstream
		{"sol.cpp", []string{"C++20 / g++", "C++17 / g++"}, "C++17 / g++"},
		{"sol.C", nil, "C++11 / g++"},
		{"sol.c", nil, "C11 / gcc"},
		{"sol.py", nil, "Python 3 / CPython"},
		{"sol.py", []string{"Python 3 / PyPy"}, "Python 3 / PyPy"},
		{"Main.java", nil, "Java / JDK"},
		{"main.go", nil, "Go"},
		{"main.kt", nil, "Kotlin / JDK"},
		{"sol.cpp", []string{"C11 / gcc"}, ""},
		{"sol", nil, ""},
		{"sol.h", nil, ""},
		{"archive.tar.gz", nil, ""},
	} {
		got := ""
		if l := r.FilenameToLanguage(c.file, c.allowed); l != nil {
			got = l.Name
		}
		if got != c.want {
			t.Errorf("FilenameToLanguage(%q, %q) = %q, want %q", c.file, c.allowed, got, c.want)
		}
	}
	for _, c := range []struct{ lang, file, want string }{
		{"", "sol.%l", "sol.%l"},
		{"C++17 / g++", "sol.%l", "sol.cpp"},
		{"Python 3 / PyPy", "grader.%l", "grader.py"},
		{"Nope", "sol.%l", "sol.invalid_language"},
		{"Rust", "plain.txt", "plain.txt"},
	} {
		if got := r.LangFilename(c.lang, c.file); got != c.want {
			t.Errorf("LangFilename(%q, %q) = %q, want %q", c.lang, c.file, got, c.want)
		}
	}
	if !r.RequiresMultithreading([]string{"C11 / gcc", "Java / JDK"}) || r.RequiresMultithreading([]string{"C11 / gcc", "Nope"}) {
		t.Error("RequiresMultithreading")
	}
	if got := r.HeaderExtensions(); !slices.Equal(got, []string{".h", "lib.pas"}) {
		t.Errorf("header extensions %q", got)
	}
	if got := r.ObjectExtensions(); !slices.Equal(got, []string{".o"}) {
		t.Errorf("object extensions %q", got)
	}
	src := r.SourceExtensions()
	for _, e := range []string{".c", ".cpp", ".C", ".cs", ".go", ".hs", ".java", ".kt", ".pas", ".php", ".py", ".rs"} {
		if !slices.Contains(src, e) {
			t.Errorf("source extensions %q lack %q", src, e)
		}
	}
	if !slices.IsSorted(src) || len(slices.Compact(slices.Clone(src))) != len(src) {
		t.Errorf("source extensions %q not sorted and unique", src)
	}
	cpp := r.byName["C++17 / g++"]
	for _, c := range []struct {
		file string
		want bool
	}{{"grader.cpp", true}, {"graderlib.h", true}, {"x.o", true}, {"x.py", false}, {"checker", false}} {
		if got := cpp.IsCompilationFile(c.file); got != c.want {
			t.Errorf("IsCompilationFile(%q) = %v", c.file, got)
		}
	}
	if !r.byName["Pascal / fpc"].IsCompilationFile("graderlib.pas") {
		t.Error("graderlib.pas is a Pascal compilation file")
	}
}

func TestLoadOverrideDir(t *testing.T) {
	if r, err := Load(""); err != nil || r != Builtin() {
		t.Fatalf("Load(\"\") = %v, %v", r, err)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing directory: want an error")
	}
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Replaces a built-in language and adds one; other files are ignored.
	write("rust.yaml", "name: Rust\nsource_extensions: [.rs]\ncompilation:\n  commands: [[/usr/bin/rustc, --edition=2021, -O, -o, \"{executable}\", \"{main_source}\"]]\n"+
		"evaluation:\n  commands: [[\"./{executable}\", \"{args}\"]]\n")
	write("zig.yml", "name: Zig\nsource_extensions: [.zig]\ncompilation:\n  commands: [[/usr/bin/zig, build-exe, \"{main_source}\"]]\n"+
		"evaluation:\n  commands: [[\"./{executable}\"]]\n")
	write("README.md", "not a language")
	if err := os.Mkdir(filepath.Join(dir, "sub.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(r.All()), len(Builtin().All())+1; got != want {
		t.Errorf("%d languages, want %d", got, want)
	}
	rust, _ := r.Get("Rust")
	cmds, _ := rust.CompilationCommands([]string{"a.rs"}, "a", true)
	if cmds[0][1] != "--edition=2021" || rust.Digest() == Builtin().byName["Rust"].Digest() {
		t.Errorf("Rust was not replaced: %q", cmds)
	}
	if _, err := r.Get("Zig"); err != nil {
		t.Error(err)
	}
	if Builtin().byName["Rust"].File != "config/languages/rust.yaml" {
		t.Error("the built-in registry changed")
	}

	// Errors name the file; duplicates are refused.
	write("broken.yaml", "name: Broken\n")
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "broken.yaml") {
		t.Errorf("broken file: %v", err)
	}
	os.Remove(filepath.Join(dir, "broken.yaml"))
	write("zig2.yaml", "name: Zig\nsource_extensions: [.zig]\ninterpreted: true\nevaluation:\n  commands: [[/usr/bin/zig, run, \"{executable}\"]]\n")
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "defined twice") {
		t.Errorf("duplicate name: %v", err)
	}
}

func TestInterpreted(t *testing.T) {
	php := Builtin().byName["PHP"]
	for _, c := range []struct {
		sources []string
		exe     string
		want    [][]string
	}{
		{[]string{"sol.php"}, "sol.php", [][]string{{"/bin/true"}}},
		{[]string{"grader.php", "sol.php"}, "sol.php", [][]string{{"/bin/cp", "grader.php", "sol.php"}}},
	} {
		got, err := php.CompilationCommands(c.sources, c.exe, true)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q -> %q: %q %v", c.sources, c.exe, got, err)
		}
	}
}
