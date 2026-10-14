// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// The update rules are those of cmscontrib/importing.py and
// ImportTask.py of CMS:
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2014-2018 William Di Luigi <williamdiluigi@gmail.com>
// Copyright © 2016 Luca Wehrstedt <luca.wehrstedt@gmail.com>

package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// TaskOptions are the options of a task import (cmsImportTask).
type TaskOptions struct {
	// ContestID puts a new task at the end of this contest (-c). An
	// existing task must be in it already, or in no contest (then it is
	// added at the end; upstream refuses).
	ContestID int32
	// Update updates the task if it exists (-U), keeping its
	// submissions; without it an existing task is an error.
	Update bool
	// SkipStatements leaves the statements and the primary statements
	// alone (-S): the loader did not read them.
	SkipStatements bool
	// Report, if set, receives the issues (it may hold those of the
	// loader already); otherwise the result has a new one.
	Report *Report
}

// WriteTask stores a task (cmsImportTask): a new task is created (in
// opts.ContestID if set); an existing one is an error unless opts.Update,
// which updates it as upstream update_task does:
//
//   - every column but the contest, the position and the active dataset
//     takes the new value (the defaults for what the format does not
//     have);
//   - statements and attachments are replaced by language and file name
//     (new ones added, changed ones updated, missing ones removed);
//     statements are left alone with SkipStatements;
//   - a dataset with the description of an existing one updates it in
//     place: its columns, and its managers and testcases by name (a
//     changed testcase keeps its id, and its evaluations); a dataset with
//     a new description is added, not active; datasets that are not in
//     the package are kept.
//
// The submissions and their results are kept. Results on a dataset whose
// judging data changed in place are not invalidated (as upstream): the
// report warns, and `cms ctl reevaluate --dataset` judges them again.
func (w *Writer) WriteTask(ctx context.Context, t *Task, opts TaskOptions) (*Result, error) {
	res := newResult(opts.Report)
	v := newValidator(w.langs(), res.Report)
	v.skipStatements = opts.SkipStatements
	v.task(t)
	if err := res.Report.Err(); err != nil {
		return res, err
	}
	if err := w.upload(ctx, []*Task{t}, opts.SkipStatements); err != nil {
		return res, err
	}
	var out *Result
	err := w.inTx(ctx, func(tx pgx.Tx, qq *q.Queries) error {
		x := newTxWriter(ctx, tx, qq, opts.SkipStatements)
		var contestID *int32
		if opts.ContestID != 0 {
			c, err := qq.ImportLockContestByID(ctx, opts.ContestID)
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("contest %d does not exist", opts.ContestID)
			}
			if err != nil {
				return err
			}
			contestID = &c.ID
			x.res.ContestID = c.ID
		}
		rows, err := qq.ImportTasksByName(ctx, []string{t.Name})
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			var num *int32
			if contestID != nil {
				n, err := qq.ImportNextTaskNum(ctx, *contestID)
				if err != nil {
					return err
				}
				num = &n
			}
			row, err := x.createTask(t, contestID, num)
			if err != nil {
				return err
			}
			if contestID != nil {
				x.res.Events = append(x.res.Events, judging.Event{Kind: judging.EventDataset, ContestID: *contestID, TaskID: row.ID})
			}
		} else {
			old := rows[0].Task
			if !opts.Update {
				return fmt.Errorf("task %q %w: use --update to update it", t.Name, ErrExists)
			}
			if contestID != nil && old.ContestID != nil && *old.ContestID != *contestID {
				return fmt.Errorf("task %q is already tied to contest %q", t.Name, deref(rows[0].ContestName))
			}
			if err := x.updateTask(old, t); err != nil {
				return err
			}
			if contestID != nil && old.ContestID == nil {
				n, err := qq.ImportNextTaskNum(ctx, *contestID)
				if err != nil {
					return err
				}
				if _, err := qq.SetTaskPosition(ctx, q.SetTaskPositionParams{ID: old.ID, ContestID: contestID, Num: &n}); err != nil {
					return err
				}
				x.res.amend("task", t.Name, fmt.Sprintf("added to the contest at position %d", n))
				old.ContestID = contestID
			}
			if old.ContestID != nil {
				x.res.Events = append(x.res.Events, judging.Event{Kind: judging.EventDataset, ContestID: *old.ContestID, TaskID: old.ID})
			}
		}
		out = x.res
		return nil
	})
	if err != nil {
		return res, err
	}
	w.done(res, out)
	return res, nil
}

