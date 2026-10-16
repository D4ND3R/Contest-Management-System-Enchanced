// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package contestweb

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
)

// upload is a file of a multipart form: field name, file name, content.
type upload struct {
	Field, Name string
	Content     []byte
}

// PostFiles sends a multipart form (with the _xsrf token unless the
// fields set it) to path.
func (c *testClient) PostFiles(path string, fields url.Values, files ...upload) *testResponse {
	c.T.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	if fields == nil {
		fields = url.Values{}
	}
	if !fields.Has("_xsrf") {
		fields.Set("_xsrf", c.XSRF())
	}
	for k, vs := range fields {
		for _, v := range vs {
			_ = mw.WriteField(k, v)
		}
	}
	for _, f := range files {
		w, err := mw.CreateFormFile(f.Field, f.Name)
		if err != nil {
			c.T.Fatal(err)
		}
		_, _ = w.Write(f.Content)
	}
	_ = mw.Close()
	req, err := http.NewRequest(http.MethodPost, c.Site.URL+path, &b)
	if err != nil {
		c.T.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return c.Do(req)
}

// notes returns the notifications (subject: text) the site queued for
// the client, and empties the queue.
func (c *testClient) notes() []string {
	c.T.Helper()
	r := c.Get("/demo/notifications")
	if r.Status != http.StatusOK {
		c.T.Fatalf("notifications: %d %s", r.Status, r.Body)
	}
	var ns []map[string]any
	if err := json.Unmarshal([]byte(r.Body), &ns); err != nil {
		c.T.Fatal(err)
	}
	var out []string
	for _, n := range ns {
		if n["type"] == "notification" {
			out = append(out, fmt.Sprintf("%s %s: %s", n["level"], n["subject"], n["text"]))
		}
	}
	return out
}

// count runs a SELECT count(*) on the test database.
func (ts *testSite) count(sql string, args ...any) int {
	ts.T.Helper()
	var n int
	if err := ts.Pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		ts.T.Fatalf("%s: %v", sql, err)
	}
	return n
}

// judgingEvents reads the events the site sent to the dispatcher.
func (ts *testSite) judgingEvents() []judging.Event {
	ts.T.Helper()
	msgs, err := judging.ReadEvents(context.Background(), ts.KV, "test", 1000, 0, false)
	if err != nil {
		ts.T.Fatal(err)
	}
	var out []judging.Event
	for _, m := range msgs {
		if m.Err != nil {
			ts.T.Fatal(m.Err)
		}
		out = append(out, m.Event)
	}
	return out
}

var cppSource = []byte("#include <cstdio>\nint main() { int a, b; scanf(\"%d %d\", &a, &b); printf(\"%d\\n\", a + b); }\n")

