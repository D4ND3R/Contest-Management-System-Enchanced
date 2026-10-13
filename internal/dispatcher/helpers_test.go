// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package dispatcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/db"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/kv"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/queue"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
)

// t0 is the start of the test contests.
var t0 = time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)

// env is a test installation: a database, a Valkey namespace, the two
// queues, recorded live events and scoreboard updates.
type env struct {
	t       testing.TB
	ctx     context.Context
	pool    *pgxpool.Pool
	qc      *db.QueryCounter
	kv      *kv.Client
	jobs    *queue.Queue
	results *queue.Queue
	live    *recorder
	scores  *scoreRecorder
	log     *slog.Logger

	contest int32
	group   int32
	n       int
}

func newEnv(t testing.TB) *env {
	t.Helper()
	pool, qc := testutil.CountingDB(t)
	c := testutil.Redis(t)
	e := &env{t: t, ctx: context.Background(), pool: pool, qc: qc, kv: c, live: &recorder{}, scores: &scoreRecorder{}}
	if os.Getenv("CMS_TEST_DISPATCHER_LOG") != "" {
		e.log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	} else {
		e.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	var err error
	jo := JobQueueOptions
	jo.BackoffBase, jo.BackoffMax, jo.PollInterval = 20*time.Millisecond, 100*time.Millisecond, 20*time.Millisecond
	if e.jobs, err = queue.Open(e.ctx, c, job.JobQueue, jo); err != nil {
		t.Fatal(err)
	}
	ro := ResultQueueOptions
	ro.PollInterval = 20 * time.Millisecond
	if e.results, err = queue.Open(e.ctx, c, job.ResultQueue, ro); err != nil {
		t.Fatal(err)
	}
	e.contest = e.id(`INSERT INTO contests (name, description, languages) VALUES ('ioi', 'IOI', '{"C++17 / g++"}') RETURNING id`)
	e.group = e.id(`INSERT INTO groups (name, contest_id, start, stop) VALUES ('main', $1, $2, $3) RETURNING id`,
		e.contest, t0, t0.Add(5*time.Hour))
	e.exec(`UPDATE contests SET main_group_id = $1 WHERE id = $2`, e.group, e.contest)
	return e
}

func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.pool.Exec(e.ctx, sql, args...); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
}

func (e *env) id(sql string, args ...any) int32 {
	e.t.Helper()
	var id int32
	if err := e.pool.QueryRow(e.ctx, sql, args...).Scan(&id); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
	return id
}

func scalar[T any](e *env, sql string, args ...any) T {
	e.t.Helper()
	var v T
	if err := e.pool.QueryRow(e.ctx, sql, args...).Scan(&v); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
	return v
}

// task creates a Batch task with its active dataset of n testcases (each
// worth 100/n with the Sum score type).
func (e *env) task(n int) (task, dataset int32) {
	e.t.Helper()
	e.n++
	name := fmt.Sprintf("task%d", e.n)
	task = e.id(`INSERT INTO tasks (num, contest_id, name, title, submission_format, score_mode)
		VALUES ($1, $2, $3, $3, $4, 'max') RETURNING id`, e.n, e.contest, name, []string{name + ".%l"})
	dataset = e.dataset(task, n, false)
	e.exec(`UPDATE tasks SET active_dataset_id = $1 WHERE id = $2`, dataset, task)
	return task, dataset
}

// dataset adds a dataset of n testcases to a task.
func (e *env) dataset(task int32, n int, autojudge bool) int32 {
	e.t.Helper()
	e.n++
	ds := e.id(`INSERT INTO datasets (task_id, description, autojudge, time_limit, memory_limit,
			task_type, task_type_parameters, score_type, score_type_parameters)
		VALUES ($1, $2, $3, 1, 268435456, 'Batch', '["alone", ["", ""], "diff"]', 'Sum', $4) RETURNING id`,
		task, fmt.Sprintf("dataset %d", e.n), autojudge, fmt.Sprint(100.0/float64(n)))
	e.exec(`INSERT INTO testcases (dataset_id, codename, public, input, output)
		SELECT $1, lpad(g::text, 3, '0'), g = 1, $2, $3 FROM generate_series(1, $4::integer) g`,
		ds, digestOf("input"), digestOf("output"), n)
	return ds
}

