// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package contestweb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
)

// insertCommunication inserts the announcements, messages and questions of
// the comparison with CMS (testdata/cms/communication*.html, recorded with
// the same rows in the reference installation).
func insertCommunication(ts *testSite) {
	ts.T.Helper()
	alice, bob := ts.Participation("alice"), ts.Participation("bob")
	ts.Exec(`INSERT INTO announcements (timestamp, subject, text, contest_id) VALUES
		('2026-10-10 08:00:00+00', 'Welcome', 'The contest **starts** soon.

* one
* two', $1),
		('2026-10-11 09:30:00+00', '', 'No subject <b>here</b>', $1),
		('2026-10-12 10:00:00+00', 'Only subject', '', $1)`, ts.Contest)
	ts.Exec(`INSERT INTO messages (timestamp, subject, text, participation_id) VALUES
		('2026-10-11 11:00:00+00', 'Private', 'Hello [link](http://example.com)', $1),
		('2026-10-12 12:00:00+00', '', '', $1)`, alice)
	ts.Exec(`INSERT INTO messages (timestamp, subject, text, participation_id) VALUES
		('2026-10-12 12:00:00+00', 'For bob', 'Not for alice', $1)`, bob)
	ts.Exec(`INSERT INTO questions (question_timestamp, subject, text, reply_timestamp, reply_subject, reply_text, ignored, participation_id) VALUES
		('2026-10-10 09:00:00+00', 'sum', 'What is <script>alert(1)</script>?
Second line', '2026-10-10 10:00:00+00', 'Answer', 'It is `+"`code`"+`', false, $1),
		('2026-10-10 09:30:00+00', '', 'Empty subject question', '2026-10-10 10:30:00+00', '', 'Reply with no subject', false, $1),
		('2026-10-11 09:40:00+00', 'Unanswered', '', NULL, NULL, NULL, false, $1),
		('2026-10-11 09:50:00+00', 'Ignored', 'Ignored question', NULL, NULL, NULL, true, $1),
		('2026-10-12 09:50:00+00', 'Yes', 'Quick answer', '2026-10-12 10:00:00+00', 'Yes', '', false, $1)`, alice)
}

// normalizeCommunication removes the documented differences of
// communication.html (R5.5: the live regions and their script;
// R5.9: contest times, whose title holds the absolute time CMS shows)
// before normalizePage.
func normalizeCommunication(s string) string {
	rep := func(re, with string) { s = regexp.MustCompile(re).ReplaceAllString(s, with) }
	rep(` id="communication_[a-z]+" data-live="[a-z ]+"`, "")
	rep(`(?s)utils\.update_unread_count = \(function \(update\) \{\n.*?\}\)\(utils\.display_notification\);\n`, "")
	rep(`<span class="timestamp" title="([^"]*)">[^<]*</span>`, `<span class="timestamp">$1</span>`)
	return s
}

// TestCMSCommunication compares the communication page with what CMS
// serves for the same rows: every list, empty lists, questions disabled,
// and before the start (no task in the subject's suggestions).
func TestCMSCommunication(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{})
	alice := ts.Client()
	alice.Login("alice", "demo")
	check := func(file string) {
		t.Helper()
		want, err := os.ReadFile(filepath.Join("testdata", "cms", file))
		if err != nil {
			t.Fatal(err)
		}
		r := alice.Get("/demo/communication")
		if r.Status != http.StatusOK {
			t.Errorf("%s: status %d", file, r.Status)
		}
		w, g := normalizePage(string(want), false), normalizePage(normalizeCommunication(r.Body), true)
		if w != g {
			t.Errorf("%s differs from CMS at %s", file, firstDiff(w, g))
		}
	}
	check("communication_empty.html")
	ts.Exec(`UPDATE contests SET allow_questions = false`)
	check("communication_noquestions.html")
	ts.Exec(`UPDATE contests SET allow_questions = true`)
	ts.Exec(`UPDATE groups SET start = '2027-01-01'`)
	alice.Login("alice", "demo")
	check("communication_before.html")
	ts.Exec(`UPDATE groups SET start = '2025-01-01'`)
	alice.Login("alice", "demo")
	insertCommunication(ts)
	check("communication.html")
}

