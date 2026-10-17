// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// The handling of a job follows execute_job_group in cms/service/Worker.py
// of CMS:
// Copyright © 2010-2014 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2013-2016 Luca Wehrstedt <luca.wehrstedt@gmail.com>

package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/sync/semaphore"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/tasktype"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/kv"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/queue"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/worker/registry"
)

// Executor runs jobs: the task types in production (TaskTypes), fakes in
// tests.
type Executor interface {
	// Execute runs j and fills res (made by j.NewResult) like
	// tasktype.Execute: res is always left valid, and an error means the
	// job failed (res.Success is false). It must stop when ctx ends.
	Execute(ctx context.Context, j *job.Job, res *job.Result) error
	// Boxes is the largest number of sandbox boxes j holds at the same
	// time.
	Boxes(j *job.Job) int
}

// TaskTypes runs jobs with their task types (tasktype.Execute) in env.
func TaskTypes(env *tasktype.Env) Executor { return taskTypes{env} }

type taskTypes struct{ env *tasktype.Env }

func (t taskTypes) Execute(ctx context.Context, j *job.Job, res *job.Result) error {
	return tasktype.Execute(ctx, t.env, j, res)
}

func (t taskTypes) Boxes(j *job.Job) int {
	if j.Operation.Type.IsCompilation() {
		return 1 // every task type compiles in a single box
	}
	tt, err := tasktype.Get(j.TaskType, j.TaskTypeParameters)
	if err != nil {
		return 1 // Execute reports the error
	}
	return tt.Boxes()
}

// Options configures a Worker. Zero durations mean their default.
type Options struct {
	// Name identifies the worker (config.ValidWorkerName): its entry in the
	// registry and its queue consumers ("<name>/<slot>").
	Name string
	// Host (default: the hostname), Version and Languages are shown on
	// the judges page; Host also tells a restart of this worker on the
	// same machine from another worker using the same name.
	Host      string
	Version   string
	Languages []string
	// Concurrency is the number of jobs run in parallel (job slots).
	Concurrency int
	// Boxes is the number of sandbox boxes the slots share
	// (worker.box_count): a job starts once the boxes it may need
	// (Executor.Boxes) are free, so parallel jobs never wait for each
	// other's boxes halfway. A job that needs more than Boxes fails at
	// once.
	Boxes int
	// ShutdownTimeout is how long Run lets running jobs finish once its
	// context ends (default 8s; worker.shutdown_timeout).
	ShutdownTimeout time.Duration
	// JobTimeout bounds a job (default 10m; worker.job_timeout);
	// evaluations get four times their time limit on top.
	JobTimeout time.Duration
	// RegisterWait bounds how long Run waits for its name to be released
	// by a previous process (default: the registry TTL plus 5s).
	RegisterWait time.Duration
	// Heartbeat is how often the registry entry is refreshed (default
	// registry.Interval). It is also refreshed, at most every 250ms, when
	// a job starts or ends.
	Heartbeat time.Duration
	// PollTimeout is how long a slot waits for a job in one call to
	// queue.Consume (default 5s).
	PollTimeout time.Duration
	// Cache is the compilation cache (nil: none).
	Cache *Cache
	// Ready checks that jobs can run (the sandbox self-test). The worker
	// takes no job until it succeeds: it is retried with a growing delay
	// of up to ReadyRetry (default 30s), and its error is reported by
	// Health and on the judges page. Nil means always ready.
	Ready      func(ctx context.Context) error
	ReadyRetry time.Duration
	// Log and Metrics may be nil.
	Log     *slog.Logger
	Metrics prometheus.Registerer
}

func (o *Options) defaults() error {
	def := func(v *time.Duration, d time.Duration) {
		if *v <= 0 {
			*v = d
		}
	}
	def(&o.ShutdownTimeout, 8*time.Second)
	def(&o.JobTimeout, 10*time.Minute)
	def(&o.RegisterWait, registry.TTL+5*time.Second)
	def(&o.Heartbeat, registry.Interval)
	def(&o.PollTimeout, 5*time.Second)
	def(&o.ReadyRetry, 30*time.Second)
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Host == "" {
		o.Host, _ = os.Hostname()
	}
	switch {
	case !config.ValidWorkerName(o.Name):
		return fmt.Errorf("worker: invalid name %q", o.Name)
	case o.Concurrency < 1:
		return errors.New("worker: concurrency must be at least 1")
	case o.Boxes < o.Concurrency:
		return fmt.Errorf("worker: %d boxes for %d slots", o.Boxes, o.Concurrency)
	}
	return nil
}

