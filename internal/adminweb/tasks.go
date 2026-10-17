// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/server/admin/handlers/task.py of CMS:
// Copyright © 2010-2013 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2012-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2014 Artem Iglikov <artem.iglikov@gmail.com>
// Copyright © 2014 Fabian Gundlach <320pointsguy@gmail.com>
// Copyright © 2016 Myungwoo Chun <mc.tamaki@gmail.com>
// Copyright © 2025 Pasit Sangprachathanarak <ouipingpasit@gmail.com>

package adminweb

// Tasks, statements and attachments: the pages of
// cms/server/admin/handlers/task.py. The contest's task list
// (contesttask.py) is in contests_tasks.go.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/scoretype"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// The task's defaults when created from the admin (CMS AddTaskHandler).
var (
	defaultTaskTypeParameters  = json.RawMessage(`["alone", ["", ""], "diff"]`)
	defaultScoreTypeParameters = json.RawMessage(`100`)
)

// loadTask returns the task of the path value {task_id}; it answers 404
// (or 500) itself and returns false when the handler must stop. With
// withContest the contest of the task becomes the contest of the page
// (CMS self.contest = task.contest).
func (c *Ctx) loadTask(withContest bool) (*q.Task, bool) {
	id, ok := c.PathID("task_id")
	if !ok {
		c.NotFound()
		return nil, false
	}
	t, err := c.S.st.Q.GetTask(c.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		c.NotFound()
		return nil, false
	}
	if err != nil {
		c.Fail(err)
		return nil, false
	}
	if withContest && t.ContestID != nil && !c.SetContest(*t.ContestID) {
		return nil, false
	}
	return &t, true
}

// notifyTask tells the dispatcher that a task or its datasets changed
// (CMS proxy_service.dataset_updated / reinitialize and
// search_operations_not_done): it judges what the change left to judge
// and sends the task to the scoreboard again.
func (s *Server) notifyTask(ctx context.Context, contestID *int32, taskID, datasetID int32) {
	ev := judging.Event{Kind: judging.EventDataset, TaskID: taskID, DatasetID: datasetID}
	if contestID != nil {
		ev.ContestID = *contestID
	}
	if err := judging.Notify(ctx, s.kv, ev); err != nil {
		s.log.Warn("notifying the dispatcher of a task change", "task", taskID, "err", err)
	}
}

// contestChanged queues the live event of a change of the tasks of a
// contest (pages reload their task lists).
func contestChanged(tx *Tx, contestID *int32) {
	if contestID != nil {
		tx.Publish(live.ContestChange{Contest: int64(*contestID)}.Event())
	}
}

func contestOf(t *q.Task) int32 { return deref(t.ContestID) }

// tasksData is the data of tasks.html.
type tasksData struct {
	*Page
	Tasks []q.AdminTaskListRow
}

// tasksPage is GET /tasks (CMS TaskListHandler).
func (s *Server) tasksPage(c *Ctx) {
	tasks, err := s.st.Q.AdminTaskList(c.Context())
	if err != nil {
		c.Fail(err)
		return
	}
	p, err := c.Page()
	if err != nil {
		c.Fail(err)
		return
	}
	c.Render("tasks.html", tasksData{Page: p, Tasks: tasks})
}

// tasksPost is POST /tasks: the Remove button opens the confirmation.
func (s *Server) tasksPost(c *Ctx) {
	taskID, ok1 := c.Arg("task_id")
	operation, ok2 := c.Arg("operation")
	if !ok1 || !ok2 {
		c.BadRequest()
		return
	}
	if operation == "Remove" || operation == c.T("Remove") {
		c.Redirect(c.URL("tasks", taskID, "remove"))
		return
	}
	c.notify(levelError, fmt.Sprintf(c.T("Invalid operation %s"), operation), "")
	c.Redirect(c.URL("tasks"))
}

// removeTaskData is the data of task_remove.html.
type removeTaskData struct {
	*Page
	Task            *q.Task
	SubmissionCount int64
}

