// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/kv"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/queue"
	cmstest "github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/worker/registry"
)

// openQueues opens the job and result queues as the worker service does,
// with a visibility timeout and quick polling for the tests.
func openQueues(ctx context.Context, c *kv.Client, vis time.Duration) (jobs, results *queue.Queue, err error) {
	jobs, err = queue.Open(ctx, c, job.JobQueue, queue.Options{
		Priorities: job.JobPriorities, Visibility: vis, PollInterval: 20 * time.Millisecond, MaxAttempts: 2,
		BackoffBase: 10 * time.Millisecond, BackoffMax: 10 * time.Millisecond,
	})
	if err != nil {
		return nil, nil, err
	}
	results, err = queue.Open(ctx, c, job.ResultQueue, queue.Options{PollInterval: 20 * time.Millisecond})
	return jobs, results, err
}

// fakeExec is a task type that succeeds at once, or runs fn.
type fakeExec struct {
	fn    func(ctx context.Context, j *job.Job, res *job.Result) error
	boxes func(j *job.Job) int
	// store, if set, receives the executables of compilations.
	store blob.Store

	mu    sync.Mutex
	calls []string // job ids
}

func (f *fakeExec) Execute(ctx context.Context, j *job.Job, res *job.Result) error {
	f.mu.Lock()
	f.calls = append(f.calls, j.ID)
	f.mu.Unlock()
	if f.fn != nil {
		return f.fn(ctx, j, res)
	}
	return f.succeed(ctx, j, res)
}

func (f *fakeExec) Boxes(j *job.Job) int {
	if f.boxes != nil {
		return f.boxes(j)
	}
	return 1
}

func (f *fakeExec) ncalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// succeed fills a successful result: compilations produce an executable
// whose content depends on the sources.
func (f *fakeExec) succeed(ctx context.Context, j *job.Job, res *job.Result) error {
	res.Success = true
	res.Stats = &job.Stats{ExecutionTime: job.Ptr(0.25), ExecutionWallClockTime: job.Ptr(0.5),
		ExecutionMemory: job.Ptr(int64(3 << 20)), ExitStatus: job.ExitOK}
	if j.Operation.Type.IsCompilation() {
		content := []byte(fmt.Sprintf("executable of %v", j.Files))
		d := blob.Digest(content)
		if f.store != nil {
			var err error
			if d, err = f.store.PutBytes(ctx, content); err != nil {
				res.Fail(err)
				return err
			}
		}
		res.CompilationOutcome = job.CompilationOK
		res.Text = []string{"Compilation succeeded"}
		res.Executables = map[string]string{"sum": d}
		return nil
	}
	res.Outcome = "1.0"
	res.Text = []string{"Output is correct"}
	return nil
}

func digest(s string) string { return blob.Digest([]byte(s)) }

func compileJob(sub int32, source string) *job.Job {
	return &job.Job{
		ID:                 fmt.Sprintf("compile-%d", sub),
		Operation:          job.Operation{Type: job.Compilation, ObjectID: sub, DatasetID: 1},
		Attempt:            1,
		Generation:         1,
		TaskType:           "Batch",
		TaskTypeParameters: json.RawMessage(`["alone", ["", ""], "diff"]`),
		Language:           "C++17 / g++",
		Files:              map[string]string{"sum.%l": digest(source)},
		Managers:           map[string]string{},
	}
}

func evalJob(sub int32, tc string) *job.Job {
	return &job.Job{
		ID:                 fmt.Sprintf("evaluate-%d-%s", sub, tc),
		Operation:          job.Operation{Type: job.Evaluation, ObjectID: sub, DatasetID: 1, TestcaseCodename: tc},
		Attempt:            1,
		Generation:         1,
		TaskType:           "Batch",
		TaskTypeParameters: json.RawMessage(`["alone", ["", ""], "diff"]`),
		Language:           "C++17 / g++",
		Files:              map[string]string{"sum.%l": digest("source")},
		Managers:           map[string]string{},
		Executables:        map[string]string{"sum": digest("exe")},
		Input:              digest("in " + tc),
		Output:             digest("out " + tc),
		TimeLimit:          1,
		MemoryLimit:        256 << 20,
	}
}

