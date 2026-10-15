// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// The route table is ported from cms/server/contest/handlers/__init__.py
// and cms/server/contest/server.py of CMS:
// Copyright © 2010-2014 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2015 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2012-2014 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2015-2018 William Di Luigi <williamdiluigi@gmail.com>

package contestweb

// Every route of CMS's ContestWebServer is registered here, relative to
// the contest ("/<contest>" without contest_web.contest, "" with it), with
// the access rules of its CMS handler. The files that implement them:
//
//	pages.go          (contest), login, logout, register, start,
//	                  documentation; the contest list ("/")
//	notifications.go  notifications (the polling of cws_utils.js)
//	events.go         events (server-sent events for live.js; new)
//	tasks.go          tasks/<t>/description, statements, attachments
//	submissions.go    tasks/<t>/submit, submissions, submissions/<id>
//	                  (+ details, files, token)
//	usertests.go      testing, tasks/<t>/test, tasks/<t>/tests/<id>
//	                  (+ details, input, output, files)
//	communication.go  communication, question
//	api.go            api/login, api/task_list, api/<t>/submit,
//	                  api/<t>/submission_list,
//	                  api/<t>/submissions/<id>/details and full_details
//
// Split of the work: tasks.go, submissions.go and usertests.go belong to
// the tasks work (tasks, submissions, user tests, tokens; with their
// templates task_description, task_submissions, submission_details,
// test_interface, user_test_details, user_test_row and macro/submission,
// and their inline handlers moved to static/cws_handlers.js),
// communication.go and api.go to the communication and API work (with
// communication.html); until they are written their handlers answer 501.
// Both replace their stubs and add files of their own; the core files
// below are shared, and changes to them should stay small:
//
//	context.go  Ctx: the request (contest, language, participation and
//	            its phases, Now), Ctx.Tasks / Ctx.Task (the tasks of the
//	            contest, one query per request), Ctx.TaskScores (the
//	            task_scores cache), Ctx.Page (the data of contest.html,
//	            embedded in every page's data), Render, Error, Fail,
//	            JSON, JSONError, Redirect, Notify*, PathID, ContestTime
//	auth.go     ValidateLogin (with the admin token: impersonation),
//	            Authenticate, ValidatePassword, HashPassword, the login
//	            data (signLogin, setLoginCookie; the API's login_data is
//	            the same signed value)
//	phase.go    ComputeActualPhase, GroupPhase, ParticipationPhase
//	funcs.go    template functions (contest_url, format_contest_time,
//	            get_score_class, format_token_rules, to_language...),
//	            FormatTokenRules, GetScoreClass, FormatContestTime
//	notifications.go  Server.Communications (announcements, messages and
//	            answers of a participant, one query)
//	helpers_test.go   test site with the demo contest, browsers with
//	            cookies and CSRF, clock, query counter, live events
//
// Path values: {task} is the task name (Ctx.Task finds it, nil if
// unknown), {id} the submission's opaque id or the user test's number
// (CMS's [1-9][0-9]*: other values get Tornado's 404 before the handler
// runs, so Ctx.PathID succeeds), {filename...} a file name, {lang} a
// statement language, {io} "input" or "output".

import (
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/httpx"
)

// Phases accepted by the routes (CMS actual_phase_required).
var (
	phasesAnyStarted = []int{PhaseCompeting, PhaseAfterWindow, PhaseAfterContest, PhaseAnalysis, PhaseOver}
	phasesSubmit     = []int{PhaseCompeting, PhaseAfterWindow, PhaseAfterContest, PhaseAnalysis}
	phasesCompeting  = []int{PhaseCompeting}
	phasesAPIOpen    = []int{PhaseCompeting, PhaseAnalysis}
	phasesBeforeOwn  = []int{PhaseBeforeWindow}
)

// route is a contest route: method, path below the contest, access
// rules and handler.
type route struct {
	method  string
	path    string
	access  access
	handler func(*Ctx)
}