// Worker takes jobs from the job queue, runs them and publishes their
// results (R2.6). Every job is acknowledged in the same step that
// publishes its result (queue.AckAndEnqueue), so a job is never lost: if
// the worker dies before, another worker gets it.
type Worker struct {
	opts    Options
	kv      *kv.Client
	jobs    *queue.Queue
	results *queue.Queue
	exec    Executor
	boxes   *semaphore.Weighted
	log     *slog.Logger
	m       *metrics
	started time.Time
	kick    chan struct{}
	// ready is closed once Options.Ready succeeds.
	ready chan struct{}

	mu           sync.Mutex
	running      map[int]registry.Job
	draining     bool
	disabled     bool
	readyErr     error
	done, failed int64
}

// New returns a worker that consumes jobs and publishes results on the
// given queues (job.JobQueue and job.ResultQueue, on the same Valkey).
func New(c *kv.Client, jobs, results *queue.Queue, exec Executor, opts Options) (*Worker, error) {
	if err := opts.defaults(); err != nil {
		return nil, err
	}
	w := &Worker{
		opts: opts, kv: c, jobs: jobs, results: results, exec: exec,
		boxes:   semaphore.NewWeighted(int64(opts.Boxes)),
		log:     opts.Log,
		m:       newMetrics(opts.Metrics),
		kick:    make(chan struct{}, 1),
		ready:   make(chan struct{}),
		running: map[int]registry.Job{},
	}
	if opts.Ready == nil {
		close(w.ready)
	} else {
		w.readyErr = errors.New("the sandbox self-test has not run yet")
	}
	w.m.slots.Set(float64(opts.Concurrency))
	return w, nil
}

// Errors that end a job's context.
var (
	errJobTimeout = errors.New("worker: job timeout")
	errShutdown   = errors.New("worker: stopped before the job ended")
)

// Durations of the bookkeeping operations.
const (
	opTimeout      = 5 * time.Second  // one Valkey operation outside a job
	publishTimeout = 30 * time.Second // publishing a result, with retries
	stopWait       = 10 * time.Second // jobs ignoring their cancellation
	minRefresh     = 250 * time.Millisecond
)

// Run registers the worker and runs its job slots until ctx ends. It then
// stops taking jobs, lets the running ones finish for ShutdownTimeout and
// gives the others back to the queue. It returns an error if the
// registration fails or another process takes the worker's name.
func (w *Worker) Run(ctx context.Context) error {
	w.started = time.Now()
	reg, err := registry.Register(ctx, w.kv, w.info(), w.opts.RegisterWait)
	if err != nil {
		return err
	}
	defer func() {
		cctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		if err := reg.Close(cctx); err != nil {
			w.log.Warn("cannot remove the worker from the registry", "err", err)
		}
	}()
	// Jobs held by the previous process with this name (it crashed or was
	// killed) go back to the queue now rather than after the visibility
	// timeout.
	w.releaseHeld(ctx, true)
	w.checkDisabled(ctx)
	w.log.Info("worker started", "name", w.opts.Name, "concurrency", w.opts.Concurrency, "boxes", w.opts.Boxes)

	// hard ends the running jobs: ShutdownTimeout after ctx ends.
	hard, stopJobs := context.WithCancelCause(context.WithoutCancel(ctx))
	defer stopJobs(nil)
	consume, stopConsuming := context.WithCancel(ctx)
	defer stopConsuming()

	beatCtx, stopBeat := context.WithCancel(context.WithoutCancel(ctx))
	beatDone := make(chan error, 1)
	go func() { beatDone <- w.heartbeat(beatCtx, reg) }()

	go w.checkReady(consume)
	var wg sync.WaitGroup
	for slot := range w.opts.Concurrency {
		wg.Go(func() { w.slot(consume, hard, slot) })
	}
	slotsDone := make(chan struct{})
	go func() { wg.Wait(); close(slotsDone) }()

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-beatDone:
		// Another process took the name: its consumers are ours, stop.
		w.log.Error("stopping", "err", runErr)
	}
	stopConsuming()
	w.setDraining()
	timer := time.NewTimer(w.opts.ShutdownTimeout)
	select {
	case <-slotsDone:
	case <-timer.C:
		w.log.Warn("stopping the running jobs: they go back to the queue", "after", w.opts.ShutdownTimeout.String())
		stopJobs(errShutdown)
		select {
		case <-slotsDone:
		case <-time.After(stopWait):
			w.log.Error("some jobs did not stop; they go to another worker after the visibility timeout")
		}
	}
	timer.Stop()
	stopBeat()
	if runErr == nil {
		if err := <-beatDone; err != nil {
			runErr = err
		}
	}
	if runErr == nil {
		// Anything still held (a job delivered while the consume call was
		// interrupted) goes back to the queue.
		w.releaseHeld(context.Background(), false)
	}
	w.log.Info("worker stopped")
	return runErr
}

