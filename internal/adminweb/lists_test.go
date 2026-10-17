// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package adminweb

import (
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"testing"
)

// TestRankingDownloads checks the text and CSV rankings (CMS's formats):
// columns, sorting by total (partial after complete), the partial marks,
// the team column only when some participant has a team, hidden
// participants left out.
func TestRankingDownloads(t *testing.T) {
	ts, f := newFixtureSite(t, testOptions{})
	c := ts.Client()
	c.Login("ro")
	base := "/contest/" + itoa(ts.Contest) + "/ranking/"
	r := c.Get(base + "txt")
	wantTxt := "" +
		"            Username                           User                          Team            sum           half           quiz   Global\n" +
		"               alice                  Alice Liddell                         Italy           100              0              0      100 \n" +
		"                 bob                    Bob Builder                                           0*             0              0        0*\n" +
		"               carol                  Carol Danvers                                           0              0              0        0 \n"
	if r.Status != 200 || r.Body != wantTxt || r.Header.Get("Content-Type") != "text/plain" ||
		r.Header.Get("Content-Disposition") != `attachment; filename="ranking.txt"` {
		t.Fatalf("ranking.txt: %d %q\n%q", r.Status, r.Header, r.Body)
	}
	r = c.Get(base + "csv")
	wantCSV := "Username,User,Team,sum,P,half,P,quiz,P,Global,P\r\n" +
		"alice,Alice Liddell,Italy,100.0,,0.0,,0.0,,100.0,\r\n" +
		"bob,Bob Builder,,0.0,*,0.0,,0.0,,0.0,*\r\n" +
		"carol,Carol Danvers,,0.0,,0.0,,0.0,,0.0,\r\n"
	if r.Status != 200 || r.Body != wantCSV || r.Header.Get("Content-Type") != "text/csv" {
		t.Fatalf("ranking.csv: %d %q", r.Status, r.Body)
	}
	// Without teams, with a hidden participant and a name to quote.
	ts.Exec(`UPDATE participations SET team_id = NULL`)
	ts.Exec(`UPDATE participations SET hidden = true WHERE id = $1`, f.Part["carol"])
	ts.Exec(`UPDATE users SET last_name = 'Builder, "Bob"' WHERE id = $1`, f.Users["bob"])
	r = c.Get(base + "csv")
	wantCSV = "Username,User,sum,P,half,P,quiz,P,Global,P\r\n" +
		"alice,Alice Liddell,100.0,,0.0,,0.0,,100.0,\r\n" +
		"bob,\"Bob Builder, \"\"Bob\"\"\",0.0,*,0.0,,0.0,,0.0,*\r\n"
	if r.Body != wantCSV {
		t.Fatalf("ranking.csv without teams: %q", r.Body)
	}
	r = c.Get(base + "txt")
	wantTxt = "" +
		"            Username                           User            sum           half           quiz   Global\n" +
		"               alice                  Alice Liddell           100              0              0      100 \n" +
		"                 bob             Bob Builder, \"Bob\"             0*             0              0        0*\n"
	if r.Body != wantTxt {
		t.Fatalf("ranking.txt without teams:\n%q", r.Body)
	}
	if r := c.Get(base + "json"); r.Status != 200 || r.Header.Get("Content-Type") == "text/csv" {
		t.Fatalf("another format is the page: %d", r.Status)
	}
}

// rowIDs returns the ids of the submissions listed on a page.
func rowIDs(body string) []int {
	var ids []int
	for _, m := range regexp.MustCompile(`href="[^"]*submission/(\d+)/\d+"`).FindAllStringSubmatch(body, -1) {
		n, _ := strconv.Atoi(m[1])
		if !slices.Contains(ids, n) {
			ids = append(ids, n)
		}
	}
	slices.Sort(ids)
	return ids
}

