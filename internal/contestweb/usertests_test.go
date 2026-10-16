// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package contestweb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
)

// TestUserTest sends a user test and follows it: the rows, the
// dispatcher's event, the testing page, the status JSON, the details and
// the downloads.
func TestUserTest(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{})
	carol := ts.Client()
	carol.Login("carol", "demo")
	input := []byte("1 2\n")
	r := carol.PostFiles("/demo/tasks/sum/test", url.Values{"language": {"C++17 / g++"}},
		upload{"sum.%l", "sum.cpp", cppSource}, upload{"input", "in.txt", input})
	if r.Status != http.StatusFound || !strings.HasPrefix(r.Location(), "../../../demo/testing?task_name=sum&user_test_id=") {
		t.Fatalf("test: %d %s", r.Status, r.Location())
	}
	if n := carol.notes(); !slices.Equal(n, []string{"success Test received: Your test has been received and is currently being executed."}) {
		t.Errorf("notifications %q", n)
	}
	var id int32
	var in string
	if err := ts.Pool.QueryRow(context.Background(), `SELECT id, input FROM user_tests WHERE participation_id = $1`,
		ts.Participation("carol")).Scan(&id, &in); err != nil {
		t.Fatal(err)
	}
	if in != blob.Digest(input) {
		t.Errorf("input %s", in)
	}
	if n := ts.count(`SELECT count(*) FROM user_test_files WHERE user_test_id = $1 AND filename = 'sum.%l' AND digest = $2`,
		id, blob.Digest(cppSource)); n != 1 {
		t.Errorf("%d files", n)
	}
	evs := ts.judgingEvents()
	if len(evs) != 1 || evs[0].Kind != judging.EventUserTest || !slices.Equal(evs[0].UserTestIDs, []int32{id}) {
		t.Errorf("judging events %+v", evs)
	}
	if names := eventNames(ts); !slices.Equal(names, []string{"user_test"}) {
		t.Errorf("live events %q", names)
	}

	page := carol.Get("/demo/testing?task_name=sum")
	for _, want := range []string{`<tr data-user-test="1" data-status="1">`, `<li class="active"><a href="#test_sum"`,
		`../demo/tasks/sum/tests/1/input`, `../demo/tasks/sum/tests/1/files/sum.cpp`} {
		if !strings.Contains(page.Body, want) {
			t.Errorf("testing page: no %q", want)
		}
	}
	if r := carol.Get("/demo/tasks/sum/tests/1"); r.Body != `{"status":1,"status_text":"Compiling..."}` {
		t.Errorf("status %s", r.Body)
	}
	if r := carol.Get("/demo/tasks/sum/tests/1/input"); r.Status != http.StatusOK || r.Body != string(input) ||
		r.Header.Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Errorf("input: %d %q %q", r.Status, r.Header.Get("Content-Type"), r.Body)
	}
	if r := carol.Get("/demo/tasks/sum/tests/1/output"); r.Status != http.StatusNotFound {
		t.Errorf("output before the result: %d", r.Status)
	}
	if r := carol.Get("/demo/tasks/sum/tests/1/files/sum.cpp"); r.Status != http.StatusOK || r.Body != string(cppSource) {
		t.Errorf("file: %d", r.Status)
	}

	// The result arrives.
	ts.Exec(`INSERT INTO user_test_results (user_test_id, dataset_id, output, compilation_outcome, compilation_text,
		compilation_tries, compilation_time, compilation_memory, evaluation_outcome, evaluation_text, evaluation_tries,
		execution_time, execution_memory)
		SELECT $1, active_dataset_id, $2, 'ok', '{"Compilation succeeded"}', 1, 0.5, 1048576, 'ok',
		'{"Execution completed successfully"}', 1, 0.25, 2097152 FROM tasks WHERE name = 'sum'`, id, in)
	r = carol.Get("/demo/tasks/sum/tests/1")
	var st map[string]any
	if err := json.Unmarshal([]byte(r.Body), &st); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"execution_time": "0.250 sec", "memory": "2.00 MiB", "output": true, "status": 4.0,
		"status_text": `Executed <a class="details">details</a>`}
	if !reflect.DeepEqual(st, want) {
		t.Errorf("status %s", r.Body)
	}
	if r := carol.Get("/demo/tasks/sum/tests/1/output"); r.Status != http.StatusOK || r.Body != string(input) {
		t.Errorf("output: %d", r.Status)
	}
	d := carol.Get("/demo/tasks/sum/tests/1/details")
	for _, want := range []string{"<h3>Evaluation outcome</h3>", "<pre>Execution completed successfully</pre>",
		"<td>Compilation succeeded</td>"} {
		if !strings.Contains(d.Body, want) {
			t.Errorf("details: no %q in\n%s", want, d.Body)
		}
	}
	// Numbers: 2 is unknown, as are the tests of another participant.
	if r := carol.Get("/demo/tasks/sum/tests/2"); r.Status != http.StatusNotFound {
		t.Errorf("test 2: %d", r.Status)
	}
	alice := ts.Client()
	alice.Login("alice", "demo")
	for _, p := range []string{"/demo/tasks/sum/tests/1", "/demo/tasks/sum/tests/1/details",
		"/demo/tasks/sum/tests/1/input", "/demo/tasks/sum/tests/1/files/sum.cpp", "/demo/tasks/half/tests/1"} {
		if r := alice.Get(p); r.Status != http.StatusNotFound {
			t.Errorf("alice GET %s: %d", p, r.Status)
		}
	}
}

