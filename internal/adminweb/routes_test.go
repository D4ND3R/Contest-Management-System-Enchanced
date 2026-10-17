// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package adminweb

import (
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/web"
)

// TestRoutesDeclarePermissions walks the route table: every route declares
// its permission, no route that changes data is public (but login and
// logout), and the table has every route of CMS's AdminWebServer.
func TestRoutesDeclarePermissions(t *testing.T) {
	ts := newTestSite(t, testOptions{})
	routes := ts.S.routeTab
	if err := checkRoutes(routes); err != nil {
		t.Fatal(err)
	}
	if err := checkRoutes(append(routes[:1:1], Route{Method: "POST", Path: "/x", Handler: ts.S.loginPage})); err == nil {
		t.Error("a route without a permission was accepted")
	}
	if err := checkRoutes([]Route{{Method: "POST", Path: "/x", Perm: PermPublic, Handler: ts.S.loginPage}}); err == nil {
		t.Error("a public POST route was accepted")
	}
	for _, rt := range routes {
		if rt.Method != "GET" && rt.Perm == PermRead && !readOnlyPost[rt.Path] {
			t.Errorf("%s %s changes data with the read permission", rt.Method, rt.Path)
		}
	}
	have := map[string]bool{}
	for _, rt := range routes {
		have[rt.Path] = true
	}
	// The paths of cms/server/admin/handlers/__init__.py (HANDLERS), with
	// the optional groups expanded.
	for _, p := range upstreamPaths {
		if !have[p] {
			t.Errorf("CMS route %s is missing", p)
		}
	}
}

// readOnlyPost are the routes that are not GET but change nothing (CMS
// gives them the "authenticated" level).
var readOnlyPost = map[string]bool{
	"/rpc/{service}/{shard}/{method}": true, // each method has its permission
	"/render_markdown":                true,
	"/contests":                       true, // redirects to the removal page
	"/users":                          true,
	"/teams":                          true,
	"/tasks":                          true,
	"/dataset/{dataset_id}/testcases/download": true,
}

var upstreamPaths = []string{
	"/{$}", "/login", "/logout", "/resourceslist", "/resources", "/resources/{shard}",
	"/resources/{shard}/{contest_id}", "/notifications", "/file/{digest}/{filename}", "/render_markdown",
	"/contests", "/contests/{contest_id}/remove", "/contests/add", "/contest/{contest_id}",
	"/contest/{contest_id}/overview", "/contest/{contest_id}/resourceslist",
	"/contest/{contest_id}/users", "/contest/{contest_id}/users/add",
	"/contest/{contest_id}/user/{user_id}/remove", "/contest/{contest_id}/user/{user_id}/edit",
	"/contest/{contest_id}/user/{user_id}/message",
	"/contest/{contest_id}/groups", "/contest/{contest_id}/groups/add", "/contest/{contest_id}/group/{group_id}/edit",
	"/contest/{contest_id}/tasks", "/contest/{contest_id}/tasks/add",
	"/contest/{contest_id}/submissions", "/contest/{contest_id}/user_tests",
	"/contest/{contest_id}/announcements", "/contest/{contest_id}/announcements/add",
	"/contest/{contest_id}/announcement/{announcement_id}",
	"/contest/{contest_id}/questions", "/contest/{contest_id}/question/{question_id}/reply",
	"/contest/{contest_id}/question/{question_id}/ignore", "/contest/{contest_id}/question/{question_id}/claim",
	"/contest/{contest_id}/ranking", "/contest/{contest_id}/ranking/{format}",
	"/tasks", "/tasks/{task_id}/remove", "/tasks/add", "/task/{task_id}", "/task/{task_id}/add_dataset",
	"/task/{task_id}/statements/add", "/task/{task_id}/statement/{statement_id}",
	"/task/{task_id}/attachments/add", "/task/{task_id}/attachment/{attachment_id}",
	"/dataset/{dataset_id}", "/dataset/{dataset_id}/clone", "/dataset/{dataset_id}/rename",
	"/dataset/{dataset_id}/delete", "/dataset/{dataset_id}/activate", "/dataset/{dataset_id}/autojudge",
	"/dataset/{dataset_id}/managers/add", "/dataset/{dataset_id}/manager/{manager_id}/delete",
	"/dataset/{dataset_id}/testcases/add", "/dataset/{dataset_id}/testcases/add_multiple",
	"/dataset/{dataset_id}/testcase/{testcase_id}/delete", "/dataset/{dataset_id}/testcases/download",
	"/users", "/users/{user_id}/remove", "/teams", "/teams/{team_id}/remove", "/users/add", "/teams/add",
	"/user/{user_id}", "/team/{team_id}", "/user/{user_id}/add_participation", "/user/{user_id}/edit_participation",
	"/admins", "/admins/add", "/admin/{admin_id}",
	"/submission/{submission_id}", "/submission/{submission_id}/{dataset_id}",
	"/submission/{submission_id}/comment", "/submission/{submission_id}/{dataset_id}/comment",
	"/submission/{submission_id}/official", "/submission/{submission_id}/{dataset_id}/official",
	"/submission_file/{file_id}", "/submission_diff/{old_id}/{new_id}",
	"/user_test/{user_test_id}", "/user_test/{user_test_id}/{dataset_id}", "/user_test_file/{file_id}",
	"/rpc/{service}/{shard}/{method}",
}

