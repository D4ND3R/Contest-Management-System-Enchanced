//go:build sandbox

// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/steps"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/steps/stepstest"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/tasktype"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
)

// Run with `make test-sandbox` (root, isolate); CMS_SANDBOX_CGROUPS=0
// without cgroups v2.

// countingExec counts the jobs that reach the task types.
type countingExec struct {
	Executor
	mu     sync.Mutex
	byKind map[job.Kind]int
}

func (c *countingExec) Execute(ctx context.Context, j *job.Job, res *job.Result) error {
	c.mu.Lock()
	c.byKind[j.Operation.Type]++
	c.mu.Unlock()
	return c.Executor.Execute(ctx, j, res)
}

func (c *countingExec) count(k job.Kind) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.byKind[k]
}

// The solutions of an a+b task in C++.
var solutions = map[string]string{
	"correct": `#include <cstdio>
int main() { long long a, b; if (scanf("%lld %lld", &a, &b) != 2) return 1; printf("%lld\n", a + b); }
`,
	"wrong": `#include <cstdio>
int main() { long long a, b; if (scanf("%lld %lld", &a, &b) != 2) return 1; printf("%lld\n", a + b + 1); }
`,
	"tle": `int main() { volatile unsigned long x = 0; for (;;) x++; }
`,
	"nonzero": `int main() { return 3; }
`,
	"ce": `int main( { return 0; }
`,
}

