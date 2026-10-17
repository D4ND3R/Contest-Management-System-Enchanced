// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from render_params_for_submissions and
// render_params_for_user_tests in cms/server/admin/handlers/base.py and
// the data the macros of cms/server/admin/templates/macro/submission.html
// and fragments/user_test_row.html read from the model, of CMS:
// Copyright © 2010-2016 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2012-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>

package adminweb

// The submission and user test tables of the admin pages (work C): the
// contest submissions, the dataset submissions, the participation page
// (through participationSubmissionsHook, set in submissions_hook.go) and
// the contest user tests. A page of rows costs a bounded number of
// queries whatever the size of the contest (D24): the count, the page
// (store.ListSubmissions), the files, the hidden texts of the results
// (compilation output, score details), the score types of the datasets
// shown and, only when some row is "Scoring...", its evaluations.

import (
	"context"
	"html/template"
	"slices"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/scoretype"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/web"
)

// scoreLocale is the language of a request as the score types need it
// (scoretype.Translation).
type scoreLocale struct{ l *web.Lang }

func (s scoreLocale) T(msgid string) string { return s.l.T(msgid) }

func (s scoreLocale) FormatDecimal(x float64) string { return s.locale().FormatDecimal(x) }

func (s scoreLocale) FormatDuration(seconds float64) string {
	return s.locale().FormatDuration(seconds, "short")
}

func (s scoreLocale) FormatSize(n int64) string { return s.locale().FormatSize(n) }

func (s scoreLocale) FormatStatusText(text []string) string { return web.FormatStatusText(text, s.l) }

func (s scoreLocale) locale() web.Locale {
	if s.l != nil && s.l.Locale != nil {
		return s.l.Locale
	}
	return web.BasicLocale{Code: "en"}
}

// datasetScoring is a dataset with its score type (CMS
// dataset.score_type_object); ST is nil when the score type cannot be
// built from its parameters (CMS: "[Cannot get score type - see logs]").
type datasetScoring struct {
	q.AdminDatasetsScoringRow
	ST scoretype.ScoreType
}

// datasetScorings loads the score types of datasets (one query).
func (s *Server) datasetScorings(ctx context.Context, ids []int32) (map[int32]*datasetScoring, error) {
	out := map[int32]*datasetScoring{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.st.Q.AdminDatasetsScoring(ctx, uniqIDs(ids))
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		public := make(map[string]bool, len(r.Codenames))
		for i, cn := range r.Codenames {
			public[cn] = i < len(r.Publics) && r.Publics[i]
		}
		d := &datasetScoring{AdminDatasetsScoringRow: r}
		st, err := scoretype.New(r.ScoreType, r.ScoreTypeParameters, public, int(r.ScorePrecision))
		if err != nil {
			s.log.Warn("cannot build the score type of a dataset", "dataset", r.ID, "err", err)
		} else {
			d.ST = st
		}
		out[r.ID] = d
	}
	return out, nil
}

// uniqIDs returns the ids sorted without duplicates (and without 0).
func uniqIDs(ids []int32) []int32 {
	out := slices.Clone(ids)
	slices.Sort(out)
	out = slices.Compact(out)
	if len(out) > 0 && out[0] == 0 {
		out = out[1:]
	}
	return out
}

// pyFloat writes a float as Python's str() (the {{ sr.score }} of the
// templates of CMS).
func pyFloat(f float64) string {
	s, _ := web.PyFormat("%s", f)
	return s
}

// subFile is a file of a submission or user test in a table.
type subFile struct {
	ID int32
	// Name is the file name with %l replaced (CMS get_lang_filename).
	Name string
}

// evalRow is an evaluation shown in a table.
type evalRow struct {
	q.AdminEvaluationsOfRow
	// Visible: the contestant sees it (a token was played or the
	// testcase is public).
	Visible bool
}

// subRow is a row of macro "submission.rows" (CMS macro row).
type subRow struct {
	ID        int32
	Timestamp time.Time
	ContestID int32
	UserID    int32
	Username  string
	TaskID    int32
	TaskName  string
	// DatasetID is the dataset whose result the row shows (0: none).
	DatasetID int32
	// Status is the store status of the result ("compiling"...).
	Status string
	// Score and MaxScore are Python's str() of the score and of the
	// score type's maximum; HasScoreType is false when the score type
	// cannot be built.
	Score, MaxScore string
	HasScoreType    bool
	// Details is the HTML of the score details (scored rows).
	Details template.HTML
	// Evaluated: the result has an evaluation outcome; Evaluations are
	// shown by "Scoring..." rows.
	Evaluated       bool
	Evaluations     []evalRow
	CompilationText []string
	// Latency is the time from the submission to its score, in seconds.
	Latency  *float64
	Files    []subFile
	Token    bool
	Official bool
	// ShortComment is the first line of the comment.
	ShortComment string
}

