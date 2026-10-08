// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package worker

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Outcomes of a job in cms_worker_jobs_total.
const (
	// outcomeSuccess: a result with success true was published.
	outcomeSuccess = "success"
	// outcomeFailure: a result with success false was published (the
	// dispatcher retries the job).
	outcomeFailure = "failure"
	// outcomeLost: the job was taken by another worker (this one stalled)
	// or finished by it first; nothing was published.
	outcomeLost = "lost"
	// outcomeRequeued: the worker stopped before the job ended and gave it
	// back to the queue.
	outcomeRequeued = "requeued"
	// outcomeUnpublished: the result could not be published (Valkey
	// unreachable); the job goes to another worker after the visibility
	// timeout.
	outcomeUnpublished = "unpublished"
	// outcomeInvalid: the payload was not a job this worker understands
	// (it is retried and then dead-lettered).
	outcomeInvalid = "invalid"
)

type metrics struct {
	jobs     *prometheus.CounterVec
	duration *prometheus.HistogramVec
	wait     *prometheus.HistogramVec
	busy     prometheus.Gauge
	slots    prometheus.Gauge
	cache    *prometheus.CounterVec
}

func newMetrics(reg prometheus.Registerer) *metrics {
	// Jobs last from a few milliseconds (a cached compilation, a small
	// testcase) to minutes.
	buckets := prometheus.ExponentialBucketsRange(0.005, 600, 18)
	m := &metrics{
		jobs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cms_worker_jobs_total",
			Help: "Jobs handled, by kind (compile, evaluate, compile_test, evaluate_test) and outcome " +
				"(success, failure, lost, requeued, unpublished, invalid).",
		}, []string{"kind", "outcome"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "cms_worker_job_duration_seconds",
			Help:    "Time from taking a job to publishing its result, by kind.",
			Buckets: buckets,
		}, []string{"kind"}),
		wait: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "cms_worker_job_wait_seconds",
			Help:    "Time jobs waited in the queue before this worker took them, by kind.",
			Buckets: buckets,
		}, []string{"kind"}),
		busy: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "cms_worker_busy_slots",
			Help: "Job slots running a job.",
		}),
		slots: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "cms_worker_slots",
			Help: "Job slots (worker.concurrency).",
		}),
		cache: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cms_worker_compilation_cache_total",
			Help: "Compilation cache lookups (hit, miss) and entries written (store).",
		}, []string{"result"}),
	}
	if reg != nil {
		reg.MustRegister(m.jobs, m.duration, m.wait, m.busy, m.slots, m.cache)
	}
	return m
}
