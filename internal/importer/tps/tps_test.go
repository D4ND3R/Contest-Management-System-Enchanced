// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package tps

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
)

func needCompiler(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath(Compiler); err != nil {
		t.Skipf("%s is not installed", Compiler)
	}
}

func load(t *testing.T, dir string) (*importer.Task, *importer.Report) {
	t.Helper()
	rep := &importer.Report{}
	task, err := Loader{}.LoadTask(dir, &importer.Options{Report: rep})
	if err != nil {
		t.Fatalf("LoadTask %s: %v", dir, err)
	}
	return task, rep
}

func messages(issues []importer.Issue) string {
	var out []string
	for _, i := range issues {
		out = append(out, i.String())
	}
	return strings.Join(out, "\n")
}

// fixtures are the tasks of testdata with the managers compiled at import
// time.
var fixtures = map[string][]string{
	"tpsbatch":  {"checker"},
	"tpscomm":   {"manager"},
	"tpsoutput": {"checker"},
	"tpsbao":    nil,
}

func TestRegistered(t *testing.T) {
	for _, name := range []string{"tps", "tps_task"} {
		if l, ok := importer.Default.Lookup(name); !ok || l.Name() != "tps" {
			t.Errorf("Lookup(%q) = %v, %v", name, l, ok)
		}
	}
	for name := range fixtures {
		if l, err := importer.Default.ChooseTask("", fixture(name)); err != nil || l.Name() != "tps" {
			t.Errorf("ChooseTask(%s) = %v, %v", name, l, err)
		}
	}
	l := Loader{}
	if l.Detect("testdata", importer.KindTask) || l.Detect(fixture("tpsbatch"), importer.KindContest) {
		t.Error("detected what is not a TPS task")
	}
}

// TestLoadTaskLikeUpstream compares the tasks of testdata with the rows
// the original CMS stored for them.
func TestLoadTaskLikeUpstream(t *testing.T) {
	needCompiler(t)
	for name, compiled := range fixtures {
		t.Run(name, func(t *testing.T) {
			task, rep := load(t, fixture(name))
			if rep.HasErrors() {
				t.Fatalf("errors:\n%s", messages(rep.Errors()))
			}
			want := readUpstream(t, filepath.Join("testdata", "upstream", name+".json"))
			got := upstreamOfModel(t, task)
			compareUpstream(t, got, want, compiled...)
			for _, m := range compiled {
				if g, w := got.Datasets[0].Managers[m], want.Datasets[0].Managers[m]; g != w {
					t.Logf("the %s compiled here (%s) is not the binary of the reference (%s): another compiler", m, g, w)
				}
			}
			importer.ValidateTask(task, nil, rep)
			if rep.HasErrors() {
				t.Errorf("validation errors:\n%s", messages(rep.Errors()))
			}
		})
	}
}

func TestLoadTask(t *testing.T) {
	needCompiler(t)
	task, rep := load(t, fixture("tpsbatch"))
	if task.Name != "tpsbatch" || task.Title != "Sum of a list" || task.ScoreMode != importer.ScoreModeMaxSubtask {
		t.Errorf("task %+v", task)
	}
	if !slices.Equal(task.PrimaryStatements, []string{"en_US"}) || len(task.Statements) != 2 {
		t.Errorf("statements %+v (primary %v)", task.Statements, task.PrimaryStatements)
	}
	d := task.Datasets[0]
	var names []string
	for _, m := range d.Managers {
		names = append(names, m.Filename)
	}
	if !slices.Equal(names, []string{"checker", "grader.cpp", "grader.java", "grader.py", "tpsbatch.h"}) {
		t.Errorf("managers %v", names)
	}
	if len(rep.Issues()) != 0 {
		t.Errorf("issues:\n%s", messages(rep.Issues()))
	}

	task, _ = load(t, fixture("tpscomm"))
	d = task.Datasets[0]
	names = nil
	for _, m := range d.Managers {
		names = append(names, m.Filename)
	}
	// stub.* are graders; manager.cpp is compiled to manager.
	if !slices.Equal(names, []string{"grader.cpp", "grader.java", "tpscomm.h", "manager"}) {
		t.Errorf("managers %v", names)
	}
	if *d.TimeLimit != 2 || task.FeedbackLevel != importer.FeedbackOIRestricted {
		t.Errorf("time limit %v, feedback %s", *d.TimeLimit, task.FeedbackLevel)
	}

	task, rep = load(t, fixture("tpsoutput"))
	if w := messages(rep.Warnings()); !strings.Contains(w, "score_mode is not used") || !strings.Contains(w, "grader folder was not found") {
		t.Errorf("warnings:\n%s", w)
	}
	if d := task.Datasets[0]; d.TimeLimit != nil || d.MemoryLimit != nil || *task.MaxSubmissionNumber != 100 {
		t.Errorf("output only: %+v", d)
	}

	// -S: no statements.
	task, err := Loader{}.LoadTask(fixture("tpsbatch"), &importer.Options{SkipStatements: true})
	if err != nil || len(task.Statements) != 0 || len(task.PrimaryStatements) != 0 {
		t.Errorf("SkipStatements: %v %v", err, task.Statements)
	}
}