func eventNames(ts *testSite) []string {
	var names []string
	for _, e := range ts.Events.Events() {
		names = append(names, e.Name)
	}
	return names
}

// TestUserTestRefused: the limits and the task types without tests, as
// CMS.
func TestUserTestRefused(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{})
	carol := ts.Client()
	carol.Login("carol", "demo")
	src := upload{"sum.%l", "sum.cpp", cppSource}
	in := upload{"input", "in.txt", []byte("1 2\n")}
	lang := url.Values{"language": {"C++17 / g++"}}
	post := func(note string, files ...upload) {
		t.Helper()
		r := carol.PostFiles("/demo/tasks/sum/test", lang, files...)
		if r.Status != http.StatusFound || r.Location() != "../../../demo/testing?task_name=sum" {
			t.Errorf("%s: %d %s", note, r.Status, r.Location())
		}
		if n := carol.notes(); !slices.Equal(n, []string{note}) {
			t.Errorf("notifications %q, want %q", n, note)
		}
	}
	post("error Invalid test format!: Please select the correct files.", src)
	post("error Input too big!: The input file must be at most 5000000 bytes long.", src,
		upload{"input", "in.txt", []byte(strings.Repeat("1", 5_000_001))})
	ts.Exec(`INSERT INTO user_tests (participation_id, task_id, timestamp, language, input)
		SELECT $1, id, $2, 'C++17 / g++', $3 FROM tasks WHERE name = 'half'`,
		ts.Participation("carol"), demoNow.Add(-60e9), blob.Digest([]byte("x")))
	ts.Exec(`UPDATE contests SET max_user_test_number = 1`)
	post("error Too many tests!: You have reached the maximum limit of at most 1 tests among all tasks.", src, in)
	ts.Exec(`UPDATE contests SET max_user_test_number = NULL, min_user_test_interval = '2 minutes'`)
	post("error Tests too frequent!: Among all tasks, you can test again after 120 seconds from last test.", src, in)
	ts.Exec(`UPDATE contests SET min_user_test_interval = NULL`)
	if n := ts.count(`SELECT count(*) FROM user_tests`); n != 1 {
		t.Errorf("%d user tests", n)
	}
	// Output only: no tests (CMS raises TestingNotAllowed: 404).
	if r := carol.PostFiles("/demo/tasks/quiz/test", nil, upload{"output_000.txt", "o", []byte("1")}, in); r.Status != http.StatusNotFound {
		t.Errorf("output only: %d", r.Status)
	}
	// Without allow_user_tests every route is 404.
	ts.Exec(`UPDATE contests SET allow_user_tests = false`)
	for _, p := range []string{"/demo/testing", "/demo/tasks/half/tests/1", "/demo/tasks/half/tests/1/details",
		"/demo/tasks/half/tests/1/input"} {
		if r := carol.Get(p); r.Status != http.StatusNotFound {
			t.Errorf("GET %s: %d", p, r.Status)
		}
	}
	if r := carol.PostFiles("/demo/tasks/sum/test", lang, src, in); r.Status != http.StatusNotFound {
		t.Errorf("POST without user tests: %d", r.Status)
	}
	// Only while competing.
	ts.Exec(`UPDATE contests SET allow_user_tests = true`)
	ts.Exec(`UPDATE groups SET stop = '2026-10-01'`)
	if r := carol.Get("/demo/testing"); r.Status != http.StatusFound || r.Location() != "../demo" {
		t.Errorf("testing after the contest: %d %s", r.Status, r.Location())
	}
	// The status JSON too: CMS redirects (actual_phase_required), it is
	// not a route of the API.
	if r := carol.Get("/demo/tasks/half/tests/1"); r.Status != http.StatusFound || r.Location() != "../../../../demo" {
		t.Errorf("status after the contest: %d %s", r.Status, r.Location())
	}
}

// TestUserTestNumbers: the page lists the latest UserTestsPageSize tests
// of each task with their numbers (oldest is 1), and every number keeps
// working (D24).
func TestUserTestNumbers(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{})
	n := UserTestsPageSize + 5
	ts.Exec(`INSERT INTO user_tests (participation_id, task_id, timestamp, language, input)
		SELECT $1, (SELECT id FROM tasks WHERE name = 'sum'), '2026-10-01'::timestamptz + i * interval '1 minute',
		'C++17 / g++', $3 FROM generate_series(1, $2::int) i`, ts.Participation("carol"), n, blob.Digest([]byte("x")))
	carol := ts.Client()
	carol.Login("carol", "demo")
	page := carol.Get("/demo/testing")
	if c := strings.Count(page.Body, "<tr data-user-test="); c != UserTestsPageSize {
		t.Errorf("%d rows", c)
	}
	if !strings.Contains(page.Body, `<tr data-user-test="`+strconv.Itoa(n)+`"`) || strings.Contains(page.Body, `<tr data-user-test="5"`) {
		t.Error("the page does not list the newest tests")
	}
	for _, num := range []int{1, 5, n} {
		if r := carol.Get("/demo/tasks/sum/tests/" + strconv.Itoa(num)); r.Status != http.StatusOK {
			t.Errorf("test %d: %d", num, r.Status)
		}
	}
	if r := carol.Get("/demo/tasks/sum/tests/" + strconv.Itoa(n+1)); r.Status != http.StatusNotFound {
		t.Errorf("test %d: %d", n+1, r.Status)
	}
}
