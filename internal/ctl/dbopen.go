// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package ctl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/db"
)

// openDB opens the database of cfg, read from the configuration file
// cfgPath ("": none was found).
func openDB(ctx context.Context, cfg *config.Config, cfgPath string) (*pgxpool.Pool, error) {
	pool, err := db.Open(ctx, cfg.Database)
	if err != nil {
		return nil, withConfigHint(err, cfgPath)
	}
	return pool, nil
}

// withConfigHint explains a database login refused with the built-in
// settings: the configuration file was not found, so the default user and
// no password were tried. The usual cause is running cmsctl as a user
// that cannot read /etc/cms/cms.yaml from a directory without cms.yaml.
func withConfigHint(err error, cfgPath string) error {
	if err == nil || cfgPath != "" || databaseFromEnv() || !isAuthError(err) {
		return err
	}
	return fmt.Errorf("%w\nno configuration file was found (looked for %s and $%s), so the built-in database "+
		"settings were used: run the command with sudo (sudo cmsctl ...) or give the file with --config",
		err, strings.Join(config.DefaultPaths, ", "), config.EnvConfigPath)
}

// databaseFromEnv reports whether the database URL comes from the
// environment (containers configured without a file).
func databaseFromEnv() bool {
	for _, name := range []string{"CMS_DATABASE_URL", "CMS_DATABASE_URL_FILE"} {
		if v, ok := os.LookupEnv(name); ok && v != "" {
			return true
		}
	}
	return false
}

// isAuthError reports whether err is PostgreSQL refusing the login: a
// wrong password (28P01) or no pg_hba.conf entry for it (28000).
func isAuthError(err error) bool {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code == "28P01" || pe.Code == "28000"
	}
	return strings.Contains(err.Error(), "password authentication failed")
}
