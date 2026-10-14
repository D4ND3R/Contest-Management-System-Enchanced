// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package pkg

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/tasktype"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
)

const pdf = "%PDF-1.4\nstatement\n"

// batchPackage is a Batch task with every part of the format.
func batchPackage() map[string]string {
	return with(tests(5), map[string]string{
		"problem.yaml": `name: sum
title: A plus B
time_limit: 1.5
memory_limit: 256
input: input.txt
output: output.txt
checker: comparator
scoring:
  type: GroupMin
  subtasks:
    - points: 30
      tests: 01-02
    - points: 70
      tests: [3, "04", 05]
public_tests: 1, 3
primary_statements: [en]
feedback_level: full
score_mode: max_subtask
score_precision: 2
max_submissions: 50
min_submission_interval: 30
min_user_test_interval: 0.5
languages: [C11 / gcc, C++20 / g++]
tokens:
  mode: finite
  max_number: 5
  gen_interval: 60
`,
		"statement/en.pdf":            pdf,
		"statement/spanish.md":        "# Suma\n",
		"statement/.DS_Store":         "x",
		"attachments/sample.zip":      "zip",
		"graders/grader.cpp":          "grader cpp",
		"graders/grader.c":            "grader c",
		"graders/sum.h":               "header",
		"graders/checker*":            "#!/bin/sh\necho 1\n",
		"graders/checker.cpp":         "kept for reference",
		"graders/testlib.h":           "testlib",
		"solutions/correct.ac.cpp":    "ok",
		"solutions/slow.tle.c":        "slow",
		"solutions/half.pa.cpp":       "half",
		"solutions/wrong.WA.c":        "wrong",
		"solutions/.gitkeep":          "",
		"tests/.gitkeep":              "",
		"tests/README.md":             "ignored",
		"attachments/.hidden":         "ignored",
		"graders/.gitignore":          "ignored",
		"statement/en-notes.txt":      "ignored",
		"solutions/correct.ac.cpp.sw": "",
	})
}

func TestLoadBatch(t *testing.T) {
	files := batchPackage()
	delete(files, "solutions/correct.ac.cpp.sw")
	delete(files, "tests/README.md")
	delete(files, "statement/en-notes.txt")
	dir := pkgDir(t, files)
	pk, rep := load(t, dir)
	tk := pk.Task
	if tk == nil {
		t.Fatalf("no task:\n%s", issues(rep))
	}
	importer.ValidateTask(tk, nil, rep)
	noIssues(t, rep)

	want := importer.NewTask("sum", "A plus B")
	want.SubmissionFormat = []string{"sum.%l"}
	want.PrimaryStatements = []string{"en"}
	want.AllowedLanguages = []string{"C11 / gcc", "C++20 / g++"}
	want.Tokens = importer.Tokens{Mode: "finite", MaxNumber: importer.Ptr(int32(5)), GenInitial: 2, GenNumber: 2, GenInterval: time.Minute}
	want.MaxSubmissionNumber = importer.Ptr(int32(50))
	want.MinSubmissionInterval = importer.Ptr(30 * time.Second)
	want.MinUserTestInterval = importer.Ptr(500 * time.Millisecond)
	want.FeedbackLevel, want.ScoreMode, want.ScorePrecision = "full", "max_subtask", 2
	got := *tk
	got.Statements, got.Attachments, got.Datasets = nil, nil, nil
	if !reflect.DeepEqual(&got, want) {
		t.Errorf("task\n got %+v\nwant %+v", &got, want)
	}
	if s := statementNames(tk); !slices.Equal(s, []string{"en", "es"}) {
		t.Errorf("statements %v", s)
	}
	if c := content(t, tk.Statements[1].File); c != "# Suma\n" {
		t.Errorf("statement es: %q", c)
	}
	if a := attachmentNames(tk); !slices.Equal(a, []string{"sample.zip"}) {
		t.Errorf("attachments %v", a)
	}
	d := tk.Datasets[0]
	if d.Description != "Default" || d.Autojudge || *d.TimeLimit != 1.5 || *d.MemoryLimit != 256*importer.MiB {
		t.Errorf("dataset %+v", d)
	}
	if d.TaskType != tasktype.NameBatch || !sameJSON(t, d.TaskTypeParameters, `["grader", ["input.txt", "output.txt"], "comparator"]`) {
		t.Errorf("task type %s %s", d.TaskType, d.TaskTypeParameters)
	}
	if d.ScoreType != "GroupMin" || !sameJSON(t, d.ScoreTypeParameters, `[[30, ["01", "02"]], [70, ["03", "04", "05"]]]`) {
		t.Errorf("score type %s %s", d.ScoreType, d.ScoreTypeParameters)
	}
	if m := managerNames(d); !slices.Equal(m, []string{"checker", "grader.c", "grader.cpp", "sum.h"}) {
		t.Errorf("managers %v", m)
	}
	if tc := testcaseNames(d); !slices.Equal(tc, []string{"01*", "02", "03*", "04", "05"}) {
		t.Errorf("testcases %v", tc)
	}
	if in, out := content(t, d.Testcases[2].Input), content(t, d.Testcases[2].Output); in != "in 3\n" || out != "out 3\n" {
		t.Errorf("test 03: %q %q", in, out)
	}
	var sols []string
	for _, s := range pk.Solutions {
		sols = append(sols, s.Name+" "+s.Verdict+" "+s.Language)
	}
	wantSols := []string{
		"solutions/correct.ac.cpp ac C++11 / g++",
		"solutions/half.pa.cpp pa C++11 / g++",
		"solutions/slow.tle.c tle C11 / gcc",
		"solutions/wrong.WA.c wa C11 / gcc",
	}
	if !slices.Equal(sols, wantSols) {
		t.Errorf("solutions\n got %q\nwant %q", sols, wantSols)
	}
}

