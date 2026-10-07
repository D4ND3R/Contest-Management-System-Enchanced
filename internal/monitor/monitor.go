// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Package monitor replaces CMS Checker, ResourceService and LogService:
// it watches the health of the services and of the host (CPU, memory,
// disk) and raises alerts. Logs go to stdout as JSON (journald, Docker),
// so there is no log service. Process supervision is left to systemd or
// Docker.
//
// Phase F0 only provides the skeleton: it checks PostgreSQL and Valkey
// and exposes the result on monitor.listen.
package monitor

import (
	"context"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/app"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/db"
)

// Name is the subcommand and log name of the service.
const Name = "monitor"

// Run runs the monitor until ctx is cancelled.
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
	return app.Run(ctx, rt.Ops(rt.Config.Monitor.Listen), app.Idle)
}
