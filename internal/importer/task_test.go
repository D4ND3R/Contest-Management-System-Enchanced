// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
)

func TestWriteTaskRoundTrip(t *testing.T) {
	ctx := context.Background()
	w, _ := testWriter(t)
	pool := w.Pool
	dir := t.TempDir()
	tk := batchTask(t, dir, "sum", 4)
	tk.ScorePrecision = 2
	tk.ScoreMode = ScoreModeMaxSubtask
	tk.FeedbackLevel = FeedbackOIRestricted
	tk.AllowedLanguages = []string{"C++17 / g++"}
	tk.MaxSubmissionNumber = Ptr(int32(50))
	tk.MinSubmissionInterval = Ptr(90 * time.Second)
	tk.Tokens = Tokens{Mode: TokenModeFinite, MaxNumber: Ptr(int32(3)), MinInterval: time.Second,
		GenInitial: 1, GenNumber: 1, GenInterval: time.Hour, GenMax: Ptr(int32(2))}
	v2 := NewDataset("v2")
	v2.Autojudge = true
	v2.TimeLimit = Ptr(0.25)
	v2.TaskType, v2.TaskTypeParameters = "Batch", json.RawMessage(`["alone",["input.txt","output.txt"],"diff"]`)
	v2.ScoreType, v2.ScoreTypeParameters = "Sum", json.RawMessage(`100`)
	v2.Testcases = []Testcase{{Codename: "only", Public: true,
		Input: file(t, dir, "v2/in", "5 5\n"), Output: file(t, dir, "v2/out", "10\n")}}
	tk.Datasets = append(tk.Datasets, v2)
	tk.ActiveDataset = "v2"

	res, err := w.WriteTask(ctx, tk, TaskOptions{})
	if err != nil {
		t.Fatalf("WriteTask: %v\n%s", err, issues(res.Report))
	}
	id := res.TaskIDs["sum"]
	if c := change(res, "task", "sum"); c.Action != Created || c.ID != id || id == 0 ||
		c.Details != "2 statements; 1 attachment; 2 datasets; 5 testcases" {
		t.Errorf("changes:\n%s", changes(res))
	}
	if len(res.Events) != 0 || res.ContestID != 0 {
		t.Errorf("a task without contest: events %v, contest %d", res.Events, res.ContestID)
	}

	wantRows(t, pool, []string{
		"Task sum|{sum.%l}|{en}|{\"C++17 / g++\"}|finite|3|00:00:01|1|1|01:00:00|2|50||00:01:30||oi_restricted|2|max_subtask||",
	}, `SELECT concat_ws('|', title, submission_format, primary_statements, allowed_languages, token_mode,
		token_max_number, token_min_interval, token_gen_initial, token_gen_number, token_gen_interval, token_gen_max,
		max_submission_number, coalesce(max_user_test_number::text, ''), min_submission_interval,
		coalesce(min_user_test_interval::text, ''), feedback_level, score_precision, score_mode,
		coalesce(contest_id::text, ''), coalesce(num::text, '')) FROM tasks WHERE id = $1`, id)
	wantRows(t, pool, []string{"en " + sha("statement of sum"), "it " + sha("testo di sum")},
		`SELECT language || ' ' || digest FROM statements WHERE task_id = $1 ORDER BY language`, id)
	wantRows(t, pool, []string{"sample.txt " + sha("1 2\n")},
		`SELECT filename || ' ' || digest FROM attachments WHERE task_id = $1`, id)
	wantRows(t, pool, []string{
		`Default|f|1.5|268435456|Batch|["alone", ["", ""], "comparator"]|GroupMin|[[40, 2], [60, 2]]`,
		`v2|t|0.25||Batch|["alone", ["input.txt", "output.txt"], "diff"]|Sum|100`,
	}, `SELECT concat_ws('|', description, autojudge, time_limit, coalesce(memory_limit::text, ''), task_type,
		task_type_parameters, score_type, score_type_parameters) FROM datasets WHERE task_id = $1 ORDER BY id`, id)
	if got := text(t, pool, `SELECT d.description FROM tasks t JOIN datasets d ON d.id = t.active_dataset_id WHERE t.id = $1`, id); got != "v2" {
		t.Errorf("active dataset %q, want v2", got)
	}
	wantRows(t, pool, []string{"Default checker " + sha("#!/bin/sh\necho 1\n")},
		`SELECT d.description || ' ' || m.filename || ' ' || m.digest FROM managers m JOIN datasets d ON d.id = m.dataset_id
		 WHERE d.task_id = $1`, id)
	var tcs []string
	for i := range 4 {
		tcs = append(tcs, fmt.Sprintf("Default %03d %s %s %s", i, map[bool]string{true: "t", false: "f"}[i == 0], sha(fmt.Sprintf("%d %d\n", i, i)), sha(fmt.Sprintf("%d\n", 2*i))))
	}
	tcs = append(tcs, "v2 only t "+sha("5 5\n")+" "+sha("10\n"))
	wantRows(t, pool, tcs, `SELECT concat_ws(' ', d.description, tc.codename, tc.public, tc.input, tc.output)
		FROM testcases tc JOIN datasets d ON d.id = tc.dataset_id WHERE d.task_id = $1 ORDER BY d.id, tc.codename`, id)

	// Every file is in the blob store, under its SHA-256.
	for _, f := range tk.Files() {
		size, err := w.Blobs.Stat(ctx, f.Digest())
		if err != nil || size != f.Size() || f.Size() <= 0 {
			t.Errorf("%s: blob %s size %d (%d): %v", f.Name(), f.Digest(), size, f.Size(), err)
		}
	}

	// The same task again: an error without Update, nothing changes.
	_, err = w.WriteTask(ctx, batchTask(t, t.TempDir(), "sum", 4), TaskOptions{})
	if !errors.Is(err, ErrExists) || !strings.Contains(err.Error(), "use --update") {
		t.Errorf("existing task without update: %v", err)
	}
}