// amend adds details to the last change of an object, which becomes an
// update.
func (r *Result) amend(kind, name, details string) {
	for i := len(r.Changes) - 1; i >= 0; i-- {
		c := &r.Changes[i]
		if c.Kind != kind || c.Name != name {
			continue
		}
		if c.Action == Unchanged {
			c.Action, c.Details = Updated, ""
		}
		c.Details = joinDetails(c.Details, details)
		return
	}
}

// merge takes what a transaction attempt did.
func (r *Result) merge(o *Result) {
	r.Report.Merge(o.Report)
	r.Changes = append(r.Changes, o.Changes...)
	if o.ContestID != 0 {
		r.ContestID = o.ContestID
	}
	for k, v := range o.TaskIDs {
		r.TaskIDs[k] = v
	}
	r.Events = append(r.Events, o.Events...)
}

// txWriter writes inside one transaction attempt.
type txWriter struct {
	ctx            context.Context
	tx             pgx.Tx
	qq             *q.Queries
	res            *Result
	skipStatements bool
}

func newTxWriter(ctx context.Context, tx pgx.Tx, qq *q.Queries, skipStatements bool) *txWriter {
	return &txWriter{ctx: ctx, tx: tx, qq: qq, res: newResult(nil), skipStatements: skipStatements}
}

// createTask inserts a task with its statements, attachments and datasets
// and makes its active dataset live.
func (x *txWriter) createTask(t *Task, contestID, num *int32) (q.Task, error) {
	p := taskParams(t, 0)
	if x.skipStatements {
		p.PrimaryStatements = []string{}
	}
	row, err := x.qq.CreateTask(x.ctx, q.CreateTaskParams{
		ContestID: contestID, Num: num, Name: p.Name, Title: p.Title,
		SubmissionFormat: p.SubmissionFormat, PrimaryStatements: p.PrimaryStatements,
		AllowedLanguages: p.AllowedLanguages, TokenMode: p.TokenMode, TokenMaxNumber: p.TokenMaxNumber,
		TokenMinInterval: p.TokenMinInterval, TokenGenInitial: p.TokenGenInitial, TokenGenNumber: p.TokenGenNumber,
		TokenGenInterval: p.TokenGenInterval, TokenGenMax: p.TokenGenMax,
		MaxSubmissionNumber: p.MaxSubmissionNumber, MaxUserTestNumber: p.MaxUserTestNumber,
		MinSubmissionInterval: p.MinSubmissionInterval, MinUserTestInterval: p.MinUserTestInterval,
		FeedbackLevel: p.FeedbackLevel, ScorePrecision: p.ScorePrecision, ScoreMode: p.ScoreMode,
	})
	if err != nil {
		return row, fmt.Errorf("task %s: %w", t.Name, err)
	}
	var statements, attachments delta
	if !x.skipStatements {
		if statements, err = x.syncStatements(row.ID, t.Statements, nil); err != nil {
			return row, err
		}
	}
	if attachments, err = x.syncAttachments(row.ID, t.Attachments, nil); err != nil {
		return row, err
	}
	active := t.Active()
	var activeID int32
	testcases := 0
	for _, d := range t.Datasets {
		ds, err := x.createDataset(t, row.ID, d)
		if err != nil {
			return row, err
		}
		if d == active {
			activeID = ds.ID
		}
		testcases += len(d.Testcases)
	}
	if row, err = x.qq.SetTaskActiveDataset(x.ctx, q.SetTaskActiveDatasetParams{ID: row.ID, ActiveDatasetID: &activeID}); err != nil {
		return row, fmt.Errorf("task %s: %w", t.Name, err)
	}
	x.res.TaskIDs[t.Name] = row.ID
	x.res.add(Created, "task", t.Name, row.ID, joinDetails(
		nonZero(statements.added, "statement"), nonZero(attachments.added, "attachment"),
		nonZero(len(t.Datasets), "dataset"), nonZero(testcases, "testcase")))
	return row, nil
}

