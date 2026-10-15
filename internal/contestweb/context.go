// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/server/contest/handlers/{base,contest}.py,
// phase_management.py (actual_phase_required) and cms/server/util.py of
// CMS:
// Copyright © 2010-2014 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2012-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2014 Artem Iglikov <artem.iglikov@gmail.com>
// Copyright © 2015-2018 William Di Luigi <williamdiluigi@gmail.com>

package contestweb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/tasktype"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/i18n"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/web"
)

// Token modes (CMS TOKEN_MODE_*).
const (
	TokenModeDisabled = "disabled"
	TokenModeFinite   = "finite"
	TokenModeInfinite = "infinite"
	// TokenModeMixed: the tasks of the contest have different modes.
	TokenModeMixed = "mixed"
)

// Contest is a contest with its main group (CMS contest.main_group).
type Contest struct {
	q.Contest
	// MainGroup holds the contest times for visitors who are not logged
	// in; zero if the contest has none.
	MainGroup q.Group
}

// Task is a task of the contest with the parts of its active dataset the
// pages show (CMS task.active_dataset); HasDataset is false without one.
type Task struct {
	q.Task
	HasDataset         bool
	TimeLimit          *float64
	MemoryLimit        *int64
	TaskType           string
	TaskTypeParameters json.RawMessage
}

// AllowedLanguages returns the programming languages of the task (CMS
// Task.get_allowed_languages): its own list, else the contest's.
func (t *Task) AllowedLanguages(c *Contest) []string {
	if t.Task.AllowedLanguages != nil {
		return t.Task.AllowedLanguages
	}
	return c.Languages
}

// TaskTypeName returns the name of the task type of the active dataset
// shown to contestants (CMS get_task_type(dataset=…).name), or the
// stored name if its parameters cannot be read.
func (t *Task) TaskTypeName() string {
	if !t.HasDataset {
		return ""
	}
	tt, err := tasktype.Get(t.TaskType, t.TaskTypeParameters)
	if err != nil {
		return t.TaskType
	}
	return tt.DisplayName()
}

// access says who may use a route (CMS's decorators).
type access struct {
	// auth: authNone, authPage (tornado.web.authenticated: GET and HEAD
	// redirect to the login form, other methods get 403) or authAPI
	// (api_login_required: a JSON 403).
	auth int
	// phases, if not nil, are the actual phases the route works in
	// (actual_phase_required); unrestricted participations pass.
	phases []int
	// noRefresh leaves the login cookie as it is (CMS refresh_cookie =
	// False: asynchronous requests must not extend the session).
	noRefresh bool
	// api marks the routes of the JSON API (CMS api_request): phase
	// errors are JSON.
	api bool
}

const (
	authNone = iota
	authPage
	authAPI
)

// Ctx is the state of a request to a contest route (CMS ContestHandler
// after prepare): what every handler and template needs.
type Ctx struct {
	S *Server
	W http.ResponseWriter
	R *http.Request
	// Now is the time of the request (CMS self.timestamp).
	Now time.Time
	// Contest is the contest of the URL (or of contest_web.contest).
	Contest *Contest
	// URL is the relative URL of the contest (CMS contest_url): "." or
	// "../…/<name>".
	URL web.URL
	// Choice is the interface language of the request.
	Choice web.Choice
	// Lang is Choice.Lang (CMS self.translation).
	Lang *web.Lang
	// RC is the render state of the templates.
	RC *web.Render
	// Participation is the logged-in participant, nil if none (CMS
	// current_user).
	Participation *Participation
	// Impersonated: an administrator logged in through the API with the
	// admin token (CMS impersonated_by_admin).
	Impersonated bool
	// API: a request to /api (CMS api_request).
	API bool
	// Phase is CMS's "phase": the phase of the participant's group (the
	// main group when nobody is logged in), 0 while the participant
	// actually competes.
	Phase int
	// Actual is the actual phase of the participant (zero without one).
	Actual ActualPhase
	// LiveSince is the id of the last live event before the page read
	// its data (the page's EventSource starts from it).
	LiveSince string

	tasks       []*Task
	tasksLoaded bool
	scores      map[int32]TaskScore
}

// Context returns the context of the request.
func (c *Ctx) Context() context.Context { return c.R.Context() }

// T translates a message of the "cms" catalogs (else "admin") into the
// language of the request.
func (c *Ctx) T(msgid string) string { return c.Lang.T(msgid) }

