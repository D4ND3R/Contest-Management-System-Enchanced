// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Package contestweb is the contestant site (CMS ContestWebServer): login,
// statements, submissions, user tests, communication and the JSON API
// under /api. Pages keep the HTML, CSS and JavaScript of CMS.
//
// Phase F0 only provides the skeleton (health, AGPL source link, metrics);
// the pages arrive in F5 (see SPEC.md).
package contestweb

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/app"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/db"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/httpx"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/kv"
)

// Name is the subcommand and log name of the service.
const Name = "contest-web"

// MaxBody caps request bodies; submission handlers enforce the per-file
// limits of the configuration (max_submission_length, max_input_length).
const MaxBody = 64 << 20

// Server is the contestant site.
type Server struct {
	rt   *app.Runtime
	pool *pgxpool.Pool
	kv   *kv.Client
}

// New returns the site with its dependencies.
func New(rt *app.Runtime, pool *pgxpool.Pool, kvc *kv.Client) *Server {
	return &Server{rt: rt, pool: pool, kv: kvc}
}

// Routes returns the router of the site.
func (s *Server) Routes() *httpx.Router {
	mux := httpx.NewRouter()
	httpx.MountCommon(mux, s.rt.Health, s.rt.Config.SourceURL)
	return mux
}

// Run serves the site until ctx is cancelled.
func Run(ctx context.Context, rt *app.Runtime) error {
	cfg := rt.Config
	if err := cfg.RequireSecretKey(); err != nil {
		return err
	}
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

	h := rt.WebHandler(New(rt, pool, kvc).Routes(), cfg.ContestWeb.NumProxies, MaxBody)
	return app.Run(ctx,
		rt.Ops(cfg.ContestWeb.OpsListen),
		rt.Serve(Name, cfg.ContestWeb.Listen, h),
	)
}