type harness struct {
	t             *testing.T
	kv            *kv.Client
	jobs, results *queue.Queue
	vis           time.Duration
}

func newHarness(t *testing.T, vis time.Duration) *harness {
	t.Helper()
	c := cmstest.Redis(t)
	jobs, results, err := openQueues(context.Background(), c, vis)
	if err != nil {
		t.Fatal(err)
	}
	return &harness{t: t, kv: c, jobs: jobs, results: results, vis: vis}
}

func (h *harness) enqueue(j *job.Job, prio int) {
	h.t.Helper()
	b, err := j.Encode()
	if err != nil {
		h.t.Fatal(err)
	}
	e, err := h.jobs.Enqueue(context.Background(), prio, b, queue.EnqueueOptions{DedupKey: j.DedupKey()})
	if err != nil || !e.Added {
		h.t.Fatalf("enqueue %s: %+v %v", j.ID, e, err)
	}
}

// result waits for the next result and acknowledges it.
func (h *harness) result(timeout time.Duration) *job.Result {
	h.t.Helper()
	msg, err := h.results.Consume(context.Background(), "dispatcher", timeout)
	if err != nil {
		h.t.Fatal(err)
	}
	if msg == nil {
		h.t.Fatalf("no result within %v", timeout)
	}
	if err := h.results.Ack(context.Background(), msg); err != nil {
		h.t.Fatal(err)
	}
	r, err := job.DecodeResult(msg.Payload)
	if err != nil {
		h.t.Fatalf("decoding the result: %v\n%s", err, msg.Payload)
	}
	return r
}

func (h *harness) noResult(d time.Duration) {
	h.t.Helper()
	msg, err := h.results.Consume(context.Background(), "dispatcher", d)
	if err != nil {
		h.t.Fatal(err)
	}
	if msg != nil {
		h.t.Fatalf("unexpected result: %s", msg.Payload)
	}
}

// idle checks that the job queue holds nothing: no ready, pending or
// delayed job.
func (h *harness) idle() {
	h.t.Helper()
	st, err := h.jobs.Stats(context.Background())
	if err != nil {
		h.t.Fatal(err)
	}
	if st.Ready+st.Pending+st.Delayed+st.DedupKeys != 0 {
		h.t.Errorf("job queue not empty: %+v", st)
	}
}

type runner struct {
	w       *Worker
	reg     *prometheus.Registry
	cancel  context.CancelFunc
	done    chan error
	stopped bool
}

