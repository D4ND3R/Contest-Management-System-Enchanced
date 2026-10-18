// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package ctl

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/cli"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/web"
)

func TestAdminCommands(t *testing.T) {
	pool := testutil.DB(t)
	t.Setenv("CMS_DATABASE_URL", pool.Config().ConnString())
	t.Setenv("CMS_CONFIG", "")
	ctx := context.Background()
	run := func(args ...string) (string, string, error) {
		var out, errb bytes.Buffer
		err := Command().Execute(ctx, &cli.Env{Stdout: &out, Stderr: &errb, Prog: "cms"}, "cms ctl", args)
		return out.String(), errb.String(), err
	}
	admin := func(username string) (auth string, all, messaging, tasks bool) {
		t.Helper()
		err := pool.QueryRow(ctx, `SELECT authentication, permission_all, permission_messaging, permission_tasks
FROM admins WHERE username = $1`, username).Scan(&auth, &all, &messaging, &tasks)
		if err != nil {
			t.Fatalf("admin %s: %v", username, err)
		}
		return
	}
	checkPassword := func(auth, password string) {
		t.Helper()
		if ok, err := web.ValidatePassword(auth, password); !ok || err != nil {
			t.Errorf("the stored password does not match: %v", err)
		}
	}

	// A generated password is printed once, on stdout only.
	out, errs, err := run("add-admin", "root")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^password: (\S{20})$`).FindAllStringSubmatch(out, -1)
	if len(m) != 1 || strings.Count(out+errs, m[0][1]) != 1 || !strings.Contains(errs, "shown only now") {
		t.Fatalf("add-admin output:\n%s\n%s", out, errs)
	}
	auth, all, messaging, tasks := admin("root")
	if !strings.HasPrefix(auth, "bcrypt:") || !all || messaging || tasks {
		t.Errorf("root: %.7s %v %v %v", auth, all, messaging, tasks)
	}
	checkPassword(auth, m[0][1])
	if n := count(t, pool, `SELECT count(*) FROM audit_log WHERE action = 'admin.create' AND details->>'username' = 'root'`); n != 1 {
		t.Errorf("audit log rows: %d", n)
	}

	// --password-file and --role.
	pwFile := filepath.Join(t.TempDir(), "pw")
	if err := os.WriteFile(pwFile, []byte("correct horse battery\r\nignored\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, err = run("add-admin", "--password-file", pwFile, "--role", "messaging", "--name", "Mel", "mel")
	if err != nil || strings.Contains(out, "password") || !strings.Contains(out, "role messaging") {
		t.Fatalf("add-admin --password-file: %v\n%s", err, out)
	}
	auth, all, messaging, tasks = admin("mel")
	if all || !messaging || tasks {
		t.Errorf("mel: %v %v %v", all, messaging, tasks)
	}
	checkPassword(auth, "correct horse battery")

	// Existing usernames.
	if _, _, err := run("add-admin", "--password-file", pwFile, "mel"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("duplicate: %v", err)
	}
	out, _, err = run("add-admin", "--if-not-exists", "--role", "readonly", "mel")
	if err != nil || !strings.HasPrefix(out, "exists:") || strings.Contains(out, "password:") {
		t.Errorf("--if-not-exists: %v\n%s", err, out)
	}
	if auth2, _, messaging, _ := admin("mel"); auth2 != auth || !messaging {
		t.Error("--if-not-exists changed the admin")
	}

	// Bad input.
	short := filepath.Join(t.TempDir(), "short")
	os.WriteFile(short, []byte("abc\n"), 0o600)
	for _, args := range [][]string{
		{"add-admin", "--role", "boss", "x"},
		{"add-admin", "bad name"},
		{"add-admin"},
		{"add-admin", "--password-file", short, "x"},
		{"add-admin", "--password-file", filepath.Join(t.TempDir(), "missing"), "x"},
	} {
		if _, _, err := run(args...); err == nil {
			t.Errorf("%q accepted", args)
		}
	}
	if n := count(t, pool, `SELECT count(*) FROM admins`); n != 2 {
		t.Errorf("%d admins", n)
	}
}

func TestAdminPasswordCommand(t *testing.T) {
	pool := testutil.DB(t)
	t.Setenv("CMS_DATABASE_URL", pool.Config().ConnString())
	t.Setenv("CMS_CONFIG", "")
	ctx := context.Background()
	run := func(args ...string) (string, string, error) {
		var out, errb bytes.Buffer
		err := Command().Execute(ctx, &cli.Env{Stdout: &out, Stderr: &errb, Prog: "cms"}, "cms ctl", args)
		return out.String(), errb.String(), err
	}
	stored := func(username string) (auth string, enabled bool) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT authentication, enabled FROM admins WHERE username = $1`, username).Scan(&auth, &enabled); err != nil {
			t.Fatal(err)
		}
		return
	}
	if _, _, err := run("add-admin", "--role", "tasks", "boss"); err != nil {
		t.Fatal(err)
	}
	before, _ := stored("boss")

	// A generated password, printed once on stdout; the role is kept.
	out, errs, err := run("admin-password", "boss")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^password: (\S{20})$`).FindAllStringSubmatch(out, -1)
	if len(m) != 1 || strings.Count(out+errs, m[0][1]) != 1 || !strings.Contains(errs, "shown only now") {
		t.Fatalf("admin-password output:\n%s\n%s", out, errs)
	}
	auth, enabled := stored("boss")
	if auth == before || !enabled {
		t.Fatalf("password not changed (%v) or admin disabled (%v)", auth == before, enabled)
	}
	if ok, err := web.ValidatePassword(auth, m[0][1]); !ok || err != nil {
		t.Errorf("the new password does not match: %v", err)
	}
	var tasks bool
	if err := pool.QueryRow(ctx, `SELECT permission_tasks FROM admins WHERE username = 'boss'`).Scan(&tasks); err != nil || !tasks {
		t.Errorf("the role changed: %v %v", tasks, err)
	}
	if n := count(t, pool, `SELECT count(*) FROM audit_log WHERE action = 'admin.password' AND details->>'username' = 'boss'`); n != 1 {
		t.Errorf("audit log rows: %d", n)
	}

	// From a file; a disabled admin stays disabled (with a warning)
	// unless --enable.
	mustExec(t, pool, `UPDATE admins SET enabled = false WHERE username = 'boss'`)
	pwFile := filepath.Join(t.TempDir(), "pw")
	if err := os.WriteFile(pwFile, []byte("correct horse battery\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errs, err = run("admin-password", "--password-file", pwFile, "boss")
	if err != nil || strings.Contains(out, "password:") || !strings.Contains(errs, "--enable") {
		t.Fatalf("--password-file: %v\n%s\n%s", err, out, errs)
	}
	auth, enabled = stored("boss")
	if ok, _ := web.ValidatePassword(auth, "correct horse battery"); !ok || enabled {
		t.Errorf("--password-file: match %v, enabled %v", ok, enabled)
	}
	if _, _, err := run("admin-password", "--password-file", pwFile, "--enable", "boss"); err != nil {
		t.Fatal(err)
	}
	if _, enabled = stored("boss"); !enabled {
		t.Error("--enable did not enable the admin")
	}

	// Unknown usernames list the administrators; nothing changes.
	_, _, err = run("admin-password", "--password-file", pwFile, "nobody")
	if err == nil || !strings.Contains(err.Error(), `no admin "nobody"`) || !strings.Contains(err.Error(), "boss") {
		t.Errorf("unknown admin: %v", err)
	}
	for _, args := range [][]string{{"admin-password"}, {"admin-password", "a", "b"}} {
		if _, _, err := run(args...); err == nil {
			t.Errorf("%q accepted", args)
		}
	}
}

func TestPublicURLsCommand(t *testing.T) {
	pool := testutil.DB(t)
	t.Setenv("CMS_DATABASE_URL", pool.Config().ConnString())
	t.Setenv("CMS_CONFIG", "")
	run := func(args ...string) string {
		t.Helper()
		var out, errb bytes.Buffer
		err := Command().Execute(context.Background(), &cli.Env{Stdout: &out, Stderr: &errb, Prog: "cms"}, "cms ctl", append([]string{"public-urls"}, args...))
		if err != nil {
			t.Fatalf("%q: %v", args, err)
		}
		return out.String()
	}
	show := func(contest, ranking string) string {
		return contestURLSetting + ": " + contest + "\n" + rankingURLSetting + ": " + ranking + "\n"
	}
	if got := run(); got != show("(not set)", "(not set)") {
		t.Errorf("empty:\n%s", got)
	}
	if got := run("--contest", ":8000"); got != show(":8000", "(not set)") {
		t.Errorf("set contest:\n%s", got)
	}
	if got := run("--if-unset", "--contest", ":9000", "--ranking", "https://ranking.example.org/"); got != show(":8000", "https://ranking.example.org/") {
		t.Errorf("--if-unset:\n%s", got)
	}
	if got := run("--contest", "http://contest.example.org"); got != show("http://contest.example.org", "https://ranking.example.org/") {
		t.Errorf("overwrite:\n%s", got)
	}
	for _, bad := range []string{":0", ":http", "ftp://x.org", "https://u:p@x.org", "https://x.org/?a=1", "x.org"} {
		var out, errb bytes.Buffer
		err := Command().Execute(context.Background(), &cli.Env{Stdout: &out, Stderr: &errb, Prog: "cms"}, "cms ctl", []string{"public-urls", "--contest", bad})
		if err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if got := run(); got != show("http://contest.example.org", "https://ranking.example.org/") {
		t.Errorf("after bad input:\n%s", got)
	}
}