// writeTask writes a TPS task with problem.json data and files.
func writeTask(t *testing.T, data map[string]any, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	b, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	files["problem.json"] = string(b)
	for p, content := range files {
		path := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func base(taskType string, params any) map[string]any {
	return map[string]any{
		"code": "x", "name": "X", "time_limit": 1, "memory_limit": 64 << 20,
		"task_type": taskType, "task_type_params": params,
	}
}

func tests(codenames ...string) map[string]string {
	m := map[string]string{}
	for _, c := range codenames {
		m["tests/"+c+".in"] = c + "\n"
		m["tests/"+c+".out"] = c + "\n"
	}
	return m
}

func TestTaskTypes(t *testing.T) {
	cases := []struct {
		name     string
		data     map[string]any
		files    map[string]string
		params   string
		format   []string
		managers []string
	}{
		{"batch defaults", base("batch", `{}`), tests("a"),
			`["grader", ["", ""], "diff"]`, []string{"x.%l"}, nil},
		{"batch alone with files", base("Batch", `{"task_type_parameters_Batch_compilation": "alone", "task_type_parameters_Batch_io_0_inputfile": "x.in"}`), tests("a"),
			`["alone", ["x.in", ""], "diff"]`, []string{"x.%l"}, nil},
		{"null params", base("OutputOnly", nil), tests("a", "b"),
			`["diff"]`, []string{"output_a.txt", "output_b.txt"}, nil},
		{"communication default processes", base("Communication", ""), tests("a"),
			`[1, "grader", "std_io"]`, []string{"x.%l"}, nil},
		{"two steps gets an empty pascal library", base("TwoSteps", "{}"), tests("a"),
			`["diff"]`, []string{"x.%l"}, []string{"xlib.pas"}},
		{"two steps with its library", base("TwoSteps", "{}"), merge(tests("a"), map[string]string{"graders/xlib.pas": "unit xlib;"}),
			`["diff"]`, []string{"x.%l"}, []string{"xlib.pas"}},
		{"output only sorts the codenames", base("OutputOnly", "{}"), tests("a-b", "a", "a_b"),
			`["diff"]`, []string{"output_a.txt", "output_a-b.txt", "output_a_b.txt"}, nil},
		{"notice", base("notice", "{}"), tests("a"),
			`[]`, []string{}, nil},
		{"protocol 2", base("Communication", []any{3, "stub", "fifo_io"}), merge(tests("a"), map[string]string{"graders/stub.c": ""}),
			`[3, "grader", "fifo_io"]`, []string{"x.%l"}, []string{"grader.c"}},
		{"protocol 2 keeps nested values", base("Batch", []any{"alone", []any{"", ""}, "diff", map[string]any{"b": []any{1.5, nil}, "a": true}}), tests("a"),
			`["alone", ["", ""], "diff", {"a": true, "b": [1.5, null]}]`, []string{"x.%l"}, nil},
		{"params as an object", base("Batch", map[string]any{"task_type_parameters_Batch_compilation": "alone"}), tests("a"),
			`["alone", ["", ""], "diff"]`, []string{"x.%l"}, nil},
		{"batch and output", base("BatchAndOutput", "{}"), merge(tests("a", "b", "c"), map[string]string{
			"subtasks/1.json": `{"score": 10, "testcases": ["c", "b"], "output_only": true}`,
			"subtasks/2.json": `{"score": 90, "testcases": ["a", "b"], "output_optional": 1}`,
		}), `["grader", ["", ""], "diff", "b,c"]`, []string{"x.%l", "output_a.txt", "output_b.txt", "output_c.txt"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			task, _ := load(t, writeTask(t, c.data, c.files))
			d := task.Datasets[0]
			if string(d.TaskTypeParameters) != c.params {
				t.Errorf("parameters %s, want %s", d.TaskTypeParameters, c.params)
			}
			if !slices.Equal(task.SubmissionFormat, c.format) {
				t.Errorf("submission format %v, want %v", task.SubmissionFormat, c.format)
			}
			var names []string
			for _, m := range d.Managers {
				names = append(names, m.Filename)
			}
			if !slices.Equal(names, c.managers) {
				t.Errorf("managers %v, want %v", names, c.managers)
			}
		})
	}
}

func merge(a, b map[string]string) map[string]string {
	for k, v := range b {
		a[k] = v
	}
	return a
}

func TestScoreTypes(t *testing.T) {
	cases := []struct {
		name     string
		files    map[string]string
		typ, par string
	}{
		{"no tests", map[string]string{}, "Sum", "100.0"},
		{"three tests", tests("a", "b", "c"), "Sum", "33.333333333333336"},
		{"subtasks without samples", merge(tests("1-1", "2.1", "x y"), map[string]string{
			"subtasks/a.json": `{"score": 40.9, "testcases": ["1-1", "2.1"]}`,
			"subtasks/b.json": `{"score": "60", "testcases": ["x y"]}`,
		}), "GroupMin", `[[40, "1\\-1|2\\.1"], [60, "x\\ y"]]`},
		{"samples", merge(tests("s", "a"), map[string]string{
			"subtasks/0.json": `{"score": 0, "testcases": ["s"]}`,
			"subtasks/1.json": `{"score": 100, "testcases": ["s", "a"]}`,
		}), "GroupMin", `[[0, "s", "Samples"], [100, "s|a", "Subtask 1"]]`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			task, _ := load(t, writeTask(t, base("Batch", "{}"), c.files))
			d := task.Datasets[0]
			if d.ScoreType != c.typ || string(d.ScoreTypeParameters) != c.par {
				t.Errorf("score type %s %s, want %s %s", d.ScoreType, d.ScoreTypeParameters, c.typ, c.par)
			}
		})
	}
	// Python's re.escape since 3.7.
	if got := reEscape("a-b.c_d e!#&~é"); got != `a\-b\.c_d\ e!\#\&\~é` {
		t.Errorf("reEscape = %s", got)
	}
}

func TestErrors(t *testing.T) {
	cases := []struct {
		name  string
		data  map[string]any
		files map[string]string
		want  string
	}{
		{"tps repository", map[string]any{"name": "x", "title": "X", "type": "Batch"}, map[string]string{}, "tps export CMS"},
		{"no code", map[string]any{"name": "X"}, map[string]string{}, `no "code"`},
		{"code not a string", map[string]any{"code": 1, "name": "X"}, map[string]string{}, `"code" must be a string`},
		{"bad feedback", merge2(base("Batch", "{}"), "feedback_level", "everything"), tests("a"), "invalid feedback_level"},
		{"feedback not a string", merge2(base("Batch", "{}"), "feedback_level", 1), tests("a"), "invalid feedback_level 1"},
		{"no time limit", del(base("Batch", "{}"), "time_limit"), tests("a"), "has no time_limit"},
		{"bad memory limit", merge2(base("Batch", "{}"), "memory_limit", "lots"), tests("a"), "invalid memory_limit"},
		{"empty task type", base("", "{}"), tests("a"), "task_type is empty"},
		{"bad params", base("Batch", "[1,"), tests("a"), "task_type_params is not a JSON object"},
		{"missing output", base("Batch", "{}"), map[string]string{"tests/a.in": ""}, "could not find the output file for testcase a"},
		{"stub and grader", base("Communication", "{}"), merge(tests("a"), map[string]string{"graders/stub.cpp": "", "graders/grader.cpp": ""}), "both a stub and a grader"},
		{"subtask not json", base("Batch", "{}"), merge(tests("a"), map[string]string{"subtasks/README": "subtasks"}), "subtasks/README"},
		{"subtask without testcases", base("Batch", "{}"), merge(tests("a"), map[string]string{"subtasks/1.json": `{"score": 5}`}), "has no testcases"},
		{"bad score precision", merge2(base("Batch", "{}"), "score_precision", "two"), tests("a"), "invalid score_precision"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Loader{}.LoadTask(writeTask(t, c.data, c.files), nil)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %v, want %q", err, c.want)
			}
		})
	}
}