// TestSubmit follows a submission from the form to the database, the
// blob store, the task score cache, the dispatcher's stream and the live
// events.
func TestSubmit(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{})
	carol := ts.Client()
	carol.Login("carol", "demo")
	p := ts.Participation("carol")
	ts.Events.Reset()

	r := carol.PostFiles("/demo/tasks/sum/submit", url.Values{"language": {"C++17 / g++"}},
		upload{"sum.%l", "a.cpp", cppSource})
	if r.Status != http.StatusFound || !strings.HasPrefix(r.Location(), "../../../demo/tasks/sum/submissions?submission_id=") {
		t.Fatalf("submit: %d %s", r.Status, r.Location())
	}
	if n := carol.notes(); !slices.Equal(n, []string{"success Submission received: Your submission has been received and is currently being evaluated."}) {
		t.Errorf("notifications %q", n)
	}

	var id int32
	var opaque int64
	var official bool
	var lang string
	err := ts.Pool.QueryRow(context.Background(), `SELECT id, opaque_id, official, language FROM submissions
		WHERE participation_id = $1`, p).Scan(&id, &opaque, &official, &lang)
	if err != nil {
		t.Fatal(err)
	}
	if !official || lang != "C++17 / g++" || opaque <= 0 {
		t.Errorf("submission: official %v, language %q, opaque %d", official, lang, opaque)
	}
	// The submission_id argument is the id, encrypted as CMS does.
	u, _ := url.Parse(r.Location())
	if got, err := decryptNumber(u.Query().Get("submission_id"), ts.Config.DeriveKey("contest-web/submission-id")); err != nil || got != int64(id) {
		t.Errorf("submission_id decrypts to %d (%v), want %d", got, err, id)
	}
	// The file, by its SHA-256, in the store.
	var fname, digest string
	if err := ts.Pool.QueryRow(context.Background(), `SELECT filename, digest FROM files WHERE submission_id = $1`, id).
		Scan(&fname, &digest); err != nil {
		t.Fatal(err)
	}
	if fname != "sum.%l" || digest != blob.Digest(cppSource) {
		t.Errorf("file %q %s", fname, digest)
	}
	rc, err := ts.Blobs.Open(context.Background(), digest)
	if err != nil {
		t.Fatal(err)
	}
	rc.Close()
	// The task score cache: partial until scored.
	var partial bool
	if err := ts.Pool.QueryRow(context.Background(), `SELECT partial FROM task_scores
		WHERE participation_id = $1 AND task_id = (SELECT id FROM tasks WHERE name = 'sum')`, p).Scan(&partial); err != nil {
		t.Fatal(err)
	}
	if !partial {
		t.Error("the task score is not partial")
	}
	// The dispatcher is told, after the commit.
	evs := ts.judgingEvents()
	if len(evs) != 1 || evs[0].Kind != judging.EventSubmission || !slices.Equal(evs[0].SubmissionIDs, []int32{id}) {
		t.Errorf("judging events %+v", evs)
	}
	// The live pages too: the new submission and the task score.
	var names []string
	for _, e := range ts.Events.Events() {
		names = append(names, e.Name)
		if s, ok := e.Data.(live.Submission); ok && (!s.New || s.Opaque != opaque || s.Status != live.SubmissionCompiling) {
			t.Errorf("live submission %+v", s)
		}
	}
	if !slices.Equal(names, []string{"submission", "task_score"}) {
		t.Errorf("live events %q", names)
	}

	// The page lists it, compiling, with its file; the status JSON says so.
	page := carol.Get("/demo/tasks/sum/submissions")
	for _, want := range []string{`<tr data-submission="` + strconv.FormatInt(opaque, 10) + `" data-status="1">`,
		`Compiling...      <img class="details"`, `/files/sum.cpp">`} {
		if !strings.Contains(page.Body, want) {
			t.Errorf("submissions page: no %q", want)
		}
	}
	st := carol.Get("/demo/tasks/sum/submissions/" + strconv.FormatInt(opaque, 10))
	if st.Status != http.StatusOK || st.Body != `{"status":1,"status_text":"Compiling..."}` {
		t.Errorf("status: %d %s", st.Status, st.Body)
	}
	// The file comes back as it was sent.
	f := carol.Get("/demo/tasks/sum/submissions/" + strconv.FormatInt(opaque, 10) + "/files/sum.cpp")
	if f.Status != http.StatusOK || f.Body != string(cppSource) ||
		f.Header.Get("Content-Disposition") != "attachment; filename=sum.cpp" {
		t.Errorf("file: %d %q %q", f.Status, f.Header.Get("Content-Disposition"), f.Body)
	}
}

// TestSubmitArchive submits an output-only task as a zip archive.
func TestSubmitArchive(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{})
	carol := ts.Client()
	carol.Login("carol", "demo")
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	for _, n := range []string{"output_000.txt", "output_001.txt"} {
		w, _ := zw.Create(n)
		_, _ = w.Write([]byte(n))
	}
	_ = zw.Close()
	r := carol.PostFiles("/demo/tasks/quiz/submit", nil, upload{"submission", "s.zip", zb.Bytes()})
	if r.Status != http.StatusFound {
		t.Fatalf("submit: %d", r.Status)
	}
	if n := carol.notes(); len(n) != 1 || !strings.HasPrefix(n[0], "success") {
		t.Fatalf("notifications %q", n)
	}
	rows, err := ts.Pool.Query(context.Background(), `SELECT f.filename, f.digest, s.language IS NULL FROM files f
		JOIN submissions s ON s.id = f.submission_id WHERE s.participation_id = $1 ORDER BY 1`, ts.Participation("carol"))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var n, d string
		var noLang bool
		if err := rows.Scan(&n, &d, &noLang); err != nil {
			t.Fatal(err)
		}
		if d != blob.Digest([]byte(n)) || !noLang {
			t.Errorf("%s: digest %s, no language %v", n, d, noLang)
		}
		got = append(got, n)
	}
	if !slices.Equal(got, []string{"output_000.txt", "output_001.txt"}) {
		t.Errorf("files %q", got)
	}
}