// TestCommunicationPage checks what the comparison normalizes: the live
// regions, the contest times with the absolute time as tooltip, the
// escaping of the participant's text and the Markdown of the staff's.
func TestCommunicationPage(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{})
	insertCommunication(ts)
	c := ts.Client()
	c.Login("alice", "demo")
	r := c.Get("/demo/communication")
	for _, want := range []string{
		`<div class="announcement_list" id="communication_announcements" data-live="announcement">`,
		`<div class="question_list" id="communication_questions" data-live="answer">`,
		`<div class="message_list" id="communication_messages" data-live="message">`,
		// The demo contest started on 2025-01-01: contest times.
		"<span class=\"timestamp\" title=\"Oct 12, 2026, 10:00:00\u202fAM\">15586:00:00</span>",
		`What is &lt;script&gt;alert(1)&lt;/script&gt;?`,
		`<div class="body"><p>It is <code>code</code></p>`,
		`<div class="body"><p>No subject &lt;b&gt;here&lt;/b&gt;</p>`,
		`utils.update_unread_count = (function (update) {`,
	} {
		if !strings.Contains(r.Body, want) {
			t.Errorf("no %q", want)
		}
	}
	if strings.Contains(r.Body, "Not for alice") {
		t.Error("the page shows a message to another participant")
	}
	// Empty lists are not on the page (as in CMS): news of their kind
	// reload it (the script checks for the list's id).
	ts.Exec(`DELETE FROM announcements; DELETE FROM messages; DELETE FROM questions`)
	r = c.Get("/demo/communication")
	for _, id := range []string{"communication_announcements", "communication_questions", "communication_messages"} {
		if strings.Contains(r.Body, `id="`+id+`"`) {
			t.Errorf("region %s on the empty page", id)
		}
		if !strings.Contains(r.Body, `"`+id+`"`) {
			t.Errorf("the script does not check for %s", id)
		}
	}
	if strings.Contains(r.Body, "<h2>Announcements</h2>") || strings.Contains(r.Body, "<h2>Messages</h2>") {
		t.Error("headings of empty lists")
	}
}

// TestQuestionPost asks questions as CMS: limits counted in characters,
// Tornado's argument cleaning, notifications, the event for the
// administrators, refusals (questions disabled, no login, no CSRF token).
func TestQuestionPost(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{})
	alice := ts.Participation("alice")
	c := ts.Client()
	c.Login("alice", "demo")
	notes := func() []Communication {
		t.Helper()
		r := c.Get("/demo/notifications?last_notification=2000000000")
		var out []Communication
		if err := json.Unmarshal([]byte(r.Body), &out); err != nil {
			t.Fatalf("notifications: %s", r.Body)
		}
		return out
	}
	count := func() int {
		var n int
		if err := ts.Pool.QueryRow(context.Background(), `SELECT count(*) FROM questions WHERE participation_id = $1`, alice).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	ts.Events.Reset()
	r := c.Post("/demo/question", url.Values{"question_subject": {"  padded  "}, "question_text": {"x\x01y"}})
	if r.Status != http.StatusFound || r.Location() != "../demo/communication" {
		t.Fatalf("question: %d %q", r.Status, r.Location())
	}
	var subject, text string
	var at time.Time
	if err := ts.Pool.QueryRow(context.Background(), `SELECT subject, text, question_timestamp FROM questions WHERE participation_id = $1`, alice).Scan(&subject, &text, &at); err != nil {
		t.Fatal(err)
	}
	if subject != "padded" || text != "x y" || !at.Equal(demoNow) {
		t.Errorf("stored %q %q at %s", subject, text, at)
	}
	n := notes()
	if len(n) != 1 || n[0].Level != "success" || n[0].Subject != "Question received" ||
		n[0].Text != "Your question has been received, you will be notified when it is answered." {
		t.Errorf("notifications: %+v", n)
	}
	evs := ts.Events.Events()
	if len(evs) != 1 || evs[0].Name != live.EventQuestion {
		t.Fatalf("events: %+v", evs)
	}
	if d := evs[0].Data.(live.Question); d.Contest != int64(ts.Contest) || d.Participation != int64(alice) || d.Change != live.QuestionNew || d.ID == 0 {
		t.Errorf("question event: %+v", d)
	}
	if len(evs[0].Topics) != 1 || evs[0].Topics[0] != live.AdminContestTopic(int64(ts.Contest)) {
		t.Errorf("question event topics: %v", evs[0].Topics)
	}

	// The limits are in characters (Python's len), not bytes.
	c.Post("/demo/question", url.Values{"question_subject": {strings.Repeat("é", 50)}, "question_text": {strings.Repeat("t", 2000)}})
	c.Post("/demo/question", url.Values{"question_subject": {strings.Repeat("é", 51)}, "question_text": {"t"}})
	c.Post("/demo/question", url.Values{"question_text": {strings.Repeat("ñ", 2001)}})
	n = notes()
	if len(n) != 3 || n[0].Level != "success" || n[1].Level != "error" || n[1].Subject != "Question too long!" ||
		n[1].Text != "Subject must be at most 50 characters, content at most 2000." || n[2].Subject != "Question too long!" {
		t.Errorf("notifications: %+v", n)
	}
	if got := count(); got != 2 {
		t.Errorf("%d questions stored, want 2", got)
	}
	// An empty question is accepted, as in CMS.
	if r := c.Post("/demo/question", nil); r.Status != http.StatusFound {
		t.Errorf("empty question: %d", r.Status)
	}

	// Refusals.
	if r := c.PostRaw("/demo/question", url.Values{"question_subject": {"no token"}}); r.Status != http.StatusForbidden ||
		!strings.Contains(r.Body, "A critical error has occurred") {
		t.Errorf("without _xsrf: %d %.80s", r.Status, r.Body)
	}
	if r := c.Get("/demo/question"); r.Status != http.StatusMethodNotAllowed {
		t.Errorf("GET question: %d", r.Status)
	}
	anon := ts.Client()
	if r := anon.Post("/demo/question", url.Values{"question_subject": {"anon"}}); r.Status != http.StatusForbidden || !strings.Contains(r.Body, "Error 403") {
		t.Errorf("without login: %d", r.Status)
	}
	if r := anon.Get("/demo/communication"); r.Status != http.StatusFound || r.Location() != "../demo?next=%2Fdemo%2Fcommunication" {
		t.Errorf("page without login: %d %q", r.Status, r.Location())
	}
	ts.Exec(`UPDATE contests SET allow_questions = false`)
	if r := c.Post("/demo/question", url.Values{"question_subject": {"off"}}); r.Status != http.StatusNotFound || !strings.Contains(r.Body, "Error 404") {
		t.Errorf("questions disabled: %d", r.Status)
	}
	if got := count(); got != 3 {
		t.Errorf("%d questions stored, want 3", got)
	}
}

