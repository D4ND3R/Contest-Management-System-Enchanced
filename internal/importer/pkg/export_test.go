// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package pkg

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer/italy"
)

// fileOpener opens files of the model where they are.
func fileOpener(_ context.Context, f *importer.File) (io.ReadCloser, error) { return f.Open() }

// roundTrip encodes a task, writes the package as a directory and as a
// zip, reads both back and checks they give the same task.
func roundTrip(t *testing.T, what string, tk *importer.Task) *Encoded {
	t.Helper()
	ctx := context.Background()
	enc, err := Encode(ctx, tk, tk.Datasets[0], nil, fileOpener)
	if err != nil {
		t.Fatalf("%s: Encode: %v", what, err)
	}
	want := canonical(t, tk, nil)
	dir := filepath.Join(t.TempDir(), "exported")
	if err := WriteDir(ctx, enc, dir, fileOpener); err != nil {
		t.Fatalf("%s: WriteDir: %v", what, err)
	}
	var zipped bytes.Buffer
	if err := WriteZip(ctx, enc, &zipped, fileOpener); err != nil {
		t.Fatalf("%s: WriteZip: %v", what, err)
	}
	zp := filepath.Join(t.TempDir(), "exported.zip")
	if err := os.WriteFile(zp, zipped.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{dir, zp} {
		pk, rep := load(t, p)
		if pk.Task == nil {
			t.Fatalf("%s: %s: no task\n%s\n%s", what, p, issues(rep), enc.Problem)
		}
		sameTask(t, what+" from "+filepath.Base(p), canonical(t, pk.Task, nil), want)
		if t.Failed() {
			t.Logf("problem.yaml:\n%s", enc.Problem)
		}
	}
	return enc
}

// TestExportRoundTrip: every package of the loader tests, exported and
// read again, is the same task.
func TestExportRoundTrip(t *testing.T) {
	yaml := "name: r\ntitle: R\ntime_limit: 1\nmemory_limit: 64\n"
	base := with(tests(5), map[string]string{"statement/en.pdf": pdf, "solutions/main.ac.cpp": ""})
	packages := map[string]map[string]string{
		"batch": func() map[string]string {
			f := batchPackage()
			delete(f, "graders/checker.cpp")
			return f
		}(),
		"defaults":                   with(base, map[string]string{"problem.yaml": "name: r\ntitle: R\n"}),
		"two statements, no primary": with(base, map[string]string{"problem.yaml": yaml, "statement/es.md": "# R"}),
		"no statement primary":       with(base, map[string]string{"problem.yaml": yaml + "primary_statements: []\n"}),
		"batch and output": with(base, map[string]string{"problem.yaml": yaml + "output_only_tests: 5\noutput_tests: 1-2\n",
			"attachments/input_02.txt": "not the input"}),
		"batch and output without inputs": with(base, map[string]string{"problem.yaml": yaml + "output_only_tests: 5\nattach_inputs: false\n"}),
		"batch and output empty":          with(base, map[string]string{"problem.yaml": yaml + "type: batch_and_output\n"}),
		"output only": with(base, map[string]string{"problem.yaml": "name: r\ntitle: R\ntype: output_only\nchecker: exact\n" +
			"submission_format: [output_01.txt, output_02.txt]\n"}),
		"output only all": with(base, map[string]string{"problem.yaml": "name: r\ntitle: R\ntype: output_only\n"}),
		"communication": with(base, map[string]string{"problem.yaml": yaml + "type: communication\nprocesses: 3\nuser_io: std_io\n",
			"graders/manager*": "\x7fELF", "graders/grader.cpp": "g", "graders/r.h": "h"}),
		"communication without grader": with(base, map[string]string{"problem.yaml": yaml + "type: communication\ngrader: false\n",
			"graders/manager*": "\x7fELF", "graders/grader.cpp": "g"}),
		"interactive": with(base, map[string]string{"problem.yaml": yaml + "type: interactive\nconcurrent: false\nprocess_limit: 4\n" +
			"controller_memory_limit: 128\ncontroller_time_limit: 2.5\ncontroller_wall_time_limit: 7\n", "graders/controller*": "#!/bin/sh\n"}),
		"two steps": with(base, map[string]string{"problem.yaml": yaml + "type: two_steps\nchecker: comparator\nsubmission_format: [a.%l, b.%l]\n",
			"graders/manager.cpp": "m", "graders/checker*": "\x7fELF"}),
		"testlib": with(base, map[string]string{"problem.yaml": yaml + "checker: testlib\nlanguages: []\n", "graders/checker*": "\x7fELF"}),
		"float":   with(base, map[string]string{"problem.yaml": yaml + "checker: float\nautojudge: true\ndataset: v2\n"}),
		"tokens and limits": with(base, map[string]string{"problem.yaml": yaml + "max_submissions: 3\nmax_user_tests: 4\n" +
			"min_submission_interval: 1.25\nmin_user_test_interval: 2\nscore_precision: 3\nfeedback_level: oi_restricted\nscore_mode: max\n" +
			"tokens: {mode: infinite, max_number: 2, min_interval: 3, gen_initial: 0, gen_number: 1, gen_interval: 0.5, gen_max: 9}\n"}),
		"sum points":   with(base, map[string]string{"problem.yaml": yaml + "scoring: {points: 30}\n"}),
		"sum per test": with(base, map[string]string{"problem.yaml": yaml + "scoring: {per_test: 0.1}\n"}),
		"regex": with(base, map[string]string{"problem.yaml": yaml +
			"score_precision: 1\nscoring:\n  type: GroupMul\n  subtasks:\n    - {points: 40, regex: \"0[12]\"}\n    - {points: 60.5, regex: \".*\"}\n"}),
		"count": with(base, map[string]string{"problem.yaml": yaml +
			"scoring:\n  type: GroupMin\n  points: 7\n  subtasks:\n    - {points: 3, count: 2}\n    - {points: 4, count: 3}\n"}),
		"threshold": with(base, map[string]string{"problem.yaml": yaml +
			"scoring:\n  type: GroupThreshold\n  subtasks:\n    - {points: 40, tests: 1-2, threshold: 0.5, always_show_tests: true}\n    - {points: 60, tests: [3, 5], threshold: 1}\n"}),
		"public names": with(base, map[string]string{"problem.yaml": yaml + "public_tests: 1-3, sample\n",
			"tests/sample.in": "s", "tests/sample.out": "s", "tests/10.in": "t", "tests/10.out": "t"}),
	}
	for name, files := range packages {
		t.Run(name, func(t *testing.T) {
			pk, rep := load(t, pkgDir(t, files))
			if pk.Task == nil || rep.HasErrors() {
				t.Fatalf("invalid package:\n%s", issues(rep))
			}
			enc := roundTrip(t, name, pk.Task)
			if len(enc.Warnings) > 0 {
				t.Errorf("warnings %v", enc.Warnings)
			}
		})
	}
}

// TestExportProblemYAML checks what problem.yaml says: the settings that
// differ from the defaults, in the readable form.
func TestExportProblemYAML(t *testing.T) {
	files := batchPackage()
	delete(files, "graders/checker.cpp")
	tk, _ := load(t, pkgDir(t, files))
	enc, err := Encode(context.Background(), tk.Task, tk.Task.Datasets[0], nil, fileOpener)
	if err != nil {
		t.Fatal(err)
	}
	want := `# Task sum, dataset "Default".
# Format: docs/en/package-format.md
name: sum
title: A plus B
dataset: Default
time_limit: 1.5
memory_limit: 256
input: input.txt
output: output.txt
checker: comparator
scoring:
  type: GroupMin
  subtasks:
    - points: 30
      tests: 01, 02
    - points: 70
      tests: 03-05
public_tests: 01, 03
languages:
  - C11 / gcc
  - C++20 / g++
primary_statements:
  - en
feedback_level: full
score_mode: max_subtask
score_precision: 2
max_submissions: 50
min_submission_interval: 30
min_user_test_interval: 0.5
tokens:
  mode: finite
  max_number: 5
  gen_interval: 60
`
	if string(enc.Problem) != want {
		t.Errorf("problem.yaml:\n%s\nwant:\n%s", enc.Problem, want)
	}
	var paths []string
	for _, f := range enc.Files {
		mark := ""
		if f.Executable {
			mark = "*"
		}
		paths = append(paths, f.Path+mark)
	}
	wantPaths := []string{"statement/en.pdf", "statement/es.md", "attachments/sample.zip",
		"tests/01.in", "tests/01.out", "tests/02.in", "tests/02.out", "tests/03.in", "tests/03.out",
		"tests/04.in", "tests/04.out", "tests/05.in", "tests/05.out",
		"graders/checker*", "graders/grader.c", "graders/grader.cpp", "graders/sum.h"}
	if !slices.Equal(paths, wantPaths) {
		t.Errorf("files\n got %v\nwant %v", paths, wantPaths)
	}
	dir := filepath.Join(t.TempDir(), "out")
	if err := WriteDir(context.Background(), enc, dir, fileOpener); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(filepath.Join(dir, "graders", "checker")); err != nil || st.Mode().Perm() != 0o755 {
		t.Errorf("checker: %v %v", st, err)
	}
	if st, err := os.Stat(filepath.Join(dir, "tests", "01.in")); err != nil || st.Mode().Perm() != 0o644 {
		t.Errorf("test: %v %v", st, err)
	}
}

// copyTree copies a directory.
func copyTree(t testing.TB, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		out := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(out, data, 0o755)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// demoContest copies the italy_yaml demo contest; the comparator of
// "half" is built by build.sh: a placeholder stands for it when missing.
func demoContest(t testing.TB) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "demo")
	copyTree(t, "../../../testdata/contests/demo", dir)
	checker := filepath.Join(dir, "half", "check", "checker")
	if _, err := os.Stat(checker); err != nil {
		if err := os.WriteFile(checker, []byte("#!/bin/sh\necho 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// italyTasks are tasks of the italy_yaml format that the demo does not
// have: graders and headers, a checker in cor/, BatchAndOutput,
// Communication with stubs, Interactive.
func italyTasks(t testing.TB) string {
	t.Helper()
	files := with(map[string]string{
		"zgr/task.yaml": "name: zgr\ntitle: Graders\ntimeout: 2\nmemlimit: 64\nversion: v2\nfeedback_level: full\n" +
			"score_mode: max_subtask\nscore_precision: 2\nmax_submission_number: 7\nmin_user_test_interval: 60\n" +
			"token_initial: 3\ntoken_total: 10\ntoken_gen_number: 1\ntoken_gen_time: 0\ntoken_max: 5\ntoken_min_interval: 1.5\n" +
			"output_only_testcases: \"1\"\noutput_optional_testcases: \"2, 3\"\npublic_testcases: \"0,2\"\nprimary_language: en\n",
		"zgr/gen/GEN":               "#ST: 30\ngen 1\n#COPY: x\n#ST: 70\ngen 3\ngen 4\n",
		"zgr/sol/grader.cpp":        "grader cpp\n",
		"zgr/sol/grader.c":          "grader c\n",
		"zgr/sol/g.h":               "header\n",
		"zgr/sol/glib.pas":          "pas\n",
		"zgr/cor/correttore":        "\x7fELF checker\n",
		"zgr/statement/english.pdf": "%PDF-en\n",
		"zgr/statement/italian.pdf": "%PDF-it\n",
		"zgr/att/sample.txt":        "sample\n",

		"zcomm/task.yaml":       "name: zcomm\ntitle: Comm\nn_input: 2\ntotal_value: 30\nnum_processes: 2\ntoken_mode: finite\ntoken_max_number: 4\ntoken_gen_interval: 90\ntimeout: 1\nmemlimit: 32\n",
		"zcomm/sol/stub.cpp":    "stub\n",
		"zcomm/sol/stub.h":      "stub h\n",
		"zcomm/check/manager":   "\x7fELF manager\n",
		"zcomm/testo/testo.pdf": "testo\n",

		"zinter/task.yaml": "name: zinter\ntitle: Inter\nn_input: 1\ncontroller_process_limit: 3\ninteractive_concurrent: false\n" +
			"controller_memory_limit: 256\ncontroller_time_limit: 10\ncontroller_wall_time_limit: 20.5\ntoken_mode: disabled\n" +
			"score_type: GroupMul\nscore_type_parameters: [[50.0, \"00[01]\"], [50, \"0\"]]\ntimeout: 1\nmemlimit: 32\n",
		"zinter/input/input0.txt":        "i\n",
		"zinter/output/output0.txt":      "o\n",
		"zinter/cor/controller":          "#!/bin/sh\n",
		"zinter/statement/statement.pdf": "s\n",
	})
	for i := range 4 {
		files["zgr/input/input"+string(rune('0'+i))+".txt"] = "in " + string(rune('0'+i)) + "\n"
		files["zgr/output/output"+string(rune('0'+i))+".txt"] = "out " + string(rune('0'+i)) + "\n"
	}
	for i := range 2 {
		files["zcomm/input/input"+string(rune('0'+i))+".txt"] = "cin " + string(rune('0'+i)) + "\n"
		files["zcomm/output/output"+string(rune('0'+i))+".txt"] = "cout " + string(rune('0'+i)) + "\n"
	}
	return writeFiles(t, t.TempDir(), files)
}

// TestExportItalyTasks: tasks read by the italy_yaml loader, exported in
// this format and read again, are the same tasks (the exporter does not
// depend on how a task was written).
func TestExportItalyTasks(t *testing.T) {
	demo := demoContest(t)
	synthetic := italyTasks(t)
	for _, dir := range []string{
		filepath.Join(demo, "sum"), filepath.Join(demo, "half"), filepath.Join(demo, "quiz"),
		filepath.Join(synthetic, "zgr"), filepath.Join(synthetic, "zcomm"), filepath.Join(synthetic, "zinter"),
	} {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			rep := &importer.Report{}
			tk, err := italy.Loader{}.LoadTask(dir, &importer.Options{Report: rep})
			if err != nil || tk == nil {
				t.Fatalf("%v\n%s", err, issues(rep))
			}
			importer.ValidateTask(tk, nil, rep)
			if rep.HasErrors() {
				t.Fatalf("invalid task:\n%s", issues(rep))
			}
			roundTrip(t, filepath.Base(dir), tk)
		})
	}
}

func TestEncodeWarningsAndErrors(t *testing.T) {
	ctx := context.Background()
	tk, _ := load(t, pkgDir(t, with(tests(2), map[string]string{
		"problem.yaml":     "name: e\ntitle: E\ntime_limit: 1\nmemory_limit: 64\nchecker: comparator\n",
		"graders/checker*": "\x7fELF", "statement/en.pdf": pdf,
	})))
	task := tk.Task
	d := task.Datasets[0]
	b := func(s string) *importer.File { return importer.FileFromBytes(s, []byte(s)) }
	d.Managers = append(d.Managers,
		importer.Manager{Filename: "testlib.h", File: b("t")},
		importer.Manager{Filename: "checker.cpp", File: b("c")},
		importer.Manager{Filename: "stub.cpp", File: b("s")})
	task.Attachments = append(task.Attachments, importer.Attachment{Filename: ".hidden", File: b("h")})
	enc, err := Encode(ctx, task, d, nil, fileOpener)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"attachment .hidden is a hidden file", "manager testlib.h will not be imported back",
		"manager checker.cpp will not be imported back", "manager stub.cpp will not be imported back"} {
		if !slices.ContainsFunc(enc.Warnings, func(s string) bool { return strings.Contains(s, w) }) {
			t.Errorf("no warning %q in %v", w, enc.Warnings)
		}
	}

	for _, c := range []struct {
		change func(tk *importer.Task, d *importer.Dataset)
		err    string
	}{
		{func(_ *importer.Task, d *importer.Dataset) { d.MemoryLimit = importer.Ptr(int64(1000)) }, "not a whole number of MiB"},
		{func(_ *importer.Task, d *importer.Dataset) { d.TaskType = "Batchy" }, "dataset \"Default\""},
		{func(_ *importer.Task, d *importer.Dataset) { d.TaskTypeParameters = json.RawMessage(`["alone"]`) }, "dataset \"Default\""},
		{func(_ *importer.Task, d *importer.Dataset) {
			d.ScoreTypeParameters = json.RawMessage(`[[50, 1], [50, "0"]]`)
		}, "dataset \"Default\""},
		{func(_ *importer.Task, d *importer.Dataset) { d.Testcases[0].Codename = "a/b" }, `invalid testcase codename "a/b"`},
		{func(tk *importer.Task, _ *importer.Dataset) { tk.Statements[0].Language = "../x" }, `statement language "../x"`},
		{func(tk *importer.Task, _ *importer.Dataset) {
			tk.Attachments = []importer.Attachment{{Filename: "../x", File: b("x")}}
		}, `invalid attachment file name "../x"`},
		{func(_ *importer.Task, d *importer.Dataset) {
			d.Managers = []importer.Manager{{Filename: "a/b", File: b("x")}}
		}, `invalid manager file name "a/b"`},
	} {
		pk, _ := load(t, pkgDir(t, with(tests(2), map[string]string{
			"problem.yaml": "name: e\ntitle: E\ntime_limit: 1\nmemory_limit: 64\n", "statement/en.pdf": pdf,
		})))
		c.change(pk.Task, pk.Task.Datasets[0])
		if _, err := Encode(ctx, pk.Task, pk.Task.Datasets[0], nil, fileOpener); err == nil || !strings.Contains(err.Error(), c.err) {
			t.Errorf("error %v, want %q", err, c.err)
		}
	}
}

func TestWriteDir(t *testing.T) {
	ctx := context.Background()
	enc := &Encoded{Problem: []byte("name: x\n"), Files: []PackageFile{{Path: "tests/01.in", File: importer.FileFromBytes("in", []byte("1"))}}}

	notEmpty := pkgDir(t, map[string]string{"x": ""})
	if err := WriteDir(ctx, enc, notEmpty, fileOpener); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Errorf("not empty: %v", err)
	}
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteDir(ctx, enc, empty, fileOpener); err != nil {
		t.Errorf("empty: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(empty, "tests", "01.in")); string(data) != "1" {
		t.Errorf("content %q", data)
	}

	// Nothing is left behind when a file fails, and unsafe paths are
	// refused.
	parent := t.TempDir()
	for _, f := range []PackageFile{
		{Path: "tests/02.in", File: importer.FileFromPath(filepath.Join(parent, "missing"))},
		{Path: "../escape", File: importer.FileFromBytes("x", nil)},
		{Path: "tests/../../escape", File: importer.FileFromBytes("x", nil)},
	} {
		bad := &Encoded{Problem: enc.Problem, Files: append(slices.Clone(enc.Files), f)}
		if err := WriteDir(ctx, bad, filepath.Join(parent, "out"), fileOpener); err == nil {
			t.Errorf("%s: no error", f.Path)
		}
		if entries, _ := os.ReadDir(parent); len(entries) != 0 {
			t.Errorf("%s: left %v", f.Path, entries)
		}
	}
}

func TestSelectionOf(t *testing.T) {
	names := []string{"001", "002", "003", "004", "005", "010", "a", "b10"}
	for _, c := range []struct {
		names  []string
		chosen []string
		want   string
	}{
		{names, []string{"001", "002", "003"}, "001-003"},
		{names, []string{"001", "002", "004"}, "001, 002, 004"},
		{names, []string{"001", "003", "004", "005", "a"}, "001, 003-005, a"},
		{names, []string{"010"}, "010"},
		{names, names, "all"},
		// With a test called 1, test 1 is that one: 001 needs its name,
		// and so does every test of a range starting at 1.
		{append(slices.Clone(names), "1"), []string{"001", "002", "003"}, "001, 002, 003"},
		{append(slices.Clone(names), "1"), []string{"002", "003", "004", "1"}, "1-004"},
	} {
		names := slices.Sorted(slices.Values(c.names))
		s := selectionOf(c.chosen, names)
		if got := s.String(); got != c.want {
			t.Errorf("%v: %q, want %q", c.chosen, got, c.want)
		}
		if got, bad := s.resolve(names); len(bad) > 0 || !slices.Equal(got, c.chosen) {
			t.Errorf("%v: resolves to %v %v", c.chosen, got, bad)
		}
	}
	if selectionOf(nil, names) != nil {
		t.Error("an empty selection is not nil")
	}
	// Any subset of any names resolves back to itself.
	r := rand.New(rand.NewPCG(1, 2))
	pool := []string{"0", "00", "01", "1", "2", "02", "3", "10", "11", "12", "x", "x1", "sample", "007", "008", "009"}
	for range 2000 {
		var names []string
		for _, n := range pool {
			if r.IntN(3) > 0 {
				names = append(names, n)
			}
		}
		slices.Sort(names)
		var chosen []string
		for _, n := range names {
			if r.IntN(2) == 0 {
				chosen = append(chosen, n)
			}
		}
		s := selectionOf(chosen, names)
		got, bad := s.resolve(names)
		if len(bad) > 0 || !slices.Equal(got, chosen) {
			t.Fatalf("names %v chosen %v: %q resolves to %v %v", names, chosen, s, got, bad)
		}
	}
}