// createDataset inserts a dataset with its managers and testcases.
func (x *txWriter) createDataset(t *Task, taskID int32, d *Dataset) (q.Dataset, error) {
	p := datasetParams(d, 0)
	ds, err := x.qq.CreateDataset(x.ctx, q.CreateDatasetParams{
		TaskID: taskID, Description: p.Description, Autojudge: p.Autojudge,
		TimeLimit: p.TimeLimit, MemoryLimit: p.MemoryLimit,
		TaskType: p.TaskType, TaskTypeParameters: p.TaskTypeParameters,
		ScoreType: p.ScoreType, ScoreTypeParameters: p.ScoreTypeParameters,
	})
	if err != nil {
		return ds, fmt.Errorf("%s: %w", datasetWhere(t.Name, d.Description), err)
	}
	if _, err := x.syncManagers(t, ds.ID, d, nil); err != nil {
		return ds, err
	}
	if _, err := x.syncTestcases(t, ds.ID, d, nil); err != nil {
		return ds, err
	}
	return ds, nil
}

// updateTask makes an existing task look like t (upstream update_task).
func (x *txWriter) updateTask(old q.Task, t *Task) error {
	ctx, qq := x.ctx, x.qq
	x.res.TaskIDs[t.Name] = old.ID
	cur := currentTaskParams(old)
	want := taskParams(t, old.ID)
	if x.skipStatements {
		want.PrimaryStatements = cur.PrimaryStatements
	}
	var details []string
	recompute := false
	if !reflect.DeepEqual(cur, want) {
		if _, err := qq.UpdateTask(ctx, want); err != nil {
			return fmt.Errorf("task %s: %w", t.Name, err)
		}
		details = append(details, "settings changed")
		recompute = cur.ScoreMode != want.ScoreMode || cur.ScorePrecision != want.ScorePrecision
	}
	if !x.skipStatements {
		existing, err := qq.ListStatementsByTask(ctx, old.ID)
		if err != nil {
			return err
		}
		d, err := x.syncStatements(old.ID, t.Statements, existing)
		if err != nil {
			return err
		}
		details = append(details, d.describe("statement"))
	}
	existingAtt, err := qq.ListAttachmentsByTask(ctx, old.ID)
	if err != nil {
		return err
	}
	att, err := x.syncAttachments(old.ID, t.Attachments, existingAtt)
	if err != nil {
		return err
	}
	details = append(details, att.describe("attachment"))

	datasets, err := qq.ListDatasetsByTask(ctx, old.ID)
	if err != nil {
		return err
	}
	byDescription := make(map[string]q.Dataset, len(datasets))
	for _, d := range datasets {
		byDescription[d.Description] = d
	}
	active := t.Active()
	var activeID int32
	var inPlace []int32
	for _, d := range t.Datasets {
		ex, ok := byDescription[d.Description]
		if !ok {
			ds, err := x.createDataset(t, old.ID, d)
			if err != nil {
				return err
			}
			x.res.add(Created, "dataset", fmt.Sprintf("%s %q", t.Name, d.Description), ds.ID,
				plural(len(d.Testcases), "testcase")+", not active")
			if d == active {
				activeID = ds.ID
			}
			continue
		}
		if d == active {
			activeID = ex.ID
		}
		changed, err := x.updateDataset(t, ex, d)
		if err != nil {
			return err
		}
		if changed {
			inPlace = append(inPlace, ex.ID)
		}
	}
	if old.ActiveDatasetID == nil && activeID != 0 {
		// Never happens with tasks written by CMS or this importer.
		if _, err := qq.SetTaskActiveDataset(ctx, q.SetTaskActiveDatasetParams{ID: old.ID, ActiveDatasetID: &activeID}); err != nil {
			return err
		}
		details = append(details, "active dataset set")
		recompute = true
	}
	if recompute {
		if _, err := judging.RecomputeTask(ctx, x.tx, old.ID); err != nil {
			return fmt.Errorf("task %s: recomputing the task scores: %w", t.Name, err)
		}
	}
	if len(inPlace) > 0 {
		withResults, err := qq.ImportDatasetsWithResults(ctx, inPlace)
		if err != nil {
			return err
		}
		for _, id := range withResults {
			for _, d := range datasets {
				if d.ID == id {
					x.res.Report.Warnf(datasetWhere(t.Name, d.Description),
						"the judging data changed but the results of the submissions were kept: "+
							"judge them again with `cms ctl reevaluate --dataset %d`", id)
				}
			}
		}
	}
	action := Updated
	detail := joinDetails(details...)
	if detail == "" {
		action = Unchanged
	}
	x.res.add(action, "task", t.Name, old.ID, detail)
	return nil
}

