// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package polygon

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
)

func needCompiler(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath(Compiler); err != nil {
		t.Skipf("%s is not installed", Compiler)
	}
}

func load(t *testing.T, dir string) (*importer.Task, *importer.Report) {
	t.Helper()
	rep := &importer.Report{}
	task, err := Loader{}.LoadTask(dir, &importer.Options{Report: rep})
	if err != nil {
		t.Fatalf("LoadTask %s: %v", dir, err)
	}
	return task, rep
}

func messages(issues []importer.Issue) string {
	var out []string
	for _, i := range issues {
		out = append(out, i.String())
	}
	return strings.Join(out, "\n")
}

func TestRegistered(t *testing.T) {
	for _, name := range []string{"polygon", "polygon_task", "polygon_contest", "polygon_user"} {
		l, ok := importer.Default.Lookup(name)
		if !ok || l.Name() != "polygon" {
			t.Errorf("Lookup(%q) = %v, %v", name, l, ok)
		}
	}
	l, err := importer.Default.ChooseTask("", fixtureTask("polyaplusb"))
	if err != nil || l.Name() != "polygon" {
		t.Errorf("ChooseTask = %v, %v", l, err)
	}
	c, err := importer.Default.ChooseContest("", fixture)
	if err != nil || c.Name() != "polygon" {
		t.Errorf("ChooseContest = %v, %v", c, err)
	}
	u, err := importer.Default.ChooseUsers("", fixture)
	if err != nil || u.Name() != "polygon" {
		t.Errorf("ChooseUsers = %v, %v", u, err)
	}
	if !strings.Contains(importer.Default.Help(), "polygon (Polygon (XML-based) format; contest, task, users), also -L polygon_contest") {
		t.Errorf("help:\n%s", importer.Default.Help())
	}
}

func TestDetect(t *testing.T) {
	l := Loader{}
	cases := []struct {
		dir  string
		kind importer.Kind
		want bool
	}{
		{fixtureTask("polyaplusb"), importer.KindTask, true},
		{fixture, importer.KindTask, false},
		{fixture, importer.KindContest, true},
		{fixtureTask("polyaplusb"), importer.KindContest, false},
		{fixture, importer.KindUsers, true},
		{filepath.Join(fixture, "polyalice"), importer.KindUsers, true}, // upstream's form
		{fixtureTask("polyaplusb"), importer.KindUsers, false},
		{fixture, importer.KindTeams, false},
	}
	for _, c := range cases {
		if got := l.Detect(c.dir, c.kind); got != c.want {
			t.Errorf("Detect(%s, %s) = %v, want %v", c.dir, c.kind, got, c.want)
		}
	}
}

// TestLoadTaskLikeUpstream compares the tasks of the fixture with the rows
// the original CMS stored for them.
func TestLoadTaskLikeUpstream(t *testing.T) {
	needCompiler(t)
	for _, name := range []string{"polyaplusb", "polypairs"} {
		t.Run(name, func(t *testing.T) {
			task, rep := load(t, fixtureTask(name))
			if rep.HasErrors() {
				t.Fatalf("errors:\n%s", messages(rep.Errors()))
			}
			want := readUpstream(t, filepath.Join("testdata", "upstream", name+".json"))
			got := upstreamOfModel(t, task)
			compareUpstream(t, got, want, "checker")
			for _, d := range want.Datasets {
				if c, ok := d.Managers["checker"]; ok {
					if gc := got.Datasets[0].Managers["checker"]; gc != c {
						t.Logf("the checker compiled here (%s) is not the binary of the reference (%s): another compiler", gc, c)
					}
				}
			}
			importer.ValidateTask(task, nil, rep)
			if rep.HasErrors() {
				t.Errorf("validation errors:\n%s", messages(rep.Errors()))
			}
		})
	}
}

