// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package contestweb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/web"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/web/webtest"
	"github.com/d4nd3r/contest-management-system-enchanced/locale"
)

// normalizePage replaces what changes between requests and the
// documented differences of the fork (testdata/cms/README).
func normalizePage(s string, ours bool) string {
	rep := func(re, with string) { s = regexp.MustCompile(re).ReplaceAllString(s, with) }
	rep(`\?h=[0-9a-f]+`, "?h=H")
	rep(`name="_xsrf" value="[^"]*"`, `name="_xsrf" value="X"`)
	// Times in the arguments of CMS.CWSUtils.
	rep(`(?m)^\s+(-?\d+(\.\d+)?)\s*([,)])`, "    NUM$3")
	if ours {
		rep(`(?m)^\s*<script src="[./]*static/(cws_handlers|live)\.js[^"]*"></script>\n`, "")
		rep(`<script nonce="[^"]*">`, "<script>")
		rep(` <span id="live_indicator"[^>]*>&nbsp;</span>`, "")
		rep(`(?m)^\s*<a href="[./]*source" rel="noreferrer" target="_blank">Source code</a>\.\n`, "")
		rep(`(?m)^\s*CMS\.live\.on\(.*\n`, "")
		s = strings.ReplaceAll(s, "if (CMS.live.polling()) { utils.update_notifications(); }", "utils.update_notifications();")
		// The outcome of the seccomp filter (R2.2) is new.
		rep(`(?s)\n    <tr>\n      <td><em>Execution killed because of a forbidden system call</em></td>.*?</tr>`, "")
		// html/template's escaping: the same text in the browser.
		s = strings.NewReplacer(`\/`, "/", "\\u002b", "+", "&#43;", "+").Replace(s)
	} else {
		s = strings.ReplaceAll(s, ` onchange="utils.switch_lang()"`, "")
		// html/template drops the comments of scripts.
		rep(`(?m)^(\s*)// .*$`, "$1")
	}
	rep(`(?m)[ \t]+$`, "")
	return s
}

// firstDiff describes the first line where two texts differ.
func firstDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		var a, b string
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			b = g[i]
		}
		if a != b {
			return "line " + strconv.Itoa(i+1) + ":\nCMS:  " + a + "\nours: " + b
		}
	}
	return ""
}

// TestCMSPages compares the pages with those the original CMS serves for
// the same contest (testdata/cms).
func TestCMSPages(t *testing.T) {
	t.Parallel()
	docs := t.TempDir()
	ts := newTestSite(t, testOptions{Configure: func(c *config.Config) { c.ContestWeb.DocsPath = docs }})
	check := func(file string, r *testResponse, status int) {
		t.Helper()
		want, err := os.ReadFile(filepath.Join("testdata", "cms", file))
		if err != nil {
			t.Fatal(err)
		}
		if r.Status != status {
			t.Errorf("%s: status %d, want %d", file, r.Status, status)
		}
		w, g := normalizePage(string(want), false), normalizePage(r.Body, true)
		if w != g {
			t.Errorf("%s differs from CMS at %s", file, firstDiff(w, g))
		}
	}
	anon := ts.Client()
	check("contest_list.html", anon.Get("/"), http.StatusOK)
	check("login.html", anon.Get("/demo"), http.StatusOK)
	check("login_error.html", anon.Get("/demo?login_error=true&next=%2Fdemo%2Fdocumentation"), http.StatusOK)
	check("error_404.html", anon.Get("/nosuch"), http.StatusNotFound)

	alice := ts.Client()
	alice.Login("alice", "demo")
	check("overview.html", alice.Get("/demo"), http.StatusOK)
	check("documentation.html", alice.Get("/demo/documentation"), http.StatusOK)

	ts.Exec(`UPDATE contests SET allow_registration = true`)
	ts.Exec(`INSERT INTO teams (code, name) VALUES ('ita', 'Italy'), ('arg', 'Argentina')`)
	check("register.html", anon.Get("/demo/register"), http.StatusOK)
	check("login_register.html", anon.Get("/demo"), http.StatusOK)
}

