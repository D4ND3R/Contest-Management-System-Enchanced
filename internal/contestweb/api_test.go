// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package contestweb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
)

// apiStep is a step of testdata/api/cms.json: SQL to run, a request, and
// the answer of the original CMS.
type apiStep struct {
	Name   string            `json:"name"`
	Method string            `json:"method"`
	Path   string            `json:"path"`
	Auth   string            `json:"auth"`
	Form   map[string]string `json:"form"`
	Files  []struct {
		Field, Filename, Content string
	} `json:"files"`
	SQL   []string  `json:"sql"`
	Save  string    `json:"save"`
	Check string    `json:"check"`
	CMS   apiAnswer `json:"cms"`
}

// apiAnswer is an answer to a step (normalized as cms.json describes).
type apiAnswer struct {
	Status      int    `json:"status"`
	ContentType string `json:"content_type"`
	JSON        any    `json:"json"`
	Body        string `json:"body"`
	Check       string `json:"check"`
}

// apiDifferences are the answers of this site that differ from CMS on
// purpose (testdata/api/README): CMS fails (500) on the public details of
// a subtask with private testcases.
var apiDifferences = map[string]apiAnswer{
	"details: public, subtasks with private testcases":  {Status: 200, JSON: publicSumDetails},
	"details: before the contest, override_phase_check": {Status: 200, JSON: publicSumDetails},
}

// publicSumDetails are the public details of alice's submission on sum
// with restricted feedback: testcase 000 is public and shown, the others
// only by name; the subtasks have no score (private testcases).
var publicSumDetails = map[string]any{"details": []any{
	map[string]any{"idx": 0.0, "testcases": []any{
		map[string]any{"idx": "000", "outcome": "Correct", "text": []any{"Output is correct"}},
		map[string]any{"idx": "001"},
	}},
	map[string]any{"idx": 1.0, "testcases": []any{
		map[string]any{"idx": "002"},
		map[string]any{"idx": "003"},
	}},
}}

