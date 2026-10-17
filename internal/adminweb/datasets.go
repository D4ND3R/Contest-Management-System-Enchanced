// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/server/admin/handlers/dataset.py and the dataset part of
// task.py (AddDatasetHandler) of CMS:
// Copyright © 2010-2013 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2012-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2014 Artem Iglikov <artem.iglikov@gmail.com>
// Copyright © 2014 Fabian Gundlach <320pointsguy@gmail.com>
// Copyright © 2016 Myungwoo Chun <mc.tamaki@gmail.com>

package adminweb

// Datasets, managers and testcases: the pages of
// cms/server/admin/handlers/dataset.py (its submissions page,
// DatasetSubmissionsHandler, is in submissions.go).

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/scoretype"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/tasktype"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// mib is a mebibyte: forms show memory limits in MiB, the database
// stores bytes.
const mib = 1 << 20

// datasetParams are the columns of a dataset as UpdateDataset takes them.
func datasetParams(d q.Dataset) q.UpdateDatasetParams {
	return q.UpdateDatasetParams{
		ID: d.ID, Description: d.Description, Autojudge: d.Autojudge,
		TimeLimit: d.TimeLimit, MemoryLimit: d.MemoryLimit,
		TaskType: d.TaskType, TaskTypeParameters: d.TaskTypeParameters,
		ScoreType: d.ScoreType, ScoreTypeParameters: d.ScoreTypeParameters,
	}
}

func sameDatasetParams(a, b q.UpdateDatasetParams) bool {
	eqF := func(x, y *float64) bool { return x == nil && y == nil || x != nil && y != nil && *x == *y }
	eqI := func(x, y *int64) bool { return x == nil && y == nil || x != nil && y != nil && *x == *y }
	return a.Description == b.Description && a.Autojudge == b.Autojudge &&
		eqF(a.TimeLimit, b.TimeLimit) && eqI(a.MemoryLimit, b.MemoryLimit) &&
		a.TaskType == b.TaskType && sameJSON(a.TaskTypeParameters, b.TaskTypeParameters) &&
		a.ScoreType == b.ScoreType && sameJSON(a.ScoreTypeParameters, b.ScoreTypeParameters)
}

// sameJSON compares two JSON values (not their spelling).
func sameJSON(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return bytes.Equal(a, b)
	}
	ja, _ := json.Marshal(x)
	jb, _ := json.Marshal(y)
	return bytes.Equal(ja, jb)
}

// datasetForm reads the limits, task type and score type of a dataset
// form (CMS get_time_limit, get_memory_limit, get_task_type and
// get_score_type) into p; absent limits are kept.
func (c *Ctx) datasetForm(p *q.UpdateDatasetParams, timeField, memoryField, typeField, typePrefix, scoreField, scoreParamsField string) error {
	if v, ok := c.Arg(timeField); ok {
		if v == "" {
			p.TimeLimit = nil
		} else {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return fmt.Errorf("Can't cast %s to float.", v)
			}
			if !(f >= 0 && !math.IsInf(f, 1)) {
				return errors.New("Time limit out of range.")
			}
			p.TimeLimit = &f
		}
	}
	if v, ok := c.Arg(memoryField); ok {
		if v == "" {
			p.MemoryLimit = nil
		} else {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return fmt.Errorf("Can't cast %s to int.", v)
			}
			if n <= 0 || n > math.MaxInt64/mib {
				return errors.New("Invalid memory limit.")
			}
			m := n * mib
			p.MemoryLimit = &m
		}
	}
	name, ok := c.Arg(typeField)
	if !ok {
		return errors.New("Task type not found.")
	}
	if !tasktype.Valid(name) {
		return fmt.Errorf("Task type not recognized: %s.", name)
	}
	params, err := tasktype.ParseForm(name, c.Arg, typePrefix+name+"_")
	if err != nil {
		return err
	}
	p.TaskType, p.TaskTypeParameters = name, params
	st, ok := c.Arg(scoreField)
	if !ok {
		return errors.New("Score type not found.")
	}
	if !scoretype.Valid(st) {
		return fmt.Errorf("Score type not recognized: %s.", st)
	}
	raw, ok := c.Arg(scoreParamsField)
	if !ok {
		return errors.New("Score type parameters not found.")
	}
	if !json.Valid([]byte(raw)) {
		return errors.New("Score type parameters are invalid JSON.")
	}
	p.ScoreType, p.ScoreTypeParameters = st, json.RawMessage(raw)
	return nil
}

