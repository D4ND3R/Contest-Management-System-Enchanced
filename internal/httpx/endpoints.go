// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package httpx

import (
	"log/slog"
	"net/http"
	"net/http/pprof"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/buildinfo"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/obs"
)

// OpsHandler serves the private endpoints of a service: Prometheus
// metrics, the detailed health report and pprof. It must only listen on
// localhost or a management network.
func OpsHandler(log *slog.Logger, reg *prometheus.Registry, health *obs.Health) http.Handler {
	mux := NewRouter()
	mux.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{
		ErrorLog: slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}))
	mux.Handle("GET /healthz", health.Handler(true))
	mux.HandleFunc("GET /debug/pprof/", pprof.Index)
	mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
	return Chain(mux, Recover(log), Observe(log, nil, 0))
}

// MountCommon registers the endpoints every public site has: a minimal
// /healthz for load balancers and /source, the AGPL link to the code of
// the running version.
func MountCommon(mux *Router, health *obs.Health, sourceBase string) {
	mux.Handle("GET /healthz", health.Handler(false))
	mux.HandleFunc("GET /source", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, buildinfo.SourceURL(sourceBase), http.StatusFound)
	})
}