// TestSubmitRefused checks the limits and the refusals: nothing is
// stored, the participant is told why (CMS's messages), as CMS.
func TestSubmitRefused(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{})
	carol := ts.Client()
	carol.Login("carol", "demo")
	p := ts.Participation("carol")
	// A previous submission ten minutes ago.
	ts.Exec(`INSERT INTO submissions (opaque_id, participation_id, task_id, timestamp, language, comment, official)
		SELECT 77, $1, id, $2, 'C++17 / g++', '', true FROM tasks WHERE name = 'sum'`, p, demoNow.Add(-10*time.Minute))
	cpp := upload{"sum.%l", "sum.cpp", cppSource}
	lang := url.Values{"language": {"C++17 / g++"}}
	cases := []struct {
		name   string
		setup  string
		fields url.Values
		files  []upload
		note   string
	}{
		{"no files", "", lang, nil,
			"error Invalid submission format!: Please select the correct files."},
		{"too big", "", lang, []upload{{"sum.%l", "sum.cpp", bytes.Repeat([]byte("x"), 100001)}},
			"error Submission too big!: Each source file must be at most 100000 bytes long."},
		{"unknown language", "", url.Values{"language": {"Brainfuck"}}, []upload{cpp},
			"error Invalid submission format!: Please select the correct files."},
		{"contest limit", `UPDATE contests SET max_submission_number = 1`, lang, []upload{cpp},
			"error Too many submissions!: You have reached the maximum limit of at most 1 submissions among all tasks."},
		{"task limit", `UPDATE tasks SET max_submission_number = 1 WHERE name = 'sum'`, lang, []upload{cpp},
			"error Too many submissions!: You have reached the maximum limit of at most 1 submissions on this task."},
		{"contest interval", `UPDATE contests SET min_submission_interval = '1 hour'`, lang, []upload{cpp},
			"error Submissions too frequent!: Among all tasks, you can submit again after 3600 seconds from last submission."},
		{"task interval", `UPDATE tasks SET min_submission_interval = '15 minutes' WHERE name = 'sum'`, lang, []upload{cpp},
			"error Submissions too frequent!: For this task, you can submit again after 900 seconds from last submission."},
	}
	for _, c := range cases {
		ts.Exec(`UPDATE contests SET max_submission_number = NULL, min_submission_interval = NULL`)
		ts.Exec(`UPDATE tasks SET max_submission_number = NULL, min_submission_interval = NULL`)
		if c.setup != "" {
			ts.Exec(c.setup)
		}
		r := carol.PostFiles("/demo/tasks/sum/submit", c.fields, c.files...)
		if r.Status != http.StatusFound || r.Location() != "../../../demo/tasks/sum/submissions" {
			t.Errorf("%s: %d %s", c.name, r.Status, r.Location())
		}
		if n := carol.notes(); !slices.Equal(n, []string{c.note}) {
			t.Errorf("%s: notifications %q, want %q", c.name, n, c.note)
		}
		if n := ts.count(`SELECT count(*) FROM submissions WHERE participation_id = $1`, p); n != 1 {
			t.Errorf("%s: %d submissions", c.name, n)
		}
	}
	ts.Exec(`UPDATE contests SET max_submission_number = NULL, min_submission_interval = NULL`)
	ts.Exec(`UPDATE tasks SET max_submission_number = NULL, min_submission_interval = NULL`)

	// The grace period: in the last minutes of the contest the interval
	// does not apply (min_submission_interval_grace_period).
	ts.Exec(`UPDATE contests SET min_submission_interval = '1 hour', min_submission_interval_grace_period = '30 minutes'`)
	ts.Exec(`UPDATE groups SET stop = $1`, demoNow.Add(20*time.Minute))
	carol.Login("carol", "demo")
	if r := carol.PostFiles("/demo/tasks/sum/submit", lang, cpp); r.Status != http.StatusFound ||
		!strings.Contains(r.Location(), "submission_id=") {
		t.Errorf("grace period: %d %s %q", r.Status, r.Location(), carol.notes())
	}
	carol.notes()
	ts.Exec(`UPDATE groups SET stop = '2030-01-01'`)

	// Other errors: unknown task, no CSRF token, no login, the wrong
	// phase.
	if r := carol.PostFiles("/demo/tasks/nosuch/submit", lang, cpp); r.Status != http.StatusNotFound {
		t.Errorf("unknown task: %d", r.Status)
	}
	if r := carol.PostFiles("/demo/tasks/sum/submit", url.Values{"_xsrf": {"x"}, "language": lang["language"]}, cpp); r.Status != http.StatusForbidden {
		t.Errorf("bad CSRF token: %d", r.Status)
	}
	anon := ts.Client()
	if r := anon.PostFiles("/demo/tasks/sum/submit", lang, cpp); r.Status != http.StatusForbidden {
		t.Errorf("not logged in: %d", r.Status)
	}
	ts.Exec(`UPDATE groups SET stop = '2026-10-01'`)
	if r := carol.PostFiles("/demo/tasks/sum/submit", lang, cpp); r.Status != http.StatusFound || r.Location() != "../../../demo" {
		t.Errorf("after the contest: %d %s", r.Status, r.Location())
	}
	if n := ts.count(`SELECT count(*) FROM submissions WHERE participation_id = $1`, p); n != 2 {
		t.Errorf("%d submissions, want 2", n)
	}
	// In analysis mode submissions are accepted, unofficial, and do not
	// touch the task score cache.
	ts.Exec(`UPDATE groups SET analysis_enabled = true, analysis_start = '2026-10-02', analysis_stop = '2030-01-01'`)
	before := ts.count(`SELECT count(*) FROM task_scores WHERE participation_id = $1`, p)
	r := carol.PostFiles("/demo/tasks/sum/submit", lang, cpp)
	if r.Status != http.StatusFound || !strings.Contains(r.Location(), "submission_id=") {
		t.Fatalf("analysis: %d %s %q", r.Status, r.Location(), carol.notes())
	}
	if n := ts.count(`SELECT count(*) FROM submissions WHERE participation_id = $1 AND NOT official`, p); n != 1 {
		t.Errorf("%d unofficial submissions", n)
	}
	if n := ts.count(`SELECT count(*) FROM task_scores WHERE participation_id = $1`, p); n != before {
		t.Errorf("task scores %d -> %d", before, n)
	}
}