// loadDataset returns the dataset of the path value {dataset_id} with its
// task (404 itself); withContest makes the task's contest the page's.
func (c *Ctx) loadDataset(withContest bool) (*q.Dataset, *q.Task, bool) {
	id, ok := c.PathID("dataset_id")
	if !ok {
		c.NotFound()
		return nil, nil, false
	}
	row, err := c.S.st.Q.AdminDatasetWithTask(c.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		c.NotFound()
		return nil, nil, false
	}
	if err != nil {
		c.Fail(err)
		return nil, nil, false
	}
	if withContest && row.Task.ContestID != nil && !c.SetContest(*row.Task.ContestID) {
		return nil, nil, false
	}
	return &row.Dataset, &row.Task, true
}

// addDatasetData is the data of add_dataset.html.
type addDatasetData struct {
	*Page
	Task *q.Task
	// CloneID is "new" or the id of the dataset cloned (Original).
	CloneID             string
	Original            *q.Dataset
	OriginalHasManagers bool
	DefaultDescription  string
	TimeLimit           string
	MemoryLimit         string
	TaskTypeNames       []string
	TaskTypes           []taskTypeOptions
	ScoreTypes          []string
	ScoreParams         template.HTML
}

func (s *Server) renderAddDataset(c *Ctx, t *q.Task, orig *q.Dataset) {
	d := addDatasetData{Task: t, CloneID: "new", DefaultDescription: "Default", TimeLimit: "1", MemoryLimit: "512",
		TaskTypeNames: taskTypeNames(), ScoreTypes: scoreTypeNames()}
	selected := ""
	var params json.RawMessage
	if orig != nil {
		d.CloneID = strconv.Itoa(int(orig.ID))
		d.Original = orig
		d.DefaultDescription = "Copy of " + orig.Description
		d.TimeLimit = pyFloatStr(orig.TimeLimit)
		d.MemoryLimit = ""
		if orig.MemoryLimit != nil {
			d.MemoryLimit = strconv.FormatInt(*orig.MemoryLimit/mib, 10)
		}
		d.ScoreParams = template.HTML(toJSONForceEscape(orig.ScoreTypeParameters))
		selected, params = orig.TaskType, orig.TaskTypeParameters
		ms, err := s.st.Q.AdminDatasetManagers(c.Context(), orig.ID)
		if err != nil {
			c.Fail(err)
			return
		}
		d.OriginalHasManagers = len(ms) > 0
	}
	d.TaskTypes = taskTypeForms(c.Lang, selected, params, "TaskTypeOptions_")
	var err error
	if d.Page, err = c.Page(); err != nil {
		c.Fail(err)
		return
	}
	c.Render("add_dataset.html", d)
}

// addDatasetPage is GET /task/<id>/add_dataset (CMS AddDatasetHandler).
func (s *Server) addDatasetPage(c *Ctx) {
	t, ok := c.loadTask(true)
	if !ok {
		return
	}
	s.renderAddDataset(c, t, nil)
}

// addDatasetPost is POST /task/<id>/add_dataset: a new, empty dataset,
// live if the task has none.
func (s *Server) addDatasetPost(c *Ctx) {
	t, ok := c.loadTask(false)
	if !ok {
		return
	}
	s.createDataset(c, t, nil, c.URL("task", t.ID, "add_dataset"))
}