func (h *harness) start(name string, exec Executor, opts Options) *runner {
	h.t.Helper()
	opts.Name = name
	if opts.Concurrency == 0 {
		opts.Concurrency = 1
	}
	if opts.Boxes == 0 {
		opts.Boxes = 10
	}
	if opts.Heartbeat == 0 {
		opts.Heartbeat = 50 * time.Millisecond
	}
	if opts.PollTimeout == 0 {
		opts.PollTimeout = 200 * time.Millisecond
	}
	reg := prometheus.NewRegistry()
	opts.Metrics = reg
	w, err := New(h.kv, h.jobs, h.results, exec, opts)
	if err != nil {
		h.t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &runner{w: w, reg: reg, cancel: cancel, done: make(chan error, 1)}
	go func() { r.done <- w.Run(ctx) }()
	h.t.Cleanup(func() {
		if !r.stopped {
			r.cancel()
			<-r.done
		}
	})
	return r
}

func (r *runner) stop(t *testing.T) error {
	t.Helper()
	r.cancel()
	select {
	case err := <-r.done:
		r.stopped = true
		return err
	case <-time.After(30 * time.Second):
		t.Fatal("the worker did not stop")
	}
	return nil
}

func (r *runner) count(kind, outcome string) float64 {
	return metricValue(r.w.m.jobs.WithLabelValues(kind, outcome))
}

// waitCount waits until the jobs counter of kind and outcome reaches want:
// the worker counts a job right after publishing its result, so a test that
// has just read the result may get there first.
func (r *runner) waitCount(t *testing.T, kind, outcome string, want float64) {
	t.Helper()
	waitFor(t, fmt.Sprintf("%s/%s = %v", kind, outcome, want), func() bool {
		return r.count(kind, outcome) == want
	})
	if n := r.count(kind, outcome); n != want {
		t.Errorf("%s/%s = %v, want %v", kind, outcome, n, want)
	}
}

// metricValue reads a counter or a gauge (prometheus/testutil needs a
// module this one does not require).
func metricValue(c prometheus.Collector) float64 {
	ch := make(chan prometheus.Metric, 1)
	c.Collect(ch)
	var m dto.Metric
	if err := (<-ch).Write(&m); err != nil {
		panic(err)
	}
	if m.Counter != nil {
		return m.Counter.GetValue()
	}
	return m.Gauge.GetValue()
}

// waitFor polls cond until it holds or 10 seconds pass.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// running returns the jobs the registry shows for a worker.
func (h *harness) running(name string) ([]registry.Job, bool) {
	l, err := registry.List(context.Background(), h.kv)
	if err != nil {
		h.t.Fatal(err)
	}
	for _, i := range l {
		if i.Name == name {
			return i.Jobs, true
		}
	}
	return nil, false
}

func TestJobsAreRunAndAcknowledged(t *testing.T) {
	h := newHarness(t, 5*time.Second)
	exec := &fakeExec{}
	r := h.start("judge-1", exec, Options{Concurrency: 2})
	jobs := []*job.Job{compileJob(1, "a"), evalJob(1, "tc1"), evalJob(1, "tc2")}
	for i, j := range jobs {
		h.enqueue(j, i%job.JobPriorities)
	}
	got := map[string]*job.Result{}
	for range jobs {
		res := h.result(10 * time.Second)
		got[res.JobID] = res
	}
	for _, j := range jobs {
		res := got[j.ID]
		switch {
		case res == nil:
			t.Fatalf("no result for %s", j.ID)
		case !res.Success || res.Worker != "judge-1" || res.Attempt != 1 || res.Generation != 1 || res.Operation != j.Operation:
			t.Errorf("%s: %+v", j.ID, res)
		case res.Durations.Total <= 0 && res.Durations.Wait <= 0:
			t.Errorf("%s: no durations: %+v", j.ID, res.Durations)
		}
	}
	if c := got["compile-1"]; c.CompilationOutcome != job.CompilationOK || len(c.Executables) != 1 {
		t.Errorf("compilation: %+v", c)
	}
	if e := got["evaluate-1-tc1"]; e.Outcome != "1.0" || e.Text[0] != "Output is correct" || e.Stats == nil {
		t.Errorf("evaluation: %+v", e)
	}
	h.noResult(100 * time.Millisecond)
	h.idle()
	r.waitCount(t, "evaluate", outcomeSuccess, 2)
	r.waitCount(t, "compile", outcomeSuccess, 1)
	if n := metricValue(r.w.m.busy); n != 0 {
		t.Errorf("busy slots = %v", n)
	}
	if err := r.stop(t); err != nil {
		t.Fatal(err)
	}
}

func TestQueueAttemptIsAuthoritative(t *testing.T) {
	h := newHarness(t, 5*time.Second)
	j := evalJob(2, "tc1")
	b, err := j.Encode()
	if err != nil {
		t.Fatal(err)
	}
	// The dispatcher enqueues the second attempt with the same payload.
	if _, err := h.jobs.Enqueue(context.Background(), 0, b, queue.EnqueueOptions{Attempt: 2}); err != nil {
		t.Fatal(err)
	}
	h.start("judge", &fakeExec{}, Options{})
	if res := h.result(10 * time.Second); res.Attempt != 2 {
		t.Errorf("attempt %d", res.Attempt)
	}
}

func TestFailuresArePublished(t *testing.T) {
	h := newHarness(t, 5*time.Second)
	exec := &fakeExec{fn: func(ctx context.Context, j *job.Job, res *job.Result) error {
		switch j.Operation.TestcaseCodename {
		case "broken":
			err := errors.New("isolate: box init failed")
			res.Fail(err)
			return err
		case "panic":
			res.Outcome = "1.0" // dropped by the failure
			panic("bug in a task type")
		}
		return (&fakeExec{}).succeed(ctx, j, res)
	}}
	r := h.start("judge", exec, Options{})
	h.enqueue(evalJob(3, "broken"), 0)
	h.enqueue(evalJob(3, "panic"), 0)
	for range 2 {
		res := h.result(10 * time.Second)
		if res.Success || res.Outcome != "" {
			t.Errorf("%s: %+v", res.JobID, res)
		}
		want := map[string]string{"evaluate-3-broken": "box init failed", "evaluate-3-panic": "worker panic: bug in a task type"}[res.JobID]
		if !strings.Contains(res.Error, want) {
			t.Errorf("%s: error %q, want %q", res.JobID, res.Error, want)
		}
	}
	h.idle()
	r.waitCount(t, "evaluate", outcomeFailure, 2)
	// The worker survived the panic.
	h.enqueue(evalJob(4, "tc1"), 0)
	if res := h.result(10 * time.Second); !res.Success {
		t.Errorf("after the panic: %+v", res)
	}
}

func TestJobTimeout(t *testing.T) {
	h := newHarness(t, 5*time.Second)
	exec := &fakeExec{fn: func(ctx context.Context, j *job.Job, res *job.Result) error {
		<-ctx.Done() // a job stuck until cancelled
		res.Fail(ctx.Err())
		return ctx.Err()
	}}
	h.start("judge", exec, Options{JobTimeout: 200 * time.Millisecond})
	j := compileJob(5, "loop")
	h.enqueue(j, 0)
	res := h.result(10 * time.Second)
	if res.Success || !strings.Contains(res.Error, "worker.job_timeout") {
		t.Errorf("%+v", res)
	}
	h.idle()
}

func TestInvalidPayloadIsDeadLettered(t *testing.T) {
	h := newHarness(t, 5*time.Second)
	r := h.start("judge", &fakeExec{}, Options{})
	for _, p := range []string{"not json", `{"v": 99, "id": "x"}`} {
		if _, err := h.jobs.Enqueue(context.Background(), 0, []byte(p), queue.EnqueueOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, "dead letters", func() bool {
		st, err := h.jobs.Stats(context.Background())
		return err == nil && st.Dead == 2
	})
	dead, err := h.jobs.ListDead(context.Background(), 10)
	if err != nil || len(dead) != 2 {
		t.Fatalf("dead letters: %+v %v", dead, err)
	}
	for _, d := range dead {
		if !strings.Contains(d.Reason, "cannot read the job") {
			t.Errorf("dead letter reason %q", d.Reason)
		}
	}
	h.noResult(50 * time.Millisecond)
	// Two attempts each (MaxAttempts 2).
	r.waitCount(t, "unknown", outcomeInvalid, 4)
}

func TestBoxesAreReserved(t *testing.T) {
	h := newHarness(t, 5*time.Second)
	const boxes = 3
	var inUse, peak, bigRunning atomic.Int64
	exec := &fakeExec{
		boxes: func(j *job.Job) int {
			switch {
			case strings.HasPrefix(j.Operation.TestcaseCodename, "big"):
				return 3
			case strings.HasPrefix(j.Operation.TestcaseCodename, "huge"):
				return 50 // more than the worker has: runs alone
			}
			return 1
		},
	}
	exec.fn = func(ctx context.Context, j *job.Job, res *job.Result) error {
		n := int64(min(exec.Boxes(j), boxes))
		if v := inUse.Add(n); v > peak.Load() {
			peak.Store(v)
		}
		if n == boxes {
			bigRunning.Add(1)
		}
		time.Sleep(30 * time.Millisecond)
		inUse.Add(-n)
		return exec.succeed(ctx, j, res)
	}
	h.start("judge", exec, Options{Concurrency: 3, Boxes: boxes})
	for i, tc := range []string{"a", "big1", "b", "c", "huge", "d", "big2", "e"} {
		h.enqueue(evalJob(int32(10+i), tc), 0)
	}
	for range 8 {
		if res := h.result(10 * time.Second); !res.Success {
			t.Errorf("%+v", res)
		}
	}
	if p := peak.Load(); p > boxes {
		t.Errorf("%d boxes in use at once, the worker has %d", p, boxes)
	}
	if bigRunning.Load() != 3 {
		t.Errorf("big jobs: %d", bigRunning.Load())
	}
}

func TestRegistryShowsRunningJobs(t *testing.T) {
	h := newHarness(t, 5*time.Second)
	release := make(chan struct{})
	exec := &fakeExec{fn: func(ctx context.Context, j *job.Job, res *job.Result) error {
		<-release
		return (&fakeExec{}).succeed(ctx, j, res)
	}}
	r := h.start("judge-7", exec, Options{Concurrency: 2, Host: "box1", Version: "v-test", Languages: []string{"C++17 / g++"}})
	waitFor(t, "registration", func() bool { _, ok := h.running("judge-7"); return ok })
	j := evalJob(20, "tc3")
	h.enqueue(j, 0)
	var jobs []registry.Job
	waitFor(t, "the running job", func() bool { jobs, _ = h.running("judge-7"); return len(jobs) == 1 })
	if jobs[0].ID != j.ID || jobs[0].Operation != j.Operation || jobs[0].Description != j.Describe() || jobs[0].StartedAt.IsZero() {
		t.Errorf("running: %+v", jobs[0])
	}
	l, _ := registry.List(context.Background(), h.kv)
	if len(l) != 1 || l[0].Host != "box1" || l[0].Version != "v-test" || l[0].Concurrency != 2 || len(l[0].Languages) != 1 {
		t.Errorf("entry: %+v", l)
	}
	if b := metricValue(r.w.m.busy); b != 1 {
		t.Errorf("busy = %v", b)
	}
	close(release)
	h.result(10 * time.Second)
	waitFor(t, "idle", func() bool {
		l, _ := registry.List(context.Background(), h.kv)
		return len(l) == 1 && len(l[0].Jobs) == 0 && l[0].Done == 1
	})
	if err := r.stop(t); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.running("judge-7"); ok {
		t.Error("the worker is still registered after a clean shutdown")
	}
}

func TestNameTaken(t *testing.T) {
	h := newHarness(t, 5*time.Second)
	h.start("judge", &fakeExec{}, Options{})
	waitFor(t, "registration", func() bool { _, ok := h.running("judge"); return ok })
	r := h.start("judge", &fakeExec{}, Options{RegisterWait: 200 * time.Millisecond})
	select {
	case err := <-r.done:
		r.stopped = true
		if !errors.Is(err, registry.ErrNameTaken) {
			t.Fatalf("second worker: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the second worker with the same name started")
	}
}

func TestShutdownFinishesRunningJob(t *testing.T) {
	h := newHarness(t, 5*time.Second)
	started := make(chan struct{})
	exec := &fakeExec{fn: func(ctx context.Context, j *job.Job, res *job.Result) error {
		close(started)
		time.Sleep(300 * time.Millisecond)
		return (&fakeExec{}).succeed(ctx, j, res)
	}}
	r := h.start("judge", exec, Options{ShutdownTimeout: 10 * time.Second})
	h.enqueue(evalJob(30, "tc1"), 0)
	<-started
	if err := r.stop(t); err != nil {
		t.Fatal(err)
	}
	// The job ended and its result was published before Run returned.
	res := h.result(time.Second)
	if !res.Success || res.Worker != "judge" {
		t.Errorf("%+v", res)
	}
	h.idle()
	// A stopped worker takes no new job.
	h.enqueue(evalJob(31, "tc1"), 0)
	h.noResult(300 * time.Millisecond)
	if exec.ncalls() != 1 {
		t.Errorf("%d calls", exec.ncalls())
	}
}

func TestShutdownGivesBackUnfinishedJob(t *testing.T) {
	h := newHarness(t, time.Minute) // without the give-back the job would wait a minute
	started := make(chan struct{})
	slow := &fakeExec{fn: func(ctx context.Context, j *job.Job, res *job.Result) error {
		close(started)
		<-ctx.Done()
		res.Fail(ctx.Err())
		return ctx.Err()
	}}
	r1 := h.start("judge-1", slow, Options{ShutdownTimeout: 100 * time.Millisecond})
	j := evalJob(40, "tc1")
	h.enqueue(j, 0)
	<-started
	start := time.Now()
	if err := r1.stop(t); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("stopping took %v", d)
	}
	r1.waitCount(t, "evaluate", outcomeRequeued, 1)
	h.noResult(50 * time.Millisecond) // no result from the stopped worker

	r2 := h.start("judge-2", &fakeExec{}, Options{})
	res := h.result(5 * time.Second)
	if !res.Success || res.Worker != "judge-2" || res.JobID != j.ID || res.Attempt != 1 {
		t.Errorf("%+v", res)
	}
	h.noResult(100 * time.Millisecond)
	h.idle()
	_ = r2
}

// startHelper runs a worker in another process (helperMain) whose jobs
// never end, and waits until it holds a job.
func (h *harness) startHelper(name string) *exec.Cmd {
	h.t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(),
		helperEnv+"="+name,
		helperRedisEnv+"="+cmstest.RedisURL(h.t),
		helperPrefixEnv+"="+h.kv.Prefix(),
		helperVisibilityEnv+"="+h.vis.String(),
	)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	waitFor(h.t, "the helper to take the job", func() bool {
		jobs, _ := h.running(name)
		return len(jobs) == 1
	})
	return cmd
}

func kill(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if err := cmd.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
}

// A worker killed in the middle of a job (kill -9) loses nothing: when it
// restarts under the same name it gives the job back at once.
func TestKilledWorkerRestartReleasesItsJob(t *testing.T) {
	h := newHarness(t, time.Minute) // the visibility timeout plays no part
	j := evalJob(50, "tc1")
	h.enqueue(j, 0)
	cmd := h.startHelper("judge-crash")
	kill(t, cmd)
	h.noResult(50 * time.Millisecond)

	start := time.Now()
	exec := &fakeExec{}
	h.start("judge-crash", exec, Options{})
	res := h.result(10 * time.Second)
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("the job came back after %v", d)
	}
	if !res.Success || res.JobID != j.ID || res.Worker != "judge-crash" {
		t.Errorf("%+v", res)
	}
	h.noResult(200 * time.Millisecond)
	h.idle()
	if exec.ncalls() != 1 {
		t.Errorf("%d executions", exec.ncalls())
	}
}

