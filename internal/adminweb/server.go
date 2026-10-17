// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package adminweb

import (
	"context"
	"embed"
	"errors"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"runtime"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/kv"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/l10n"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/obs"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/web"
	"github.com/d4nd3r/contest-management-system-enchanced/locale"
)

// The templates and static files of CMS's administration
// (cms/server/admin/templates and static), ported.
//
//go:embed templates static
var assets embed.FS

// Templates holds the page templates (html/template, see internal/web).
var Templates fs.FS = mustSub(assets, "templates")

// StaticFiles holds the admin site's static files, served under /static
// on top of web.SharedStatic (jQuery, favicon, loading.gif, live.js).
var StaticFiles fs.FS = mustSub(assets, "static")

func mustSub(f fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return sub
}

// Options are the dependencies of the site. Run builds them from the
// configuration; tests pass their own.
type Options struct {
	Config *config.Config
	// Log receives the logs (nil: discarded).
	Log  *slog.Logger
	Pool *pgxpool.Pool
	// KV holds the notifications of the admins, the worker registry and
	// the queues.
	KV *kv.Client
	// Blobs stores and serves files (statements, test data, submissions).
	Blobs blob.Store
	// Hub streams the live events of /events; nil answers 503 there
	// (the pages then poll, as CMS).
	Hub *live.Hub
	// Publisher receives the live events the site publishes (nil:
	// dropped).
	Publisher live.Sink
	// Languages are the programming languages (nil: the built-in ones).
	Languages *language.Registry
	// Health serves /healthz (nil: a new one with no checks).
	Health *obs.Health
	// Clock returns the current time (nil: time.Now); tests move it.
	Clock func() time.Time
}

// Server is the administration site.
type Server struct {
	cfg    *config.Config
	log    *slog.Logger
	pool   *pgxpool.Pool
	st     *store.Store
	kv     *kv.Client
	blobs  blob.Store
	hub    *live.Hub
	pub    live.Sink
	langs  *language.Registry
	health *obs.Health
	clock  func() time.Time
	judge  *judging.Judge
	// started is when the site started (the Resource usage page).
	started time.Time

	site   *web.Site
	ui     *web.Languages
	static *web.Static
	csrf   *web.CSRF
	// signer signs the login cookie; authKey fingerprints the
	// authentication of the admin stored in it (a password change ends
	// the other sessions).
	signer  *web.Signer
	authKey []byte

	loginByUser *web.RateLimiter
	loginByIP   *web.RateLimiter
	bcrypt      *web.BcryptGate

	jobs     jobQueue
	tz       *settingsCache
	notes    *notifications
	routeTab []Route
}

// New builds the site.
func New(ctx context.Context, o Options) (*Server, error) {
	if o.Config == nil || o.Pool == nil || o.KV == nil || o.Blobs == nil {
		return nil, errors.New("adminweb: Config, Pool, KV and Blobs are required")
	}
	if err := o.Config.RequireSecretKey(); err != nil {
		return nil, err
	}
	s := &Server{
		cfg:    o.Config,
		log:    o.Log,
		pool:   o.Pool,
		st:     store.New(o.Pool),
		kv:     o.KV,
		blobs:  o.Blobs,
		hub:    o.Hub,
		pub:    o.Publisher,
		langs:  o.Languages,
		health: o.Health,
		clock:  o.Clock,
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	if s.pub == nil {
		s.pub = live.Discard
	}
	if s.langs == nil {
		s.langs = language.Builtin()
	}
	if s.health == nil {
		s.health = obs.NewHealth()
	}
	if s.clock == nil {
		s.clock = time.Now
	}
	s.started = s.clock()
	s.judge = judging.New(s.pool, judging.Options{Languages: s.langs, Logger: s.log, Now: s.clock,
		Tries: judging.TriesFrom(s.cfg.Dispatcher)})
	s.signer = web.NewSigner(s.cfg.DeriveKey("admin-web/login-cookie"))
	s.authKey = s.cfg.DeriveKey("admin-web/authentication-fingerprint")
	// Per user: 10 failed attempts, then one every 6 s; per address:
	// 300 attempts, then 5 per second (D42).
	s.loginByUser = web.NewRateLimiter(6*time.Second, 10, 0)
	s.loginByIP = web.NewRateLimiter(200*time.Millisecond, 300, 0)
	s.bcrypt = web.NewBcryptGate(runtime.GOMAXPROCS(0), 16, 256)
	s.notes = newNotifications(s.kv)
	s.tz = newSettingsCache(s.cfg, s.loadSettings)
	s.tz.refresh(ctx)

	var err error
	s.static, err = web.NewStatic(web.SharedStatic, StaticFiles)
	if err != nil {
		return nil, err
	}
	s.static.NotFound = http.HandlerFunc(notFound)
	s.ui, err = web.NewLanguages(web.LanguagesOptions{
		Catalogs: locale.FS,
		// The languages of the admin catalogs (English and Spanish);
		// the msgids CMS already translates come from its catalogs.
		Domains: []string{locale.DomainAdmin, locale.DomainContest},
		Locale:  func(code string) web.Locale { return l10n.Get(code) },
	})
	if err != nil {
		return nil, err
	}
	eng, err := web.NewEngine(web.EngineOptions{
		Templates: Templates,
		Static:    s.static,
		Funcs:     func(cur func() *web.Render) template.FuncMap { return s.funcs(cur) },
	})
	if err != nil {
		return nil, err
	}
	// Multipart parts beyond formMemory go to temporary files.
	s.csrf = web.NewCSRF(web.CSRFOptions{MaxMemory: formMemory})
	s.site = &web.Site{
		Engine:    eng,
		Languages: s.ui,
		CSRF:      s.csrf,
		Location:  s.tz.Location,
		Clock:     s.clock,
		Log:       s.log,
		// Ctx.Error renders error.html with the page context; this is
		// the fallback when even that fails.
		ErrorPage: "",
	}
	s.routeTab = s.routes()
	if err := checkRoutes(s.routeTab); err != nil {
		return nil, err
	}
	return s, nil
}

// Handler returns the site: every route of routes.go (each one checks
// its body size, CSRF token and permission) plus the static files and
// the common endpoints. Run wraps it with the middleware of internal/httpx.
func (s *Server) Handler() http.Handler {
	return s.router()
}

// now returns the time of a request, with the microsecond precision of
// the database (CMS make_datetime).
func (s *Server) now() time.Time {
	return s.clock().UTC().Truncate(time.Microsecond)
}

// Store returns the data access layer (for the tests of other packages
// and the page files).
func (s *Server) Store() *store.Store { return s.st }

// Judge returns the judging core (invalidations, dataset activation, task
// scores).
func (s *Server) Judge() *judging.Judge { return s.judge }