// consumer is the queue consumer of a slot: stable across restarts, so a
// new process can release what the previous one held.
func (w *Worker) consumer(slot int) string { return w.opts.Name + "/" + strconv.Itoa(slot) }

// releaseHeld gives back the jobs held by the consumers of this worker's
// name. At startup it also looks for slots beyond Concurrency (the
// previous process may have had more).
func (w *Worker) releaseHeld(ctx context.Context, all bool) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	names := map[string]bool{}
	for i := range w.opts.Concurrency {
		names[w.consumer(i)] = true
	}
	if all {
		held, err := w.jobs.InFlight(ctx, 1000)
		if err != nil {
			w.log.Warn("cannot list the jobs held by a previous process", "err", err)
		}
		for _, h := range held {
			if strings.HasPrefix(h.Consumer, w.opts.Name+"/") {
				names[h.Consumer] = true
			}
		}
	}
	for name := range names {
		n, err := w.jobs.Release(ctx, name)
		if err != nil {
			w.log.Warn("cannot release held jobs", "consumer", name, "err", err)
			continue
		}
		if n > 0 {
			w.log.Info("gave held jobs back to the queue", "consumer", name, "jobs", n)
		}
	}
}

// slot takes jobs one at a time until consume ends; hard ends the job.
func (w *Worker) slot(consume, hard context.Context, slot int) {
	select {
	case <-w.ready:
	case <-consume.Done():
		return
	}
	consumer := w.consumer(slot)
	delay := 100 * time.Millisecond
	for consume.Err() == nil {
		if w.isDisabled() {
			// Disabled by an administrator: no new jobs until enabled
			// (the heartbeat reads the flag).
			select {
			case <-consume.Done():
				return
			case <-time.After(min(w.opts.Heartbeat, time.Second)):
			}
			continue
		}
		msg, err := w.jobs.Consume(consume, consumer, w.opts.PollTimeout)
		if err != nil {
			if consume.Err() != nil {
				return
			}
			w.log.Warn("cannot take a job", "slot", slot, "err", err, "retry_in", delay.String())
			select {
			case <-consume.Done():
				return
			case <-time.After(delay):
			}
			delay = min(delay*2, 5*time.Second)
			continue
		}
		delay = 100 * time.Millisecond
		if msg != nil {
			w.handle(hard, slot, msg)
		}
	}
}

