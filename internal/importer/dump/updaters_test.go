// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package dump

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
)

// testdata/v14 and testdata/v44 are dumps written with the data models 14
// (CMS 1.2: users still inside contests) and 44 (CMS 1.5), with an object
// of most classes; testdata/v14.updated.json and v44.updated.json are
// their contest.json after cmsDumpUpdater of CMS (version 49).

// canonical encodes objects for comparison; the opaque ids that
// update_45 draws at random are left out.
func canonical(t *testing.T, objs objects) string {
	t.Helper()
	for _, v := range objs {
		if class(v) == "Submission" {
			if id, ok := v["opaque_id"]; ok && id != nil {
				v["opaque_id"] = "random"
			}
		}
	}
	b, err := json.MarshalIndent(objs, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestUpdatersGolden: the updaters give the same objects as those of CMS.
func TestUpdatersGolden(t *testing.T) {
	for _, v := range []string{"v14", "v44"} {
		t.Run(v, func(t *testing.T) {
			f, err := os.Open(filepath.Join("testdata", v, modelName))
			if err != nil {
				t.Fatal(err)
			}
			raw, err := readRaw(f)
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
			var warnings []string
			objs, err := raw.updated(func(format string, args ...any) {
				warnings = append(warnings, format)
			})
			if err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(filepath.Join("testdata", v+".updated.json"))
			if err != nil {
				t.Fatal(err)
			}
			dec := json.NewDecoder(bytes.NewReader(b))
			dec.UseNumber()
			var golden map[string]any
			if err := dec.Decode(&golden); err != nil {
				t.Fatal(err)
			}
			if golden["_version"] != json.Number("49") {
				t.Fatalf("golden version %v", golden["_version"])
			}
			want := objects{}
			for k, o := range golden {
				if !strings.HasPrefix(k, "_") {
					want[k] = o.(map[string]any)
				}
			}
			if got, exp := canonical(t, objs), canonical(t, want); got != exp {
				t.Errorf("updated objects differ from cmsDumpUpdater:\n%s", diffLines(got, exp))
			}
			if v == "v14" && len(warnings) != 2 {
				t.Errorf("warnings %q", warnings)
			}
		})
	}
}

// diffLines shows the first lines that differ.
func diffLines(a, b string) string {
	la, lb := strings.Split(a, "\n"), strings.Split(b, "\n")
	var out []string
	for i := 0; i < max(len(la), len(lb)) && len(out) < 20; i++ {
		var x, y string
		if i < len(la) {
			x = la[i]
		}
		if i < len(lb) {
			y = lb[i]
		}
		if x != y {
			out = append(out, "ours:     "+x, "upstream: "+y)
		}
	}
	return strings.Join(out, "\n")
}

func TestImportVersion14(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	dir := filepath.Join("testdata", "v14")
	res := e.imp(t, dir, Options{})
	text := issues(res.Report)
	for _, want := range []string{
		"the dump was written with the data model version 14 of CMS: it was updated to version 49",
		"some file names contained '%'", "Submission 41 uses Java 1.4 / gcj, updated to Java / JDK",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("no %q in\n%s", want, text)
		}
	}
	checkCounts(t, e.counts(t), map[string]int{"contests": 1, "groups": 1, "announcements": 1, "users": 2,
		"participations": 2, "messages": 1, "questions": 1, "tasks": 1, "statements": 1, "datasets": 2,
		"managers": 2, "testcases": 4, "submissions": 2, "files": 2, "tokens": 1, "submission_results": 2,
		"executables": 1, "evaluations": 2, "user_tests": 1, "user_test_files": 1, "user_test_results": 1,
		"task_scores": 2})
	if n := e.checkDigests(t, dir); n != 0 {
		t.Errorf("%d tombstones", n)
	}

	var langs []string
	var start, stop, analysis time.Time
	if err := e.pool.QueryRow(ctx, `SELECT c.languages, g.start, g.stop, g.analysis_start FROM contests c
		JOIN groups g ON g.id = c.main_group_id AND g.name = 'default'`).Scan(&langs, &start, &stop, &analysis); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(langs, []string{"C++11 / g++", "C11 / gcc", "Java / JDK"}) || !start.Equal(at(1400000000)) ||
		!stop.Equal(at(1400018000)) || !analysis.Equal(stop) {
		t.Errorf("contest: %v %v %v %v", langs, start, stop, analysis)
	}
	var format []string
	var feedback string
	if err := e.pool.QueryRow(ctx, `SELECT submission_format, feedback_level FROM tasks`).Scan(&format, &feedback); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(format, []string{"sum.%l"}) || feedback != "full" {
		t.Errorf("task: %v %s", format, feedback)
	}
	// Users split from their participations, names encoded, passwords
	// with their method.
	if n := e.count(t, `SELECT count(*) FROM users WHERE (username, password) IN (('ana', 'plaintext:secret'), ('jos__e9', 'plaintext:pw2'))`); n != 2 {
		t.Errorf("users: %d", n)
	}
	var ip []string
	var extra time.Duration
	var starting time.Time
	if err := e.pool.QueryRow(ctx, `SELECT p.ip::text[], p.extra_time, p.starting_time FROM participations p
		JOIN users u ON u.id = p.user_id WHERE u.username = 'ana'`).Scan(&ip, &extra, &starting); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ip, []string{"10.0.0.1/32", "192.168.0.0/16"}) || extra != 10*time.Minute || !starting.Equal(at(1400000500.25)) {
		t.Errorf("participation: %v %v %v", ip, extra, starting)
	}
	// Limits in bytes, no limit for 0; Communication with a grader.
	var mem *int64
	var tl *float64
	var params string
	if err := e.pool.QueryRow(ctx, `SELECT memory_limit, time_limit, task_type_parameters::text FROM datasets WHERE description = 'Comm'`).
		Scan(&mem, &tl, &params); err != nil {
		t.Fatal(err)
	}
	if mem != nil || tl != nil || params != `[1, "grader", "fifo_io"]` {
		t.Errorf("Comm dataset: %v %v %s", mem, tl, params)
	}
	if n := e.count(t, `SELECT count(*) FROM managers WHERE filename = 'grader.cpp'`); n != 1 {
		t.Errorf("grader: %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM datasets WHERE memory_limit = 268435456 AND score_type_parameters = '[[40, 1], [60, 1]]'`); n != 1 {
		t.Errorf("Default dataset")
	}
	// Results: score details as fractions, texts as lists, sandboxes as
	// lists of paths; the gcj executable is gone.
	var details string
	var paths []string
	var ctext []string
	if err := e.pool.QueryRow(ctx, `SELECT r.score_details::text, r.compilation_sandbox_paths, r.compilation_text FROM submission_results r
		JOIN submissions s ON s.id = r.submission_id WHERE s.language = 'C++11 / g++'`).Scan(&details, &paths, &ctext); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(details, `"score_fraction": 1.0`) || !strings.Contains(details, `"score_fraction": 0.0`) ||
		strings.Contains(details, `"score":`) || !slices.Equal(paths, []string{"/tmp/a", "/tmp/b"}) ||
		!slices.Equal(ctext, []string{"Compilation succeeded"}) {
		t.Errorf("result: %s %v %v", details, paths, ctext)
	}
	if n := e.count(t, `SELECT count(*) FROM evaluations WHERE text = '{"Output isn''t correct: %s",3}' AND evaluation_sandbox_paths = '{}'`); n != 1 {
		t.Errorf("evaluation text: %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM executables e JOIN submissions s ON s.id = e.submission_id WHERE s.language = 'Java / JDK'`); n != 0 {
		t.Errorf("gcj executables: %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM submissions WHERE opaque_id >= 0 AND official`); n != 2 {
		t.Errorf("submissions: %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM announcements WHERE admin_id IS NULL`); n != 1 {
		t.Errorf("announcements: %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM user_test_results WHERE compilation_sandbox_paths = '{}' AND evaluation_sandbox_paths IS NULL`); n != 1 {
		t.Errorf("user test results: %d", n)
	}
}

func TestImportVersion44(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	dir := filepath.Join("testdata", "v44")
	res := e.imp(t, dir, Options{})
	if len(res.Report.Errors()) > 0 {
		t.Errorf("issues:\n%s", issues(res.Report))
	}
	checkCounts(t, e.counts(t), map[string]int{"contests": 1, "groups": 1, "users": 1, "teams": 1, "participations": 1,
		"tasks": 1, "datasets": 1, "managers": 2, "testcases": 1, "submissions": 1, "files": 1, "submission_results": 1,
		"executables": 1, "evaluations": 1, "user_tests": 1, "user_test_managers": 1, "task_scores": 1})
	if n := e.checkDigests(t, dir); n != 0 {
		t.Errorf("%d tombstones", n)
	}
	var perUser time.Duration
	var analysis bool
	var aStart time.Time
	if err := e.pool.QueryRow(ctx, `SELECT g.per_user_time, g.analysis_enabled, g.analysis_start FROM groups g
		JOIN participations p ON p.group_id = g.id`).Scan(&perUser, &analysis, &aStart); err != nil {
		t.Fatal(err)
	}
	if perUser != 2*time.Hour || !analysis || !aStart.Equal(at(1710020000)) {
		t.Errorf("group: %v %v %v", perUser, analysis, aStart)
	}
	if n := e.count(t, `SELECT count(*) FROM executables WHERE filename = 'eco'`); n != 1 {
		t.Error("executable") // update_44 made version 44: the name is already right
	}
	if n := e.count(t, `SELECT count(*) FROM user_test_managers WHERE filename = 'grader.cpp'`); n != 1 {
		t.Error("user test grader")
	}
	if n := e.count(t, `SELECT count(*) FROM datasets WHERE task_type_parameters = '[1, "grader", "std_io"]'`); n != 1 {
		t.Error("parameters")
	}
	if n := e.count(t, `SELECT count(*) FROM evaluations WHERE evaluation_sandbox_paths = '{/var/cms/b,/var/cms/c}'`); n != 1 {
		t.Error("sandbox paths")
	}
	if n := e.count(t, `SELECT count(*) FROM tasks WHERE feedback_level = 'oi_restricted' AND score_mode = 'max_subtask'`); n != 1 {
		t.Error("task")
	}
	if n := e.count(t, `SELECT count(*) FROM users WHERE password LIKE 'bcrypt:$2b$12$%' AND preferred_languages = '{es}'`); n != 1 {
		t.Error("user")
	}
	if n := e.count(t, `SELECT count(*) FROM task_scores WHERE score = 100`); n != 1 {
		t.Error("task score")
	}
}

func TestVersions(t *testing.T) {
	for _, c := range []struct {
		json, want string
	}{
		{`{"_version": 50}`, "written by a newer CMS (data model version 50; this importer reads up to 49)"},
		{`{"_version": 13}`, "update it with cmsDumpUpdater of CMS first"},
		{`{"0": {"_class": "Contest"}}`, "data model version 0"},
	} {
		raw, err := readRaw(strings.NewReader(c.json))
		if err != nil {
			t.Fatal(err)
		}
		err = raw.decodeObjects(func(string, ...any) {}, func(string, *object) error { return nil })
		if !errors.Is(err, ErrVersion) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", c.json, err)
		}
	}
	// Through Import: nothing is written.
	b := newBuilder(t, 50)
	e := newEnv(t)
	if _, err := Import(context.Background(), e.w, b.write(), Options{}); !errors.Is(err, ErrVersion) {
		t.Errorf("import: %v", err)
	}
}

func TestUpdaterErrors(t *testing.T) {
	run := func(version int, objs objects) error {
		var order []string
		for k := range objs {
			order = append(order, k)
		}
		slices.Sort(order)
		return update(objs, order, 2, version, func(string, ...any) {})
	}
	// update_41: invalid names are an error.
	err := run(40, objects{"0": {"_class": "Contest", "name": "bad name", "languages": []any{}}})
	if err == nil || !strings.Contains(err.Error(), "invalid codenames in Contest.name") {
		t.Errorf("update_41: %v", err)
	}
	err = run(40, objects{"0": {"_class": "File", "filename": "a/b", "digest": "nope"}})
	if err == nil || !strings.Contains(err.Error(), "invalid filename schemas in File.filename; invalid digests in File.digest") {
		t.Errorf("update_41: %v", err)
	}
	// update_49: a dataset with both the stub and the grader.
	err = run(48, objects{
		"0": {"_class": "Dataset", "task_type": "Communication", "task_type_parameters": []any{json.Number("1"), "stub", "fifo_io"}},
		"1": {"_class": "Manager", "dataset": "0", "filename": "stub.c"},
		"2": {"_class": "Manager", "dataset": "0", "filename": "grader.c"},
	})
	if err == nil || err.Error() != "updating the dump to version 49: dataset 0 contains both stub.c and grader.c" {
		t.Errorf("update_49: %v", err)
	}
	// update_26: a name that encodes to nothing.
	err = run(25, objects{"0": {"_class": "Task", "name": ""}})
	if err == nil || !strings.Contains(err.Error(), "invalid codename") {
		t.Errorf("update_26: %v", err)
	}
	// update_22: Communication parameters that are not JSON.
	err = run(21, objects{"0": {"_class": "Dataset", "task_type": "Communication", "task_type_parameters": "[1"}})
	if err == nil || !strings.Contains(err.Error(), "task type parameters") {
		t.Errorf("update_22: %v", err)
	}
}

// TestUpdaters43: the updaters from version 44 on, on a dump of CMS 1.4.
func TestUpdaters43(t *testing.T) {
	objs := objects{
		"0": {"_class": "Contest", "name": "c", "start": json.Number("10.0"), "stop": json.Number("20.0"), "languages": []any{}},
		"1": {"_class": "Participation", "contest": "0", "printjobs": []any{"5"}},
		"2": {"_class": "Submission", "language": "Python 3 / CPython", "results": []any{"3"}},
		"3": {"_class": "SubmissionResult", "submission": "2", "executables": map[string]any{"s": "4"},
			"compilation_sandbox": "/a:/b"},
		"4": {"_class": "Executable", "submission": "2", "submission_result": "3", "filename": "s"},
		"5": {"_class": "PrintJob", "participation": "1"},
		"6": {"_class": "Evaluation", "evaluation_sandbox": ""},
		"7": {"_class": "UserTestResult", "compilation_sandbox": nil},
		"8": {"_class": "Submission", "language": "C11 / gcc", "opaque_id": json.Number("7"), "results": []any{}},
	}
	order := []string{"0", "1", "2", "3", "4", "5", "6", "7", "8"}
	if err := update(objs, order, 2, 43, func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}
	if objs["4"]["filename"] != "s.pyz" || stringMap(objs["3"]["executables"])["s.pyz"] != "4" {
		t.Errorf("update_44: %v %v", objs["4"], objs["3"])
	}
	if id, ok := objs["2"]["opaque_id"].(json.Number); !ok || id == "" || objs["8"]["opaque_id"] != json.Number("7") {
		t.Errorf("update_45: %v %v", objs["2"]["opaque_id"], objs["8"]["opaque_id"])
	}
	if !slices.Equal(list(objs["3"]["compilation_sandbox_paths"]), []any{"/a", "/b"}) ||
		len(list(objs["6"]["evaluation_sandbox_paths"])) != 0 || objs["6"]["evaluation_sandbox_paths"] == nil ||
		objs["7"]["compilation_sandbox_paths"] != nil || objs["7"]["evaluation_sandbox_paths"] != nil {
		t.Errorf("update_46: %v %v %v", objs["3"], objs["6"], objs["7"])
	}
	if _, ok := objs["5"]; ok || objs["1"]["printjobs"] != nil {
		t.Errorf("update_47: %v", objs["1"])
	}
	// len(data) = 8 objects (update_47 removed the print job) + _version
	// and _objects: get_id gives 10.
	g := objs["10"]
	if class(g) != "Group" || g["contest"] != "0" || g["start"] != json.Number("10.0") || objs["0"]["main_group"] != "10" ||
		objs["1"]["group"] != "10" {
		t.Errorf("update_48: %v %v", g, objs["0"])
	}
}

func TestUpdaterHelpers(t *testing.T) {
	for in, want := range map[string]string{"ana": "ana", "josé": "jos__e9", "a b": "a__20b", "x.y": "x__2ey", "日": "__65e5"} {
		if got := encodeCodename(in, ""); got != want {
			t.Errorf("encodeCodename(%q) = %q, want %q", in, got, want)
		}
	}
	if got := encodeCodename("a%.b c", "%."); got != "a%.b__20c" {
		t.Errorf("file name: %q", got)
	}
	for _, c := range []struct {
		in   any
		want []any
	}{
		{nil, []any{}},
		{`["Killed with signal %d", 9]`, []any{"Killed with signal %s", "9"}},
		{"plain text", []any{"plain text"}},
		{`[]`, []any{"[]"}},
	} {
		if got := fixText(c.in); !slices.Equal(got, c.want) {
			t.Errorf("fixText(%v) = %q, want %q", c.in, got, c.want)
		}
	}
	for f, want := range map[float64]string{1: "1.0", 0: "0.0", 0.5: "0.5", 1.0 / 3: "0.3333333333333333", 1e-5: "1e-05"} {
		if got := pyFloat(f); string(got) != want {
			t.Errorf("pyFloat(%v) = %s, want %s", f, got, want)
		}
	}
}

// TestImportOldDumpArchive: an old dump in an archive goes through the
// updaters too.
func TestImportOldDumpArchive(t *testing.T) {
	e := newEnv(t)
	p := tarDump(t, filepath.Join("testdata", "v44"), "omi/", true, nil)
	res := e.imp(t, p, Options{})
	if res.Count("contest", importer.Created) != 1 || !strings.Contains(issues(res.Report), "data model version 44") {
		t.Errorf("result %v\n%s", res.Changes, issues(res.Report))
	}
}