// TestSubmitConcurrent sends many submissions at once with room for one:
// the limit is checked again under the lock of the participation.
func TestSubmitConcurrent(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{})
	ts.Exec(`UPDATE tasks SET max_submission_number = 1 WHERE name = 'sum'`)
	carol := ts.Client()
	carol.Login("carol", "demo")
	carol.XSRF()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			carol.PostFiles("/demo/tasks/sum/submit", url.Values{"language": {"C++17 / g++"}},
				upload{"sum.%l", "sum.cpp", cppSource})
		}()
	}
	wg.Wait()
	if n := ts.count(`SELECT count(*) FROM submissions WHERE participation_id = $1`, ts.Participation("carol")); n != 1 {
		t.Errorf("%d submissions accepted, want 1", n)
	}
	ok, refused := 0, 0
	for _, n := range carol.notes() {
		switch {
		case strings.HasPrefix(n, "success Submission received"):
			ok++
		case strings.HasPrefix(n, "error Too many submissions!"):
			refused++
		}
	}
	if ok != 1 || refused != 7 {
		t.Errorf("%d accepted and %d refused, want 1 and 7", ok, refused)
	}
}

// TestSubmissionAccess: the submissions of others, of another task or
// unknown ones answer 404 (CMS get_submission), JSON routes ask for a
// login with a JSON 403.
func TestSubmissionAccess(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{})
	carol := ts.Client()
	carol.Login("carol", "demo")
	alice := ts.Client()
	alice.Login("alice", "demo")
	const aliceSub = "/demo/tasks/sum/submissions/4926046975063954292"
	if r := alice.Get(aliceSub); r.Status != http.StatusOK {
		t.Fatalf("own submission: %d", r.Status)
	}
	for _, p := range []string{aliceSub, aliceSub + "/details", aliceSub + "/files/sum.cpp",
		"/demo/tasks/half/submissions/4926046975063954292", "/demo/tasks/sum/submissions/123",
		"/demo/tasks/nosuch/submissions/4926046975063954292"} {
		if r := carol.Get(p); r.Status != http.StatusNotFound {
			t.Errorf("carol GET %s: %d", p, r.Status)
		}
	}
	if r := carol.Post(aliceSub+"/token", nil); r.Status != http.StatusNotFound {
		t.Errorf("token on another's submission: %d", r.Status)
	}
	// Not a number: no such route.
	for _, p := range []string{"/demo/tasks/sum/submissions/0", "/demo/tasks/sum/submissions/-1",
		"/demo/tasks/sum/submissions/x1"} {
		if r := alice.Get(p); r.Status != http.StatusNotFound {
			t.Errorf("GET %s: %d", p, r.Status)
		}
	}
	anon := ts.Client()
	if r := anon.Get(aliceSub); r.Status != http.StatusForbidden || r.Body != `{"error":"An authenticated user is required"}` {
		t.Errorf("anonymous status: %d %s", r.Status, r.Body)
	}
	if r := anon.Get("/demo/tasks/sum/submissions"); r.Status != http.StatusFound ||
		r.Location() != "../../../demo?next=%2Fdemo%2Ftasks%2Fsum%2Fsubmissions" {
		t.Errorf("anonymous page: %d %s", r.Status, r.Location())
	}
	// Downloads only when the contest allows them.
	ts.Exec(`UPDATE contests SET submissions_download_allowed = false`)
	if r := alice.Get(aliceSub + "/files/sum.cpp"); r.Status != http.StatusNotFound {
		t.Errorf("download not allowed: %d", r.Status)
	}
}