// removeTaskPage is GET /tasks/<id>/remove (CMS RemoveTaskHandler).
func (s *Server) removeTaskPage(c *Ctx) {
	t, ok := c.loadTask(false)
	if !ok {
		return
	}
	n, err := s.st.Q.AdminTaskSubmissionCount(c.Context(), t.ID)
	if err != nil {
		c.Fail(err)
		return
	}
	p, err := c.Page()
	if err != nil {
		c.Fail(err)
		return
	}
	c.Render("task_remove.html", removeTaskData{Page: p, Task: t, SubmissionCount: n})
}

// removeTaskDelete is DELETE /tasks/<id>/remove: the task goes with its
// statements, datasets and submissions; the following tasks of its
// contest move up.
func (s *Server) removeTaskDelete(c *Ctx) {
	t, ok := c.loadTask(false)
	if !ok {
		return
	}
	err := c.Write(func(tx *Tx) error {
		old, err := tx.Q.AdminLockTask(tx.Ctx, t.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return Invalid("The task does not exist any more.")
		}
		if err != nil {
			return err
		}
		subs, err := tx.Q.AdminTaskSubmissionCount(tx.Ctx, old.ID)
		if err != nil {
			return err
		}
		if _, err := tx.Q.DeleteTask(tx.Ctx, old.ID); err != nil {
			return err
		}
		if old.ContestID != nil && old.Num != nil {
			if err := tx.Q.AdminCompactTaskNums(tx.Ctx, q.AdminCompactTaskNumsParams{ContestID: *old.ContestID, Num: *old.Num}); err != nil {
				return err
			}
			if err := tx.Q.AdminFinishCompactTaskNums(tx.Ctx, *old.ContestID); err != nil {
				return err
			}
		}
		contestChanged(tx, old.ContestID)
		return tx.Audit(AuditEntry{Action: "task.delete", TargetType: "task", TargetID: int64(old.ID),
			ContestID: contestOf(&old), Before: old, Details: map[string]any{"submissions": subs}})
	})
	if c.Commit(err) && t.ContestID != nil {
		if err := judging.Notify(c.Context(), s.kv, judging.Event{Kind: judging.EventSweep, ContestID: *t.ContestID}); err != nil {
			s.log.Warn("notifying the dispatcher of a removed task", "task", t.ID, "err", err)
		}
	}
	c.WriteText("../../tasks")
}

// addTaskPage is GET /tasks/add (CMS AddTaskHandler).
func (s *Server) addTaskPage(c *Ctx) {
	p, err := c.Page()
	if err != nil {
		c.Fail(err)
		return
	}
	c.Render("add_task.html", struct{ *Page }{p})
}

// addTaskPost is POST /tasks/add: a task with the given name (also its
// title), the submission format <name>.%l and a first, live dataset
// "Default" (Batch, Sum of 100 points).
func (s *Server) addTaskPost(c *Ctx) {
	fallback := c.URL("tasks", "add")
	name, _ := c.Arg("name")
	if name == "" {
		c.NotifyError(N("Invalid field(s)"), "No task name specified.")
		c.Redirect(fallback)
		return
	}
	var task q.Task
	err := c.Write(func(tx *Tx) error {
		var err error
		task, err = s.createTask(tx, name, name, []string{name + ".%l"})
		return err
	})
	if !c.Commit(err) {
		c.Redirect(fallback)
		return
	}
	c.Redirect(c.URL("task", task.ID))
}

