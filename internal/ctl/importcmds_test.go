// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package ctl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/cli"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
)

// memLoader serves models built by the test, by directory.
type memLoader struct {
	mu       sync.Mutex
	contests map[string]func() *importer.Contest
	tasks    map[string]func() *importer.Task
}

var testLoader = &memLoader{contests: map[string]func() *importer.Contest{}, tasks: map[string]func() *importer.Task{}}

func init() { importer.Register(testLoader, "memory_alias") }

func (l *memLoader) Name() string        { return "memory" }
func (l *memLoader) Description() string { return "models built by the tests" }
func (l *memLoader) Detect(dir string, kind importer.Kind) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch kind {
	case importer.KindContest, importer.KindUsers, importer.KindTeams:
		return l.contests[dir] != nil
	case importer.KindTask:
		return l.tasks[dir] != nil
	}
	return false
}

func (l *memLoader) LoadContest(dir string, _ *importer.Options) (*importer.Contest, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if f := l.contests[dir]; f != nil {
		return f(), nil
	}
	return nil, fmt.Errorf("no contest at %s", dir)
}

func (l *memLoader) TaskLoader(dir, name string) (importer.TaskLoader, string) {
	return l, filepath.Join(dir, name)
}

func (l *memLoader) LoadTask(dir string, _ *importer.Options) (*importer.Task, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if f := l.tasks[dir]; f != nil {
		return f(), nil
	}
	return nil, fmt.Errorf("no task at %s", dir)
}

func (l *memLoader) LoadUsers(dir string, opts *importer.Options) ([]importer.User, error) {
	c, err := l.LoadContest(dir, opts)
	if err != nil {
		return nil, err
	}
	return c.Users, nil
}

func (l *memLoader) LoadTeams(dir string, opts *importer.Options) ([]importer.Team, error) {
	c, err := l.LoadContest(dir, opts)
	if err != nil {
		return nil, err
	}
	return c.Teams, nil
}

// memTask returns a Batch task (diff, Sum) with two testcases in dir.
func memTask(t *testing.T, dir, name, title string) func() *importer.Task {
	t.Helper()
	write := func(rel, content string) *importer.File {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return importer.FileFromPath(p)
	}
	write("statement.pdf", "statement "+name)
	for i := range 2 {
		write(fmt.Sprintf("in%d", i), fmt.Sprint(i))
		write(fmt.Sprintf("out%d", i), fmt.Sprint(2*i))
	}
	return func() *importer.Task {
		tk := importer.NewTask(name, title)
		tk.SubmissionFormat = []string{name + ".%l"}
		tk.PrimaryStatements = []string{"en"}
		tk.Statements = []importer.Statement{{Language: "en", File: importer.FileFromPath(filepath.Join(dir, "statement.pdf"))}}
		d := importer.NewDataset("Default")
		d.TimeLimit, d.MemoryLimit = importer.Ptr(1.0), importer.Ptr(int64(256*importer.MiB))
		d.TaskType, d.TaskTypeParameters = "Batch", json.RawMessage(`["alone", ["", ""], "diff"]`)
		d.ScoreType, d.ScoreTypeParameters = "Sum", json.RawMessage(`50`)
		for i := range 2 {
			d.Testcases = append(d.Testcases, importer.Testcase{Codename: fmt.Sprintf("%02d", i), Public: i == 0,
				Input:  importer.FileFromPath(filepath.Join(dir, fmt.Sprintf("in%d", i))),
				Output: importer.FileFromPath(filepath.Join(dir, fmt.Sprintf("out%d", i)))})
		}
		tk.Datasets = []*importer.Dataset{d}
		return tk
	}
}