func TestWriteTaskUpdate(t *testing.T) {
	ctx := context.Background()
	w, _ := testWriter(t)
	pool := w.Pool
	contestID := contestWithGroup(t, w, "c")
	res, err := w.WriteTask(ctx, batchTask(t, t.TempDir(), "sum", 4), TaskOptions{ContestID: contestID})
	if err != nil {
		t.Fatalf("WriteTask: %v\n%s", err, issues(res.Report))
	}
	id := res.TaskIDs["sum"]
	if want := (judging.Event{Kind: judging.EventDataset, ContestID: contestID, TaskID: id}); !sameEvents(res.Events, want) {
		t.Errorf("events %+v, want %+v", res.Events, want)
	}
	if got := text(t, pool, `SELECT concat_ws(' ', contest_id, num) FROM tasks WHERE id = $1`, id); got != fmt.Sprintf("%d 0", contestID) {
		t.Errorf("contest and position %q", got)
	}
	defaultID := activeDataset(t, pool, id)
	tcIDs := map[string]int32{}
	rows, err := pool.Query(ctx, `SELECT codename, id FROM testcases WHERE dataset_id = $1`, defaultID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var c string
		var tid int32
		if err := rows.Scan(&c, &tid); err != nil {
			t.Fatal(err)
		}
		tcIDs[c] = tid
	}
	sub := addScored(t, pool, contestID, id, defaultID, "alice", 100.0/3)
	if _, err := pool.Exec(ctx, `INSERT INTO evaluations (submission_id, dataset_id, testcase_id, outcome)
		SELECT $1, dataset_id, id, '1.0' FROM testcases WHERE dataset_id = $2`, sub, defaultID); err != nil {
		t.Fatal(err)
	}

	// The same package again: nothing changes.
	res, err = w.WriteTask(ctx, batchTask(t, t.TempDir(), "sum", 4), TaskOptions{Update: true})
	if err != nil {
		t.Fatalf("WriteTask: %v\n%s", err, issues(res.Report))
	}
	if c := change(res, "task", "sum"); c.Action != Unchanged || len(res.Changes) != 1 || len(res.Report.Issues()) != 0 {
		t.Errorf("changes:\n%s\nissues:\n%s", changes(res), issues(res.Report))
	}

	// A new version: new title and precision, statements, attachments,
	// judging data of the active dataset and a new dataset.
	dir := t.TempDir()
	tk := batchTask(t, dir, "sum", 4)
	tk.Title = "Sum v2"
	tk.ScorePrecision = 2
	tk.Statements = []Statement{
		{Language: "en", File: file(t, dir, "new/en.pdf", "new statement")},
		{Language: "es", File: file(t, dir, "new/es.pdf", "enunciado")},
	}
	tk.Attachments = []Attachment{{Filename: "sample2.txt", File: file(t, dir, "new/sample2.txt", "3 4\n")}}
	d := tk.Datasets[0]
	d.TimeLimit = Ptr(2.0)
	d.TaskTypeParameters = json.RawMessage(`["alone", ["", ""], "diff"]`)
	d.Managers = nil
	d.Testcases[1].Output = file(t, dir, "new/out1", "3\n")
	d.Testcases[3] = Testcase{Codename: "004", Input: file(t, dir, "new/in4", "4 4\n"), Output: file(t, dir, "new/out4", "8\n")}
	v2 := NewDataset("v2")
	v2.TimeLimit, v2.MemoryLimit = Ptr(1.0), Ptr(int64(64*MiB))
	v2.TaskType, v2.TaskTypeParameters = "Batch", json.RawMessage(`["alone", ["", ""], "diff"]`)
	v2.ScoreType, v2.ScoreTypeParameters = "Sum", json.RawMessage(`100`)
	v2.Testcases = []Testcase{{Codename: "000", Input: d.Testcases[0].Input, Output: d.Testcases[0].Output}}
	tk.Datasets = []*Dataset{v2, d} // the order does not choose the active dataset of an existing task

	res, err = w.WriteTask(ctx, tk, TaskOptions{Update: true})
	if err != nil {
		t.Fatalf("WriteTask: %v\n%s", err, issues(res.Report))
	}
	if res.TaskIDs["sum"] != id {
		t.Errorf("task id %d, want %d", res.TaskIDs["sum"], id)
	}
	for _, want := range []Change{
		{Updated, "task", "sum", id, "settings changed; 1 statement added, 1 statement changed, 1 statement removed; 1 attachment added, 1 attachment removed"},
		{Updated, "dataset", `sum "Default"`, defaultID, "settings changed; 1 manager removed; 1 testcase added, 1 testcase changed, 1 testcase removed"},
	} {
		if got := change(res, want.Kind, want.Name); got != want {
			t.Errorf("change %+v, want %+v", got, want)
		}
	}
	if c := change(res, "dataset", `sum "v2"`); c.Action != Created || c.Details != "1 testcase, not active" {
		t.Errorf("changes:\n%s", changes(res))
	}
	if !hasIssue(res.Report, SeverityWarning, `task sum: dataset "Default"`, fmt.Sprintf("cms ctl reevaluate --dataset %d", defaultID)) ||
		len(res.Report.Issues()) != 1 {
		t.Errorf("issues:\n%s", issues(res.Report))
	}
	if want := (judging.Event{Kind: judging.EventDataset, ContestID: contestID, TaskID: id}); !sameEvents(res.Events, want) {
		t.Errorf("events %+v, want %+v", res.Events, want)
	}

	if got := text(t, pool, `SELECT concat_ws(' ', title, score_precision, contest_id, num) FROM tasks WHERE id = $1`, id); got != fmt.Sprintf("Sum v2 2 %d 0", contestID) {
		t.Errorf("task %q", got)
	}
	if activeDataset(t, pool, id) != defaultID {
		t.Error("the active dataset changed")
	}
	wantRows(t, pool, []string{"en " + sha("new statement"), "es " + sha("enunciado")},
		`SELECT language || ' ' || digest FROM statements WHERE task_id = $1 ORDER BY language`, id)
	wantRows(t, pool, []string{"sample2.txt"}, `SELECT filename FROM attachments WHERE task_id = $1`, id)
	wantRows(t, pool, []string{"Default|f|2", "v2|f|1"},
		`SELECT concat_ws('|', description, autojudge, time_limit) FROM datasets WHERE task_id = $1 ORDER BY id`, id)
	wantRows(t, pool, nil, `SELECT filename FROM managers WHERE dataset_id = $1`, defaultID)
	// Testcases are matched by codename: a changed one keeps its id and
	// its evaluations; a removed one loses them.
	wantRows(t, pool, []string{
		fmt.Sprintf("000 %d", tcIDs["000"]), fmt.Sprintf("001 %d %s", tcIDs["001"], sha("3\n")),
		fmt.Sprintf("002 %d", tcIDs["002"]), "004",
	}, `SELECT CASE WHEN codename = '001' THEN concat_ws(' ', codename, id, output)
		WHEN codename = '004' THEN codename ELSE concat_ws(' ', codename, id) END
		FROM testcases WHERE dataset_id = $1 ORDER BY codename`, defaultID)
	wantRows(t, pool, []string{"000", "001", "002"}, `SELECT tc.codename FROM evaluations e
		JOIN testcases tc ON tc.id = e.testcase_id WHERE e.submission_id = $1 ORDER BY 1`, sub)
	// The submission and its result are kept; the task score cache follows
	// the new precision.
	if n := count(t, pool, `SELECT count(*) FROM submission_results WHERE submission_id = $1 AND dataset_id = $2`, sub, defaultID); n != 1 {
		t.Errorf("%d results", n)
	}
	if got := text(t, pool, `SELECT score::text FROM task_scores WHERE task_id = $1`, id); got != "33.33" {
		t.Errorf("cached task score %s, want 33.33", got)
	}
}