// contestRoutes is the route table (CMS HANDLERS, plus events).
func (s *Server) contestRoutes() []route {
	page := access{auth: authPage}
	return []route{
		// Main.
		{"GET", "", access{}, s.overviewPage},
		{"POST", "/login", access{}, s.loginPage},
		{"POST", "/logout", access{}, s.logoutPage},
		{"GET", "/register", access{}, s.registerPage},
		{"POST", "/register", access{}, s.registerPost},
		{"POST", "/start", access{auth: authPage, phases: phasesBeforeOwn}, s.startPage},
		{"GET", "/notifications", access{auth: authAPI, noRefresh: true}, s.notificationsPage},
		{"GET", "/documentation", page, s.documentationPage},
		{"GET", "/events", access{auth: authAPI, noRefresh: true}, s.eventsPage},
		// Tasks.
		{"GET", "/tasks/{task}/description", access{auth: authPage, phases: phasesAnyStarted}, s.taskDescriptionPage},
		{"GET", "/tasks/{task}/statements/{lang}", access{auth: authPage, phases: phasesAnyStarted}, s.taskStatementPage},
		{"GET", "/tasks/{task}/statements/{lang}/{rest...}", access{auth: authPage, phases: phasesAnyStarted}, s.taskStatementPage},
		{"GET", "/tasks/{task}/attachments/{filename...}", access{auth: authPage, phases: phasesAnyStarted}, s.taskAttachmentPage},
		// Task submissions.
		{"POST", "/tasks/{task}/submit", access{auth: authPage, phases: phasesSubmit}, s.submitPost},
		{"GET", "/tasks/{task}/submissions", access{auth: authPage, phases: phasesAnyStarted}, s.taskSubmissionsPage},
		{"GET", "/tasks/{task}/submissions/{id}", access{auth: authAPI, phases: phasesAnyStarted, noRefresh: true}, s.submissionStatusPage},
		{"GET", "/tasks/{task}/submissions/{id}/details", access{auth: authAPI, phases: phasesAnyStarted, noRefresh: true}, s.submissionDetailsPage},
		{"GET", "/tasks/{task}/submissions/{id}/files/{filename...}", access{auth: authPage, phases: phasesAnyStarted}, s.submissionFilePage},
		{"POST", "/tasks/{task}/submissions/{id}/token", access{auth: authPage, phases: phasesCompeting}, s.useTokenPost},
		// Task user tests.
		{"GET", "/testing", access{auth: authPage, phases: phasesCompeting}, s.testInterfacePage},
		{"POST", "/tasks/{task}/test", access{auth: authPage, phases: phasesCompeting, noRefresh: true}, s.userTestPost},
		{"GET", "/tasks/{task}/tests/{id}", access{auth: authAPI, phases: phasesCompeting, noRefresh: true}, s.userTestStatusPage},
		{"GET", "/tasks/{task}/tests/{id}/details", access{auth: authAPI, phases: phasesCompeting, noRefresh: true}, s.userTestDetailsPage},
		{"GET", "/tasks/{task}/tests/{id}/input", access{auth: authPage, phases: phasesCompeting}, withIO("input", s.userTestIOPage)},
		{"GET", "/tasks/{task}/tests/{id}/output", access{auth: authPage, phases: phasesCompeting}, withIO("output", s.userTestIOPage)},
		{"GET", "/tasks/{task}/tests/{id}/files/{filename...}", access{auth: authPage, phases: phasesCompeting}, s.userTestFilePage},
		// Communication.
		{"GET", "/communication", page, s.communicationPage},
		{"POST", "/question", page, s.questionPost},
		// API.
		{"POST", "/api/login", access{api: true}, s.apiLogin},
		{"GET", "/api/task_list", access{auth: authAPI, phases: phasesAPIOpen, api: true}, s.apiTaskList},
		{"POST", "/api/{task}/submit", access{auth: authAPI, phases: phasesAPIOpen, api: true}, s.apiSubmit},
		{"GET", "/api/{task}/submission_list", access{auth: authAPI, phases: phasesAPIOpen, api: true}, s.apiSubmissionList},
		{"GET", "/api/{task}/submissions/{id}/details", access{auth: authAPI, phases: phasesAnyStarted, api: true}, s.apiSubmissionDetails},
		{"GET", "/api/{task}/submissions/{id}/full_details", access{auth: authAPI, phases: phasesAnyStarted, api: true}, s.apiSubmissionFullDetails},
	}
}

// withIO sets the path value "io" of the user test input and output
// routes (CMS's (input|output)): two literal routes, because a wildcard
// there would clash with .../details in the router.
func withIO(io string, h func(*Ctx)) func(*Ctx) {
	return func(c *Ctx) {
		c.R.SetPathValue("io", io)
		h(c)
	}
}