// TestOverviewPhases checks the texts of the overview in the phases of
// the contest (the reference comparisons cover the exact markup). As in
// CMS, the tasks are listed from the start on (actual phase >= 0, also
// after the contest) and the testing page only while competing.
func TestOverviewPhases(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{})
	c := ts.Client()
	c.Login("alice", "demo")
	at := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
	ts.Exec(`UPDATE groups SET analysis_enabled = true, analysis_start = '2030-02-01', analysis_stop = '2030-03-01'`)
	for _, step := range []struct {
		now      time.Time
		want     []string
		tasks    bool
		testing_ bool
	}{
		{at(2024, 6, 1), []string{"The contest hasn't started yet.", "The contest will start at Jan 1, 2025, 12:00:00\u202fAM and will end at Jan 1, 2030, 12:00:00\u202fAM.", "utils.update_time(false)"}, false, false},
		{at(2026, 6, 1), []string{"The contest is currently running.", "The contest started at"}, true, true},
		{at(2030, 1, 15), []string{"The contest has already ended.", "The analysis mode hasn't started yet.", "The analysis mode will start at Feb 1, 2030"}, true, false},
		{at(2030, 2, 15), []string{"The analysis mode is currently running."}, true, false},
		{at(2030, 6, 1), []string{"The analysis mode has already ended.", "The analysis mode started at Feb 1, 2030, 12:00:00\u202fAM and ended at Mar 1, 2030"}, true, false},
	} {
		ts.Clock.Set(step.now)
		c.Login("alice", "demo") // the cookie is from another time
		r := c.Get("/demo")
		for _, w := range step.want {
			if !strings.Contains(r.Body, w) {
				t.Errorf("at %s: no %q", step.now.Format(time.DateOnly), w)
			}
		}
		if got := strings.Contains(r.Body, "Task overview") && strings.Contains(r.Body, "tasks/sum/description"); got != step.tasks {
			t.Errorf("at %s: tasks shown %v, want %v", step.now.Format(time.DateOnly), got, step.tasks)
		}
		if got := strings.Contains(r.Body, `href="./demo/testing"`); got != step.testing_ {
			t.Errorf("at %s: testing link %v, want %v", step.now.Format(time.DateOnly), got, step.testing_)
		}
	}

	// Unrestricted participants see the tasks in every phase.
	ts.Exec(`UPDATE participations SET unrestricted = true WHERE id = $1`, ts.Participation("alice"))
	ts.Clock.Set(at(2024, 6, 1))
	c.Login("alice", "demo")
	if r := c.Get("/demo"); !strings.Contains(r.Body, "Task overview") || !strings.Contains(r.Body, `href="./demo/testing"`) {
		t.Error("an unrestricted participant does not see the tasks before the contest")
	}
}

// TestUsacoStart drives a contest with a time frame per participant
// (per_user_time): the Start! button, the start, the end of the frame.
func TestUsacoStart(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{})
	ts.Exec(`UPDATE groups SET per_user_time = '2 hours'`)
	c := ts.Client()
	c.Login("alice", "demo")
	r := c.Get("/demo")
	for _, w := range []string{"uninterrupted time frame of 2 hours.", "By clicking on the button below you can start your time frame.",
		`action="./demo/start"`, "Start!", "utils.update_time(true)", `<div class="span5">`} {
		if !strings.Contains(r.Body, w) {
			t.Errorf("before the start: no %q", w)
		}
	}
	if strings.Contains(r.Body, "Task overview") {
		t.Error("tasks shown before the start of the time frame")
	}
	// The tasks need the start (redirect to the overview).
	if r = c.Get("/demo/tasks/sum/description"); r.Status != http.StatusFound || r.Location() != "../../../demo" {
		t.Errorf("task before the start: %d %q", r.Status, r.Location())
	}

	r = c.Post("/demo/start", url.Values{"next": {"./demo"}})
	if r.Status != http.StatusFound || r.Location() != "../demo" {
		t.Fatalf("start: %d %q", r.Status, r.Location())
	}
	var started time.Time
	if err := ts.Pool.QueryRow(context.Background(), `SELECT starting_time FROM participations WHERE id = $1`,
		ts.Participation("alice")).Scan(&started); err != nil || !started.Equal(demoNow) {
		t.Fatalf("starting_time %v, %v; want %v", started, err, demoNow)
	}
	if ev := ts.Events.Events(); len(ev) != 1 || ev[0].Name != live.EventParticipation {
		t.Errorf("events after the start: %+v", ev)
	}
	r = c.Get("/demo")
	if !strings.Contains(r.Body, "You started your time frame at") || !strings.Contains(r.Body, "Task overview") {
		t.Errorf("after the start:\n%s", r.Body)
	}
	// A second start does not move it: the phase is not -1 any more.
	ts.Clock.Add(time.Minute)
	if r = c.Post("/demo/start", nil); r.Status != http.StatusFound {
		t.Errorf("second start: %d", r.Status)
	}
	var again time.Time
	_ = ts.Pool.QueryRow(context.Background(), `SELECT starting_time FROM participations WHERE id = $1`, ts.Participation("alice")).Scan(&again)
	if !again.Equal(started) {
		t.Errorf("a second start moved the starting time to %v", again)
	}
	// After the frame.
	ts.Clock.Add(3 * time.Hour)
	c.Login("alice", "demo")
	r = c.Get("/demo")
	if !strings.Contains(r.Body, "and you already finished it.") || !strings.Contains(r.Body, "There's nothing you can do now.") {
		t.Errorf("after the time frame:\n%s", r.Body)
	}
}

