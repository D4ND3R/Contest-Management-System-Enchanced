// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package importer

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
)

func TestRegistry(t *testing.T) {
	r := NewRegistry()
	a, b := newFakeLoader("alpha"), newFakeLoader("beta")
	if err := r.Register(a, "alpha_contest", "alpha_task"); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(taskOnly{b}); err != nil {
		t.Fatal(err)
	}
	for _, dup := range []Loader{newFakeLoader("alpha"), newFakeLoader("alpha_task")} {
		if err := r.Register(dup); err == nil {
			t.Errorf("%s registered twice", dup.Name())
		}
	}
	if err := r.Register(newFakeLoader("gamma"), "beta"); err == nil {
		t.Error("an alias that is a name accepted")
	}
	if l, ok := r.Lookup("alpha_task"); !ok || l != a {
		t.Error("lookup by alias")
	}
	if _, ok := r.Lookup("gamma"); ok {
		t.Error("a loader whose registration failed is registered")
	}

	// By name.
	if l, err := r.ChooseTask("beta", "/x"); err != nil || l.Name() != "beta" {
		t.Errorf("ChooseTask(beta) = %v, %v", l, err)
	}
	if _, err := r.ChooseContest("beta", "/x"); err == nil || !strings.Contains(err.Error(), `loader "beta" cannot read a contest (loaders of contests: alpha)`) {
		t.Errorf("a task loader for a contest: %v", err)
	}
	if _, err := r.ChooseUsers("nope", "/x"); !errors.Is(err, ErrUnknownLoader) {
		t.Errorf("unknown loader: %v", err)
	}
	// Autodetection: exactly one loader must accept the directory.
	a.tasks["/both"], b.tasks["/both"] = &Task{}, &Task{}
	a.tasks["/a"], b.tasks["/b"] = &Task{}, &Task{}
	a.teams["/teams"] = []Team{}
	for dir, want := range map[string]string{"/a": "alpha", "/b": "beta"} {
		if l, err := r.ChooseTask("", dir); err != nil || l.Name() != want {
			t.Errorf("ChooseTask(%s) = %v, %v; want %s", dir, l, err, want)
		}
	}
	if _, err := r.ChooseTask("", "/both"); !errors.Is(err, ErrDetect) || !strings.Contains(err.Error(), "alpha, beta") {
		t.Errorf("two loaders: %v", err)
	}
	if _, err := r.ChooseTask("", "/none"); !errors.Is(err, ErrDetect) {
		t.Errorf("no loader: %v", err)
	}
	if l, err := r.ChooseTeams("", "/teams"); err != nil || l.Name() != "alpha" {
		t.Errorf("ChooseTeams = %v, %v", l, err)
	}
	help := r.Help()
	for _, want := range []string{
		" * alpha (fake format alpha; contest, task, users, teams), also -L alpha_contest, -L alpha_task\n",
		" * beta (fake format beta; task)\n",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("help %q does not contain %q", help, want)
		}
	}
	if !strings.Contains(NewRegistry().Help(), "(none)") {
		t.Error("help of an empty registry")
	}
}

