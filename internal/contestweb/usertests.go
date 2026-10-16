// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/server/contest/handlers/taskusertest.py and
// accept_user_test (cms/server/contest/submission/workflow.py) of CMS:
// Copyright © 2010-2014 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2012-2016 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2013 Bernard Blackham <bernard@largestprime.net>
// Copyright © 2014 Artem Iglikov <artem.iglikov@gmail.com>
// Copyright © 2014 Fabian Gundlach <320pointsguy@gmail.com>
// Copyright © 2015-2016 William Di Luigi <williamdiluigi@gmail.com>
// Copyright © 2016 Myungwoo Chun <mc.tamaki@gmail.com>
// Copyright © 2016 Amir Keivan Mohtashami <akmohtashami97@gmail.com>

package contestweb

// User tests (CMS handlers/taskusertest.py): the testing page, the test
// form, the status JSON polled by the page, the details and the
// downloads. User tests are numbered per participant and task from 1,
// oldest first (CMS get_user_test).

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/contestweb/submission"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/web"
)

// UserTestsPageSize bounds the user tests the testing page lists per
// task, newest first (D24; CMS lists all of them).
const UserTestsPageSize = 200

// userTestRow is a row of a table of user tests (CMS
// user_test_row.html), with what the template would compute with
// functions (slow in html/template) computed.
type userTestRow struct {
	// Tab is the tab of the row's task.
	Tab       *testTab
	Num       int64
	Timestamp time.Time
	// TimeText is the contest time of the test (R5.9), TimeTitle the
	// time CMS shows there (its title).
	TimeText, TimeTitle string
	Status              int
	// StatusText is the translated status; Final: compilation failed
	// or executed (the details link).
	StatusText template.HTML
	Final      bool
	// Waiting: compiling or executing (the output button waits).
	Waiting   bool
	HasResult bool
	// Time and Memory are the execution time and memory, formatted
	// ("" when unknown).
	Time, Memory string
	HasOutput    bool
	InputURL     string
	OutputURL    string
	// Files are the files and managers to download; OneFile is the only
	// one, if there is exactly one.
	Files   []fileLink
	OneFile *fileLink
}

// testTab is the tab of a task on the testing page.
type testTab struct {
	Task     *Task
	Testable bool
	Active   bool
	// Left is how many tests are left (nil: no limit).
	Left *int64
	// Fields are the file fields: the submission format and the
	// managers contestants provide.
	Fields        []string
	LanguageNames []string
	// ZipForm: no field has a language (the submission.zip form).
	ZipForm  bool
	ShowDate bool
	Rows     []*userTestRow
}

// taskLanguages is a task with its languages (TASK_LANGUAGES of the
// testing page).
type taskLanguages struct {
	TaskName  string
	Languages []taskLanguage
}

// testInterfaceData is the data of test_interface.html.
type testInterfaceData struct {
	*Page
	TaskLanguages []taskLanguages
	Tabs          []*testTab
	// UT are the status numbers (CMS UserTestResult.COMPILING…).
	UT statusNumbers
}

