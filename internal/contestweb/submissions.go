// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/server/contest/handlers/tasksubmission.py and
// cms/server/contest/submission/workflow.py of CMS:
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

// Submissions and tokens (CMS handlers/tasksubmission.py): the submit
// form, the submissions of a task, the status JSON polled by the page,
// the details, the files and the tokens.

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/contestweb/submission"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/scoretype"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/tasktype"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/web"
)

// SubmissionsPageSize bounds the submissions the submissions page of a
// task lists, newest first (D24; CMS lists all of them): a contestant
// with more sees the latest ones.
const SubmissionsPageSize = 500

// formMaxMemory is the part of a multipart body kept in memory; the rest
// goes to temporary files (the body itself is capped by MaxBody).
const formMaxMemory = 8 << 20

// SubmitOptions are the choices of a submission besides its files (CMS
// accept_submission's arguments).
type SubmitOptions struct {
	// Official: the submission counts for the score (the participant
	// competes; false in analysis mode or after their time).
	Official bool
	// OverrideMaxNumber and OverrideMinInterval skip the limits on the
	// number of submissions and on the time between them (an
	// administrator impersonating the participant through the API).
	OverrideMaxNumber, OverrideMinInterval bool
}

// AcceptSubmission receives a submission of the logged-in participant on
// a task from the files of the request (multipart fields named after the
// submission format, or one archive in "submission") and its "language"
// argument (CMS accept_submission): it applies the limits, matches and
// stores the files, inserts the submission and its files in a transaction
// that checks the limits again under a lock on the participation and
// refreshes the task score cache, then notifies the dispatcher and the
// live pages. A refusal is a *submission.Unacceptable (the pages notify
// it, the API returns it); other errors are failures (500), except an
// oversized or malformed body, an errStatus (413, 400).
func (s *Server) AcceptSubmission(c *Ctx, t *Task, o SubmitOptions) (*q.Submission, error) {
	ctx := c.Context()
	p := c.Participation
	if err := web.ParseForm(c.W, c.R, MaxBody, formMaxMemory); err != nil {
		return nil, errStatus(web.FormStatus(err))
	}
	tt, err := s.taskType(t)
	if err != nil {
		return nil, err
	}
	if tt == nil {
		return nil, &submission.Unacceptable{Subject: submission.MsgInvalidSubmissionFormat,
			Text: submission.MsgSelectCorrectFiles, Err: errors.New("the task has no active dataset")}
	}
	rules := s.submissionRules(c, t)
	check := func(qq *q.Queries) error {
		st, err := submissionStats(ctx, qq, c.Contest.ID, p.ID, t.ID)
		if err != nil {
			return err
		}
		if u := submission.CheckSubmission(rules, st, c.Now, o.OverrideMaxNumber, o.OverrideMinInterval); u != nil {
			return u
		}
		return nil
	}
	if err := check(s.st.Q); err != nil {
		return nil, err
	}
	req := s.submitRequest(c, t, tt)
	req.Previous = func(ctx context.Context) (*submission.Previous, error) {
		return s.previousSubmission(ctx, p.ID, t.ID)
	}
	acc, err := submission.AcceptSubmission(ctx, req)
	if err != nil {
		return nil, err
	}
	var sub q.Submission
	var changes []judging.TaskScoreChange
	err = s.st.RunTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		qq := s.st.Q.WithTx(tx)
		if _, err := qq.CWSLockParticipation(ctx, p.ID); err != nil {
			return err
		}
		if err := check(qq); err != nil {
			return err
		}
		var err error
		sub, err = store.CreateSubmission(ctx, qq, q.CreateSubmissionParams{
			ParticipationID: p.ID, TaskID: t.ID, Timestamp: c.Now,
			Language: acc.LanguageName(), Comment: acc.Comment, Official: o.Official,
		})
		if err != nil {
			return err
		}
		names, digests := sortedFiles(acc.Files)
		if _, err := qq.CreateFiles(ctx, q.CreateFilesParams{SubmissionID: sub.ID, Filenames: names, Digests: digests}); err != nil {
			return err
		}
		changes = nil
		if o.Official {
			changes, err = judging.RefreshTaskScores(ctx, tx, []judging.TaskKey{{ParticipationID: p.ID, TaskID: t.ID}})
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	s.log.Info("submission accepted", "contest", c.Contest.Name, "username", p.User.Username,
		"task", t.Name, "submission", sub.ID, "official", o.Official)
	if err := judging.Notify(ctx, s.kv, judging.Event{Kind: judging.EventSubmission,
		ContestID: c.Contest.ID, SubmissionIDs: []int32{sub.ID}}); err != nil {
		// The sweep of the dispatcher finds it anyway.
		s.log.Warn("notifying the dispatcher of a submission", "submission", sub.ID, "err", err)
	}
	ev := live.Submission{Contest: int64(c.Contest.ID), Participation: int64(p.ID), Task: int64(t.ID),
		ID: int64(sub.ID), Opaque: sub.OpaqueID, Status: live.SubmissionCompiling, New: true, ActiveDataset: true}
	if t.ActiveDatasetID != nil {
		ev.Dataset = int64(*t.ActiveDatasetID)
	}
	events := []live.Event{ev.Event()}
	events = append(events, taskScoreEvents(changes)...)
	s.pub.Publish(events...)
	return &sub, nil
}

// taskScoreEvents are the live events of cached task scores that
// changed.
func taskScoreEvents(changes []judging.TaskScoreChange) []live.Event {
	var out []live.Event
	for _, ch := range changes {
		out = append(out, live.TaskScore{Contest: int64(ch.ContestID), Participation: int64(ch.ParticipationID),
			Task: int64(ch.TaskID)}.Event())
	}
	return out
}

// taskType returns the task type of the active dataset of a task, nil
// without one.
func (s *Server) taskType(t *Task) (tasktype.TaskType, error) {
	if !t.HasDataset {
		return nil, nil
	}
	tt, err := tasktype.Get(t.TaskType, t.TaskTypeParameters)
	if err != nil {
		return nil, fmt.Errorf("task %s: %w", t.Name, err)
	}
	return tt, nil
}

// submissionRules are the limits on the submissions of the participant
// on a task.
func (s *Server) submissionRules(c *Ctx, t *Task) submission.Rules {
	p := c.Participation
	r := submission.Rules{
		ContestMaxNumber:   c.Contest.MaxSubmissionNumber,
		TaskMaxNumber:      t.MaxSubmissionNumber,
		ContestMinInterval: c.Contest.MinSubmissionInterval,
		TaskMinInterval:    t.MinSubmissionInterval,
		Unrestricted:       p.Unrestricted,
	}
	if end, ok := submission.EndTime(p.Group.Stop, p.Group.PerUserTime, p.StartingTime, p.DelayTime, p.ExtraTime); ok {
		r.LastMinutes = submission.IsLastMinutes(c.Now, end, c.Contest.MinSubmissionIntervalGracePeriod, p.Unrestricted)
	}
	return r
}

// submissionStats reads how many submissions the participant sent and
// when the last ones were (one query).
func submissionStats(ctx context.Context, qq *q.Queries, contest, participation, task int32) (submission.Stats, error) {
	row, err := qq.GetSubmissionStats(ctx, q.GetSubmissionStatsParams{TaskID: task, ParticipationID: participation, ContestID: contest})
	if err != nil {
		return submission.Stats{}, err
	}
	return submission.Stats{TaskCount: row.TaskCount, ContestCount: row.ContestCount,
		TaskLast: row.TaskLast, ContestLast: row.ContestLast}, nil
}

// submitRequest describes the files of the request for the workflow.
func (s *Server) submitRequest(c *Ctx, t *Task, tt tasktype.TaskType) *submission.Request {
	req := &submission.Request{
		SubmissionFormat:    t.SubmissionFormat,
		AllowedLanguages:    t.AllowedLanguages(c.Contest),
		TaskType:            tt,
		MaxSubmissionLength: s.cfg.ContestWeb.MaxSubmissionLength,
		MaxInputLength:      s.cfg.ContestWeb.MaxInputLength,
		Languages:           s.langs,
		Store:               s.blobs,
	}
	if c.R.MultipartForm != nil {
		req.Form = c.R.MultipartForm.File
	}
	if l, ok := c.Arg("language"); ok {
		req.Language = &l
	}
	return req
}

// previousSubmission reads the latest submission of the participant on
// the task with its files (nil if none).
func (s *Server) previousSubmission(ctx context.Context, participation, task int32) (*submission.Previous, error) {
	rows, err := s.st.Q.CWSLatestSubmissionFiles(ctx, q.CWSLatestSubmissionFilesParams{ParticipationID: participation, TaskID: task})
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	prev := &submission.Previous{Language: rows[0].Language, Files: map[string]string{}}
	for _, r := range rows {
		if r.Filename != nil && r.Digest != nil {
			prev.Files[*r.Filename] = *r.Digest
		}
	}
	return prev, nil
}

// sortedFiles returns the names and digests of a set of files, by name.
func sortedFiles(files map[string]string) ([]string, []string) {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	slices.Sort(names)
	digests := make([]string, len(names))
	for i, n := range names {
		digests[i] = files[n]
	}
	return names, digests
}

// notifyUnacceptable logs a refused submission or user test and notifies
// the participant (CMS notify_error with the exception's texts).
func (c *Ctx) notifyUnacceptable(u *submission.Unacceptable) {
	c.S.log.Info("sent error", "subject", u.Subject, "text", u.FormattedText(), "cause", u.Err)
	if len(u.Params) == 0 {
		c.NotifyError(u.Subject, u.Text, nil)
	} else {
		c.NotifyError(u.Subject, u.Text, u.Params)
	}
}

// submitPost is POST tasks/{task}/submit (CMS SubmitHandler).
func (s *Server) submitPost(c *Ctx) {
	// Unofficial submissions outside the official window and the analysis
	// mode only if the contest allows them.
	if a := c.Actual.Phase; 0 < a && a < 3 && !c.Contest.AllowUnofficialSubmissionBeforeAnalysisMode {
		c.Redirect(c.URL.String())
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
	var args []string
	sub, err := s.AcceptSubmission(c, t, SubmitOptions{Official: c.Actual.Phase == PhaseCompeting})
	var u *submission.Unacceptable
	var st errStatus
	switch {
	case errors.As(err, &u):
		c.notifyUnacceptable(u)
	case errors.As(err, &st):
		c.Error(int(st))
		return
	case err != nil:
		c.Fail(err)
		return
	default:
		c.NotifySuccess(submission.MsgSubmissionReceived, submission.MsgSubmissionReceivedText, nil)
		// Not used by the pages (nor does it disclose anything): automatic
		// tests read the submission from it, as in CMS.
		args = []string{"submission_id", encryptNumber(int64(sub.ID), s.cfg.DeriveKey("contest-web/submission-id"))}
	}
	c.Redirect(c.URL.JoinQuery([]any{"tasks", t.Name, "submissions"}, args...))
}

// submissionRow is a row of the submissions table (CMS
// macro/submission.html row). The rows of a page are many: what the
// template would compute with functions (slow in html/template) comes
// computed, and the template reads fields.
type submissionRow struct {
	Opaque    int64
	Timestamp time.Time
	// TimeText is the contest time of the submission (R5.9), TimeTitle
	// the time CMS shows there (its title).
	TimeText, TimeTitle string
	Official            bool
	Status              int
	// StatusText is the translated status; Final: compilation failed or
	// scored (the details link; otherwise the loading image).
	StatusText template.HTML
	Final      bool
	// PublicScore and Score are the cells of the scores: Shown is false
	// for "N/A".
	PublicScore, Score scoreCell
	Tokened            bool
	// TokenURL is the form that plays a token on it.
	TokenURL string
	// Files are the download links (empty: no files); OneFile is the
	// only one, if there is exactly one.
	Files   []fileLink
	OneFile *fileLink
}

// submissionTable is a table of submissions (CMS macro rows): the
// official or the unofficial ones.
type submissionTable struct {
	// Name is "official" or "unofficial" (the id of the live region).
	Name string
	Rows []*submissionRow
	D    *taskSubmissionsData
}

// scoreCell is a score in a table: its text and CSS class.
type scoreCell struct {
	Shown bool
	Class string
	Text  string
}

// fileLink is a file of a submission or user test to download.
type fileLink struct {
	Name string
	URL  string
}

// taskSubmissionsData is the data of task_submissions.html.
type taskSubmissionsData struct {
	*Page
	// HasScoreType: the active dataset has a score type (CMS "score_type
	// is defined"); MaxScore and MaxPublicScore are its maxima.
	HasScoreType             bool
	MaxScore, MaxPublicScore float64
	// PublicScore and TokenedScore are the task scores of the boxes.
	PublicScore, TokenedScore scoreCell
	IsScorePartial            bool
	// TokensTask is the token mode of the task; the Can… flags are the
	// ones the template of CMS computes.
	TokensTask            string
	CanUseTokensInContest bool
	CanUseTokens          bool
	CanPlayToken          bool
	CanPlayTokenNow       bool
	TokensInfo            TokensInfo
	// WaitAfterGen: the next token is generated before the participant
	// may play one (CMS tokens_info[2] > tokens_info[1]).
	WaitAfterGen bool
	// SubmissionsLeft is nil without limits.
	SubmissionsLeft *int64
	DownloadAllowed bool
	// CanSubmit: the submit form is shown.
	CanSubmit    bool
	AllowPartial bool
	// LanguageFormat: the submission format has ".%l" elements (the
	// language selector); ZipForm: the submission.zip form is shown.
	LanguageFormat bool
	ZipForm        bool
	// TaskLanguages are the languages of the task (TASK_LANGUAGES) and
	// LanguageNames the options of the selector.
	TaskLanguages []taskLanguage
	LanguageNames []string
	// Official and Unofficial are the tables of the submissions, newest
	// first; HasUnofficial: some is unofficial.
	Official, Unofficial submissionTable
	HasUnofficial        bool
	ShowDate             bool
	// TwoScores: the tables have a public and a total score column (CMS
	// 0 < max_public_score < max_score); ShowPublic and ShowTotal: the
	// rows have a public and a total score cell; NumCols is the number of
	// columns of the tables; TokenColumn: they have a token column;
	// WaitToken: its buttons say "Wait..." when no token can be played
	// now (CMS can_play_token or tokens_info[1] is not none).
	TwoScores, ShowPublic, ShowTotal bool
	NumCols                          int
	TokenColumn, WaitToken           bool
	// SR are the status numbers (CMS SubmissionResult.COMPILING…).
	SR statusNumbers
}

// taskLanguage is a language of a task in the pages' JavaScript.
type taskLanguage struct {
	Name       string
	Extensions []string
}

// taskSubmissionsPage is GET tasks/{task}/submissions (CMS
// TaskSubmissionsHandler).
func (s *Server) taskSubmissionsPage(c *Ctx) {
	t, err := c.Task(c.R.PathValue("task"))
	if err != nil {
		c.Fail(err)
		return
	}
	if t == nil {
		c.Error(http.StatusNotFound)
		return
	}
	d, err := s.taskSubmissionsData(c, t)
	if err != nil {
		c.Fail(err)
		return
	}
	c.Render(http.StatusOK, "task_submissions.html", d)
}

func (s *Server) taskSubmissionsData(c *Ctx, t *Task) (*taskSubmissionsData, error) {
	ctx := c.Context()
	p := c.Participation
	page, err := c.Page("task_submissions")
	if err != nil {
		return nil, err
	}
	page.Task = t
	d := &taskSubmissionsData{Page: page, TokensTask: t.TokenMode,
		DownloadAllowed: c.Contest.SubmissionsDownloadAllowed, SR: statuses}

	st, err := c.ScoreType(t)
	if err != nil {
		// CMS's safe_get_score_type: the page shows no scores.
		s.log.Warn("score type of a task", "task", t.Name, "err", err)
		st = nil
	}
	rows, err := s.st.Q.CWSTaskSubmissions(ctx, q.CWSTaskSubmissionsParams{DatasetID: t.ActiveDatasetID,
		ParticipationID: p.ID, TaskID: t.ID, PageSize: SubmissionsPageSize})
	if err != nil {
		return nil, err
	}
	scores, err := c.TaskScores()
	if err != nil {
		return nil, err
	}
	ts := scores[t.ID]
	tr := c.ScoreLocale()

	d.CanUseTokensInContest = page.TokensContest != TokenModeDisabled && page.TokensTasks != TokenModeDisabled
	d.CanUseTokens = page.TokensContest != TokenModeDisabled && t.TokenMode != TokenModeDisabled
	if d.CanUseTokens && c.Actual.Phase == PhaseCompeting {
		played, err := s.st.Q.CWSParticipationTokens(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		d.TokensInfo = TokensAvailable(mustTokenRules(c.Contest), mustTokenRules(t), t.ID, tokenStart(p),
			tokensPlayed(played), c.Now)
		d.CanPlayToken = d.TokensInfo.CanPlay()
		d.CanPlayTokenNow = d.CanPlayToken && d.TokensInfo.Expiration == nil
		if e, g := d.TokensInfo.Expiration, d.TokensInfo.NextGen; e != nil && g != nil {
			d.WaitAfterGen = e.After(*g)
		}
	}

	if st != nil {
		d.HasScoreType = true
		d.MaxScore, d.MaxPublicScore = st.MaxScore(), st.MaxPublicScore()
		d.PublicScore = scoreCell{Shown: true, Class: GetScoreClass(ts.PublicScore, d.MaxPublicScore),
			Text: scoretype.FormatScore(ts.PublicScore, d.MaxPublicScore, tr)}
		d.TokenedScore = scoreCell{Shown: d.CanUseTokens, Class: "undefined"}
		if d.CanUseTokens {
			d.TokenedScore.Class = GetScoreClass(ts.TokenedScore, d.MaxScore)
			d.TokenedScore.Text = scoretype.FormatScore(ts.TokenedScore, d.MaxScore, tr)
		}
		d.IsScorePartial = ts.Partial
	}

	// How many submissions are left (never negative).
	if c.Contest.MaxSubmissionNumber != nil || t.MaxSubmissionNumber != nil {
		stats, err := submissionStats(ctx, s.st.Q, c.Contest.ID, p.ID, t.ID)
		if err != nil {
			return nil, err
		}
		var left *int64
		if c.Contest.MaxSubmissionNumber != nil {
			l := int64(*c.Contest.MaxSubmissionNumber) - stats.ContestCount
			left = &l
		}
		if t.MaxSubmissionNumber != nil {
			l := int64(*t.MaxSubmissionNumber) - stats.TaskCount
			if left == nil || *left > l {
				left = &l
			}
		}
		*left = max(0, *left)
		d.SubmissionsLeft = left
	}

	a := c.Actual.Phase
	d.CanSubmit = a == PhaseCompeting || a == PhaseAnalysis || p.Unrestricted ||
		(0 <= a && a <= 3 && c.Contest.AllowUnofficialSubmissionBeforeAnalysisMode)
	if tt, err := s.taskType(t); err == nil && tt != nil {
		d.AllowPartial = tt.AllowPartialSubmission()
	}
	d.LanguageFormat = hasLanguageElement(t.SubmissionFormat)
	d.ZipForm = len(t.SubmissionFormat) > 1 && !d.LanguageFormat
	d.LanguageNames = t.AllowedLanguages(c.Contest)
	d.TaskLanguages = s.taskLanguages(d.LanguageNames)

	d.Official = submissionTable{Name: "official", D: d}
	d.Unofficial = submissionTable{Name: "unofficial", D: d}
	allToday := true
	for i := range rows {
		r := &rows[i]
		row := &submissionRow{Opaque: r.OpaqueID, Timestamp: r.Timestamp, Official: r.Official, Tokened: r.Tokened,
			Status:   cmsStatus(store.StatusOf(r.CompilationOutcome, r.EvaluationOutcome, r.Scored)),
			TimeText: c.contestTimeText(r.Timestamp)}
		row.StatusText = template.HTML(c.T(submissionStatusText[row.Status]))
		row.Final = row.Status == statusCompilationFailed || row.Status == statusScored
		if r.Official {
			d.Official.Rows = append(d.Official.Rows, row)
		} else {
			d.HasUnofficial = true
			d.Unofficial.Rows = append(d.Unofficial.Rows, row)
		}
		if !sameDay(r.Timestamp, c.Now, c.RC.TZ) {
			allToday = false
		}
		if st != nil && row.Status == statusScored {
			ps, sc := deref(r.PublicScore), deref(r.Score)
			row.PublicScore = scoreCell{Shown: true, Class: GetScoreClass(ps, d.MaxPublicScore),
				Text: scoretype.FormatScore(ps, d.MaxPublicScore, tr)}
			if r.Tokened || a == PhaseAnalysis {
				row.Score = scoreCell{Shown: true, Class: GetScoreClass(sc, d.MaxScore),
					Text: scoretype.FormatScore(sc, d.MaxScore, tr)}
			}
		}
		row.Files = s.submissionFileLinks(c, t, r.OpaqueID, r.Language, r.Files)
		if len(row.Files) == 1 {
			row.OneFile = &row.Files[0]
		}
		row.TokenURL = c.URL.Join("tasks", t.Name, "submissions", r.OpaqueID, "token")
	}
	d.ShowDate = !allToday
	for _, tb := range []*submissionTable{&d.Official, &d.Unofficial} {
		for _, row := range tb.Rows {
			row.TimeTitle = c.timeTitle(row.Timestamp, d.ShowDate)
		}
	}
	d.NumCols = 2
	if st != nil {
		d.TwoScores = 0 < d.MaxPublicScore && d.MaxPublicScore < d.MaxScore
		d.ShowPublic = d.MaxPublicScore > 0
		d.ShowTotal = d.MaxPublicScore < d.MaxScore
		d.NumCols++
		if d.TwoScores {
			d.NumCols++
		}
	}
	if d.DownloadAllowed {
		d.NumCols++
	}
	if d.CanUseTokens && a == PhaseCompeting {
		d.TokenColumn = true
		d.WaitToken = d.CanPlayToken || d.TokensInfo.NextGen != nil
		d.NumCols++
	}
	return d, nil
}

// contestTimeText is what the tables show for a time (the template
// function format_contest_time): the contest time of the participant
// (R5.9), else CMS's format_datetime_smart.
func (c *Ctx) contestTimeText(t time.Time) string {
	if d, ok := c.ContestTime(t); ok {
		return FormatContestTime(d)
	}
	return scoreLocale{c.Lang}.locale().FormatDatetimeSmart(t, c.Now, c.tz())
}

// timeTitle is the time CMS shows in the tables (format_datetime, or
// format_time when every row is of today), the title of the contest
// time.
func (c *Ctx) timeTitle(t time.Time, date bool) string {
	l := scoreLocale{c.Lang}.locale()
	if date {
		return l.FormatDatetime(t, c.tz())
	}
	return l.FormatTime(t, c.tz())
}

// tz is the time zone of the pages.
func (c *Ctx) tz() *time.Location {
	if c.RC != nil && c.RC.TZ != nil {
		return c.RC.TZ
	}
	return time.UTC
}

// mustTokenRules returns the token rules of a contest or a task.
func mustTokenRules(v any) TokenRules {
	r, _ := ExtractTokenParams(v)
	return r
}

func tokensPlayed(rows []q.CWSParticipationTokensRow) []TokenPlayed {
	out := make([]TokenPlayed, len(rows))
	for i, r := range rows {
		out[i] = TokenPlayed{Timestamp: r.Timestamp, TaskID: r.TaskID}
	}
	return out
}

// sameDay is Jinja's "today" test of CMS: t falls on the day of now in
// the time zone.
func sameDay(t, now time.Time, loc *time.Location) bool {
	if loc == nil {
		loc = time.UTC
	}
	y1, m1, d1 := t.In(loc).Date()
	y2, m2, d2 := now.In(loc).Date()
	return y1 == y2 && m1 == m2 && d1 == d2
}

// hasLanguageElement reports whether a submission format has an element
// ending in ".%l" (CMS submission_format|any("endswith", ".%l")).
func hasLanguageElement(format []string) bool {
	for _, f := range format {
		if len(f) >= 3 && f[len(f)-3:] == ".%l" {
			return true
		}
	}
	return false
}

// taskLanguages returns the languages of the task that exist, for the
// TASK_LANGUAGES object of the pages.
func (s *Server) taskLanguages(names []string) []taskLanguage {
	var out []taskLanguage
	for _, n := range names {
		if l, ok := s.langs.Lookup(n); ok {
			out = append(out, taskLanguage{Name: n, Extensions: l.SourceExtensions})
		}
	}
	return out
}

// submissionFileLinks are the download links of the files of a
// submission (CMS macro/submission.html): ".%l" becomes the language's
// extension, unless another file already has that name (then the stored
// name is used, except with a single file).
func (s *Server) submissionFileLinks(c *Ctx, t *Task, opaque int64, lang *string, files []string) []fileLink {
	ext := ""
	if lang != nil {
		if l, ok := s.langs.Lookup(*lang); ok {
			ext = l.SourceExtension()
		}
	}
	out := make([]fileLink, 0, len(files))
	for _, f := range files {
		name := f
		if lang != nil {
			repl := strings.ReplaceAll(f, ".%l", ext)
			if len(files) == 1 || !slices.Contains(files, repl) {
				name = repl
			}
		}
		out = append(out, fileLink{Name: name,
			URL: c.URL.Join("tasks", t.Name, "submissions", opaque, "files", name)})
	}
	return out
}

// submissionView is a submission of the participant with its result on
// the active dataset of its task.
type submissionView struct {
	row q.CWSSubmissionRow
	// Status is the status of the result (compiling without one).
	Status int
}

// scored reports whether every score column is set (CMS
// SubmissionResult.scored()).
func (v *submissionView) scored() bool {
	r := &v.row
	return r.Score != nil && r.ScoreDetails != nil && r.PublicScore != nil && r.PublicScoreDetails != nil && r.HasRankingDetails
}

// loadSubmission returns the submission of the participant on the task
// with the opaque id of the path (CMS get_submission), nil if there is
// none.
func (s *Server) loadSubmission(c *Ctx, t *Task) (*submissionView, error) {
	opaque, ok := c.PathID("id")
	if !ok {
		return nil, nil
	}
	row, err := s.st.Q.CWSSubmission(c.Context(), q.CWSSubmissionParams{DatasetID: t.ActiveDatasetID,
		ParticipationID: c.Participation.ID, TaskID: t.ID, OpaqueID: opaque})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	v := &submissionView{row: row}
	v.Status = statusCompiling
	if row.HasResult {
		v.Status = cmsStatus(store.StatusOf(row.CompilationOutcome, row.EvaluationOutcome, v.scored()))
	}
	return v, nil
}

// SubmissionScoreDetails returns the score details of a submission of the
// logged-in participant on a task (by its opaque id) as CMS's API shows
// them (ApiSubmissionDetailsHandler and ApiSubmissionFullDetailsHandler):
// nil while the submission is not scored; with full, the private
// details unfiltered; otherwise the private details if a token was
// played or in analysis mode, else the public ones, filtered with the
// task's feedback level (full in analysis mode). found is false if the
// participant has no such submission.
func (s *Server) SubmissionScoreDetails(c *Ctx, t *Task, opaque int64, full bool) (details *scoretype.Details, found bool, err error) {
	row, err := s.st.Q.CWSSubmission(c.Context(), q.CWSSubmissionParams{DatasetID: t.ActiveDatasetID,
		ParticipationID: c.Participation.ID, TaskID: t.ID, OpaqueID: opaque})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	v := &submissionView{row: row}
	if !row.HasResult || !v.scored() {
		return nil, true, nil
	}
	st, err := c.ScoreType(t)
	if err != nil || st == nil {
		return nil, true, err
	}
	raw, level := v.detailsFor(c, t)
	if full {
		raw, level = row.ScoreDetails, scoretype.FeedbackFull
	}
	d, err := st.JSONDetails(raw, level)
	return d, true, err
}

// detailsFor returns the details the participant sees and the feedback
// level (CMS SubmissionDetailsHandler): the private details once a token
// is played or in analysis mode (with full feedback), the public ones
// otherwise.
func (v *submissionView) detailsFor(c *Ctx, t *Task) ([]byte, scoretype.FeedbackLevel) {
	analysis := c.Actual.Phase == PhaseAnalysis
	raw := v.row.PublicScoreDetails
	if v.row.Tokened || analysis {
		raw = v.row.ScoreDetails
	}
	level := scoretype.FeedbackLevel(t.FeedbackLevel)
	if analysis {
		level = scoretype.FeedbackFull
	}
	return raw, level
}

// submissionStatusPage is GET tasks/{task}/submissions/{id} (CMS
// SubmissionStatusHandler): the status of a submission as JSON, with the
// scores once it is final, for the polling of the submissions page.
func (s *Server) submissionStatusPage(c *Ctx) {
	t, err := c.Task(c.R.PathValue("task"))
	if err != nil {
		c.Fail(err)
		return
	}
	if t == nil {
		c.Error(http.StatusNotFound)
		return
	}
	v, err := s.loadSubmission(c, t)
	if err != nil {
		c.Fail(err)
		return
	}
	if v == nil {
		c.Error(http.StatusNotFound)
		return
	}
	data := map[string]any{"status": v.Status, "status_text": c.T(submissionStatusText[v.Status])}
	if v.Status == statusCompilationFailed || v.Status == statusScored {
		st, err := c.ScoreType(t)
		if err != nil {
			c.Fail(err)
			return
		}
		scores, err := c.TaskScores()
		if err != nil {
			c.Fail(err)
			return
		}
		if st != nil {
			tr := c.ScoreLocale()
			ts := scores[t.ID]
			data["task_public_score"] = ts.PublicScore
			data["task_tokened_score"] = ts.TokenedScore
			data["task_score_is_partial"] = ts.Partial
			data["task_public_score_message"] = scoretype.FormatScore(ts.PublicScore, st.MaxPublicScore(), tr)
			data["task_tokened_score_message"] = scoretype.FormatScore(ts.TokenedScore, st.MaxScore(), tr)
			if st.MaxPublicScore() > 0 {
				data["max_public_score"] = st.MaxPublicScore()
				if v.Status == statusScored {
					data["public_score"] = deref(v.row.PublicScore)
					data["public_score_message"] = scoretype.FormatScore(deref(v.row.PublicScore), st.MaxPublicScore(), tr)
				}
			}
			if st.MaxPublicScore() < st.MaxScore() {
				data["max_score"] = st.MaxScore()
				if v.Status == statusScored && (v.row.Tokened || c.Actual.Phase == PhaseAnalysis) {
					data["score"] = deref(v.row.Score)
					data["score_message"] = scoretype.FormatScore(deref(v.row.Score), st.MaxScore(), tr)
				}
			}
		}
	}
	c.JSON(http.StatusOK, data)
}

// submissionDetailsData is the data of submission_details.html (a
// fragment loaded into the dialog of the submissions page).
type submissionDetailsData struct {
	// Details is the HTML of the score details (nil: none yet).
	Details *scoreDetailsHTML
	// Result is the compilation output, nil if not compiled.
	Result *compilationOutput
}

// scoreDetailsHTML is the HTML of score details (CMS
// get_html_details, rendered with |safe).
type scoreDetailsHTML struct{ HTML any }

// compilationOutput is the compilation part of a result (CMS
// sr.compiled() and its columns).
type compilationOutput struct {
	Text           []string
	Time           *float64
	Memory         *int64
	Stdout, Stderr *string
}

// submissionDetailsPage is GET tasks/{task}/submissions/{id}/details (CMS
// SubmissionDetailsHandler): the score details and the compilation
// output, loaded into the details dialog.
func (s *Server) submissionDetailsPage(c *Ctx) {
	t, err := c.Task(c.R.PathValue("task"))
	if err != nil {
		c.Fail(err)
		return
	}
	if t == nil {
		c.Error(http.StatusNotFound)
		return
	}
	v, err := s.loadSubmission(c, t)
	if err != nil {
		c.Fail(err)
		return
	}
	if v == nil {
		c.Error(http.StatusNotFound)
		return
	}
	var d submissionDetailsData
	if v.row.HasResult && v.scored() {
		st, err := c.ScoreType(t)
		if err != nil {
			c.Fail(err)
			return
		}
		if st != nil {
			raw, level := v.detailsFor(c, t)
			d.Details = &scoreDetailsHTML{HTML: scoretype.HTMLDetails(st, raw, level, c.ScoreLocale())}
		}
	}
	if v.row.HasResult && v.row.CompilationOutcome != nil {
		r := &v.row
		d.Result = &compilationOutput{Text: r.CompilationText, Time: r.CompilationTime, Memory: r.CompilationMemory,
			Stdout: r.CompilationStdout, Stderr: r.CompilationStderr}
	}
	c.Render(http.StatusOK, "submission_details.html", d)
}

// submissionFilePage is GET tasks/{task}/submissions/{id}/files/{filename}
// (CMS SubmissionFileHandler): a file of a submission of the participant,
// if the contest allows downloading them. The name in the URL has the
// language's extension instead of ".%l".
func (s *Server) submissionFilePage(c *Ctx) {
	if !c.Contest.SubmissionsDownloadAllowed {
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
	opaque, _ := c.PathID("id")
	rows, err := s.st.Q.CWSSubmissionFiles(c.Context(), q.CWSSubmissionFilesParams{
		ParticipationID: c.Participation.ID, TaskID: t.ID, OpaqueID: opaque})
	if err != nil {
		c.Fail(err)
		return
	}
	if len(rows) == 0 {
		c.Error(http.StatusNotFound)
		return
	}
	filename := c.R.PathValue("filename")
	stored := filename
	if lang := rows[0].Language; lang != nil {
		if l, ok := s.langs.Lookup(*lang); ok {
			stored = replaceExtension(filename, l.SourceExtension())
		}
	}
	for _, r := range rows {
		if r.Filename != nil && *r.Filename == stored && r.Digest != nil {
			s.serveFile(c, *r.Digest, filename, "", "")
			return
		}
	}
	c.Error(http.StatusNotFound)
}

// charsetTypes are the non-text types werkzeug sends with a charset
// (werkzeug.utils.get_content_type).
var charsetTypes = map[string]bool{
	"application/ecmascript":                 true,
	"application/javascript":                 true,
	"application/sql":                        true,
	"application/xml":                        true,
	"application/xml-dtd":                    true,
	"application/xml-external-parsed-entity": true,
}

// withCharset adds "; charset=utf-8" to a content type as werkzeug does
// for the downloads of CMS: text types, some application types and the
// +xml ones.
func withCharset(ct string) string {
	if strings.Contains(ct, ";") {
		return ct
	}
	if strings.HasPrefix(ct, "text/") || charsetTypes[ct] || strings.HasSuffix(ct, "+xml") {
		return ct + "; charset=utf-8"
	}
	return ct
}

// replaceExtension turns a downloaded file name back into the stored one
// (CMS re.sub(r'%s$' % extension, '.%l', filename): the extension is a
// regular expression there, so its dot matches any character but a
// newline). Extensions are letters and dots, compared from the end
// without compiling anything; one with other regular expression syntax
// is compiled as CMS does.
func replaceExtension(filename, ext string) string {
	if strings.ContainsAny(ext, `\^$*+?()[]{}|`) {
		re, err := regexp.Compile("(?:" + ext + ")$")
		if err != nil {
			return filename
		}
		return re.ReplaceAllLiteralString(filename, ".%l")
	}
	end := len(filename)
	for i := len(ext) - 1; i >= 0; i-- {
		if end == 0 {
			return filename
		}
		if ext[i] == '.' {
			r, n := utf8.DecodeLastRuneInString(filename[:end])
			if r == '\n' {
				return filename
			}
			end -= n
			continue
		}
		if filename[end-1] != ext[i] {
			return filename
		}
		end--
	}
	return filename[:end] + ".%l"
}

// serveFile sends a file of the blob store (CMS FileHandler.fetch): the
// content type from the name (application/octet-stream if unknown)
// unless given, with the charset werkzeug adds to text types.
func (s *Server) serveFile(c *Ctx, digest, filename, contentType, disposition string) {
	if contentType == "" {
		if contentType = web.MimeType(filename); contentType == "" {
			contentType = "application/octet-stream"
		}
	}
	contentType = withCharset(contentType)
	err := web.ServeBlob(c.W, c.R, s.blobs, web.Download{Digest: digest, Filename: filename,
		ContentType: contentType, Disposition: disposition})
	if err != nil {
		s.log.Error("serving a file", "digest", digest, "err", err)
	}
}

// useTokenPost is POST tasks/{task}/submissions/{id}/token (CMS
// UseTokenHandler): plays a token on a submission. The tokens available
// are checked again in the transaction that inserts the token, under a
// lock on the participation.
func (s *Server) useTokenPost(c *Ctx) {
	ctx := c.Context()
	t, err := c.Task(c.R.PathValue("task"))
	if err != nil {
		c.Fail(err)
		return
	}
	if t == nil {
		c.Error(http.StatusNotFound)
		return
	}
	v, err := s.loadSubmission(c, t)
	if err != nil {
		c.Fail(err)
		return
	}
	if v == nil {
		c.Error(http.StatusNotFound)
		return
	}
	p := c.Participation
	var changes []judging.TaskScoreChange
	err = s.st.RunTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		qq := s.st.Q.WithTx(tx)
		if _, err := qq.CWSLockParticipation(ctx, p.ID); err != nil {
			return err
		}
		played, err := qq.CWSParticipationTokens(ctx, p.ID)
		if err != nil {
			return err
		}
		info := TokensAvailable(mustTokenRules(c.Contest), mustTokenRules(t), t.ID, tokenStart(p), tokensPlayed(played), c.Now)
		already := v.row.Tokened
		if !already {
			// Played since the submission was read.
			if _, err := qq.GetTokenBySubmission(ctx, v.row.Submission.ID); err == nil {
				already = true
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		if err := checkToken(info, already); err != nil {
			return err
		}
		if _, err := qq.CreateToken(ctx, q.CreateTokenParams{SubmissionID: v.row.Submission.ID, Timestamp: c.Now}); err != nil {
			return err
		}
		changes, err = judging.RefreshTaskScores(ctx, tx, []judging.TaskKey{{ParticipationID: p.ID, TaskID: t.ID}})
		return err
	})
	switch {
	case errors.Is(err, errTokenUnacceptable):
		s.log.Warn("user tried to play a token when they shouldn't", "username", p.User.Username)
		c.NotifyError(msgTokenDiscarded, msgNoTokens, nil)
	case errors.Is(err, errTokenAlreadyPlayed):
		c.NotifyWarning(msgTokenDiscarded, msgTokenAlreadyPlayed, nil)
	case err != nil:
		c.Fail(err)
		return
	default:
		if err := judging.Notify(ctx, s.kv, judging.Event{Kind: judging.EventToken, ContestID: c.Contest.ID,
			SubmissionIDs: []int32{v.row.Submission.ID}}); err != nil {
			s.log.Warn("notifying the dispatcher of a token", "submission", v.row.Submission.ID, "err", err)
		}
		s.pub.Publish(taskScoreEvents(changes)...)
		s.log.Info("token played", "username", p.User.Username, "task", t.Name)
		c.NotifySuccess(msgTokenReceived, msgTokenReceivedText, nil)
	}
	c.Redirect(c.URL.Join("tasks", t.Name, "submissions"))
}
