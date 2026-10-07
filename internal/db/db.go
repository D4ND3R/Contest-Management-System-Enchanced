// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Package db opens the PostgreSQL pool and applies the embedded,
// forward-only migrations. Queries use explicit SQL: sqlc for static
// queries and pgx by hand for dynamic filters; there is no ORM.
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/app"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
)

// Open connects to PostgreSQL and checks the connection.
func Open(ctx context.Context, cfg config.Database) (*pgxpool.Pool, error) {
	pc, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("database.url: %w", err)
	}
	pc.MaxConns = int32(cfg.MaxConns)
	if pc.ConnConfig.RuntimeParams == nil {
		pc.ConnConfig.RuntimeParams = map[string]string{}
	}
	// Timestamps are stored as timestamptz and always handled in UTC by
	// the code; the server timezone only affects presentation.
	pc.ConnConfig.RuntimeParams["timezone"] = "UTC"
	if _, ok := pc.ConnConfig.RuntimeParams["application_name"]; !ok {
		pc.ConnConfig.RuntimeParams["application_name"] = "cms"
	}
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, fmt.Errorf("connecting to the database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connecting to the database: %w", err)
	}
	return pool, nil
}

// OpenService opens the pool of a service: it waits (up to
// app.StartupWait) until PostgreSQL answers and the schema matches this
// binary, then registers the "database" health check.
func OpenService(ctx context.Context, rt *app.Runtime) (*pgxpool.Pool, error) {
	var pool *pgxpool.Pool
	err := rt.Retry(ctx, "the database", func(ctx context.Context) error {
		if pool == nil {
			p, err := Open(ctx, rt.Config.Database)
			if err != nil {
				return err
			}
			pool = p
		}
		return CheckSchema(ctx, pool)
	})
	if err != nil {
		if pool != nil {
			pool.Close()
		}
		return nil, err
	}
	rt.Health.Register("database", func(ctx context.Context) error { return pool.Ping(ctx) })
	return pool, nil
}
