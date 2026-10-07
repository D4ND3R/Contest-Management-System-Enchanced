// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Package httpx is the HTTP plumbing shared by the three web sites and the
// ops listeners: server lifecycle, middleware (recovery, access log,
// metrics, security headers, body limits, client address) and the
// /healthz, /metrics, /debug/pprof and /source endpoints.
package httpx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// ShutdownTimeout bounds the graceful shutdown of a server.
var ShutdownTimeout = 10 * time.Second

type drainKey struct{}

// Draining returns a channel closed when the server starts shutting down.
// Long-lived handlers (server-sent events) must return when it closes, so
// the graceful shutdown does not wait for them.
func Draining(ctx context.Context) <-chan struct{} {
	if ch, ok := ctx.Value(drainKey{}).(chan struct{}); ok {
		return ch
	}
	return nil
}

// Listen opens a TCP listener. Opening it before serving reports a busy
// port at startup instead of in a background goroutine.
func Listen(addr string) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", addr, err)
	}
	return ln, nil
}

// Serve runs h on ln until ctx is cancelled, then shuts down gracefully.
func Serve(ctx context.Context, log *slog.Logger, name string, ln net.Listener, h http.Handler) error {
	drain := make(chan struct{})
	base := context.WithValue(context.WithoutCancel(ctx), drainKey{}, drain)
	base, cancelBase := context.WithCancel(base)
	defer cancelBase()

	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		// Uploads (submissions, test cases, zips) must finish in this time.
		ReadTimeout: 5 * time.Minute,
		// No WriteTimeout: event streams stay open for hours.
		IdleTimeout:    2 * time.Minute,
		MaxHeaderBytes: 64 << 10,
		BaseContext:    func(net.Listener) context.Context { return base },
		ErrorLog:       slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	log.Info("listening", "server", name, "addr", ln.Addr().String())

	select {
	case err := <-errc:
		return fmt.Errorf("%s server: %w", name, err)
	case <-ctx.Done():
	}

	close(drain)
	sctx, cancel := context.WithTimeout(context.Background(), ShutdownTimeout)
	defer cancel()
	err := srv.Shutdown(sctx)
	cancelBase()
	if err != nil {
		_ = srv.Close()
		log.Warn("forced shutdown", "server", name, "err", err)
	}
	if serr := <-errc; serr != nil && !errors.Is(serr, http.ErrServerClosed) {
		return fmt.Errorf("%s server: %w", name, serr)
	}
	log.Info("stopped", "server", name)
	return nil
}

// ListenAndServe combines Listen and Serve.
func ListenAndServe(ctx context.Context, log *slog.Logger, name, addr string, h http.Handler) error {
	ln, err := Listen(addr)
	if err != nil {
		return err
	}
	return Serve(ctx, log, name, ln, h)
}