// Another worker takes the job of a killed worker after the visibility
// timeout.
func TestKilledWorkerJobGoesToAnotherWorker(t *testing.T) {
	h := newHarness(t, 500*time.Millisecond)
	j := compileJob(60, "x")
	h.enqueue(j, 0)
	cmd := h.startHelper("judge-crash")
	// While the helper lives it keeps the job: nobody else gets it.
	exec := &fakeExec{}
	h.start("judge-other", exec, Options{})
	h.noResult(time.Second)
	kill(t, cmd)

	res := h.result(10 * time.Second)
	if !res.Success || res.JobID != j.ID || res.Worker != "judge-other" {
		t.Errorf("%+v", res)
	}
	h.noResult(200 * time.Millisecond)
	h.idle()
	if exec.ncalls() != 1 {
		t.Errorf("%d executions", exec.ncalls())
	}
}

func TestNameFromHost(t *testing.T) {
	for host, want := range map[string]string{
		"judge1.example.org":    "judge1.example.org",
		"my host":               "my-host",
		"-weird":                "weird",
		"":                      "worker",
		strings.Repeat("a", 70): strings.Repeat("a", 64),
	} {
		if got := NameFromHost(host); got != want {
			t.Errorf("%q: %q, want %q", host, got, want)
		}
	}
}

