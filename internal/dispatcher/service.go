// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package dispatcher

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/kv"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/queue"
)

// Consumer names of the active dispatcher on the results queue and the
// notification stream. They are the same for every instance: only the
// lease holder consumes, and the next one finds what the previous one
// left unacknowledged under the same name (Release, ReadEvents pending).
const (
	ResultsConsumer = "dispatcher"
	EventsConsumer  = "dispatcher"
)

// Options configures a Dispatcher. Zero values take the defaults.
type Options struct {
	// Instance names this process in the lease and the logs (default
	// host/pid/random); it must be unique among running dispatchers.
	Instance string
	// SweepInterval is the time between the end of a sweep of every
	// contest and the start of the next one (default 1m;
	// dispatcher.sweep_interval).
	SweepInterval time.Duration
	// SweepWindow and SweepLimit configure each sweep step
	// (judging.SweepOptions; defaults 5000 and 500).
	SweepWindow int32
	SweepLimit  int32
	// ResultBatch bounds the results stored in one transaction (default
	// 128; dispatcher.result_batch).
	ResultBatch int
	// EventBatch bounds the notifications handled together (default 256).
	EventBatch int
	// IdleWait is how long the loop waits for a result when it has nothing
	// else to do (default 100ms): notifications wait at most that long.
	IdleWait time.Duration
	// LeaseTTL is the lifetime of the lease without renewal (default 10s):
	// a standby takes over at most that long after the active dispatcher
	// died. LeaseRetry is how often a standby tries to take it (default
	// 1s).
	LeaseTTL   time.Duration
	LeaseRetry time.Duration
	// DeadLetterInterval is how often dead letters are handled (default
	// 10s).
	DeadLetterInterval time.Duration
	// ProgressInterval is how often, at most, the judging counters of the
	// admin pages are published (default 1s; every 15s when nothing
	// changes, for the worker list).
	ProgressInterval time.Duration
	// Live receives the live events (default live.Discard).
	Live live.Sink
	// Scores receives the changes for the public scoreboard (default
	// NopScoreSink).
	Scores ScoreSink
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
	def(&o.SweepInterval, time.Minute)
	def(&o.IdleWait, 100*time.Millisecond)
	def(&o.LeaseTTL, 10*time.Second)
	def(&o.LeaseRetry, time.Second)
	def(&o.DeadLetterInterval, 10*time.Second)
	def(&o.ProgressInterval, time.Second)
	if o.SweepWindow <= 0 {
		o.SweepWindow = 5000
	}
	if o.SweepLimit <= 0 {
		o.SweepLimit = 500
	}
	if o.ResultBatch <= 0 {
		o.ResultBatch = 128
	}
	if o.EventBatch <= 0 {
		o.EventBatch = 256
	}
	if o.Live == nil {
		o.Live = live.Discard
	}
	if o.Scores == nil {
		o.Scores = NopScoreSink{}
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Instance == "" {
		host, _ := os.Hostname()
		var b [4]byte
		_, _ = rand.Read(b[:])
		o.Instance = host + "/" + strconv.Itoa(os.Getpid()) + "/" + hex.EncodeToString(b[:])
	}
	if o.LeaseTTL < 30*time.Millisecond {
		return errors.New("dispatcher: the lease TTL must be at least 30ms")
	}
	return nil
}

// Dispatcher replaces CMS EvaluationService and ScoringService (and hosts
// the hook of ProxyService): it turns notifications and sweeps into jobs,
// stores the results the workers send back, scores them and publishes
// what changed. One dispatcher of an installation is active (Lease); the
// others wait as hot standbys.
type Dispatcher struct {
	opts    Options
	pool    *pgxpool.Pool
	kv      *kv.Client
	jobs    *queue.Queue
	results *queue.Queue
	judge   *judging.Judge
	lease   *Lease
	live    live.Sink
	scores  ScoreSink
	log     *slog.Logger
	m       *metrics

	mu           sync.Mutex
	leading      bool
	failing      error
	failingSince time.Time

	hooks testHooks
}

// testHooks simulate crashes in the tests (zero in production).
type testHooks struct {
	// keepLease: stopping does not release the lease (a crash).
	keepLease bool
	// beforeCommit and afterCommit run around the transaction that stores
	// a batch of results; returning true makes the loop stop right there
	// without acknowledging anything, as if the process died.
	beforeCommit func() bool
	afterCommit  func() bool
}

// errCrash is the simulated death of the tests.
var errCrash = errors.New("dispatcher: simulated crash")

// New returns a dispatcher. jobs and results must be job.JobQueue (with
// job.JobPriorities priorities) and job.ResultQueue on kvc; judge works on
// pool.
func New(pool *pgxpool.Pool, kvc *kv.Client, jobs, results *queue.Queue, judge *judging.Judge, opts Options) (*Dispatcher, error) {
	if err := opts.defaults(); err != nil {
		return nil, err
	}
	d := &Dispatcher{
		opts: opts, pool: pool, kv: kvc, jobs: jobs, results: results, judge: judge,
		lease:  NewLease(kvc, opts.Instance, opts.LeaseTTL),
		live:   opts.Live,
		scores: opts.Scores,
		log:    opts.Log.With("instance", opts.Instance),
		m:      newMetrics(opts.Metrics),
	}
	return d, nil
}

// Leading reports whether this dispatcher holds the lease and judges.
func (d *Dispatcher) Leading() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.leading
}

