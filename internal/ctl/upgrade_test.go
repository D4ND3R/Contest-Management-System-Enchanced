// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package ctl

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/buildinfo"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
)

// fakeCMSEnv makes the test binary act as the new cms binary of an
// upgrade: "version" prints that version and "migrate" leaves a marker in
// the database of CMS_DATABASE_URL.
const fakeCMSEnv = "CMS_TEST_FAKE_UPGRADE_BINARY"

func TestMain(m *testing.M) {
	if v := os.Getenv(fakeCMSEnv); v != "" {
		os.Exit(fakeCMS(v, os.Args[1:]))
	}
	os.Exit(m.Run())
}

func fakeCMS(version string, args []string) int {
	switch {
	case slices.Contains(args, "version"):
		fmt.Printf("cms %s\nsource: https://example.org/cms\n", version)
		return 0
	case slices.Contains(args, "migrate"):
		ctx := context.Background()
		conn, err := pgx.Connect(ctx, os.Getenv("CMS_DATABASE_URL"))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		defer conn.Close(ctx)
		if _, err := conn.Exec(ctx, `INSERT INTO server_settings (key, value) VALUES ('upgrade.marker', '"new"')`); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(os.Stderr, "fake cms: unexpected arguments %q\n", args)
	return 2
}

func TestUpgrade(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	const oldBinary = "#!/bin/sh\necho the old binary\n"

	type result struct {
		err           error
		out           string
		current, prev string // contents ("" missing)
		stagedLeft    bool
		marker, kept  int // rows of the new migration, of the old data
		backups       []string
	}
	upgrade := func(t *testing.T, newVersion, failAt string, force bool) result {
		t.Helper()
		ctx := context.Background()
		pool := testutil.DB(t)
		mustExec(t, pool, `INSERT INTO server_settings (key, value) VALUES ('kept', '1')`)
		url := pool.Config().ConnString()
		t.Setenv("CMS_DATABASE_URL", url)
		t.Setenv(fakeCMSEnv, newVersion)
		t.Setenv(UpgradeFailEnv, failAt)
		dir := t.TempDir()
		current := filepath.Join(dir, "cms")
		if err := os.WriteFile(current, []byte(oldBinary), 0o755); err != nil {
			t.Fatal(err)
		}
		backupDir := t.TempDir()
		var out bytes.Buffer
		u := &upgrader{
			o:       upgradeOptions{binary: self, noServices: true, force: force, backupDir: backupDir, healthTimeout: time.Second},
			cfg:     testConfig(t, url, t.TempDir()),
			out:     &out,
			current: current,
		}
		r := result{err: u.run(ctx), out: out.String()}
		read := func(p string) string {
			b, err := os.ReadFile(p)
			if err != nil {
				return ""
			}
			if len(b) > len(oldBinary) {
				return "new"
			}
			return string(b)
		}
		r.current, r.prev = read(current), read(current+".prev")
		_, err := os.Stat(filepath.Join(dir, ".cms.upgrade-new"))
		r.stagedLeft = err == nil
		pool.Reset() // a restore --clean recreated the tables
		r.marker = count(t, pool, `SELECT count(*) FROM server_settings WHERE key = 'upgrade.marker'`)
		r.kept = count(t, pool, `SELECT count(*) FROM server_settings WHERE key = 'kept'`)
		r.backups, _ = filepath.Glob(filepath.Join(backupDir, BackupPrefix+"*-pre-upgrade"+BackupSuffix))
		return r
	}

	for _, step := range upgradeSteps {
		t.Run("fail at "+step, func(t *testing.T) {
			r := upgrade(t, "v9.9.9", step, false)
			want := "everything was rolled back"
			if step == stepFetch {
				want = "aborted before changing anything"
			}
			if r.err == nil || !strings.Contains(r.err.Error(), want) || !strings.Contains(r.err.Error(), "simulated failure") {
				t.Fatalf("error %v, want %q\n%s", r.err, want, r.out)
			}
			if r.current != oldBinary || r.prev != "" || r.stagedLeft {
				t.Errorf("binaries: current %q, prev %q, staged left %v\n%s", r.current, r.prev, r.stagedLeft, r.out)
			}
			if r.marker != 0 || r.kept != 1 {
				t.Errorf("database not rolled back: marker %d, kept %d\n%s", r.marker, r.kept, r.out)
			}
			reachedMigrate := slices.Index(upgradeSteps, step) >= slices.Index(upgradeSteps, stepMigrate)
			if restored := strings.Contains(r.out, "restoring the database"); restored != reachedMigrate {
				t.Errorf("database restored: %v, want %v\n%s", restored, reachedMigrate, r.out)
			}
		})
	}

	t.Run("success", func(t *testing.T) {
		r := upgrade(t, "v9.9.9", "", false)
		if r.err != nil {
			t.Fatalf("%v\n%s", r.err, r.out)
		}
		if r.current != "new" || r.prev != oldBinary || r.stagedLeft {
			t.Errorf("binaries: current %q, prev %q, staged left %v", r.current, r.prev, r.stagedLeft)
		}
		if r.marker != 1 || r.kept != 1 || len(r.backups) != 1 {
			t.Errorf("marker %d, kept %d, backups %v", r.marker, r.kept, r.backups)
		}
		if !strings.Contains(r.out, "new binary: cms v9.9.9") || !strings.Contains(r.out, "done: cms upgraded") {
			t.Errorf("output:\n%s", r.out)
		}
	})

	t.Run("same version", func(t *testing.T) {
		r := upgrade(t, buildinfo.String(), "", false)
		if r.err == nil || !strings.Contains(r.err.Error(), "--force") {
			t.Fatalf("same version: %v\n%s", r.err, r.out)
		}
		if r.current != oldBinary || r.prev != "" || r.stagedLeft || r.marker != 0 || len(r.backups) != 0 {
			t.Errorf("something changed: %+v", r)
		}
		r = upgrade(t, buildinfo.String(), "", true)
		if r.err != nil || r.current != "new" || r.prev != oldBinary || r.marker != 1 {
			t.Errorf("--force: %+v", r)
		}
	})
}
