//go:build sandbox

// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package tasktype

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/steps"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/steps/stepstest"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
)

// goStdDir is the -pkgdir of the Go language (see config/languages/go.yaml):
// without it a compilation exceeds the time limit (the steps tests
// prepare it).
const goStdDir = "/var/lib/cms/go-std"

// TestTaskTypeExtras covers what cmstestsuite does not: the languages
// added by this fork, the seccomp filter, Communication tasks whose user
// process dies early, archived boxes (testdata/extra).
func TestTaskTypeExtras(t *testing.T) {
	j := newJudge(t)
	cases := []struct {
		name  string
		task  *task
		file  string
		lang  string
		check check
		// within bounds the time of the whole submission.
		within time.Duration
	}{
		{"correct-stdio", batchStdio, "../extra/correct-stdio.%l", "Kotlin / JDK", score(100), 0},
		{"correct-stdio", batchStdio, "../extra/correct-stdio.%l", "Go", score(100), 0},
		{"forbidden-syscall", batchStdio, "../extra/forbidden-syscall-stdio.%l", langCC,
			failed("Execution killed because of a forbidden system call"), 0},
		{"socket", batchStdio, "../extra/socket-stdio.%l", langCC, score(100), 0},
		// The manager would wait for the FIFOs until its wall clock limit
		// (21 s) and then fail the job (upstream); here the user's
		// failure is the outcome, at once.
		{"communication-crash-before-fifos", communicationFifoioStubbed, "../extra/communication-crash-before-fifos.%l",
			langCPP, failed("Execution killed by signal"), 10 * time.Second},
		// The Python manager dies of a broken pipe; the outcome is the
		// user's failure, not a failed job.
		{"communication-stdio-crash", communicationStdio, "../extra/communication-stdio-crash.%l", langCC,
			failed("Execution killed by signal"), 10 * time.Second},
	}
	for _, c := range cases {
		t.Run(c.name+"/"+c.lang, func(t *testing.T) {
			lang, err := j.langs.Get(c.lang)
			if err != nil {
				t.Fatal(err)
			}
			if ok, missing := installedLanguage(lang); !ok {
				t.Skipf("%s is not installed", missing)
			}
			if lang.Name == "Go" {
				if _, err := os.Stat(goStdDir); err != nil {
					t.Skipf("%s is not prepared (see go.yaml)", goStdDir)
				}
			}
			t.Parallel()
			start := time.Now()
			r := j.submit(t, c.task, lang, []string{strings.ReplaceAll(c.file, ".%l", lang.SourceExtension())})
			c.check(t, r)
			if took := time.Since(start); c.within > 0 && took > c.within {
				t.Errorf("took %v", took)
			}
			t.Logf("%s in %v", r, time.Since(start).Round(time.Millisecond))
		})
	}

	// The boxes of an Interactive evaluation are archived, also those of
	// the solutions, closed in other goroutines (21 boxes: the
	// controller and 20 solutions).
	t.Run("archive/interactive-many", func(t *testing.T) {
		t.Parallel()
		aj := *j
		aj.archive = true
		lang, err := j.langs.Get(langCPP)
		if err != nil {
			t.Fatal(err)
		}
		r := aj.submit(t, interactiveMany, lang, []string{"interactive-many-correct.cpp"})
		score(100)(t, r)
		for _, e := range r.evaluations {
			if len(e.SandboxDigests) != 21 || len(e.Sandboxes) != 21 {
				t.Errorf("%d archives of %d boxes", len(e.SandboxDigests), len(e.Sandboxes))
			}
			for dir, d := range e.SandboxDigests {
				if !strings.Contains(dir, "controller") && !strings.Contains(dir, "solution_") {
					t.Errorf("archive of %s: %s", dir, d)
				}
			}
		}
	})
}

// BenchmarkBatchEvaluation measures the cost of judging one testcase of
// a Batch task (stdin/stdout, white diff) in real boxes: fetching the
// executable and the input, the box, the run under the seccomp filter,
// the comparison and the clean-up.
func BenchmarkBatchEvaluation(b *testing.B) {
	for _, eval := range []string{"diff", "comparator"} {
		b.Run(eval, func(b *testing.B) {
			s := stepstest.Sandbox(b, true)
			store, err := blob.NewLocal(b.TempDir())
			if err != nil {
				b.Fatal(err)
			}
			cfg := steps.ConfigFrom(config.Default())
			cfg.Cgroups = stepstest.Cgroups()
			env := &Env{Boxes: SandboxBoxes(s), Store: store, Languages: language.Builtin(), Steps: &cfg}
			ctx := context.Background()
			put := func(path string) string {
				data, err := os.ReadFile(path)
				if err != nil {
					b.Fatal(err)
				}
				d, err := store.PutBytes(ctx, data)
				if err != nil {
					b.Fatal(err)
				}
				return d
			}
			params := `["alone", ["", ""], "` + eval + `"]`
			cj := newJob(job.Compilation, NameBatch, params)
			cj.Language = langCC
			cj.Files["batchstdio.%l"] = put("testdata/code/correct-stdio.c")
			cres := cj.NewResult("bench")
			if err := Execute(ctx, env, cj, cres); err != nil || cres.CompilationOutcome != job.CompilationOK {
				b.Fatalf("compilation: %v %+v", err, cres)
			}
			ej := newJob(job.Evaluation, NameBatch, params)
			ej.Language = langCC
			ej.Executables = cres.Executables
			ej.Managers["checker"] = put("testdata/tasks/batch_fileio_managed/code/checker")
			ej.Input = put("testdata/tasks/batch_stdio/data/1.in")
			ej.Output = put("testdata/tasks/batch_stdio/data/1.out")
			ej.TimeLimit, ej.MemoryLimit = 1, 256<<20
			b.ResetTimer()
			for b.Loop() {
				res := ej.NewResult("bench")
				if err := Execute(ctx, env, ej, res); err != nil || res.Outcome != "1.0" {
					b.Fatalf("evaluation: %v %+v", err, res)
				}
			}
		})
	}
}
