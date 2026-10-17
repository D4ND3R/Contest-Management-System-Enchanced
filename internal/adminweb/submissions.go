// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/server/admin/handlers/submission.py,
// contestsubmission.py and DatasetSubmissionsHandler in dataset.py of
// CMS:
// Copyright © 2010-2013 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2017 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2012-2014 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2014 Artem Iglikov <artem.iglikov@gmail.com>
// Copyright © 2014 Fabian Gundlach <320pointsguy@gmail.com>
// Copyright © 2016 Amir Keivan Mohtashami <akmohtashami97@gmail.com>

package adminweb

// Submissions (work C): the contest submissions page with the filters
// of R6.3, the submissions of a dataset, the submission page, its
// comment and official status, its files and the diff between two
// submissions (submissions_diff.go). The tables are built in
// submissions_rows.go; user tests are in usertests.go and the
// reevaluation RPC in reevaluate.go.

import (
	"errors"
	"html/template"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/scoretype"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// option is an entry of a <select>.
type option struct {
	Value, Label string
}

// subFilter is the filter form of the submission lists (R6.3; new, with
// the form components of the original).
type subFilter struct {
	// Tasks are the tasks of the contest (nil: no task filter, the
	// dataset page).
	Tasks     []q.Task
	Statuses  []option
	Languages []string
	// The values of the request.
	Task                             int32
	User, Status, Official, Language string
	// Active: some filter is set.
	Active bool
}

// statusOptions are the statuses of the status filter, with the texts
// of the rows.
var statusOptions = []option{
	{string(store.StatusCompiling), N("Compiling...")},
	{string(store.StatusCompilationFailed), N("Compilation failed")},
	{string(store.StatusEvaluating), N("Evaluating...")},
	{string(store.StatusScoring), N("Scoring...")},
	{string(store.StatusScored), N("Scored")},
}

// readSubFilter reads the filter arguments of a list of the contest
// contestID into f. ok is false when the filters select nothing (an
// unknown user): the list is then empty.
func (s *Server) readSubFilter(c *Ctx, contestID int32, f *store.SubmissionFilter, form *subFilter) (ok bool, err error) {
	form.Statuses = statusOptions
	form.Languages = s.langs.Names()
	if v := c.ArgDefault("task", ""); v != "" && form.Tasks != nil {
		if n, err := strconv.ParseInt(v, 10, 32); err == nil {
			for _, t := range form.Tasks {
				if t.ID == int32(n) {
					form.Task, f.TaskID = t.ID, t.ID
				}
			}
		}
	}
	if v := c.ArgDefault("status", ""); store.SubmissionStatus(v).Valid() {
		form.Status, f.Status = v, store.SubmissionStatus(v)
	}
	switch v := c.ArgDefault("official", ""); v {
	case "yes", "no":
		b := v == "yes"
		form.Official, f.Official = v, &b
	}
	if v := c.ArgDefault("language", ""); v != "" {
		form.Language, f.Language = v, v
	}
	ok = true
	if v := c.ArgDefault("user", ""); v != "" {
		form.User = v
		pid, err := s.st.Q.AdminParticipationByUsername(c.Context(), q.AdminParticipationByUsernameParams{ContestID: contestID, Username: v})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			ok = false
		case err != nil:
			return false, err
		default:
			f.ParticipationID = pid
		}
	}
	form.Active = form.Task != 0 || form.Status != "" || form.Official != "" || form.Language != "" || form.User != ""
	return ok, nil
}

// contestSubmissionsData is the data of contest_submissions.html.
type contestSubmissionsData struct {
	*Page
	// Count is the number of submissions of the contest (CMS
	// submission_count) or, with filters, of those that match.
	Count  int64
	Table  *subTable
	Filter *subFilter
}