// Tn translates a message with plural forms.
func (c *Ctx) Tn(msgid, plural string, n int) string { return c.Lang.Tn(msgid, plural, n) }

// Arg returns the last value of a query or form argument (Tornado
// get_argument): ok is false if absent.
func (c *Ctx) Arg(name string) (string, bool) { return web.Arg(c.R, name) }

// ArgDefault returns Arg or def.
func (c *Ctx) ArgDefault(name, def string) string { return web.ArgDefault(c.R, name, def) }

// Tasks returns the tasks of the contest in order (CMS contest.tasks),
// loaded once per request with one query.
func (c *Ctx) Tasks() ([]*Task, error) {
	if c.tasksLoaded {
		return c.tasks, nil
	}
	cid := c.Contest.ID
	rows, err := c.S.st.Q.CWSTasks(c.Context(), &cid)
	if err != nil {
		return nil, err
	}
	c.tasks = make([]*Task, len(rows))
	for i, r := range rows {
		t := &Task{Task: r.Task, TimeLimit: r.TimeLimit, MemoryLimit: r.MemoryLimit,
			TaskTypeParameters: r.TaskTypeParameters}
		if r.TaskType != nil {
			t.HasDataset = true
			t.TaskType = *r.TaskType
		}
		c.tasks[i] = t
	}
	c.tasksLoaded = true
	return c.tasks, nil
}

// TaskScore is the score of the participant on a task, from the
// task_scores cache (D34; CMS task_score): Score is what administrators
// and rankings see, PublicScore what the contestant always sees,
// TokenedScore the part of the score the contestant knows through tokens;
// Partial: some official submission is not scored yet (the same for the
// three).
type TaskScore struct {
	Score, PublicScore, TokenedScore float64
	Partial                          bool
}

// TaskScores returns the scores of the participant by task id, loaded
// once per request with one query; a task without submissions is not in
// the map (its score is 0, not partial). Nil without a participant.
func (c *Ctx) TaskScores() (map[int32]TaskScore, error) {
	if c.Participation == nil {
		return nil, nil
	}
	if c.scores != nil {
		return c.scores, nil
	}
	rows, err := c.S.st.Q.CWSTaskScores(c.Context(), c.Participation.ID)
	if err != nil {
		return nil, err
	}
	c.scores = make(map[int32]TaskScore, len(rows))
	for _, r := range rows {
		c.scores[r.TaskID] = TaskScore{Score: r.Score, PublicScore: r.PublicScore,
			TokenedScore: r.TokenedScore, Partial: r.Partial}
	}
	return c.scores, nil
}

// Task returns the task of the contest with the given name, nil if there
// is none (CMS get_task). It uses the task list of the menu: no extra
// query.
func (c *Ctx) Task(name string) (*Task, error) {
	ts, err := c.Tasks()
	if err != nil {
		return nil, err
	}
	for _, t := range ts {
		if t.Name == name {
			return t, nil
		}
	}
	return nil, nil
}

// Page is the context of contest.html (CMS ContestHandler.render_params):
// pages put it in their data by embedding it,
//
//	type overviewData struct {
//		*Page
//		Tasks2 …
//	}
//
// so templates read .Contest, .Participation, .Phase… directly.
type Page struct {
	c *Ctx

	Contest *Contest
	// Participation is the logged-in participant (nil: the login form).
	Participation *Participation
	// Phase, ActualPhase and the four times are CMS's phase,
	// actual_phase, current_phase_begin/end and valid_phase_begin/end
	// (nil: infinite or unknown).
	Phase                              int
	ActualPhase                        int
	CurrentPhaseBegin, CurrentPhaseEnd *time.Time
	ValidPhaseBegin, ValidPhaseEnd     *time.Time
	// QuestionsEnabled and TestingEnabled: allow_questions and
	// allow_user_tests of the contest.
	QuestionsEnabled bool
	TestingEnabled   bool
	// TokensContest is the token mode of the contest, TokensTasks the
	// mode of every task or TokenModeMixed.
	TokensContest string
	TokensTasks   string
	// Tasks are the tasks of the contest (the menu); nil when nobody is
	// logged in.
	Tasks []*Task
	// Languages are the programming languages of the contest that exist
	// (the LANGUAGES object of the pages' JavaScript).
	Languages []*language.Language
	// Choice is the interface language and the selector's options.
	Choice web.Choice
	// PageName is CMS's "page" (the active menu entry): "overview",
	// "communication", "task_description", "task_submissions",
	// "documentation", "test_interface".
	PageName string
	// Task is the task of the page (CMS "task"), for the menu.
	Task *Task
	// LiveURL and LiveSince configure live.js (the indicator): the
	// event stream of the participant and the id of the last event
	// before the page read its data.
	LiveURL   string
	LiveSince string
	// LoginError and Next are the login_error and next arguments of
	// the login form (CMS handler.get_argument); Next is nil when
	// absent.
	LoginError bool
	Next       *string
}