// apiRequest builds the request of a step: a multipart form for POST (as
// the recorder sends them), the login data in X-CMS-Authorization.
func apiRequest(t *testing.T, base string, s *apiStep, login string) *http.Request {
	t.Helper()
	var body io.Reader
	ctype := ""
	if s.Method == http.MethodPost {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		for k, v := range s.Form {
			_ = mw.WriteField(k, v)
		}
		for _, f := range s.Files {
			w, err := mw.CreateFormFile(f.Field, f.Filename)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.WriteString(w, f.Content)
		}
		_ = mw.Close()
		body, ctype = &buf, mw.FormDataContentType()
	}
	req, err := http.NewRequest(s.Method, base+s.Path, body)
	if err != nil {
		t.Fatal(err)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if login != "" {
		req.Header.Set(authHeader, login)
	}
	return req
}

// TestCMSAPI replays the requests recorded from the original CMS
// (testdata/api/cms.json) and compares the answers: status codes, JSON
// values and error messages, the login data, impersonation, phases, the
// feedback levels of the details and, for submissions, what is stored.
func TestCMSAPI(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("testdata", "api", "cms.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rec struct{ Steps []apiStep }
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	ts := newTestSite(t, testOptions{Configure: func(c *config.Config) { c.ContestWeb.AdminToken = "test-admin-token" }})
	details, err := os.ReadFile(filepath.Join("testdata", "api", "details.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range strings.Split(strings.TrimSpace(string(details)), ";\n") {
		ts.Exec(q)
	}

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	saved := map[string]string{"garbage": "garbage"}
	ids := map[string]string{}
	for i := range rec.Steps {
		s := &rec.Steps[i]
		for _, q := range s.SQL {
			ts.Exec(q)
		}
		resp, err := client.Do(apiRequest(t, ts.URL, s, saved[s.Auth]))
		if err != nil {
			t.Fatalf("%s: %v", s.Name, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		got := apiAnswer{Status: resp.StatusCode, ContentType: resp.Header.Get("Content-Type")}
		var v any
		if json.Unmarshal(body, &v) == nil {
			if m, ok := v.(map[string]any); ok {
				if ld, ok := m["login_data"].(string); ok {
					if s.Save != "" {
						saved[s.Save] = ld
					}
					if s.Auth != "" && ld == saved[s.Auth] {
						m["login_data"] = "<sent login_data>"
					} else {
						m["login_data"] = "<login_data>"
					}
				}
				if id, ok := m["id"].(string); ok && strings.Contains(s.Path, "/submit") {
					ids[id] = fmt.Sprintf("<id:%d>", len(ids))
					m["id"] = ids[id]
				}
				if list, ok := m["list"].([]any); ok {
					for _, it := range list {
						if item, ok := it.(map[string]any); ok {
							if n, ok := ids[fmt.Sprint(item["id"])]; ok {
								item["id"] = n
							}
						}
					}
				}
			}
			got.JSON = v
		} else {
			got.Body = string(body)
		}
		if s.Check != "" {
			if err := ts.Pool.QueryRow(context.Background(), s.Check).Scan(&got.Check); err != nil {
				t.Fatalf("%s: %s: %v", s.Name, s.Check, err)
			}
		}

		want, differs := apiDifferences[s.Name]
		if !differs {
			want = s.CMS
		}
		if got.Status != want.Status {
			t.Errorf("%s: status %d, want %d (%.200s)", s.Name, got.Status, want.Status, body)
			continue
		}
		if want.JSON != nil {
			if got.ContentType != "application/json; charset=utf-8" {
				t.Errorf("%s: Content-Type %q", s.Name, got.ContentType)
			}
			if !reflect.DeepEqual(got.JSON, want.JSON) {
				g, _ := json.Marshal(got.JSON)
				w, _ := json.Marshal(want.JSON)
				t.Errorf("%s:\n got %s\nwant %s", s.Name, g, w)
			}
		} else if want.Body != "" && !strings.HasPrefix(strings.Join(strings.Fields(got.Body), " "), want.Body[:min(len(want.Body), 30)]) {
			t.Errorf("%s: body %.100q, want %.100q", s.Name, got.Body, want.Body)
		}
		if got.Check != want.Check {
			t.Errorf("%s: stored %q, want %q", s.Name, got.Check, want.Check)
		}
	}
}

// TestAPILoginWithCookie: a browser logged in with the form gets its login
// cookie back as login_data, which works in the header; IP autologin gives
// CMS's placeholder.
func TestAPILoginWithCookie(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{})
	c := ts.Client()
	c.Login("alice", "demo")
	// No CSRF check on the API login, as in CMS.
	r := c.PostRaw("/demo/api/login", url.Values{"username": {"alice"}})
	var got map[string]string
	if err := json.Unmarshal([]byte(r.Body), &got); err != nil || r.Status != http.StatusOK || got["login_data"] == "" {
		t.Fatalf("login with the cookie: %d %s", r.Status, r.Body)
	}
	anon := ts.Client()
	if r := anon.Get("/demo/api/task_list", authHeader, got["login_data"]); r.Status != http.StatusOK || !strings.Contains(r.Body, `"name":"sum"`) {
		t.Errorf("task_list with the cookie's login_data: %d %s", r.Status, r.Body)
	}

	ts.Exec(`UPDATE contests SET ip_autologin = true`)
	ts.Exec(`UPDATE participations SET ip = '{127.0.0.1/32}' WHERE id = $1`, ts.Participation("alice"))
	// (A new client: the requests with the header set the login cookie,
	// as in CMS.)
	if r := ts.Client().PostRaw("/demo/api/login", nil); r.Body != `{"login_data":"Already-Logged-In"}` {
		t.Errorf("IP autologin: %d %s", r.Status, r.Body)
	}
}

// TestAPICSRF: requests without the header are checked as forms (a page
// of another site cannot use the cookie to submit).
func TestAPICSRF(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{})
	c := ts.Client()
	c.Login("alice", "demo")
	if r := c.PostRaw("/demo/api/sum/submit", url.Values{"language": {"C++17 / g++"}}); r.Status != http.StatusForbidden ||
		!strings.Contains(r.Body, "A critical error has occurred") {
		t.Errorf("submit with the cookie and no _xsrf: %d %.80s", r.Status, r.Body)
	}
	if r := c.Get("/demo/api/task_list"); r.Status != http.StatusOK {
		t.Errorf("task_list with the cookie: %d", r.Status)
	}
}

// TestAPILoginRateLimit: the API login shares the limits of the login
// form and answers them in JSON.
func TestAPILoginRateLimit(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{})
	c := ts.Client()
	var r *testResponse
	for range 11 {
		r = c.PostRaw("/demo/api/login", url.Values{"username": {"alice"}, "password": {"wrong"}})
	}
	if r.Status != http.StatusTooManyRequests || r.Body != `{"error":"Too many login attempts"}` || r.Header.Get("Retry-After") == "" {
		t.Errorf("11th attempt: %d %s (Retry-After %q)", r.Status, r.Body, r.Header.Get("Retry-After"))
	}
	// Another user is not limited.
	if r := c.PostRaw("/demo/api/login", url.Values{"username": {"bob"}, "password": {"demo"}}); r.Status != http.StatusOK {
		t.Errorf("bob: %d %s", r.Status, r.Body)
	}
}

// TestAPIQueries: task_list and the details run a bounded number of
// queries whatever the number of tasks and statements (N2).
func TestAPIQueries(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{Counting: true})
	c := ts.Client()
	r := c.PostRaw("/demo/api/login", url.Values{"username": {"alice"}, "password": {"demo"}})
	var ld map[string]string
	if err := json.Unmarshal([]byte(r.Body), &ld); err != nil {
		t.Fatal(err)
	}
	measure := func(path string) int64 {
		t.Helper()
		ts.Counter.Reset()
		if r := c.Get(path, authHeader, ld["login_data"]); r.Status != http.StatusOK {
			t.Fatalf("%s: %d %s", path, r.Status, r.Body)
		}
		return ts.Counter.Count()
	}
	before := measure("/demo/api/task_list")
	ts.Exec(`INSERT INTO tasks (num, contest_id, name, title, submission_format)
		SELECT 100 + i, $1, 'extra' || i, 'Extra ' || i, '{extra.%l}' FROM generate_series(1, 30) i`, ts.Contest)
	ts.Exec(`INSERT INTO statements (task_id, language, digest)
		SELECT t.id, l, 'x' FROM tasks t, unnest(ARRAY['en', 'es', 'it']) l WHERE t.name LIKE 'extra%'`)
	after := measure("/demo/api/task_list")
	if after != before || after > 4 {
		t.Errorf("task_list: %d queries with 33 tasks, %d with 3", after, before)
	}
	r = c.Get("/demo/api/task_list", authHeader, ld["login_data"])
	if !strings.Contains(r.Body, `{"name":"extra30","statements":["en","es","it"],"submission_format":["extra.%l"]}`) {
		t.Errorf("task_list: %.300s", r.Body)
	}
	// The contest, the participant, the tasks, the submission with its
	// result, the score type of the dataset.
	if n := measure("/demo/api/sum/submissions/4926046975063954292/details"); n > 5 {
		t.Errorf("details: %d queries", n)
	}
}
