// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package tasktype

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/steps"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/steps/stepstest"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/sandbox"
)

// fakeBox is a box of the fake environment.
type fakeBox struct {
	*stepstest.FakeBox
	name   string
	mu     sync.Mutex
	closed bool
	kept   bool
}

func (b *fakeBox) Keep() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.kept = true
}

func (b *fakeBox) Dir() string { return b.FakeBox.Dir }

func (b *fakeBox) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	return nil
}

func (b *fakeBox) isClosed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closed
}

// fakeRun plays the programs of a box: name is the name the task type
// gave the box ("compile", "evaluate", "check").
type fakeRun func(name string, b *fakeBox, spec sandbox.RunSpec) (*sandbox.Result, error)

// fakeEnv is an Env whose boxes are fakes (stepstest.FakeBox) and whose
// store is a local blob store.
type fakeEnv struct {
	t     *testing.T
	env   *Env
	store blob.Store
	run   fakeRun

	mu    sync.Mutex
	boxes []*fakeBox
}

func newFakeEnv(t *testing.T, run fakeRun) *fakeEnv {
	t.Helper()
	store, err := blob.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fe := &fakeEnv{t: t, store: store, run: run}
	cfg := steps.ConfigFrom(config.Default())
	fe.env = &Env{
		Boxes:     fe,
		Store:     store,
		Languages: language.Builtin(),
		Steps:     &cfg,
	}
	return fe
}

func (fe *fakeEnv) NewBox(ctx context.Context, name string) (Box, error) {
	b := &fakeBox{name: name}
	b.FakeBox = stepstest.New(fe.t, func(_ *stepstest.FakeBox, spec sandbox.RunSpec) (*sandbox.Result, error) {
		if fe.run == nil {
			return stepstest.OK(), nil
		}
		return fe.run(name, b, spec)
	})
	fe.mu.Lock()
	fe.boxes = append(fe.boxes, b)
	fe.mu.Unlock()
	return b, nil
}

// box returns the i-th box created.
func (fe *fakeEnv) box(i int) *fakeBox {
	fe.t.Helper()
	fe.mu.Lock()
	defer fe.mu.Unlock()
	if i >= len(fe.boxes) {
		fe.t.Fatalf("only %d boxes were created", len(fe.boxes))
	}
	return fe.boxes[i]
}

// names returns the names of the boxes created, in order.
func (fe *fakeEnv) names() []string {
	fe.mu.Lock()
	defer fe.mu.Unlock()
	var n []string
	for _, b := range fe.boxes {
		n = append(n, b.name)
	}
	return n
}

// allClosed fails the test if a box was not released.
func (fe *fakeEnv) allClosed() {
	fe.t.Helper()
	fe.mu.Lock()
	defer fe.mu.Unlock()
	for _, b := range fe.boxes {
		if !b.isClosed() {
			fe.t.Errorf("box %s was not closed", b.name)
		}
	}
}

// put stores data and returns its digest.
func (fe *fakeEnv) put(data string) string {
	fe.t.Helper()
	d, err := fe.store.PutBytes(context.Background(), []byte(data))
	if err != nil {
		fe.t.Fatal(err)
	}
	return d
}

// get returns the content of a stored file.
func (fe *fakeEnv) get(digest string) string {
	fe.t.Helper()
	rc, err := fe.store.Open(context.Background(), digest)
	if err != nil {
		fe.t.Fatal(err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		fe.t.Fatal(err)
	}
	return string(data)
}

// execute runs a job and checks that the result is valid.
func (fe *fakeEnv) execute(j *job.Job) (*job.Result, error) {
	fe.t.Helper()
	res := j.NewResult("test-worker")
	err := Execute(context.Background(), fe.env, j, res)
	if verr := res.Validate(); verr != nil {
		fe.t.Fatalf("invalid result %+v: %v", res, verr)
	}
	if (err == nil) != res.Success {
		fe.t.Fatalf("error %v with success %v", err, res.Success)
	}
	return res, err
}

// mustExecute runs a job that must succeed.
func (fe *fakeEnv) mustExecute(j *job.Job) *job.Result {
	fe.t.Helper()
	res, err := fe.execute(j)
	if err != nil {
		fe.t.Fatalf("%s: %v", j.Describe(), err)
	}
	return res
}

var jobCounter struct {
	sync.Mutex
	n int
}

// newJob returns a job of a kind for a task type, with valid ids.
func newJob(kind job.Kind, taskType, params string) *job.Job {
	jobCounter.Lock()
	jobCounter.n++
	n := jobCounter.n
	jobCounter.Unlock()
	j := &job.Job{
		ID:                 fmt.Sprintf("job-%d", n),
		Operation:          job.Operation{Type: kind, ObjectID: int32(n), DatasetID: 1},
		Attempt:            1,
		TaskType:           taskType,
		TaskTypeParameters: json.RawMessage(params),
		Files:              map[string]string{},
		Managers:           map[string]string{},
		Executables:        map[string]string{},
	}
	if kind == job.Evaluation {
		j.Operation.TestcaseCodename = "tc1"
	}
	if kind == job.UserTestEvaluation {
		j.OnlyExecution, j.GetOutput = true, true
	}
	return j
}

// argv0 is the program of a run (its base name).
func argv0(spec sandbox.RunSpec) string {
	if len(spec.Argv) == 0 {
		return ""
	}
	a := spec.Argv[0]
	return a[strings.LastIndexByte(a, '/')+1:]
}

// exited is the result of a program that ended with a status.
func exited(st sandbox.Status, cpu float64) *sandbox.Result {
	r := &sandbox.Result{Status: st, CPUTime: cpu, WallTime: cpu + 0.01, MemoryKiB: 1024}
	switch st {
	case sandbox.StatusRuntimeError:
		r.ExitCode = 1
	case sandbox.StatusSignal:
		r.Signal = 11
	}
	return r
}

func equalText(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s: text %q, want %q", what, got, want)
	}
}
