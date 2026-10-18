// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

//go:build unix

package ctl

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
)

// TestBackupAsRootKeepsTheOwner: `sudo cmsctl backup` leaves the backups
// directory and the archive to the owner of the data directory (the
// services' user), like the backups of the timer.
func TestBackupAsRootKeepsTheOwner(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root (chown)")
	}
	const nobody = 65534
	src := testutil.DB(t)
	cfg := testConfig(t, src.Config().ConnString(), t.TempDir())
	if err := os.Chown(cfg.DataDir, nobody, nobody); err != nil {
		t.Fatal(err)
	}
	file, err := Backup(context.Background(), cfg, "", BackupOptions{NoBlobs: true}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(file) != filepath.Join(cfg.DataDir, "backups") {
		t.Fatalf("backup written to %s", file)
	}
	for _, p := range []string{filepath.Dir(file), file} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if uid := st.Sys().(*syscall.Stat_t).Uid; uid != nobody {
			t.Errorf("%s: owner %d, want %d", p, uid, nobody)
		}
	}
}