// createTask creates a task with CMS's defaults and its first dataset,
// live (AddTaskHandler), with their audit entries.
func (s *Server) createTask(tx *Tx, name, title string, format []string) (q.Task, error) {
	task, err := tx.Q.CreateTask(tx.Ctx, q.CreateTaskParams{
		Name: name, Title: title, SubmissionFormat: format, PrimaryStatements: []string{},
		TokenMode: "disabled", TokenGenInitial: 2, TokenGenNumber: 2, TokenGenInterval: 30 * 60 * 1e9,
		FeedbackLevel: "restricted", ScoreMode: "max_tokened_last",
	})
	if err != nil {
		return task, err
	}
	ds, err := tx.Q.CreateDataset(tx.Ctx, q.CreateDatasetParams{
		TaskID: task.ID, Description: "Default", Autojudge: true,
		TaskType: "Batch", TaskTypeParameters: defaultTaskTypeParameters,
		ScoreType: "Sum", ScoreTypeParameters: defaultScoreTypeParameters,
	})
	if err != nil {
		return task, err
	}
	task, err = tx.Q.SetTaskActiveDataset(tx.Ctx, q.SetTaskActiveDatasetParams{ID: task.ID, ActiveDatasetID: &ds.ID})
	if err != nil {
		return task, err
	}
	if err := tx.Audit(AuditEntry{Action: "task.create", TargetType: "task", TargetID: int64(task.ID), After: task}); err != nil {
		return task, err
	}
	return task, tx.Audit(AuditEntry{Action: "dataset.create", TargetType: "dataset", TargetID: int64(ds.ID), After: ds})
}

// taskPageData is the data of task.html.
type taskPageData struct {
	*Page
	Task            *q.Task
	SubmissionCount int64
	Statements      []q.Statement
	Primary         map[string]bool
	Attachments     []q.Attachment
	Languages       []string
	Datasets        []*datasetView
	ScoreTypes      []string
	TaskTypeNames   []string
	Form            taskFormView
}

// taskFormView holds the values of the task form as CMS prints them.
type taskFormView struct {
	ActiveDataset         int32
	Num                   string
	Allowed               map[string]bool
	TokenMaxNumber        string
	TokenMinInterval      string
	TokenGenInterval      string
	TokenGenMax           string
	MaxSubmissionNumber   string
	MaxUserTestNumber     string
	MinSubmissionInterval string
	MinUserTestInterval   string
}

// optInt prints a nullable integer ("" for NULL, as CMS's forms).
func taskOptInt(p *int32) string {
	if p == nil {
		return ""
	}
	return strconv.Itoa(int(*p))
}

// optSeconds prints a nullable interval as int(total_seconds()).
func optSeconds(d *time.Duration) string {
	if d == nil {
		return ""
	}
	return strconv.FormatInt(int64(d.Seconds()), 10)
}

func newTaskFormView(t *q.Task) taskFormView {
	v := taskFormView{
		ActiveDataset:         deref(t.ActiveDatasetID),
		Num:                   "None",
		Allowed:               map[string]bool{},
		TokenMaxNumber:        taskOptInt(t.TokenMaxNumber),
		TokenMinInterval:      optSeconds(&t.TokenMinInterval),
		TokenGenInterval:      strconv.FormatInt(int64(t.TokenGenInterval.Seconds())/60, 10),
		TokenGenMax:           taskOptInt(t.TokenGenMax),
		MaxSubmissionNumber:   taskOptInt(t.MaxSubmissionNumber),
		MaxUserTestNumber:     taskOptInt(t.MaxUserTestNumber),
		MinSubmissionInterval: optSeconds(t.MinSubmissionInterval),
		MinUserTestInterval:   optSeconds(t.MinUserTestInterval),
	}
	if t.Num != nil {
		v.Num = strconv.Itoa(int(*t.Num))
	}
	for _, l := range t.AllowedLanguages {
		v.Allowed[l] = true
	}
	return v
}

// datasetView is a dataset of the task page with what it shows of it.
type datasetView struct {
	q.Dataset
	Live           bool
	Managers       []q.Manager
	Testcases      []q.Testcase
	TaskTypeKnown  bool
	ScoreTypeKnown bool
	TaskTypes      []taskTypeOptions
	TimeLimit      string
	MemoryLimit    string
	ScoreParams    template.HTML
}