func TestUpload(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := blob.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := writeFile(t, dir, "a.txt", "alpha")
	var opens atomic.Int32
	fn := FileFromFunc("member", func() (io.ReadCloser, error) {
		opens.Add(1)
		return io.NopCloser(strings.NewReader("from func")), nil
	})
	a1, a2 := FileFromPath(p), FileFromPath(filepath.Join(dir, ".", "a.txt"))
	b := FileFromBytes("bytes", []byte("bravo"))
	empty := FileFromBytes("empty", nil)
	files := []*File{a1, a2, b, fn, fn, nil, empty}
	if err := Upload(ctx, store, files, 2); err != nil {
		t.Fatal(err)
	}
	for f, want := range map[*File]string{a1: "alpha", a2: "alpha", b: "bravo", fn: "from func", empty: ""} {
		if f.Digest() != sha(want) || f.Size() != int64(len(want)) {
			t.Errorf("%s: digest %s size %d, want %s %d", f.Name(), f.Digest(), f.Size(), sha(want), len(want))
		}
		rc, err := store.Open(ctx, f.Digest())
		if err != nil {
			t.Fatalf("%s: %v", f.Name(), err)
		}
		got, err := io.ReadAll(rc)
		rc.Close()
		if err != nil || string(got) != want {
			t.Errorf("%s: stored %q, %v", f.Name(), got, err)
		}
	}
	if opens.Load() != 1 {
		t.Errorf("the same file was read %d times", opens.Load())
	}
	// Stored files are not read again.
	if err := Upload(ctx, store, []*File{fn}, 0); err != nil || opens.Load() != 1 {
		t.Errorf("err %v, %d reads", err, opens.Load())
	}

	// Hash computes digests without storing; a later Upload stores.
	h := FileFromBytes("hashed", []byte("hotel"))
	if err := Hash(ctx, store, []*File{h}, 0); err != nil || h.Digest() != sha("hotel") || h.Size() != 5 {
		t.Errorf("Hash: %v %s %d", err, h.Digest(), h.Size())
	}
	if _, err := store.Stat(ctx, sha("hotel")); !errors.Is(err, blob.ErrNotFound) {
		t.Errorf("Hash stored the file: %v", err)
	}
	if err := Upload(ctx, store, []*File{h}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Stat(ctx, sha("hotel")); err != nil {
		t.Errorf("Upload after Hash: %v", err)
	}
	if err := Hash(ctx, store, []*File{FileFromDigest("lost", sha("nothing"))}, 0); !errors.Is(err, ErrMissingFile) {
		t.Errorf("Hash of a missing digest: %v", err)
	}

	// A file known by digest must be in the store.
	known := FileFromDigest("known", sha("bravo"))
	if err := Upload(ctx, store, []*File{known}, 0); err != nil || known.Size() != 5 {
		t.Errorf("known digest: %v, size %d", err, known.Size())
	}
	if _, err := known.Open(); err == nil {
		t.Error("a file known by digest opened")
	}
	for _, f := range []*File{FileFromDigest("lost", sha("nothing")), FileFromPath(filepath.Join(dir, "missing"))} {
		if err := Upload(ctx, store, []*File{f}, 0); !errors.Is(err, ErrMissingFile) || !strings.Contains(err.Error(), f.Name()) {
			t.Errorf("%s: %v", f.Name(), err)
		}
	}
	bad := FileFromFunc("bad", func() (io.ReadCloser, error) { return nil, os.ErrPermission })
	if err := Upload(ctx, store, []*File{bad}, 0); !errors.Is(err, os.ErrPermission) {
		t.Errorf("bad: %v", err)
	}
	if a1.Path() != p || b.Path() != "" {
		t.Error("Path")
	}
}

func TestImportContestReadsOnlyWhatItNeeds(t *testing.T) {
	ctx := context.Background()
	w, _ := testWriter(t)
	fl := newFakeLoader("fake")
	fl.contests["/demo"] = demoContest("demo", "a", "b")
	for name, tk := range demoTasks(t, "a", "b", "c") {
		fl.tasks["/demo/"+name] = tk
	}
	loads := func() string {
		var parts []string
		for _, n := range []string{"a", "b", "c"} {
			parts = append(parts, string(rune('0'+fl.loads["/demo/"+n])))
		}
		return strings.Join(parts, "")
	}

	res, err := w.ImportContest(ctx, fl, "/demo", ContestOptions{ImportTasks: true, ImportUsers: true})
	if err != nil {
		t.Fatalf("ImportContest: %v\n%s", err, issues(res.Report))
	}
	if loads() != "110" || res.Count("task", Created) != 2 || res.ContestID == 0 {
		t.Errorf("loads %s, changes:\n%s", loads(), changes(res))
	}
	// Existing contest without update flags: an error before reading tasks.
	if _, err := w.ImportContest(ctx, fl, "/demo", ContestOptions{ImportTasks: true}); !errors.Is(err, ErrExists) || loads() != "110" {
		t.Errorf("err %v, loads %s", err, loads())
	}
	// -u: the tasks exist, none is read.
	if res, err = w.ImportContest(ctx, fl, "/demo", ContestOptions{UpdateContest: true, ImportTasks: true}); err != nil || loads() != "110" {
		t.Errorf("err %v, loads %s", err, loads())
	}
	// -U: all are read again; a new task needs -i and is not read without it.
	fl.contests["/demo"] = demoContest("demo", "a", "b", "c")
	_, err = w.ImportContest(ctx, fl, "/demo", ContestOptions{UpdateTasks: true})
	if err == nil || !strings.Contains(err.Error(), `task "c" not found in the database: use --import-tasks`) || loads() != "220" {
		t.Errorf("err %v, loads %s", err, loads())
	}
	res, err = w.ImportContest(ctx, fl, "/demo", ContestOptions{UpdateTasks: true, ImportTasks: true})
	if err != nil || loads() != "331" || change(res, "task", "c").Action != Created || change(res, "task", "a").Action != Unchanged {
		t.Errorf("err %v, loads %s, changes:\n%s", err, loads(), changes(res))
	}
	// A loader error names the task.
	delete(fl.tasks, "/demo/b")
	if _, err := w.ImportContest(ctx, fl, "/demo", ContestOptions{UpdateTasks: true}); err == nil || !strings.Contains(err.Error(), "loading task b at /demo/b: no task at /demo/b") {
		t.Errorf("err %v", err)
	}
	if _, err := w.ImportContest(ctx, fl, "/nowhere", ContestOptions{}); err == nil || !strings.Contains(err.Error(), "loading the contest at /nowhere") {
		t.Errorf("err %v", err)
	}
}

func TestImportTaskUsersTeams(t *testing.T) {
	ctx := context.Background()
	w, _ := testWriter(t)
	fl := newFakeLoader("fake")
	fl.tasks["/sum"] = batchTask(t, t.TempDir(), "sum", 2)
	res, err := w.ImportTask(ctx, fl, "/sum", TaskImport{Name: "addition", Prefix: "ioi_"})
	if err != nil {
		t.Fatalf("ImportTask: %v\n%s", err, issues(res.Report))
	}
	if res.TaskIDs["ioi_addition"] == 0 || change(res, "task", "ioi_addition").Action != Created {
		t.Errorf("changes:\n%s", changes(res))
	}
	fl.tasks["/sum"] = batchTask(t, t.TempDir(), "sum", 2) // loaders return a new model every time
	res, err = w.ImportTask(ctx, fl, "/sum", TaskImport{Prefix: "ioi_", TaskOptions: TaskOptions{SkipStatements: true}})
	if err != nil || change(res, "task", "ioi_sum").Action != Created {
		t.Errorf("err %v, changes:\n%s", err, changes(res))
	}
	if n := count(t, w.Pool, `SELECT count(*) FROM statements s JOIN tasks t ON t.id = s.task_id WHERE t.name = 'ioi_sum'`); n != 0 {
		t.Errorf("%d statements imported with SkipStatements", n)
	}
	if _, err := w.ImportTask(ctx, fl, "/nope", TaskImport{}); err == nil || !strings.Contains(err.Error(), "loading the task at /nope") {
		t.Errorf("err %v", err)
	}

	fl.users["/people"] = []User{{Username: "u1", Password: "plaintext:a"}, {Username: "u2"}}
	fl.teams["/people"] = []Team{{Code: "t1", Name: "Team 1"}}
	if res, err = w.ImportUsers(ctx, fl, "/people", UserOptions{}); err != nil || res.Count("user", Created) != 2 {
		t.Errorf("err %v, changes:\n%s", err, changes(res))
	}
	if res, err = w.ImportTeams(ctx, fl, "/people", TeamOptions{}); err != nil || res.Count("team", Created) != 1 {
		t.Errorf("err %v, changes:\n%s", err, changes(res))
	}
}

func TestValidateDirs(t *testing.T) {
	fl := newFakeLoader("fake")
	good := batchTask(t, t.TempDir(), "sum", 2)
	bad := batchTask(t, t.TempDir(), "bad", 2)
	bad.Datasets[0].ScoreType = "Nope"
	fl.tasks["/sum"], fl.tasks["/c/sum"], fl.tasks["/c/wrong"] = good, good, bad
	tk, rep, err := ValidateTaskDir(fl, "/sum", nil)
	if err != nil || tk != good || len(rep.Issues()) != 0 {
		t.Errorf("task %v, err %v, issues:\n%s", tk, err, issues(rep))
	}
	if _, _, err := ValidateTaskDir(fl, "/none", nil); err == nil {
		t.Error("no error for a missing task")
	}

	fl.contests["/c"] = demoContest("c", "sum", "wrong", "missing")
	c, tasks, rep, err := ValidateContestDir(fl, "/c", nil)
	if err != nil || c == nil || len(tasks) != 2 {
		t.Fatalf("contest %v, %d tasks, err %v", c, len(tasks), err)
	}
	var b bytes.Buffer
	_, _ = rep.WriteTo(&b)
	for _, want := range []string{
		`error: task wrong: the package of task "wrong" contains task "bad"`,
		`error: task bad: dataset "Default": unknown score type "Nope"`,
		"error: task missing: loading task missing at /c/missing: no task at /c/missing",
	} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("report:\n%s\nlacks %q", b.String(), want)
		}
	}
}