// participant creates a user and their participation.
func (e *env) participant(username string) int32 {
	e.t.Helper()
	u := e.id(`INSERT INTO users (first_name, last_name, username, password) VALUES ($1, $1, $1, 'plaintext:x') RETURNING id`, username)
	return e.id(`INSERT INTO participations (contest_id, user_id, group_id) VALUES ($1, $2, $3) RETURNING id`,
		e.contest, u, e.group)
}

// submit creates submissions of p to task (without notifying).
func (e *env) submit(p, task int32, count int) []int32 {
	e.t.Helper()
	var ids []int32
	for range count {
		e.n++
		s := e.id(`INSERT INTO submissions (opaque_id, participation_id, task_id, timestamp, language)
			VALUES ($1, $2, $3, $4, 'C++17 / g++') RETURNING id`, e.n, p, task, time.Now())
		e.exec(`INSERT INTO files (submission_id, filename, digest)
			SELECT $1, submission_format[1], $2 FROM tasks WHERE id = $3`, s, digestOf(fmt.Sprint("source ", e.n)), task)
		ids = append(ids, s)
	}
	return ids
}

// userTest creates a user test of p on task (without notifying).
func (e *env) userTest(p, task int32) int32 {
	e.t.Helper()
	e.n++
	u := e.id(`INSERT INTO user_tests (participation_id, task_id, timestamp, language, input)
		VALUES ($1, $2, $3, 'C++17 / g++', $4) RETURNING id`, p, task, time.Now(), digestOf(fmt.Sprint("input ", e.n)))
	e.exec(`INSERT INTO user_test_files (user_test_id, filename, digest)
		SELECT $1, submission_format[1], $2 FROM tasks WHERE id = $3`, u, digestOf(fmt.Sprint("source ", e.n)), task)
	return u
}

// notify sends events to the dispatcher.
func (e *env) notify(evs ...judging.Event) {
	e.t.Helper()
	if err := judging.Notify(e.ctx, e.kv, evs...); err != nil {
		e.t.Fatal(err)
	}
}

func digestOf(s string) string { return blob.Digest([]byte(s)) }

// judge returns a judging core on the test database.
func (e *env) judge() *judging.Judge {
	return judging.New(e.pool, judging.Options{Logger: e.log})
}

// options are the dispatcher options of the tests: fast lease, loop and
// sweeps.
func (e *env) options(instance string) Options {
	return Options{
		Instance:           instance,
		SweepInterval:      time.Hour,
		IdleWait:           10 * time.Millisecond,
		LeaseTTL:           2 * time.Second,
		LeaseRetry:         20 * time.Millisecond,
		DeadLetterInterval: 50 * time.Millisecond,
		ProgressInterval:   50 * time.Millisecond,
		Live:               e.live,
		Scores:             e.scores,
		Log:                e.log.With("dispatcher", instance),
		Metrics:            prometheus.NewRegistry(),
	}
}

// running is a dispatcher running in the background.
type running struct {
	d      *Dispatcher
	cancel context.CancelFunc
	done   chan error
}

// start runs a dispatcher until the test ends (or stop).
func (e *env) start(opts Options) *running {
	e.t.Helper()
	d, err := New(e.pool, e.kv, e.jobs, e.results, judging.New(e.pool, judging.Options{Logger: opts.Log}), opts)
	if err != nil {
		e.t.Fatal(err)
	}
	return e.run(d)
}

func (e *env) run(d *Dispatcher) *running {
	ctx, cancel := context.WithCancel(e.ctx)
	r := &running{d: d, cancel: cancel, done: make(chan error, 1)}
	go func() { r.done <- d.Run(ctx) }()
	e.t.Cleanup(r.stop)
	return r
}

// stop stops the dispatcher and waits for it.
func (r *running) stop() {
	r.cancel()
	<-r.done
	r.done <- nil // stop is idempotent
}

// --- simulated workers ---

// verdict decides the result of a job: a compilation outcome or an
// evaluation outcome, "" for an infrastructure failure.
type verdict func(j *job.Job) string

func allCorrect(j *job.Job) string {
	if j.Operation.Type.IsCompilation() {
		return job.CompilationOK
	}
	return "1.0"
}