// Page returns the context of contest.html for the page named name (CMS
// "page"), loading the tasks of the menu when a participant is logged in
// (Page.Tasks and TokensTasks are empty for the login form).
func (c *Ctx) Page(name string) (*Page, error) {
	// The login form (nobody logged in) shows no task: no query.
	var tasks []*Task
	if c.Participation != nil {
		var err error
		if tasks, err = c.Tasks(); err != nil {
			return nil, err
		}
	}
	p := &Page{
		c:                c,
		Contest:          c.Contest,
		Participation:    c.Participation,
		Phase:            c.Phase,
		QuestionsEnabled: c.Contest.AllowQuestions,
		TestingEnabled:   c.Contest.AllowUserTests,
		TokensContest:    c.Contest.TokenMode,
		TokensTasks:      tokensTasks(tasks),
		Tasks:            tasks,
		Choice:           c.Choice,
		PageName:         name,
		Next:             pageNext(c.R),
	}
	if c.Participation == nil {
		p.LoginError = c.ArgDefault("login_error", "") != ""
	} else {
		a := c.Actual
		p.ActualPhase = a.Phase
		p.CurrentPhaseBegin, p.CurrentPhaseEnd = a.Begin, a.End
		p.ValidPhaseBegin, p.ValidPhaseEnd = a.ValidBegin, a.ValidEnd
		p.LiveURL = c.URL.Join("events")
		p.LiveSince = c.LiveSince
	}
	for _, name := range c.Contest.Languages {
		if l, ok := c.S.langs.Lookup(name); ok {
			p.Languages = append(p.Languages, l)
		}
	}
	return p, nil
}

// tokensTasks is CMS's tokens_tasks: the token mode shared by every task,
// else "mixed" (also with no tasks).
func tokensTasks(tasks []*Task) string {
	mode := ""
	for i, t := range tasks {
		if i == 0 {
			mode = t.TokenMode
		} else if t.TokenMode != mode {
			return TokenModeMixed
		}
	}
	if mode == "" {
		return TokenModeMixed
	}
	return mode
}

// Render sends a page of the site with status (200 normally). Template
// failures become the error page (500).
func (c *Ctx) Render(status int, name string, data any) {
	c.S.site.Page(c.W, c.R, c.RC, status, name, data)
}

// Error sends the error page with status (CMS HTTPError + write_error).
func (c *Ctx) Error(status int) {
	c.S.site.Error(c.W, c.R, c.RC, status)
}

// Fail logs an unexpected error and sends the error page with 500 (or
// nothing if the client went away).
func (c *Ctx) Fail(err error) {
	if c.Context().Err() != nil {
		return
	}
	c.S.log.Error("contest page failed", "path", c.R.URL.Path, "err", err)
	c.Error(http.StatusInternalServerError)
}

// JSON sends v as JSON (CMS json()).
func (c *Ctx) JSON(status int, v any) {
	_ = web.WriteJSON(c.W, status, v)
}

// JSONError sends {"error": msg} (the errors of CMS's API).
func (c *Ctx) JSONError(status int, msg string) {
	c.JSON(status, map[string]string{"error": msg})
}

// Redirect answers 302 with a relative Location (Tornado redirect).
func (c *Ctx) Redirect(url string) {
	web.Redirect(c.W, url, http.StatusFound)
}

// Notify queues a notification for the participant, shown by the next
// poll of <contest>/notifications (CMS add_notification): subject and
// text are msgids translated now, text formatted with params (a
// map[string]any for "%(name)s" placeholders, or []any for positional
// ones). level is "success", "warning" or "error".
func (c *Ctx) Notify(level, subject, text string, params any) {
	if c.Participation == nil {
		return
	}
	msg := c.T(text)
	switch p := params.(type) {
	case nil:
	case map[string]any:
		msg = i18n.Format(msg, p)
	case []any:
		if s, err := web.PyFormat(msg, p...); err == nil {
			msg = s
		}
	default:
		if s, err := web.PyFormat(msg, p); err == nil {
			msg = s
		}
	}
	c.S.notes.push(c.Context(), c.Participation.ID, notification{
		Timestamp: web.Timestamp(float64(c.Now.UnixMicro()) / 1e6),
		Subject:   c.T(subject),
		Text:      msg,
		Level:     level,
	})
}