// createDataset reads the form of add_dataset.html and creates the
// dataset (a clone of orig with its managers and testcases, and its
// results if asked, when orig is not nil).
func (s *Server) createDataset(c *Ctx, t *q.Task, orig *q.Dataset, fallback string) {
	description, _ := c.Arg("description")
	taken, err := s.st.Q.AdminDatasetDescriptionTaken(c.Context(), q.AdminDatasetDescriptionTakenParams{TaskID: t.ID, Description: description})
	if err != nil {
		c.Fail(err)
		return
	}
	if taken {
		c.notify(levelError, fmt.Sprintf(c.T("Dataset name %s is already taken."), pyRepr(description)),
			c.T("Please choose a unique name for this dataset."))
		c.Redirect(fallback)
		return
	}
	p := q.UpdateDatasetParams{Description: description}
	if err := c.datasetForm(&p, "time_limit", "memory_limit", "task_type", "TaskTypeOptions_", "score_type", "score_type_parameters"); err != nil {
		c.NotifyError(N("Invalid field(s)"), err.Error())
		c.Redirect(fallback)
		return
	}
	cloneResults := false
	if orig != nil {
		v, _ := c.Arg("clone_results")
		cloneResults = v != ""
	}
	var ds q.Dataset
	err = c.Write(func(tx *Tx) error {
		task, err := tx.Q.AdminLockTask(tx.Ctx, t.ID)
		if err != nil {
			return err
		}
		ds, err = tx.Q.CreateDataset(tx.Ctx, q.CreateDatasetParams{
			TaskID: task.ID, Description: p.Description, Autojudge: false,
			TimeLimit: p.TimeLimit, MemoryLimit: p.MemoryLimit,
			TaskType: p.TaskType, TaskTypeParameters: p.TaskTypeParameters,
			ScoreType: p.ScoreType, ScoreTypeParameters: p.ScoreTypeParameters,
		})
		if err != nil {
			return err
		}
		details := map[string]any{}
		if orig != nil {
			details["cloned_from"] = orig.ID
			cp := q.CopyDatasetManagersParams{ToDatasetID: ds.ID, FromDatasetID: orig.ID}
			if _, err := tx.Q.CopyDatasetManagers(tx.Ctx, cp); err != nil {
				return err
			}
			if _, err := tx.Q.CopyDatasetTestcases(tx.Ctx, q.CopyDatasetTestcasesParams(cp)); err != nil {
				return err
			}
			if cloneResults {
				details["clone_results"] = true
				if _, err := tx.Q.AdminCloneResults(tx.Ctx, q.AdminCloneResultsParams(cp)); err != nil {
					return err
				}
				if _, err := tx.Q.AdminCloneExecutables(tx.Ctx, q.AdminCloneExecutablesParams(cp)); err != nil {
					return err
				}
				if _, err := tx.Q.AdminCloneEvaluations(tx.Ctx, q.AdminCloneEvaluationsParams(cp)); err != nil {
					return err
				}
			}
		}
		if task.ActiveDatasetID == nil {
			if _, err := tx.Q.SetTaskActiveDataset(tx.Ctx, q.SetTaskActiveDatasetParams{ID: task.ID, ActiveDatasetID: &ds.ID}); err != nil {
				return err
			}
			details["live"] = true
		}
		return tx.Audit(AuditEntry{Action: "dataset.create", TargetType: "dataset", TargetID: int64(ds.ID),
			ContestID: contestOf(&task), After: ds, Details: details})
	})
	if c.Commit(err) {
		c.Redirect(c.URL("task", t.ID))
	} else {
		c.Redirect(fallback)
	}
}