// subTable is the data of macro "submission.rows": a page of
// submissions.
type subTable struct {
	Admin *Admin
	Pager *Pager
	Rows  []*subRow
	// MatchParticipation and MatchTask limit the live region of the
	// rows to the events of a participation or a task (0: any).
	MatchParticipation int32
	MatchTask          int32
}

// shortComment is CMS's Submission.short_comment.
func shortComment(c string) string {
	for i := 0; i < len(c); i++ {
		if c[i] == '\n' {
			return c[:i]
		}
	}
	return c
}

// submissionTable loads a page (the request's ?page=) of the
// submissions that match f, newest first, for macro "submission.rows".
// It returns the table and the number of matching submissions.
func (s *Server) submissionTable(c *Ctx, f store.SubmissionFilter) (*subTable, int64, error) {
	ctx := c.Context()
	count, err := store.CountSubmissions(ctx, s.pool, f)
	if err != nil {
		return nil, 0, err
	}
	pg := c.Pager(count, DefaultPageSize)
	tab := &subTable{Admin: c.Admin, Pager: pg}
	if count == 0 {
		return tab, 0, nil
	}
	f.Limit, f.Offset = pg.Size, min(pg.Offset(), store.MaxOffset)
	rows, err := store.ListSubmissions(ctx, s.pool, f)
	if err != nil {
		return nil, 0, err
	}
	if err := s.fillSubRows(c, tab, rows); err != nil {
		return nil, 0, err
	}
	return tab, count, nil
}

// fillSubRows turns rows of store.ListSubmissions into table rows, with
// what they hide (files, texts, details, evaluations) in a bounded
// number of queries.
func (s *Server) fillSubRows(c *Ctx, tab *subTable, rows []store.SubmissionRow) error {
	ctx := c.Context()
	var ids, resSubs, resDs, dsIDs []int32
	for _, r := range rows {
		ids = append(ids, r.Submission.ID)
		if r.DatasetID != nil {
			dsIDs = append(dsIDs, *r.DatasetID)
			if r.Result != nil {
				resSubs = append(resSubs, r.Submission.ID)
				resDs = append(resDs, *r.DatasetID)
			}
		}
	}
	files, err := s.st.Q.ListFilesBySubmissions(ctx, ids)
	if err != nil {
		return err
	}
	byFile := map[int32][]q.File{}
	for _, f := range files {
		byFile[f.SubmissionID] = append(byFile[f.SubmissionID], f)
	}
	texts := map[int32]q.AdminSubmissionRowTextsRow{}
	if len(resSubs) > 0 {
		tr, err := s.st.Q.AdminSubmissionRowTexts(ctx, q.AdminSubmissionRowTextsParams{SubmissionIds: resSubs, DatasetIds: resDs})
		if err != nil {
			return err
		}
		for _, t := range tr {
			texts[t.SubmissionID] = t
		}
	}
	scorings, err := s.datasetScorings(ctx, dsIDs)
	if err != nil {
		return err
	}
	// The evaluations of the rows that are "Scoring..." (rare: results
	// wait for their score for a moment).
	var evSubs, evDs []int32
	for _, r := range rows {
		if r.Status() == store.StatusScoring {
			evSubs = append(evSubs, r.Submission.ID)
			evDs = append(evDs, *r.DatasetID)
		}
	}
	evals := map[int32][]q.AdminEvaluationsOfRow{}
	if len(evSubs) > 0 {
		er, err := s.st.Q.AdminEvaluationsOf(ctx, q.AdminEvaluationsOfParams{SubmissionIds: evSubs, DatasetIds: evDs})
		if err != nil {
			return err
		}
		for _, e := range er {
			evals[e.SubmissionID] = append(evals[e.SubmissionID], e)
		}
	}
	tr := scoreLocale{c.Lang}
	for _, r := range rows {
		row := &subRow{
			ID: r.Submission.ID, Timestamp: r.Submission.Timestamp, UserID: r.UserID, Username: r.Username,
			TaskID: r.Submission.TaskID, TaskName: r.TaskName, Status: string(r.Status()),
			Token: r.TokenTimestamp != nil, Official: r.Submission.Official,
			ShortComment: shortComment(r.Submission.Comment),
		}
		row.ContestID = deref(r.ContestID)
		lang := deref(r.Submission.Language)
		for _, f := range byFile[r.Submission.ID] {
			row.Files = append(row.Files, subFile{ID: f.ID, Name: s.langs.LangFilename(lang, f.Filename)})
		}
		if r.DatasetID != nil {
			row.DatasetID = *r.DatasetID
		}
		ds := scorings[row.DatasetID]
		if ds != nil && ds.ST != nil {
			row.HasScoreType = true
			row.MaxScore = pyFloat(ds.ST.MaxScore())
		}
		if res := r.Result; res != nil {
			row.Evaluated = res.EvaluationOutcome != nil
			if res.Score != nil {
				row.Score = pyFloat(*res.Score)
			}
			if res.ScoredAt != nil {
				l := res.ScoredAt.Sub(r.Submission.Timestamp).Seconds()
				row.Latency = &l
			}
			t := texts[r.Submission.ID]
			row.CompilationText = t.CompilationText
			if row.Status == string(store.StatusScored) && row.HasScoreType {
				row.Details = scoretype.HTMLDetails(ds.ST, t.ScoreDetails, scoretype.FeedbackFull, tr)
			}
		}
		for _, e := range evals[r.Submission.ID] {
			row.Evaluations = append(row.Evaluations, evalRow{AdminEvaluationsOfRow: e, Visible: row.Token || e.Public})
		}
		tab.Rows = append(tab.Rows, row)
	}
	return nil
}