// testInterfacePage is GET testing (CMS UserTestInterfaceHandler).
func (s *Server) testInterfacePage(c *Ctx) {
	if !c.Contest.AllowUserTests {
		c.Error(http.StatusNotFound)
		return
	}
	page, err := c.Page("test_interface")
	if err != nil {
		c.Fail(err)
		return
	}
	p := c.Participation
	rows, err := s.st.Q.CWSUserTests(c.Context(), q.CWSUserTestsParams{ParticipationID: p.ID,
		ContestID: c.Contest.ID, PageSize: UserTestsPageSize})
	if err != nil {
		c.Fail(err)
		return
	}
	byTask := map[int32][]*q.CWSUserTestsRow{}
	totals := map[int32]int64{}
	var contestCount int64
	for i := range rows {
		r := &rows[i]
		if _, seen := totals[r.TaskID]; !seen {
			totals[r.TaskID] = r.Total
			contestCount += r.Total
		}
		byTask[r.TaskID] = append(byTask[r.TaskID], r)
	}
	var contestLeft *int64
	if m := c.Contest.MaxUserTestNumber; m != nil {
		l := int64(*m) - contestCount
		contestLeft = &l
	}
	d := testInterfaceData{Page: page, UT: statusNumbers{Compiling: utCompiling,
		CompilationFailed: utCompilationFailed, Evaluating: utEvaluating, Evaluated: utEvaluated}}
	defaultName := c.ArgDefault("task_name", "")
	var defaultTab *testTab
	for _, t := range page.Tasks {
		names := t.AllowedLanguages(c.Contest)
		d.TaskLanguages = append(d.TaskLanguages, taskLanguages{TaskName: t.Name, Languages: s.taskLanguages(names)})
		tab := &testTab{Task: t, LanguageNames: names}
		tt, err := s.taskType(t)
		if err != nil {
			c.Fail(err)
			return
		}
		if tt != nil {
			tab.Testable = tt.Testable()
			tab.Fields = append(append(tab.Fields, t.SubmissionFormat...), tt.UserManagers()...)
		}
		tab.ZipForm = !hasLanguageElement(tab.Fields)
		left := contestLeft
		if m := t.MaxUserTestNumber; m != nil {
			l := int64(*m) - totals[t.ID]
			if left == nil || *left > l {
				left = &l
			}
		}
		if left != nil {
			l := max(0, *left)
			tab.Left = &l
		}
		allToday := true
		for _, r := range byTask[t.ID] {
			row := s.userTestRow(c, t, r)
			row.Tab = tab
			if !sameDay(r.Timestamp, c.Now, c.RC.TZ) {
				allToday = false
			}
			tab.Rows = append(tab.Rows, row)
		}
		tab.ShowDate = !allToday
		for _, row := range tab.Rows {
			row.TimeTitle = c.timeTitle(row.Timestamp, tab.ShowDate)
		}
		if t.Name == defaultName && defaultTab == nil {
			defaultTab = tab
		}
		d.Tabs = append(d.Tabs, tab)
	}
	if defaultTab == nil && len(d.Tabs) > 0 {
		defaultTab = d.Tabs[0]
	}
	if defaultTab != nil {
		defaultTab.Active = true
	}
	c.Render(http.StatusOK, "test_interface.html", d)
}

// userTestRow builds a row of the testing page.
func (s *Server) userTestRow(c *Ctx, t *Task, r *q.CWSUserTestsRow) *userTestRow {
	row := &userTestRow{Num: r.Num, Timestamp: r.Timestamp, HasResult: r.HasResult,
		Status:    userTestStatus(r.HasResult, r.CompilationOutcome, r.EvaluationOutcome),
		TimeText:  c.contestTimeText(r.Timestamp),
		HasOutput: r.HasOutput,
		InputURL:  c.URL.Join("tasks", t.Name, "tests", r.Num, "input"),
		OutputURL: c.URL.Join("tasks", t.Name, "tests", r.Num, "output"),
	}
	row.StatusText = template.HTML(c.T(userTestStatusText[row.Status]))
	row.Final = row.Status == utCompilationFailed || row.Status == utEvaluated
	row.Waiting = row.Status == utCompiling || row.Status == utEvaluating
	loc := scoreLocale{c.Lang}.locale()
	if r.ExecutionTime != nil {
		row.Time = loc.FormatDuration(*r.ExecutionTime, "short")
	}
	if r.ExecutionMemory != nil {
		row.Memory = loc.FormatSize(*r.ExecutionMemory)
	}
	ext := ""
	if r.Language != nil {
		if l, ok := s.langs.Lookup(*r.Language); ok {
			ext = l.SourceExtension()
		}
	}
	all := append(append([]string{}, r.Files...), r.Managers...)
	for _, f := range all {
		name := f
		if r.Language != nil {
			name = strings.ReplaceAll(f, ".%l", ext)
		}
		row.Files = append(row.Files, fileLink{Name: name, URL: c.URL.Join("tasks", t.Name, "tests", r.Num, "files", name)})
	}
	if len(row.Files) == 1 {
		row.OneFile = &row.Files[0]
	}
	return row
}

