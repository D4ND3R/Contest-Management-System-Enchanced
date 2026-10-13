// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Package dispatcher is the service that replaces CMS EvaluationService
// and ScoringService (SPEC R4.1-R4.6) and hosts the hook of ProxyService
// (ScoreSink, implemented in F7). The database side (what to do, storing
// results, scores, the task score cache) is internal/judging; this
// package drives it with the queues (internal/queue) and the workers
// (internal/worker).
//
// # The loop
//
// The active dispatcher runs one loop (leader.run) that, in turn:
//
//   - consumes worker results from job.ResultQueue in batches, stores each
//     batch in one transaction (judging.Apply), enqueues what follows
//     (evaluations after a compilation, retries of failed jobs with a
//     backoff), publishes the changes and acknowledges the messages after
//     the commit: a crash before the acknowledgement redelivers results,
//     which are idempotent (D21);
//   - handles the notifications of the sites and ctl
//     (judging.EventsStream: new submissions and user tests, tokens,
//     invalidations, dataset changes, sweep requests) and enqueues their
//     work (judging.Pending, judging.BuildJobs);
//   - sweeps the unfinished results (judging.Sweep, one step per round,
//     at start and every dispatcher.sweep_interval) so that lost
//     notifications and crashes only delay work;
//   - turns dead letters into failed attempts and publishes the progress
//     counters of the admin pages and the worker changes (internal/live).
//
// Jobs are enqueued with the dedup key of their operation and generation,
// so an operation is never queued twice while pending, and an
// invalidation makes the jobs in flight stale. Before enqueueing work read
// from the database the loop drops the operations already queued
// (queue.Holding) and stores the results already on their way (see
// schedule), so sweeps never make a worker run a job twice.
//
// Priorities (R4.1, JobPriority): contestant submissions first, then user
// tests and re-evaluations, then retries, then datasets that are not
// active; the oldest submission first within a priority.
//
// # One active dispatcher
//
// Dispatchers campaign for a Valkey lease (Lease: SET NX PX, renewed every
// third of its TTL, with a fencing epoch). The holder judges; the others
// wait as hot standbys and take over within the TTL (10s) when the holder
// dies, or at once when it shuts down (it releases the lease). A holder
// stops writing as soon as its local deadline passes without a renewal,
// before anyone else can take the lease; correctness never depends on it
// anyway: every write is idempotent and checks generations under row
// locks (D21, D23), so a stalled former holder can only cause duplicates
// that are dropped.
//
// # Blobs
//
// With dispatcher.blob_listen set, the dispatcher also serves the blob
// store to judges on other machines (blob.NewServer, D22, D25).
package dispatcher

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/app"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/db"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/httpx"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/queue"
)

// Name is the subcommand and log name of the service.
const Name = "dispatcher"

// JobQueueOptions and ResultQueueOptions are the options of the two
// queues: the workers open them with the same priorities and visibility.
// Retries of failed jobs wait 1s, 2s, 4s... (at most 1m) minus up to 20%
// so that jobs that failed together do not come back together.
var (
	JobQueueOptions = queue.Options{
		Priorities: job.JobPriorities, Visibility: job.JobVisibility,
		BackoffBase: time.Second, BackoffMax: time.Minute, BackoffJitter: 0.2,
	}
	ResultQueueOptions = queue.Options{}
)

// Run runs the dispatcher until ctx is cancelled.
func Run(ctx context.Context, rt *app.Runtime) error {
	cfg := rt.Config
	dc := cfg.Dispatcher
	rt.Started()
	langs, err := language.Load(cfg.LanguagesDir)
	if err != nil {
		return err
	}
	tasks := []func(context.Context) error{rt.Ops(dc.OpsListen)}
	if dc.BlobListen != "" {
		if err := cfg.RequireSecretKey(); err != nil {
			return err
		}
		store, err := blob.Open(cfg.Storage, Name, rt.Metrics)
		if err != nil {
			return err
		}
		defer func() {
			if err := blob.Close(store); err != nil {
				rt.Log.Warn("closing the blob store", "err", err)
			}
		}()
		rt.Health.Register("blob", func(ctx context.Context) error { return blob.Check(ctx, store) })
		srv, err := blob.NewServer(store, cfg.SecretKeyBytes(), 0, rt.Log)
		if err != nil {
			return err
		}
		mux := httpx.NewRouter()
		mux.Handle("/blobs/", srv)
		tasks = append(tasks, rt.Serve("blobs", dc.BlobListen, rt.WebHandler(mux, 0, blob.DefaultMaxUpload)))
	}

	pool, err := db.OpenService(ctx, rt)
	if err != nil {
		return err
	}
	defer pool.Close()
	kvc, err := rt.OpenKV(ctx)
	if err != nil {
		return err
	}
	defer kvc.Close()
	jobs, err := queue.Open(ctx, kvc, job.JobQueue, JobQueueOptions)
	if err != nil {
		return err
	}
	results, err := queue.Open(ctx, kvc, job.ResultQueue, ResultQueueOptions)
	if err != nil {
		return err
	}
	// The queue state is shared: only the dispatcher exports it.
	rt.Metrics.MustRegister(jobs.Collector(), results.Collector(),
		statsCollectors{jobs.StatsCollector(2 * time.Second), results.StatsCollector(2 * time.Second)})
	rt.Health.Register("queues", func(ctx context.Context) error {
		_, err := jobs.Stats(ctx)
		return err
	})

	pub := live.NewPublisher(kvc, rt.Log, rt.Metrics, live.PublisherOptions{})
	judge := judging.New(pool, judging.Options{Languages: langs, Logger: rt.Log, Tries: judging.TriesFrom(dc)})
	d, err := New(pool, kvc, jobs, results, judge, Options{
		SweepInterval: dc.SweepInterval.D(),
		ResultBatch:   dc.ResultBatch,
		Live:          pub,
		Log:           rt.Log,
		Metrics:       rt.Metrics,
	})
	if err != nil {
		return err
	}
	rt.Health.Register(Name, d.Health)
	// The publisher stops after the loop, so the last events go out.
	pubCtx, stopPub := context.WithCancel(context.WithoutCancel(ctx))
	defer stopPub()
	pubDone := make(chan error, 1)
	go func() { pubDone <- pub.Run(pubCtx) }()
	tasks = append(tasks, d.Run)
	err = app.Run(ctx, tasks...)
	stopPub()
	if perr := <-pubDone; err == nil && perr != nil && ctx.Err() == nil {
		err = perr
	}
	return err
}

// statsCollectors exports the state of several queues: their collectors
// describe the same metrics (labelled by queue), which a registry accepts
// only once.
type statsCollectors []prometheus.Collector

func (s statsCollectors) Describe(ch chan<- *prometheus.Desc) {
	if len(s) > 0 {
		s[0].Describe(ch)
	}
}

func (s statsCollectors) Collect(ch chan<- prometheus.Metric) {
	for _, c := range s {
		c.Collect(ch)
	}
}