// contestSubmissionsPage is GET /contest/<id>/submissions (CMS
// ContestSubmissionsHandler), with the filters of R6.3: task, user,
// status, official, language.
func (s *Server) contestSubmissionsPage(c *Ctx) {
	if !c.ContestFromPath() {
		return
	}
	ctx := c.Context()
	tasks, err := s.st.Q.ListTasksByContest(ctx, c.Contest.ID)
	if err != nil {
		c.Fail(err)
		return
	}
	form := &subFilter{Tasks: tasks}
	f := store.SubmissionFilter{ContestID: c.Contest.ID}
	ok, err := s.readSubFilter(c, c.Contest.ID, &f, form)
	if err != nil {
		c.Fail(err)
		return
	}
	tab, count := &subTable{Admin: c.Admin, Pager: c.Pager(0, DefaultPageSize)}, int64(0)
	if ok {
		if tab, count, err = s.submissionTable(c, f); err != nil {
			c.Fail(err)
			return
		}
	}
	p, err := c.Page()
	if err != nil {
		c.Fail(err)
		return
	}
	c.Render("contest_submissions.html", contestSubmissionsData{Page: p, Count: count, Table: tab, Filter: form})
}

// datasetItem is a dataset of the dataset selector.
type datasetItem struct {
	ID          int32
	Description string
	Autojudge   bool
	// Active: it is the active dataset of its task.
	Active bool
}

// datasetSelector is the data of fragments/dataset_selector.html.
type datasetSelector struct {
	Shown    datasetItem
	Datasets []datasetItem
	// URL is the page whose dataset is chosen (dataset_selector_par_url):
	// a dataset's link is URL + "/" + its id.
	URL string
}

// loadDatasets returns the datasets of a task in CMS's order and the one
// with id shown (ok false if the task has no such dataset).
func (s *Server) loadDatasets(c *Ctx, taskID, shown int32, url string) (sel *datasetSelector, ok bool, err error) {
	rows, err := s.st.Q.AdminSubmissionDatasets(c.Context(), taskID)
	if err != nil {
		return nil, false, err
	}
	sel = &datasetSelector{URL: url}
	for _, r := range rows {
		d := datasetItem{ID: r.ID, Description: r.Description, Autojudge: r.Autojudge, Active: r.Active}
		sel.Datasets = append(sel.Datasets, d)
		if r.ID == shown {
			sel.Shown, ok = d, true
		}
	}
	return sel, ok, nil
}

// datasetSubmissionsData is the data of dataset.html.
type datasetSubmissionsData struct {
	*Page
	Task     q.Task
	Selector *datasetSelector
	Count    int64
	Table    *subTable
	Filter   *subFilter
}

// datasetSubmissionsPage is GET /dataset/<id> (CMS
// DatasetSubmissionsHandler): the submissions of the task of a dataset
// with their results on it, with the filters of the contest list (but
// the task).
func (s *Server) datasetSubmissionsPage(c *Ctx) {
	id := mustID(c, "dataset_id")
	row, err := s.st.Q.AdminDatasetTask(c.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		c.NotFound()
		return
	}
	if err != nil {
		c.Fail(err)
		return
	}
	t := row.Task
	if t.ContestID != nil && !c.SetContest(*t.ContestID) {
		return
	}
	sel, _, err := s.loadDatasets(c, t.ID, id, c.URL("dataset"))
	if err != nil {
		c.Fail(err)
		return
	}
	form := &subFilter{}
	f := store.SubmissionFilter{DatasetID: id}
	ok := true
	if t.ContestID != nil {
		if ok, err = s.readSubFilter(c, *t.ContestID, &f, form); err != nil {
			c.Fail(err)
			return
		}
	}
	tab, count := &subTable{Admin: c.Admin, Pager: c.Pager(0, DefaultPageSize)}, int64(0)
	if ok {
		if tab, count, err = s.submissionTable(c, f); err != nil {
			c.Fail(err)
			return
		}
	}
	tab.MatchTask = t.ID
	p, err := c.Page()
	if err != nil {
		c.Fail(err)
		return
	}
	c.Render("dataset.html", datasetSubmissionsData{Page: p, Task: t, Selector: sel, Count: count, Table: tab, Filter: form})
}

// sandboxLink is an archived sandbox of a result (a link to its
// archive when it was stored).
type sandboxLink struct {
	Path     string
	Digest   string
	Filename string
}

// sandboxLinks pairs the sandbox paths of a result with their archives:
// links when there are digests (CMS shows the paths as text otherwise).
func sandboxLinks(paths, digests []string, name func(i int) string) []sandboxLink {
	var out []sandboxLink
	for i, d := range digests {
		l := sandboxLink{Digest: d, Filename: name(i + 1)}
		if i < len(paths) {
			l.Path = paths[i]
		}
		out = append(out, l)
	}
	return out
}