// TestAccessRules checks the answers of CMS's decorators and router.
func TestAccessRules(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{})
	anon := ts.Client()
	for _, c := range []struct {
		method, path string
		status       int
		location     string
		body         string
	}{
		// tornado.web.authenticated: GET goes to the login form.
		{"GET", "/demo/documentation", 302, "../demo?next=%2Fdemo%2Fdocumentation", ""},
		{"GET", "/demo/documentation?a=b&c=%C3%A9", 302, "../demo?next=%2Fdemo%2Fdocumentation%3Fa%3Db%26c%3D%25C3%25A9", ""},
		{"GET", "/demo/tasks/sum/description", 302, "../../../demo?next=%2Fdemo%2Ftasks%2Fsum%2Fdescription", ""},
		// api_login_required: JSON.
		{"GET", "/demo/notifications", 403, "", `{"error":"An authenticated user is required"}`},
		{"GET", "/demo/events", 403, "", `{"error":"An authenticated user is required"}`},
		// Other methods: 405 with the error page.
		{"GET", "/demo/login", 405, "", "Error 405"},
		{"GET", "/demo/start", 405, "", "Error 405"},
		{"OPTIONS", "/demo", 405, "", "Error 405"},
		// As in CMS (Tornado), the CSRF check comes first.
		{"PUT", "/demo", 403, "", "A critical error has occurred :-("},
		{"DELETE", "/demo/tasks/sum/submissions", 403, "", "A critical error has occurred :-("},
		// Unknown contest: CMS's error page; unknown URL: Tornado's.
		{"GET", "/nosuch", 404, "", "Error 404"},
		{"GET", "/nosuch/documentation", 404, "", "Error 404"},
		{"GET", "/demo/nosuchpage", 404, "", "<html><title>404: Not Found</title><body>404: Not Found</body></html>"},
		{"GET", "/demo/", 404, "", "404: Not Found"},
		{"GET", "/static/nosuch.css", 404, "", "404: Not Found"},
		// Submission numbers are [1-9][0-9]*.
		{"GET", "/demo/tasks/sum/submissions/012/details", 404, "", "404: Not Found"},
		// Registration is off.
		{"GET", "/demo/register", 404, "", "Error 404"},
		// The common endpoints.
		{"GET", "/healthz", 200, "", `"status"`},
		// CMS's handlers have no head(): 405 after prepare (404 for an
		// unknown contest); static files serve HEAD.
		{"HEAD", "/", 405, "", ""},
		{"HEAD", "/demo", 405, "", ""},
		{"HEAD", "/demo/tasks/sum/description", 405, "", ""},
		{"HEAD", "/demo/api/task_list", 405, "", ""},
		{"HEAD", "/nosuch", 404, "", ""},
		{"HEAD", "/static/cws_utils.js", 200, "", ""},
		{"OPTIONS", "/demo", 405, "", "Error 405"},
	} {
		req, _ := http.NewRequest(c.method, ts.URL+c.path, nil)
		r := anon.Do(req)
		if r.Status != c.status || r.Location() != c.location || !strings.Contains(r.Body, c.body) {
			t.Errorf("%s %s: %d %q\n%.300s", c.method, c.path, r.Status, r.Location(), r.Body)
		}
	}
	if r := anon.Get("/demo/login"); r.Header.Get("Allow") != "POST" {
		t.Errorf("Allow: %q", r.Header.Get("Allow"))
	}
	if r := anon.Do(mustRequest(t, "OPTIONS", ts.URL+"/demo")); r.Header.Get("Allow") != "GET" {
		t.Errorf("Allow of a page: %q", r.Header.Get("Allow"))
	}
	if r := anon.Post("/demo/documentation", nil); r.Status != http.StatusMethodNotAllowed {
		t.Errorf("POST documentation: %d", r.Status)
	}
	if r := anon.Get("/source"); r.Status != http.StatusFound || !strings.HasPrefix(r.Location(), "https://") {
		t.Errorf("source: %d %q", r.Status, r.Location())
	}
	if r := anon.Post("/demo/start", nil); r.Status != http.StatusForbidden || !strings.Contains(r.Body, "Error 403") {
		t.Errorf("POST start without login: %d", r.Status)
	}

	// Phases: the start of a traditional contest redirects to the
	// overview; the routes not written yet answer 501.
	alice := ts.Client()
	alice.Login("alice", "demo")
	if r := alice.Post("/demo/start", nil); r.Status != http.StatusFound || r.Location() != "../demo" {
		t.Errorf("start in a traditional contest: %d %q", r.Status, r.Location())
	}
	ts.Clock.Set(time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC))
	alice.Login("alice", "demo")
	if r := alice.Get("/demo/testing"); r.Status != http.StatusFound || r.Location() != "../demo" {
		t.Errorf("testing after the contest: %d %q", r.Status, r.Location())
	}
}