// handle runs one job and publishes its result.
func (w *Worker) handle(hard context.Context, slot int, msg *queue.Message) {
	start := time.Now()
	j, err := job.DecodeJob(msg.Payload)
	if err != nil {
		w.invalid(hard, msg, err)
		return
	}
	// The queue's attempt number is authoritative: a retry re-delivers
	// the same payload.
	j.Attempt = msg.Attempt
	kind := string(j.Operation.Type)
	log := w.log.With("job", j.ID, "operation", j.Operation.Key(), "attempt", j.Attempt, "slot", slot)
	res := j.NewResult(w.opts.Name)
	res.Durations.Wait = max(0, start.Sub(msg.EnqueuedAt))
	w.m.wait.WithLabelValues(kind).Observe(res.Durations.Wait.Seconds())
	log.Debug("job started", "description", j.Describe(), "deliveries", msg.Deliveries)

	// keep ends with cause queue.ErrNotOwner if the job is taken away.
	keep, stopKeep := w.jobs.KeepAlive(hard, msg)
	defer stopKeep()
	w.setRunning(slot, j, start)
	defer w.setIdle(slot)

	cached, err := w.run(keep, j, res)
	switch {
	case err == nil:
	case hard.Err() != nil:
		w.giveBack(msg, log)
		w.m.jobs.WithLabelValues(kind, outcomeRequeued).Inc()
		return
	case errors.Is(context.Cause(keep), queue.ErrNotOwner):
		log.Warn("the job was taken by another worker: this one was too slow to extend it", "err", err)
		w.m.jobs.WithLabelValues(kind, outcomeLost).Inc()
		return
	}

	payload, encErr := res.Encode()
	if encErr != nil {
		// Execute validates its results: this is a bug.
		log.Error("the result is invalid", "err", encErr)
		f := j.NewResult(w.opts.Name)
		f.Durations = res.Durations
		f.Fail(fmt.Errorf("the worker produced an invalid result: %w", encErr))
		res = f
		if payload, encErr = res.Encode(); encErr != nil {
			log.Error("cannot encode a failure", "err", encErr)
			w.m.jobs.WithLabelValues(kind, outcomeUnpublished).Inc()
			return
		}
	}
	acked, err := w.publish(hard, msg, payload, log)
	elapsed := time.Since(start)
	switch {
	case err != nil:
		log.Error("cannot publish the result: the job goes to another worker after the visibility timeout", "err", err)
		w.m.jobs.WithLabelValues(kind, outcomeUnpublished).Inc()
		return
	case !acked:
		log.Warn("another worker finished the job first; result dropped")
		w.m.jobs.WithLabelValues(kind, outcomeLost).Inc()
		return
	}
	w.m.duration.WithLabelValues(kind).Observe(elapsed.Seconds())
	attrs := []any{"success", res.Success, "cached", cached, "seconds", elapsed.Seconds(),
		"run_seconds", res.Durations.Run.Seconds()}
	if res.Outcome != "" {
		attrs = append(attrs, "outcome", res.Outcome)
	}
	if res.CompilationOutcome != "" {
		attrs = append(attrs, "compilation", res.CompilationOutcome)
	}
	w.mu.Lock()
	w.done++
	if !res.Success {
		w.failed++
	}
	w.mu.Unlock()
	if res.Success {
		w.m.jobs.WithLabelValues(kind, outcomeSuccess).Inc()
		log.Info("job done", attrs...)
	} else {
		w.m.jobs.WithLabelValues(kind, outcomeFailure).Inc()
		attrs = append(attrs, "err", res.Error, "tombstone", res.Tombstone)
		log.Error("job failed", attrs...)
	}
}

// run produces the result of a job: from the compilation cache, or by
// executing it once its boxes are free. An error means res is a failure
// (or the job must be given up: see handle).
func (w *Worker) run(ctx context.Context, j *job.Job, res *job.Result) (cached bool, err error) {
	start := time.Now()
	key := w.opts.Cache.Key(j)
	if key != "" {
		if w.opts.Cache.Get(ctx, key, res) {
			w.m.cache.WithLabelValues("hit").Inc()
			res.Durations.Total = time.Since(start)
			return true, nil
		}
		w.m.cache.WithLabelValues("miss").Inc()
	}

	timeout := w.opts.JobTimeout
	if j.Operation.Type.IsEvaluation() {
		timeout += time.Duration(4 * j.TimeLimit * float64(time.Second))
	}
	jctx, cancel := context.WithTimeoutCause(ctx, timeout, errJobTimeout)
	defer cancel()
	err = w.execute(jctx, j, res)
	if err != nil && errors.Is(context.Cause(jctx), errJobTimeout) && ctx.Err() == nil {
		err = fmt.Errorf("the job did not end within %s (worker.job_timeout): %w", timeout, err)
		failed(res, err)
	}
	if err == nil && key != "" && Cacheable(res) {
		if perr := w.opts.Cache.Put(ctx, key, res); perr != nil {
			w.log.Warn("cannot remember a compilation", "err", perr)
		} else {
			w.m.cache.WithLabelValues("store").Inc()
		}
	}
	return false, err
}