// userTestStatusText are the texts of the statuses of user tests (CMS
// user_test_row.html).
var userTestStatusText = map[int]string{
	utCompiling:         "Compiling...",
	utCompilationFailed: "Compilation failed",
	utEvaluating:        "Executing...",
	utEvaluated:         "Executed",
}

// AcceptUserTest receives a user test of the logged-in participant on a
// task from the files of the request (the submission format, the
// managers the task type lets contestants provide, and "input"; or one
// archive) and its "language" argument (CMS accept_user_test), as
// AcceptSubmission does for submissions. submission.ErrTestingNotAllowed
// if the task type has no user tests; a refusal is a
// *submission.Unacceptable.
func (s *Server) AcceptUserTest(c *Ctx, t *Task) (*q.UserTest, error) {
	ctx := c.Context()
	p := c.Participation
	if err := web.ParseForm(c.W, c.R, MaxBody, formMaxMemory); err != nil {
		return nil, errStatus(web.FormStatus(err))
	}
	tt, err := s.taskType(t)
	if err != nil {
		return nil, err
	}
	if tt == nil || !tt.Testable() {
		return nil, submission.ErrTestingNotAllowed
	}
	rules := submission.Rules{
		ContestMaxNumber:   c.Contest.MaxUserTestNumber,
		TaskMaxNumber:      t.MaxUserTestNumber,
		ContestMinInterval: c.Contest.MinUserTestInterval,
		TaskMinInterval:    t.MinUserTestInterval,
		Unrestricted:       p.Unrestricted,
	}
	check := func(qq *q.Queries) error {
		row, err := qq.GetUserTestStats(ctx, q.GetUserTestStatsParams{TaskID: t.ID, ParticipationID: p.ID, ContestID: c.Contest.ID})
		if err != nil {
			return err
		}
		st := submission.Stats{TaskCount: row.TaskCount, ContestCount: row.ContestCount,
			TaskLast: row.TaskLast, ContestLast: row.ContestLast}
		if u := submission.CheckUserTest(rules, st, c.Now); u != nil {
			return u
		}
		return nil
	}
	if err := check(s.st.Q); err != nil {
		return nil, err
	}
	req := s.submitRequest(c, t, tt)
	req.Previous = func(ctx context.Context) (*submission.Previous, error) {
		return s.previousUserTest(ctx, p.ID, t.ID)
	}
	acc, err := submission.AcceptUserTest(ctx, req)
	if err != nil {
		return nil, err
	}
	var ut q.UserTest
	err = s.st.InTx(ctx, func(qq *q.Queries) error {
		if _, err := qq.CWSLockParticipation(ctx, p.ID); err != nil {
			return err
		}
		if err := check(qq); err != nil {
			return err
		}
		var err error
		ut, err = qq.CreateUserTest(ctx, q.CreateUserTestParams{ParticipationID: p.ID, TaskID: t.ID,
			Timestamp: c.Now, Language: acc.LanguageName(), Input: acc.Input})
		if err != nil {
			return err
		}
		names, digests := sortedFiles(acc.Files)
		if _, err := qq.CreateUserTestFiles(ctx, q.CreateUserTestFilesParams{UserTestID: ut.ID, Filenames: names, Digests: digests}); err != nil {
			return err
		}
		names, digests = sortedFiles(acc.Managers)
		_, err = qq.CreateUserTestManagers(ctx, q.CreateUserTestManagersParams{UserTestID: ut.ID, Filenames: names, Digests: digests})
		return err
	})
	if err != nil {
		return nil, err
	}
	s.log.Info("user test accepted", "contest", c.Contest.Name, "username", p.User.Username,
		"task", t.Name, "user_test", ut.ID)
	if err := judging.Notify(ctx, s.kv, judging.Event{Kind: judging.EventUserTest,
		ContestID: c.Contest.ID, UserTestIDs: []int32{ut.ID}}); err != nil {
		s.log.Warn("notifying the dispatcher of a user test", "user_test", ut.ID, "err", err)
	}
	s.pub.Publish(live.UserTest{Contest: int64(c.Contest.ID), Participation: int64(p.ID), Task: int64(t.ID),
		ID: int64(ut.ID), Status: live.UserTestCompiling, New: true}.Event())
	return &ut, nil
}