// updateDataset makes an existing dataset look like d (upstream
// update_dataset) and reports whether anything changed.
func (x *txWriter) updateDataset(t *Task, ex q.Dataset, d *Dataset) (bool, error) {
	ctx, qq := x.ctx, x.qq
	var details []string
	want := datasetParams(d, ex.ID)
	cur := q.UpdateDatasetParams{
		ID: ex.ID, Description: ex.Description, Autojudge: ex.Autojudge, TimeLimit: ex.TimeLimit,
		MemoryLimit: ex.MemoryLimit, TaskType: ex.TaskType, TaskTypeParameters: ex.TaskTypeParameters,
		ScoreType: ex.ScoreType, ScoreTypeParameters: ex.ScoreTypeParameters,
	}
	if !sameDataset(cur, want) {
		if _, err := qq.UpdateDataset(ctx, want); err != nil {
			return false, fmt.Errorf("%s: %w", datasetWhere(t.Name, d.Description), err)
		}
		details = append(details, "settings changed")
	}
	managers, err := qq.ListManagersByDataset(ctx, ex.ID)
	if err != nil {
		return false, err
	}
	md, err := x.syncManagers(t, ex.ID, d, managers)
	if err != nil {
		return false, err
	}
	details = append(details, md.describe("manager"))
	testcases, err := qq.ListTestcasesByDataset(ctx, ex.ID)
	if err != nil {
		return false, err
	}
	td, err := x.syncTestcases(t, ex.ID, d, testcases)
	if err != nil {
		return false, err
	}
	details = append(details, td.describe("testcase"))
	detail := joinDetails(details...)
	if detail == "" {
		return false, nil
	}
	x.res.add(Updated, "dataset", fmt.Sprintf("%s %q", t.Name, d.Description), ex.ID, detail)
	return true, nil
}

// syncStatements makes the statements of a task (existing, nil for a
// new task) those of want, keyed by language.
func (x *txWriter) syncStatements(taskID int32, want []Statement, existing []q.Statement) (delta, error) {
	rows := make([]keyedRow, len(existing))
	for i, s := range existing {
		rows[i] = keyedRow{id: s.ID, key: s.Language, digest: s.Digest}
	}
	files := make([]keyedFile, len(want))
	for i, s := range want {
		files[i] = keyedFile{key: s.Language, file: s.File}
	}
	del, keys, digests, d, err := diffKeyed(rows, files)
	if err != nil {
		return d, err
	}
	if len(del) > 0 {
		if _, err := x.qq.ImportDeleteStatements(x.ctx, del); err != nil {
			return d, err
		}
	}
	if len(keys) > 0 {
		if _, err := x.qq.ImportUpsertStatements(x.ctx, q.ImportUpsertStatementsParams{TaskID: taskID, Languages: keys, Digests: digests}); err != nil {
			return d, err
		}
	}
	return d, nil
}

func (x *txWriter) syncAttachments(taskID int32, want []Attachment, existing []q.Attachment) (delta, error) {
	rows := make([]keyedRow, len(existing))
	for i, a := range existing {
		rows[i] = keyedRow{id: a.ID, key: a.Filename, digest: a.Digest}
	}
	files := make([]keyedFile, len(want))
	for i, a := range want {
		files[i] = keyedFile{key: a.Filename, file: a.File}
	}
	del, keys, digests, d, err := diffKeyed(rows, files)
	if err != nil {
		return d, err
	}
	if len(del) > 0 {
		if _, err := x.qq.ImportDeleteAttachments(x.ctx, del); err != nil {
			return d, err
		}
	}
	if len(keys) > 0 {
		if _, err := x.qq.ImportUpsertAttachments(x.ctx, q.ImportUpsertAttachmentsParams{TaskID: taskID, Filenames: keys, Digests: digests}); err != nil {
			return d, err
		}
	}
	return d, nil
}

func (x *txWriter) syncManagers(t *Task, datasetID int32, ds *Dataset, existing []q.Manager) (delta, error) {
	rows := make([]keyedRow, len(existing))
	for i, m := range existing {
		rows[i] = keyedRow{id: m.ID, key: m.Filename, digest: m.Digest}
	}
	files := make([]keyedFile, len(ds.Managers))
	for i, m := range ds.Managers {
		files[i] = keyedFile{key: m.Filename, file: m.File}
	}
	del, keys, digests, d, err := diffKeyed(rows, files)
	if err != nil {
		return d, fmt.Errorf("%s: %w", datasetWhere(t.Name, ds.Description), err)
	}
	if len(del) > 0 {
		if _, err := x.qq.ImportDeleteManagers(x.ctx, del); err != nil {
			return d, err
		}
	}
	if len(keys) > 0 {
		if _, err := x.qq.ImportUpsertManagers(x.ctx, q.ImportUpsertManagersParams{DatasetID: datasetID, Filenames: keys, Digests: digests}); err != nil {
			return d, err
		}
	}
	return d, nil
}