func TestLoadIgnoredFiles(t *testing.T) {
	_, rep := load(t, pkgDir(t, batchPackage()))
	wantIssue(t, rep, importer.SeverityWarning, "ignored: tests are <name>.in with <name>.out")
	wantIssue(t, rep, importer.SeverityWarning, "ignored: statements are statement/<language>.pdf or .md")
	wantIssue(t, rep, importer.SeverityWarning, `unknown expected verdict "cpp"`)
	if len(rep.Errors()) > 0 {
		t.Errorf("errors:\n%s", issues(rep))
	}
}

func TestLoadDefaults(t *testing.T) {
	dir := pkgDir(t, with(tests(3), map[string]string{
		"problem.yaml":           "name: t\ntitle: T\ntime_limit: 1\nmemory_limit: 64\n",
		"statement/en.pdf":       pdf,
		"solutions/main.ac.cpp":  "",
		"graders/notes.txt":      "a manager too",
		"graders/stub.py":        "stub",
		"attachments/a.txt":      "a",
		"attachments/b.out.txt":  "b",
		"tests/x/ignored":        "",
		"solutions/dir/ignored":  "",
		"solutions/main.ac.java": "",
	}))
	tk, rep := loadValid(t, dir)
	wantIssue(t, rep, importer.SeverityWarning, "directories in tests/ are ignored")
	wantIssue(t, rep, importer.SeverityWarning, "ignored: solutions are source files")
	d := tk.Datasets[0]
	if d.TaskType != "Batch" || !sameJSON(t, d.TaskTypeParameters, `["grader", ["", ""], "diff"]`) {
		t.Errorf("task type %s %s", d.TaskType, d.TaskTypeParameters)
	}
	if d.ScoreType != "Sum" || string(d.ScoreTypeParameters) != "33.333333333333336" {
		t.Errorf("score type %s %s", d.ScoreType, d.ScoreTypeParameters)
	}
	// stub.py is the grader of Python, any other file is a manager.
	if m := managerNames(d); !slices.Equal(m, []string{"grader.py", "notes.txt"}) {
		t.Errorf("managers %v", m)
	}
	if !slices.Equal(tk.PrimaryStatements, []string{"en"}) || !slices.Equal(tk.SubmissionFormat, []string{"t.%l"}) ||
		tk.AllowedLanguages != nil || tk.FeedbackLevel != "restricted" || tk.ScoreMode != "max_tokened_last" ||
		tk.Tokens.Mode != "disabled" {
		t.Errorf("task %+v", tk)
	}
	if tc := testcaseNames(d); !slices.Equal(tc, []string{"01", "02", "03"}) {
		t.Errorf("testcases %v", tc)
	}
}

func TestLoadBatchAndOutput(t *testing.T) {
	base := with(tests(4), map[string]string{
		"statement/en.pdf":         pdf,
		"solutions/main.ac.cpp":    "",
		"attachments/input_02.txt": "given by the package",
	})
	t.Run("implicit", func(t *testing.T) {
		dir := pkgDir(t, with(base, map[string]string{"problem.yaml": "name: bo\ntitle: BO\ntime_limit: 1\nmemory_limit: 64\n" +
			"output_only_tests: 4\noutput_tests: 1-2\n"}))
		tk, _ := loadValid(t, dir)
		d := tk.Datasets[0]
		if d.TaskType != "BatchAndOutput" || !sameJSON(t, d.TaskTypeParameters, `["alone", ["", ""], "diff", "04"]`) {
			t.Errorf("task type %s %s", d.TaskType, d.TaskTypeParameters)
		}
		if want := []string{"bo.%l", "output_01.txt", "output_02.txt", "output_04.txt"}; !slices.Equal(tk.SubmissionFormat, want) {
			t.Errorf("submission format %v", tk.SubmissionFormat)
		}
		if a := attachmentNames(tk); !slices.Equal(a, []string{"input_01.txt", "input_02.txt", "input_04.txt"}) {
			t.Errorf("attachments %v", a)
		}
		// The package's own attachment wins; the others are the inputs.
		if c := content(t, tk.Attachments[1].File); c != "given by the package" {
			t.Errorf("input_02.txt: %q", c)
		}
		if tk.Attachments[0].File != d.Testcases[0].Input {
			t.Errorf("input_01.txt is not the input of test 01")
		}
	})
	t.Run("no inputs attached", func(t *testing.T) {
		dir := pkgDir(t, with(base, map[string]string{"problem.yaml": "name: bo\ntitle: BO\ntime_limit: 1\nmemory_limit: 64\n" +
			"type: batch_and_output\noutput_only_tests: [1, 3]\nattach_inputs: false\n"}))
		tk, _ := loadValid(t, dir)
		d := tk.Datasets[0]
		if !sameJSON(t, d.TaskTypeParameters, `["alone", ["", ""], "diff", "01,03"]`) {
			t.Errorf("params %s", d.TaskTypeParameters)
		}
		if a := attachmentNames(tk); !slices.Equal(a, []string{"input_02.txt"}) {
			t.Errorf("attachments %v", a)
		}
	})
	t.Run("no output tests", func(t *testing.T) {
		dir := pkgDir(t, with(base, map[string]string{"problem.yaml": "name: bo\ntitle: BO\ntime_limit: 1\nmemory_limit: 64\ntype: BatchAndOutput\n"}))
		tk, _ := loadValid(t, dir)
		d := tk.Datasets[0]
		if d.TaskType != "BatchAndOutput" || !sameJSON(t, d.TaskTypeParameters, `["alone", ["", ""], "diff", ""]`) ||
			!slices.Equal(tk.SubmissionFormat, []string{"bo.%l"}) {
			t.Errorf("task type %s %s %v", d.TaskType, d.TaskTypeParameters, tk.SubmissionFormat)
		}
	})
}

