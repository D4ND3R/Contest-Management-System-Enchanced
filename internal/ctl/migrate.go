// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package ctl

import (
	"context"
	"fmt"
	"os"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/cli"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/db"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/obs"
)

// MigrateCommand returns `cms migrate`.
func MigrateCommand() *cli.Command {
	return &cli.Command{
		Name:  "migrate",
		Short: "apply the database migrations (same as `migrate up`)",
		Run:   migrateUp,
		Sub: []*cli.Command{
			{Name: "up", Short: "apply the pending migrations (safe to run concurrently)", Run: migrateUp},
			{Name: "status", Short: "show the schema version and the pending migrations", Run: migrateStatus},
		},
	}
}

func migrateUp(ctx context.Context, env *cli.Env, args []string) error {
	if err := noArgs(args); err != nil {
		return err
	}
	cfg, _, err := config.Load(env.ConfigPath)
	if err != nil {
		return err
	}
	pool, err := db.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer pool.Close()
	log := obs.NewLogger(os.Stderr, cfg.Log, "migrate")
	applied, err := db.Migrate(ctx, pool, log)
	if err != nil {
		return err
	}
	all, _ := db.Migrations()
	fmt.Fprintf(env.Stdout, "applied %d migration(s); schema version %d\n", len(applied), len(all))
	return nil
}

func migrateStatus(ctx context.Context, env *cli.Env, args []string) error {
	if err := noArgs(args); err != nil {
		return err
	}
	cfg, _, err := config.Load(env.ConfigPath)
	if err != nil {
		return err
	}
	pool, err := db.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer pool.Close()
	v, pending, err := db.Status(ctx, pool)
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "schema version: %d\n", v)
	if len(pending) == 0 {
		fmt.Fprintln(env.Stdout, "up to date")
		return nil
	}
	fmt.Fprintf(env.Stdout, "pending (%d):\n", len(pending))
	for _, m := range pending {
		fmt.Fprintf(env.Stdout, "  %04d_%s\n", m.Version, m.Name)
	}
	return nil
}
