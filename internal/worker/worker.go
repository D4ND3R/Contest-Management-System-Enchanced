// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Package worker compiles and runs submissions in the sandbox. Workers
// never connect to PostgreSQL (TestWorkerHasNoDatabaseAccess): every job
// carries what it needs and files are fetched by digest from the blob
// store.
//
// Phase F0 only provides the skeleton (Valkey connection, health,
// metrics); the sandbox and the job loop arrive in F2.
package worker

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/app"
)

// Name is the subcommand and log name of the service.
const Name = "worker"

// Run runs the worker until ctx is cancelled.
func Run(ctx context.Context, rt *app.Runtime) error {
	cfg := &rt.Config.Worker
	// isolate rejects relative --dir paths: every directory handed to the
	// sandbox derives from this absolute path.
	abs, err := filepath.Abs(cfg.WorkDir)
	if err != nil {
		return fmt.Errorf("worker.work_dir: %w", err)
	}
	cfg.WorkDir = abs
	rt.Started()
	kvc, err := rt.OpenKV(ctx)
	if err != nil {
		return err
	}
	defer kvc.Close()
	return app.Run(ctx, rt.Ops(cfg.OpsListen), app.Idle)
}