// resultOf is what a worker returns for j with the verdict v.
func resultOf(j *job.Job, worker, v string) *job.Result {
	r := j.NewResult(worker)
	r.Durations.Total = time.Millisecond
	if v == "" {
		r.Fail(errors.New("sandbox broken"))
		return r
	}
	r.Success = true
	r.Stats = &job.Stats{ExecutionTime: job.Ptr(0.25), ExecutionWallClockTime: job.Ptr(0.5),
		ExecutionMemory: job.Ptr(int64(8 << 20)), ExitStatus: job.ExitOK}
	switch j.Operation.Type {
	case job.Compilation, job.UserTestCompilation:
		r.CompilationOutcome = v
		if v == job.CompilationOK {
			r.Text = []string{"Compilation succeeded"}
			r.Executables = map[string]string{"main": digestOf(j.Operation.Key() + "@" + fmt.Sprint(j.Generation))}
		} else {
			r.Text = []string{"Compilation failed"}
		}
	case job.Evaluation:
		r.Outcome = v
		r.Text = []string{"Output is correct"}
		if v != "1.0" {
			r.Text = []string{"Output isn't correct"}
		}
	case job.UserTestEvaluation:
		r.Text = []string{"Execution completed successfully"}
		r.UserOutput = digestOf("output of " + j.Operation.Key())
	}
	return r
}

// workers are simulated workers on the job queue: they decide results
// with a verdict, count how many times each job ran and when results were
// published.
type workers struct {
	e       *env
	verdict verdict
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	mu sync.Mutex
	// runs counts the executions of each job (its dedup key).
	runs map[string]int
	// ops are the operations run, in order.
	ops []job.Operation
	// lastResult is when the last result of each submission was
	// published.
	lastResult map[int32]time.Time
}

// startWorkers starts n simulated workers.
func (e *env) startWorkers(n int, v verdict) *workers {
	ctx, cancel := context.WithCancel(e.ctx)
	w := &workers{e: e, verdict: v, cancel: cancel, runs: map[string]int{}, lastResult: map[int32]time.Time{}}
	for i := range n {
		w.wg.Add(1)
		go w.loop(ctx, fmt.Sprintf("fake/%d", i))
	}
	e.t.Cleanup(w.stop)
	return w
}

func (w *workers) stop() {
	w.cancel()
	w.wg.Wait()
}

func (w *workers) loop(ctx context.Context, name string) {
	defer w.wg.Done()
	for ctx.Err() == nil {
		msg, err := w.e.jobs.Consume(ctx, name, 50*time.Millisecond)
		if err != nil || msg == nil {
			continue
		}
		jb, err := job.DecodeJob(msg.Payload)
		if err != nil {
			w.e.t.Errorf("worker %s: %v", name, err)
			continue
		}
		v := w.verdict(jb)
		res := resultOf(jb, name, v)
		payload, err := res.Encode()
		if err != nil {
			w.e.t.Errorf("worker %s: %v", name, err)
			continue
		}
		w.mu.Lock()
		w.runs[jb.DedupKey()]++
		w.ops = append(w.ops, jb.Operation)
		if jb.Operation.Type.ForSubmission() {
			w.lastResult[jb.Operation.ObjectID] = time.Now()
		}
		w.mu.Unlock()
		if _, err := w.e.jobs.AckAndEnqueue(context.WithoutCancel(ctx), msg, w.e.results, 0, payload, queue.EnqueueOptions{}); err != nil {
			w.e.t.Errorf("worker %s: %v", name, err)
		}
	}
}

// ranTwice returns the jobs that ran more than once.
func (w *workers) ranTwice() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []string
	for k, n := range w.runs {
		if n > 1 {
			out = append(out, fmt.Sprintf("%s×%d", k, n))
		}
	}
	slices.Sort(out)
	return out
}

func (w *workers) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.ops)
}

// --- recorders ---

// recorder is a live.Sink that keeps the events with their time.
type recorder struct {
	mu     sync.Mutex
	events []stamped
}

type stamped struct {
	at time.Time
	ev live.Event
}

func (r *recorder) Publish(evs ...live.Event) {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ev := range evs {
		r.events = append(r.events, stamped{now, ev})
	}
}