func TestWriteTaskSkipStatements(t *testing.T) {
	ctx := context.Background()
	w, _ := testWriter(t)
	res, err := w.WriteTask(ctx, batchTask(t, t.TempDir(), "sum", 2), TaskOptions{})
	if err != nil {
		t.Fatal(err)
	}
	id := res.TaskIDs["sum"]
	tk := batchTask(t, t.TempDir(), "sum", 2)
	tk.Statements, tk.PrimaryStatements = nil, nil
	tk.Title = "New title"
	res, err = w.WriteTask(ctx, tk, TaskOptions{Update: true, SkipStatements: true})
	if err != nil {
		t.Fatalf("WriteTask: %v\n%s", err, issues(res.Report))
	}
	if c := change(res, "task", "sum"); c.Action != Updated || c.Details != "settings changed" {
		t.Errorf("changes:\n%s", changes(res))
	}
	wantRows(t, w.Pool, []string{"New title {en}"}, `SELECT concat_ws(' ', title, primary_statements) FROM tasks WHERE id = $1`, id)
	wantRows(t, w.Pool, []string{"en", "it"}, `SELECT language FROM statements WHERE task_id = $1 ORDER BY 1`, id)

	// A new task without statements (-S) has no primary statements.
	tk = batchTask(t, t.TempDir(), "other", 2)
	tk.Statements = nil
	if res, err = w.WriteTask(ctx, tk, TaskOptions{SkipStatements: true}); err != nil {
		t.Fatalf("WriteTask: %v\n%s", err, issues(res.Report))
	}
	wantRows(t, w.Pool, []string{"{}"}, `SELECT primary_statements::text FROM tasks WHERE name = 'other'`)
}