func TestLoadTask(t *testing.T) {
	needCompiler(t)
	task, rep := load(t, fixtureTask("polyaplusb"))
	if task.Name != "polyaplusb" || task.Title != "A plus B" {
		t.Errorf("name, title = %q, %q", task.Name, task.Title)
	}
	if !slices.Equal(task.PrimaryStatements, []string{"en", "es"}) || len(task.Statements) != 2 || task.Statements[0].Language != "en" {
		t.Errorf("statements %v, primary %v", task.Statements, task.PrimaryStatements)
	}
	if task.MinSubmissionInterval == nil || *task.MinSubmissionInterval != 30*time.Second || task.FeedbackLevel != "full" {
		t.Errorf("cms_conf.py general not applied: %+v", task)
	}
	d := task.Active()
	if d == nil || d.Description != "tests" || len(d.Testcases) != 5 || len(d.Managers) != 1 || d.Managers[0].Filename != "checker" {
		t.Fatalf("active dataset %+v", d)
	}
	// The checker was built with the testlib.h of CMS: the one of the
	// package has an #error.
	rc, err := d.Managers[0].File.Open()
	if err != nil {
		t.Fatal(err)
	}
	bin, _ := io.ReadAll(rc)
	rc.Close()
	if len(bin) < 4 || string(bin[1:4]) != "ELF" {
		t.Errorf("the checker is not an executable (%d bytes)", len(bin))
	}
	// The groups and points of Polygon are not used, but cms_conf.py
	// sets the score type: no warning.
	if w := messages(rep.Warnings()); w != "" {
		t.Errorf("warnings:\n%s", w)
	}

	// -S: no statements at all.
	rep = &importer.Report{}
	task, err = Loader{}.LoadTask(fixtureTask("polyaplusb"), &importer.Options{SkipStatements: true, Report: rep})
	if err != nil || len(task.Statements) != 0 || len(task.PrimaryStatements) != 0 {
		t.Errorf("SkipStatements: %v, %v, %v", err, task.Statements, task.PrimaryStatements)
	}
}