// samplePath fills the path values of a route with values that match its
// rules but name nothing that exists (so allowed handlers stop at 404 or
// their stub's 501 and never change data).
func samplePath(p string) string {
	values := map[string]string{"digest": "ab", "filename": "x.txt", "format": "txt", "shard": "0",
		"service": "EvaluationService", "method": "queue_status"}
	p = strings.TrimSuffix(p, "{$}")
	return wildcardsRe.ReplaceAllStringFunc(p, func(m string) string {
		name := wildcardsRe.FindStringSubmatch(m)[1]
		if v, ok := values[name]; ok {
			return v
		}
		return "999999"
	})
}

// TestRolesEnforcement sends every route of the table as every kind of
// admin (and logged out): a route answers 403 exactly to the admins
// without its permission; logged-out requests go to the login page (GET)
// or get 403.
func TestRolesEnforcement(t *testing.T) {
	ts := newTestSite(t, testOptions{})
	clients := map[string]*testClient{"": ts.Client()}
	for _, name := range []string{"admin", "msg", "prep", "ro"} {
		c := ts.Client()
		c.Login(name)
		clients[name] = c
	}
	admins := map[string]*Admin{}
	for name := range clients {
		if name == "" {
			continue
		}
		a, err := ts.S.st.Q.GetAdminByUsername(t.Context(), name)
		if err != nil {
			t.Fatal(err)
		}
		admins[name] = &Admin{Admin: a}
	}
	for _, rt := range ts.S.routeTab {
		if rt.Path == "/logout" || rt.Path == "/login" || rt.Path == "/events" || rt.Path == "/contest/{contest_id}/events" {
			continue
		}
		path := samplePath(rt.Path)
		for name, c := range clients {
			var r *testResponse
			switch rt.Method {
			case "GET":
				r = c.Get(path)
			case "POST":
				if strings.HasPrefix(rt.Path, "/rpc/") {
					r = c.RPC("EvaluationService", "queue_status", map[string]any{})
				} else {
					r = c.Post(path, url.Values{})
				}
			case "DELETE":
				r = c.Delete(path)
			}
			label := rt.Method + " " + path + " as " + name
			if name == "" {
				want := http.StatusForbidden
				if rt.Method == "GET" {
					want = http.StatusFound
				}
				if r.Status != want {
					t.Errorf("%s: %d, want %d", label, r.Status, want)
				}
				continue
			}
			allowed := admins[name].Can(rt.Perm)
			if allowed && r.Status == http.StatusForbidden {
				t.Errorf("%s: 403 although the admin has %s", label, rt.Perm)
			}
			if !allowed && r.Status != http.StatusForbidden {
				t.Errorf("%s: %d, want 403 (requires %s)", label, r.Status, rt.Perm)
			}
		}
	}
	// SelfAllowed: an admin without permission_all edits themself.
	ro := clients["ro"]
	id := itoa(ts.Admins["ro"])
	r := ro.Post("/admin/"+id, url.Values{"username": {"ro"}, "name": {"Read Only"}, "permission_all": {"on"}})
	if r.Status != http.StatusFound || r.Location() != "../admins" {
		t.Fatalf("self edit: %d %q", r.Status, r.Location())
	}
	if n := ts.QueryInt(`SELECT count(*) FROM admins WHERE username = 'ro' AND name = 'Read Only' AND NOT permission_all`); n != 1 {
		t.Fatal("the self edit did not change the name, or changed the permissions")
	}
	if r := ro.Post("/admin/"+itoa(ts.Admins["msg"]), url.Values{"username": {"msg"}, "name": {"x"}}); r.Status != http.StatusForbidden {
		t.Fatalf("editing another admin without permission_all: %d", r.Status)
	}
}

