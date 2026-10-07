// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Package adminweb is the administration site (CMS AdminWebServer), with
// the pages of CMS plus problem preparation from the browser, the judges
// page and the Server page (see SPEC.md §4).
//
// Phase F0 only provides the skeleton (health, AGPL source link, metrics);
// the pages arrive in F6.
package adminweb

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/app"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/db"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/httpx"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/kv"
)

// Name is the subcommand and log name of the service.
const Name = "admin-web"

// MaxBody caps request bodies: test data zips can be large. Upload
// handlers stream to disk instead of memory.
const MaxBody = 1 << 30

// Server is the administration site.
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

	h := rt.WebHandler(New(rt, pool, kvc).Routes(), cfg.AdminWeb.NumProxies, MaxBody)
	return app.Run(ctx,
		rt.Ops(cfg.AdminWeb.OpsListen),
		rt.Serve(Name, cfg.AdminWeb.Listen, h),
	)
}
