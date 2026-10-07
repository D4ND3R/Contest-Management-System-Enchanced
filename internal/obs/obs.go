// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Package obs holds the observability plumbing shared by every service:
// JSON logs on stdout (log/slog), a Prometheus registry and health checks.
package obs

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/buildinfo"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
)

// NewLogger returns the logger of a service. Every record carries the
// service name so that logs of all services can be merged (CMS LogService).
func NewLogger(w io.Writer, cfg config.Log, service string) *slog.Logger {
	var level slog.Level
	_ = level.UnmarshalText([]byte(cfg.Level))
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler
	if cfg.Format == "text" {
		h = slog.NewTextHandler(w, opts)
	} else {
		h = slog.NewJSONHandler(w, opts)
	}
	l := slog.New(h)
	if service != "" {
		l = l.With("service", service)
	}
	return l
}

// NewRegistry returns a metrics registry with the Go runtime and process
// collectors and a cms_build_info gauge.
func NewRegistry(service string) *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	bi := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cms_build_info",
		Help: "Build of the running binary; always 1.",
	}, []string{"service", "version", "commit"})
	bi.WithLabelValues(service, buildinfo.Version, buildinfo.Commit).Set(1)
	reg.MustRegister(bi)
	return reg
}

// CheckFunc reports whether a dependency is usable.
type CheckFunc func(ctx context.Context) error

// Health aggregates the checks of a service (database, Valkey, ...).
type Health struct {
	mu     sync.RWMutex
	checks map[string]CheckFunc
	// Timeout bounds each check.
	Timeout time.Duration
}

// NewHealth returns an empty set of checks.
func NewHealth() *Health {
	return &Health{checks: map[string]CheckFunc{}, Timeout: 2 * time.Second}
}

// Register adds or replaces a named check.
func (h *Health) Register(name string, fn CheckFunc) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.checks[name] = fn
}

// Report is the outcome of running every check.
type Report struct {
	Status  string            `json:"status"` // "ok" or "fail"
	Checks  map[string]string `json:"checks,omitempty"`
	Version string            `json:"version,omitempty"`
}

// Run executes the checks concurrently.
func (h *Health) Run(ctx context.Context) Report {
	h.mu.RLock()
	names := make([]string, 0, len(h.checks))
	for n := range h.checks {
		names = append(names, n)
	}
	sort.Strings(names)
	fns := make([]CheckFunc, len(names))
	for i, n := range names {
		fns[i] = h.checks[n]
	}
	h.mu.RUnlock()

	results := make([]string, len(names))
	var wg sync.WaitGroup
	for i := range fns {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, h.Timeout)
			defer cancel()
			if err := fns[i](cctx); err != nil {
				results[i] = err.Error()
			} else {
				results[i] = "ok"
			}
		}(i)
	}
	wg.Wait()

	rep := Report{Status: "ok", Checks: map[string]string{}, Version: buildinfo.Version}
	for i, n := range names {
		rep.Checks[n] = results[i]
		if results[i] != "ok" {
			rep.Status = "fail"
		}
	}
	return rep
}

// Handler serves the report as JSON with 200 or 503. When detailed is
// false (public listeners) only the overall status is returned, so error
// messages never leak to the Internet.
func (h *Health) Handler(detailed bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rep := h.Run(r.Context())
		if !detailed {
			rep = Report{Status: rep.Status}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if rep.Status != "ok" {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(rep)
	})
}