// TestSingleContest serves one contest at "/" (contest_web.contest).
func TestSingleContest(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{Configure: func(c *config.Config) { c.ContestWeb.Contest = "demo" }})
	c := ts.Client()
	r := c.Get("/")
	if r.Status != http.StatusOK || !strings.Contains(r.Body, `<form class="form-horizontal" action="./login" method="POST">`) {
		t.Fatalf("contest at /: %d\n%s", r.Status, r.Body)
	}
	r = c.Post("/login", url.Values{"username": {"alice"}, "password": {"demo"}})
	if r.Status != http.StatusFound || r.Location() != "." {
		t.Fatalf("login: %d %q", r.Status, r.Location())
	}
	r = c.Get("/documentation")
	if r.Status != http.StatusOK || !strings.Contains(r.Body, `<a href="./documentation">Documentation</a>`) || !strings.Contains(r.Body, `data-live-url="./events"`) {
		t.Fatalf("documentation: %d\n%s", r.Status, r.Body)
	}
	if r = c.Get("/demo"); r.Status != http.StatusNotFound {
		t.Errorf("/demo in single-contest mode: %d", r.Status)
	}
	if r = c.Get("/static/cws_style.css"); r.Status != http.StatusOK {
		t.Errorf("static: %d", r.Status)
	}
	// A contest that does not exist: every page is the 404 page.
	ts2 := newTestSite(t, testOptions{Configure: func(c *config.Config) { c.ContestWeb.Contest = "nosuch" }})
	if r = ts2.Client().Get("/"); r.Status != http.StatusNotFound {
		t.Errorf("missing contest: %d", r.Status)
	}
}

// TestContestList lists the contests by name, grey when over.
func TestContestList(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{})
	ts.Exec(`INSERT INTO contests (name, description) VALUES ('archive', 'Old <contest>')`)
	ts.Exec(`INSERT INTO groups (contest_id, name, start, stop) SELECT id, 'default', '2020-01-01', '2020-01-02' FROM contests WHERE name = 'archive'`)
	ts.Exec(`UPDATE contests c SET main_group_id = g.id FROM groups g WHERE g.contest_id = c.id AND c.name = 'archive'`)
	r := ts.Client().Get("/")
	want := `            <li>
                <a  style="color: #adadad"  href="./archive">Old &lt;contest&gt;</a>
            </li>
            <li>
                <a  href="./demo">Demo Contest</a>
            </li>`
	if r.Status != http.StatusOK || !strings.Contains(r.Body, want) {
		t.Fatalf("contest list: %d\n%s", r.Status, r.Body)
	}
}

