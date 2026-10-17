// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Package rankingweb is the public scoreboard (CMS RankingWebServer,
// SPEC §1.2 and F7). Like the original it keeps its own state, pushed by
// the dispatcher over the REST API of CMS (D14), so it can run on a
// public host without access to the database or Valkey. Its page and
// JavaScript are those of cmsranking (static/), with jQuery served locally
// and the inline handlers moved to JavaScript for the CSP (D10).
//
//   - entity.go: the entities and their validation (cmsranking Contest,
//     Task, Team, User, Submission, Subchange);
//   - store.go, persist.go: the store (cmsranking Store), durable in
//     data_dir (snapshot and journal);
//   - scoring.go: the scores and their history (cmsranking Scoring),
//     twice: every subchange for administrators, and without those hidden
//     by a freeze for the public (R7.3);
//   - handlers.go: the HTTP API (cmsranking RankingWebServer) with the
//     events of the local live hub (R7.2);
//   - access.go: visibility (R7.4): public, secret link, administrators.
package rankingweb

import (
	"context"
	"embed"
	"io/fs"
	"sync"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/app"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/httpx"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/web"
)

// Name is the subcommand and log name of the service.
const Name = "ranking-web"

// MaxBody caps request bodies (data pushed by the dispatcher, which sends
// at most MaxBatch entities per request).
const MaxBody = 16 << 20

// MaxBatch is the largest number of entities the dispatcher sends in one
// request.
const MaxBatch = 5000

//go:embed static
var staticFS embed.FS

func staticFiles() fs.FS {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	return sub
}

// Server is the scoreboard site.
type Server struct {
	rt     *app.Runtime
	cfg    *config.Config
	store  *Store
	hub    *live.Hub
	static fs.FS
	signer *web.Signer
	user   string
	pass   string

	cacheMu sync.Mutex
	cache   map[string]cached
}

type cached struct {
	version uint64
	body    []byte
}

// New returns the site on store, whose events go through hub.
func New(rt *app.Runtime, store *Store, hub *live.Hub) *Server {
	cfg := rt.Config
	user, pass := cfg.RankingWebCredentials()
	return &Server{
		rt: rt, cfg: cfg, store: store, hub: hub, static: staticFiles(),
		signer: web.NewSigner(cfg.DeriveKey("ranking-web/cookie")),
		user:   user, pass: pass,
		cache: map[string]cached{},
	}
}

// Routes returns the router of the site.
func (s *Server) Routes() *httpx.Router {
	mux := httpx.NewRouter()
	httpx.MountCommon(mux, s.rt.Health, s.cfg.SourceURL)
	s.mount(mux)
	return mux
}

// Run serves the site until ctx is cancelled.
func Run(ctx context.Context, rt *app.Runtime) error {
	cfg := rt.Config
	// The secret key derives the secret links, the admin links, the cookie
	// key and the default credentials; it must be the one of the other
	// services (D5).
	if err := cfg.RequireSecretKey(); err != nil {
		return err
	}
	hub := live.NewLocalHub(rt.Log, rt.Metrics, live.HubOptions{})
	store, err := Open(cfg.RankingWeb.DataDir, hub, rt.Log)
	if err != nil {
		return err
	}
	defer func() {
		if err := store.Close(); err != nil {
			rt.Log.Warn("closing the ranking store", "err", err)
		}
	}()
	rt.Health.Register("store", func(context.Context) error { return store.Check() })
	if cfg.RankingWeb.Username == "" || cfg.RankingWeb.Password == "" {
		rt.Log.Info("ranking writes use the credentials derived from secret_key unless set in ranking_web")
	}
	rt.Log.Info("scoreboard", "visibility", cfg.RankingWeb.Visibility, "data_dir", cfg.RankingWeb.DataDir)
	rt.Started()
	h := rt.WebHandler(New(rt, store, hub).Routes(), cfg.RankingWeb.NumProxies, MaxBody)
	return app.Run(ctx,
		rt.Ops(cfg.RankingWeb.OpsListen),
		rt.Serve(Name, cfg.RankingWeb.Listen, h),
	)
}