// taskPage is GET /task/<id> (CMS TaskHandler): every column of the task,
// its statements and attachments, and each dataset with its parameters,
// managers and testcases (eight queries whatever their number).
func (s *Server) taskPage(c *Ctx) {
	t, ok := c.loadTask(true)
	if !ok {
		return
	}
	ctx := c.Context()
	d := taskPageData{Task: t, Primary: map[string]bool{}, ScoreTypes: scoreTypeNames(), TaskTypeNames: taskTypeNames(),
		Form: newTaskFormView(t)}
	var err error
	if d.SubmissionCount, err = s.st.Q.AdminTaskSubmissionCount(ctx, t.ID); err != nil {
		c.Fail(err)
		return
	}
	if d.Statements, err = s.st.Q.AdminTaskStatements(ctx, t.ID); err != nil {
		c.Fail(err)
		return
	}
	for _, l := range t.PrimaryStatements {
		d.Primary[l] = true
	}
	if d.Attachments, err = s.st.Q.AdminTaskAttachments(ctx, t.ID); err != nil {
		c.Fail(err)
		return
	}
	if d.Datasets, err = s.datasetViews(c, t); err != nil {
		c.Fail(err)
		return
	}
	for _, l := range s.langs.All() {
		d.Languages = append(d.Languages, l.Name)
	}
	slices.Sort(d.Languages)
	if d.Page, err = c.Page(); err != nil {
		c.Fail(err)
		return
	}
	c.Render("task.html", d)
}

