// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Command cms is the single binary of the installation: one subcommand per
// service, plus `ctl` (administration) and `migrate`. Installed as
// `cmsctl` it behaves as `cms ctl`.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/adminweb"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/app"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/buildinfo"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/cli"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/contestweb"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/ctl"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/dispatcher"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/monitor"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/rankingweb"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/sandbox"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/worker"
)

func main() {
	// `cms sandbox-exec` is the seccomp launcher the sandbox puts in front
	// of contestant programs. It is hidden from the help and must start
	// as fast as possible: it runs before anything else and never returns.
	sandbox.MaybeRunLauncher()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// service wraps the Run function of a service as a subcommand.
func service(name, short string, runFn func(context.Context, *app.Runtime) error) *cli.Command {
	return &cli.Command{
		Name:  name,
		Short: short,
		Run: func(ctx context.Context, env *cli.Env, args []string) error {
			if len(args) > 0 {
				return cli.UsageError("unexpected arguments %q", args)
			}
			cfg, path, err := config.Load(env.ConfigPath)
			if err != nil {
				return err
			}
			rt := app.New(name, cfg, path, env.Stdout)
			if err := runFn(ctx, rt); err != nil {
				rt.Log.Error("exiting", "err", err.Error())
				return err
			}
			return nil
		},
	}
}

func root() *cli.Command {
	return &cli.Command{
		Name: "cms",
		Long: `Contest Management System (Go rewrite of CMS).

Global flags (before the command):
  --config FILE   configuration file (default: $CMS_CONFIG, ./cms.yaml, /etc/cms/cms.yaml)`,
		Sub: []*cli.Command{
			service(contestweb.Name, "contestant site (CMS ContestWebServer)", contestweb.Run),
			service(adminweb.Name, "administration site (CMS AdminWebServer)", adminweb.Run),
			service(rankingweb.Name, "public scoreboard (CMS RankingWebServer)", rankingweb.Run),
			service(dispatcher.Name, "job queues, scoring and scoreboard push (CMS Evaluation/Scoring/ProxyService)", dispatcher.Run),
			service(worker.Name, "compiles and runs submissions in the sandbox (CMS Worker)", worker.Run),
			service(monitor.Name, "health, host resources and alerts (CMS Checker/ResourceService/LogService)", monitor.Run),
			ctl.Command(),
			ctl.MigrateCommand(),
			{
				Name:  "healthcheck",
				Args:  "URL",
				Short: "exit 0 if URL answers 200 (Docker HEALTHCHECK in images without a shell)",
				Run:   healthcheck,
			},
			{
				Name:  "version",
				Short: "print the version and the source code URL",
				Run: func(_ context.Context, env *cli.Env, _ []string) error {
					fmt.Fprintf(env.Stdout, "cms %s\nsource: %s\n", buildinfo.String(), buildinfo.SourceURL(""))
					return nil
				},
			},
		},
	}
}

func healthcheck(ctx context.Context, env *cli.Env, args []string) error {
	if len(args) != 1 {
		return cli.UsageError("expected one URL")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, args[0], nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %s", args[0], resp.Status)
	}
	return nil
}

func run(ctx context.Context, argv []string, stdout, stderr io.Writer) int {
	prog := filepath.Base(argv[0])
	args := argv[1:]

	fs := flag.NewFlagSet("cms", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "configuration file")
	if err := fs.Parse(args); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintf(stderr, "cms: %v\n", err)
		return 2
	}
	args = fs.Args()
	path := "cms"
	if prog == "cmsctl" {
		args = append([]string{"ctl"}, args...)
	}

	env := &cli.Env{Stdout: stdout, Stderr: stderr, ConfigPath: *configPath, Prog: prog}
	err := root().Execute(ctx, env, path, args)
	switch {
	case err == nil:
		return 0
	case errors.Is(err, cli.ErrUsage):
		return 2
	default:
		fmt.Fprintf(stderr, "cms: error: %v\n", err)
		return 1
	}
}