func TestNoJobsUntilReady(t *testing.T) {
	h := newHarness(t, 5*time.Second)
	var checks atomic.Int64
	ready := func(ctx context.Context) error {
		if checks.Add(1) < 3 {
			return errors.New("isolate: cannot open /run/isolate/cgroup")
		}
		return nil
	}
	exec := &fakeExec{}
	gate := make(chan struct{})
	slowReady := func(ctx context.Context) error {
		<-gate
		return ready(ctx)
	}
	r := h.start("judge", exec, Options{Ready: slowReady, ReadyRetry: 100 * time.Millisecond})
	if err := r.w.Health(context.Background()); err == nil {
		t.Error("healthy before the self-test")
	}
	h.enqueue(evalJob(70, "tc1"), 0)
	close(gate)
	waitFor(t, "a failed self-test", func() bool {
		l, _ := registry.List(context.Background(), h.kv)
		return len(l) == 1 && strings.Contains(l[0].Error, "cgroup")
	})
	if err := r.w.Health(context.Background()); err == nil || !strings.Contains(err.Error(), "cgroup") {
		t.Errorf("health: %v", err)
	}
	if exec.ncalls() != 0 {
		t.Error("a job ran before the sandbox was ready")
	}
	res := h.result(10 * time.Second)
	if !res.Success || checks.Load() != 3 {
		t.Errorf("%d checks, %+v", checks.Load(), res)
	}
	if err := r.w.Health(context.Background()); err != nil {
		t.Errorf("health after the self-test: %v", err)
	}
	waitFor(t, "the error to clear", func() bool {
		l, _ := registry.List(context.Background(), h.kv)
		return len(l) == 1 && l[0].Error == ""
	})
	// A worker that never becomes ready still stops cleanly.
	r2 := h.start("judge-2", &fakeExec{}, Options{Ready: func(context.Context) error { return errors.New("broken") }})
	waitFor(t, "registration", func() bool { _, ok := h.running("judge-2"); return ok })
	if err := r2.stop(t); err != nil {
		t.Fatal(err)
	}
}