// TestCSRF checks that every route that is not GET refuses a request
// without the _xsrf token (or with a wrong one), before anything else.
func TestCSRF(t *testing.T) {
	ts := newTestSite(t, testOptions{})
	c := ts.Client()
	c.Login("admin")
	for _, rt := range ts.S.routeTab {
		if rt.Method == "GET" {
			continue
		}
		path := samplePath(rt.Path)
		for _, token := range []string{"", "2|00000000|0f0e0d0c0b0a09080706050403020100|0"} {
			req, _ := http.NewRequest(rt.Method, ts.URL+path, strings.NewReader("a=b"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if token != "" {
				req.Header.Set("X-XSRFToken", token)
			}
			r := c.Do(req)
			if r.Status != http.StatusForbidden || r.Body != web.CriticalError {
				t.Errorf("%s %s with token %q: %d %q", rt.Method, path, token, r.Status, r.Body)
			}
		}
	}
	// A request from another site is refused even with the token.
	req, _ := http.NewRequest("POST", ts.URL+"/server", strings.NewReader("_xsrf="+url.QueryEscape(c.XSRF())))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	if r := c.Do(req); r.Status != http.StatusForbidden {
		t.Errorf("cross-site POST: %d", r.Status)
	}
}

// TestPathRules checks CMS's URL patterns: ids that are not [0-9]+ (or do
// not fit in 32 bits) and other methods.
func TestPathRules(t *testing.T) {
	ts := newTestSite(t, testOptions{})
	c := ts.Client()
	c.Login("admin")
	for _, p := range []string{"/admin/x", "/admin/-1", "/admin/99999999999", "/file/XYZ/a", "/file/ab/a b",
		"/contest/1/ranking/TXT", "/resources/x", "/nonexistent", "/static/nonexistent.js"} {
		if r := c.Get(p); r.Status != http.StatusNotFound {
			t.Errorf("GET %s: %d", p, r.Status)
		}
	}
	notFound := regexp.MustCompile(`^<html><title>404: Not Found</title>`)
	if r := c.Get("/admin/x"); !notFound.MatchString(r.Body) {
		t.Errorf("Tornado's 404: %q", r.Body)
	}
	req, _ := http.NewRequest("PUT", ts.URL+"/admins", nil)
	req.Header.Set("X-XSRFToken", c.XSRF())
	r := c.Do(req)
	if r.Status != http.StatusMethodNotAllowed || r.Header.Get("Allow") != "GET" || !strings.Contains(r.Body, "Error 405") {
		t.Errorf("PUT /admins: %d %q", r.Status, r.Header.Get("Allow"))
	}
	req, _ = http.NewRequest("HEAD", ts.URL+"/admins", nil)
	if r := c.Do(req); r.Status != http.StatusMethodNotAllowed {
		t.Errorf("HEAD /admins: %d", r.Status)
	}
	// Stubs answer 501 with the error page.
	if r := c.Get("/contests"); r.Status != http.StatusNotImplemented && r.Status != http.StatusOK {
		t.Errorf("GET /contests: %d", r.Status)
	}
	resp, err := http.Get(ts.URL + "/static/aws_utils.js")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), "CMS.AWSUtils") {
		t.Errorf("static file: %d", resp.StatusCode)
	}
}