// execute waits for the boxes the job needs and runs it. A job that
// needs more boxes than the worker has fails at once: it would take them
// all and then wait for one more until its timeout, stalling the other
// slots meanwhile. A panic of the executor (a bug) fails the job instead
// of killing the worker.
func (w *Worker) execute(ctx context.Context, j *job.Job, res *job.Result) (err error) {
	need := max(w.exec.Boxes(j), 1)
	start := time.Now()
	if need > w.opts.Boxes {
		err = fmt.Errorf("the job needs %d sandbox boxes at the same time and worker.box_count is %d", need, w.opts.Boxes)
		failed(res, err)
		res.Durations.Total = time.Since(start)
		return err
	}
	n := int64(need)
	if err := w.boxes.Acquire(ctx, n); err != nil {
		err = fmt.Errorf("waiting for %d sandbox boxes: %w", n, context.Cause(ctx))
		failed(res, err)
		res.Durations.Total = time.Since(start)
		return err
	}
	defer w.boxes.Release(n)
	defer func() {
		if p := recover(); p != nil {
			w.log.Error("panic while running a job", "panic", fmt.Sprint(p), "stack", string(debug.Stack()))
			err = fmt.Errorf("worker panic: %v", p)
			failed(res, err)
		}
	}()
	return w.exec.Execute(ctx, j, res)
}

// failed turns res into a failure, like tasktype.Execute does.
func failed(res *job.Result, err error) {
	res.Fail(err)
	res.Text, res.AdminText, res.Stats = nil, "", nil
	res.Tombstone = false
}

// publish acknowledges the job and publishes its result in one step,
// retrying transient errors for up to publishTimeout (one attempt once
// hard has ended). acked is false if another worker finished the job
// first.
func (w *Worker) publish(hard context.Context, msg *queue.Message, payload []byte, log *slog.Logger) (acked bool, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), publishTimeout)
	defer cancel()
	delay := 100 * time.Millisecond
	for {
		acked, err = w.jobs.AckAndEnqueue(ctx, msg, w.results, 0, payload, queue.EnqueueOptions{})
		if err == nil || ctx.Err() != nil || hard.Err() != nil {
			return acked, err
		}
		log.Warn("cannot publish the result, retrying", "err", err, "retry_in", delay.String())
		select {
		case <-ctx.Done():
			return false, err
		case <-hard.Done():
		case <-time.After(delay):
		}
		delay = min(delay*2, 5*time.Second)
	}
}

// giveBack returns a job the worker could not finish to the queue, for
// another worker. If that fails the job comes back after the visibility
// timeout anyway.
func (w *Worker) giveBack(msg *queue.Message, log *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	if err := w.jobs.Nack(ctx, msg); err != nil && !errors.Is(err, queue.ErrNotOwner) {
		log.Warn("cannot give the job back to the queue", "err", err)
		return
	}
	log.Info("job given back to the queue: the worker is stopping")
}

// invalid handles a payload that is not a job: it is retried (a newer
// worker may understand it) and then dead-lettered, where the admins see
// it.
func (w *Worker) invalid(hard context.Context, msg *queue.Message, cause error) {
	w.m.jobs.WithLabelValues("unknown", outcomeInvalid).Inc()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(hard), opTimeout)
	defer cancel()
	dead, err := w.jobs.Retry(ctx, msg, fmt.Errorf("worker %s cannot read the job: %w", w.opts.Name, cause))
	w.log.Error("cannot read a job", "message", msg.ID, "attempt", msg.Attempt, "err", cause,
		"dead_lettered", dead, "retry_err", err)
}