// BenchmarkWorkerOverhead measures what the worker adds to each job
// (taking it, keeping it alive, publishing the result and acknowledging
// it) with a task type that does nothing, four slots.
func BenchmarkWorkerOverhead(b *testing.B) {
	c := cmstest.Redis(b)
	ctx := context.Background()
	jobs, results, err := openQueues(ctx, c, 30*time.Second)
	if err != nil {
		b.Fatal(err)
	}
	items := make([]queue.Item, b.N)
	for i := range items {
		j := evalJob(int32(i+1), "tc1")
		p, err := j.Encode()
		if err != nil {
			b.Fatal(err)
		}
		items[i] = queue.Item{Priority: job.PriorityMedium, Payload: p, Options: queue.EnqueueOptions{DedupKey: j.DedupKey()}}
	}
	if _, err := jobs.EnqueueBatch(ctx, items); err != nil {
		b.Fatal(err)
	}
	w, err := New(c, jobs, results, &fakeExec{}, Options{Name: "bench", Concurrency: 4, Boxes: 4,
		PollTimeout: 200 * time.Millisecond})
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	wctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- w.Run(wctx) }()
	for n := 0; n < b.N; {
		msg, err := results.Consume(ctx, "dispatcher", 10*time.Second)
		if err != nil || msg == nil {
			b.Fatalf("result %d: %v", n, err)
		}
		if err := results.Ack(ctx, msg); err != nil {
			b.Fatal(err)
		}
		n++
	}
	b.StopTimer()
	cancel()
	if err := <-done; err != nil {
		b.Fatal(err)
	}
}
