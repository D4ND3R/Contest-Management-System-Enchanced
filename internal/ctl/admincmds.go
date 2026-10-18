// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package ctl

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/cli"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/db"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/web"
)

// AdminCommands returns `add-admin` (CMS cmsAddAdmin), `admin-password`
// (a new password for an existing administrator) and `public-urls` (the
// addresses of the Server page, set by the installer).
func AdminCommands() []*cli.Command {
	return []*cli.Command{addAdminCommand(), adminPasswordCommand(), publicURLsCommand()}
}

var adminUsername = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// adminRoles maps --role to the permission bits of admins
// (permission_all, permission_messaging, permission_tasks).
var adminRoles = map[string][3]bool{
	"all":       {true, false, false},
	"messaging": {false, true, false},
	"tasks":     {false, false, true},
	"readonly":  {false, false, false},
}

// GeneratePassword returns a random password of n characters from an
// alphabet without look-alike characters (0/O, 1/l/I).
func GeneratePassword(n int) string {
	const alphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	var b strings.Builder
	for range n {
		i, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			panic(err) // crypto/rand never fails on supported platforms
		}
		b.WriteByte(alphabet[i.Int64()])
	}
	return b.String()
}

func addAdminCommand() *cli.Command {
	var name, passwordFile, role string
	var ifNotExists bool
	return &cli.Command{
		Name:  "add-admin",
		Args:  "[flags] USERNAME",
		Short: "create an administrator (CMS cmsAddAdmin)",
		Long: `Creates an administrator of the admin site with a bcrypt password.

The password is read from --password-file (first line; "-" is stdin) or, by
default, generated and printed ONCE on stdout. It never goes on the command
line, where other users of the machine could see it.

--role sets the permissions: all (every permission, the default, like
cmsAddAdmin), messaging (answer questions and send announcements), tasks
(problem preparer) or readonly.

With --if-not-exists an existing username is not an error: nothing changes
and "exists" is printed (the installer uses it on re-runs).`,
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&name, "name", "", "full name (default: the username)")
			fs.StringVar(&passwordFile, "password-file", "", `file with the password ("-": stdin); default: generate one`)
			fs.StringVar(&role, "role", "all", "all, messaging, tasks or readonly")
			fs.BoolVar(&ifNotExists, "if-not-exists", false, "do nothing if the username exists")
		},
		Run: func(ctx context.Context, env *cli.Env, args []string) error {
			if len(args) != 1 {
				return cli.UsageError("expected one USERNAME")
			}
			username := args[0]
			if !adminUsername.MatchString(username) {
				return cli.UsageError("the username may only contain letters, digits, '_' and '-'")
			}
			perms, ok := adminRoles[role]
			if !ok {
				return cli.UsageError("--role must be all, messaging, tasks or readonly, not %q", role)
			}
			if name == "" {
				name = username
			}
			generated := passwordFile == ""
			var password string
			if generated {
				password = GeneratePassword(20)
			} else {
				var err error
				if password, err = readPasswordFile(passwordFile); err != nil {
					return err
				}
			}
			auth, err := web.HashPassword(password, "bcrypt")
			if err != nil {
				return err
			}
			pool, err := openMigratedDB(ctx, env)
			if err != nil {
				return err
			}
			defer pool.Close()
			var id int32
			err = pool.QueryRow(ctx, `
INSERT INTO admins (name, username, authentication, enabled, permission_all, permission_messaging, permission_tasks)
VALUES ($1, $2, $3, true, $4, $5, $6)
ON CONFLICT (username) DO NOTHING
RETURNING id`, name, username, auth, perms[0], perms[1], perms[2]).Scan(&id)
			if errors.Is(err, pgx.ErrNoRows) {
				if ifNotExists {
					fmt.Fprintf(env.Stdout, "exists: admin %q already exists; nothing changed (to set a new password run cmsctl admin-password %s)\n", username, username)
					return nil
				}
				return fmt.Errorf("admin %q already exists (to set a new password: admin-password %s)", username, username)
			}
			if err != nil {
				return err
			}
			_, _ = pool.Exec(ctx, `INSERT INTO audit_log (action, target_type, target_id, details)
VALUES ('admin.create', 'admin', $1, jsonb_build_object('username', $2::text, 'role', $3::text, 'via', 'cms ctl add-admin'))`,
				id, username, role)
			fmt.Fprintf(env.Stdout, "created admin %q (id %d, role %s)\n", username, id, role)
			if generated {
				fmt.Fprintf(env.Stdout, "password: %s\n", password)
				fmt.Fprintln(env.Stderr, "The password is shown only now: store it safely.")
			}
			return nil
		},
	}
}

