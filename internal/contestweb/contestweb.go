// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Package contestweb is the contestant site (CMS ContestWebServer): login,
// statements, submissions, user tests, communication and the JSON API
// under /api. Pages keep the HTML, CSS and JavaScript of CMS
// (cms/server/contest/templates and static, ported to html/template with
// internal/web).
//
// # URLs (D17)
//
// Without contest_web.contest, "/" lists the contests and each contest
// lives under "/<name>" ("/<name>" is its overview, "/<name>/login"...);
// with it, that contest is served at "/". /static holds the assets of
// CMS (contest and shared), /docs the language documentation of
// contest_web.docs_path, /healthz and /source (AGPL) the common
// endpoints. routes.go lists every route and which file implements it.
//
// # Requests
//
// Every contest route runs through Server.contestRoute, which builds a
// Ctx (CMS ContestHandler.prepare and render_params): the contest, the
// interface language (the contest's allowed_localizations, the
// "language" cookie, Accept-Language), the logged-in participation (IP
// autologin, the <contest>_login cookie or the X-CMS-Authorization
// header of the API, see auth.go), its phase (phase.go) and the render
// state. The route's access rules then run as CMS's decorators:
// tornado.web.authenticated, api_login_required and
// actual_phase_required. Handlers receive the Ctx and render with
// Ctx.Render, using a *Page (the context of contest.html) embedded in
// their own data.
//
// A page runs a bounded number of queries, whatever the number of tasks,
// users or submissions (N2): the contest with its main group, the
// participation with its user and group, and the tasks (once, for the
// menu and the task lookups) — three queries for a logged-in page.
//
// # Templates
//
// templates/ holds CMS's templates ported line by line to html/template
// (internal/web explains the mapping): a page extends contest.html with
// {{/* extends "contest.html" */}}, fills {{define "core"}} (and
// "additional_js" for its script) and gets a data struct embedding *Page
// (Ctx.Page). A {% trans %} block of CMS at the start of a line loses its
// indentation and the newline after it, so the port writes {{T "..."}} at
// column 0 and joins the next line (or uses {{- else}} / {{- end}} when a
// block tag follows): the pages render to the bytes CMS sends, which
// TestCMSPages checks against pages captured from CMS (testdata/cms).
// html/template drops HTML comments: {{comment " text "}} writes them.
// Inline <script> elements carry nonce="{{nonce}}" and inline handlers
// go to static/cws_handlers.js (D10). Times of the contest (submissions,
// messages) are shown with format_contest_time and the absolute time in
// a title attribute (R5.9).
//
// # Live updates (R5.8)
//
// GET <contest>/events streams the live events of the participation (p:)
// and of its contest (c:) with internal/live; live.js (shared by the
// three sites, internal/web/static) shows the connection indicator next
// to the user name (contest.html) and refreshes the regions marked
// data-live="event…" (an element with an id; data-live-match-task="3"
// limits it to the events of one task) by fetching the page again. While
// the indicator is not green the pages poll: the regions every 15-30 s
// and the notifications of CMS every 30 s. contest.html fetches the
// notifications at once on message and answer events (they go to one
// participant) and after a random delay on announcement events
// (CMS.live.later: every page of the contest gets them at the same
// time), and reloads the page (after a random delay) on contest and
// participation events.
package contestweb

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/app"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/db"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
)

// Name is the subcommand and log name of the service.
const Name = "contest-web"

// MaxBody caps the bodies of the uploads (submissions and user tests,
// multipart); their handlers enforce the per-file limits of the
// configuration (max_submission_length, max_input_length).
const MaxBody = 64 << 20

// MaxFormBody caps the bodies of every other request (login,
// registration, questions, tokens...): a urlencoded form is read whole
// into memory, before any login.
const MaxFormBody = 1 << 20

// limitBodies applies MaxFormBody to the requests that are not uploads
// (MaxBody, applied by the middleware of internal/httpx, is the cap of
// those).
func limitBodies(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && r.Body != http.NoBody && !isUpload(r) {
			r.Body = http.MaxBytesReader(w, r.Body, MaxFormBody)
		}
		next.ServeHTTP(w, r)
	})
}

// isUpload reports the routes that take files: <contest>/tasks/<t>/submit,
// <contest>/tasks/<t>/test and <contest>/api/<t>/submit.
func isUpload(r *http.Request) bool {
	p := r.URL.Path
	return r.Method == http.MethodPost && (strings.HasSuffix(p, "/submit") || strings.HasSuffix(p, "/test"))
}

// Run serves the site until ctx is cancelled.
func Run(ctx context.Context, rt *app.Runtime) error {
	cfg := rt.Config
	if err := cfg.RequireSecretKey(); err != nil {
		return err
	}
	rt.Started()
	langs, err := language.Load(cfg.LanguagesDir)
	if err != nil {
		return err
	}
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
	blobs, err := blob.Open(cfg.Storage, Name, rt.Metrics)
	if err != nil {
		return err
	}
	defer func() {
		if err := blob.Close(blobs); err != nil {
			rt.Log.Warn("closing the blob store", "err", err)
		}
	}()
	rt.Health.Register("blob", func(ctx context.Context) error { return blob.Check(ctx, blobs) })

	hub := live.NewHub(kvc, rt.Log, rt.Metrics, live.HubOptions{})
	rt.Health.Register("live", hub.Check)
	pub := live.NewPublisher(kvc, rt.Log, rt.Metrics, live.PublisherOptions{})

	s, err := New(ctx, Options{
		Config:    cfg,
		Log:       rt.Log,
		Pool:      pool,
		KV:        kvc,
		Blobs:     blobs,
		Hub:       hub,
		Publisher: pub,
		Languages: langs,
		Health:    rt.Health,
	})
	if err != nil {
		return err
	}
	h := rt.WebHandler(s.Handler(), cfg.ContestWeb.NumProxies, MaxBody)
	err = app.Run(ctx,
		hub.Run,
		pub.Run,
		rt.Ops(cfg.ContestWeb.OpsListen),
		rt.Serve(Name, cfg.ContestWeb.Listen, h),
	)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