// syncTestcases makes the testcases of a dataset those of ds, keyed by
// codename, with at most two statements however many there are.
func (x *txWriter) syncTestcases(t *Task, datasetID int32, ds *Dataset, existing []q.Testcase) (delta, error) {
	var d delta
	byCodename := make(map[string]q.Testcase, len(existing))
	for _, tc := range existing {
		byCodename[tc.Codename] = tc
	}
	wanted := make(map[string]bool, len(ds.Testcases))
	p := q.ImportUpsertTestcasesParams{DatasetID: datasetID}
	for _, tc := range ds.Testcases {
		wanted[tc.Codename] = true
		in, err := digestOf(tc.Input)
		if err != nil {
			return d, fmt.Errorf("%s: testcase %s: %w", datasetWhere(t.Name, ds.Description), tc.Codename, err)
		}
		out, err := digestOf(tc.Output)
		if err != nil {
			return d, fmt.Errorf("%s: testcase %s: %w", datasetWhere(t.Name, ds.Description), tc.Codename, err)
		}
		if ex, ok := byCodename[tc.Codename]; ok {
			if ex.Public == tc.Public && ex.Input == in && ex.Output == out {
				continue
			}
			d.changed++
		} else {
			d.added++
		}
		p.Codenames = append(p.Codenames, tc.Codename)
		p.Publics = append(p.Publics, tc.Public)
		p.Inputs = append(p.Inputs, in)
		p.Outputs = append(p.Outputs, out)
	}
	var del []int32
	for _, tc := range existing {
		if !wanted[tc.Codename] {
			del = append(del, tc.ID)
		}
	}
	d.removed = len(del)
	if len(del) > 0 {
		if _, err := x.qq.ImportDeleteTestcases(x.ctx, del); err != nil {
			return d, err
		}
	}
	if len(p.Codenames) > 0 {
		if _, err := x.qq.ImportUpsertTestcases(x.ctx, p); err != nil {
			return d, fmt.Errorf("%s: %w", datasetWhere(t.Name, ds.Description), err)
		}
	}
	return d, nil
}

// keyedRow is an existing row of a table of files keyed by name.
type keyedRow struct {
	id          int32
	key, digest string
}

// keyedFile is a wanted row.
type keyedFile struct {
	key  string
	file *File
}

// diffKeyed compares rows with what is wanted: the ids to delete, and the
// keys and digests to insert or update.
func diffKeyed(rows []keyedRow, want []keyedFile) (del []int32, keys, digests []string, d delta, err error) {
	byKey := make(map[string]keyedRow, len(rows))
	for _, r := range rows {
		byKey[r.key] = r
	}
	wanted := make(map[string]bool, len(want))
	for _, f := range want {
		wanted[f.key] = true
		dg, err := digestOf(f.file)
		if err != nil {
			return nil, nil, nil, d, fmt.Errorf("%s: %w", f.key, err)
		}
		if r, ok := byKey[f.key]; ok {
			if r.digest == dg {
				continue
			}
			d.changed++
		} else {
			d.added++
		}
		keys = append(keys, f.key)
		digests = append(digests, dg)
	}
	for _, r := range rows {
		if !wanted[r.key] {
			del = append(del, r.id)
		}
	}
	d.removed = len(del)
	return del, keys, digests, d, nil
}

// errNotStored: a file reached the transaction without being uploaded
// (a bug of the caller of the txWriter).
var errNotStored = errors.New("the file was not stored before writing")

func digestOf(f *File) (string, error) {
	if f == nil || f.digest == "" {
		return "", errNotStored
	}
	return f.digest, nil
}

