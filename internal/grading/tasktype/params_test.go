// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package tasktype

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
)

// Parameters of the tasks of cmstestsuite/tasks (upstream), as stored in
// datasets.task_type_parameters.
var upstreamParams = map[string][]string{
	NameBatch: {
		`["alone", ["", ""], "diff"]`,
		`["alone", ["input.txt", "output.txt"], "diff"]`,
		`["grader", ["input.txt", "output.txt"], "comparator"]`,
	},
	NameBatchAndOutput: {`["alone", ["", ""], "comparator", "000,001"]`},
	NameOutputOnly:     {`["diff"]`, `["comparator"]`},
	NameCommunication: {
		`[1, "alone", "std_io"]`,
		`[1, "grader", "fifo_io"]`,
		`[2, "grader", "std_io"]`,
	},
	NameInteractive: {
		`[200, "grader", true, 128.0, 1.0, 5.0]`,
		`[200, "alone", true, 512.0, 1.0, 5.0]`,
		`[1, "alone", false, 64, 2, 10]`,
	},
	NameTwoSteps: {`["diff"]`, `["comparator"]`},
}

func TestGetUpstreamParameters(t *testing.T) {
	for name, list := range upstreamParams {
		for _, p := range list {
			tt, err := Get(name, json.RawMessage(p))
			if err != nil {
				t.Errorf("%s %s: %v", name, p, err)
				continue
			}
			if tt.Name() != name {
				t.Errorf("%s: Name() = %q", name, tt.Name())
			}
		}
	}
	if len(upstreamParams) != len(Names()) {
		t.Errorf("not every task type is tested")
	}
	for _, n := range Names() {
		if !Valid(n) {
			t.Errorf("%s is not valid", n)
		}
	}
	if Valid("batch") || Valid("") {
		t.Error("unknown task types are valid")
	}
}

func TestGetInvalidParameters(t *testing.T) {
	cases := []struct {
		name, params, want string
	}{
		{NameBatch, `{}`, "Task type parameters for Batch are not a list"},
		{NameBatch, `null`, "Task type parameters for Batch are not a list"},
		{NameBatch, `not json`, "Task type parameters for Batch are not a list"},
		{NameBatch, `["alone"]`, "Task type Batch should have 3 parameters, received 1"},
		{NameBatch, `["x", ["", ""], "diff"]`, "Invalid choice x for parameter Compilation"},
		{NameBatch, `[1, ["", ""], "diff"]`, "Invalid choice 1 for parameter Compilation"},
		{NameBatch, `["alone", "", "diff"]`, "Parameter I/O (blank for stdin/stdout) should be a list"},
		{NameBatch, `["alone", [""], "diff"]`, "Invalid value for parameter I/O (blank for stdin/stdout)"},
		{NameBatch, `["alone", [1, ""], "diff"]`, "Invalid value for string parameter Input file"},
		{NameBatch, `["alone", ["", null], "diff"]`, "Invalid value for string parameter Output file"},
		{NameBatch, `["alone", ["", ""], "white"]`, "Invalid choice white for parameter Output evaluation"},
		// This fork: I/O files are plain names of the box.
		{NameBatch, `["alone", ["../input.txt", ""], "diff"]`, "invalid I/O file name"},
		{NameBatch, `["alone", ["", "a/b"], "diff"]`, "invalid I/O file name"},
		{NameBatchAndOutput, `["alone", ["", ""], "diff"]`, "Task type BatchAndOutput should have 4 parameters, received 3"},
		{NameBatchAndOutput, `["alone", ["", ""], "diff", 1]`, "Invalid value for string parameter Comma-separated list of output only testcases"},
		{NameOutputOnly, `["diff", "x"]`, "Task type OutputOnly should have 1 parameters, received 2"},
		{NameCommunication, `["1", "alone", "std_io"]`, "Invalid value for int parameter Number of Processes"},
		{NameCommunication, `[1.5, "alone", "std_io"]`, "Invalid value for int parameter Number of Processes"},
		{NameCommunication, `[1e2, "alone", "std_io"]`, "Invalid value for int parameter Number of Processes"},
		{NameCommunication, `[true, "alone", "std_io"]`, "Invalid value for int parameter Number of Processes"},
		{NameCommunication, `[0, "alone", "std_io"]`, "the number of processes must be between 1 and 1000"},
		{NameCommunication, `[1001, "alone", "std_io"]`, "the number of processes must be between 1 and 1000"},
		{NameCommunication, `[1, "alone", "pipes"]`, "Invalid choice pipes for parameter User I/O"},
		{NameInteractive, `[200, "grader", 1, 128.0, 1.0, 5.0]`, "Invalid value for bool parameter Concurrent solutions"},
		{NameInteractive, `[200, "grader", true, "128", 1.0, 5.0]`, "Invalid value for float parameter Controller memory limit (MB)"},
		{NameInteractive, `[200.0, "grader", true, 128.0, 1.0, 5.0]`, "Invalid value for int parameter Process limit"},
		{NameInteractive, `[0, "grader", true, 128.0, 1.0, 5.0]`, "the process limit must be between 1 and 1000"},
		{NameInteractive, `[200, "grader", true, 0, 1.0, 5.0]`, "the controller memory limit must be positive"},
		{NameInteractive, `[200, "grader", true, 128.0, 0, 5.0]`, "the controller time limits"},
		{NameInteractive, `[200, "grader", true, 128.0, 1.0, 4000]`, "the controller time limits"},
		{NameTwoSteps, `[]`, "Task type TwoSteps should have 1 parameters, received 0"},
	}
	for _, c := range cases {
		_, err := Get(c.name, json.RawMessage(c.params))
		if !errors.Is(err, ErrParameters) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s %s: error %v, want %q", c.name, c.params, err, c.want)
		}
	}
	if _, err := Get("Nope", json.RawMessage(`[]`)); !errors.Is(err, ErrUnknown) {
		t.Errorf("unknown task type: %v", err)
	}
	if _, err := Params("Nope"); !errors.Is(err, ErrUnknown) {
		t.Errorf("unknown task type: %v", err)
	}
}

