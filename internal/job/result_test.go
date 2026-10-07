// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package job_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
)

// results returns a valid successful result for each kind.
func results() map[job.Kind]*job.Result {
	out := map[job.Kind]*job.Result{}
	for k, j := range jobs() {
		r := j.NewResult("worker-1")
		r.Success = true
		r.Durations = job.Durations{Wait: time.Millisecond, Fetch: 2 * time.Millisecond, Run: time.Second, Total: 1100 * time.Millisecond}
		stats := &job.Stats{
			ExecutionTime:          job.Ptr(0.012),
			ExecutionWallClockTime: job.Ptr(0.05),
			ExecutionMemory:        job.Ptr(int64(3 << 20)),
			ExitStatus:             job.ExitOK,
		}
		switch k {
		case job.Compilation, job.UserTestCompilation:
			r.CompilationOutcome = job.CompilationOK
			r.Text = []string{"Compilation succeeded"}
			r.Executables = map[string]string{"sum": dExe}
			stats.Stdout, stats.Stderr = "", "sum.cpp:1:1: warning: ‘x’ unused"
		case job.Evaluation:
			r.Outcome = job.FormatOutcome(0.5)
			r.Text = []string{"Output is partially correct"}
			r.AdminText = "line 3 differs"
			r.Sandboxes = []string{"/var/lib/cms/worker/box-7"}
			r.SandboxDigests = map[string]string{"/var/lib/cms/worker/box-7": dUser}
		case job.UserTestEvaluation:
			r.Outcome = "0.0"
			r.Text = []string{"Execution completed successfully"}
			r.UserOutput = dUser
		}
		r.Stats = stats
		out[k] = r
	}
	return out
}

func TestResultRoundTrip(t *testing.T) {
	all := results()
	// A failure and a compilation error round trip too.
	f := jobs()[job.Evaluation].NewResult("worker-2")
	f.Fail(errors.New("isolate: box 3 is busy\x00"))
	f.Tombstone = true
	ce := jobs()[job.Compilation].NewResult("worker-1")
	ce.Success = true
	ce.CompilationOutcome = job.CompilationFail
	ce.Text = []string{"Compilation failed"}
	ce.Stats = &job.Stats{ExitStatus: job.ExitNonzeroReturn, Stderr: "error: expected ';'"}
	sig := results()[job.Evaluation]
	sig.Outcome = "0.0"
	sig.Text = []string{"Execution killed with signal %s", "11"}
	sig.Stats.ExitStatus, sig.Stats.Signal = job.ExitSignal, 11
	for name, r := range map[string]*job.Result{
		"compile": all[job.Compilation], "evaluate": all[job.Evaluation],
		"compile_test": all[job.UserTestCompilation], "evaluate_test": all[job.UserTestEvaluation],
		"failure": f, "compilation error": ce, "signal": sig,
	} {
		t.Run(name, func(t *testing.T) {
			b, err := r.Encode()
			if err != nil {
				t.Fatal(err)
			}
			got, err := job.DecodeResult(b)
			if err != nil {
				t.Fatalf("decode %s: %v", b, err)
			}
			want := *r
			want.V = job.Version
			if !reflect.DeepEqual(got, &want) {
				t.Fatalf("round trip:\n got %+v\nwant %+v", got, &want)
			}
		})
	}
	if f.Error != "isolate: box 3 is busy�" || f.Success {
		t.Errorf("Fail: %+v", f)
	}
	if v, ok, err := all[job.Evaluation].OutcomeValue(); v != 0.5 || !ok || err != nil {
		t.Errorf("outcome value: %v %v %v", v, ok, err)
	}
	if _, ok, err := f.OutcomeValue(); ok || err != nil {
		t.Errorf("outcome value of a failure: %v %v", ok, err)
	}
}

func TestNewResultCopiesIdentity(t *testing.T) {
	j := jobs()[job.Evaluation]
	j.Attempt, j.Generation = 3, 9
	r := j.NewResult("w")
	if r.JobID != j.ID || r.Operation != j.Operation || r.Attempt != 3 || r.Generation != 9 || r.Worker != "w" {
		t.Fatalf("%+v", r)
	}
}

