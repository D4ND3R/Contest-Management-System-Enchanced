// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package store_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

func TestTasks(t *testing.T) {
	f, _ := newFixture(t)
	ctx, qs := f.ctx, f.q

	c, _ := f.contest("ioi")
	b, _ := f.task(c, 1, "bbb")
	a, _ := f.task(c, 0, "aaa")
	loose := must[q.Task](t)(qs.CreateTask(ctx, taskParams(nil, nil, "loose")))
	if loose.ContestID != nil || loose.Num != nil || loose.ActiveDatasetID != nil || loose.AllowedLanguages != nil {
		t.Fatalf("CreateTask without contest = %+v", loose)
	}
	if got := must[q.Task](t)(qs.GetTask(ctx, a.ID)); got.Name != "aaa" || len(got.SubmissionFormat) != 1 || got.PrimaryStatements == nil {
		t.Fatalf("GetTask = %+v", got)
	}
	if got := must[q.Task](t)(qs.GetTaskByName(ctx, "bbb")); got.ID != b.ID {
		t.Fatalf("GetTaskByName = %+v", got)
	}
	if got := must[q.Task](t)(qs.GetTaskByContestName(ctx, q.GetTaskByContestNameParams{ContestID: c.ID, Name: "aaa"})); got.ID != a.ID {
		t.Fatalf("GetTaskByContestName = %+v", got)
	}
	_, err := qs.GetTaskByContestName(ctx, q.GetTaskByContestNameParams{ContestID: c.ID, Name: "loose"})
	wantNoRows(t, "GetTaskByContestName of a task outside the contest", err)

	byContest := must[[]q.Task](t)(qs.ListTasksByContest(ctx, c.ID))
	if len(byContest) != 2 || byContest[0].ID != a.ID || byContest[1].ID != b.ID {
		t.Fatalf("ListTasksByContest = %+v, want aaa (num 0), bbb (num 1)", byContest)
	}
	all := must[[]q.ListTasksRow](t)(qs.ListTasks(ctx))
	if len(all) != 3 || all[0].Task.Name != "aaa" || *all[0].ContestName != "ioi" || all[2].ContestName != nil {
		t.Fatalf("ListTasks = %+v", all)
	}
	if free := must[[]q.Task](t)(qs.ListTasksWithoutContest(ctx)); len(free) != 1 || free[0].ID != loose.ID {
		t.Fatalf("ListTasksWithoutContest = %+v", free)
	}

	// Swap the positions of aaa and bbb through a NULL num, as CMS.
	must[q.Task](t)(qs.SetTaskPosition(ctx, q.SetTaskPositionParams{ID: a.ID, ContestID: &c.ID}))
	must[q.Task](t)(qs.SetTaskPosition(ctx, q.SetTaskPositionParams{ID: b.ID, ContestID: &c.ID, Num: ptr(int32(0))}))
	must[q.Task](t)(qs.SetTaskPosition(ctx, q.SetTaskPositionParams{ID: a.ID, ContestID: &c.ID, Num: ptr(int32(1))}))
	if got := must[[]q.Task](t)(qs.ListTasksByContest(ctx, c.ID)); got[0].ID != b.ID {
		t.Fatalf("after swapping, ListTasksByContest = %+v", got)
	}

	a = must[q.Task](t)(qs.UpdateTask(ctx, q.UpdateTaskParams{
		ID: a.ID, Name: "aaa", Title: "A plus B", SubmissionFormat: nil, AllowedLanguages: []string{"C++20 / g++"},
		TokenMode: "finite", TokenMaxNumber: ptr(int32(2)), TokenMinInterval: time.Minute,
		TokenGenInitial: 1, TokenGenNumber: 1, TokenGenInterval: time.Hour, TokenGenMax: ptr(int32(3)),
		MaxSubmissionNumber: ptr(int32(10)), MaxUserTestNumber: ptr(int32(20)),
		MinSubmissionInterval: ptr(time.Minute), MinUserTestInterval: ptr(time.Second),
		FeedbackLevel: "full", ScorePrecision: 2, ScoreMode: "max_subtask",
	}))
	if a.Title != "A plus B" || a.SubmissionFormat == nil || len(a.SubmissionFormat) != 0 ||
		len(a.AllowedLanguages) != 1 || *a.MinUserTestInterval != time.Second || a.ScoreMode != "max_subtask" {
		t.Fatalf("UpdateTask = %+v", a)
	}

	n, err := qs.DeleteTask(ctx, loose.ID)
	wantRows(t, "DeleteTask", n, err, 1)
	_, err = qs.GetTask(ctx, loose.ID)
	wantNoRows(t, "GetTask after delete", err)
}