// TestLanguageChoice: the selector, the cookie and the contest's allowed
// languages.
func TestLanguageChoice(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{})
	c := ts.Client()
	r := c.Get("/demo", "Accept-Language", "es-CL,es;q=0.9")
	if r.Header.Get("Content-Language") != "es-CL" || !strings.Contains(r.Body, "Bienvenido") ||
		!strings.Contains(r.Body, `<option value="" selected>Automático (español (Chile))</option>`) {
		t.Fatalf("Accept-Language es-CL: %q\n%s", r.Header.Get("Content-Language"), r.Body)
	}
	u, _ := url.Parse(ts.URL + "/demo")
	c.HTTP.Jar.SetCookies(u, []*http.Cookie{{Name: "language", Value: "it", Path: "/demo"}})
	r = c.Get("/demo", "Accept-Language", "es")
	if r.Header.Get("Content-Language") != "it" || !strings.Contains(r.Body, `<option value="it" selected>italiano</option>`) {
		t.Fatalf("language cookie: %q", r.Header.Get("Content-Language"))
	}
	// A contest limited to some languages (prefixes: "es" is es and
	// es-CL), without a selector when only one is left.
	ts.Exec(`UPDATE contests SET allowed_localizations = '{es}'`)
	r = c.Get("/demo", "Accept-Language", "en")
	if r.Header.Get("Content-Language") != "es" || !strings.Contains(r.Body, `<option value="es-CL">`) || strings.Contains(r.Body, `value="it"`) {
		t.Fatalf("allowed_localizations es: %q", r.Header.Get("Content-Language"))
	}
	ts.Exec(`UPDATE contests SET allowed_localizations = '{fr}'`)
	r = c.Get("/demo", "Accept-Language", "en")
	if r.Header.Get("Content-Language") != "fr" || strings.Contains(r.Body, `<select id="lang"`) {
		t.Fatalf("allowed_localizations: %q", r.Header.Get("Content-Language"))
	}
	// New strings of the fork come from the admin catalogs.
	ts.Exec(`UPDATE contests SET allowed_localizations = '{}'`)
	c = ts.Client()
	c.Header.Set("Accept-Language", "es")
	c.Login("alice", "demo")
	r = c.Get("/demo")
	if !strings.Contains(r.Body, "Código fuente") || !strings.Contains(r.Body, `title="Actualizaciones en vivo: conectando"`) {
		t.Error("the strings of the fork are not translated")
	}
}

// TestRegister runs the cases compared with CMS (statuses of every
// outcome) and checks the rows.
func TestRegister(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{})
	ts.Exec(`UPDATE contests SET allow_registration = true`)
	ts.Exec(`INSERT INTO users (first_name, last_name, username, password) VALUES ('Dave', 'D', 'dave', 'plaintext:davepw')`)
	c := ts.Client()
	post := func(f url.Values) *testResponse { return c.Post("/demo/register", f) }
	user := func(username, password string) url.Values {
		return url.Values{"new_user": {"true"}, "first_name": {"Eve"}, "last_name": {"E"}, "username": {username},
			"password": {password}, "email": {""}}
	}
	if r := post(user("eve1", "secret1")); r.Status != http.StatusOK || r.Body != "eve1" {
		t.Fatalf("new user: %d %q", r.Status, r.Body)
	}
	var hash string
	var group, mainGroup int32
	if err := ts.Pool.QueryRow(context.Background(), `SELECT u.password, p.group_id, c.main_group_id FROM users u
		JOIN participations p ON p.user_id = u.id JOIN contests c ON c.id = p.contest_id WHERE u.username = 'eve1'`).
		Scan(&hash, &group, &mainGroup); err != nil {
		t.Fatal(err)
	}
	if ok, _ := ValidatePassword(hash, "secret1"); !ok || !strings.HasPrefix(hash, "bcrypt:") || group != mainGroup {
		t.Errorf("registered user: %q, group %d (main %d)", hash, group, mainGroup)
	}
	if ev := ts.Events.Events(); len(ev) != 1 || ev[0].Name != live.EventParticipation {
		t.Errorf("events: %+v", ev)
	}
	join := func(username, password string) url.Values {
		return url.Values{"new_user": {"false"}, "username": {username}, "password": {password}}
	}
	long := user("eve8", "secret1")
	long.Set("first_name", strings.Repeat("x", 51))
	noEmail := user("eve7", "secret1")
	noEmail.Del("email")
	for _, cs := range []struct {
		name   string
		form   url.Values
		status int
	}{
		{"same again", user("eve1", "secret1"), 409},
		{"short password", user("eve4", "12345"), 400},
		{"bad username", user("eve 5", "secret1"), 400},
		{"long name", long, 400},
		{"missing email", noEmail, 400},
		{"join already", join("carol", "demo"), 409},
		{"join wrong password", join("dave", "nope"), 403},
		{"join no user", join("nobody", "x"), 404},
		{"no new_user", url.Values{"username": {"dave"}, "password": {"davepw"}}, 400},
		{"join ok", join("dave", "davepw"), 200},
		{"join ok again", join("dave", "davepw"), 409},
	} {
		if r := post(cs.form); r.Status != cs.status {
			t.Errorf("%s: %d, want %d (%.100s)", cs.name, r.Status, cs.status, r.Body)
		}
	}
	// With teams, the team is mandatory.
	ts.Exec(`INSERT INTO teams (code, name) VALUES ('ita', 'Italy')`)
	if r := post(user("eve9", "secret1")); r.Status != http.StatusBadRequest {
		t.Errorf("without a team: %d", r.Status)
	}
	f := user("eve9", "secret1")
	f.Set("team", "zzz")
	if r := post(f); r.Status != http.StatusBadRequest {
		t.Errorf("unknown team: %d", r.Status)
	}
	f.Set("team", "ita")
	if r := post(f); r.Status != http.StatusOK {
		t.Errorf("with a team: %d", r.Status)
	}
	// Registration closed: 404.
	ts.Exec(`UPDATE contests SET allow_registration = false`)
	if r := post(user("eve10", "secret1")); r.Status != http.StatusNotFound {
		t.Errorf("registration closed: %d", r.Status)
	}
}