// TestUseToken plays tokens: the token row, the task score cache, the
// dispatcher's event; a second token on the same submission is refused,
// and with one token left two concurrent requests play one.
func TestUseToken(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{})
	alice := ts.Client()
	alice.Login("alice", "demo")
	const path = "/demo/tasks/sum/submissions/4926046975063954292/token"
	ts.judgingEvents()
	r := alice.Post(path, nil)
	if r.Status != http.StatusFound || r.Location() != "../../../../../demo/tasks/sum/submissions" {
		t.Fatalf("token: %d %s", r.Status, r.Location())
	}
	if n := alice.notes(); !slices.Equal(n, []string{"success Token request received: Your request has been received and applied to the submission."}) {
		t.Errorf("notifications %q", n)
	}
	if n := ts.count(`SELECT count(*) FROM tokens`); n != 1 {
		t.Errorf("%d tokens", n)
	}
	evs := ts.judgingEvents()
	if len(evs) != 1 || evs[0].Kind != judging.EventToken {
		t.Errorf("judging events %+v", evs)
	}
	var tokened float64
	if err := ts.Pool.QueryRow(context.Background(), `SELECT tokened_score FROM task_scores
		WHERE participation_id = $1 AND task_id = (SELECT id FROM tasks WHERE name = 'sum')`,
		ts.Participation("alice")).Scan(&tokened); err != nil {
		t.Fatal(err)
	}
	var score float64
	if err := ts.Pool.QueryRow(context.Background(), `SELECT score FROM submission_results sr
		JOIN submissions s ON s.id = sr.submission_id WHERE s.opaque_id = 4926046975063954292`).Scan(&score); err != nil {
		t.Fatal(err)
	}
	if tokened != score {
		t.Errorf("tokened score %v, want %v", tokened, score)
	}
	r = alice.Post(path, nil)
	if n := alice.notes(); r.Status != http.StatusFound || !slices.Equal(n, []string{"warning Token request discarded: Your request has been discarded because you already used a token on that submission."}) {
		t.Errorf("second token: %d %q", r.Status, n)
	}
	if n := ts.count(`SELECT count(*) FROM tokens`); n != 1 {
		t.Errorf("%d tokens", n)
	}
	// Only while competing.
	ts.Exec(`UPDATE groups SET stop = '2026-10-01'`)
	if r := alice.Post(path, nil); r.Status != http.StatusFound || r.Location() != "../../../../../demo" {
		t.Errorf("after the contest: %d %s", r.Status, r.Location())
	}
	ts.Exec(`UPDATE groups SET stop = '2030-01-01'`)

	// One contest token left for carol, two submissions, two requests at
	// once.
	ts.Exec(`UPDATE contests SET token_mode = 'finite', token_gen_initial = 1, token_gen_number = 0, token_max_number = NULL, token_gen_max = NULL, token_min_interval = '0'`)
	p := ts.Participation("carol")
	ts.Exec(`INSERT INTO submissions (opaque_id, participation_id, task_id, timestamp, language, comment, official)
		SELECT o, $1, (SELECT id FROM tasks WHERE name = 'sum'), '2026-10-15 10:00', 'C++17 / g++', '', true
		FROM unnest(ARRAY[11, 12]) o`, p)
	carol := ts.Client()
	carol.Login("carol", "demo")
	carol.XSRF()
	var wg sync.WaitGroup
	for _, o := range []string{"11", "12"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			carol.Post("/demo/tasks/sum/submissions/"+o+"/token", nil)
		}()
	}
	wg.Wait()
	if n := ts.count(`SELECT count(*) FROM tokens t JOIN submissions s ON s.id = t.submission_id WHERE s.participation_id = $1`, p); n != 1 {
		t.Errorf("carol played %d tokens with one available", n)
	}
	notes := carol.notes()
	slices.Sort(notes)
	if !slices.Equal(notes, []string{"error Token request discarded: Your request has been discarded because you have no tokens available.",
		"success Token request received: Your request has been received and applied to the submission."}) {
		t.Errorf("notifications %q", notes)
	}
}