// writePackage writes a minimal Polygon package with files (relative
// path → content) and returns its directory, named name.
func writePackage(t *testing.T, name string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	for p, content := range files {
		path := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func packageXML(judging string) string {
	return `<?xml version="1.0" encoding="utf-8"?>
<problem><names><name language="english" value="T"/></names>` + judging + `</problem>`
}

const oneTest = `<judging input-file="" output-file="">
<testset name="tests"><time-limit>700</time-limit><memory-limit>1048576</memory-limit><test-count>1</test-count>
<input-path-pattern>tests/%02d</input-path-pattern><answer-path-pattern>tests/%02d.a</answer-path-pattern></testset></judging>`

func TestLoadTaskCases(t *testing.T) {
	base := map[string]string{"problem.xml": packageXML(oneTest), "tests/01": "1\n", "tests/01.a": "1\n"}
	with := func(extra map[string]string) map[string]string {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for k, v := range extra {
			if v == "" {
				delete(m, k)
			} else {
				m[k] = v
			}
		}
		return m
	}

	t.Run("defaults", func(t *testing.T) {
		task, rep := load(t, writePackage(t, "plain", base))
		d := task.Datasets[0]
		// 700 ms are 0.7 s (upstream: 0.7000000000000001).
		if *d.TimeLimit != 0.7 {
			t.Errorf("time limit %v", *d.TimeLimit)
		}
		if string(d.TaskTypeParameters) != `["alone", ["", ""], "diff"]` || string(d.ScoreTypeParameters) != "100.0" || d.ScoreType != "Sum" {
			t.Errorf("parameters %s %s %s", d.TaskTypeParameters, d.ScoreType, d.ScoreTypeParameters)
		}
		if len(d.Managers) != 0 || task.ActiveDataset != "tests" || d.Testcases[0].Codename != "000" || !d.Testcases[0].Public {
			t.Errorf("dataset %+v", d)
		}
		if rep.HasErrors() || len(rep.Warnings()) != 0 {
			t.Errorf("issues:\n%s", messages(rep.Issues()))
		}
		if !slices.Equal(task.SubmissionFormat, []string{"plain.%l"}) {
			t.Errorf("submission format %v", task.SubmissionFormat)
		}
	})

	t.Run("points and groups", func(t *testing.T) {
		xml := strings.ReplaceAll(oneTest, "</answer-path-pattern>", `</answer-path-pattern><tests><test group="1" points="100"/></tests>`)
		_, rep := load(t, writePackage(t, "x", with(map[string]string{"problem.xml": packageXML(xml)})))
		if w := messages(rep.Warnings()); !strings.Contains(w, "the points and groups of the tests are not used") {
			t.Errorf("warnings:\n%s", w)
		}
		_, rep = load(t, writePackage(t, "x", with(map[string]string{"problem.xml": packageXML(xml),
			"files/cms_conf.py": `datasets = {"tests": {"score_type": "GroupMin", "score_type_parameters": [[100, 1]]}}`})))
		if w := messages(rep.Warnings()); w != "" {
			t.Errorf("warnings with the score type in cms_conf.py:\n%s", w)
		}
	})

	t.Run("missing test", func(t *testing.T) {
		_, rep := load(t, writePackage(t, "x", with(map[string]string{"tests/01.a": ""})))
		if e := messages(rep.Errors()); !strings.Contains(e, "test 1:") || !strings.Contains(e, "01.a is missing") {
			t.Errorf("errors:\n%s", e)
		}
	})

	t.Run("checker does not compile", func(t *testing.T) {
		needCompiler(t)
		task, rep := load(t, writePackage(t, "x", with(map[string]string{"check.cpp": "#include \"testlib.h\"\nint main( {}\n"})))
		if e := messages(rep.Errors()); !strings.Contains(e, "could not compile the checker") {
			t.Errorf("errors:\n%s", e)
		}
		// The task still says comparator: validation reports the missing
		// manager.
		if !strings.Contains(string(task.Datasets[0].TaskTypeParameters), "comparator") {
			t.Errorf("parameters %s", task.Datasets[0].TaskTypeParameters)
		}
	})

	t.Run("no compiler", func(t *testing.T) {
		old := Compiler
		Compiler = "no-such-compiler-for-cms"
		defer func() { Compiler = old }()
		_, rep := load(t, writePackage(t, "x", with(map[string]string{"files/check.cpp": "// another source\nint main() {}\n"})))
		if e := messages(rep.Errors()); !strings.Contains(e, "no-such-compiler-for-cms is not installed") {
			t.Errorf("errors:\n%s", e)
		}
	})

	t.Run("cms_conf.py", func(t *testing.T) {
		task, rep := load(t, writePackage(t, "x", with(map[string]string{"files/cms_conf.py": `
import datetime
general = {"token_mode": "finite", "token_max_number": 3,
           "token_gen_interval": datetime.timedelta(minutes=10), "allowed_languages": ["C++20 / g++"]}
general["title"] = "Renamed"
datasets = {"tests": {"description": "Main", "time_limit": 2, "memory_limit": 256 * 1024 * 1024,
                      "task_type_parameters": ("grader", ("", ""), "diff"), "autojudge": True},
            "other": {}}
`})))
		if task.Title != "Renamed" || task.Tokens.Mode != "finite" || *task.Tokens.MaxNumber != 3 ||
			task.Tokens.GenInterval != 10*time.Minute || !slices.Equal(task.AllowedLanguages, []string{"C++20 / g++"}) {
			t.Errorf("general not applied: %+v", task)
		}
		d := task.Datasets[0]
		if d.Description != "Main" || task.ActiveDataset != "Main" || *d.TimeLimit != 2 || *d.MemoryLimit != 256<<20 ||
			string(d.TaskTypeParameters) != `["grader", ["", ""], "diff"]` || !d.Autojudge {
			t.Errorf("dataset not updated: %+v (params %s)", d, d.TaskTypeParameters)
		}
		if w := messages(rep.Warnings()); !strings.Contains(w, `ignored: "other"`) {
			t.Errorf("warnings:\n%s", w)
		}
	})

	t.Run("cms_conf.py errors", func(t *testing.T) {
		_, rep := load(t, writePackage(t, "x", with(map[string]string{"files/cms_conf.py": `
general = {"statements": {}, "score_precision": "2"}
datasets = {"tests": {"testcases": {}}}
`})))
		e := messages(rep.Errors())
		for _, want := range []string{"general: statements: not a column", `datasets["tests"]: testcases: not a column`} {
			if !strings.Contains(e, want) {
				t.Errorf("errors lack %q:\n%s", want, e)
			}
		}
		_, err := Loader{}.LoadTask(writePackage(t, "x", with(map[string]string{"files/cms_conf.py": "import os\nos.system('true')\n"})), nil)
		if err == nil || !strings.Contains(err.Error(), "cms_conf.py: line 2: unsupported statement") {
			t.Errorf("code in cms_conf.py: %v", err)
		}
	})

	t.Run("no testset tests", func(t *testing.T) {
		xml := strings.ReplaceAll(oneTest, `name="tests"`, `name="main"`)
		task, rep := load(t, writePackage(t, "x", with(map[string]string{"problem.xml": packageXML(xml)})))
		if task.Active() == nil || task.Active().Description != "main" {
			t.Errorf("active %v", task.Active())
		}
		if w := messages(rep.Warnings()); !strings.Contains(w, `no testset is called "tests"`) {
			t.Errorf("warnings:\n%s", w)
		}
	})

	t.Run("path patterns", func(t *testing.T) {
		// A pattern of problem.xml is followed; one that leaves the
		// package is not.
		xml := strings.ReplaceAll(oneTest, "tests/%02d.a", "answers/%03d.ans")
		xml = strings.ReplaceAll(xml, "tests/%02d<", "../../etc/%02d<")
		task, rep := load(t, writePackage(t, "x", with(map[string]string{"problem.xml": packageXML(xml), "answers/001.ans": "1\n", "tests/01.a": ""})))
		if rep.HasErrors() {
			t.Fatalf("errors:\n%s", messages(rep.Errors()))
		}
		tc := task.Datasets[0].Testcases[0]
		if !strings.HasSuffix(tc.Input.Name(), filepath.Join("x", "tests", "01")) || !strings.HasSuffix(tc.Output.Name(), filepath.Join("answers", "001.ans")) {
			t.Errorf("files %s %s", tc.Input.Name(), tc.Output.Name())
		}
	})

	t.Run("bad problem.xml", func(t *testing.T) {
		for _, files := range []map[string]string{
			{"problem.xml": "<problem>"},
			{"problem.xml": packageXML("")},
			{"problem.xml": `<problem><judging/></problem>`},
			{"problem.xml": packageXML(strings.ReplaceAll(oneTest, "<test-count>1</test-count>", ""))},
			{"problem.xml": packageXML(strings.ReplaceAll(oneTest, "700", "fast"))},
			{"problem.xml": `<?xml version="1.0" encoding="windows-1251"?><problem/>`},
		} {
			if _, err := (Loader{}).LoadTask(writePackage(t, "x", files), nil); err == nil {
				t.Errorf("no error for %s", files["problem.xml"])
			}
		}
		_, rep := load(t, writePackage(t, "x", map[string]string{"problem.xml": packageXML(`<judging input-file="" output-file=""/>`)}))
		if !strings.Contains(messages(rep.Errors()), "problem.xml has no testset") {
			t.Errorf("errors:\n%s", messages(rep.Errors()))
		}
	})
}

func TestLoadContest(t *testing.T) {
	rep := &importer.Report{}
	c, err := Loader{}.LoadContest(fixture, &importer.Options{Report: rep})
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "polycontest" || c.Description != "Polygon Test Round" || !slices.Equal(c.Tasks, []string{"polyaplusb", "polypairs"}) {
		t.Errorf("contest %q %q %v", c.Name, c.Description, c.Tasks)
	}
	if len(c.Groups) != 1 || c.Groups[0].Name != "default" || !c.Groups[0].Start.Equal(importer.ZeroTime) || !c.Groups[0].Stop.Equal(importer.ZeroTime) {
		t.Errorf("groups %+v", c.Groups)
	}
	if len(c.Participations) != 3 || len(c.Users) != 3 {
		t.Fatalf("participations %+v users %+v", c.Participations, c.Users)
	}
	bob := c.Participations[1]
	if bob.Username != "polybob" || *bob.Password != "plaintext:bobpw" || bob.Hidden == nil || *bob.Hidden {
		t.Errorf("bob %+v", bob)
	}
	if jury := c.Participations[2]; jury.Hidden == nil || !*jury.Hidden {
		t.Errorf("jury %+v", jury)
	}
	if u := c.Users[1]; u.FirstName != "Bob" || u.LastName != "Builder" || u.Password != "plaintext:bobpw" {
		t.Errorf("user %+v", u)
	}
	tl, dir := Loader{}.TaskLoader(fixture, "polypairs")
	if tl == nil || dir != fixtureTask("polypairs") {
		t.Errorf("TaskLoader = %v, %s", tl, dir)
	}
	importer.ValidateContest(c, nil, rep)
	if len(rep.Issues()) != 0 {
		t.Errorf("issues:\n%s", messages(rep.Issues()))
	}
}

func TestLoadUsers(t *testing.T) {
	users, err := Loader{}.LoadUsers(fixture, nil)
	if err != nil || len(users) != 3 || users[0].Username != "polyalice" {
		t.Fatalf("all: %+v, %v", users, err)
	}
	// As upstream: <contest>/<username> is that user.
	users, err = Loader{}.LoadUsers(filepath.Join(fixture, "polyjury"), nil)
	if err != nil || len(users) != 1 || users[0].Username != "polyjury" || users[0].FirstName != "Ju" {
		t.Fatalf("one: %+v, %v", users, err)
	}
	if _, err := (Loader{}).LoadUsers(filepath.Join(fixture, "nobody"), nil); err == nil || !strings.Contains(err.Error(), "user nobody not found") {
		t.Errorf("missing user: %v", err)
	}
}

func TestContestants(t *testing.T) {
	dir := writePackage(t, "c", map[string]string{contestantsFile: "\uFEFFa;pw\r\nb;pw;B;Bee;yes\r\nc;pw;C;See;off\n"})
	cs, err := readContestants(filepath.Join(dir, contestantsFile))
	if err != nil || len(cs) != 3 {
		t.Fatalf("%+v %v", cs, err)
	}
	if cs[0].username != "a" || cs[0].hidden != nil || cs[0].names || !*cs[1].hidden || *cs[2].hidden {
		t.Errorf("%+v", cs)
	}
	for _, bad := range []string{"a\n", "a;pw;A;B;maybe\n", "a;pw;A;B;\n"} {
		dir := writePackage(t, "c", map[string]string{contestantsFile: bad})
		if _, err := readContestants(filepath.Join(dir, contestantsFile)); err == nil {
			t.Errorf("no error for %q", bad)
		}
	}
	for s, want := range map[string]bool{"t": true, "TRUE": true, "y": true, "on": true, "1": true, "f": false, "no": false, "of": false, "0": false} {
		if got, ok := pgBool(s); !ok || got != want {
			t.Errorf("pgBool(%q) = %v, %v", s, got, ok)
		}
	}
	for _, s := range []string{"", "o", "2", "truth"} {
		if _, ok := pgBool(s); ok {
			t.Errorf("pgBool(%q) accepted", s)
		}
	}
}

// TestCompileCache: the same source compiles once.
func TestCompileCache(t *testing.T) {
	needCompiler(t)
	src := filepath.Join(t.TempDir(), "check.cpp")
	if err := os.WriteFile(src, []byte("// cache test\n#include \"testlib.h\"\nint main(int argc, char* argv[]) { registerTestlibCmd(argc, argv); quitf(_ok, \"ok\"); }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := compileChecker(src)
	if err != nil {
		t.Fatal(err)
	}
	old := Compiler
	defer func() { Compiler = old }()
	Compiler = old + " " // another name: a cache miss would fail
	if _, err := compileChecker(src); err == nil {
		t.Fatal("the compiler name is part of the key")
	}
	Compiler = old
	b, err := compileChecker(src)
	if err != nil || &a[0] != &b[0] {
		t.Errorf("not cached: %v", err)
	}
}
