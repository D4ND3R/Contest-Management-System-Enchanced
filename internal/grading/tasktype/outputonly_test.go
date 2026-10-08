// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Several cases follow cmstestsuite/unit_tests/grading/tasktypes/
// OutputOnlyTest.py and the BatchAndOutput tests of cmstestsuite/Tests.py
// of CMS (Copyright © 2017-2018 Stefano Maggiolo <s.maggiolo@gmail.com>).

package tasktype

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/sandbox"
)

func TestOutputOnly(t *testing.T) {
	var check func(b *fakeBox, spec sandbox.RunSpec) (*sandbox.Result, error)
	fe := newFakeEnv(t, func(name string, b *fakeBox, spec sandbox.RunSpec) (*sandbox.Result, error) {
		if name != "check" {
			t.Errorf("unexpected box %s", name)
		}
		return check(b, spec)
	})
	evalJob := func(params, codename string, files map[string]string, correct string) *job.Job {
		j := newJob(job.Evaluation, NameOutputOnly, params)
		j.Operation.TestcaseCodename = codename
		for k, v := range files {
			j.Files[k] = fe.put(v)
		}
		j.Input = fe.put("input")
		j.Output = fe.put(correct)
		j.TimeLimit, j.MemoryLimit = 1, 256<<20
		return j
	}

	// Compilation: nothing to do.
	res := fe.mustExecute(newJob(job.Compilation, NameOutputOnly, `["diff"]`))
	if res.CompilationOutcome != job.CompilationOK || res.Stats != nil || res.Executables != nil {
		t.Errorf("compilation %+v", res)
	}
	equalText(t, "compile", res.Text, []string{"No compilation needed"})

	files := map[string]string{"output_000.txt": "correct 1\n", "output_001.txt": "wrong\n"}
	res = fe.mustExecute(evalJob(`["diff"]`, "000", files, "correct  1"))
	if res.Outcome != "1.0" || res.Stats != nil {
		t.Errorf("correct: %+v", res)
	}
	equalText(t, "correct", res.Text, []string{"Output is correct"})
	res = fe.mustExecute(evalJob(`["diff"]`, "001", files, "correct 2"))
	if res.Outcome != "0.0" {
		t.Errorf("wrong: %+v", res)
	}
	// Partial submissions: a missing file scores 0.
	res = fe.mustExecute(evalJob(`["diff"]`, "002", files, "x"))
	if res.Outcome != "0.0" || res.Stats != nil {
		t.Errorf("missing: %+v", res)
	}
	equalText(t, "missing", res.Text, []string{"File not submitted"})
	if len(fe.names()) != 0 {
		t.Errorf("a white diff used boxes: %v", fe.names())
	}

	// Comparator: the submitted file is the user output.
	check = comparator(t, "1.0", "translate:success")
	res = fe.mustExecute(withChecker(fe, evalJob(`["comparator"]`, "000", files, "correct 1")))
	if res.Outcome != "1.0" || res.Stats != nil || len(res.Sandboxes) != 1 {
		t.Errorf("comparator: %+v", res)
	}
	equalText(t, "comparator", res.Text, []string{"Output is correct"})
	fe.allClosed()
}

func withChecker(fe *fakeEnv, j *job.Job) *job.Job {
	j.Managers["checker"] = fe.put("checker program")
	return j
}