// A worker loop over real boxes: Batch compilations and evaluations of a
// C++ a+b task whose files are in a local blob store, results read from
// the result queue, and the compilation cache.
func TestEndToEndBatch(t *testing.T) {
	sb := stepstest.Sandbox(t, true)
	store, err := blob.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	langs := language.Builtin()
	stepsCfg := steps.ConfigFrom(config.Default())
	stepsCfg.Cgroups = stepstest.Cgroups()
	exec := &countingExec{
		Executor: TaskTypes(&tasktype.Env{Boxes: tasktype.SandboxBoxes(sb), Store: store, Languages: langs, Steps: &stepsCfg}),
		byKind:   map[job.Kind]int{},
	}
	h := newHarness(t, job.JobVisibility)
	cache := NewCache(h.kv, store, langs, CacheOptions{
		TTL: time.Hour, Limits: config.Default().Sandbox.Compilation, Cgroups: stepsCfg.Cgroups,
	})
	r := h.start("judge-e2e", exec, Options{Concurrency: 2, Boxes: sb.Config().BoxCount, Cache: cache, Ready: sb.SelfTest})

	ctx := context.Background()
	put := func(s string) string {
		d, err := store.PutBytes(ctx, []byte(s))
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	const lang = "C++17 / g++"
	params := json.RawMessage(`["alone", ["", ""], "diff"]`)
	testcases := map[string][2]string{"tc1": {put("1 2\n"), put("3\n")}, "tc2": {put("40 2\n"), put("42\n")}}
	ids := map[string]int32{"correct": 1, "wrong": 2, "tle": 3, "nonzero": 4, "ce": 5}
	sources := map[string]string{}

	compile := func(name string, sub int32) {
		sources[name] = put(solutions[name])
		h.enqueue(&job.Job{
			ID:                 fmt.Sprintf("compile-%d", sub),
			Operation:          job.Operation{Type: job.Compilation, ObjectID: sub, DatasetID: 1},
			Attempt:            1,
			TaskType:           tasktype.NameBatch,
			TaskTypeParameters: params,
			Language:           lang,
			Files:              map[string]string{"sum.%l": sources[name]},
			Managers:           map[string]string{},
		}, job.PriorityHigh)
	}
	for name, sub := range ids {
		compile(name, sub)
	}
	executables := map[int32]map[string]string{}
	compileDurations := map[int32]time.Duration{}
	for range ids {
		res := h.result(2 * time.Minute)
		if !res.Success {
			t.Fatalf("compilation %s failed: %s", res.JobID, res.Error)
		}
		executables[res.Operation.ObjectID] = res.Executables
		compileDurations[res.Operation.ObjectID] = res.Durations.Total
		if res.Operation.ObjectID == ids["ce"] {
			if res.CompilationOutcome != job.CompilationFail || res.Text[0] != "Compilation failed" ||
				res.Stats == nil || res.Stats.ExitStatus != job.ExitNonzeroReturn || !strings.Contains(res.Stats.Stderr, "error") {
				t.Errorf("compilation error: %+v %+v", res, res.Stats)
			}
			continue
		}
		if res.CompilationOutcome != job.CompilationOK || len(res.Executables) != 1 || res.Executables["sum"] == "" ||
			res.Text[0] != "Compilation succeeded" || len(res.Sandboxes) != 1 {
			t.Errorf("compilation %s: %+v", res.JobID, res)
		}
	}
	if n := exec.count(job.Compilation); n != len(ids) {
		t.Errorf("%d compilations ran", n)
	}

	evaluate := func(sub int32, tc string) {
		h.enqueue(&job.Job{
			ID:                 fmt.Sprintf("evaluate-%d-%s", sub, tc),
			Operation:          job.Operation{Type: job.Evaluation, ObjectID: sub, DatasetID: 1, TestcaseCodename: tc},
			Attempt:            1,
			TaskType:           tasktype.NameBatch,
			TaskTypeParameters: params,
			Language:           lang,
			Files:              map[string]string{"sum.%l": digest(fmt.Sprint(sub))},
			Managers:           map[string]string{},
			Executables:        executables[sub],
			Input:              testcases[tc][0],
			Output:             testcases[tc][1],
			TimeLimit:          0.5,
			MemoryLimit:        256 << 20,
		}, job.PriorityMedium)
	}
	type want struct {
		outcome, text, exit string
	}
	wants := map[string]want{
		"evaluate-1-tc1": {"1.0", "Output is correct", job.ExitOK},
		"evaluate-1-tc2": {"1.0", "Output is correct", job.ExitOK},
		"evaluate-2-tc1": {"0.0", "Output isn't correct", job.ExitOK},
		"evaluate-3-tc1": {"0.0", "Execution timed out", job.ExitTimeout},
		"evaluate-4-tc1": {"0.0", "Execution failed because the return code was nonzero", job.ExitNonzeroReturn},
	}
	evaluate(1, "tc1")
	evaluate(1, "tc2")
	evaluate(2, "tc1")
	evaluate(3, "tc1")
	evaluate(4, "tc1")
	for range wants {
		res := h.result(2 * time.Minute)
		w, ok := wants[res.JobID]
		if !ok {
			t.Fatalf("unexpected result %s", res.JobID)
		}
		if !res.Success || res.Outcome != w.outcome || len(res.Text) == 0 || res.Text[0] != w.text ||
			res.Stats == nil || res.Stats.ExitStatus != w.exit || res.Worker != "judge-e2e" {
			t.Errorf("%s: %+v, plus %+v", res.JobID, res, res.Stats)
		}
		if res.Stats != nil && w.exit == job.ExitTimeout && (res.Stats.ExecutionTime == nil || *res.Stats.ExecutionTime < 0.5) {
			t.Errorf("%s: execution time %v", res.JobID, res.Stats.ExecutionTime)
		}
	}

	// The correct source submitted again compiles from the cache, and its
	// executable runs.
	start := time.Now()
	compile("correct", 6)
	res := h.result(2 * time.Minute)
	if !res.Success || res.CompilationOutcome != job.CompilationOK || !reflect.DeepEqual(res.Executables, executables[1]) {
		t.Fatalf("cached compilation: %+v", res)
	}
	if n := exec.count(job.Compilation); n != len(ids) {
		t.Errorf("the identical compilation ran (%d compilations)", n)
	}
	if hits := metricValue(r.w.m.cache.WithLabelValues("hit")); hits != 1 {
		t.Errorf("%v cache hits", hits)
	}
	t.Logf("compilation: %v in the sandbox, %v from the cache (%v until read back)",
		compileDurations[1], res.Durations.Total, time.Since(start))
	if res.Durations.Total >= compileDurations[1] {
		t.Errorf("the cached compilation took %v, the real one %v", res.Durations.Total, compileDurations[1])
	}
	executables[6] = res.Executables
	evaluate(6, "tc2")
	if res := h.result(2 * time.Minute); res.Outcome != "1.0" {
		t.Errorf("evaluation of the cached executable: %+v", res)
	}

	h.noResult(100 * time.Millisecond)
	h.idle()
	r.waitCount(t, "evaluate", outcomeSuccess, 6)
	if err := r.stop(t); err != nil {
		t.Fatal(err)
	}
}