// TestNotifications checks the JSON of CMS's polling and the site's
// notifications (Ctx.Notify).
func TestNotifications(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{})
	alice := ts.Participation("alice")
	ts.Exec(`INSERT INTO announcements (timestamp, subject, text, contest_id) VALUES ('2026-10-14 10:00:00.123456+00', 'Welcome <b>', 'Text', $1)`, ts.Contest)
	ts.Exec(`INSERT INTO announcements (timestamp, subject, text, contest_id) VALUES ('2026-10-16 10:00:00+00', 'Future', 'Not yet', $1)`, ts.Contest)
	ts.Exec(`INSERT INTO messages (timestamp, subject, text, participation_id) VALUES ('2026-10-14 11:00:00+00', 'Private', 'Hello', $1)`, alice)
	ts.Exec(`INSERT INTO messages (timestamp, subject, text, participation_id) VALUES ('2026-10-14 11:00:00+00', 'Other', 'Not for alice', $1)`, ts.Participation("bob"))
	ts.Exec(`INSERT INTO questions (question_timestamp, subject, text, reply_timestamp, reply_subject, reply_text, participation_id)
		VALUES ('2026-10-14 09:00:00+00', 'Q1', 'What?', '2026-10-14 12:00:00+00', NULL, 'Answer text', $1),
		       ('2026-10-14 09:30:00+00', 'Q2', 'Why?', '2026-10-14 12:30:00+00', 'Yes', 'Because', $1),
		       ('2026-10-14 09:40:00+00', 'Q3', 'Unanswered', NULL, NULL, NULL, $1)`, alice)
	c := ts.Client()
	c.Login("alice", "demo")
	r := c.Get("/demo/notifications")
	want := `[{"type":"announcement","timestamp":1791972000.123456,"subject":"Welcome \u003cb\u003e","text":"Text"},` +
		`{"type":"message","timestamp":1791975600.0,"subject":"Private","text":"Hello"},` +
		`{"type":"question","timestamp":1791979200.0,"subject":"Answer text","text":""},` +
		`{"type":"question","timestamp":1791981000.0,"subject":"Yes","text":"Because"}]`
	if r.Status != http.StatusOK || r.Body != want {
		t.Fatalf("notifications: %d\n got %s\nwant %s", r.Status, r.Body, want)
	}
	if r = c.Get("/demo/notifications?last_notification=1791979200.0"); r.Body != `[{"type":"question","timestamp":1791981000.0,"subject":"Yes","text":"Because"}]` {
		t.Errorf("after the third: %s", r.Body)
	}
	if r = c.Get("/demo/notifications?last_notification=1791981000"); r.Body != "[]" {
		t.Errorf("after the last: %s", r.Body)
	}
	if r = c.Get("/demo/notifications?last_notification=x"); r.Status != http.StatusBadRequest {
		t.Errorf("bad last_notification: %d", r.Status)
	}

	// Notifications of the site are delivered once, with the next poll,
	// translated and formatted.
	ctx := &Ctx{S: ts.S, Now: demoNow, Lang: ts.S.ui.Get("it"), R: (&http.Request{}).WithContext(context.Background())}
	ctx.Participation, _ = ts.S.participationByUsername(context.Background(), ts.Contest, "alice")
	ctx.NotifySuccess("Submission received", "Your submission has been received and is currently being evaluated.", nil)
	ctx.NotifyError("Too many submissions!", "You have reached the maximum limit of at most %d submissions among all tasks.", []any{5})
	r = c.Get("/demo/notifications?last_notification=1791981000")
	var got []Communication
	if err := json.Unmarshal([]byte(r.Body), &got); err != nil || len(got) != 2 {
		t.Fatalf("notifications: %s (%v)", r.Body, err)
	}
	if got[0].Type != "notification" || got[0].Level != "success" || got[0].Subject != ctx.T("Submission received") ||
		got[0].Subject == "Submission received" || float64(got[0].Timestamp) != float64(demoNow.Unix()) {
		t.Errorf("first notification: %+v", got[0])
	}
	if got[1].Level != "error" || !strings.Contains(got[1].Text, "5") {
		t.Errorf("second notification: %+v", got[1])
	}
	if r = c.Get("/demo/notifications?last_notification=1791981000"); r.Body != "[]" {
		t.Errorf("notifications delivered twice: %s", r.Body)
	}
}

