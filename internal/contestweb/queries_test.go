// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package contestweb

import (
	"net/http"
	"net/url"
	"testing"
)

// TestQueryCounts checks N2 on the pages of the core: each one runs a
// fixed, small number of queries, the same with 3 or 33 tasks and with 3
// or 203 participants.
func TestQueryCounts(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{Counting: true})
	ts.Exec(`UPDATE contests SET allow_registration = true`)
	alice := ts.Client()
	alice.Login("alice", "demo")
	anon := ts.Client()
	anon.XSRF()

	type page struct {
		name string
		max  int64
		run  func() *testResponse
	}
	// fresh is a new browser for every page (the "login" one logs carol
	// in with it), with its CSRF cookie set before counting.
	var fresh *testClient
	newBrowser := func() {
		fresh = ts.Client()
		fresh.XSRF()
	}
	pages := []page{
		// Contest list.
		{"contest list", 1, func() *testResponse { return anon.Get("/") }},
		// Contest (with its main group); nobody logged in.
		{"login form", 1, func() *testResponse { return anon.Get("/demo") }},
		// Contest, teams.
		{"register form", 2, func() *testResponse { return anon.Get("/demo/register") }},
		// Contest, participation (with user and group), tasks.
		{"overview", 3, func() *testResponse { return alice.Get("/demo") }},
		{"documentation", 3, func() *testResponse { return alice.Get("/demo/documentation") }},
		// Contest, participation, communications.
		{"notifications", 3, func() *testResponse { return alice.Get("/demo/notifications") }},
		// Contest, participation (login).
		{"login", 2, func() *testResponse {
			return fresh.Post("/demo/login", url.Values{"username": {"carol"}, "password": {"demo"}})
		}},
		// Contest, participation, phase-protected route redirect.
		{"start (wrong phase)", 2, func() *testResponse { return alice.Post("/demo/start", nil) }},
	}
	measure := func() []int64 {
		out := make([]int64, len(pages))
		for i, p := range pages {
			newBrowser()
			ts.Counter.Reset()
			r := p.run()
			if r.Status >= 400 {
				t.Fatalf("%s: %d\n%s", p.name, r.Status, r.Body)
			}
			out[i] = ts.Counter.Count()
			if out[i] > p.max {
				t.Errorf("%s: %d queries, want at most %d", p.name, out[i], p.max)
			}
		}
		return out
	}
	small := measure()

	// 30 more tasks with datasets, 200 more participants with
	// announcements and messages.
	ts.Exec(`INSERT INTO tasks (name, title, contest_id, num, submission_format)
		SELECT 'extra' || i, 'Extra ' || i, $1, 10 + i, '{extra.%l}' FROM generate_series(1, 30) i`, ts.Contest)
	ts.Exec(`INSERT INTO datasets (task_id, description, time_limit, memory_limit, task_type, task_type_parameters, score_type, score_type_parameters)
		SELECT id, 'default', 1, 268435456, 'Batch', '["alone", ["", ""], "diff"]', 'Sum', '10' FROM tasks WHERE name LIKE 'extra%'`)
	ts.Exec(`UPDATE tasks t SET active_dataset_id = d.id FROM datasets d WHERE d.task_id = t.id AND t.name LIKE 'extra%'`)
	ts.Exec(`INSERT INTO users (first_name, last_name, username, password)
		SELECT 'U', 'U' || i, 'user' || i, 'plaintext:x' FROM generate_series(1, 200) i`)
	ts.Exec(`INSERT INTO participations (contest_id, user_id, group_id)
		SELECT c.id, u.id, c.main_group_id FROM contests c, users u WHERE c.id = $1 AND u.username LIKE 'user%'`, ts.Contest)
	ts.Exec(`INSERT INTO announcements (timestamp, subject, text, contest_id)
		SELECT '2026-01-01'::timestamptz + i * interval '1 minute', 'A' || i, 'x', $1 FROM generate_series(1, 50) i`, ts.Contest)
	ts.Exec(`INSERT INTO messages (timestamp, subject, text, participation_id)
		SELECT '2026-01-01'::timestamptz + i * interval '1 minute', 'M' || i, 'x', $1 FROM generate_series(1, 50) i`, ts.Participation("alice"))

	large := measure()
	for i, p := range pages {
		if small[i] != large[i] {
			t.Errorf("%s: %d queries with the small contest, %d with the large one", p.name, small[i], large[i])
		}
	}
	if r := alice.Get("/demo"); r.Status != http.StatusOK {
		t.Fatal(r.Status)
	}
	t.Logf("queries per page: %v", large)
}