// Notification levels (CMS NOTIFICATION_*).
const (
	NotificationError   = "error"
	NotificationWarning = "warning"
	NotificationSuccess = "success"
)

// NotifySuccess, NotifyWarning and NotifyError are Notify with a level.
func (c *Ctx) NotifySuccess(subject, text string, params any) {
	c.Notify(NotificationSuccess, subject, text, params)
}

// NotifyWarning queues a warning (see Notify).
func (c *Ctx) NotifyWarning(subject, text string, params any) {
	c.Notify(NotificationWarning, subject, text, params)
}

// NotifyError queues an error (see Notify).
func (c *Ctx) NotifyError(subject, text string, params any) {
	c.Notify(NotificationError, subject, text, params)
}

// PathID returns a path value matching CMS's [1-9][0-9]* (submission and
// user test numbers); ok is false otherwise (the route does not exist).
func (c *Ctx) PathID(name string) (int64, bool) {
	v := c.R.PathValue(name)
	if v == "" || v[0] < '1' || v[0] > '9' {
		return 0, false
	}
	for i := 1; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		// Larger than any id or opaque id (63 bits): nothing has it.
		return 0, false
	}
	return n, true
}

// contestRoute adapts a handler of a contest route: it builds the Ctx and
// applies the route's access rules.
func (s *Server) contestRoute(a access, h func(*Ctx)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, ok := s.newCtx(w, r, a)
		if !ok {
			return
		}
		switch a.auth {
		case authPage:
			if c.Participation == nil {
				c.loginRequired()
				return
			}
		case authAPI:
			if c.Participation == nil {
				c.JSONError(http.StatusForbidden, "An authenticated user is required")
				return
			}
		}
		if a.phases != nil && !c.phaseAllowed(a.phases) {
			return
		}
		h(c)
	}
}

// newCtx prepares a request (CMS ContestHandler.prepare): it answers
// itself (404, 500) and returns false when the request cannot go on.
func (s *Server) newCtx(w http.ResponseWriter, r *http.Request, a access) (*Ctx, bool) {
	c := &Ctx{S: s, W: w, R: r, Now: s.now(), API: a.api}
	if s.hub != nil {
		// Before reading anything: what is published from now on is
		// replayed to the page's stream.
		c.LiveSince = s.hub.LastID()
	}
	name := s.cfg.ContestWeb.Contest
	if s.multiContest() {
		name = r.PathValue("contest")
	}
	contest, err := s.loadContest(r.Context(), name)
	if err != nil {
		s.log.Error("loading the contest", "contest", name, "err", err)
		s.plainPage(w, r, http.StatusInternalServerError)
		return nil, false
	}
	root := web.RequestRoot(r)
	if contest == nil {
		// CMS renders the error page with the base parameters only.
		ch := s.ui.Choose(r, s.ui.All())
		rc := s.site.NewRender(w, r, ch.Lang)
		rc.Now = c.Now
		s.site.Error(w, r, rc, http.StatusNotFound)
		return nil, false
	}
	c.Contest = contest
	if s.multiContest() {
		c.URL = root.Sub(contest.Name)
	} else {
		c.URL = root
	}
	available := s.ui.All()
	if len(contest.AllowedLocalizations) > 0 {
		if f := s.ui.Filter(contest.AllowedLocalizations); len(f) > 0 {
			available = f
		}
	}
	c.Choice = s.ui.Choose(r, available)
	c.Lang = c.Choice.Lang
	c.RC = s.site.NewRender(w, r, c.Lang)
	c.RC.Now = c.Now
	c.RC.Extra = c
	w.Header().Set("Cache-Control", "no-cache, must-revalidate")

	if err := s.authenticateRequest(c, a); err != nil {
		c.Fail(err)
		return nil, false
	}
	if c.Participation == nil {
		c.Phase = GroupPhase(c.Now, &contest.MainGroup)
	} else {
		c.Phase = GroupPhase(c.Now, &c.Participation.Group)
		c.Actual = ParticipationPhase(c.Now, &c.Participation.Group, &c.Participation.Participation)
		if c.Actual.Phase == PhaseCompeting {
			c.Phase = 0
		}
	}
	return c, true
}