// form returns the getter of ParseForm over a map of fields.
func form(fields map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := fields[k]
		return v, ok
	}
}

func TestParseForm(t *testing.T) {
	// The fields of the admin forms of cmstestsuite/tasks (upstream).
	cases := []struct {
		name   string
		fields map[string]string
		want   string
	}{
		{NameBatch, map[string]string{
			"compilation":      "alone",
			"io_0_inputfile":   "",
			"io_1_outputfile":  "",
			"output_eval":      "diff",
			"unrelated_fields": "x",
		}, `["alone",["",""],"diff"]`},
		{NameBatch, map[string]string{
			"compilation":     "grader",
			"io_0_inputfile":  "input.txt",
			"io_1_outputfile": "output.txt",
			"output_eval":     "comparator",
		}, `["grader",["input.txt","output.txt"],"comparator"]`},
		{NameBatchAndOutput, map[string]string{
			"compilation":           "alone",
			"io_0_inputfile":        "",
			"io_1_outputfile":       "",
			"output_eval":           "comparator",
			"output_only_testcases": "000,001",
		}, `["alone",["",""],"comparator","000,001"]`},
		{NameCommunication, map[string]string{
			"num_processes": " 2 ",
			"compilation":   "grader",
			"user_io":       "fifo_io",
		}, `[2,"grader","fifo_io"]`},
		{NameInteractive, map[string]string{
			"compilation":             "grader",
			"process_limit":           "200",
			"concurrent":              "true",
			"controller_time_limit":   "1.0",
			"controller_wall_limit":   "5.0",
			"controller_memory_limit": "128.5",
		}, `[200,"grader",true,128.5,1,5]`},
		{NameInteractive, map[string]string{ // an unchecked checkbox
			"compilation":             "alone",
			"process_limit":           "1",
			"controller_time_limit":   "1",
			"controller_wall_limit":   "5",
			"controller_memory_limit": "128",
		}, `[1,"alone",false,128,1,5]`},
		{NameOutputOnly, map[string]string{"output_eval": "real"}, `["real"]`},
		{NameTwoSteps, map[string]string{"output_eval": "diff"}, `["diff"]`},
	}
	for _, c := range cases {
		prefixed := map[string]string{}
		prefix := "TaskTypeOptions_7_" + c.name + "_"
		for k, v := range c.fields {
			prefixed[prefix+k] = v
		}
		got, err := ParseForm(c.name, form(prefixed), prefix)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if string(got) != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
		if _, err := Get(c.name, got); err != nil {
			t.Errorf("%s: the parsed parameters are invalid: %v", c.name, err)
		}
	}

	bad := []struct {
		name   string
		fields map[string]string
		want   string
	}{
		{NameBatch, map[string]string{"compilation": "alone", "io_0_inputfile": "", "output_eval": "diff"},
			"Invalid parameter I/O (blank for stdin/stdout): missing argument io_1_outputfile."},
		{NameBatch, map[string]string{"compilation": "both", "io_0_inputfile": "", "io_1_outputfile": "", "output_eval": "diff"},
			"Invalid parameter Compilation: Value both doesn't match any allowed choice.."},
		{NameCommunication, map[string]string{"num_processes": "two", "compilation": "alone", "user_io": "std_io"},
			"Invalid parameter Number of Processes: invalid literal for int() with base 10"},
		{NameCommunication, map[string]string{"num_processes": "0", "compilation": "alone", "user_io": "std_io"},
			"the number of processes must be between 1 and 1000"},
		{NameInteractive, map[string]string{"compilation": "alone", "process_limit": "1",
			"controller_time_limit": "nan", "controller_wall_limit": "5", "controller_memory_limit": "128"},
			"Invalid parameter Controller time limit (s): could not convert string to float"},
	}
	for _, c := range bad {
		_, err := ParseForm(c.name, form(c.fields), "")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s %v: error %v, want %q", c.name, c.fields, err, c.want)
		}
	}
	if _, err := ParseForm("Nope", form(nil), ""); !errors.Is(err, ErrUnknown) {
		t.Errorf("unknown task type: %v", err)
	}
}