// previousUserTest reads the latest user test of the participant on the
// task with its files and managers (nil if none).
func (s *Server) previousUserTest(ctx context.Context, participation, task int32) (*submission.Previous, error) {
	r, err := s.st.Q.CWSLatestUserTest(ctx, q.CWSLatestUserTestParams{ParticipationID: participation, TaskID: task})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	prev := &submission.Previous{Language: r.Language, Input: r.Input, Files: map[string]string{}, Managers: map[string]string{}}
	for i, n := range r.FileNames {
		prev.Files[n] = r.FileDigests[i]
	}
	for i, n := range r.ManagerNames {
		prev.Managers[n] = r.ManagerDigests[i]
	}
	return prev, nil
}

// userTestPost is POST tasks/{task}/test (CMS UserTestHandler).
func (s *Server) userTestPost(c *Ctx) {
	if !c.Contest.AllowUserTests {
		c.Error(http.StatusNotFound)
		return
	}
	t, err := c.Task(c.R.PathValue("task"))
	if err != nil {
		c.Fail(err)
		return
	}
	if t == nil {
		c.Error(http.StatusNotFound)
		return
	}
	args := []string{"task_name", t.Name}
	ut, err := s.AcceptUserTest(c, t)
	var u *submission.Unacceptable
	var st errStatus
	switch {
	case errors.Is(err, submission.ErrTestingNotAllowed):
		s.log.Warn("user tried to make a test on a task without tests", "username", c.Participation.User.Username, "task", t.Name)
		c.Error(http.StatusNotFound)
		return
	case errors.As(err, &u):
		c.notifyUnacceptable(u)
	case errors.As(err, &st):
		c.Error(int(st))
		return
	case err != nil:
		c.Fail(err)
		return
	default:
		c.NotifySuccess(submission.MsgTestReceived, submission.MsgTestReceivedText, nil)
		args = append(args, "user_test_id", encryptNumber(int64(ut.ID), s.cfg.DeriveKey("contest-web/submission-id")))
	}
	c.Redirect(c.URL.JoinQuery([]any{"testing"}, args...))
}

// userTestView is a user test of the participant with its result.
type userTestView struct {
	row    q.CWSUserTestRow
	Status int
}