func (r *recorder) all() []stamped {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.events)
}

// submissionStatuses returns the statuses published for a submission on
// a dataset, in order.
func (r *recorder) submissionStatuses(sub, dataset int32) []string {
	var out []string
	for _, s := range r.all() {
		if p, ok := s.ev.Data.(live.Submission); ok && p.ID == int64(sub) && p.Dataset == int64(dataset) {
			out = append(out, p.Status)
		}
	}
	return out
}

// scoreRecorder is a ScoreSink that keeps the updates.
type scoreRecorder struct {
	mu      sync.Mutex
	updates []ScoreUpdate
}

func (s *scoreRecorder) Push(_ context.Context, u ScoreUpdate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updates = append(s.updates, u)
}

func (s *scoreRecorder) all() []ScoreUpdate {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.updates)
}

// --- waiting ---

// eventually waits until cond holds, failing the test after timeout.
func (e *env) eventually(timeout time.Duration, what string, cond func() bool) {
	e.t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			e.t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// scored counts the scored results on a dataset.
func (e *env) scored(dataset int32) int {
	return int(scalar[int64](e, `SELECT count(*) FROM submission_results WHERE dataset_id = $1 AND score IS NOT NULL
		AND score_details IS NOT NULL AND public_score IS NOT NULL AND public_score_details IS NOT NULL
		AND ranking_score_details IS NOT NULL`, dataset))
}

// waitScored waits until n results of dataset are scored.
func (e *env) waitScored(dataset int32, n int, timeout time.Duration) {
	e.t.Helper()
	e.eventually(timeout, fmt.Sprintf("%d scored results on dataset %d", n, dataset), func() bool {
		return e.scored(dataset) >= n
	})
}

// result is the state of a submission result.
type result struct {
	CompilationOutcome *string
	CompilationTries   int32
	EvaluationOutcome  *string
	EvaluationTries    int32
	Generation         int64
	Score              *float64
	Evaluations        int
}

func (e *env) result(sub, ds int32) *result {
	e.t.Helper()
	var r result
	err := e.pool.QueryRow(e.ctx, `SELECT compilation_outcome, compilation_tries, evaluation_outcome, evaluation_tries,
			generation, score,
			(SELECT count(*) FROM evaluations e WHERE e.submission_id = sr.submission_id AND e.dataset_id = sr.dataset_id)
		FROM submission_results sr WHERE submission_id = $1 AND dataset_id = $2`, sub, ds).Scan(
		&r.CompilationOutcome, &r.CompilationTries, &r.EvaluationOutcome, &r.EvaluationTries,
		&r.Generation, &r.Score, &r.Evaluations)
	if err != nil {
		return nil
	}
	return &r
}

// counter reads a counter of a dispatcher's registry.
func counter(t testing.TB, reg prometheus.Registerer, name string, labels ...string) float64 {
	t.Helper()
	g, ok := reg.(prometheus.Gatherer)
	if !ok {
		t.Fatal("not a gatherer")
	}
	mfs, err := g.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var total float64
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
	metric:
		for _, m := range mf.GetMetric() {
			for i := 0; i+1 < len(labels); i += 2 {
				found := false
				for _, lp := range m.GetLabel() {
					if lp.GetName() == labels[i] && lp.GetValue() == labels[i+1] {
						found = true
					}
				}
				if !found {
					continue metric
				}
			}
			switch {
			case m.Counter != nil:
				total += m.Counter.GetValue()
			case m.Gauge != nil:
				total += m.Gauge.GetValue()
			case m.Histogram != nil:
				total += float64(m.Histogram.GetSampleCount())
			}
		}
	}
	return total
}

// histogramSum is the sum of the observations of a histogram.
func histogramSum(t testing.TB, reg prometheus.Registerer, name string) float64 {
	t.Helper()
	mfs, err := reg.(prometheus.Gatherer).Gather()
	if err != nil {
		t.Fatal(err)
	}
	var sum float64
	for _, mf := range mfs {
		if mf.GetName() == name {
			for _, m := range mf.GetMetric() {
				sum += m.GetHistogram().GetSampleSum()
			}
		}
	}
	return sum
}
