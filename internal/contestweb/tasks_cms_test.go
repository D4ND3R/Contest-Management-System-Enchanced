// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package contestweb

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
)

// cmsCapture is testdata/cms/tasks/captured.json.gz: what the original
// CMS answered in the states of scenarios.json (capture.py).
type cmsCapture struct {
	Base      []string `json:"base"`
	User      string   `json:"user"`
	Scenarios []struct {
		Name  string    `json:"name"`
		Now   time.Time `json:"now"`
		SQL   []string  `json:"sql"`
		Pages []struct {
			File               string  `json:"file"`
			Path               string  `json:"path"`
			Lang               string  `json:"lang"`
			Status             int     `json:"status"`
			ContentType        string  `json:"content_type"`
			ContentDisposition string  `json:"content_disposition"`
			Location           string  `json:"location"`
			Body               *string `json:"body"`
		} `json:"pages"`
	} `json:"scenarios"`
}

func loadCMSCapture(t *testing.T) *cmsCapture {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "cms", "tasks", "captured.json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	var c cmsCapture
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	return &c
}

// normalizeTasks removes the documented differences of the task pages
// (testdata/cms/README): the attributes of the live regions (R5.8), the
// contest times shown instead of CMS's times, which become their title
// (R5.9), the onchange handlers moved to cws_handlers.js and the comments
// html/template drops from scripts (D10), and the newline CMS writes
// before the page (the line of the macro import outside the blocks).
func normalizeTasks(s string, ours bool) string {
	rep := func(re, with string) { s = regexp.MustCompile(re).ReplaceAllString(s, with) }
	if ours {
		rep(` id="submission_list_[a-z]+" data-live="submission" data-live-match-task="\d+"`, "")
		rep(` id="user_test_list_[^"]*" data-live="user_test" data-live-match-task="\d+"`, "")
		rep(` data-live="task_score" data-live-match-task="\d+"`, "")
		rep(`<td class="(time|datetime)" title="([^"]*)">[^<]*</td>`, `<td class="$1">$2</td>`)
	} else {
		rep(`\n\s*onchange="CMS\.CWSUtils\.filter_languages\([^"]*"`, "")
		rep(`(?s)\n/\*\*\n.*?\*/\n`, "\n\n\n")
		if strings.HasPrefix(s, "\n<!DOCTYPE") {
			s = s[1:]
		}
	}
	return s
}

// refreshAllTaskScores recomputes the task score cache of every
// participant of the demo contest (the dispatcher does it after a
// result; the scenarios write results directly).
func (ts *testSite) refreshAllTaskScores() {
	ts.T.Helper()
	ctx := context.Background()
	rows, err := ts.Pool.Query(ctx, `SELECT p.id, t.id FROM participations p JOIN tasks t ON t.contest_id = p.contest_id
		WHERE p.contest_id = $1`, ts.Contest)
	if err != nil {
		ts.T.Fatal(err)
	}
	keys, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (judging.TaskKey, error) {
		var k judging.TaskKey
		err := r.Scan(&k.ParticipationID, &k.TaskID)
		return k, err
	})
	if err != nil {
		ts.T.Fatal(err)
	}
	tx, err := ts.Pool.Begin(ctx)
	if err != nil {
		ts.T.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := judging.RefreshTaskScores(ctx, tx, keys); err != nil {
		ts.T.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		ts.T.Fatal(err)
	}
}

// cmsTaskDiffs are the answers in which this fork differs from CMS on
// purpose: the test checks that they contain these texts instead.
var cmsTaskDiffs = map[string][]string{
	// CMS's get_json_details raises a KeyError on the public details of
	// a subtask with private testcases (they have no score_fraction) and
	// shows "Score details temporarily unavailable."; this fork renders
	// them with CMS's template (R5.4: the public testcases of every
	// subtask), as the API does (docs/en/api.md).
	"submissions/details_6": {`<div class="subtask undefined">`, "Subtask 0", `<tr class="correct">`,
		"Output is correct", "(N/A)", "<h3>Compilation output</h3>"},
}