func TestWriteTaskInContest(t *testing.T) {
	ctx := context.Background()
	w, _ := testWriter(t)
	pool := w.Pool
	c := contestWithGroup(t, w, "c")
	d := contestWithGroup(t, w, "d")
	for _, name := range []string{"a", "b"} {
		if res, err := w.WriteTask(ctx, batchTask(t, t.TempDir(), name, 2), TaskOptions{ContestID: c}); err != nil {
			t.Fatalf("WriteTask %s: %v\n%s", name, err, issues(res.Report))
		}
	}
	if _, err := w.WriteTask(ctx, batchTask(t, t.TempDir(), "free", 2), TaskOptions{}); err != nil {
		t.Fatal(err)
	}
	// A task of no contest is added at the end (upstream refuses).
	res, err := w.WriteTask(ctx, batchTask(t, t.TempDir(), "free", 2), TaskOptions{ContestID: c, Update: true})
	if err != nil {
		t.Fatal(err)
	}
	if ch := change(res, "task", "free"); ch.Details != "added to the contest at position 2" {
		t.Errorf("changes:\n%s", changes(res))
	}
	if len(res.Events) != 1 || res.Events[0].ContestID != c || res.Events[0].Kind != judging.EventDataset {
		t.Errorf("events %+v", res.Events)
	}
	wantRows(t, pool, []string{"a 0", "b 1", "free 2"},
		`SELECT name || ' ' || num FROM tasks WHERE contest_id = $1 ORDER BY num`, c)

	// A task of another contest cannot move; an unknown contest is an error.
	_, err = w.WriteTask(ctx, batchTask(t, t.TempDir(), "a", 2), TaskOptions{ContestID: d, Update: true})
	if err == nil || !strings.Contains(err.Error(), `task "a" is already tied to contest "c"`) {
		t.Errorf("task of another contest: %v", err)
	}
	_, err = w.WriteTask(ctx, batchTask(t, t.TempDir(), "z", 2), TaskOptions{ContestID: 999})
	if err == nil || !strings.Contains(err.Error(), "contest 999 does not exist") {
		t.Errorf("unknown contest: %v", err)
	}
	if n := count(t, pool, `SELECT count(*) FROM tasks WHERE name = 'z'`); n != 0 {
		t.Error("the task was created anyway")
	}
	// Updating a task of the contest with the contest given is fine.
	if _, err := w.WriteTask(ctx, batchTask(t, t.TempDir(), "b", 2), TaskOptions{ContestID: c, Update: true}); err != nil {
		t.Error(err)
	}
}

