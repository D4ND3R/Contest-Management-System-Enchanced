// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package db

import (
	"context"
	"sync/atomic"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/multitracer"
	"github.com/jackc/pgx/v5/pgxpool"
)

// QueryCounter is a pgx tracer that counts the queries a pool runs: every
// Query, QueryRow and Exec (BEGIN and COMMIT included), every batch once
// whatever the number of statements in it, and every CopyFrom once. The
// statement descriptions pgx fetches on its own (the statement cache, the
// column types of a COPY) are not counted. Tests use it to prove that a
// page runs a bounded number of queries whatever the number of rows (N2:
// no N+1). It is safe for concurrent use; the zero value is ready.
type QueryCounter struct {
	n atomic.Int64
}

var (
	_ pgx.QueryTracer    = (*QueryCounter)(nil)
	_ pgx.BatchTracer    = (*QueryCounter)(nil)
	_ pgx.CopyFromTracer = (*QueryCounter)(nil)
)

// Install adds the counter to the tracers of a pool configuration,
// keeping any tracer already there. Call it before creating the pool.
func (c *QueryCounter) Install(cfg *pgxpool.Config) {
	if cfg.ConnConfig.Tracer == nil {
		cfg.ConnConfig.Tracer = c
		return
	}
	cfg.ConnConfig.Tracer = multitracer.New(cfg.ConnConfig.Tracer, c)
}

// Count returns the number of round trips since the last Reset.
func (c *QueryCounter) Count() int64 { return c.n.Load() }

// Reset sets the count back to zero.
func (c *QueryCounter) Reset() { c.n.Store(0) }

// TraceQueryStart implements pgx.QueryTracer.
func (c *QueryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.n.Add(1)
	return ctx
}

// TraceQueryEnd implements pgx.QueryTracer.
func (c *QueryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// TraceBatchStart implements pgx.BatchTracer: a batch counts once.
func (c *QueryCounter) TraceBatchStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchStartData) context.Context {
	c.n.Add(1)
	return ctx
}

// TraceBatchQuery implements pgx.BatchTracer.
func (c *QueryCounter) TraceBatchQuery(context.Context, *pgx.Conn, pgx.TraceBatchQueryData) {}

// TraceBatchEnd implements pgx.BatchTracer.
func (c *QueryCounter) TraceBatchEnd(context.Context, *pgx.Conn, pgx.TraceBatchEndData) {}

// TraceCopyFromStart implements pgx.CopyFromTracer.
func (c *QueryCounter) TraceCopyFromStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceCopyFromStartData) context.Context {
	c.n.Add(1)
	return ctx
}

// TraceCopyFromEnd implements pgx.CopyFromTracer.
func (c *QueryCounter) TraceCopyFromEnd(context.Context, *pgx.Conn, pgx.TraceCopyFromEndData) {}