// pyRepr is Python's repr() of a string ('Default', "it's").
func pyRepr(s string) string {
	q := "'"
	if strings.Contains(s, "'") && !strings.Contains(s, `"`) {
		q = `"`
	}
	var b strings.Builder
	b.WriteString(q)
	for _, r := range s {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case string(r) == q:
			b.WriteString(`\` + q)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteString(q)
	return b.String()
}

// cloneDatasetPage is GET /dataset/<id>/clone (CMS CloneDatasetHandler).
func (s *Server) cloneDatasetPage(c *Ctx) {
	ds, t, ok := c.loadDataset(true)
	if !ok {
		return
	}
	s.renderAddDataset(c, t, ds)
}

// cloneDatasetPost is POST /dataset/<id>/clone.
func (s *Server) cloneDatasetPost(c *Ctx) {
	ds, t, ok := c.loadDataset(false)
	if !ok {
		return
	}
	s.createDataset(c, t, ds, c.URL("dataset", ds.ID, "clone"))
}

// datasetPageData is the data of the pages of one dataset
// (rename_dataset.html, delete_dataset.html, add_manager.html,
// add_testcase(s).html, download_testcases.html).
type datasetPageData struct {
	*Page
	Task    *q.Task
	Dataset *q.Dataset
}

func (s *Server) datasetSubpage(c *Ctx, page string) {
	ds, t, ok := c.loadDataset(true)
	if !ok {
		return
	}
	p, err := c.Page()
	if err != nil {
		c.Fail(err)
		return
	}
	c.Render(page, datasetPageData{Page: p, Task: t, Dataset: ds})
}

// renameDatasetPage is GET /dataset/<id>/rename.
func (s *Server) renameDatasetPage(c *Ctx) { s.datasetSubpage(c, "rename_dataset.html") }

// renameDatasetPost is POST /dataset/<id>/rename (CMS RenameDatasetHandler).
func (s *Server) renameDatasetPost(c *Ctx) {
	ds, t, ok := c.loadDataset(false)
	if !ok {
		return
	}
	fallback := c.URL("dataset", ds.ID, "rename")
	description := c.ArgDefault("description", "")
	taken, err := s.st.Q.AdminDatasetDescriptionTaken(c.Context(), q.AdminDatasetDescriptionTakenParams{
		TaskID: t.ID, Description: description, ExceptID: ds.ID})
	if err != nil {
		c.Fail(err)
		return
	}
	if taken {
		c.notify(levelError, fmt.Sprintf(c.T("Dataset name \"%s\" is already taken."), description),
			c.T("Please choose a unique name for this dataset."))
		c.Redirect(fallback)
		return
	}
	err = c.Write(func(tx *Tx) error {
		p := datasetParams(*ds)
		p.Description = description
		nd, err := tx.Q.UpdateDataset(tx.Ctx, p)
		if err != nil {
			return err
		}
		return tx.Audit(AuditEntry{Action: "dataset.rename", TargetType: "dataset", TargetID: int64(ds.ID),
			ContestID: contestOf(t), Before: ds, After: nd})
	})
	if c.Commit(err) {
		c.Redirect(c.URL("task", t.ID))
	} else {
		c.Redirect(fallback)
	}
}

// deleteDatasetPage is GET /dataset/<id>/delete.
func (s *Server) deleteDatasetPage(c *Ctx) { s.datasetSubpage(c, "delete_dataset.html") }

// deleteDatasetPost is POST /dataset/<id>/delete (CMS
// DeleteDatasetHandler). The live dataset cannot be deleted
// (fk_active_dataset_id).
func (s *Server) deleteDatasetPost(c *Ctx) {
	ds, t, ok := c.loadDataset(false)
	if !ok {
		return
	}
	err := c.Write(func(tx *Tx) error {
		if _, err := tx.Q.DeleteDataset(tx.Ctx, ds.ID); err != nil {
			return err
		}
		return tx.Audit(AuditEntry{Action: "dataset.delete", TargetType: "dataset", TargetID: int64(ds.ID),
			ContestID: contestOf(t), Before: ds})
	})
	c.Commit(err)
	c.Redirect(c.URL("task", t.ID))
}

// maxDatasetChanges bounds the rows of the activation preview.
const maxDatasetChanges = 5000

// activateData is the data of activate_dataset.html.
type activateData struct {
	*Page
	Task    *q.Task
	Dataset *q.Dataset
	// Active is the live dataset (nil: none).
	Active *q.Dataset
	// Changes are grouped by participation.
	Changes   []participationChanges
	Truncated bool
	NoChanges bool
}

// participationChanges are the changes of one participant's submissions.
type participationChanges struct {
	ParticipationID int32
	UserID          int32
	Username        string
	DefaultNotify   bool
	Rows            []datasetChange
}

// datasetChange is a submission whose scores differ (CMS
// SubmissionScoreDelta), as the page prints them (Python's str).
type datasetChange struct {
	SubmissionID                int32
	Tokened                     bool
	Score, PublicScore, Ranking bool
	OldScore, NewScore          string
	OldPublicScore, NewPublic   string
	OldRanking, NewRanking      template.HTML
}

func pyOptFloat(f *float64) string {
	if f == nil {
		return "None"
	}
	return pyFloatStr(f)
}

// rankingJSON is x|tojson|forceescape of ranking_score_details.
func rankingJSON(details []string) template.HTML {
	b, _ := json.Marshal(details)
	return template.HTML(toJSONForceEscape(b))
}

// activateDatasetPage is GET /dataset/<id>/activate (CMS
// ActivateDatasetHandler): what changes for each submission when the
// dataset becomes live, and the participants to notify.
func (s *Server) activateDatasetPage(c *Ctx) {
	ds, t, ok := c.loadDataset(true)
	if !ok {
		return
	}
	ctx := c.Context()
	d := activateData{Task: t, Dataset: ds}
	if t.ActiveDatasetID != nil {
		a, err := s.st.Q.GetDataset(ctx, *t.ActiveDatasetID)
		if err != nil {
			c.Fail(err)
			return
		}
		d.Active = &a
	}
	rows, err := s.st.Q.AdminDatasetChanges(ctx, q.AdminDatasetChangesParams{
		OldDatasetID: t.ActiveDatasetID, NewDatasetID: ds.ID, TaskID: t.ID, MaxRows: maxDatasetChanges + 1})
	if err != nil {
		c.Fail(err)
		return
	}
	if len(rows) > maxDatasetChanges {
		rows, d.Truncated = rows[:maxDatasetChanges], true
	}
	for _, r := range rows {
		ch := datasetChange{SubmissionID: r.SubmissionID, Tokened: r.Tokened}
		if !eqFloat(r.OldScore, r.NewScore) {
			ch.Score, ch.OldScore, ch.NewScore = true, pyOptFloat(r.OldScore), pyOptFloat(r.NewScore)
		}
		if !eqFloat(r.OldPublicScore, r.NewPublicScore) {
			ch.PublicScore, ch.OldPublicScore, ch.NewPublic = true, pyOptFloat(r.OldPublicScore), pyOptFloat(r.NewPublicScore)
		}
		if !slices.Equal(r.OldRankingScoreDetails, r.NewRankingScoreDetails) || (r.OldRankingScoreDetails == nil) != (r.NewRankingScoreDetails == nil) {
			ch.Ranking = true
			ch.OldRanking, ch.NewRanking = "null", "null"
			if r.OldRankingScoreDetails != nil {
				ch.OldRanking = rankingJSON(r.OldRankingScoreDetails)
			}
			if r.NewRankingScoreDetails != nil {
				ch.NewRanking = rankingJSON(r.NewRankingScoreDetails)
			}
		}
		if n := len(d.Changes); n == 0 || d.Changes[n-1].ParticipationID != r.ParticipationID {
			d.Changes = append(d.Changes, participationChanges{ParticipationID: r.ParticipationID, UserID: r.UserID, Username: r.Username})
		}
		pc := &d.Changes[len(d.Changes)-1]
		// CMS notifies by default those whose public score changed, or
		// whose score changed on a tokened submission.
		if ch.PublicScore || r.Tokened && ch.Score {
			pc.DefaultNotify = true
		}
		pc.Rows = append(pc.Rows, ch)
	}
	d.NoChanges = len(d.Changes) == 0
	if d.Page, err = c.Page(); err != nil {
		c.Fail(err)
		return
	}
	c.Render("activate_dataset.html", d)
}

func eqFloat(a, b *float64) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

var notifyArg = regexp.MustCompile(`^notify_([0-9]+)$`)

// activateDatasetPost is POST /dataset/<id>/activate: the dataset becomes
// live (judging.ActivateDataset recomputes the scores of the task in its
// own transaction), then the chosen participants get a message.
func (s *Server) activateDatasetPost(c *Ctx) {
	ds, t, ok := c.loadDataset(false)
	if !ok {
		return
	}
	ctx := c.Context()
	prev, changes, err := s.judge.ActivateDataset(ctx, t.ID, ds.ID)
	if err != nil {
		c.Commit(err)
		c.Redirect(c.URL("task", t.ID))
		return
	}
	var ids []int32
	if c.R.Form != nil {
		for k := range c.R.Form {
			if m := notifyArg.FindStringSubmatch(k); m != nil {
				if n, err := strconv.ParseInt(m[1], 10, 32); err == nil {
					ids = append(ids, int32(n))
				}
			}
		}
	}
	slices.Sort(ids)
	subject := c.ArgDefault("message_subject", "")
	text := c.ArgDefault("message_text", "")
	sent := 0
	err = c.Write(func(tx *Tx) error {
		details := map[string]any{"previous": prev, "task_scores_changed": len(changes)}
		if len(ids) > 0 && t.ContestID != nil {
			rows, err := tx.Q.AdminSendMessages(tx.Ctx, q.AdminSendMessagesParams{AdminID: &c.Admin.ID, Timestamp: c.Now,
				Subject: subject, Text: text, ParticipationIds: ids, ContestID: *t.ContestID})
			if err != nil {
				return err
			}
			sent = len(rows)
			for _, r := range rows {
				tx.Publish(live.PrivateMessage{Contest: int64(*t.ContestID), Participation: int64(r.ParticipationID), ID: int64(r.ID)}.Event())
			}
			details["messages"] = sent
		}
		if t.ContestID != nil {
			tx.Publish(live.ScoreboardChange{Contest: int64(*t.ContestID), All: true}.Event())
		}
		return tx.Audit(AuditEntry{Action: "dataset.activate", TargetType: "dataset", TargetID: int64(ds.ID),
			ContestID: contestOf(t), Details: details})
	})
	if c.Commit(err) {
		s.notifyTask(ctx, t.ContestID, t.ID, ds.ID)
		c.notify(levelSuccess, fmt.Sprintf(c.T("Messages sent to %d users."), sent), "")
	}
	c.Redirect(c.URL("task", t.ID))
}

// autojudgeDatasetPost is POST /dataset/<id>/autojudge (CMS
// ToggleAutojudgeDatasetHandler): background judging on or off.
func (s *Server) autojudgeDatasetPost(c *Ctx) {
	ds, t, ok := c.loadDataset(false)
	if !ok {
		return
	}
	err := c.Write(func(tx *Tx) error {
		nd, err := tx.Q.AdminToggleAutojudge(tx.Ctx, ds.ID)
		if err != nil {
			return err
		}
		return tx.Audit(AuditEntry{Action: "dataset.autojudge", TargetType: "dataset", TargetID: int64(ds.ID),
			ContestID: contestOf(t), Before: ds, After: nd})
	})
	if c.Commit(err) {
		s.notifyTask(c.Context(), t.ContestID, t.ID, ds.ID)
	}
	c.WriteText(fmt.Sprintf("./%d", t.ID))
}

// addManagerPage is GET /dataset/<id>/managers/add.
func (s *Server) addManagerPage(c *Ctx) { s.datasetSubpage(c, "add_manager.html") }

// addManagerPost is POST /dataset/<id>/managers/add (CMS AddManagerHandler).
func (s *Server) addManagerPost(c *Ctx) {
	ds, t, ok := c.loadDataset(false)
	if !ok {
		return
	}
	fallback := c.URL("dataset", ds.ID, "managers", "add")
	fh := c.File("manager")
	if fh == nil {
		c.BadRequest()
		return
	}
	digest, _, err := c.PutUpload(fh)
	if err != nil {
		c.NotifyError(N("Manager storage failed"), err.Error())
		c.Redirect(fallback)
		return
	}
	err = c.Write(func(tx *Tx) error {
		m, err := tx.Q.CreateManager(tx.Ctx, q.CreateManagerParams{DatasetID: ds.ID, Filename: fh.Filename, Digest: digest})
		if err != nil {
			return err
		}
		return tx.Audit(AuditEntry{Action: "manager.create", TargetType: "dataset", TargetID: int64(ds.ID),
			ContestID: contestOf(t), After: m})
	})
	if c.Commit(err) {
		c.Redirect(c.URL("task", t.ID))
	} else {
		c.Redirect(fallback)
	}
}

// managerDelete is DELETE /dataset/<id>/manager/<id>/delete.
func (s *Server) managerDelete(c *Ctx) {
	ds, t, ok := c.loadDataset(false)
	if !ok {
		return
	}
	id, _ := c.PathID("manager_id")
	m, err := s.st.Q.GetManager(c.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && m.DatasetID != ds.ID {
		c.NotFound()
		return
	}
	if err != nil {
		c.Fail(err)
		return
	}
	err = c.Write(func(tx *Tx) error {
		if _, err := tx.Q.DeleteManager(tx.Ctx, m.ID); err != nil {
			return err
		}
		return tx.Audit(AuditEntry{Action: "manager.delete", TargetType: "dataset", TargetID: int64(ds.ID),
			ContestID: contestOf(t), Before: m})
	})
	c.Commit(err)
	c.WriteText(fmt.Sprintf("./%d", t.ID))
}

// addTestcasePage is GET /dataset/<id>/testcases/add.
func (s *Server) addTestcasePage(c *Ctx) { s.datasetSubpage(c, "add_testcase.html") }

// addTestcasePost is POST /dataset/<id>/testcases/add (CMS
// AddTestcaseHandler).
func (s *Server) addTestcasePost(c *Ctx) {
	ds, t, ok := c.loadDataset(false)
	if !ok {
		return
	}
	fallback := c.URL("dataset", ds.ID, "testcases", "add")
	codename, ok := c.Arg("codename")
	if !ok {
		c.BadRequest()
		return
	}
	in, out := c.File("input"), c.File("output")
	if in == nil || out == nil {
		c.Notify(N("Invalid data"), N("Please fill both input and output."), nil)
		c.Redirect(fallback)
		return
	}
	_, public := c.Arg("public")
	inDigest, _, err := c.PutUpload(in)
	var outDigest string
	if err == nil {
		outDigest, _, err = c.PutUpload(out)
	}
	if err != nil {
		c.NotifyError(N("Testcase storage failed"), err.Error())
		c.Redirect(fallback)
		return
	}
	err = c.Write(func(tx *Tx) error {
		tc, err := tx.Q.CreateTestcase(tx.Ctx, q.CreateTestcaseParams{DatasetID: ds.ID, Codename: codename,
			Public: public, Input: inDigest, Output: outDigest})
		if err != nil {
			return err
		}
		return tx.Audit(AuditEntry{Action: "testcase.create", TargetType: "dataset", TargetID: int64(ds.ID),
			ContestID: contestOf(t), After: tc})
	})
	if c.Commit(err) {
		s.notifyTask(c.Context(), t.ContestID, t.ID, ds.ID)
		c.Redirect(c.URL("task", t.ID))
	} else {
		c.Redirect(fallback)
	}
}

// addTestcasesPage is GET /dataset/<id>/testcases/add_multiple.
func (s *Server) addTestcasesPage(c *Ctx) { s.datasetSubpage(c, "add_testcases.html") }

// templateRegexp turns a file name template ("input.*") into CMS's
// regular expression: the template escaped, "*" capturing the codename,
// anchored at the end (and at the start by Python's re.match).
func templateRegexp(tmpl string) (*regexp.Regexp, error) {
	parts := strings.Split(tmpl, "*")
	for i := range parts {
		parts[i] = regexp.QuoteMeta(parts[i])
	}
	return regexp.Compile("^" + strings.Join(parts, "(.*)") + "$")
}

// addTestcasesPost is POST /dataset/<id>/testcases/add_multiple (CMS
// AddTestcasesHandler and import_testcases_from_zipfile): the inputs and
// outputs of a zip, matched by the two name templates. The zip is read in
// place from the upload and every file streams to the blob store; all
// rows are written in one transaction (CMS commits them one by one).
func (s *Server) addTestcasesPost(c *Ctx) {
	ds, t, ok := c.loadDataset(false)
	if !ok {
		return
	}
	fallback := c.URL("dataset", ds.ID, "testcases", "add_multiple")
	fh := c.File("archive")
	if fh == nil {
		c.Notify(N("Invalid data"), N("Please choose tests archive."), nil)
		c.Redirect(fallback)
		return
	}
	_, public := c.Arg("public")
	_, overwrite := c.Arg("overwrite")
	inRe, err1 := templateRegexp(c.ArgDefault("input_template", "input.*"))
	outRe, err2 := templateRegexp(c.ArgDefault("output_template", "output.*"))
	if err := errors.Join(err1, err2); err != nil {
		c.NotifyError(N("Invalid field(s)"), err.Error())
		c.Redirect(fallback)
		return
	}
	f, err := OpenUpload(fh)
	if err != nil {
		c.Fail(err)
		return
	}
	defer f.Close()
	zr, err := zip.NewReader(f, fh.Size)
	if err != nil {
		msg := c.T("The selected file is not a zip file. Please select a valid zip file.")
		c.notify(levelError, msg, err.Error())
		c.Redirect(fallback)
		return
	}
	type pair struct{ in, out *zip.File }
	tests := map[string]*pair{}
	var order []string
	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() {
			continue
		}
		var codename string
		isIn := false
		if m := inRe.FindStringSubmatch(zf.Name); m != nil {
			codename, isIn = m[1], true
		} else if m := outRe.FindStringSubmatch(zf.Name); m != nil {
			codename = m[1]
		} else {
			continue
		}
		p := tests[codename]
		if p == nil {
			p = &pair{}
			tests[codename] = p
			order = append(order, codename)
		}
		if isIn {
			p.in = zf
		} else {
			p.out = zf
		}
	}
	existing, err := s.st.Q.AdminDatasetTestcases(c.Context(), ds.ID)
	if err != nil {
		c.Fail(err)
		return
	}
	have := map[string]bool{}
	for _, tc := range existing {
		have[tc.Codename] = true
	}
	var added, overwritten, skipped []string
	var rows []q.CreateTestcasesParams
	for _, codename := range order {
		p := tests[codename]
		if p.in == nil || p.out == nil {
			continue
		}
		if have[codename] {
			if !overwrite {
				skipped = append(skipped, codename)
				continue
			}
			overwritten = append(overwritten, codename)
		} else {
			added = append(added, codename)
		}
		inD, err := s.putZipFile(c, p.in)
		if err != nil {
			c.notify(levelError, fmt.Sprintf(c.T("Reading testcase %s failed"), codename), err.Error())
			c.Redirect(fallback)
			return
		}
		outD, err := s.putZipFile(c, p.out)
		if err != nil {
			c.notify(levelError, fmt.Sprintf(c.T("Reading testcase %s failed"), codename), err.Error())
			c.Redirect(fallback)
			return
		}
		rows = append(rows, q.CreateTestcasesParams{DatasetID: ds.ID, Codename: codename, Public: public, Input: inD, Output: outD})
	}
	err = c.Write(func(tx *Tx) error {
		if len(overwritten) > 0 {
			if _, err := tx.Q.AdminDeleteTestcasesByCodename(tx.Ctx, q.AdminDeleteTestcasesByCodenameParams{DatasetID: ds.ID, Codenames: overwritten}); err != nil {
				return err
			}
		}
		if len(rows) > 0 {
			if _, err := tx.Q.CreateTestcases(tx.Ctx, rows); err != nil {
				return err
			}
		}
		return tx.Audit(AuditEntry{Action: "testcase.import", TargetType: "dataset", TargetID: int64(ds.ID),
			ContestID: contestOf(t), Details: map[string]any{"added": added, "overwritten": overwritten, "skipped": skipped}})
	})
	if err != nil {
		c.Commit(err)
		c.Redirect(fallback)
		return
	}
	none := func(l []string) string {
		if len(l) == 0 {
			return c.T("none")
		}
		return strings.Join(l, ", ")
	}
	c.notify(levelSuccess,
		fmt.Sprintf(c.T("Successfully added %d and overwritten %d testcase(s)"), len(added), len(overwritten)),
		fmt.Sprintf(c.T("Added: %s; overwritten: %s; skipped: %s"), none(added), none(overwritten), none(skipped)))
	s.notifyTask(c.Context(), t.ContestID, t.ID, ds.ID)
	c.Redirect(c.URL("task", t.ID))
}

// maxZipMember bounds a file read from an uploaded zip (its declared size
// is checked and the reader is limited: zip bombs stop there).
const maxZipMember = 4 << 30

// putZipFile streams a member of a zip into the blob store.
func (s *Server) putZipFile(c *Ctx, zf *zip.File) (string, error) {
	if zf.UncompressedSize64 > maxZipMember {
		return "", fmt.Errorf("%s is too large", zf.Name)
	}
	rc, err := zf.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()
	digest, _, err := s.blobs.Put(c.Context(), &limitedReader{r: rc, n: int64(zf.UncompressedSize64)})
	return digest, err
}

// testcaseDelete is DELETE /dataset/<id>/testcase/<id>/delete.
func (s *Server) testcaseDelete(c *Ctx) {
	ds, t, ok := c.loadDataset(false)
	if !ok {
		return
	}
	id, _ := c.PathID("testcase_id")
	tc, err := s.st.Q.GetTestcase(c.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && tc.DatasetID != ds.ID {
		c.NotFound()
		return
	}
	if err != nil {
		c.Fail(err)
		return
	}
	err = c.Write(func(tx *Tx) error {
		if _, err := tx.Q.DeleteTestcase(tx.Ctx, tc.ID); err != nil {
			return err
		}
		return tx.Audit(AuditEntry{Action: "testcase.delete", TargetType: "dataset", TargetID: int64(ds.ID),
			ContestID: contestOf(t), Before: tc})
	})
	if c.Commit(err) {
		s.notifyTask(c.Context(), t.ContestID, t.ID, ds.ID)
	}
	c.WriteText(fmt.Sprintf("./%d", t.ID))
}

// downloadTestcasesPage is GET /dataset/<id>/testcases/download.
func (s *Server) downloadTestcasesPage(c *Ctx) { s.datasetSubpage(c, "download_testcases.html") }

// downloadTestcasesPost is POST /dataset/<id>/testcases/download (CMS
// DownloadTestcasesHandler): every testcase in a zip, streamed (CMS
// builds it in memory).
func (s *Server) downloadTestcasesPost(c *Ctx) {
	ds, _, ok := c.loadDataset(false)
	if !ok {
		return
	}
	fallback := c.URL("dataset", ds.ID, "testcases", "download")
	zipName := c.ArgDefault("zip_filename", "testcases.zip")
	inT := c.ArgDefault("input_template", "input.*")
	outT := c.ArgDefault("output_template", "output.*")
	if strings.Count(inT, "*") != 1 || strings.Count(outT, "*") != 1 {
		c.Notify(N("Invalid template format"), N("You must have exactly one '*' in input/output template."), nil)
		c.Redirect(fallback)
		return
	}
	inT, outT = strings.TrimSpace(inT), strings.TrimSpace(outT)
	tcs, err := s.st.Q.AdminDatasetTestcases(c.Context(), ds.ID)
	if err != nil {
		c.Fail(err)
		return
	}
	h := c.W.Header()
	h.Set("Content-Type", "application/zip")
	h.Set("Content-Disposition", `attachment; filename="`+strings.NewReplacer(`"`, "", "\r", "", "\n", "").Replace(zipName)+`"`)
	zw := zip.NewWriter(c.W)
	for _, tc := range tcs {
		for _, f := range [][2]string{{strings.Replace(inT, "*", tc.Codename, 1), tc.Input}, {strings.Replace(outT, "*", tc.Codename, 1), tc.Output}} {
			w, err := zw.Create(f[0])
			if err != nil {
				s.log.Error("writing the testcases zip", "err", err)
				return
			}
			if err := s.copyBlob(c, w, f[1]); err != nil {
				s.log.Error("writing the testcases zip", "digest", f[1], "err", err)
				return
			}
		}
	}
	if err := zw.Close(); err != nil {
		s.log.Error("writing the testcases zip", "err", err)
	}
}