func TestWriteTaskInvalid(t *testing.T) {
	ctx := context.Background()
	w, qc := testWriter(t)
	tk := batchTask(t, t.TempDir(), "sum", 2)
	tk.Datasets[0].TaskType = "Nope"
	qc.Reset()
	res, err := w.WriteTask(ctx, tk, TaskOptions{})
	var ve *ValidationError
	if !errors.As(err, &ve) || !hasIssue(res.Report, SeverityError, `dataset "Default"`, `unknown task type "Nope"`) {
		t.Fatalf("err %v\n%s", err, issues(res.Report))
	}
	if qc.Count() != 0 {
		t.Errorf("%d queries for an invalid task", qc.Count())
	}
	n := 0
	if err := w.Blobs.Walk(ctx, func(string, int64) error { n++; return nil }); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d blobs stored for an invalid task", n)
	}
}

func TestImportDataset(t *testing.T) {
	ctx := context.Background()
	w, _ := testWriter(t)
	c := contestWithGroup(t, w, "c")
	res, err := w.WriteTask(ctx, batchTask(t, t.TempDir(), "sum", 2), TaskOptions{ContestID: c})
	if err != nil {
		t.Fatal(err)
	}
	id := res.TaskIDs["sum"]
	active := activeDataset(t, w.Pool, id)
	fl := newFakeLoader("fake")
	tk := batchTask(t, t.TempDir(), "sum", 4)
	tk.Statements[0].File = FileFromPath("/nonexistent/statement.pdf") // not read
	fl.tasks["/sum"] = tk

	res, err = w.ImportDataset(ctx, fl, "/sum", "bigger", nil)
	if err != nil {
		t.Fatalf("ImportDataset: %v\n%s", err, issues(res.Report))
	}
	ch := change(res, "dataset", `sum "bigger"`)
	if ch.Action != Created || ch.Details != "4 testcases, not active" || res.ContestID != c ||
		!sameEvents(res.Events, judging.Event{Kind: judging.EventDataset, ContestID: c, TaskID: id, DatasetID: ch.ID}) {
		t.Errorf("changes:\n%s\nevents %+v", changes(res), res.Events)
	}
	if activeDataset(t, w.Pool, id) != active {
		t.Error("the active dataset changed")
	}
	wantRows(t, w.Pool, []string{"Default 2", "bigger 4"}, `SELECT d.description || ' ' || count(tc.id) FROM datasets d
		LEFT JOIN testcases tc ON tc.dataset_id = d.id WHERE d.task_id = $1 GROUP BY d.id ORDER BY d.id`, id)

	if _, err := w.ImportDataset(ctx, fl, "/sum", "bigger", nil); err == nil || !strings.Contains(err.Error(), `already has a dataset "bigger"`) {
		t.Errorf("duplicate description: %v", err)
	}
	if _, err := w.ImportDataset(ctx, fl, "/sum", "", nil); err == nil {
		t.Error("empty description accepted")
	}
	fl.tasks["/other"] = batchTask(t, t.TempDir(), "other", 2)
	if _, err := w.ImportDataset(ctx, fl, "/other", "x", nil); err == nil || !strings.Contains(err.Error(), `task "other" does not exist`) {
		t.Errorf("unknown task: %v", err)
	}
}

