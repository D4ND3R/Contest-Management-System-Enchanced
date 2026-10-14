// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package pkg

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer/italy"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
)

func testWriter(t testing.TB) (*importer.Writer, *pgxpool.Pool) {
	t.Helper()
	pool := testutil.DB(t)
	bs, err := blob.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return importer.NewWriter(pool, bs, nil), pool
}

// rows runs a query and returns each row as its columns printed with %v.
func rows(t testing.TB, pool *pgxpool.Pool, sql string, args ...any) []string {
	t.Helper()
	r, err := pool.Query(context.Background(), sql, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var out []string
	for r.Next() {
		vals, err := r.Values()
		if err != nil {
			t.Fatal(err)
		}
		parts := make([]string, len(vals))
		for i, v := range vals {
			switch v := v.(type) {
			case nil:
				parts[i] = "NULL"
			case pgtype.Interval:
				parts[i] = (time.Duration(v.Microseconds)*time.Microsecond + time.Duration(v.Days)*24*time.Hour).String()
			default:
				parts[i] = fmt.Sprint(v)
			}
		}
		out = append(out, strings.Join(parts, " | "))
	}
	if err := r.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// taskRows are the rows of a task, its statements, attachments, datasets,
// managers and testcases, without ids nor the task's name and position;
// parameters as jsonb (50 and 50.0 compare equal, as for the writer).
func taskRows(t testing.TB, pool *pgxpool.Pool, name string) []string {
	t.Helper()
	var out []string
	add := func(prefix string, sql string) {
		for _, r := range rows(t, pool, sql, name) {
			out = append(out, prefix+": "+r)
		}
	}
	add("task", `SELECT title, submission_format, primary_statements, allowed_languages, token_mode, token_max_number,
		token_min_interval, token_gen_initial, token_gen_number, token_gen_interval, token_gen_max, max_submission_number,
		max_user_test_number, min_submission_interval, min_user_test_interval, feedback_level, score_precision, score_mode,
		(SELECT description FROM datasets WHERE id = active_dataset_id)
		FROM tasks WHERE name = $1`)
	add("statement", `SELECT language, digest FROM statements s JOIN tasks t ON t.id = s.task_id WHERE t.name = $1 ORDER BY language`)
	add("attachment", `SELECT filename, digest FROM attachments a JOIN tasks t ON t.id = a.task_id WHERE t.name = $1 ORDER BY filename`)
	add("dataset", `SELECT description, autojudge, time_limit, memory_limit, task_type, task_type_parameters::jsonb::text,
		score_type, score_type_parameters::jsonb::text
		FROM datasets d JOIN tasks t ON t.id = d.task_id WHERE t.name = $1 ORDER BY description`)
	add("manager", `SELECT d.description, filename, digest FROM managers m JOIN datasets d ON d.id = m.dataset_id
		JOIN tasks t ON t.id = d.task_id WHERE t.name = $1 ORDER BY d.description, filename`)
	add("testcase", `SELECT d.description, codename, public, input, output FROM testcases c JOIN datasets d ON d.id = c.dataset_id
		JOIN tasks t ON t.id = d.task_id WHERE t.name = $1 ORDER BY d.description, codename`)
	return out
}

func taskID(t testing.TB, pool *pgxpool.Pool, name string) int32 {
	t.Helper()
	var id int32
	if err := pool.QueryRow(context.Background(), "SELECT id FROM tasks WHERE name = $1", name).Scan(&id); err != nil {
		t.Fatalf("task %s: %v", name, err)
	}
	return id
}

// unchanged fails if an import changed something.
func unchanged(t testing.TB, what string, res *importer.Result) {
	t.Helper()
	for _, c := range res.Changes {
		if c.Action != importer.Unchanged {
			t.Errorf("%s: %s", what, c)
		}
	}
}

// exportAndCompare exports the task called name as a zip and as a
// directory, imports both again over the task (nothing changes) and under
// another name (the same rows), and returns the export.
func exportAndCompare(t *testing.T, w *importer.Writer, pool *pgxpool.Pool, name string) *Exported {
	t.Helper()
	ctx := context.Background()
	want := taskRows(t, pool, name)
	if len(want) == 0 {
		t.Fatalf("no rows for %s", name)
	}
	var exp *Exported
	for _, dest := range []string{filepath.Join(t.TempDir(), name+".zip"), filepath.Join(t.TempDir(), name)} {
		var err error
		exp, err = Export(ctx, pool, w.Blobs, nil, taskID(t, pool, name), "", dest)
		if err != nil {
			t.Fatalf("%s: Export: %v", dest, err)
		}
		if exp.Task != name || len(exp.Warnings) > 0 {
			t.Errorf("%s: %+v", dest, exp)
		}
		res, err := w.ImportTask(ctx, Loader{}, dest, importer.TaskImport{TaskOptions: importer.TaskOptions{Update: true}})
		if err != nil {
			t.Fatalf("%s: import over the task: %v\n%s", dest, err, issues(res.Report))
		}
		unchanged(t, dest, res)
		copyName := name + "_" + strings.TrimPrefix(filepath.Ext(dest), ".") + "copy"
		res, err = w.ImportTask(ctx, Loader{}, dest, importer.TaskImport{Name: copyName})
		if err != nil {
			t.Fatalf("%s: import as a new task: %v\n%s", dest, err, issues(res.Report))
		}
		if got := taskRows(t, pool, copyName); !slices.Equal(got, want) {
			t.Errorf("%s: the imported copy differs\n got %s\nwant %s", dest, strings.Join(got, "\n     "), strings.Join(want, "\n     "))
		}
	}
	return exp
}

// TestDatabaseRoundTrip imports packages, exports the tasks from the
// database and imports them again: the rows are the same.
func TestDatabaseRoundTrip(t *testing.T) {
	w, pool := testWriter(t)
	ctx := context.Background()
	base := with(tests(4), map[string]string{"statement/en.pdf": pdf, "statement/es.md": "# T", "solutions/main.ac.cpp": ""})
	yaml := "time_limit: 1\nmemory_limit: 64\n"
	packages := map[string]map[string]string{
		"sum": func() map[string]string {
			f := batchPackage()
			delete(f, "graders/checker.cpp")
			return f
		}(),
		"bo": with(base, map[string]string{"problem.yaml": "name: bo\ntitle: BO\n" + yaml + "output_only_tests: 4\noutput_tests: 1\n" +
			"scoring:\n  type: GroupThreshold\n  subtasks:\n    - {points: 50, tests: 1-2, threshold: 0.5, always_show_tests: true}\n    - {points: 50, tests: 3-4, threshold: 1}\n"}),
		"oo": with(base, map[string]string{"problem.yaml": "name: oo\ntitle: OO\ntype: output_only\nchecker: float\nattach_inputs: false\n" +
			"scoring: {per_test: 25}\nprimary_statements: [es]\n"}),
		"comm": with(base, map[string]string{"problem.yaml": "name: comm\ntitle: Comm\n" + yaml + "type: communication\nprocesses: 2\n" +
			"scoring:\n  type: GroupMul\n  subtasks:\n    - {points: 30, regex: \"0[12]\"}\n    - {points: 70, regex: \"0[34]\"}\n",
			"graders/manager*": "\x7fELF", "graders/grader.cpp": "g", "graders/grader.java": "g"}),
		"inter": with(base, map[string]string{"problem.yaml": "name: inter\ntitle: Inter\n" + yaml + "type: interactive\n" +
			"controller_memory_limit: 64\ncontroller_time_limit: 1\ncontroller_wall_time_limit: 3\n" +
			"tokens: {mode: infinite, max_number: 3, gen_interval: 10}\nmax_user_tests: 5\nmin_user_test_interval: 2\n" +
			"scoring:\n  type: GroupMin\n  points: 60\n  subtasks:\n    - {points: 20, count: 1}\n    - {points: 40, count: 3}\n",
			"graders/controller*": "#!/bin/sh\n"}),
		"steps": with(base, map[string]string{"problem.yaml": "name: steps\ntitle: Steps\n" + yaml + "type: two_steps\n" +
			"submission_format: [encoder.%l, decoder.%l]\nlanguages: [C++20 / g++]\n", "graders/manager.cpp": "m", "graders/manager.h": "h"}),
	}
	for name, files := range packages {
		res, err := w.ImportTask(ctx, Loader{}, pkgDir(t, files), importer.TaskImport{})
		if err != nil {
			t.Fatalf("%s: %v\n%s", name, err, issues(res.Report))
		}
	}
	for name := range packages {
		t.Run(name, func(t *testing.T) { exportAndCompare(t, w, pool, name) })
	}
}

// TestDatabaseRoundTripItaly exports the tasks of the italy_yaml demo
// contest, imported by its own loader, and imports them as packages of
// this format: the rows are the same.
func TestDatabaseRoundTripItaly(t *testing.T) {
	w, pool := testWriter(t)
	ctx := context.Background()
	res, err := w.ImportContest(ctx, italy.Loader{}, demoContest(t), importer.ContestOptions{ImportTasks: true, ImportUsers: true})
	if err != nil {
		t.Fatalf("%v\n%s", err, issues(res.Report))
	}
	for _, name := range []string{"sum", "half", "quiz"} {
		t.Run(name, func(t *testing.T) { exportAndCompare(t, w, pool, name) })
	}
	// The synthetic italy_yaml tasks too (all task types).
	dir := italyTasks(t)
	for _, name := range []string{"zgr", "zcomm", "zinter"} {
		res, err := w.ImportTask(ctx, italy.Loader{}, filepath.Join(dir, name), importer.TaskImport{})
		if err != nil {
			t.Fatalf("%s: %v\n%s", name, err, issues(res.Report))
		}
		t.Run(name, func(t *testing.T) { exportAndCompare(t, w, pool, name) })
	}
}

func TestExportDatasets(t *testing.T) {
	w, pool := testWriter(t)
	ctx := context.Background()
	files := with(tests(2), map[string]string{"problem.yaml": "name: ds\ntitle: DS\ntime_limit: 1\nmemory_limit: 64\n",
		"statement/en.pdf": pdf, "solutions/a.ac.cpp": ""})
	if _, err := w.ImportTask(ctx, Loader{}, pkgDir(t, files), importer.TaskImport{}); err != nil {
		t.Fatal(err)
	}
	// A second dataset: another time limit, one more test.
	files["problem.yaml"] = "name: ds\ntitle: DS\ntime_limit: 2\nmemory_limit: 64\ndataset: faster machines\n"
	files["tests/03.in"], files["tests/03.out"] = "3", "3"
	if res, err := w.ImportDataset(ctx, Loader{}, pkgDir(t, files), "faster machines", nil); err != nil {
		t.Fatalf("%v\n%s", err, issues(res.Report))
	}
	id := taskID(t, pool, "ds")

	exp, err := Export(ctx, pool, w.Blobs, nil, id, "", filepath.Join(t.TempDir(), "active.zip"))
	if err != nil {
		t.Fatal(err)
	}
	if exp.Dataset != "Default" || !slices.Equal(exp.OtherDatasets, []string{"faster machines"}) || exp.Files != 1+1+4 {
		t.Errorf("active: %+v", exp)
	}
	dest := filepath.Join(t.TempDir(), "other")
	exp, err = Export(ctx, pool, w.Blobs, nil, id, "faster machines", dest)
	if err != nil {
		t.Fatal(err)
	}
	if exp.Dataset != "faster machines" || !slices.Equal(exp.OtherDatasets, []string{"Default"}) {
		t.Errorf("other: %+v", exp)
	}
	pk, rep := load(t, dest)
	if d := pk.Task.Datasets[0]; d.Description != "faster machines" || *d.TimeLimit != 2 || len(d.Testcases) != 3 {
		t.Errorf("dataset %+v\n%s", d, issues(rep))
	}
	// Importing it over the task changes nothing (the dataset exists).
	res, err := w.ImportTask(ctx, Loader{}, dest, importer.TaskImport{TaskOptions: importer.TaskOptions{Update: true}})
	if err != nil {
		t.Fatal(err)
	}
	unchanged(t, "other dataset", res)

	for _, c := range []struct {
		id          int32
		description string
		dest        string
		err         string
	}{
		{id, "missing", filepath.Join(t.TempDir(), "x.zip"), `task ds has no dataset "missing"`},
		{id + 1000, "", filepath.Join(t.TempDir(), "x.zip"), "does not exist"},
		{id, "", dest, "already exists and is not empty"},
	} {
		if _, err := Export(ctx, pool, w.Blobs, nil, c.id, c.description, c.dest); err == nil || !strings.Contains(err.Error(), c.err) {
			t.Errorf("error %v, want %q", err, c.err)
		}
	}
	// An existing zip is not overwritten.
	existing := filepath.Join(t.TempDir(), "existing.zip")
	if err := os.WriteFile(existing, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Export(ctx, pool, w.Blobs, nil, id, "", existing); err == nil {
		t.Error("an existing zip was overwritten")
	}
	if data, _ := os.ReadFile(existing); string(data) != "keep" {
		t.Errorf("existing zip: %q", data)
	}
	// A blob missing from the store: the export fails and leaves nothing.
	if _, err := pool.Exec(ctx, `UPDATE testcases SET input = repeat('0', 64)
		WHERE dataset_id = (SELECT active_dataset_id FROM tasks WHERE id = $1) AND codename = '01'`, id); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(t.TempDir(), "broken.zip")
	if _, err := Export(ctx, pool, w.Blobs, nil, id, "", broken); err == nil || !strings.Contains(err.Error(), "tests/01.in") {
		t.Errorf("missing blob: %v", err)
	}
	if _, err := os.Stat(broken); !os.IsNotExist(err) {
		t.Errorf("a broken export was left: %v", err)
	}
}

// TestImportPackageDryRun: a dry run of a zip package compiles and hashes
// without storing anything.
func TestImportPackageDryRun(t *testing.T) {
	w, pool := testWriter(t)
	w.DryRun = true
	p := writeZip(t, zipOf("", smallPackage()))
	res, err := w.ImportTask(context.Background(), Loader{}, p, importer.TaskImport{})
	if err != nil {
		t.Fatalf("%v\n%s", err, issues(res.Report))
	}
	if res.Count("task", importer.Created) != 1 {
		t.Errorf("changes %v", res.Changes)
	}
	if n := rows(t, pool, "SELECT count(*) FROM tasks"); n[0] != "0" {
		t.Errorf("%s tasks written", n[0])
	}
}