// TestTemplates renders every page without data (no function may crash)
// and checks that the site's templates parse with the functions.
func TestTemplates(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{NoImport: true})
	webtest.RenderPages(t, ts.S.site.Engine, webtest.Options{})
	for _, p := range []string{"base.html", "contest.html", "contest_list.html", "overview.html",
		"documentation.html", "register.html", "error.html"} {
		if !ts.S.site.Engine.Has(p) {
			t.Errorf("no template %s", p)
		}
	}
	// The static files of CMS and of the fork.
	c := ts.Client()
	for _, p := range []string{"/static/cws_utils.js", "/static/cws_handlers.js", "/static/live.js", "/static/cws_style.css",
		"/static/css/bootstrap.css", "/static/js/bootstrap.js", "/static/img/mimetypes/text-x-generic.png",
		"/static/jq/jquery-3.6.0.min.js", "/static/favicon.ico", "/static/loading.gif"} {
		if r := c.Get(p); r.Status != http.StatusOK {
			t.Errorf("%s: %d", p, r.Status)
		}
	}
}

// TestDocs serves contest_web.docs_path under /docs and lists the
// documentation of the contest's languages.
func TestDocs(t *testing.T) {
	t.Parallel()
	docs := t.TempDir()
	if err := os.MkdirAll(filepath.Join(docs, "cpp"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docs, "cpp", "index.html"), []byte("<p>C++ docs</p>"), 0o644); err != nil {
		t.Fatal(err)
	}
	ts := newTestSite(t, testOptions{Configure: func(c *config.Config) { c.ContestWeb.DocsPath = docs }})
	c := ts.Client()
	if r := c.Get("/docs/cpp/index.html"); r.Status != http.StatusOK || r.Body != "<p>C++ docs</p>" {
		t.Errorf("docs file: %d %q", r.Status, r.Body)
	}
	if r := c.Get("/docs/"); r.Status != http.StatusNotFound {
		t.Errorf("docs listing: %d", r.Status)
	}
	if r := c.Get("/docs/../cms.yaml"); r.Status == http.StatusOK {
		t.Errorf("docs traversal: %d", r.Status)
	}
	c.Login("alice", "demo")
	r := c.Get("/demo/documentation")
	if !strings.Contains(r.Body, "<h3>C&#43;&#43;17 / g&#43;&#43;</h3>\n<p><a href=\"../docs/cpp/index.html\">Documentation</a></p>") ||
		!strings.Contains(r.Body, "<h3>Java</h3>") {
		t.Errorf("documentation page:\n%s", r.Body)
	}
}