// subEval is a row of the admin evaluations table of the submission page.
type subEval struct {
	evalRow
	Time, WallTime string
	MemoryMiB      *int64
	Sandboxes      []sandboxLink
}

// submissionData is the data of submission.html.
type submissionData struct {
	*Page
	S         q.Submission
	User      q.User
	ContestID int32
	Task      q.Task
	Selector  *datasetSelector
	Files     []subFile
	Token     bool
	// R is the result on the dataset shown (nil: none, compiling).
	R      *q.SubmissionResult
	Status string
	// HasST: the score type of the dataset could be built.
	HasST bool
	// Score and MaxScore are Python's str() (the Status row).
	Score, MaxScore string
	// UserScore is the score as the contestant sees it (CMS
	// format_score).
	UserScore string
	Latency   *float64
	// MaxTime (Python's str()) and MaxMemoryMiB are the largest of the
	// evaluations (CMS get_max_evaluation_resources); nil: N/A.
	MaxTime      *string
	MaxMemoryMiB *int64
	Evaluated    bool
	Compiled     bool
	Scored       bool
	CompSandbox  []sandboxLink
	Evaluations  []subEval
	// UserDetails are the score details as the contestant sees them.
	UserDetails template.HTML
}

// submissionPage is GET /submission/<id>[/<dataset>] (CMS
// SubmissionHandler): the submission with its result on a dataset (the
// active one by default).
func (s *Server) submissionPage(c *Ctx) {
	ctx := c.Context()
	id := mustID(c, "submission_id")
	det, err := s.st.Q.GetSubmissionDetails(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		c.NotFound()
		return
	}
	if err != nil {
		c.Fail(err)
		return
	}
	if !c.SetContest(det.Participation.ContestID) {
		return
	}
	t := det.Task
	var dsID int32
	if c.R.PathValue("dataset_id") != "" {
		dsID = mustID(c, "dataset_id")
	} else if t.ActiveDatasetID != nil {
		dsID = *t.ActiveDatasetID
	}
	sel, ok, err := s.loadDatasets(c, t.ID, dsID, c.URL("submission", id))
	if err != nil {
		c.Fail(err)
		return
	}
	if !ok {
		// CMS: a dataset of another task is an assertion error.
		c.NotFound()
		return
	}
	d := submissionData{S: det.Submission, User: det.User, ContestID: det.Participation.ContestID, Task: t,
		Selector: sel, Token: det.TokenTimestamp != nil, Status: string(store.StatusCompiling)}
	files, err := s.st.Q.ListFilesBySubmission(ctx, id)
	if err != nil {
		c.Fail(err)
		return
	}
	// The files in the order of the submission format (CMS).
	lang := deref(det.Submission.Language)
	for _, name := range t.SubmissionFormat {
		for _, f := range files {
			if f.Filename == name {
				d.Files = append(d.Files, subFile{ID: f.ID, Name: s.langs.LangFilename(lang, name)})
			}
		}
	}
	scorings, err := s.datasetScorings(ctx, []int32{dsID})
	if err != nil {
		c.Fail(err)
		return
	}
	var st scoretype.ScoreType
	if ds := scorings[dsID]; ds != nil && ds.ST != nil {
		st, d.HasST = ds.ST, true
		d.MaxScore = pyFloat(st.MaxScore())
	}
	r, err := s.st.Q.GetSubmissionResult(ctx, q.GetSubmissionResultParams{SubmissionID: id, DatasetID: dsID})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		c.Fail(err)
		return
	default:
		d.R = &r
		d.Status = string(store.ResultStatus(r))
		d.Compiled = r.CompilationOutcome != nil
		d.Evaluated = r.EvaluationOutcome != nil
		d.Scored = d.Status == string(store.StatusScored) || r.Score != nil && r.ScoreDetails != nil &&
			r.PublicScore != nil && r.PublicScoreDetails != nil && r.RankingScoreDetails != nil
		if r.Score != nil {
			d.Score = pyFloat(*r.Score)
		}
		if r.ScoredAt != nil {
			l := r.ScoredAt.Sub(det.Submission.Timestamp).Seconds()
			d.Latency = &l
		}
		d.CompSandbox = sandboxLinks(r.CompilationSandboxPaths, r.CompilationSandboxDigests, func(i int) string {
			return "submission_" + strconv.Itoa(int(id)) + "_compilation_sandbox_" + strconv.Itoa(i) + ".tar.gz"
		})
		tr := scoreLocale{c.Lang}
		if d.HasST && d.Status == string(store.StatusScored) {
			if d.Token {
				d.UserScore = scoretype.FormatScore(deref(r.Score), st.MaxScore(), tr)
			} else {
				d.UserScore = scoretype.FormatScore(deref(r.PublicScore), st.MaxPublicScore(), tr)
			}
		}
		if d.Scored && d.HasST {
			level := scoretype.FeedbackLevel(t.FeedbackLevel)
			if d.Token {
				d.UserDetails = scoretype.HTMLDetails(st, r.ScoreDetails, level, tr)
			} else {
				d.UserDetails = scoretype.HTMLDetails(st, r.PublicScoreDetails, level, tr)
			}
		}
		if d.Evaluated {
			evs, err := s.st.Q.AdminEvaluationsOf(ctx, q.AdminEvaluationsOfParams{SubmissionIds: []int32{id}, DatasetIds: []int32{dsID}})
			if err != nil {
				c.Fail(err)
				return
			}
			var maxT *float64
			for _, e := range evs {
				se := subEval{evalRow: evalRow{AdminEvaluationsOfRow: e, Visible: d.Token || e.Public}}
				if e.ExecutionTime != nil {
					se.Time = pyFloat(*e.ExecutionTime)
					if maxT == nil || *maxT < *e.ExecutionTime {
						maxT = e.ExecutionTime
					}
				}
				if e.ExecutionWallClockTime != nil {
					se.WallTime = pyFloat(*e.ExecutionWallClockTime)
				}
				if e.ExecutionMemory != nil {
					m := *e.ExecutionMemory / (1024 * 1024)
					se.MemoryMiB = &m
					if d.MaxMemoryMiB == nil || *d.MaxMemoryMiB < m {
						d.MaxMemoryMiB = &m
					}
				}
				codename := e.Codename
				se.Sandboxes = sandboxLinks(e.EvaluationSandboxPaths, e.EvaluationSandboxDigests, func(i int) string {
					return "submission_" + strconv.Itoa(int(id)) + "_testcase_" + codename + "_sandbox_" + strconv.Itoa(i) + ".tar.gz"
				})
				d.Evaluations = append(d.Evaluations, se)
			}
			if maxT != nil {
				v := pyFloat(*maxT)
				d.MaxTime = &v
			}
		}
	}
	p, err := c.Page()
	if err != nil {
		c.Fail(err)
		return
	}
	d.Page = p
	c.Render("submission.html", d)
}