// TestSubmissionPageBound: the page lists the latest SubmissionsPageSize
// submissions (D24).
func TestSubmissionPageBound(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{})
	ts.Exec(`INSERT INTO submissions (opaque_id, participation_id, task_id, timestamp, language, comment, official)
		SELECT i, $1, (SELECT id FROM tasks WHERE name = 'half'), '2026-10-01'::timestamptz + i * interval '1 minute', 'C++17 / g++', '', true
		FROM generate_series(1, $2::int) i`, ts.Participation("carol"), SubmissionsPageSize+20)
	carol := ts.Client()
	carol.Login("carol", "demo")
	r := carol.Get("/demo/tasks/half/submissions")
	if n := strings.Count(r.Body, "<tr data-submission="); n != SubmissionsPageSize {
		t.Errorf("%d rows", n)
	}
	newest := strconv.Itoa(SubmissionsPageSize + 20)
	if !strings.Contains(r.Body, `<tr data-submission="`+newest+`"`) || strings.Contains(r.Body, `<tr data-submission="20"`) {
		t.Error("the page does not list the newest submissions")
	}
}

// TestSubmissionQueryCounts checks N2: the task pages run a fixed number
// of queries, the same with 1 or 300 submissions and tokens.
func TestSubmissionQueryCounts(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{Counting: true})
	alice := ts.Client()
	alice.Login("alice", "demo")
	const sub = "/demo/tasks/sum/submissions/4926046975063954292"
	pages := []struct {
		name string
		max  int64
		run  func() *testResponse
	}{
		// Contest, participation, tasks, statements and attachments.
		{"description", 4, func() *testResponse { return alice.Get("/demo/tasks/sum/description") }},
		// + score type, submissions, task scores, tokens.
		{"submissions", 7, func() *testResponse { return alice.Get("/demo/tasks/sum/submissions") }},
		// + submission, score type, task scores.
		{"status", 6, func() *testResponse { return alice.Get(sub) }},
		{"details", 5, func() *testResponse { return alice.Get(sub + "/details") }},
		{"file", 4, func() *testResponse { return alice.Get(sub + "/files/sum.cpp") }},
		// Contest, participation, tasks, user tests.
		{"testing", 4, func() *testResponse { return alice.Get("/demo/testing") }},
	}
	measure := func() []int64 {
		out := make([]int64, len(pages))
		for i, p := range pages {
			ts.Counter.Reset()
			if r := p.run(); r.Status != http.StatusOK {
				t.Fatalf("%s: %d", p.name, r.Status)
			}
			out[i] = ts.Counter.Count()
			if out[i] > p.max {
				t.Errorf("%s: %d queries, want at most %d", p.name, out[i], p.max)
			}
		}
		return out
	}
	small := measure()
	p := ts.Participation("alice")
	ts.Exec(`INSERT INTO submissions (opaque_id, participation_id, task_id, timestamp, language, comment, official)
		SELECT 1000 + i, $1, (SELECT id FROM tasks WHERE name = 'sum'), '2026-10-01'::timestamptz + i * interval '1 minute', 'C++17 / g++', '', i % 7 <> 0
		FROM generate_series(1, 300) i`, p)
	ts.Exec(`INSERT INTO files (submission_id, filename, digest)
		SELECT s.id, 'sum.%l', f.digest FROM submissions s, (SELECT digest FROM files LIMIT 1) f WHERE s.opaque_id BETWEEN 1001 AND 1300`)
	ts.Exec(`INSERT INTO tokens (submission_id, timestamp) SELECT id, timestamp FROM submissions WHERE opaque_id BETWEEN 1001 AND 1100`)
	ts.Exec(`INSERT INTO user_tests (participation_id, task_id, timestamp, language, input)
		SELECT $1, t.id, '2026-10-01'::timestamptz + i * interval '1 minute', 'C++17 / g++', (SELECT digest FROM files LIMIT 1)
		FROM generate_series(1, 100) i, tasks t WHERE t.name IN ('sum', 'half')`, p)
	ts.refreshAllTaskScores()
	large := measure()
	for i, pg := range pages {
		if small[i] != large[i] {
			t.Errorf("%s: %d queries with one submission, %d with 300", pg.name, small[i], large[i])
		}
	}
	t.Logf("queries per page: %v", large)
}