// cmsEnglishTimes are the pages in other languages with submission
// tables: CMS imports macro/submission.html without context, so its
// format_datetime and format_time filters fall back to English (and to
// the server's time zone); this fork formats those times in the language
// of the page. The comparison ignores the times of their rows.
var cmsEnglishTimes = map[string]bool{"submissions/half_submissions_ja": true}

var rowTimes = regexp.MustCompile(`<td class="(time|datetime)">[^<]*</td>`)

// TestCMSTaskPages compares the task, submission and user test pages,
// their JSON and their downloads with what the original CMS answered in
// the same states (testdata/cms/tasks): descriptions, submissions in every
// status, official and not, with and without tokens, limits, analysis
// mode, the details of each score type, user tests in every status, in a
// few languages.
func TestCMSTaskPages(t *testing.T) {
	t.Parallel()
	capture := loadCMSCapture(t)
	ts := newTestSite(t, testOptions{})
	compared := 0
	// One login: the login attempts of a user are rate limited.
	c := ts.Client()
	for i, sc := range capture.Scenarios {
		tx, err := ts.Pool.Begin(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, q := range append(append([]string{}, capture.Base...), sc.SQL...) {
			if _, err := tx.Exec(context.Background(), q); err != nil {
				t.Fatalf("%s: %s: %v", sc.Name, q, err)
			}
		}
		if err := tx.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
		ts.refreshAllTaskScores()
		ts.Clock.Set(sc.Now)
		if i == 0 {
			c.Login(capture.User, "demo")
		}
		for _, p := range sc.Pages {
			name := sc.Name + "/" + p.File
			lang := p.Lang
			if lang == "" {
				lang = "en"
			}
			r := c.Get(p.Path, "Accept-Language", lang)
			if r.Status != p.Status {
				t.Errorf("%s: status %d, want %d", name, r.Status, p.Status)
				continue
			}
			if loc := r.Location(); loc != p.Location {
				t.Errorf("%s: Location %q, want %q", name, loc, p.Location)
			}
			if got, want := r.Header.Get("Content-Disposition"), p.ContentDisposition; got != want {
				t.Errorf("%s: Content-Disposition %q, want %q", name, got, want)
			}
			if got, want := strings.ToLower(r.Header.Get("Content-Type")), strings.ToLower(p.ContentType); got != want && p.Status == http.StatusOK {
				t.Errorf("%s: Content-Type %q, want %q", name, got, want)
			}
			if p.Body == nil {
				compared++
				continue
			}
			if strings.HasPrefix(p.ContentType, "application/json") {
				var want, got any
				if err := json.Unmarshal([]byte(*p.Body), &want); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(r.Body), &got); err != nil {
					t.Errorf("%s: %v: %s", name, err, r.Body)
					continue
				}
				if !reflect.DeepEqual(want, got) {
					t.Errorf("%s: JSON\n got  %s\n want %s", name, r.Body, *p.Body)
				}
				compared++
				continue
			}
			if texts, ok := cmsTaskDiffs[name]; ok {
				for _, x := range texts {
					if !strings.Contains(r.Body, x) {
						t.Errorf("%s: no %q in\n%s", name, x, r.Body)
					}
				}
				compared++
				continue
			}
			w := normalizePage(normalizeTasks(*p.Body, false), false)
			g := normalizePage(normalizeTasks(r.Body, true), true)
			if cmsEnglishTimes[name] {
				w = rowTimes.ReplaceAllString(w, `<td class="$1">T</td>`)
				g = rowTimes.ReplaceAllString(g, `<td class="$1">T</td>`)
			}
			if w != g {
				t.Errorf("%s differs from CMS at %s", name, firstDiff(w, g))
				if dir := os.Getenv("CMS_TEST_DIFF_DIR"); dir != "" {
					f := strings.ReplaceAll(name, "/", "_")
					_ = os.WriteFile(filepath.Join(dir, f+".cms.html"), []byte(w), 0o644)
					_ = os.WriteFile(filepath.Join(dir, f+".ours.html"), []byte(g), 0o644)
				}
			}
			compared++
		}
	}
	if compared < 80 {
		t.Errorf("only %d answers compared", compared)
	}
}