// TestQuestionRateLimit: after questionBurst questions in a row, the
// next one is refused with a notification (new; CMS has no limit).
func TestQuestionRateLimit(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{})
	c := ts.Client()
	c.Login("alice", "demo")
	for i := range questionBurst {
		c.Post("/demo/question", url.Values{"question_subject": {fmt.Sprint(i)}})
	}
	c.Get("/demo/notifications?last_notification=2000000000")
	if r := c.Post("/demo/question", url.Values{"question_subject": {"one too many"}}); r.Status != http.StatusFound ||
		r.Location() != "../demo/communication" {
		t.Fatalf("limited question: %d %q", r.Status, r.Location())
	}
	var n int
	if err := ts.Pool.QueryRow(context.Background(), `SELECT count(*) FROM questions`).Scan(&n); err != nil || n != questionBurst {
		t.Errorf("%d questions stored (%v), want %d", n, err, questionBurst)
	}
	r := c.Get("/demo/notifications?last_notification=2000000000")
	if !strings.Contains(r.Body, `"subject":"Too many questions!"`) || !strings.Contains(r.Body, `"level":"error"`) {
		t.Errorf("notification: %s", r.Body)
	}
	// Other participants are not affected; Spanish has the translation.
	b := ts.Client()
	b.Header.Set("Accept-Language", "es")
	b.Login("bob", "demo")
	if r := b.Post("/demo/question", url.Values{"question_subject": {"bob"}}); r.Status != http.StatusFound {
		t.Errorf("bob: %d", r.Status)
	}
	for range questionBurst {
		b.Post("/demo/question", url.Values{"question_subject": {"bob"}})
	}
	if r := b.Get("/demo/notifications?last_notification=2000000000"); !strings.Contains(r.Body, "¡Demasiadas preguntas!") {
		t.Errorf("Spanish notification: %s", r.Body)
	}
}

// TestCommunicationQueries: the page runs the same number of queries
// whatever the length of its lists (N2).
func TestCommunicationQueries(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{Counting: true})
	c := ts.Client()
	c.Login("alice", "demo")
	measure := func() int64 {
		ts.Counter.Reset()
		if r := c.Get("/demo/communication"); r.Status != http.StatusOK {
			t.Fatalf("communication: %d", r.Status)
		}
		return ts.Counter.Count()
	}
	empty := measure()
	alice := ts.Participation("alice")
	ts.Exec(`INSERT INTO announcements (timestamp, subject, text, contest_id)
		SELECT '2026-10-01'::timestamptz + i * interval '1 minute', 'A' || i, 'text', $1 FROM generate_series(1, 200) i`, ts.Contest)
	ts.Exec(`INSERT INTO messages (timestamp, subject, text, participation_id)
		SELECT '2026-10-01'::timestamptz + i * interval '1 minute', 'M' || i, 'text', $1 FROM generate_series(1, 200) i`, alice)
	ts.Exec(`INSERT INTO questions (question_timestamp, subject, text, reply_timestamp, reply_subject, reply_text, participation_id)
		SELECT '2026-10-01'::timestamptz + i * interval '1 minute', 'Q' || i, 'text',
		       CASE WHEN i % 2 = 0 THEN '2026-10-02'::timestamptz END, 'R', 'reply', $1 FROM generate_series(1, 200) i`, alice)
	full := measure()
	if full != empty {
		t.Errorf("%d queries with 600 items, %d without", full, empty)
	}
	if full > 4 {
		t.Errorf("%d queries for the communication page", full)
	}
	// Newest first within each list.
	r := c.Get("/demo/communication")
	if i, j := strings.Index(r.Body, ">A200<"), strings.Index(r.Body, ">A1<"); i < 0 || j < 0 || i > j {
		t.Errorf("announcements not newest first (%d, %d)", i, j)
	}
	if i, j := strings.Index(r.Body, ">Q200<"), strings.Index(r.Body, ">Q199<"); i < 0 || j < 0 || i > j {
		t.Errorf("questions not newest first (%d, %d)", i, j)
	}
}