// BenchmarkSubmissionsPage measures the submissions page of a task with
// 30 scored submissions, files and tokens (the page a contestant reloads
// most), through HTTP on the loopback.
func BenchmarkSubmissionsPage(b *testing.B) {
	ts := newTestSite(b, testOptions{})
	p := ts.Participation("carol")
	ts.Exec(`INSERT INTO submissions (opaque_id, participation_id, task_id, timestamp, language, comment, official)
		SELECT 1000 + i, $1, (SELECT id FROM tasks WHERE name = 'sum'), '2026-10-01'::timestamptz + i * interval '1 minute', 'C++17 / g++', '', true
		FROM generate_series(1, 30) i`, p)
	ts.Exec(`INSERT INTO files (submission_id, filename, digest)
		SELECT s.id, 'sum.%l', f.digest FROM submissions s, (SELECT digest FROM files LIMIT 1) f WHERE s.participation_id = $1`, p)
	ts.Exec(`INSERT INTO submission_results (submission_id, dataset_id, compilation_outcome, compilation_text, compilation_tries,
		evaluation_outcome, evaluation_tries, score, score_details, public_score, public_score_details, ranking_score_details)
		SELECT s.id, t.active_dataset_id, 'ok', '{"Compilation succeeded"}', 1, 'ok', 1, 40, '[]', 0, '[]', '{40,0}'
		FROM submissions s JOIN tasks t ON t.id = s.task_id WHERE s.participation_id = $1`, p)
	ts.Exec(`INSERT INTO tokens (submission_id, timestamp) SELECT id, timestamp FROM submissions WHERE opaque_id BETWEEN 1001 AND 1003`)
	ts.refreshAllTaskScores()
	c := ts.Client()
	c.Login("carol", "demo")
	if r := c.Get("/demo/tasks/sum/submissions"); r.Status != http.StatusOK || strings.Count(r.Body, "<tr data-submission=") != 30 {
		b.Fatalf("%d", r.Status)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		c.Get("/demo/tasks/sum/submissions")
	}
}