func TestLoadOutputOnly(t *testing.T) {
	dir := pkgDir(t, with(tests(3), map[string]string{
		"problem.yaml":                    "name: oo\ntitle: OO\ntype: output_only\nchecker: {type: float, tolerance: 1e-6}\n",
		"statement/italian.pdf":           pdf,
		"solutions/full.ac/output_01.txt": "out 1\n",
		"solutions/half.pa.zip":           "zip",
		"solutions/none":                  "",
	}))
	pk, rep := load(t, dir)
	tk := pk.Task
	importer.ValidateTask(tk, nil, rep)
	if rep.HasErrors() {
		t.Fatalf("errors:\n%s", issues(rep))
	}
	wantIssue(t, rep, importer.SeverityWarning, "no expected verdict in the name")
	if hasIssue(rep, importer.SeverityWarning, "no solution is expected to be accepted") ||
		hasIssue(rep, importer.SeverityWarning, "no time limit") {
		t.Errorf("unexpected warnings:\n%s", issues(rep))
	}
	d := tk.Datasets[0]
	if d.TaskType != "OutputOnly" || !sameJSON(t, d.TaskTypeParameters, `["real"]`) || d.TimeLimit != nil || d.MemoryLimit != nil {
		t.Errorf("dataset %s %s %v %v", d.TaskType, d.TaskTypeParameters, d.TimeLimit, d.MemoryLimit)
	}
	if want := []string{"output_01.txt", "output_02.txt", "output_03.txt"}; !slices.Equal(tk.SubmissionFormat, want) {
		t.Errorf("submission format %v", tk.SubmissionFormat)
	}
	if a := attachmentNames(tk); !slices.Equal(a, []string{"input_01.txt", "input_02.txt", "input_03.txt"}) {
		t.Errorf("attachments %v", a)
	}
	if s := statementNames(tk); !slices.Equal(s, []string{"it"}) || !slices.Equal(tk.PrimaryStatements, []string{"it"}) {
		t.Errorf("statements %v %v", s, tk.PrimaryStatements)
	}
	var sols []string
	for _, s := range pk.Solutions {
		sols = append(sols, s.Name+" "+s.Verdict)
	}
	if want := []string{"solutions/half.pa.zip pa", "solutions/none ", "solutions/full.ac ac"}; !slices.Equal(sols, want) {
		t.Errorf("solutions %q", sols)
	}
}

func TestLoadCommunication(t *testing.T) {
	base := with(tests(2), map[string]string{
		"statement/en.pdf":      pdf,
		"solutions/main.ac.cpp": "",
		"graders/manager*":      "\x7fELF manager",
		"graders/manager.cpp":   "source, not compiled: the binary is there",
	})
	for _, c := range []struct {
		yaml, params string
		graders      bool
	}{
		{"", `[1, "alone", "std_io"]`, false},
		{"processes: 2\n", `[2, "grader", "fifo_io"]`, true},
		{"processes: 2\nuser_io: std_io\n", `[2, "grader", "std_io"]`, true},
		{"grader: false\n", `[1, "alone", "std_io"]`, false},
	} {
		files := with(base, map[string]string{"problem.yaml": "name: c\ntitle: C\ntype: communication\ntime_limit: 1\nmemory_limit: 64\n" + c.yaml})
		if c.graders {
			files["graders/grader.cpp"] = "grader"
			files["graders/grader.c"] = "grader"
		}
		tk, rep := loadValid(t, pkgDir(t, files))
		d := tk.Datasets[0]
		if d.TaskType != "Communication" || !sameJSON(t, d.TaskTypeParameters, c.params) {
			t.Errorf("%q: %s %s", c.yaml, d.TaskType, d.TaskTypeParameters)
		}
		if !slices.Contains(managerNames(d), "manager") || slices.Contains(managerNames(d), "manager.cpp") {
			t.Errorf("%q: managers %v", c.yaml, managerNames(d))
		}
		if hasIssue(rep, importer.SeverityWarning, "does not look like an executable") {
			t.Errorf("%q: %s", c.yaml, issues(rep))
		}
	}
}

func TestLoadInteractive(t *testing.T) {
	base := with(tests(1), map[string]string{
		"statement/en.pdf":      pdf,
		"solutions/main.ac.cpp": "",
		"graders/controller*":   "#!/usr/bin/env python3\n",
	})
	yaml := "name: i\ntitle: I\ntype: interactive\ntime_limit: 1\nmemory_limit: 64\n"
	tk, _ := loadValid(t, pkgDir(t, with(base, map[string]string{"problem.yaml": yaml +
		"process_limit: 3\nconcurrent: false\ncontroller_memory_limit: 256\ncontroller_time_limit: 10\ncontroller_wall_time_limit: 20.5\n"})))
	d := tk.Datasets[0]
	if d.TaskType != "Interactive" || !sameJSON(t, d.TaskTypeParameters, `[3, "alone", false, 256, 10, 20.5]`) {
		t.Errorf("%s %s", d.TaskType, d.TaskTypeParameters)
	}
	if m := managerNames(d); !slices.Equal(m, []string{"controller"}) {
		t.Errorf("managers %v", m)
	}

	pk, rep := load(t, pkgDir(t, with(base, map[string]string{"problem.yaml": yaml + "controller_time_limit: 10\n"})))
	wantIssue(t, rep, importer.SeverityError, "type interactive needs controller_memory_limit, controller_wall_time_limit")
	if pk.Task == nil {
		t.Fatal("no task")
	}
}