// TestTaskScores reads the task_scores cache of the participant once.
func TestTaskScores(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{Counting: true})
	alice := ts.Participation("alice")
	ts.Exec(`DELETE FROM task_scores`)
	ts.Exec(`INSERT INTO task_scores (participation_id, task_id, score, public_score, tokened_score, partial)
		SELECT $1, id, 30, 10, 20, true FROM tasks WHERE name = 'sum'`, alice)
	var sum int32
	_ = ts.Pool.QueryRow(context.Background(), `SELECT id FROM tasks WHERE name = 'sum'`).Scan(&sum)
	p, _ := ts.S.participationByUsername(context.Background(), ts.Contest, "alice")
	c := &Ctx{S: ts.S, Participation: p, R: (&http.Request{}).WithContext(context.Background())}
	ts.Counter.Reset()
	for i := 0; i < 3; i++ {
		scores, err := c.TaskScores()
		if err != nil || len(scores) != 1 || scores[sum] != (TaskScore{Score: 30, PublicScore: 10, TokenedScore: 20, Partial: true}) {
			t.Fatalf("scores: %+v, %v", scores, err)
		}
	}
	if n := ts.Counter.Count(); n != 1 {
		t.Errorf("%d queries for the scores, want 1", n)
	}
}

// TestFuncs checks the template functions of the site.
func TestFuncs(t *testing.T) {
	t.Parallel()
	if got := FormatContestTime(83*time.Minute + 5*time.Second); got != "01:23:05" {
		t.Errorf("FormatContestTime: %q", got)
	}
	if got := FormatContestTime(-90 * time.Second); got != "-00:01:30" {
		t.Errorf("FormatContestTime negative: %q", got)
	}
	if got := FormatContestTime(30 * time.Hour); got != "30:00:00" {
		t.Errorf("FormatContestTime long: %q", got)
	}
	for _, c := range []struct {
		score, max float64
		want       string
	}{{0, 100, "score_0"}, {-1, 100, "score_0"}, {50, 100, "score_0_100"}, {100, 100, "score_100"}, {120, 100, "score_100"}, {0, 0, "score_0"}} {
		if got := GetScoreClass(c.score, c.max); got != c.want {
			t.Errorf("GetScoreClass(%v, %v) = %q", c.score, c.max, got)
		}
	}
}

// TestFormatTokenRules compares FormatTokenRules with CMS's
// format_token_rules (testdata/cms/token_rules.json), plural quirks
// included: "Every 0.5 minutes", and the forms Babel fills in for
// untranslated plural messages.
func TestFormatTokenRules(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("testdata", "cms", "token_rules.json"))
	if err != nil {
		t.Fatal(err)
	}
	var golden struct {
		Rows [][]any `json:"rows"`
	}
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatal(err)
	}
	p := func(n int32) *int32 { return &n }
	cases := []TokenRules{
		{Mode: TokenModeFinite, MaxNumber: p(10), GenInitial: 2, GenNumber: 1, GenInterval: 30 * time.Second, GenMax: p(5)},
		{Mode: TokenModeFinite, MinInterval: 90 * time.Second, GenInitial: 1, GenNumber: 3, GenInterval: time.Minute},
		{Mode: TokenModeFinite, MaxNumber: p(1), MinInterval: time.Second, GenInterval: 2 * time.Minute},
		{Mode: TokenModeFinite, GenInitial: 5, GenNumber: 2, GenInterval: 90 * time.Minute, GenMax: p(1)},
		{Mode: TokenModeInfinite, GenInterval: time.Minute},
		{Mode: TokenModeDisabled, GenInterval: time.Minute},
	}
	langs, err := web.NewLanguages(web.LanguagesOptions{Catalogs: locale.FS,
		Domains: []string{locale.DomainContest, locale.DomainAdmin}})
	if err != nil {
		t.Fatal(err)
	}
	if len(golden.Rows) != 7*len(cases)*3 {
		t.Fatalf("%d rows", len(golden.Rows))
	}
	for _, row := range golden.Rows {
		code, i, tType, want := row[0].(string), int(row[1].(float64)), row[2].(string), row[3].(string)
		l := langs.Get(code)
		if l == nil {
			t.Fatalf("no language %s", code)
		}
		if got := FormatTokenRules(cases[i], tType, l); got != want {
			t.Errorf("%s, case %d, %q:\n got %q\nwant %q", code, i, tType, got, want)
		}
	}
}

func mustRequest(t *testing.T, method, url string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}