// utRow is a row of the user test tables (CMS
// fragments/user_test_row.html).
type utRow struct {
	ID        int32
	Timestamp time.Time
	ContestID int32
	UserID    int32
	Username  string
	TaskID    int32
	TaskName  string
	// Status is "compiling", "compilation_failed", "evaluating" or
	// "evaluated" (CMS UserTestResult.get_status).
	Status          string
	CompilationText []string
	Input           string
	Files           []subFile
}

// utTable is the data of fragments/user_test_rows.html.
type utTable struct {
	Pager *Pager
	Rows  []*utRow
	// Count is the number of user tests of the list.
	Count int64
}

// userTestStatus is CMS's UserTestResult.get_status.
func userTestStatus(hasResult bool, compilation, evaluation *string) string {
	switch {
	case !hasResult || compilation == nil:
		return "compiling"
	case *compilation != "ok":
		return "compilation_failed"
	case evaluation == nil:
		return "evaluating"
	}
	return "evaluated"
}

// userTestTable loads a page (?page=) of the user tests of a contest
// (optionally of a task or a participation), newest first.
func (s *Server) userTestTable(c *Ctx, contestID int32, taskID, participationID *int32) (*utTable, error) {
	ctx := c.Context()
	count, err := s.st.Q.AdminCountUserTests(ctx, q.AdminCountUserTestsParams{ContestID: contestID, TaskID: taskID, ParticipationID: participationID})
	if err != nil {
		return nil, err
	}
	pg := c.Pager(count, DefaultPageSize)
	tab := &utTable{Pager: pg, Count: count}
	if count == 0 {
		return tab, nil
	}
	rows, err := s.st.Q.AdminUserTests(ctx, q.AdminUserTestsParams{ContestID: contestID, TaskID: taskID,
		ParticipationID: participationID, PageSize: int32(pg.Size), PageOffset: int32(min(pg.Offset(), store.MaxOffset))})
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		row := &utRow{ID: r.UserTest.ID, Timestamp: r.UserTest.Timestamp, ContestID: r.ContestID, UserID: r.UserID,
			Username: r.Username, TaskID: r.UserTest.TaskID, TaskName: r.TaskName, Input: r.UserTest.Input,
			Status:          userTestStatus(r.HasResult, r.CompilationOutcome, r.EvaluationOutcome),
			CompilationText: r.CompilationText}
		lang := deref(r.UserTest.Language)
		for i, id := range r.FileIds {
			if i < len(r.FileNames) {
				row.Files = append(row.Files, subFile{ID: id, Name: s.langs.LangFilename(lang, r.FileNames[i])})
			}
		}
		tab.Rows = append(tab.Rows, row)
	}
	return tab, nil
}