func TestLoadTwoSteps(t *testing.T) {
	dir := pkgDir(t, with(tests(2), map[string]string{
		"problem.yaml":          "name: ts\ntitle: TS\ntype: two_steps\ntime_limit: 1\nmemory_limit: 64\nsubmission_format: [encoder.%l, decoder.%l]\n",
		"statement/en.pdf":      pdf,
		"solutions/main.ac.cpp": "",
		"graders/manager.cpp":   "manager",
		"graders/manager.c":     "manager",
		"graders/manager.h":     "header",
		"graders/encoder.h":     "header",
	}))
	tk, _ := loadValid(t, dir)
	d := tk.Datasets[0]
	if d.TaskType != "TwoSteps" || !sameJSON(t, d.TaskTypeParameters, `["diff"]`) {
		t.Errorf("%s %s", d.TaskType, d.TaskTypeParameters)
	}
	if !slices.Equal(tk.SubmissionFormat, []string{"encoder.%l", "decoder.%l"}) {
		t.Errorf("submission format %v", tk.SubmissionFormat)
	}
	if m := managerNames(d); !slices.Equal(m, []string{"encoder.h", "manager.c", "manager.cpp", "manager.h"}) {
		t.Errorf("managers %v", m)
	}

	_, rep := load(t, pkgDir(t, with(tests(1), map[string]string{"problem.yaml": "name: ts\ntitle: TS\ntype: TwoSteps\n"})))
	wantIssue(t, rep, importer.SeverityError, "submission_format is needed with type two_steps")
}

func TestLoadCheckers(t *testing.T) {
	base := with(tests(1), map[string]string{"statement/en.pdf": pdf, "solutions/main.ac.cpp": ""})
	yaml := "name: c\ntitle: C\ntime_limit: 1\nmemory_limit: 64\n"
	for _, c := range []struct{ checker, eval string }{
		{"", "diff"}, {"whitediff", "diff"}, {"white_diff", "diff"}, {"diff", "diff"}, {"exact", "exact"},
		{"float", "real"}, {"real", "real"}, {"{type: float, tolerance: 0.000001}", "real"},
	} {
		y := yaml
		if c.checker != "" {
			y += "checker: " + c.checker + "\n"
		}
		tk, _ := loadValid(t, pkgDir(t, with(base, map[string]string{"problem.yaml": y})))
		if p := tk.Datasets[0].TaskTypeParameters; !sameJSON(t, p, `["alone", ["", ""], "`+c.eval+`"]`) {
			t.Errorf("checker %q: %s", c.checker, p)
		}
	}
	for _, c := range []struct{ checker, eval string }{{"comparator", "comparator"}, {"Testlib", "testlib"}} {
		tk, _ := loadValid(t, pkgDir(t, with(base, map[string]string{
			"problem.yaml":     yaml + "checker: " + c.checker + "\n",
			"graders/checker*": "\x7fELF",
		})))
		if p := tk.Datasets[0].TaskTypeParameters; !sameJSON(t, p, `["alone", ["", ""], "`+c.eval+`"]`) {
			t.Errorf("checker %q: %s", c.checker, p)
		}
	}
}