// submissionRedirect is where the forms of the submission page go back
// to (with the dataset when the URL had one).
func submissionRedirect(c *Ctx, id int32) string {
	if c.R.PathValue("dataset_id") != "" {
		return c.URL("submission", id, mustID(c, "dataset_id"))
	}
	return c.URL("submission", id)
}

// submissionCommentPost is POST /submission/<id>[/<dataset>]/comment
// (CMS SubmissionCommentHandler).
func (s *Server) submissionCommentPost(c *Ctx) {
	id := mustID(c, "submission_id")
	comment, has := c.Arg("comment")
	err := c.Write(func(tx *Tx) error {
		old, err := tx.Q.GetSubmission(tx.Ctx, id)
		if err != nil {
			return err
		}
		upd := old
		if has {
			if upd, err = tx.Q.AdminSetSubmissionComment(tx.Ctx, q.AdminSetSubmissionCommentParams{ID: id, Comment: comment}); err != nil {
				return err
			}
		}
		sc, err := tx.Q.AdminSubmissionContest(tx.Ctx, id)
		if err != nil {
			return err
		}
		tx.Publish(live.Submission{Contest: int64(sc.ContestID), Participation: int64(old.ParticipationID),
			Task: int64(old.TaskID), ID: int64(id), Status: ""}.Event())
		return tx.Audit(AuditEntry{Action: "submission.comment", TargetType: "submission", TargetID: int64(id),
			ContestID: sc.ContestID, Before: map[string]any{"comment": old.Comment}, After: map[string]any{"comment": upd.Comment}})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		c.NotFound()
		return
	}
	c.Commit(err)
	c.Redirect(submissionRedirect(c, id))
}

// submissionOfficialPost is POST /submission/<id>[/<dataset>]/official
// (CMS SubmissionOfficialStatusHandler). Unlike CMS, the task score of
// the participant is computed again in the same transaction (only
// official submissions count) and the dispatcher is told, so the
// scoreboard follows.
func (s *Server) submissionOfficialPost(c *Ctx) {
	id := mustID(c, "submission_id")
	official := c.ArgDefault("official", "yes") == "yes"
	var sc q.AdminSubmissionContestRow
	err := c.Write(func(tx *Tx) error {
		old, err := tx.Q.GetSubmission(tx.Ctx, id)
		if err != nil {
			return err
		}
		upd, err := tx.Q.AdminSetSubmissionOfficial(tx.Ctx, q.AdminSetSubmissionOfficialParams{ID: id, Official: official})
		if err != nil {
			return err
		}
		if sc, err = tx.Q.AdminSubmissionContest(tx.Ctx, id); err != nil {
			return err
		}
		changes, err := judging.RefreshTaskScores(tx.Ctx, tx.Tx, []judging.TaskKey{{ParticipationID: old.ParticipationID, TaskID: old.TaskID}})
		if err != nil {
			return err
		}
		tx.Publish(taskScoreLiveEvents(changes)...)
		tx.Publish(live.Submission{Contest: int64(sc.ContestID), Participation: int64(old.ParticipationID),
			Task: int64(old.TaskID), ID: int64(id)}.Event())
		return tx.Audit(AuditEntry{Action: "submission.official", TargetType: "submission", TargetID: int64(id),
			ContestID: sc.ContestID, Before: map[string]any{"official": old.Official}, After: map[string]any{"official": upd.Official}})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		c.NotFound()
		return
	}
	if c.Commit(err) {
		s.log.Info("submission official status changed", "submission", id, "official", official, "admin", c.Admin.Username)
		// The dispatcher refreshes the cache and sends the change to the
		// scoreboard (as for a token).
		if err := judging.Notify(c.Context(), s.kv, judging.Event{Kind: judging.EventToken, ContestID: sc.ContestID,
			SubmissionIDs: []int32{id}}); err != nil {
			s.log.Warn("cannot notify the dispatcher", "err", err)
		}
	}
	c.Redirect(submissionRedirect(c, id))
}

// taskScoreLiveEvents are the live events of task score changes: one
// task_score per contestant and task (at most maxTaskScoreEvents, the
// contestant pages also learn it from their submissions) and one
// scoreboard event per contest.
func taskScoreLiveEvents(changes []judging.TaskScoreChange) []live.Event {
	const maxTaskScoreEvents = 1000
	var evs []live.Event
	board := map[int32][]int64{}
	var contests []int32
	for i, ch := range changes {
		if i < maxTaskScoreEvents {
			evs = append(evs, live.TaskScore{Contest: int64(ch.ContestID), Participation: int64(ch.ParticipationID),
				Task: int64(ch.TaskID)}.Event())
		}
		if ch.ContestID != 0 && (ch.Old.Score != ch.New.Score || ch.Old.Partial != ch.New.Partial) {
			if _, seen := board[ch.ContestID]; !seen {
				contests = append(contests, ch.ContestID)
			}
			board[ch.ContestID] = append(board[ch.ContestID], int64(ch.ParticipationID))
		}
	}
	for _, cid := range contests {
		evs = append(evs, live.ScoreboardChange{Contest: int64(cid), Participations: board[cid]}.Event())
	}
	return evs
}

// submissionFilePage is GET /submission_file/<id> (CMS
// SubmissionFileHandler): a file of a submission, named for its
// language.
func (s *Server) submissionFilePage(c *Ctx) {
	f, err := s.st.Q.AdminSubmissionFile(c.Context(), mustID(c, "file_id"))
	if errors.Is(err, pgx.ErrNoRows) {
		c.NotFound()
		return
	}
	if err != nil {
		c.Fail(err)
		return
	}
	s.ServeFile(c, f.Digest, s.langs.LangFilename(deref(f.Language), f.Filename), "text/plain")
}
