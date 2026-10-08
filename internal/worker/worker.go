// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Package worker compiles and runs submissions in the sandbox (CMS
// Worker). Workers never connect to PostgreSQL (D12,
// TestWorkerHasNoDatabaseAccess): every job carries what it needs and
// files are fetched by digest from the blob store.
//
// A worker takes jobs from the job queue of Valkey (internal/queue,
// job.JobQueue), runs them with their task type in isolate boxes
// (internal/grading/tasktype) and publishes each result on the result
// queue in the same atomic step that acknowledges the job. It runs
// worker.concurrency jobs in parallel, keeps its running jobs alive in
// the queue (queue.KeepAlive), remembers successful compilations (Cache)
// and announces itself, with what it is doing, in the worker registry
// (internal/worker/registry) that the judges page reads. A worker whose
// sandbox fails its self-test takes no jobs, reports it in /healthz and
// on the judges page, and tries again every 30 seconds.
//
// Nothing is lost when a worker stops or dies: a stopping worker takes no
// new jobs, lets the running ones finish for worker.shutdown_timeout and
// gives the others back; a job of a worker that died goes to another
// worker after the visibility timeout (job.JobVisibility), or at once
// when the worker restarts under the same name.
package worker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/app"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/buildinfo"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/steps"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/tasktype"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/queue"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/sandbox"
)

// Name is the subcommand and log name of the service.
const Name = "worker"

// Run runs the worker until ctx is cancelled.
func Run(ctx context.Context, rt *app.Runtime) error {
	cfg := rt.Config
	wc := &cfg.Worker
	// isolate rejects relative --dir paths: every directory handed to the
	// sandbox derives from this absolute path.
	abs, err := filepath.Abs(wc.WorkDir)
	if err != nil {
		return fmt.Errorf("worker.work_dir: %w", err)
	}
	wc.WorkDir = abs
	rt.Started()

	host, _ := os.Hostname()
	name := wc.Name
	if name == "" {
		name = NameFromHost(host)
	}
	langs, err := language.Load(cfg.LanguagesDir)
	if err != nil {
		return err
	}

	sbCfg := sandbox.ConfigFrom(cfg)
	sbCfg.Logger = rt.Log
	sb, err := sandbox.New(sbCfg)
	if err != nil {
		return err
	}

	store, err := blob.Open(cfg.Storage, Name, rt.Metrics)
	if err != nil {
		return err
	}
	defer func() {
		if err := blob.Close(store); err != nil {
			rt.Log.Warn("closing the blob store", "err", err)
		}
	}()
	rt.Health.Register("blob", func(ctx context.Context) error { return blob.Check(ctx, store) })

	kvc, err := rt.OpenKV(ctx)
	if err != nil {
		return err
	}
	defer kvc.Close()
	jobs, err := queue.Open(ctx, kvc, job.JobQueue, queue.Options{Priorities: job.JobPriorities, Visibility: job.JobVisibility})
	if err != nil {
		return err
	}
	results, err := queue.Open(ctx, kvc, job.ResultQueue, queue.Options{})
	if err != nil {
		return err
	}
	rt.Metrics.MustRegister(jobs.Collector(), results.Collector())

	stepsCfg := steps.ConfigFrom(cfg)
	stepsCfg.Log = rt.Log
	env := &tasktype.Env{
		Boxes:     tasktype.SandboxBoxes(sb),
		Store:     store,
		Languages: langs,
		Steps:     &stepsCfg,
		Log:       rt.Log,
	}
	var cache *Cache
	if ttl := wc.CompilationCacheTTL.D(); ttl > 0 {
		cache = NewCache(kvc, store, langs, CacheOptions{
			TTL:     ttl,
			Limits:  cfg.Sandbox.Compilation,
			Cgroups: cfg.Sandbox.Cgroups,
			Log:     rt.Log,
		})
	}
	shutdown := wc.ShutdownTimeout.D()
	if shutdown == 0 {
		shutdown = time.Nanosecond // give the running jobs back at once
	}
	w, err := New(kvc, jobs, results, TaskTypes(env), Options{
		Name:            name,
		Host:            host,
		Version:         buildinfo.String(),
		Languages:       langs.Names(),
		Concurrency:     wc.Concurrency,
		Boxes:           wc.BoxCount,
		ShutdownTimeout: shutdown,
		JobTimeout:      wc.JobTimeout.D(),
		Cache:           cache,
		// A broken host takes no jobs (rather than failing them all) and
		// says so in /healthz and on the judges page; it starts working
		// when the sandbox does (isolate.service started late...).
		Ready:   sb.SelfTest,
		Log:     rt.Log,
		Metrics: rt.Metrics,
	})
	if err != nil {
		return err
	}
	rt.Health.Register("sandbox", w.Health)
	return app.Run(ctx, rt.Ops(wc.OpsListen), w.Run)
}

// NameFromHost turns a host name into a worker name (config.ValidWorkerName):
// characters other than letters, digits, ".", "_" and "-" become "-", and
// it is cut to 64 characters.
func NameFromHost(host string) string {
	b := []byte(host)
	for i, c := range b {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '.' && c != '_' && c != '-' {
			b[i] = '-'
		}
	}
	name := strings.TrimLeft(string(b), ".-")
	if len(name) > 64 {
		name = name[:64]
	}
	if !config.ValidWorkerName(name) {
		return "worker"
	}
	return name
}