func TestImportCommands(t *testing.T) {
	pool := testutil.DB(t)
	redis := testutil.Redis(t)
	t.Setenv("CMS_DATABASE_URL", pool.Config().ConnString())
	t.Setenv("CMS_REDIS_URL", testutil.RedisURL(t))
	t.Setenv("CMS_REDIS_KEY_PREFIX", redis.Prefix())
	t.Setenv("CMS_STORAGE_BACKEND", "local")
	t.Setenv("CMS_STORAGE_LOCAL_PATH", t.TempDir())
	ctx := context.Background()
	root := &cli.Command{Name: "ctl", Sub: ImportCommands()}
	run := func(args ...string) (string, string, error) {
		var out, errb bytes.Buffer
		err := root.Execute(ctx, &cli.Env{Stdout: &out, Stderr: &errb, Prog: "cms"}, "cms ctl", args)
		return out.String(), errb.String(), err
	}

	base := t.TempDir()
	cdir := filepath.Join(base, "demo")
	testLoader.mu.Lock()
	testLoader.tasks[filepath.Join(cdir, "sum")] = memTask(t, filepath.Join(base, "files", "sum"), "sum", "Sum")
	testLoader.tasks[filepath.Join(cdir, "mul")] = memTask(t, filepath.Join(base, "files", "mul"), "mul", "Mul")
	testLoader.contests[cdir] = func() *importer.Contest {
		c := importer.NewContest("demo")
		c.Languages = []string{"C++17 / g++"}
		c.Groups = []importer.Group{importer.NewGroup("main")}
		c.Tasks = []string{"sum", "mul"}
		for _, u := range []string{"alice", "bob"} {
			c.Users = append(c.Users, importer.User{Username: u, FirstName: u, Password: "plaintext:" + u})
			c.Participations = append(c.Participations, importer.Participation{Username: u})
		}
		c.Teams = []importer.Team{{Code: "t1", Name: "Team"}}
		return c
	}
	testLoader.mu.Unlock()

	// Without --users the participants must exist: nothing is written.
	_, stderr, err := run("import-contest", "-i", cdir)
	if err == nil || !strings.Contains(err.Error(), "users not found in the database: alice, bob") ||
		!strings.Contains(err.Error(), "(nothing was written)") {
		t.Errorf("err %v, stderr %q", err, stderr)
	}
	out, stderr, err := run("import-contest", "--dry-run", cdir, "--import-tasks", "--users")
	if err != nil || !strings.Contains(out, "created task sum") || !strings.HasSuffix(out, "dry run: nothing was written\n") {
		t.Errorf("dry run: %v\n%s%s", err, out, stderr)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tasks`).Scan(&n); err != nil || n != 0 {
		t.Errorf("a dry run wrote %d tasks (%v)", n, err)
	}
	out, stderr, err = run("import-contest", cdir, "--import-tasks", "--users") // flags after the directory
	if err != nil {
		t.Fatalf("import-contest: %v\n%s", err, stderr)
	}
	for _, want := range []string{"created contest demo (id ", "created task sum (id ", "created task mul (id ",
		"created user alice", "created participation bob", "the dispatcher was notified"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	msgs, err := judging.ReadEvents(ctx, redis, "test", 10, 0, false)
	if err != nil || len(msgs) != 1 || msgs[0].Event.Kind != judging.EventSweep || msgs[0].Event.ContestID == 0 {
		t.Errorf("events %+v %v", msgs, err)
	}
	if _, _, err := run("import-contest", "-L", "memory_alias", cdir); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("existing contest: %v", err)
	}

	// import-task: -u with the flag after the directory, -c by name.
	tdir := filepath.Join(base, "extra")
	testLoader.mu.Lock()
	testLoader.tasks[tdir] = memTask(t, filepath.Join(base, "files", "extra"), "extra", "Extra")
	testLoader.mu.Unlock()
	if out, stderr, err = run("import-task", "-c", "demo", tdir); err != nil || !strings.Contains(out, "created task extra") {
		t.Errorf("import-task: %v\n%s%s", err, out, stderr)
	}
	if _, _, err = run("import-task", tdir); err == nil || !strings.Contains(err.Error(), "use --update") {
		t.Errorf("existing task: %v", err)
	}
	if out, _, err = run("import-task", tdir, "-U", "-p", "x_"); err != nil || !strings.Contains(out, "created task x_extra") {
		t.Errorf("import-task -p: %v\n%s", err, out)
	}
	if out, _, err = run("import-task", "-u", tdir); err != nil || !strings.Contains(out, "unchanged task extra") {
		t.Errorf("import-task -u: %v\n%s", err, out)
	}
	if _, _, err = run("import-task", "-c", "nope", tdir); err == nil || !strings.Contains(err.Error(), `contest "nope" not found`) {
		t.Errorf("unknown contest: %v", err)
	}
	var num int
	if err := pool.QueryRow(ctx, `SELECT num FROM tasks WHERE name = 'extra'`).Scan(&num); err != nil || num != 2 {
		t.Errorf("extra at %d, %v", num, err)
	}

	// import-dataset.
	if _, _, err = run("import-dataset", tdir); !errors.Is(err, cli.ErrUsage) {
		t.Errorf("no description: %v", err)
	}
	if out, _, err = run("import-dataset", "-d", "second", tdir); err != nil || !strings.Contains(out, `created dataset extra "second"`) {
		t.Errorf("import-dataset: %v\n%s", err, out)
	}

	// import-users and import-team, some or all.
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE username = 'bob'`); err != nil {
		t.Fatal(err)
	}
	if out, _, err = run("import-users", cdir, "bob", "-c", "demo"); err != nil ||
		!strings.Contains(out, "created user bob") || !strings.Contains(out, "created participation bob") || strings.Contains(out, "alice") {
		t.Errorf("import-users: %v\n%s", err, out)
	}
	if _, _, err = run("import-users", cdir, "zed"); err == nil || !strings.Contains(err.Error(), "not in the package: user zed") {
		t.Errorf("unknown user: %v", err)
	}
	if out, _, err = run("import-users", "-A", "-u", cdir); err != nil || !strings.Contains(out, "unchanged user alice") {
		t.Errorf("import-users -u: %v\n%s", err, out)
	}
	if out, _, err = run("import-team", cdir); err != nil || !strings.Contains(out, "skipped team t1") {
		t.Errorf("import-team: %v\n%s", err, out)
	}

	// validate-task and validate-contest.
	out, _, err = run("validate-task", tdir)
	if err != nil || !strings.Contains(out, `task extra "Extra": 1 statement, 0 attachments, 1 dataset`) ||
		!strings.Contains(out, `dataset "Default" (active): Batch, Sum, 1 s, 256 MiB, 2 testcases (1 public), 0 managers`) ||
		!strings.HasSuffix(out, "0 errors, 0 warnings\n") {
		t.Errorf("validate-task: %v\n%s", err, out)
	}
	bad := filepath.Join(base, "bad")
	testLoader.mu.Lock()
	good := memTask(t, filepath.Join(base, "files", "bad"), "bad", "")
	testLoader.tasks[bad] = func() *importer.Task {
		tk := good()
		tk.Datasets[0].TaskType = "Batchy"
		return tk
	}
	testLoader.mu.Unlock()
	out, _, err = run("validate-task", "--json", bad)
	var v validation
	if err == nil || json.Unmarshal([]byte(out), &v) != nil || v.Errors != 1 || v.Warnings != 1 || v.Name != "bad" || v.Loader != "memory" {
		t.Errorf("validate-task --json: %v\n%s", err, out)
	}
	if _, _, err = run("validate-task", filepath.Join(base, "nothing")); err == nil || !strings.Contains(err.Error(), "autodetect") {
		t.Errorf("no loader: %v", err)
	}
	if out, _, err = run("validate-contest", cdir); err != nil || !strings.Contains(out, `contest demo "demo": 1 group, 2 tasks, 2 users, 2 participations`) {
		t.Errorf("validate-contest: %v\n%s", err, out)
	}
	if out, _, err = run("export-task", "sum", filepath.Join(t.TempDir(), "sum.zip")); err != nil || !strings.Contains(out, "exported task sum") {
		t.Errorf("export-task: %v\n%s", err, out)
	}
	if _, _, err = run("import-dump", t.TempDir()); err == nil || !strings.Contains(err.Error(), "not a dump of CMS") {
		t.Errorf("import-dump: %v", err)
	}
	// A dump exported without files (cmsDumpExporter -F): one clear error,
	// and the help says so.
	light := t.TempDir()
	model, err := os.ReadFile(filepath.Join("..", "..", "testdata", "dumps", "demo", "contest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(light, "contest.json"), model, 0o644); err != nil {
		t.Fatal(err)
	}
	out, errOut, err := run("import-dump", "--dry-run", light)
	if err == nil || strings.Count(errOut, "error:") != 1 ||
		!strings.Contains(errOut, "it was exported without files (cmsDumpExporter -F), and such a dump cannot be imported") {
		t.Errorf("import-dump of a dump without files: %v\n%s%s", err, out, errOut)
	}
	if out, _, _ = run("help", "import-dump"); !strings.Contains(out, "a dump exported without files\n(cmsDumpExporter -F) cannot be imported") {
		t.Errorf("help:\n%s", out)
	}
	// The help lists the loaders.
	if out, _, _ = run("help", "import-contest"); !strings.Contains(out, " * memory (models built by the tests; contest, task, users, teams), also -L memory_alias") {
		t.Errorf("help:\n%s", out)
	}
}

func TestPrintChanges(t *testing.T) {
	var changes []importer.Change
	for i := range 12 {
		changes = append(changes, importer.Change{Action: importer.Created, Kind: "user", Name: fmt.Sprint("u", i)})
	}
	changes = append(changes, importer.Change{Action: importer.Updated, Kind: "user", Name: "x", ID: 3})
	var b bytes.Buffer
	printChanges(&b, changes)
	if b.String() != "created 12 users\nupdated user x (id 3)\n" {
		t.Errorf("%q", b.String())
	}
}