func merge2(m map[string]any, k string, v any) map[string]any {
	m[k] = v
	return m
}

func del(m map[string]any, k string) map[string]any {
	delete(m, k)
	return m
}

func TestWarnings(t *testing.T) {
	needCompiler(t)
	data := base("Batch", "{}")
	data["memory_limit"] = 256
	files := merge(tests("a"), map[string]string{
		"subtasks/1.json":        `{"score": 100, "testcases": ["a", "zz"]}`,
		"attachments/dir/x":      "",
		"checker/checker.cpp":    "int main( {",
		"statements/en_US.pdf":   "%PDF",
		"statements/README.txt":  "",
		"graders/sub/grader.cpp": "",
	})
	task, rep := load(t, writeTask(t, data, files))
	w := messages(rep.Warnings())
	for _, want := range []string{"memory_limit is in bytes", "testcase zz is not in tests/", "dir is a directory: skipped", "sub is a directory: skipped"} {
		if !strings.Contains(w, want) {
			t.Errorf("warnings lack %q:\n%s", want, w)
		}
	}
	if e := messages(rep.Errors()); !strings.Contains(e, "could not compile the checker") {
		t.Errorf("errors:\n%s", e)
	}
	if len(task.Attachments) != 0 || len(task.Statements) != 1 {
		t.Errorf("attachments %v statements %v", task.Attachments, task.Statements)
	}
	// The validator rejects the memory limit and the missing checker.
	importer.ValidateTask(task, nil, rep)
	e := messages(rep.Errors())
	for _, want := range []string{"invalid memory limit 256 bytes", `needs the manager "checker"`} {
		if !strings.Contains(e, want) {
			t.Errorf("errors lack %q:\n%s", want, e)
		}
	}
}