// TestSubmitQueryCounts: a submission, a user test and a token run the
// same number of queries with 1 or 300 earlier submissions (N2).
func TestSubmitQueryCounts(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{Counting: true})
	ts.Exec(`UPDATE contests SET max_submission_number = 1000, max_user_test_number = 1000`)
	carol := ts.Client()
	carol.Login("carol", "demo")
	carol.XSRF()
	p := ts.Participation("carol")
	measure := func(opaque string) []int64 {
		var out []int64
		ts.Counter.Reset()
		r := carol.PostFiles("/demo/tasks/sum/submit", url.Values{"language": {"C++17 / g++"}}, upload{"sum.%l", "sum.cpp", cppSource})
		if !strings.Contains(r.Location(), "submission_id=") {
			t.Fatalf("submit: %d %s %q", r.Status, r.Location(), carol.notes())
		}
		out = append(out, ts.Counter.Count())
		ts.Counter.Reset()
		r = carol.PostFiles("/demo/tasks/sum/test", url.Values{"language": {"C++17 / g++"}},
			upload{"sum.%l", "sum.cpp", cppSource}, upload{"input", "in", []byte("1 2")})
		if !strings.Contains(r.Location(), "user_test_id=") {
			t.Fatalf("test: %d %s", r.Status, r.Location())
		}
		out = append(out, ts.Counter.Count())
		ts.Counter.Reset()
		if r := carol.Post("/demo/tasks/sum/submissions/"+opaque+"/token", nil); r.Status != http.StatusFound {
			t.Fatalf("token: %d", r.Status)
		}
		out = append(out, ts.Counter.Count())
		return out
	}
	ts.Exec(`INSERT INTO submissions (opaque_id, participation_id, task_id, timestamp, language, comment, official)
		SELECT o, $1, (SELECT id FROM tasks WHERE name = 'sum'), '2026-10-01', 'C++17 / g++', '', true FROM unnest(ARRAY[1, 2]) o`, p)
	// The first submission creates the row of the task score cache.
	if r := carol.PostFiles("/demo/tasks/sum/submit", url.Values{"language": {"C++17 / g++"}}, upload{"sum.%l", "sum.cpp", cppSource}); r.Status != http.StatusFound {
		t.Fatal(r.Status)
	}
	small := measure("1")
	ts.Exec(`INSERT INTO submissions (opaque_id, participation_id, task_id, timestamp, language, comment, official)
		SELECT 1000 + i, $1, (SELECT id FROM tasks WHERE name = 'sum'), '2026-10-01'::timestamptz + i * interval '1 minute', 'C++17 / g++', '', true
		FROM generate_series(1, 300) i`, p)
	ts.Exec(`INSERT INTO tokens (submission_id, timestamp) SELECT id, timestamp FROM submissions WHERE opaque_id BETWEEN 1001 AND 1050`)
	large := measure("2")
	if !slices.Equal(small, large) {
		t.Errorf("queries (submit, test, token): %v with 2 submissions, %v with 300", small, large)
	}
	for i, max := range []int64{14, 12, 14} {
		if large[i] > max {
			t.Errorf("POST %d: %d queries, want at most %d", i, large[i], max)
		}
	}
	t.Logf("queries (submit, test, token): %v", large)
}
