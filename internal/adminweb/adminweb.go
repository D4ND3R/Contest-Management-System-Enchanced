// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Package adminweb is the administration site (CMS AdminWebServer): the
// pages of CMS (cms/server/admin/templates and static, ported to
// html/template with internal/web) plus the new pages of SPEC F6:
// problem preparation from the browser (R6.2), the judges page (R6.7),
// the Server page (R6.8) and the audit log (R6.6).
//
// # Requests
//
// routes.go lists every route with the permission it requires and the
// file that implements it. Every route runs through Server.route, which
// builds a Ctx (ctx.go): it caps the body (MaxFormBody, or MaxBody for
// uploads), checks the CSRF token of every unsafe method, finds the
// logged-in admin from the signed "awslogin" cookie (auth.go: CMS's
// AWSAuthMiddleware), checks the route's permission (perm.go: CMS's
// require_permission plus the problem preparer role of R6.5) and then
// calls the handler. Handlers never check permissions by hand except
// for finer decisions inside a page (Ctx.Can).
//
// Pages render with Ctx.Render and a data struct embedding *Page (the
// context of base.html: the admin, the contest of the page with its
// menu or the global menu, the live updates indicator). A page runs a
// bounded number of queries (N2): the admin, then either the sidebar
// lists (one query) or the contest with its main group and unanswered
// questions (one query), plus its own.
//
// Every change goes through Ctx.Write (audit.go): one transaction that
// must record at least one audit_log entry (R6.6) and whose live events
// are published after the commit.
//
// # Templates
//
// templates/ holds the templates of CMS ported line by line (see
// internal/web for the mapping); every visible text goes through {{T}}
// with the msgids of CMS's English text, so the English pages render the
// bytes CMS sends, and Spanish comes from locale/es/LC_MESSAGES/admin.po
// (falling back to the "cms" catalogs for the msgids CMS translates).
// Inline <script> elements carry nonce="{{nonce}}"; inline event handlers
// are replaced by data- attributes handled in static/aws_handlers.js
// (D10). New pages use the components of the original (core_title,
// sub_table, bordered tables, forms, notifications).
//
// # Live updates (R6.9)
//
// GET /events and /contest/<id>/events stream the admin topics ("a" and
// "a:<contest>") with internal/live; live.js shows the indicator next to
// the admin name and refreshes the elements marked data-live.
package adminweb

import (
	"context"
	"errors"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/app"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/db"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
)

// Name is the subcommand and log name of the service.
const Name = "admin-web"

// MaxBody caps the bodies of the upload routes (Route.Upload: test data
// zips, packages): multipart parts larger than formMemory go to
// temporary files, never whole into memory.
const MaxBody = 1 << 30

// MaxFormBody caps the bodies of every other request.
const MaxFormBody = 4 << 20

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
	h := rt.WebHandler(s.Handler(), cfg.AdminWeb.NumProxies, MaxBody)
	err = app.Run(ctx,
		hub.Run,
		pub.Run,
		rt.Ops(cfg.AdminWeb.OpsListen),
		rt.Serve(Name, cfg.AdminWeb.Listen, h),
	)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
