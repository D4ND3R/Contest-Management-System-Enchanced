// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/server/contest/handlers/api.py of CMS (documented in
// docs/API.rst):
// Copyright © 2010-2014 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2012-2018 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// and the CMS contributors of the JSON API.

package contestweb

// The JSON API of CMS (R5.7; docs/en/api.md): POST api/login gives the
// login_data that the other calls send in the X-CMS-Authorization header
// (or the login cookie, or IP autologin); api/task_list, api/<task>/submit
// (api_submit.go), api/<task>/submission_list and the details of a
// submission filtered by the task's feedback level
// (api/<task>/submissions/<id>/details) or unfiltered for an
// administrator impersonating the participant (.../full_details).
//
// An administrator impersonates a participant by logging in with
// admin_token = contest_web.admin_token instead of the password
// (Server.ValidateLogin): the requests then skip the IP restriction and
// the block of hidden participations, and accept override_phase_check
// (routes, Ctx.phaseAllowed), override_official, override_max_number and
// override_min_interval (submit) set to "1".
//
// Answers are JSON with the status codes and "error" messages of CMS,
// which are English and not translated (the API is for programs). The
// JSON is compact; numbers keep their value but not always CMS's
// spelling (10 rather than 10.0).

import (
	"fmt"
	"net/http"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/scoretype"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/web"
)

// alreadyLoggedIn is CMS's login_data for a participant logged in by IP
// address (there is no login data to give back).
const alreadyLoggedIn = "Already-Logged-In"

// apiLogin is POST api/login (CMS ApiLoginHandler): username and
// password, or username and admin_token, give {"login_data": …}; a
// failure is 403 {"error": "Login failed"}. A participant already
// logged in gets their own login data back (or 400 when the username
// names someone else). It has no CSRF check (csrfExempt) and shares the
// rate limits of the login form, answered with a JSON 429.
func (s *Server) apiLogin(c *Ctx) {
	username := c.ArgDefault("username", "")
	password := c.ArgDefault("password", "")
	adminToken := c.ArgDefault("admin_token", "")

	if p := c.Participation; p != nil {
		if username != "" && p.User.Username != username {
			c.JSONError(http.StatusBadRequest,
				fmt.Sprintf("Logged in as %s but trying to login as %s", p.User.Username, username))
			return
		}
		// CMS returns the header, else the login cookie, else (IP
		// autologin) a placeholder. Its cookie branch fails (it puts the
		// decoded cookie, bytes, in the JSON); here the signed cookie is
		// returned, which works as login_data.
		data := c.R.Header.Get(authHeader)
		if data == "" {
			if ck, err := c.R.Cookie(loginCookieName(c.Contest.Name)); err == nil && ck.Value != "" {
				data = ck.Value
			}
		}
		if data == "" {
			data = alreadyLoggedIn
		}
		c.JSON(http.StatusOK, map[string]string{"login_data": data})
		return
	}

	addr := clientAddr(c.R)
	if ok, wait := s.allowAPILogin(c, username, addr.String()); !ok {
		c.W.Header().Set("Retry-After", fmt.Sprint(max(1, int(wait.Seconds()+0.999))))
		c.JSONError(http.StatusTooManyRequests, "Too many login attempts")
		return
	}
	res, err := s.ValidateLogin(c.Context(), c.Contest, c.Now, username, password, addr, adminToken)
	if err != nil {
		c.apiFail(err)
		return
	}
	if res.Participation == nil {
		c.JSONError(http.StatusForbidden, "Login failed")
		return
	}
	c.JSON(http.StatusOK, map[string]string{"login_data": res.Data})
}

// allowAPILogin applies the limits of the login form (allowLogin) without
// answering.
func (s *Server) allowAPILogin(c *Ctx, username, addr string) (bool, time.Duration) {
	ok, wait := s.loginByUser.Allow(c.Contest.Name + "\x00" + username)
	if ok {
		ok, wait = s.loginByIP.Allow(addr)
	}
	if !ok {
		s.log.Warn("too many API login attempts", "contest", c.Contest.Name, "username", username, "ip", addr)
	}
	return ok, wait
}

// apiTask is a task of api/task_list.
type apiTask struct {
	Name string `json:"name"`
	// Statements are the languages of the task's statements.
	Statements []string `json:"statements"`
	// SubmissionFormat are the files to submit ("%l": the extension of
	// the language).
	SubmissionFormat []string `json:"submission_format"`
}