// datasetViews loads the datasets of a task (by description) with their
// managers and testcases.
func (s *Server) datasetViews(c *Ctx, t *q.Task) ([]*datasetView, error) {
	ctx := c.Context()
	datasets, err := s.st.Q.AdminTaskDatasets(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	managers, err := s.st.Q.AdminTaskManagers(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	testcases, err := s.st.Q.AdminTaskTestcases(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	byID := map[int32]*datasetView{}
	var out []*datasetView
	for _, ds := range datasets {
		v := &datasetView{
			Dataset:        ds,
			Live:           t.ActiveDatasetID != nil && *t.ActiveDatasetID == ds.ID,
			TaskTypeKnown:  slices.Contains(taskTypeNames(), ds.TaskType),
			ScoreTypeKnown: scoretype.Valid(ds.ScoreType),
			TaskTypes:      taskTypeForms(c.Lang, ds.TaskType, ds.TaskTypeParameters, fmt.Sprintf("TaskTypeOptions_%d_", ds.ID)),
			TimeLimit:      pyFloatStr(ds.TimeLimit),
			ScoreParams:    template.HTML(toJSONForceEscape(ds.ScoreTypeParameters)),
		}
		if ds.MemoryLimit != nil {
			v.MemoryLimit = strconv.FormatInt(*ds.MemoryLimit/(1<<20), 10)
		}
		byID[ds.ID] = v
		out = append(out, v)
	}
	for _, m := range managers {
		if v := byID[m.DatasetID]; v != nil {
			v.Managers = append(v.Managers, m)
		}
	}
	for _, tc := range testcases {
		if v := byID[tc.DatasetID]; v != nil {
			v.Testcases = append(v.Testcases, tc)
		}
	}
	return out, nil
}

// taskForm reads the task part of the task page's form into p (CMS
// TaskHandler.post).
func (c *Ctx) taskForm(p *q.UpdateTaskParams, statements []q.Statement) error {
	f := c.Form()
	var name *string
	if v, ok := f.Get("name"); ok {
		name = &v
	} else {
		name = &p.Name
	}
	if *name == "" {
		f.Fail("No task name specified.")
	}
	p.Name = *name
	f.String(&p.Title, "title")
	primary := []string{}
	for _, st := range statements {
		var on bool
		f.Bool(&on, "primary_statement_"+st.Language)
		if on {
			primary = append(primary, st.Language)
		}
	}
	slices.Sort(primary)
	p.PrimaryStatements = primary
	// CMS get_submission_format with submission_format_choice "other".
	switch choice := c.ArgDefault("submission_format_choice", "other"); choice {
	case "simple":
		p.SubmissionFormat = []string{p.Name + ".%l"}
	case "other":
		p.SubmissionFormat = []string{}
		if v, ok := f.Get("submission_format"); ok {
			for _, e := range strings.Split(v, ",") {
				if e = strings.TrimSpace(e); e != "" {
					p.SubmissionFormat = append(p.SubmissionFormat, e)
				}
			}
		}
	default:
		f.Fail("Submission format not recognized.")
	}
	f.String(&p.FeedbackLevel, "feedback_level")
	if c.R.Form != nil {
		if langs := c.R.Form["allowed_languages"]; len(langs) > 0 {
			p.AllowedLanguages = slices.Clone(langs)
		} else {
			p.AllowedLanguages = nil
		}
	}
	f.String(&p.TokenMode, "token_mode")
	f.OptInt32(&p.TokenMaxNumber, "token_max_number")
	f.Duration(&p.TokenMinInterval, "token_min_interval")
	f.Int32(&p.TokenGenInitial, "token_gen_initial")
	f.Int32(&p.TokenGenNumber, "token_gen_number")
	f.DurationMin(&p.TokenGenInterval, "token_gen_interval")
	f.OptInt32(&p.TokenGenMax, "token_gen_max")
	f.OptInt32(&p.MaxSubmissionNumber, "max_submission_number")
	f.OptInt32(&p.MaxUserTestNumber, "max_user_test_number")
	f.OptDuration(&p.MinSubmissionInterval, "min_submission_interval")
	f.OptDuration(&p.MinUserTestInterval, "min_user_test_interval")
	f.Int32(&p.ScorePrecision, "score_precision")
	f.String(&p.ScoreMode, "score_mode")
	return f.Err()
}

// taskPost is POST /task/<id>: the task and the limits, task type, score
// type and public testcases of each dataset, in one transaction.
func (s *Server) taskPost(c *Ctx) {
	t, ok := c.loadTask(false)
	if !ok {
		return
	}
	back := c.URL("task", t.ID)
	ctx := c.Context()
	statements, err := s.st.Q.AdminTaskStatements(ctx, t.ID)
	if err != nil {
		c.Fail(err)
		return
	}
	datasets, err := s.st.Q.AdminTaskDatasets(ctx, t.ID)
	if err != nil {
		c.Fail(err)
		return
	}
	testcases, err := s.st.Q.AdminTaskTestcases(ctx, t.ID)
	if err != nil {
		c.Fail(err)
		return
	}
	tp := currentTaskParams(t)
	if err := c.taskForm(&tp, statements); err != nil {
		c.NotifyError(N("Invalid field(s)"), err.Error())
		c.Redirect(back)
		return
	}
	var dps []q.UpdateDatasetParams
	for _, ds := range datasets {
		dp := datasetParams(ds)
		if err := c.datasetForm(&dp, fmt.Sprintf("time_limit_%d", ds.ID), fmt.Sprintf("memory_limit_%d", ds.ID),
			fmt.Sprintf("task_type_%d", ds.ID), fmt.Sprintf("TaskTypeOptions_%d_", ds.ID),
			fmt.Sprintf("score_type_%d", ds.ID), fmt.Sprintf("score_type_parameters_%d", ds.ID)); err != nil {
			c.NotifyError(N("Invalid field(s)"), err.Error())
			c.Redirect(back)
			return
		}
		public := map[string]bool{}
		for _, tc := range testcases {
			if tc.DatasetID == ds.ID {
				v, _ := c.Arg(fmt.Sprintf("testcase_%d_public", tc.ID))
				public[tc.Codename] = v != ""
			}
		}
		if _, err := scoretype.New(dp.ScoreType, dp.ScoreTypeParameters, public, int(tp.ScorePrecision)); err != nil {
			c.NotifyError(N("Invalid score type parameters"), err.Error())
			c.Redirect(back)
			return
		}
		dps = append(dps, dp)
	}
	var publicIDs []int32
	for _, tc := range testcases {
		if v, _ := c.Arg(fmt.Sprintf("testcase_%d_public", tc.ID)); v != "" {
			publicIDs = append(publicIDs, tc.ID)
		}
	}
	err = c.Write(func(tx *Tx) error {
		old, err := tx.Q.AdminLockTask(tx.Ctx, t.ID)
		if err != nil {
			return err
		}
		tp.ID = old.ID
		upd, err := tx.Q.UpdateTask(tx.Ctx, tp)
		if err != nil {
			return err
		}
		if err := tx.Audit(AuditEntry{Action: "task.update", TargetType: "task", TargetID: int64(upd.ID),
			ContestID: contestOf(&upd), Before: old, After: upd}); err != nil {
			return err
		}
		for i, dp := range dps {
			if sameDatasetParams(datasetParams(datasets[i]), dp) {
				continue
			}
			nd, err := tx.Q.UpdateDataset(tx.Ctx, dp)
			if err != nil {
				return err
			}
			if err := tx.Audit(AuditEntry{Action: "dataset.update", TargetType: "dataset", TargetID: int64(nd.ID),
				ContestID: contestOf(&upd), Before: datasets[i], After: nd}); err != nil {
				return err
			}
		}
		n, err := tx.Q.AdminSetTaskTestcasesPublic(tx.Ctx, q.AdminSetTaskTestcasesPublicParams{PublicIds: publicIDs, TaskID: upd.ID})
		if err != nil {
			return err
		}
		if n > 0 {
			if err := tx.Audit(AuditEntry{Action: "testcase.public", TargetType: "task", TargetID: int64(upd.ID),
				ContestID: contestOf(&upd), Details: map[string]any{"changed": n, "public_ids": publicIDs}}); err != nil {
				return err
			}
		}
		if old.ScoreMode != upd.ScoreMode || old.ScorePrecision != upd.ScorePrecision {
			if _, err := judging.RecomputeTask(tx.Ctx, tx.Tx, upd.ID); err != nil {
				return err
			}
		}
		contestChanged(tx, upd.ContestID)
		return nil
	})
	if c.Commit(err) {
		s.notifyTask(ctx, t.ContestID, t.ID, 0)
	}
	c.Redirect(back)
}

// currentTaskParams are the columns of a task as UpdateTask takes them.
func currentTaskParams(t *q.Task) q.UpdateTaskParams {
	return q.UpdateTaskParams{
		ID: t.ID, Name: t.Name, Title: t.Title, SubmissionFormat: t.SubmissionFormat,
		PrimaryStatements: t.PrimaryStatements, AllowedLanguages: t.AllowedLanguages,
		TokenMode: t.TokenMode, TokenMaxNumber: t.TokenMaxNumber, TokenMinInterval: t.TokenMinInterval,
		TokenGenInitial: t.TokenGenInitial, TokenGenNumber: t.TokenGenNumber,
		TokenGenInterval: t.TokenGenInterval, TokenGenMax: t.TokenGenMax,
		MaxSubmissionNumber: t.MaxSubmissionNumber, MaxUserTestNumber: t.MaxUserTestNumber,
		MinSubmissionInterval: t.MinSubmissionInterval, MinUserTestInterval: t.MinUserTestInterval,
		FeedbackLevel: t.FeedbackLevel, ScorePrecision: t.ScorePrecision, ScoreMode: t.ScoreMode,
	}
}

// statementPageData is the data of add_statement.html, add_attachment.html.
type taskSubpageData struct {
	*Page
	Task *q.Task
}

// addStatementPage is GET /task/<id>/statements/add.
func (s *Server) addStatementPage(c *Ctx) {
	s.taskSubpage(c, "add_statement.html")
}

func (s *Server) taskSubpage(c *Ctx, page string) {
	t, ok := c.loadTask(true)
	if !ok {
		return
	}
	p, err := c.Page()
	if err != nil {
		c.Fail(err)
		return
	}
	c.Render(page, taskSubpageData{Page: p, Task: t})
}

// addStatementPost is POST /task/<id>/statements/add (CMS
// AddStatementHandler): a PDF for a language code.
func (s *Server) addStatementPost(c *Ctx) {
	t, ok := c.loadTask(false)
	if !ok {
		return
	}
	fallback := c.URL("task", t.ID, "statements", "add")
	language, _ := c.Arg("language")
	if language == "" {
		c.Notify(N("No language code specified"), N("The language code can be any string."), nil)
		c.Redirect(fallback)
		return
	}
	fh := c.File("statement")
	if fh == nil {
		c.BadRequest()
		return
	}
	if !strings.HasSuffix(fh.Filename, ".pdf") {
		c.Notify(N("Invalid task statement"), N("The task statement must be a .pdf file."), nil)
		c.Redirect(fallback)
		return
	}
	digest, _, err := c.PutUpload(fh)
	if err != nil {
		c.NotifyError(N("Task statement storage failed"), err.Error())
		c.Redirect(fallback)
		return
	}
	err = c.Write(func(tx *Tx) error {
		st, err := tx.Q.CreateStatement(tx.Ctx, q.CreateStatementParams{TaskID: t.ID, Language: language, Digest: digest})
		if err != nil {
			return err
		}
		contestChanged(tx, t.ContestID)
		return tx.Audit(AuditEntry{Action: "statement.create", TargetType: "task", TargetID: int64(t.ID),
			ContestID: contestOf(t), After: st})
	})
	if c.Commit(err) {
		c.Redirect(c.URL("task", t.ID))
	} else {
		c.Redirect(fallback)
	}
}

// statementDelete is DELETE /task/<id>/statement/<id>.
func (s *Server) statementDelete(c *Ctx) {
	t, ok := c.loadTask(false)
	if !ok {
		return
	}
	id, _ := c.PathID("statement_id")
	st, err := s.st.Q.GetStatement(c.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && st.TaskID != t.ID {
		c.NotFound()
		return
	}
	if err != nil {
		c.Fail(err)
		return
	}
	err = c.Write(func(tx *Tx) error {
		if _, err := tx.Q.DeleteStatement(tx.Ctx, st.ID); err != nil {
			return err
		}
		contestChanged(tx, t.ContestID)
		return tx.Audit(AuditEntry{Action: "statement.delete", TargetType: "task", TargetID: int64(t.ID),
			ContestID: contestOf(t), Before: st})
	})
	c.Commit(err)
	c.WriteText(strconv.Itoa(int(t.ID)))
}

// addAttachmentPage is GET /task/<id>/attachments/add.
func (s *Server) addAttachmentPage(c *Ctx) {
	s.taskSubpage(c, "add_attachment.html")
}

// addAttachmentPost is POST /task/<id>/attachments/add (CMS
// AddAttachmentHandler).
func (s *Server) addAttachmentPost(c *Ctx) {
	t, ok := c.loadTask(false)
	if !ok {
		return
	}
	fallback := c.URL("task", t.ID, "attachments", "add")
	fh := c.File("attachment")
	if fh == nil {
		c.BadRequest()
		return
	}
	digest, _, err := c.PutUpload(fh)
	if err != nil {
		c.NotifyError(N("Attachment storage failed"), err.Error())
		c.Redirect(fallback)
		return
	}
	err = c.Write(func(tx *Tx) error {
		a, err := tx.Q.CreateAttachment(tx.Ctx, q.CreateAttachmentParams{TaskID: t.ID, Filename: fh.Filename, Digest: digest})
		if err != nil {
			return err
		}
		contestChanged(tx, t.ContestID)
		return tx.Audit(AuditEntry{Action: "attachment.create", TargetType: "task", TargetID: int64(t.ID),
			ContestID: contestOf(t), After: a})
	})
	if c.Commit(err) {
		c.Redirect(c.URL("task", t.ID))
	} else {
		c.Redirect(fallback)
	}
}

// attachmentDelete is DELETE /task/<id>/attachment/<id>.
func (s *Server) attachmentDelete(c *Ctx) {
	t, ok := c.loadTask(false)
	if !ok {
		return
	}
	id, _ := c.PathID("attachment_id")
	a, err := s.st.Q.GetAttachment(c.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && a.TaskID != t.ID {
		c.NotFound()
		return
	}
	if err != nil {
		c.Fail(err)
		return
	}
	err = c.Write(func(tx *Tx) error {
		if _, err := tx.Q.DeleteAttachment(tx.Ctx, a.ID); err != nil {
			return err
		}
		contestChanged(tx, t.ContestID)
		return tx.Audit(AuditEntry{Action: "attachment.delete", TargetType: "task", TargetID: int64(t.ID),
			ContestID: contestOf(t), Before: a})
	})
	c.Commit(err)
	c.WriteText(strconv.Itoa(int(t.ID)))
}
