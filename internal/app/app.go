// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Package app is the runtime shared by every service: configuration,
// logger, metrics registry, health checks, connections and lifecycle.
// It must not import internal/db: workers use it and never touch the
// database (db.OpenService opens the pool for the other services).
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/sync/errgroup"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/buildinfo"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/httpx"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/kv"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/obs"
)

// StartupWait bounds how long a service waits for PostgreSQL, Valkey or
// pending migrations at startup (services may start before their
// dependencies, e.g. under Docker Compose).
var StartupWait = 90 * time.Second

// Runtime groups what every service needs.
type Runtime struct {
	Service    string
	Config     *config.Config
	ConfigPath string
	Log        *slog.Logger
	Metrics    *prometheus.Registry
	Health     *obs.Health
}

// New builds the runtime of a service.
func New(service string, cfg *config.Config, configPath string, logOut io.Writer) *Runtime {
	rt := &Runtime{
		Service:    service,
		Config:     cfg,
		ConfigPath: configPath,
		Log:        obs.NewLogger(logOut, cfg.Log, service),
		Metrics:    obs.NewRegistry(service),
		Health:     obs.NewHealth(),
	}
	return rt
}

// Started logs the startup line every service prints.
func (rt *Runtime) Started() {
	src := rt.ConfigPath
	if src == "" {
		src = "(defaults and environment)"
	}
	rt.Log.Info("starting", "version", buildinfo.String(), "config", src)
}

// Retry calls fn until it succeeds, ctx ends or StartupWait passes.
func (rt *Runtime) Retry(ctx context.Context, what string, fn func(context.Context) error) error {
	deadline := time.Now().Add(StartupWait)
	delay := 250 * time.Millisecond
	for {
		err := fn(ctx)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		rt.Log.Warn("waiting for "+what, "err", err.Error(), "retry_in", delay.String())
		select {
		case <-ctx.Done():
			return errors.Join(err, ctx.Err())
		case <-time.After(delay):
		}
		delay = min(delay*2, 5*time.Second)
	}
}

// OpenKV connects to Valkey and registers the "valkey" health check.
func (rt *Runtime) OpenKV(ctx context.Context) (*kv.Client, error) {
	var c *kv.Client
	err := rt.Retry(ctx, "Valkey", func(ctx context.Context) error {
		var err error
		c, err = kv.Open(ctx, rt.Config.Redis)
		return err
	})
	if err != nil {
		return nil, err
	}
	rt.Health.Register("valkey", c.Ping)
	return c, nil
}

// Ops returns a task serving /metrics, the detailed /healthz and pprof on
// addr. An empty address disables the listener.
func (rt *Runtime) Ops(addr string) func(context.Context) error {
	return func(ctx context.Context) error {
		if addr == "" {
			<-ctx.Done()
			return nil
		}
		return httpx.ListenAndServe(ctx, rt.Log, "ops", addr, httpx.OpsHandler(rt.Log, rt.Metrics, rt.Health))
	}
}

// WebHandler wraps a site router with the standard middleware stack.
func (rt *Runtime) WebHandler(mux http.Handler, numProxies int, maxBody int64) http.Handler {
	return httpx.Chain(mux,
		httpx.Recover(rt.Log),
		httpx.Observe(rt.Log, rt.Metrics, numProxies),
		httpx.SecureHeaders,
		httpx.LimitBody(maxBody),
	)
}

// Serve returns a task serving h on addr.
func (rt *Runtime) Serve(name, addr string, h http.Handler) func(context.Context) error {
	return func(ctx context.Context) error {
		return httpx.ListenAndServe(ctx, rt.Log, name, addr, h)
	}
}

// Run runs tasks until ctx is cancelled or one of them fails; the others
// are then stopped.
func Run(ctx context.Context, tasks ...func(context.Context) error) error {
	g, gctx := errgroup.WithContext(ctx)
	for _, t := range tasks {
		g.Go(func() error { return t(gctx) })
	}
	err := g.Wait()
	if err != nil && ctx.Err() != nil && errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// Idle is a task that just waits for the shutdown (placeholder for the
// main loop of services whose logic arrives in later phases).
func Idle(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

// Wrap adds the service name to an error.
func Wrap(service string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", service, err)
}