func TestResultValidation(t *testing.T) {
	cases := []struct {
		name string
		kind job.Kind
		mut  func(*job.Result)
	}{
		{"bad operation", job.Evaluation, func(r *job.Result) { r.Operation.TestcaseCodename = "" }},
		{"no job id", job.Evaluation, func(r *job.Result) { r.JobID = "" }},
		{"attempt 0", job.Evaluation, func(r *job.Result) { r.Attempt = 0 }},
		{"negative generation", job.Evaluation, func(r *job.Result) { r.Generation = -2 }},
		{"no worker", job.Evaluation, func(r *job.Result) { r.Worker = "" }},
		{"NUL in text", job.Evaluation, func(r *job.Result) { r.Text = []string{"a\x00"} }},
		{"invalid UTF-8 in text", job.Evaluation, func(r *job.Result) { r.Text = []string{"%s", "\xff"} }},
		{"long text", job.Evaluation, func(r *job.Result) {
			r.Text = []string{"%s%s", strings.Repeat("a", job.MaxTextLen/2), strings.Repeat("b", job.MaxTextLen/2)}
		}},
		{"long admin text", job.Evaluation, func(r *job.Result) { r.AdminText = strings.Repeat("a", job.MaxTextLen+1) }},
		{"NUL in stderr", job.Compilation, func(r *job.Result) { r.Stats.Stderr = "\x00" }},
		{"long stdout", job.Compilation, func(r *job.Result) { r.Stats.Stdout = strings.Repeat("a", job.MaxOutputLen+1) }},
		{"negative duration", job.Evaluation, func(r *job.Result) { r.Durations.Fetch = -1 }},
		{"negative time", job.Evaluation, func(r *job.Result) { r.Stats.ExecutionTime = job.Ptr(-0.1) }},
		{"NaN wall time", job.Evaluation, func(r *job.Result) { r.Stats.ExecutionWallClockTime = job.Ptr(nan()) }},
		{"negative memory", job.Evaluation, func(r *job.Result) { r.Stats.ExecutionMemory = job.Ptr(int64(-1)) }},
		{"unknown exit status", job.Evaluation, func(r *job.Result) { r.Stats.ExitStatus = "crashed" }},
		{"no exit status", job.Evaluation, func(r *job.Result) { r.Stats.ExitStatus = "" }},
		{"signal without status", job.Evaluation, func(r *job.Result) { r.Stats.Signal = 9 }},
		{"negative signal", job.Evaluation, func(r *job.Result) { r.Stats.ExitStatus, r.Stats.Signal = job.ExitSignal, -1 }},
		{"bad sandbox path", job.Evaluation, func(r *job.Result) { r.Sandboxes = []string{"a\nb"} }},
		{"tombstone archive", job.Evaluation, func(r *job.Result) {
			r.SandboxDigests = map[string]string{"/b": job.TombstoneDigest}
		}},
		{"tombstone executable", job.Compilation, func(r *job.Result) { r.Executables["sum"] = job.TombstoneDigest }},
		{"executable with a path", job.Compilation, func(r *job.Result) { r.Executables["bin/sum"] = dExe }},
		{"bad user output", job.UserTestEvaluation, func(r *job.Result) { r.UserOutput = "x" }},
		{"successful tombstone", job.Evaluation, func(r *job.Result) { r.Tombstone = true }},
		{"successful with error", job.Evaluation, func(r *job.Result) { r.Error = "boom" }},
		{"failure with outcome", job.Evaluation, func(r *job.Result) { r.Success = false }},
		{"failure with compilation outcome", job.Compilation, func(r *job.Result) {
			r.Success, r.Executables = false, nil
		}},
		{"failure with executables", job.Compilation, func(r *job.Result) {
			r.Success, r.CompilationOutcome = false, ""
		}},
		{"no compilation outcome", job.Compilation, func(r *job.Result) { r.CompilationOutcome = "" }},
		{"compilation outcome true", job.Compilation, func(r *job.Result) { r.CompilationOutcome = "true" }},
		{"failed compilation with executables", job.Compilation, func(r *job.Result) { r.CompilationOutcome = job.CompilationFail }},
		{"compilation with outcome", job.UserTestCompilation, func(r *job.Result) { r.Outcome = "1.0" }},
		{"evaluation without outcome", job.Evaluation, func(r *job.Result) { r.Outcome = "" }},
		{"non-numeric outcome", job.Evaluation, func(r *job.Result) { r.Outcome = "ok" }},
		{"infinite outcome", job.Evaluation, func(r *job.Result) { r.Outcome = "inf" }},
		{"NaN outcome", job.UserTestEvaluation, func(r *job.Result) { r.Outcome = "nan" }},
		{"evaluation with compilation outcome", job.Evaluation, func(r *job.Result) { r.CompilationOutcome = "ok" }},
		{"evaluation with executables", job.UserTestEvaluation, func(r *job.Result) { r.Executables = map[string]string{"a": dExe} }},
		{"submission with user output", job.Evaluation, func(r *job.Result) { r.UserOutput = dUser }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := results()[c.kind]
			c.mut(r)
			if err := r.Validate(); !errors.Is(err, job.ErrInvalid) {
				t.Fatalf("validate: %v", err)
			}
			if _, err := r.Encode(); !errors.Is(err, job.ErrInvalid) {
				t.Fatalf("encode: %v", err)
			}
		})
	}

	// Valid variations.
	for name, mut := range map[string]func(*job.Result){
		"no stats":                  func(r *job.Result) { r.Stats = nil },
		"unknown values":            func(r *job.Result) { r.Stats = &job.Stats{ExitStatus: job.ExitSandboxError} },
		"user test without outcome": func(r *job.Result) { r.Outcome, r.UserOutput = "", "" },
		"security violation": func(r *job.Result) {
			r.Stats.ExitStatus = job.ExitSecurityViolation
			r.Outcome = "0.0"
		},
		"failure keeps stats and text": func(r *job.Result) {
			r.Fail(nil)
			r.Text = []string{"Execution failed because the return code was nonzero"}
		},
	} {
		r := results()[job.UserTestEvaluation]
		mut(r)
		if err := r.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	r := results()[job.UserTestEvaluation]
	r.Fail(nil)
	if r.Error != "unknown error" || r.Outcome != "" || r.UserOutput != "" {
		t.Errorf("Fail(nil): %+v", r)
	}
}

func BenchmarkResultEncodeDecode(b *testing.B) {
	r := results()[job.Evaluation]
	b.ReportAllocs()
	for b.Loop() {
		enc, err := r.Encode()
		if err != nil {
			b.Fatal(err)
		}
		if _, err := job.DecodeResult(enc); err != nil {
			b.Fatal(err)
		}
	}
}