func adminPasswordCommand() *cli.Command {
	var passwordFile string
	var enable bool
	return &cli.Command{
		Name:  "admin-password",
		Args:  "[flags] USERNAME",
		Short: "set a new password for an administrator (the first admin's included)",
		Long: `Sets a new bcrypt password for the existing administrator USERNAME, as
add-admin would. Use it when the password of an administrator was lost,
for example the one the installer printed for the first administrator.

The password is read from --password-file (first line; "-" is stdin) or, by
default, generated and printed ONCE on stdout. It never goes on the command
line, where other users of the machine could see it.

With --enable a disabled administrator is enabled again. The permissions do
not change. The sessions open in other browsers stay open until they expire.

Like every command that uses the database, run it as a user that can read
the configuration: sudo cmsctl admin-password admin`,
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&passwordFile, "password-file", "", `file with the password ("-": stdin); default: generate one`)
			fs.BoolVar(&enable, "enable", false, "also enable the administrator if it is disabled")
		},
		Run: func(ctx context.Context, env *cli.Env, args []string) error {
			if len(args) != 1 {
				return cli.UsageError("expected one USERNAME")
			}
			username := args[0]
			generated := passwordFile == ""
			var password string
			if generated {
				password = GeneratePassword(20)
			} else {
				var err error
				if password, err = readPasswordFile(passwordFile); err != nil {
					return err
				}
			}
			auth, err := web.HashPassword(password, "bcrypt")
			if err != nil {
				return err
			}
			pool, err := openMigratedDB(ctx, env)
			if err != nil {
				return err
			}
			defer pool.Close()
			var id int32
			var enabled bool
			err = pool.QueryRow(ctx, `
UPDATE admins SET authentication = $2, enabled = enabled OR $3
WHERE username = $1
RETURNING id, enabled`, username, auth, enable).Scan(&id, &enabled)
			if errors.Is(err, pgx.ErrNoRows) {
				return unknownAdmin(ctx, pool, username)
			}
			if err != nil {
				return err
			}
			_, _ = pool.Exec(ctx, `INSERT INTO audit_log (action, target_type, target_id, details)
VALUES ('admin.password', 'admin', $1, jsonb_build_object('username', $2::text, 'enable', $3::boolean, 'via', 'cms ctl admin-password'))`,
				id, username, enable)
			fmt.Fprintf(env.Stdout, "new password set for admin %q (id %d)\n", username, id)
			if !enabled {
				fmt.Fprintf(env.Stderr, "admin %q is disabled and cannot log in: run the command again with --enable\n", username)
			}
			if generated {
				fmt.Fprintf(env.Stdout, "password: %s\n", password)
				fmt.Fprintln(env.Stderr, "The password is shown only now: store it safely.")
			}
			return nil
		},
	}
}

// unknownAdmin is the error of a username that is not an administrator:
// it lists the usernames that are.
func unknownAdmin(ctx context.Context, pool *pgxpool.Pool, username string) error {
	rows, err := pool.Query(ctx, `SELECT username FROM admins ORDER BY username`)
	if err != nil {
		return err
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return fmt.Errorf("there is no admin %q: there are no administrators; create one with add-admin", username)
	}
	return fmt.Errorf("there is no admin %q (administrators: %s)", username, strings.Join(names, ", "))
}

