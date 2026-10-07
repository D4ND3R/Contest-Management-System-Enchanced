// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Package dispatcher replaces CMS EvaluationService, ScoringService and
// ProxyService: it queues compilation and evaluation jobs on Valkey
// Streams, stores results idempotently, computes scores and pushes them to
// the scoreboard.
//
// Phase F0 only provides the skeleton (connections, health, metrics); the
// queues arrive in F4.
package dispatcher

import (
	"context"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/app"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/db"
)

// Name is the subcommand and log name of the service.
const Name = "dispatcher"

// Run runs the dispatcher until ctx is cancelled.
func Run(ctx context.Context, rt *app.Runtime) error {
	rt.Started()
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
	return app.Run(ctx, rt.Ops(rt.Config.Dispatcher.OpsListen), app.Idle)
}
