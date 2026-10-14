// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package dispatcher

import (
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
)

// metrics are the Prometheus metrics of the dispatcher. The depth of the
// queues per priority comes from queue.StatsCollector (cms_queue_ready,
// cms_queue_pending...), registered by Run.
type metrics struct {
	leader     prometheus.Gauge
	epoch      prometheus.Gauge
	leaseEvent *prometheus.CounterVec

	enqueued   *prometheus.CounterVec
	duplicates prometheus.Counter
	held       prometheus.Counter
	skipped    *prometheus.CounterVec

	results     *prometheus.CounterVec
	applyErrors prometheus.Counter
	applyTime   prometheus.Histogram
	batchSize   prometheus.Histogram
	gaveUp      *prometheus.CounterVec
	scoreErrors prometheus.Counter
	latency     prometheus.Histogram

	events       *prometheus.CounterVec
	rechecks     *prometheus.CounterVec
	sweeps       prometheus.Counter
	sweepTime    prometheus.Histogram
	sweepFound   *prometheus.CounterVec
	deadLetters  *prometheus.CounterVec
	loopErrors   *prometheus.CounterVec
	scoreUpdates prometheus.Counter
}

func newMetrics(reg prometheus.Registerer) *metrics {
	m := &metrics{
		leader: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "cms_dispatcher_leader",
			Help: "1 while this dispatcher holds the lease and judges, 0 while it waits as a standby.",
		}),
		epoch: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "cms_dispatcher_lease_epoch",
			Help: "Fencing epoch of the lease this dispatcher holds or last held (grows with every takeover).",
		}),
		leaseEvent: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cms_dispatcher_lease_changes_total",
			Help: "Lease changes of this dispatcher: acquired, lost (renewal failed or expired), released (shutdown) and given_up (judging kept failing).",
		}, []string{"change"}),
		enqueued: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cms_dispatcher_jobs_enqueued_total",
			Help: "Jobs added to the job queue, by priority (0 is served first).",
		}, []string{"priority"}),
		duplicates: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "cms_dispatcher_jobs_duplicate_total",
			Help: "Jobs not added because the same operation was already queued or running.",
		}),
		held: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "cms_dispatcher_operations_already_queued_total",
			Help: "Operations found by sweeps and notifications that were already queued or running (not rebuilt).",
		}),
		skipped: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cms_dispatcher_operations_skipped_total",
			Help: "Operations that needed no job: done meanwhile, gone, or invalid (the dataset must be fixed).",
		}, []string{"reason"}),
		results: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cms_dispatcher_results_total",
			Help: "Worker results by outcome: applied, failed (a failed job, counted as a try), stale (invalidated meanwhile), duplicate, gone, invalid.",
		}, []string{"outcome"}),
		applyErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "cms_dispatcher_apply_errors_total",
			Help: "Transactions storing results that failed and were retried.",
		}),
		applyTime: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "cms_dispatcher_apply_seconds",
			Help:    "Time to store a batch of results (one transaction) and enqueue what follows.",
			Buckets: []float64{.001, .0025, .005, .01, .025, .05, .1, .25, .5, 1, 2.5},
		}),
		batchSize: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "cms_dispatcher_result_batch_size",
			Help:    "Results stored per transaction.",
			Buckets: []float64{1, 2, 4, 8, 16, 32, 64, 128, 256, 512},
		}),
		gaveUp: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cms_dispatcher_gave_up_total",
			Help: "Operations that reached their maximum number of tries (they wait for an admin).",
		}, []string{"operation"}),
		scoreErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "cms_dispatcher_score_errors_total",
			Help: "Results that could not be scored (invalid score type parameters...).",
		}),
		latency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "cms_dispatcher_submission_score_seconds",
			Help:    "Time from a new submission to its score (or compilation error) on the active dataset.",
			Buckets: []float64{.25, .5, 1, 2, 3, 5, 7.5, 10, 15, 20, 30, 45, 60, 90, 120, 180, 300, 600},
		}),
		events: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cms_dispatcher_events_total",
			Help: "Notifications handled, by kind.",
		}, []string{"kind"}),
		rechecks: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cms_dispatcher_rechecks_total",
			Help: "Notified submissions and user tests looked at again because they were not visible yet (notified before their transaction committed).",
		}, []string{"kind"}),
		sweeps: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "cms_dispatcher_sweeps_total",
			Help: "Complete sweeps of the unfinished results.",
		}),
		sweepTime: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "cms_dispatcher_sweep_seconds",
			Help:    "Duration of complete sweeps (steps are interleaved with results).",
			Buckets: []float64{.01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60},
		}),
		sweepFound: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cms_dispatcher_sweep_found_total",
			Help: "What sweeps found that nobody had asked for: operations enqueued, results scored, cached task scores missing.",
		}, []string{"kind"}),
		deadLetters: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cms_dispatcher_dead_letters_total",
			Help: "Dead letters handled: jobs abandoned by their workers (counted as a failed try) and results.",
		}, []string{"queue"}),
		loopErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cms_dispatcher_errors_total",
			Help: "Errors of the main loop, by step (retried).",
		}, []string{"step"}),
		scoreUpdates: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "cms_dispatcher_score_updates_total",
			Help: "Updates handed to the scoreboard sink.",
		}),
	}
	if reg != nil {
		reg.MustRegister(m.leader, m.epoch, m.leaseEvent, m.enqueued, m.duplicates, m.held, m.skipped,
			m.results, m.applyErrors, m.applyTime, m.batchSize, m.gaveUp, m.scoreErrors, m.latency,
			m.events, m.rechecks, m.sweeps, m.sweepTime, m.sweepFound, m.deadLetters, m.loopErrors, m.scoreUpdates)
	}
	return m
}

func prioLabel(p int) string { return strconv.Itoa(p) }