// TestWriteTaskQueries checks that writing a task takes the same number of
// round trips whatever the number of its testcases (no N+1), both to
// create it and to update every testcase.
func TestWriteTaskQueries(t *testing.T) {
	ctx := context.Background()
	w, qc := testWriter(t)
	measure := func(name string, n int, update bool) int64 {
		t.Helper()
		tk := batchTask(t, t.TempDir(), name, n)
		if update {
			for i := range tk.Datasets[0].Testcases {
				tk.Datasets[0].Testcases[i].Output = FileFromBytes("changed", []byte(fmt.Sprintf("changed %d\n", i)))
			}
		}
		if err := Upload(ctx, w.Blobs, tk.Files(), 0); err != nil {
			t.Fatal(err)
		}
		qc.Reset()
		res, err := w.WriteTask(ctx, tk, TaskOptions{Update: update})
		if err != nil {
			t.Fatalf("WriteTask: %v\n%s", err, issues(res.Report))
		}
		return qc.Count()
	}
	small, big := measure("small", 4, false), measure("big", 200, false)
	if small != big {
		t.Errorf("creating a task: %d round trips with 4 testcases, %d with 200", small, big)
	}
	small, big = measure("small", 4, true), measure("big", 200, true)
	if small != big {
		t.Errorf("updating a task: %d round trips with 4 testcases, %d with 200", small, big)
	}
	t.Logf("%d round trips to update a task", small)
	if n := count(t, w.Pool, `SELECT count(*) FROM testcases tc JOIN datasets d ON d.id = tc.dataset_id
		JOIN tasks t ON t.id = d.task_id WHERE t.name = 'big' AND tc.output = $1`, sha("changed 7\n")); n != 1 {
		t.Errorf("%d updated testcases with the new output", n)
	}
}

func TestSameJSON(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		same bool
	}{
		{`50`, `50.0`, true},
		{`["alone", ["", ""], "diff"]`, `["alone",["",""],"diff"]`, true},
		{`[[40, 2], [60, "0[1-2]"]]`, `[[40,2],[60,"0[1-2]"]]`, true},
		{`{"a": 1, "b": 2}`, `{"b": 2, "a": 1}`, true},
		{`33.333333333333336`, `33.33333333333333`, false},
		{`["diff"]`, `["comparator"]`, false},
		{`[1, 2]`, `[2, 1]`, false},
		{`not json`, `not json`, true},
		{`not json`, `[]`, false},
	} {
		if got := sameJSON(json.RawMessage(tc.a), json.RawMessage(tc.b)); got != tc.same {
			t.Errorf("sameJSON(%s, %s) = %v", tc.a, tc.b, got)
		}
	}
}