func (w *Worker) setRunning(slot int, j *job.Job, start time.Time) {
	w.mu.Lock()
	w.running[slot] = registry.Job{Slot: slot, ID: j.ID, Operation: j.Operation, Description: j.Describe(),
		Attempt: j.Attempt, StartedAt: start}
	w.mu.Unlock()
	w.m.busy.Inc()
	w.refresh()
}

func (w *Worker) setIdle(slot int) {
	w.mu.Lock()
	delete(w.running, slot)
	w.mu.Unlock()
	w.m.busy.Dec()
	w.refresh()
}

// isDisabled reports whether an administrator disabled the worker.
func (w *Worker) isDisabled() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.disabled
}

// checkDisabled reads whether an administrator disabled the worker
// (registry.SetDisabled); on errors the state stays as it was.
func (w *Worker) checkDisabled(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	d, err := registry.IsDisabled(ctx, w.kv, w.opts.Name)
	if err != nil {
		if ctx.Err() == nil {
			w.log.Warn("cannot read whether the worker is disabled", "err", err)
		}
		return
	}
	w.mu.Lock()
	changed := d != w.disabled
	w.disabled = d
	w.mu.Unlock()
	if changed {
		if d {
			w.log.Info("worker disabled by an administrator: it takes no new jobs")
		} else {
			w.log.Info("worker enabled by an administrator")
		}
	}
}

func (w *Worker) setDraining() {
	w.mu.Lock()
	w.draining = true
	w.mu.Unlock()
	w.refresh()
}

// checkReady runs Options.Ready until it succeeds or ctx ends, then
// lets the slots take jobs.
func (w *Worker) checkReady(ctx context.Context) {
	if w.opts.Ready == nil {
		return
	}
	delay := min(time.Second, w.opts.ReadyRetry)
	for {
		err := w.opts.Ready(ctx)
		if ctx.Err() != nil {
			return
		}
		w.mu.Lock()
		w.readyErr = err
		w.mu.Unlock()
		w.refresh()
		if err == nil {
			close(w.ready)
			return
		}
		w.log.Error("the sandbox does not work: the worker takes no jobs until it does", "err", err,
			"retry_in", delay.String())
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, w.opts.ReadyRetry)
	}
}

// Health reports whether the worker can run jobs: the error of its last
// Ready check, until one succeeds (the "sandbox" health check).
func (w *Worker) Health(context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.readyErr
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// refresh asks the heartbeat to write the registry entry soon.
func (w *Worker) refresh() {
	select {
	case w.kick <- struct{}{}:
	default:
	}
}

// info is the registry entry.
func (w *Worker) info() registry.Info {
	w.mu.Lock()
	defer w.mu.Unlock()
	jobs := make([]registry.Job, 0, len(w.running))
	for _, j := range w.running {
		jobs = append(jobs, j)
	}
	slices.SortFunc(jobs, func(a, b registry.Job) int { return a.Slot - b.Slot })
	return registry.Info{
		Name: w.opts.Name, Host: w.opts.Host, PID: os.Getpid(), Version: w.opts.Version,
		Concurrency: w.opts.Concurrency, Boxes: w.opts.Boxes, Languages: w.opts.Languages,
		StartedAt: w.started, Draining: w.draining, Disabled: w.disabled, Jobs: jobs, Done: w.done, Failed: w.failed,
		Error: errorText(w.readyErr),
	}
}

// heartbeat refreshes the registry entry every Heartbeat, and soon after
// a job starts or ends, until ctx ends. It returns registry.ErrNameTaken
// if another process took the name.
func (w *Worker) heartbeat(ctx context.Context, reg *registry.Registration) error {
	t := time.NewTicker(w.opts.Heartbeat)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		case <-w.kick:
		}
		w.checkDisabled(ctx)
		uctx, cancel := context.WithTimeout(ctx, opTimeout)
		err := reg.Update(uctx, w.info())
		cancel()
		if errors.Is(err, registry.ErrNameTaken) {
			return err
		}
		if err != nil && ctx.Err() == nil {
			w.log.Warn("cannot refresh the worker registry", "err", err)
		}
		// Jobs starting and ending in a burst make one write.
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(minRefresh):
		}
	}
}