// loadUserTest returns the user test of the path (CMS get_user_test),
// nil if there is none or the contest has no user tests.
func (s *Server) loadUserTest(c *Ctx) (*Task, *userTestView, error) {
	if !c.Contest.AllowUserTests {
		return nil, nil, nil
	}
	t, err := c.Task(c.R.PathValue("task"))
	if err != nil || t == nil {
		return nil, nil, err
	}
	num, ok := c.PathID("id")
	if !ok {
		return t, nil, nil
	}
	row, err := s.st.Q.CWSUserTest(c.Context(), q.CWSUserTestParams{DatasetID: t.ActiveDatasetID,
		ParticipationID: c.Participation.ID, TaskID: t.ID, Num: num})
	if errors.Is(err, pgx.ErrNoRows) {
		return t, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return t, &userTestView{row: row, Status: userTestStatus(row.HasResult, row.CompilationOutcome, row.EvaluationOutcome)}, nil
}

// userTestFromPath answers 404 (or 500) when there is no user test.
func (s *Server) userTestFromPath(c *Ctx) (*Task, *userTestView, bool) {
	t, v, err := s.loadUserTest(c)
	if err != nil {
		c.Fail(err)
		return nil, nil, false
	}
	if v == nil {
		c.Error(http.StatusNotFound)
		return nil, nil, false
	}
	return t, v, true
}

// userTestStatusPage is GET tasks/{task}/tests/{id} (CMS
// UserTestStatusHandler): the status of a user test as JSON, for the
// polling of the testing page.
func (s *Server) userTestStatusPage(c *Ctx) {
	_, v, ok := s.userTestFromPath(c)
	if !ok {
		return
	}
	data := map[string]any{"status": v.Status}
	details := func(msgid string) string {
		return template.HTMLEscapeString(c.T(msgid)) + ` <a class="details">` + template.HTMLEscapeString(c.T("details")) + `</a>`
	}
	switch v.Status {
	case utCompiling:
		data["status_text"] = c.T("Compiling...")
	case utCompilationFailed:
		data["status_text"] = details("Compilation failed")
	case utEvaluating:
		data["status_text"] = c.T("Executing...")
	case utEvaluated:
		data["status_text"] = details("Executed")
		loc := scoreLocale{c.Lang}.locale()
		data["execution_time"] = nil
		if v.row.ExecutionTime != nil {
			data["execution_time"] = loc.FormatDuration(*v.row.ExecutionTime, "short")
		}
		data["memory"] = nil
		if v.row.ExecutionMemory != nil {
			data["memory"] = loc.FormatSize(*v.row.ExecutionMemory)
		}
		data["output"] = v.row.Output != nil
	}
	c.JSON(http.StatusOK, data)
}

// userTestDetailsData is the data of user_test_details.html (a fragment
// loaded into the dialog of the testing page).
type userTestDetailsData struct {
	// Evaluated: the result has an evaluation outcome; Compiled: a
	// compilation outcome (CMS evaluated(), compiled()).
	Evaluated, Compiled bool
	EvaluationText      []string
	Result              compilationOutput
}

// userTestDetailsPage is GET tasks/{task}/tests/{id}/details (CMS
// UserTestDetailsHandler).
func (s *Server) userTestDetailsPage(c *Ctx) {
	_, v, ok := s.userTestFromPath(c)
	if !ok {
		return
	}
	r := &v.row
	d := userTestDetailsData{
		Evaluated:      r.HasResult && r.EvaluationOutcome != nil,
		Compiled:       r.HasResult && r.CompilationOutcome != nil,
		EvaluationText: r.EvaluationText,
		Result: compilationOutput{Text: r.CompilationText, Time: r.CompilationTime, Memory: r.CompilationMemory,
			Stdout: r.CompilationStdout, Stderr: r.CompilationStderr},
	}
	c.Render(http.StatusOK, "user_test_details.html", d)
}

// userTestIOPage is GET tasks/{task}/tests/{id}/input and …/output (CMS
// UserTestIOHandler).
func (s *Server) userTestIOPage(c *Ctx) {
	_, v, ok := s.userTestFromPath(c)
	if !ok {
		return
	}
	io := c.R.PathValue("io")
	digest := v.row.UserTest.Input
	if io == "output" {
		digest = ""
		if v.row.Output != nil {
			digest = *v.row.Output
		}
	}
	if digest == "" {
		c.Error(http.StatusNotFound)
		return
	}
	s.serveFile(c, digest, io, "text/plain", "")
}

// userTestFilePage is GET tasks/{task}/tests/{id}/files/{filename} (CMS
// UserTestFileHandler): a file of the user test (named with the
// language's extension) or a manager (named as stored).
func (s *Server) userTestFilePage(c *Ctx) {
	_, v, ok := s.userTestFromPath(c)
	if !ok {
		return
	}
	r := &v.row
	filename := c.R.PathValue("filename")
	stored := filename
	if lang := r.UserTest.Language; lang != nil {
		if l, ok := s.langs.Lookup(*lang); ok {
			stored = replaceExtension(filename, l.SourceExtension())
		}
	}
	for i, n := range r.FileNames {
		if n == stored {
			s.serveFile(c, r.FileDigests[i], filename, "", "")
			return
		}
	}
	for i, n := range r.ManagerNames {
		if n == filename {
			s.serveFile(c, r.ManagerDigests[i], filename, "", "")
			return
		}
	}
	c.Error(http.StatusNotFound)
}