// apiTaskList is GET api/task_list (CMS ApiTaskListHandler): the tasks
// in the order of the contest with their statement languages and
// submission format. Two queries whatever the number of tasks.
func (s *Server) apiTaskList(c *Ctx) {
	tasks, err := c.Tasks()
	if err != nil {
		c.apiFail(err)
		return
	}
	cid := c.Contest.ID
	rows, err := s.st.Q.CWSAPIStatementLanguages(c.Context(), &cid)
	if err != nil {
		c.apiFail(err)
		return
	}
	langs := make(map[int32][]string, len(rows))
	for _, r := range rows {
		langs[r.TaskID] = r.Languages
	}
	out := make([]apiTask, len(tasks))
	for i, t := range tasks {
		st := langs[t.ID]
		if st == nil {
			st = []string{}
		}
		sf := t.SubmissionFormat
		if sf == nil {
			sf = []string{}
		}
		out[i] = apiTask{Name: t.Name, Statements: st, SubmissionFormat: sf}
	}
	c.JSON(http.StatusOK, map[string][]apiTask{"tasks": out})
}

// apiSubmissionID is a submission of api/<task>/submission_list: its
// opaque id as a string (it may not fit in a JavaScript number).
type apiSubmissionID struct {
	ID string `json:"id"`
}

// apiSubmissionList is GET api/<task>/submission_list (CMS
// ApiSubmissionListHandler): the participant's submissions on the task,
// oldest first.
func (s *Server) apiSubmissionList(c *Ctx) {
	task, err := c.Task(c.R.PathValue("task"))
	if err != nil {
		c.apiFail(err)
		return
	}
	if task == nil {
		c.JSONError(http.StatusNotFound, "Not found")
		return
	}
	ids, err := s.st.Q.CWSAPISubmissionList(c.Context(), q.CWSAPISubmissionListParams{
		ParticipationID: c.Participation.ID,
		TaskID:          task.ID,
	})
	if err != nil {
		c.apiFail(err)
		return
	}
	out := make([]apiSubmissionID, len(ids))
	for i, id := range ids {
		out[i] = apiSubmissionID{ID: fmt.Sprint(id)}
	}
	c.JSON(http.StatusOK, map[string][]apiSubmissionID{"list": out})
}

// apiSubmissionDetails is GET api/<task>/submissions/<id>/details (CMS
// ApiSubmissionDetailsHandler): the score details of a scored submission
// as the contestant may see them: the public details filtered by the
// task's feedback level, or the full details (with a token, and with full
// feedback in analysis mode). {"details": null} while it is not scored.
func (s *Server) apiSubmissionDetails(c *Ctx) {
	s.apiDetails(c, false)
}

// apiSubmissionFullDetails is GET
// api/<task>/submissions/<id>/full_details (CMS
// ApiSubmissionFullDetailsHandler): the unfiltered details, for an
// administrator impersonating the participant only.
func (s *Server) apiSubmissionFullDetails(c *Ctx) {
	if !c.Impersonated {
		c.JSONError(http.StatusForbidden, "Admin impersonation required")
		return
	}
	s.apiDetails(c, true)
}

// apiDetails answers both details calls with the details of the
// submission pages (Server.SubmissionScoreDetails, submissions.go).
func (s *Server) apiDetails(c *Ctx, full bool) {
	task, err := c.Task(c.R.PathValue("task"))
	if err != nil {
		c.apiFail(err)
		return
	}
	if task == nil {
		c.JSONError(http.StatusNotFound, "Task not found")
		return
	}
	opaque, _ := c.PathID("id")
	details, found, err := s.SubmissionScoreDetails(c, task, opaque, full)
	if err != nil {
		c.apiFail(fmt.Errorf("details of submission %d on %s: %w", opaque, task.Name, err))
		return
	}
	if !found {
		c.JSONError(http.StatusNotFound, "Submission not found")
		return
	}
	c.JSON(http.StatusOK, map[string]*scoretype.Details{"details": details})
}

// apiFail logs an unexpected error and answers 500 with a JSON error
// (CMS's API answers its HTML error page).
func (c *Ctx) apiFail(err error) {
	if c.Context().Err() != nil {
		return
	}
	c.S.log.Error("API request failed", "path", c.R.URL.Path, "err", err)
	c.JSONError(http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
}

// boolArg is CMS get_boolean_argument for the API: absent or empty is
// def, "0" and "1" are false and true; anything else answers 400 with
// CMS's message and returns ok false.
func (c *Ctx) boolArg(name string, def bool) (v, ok bool) {
	v, err := web.BoolArg(c.R, name, def)
	if err != nil {
		c.JSONError(http.StatusBadRequest, "Cannot parse boolean argument "+name)
		return false, false
	}
	return v, true
}
