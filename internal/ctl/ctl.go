// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Package ctl is `cms ctl` (also installed as `cmsctl`), the command line
// administration that replaces cmscontrib: configuration checks, secrets,
// and in later phases imports, exports, users, backups and upgrades.
package ctl

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"text/tabwriter"

	"go.yaml.in/yaml/v3"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/cli"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
)

// Command returns the `ctl` command tree.
func Command() *cli.Command {
	c := &cli.Command{
		Name:  "ctl",
		Short: "administration commands (replaces cmscontrib)",
		Sub: []*cli.Command{
			{
				Name:  "config",
				Short: "inspect the configuration",
				Sub: []*cli.Command{
					{Name: "check", Short: "validate the configuration and report problems", Run: configCheck},
					{Name: "show", Short: "print the effective configuration (secrets hidden)", Run: configShow},
					{Name: "env", Short: "list the CMS_* environment variables", Run: configEnv},
				},
			},
			{
				Name:  "blob-token",
				Short: "print the token judges on other machines use for the blob server",
				Long: `Prints storage.remote.token for judges that reach the blob server of the
dispatcher (storage.backend: remote). It is derived from secret_key, so the
judges never need the secret_key itself.`,
				Run: blobToken,
			},
			verifyHostCommand(),
			{
				Name:  "gen-secret",
				Short: "print a new random secret_key",
				Long: `Prints a new random secret_key (32 bytes, hexadecimal).

Generate it ONCE per installation and use the same value in every service:
it signs cookies and derives the keys of the secret scoreboard links.`,
				Run: genSecret,
			},
		},
	}
	c.Sub = append(c.Sub, JudgeCommands()...)
	c.Sub = append(c.Sub, ImportCommands()...)
	return c
}

func noArgs(args []string) error {
	if len(args) > 0 {
		return cli.UsageError("unexpected arguments %q", args)
	}
	return nil
}

func configCheck(_ context.Context, env *cli.Env, args []string) error {
	if err := noArgs(args); err != nil {
		return err
	}
	cfg, path, err := config.Load(env.ConfigPath)
	if err != nil {
		return err
	}
	if path == "" {
		path = "(no file: defaults and environment)"
	}
	fmt.Fprintf(env.Stdout, "configuration: %s\n", path)
	if err := cfg.RequireSecretKey(); err != nil {
		return fmt.Errorf("the web services will not start: %w", err)
	}
	fmt.Fprintln(env.Stdout, "ok")
	return nil
}

func configShow(_ context.Context, env *cli.Env, args []string) error {
	if err := noArgs(args); err != nil {
		return err
	}
	cfg, _, err := config.Load(env.ConfigPath)
	if err != nil {
		return err
	}
	out, err := yaml.Marshal(cfg.Redacted())
	if err != nil {
		return err
	}
	_, err = env.Stdout.Write(out)
	return err
}

func configEnv(_ context.Context, env *cli.Env, args []string) error {
	if err := noArgs(args); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(env.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "VARIABLE\tSETTING\t")
	for _, s := range config.Settings(config.Default()) {
		note := ""
		if s.Secret {
			note = "(secret: prefer " + s.Env + "_FILE)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", s.Env, s.Path, note)
	}
	return tw.Flush()
}

// NewSecret returns a random 32-byte key, hex encoded.
func NewSecret() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return hex.EncodeToString(b[:])
}

func blobToken(_ context.Context, env *cli.Env, args []string) error {
	if err := noArgs(args); err != nil {
		return err
	}
	cfg, _, err := config.Load(env.ConfigPath)
	if err != nil {
		return err
	}
	if err := cfg.RequireSecretKey(); err != nil {
		return err
	}
	fmt.Fprintln(env.Stdout, blob.Token(cfg.SecretKeyBytes()))
	return nil
}

func genSecret(_ context.Context, env *cli.Env, args []string) error {
	if err := noArgs(args); err != nil {
		return err
	}
	fmt.Fprintln(env.Stdout, NewSecret())
	return nil
}