func TestLoadScoring(t *testing.T) {
	base := with(tests(5), map[string]string{"statement/en.pdf": pdf, "solutions/main.ac.cpp": ""})
	yaml := "name: s\ntitle: S\ntime_limit: 1\nmemory_limit: 64\n"
	for _, c := range []struct {
		name, scoring, typ, params string
		warning                    string
	}{
		{"sum points", "points: 50", "Sum", `10.0`, ""},
		{"sum per test", "per_test: 7", "Sum", `7`, ""},
		{"sum type", "type: sum\n  per_test: 2.5", "Sum", `2.5`, ""},
		{"regex", "type: GroupMin\n  subtasks:\n    - {points: 40, regex: \"0[12]\"}\n    - {points: 60, regex: \"0[345]\"}",
			"GroupMin", `[[40, "0[12]"], [60, "0[345]"]]`, ""},
		{"count", "type: GroupMul\n  subtasks:\n    - {points: 40, count: 2}\n    - {points: 60, count: 3}",
			"GroupMul", `[[40, 2], [60, 3]]`, ""},
		{"threshold", "type: groupthreshold\n  subtasks:\n    - {points: 40, tests: 1-2, threshold: 0.5}\n    - {points: 60.0, tests: 3-5, threshold: 1}",
			"GroupThreshold", `[[40, ["01", "02"], 0.5], [60.0, ["03", "04", "05"], 1]]`, ""},
		{"always show", "type: GroupMin\n  subtasks:\n    - {points: 40, tests: 1-2, always_show_tests: true}\n    - {points: 60, tests: 3-5}",
			"GroupMin", `[{"max_score": 40, "testcases": ["01", "02"], "always_show_testcases": true}, [60, ["03", "04", "05"]]]`, ""},
		{"threshold object", "type: GroupThreshold\n  subtasks:\n    - {points: 100, tests: all, threshold: 2, always_show_tests: false}",
			"GroupThreshold", `[{"max_score": 100, "testcases": ["01", "02", "03", "04", "05"], "threshold": 2, "always_show_testcases": false}]`, ""},
		{"not 100", "type: GroupMin\n  subtasks:\n    - {points: 40, tests: 1-2}\n    - {points: 50, tests: 3-5}",
			"GroupMin", `[[40, ["01", "02"]], [50, ["03", "04", "05"]]]`, "add up to 90, not 100"},
		{"total given", "type: GroupMin\n  points: 90\n  subtasks:\n    - {points: 40, tests: 1-2}\n    - {points: 50, tests: 3-5}",
			"GroupMin", `[[40, ["01", "02"]], [50, ["03", "04", "05"]]]`, ""},
		{"uncovered", "type: GroupMin\n  subtasks:\n    - {points: 40, tests: 1-2}\n    - {points: 60, tests: 4}",
			"GroupMin", `[[40, ["01", "02"]], [60, ["04"]]]`, "tests in no subtask (they do not count): 03, 05"},
		{"overlap", "type: GroupMin\n  subtasks:\n    - {points: 40, tests: 1-3}\n    - {points: 60, tests: 3-5}",
			"GroupMin", `[[40, ["01", "02", "03"]], [60, ["03", "04", "05"]]]`, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			pk, rep := load(t, pkgDir(t, with(base, map[string]string{"problem.yaml": yaml + "scoring:\n  " + c.scoring + "\n"})))
			if pk.Task == nil {
				t.Fatalf("no task:\n%s", issues(rep))
			}
			importer.ValidateTask(pk.Task, nil, rep)
			if rep.HasErrors() {
				t.Fatalf("errors:\n%s", issues(rep))
			}
			d := pk.Task.Datasets[0]
			if d.ScoreType != c.typ || !sameJSON(t, d.ScoreTypeParameters, c.params) {
				t.Errorf("%s %s, want %s %s", d.ScoreType, d.ScoreTypeParameters, c.typ, c.params)
			}
			if c.warning != "" {
				wantIssue(t, rep, importer.SeverityWarning, c.warning)
			} else {
				noIssues(t, rep)
			}
		})
	}
	// Numbers keep their type in the parameters, as Python writes them:
	// 40 stays 40, 60.0 stays 60.0; the share of each test of a Sum is a
	// float.
	for _, c := range []struct{ scoring, params string }{
		{"type: GroupMin\n  subtasks:\n    - {points: 40, count: 2}\n    - {points: 60.0, count: 3}", `[[40,2],[60.0,3]]`},
		{"type: GroupThreshold\n  subtasks:\n    - {points: 1e2, count: 5, threshold: 0.00001}", `[[100.0,5,1e-05]]`},
		{"points: 50", `10.0`},
		{"per_test: 3", `3`},
		{"per_test: 3.0", `3.0`},
	} {
		pk, _ := load(t, pkgDir(t, with(base, map[string]string{"problem.yaml": yaml + "scoring:\n  " + c.scoring + "\n"})))
		if p := string(pk.Task.Datasets[0].ScoreTypeParameters); p != c.params {
			t.Errorf("%q: parameters %s, want %s", c.scoring, p, c.params)
		}
	}
}

func TestLoadScoringErrors(t *testing.T) {
	base := with(tests(5), map[string]string{"statement/en.pdf": pdf, "solutions/main.ac.cpp": ""})
	yaml := "name: s\ntitle: S\ntime_limit: 1\nmemory_limit: 64\nscoring:\n  "
	for _, c := range []struct{ scoring, err string }{
		{"type: Max", `scoring: unknown type "Max"`},
		{"points: 10\n  per_test: 2", "give points (the total) or per_test, not both"},
		{"subtasks:\n    - {points: 100, tests: all}", "subtasks need a group type"},
		{"type: GroupMin", "type GroupMin needs subtasks"},
		{"type: GroupMin\n  per_test: 3\n  subtasks:\n    - {points: 100, tests: all}", "per_test is for type Sum"},
		{"type: GroupMin\n  subtasks:\n    - {tests: all}", "scoring.subtasks[0]: points is missing"},
		{"type: GroupMin\n  subtasks:\n    - {points: 100}", "give exactly one of tests, regex and count"},
		{"type: GroupMin\n  subtasks:\n    - {points: 100, tests: all, count: 5}", "give exactly one of tests, regex and count"},
		{"type: GroupMin\n  subtasks:\n    - {points: 50, tests: 1-2}\n    - {points: 50, count: 3}", "every subtask must use the same of tests, regex and count"},
		{"type: GroupMin\n  subtasks:\n    - {points: 100, tests: 01-09}", "scoring.subtasks[0].tests: there is no test 6"},
		{"type: GroupMin\n  subtasks:\n    - {points: 100, tests: 5-3}", `invalid range "5-3"`},
		{"type: GroupMin\n  subtasks:\n    - {points: 100, tests: big}", `there is no test "big"`},
		{"type: GroupMin\n  subtasks:\n    - {points: 50, count: 2}\n    - {points: 50, count: 4}", "scoring.subtasks[1].count: 4 tests from test 3, but there are 5 tests"},
		{"type: GroupThreshold\n  subtasks:\n    - {points: 100, tests: all}", "threshold is missing (type GroupThreshold)"},
		{"type: GroupMin\n  subtasks:\n    - {points: 100, tests: all, threshold: 1}", "threshold is for type GroupThreshold"},
		{"type: GroupMin\n  points: 100\n  subtasks:\n    - {points: 40, tests: 1-2}\n    - {points: 50, tests: 3-5}", "add up to 90, not 100"},
		{"type: GroupMin\n  subtasks:\n    - {points: 100, regex: \"(?=0)\"}", "invalid parameters"},
	} {
		pk, rep := load(t, pkgDir(t, with(base, map[string]string{"problem.yaml": yaml + c.scoring + "\n"})))
		if pk.Task != nil {
			importer.ValidateTask(pk.Task, nil, rep)
		}
		if !hasIssue(rep, importer.SeverityError, c.err) {
			t.Errorf("%q: no error %q:\n%s", c.scoring, c.err, issues(rep))
		}
	}
}

