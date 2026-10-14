// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package ctl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/cli"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
)

// TestExportTaskCommand imports a package of the own format, exports it
// as a zip and as a directory, and imports both again.
func TestExportTaskCommand(t *testing.T) {
	pool := testutil.DB(t)
	t.Setenv("CMS_DATABASE_URL", pool.Config().ConnString())
	t.Setenv("CMS_REDIS_URL", testutil.RedisURL(t))
	t.Setenv("CMS_STORAGE_BACKEND", "local")
	t.Setenv("CMS_STORAGE_LOCAL_PATH", t.TempDir())
	ctx := context.Background()
	root := &cli.Command{Name: "ctl", Sub: ImportCommands()}
	run := func(args ...string) (string, string, error) {
		var out, errb bytes.Buffer
		err := root.Execute(ctx, &cli.Env{Stdout: &out, Stderr: &errb, Prog: "cms"}, "cms ctl", args)
		return out.String(), errb.String(), err
	}

	src := filepath.Join(t.TempDir(), "pkgsum")
	for name, content := range map[string]string{
		"problem.yaml":       "name: pkgsum\ntitle: Sum\ntime_limit: 1\nmemory_limit: 64\npublic_tests: 1\n",
		"statement/en.md":    "# Sum\n",
		"tests/1.in":         "1 2\n",
		"tests/1.out":        "3\n",
		"tests/2.in":         "2 2\n",
		"tests/2.out":        "4\n",
		"solutions/a.ac.cpp": "",
	} {
		p := filepath.Join(src, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if out, stderr, err := run("import-task", "-L", "problem_yaml", src); err != nil || !strings.Contains(out, "created task pkgsum") {
		t.Fatalf("import-task: %v\n%s%s", err, out, stderr)
	}
	if out, stderr, err := run("import-dataset", "-d", "second", src); err != nil {
		t.Fatalf("import-dataset: %v\n%s%s", err, out, stderr)
	}

	dir := t.TempDir()
	zipPath := filepath.Join(dir, "pkgsum.zip")
	out, stderr, err := run("export-task", "pkgsum", zipPath)
	if err != nil || out != "exported task pkgsum, dataset \"Default\", to "+zipPath+" (6 files)\n"+
		"not exported: 1 other dataset \"second\" (export them with -d)\n" || stderr != "" {
		t.Errorf("export-task: %v\n%q\n%s", err, out, stderr)
	}
	// Flags after the arguments, the task by id, a directory.
	var id int32
	if err := pool.QueryRow(ctx, `SELECT id FROM tasks WHERE name = 'pkgsum'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	if out, _, err = run("export-task", fmt.Sprint(id), outDir, "-d", "second"); err != nil ||
		!strings.Contains(out, `dataset "second"`) {
		t.Errorf("export-task -d: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(outDir, "problem.yaml")); err != nil {
		t.Error(err)
	}
	// Both import back over the task without changing anything.
	for _, p := range []string{zipPath, outDir} {
		if out, stderr, err = run("import-task", "-u", p); err != nil || !strings.Contains(out, "unchanged task pkgsum") ||
			strings.Contains(out, "updated") || strings.Contains(out, "created") {
			t.Errorf("import-task -u %s: %v\n%s%s", p, err, out, stderr)
		}
	}

	for _, c := range []struct {
		args []string
		err  string
	}{
		{[]string{"export-task", "pkgsum"}, "want the task"},
		{[]string{"export-task", "nope", filepath.Join(dir, "x.zip")}, `task "nope" not found`},
		{[]string{"export-task", "pkgsum", filepath.Join(dir, "y.zip"), "-d", "third"}, `task pkgsum has no dataset "third"`},
		{[]string{"export-task", "pkgsum", zipPath}, "exists"},
		{[]string{"export-task", "pkgsum", outDir}, "already exists and is not empty"},
	} {
		if _, _, err := run(c.args...); err == nil || !strings.Contains(err.Error(), c.err) {
			t.Errorf("%v: %v, want %q", c.args, err, c.err)
		}
	}
	if _, _, err := run("export-task"); !errors.Is(err, cli.ErrUsage) {
		t.Errorf("no arguments: %v", err)
	}
}