func TestBatchAndOutput(t *testing.T) {
	// The task batch_and_output of cmstestsuite: testcases 000 and 001
	// are output only, 002 and 003 batch; the comparator gets "batch" or
	// "outputonly" as fourth argument. Its checker accepts the output
	// "0" for batch testcases and "1" for output-only ones; here the
	// program prints its source and the comparator checks the argument.
	const params = `["alone", ["", ""], "comparator", "000,001"]`
	var program string // what the program prints
	fe := newFakeEnv(t, func(name string, b *fakeBox, spec sandbox.RunSpec) (*sandbox.Result, error) {
		switch name {
		case "compile":
			return fakeCompiler(b, spec)
		case "evaluate":
			if program == "timeout" {
				return exited(sandbox.StatusTimedOut, 0.6), nil
			}
			return exited(sandbox.StatusOK, 0.1), b.WriteFile(spec.Stdout, []byte(program))
		case "check":
			kind := spec.Argv[len(spec.Argv)-1]
			out := strings.TrimSpace(b.Read("user_output.txt"))
			ok := kind == "batch" && out == "0" || kind == "outputonly" && out == "1"
			outcome := map[bool]string{true: "1", false: "0"}[ok]
			_ = b.WriteFile(spec.Stdout, []byte(outcome))
			_ = b.WriteFile(spec.Stderr, []byte("checked "+kind))
			return exited(sandbox.StatusOK, 0.01), nil
		}
		t.Errorf("unexpected box %s", name)
		return nil, nil
	})

	// A submission: a source (or not) and some outputs (Tests.py).
	submit := func(source string, outputs map[string]string) (executables map[string]string, files map[string]string) {
		files = map[string]string{}
		for k, v := range outputs {
			files["output_"+k+".txt"] = fe.put(v)
		}
		if source == "" {
			j := newJob(job.Compilation, NameBatchAndOutput, params)
			j.Language = langC
			j.Files = files
			res := fe.mustExecute(j)
			equalText(t, "no source", res.Text, []string{"No compilation needed"})
			if res.CompilationOutcome != job.CompilationOK || res.Executables != nil {
				t.Errorf("compilation without sources: %+v", res)
			}
			return nil, files
		}
		program = source
		files["batch_and_output.%l"] = fe.put(source)
		j := newJob(job.Compilation, NameBatchAndOutput, params)
		j.Language = langC
		j.Files = files
		res := fe.mustExecute(j)
		if res.CompilationOutcome != job.CompilationOK || len(res.Executables) != 1 || res.Executables["batch_and_output"] == "" {
			t.Fatalf("compilation: %+v", res)
		}
		return res.Executables, files
	}
	score := func(executables, files map[string]string) (total float64, texts []string) {
		for _, tc := range []string{"000", "001", "002", "003"} {
			j := newJob(job.Evaluation, NameBatchAndOutput, params)
			j.Language = langC
			j.Operation.TestcaseCodename = tc
			j.Files, j.Executables = files, executables
			if j.Executables == nil {
				j.Executables = map[string]string{}
			}
			j.Managers["checker"] = fe.put("checker")
			j.Input, j.Output = fe.put(""), fe.put("")
			j.TimeLimit = 0.5
			res := fe.mustExecute(j)
			v, _, err := res.OutcomeValue()
			if err != nil {
				t.Fatal(err)
			}
			total += v * 25
			texts = append(texts, strings.Join(res.Text, "|"))
		}
		return total, texts
	}

	cases := []struct {
		name    string
		source  string
		outputs map[string]string
		want    float64
	}{
		// Upstream Tests.py, batchandoutput-*.
		{"batchonly-correct", "0", nil, 50},
		{"batchonly-incorrect", "1", nil, 0},
		{"outputonly-correct", "", map[string]string{"000": "1", "001": "1", "002": "0"}, 75},
		{"outputonly-incorrect", "", map[string]string{"000": "0", "001": "0", "002": "1"}, 0},
		{"mixed-correct", "0", map[string]string{"000": "1", "001": "1", "002": "0"}, 100},
		{"mixed-incorrect", "1", map[string]string{"000": "0", "001": "0", "002": "1"}, 0},
		{"tle", "timeout", map[string]string{"000": "1", "001": "1", "002": "0"}, 75},
	}
	for _, c := range cases {
		exes, files := submit(c.source, c.outputs)
		got, texts := score(exes, files)
		if got != c.want {
			t.Errorf("%s: score %v, want %v (%q)", c.name, got, c.want, texts)
		}
		if c.name == "tle" && texts[3] != "Execution timed out" {
			t.Errorf("tle: texts %q", texts)
		}
		if c.name == "outputonly-correct" && texts[3] != "File not submitted" {
			t.Errorf("outputonly: texts %q", texts)
		}
	}
	fe.allClosed()

	// User tests run the program even when no testcase is output only.
	tt := mustGet(t, NameBatchAndOutput, `["alone", ["", ""], "diff", ""]`)
	program = "hello"
	j := newJob(job.UserTestEvaluation, NameBatchAndOutput, `["alone", ["", ""], "diff", ""]`)
	j.Language = langC
	j.Executables["batch_and_output"] = fe.put("exe")
	j.Input = fe.put("in")
	res := j.NewResult("w")
	if err := tt.Evaluate(context.Background(), fe.env, j, res); err != nil {
		t.Fatal(err)
	}
	if res.UserOutput == "" || fe.get(res.UserOutput) != "hello" {
		t.Errorf("user test: %+v", res)
	}
}

func TestExecute(t *testing.T) {
	fe := newFakeEnv(t, nil)
	j := newJob(job.Compilation, "Nope", `[]`)
	res, err := fe.execute(j)
	if !errors.Is(err, ErrConfiguration) || !errors.Is(err, ErrUnknown) || res.Success || res.Error == "" {
		t.Errorf("unknown task type: %v %+v", err, res)
	}
	j = newJob(job.Compilation, NameBatch, `["alone"]`)
	if _, err := fe.execute(j); !errors.Is(err, ErrConfiguration) || !errors.Is(err, ErrParameters) {
		t.Errorf("bad parameters: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	j = newJob(job.Compilation, NameOutputOnly, `["diff"]`)
	res = j.NewResult("w")
	if err := Execute(ctx, fe.env, j, res); !errors.Is(err, context.Canceled) || res.Success {
		t.Errorf("cancelled: %v %+v", err, res)
	}
	if res.Durations.Total <= 0 {
		t.Errorf("durations %+v", res.Durations)
	}
}

// TestFinish: texts from checkers and managers are made storable (valid
// UTF-8, no NUL, bounded).
func TestFinish(t *testing.T) {
	long := strings.Repeat("é", job.MaxTextLen)
	res := &job.Result{
		Text:      []string{"bad \xff byte\x00", long, long},
		AdminText: "admin \x00" + long,
	}
	finish(res)
	if !res.Success || res.Text[0] != "bad \ufffd byte" {
		t.Errorf("text %q", res.Text[0])
	}
	n := 0
	for _, s := range res.Text {
		n += len(s)
	}
	if n > job.MaxTextLen || len(res.AdminText) > job.MaxTextLen || strings.Contains(res.AdminText, "\x00") {
		t.Errorf("text of %d bytes, admin text of %d", n, len(res.AdminText))
	}
	if !strings.HasPrefix(res.Text[1], "éé") || res.Text[2] != "" {
		t.Errorf("truncation: %d and %d bytes", len(res.Text[1]), len(res.Text[2]))
	}
}