func TestLoadPublicTests(t *testing.T) {
	base := with(tests(3), map[string]string{
		"statement/en.pdf": pdf, "solutions/main.ac.cpp": "",
		"tests/sample.in": "s", "tests/sample.out": "s",
		"tests/10.in": "10", "tests/10.out": "10",
	})
	yaml := "name: p\ntitle: P\ntime_limit: 1\nmemory_limit: 64\n"
	for _, c := range []struct {
		public string
		want   []string
	}{
		{"all", []string{"01*", "02*", "03*", "10*", "sample*"}},
		{"sample", []string{"01", "02", "03", "10", "sample*"}},
		{"sample, 2-3", []string{"01", "02*", "03*", "10", "sample*"}},
		{"[10, 1]", []string{"01*", "02", "03", "10*", "sample"}},
		{"''", []string{"01", "02", "03", "10", "sample"}},
	} {
		tk, _ := loadValid(t, pkgDir(t, with(base, map[string]string{"problem.yaml": yaml + "public_tests: " + c.public + "\n"})))
		if got := testcaseNames(tk.Datasets[0]); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.public, got, c.want)
		}
	}
	_, rep := load(t, pkgDir(t, with(base, map[string]string{"problem.yaml": yaml + "public_tests: 4-5, nope\n"})))
	wantIssue(t, rep, importer.SeverityError, "public_tests: there is no test 4")
	wantIssue(t, rep, importer.SeverityError, `public_tests: there is no test "nope"`)

	// 1 is ambiguous with tests 1 and 01: it needs the name.
	_, rep = load(t, pkgDir(t, with(base, map[string]string{"problem.yaml": yaml + "public_tests: 1\n", "tests/001.in": "", "tests/001.out": ""})))
	wantIssue(t, rep, importer.SeverityError, "test 1 is ambiguous (001, 01): use its name")
}