// taskParams returns the columns of t as an update of task id.
func taskParams(t *Task, id int32) q.UpdateTaskParams {
	return q.UpdateTaskParams{
		ID:                    id,
		Name:                  t.Name,
		Title:                 t.Title,
		SubmissionFormat:      nonNil(t.SubmissionFormat),
		PrimaryStatements:     nonNil(t.PrimaryStatements),
		AllowedLanguages:      t.AllowedLanguages,
		TokenMode:             t.Tokens.Mode,
		TokenMaxNumber:        t.Tokens.MaxNumber,
		TokenMinInterval:      usec(t.Tokens.MinInterval),
		TokenGenInitial:       t.Tokens.GenInitial,
		TokenGenNumber:        t.Tokens.GenNumber,
		TokenGenInterval:      usec(t.Tokens.GenInterval),
		TokenGenMax:           t.Tokens.GenMax,
		MaxSubmissionNumber:   t.MaxSubmissionNumber,
		MaxUserTestNumber:     t.MaxUserTestNumber,
		MinSubmissionInterval: usecPtr(t.MinSubmissionInterval),
		MinUserTestInterval:   usecPtr(t.MinUserTestInterval),
		FeedbackLevel:         t.FeedbackLevel,
		ScorePrecision:        t.ScorePrecision,
		ScoreMode:             t.ScoreMode,
	}
}

// currentTaskParams returns the columns of a stored task as an update.
func currentTaskParams(t q.Task) q.UpdateTaskParams {
	return q.UpdateTaskParams{
		ID:                    t.ID,
		Name:                  t.Name,
		Title:                 t.Title,
		SubmissionFormat:      nonNil(t.SubmissionFormat),
		PrimaryStatements:     nonNil(t.PrimaryStatements),
		AllowedLanguages:      t.AllowedLanguages,
		TokenMode:             t.TokenMode,
		TokenMaxNumber:        t.TokenMaxNumber,
		TokenMinInterval:      t.TokenMinInterval,
		TokenGenInitial:       t.TokenGenInitial,
		TokenGenNumber:        t.TokenGenNumber,
		TokenGenInterval:      t.TokenGenInterval,
		TokenGenMax:           t.TokenGenMax,
		MaxSubmissionNumber:   t.MaxSubmissionNumber,
		MaxUserTestNumber:     t.MaxUserTestNumber,
		MinSubmissionInterval: t.MinSubmissionInterval,
		MinUserTestInterval:   t.MinUserTestInterval,
		FeedbackLevel:         t.FeedbackLevel,
		ScorePrecision:        t.ScorePrecision,
		ScoreMode:             t.ScoreMode,
	}
}

// datasetParams returns the columns of d as an update of dataset id.
func datasetParams(d *Dataset, id int32) q.UpdateDatasetParams {
	return q.UpdateDatasetParams{
		ID: id, Description: d.Description, Autojudge: d.Autojudge,
		TimeLimit: d.TimeLimit, MemoryLimit: d.MemoryLimit,
		TaskType: d.TaskType, TaskTypeParameters: d.TaskTypeParameters,
		ScoreType: d.ScoreType, ScoreTypeParameters: d.ScoreTypeParameters,
	}
}

// sameDataset compares the columns of two datasets, the JSON parameters
// by value (jsonb does not keep the spacing).
func sameDataset(a, b q.UpdateDatasetParams) bool {
	return a.Description == b.Description && a.Autojudge == b.Autojudge &&
		reflect.DeepEqual(a.TimeLimit, b.TimeLimit) && reflect.DeepEqual(a.MemoryLimit, b.MemoryLimit) &&
		a.TaskType == b.TaskType && a.ScoreType == b.ScoreType &&
		sameJSON(a.TaskTypeParameters, b.TaskTypeParameters) && sameJSON(a.ScoreTypeParameters, b.ScoreTypeParameters)
}

func sameJSON(a, b json.RawMessage) bool {
	va, ea := decodeJSON(a)
	vb, eb := decodeJSON(b)
	if ea != nil || eb != nil {
		return string(a) == string(b)
	}
	return reflect.DeepEqual(va, vb)
}

// decodeJSON decodes parameters; numbers become float64, so 50 and 50.0
// (Python's float, in datasets written by CMS) are the same.
func decodeJSON(raw json.RawMessage) (any, error) {
	var v any
	err := json.Unmarshal(raw, &v)
	return v, err
}

// usec rounds a duration to what PostgreSQL stores (microseconds).
func usec(d time.Duration) time.Duration { return d.Truncate(time.Microsecond) }

func usecPtr(d *time.Duration) *time.Duration {
	if d == nil {
		return nil
	}
	v := usec(*d)
	return &v
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