func TestStatementsAndAttachments(t *testing.T) {
	f, _ := newFixture(t)
	ctx, qs := f.ctx, f.q

	c, _ := f.contest("ioi")
	a, _ := f.task(c, 0, "aaa")
	b, _ := f.task(c, 1, "bbb")
	en := must[q.Statement](t)(qs.CreateStatement(ctx, q.CreateStatementParams{TaskID: a.ID, Language: "en", Digest: digest("en")}))
	es := must[q.Statement](t)(qs.CreateStatement(ctx, q.CreateStatementParams{TaskID: a.ID, Language: "es", Digest: digest("es")}))
	must[q.Statement](t)(qs.CreateStatement(ctx, q.CreateStatementParams{TaskID: b.ID, Language: "it", Digest: digest("it")}))

	if got := must[q.Statement](t)(qs.GetStatement(ctx, en.ID)); got != en {
		t.Fatalf("GetStatement = %+v", got)
	}
	if got := must[q.Statement](t)(qs.GetStatementByLanguage(ctx, q.GetStatementByLanguageParams{TaskID: a.ID, Language: "es"})); got != es {
		t.Fatalf("GetStatementByLanguage = %+v", got)
	}
	if got := must[[]q.Statement](t)(qs.ListStatementsByTask(ctx, a.ID)); len(got) != 2 || got[0].Language != "en" {
		t.Fatalf("ListStatementsByTask = %+v", got)
	}
	got := must[[]q.Statement](t)(qs.ListStatementsByContest(ctx, c.ID))
	if len(got) != 3 || got[0].TaskID != a.ID || got[2].TaskID != b.ID {
		t.Fatalf("ListStatementsByContest = %+v", got)
	}
	es = must[q.Statement](t)(qs.UpdateStatement(ctx, q.UpdateStatementParams{ID: es.ID, Language: "es_MX", Digest: "x"}))
	if es.Language != "es_MX" || es.Digest != "x" {
		t.Fatalf("UpdateStatement = %+v", es)
	}
	n, err := qs.DeleteStatement(ctx, es.ID)
	wantRows(t, "DeleteStatement", n, err, 1)

	att := must[q.Attachment](t)(qs.CreateAttachment(ctx, q.CreateAttachmentParams{TaskID: a.ID, Filename: "grader.cpp", Digest: digest("g")}))
	must[q.Attachment](t)(qs.CreateAttachment(ctx, q.CreateAttachmentParams{TaskID: a.ID, Filename: "aaa.h", Digest: digest("h")}))
	if got := must[q.Attachment](t)(qs.GetAttachment(ctx, att.ID)); got != att {
		t.Fatalf("GetAttachment = %+v", got)
	}
	if got := must[q.Attachment](t)(qs.GetAttachmentByFilename(ctx, q.GetAttachmentByFilenameParams{TaskID: a.ID, Filename: "grader.cpp"})); got != att {
		t.Fatalf("GetAttachmentByFilename = %+v", got)
	}
	if got := must[[]q.Attachment](t)(qs.ListAttachmentsByTask(ctx, a.ID)); len(got) != 2 || got[0].Filename != "aaa.h" {
		t.Fatalf("ListAttachmentsByTask = %+v", got)
	}
	att = must[q.Attachment](t)(qs.UpdateAttachment(ctx, q.UpdateAttachmentParams{ID: att.ID, Filename: "grader.c", Digest: digest("g2")}))
	if att.Filename != "grader.c" {
		t.Fatalf("UpdateAttachment = %+v", att)
	}
	n, err = qs.DeleteAttachment(ctx, att.ID)
	wantRows(t, "DeleteAttachment", n, err, 1)
}