// isAPILogin reports whether a path is the API login of a contest (no
// CSRF check there, as CMS).
func isAPILogin(path string) bool {
	return path == "/api/login" || strings.HasSuffix(path, "/api/login") && strings.Count(path, "/") == 3
}

// routes builds the router of the site.
func (s *Server) routes() http.Handler {
	mux := httpx.NewRouter()
	httpx.MountCommon(mux, s.health, s.cfg.SourceURL)
	mux.Handle("GET /static/", http.StripPrefix("/static", s.static))
	if dir := s.cfg.ContestWeb.DocsPath; dir != "" {
		mux.Handle("GET /docs/", http.StripPrefix("/docs", docsHandler(dir)))
	}

	contest := mux
	prefix := ""
	if s.multiContest() {
		mux.HandleFunc("GET /{$}", s.contestListPage)
		// Everything below a contest goes to its own router, so that its
		// wildcard patterns do not clash with /static/ and /docs/.
		contest = httpx.NewRouter()
		mux.Handle("/{contest}/", contest)
		prefix = "/{contest}"
		contest.HandleFunc("/{contest}/", notFound)
	}
	allowed := map[string][]string{}
	var paths []string
	for _, rt := range s.contestRoutes() {
		p := prefix + rt.path
		if p == "" {
			p = "/{$}"
		}
		if _, ok := allowed[p]; !ok {
			paths = append(paths, p)
		}
		allowed[p] = append(allowed[p], rt.method)
		target := contest
		if rt.path == "" {
			target = mux // "/{contest}" (no slash) or "/"
		}
		h := s.contestRoute(rt.access, rt.handler)
		if strings.Contains(rt.path, "{id}") {
			h = validID(h)
		}
		target.HandleFunc(rt.method+" "+p, h)
	}
	// Other methods: CMS answers 405 with its error page.
	for _, p := range paths {
		target := contest
		if !strings.HasPrefix(p, prefix+"/") || p == "/{$}" {
			target = mux
		}
		methods := allowed[p]
		target.HandleFunc(p, s.contestRoute(access{}, func(c *Ctx) {
			allow := strings.Join(methods, ", ")
			if strings.Contains(allow, "GET") {
				allow += ", HEAD"
			}
			c.W.Header().Set("Allow", allow)
			c.Error(http.StatusMethodNotAllowed)
		}))
	}
	mux.HandleFunc("/", notFound)
	return mux
}

// validID answers 404 when the {id} of a route is not a number of CMS's
// form [1-9][0-9]* (the URL matches no handler in CMS), before anything
// else; Ctx.PathID then always succeeds.
func validID(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v := r.PathValue("id")
		ok := v != "" && v[0] >= '1' && v[0] <= '9'
		for i := 1; ok && i < len(v); i++ {
			ok = v[i] >= '0' && v[i] <= '9'
		}
		if !ok {
			notFound(w, r)
			return
		}
		h(w, r)
	}
}

// notFound is Tornado's answer to a URL no handler matches.
func notFound(w http.ResponseWriter, r *http.Request) {
	plainPage(w, r, http.StatusNotFound)
}

// plainPage answers with Tornado's default error page ("404: Not Found"),
// for errors that happen before CMS has a page to render.
func plainPage(w http.ResponseWriter, _ *http.Request, status int) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=UTF-8")
	h.Del("Content-Length")
	w.WriteHeader(status)
	msg := strconv.Itoa(status) + ": " + http.StatusText(status)
	_, _ = w.Write([]byte("<html><title>" + msg + "</title><body>" + msg + "</body></html>"))
}

func (s *Server) plainPage(w http.ResponseWriter, r *http.Request, status int) {
	plainPage(w, r, status)
}

// docsHandler serves the language documentation of
// contest_web.docs_path (CMS's SharedDataMiddleware on /docs): files
// only; directories and missing files get Tornado's 404.
func docsHandler(dir string) http.Handler {
	root := http.Dir(dir)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, err := root.Open(path.Clean("/" + r.URL.Path))
		if err != nil {
			notFound(w, r)
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil || st.IsDir() {
			notFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=31536000")
		http.ServeContent(w, r, st.Name(), st.ModTime(), f)
	})
}