// Epoch returns the fencing epoch of the lease held or last held.
func (d *Dispatcher) Epoch() int64 { return d.lease.Epoch() }

// Health fails while the main loop of the active dispatcher keeps failing
// (database or Valkey trouble) for more than 30 seconds. A standby is
// healthy.
func (d *Dispatcher) Health(context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.failing != nil && time.Since(d.failingSince) > 30*time.Second {
		return fmt.Errorf("judging has been failing since %s: %w", d.failingSince.UTC().Format(time.RFC3339), d.failing)
	}
	return nil
}

func (d *Dispatcher) setLeading(v bool) {
	d.mu.Lock()
	d.leading = v
	d.failing = nil
	d.mu.Unlock()
	if v {
		d.m.leader.Set(1)
	} else {
		d.m.leader.Set(0)
	}
}

// noteError records a failure of the main loop (Health).
func (d *Dispatcher) noteError(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.failing == nil {
		d.failingSince = time.Now()
	}
	d.failing = err
}

func (d *Dispatcher) noteOK() {
	d.mu.Lock()
	d.failing = nil
	d.mu.Unlock()
}

// Run campaigns for the lease and judges while holding it, until ctx
// ends. A dispatcher that loses the lease goes back to waiting.
func (d *Dispatcher) Run(ctx context.Context) error {
	var lastHolder string
	var unreachable bool
	for {
		ok, holder, err := d.lease.TryAcquire(ctx)
		if err == nil && unreachable {
			unreachable = false
			d.log.Info("the lease is reachable again")
		}
		switch {
		case ctx.Err() != nil:
			return nil
		case err != nil:
			if !unreachable {
				unreachable = true
				d.log.Warn("cannot reach the lease, retrying", "err", err)
			}
		case ok:
			lastHolder = ""
			d.lead(ctx)
			if ctx.Err() != nil {
				return nil
			}
		case holder != lastHolder:
			lastHolder = holder
			d.log.Info("another dispatcher is active: waiting as a standby", "holder", holder)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(d.opts.LeaseRetry):
		}
	}
}

// lead judges while the lease is held, then gives it up.
func (d *Dispatcher) lead(parent context.Context) {
	epoch := d.lease.Epoch()
	d.m.epoch.Set(float64(epoch))
	d.m.leaseEvent.WithLabelValues("acquired").Inc()
	d.log.Info("active dispatcher", "epoch", epoch)
	d.setLeading(true)
	defer d.setLeading(false)

	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	// Not active any more as soon as the work stops (the loop may take a
	// moment to return).
	context.AfterFunc(ctx, func() { d.setLeading(false) })
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		d.lease.Keep(ctx, cancel)
	}()
	err := newLeader(d).run(ctx)
	cause := context.Cause(ctx)
	cancel(nil)
	wg.Wait()

	switch {
	case errors.Is(cause, ErrLeaseLost):
		d.m.leaseEvent.WithLabelValues("lost").Inc()
		d.log.Error("lease lost: stopped judging", "epoch", epoch, "err", cause)
	case parent.Err() != nil:
		d.m.leaseEvent.WithLabelValues("released").Inc()
		d.log.Info("stopped judging", "epoch", epoch)
	default:
		d.m.leaseEvent.WithLabelValues("released").Inc()
		d.log.Error("judging stopped", "epoch", epoch, "err", err)
	}
	if d.hooks.keepLease {
		return
	}
	// Give the lease up (if still ours) so a standby takes over at once.
	rctx, rcancel := context.WithTimeout(context.WithoutCancel(parent), 2*time.Second)
	defer rcancel()
	if err := d.lease.Release(rctx); err != nil {
		d.log.Warn("cannot release the lease", "err", err)
	}
}