func TestDatasetsManagersTestcases(t *testing.T) {
	f, _ := newFixture(t)
	ctx, qs := f.ctx, f.q

	c, _ := f.contest("ioi")
	a, active := f.task(c, 0, "aaa")
	_, bActive := f.task(c, 1, "bbb")
	auto := must[q.Dataset](t)(qs.CreateDataset(ctx, datasetParams(a.ID, "faster")))
	spare := must[q.Dataset](t)(qs.CreateDataset(ctx, datasetParams(a.ID, "spare")))

	if got := must[q.Dataset](t)(qs.GetDataset(ctx, auto.ID)); got.Description != "faster" || *got.MemoryLimit != 256<<20 {
		t.Fatalf("GetDataset = %+v", got)
	}
	if got := must[q.Dataset](t)(qs.GetActiveDataset(ctx, a.ID)); got.ID != active.ID {
		t.Fatalf("GetActiveDataset = %+v", got)
	}
	if got := must[[]q.Dataset](t)(qs.ListDatasetsByTask(ctx, a.ID)); len(got) != 3 || got[0].ID != active.ID {
		t.Fatalf("ListDatasetsByTask = %+v", got)
	}
	if got := must[[]q.Dataset](t)(qs.ListActiveDatasetsByContest(ctx, c.ID)); len(got) != 2 ||
		got[0].ID != active.ID || got[1].ID != bActive.ID {
		t.Fatalf("ListActiveDatasetsByContest = %+v", got)
	}
	auto = must[q.Dataset](t)(qs.UpdateDataset(ctx, q.UpdateDatasetParams{
		ID: auto.ID, Description: "faster", Autojudge: true, TimeLimit: ptr(0.5), MemoryLimit: ptr(int64(512 << 20)),
		TaskType: "Batch", TaskTypeParameters: json.RawMessage(`["grader", ["", ""], "comparator"]`),
		ScoreType: "GroupMin", ScoreTypeParameters: json.RawMessage(`[[40, 2], [60, 3]]`),
	}))
	if !auto.Autojudge || *auto.TimeLimit != 0.5 || auto.ScoreType != "GroupMin" {
		t.Fatalf("UpdateDataset = %+v", auto)
	}
	judged := must[[]q.Dataset](t)(qs.ListJudgedDatasetsByTask(ctx, a.ID))
	if len(judged) != 2 || judged[0].ID != active.ID || judged[1].ID != auto.ID {
		t.Fatalf("ListJudgedDatasetsByTask = %+v, want the active and the autojudge datasets", judged)
	}

	// Managers.
	m := must[q.Manager](t)(qs.CreateManager(ctx, q.CreateManagerParams{DatasetID: active.ID, Filename: "checker", Digest: digest("c")}))
	must[q.Manager](t)(qs.CreateManager(ctx, q.CreateManagerParams{DatasetID: active.ID, Filename: "grader.cpp", Digest: digest("g")}))
	if got := must[q.Manager](t)(qs.GetManager(ctx, m.ID)); got != m {
		t.Fatalf("GetManager = %+v", got)
	}
	m = must[q.Manager](t)(qs.UpdateManager(ctx, q.UpdateManagerParams{ID: m.ID, Filename: "checker", Digest: digest("c2")}))
	if got := must[[]q.Manager](t)(qs.ListManagersByDataset(ctx, active.ID)); len(got) != 2 || got[0] != m {
		t.Fatalf("ListManagersByDataset = %+v", got)
	}

	// Testcases, one by one and in bulk.
	t1 := f.testcase(active, "002", false)
	must[q.Testcase](t)(qs.CreateTestcase(ctx, q.CreateTestcaseParams{
		DatasetID: active.ID, Codename: "001", Input: digest("i1"), Output: digest("o1"),
	}))
	bulk := []q.CreateTestcasesParams{
		{DatasetID: active.ID, Codename: "004", Public: true, Input: digest("i4"), Output: digest("o4")},
		{DatasetID: active.ID, Codename: "003", Input: digest("i3"), Output: digest("o3")},
	}
	if n := must[int64](t)(qs.CreateTestcases(ctx, bulk)); n != 2 {
		t.Fatalf("CreateTestcases = %d, want 2", n)
	}
	if got := must[q.Testcase](t)(qs.GetTestcase(ctx, t1.ID)); got != t1 {
		t.Fatalf("GetTestcase = %+v", got)
	}
	t3 := must[q.Testcase](t)(qs.GetTestcaseByCodename(ctx, q.GetTestcaseByCodenameParams{DatasetID: active.ID, Codename: "003"}))
	tcs := must[[]q.Testcase](t)(qs.ListTestcasesByDataset(ctx, active.ID))
	if len(tcs) != 4 || tcs[0].Codename != "001" || tcs[3].Codename != "004" || !tcs[3].Public {
		t.Fatalf("ListTestcasesByDataset = %+v", tcs)
	}
	t1 = must[q.Testcase](t)(qs.UpdateTestcase(ctx, q.UpdateTestcaseParams{
		ID: t1.ID, Codename: "002", Public: false, Input: digest("i2b"), Output: digest("o2b"),
	}))
	if t1.Input != digest("i2b") {
		t.Fatalf("UpdateTestcase = %+v", t1)
	}
	n, err := qs.SetTestcasesPublic(ctx, q.SetTestcasesPublicParams{DatasetID: active.ID, PublicIds: []int32{t1.ID, t3.ID}})
	wantRows(t, "SetTestcasesPublic", n, err, 4)
	for _, tc := range must[[]q.Testcase](t)(qs.ListTestcasesByDataset(ctx, active.ID)) {
		if want := tc.ID == t1.ID || tc.ID == t3.ID; tc.Public != want {
			t.Fatalf("testcase %s public = %v, want %v", tc.Codename, tc.Public, want)
		}
	}
	// No box ticked: the handler passes a nil slice (NULL), which must
	// make every testcase private, not violate public NOT NULL.
	n, err = qs.SetTestcasesPublic(ctx, q.SetTestcasesPublicParams{DatasetID: active.ID})
	wantRows(t, "SetTestcasesPublic with no ids", n, err, 4)
	for _, tc := range must[[]q.Testcase](t)(qs.ListTestcasesByDataset(ctx, active.ID)) {
		if tc.Public {
			t.Fatalf("testcase %s is still public after SetTestcasesPublic with no ids", tc.Codename)
		}
	}

	// Cloning a dataset.
	n, err = qs.CopyDatasetManagers(ctx, q.CopyDatasetManagersParams{FromDatasetID: active.ID, ToDatasetID: spare.ID})
	wantRows(t, "CopyDatasetManagers", n, err, 2)
	n, err = qs.CopyDatasetTestcases(ctx, q.CopyDatasetTestcasesParams{FromDatasetID: active.ID, ToDatasetID: spare.ID})
	wantRows(t, "CopyDatasetTestcases", n, err, 4)
	if got := must[[]q.Testcase](t)(qs.ListTestcasesByDataset(ctx, spare.ID)); len(got) != 4 || got[1].Input != digest("i2b") {
		t.Fatalf("copied testcases = %+v", got)
	}

	n, err = qs.DeleteTestcase(ctx, t1.ID)
	wantRows(t, "DeleteTestcase", n, err, 1)
	n, err = qs.DeleteManager(ctx, m.ID)
	wantRows(t, "DeleteManager", n, err, 1)

	// The active dataset cannot be deleted (fk_active_dataset_id); once
	// another one is live, it goes with its managers and testcases.
	_, err = qs.DeleteDataset(ctx, active.ID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" || pgErr.ConstraintName != "fk_active_dataset_id" {
		t.Fatalf("DeleteDataset of the active dataset: got error %v, want a fk_active_dataset_id violation", err)
	}
	must[q.Task](t)(qs.SetTaskActiveDataset(ctx, q.SetTaskActiveDatasetParams{ID: a.ID, ActiveDatasetID: &spare.ID}))
	n, err = qs.DeleteDataset(ctx, active.ID)
	wantRows(t, "DeleteDataset", n, err, 1)
	if got := must[q.Dataset](t)(qs.GetActiveDataset(ctx, a.ID)); got.ID != spare.ID {
		t.Fatalf("GetActiveDataset after deleting the old one = %+v, want %d", got, spare.ID)
	}
	if got := must[[]q.Testcase](t)(qs.ListTestcasesByDataset(ctx, active.ID)); len(got) != 0 {
		t.Fatalf("testcases of the deleted dataset = %+v", got)
	}
	// A task can be left without an active dataset.
	must[q.Task](t)(qs.SetTaskActiveDataset(ctx, q.SetTaskActiveDatasetParams{ID: a.ID}))
	_, err = qs.GetActiveDataset(ctx, a.ID)
	wantNoRows(t, "GetActiveDataset without one", err)
	// A dataset of another task cannot become active (fk_active_dataset_id).
	if _, err := qs.SetTaskActiveDataset(ctx, q.SetTaskActiveDatasetParams{ID: a.ID, ActiveDatasetID: &bActive.ID}); err == nil {
		t.Fatal("SetTaskActiveDataset accepted a dataset of another task")
	}
}