func TestParamDescriptions(t *testing.T) {
	// Every parameter has a name and a short name, and the short names
	// of a task type are distinct (they name the form fields).
	for _, name := range Names() {
		ps, err := Params(name)
		if err != nil {
			t.Fatal(err)
		}
		var walk func(prefix string, ps []Param)
		seen := map[string]bool{}
		walk = func(prefix string, ps []Param) {
			for _, p := range ps {
				if p.Name == "" || p.ShortName == "" || strings.ContainsAny(p.ShortName, " \t") {
					t.Errorf("%s: parameter %+v", name, p)
				}
				if seen[prefix+p.ShortName] {
					t.Errorf("%s: duplicate short name %s", name, p.ShortName)
				}
				seen[prefix+p.ShortName] = true
				if p.Kind == ParamChoice && len(p.Choices) == 0 {
					t.Errorf("%s: choice %s without choices", name, p.Name)
				}
				if p.Kind == ParamCollection {
					walk(prefix+p.ShortName+"/", p.Params)
				}
			}
		}
		walk("", ps)
	}
	// The order of the choices of output_eval: CMS's first.
	ps, _ := Params(NameOutputOnly)
	var values []string
	for _, c := range ps[0].Choices {
		values = append(values, c.Value)
	}
	if !slices.Equal(values, []string{"diff", "comparator", "exact", "real", "testlib"}) {
		t.Errorf("output_eval choices %v", values)
	}
}

func mustGet(t *testing.T, name, params string) TaskType {
	t.Helper()
	tt, err := Get(name, json.RawMessage(params))
	if err != nil {
		t.Fatal(err)
	}
	return tt
}

func TestTaskTypeProperties(t *testing.T) {
	cases := []struct {
		name, params             string
		display                  string
		testable, partial, reuse bool
		user, auto, managers     []string
		boxes                    int
	}{
		{NameBatch, `["alone", ["", ""], "diff"]`, "Batch", true, false, true,
			[]string{}, []string{}, nil, 1},
		{NameBatch, `["grader", ["", ""], "comparator"]`, "Batch", true, false, true,
			[]string{"grader.%l"}, []string{}, []string{"grader.%l", "checker"}, 2},
		{NameBatch, `["alone", ["", ""], "testlib"]`, "Batch", true, false, true,
			[]string{}, []string{}, []string{"checker"}, 2},
		{NameBatchAndOutput, `["alone", ["", ""], "comparator", "000"]`, "BatchAndOutput", true, true, false,
			[]string{}, []string{}, []string{"checker"}, 2},
		{NameOutputOnly, `["diff"]`, "Output only", false, true, true,
			[]string{}, []string{}, nil, 0},
		{NameOutputOnly, `["comparator"]`, "Output only", false, true, true,
			[]string{}, []string{}, []string{"checker"}, 1},
		{NameCommunication, `[3, "alone", "std_io"]`, "Communication", true, false, true,
			[]string{}, []string{"manager"}, []string{"manager"}, 4},
		{NameCommunication, `[1, "grader", "fifo_io"]`, "Communication", true, false, true,
			[]string{"grader.%l"}, []string{"manager"}, []string{"manager", "grader.%l"}, 2},
		{NameInteractive, `[200, "grader", true, 128.0, 1.0, 5.0]`, "Interactive", true, false, true,
			[]string{"grader.%l"}, []string{"controller"}, []string{"controller", "grader.%l"}, 201},
		{NameTwoSteps, `["diff"]`, "Two steps", true, false, true,
			[]string{"manager.%l"}, []string{}, []string{"manager.%l"}, 2},
		{NameTwoSteps, `["comparator"]`, "Two steps", true, false, true,
			[]string{"manager.%l"}, []string{}, []string{"manager.%l", "checker"}, 2},
	}
	for _, c := range cases {
		tt := mustGet(t, c.name, c.params)
		what := c.name + " " + c.params
		if tt.DisplayName() != c.display || tt.Testable() != c.testable ||
			tt.AllowPartialSubmission() != c.partial || tt.ReusePreviousSubmission() != c.reuse {
			t.Errorf("%s: %q testable %v partial %v reuse %v", what, tt.DisplayName(), tt.Testable(),
				tt.AllowPartialSubmission(), tt.ReusePreviousSubmission())
		}
		if !slices.Equal(tt.UserManagers(), c.user) || tt.UserManagers() == nil {
			t.Errorf("%s: user managers %q", what, tt.UserManagers())
		}
		if !slices.Equal(tt.AutoManagers(), c.auto) || tt.AutoManagers() == nil {
			t.Errorf("%s: auto managers %q", what, tt.AutoManagers())
		}
		if !slices.Equal(tt.Managers(), c.managers) {
			t.Errorf("%s: managers %q, want %q", what, tt.Managers(), c.managers)
		}
		if tt.Boxes() != c.boxes {
			t.Errorf("%s: %d boxes, want %d", what, tt.Boxes(), c.boxes)
		}
	}
}

