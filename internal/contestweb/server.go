// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package contestweb

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
	"github.com/d4nd3r/contest-management-system-enchanced/internal/kv"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/l10n"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/obs"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/web"
	"github.com/d4nd3r/contest-management-system-enchanced/locale"
)

// The templates and static files of CMS's contestant site
// (cms/server/contest/templates and static), ported.
//
//go:embed templates static
var assets embed.FS

// Templates holds the page templates (html/template, see internal/web).
var Templates fs.FS = mustSub(assets, "templates")

// StaticFiles holds the contest site's static files, served under /static
// on top of web.SharedStatic (jQuery, favicon, live.js...).
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
	// KV stores the pending notifications of the participants (CMS kept
	// them in the memory of its process).
	KV *kv.Client
	// Blobs serves statements, attachments and submitted files, and
	// stores submissions.
	Blobs blob.Store
	// Hub streams the live events of <contest>/events; nil answers 503
	// there (the pages then poll, as CMS).
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

// Server is the contestant site.
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

	site   *web.Site
	ui     *web.Languages
	static *web.Static
	csrf   *web.CSRF
	// signer signs the login cookies and the API's login_data.
	signer *web.Signer
	// pwKey fingerprints the passwords on record stored in the login
	// cookie (CMS stores the password itself).
	pwKey []byte

	// loginByUser and loginByIP limit password attempts.
	loginByUser *web.RateLimiter
	loginByIP   *web.RateLimiter
	// bcryptSlots bounds the concurrent bcrypt verifications (≈ 250 ms
	// of CPU each) so a burst of logins does not starve the other pages.
	bcryptSlots chan struct{}

	tz    *tzCache
	notes *notifications
}

// New builds the site. It reads the server time zone once (D6).
func New(ctx context.Context, o Options) (*Server, error) {
	if o.Config == nil || o.Pool == nil || o.KV == nil || o.Blobs == nil {
		return nil, errors.New("contestweb: Config, Pool, KV and Blobs are required")
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
	s.signer = web.NewSigner(s.cfg.DeriveKey("contest-web/login-cookie"))
	s.pwKey = s.cfg.DeriveKey("contest-web/password-fingerprint")
	// Per user: 10 attempts, then one every 6 s. Per address: generous,
	// since a whole contest hall may come from one NAT address.
	s.loginByUser = web.NewRateLimiter(6*time.Second, 10, 0)
	s.loginByIP = web.NewRateLimiter(100*time.Millisecond, 600, 0)
	s.bcryptSlots = make(chan struct{}, max(1, runtime.GOMAXPROCS(0)/2))
	s.notes = newNotifications(s.kv)
	s.tz = newTZCache(s.cfg.Location(), s.loadTimezone)
	s.tz.refresh(ctx)
	if name := s.cfg.ContestWeb.Contest; name != "" {
		// Not an error: the contest may be created later, until then
		// every page is the 404 page.
		if c, err := s.loadContest(ctx, name); err == nil && c == nil {
			s.log.Warn("contest_web.contest does not exist", "contest", name)
		}
	}

	var err error
	s.static, err = web.NewStatic(web.SharedStatic, StaticFiles)
	if err != nil {
		return nil, err
	}
	s.static.NotFound = http.HandlerFunc(notFound)
	s.ui, err = web.NewLanguages(web.LanguagesOptions{
		Catalogs: locale.FS,
		Domains:  []string{locale.DomainContest, locale.DomainAdmin},
		Locale:   func(code string) web.Locale { return l10n.Get(code) },
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
	s.csrf = web.NewCSRF(web.CSRFOptions{Exempt: csrfExempt})
	s.site = &web.Site{
		Engine:    eng,
		Languages: s.ui,
		CSRF:      s.csrf,
		Location:  s.tz.Location,
		Clock:     s.clock,
		Log:       s.log,
		ErrorPage: "error.html",
	}
	return s, nil
}

// csrfExempt skips the CSRF check as CMS: requests authenticated by the
// X-CMS-Authorization header (no form can send it) and the API login.
func csrfExempt(r *http.Request) bool {
	if r.Header.Get(authHeader) != "" {
		return true
	}
	return isAPILogin(r.URL.Path)
}

// Handler returns the site: every route (routes.go) behind the CSRF
// check. Run wraps it with the middleware of internal/httpx.
func (s *Server) Handler() http.Handler {
	return s.csrf.Protect(s.routes())
}

// now returns the time of a request, with the microsecond precision of
// the database (CMS make_datetime).
func (s *Server) now() time.Time {
	return s.clock().UTC().Truncate(time.Microsecond)
}

// multiContest reports whether the site serves every contest (CMS
// contest_id is None).
func (s *Server) multiContest() bool { return s.cfg.ContestWeb.Contest == "" }