// authenticateRequest finds the participant (CMS get_current_user) and
// refreshes or clears the login cookie.
func (s *Server) authenticateRequest(c *Ctx, a access) error {
	r := c.R
	name := loginCookieName(c.Contest.Name)
	cookie := ""
	hadCookie := false
	if ck, err := r.Cookie(name); err == nil {
		cookie, hadCookie = ck.Value, true
	}
	res, err := s.Authenticate(r.Context(), c.Contest, c.Now, cookie, r.Header.Get(authHeader), clientAddr(r))
	if err != nil {
		return err
	}
	switch {
	case res.Data == "":
		// CMS clears the cookie on every such answer; only when the
		// browser sent one is enough.
		if hadCookie {
			s.setLoginCookie(c.W, r, c.Contest, "")
		}
	case !a.noRefresh:
		s.setLoginCookie(c.W, r, c.Contest, res.Data)
	}
	c.Participation = res.Participation
	c.Impersonated = res.Impersonated
	return nil
}

// loadContest returns the contest named name with its main group, nil if
// there is none.
func (s *Server) loadContest(ctx context.Context, name string) (*Contest, error) {
	if name == "" {
		return nil, nil
	}
	row, err := s.st.Q.CWSContest(ctx, name)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c := &Contest{Contest: row.Contest}
	if row.MainStart != nil {
		c.MainGroup = q.Group{
			ID:              deref(row.Contest.MainGroupID),
			Start:           *row.MainStart,
			Stop:            *row.MainStop,
			AnalysisEnabled: *row.MainAnalysisEnabled,
			AnalysisStart:   *row.MainAnalysisStart,
			AnalysisStop:    *row.MainAnalysisStop,
			PerUserTime:     row.MainPerUserTime,
			ContestID:       row.Contest.ID,
		}
	}
	return c, nil
}

func deref[T any](p *T) T {
	var z T
	if p == nil {
		return z
	}
	return *p
}

// loginRequired answers a request of a page that needs a participant
// (tornado.web.authenticated): GET and HEAD go to the login form with
// ?next=<the request URI>, other methods get 403.
func (c *Ctx) loginRequired() {
	if c.R.Method == http.MethodGet || c.R.Method == http.MethodHead {
		c.Redirect(c.URL.String() + "?" + web.QueryString("next", c.R.RequestURI))
		return
	}
	c.Error(http.StatusForbidden)
}

// phaseAllowed is CMS actual_phase_required: true if the participant's
// actual phase is one of phases or they are unrestricted (an
// impersonating administrator decides with override_phase_check);
// otherwise it answers (redirect to the contest, or a JSON 403 on the
// API) and returns false.
func (c *Ctx) phaseAllowed(phases []int) bool {
	unrestricted := c.Participation != nil && c.Participation.Unrestricted
	if c.Impersonated {
		v, err := web.BoolArg(c.R, "override_phase_check", unrestricted)
		if err != nil {
			c.JSONError(http.StatusBadRequest, fmt.Sprintf("Cannot parse boolean argument %s", "override_phase_check"))
			return false
		}
		unrestricted = v
	}
	if unrestricted || slices.Contains(phases, c.Actual.Phase) {
		return true
	}
	if c.API {
		open := true
		for _, p := range phases {
			if p != PhaseCompeting && p != PhaseAnalysis {
				open = false
			}
		}
		if open {
			c.JSONError(http.StatusForbidden, "The contest is not open")
		} else {
			c.JSONError(http.StatusForbidden, "Wrong contest phase")
		}
		return false
	}
	c.Redirect(c.URL.String())
	return false
}

// ContestTime returns the contest time of t for the participant: the time
// since the start of their time frame (R5.9); ok is false without a
// participant or a known start.
func (c *Ctx) ContestTime(t time.Time) (time.Duration, bool) {
	if c == nil || c.Participation == nil || c.Actual.ValidBegin == nil {
		return 0, false
	}
	return t.Sub(*c.Actual.ValidBegin), true
}

// notImplemented answers the routes whose handlers are not written yet.
func (c *Ctx) notImplemented() {
	c.Error(http.StatusNotImplemented)
}