// TestValidationReport checks the problems the report lists.
func TestValidationReport(t *testing.T) {
	ok := with(tests(2), map[string]string{
		"problem.yaml":          "name: v\ntitle: V\ntime_limit: 1\nmemory_limit: 64\n",
		"statement/en.pdf":      pdf,
		"solutions/main.ac.cpp": "",
	})
	cases := []struct {
		name   string
		change map[string]string
		remove []string
		sev    importer.Severity
		text   string
	}{
		{"missing statement", nil, []string{"statement/en.pdf"}, importer.SeverityWarning, "the task has no statement"},
		{"no tests", nil, []string{"tests/01.in", "tests/01.out", "tests/02.in", "tests/02.out"}, importer.SeverityError, "the package has no tests"},
		{"test without output", nil, []string{"tests/02.out"}, importer.SeverityError, "test 02 has no output (tests/02.out)"},
		{"output without input", nil, []string{"tests/02.in"}, importer.SeverityError, "test 02 has no input (tests/02.in)"},
		{"invalid test name", map[string]string{"tests/a b.in": "", "tests/a b.out": ""}, nil, importer.SeverityError, `invalid test name "a b"`},
		{"no accepted solution", map[string]string{"solutions/main.wa.cpp": ""}, []string{"solutions/main.ac.cpp"}, importer.SeverityWarning,
			"no solution is expected to be accepted (solutions/<name>.ac.<extension>)"},
		{"no solutions", nil, []string{"solutions/main.ac.cpp"}, importer.SeverityWarning, "the package has no solutions to check it with"},
		{"unknown verdict", map[string]string{"solutions/x.ok.cpp": ""}, nil, importer.SeverityWarning, `unknown expected verdict "ok"`},
		{"no verdict", map[string]string{"solutions/x.cpp": ""}, nil, importer.SeverityWarning, "no expected verdict in the name (e.g. x.ac.cpp"},
		{"unknown language", map[string]string{"solutions/x.ac.txt": ""}, nil, importer.SeverityWarning, `unknown language of ".txt"`},
		{"language not allowed", map[string]string{"problem.yaml": "name: v\ntitle: V\ntime_limit: 1\nmemory_limit: 64\nlanguages: [C11 / gcc]\n",
			"solutions/x.ac.c": ""}, nil, importer.SeverityWarning, "C++11 / g++ is not among the languages of the task"},
		{"unknown allowed language", map[string]string{"problem.yaml": "name: v\ntitle: V\ntime_limit: 1\nmemory_limit: 64\nlanguages: [C99]\n"},
			nil, importer.SeverityError, `unknown allowed language "C99"`},
		{"comparator missing", map[string]string{"problem.yaml": "name: v\ntitle: V\ntime_limit: 1\nmemory_limit: 64\nchecker: comparator\n"},
			nil, importer.SeverityError, "checker: comparator needs graders/checker (an executable) or its C++ source graders/checker.cpp"},
		{"testlib missing", map[string]string{"problem.yaml": "name: v\ntitle: V\ntime_limit: 1\nmemory_limit: 64\nchecker: testlib\n"},
			nil, importer.SeverityError, `task type Batch needs the manager "checker"`},
		{"checker not C++", map[string]string{"problem.yaml": "name: v\ntitle: V\ntime_limit: 1\nmemory_limit: 64\nchecker: testlib\n",
			"graders/checker.py": ""}, nil, importer.SeverityError, "graders/checker.py: only C++ sources are compiled at import"},
		{"manager missing", map[string]string{"problem.yaml": "name: v\ntitle: V\ntime_limit: 1\nmemory_limit: 64\ntype: communication\n"},
			nil, importer.SeverityError, "type communication needs graders/manager"},
		{"controller missing", map[string]string{"problem.yaml": "name: v\ntitle: V\ntime_limit: 1\nmemory_limit: 64\ntype: interactive\n" +
			"controller_memory_limit: 1\ncontroller_time_limit: 1\ncontroller_wall_time_limit: 1\n"},
			nil, importer.SeverityError, "type interactive needs graders/controller"},
		{"grader missing", map[string]string{"problem.yaml": "name: v\ntitle: V\ntime_limit: 1\nmemory_limit: 64\ngrader: true\n"},
			nil, importer.SeverityError, "task type Batch needs grader.%l"},
		{"checker unused", map[string]string{"graders/checker*": "\x7fELF"}, nil, importer.SeverityWarning,
			"graders/checker is stored but not used: set checker: comparator (or testlib) to use it"},
		{"checker not executable", map[string]string{"problem.yaml": "name: v\ntitle: V\ntime_limit: 1\nmemory_limit: 64\nchecker: comparator\n",
			"graders/checker": "int main() {}"}, nil, importer.SeverityWarning, "graders/checker does not look like an executable"},
		{"grader false", map[string]string{"problem.yaml": "name: v\ntitle: V\ntime_limit: 1\nmemory_limit: 64\ngrader: false\n",
			"graders/grader.cpp": ""}, nil, importer.SeverityWarning, "grader is false: grader.cpp are stored but not used"},
		{"grader extension", map[string]string{"graders/grader.cc": ""}, nil, importer.SeverityWarning, "graders/grader.cc is never used"},
		{"stub and grader", map[string]string{"graders/grader.cpp": "", "graders/stub.cpp": ""}, nil, importer.SeverityError,
			"both stub.cpp and grader.cpp: keep one"},
		{"unknown key", map[string]string{"problem.yaml": "name: v\ntitle: V\ntime_limit: 1\nmemory_limit: 64\nchecker: comparator\nsubtasks: []\n"},
			nil, importer.SeverityError, "field subtasks not found"},
		{"key of another type", map[string]string{"problem.yaml": "name: v\ntitle: V\ntime_limit: 1\nmemory_limit: 64\nprocesses: 2\nuser_io: std_io\n"},
			nil, importer.SeverityWarning, "processes does not apply to type batch: ignored"},
		{"unknown type", map[string]string{"problem.yaml": "name: v\ntitle: V\ntype: batchy\n"}, nil, importer.SeverityError, `unknown type "batchy"`},
		{"unknown checker", map[string]string{"problem.yaml": "name: v\ntitle: V\ntime_limit: 1\nmemory_limit: 64\nchecker: lines\n"},
			nil, importer.SeverityError, `unknown checker "lines"`},
		{"tolerance", map[string]string{"problem.yaml": "name: v\ntitle: V\ntime_limit: 1\nmemory_limit: 64\nchecker: {type: float, tolerance: 0.001}\n"},
			nil, importer.SeverityError, "the tolerance can only be 1e-06, with type float"},
		{"checker of communication", map[string]string{"problem.yaml": "name: v\ntitle: V\ntime_limit: 1\nmemory_limit: 64\ntype: communication\nchecker: exact\n",
			"graders/manager*": "\x7fELF"}, nil, importer.SeverityError, "checker does not apply to type communication: its manager decides the outcome"},
		{"output tests of output only", map[string]string{"problem.yaml": "name: v\ntitle: V\ntype: output_only\noutput_tests: 1\n"},
			nil, importer.SeverityError, "output_only_tests and output_tests need type batch_and_output"},
		{"no name", map[string]string{"problem.yaml": "title: V\n"}, nil, importer.SeverityError, "name is missing"},
		{"no title", map[string]string{"problem.yaml": "name: v\ntitle: ' '\n"}, nil, importer.SeverityError, "title is missing"},
		{"invalid name", map[string]string{"problem.yaml": "name: a/b\ntitle: V\ntime_limit: 1\nmemory_limit: 64\n"}, nil, importer.SeverityError, `invalid task name "a/b"`},
		{"memory limit", map[string]string{"problem.yaml": "name: v\ntitle: V\ntime_limit: 1\nmemory_limit: 0\n"}, nil, importer.SeverityError, "memory_limit: 0 MiB is not a valid limit"},
		{"time limit", map[string]string{"problem.yaml": "name: v\ntitle: V\ntime_limit: -1\nmemory_limit: 64\n"}, nil, importer.SeverityError, "invalid time limit -1"},
		{"no limits", map[string]string{"problem.yaml": "name: v\ntitle: V\n"}, nil, importer.SeverityWarning, "no time limit"},
		{"negative interval", map[string]string{"problem.yaml": "name: v\ntitle: V\ntime_limit: 1\nmemory_limit: 64\nmin_submission_interval: -5\n"},
			nil, importer.SeverityError, "min_submission_interval: -5 is not a valid number of seconds"},
		{"token mode", map[string]string{"problem.yaml": "name: v\ntitle: V\ntime_limit: 1\nmemory_limit: 64\ntokens: {mode: some}\n"},
			nil, importer.SeverityError, "some"},
		{"feedback level", map[string]string{"problem.yaml": "name: v\ntitle: V\ntime_limit: 1\nmemory_limit: 64\nfeedback_level: all\n"},
			nil, importer.SeverityError, `invalid feedback level "all"`},
		{"score mode", map[string]string{"problem.yaml": "name: v\ntitle: V\ntime_limit: 1\nmemory_limit: 64\nscore_mode: min\n"},
			nil, importer.SeverityError, `invalid score mode "min"`},
		{"statement language", map[string]string{"statement/e n.pdf": pdf}, nil, importer.SeverityError, `invalid language "e n"`},
		{"two statements", map[string]string{"statement/english.md": "x"}, nil, importer.SeverityError, "a second statement in en"},
		{"primary without statement", map[string]string{"problem.yaml": "name: v\ntitle: V\ntime_limit: 1\nmemory_limit: 64\nprimary_statements: [es]\n"},
			nil, importer.SeverityWarning, `primary statement language "es" has no statement`},
		{"attachment directory", map[string]string{"attachments/data/x": ""}, nil, importer.SeverityError, "attachments are files: zip the directory"},
		{"attachment name", map[string]string{"attachments/a b.txt": ""}, nil, importer.SeverityError, `invalid attachment file name "a b.txt"`},
		{"submission format", map[string]string{"problem.yaml": "name: v\ntitle: V\ntime_limit: 1\nmemory_limit: 64\nsubmission_format: [a/b.%l]\n"},
			nil, importer.SeverityError, `invalid submission format file "a/b.%l"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := with(ok, c.change)
			for _, r := range c.remove {
				delete(files, r)
			}
			dir := pkgDir(t, files)
			rep := &importer.Report{}
			pk, err := Load(dir, &importer.Options{Report: rep})
			if err != nil {
				rep.Errorf(dir, "%v", err)
			} else if pk.Task != nil {
				importer.ValidateTask(pk.Task, nil, rep)
			}
			wantIssue(t, rep, c.sev, c.text)
		})
	}
	// The package as it is: no issue at all.
	_, rep := loadValid(t, pkgDir(t, ok))
	noIssues(t, rep)
}

func TestLoadErrors(t *testing.T) {
	for _, c := range []struct {
		files map[string]string
		err   string
	}{
		{map[string]string{"tests/01.in": ""}, "file missing"},
		{map[string]string{"problem.yaml": ""}, "problem.yaml is empty"},
		{map[string]string{"problem.yaml": "name: [x\n"}, "problem.yaml"},
		{map[string]string{"problem.yaml": "- a\n"}, "cannot unmarshal"},
		{map[string]string{"problem.yaml": "name: x\nscoring: {subtasks: [{points: ten}]}\n"}, "expected a number"},
		{map[string]string{"problem.yaml": "name: x\npublic_tests: {a: b}\n"}, `expected "all", test names, numbers or ranges`},
		{map[string]string{"problem.yaml": "name: x\nchecker: {type: float, tol: 1}\n"}, "field tol not found"},
	} {
		_, err := Load(pkgDir(t, c.files), nil)
		if err == nil || !strings.Contains(err.Error(), c.err) {
			t.Errorf("%v: error %v, want %q", c.files, err, c.err)
		}
	}
	if _, err := Load(t.TempDir()+"/missing", nil); err == nil {
		t.Error("no error for a missing package")
	}
	big := pkgDir(t, map[string]string{"problem.yaml": "name: x\n# " + strings.Repeat("x", maxProblemYAML) + "\n"})
	if _, err := Load(big, nil); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("error %v", err)
	}
}

func TestLoadSkipStatements(t *testing.T) {
	dir := pkgDir(t, with(tests(1), map[string]string{
		"problem.yaml": "name: s\ntitle: S\ntime_limit: 1\nmemory_limit: 64\n", "solutions/a.ac.c": "",
	}))
	rep := &importer.Report{}
	pk, err := Load(dir, &importer.Options{Report: rep, SkipStatements: true})
	if err != nil || pk.Task == nil {
		t.Fatal(err, issues(rep))
	}
	if len(pk.Task.Statements) != 0 || hasIssue(rep, importer.SeverityWarning, "no statement") {
		t.Errorf("statements %v\n%s", pk.Task.Statements, issues(rep))
	}
}

func TestRegisteredAndDetect(t *testing.T) {
	for _, name := range []string{Name, "package"} {
		if l, ok := importer.Default.Lookup(name); !ok || l.Name() != Name {
			t.Errorf("loader %s not registered", name)
		}
	}
	dir := pkgDir(t, with(tests(1), map[string]string{"problem.yaml": "name: d\ntitle: D\n"}))
	l, err := importer.Default.ChooseTask("", dir)
	if err != nil || l.Name() != Name {
		t.Errorf("autodetection: %v %v", l, err)
	}
	if (Loader{}).Detect(dir, importer.KindContest) || Detect(t.TempDir()) {
		t.Error("detected what is not a package")
	}
}