// TestFalseFeedbackLevel: upstream ignores a false feedback_level.
func TestFalseFeedbackLevel(t *testing.T) {
	for _, v := range []any{nil, "", 0, false} {
		task, _ := load(t, writeTask(t, merge2(base("Batch", "{}"), "feedback_level", v), tests("a")))
		if task.FeedbackLevel != importer.FeedbackRestricted {
			t.Errorf("feedback_level %v: %s", v, task.FeedbackLevel)
		}
	}
}

func TestNoticeIsRejected(t *testing.T) {
	task, rep := load(t, writeTask(t, base("Notice", "{}"), tests("a")))
	importer.ValidateTask(task, nil, rep)
	if e := messages(rep.Errors()); !strings.Contains(e, `unknown task type "Notice"`) {
		t.Errorf("errors:\n%s", e)
	}
}

func TestConversions(t *testing.T) {
	ints := map[string]int64{`3`: 3, `3.9`: 3, `-3.9`: -3, `" 42 "`: 42, `true`: 1, `"1_000"`: 1000}
	for in, want := range ints {
		if got, err := pyInt(json.RawMessage(in)); err != nil || got != want {
			t.Errorf("pyInt(%s) = %d, %v", in, got, err)
		}
	}
	for _, in := range []string{`"3.5"`, `null`, `[1]`, `"x"`} {
		if _, err := pyInt(json.RawMessage(in)); err == nil {
			t.Errorf("pyInt(%s) accepted", in)
		}
	}
	floats := map[string]float64{`1.5`: 1.5, `"2"`: 2, `" 0.25 "`: 0.25, `false`: 0}
	for in, want := range floats {
		if got, err := pyFloat(json.RawMessage(in)); err != nil || got != want {
			t.Errorf("pyFloat(%s) = %v, %v", in, got, err)
		}
	}
	for _, s := range []struct{ in, want string }{{"stub.cpp", "stub"}, {"stub", "stub"}, {".stub", ".stub"}, {"a.b.c", "a.b"}, {"..x", "..x"}} {
		if got := stem(s.in); got != s.want {
			t.Errorf("stem(%q) = %q, want %q", s.in, got, s.want)
		}
	}
	for f, want := range map[float64]string{100: "100.0", 100.0 / 3: "33.333333333333336", 1e16: "1e+16", 1e-5: "1e-05", 0: "0.0", 12.5: "12.5"} {
		if got := pyFloatRepr(f); got != want {
			t.Errorf("pyFloatRepr(%v) = %s, want %s", f, got, want)
		}
	}
}

// TestCompileCache: a source compiles once while its directory does not
// change.
func TestCompileCache(t *testing.T) {
	needCompiler(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "checker.cpp")
	os.WriteFile(src, []byte("#include \"x.h\"\nint main() { return X; }\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "x.h"), []byte("#define X 0\n"), 0o644)
	a, err := compile(src, "checker")
	if err != nil {
		t.Fatal(err)
	}
	b, err := compile(src, "checker")
	if err != nil || &a[0] != &b[0] {
		t.Errorf("not cached: %v", err)
	}
	// A header changed: compiled again.
	os.WriteFile(filepath.Join(dir, "x.h"), []byte("#define X 1\n"), 0o644)
	c, err := compile(src, "checker")
	if err != nil || &c[0] == &a[0] {
		t.Errorf("cached across a change: %v", err)
	}
	// A stale binary named checker does not count.
	os.WriteFile(filepath.Join(dir, "checker"), []byte("old"), 0o755)
	d, err := compile(src, "checker")
	if err != nil || &d[0] != &c[0] {
		t.Errorf("the stale binary changed the key: %v", err)
	}
}
