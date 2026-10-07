// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Package rankingweb is the public scoreboard (CMS RankingWebServer). Like
// the original it keeps its own state, pushed by the dispatcher, so it can
// run on a public host without access to the database or Valkey. Its
// pages and JavaScript are the ones of cmsranking.
//
// Phase F0 only provides the skeleton (health, AGPL source link, metrics);
// the scoreboard arrives in F7.
package rankingweb

import (
	"context"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/app"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/httpx"
)

// Name is the subcommand and log name of the service.
const Name = "ranking-web"

// MaxBody caps request bodies (data pushed by the dispatcher).
const MaxBody = 16 << 20

// Server is the scoreboard site.
type Server struct {
	rt *app.Runtime
}

// New returns the site.
func New(rt *app.Runtime) *Server { return &Server{rt: rt} }

// Routes returns the router of the site.
func (s *Server) Routes() *httpx.Router {
	mux := httpx.NewRouter()
	httpx.MountCommon(mux, s.rt.Health, s.rt.Config.SourceURL)
	return mux
}

// Run serves the site until ctx is cancelled.
func Run(ctx context.Context, rt *app.Runtime) error {
	cfg := rt.Config
	// The secret key derives the keys of the secret scoreboard links; it
	// must be the one of the other services.
	if err := cfg.RequireSecretKey(); err != nil {
		return err
	}
	rt.Started()
	h := rt.WebHandler(New(rt).Routes(), cfg.RankingWeb.NumProxies, MaxBody)
	return app.Run(ctx,
		rt.Ops(cfg.RankingWeb.OpsListen),
		rt.Serve(Name, cfg.RankingWeb.Listen, h),
	)
}