func readPasswordFile(path string) (string, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = readAllLimited(os.Stdin, 4096)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return "", fmt.Errorf("--password-file: %w", err)
	}
	pw, _, _ := strings.Cut(string(data), "\n")
	pw = strings.TrimRight(pw, "\r")
	if len(pw) < 8 {
		return "", errors.New("--password-file: the password must have at least 8 characters")
	}
	return pw, nil
}

// openMigratedDB opens the database and checks that its schema matches
// this binary.
func openMigratedDB(ctx context.Context, env *cli.Env) (*pgxpool.Pool, error) {
	cfg, cfgPath, err := config.Load(env.ConfigPath)
	if err != nil {
		return nil, err
	}
	pool, err := openDB(ctx, cfg, cfgPath)
	if err != nil {
		return nil, err
	}
	if err := db.CheckSchema(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// Keys of server_settings edited on the Server page of the administration
// (internal/adminweb/serverpage.go: ContestURLSetting, RankingURLSetting).
const (
	contestURLSetting = "public_url.contest"
	rankingURLSetting = "public_url.ranking"
)

// validPublicURL is the rule of the Server page: ":PORT" or an absolute
// http(s) URL without credentials, query or fragment.
func validPublicURL(v string) bool {
	if strings.HasPrefix(v, ":") {
		n, err := strconv.Atoi(v[1:])
		return err == nil && n > 0 && n < 65536
	}
	u, err := url.Parse(v)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" &&
		u.RawQuery == "" && u.Fragment == "" && u.User == nil
}

func publicURLsCommand() *cli.Command {
	var contest, ranking string
	var ifUnset bool
	return &cli.Command{
		Name:  "public-urls",
		Short: "show or set the public addresses of the contestant site and the scoreboard (Server page)",
		Long: `Shows or sets the addresses the administration uses to link to the
contestant site and to the scoreboard (the Server page). A full address
(https://contest.example.org/) is used as is; ":8000" means "the host the
admin page was loaded from, on port 8000" (installations by ports).

Without flags it prints the stored values. With --if-unset a value already
stored (for example edited on the Server page) is kept.`,
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&contest, "contest", "", `contestant site address (":PORT" or URL)`)
			fs.StringVar(&ranking, "ranking", "", `scoreboard address (":PORT" or URL)`)
			fs.BoolVar(&ifUnset, "if-unset", false, "keep the values already stored")
		},
		Run: func(ctx context.Context, env *cli.Env, args []string) error {
			if err := noArgs(args); err != nil {
				return err
			}
			for flagName, v := range map[string]string{"--contest": contest, "--ranking": ranking} {
				if v != "" && !validPublicURL(v) {
					return cli.UsageError("%s: %q is neither :PORT nor an http(s) URL", flagName, v)
				}
			}
			pool, err := openMigratedDB(ctx, env)
			if err != nil {
				return err
			}
			defer pool.Close()
			conflict := `ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`
			if ifUnset {
				conflict = `ON CONFLICT (key) DO NOTHING`
			}
			for key, v := range map[string]string{contestURLSetting: contest, rankingURLSetting: ranking} {
				if v == "" {
					continue
				}
				js, _ := json.Marshal(v)
				if _, err := pool.Exec(ctx, `INSERT INTO server_settings (key, value) VALUES ($1, $2::jsonb) `+conflict, key, string(js)); err != nil {
					return err
				}
			}
			for _, key := range []string{contestURLSetting, rankingURLSetting} {
				var v string
				err := pool.QueryRow(ctx, `SELECT value #>> '{}' FROM server_settings WHERE key = $1`, key).Scan(&v)
				if err != nil && !errors.Is(err, pgx.ErrNoRows) {
					return err
				}
				if v == "" {
					v = "(not set)"
				}
				fmt.Fprintf(env.Stdout, "%s: %s\n", key, v)
			}
			return nil
		},
	}
}