// TestSubmissionFilters checks the filters of the submission list and
// that its number of queries does not grow with the rows.
func TestSubmissionFilters(t *testing.T) {
	ts, f := newFixtureSite(t, testOptions{Counting: true})
	c := ts.Client()
	c.Login("ro")
	page := "/contest/" + itoa(ts.Contest) + "/submissions"
	all := []int{int(f.Scored), int(f.Compiling), int(f.Failed), int(f.Unofficial)}
	slices.Sort(all)
	ids := func(ids ...int32) []int {
		var out []int
		for _, id := range ids {
			out = append(out, int(id))
		}
		slices.Sort(out)
		return out
	}
	for _, tc := range []struct {
		query url.Values
		want  []int
	}{
		{url.Values{}, all},
		{url.Values{"task": {itoa(f.Tasks["sum"])}}, ids(f.Scored, f.Compiling, f.Unofficial)},
		{url.Values{"task": {itoa(f.Tasks["half"])}}, ids(f.Failed)},
		{url.Values{"task": {"999999"}}, all}, // not a task of the contest: ignored
		{url.Values{"user": {"alice"}}, ids(f.Scored, f.Unofficial)},
		{url.Values{"user": {"nobody"}}, nil},
		{url.Values{"status": {"scored"}}, ids(f.Scored)},
		{url.Values{"status": {"compiling"}}, ids(f.Compiling, f.Unofficial)},
		{url.Values{"status": {"compilation_failed"}}, ids(f.Failed)},
		{url.Values{"status": {"evaluating"}}, nil},
		{url.Values{"official": {"no"}}, ids(f.Unofficial)},
		{url.Values{"official": {"yes"}}, ids(f.Scored, f.Compiling, f.Failed)},
		{url.Values{"language": {"C++17 / g++"}}, all},
		{url.Values{"language": {"Python 3 / CPython"}}, nil},
		{url.Values{"task": {itoa(f.Tasks["sum"])}, "user": {"alice"}, "official": {"yes"}}, ids(f.Scored)},
	} {
		r := c.Get(page + "?" + tc.query.Encode())
		if got := rowIDs(r.Body); r.Status != 200 || !slices.Equal(got, tc.want) {
			t.Errorf("%s: %d %v, want %v", tc.query.Encode(), r.Status, got, tc.want)
		}
	}
	// The dataset page lists the submissions of its task.
	if got := rowIDs(c.Get("/dataset/" + itoa(f.Datasets["half"])).Body); !slices.Equal(got, ids(f.Failed)) {
		t.Errorf("dataset page: %v", got)
	}

	// Queries per page: the same with 4 rows and with 54.
	count := func(q string) int64 {
		ts.Counter.Reset()
		if r := c.Get(page + q); r.Status != 200 {
			t.Fatalf("GET %s: %d", q, r.Status)
		}
		return ts.Counter.Count()
	}
	queries := []string{"", "?task=" + itoa(f.Tasks["sum"]), "?user=alice&status=scored", "?official=yes&language=C%2B%2B17+%2F+g%2B%2B"}
	before := make([]int64, len(queries))
	for i, q := range queries {
		before[i] = count(q)
	}
	ts.Exec(`INSERT INTO submissions (opaque_id, participation_id, task_id, timestamp, language)
		SELECT 100 + g, $1, $2, '2026-10-15 11:00:00+00'::timestamptz + make_interval(secs => g), 'C++17 / g++'
		FROM generate_series(1, 50) g`, f.Part["alice"], f.Tasks["sum"])
	ts.Exec(`INSERT INTO files (submission_id, filename, digest) SELECT id, 'sum.%l', $1 FROM submissions WHERE opaque_id > 100`, f.FileDigest)
	ts.Exec(`INSERT INTO submission_results (submission_id, dataset_id, compilation_outcome, compilation_tries,
		evaluation_outcome, evaluation_tries, score, score_details, scored_at, public_score, public_score_details, ranking_score_details)
		SELECT id, $1, 'ok', 1, 'ok', 1, 50, '[]', now(), 50, '[]', '{50}' FROM submissions WHERE opaque_id > 100 AND opaque_id % 2 = 0`,
		f.Datasets["sum"])
	for i, q := range queries {
		if after := count(q); after > before[i] {
			t.Errorf("GET %s: %d queries with 54 submissions, %d with 4", q, after, before[i])
		}
	}
	if n := len(rowIDs(c.Get(page + "?user=alice").Body)); n < 20 {
		t.Errorf("the page lists %d rows of alice", n)
	}
}