// lastCommand returns the last compilation command of a language.
func lastCommand(t *testing.T, cc map[string][][]string, lang string) []string {
	t.Helper()
	cmds, ok := cc[lang]
	if !ok || len(cmds) == 0 {
		t.Fatalf("no commands for %s in %v", lang, cc)
	}
	return cmds[len(cmds)-1]
}

func TestCompilationCommands(t *testing.T) {
	langs := language.Builtin()
	const c = "C11 / gcc"
	gcc := func(exe string, sources ...string) []string {
		return append([]string{"/usr/bin/gcc", "-DEVAL", "-std=gnu11", "-O2", "-pipe", "-static", "-s", "-o", exe},
			append(sources, "-lm")...)
	}
	cases := []struct {
		name, params string
		format       []string
		want         []string
	}{
		// upstream BatchTest.TestGetCompilationCommands
		{NameBatch, `["alone", ["", ""], "diff"]`, []string{"foo.%l"}, gcc("foo", "foo.c")},
		{NameBatch, `["grader", ["", ""], "diff"]`, []string{"foo.%l"}, gcc("foo", "grader.c", "foo.c")},
		{NameBatch, `["alone", ["", ""], "diff"]`, []string{"foo.%l", "bar.%l"}, gcc("bar_foo", "foo.c", "bar.c")},
		{NameBatch, `["grader", ["", ""], "diff"]`, []string{"foo.%l", "bar.%l"}, gcc("bar_foo", "grader.c", "foo.c", "bar.c")},
		// The outputs of BatchAndOutput are not compiled.
		{NameBatchAndOutput, `["alone", ["", ""], "diff", ""]`, []string{"batch_and_output.%l", "output_000.txt"},
			gcc("batch_and_output", "batch_and_output.c")},
		{NameCommunication, `[2, "grader", "std_io"]`, []string{"user1.%l", "user2.%l"},
			gcc("user1_user2", "grader.c", "user1.c", "user2.c")},
		// upstream InteractiveTest
		{NameInteractive, `[200, "grader", true, 128.0, 1.0, 5.0]`, []string{"foo.%l"}, gcc("foo", "grader.c", "foo.c")},
		{NameInteractive, `[200, "alone", true, 128.0, 1.0, 5.0]`, []string{"foo.%l"}, gcc("foo", "foo.c")},
		{NameTwoSteps, `["diff"]`, []string{"first.%l", "second.%l"},
			gcc("manager", "manager.c", "manager.h", "first.c", "first.h", "second.c", "second.h")},
	}
	for _, tc := range cases {
		cc := mustGet(t, tc.name, tc.params).CompilationCommands(langs, tc.format)
		if got := lastCommand(t, cc, c); !slices.Equal(got, tc.want) {
			t.Errorf("%s %s %v:\n got %q\nwant %q", tc.name, tc.params, tc.format, got, tc.want)
		}
		if len(cc) != len(langs.All()) {
			t.Errorf("%s: commands for %d languages, want %d", tc.name, len(cc), len(langs.All()))
		}
	}
	// Executable names with extensions.
	cc := mustGet(t, NameBatch, `["alone", ["", ""], "diff"]`).CompilationCommands(langs, []string{"foo.%l"})
	if got := strings.Join(lastCommand(t, cc, "Java / JDK"), " "); !strings.Contains(got, "foo.jar") {
		t.Errorf("Java: %s", got)
	}
	if cc := mustGet(t, NameOutputOnly, `["diff"]`).CompilationCommands(langs, []string{"output_000.txt"}); cc != nil {
		t.Errorf("OutputOnly compiles: %v", cc)
	}
}
