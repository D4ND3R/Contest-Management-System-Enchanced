// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package italy

import (
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
)

func TestRegistered(t *testing.T) {
	l, ok := importer.Default.Lookup("italy_yaml")
	if !ok || l.Name() != Name {
		t.Fatalf("italy_yaml is not registered: %v", l)
	}
	for _, k := range []importer.Kind{importer.KindContest, importer.KindTask, importer.KindUsers, importer.KindTeams} {
		if !importer.Supports(l, k) {
			t.Errorf("does not support %s", k)
		}
	}
}

func TestDetect(t *testing.T) {
	dir, _ := copyDemo(t)
	empty := t.TempDir()
	deprecated := writeFiles(t, t.TempDir(), map[string]string{"old.yaml": "name: old\n", "old/input/input0.txt": ""})
	cases := []struct {
		dir  string
		kind importer.Kind
		want bool
	}{
		{dir, importer.KindContest, true},
		{dir, importer.KindTask, false},
		{dir, importer.KindUsers, true},
		{dir, importer.KindTeams, true},
		{filepath.Join(dir, "sum"), importer.KindTask, true},
		{filepath.Join(dir, "sum"), importer.KindContest, false},
		{filepath.Join(dir, "alice"), importer.KindUsers, true}, // upstream style: <contest>/<username>
		{filepath.Join(deprecated, "old"), importer.KindTask, true},
		{empty, importer.KindContest, false},
		{empty, importer.KindTask, false},
		{empty, importer.KindUsers, false},
	}
	for _, c := range cases {
		if got := (Loader{}).Detect(c.dir, c.kind); got != c.want {
			t.Errorf("Detect(%s, %s) = %v, want %v", c.dir, c.kind, got, c.want)
		}
	}
}

func TestParseGEN(t *testing.T) {
	cases := []struct {
		gen      string
		n        int
		subtasks []Subtask
		err      string
	}{
		{"gen 1\n\ngen 2\n# a comment\ngen 3 # with a comment\n", 3, nil, ""},
		{"#ST: 30\ngen 1\n#COPY: a.txt\n# ST: 70\n  gen 3  \n", 3, []Subtask{{30, 2}, {70, 1}}, ""},
		{"# ST: 100\n#ST: 0\n", 0, []Subtask{{100, 0}, {0, 0}}, ""},
		{"gen 1\n#ST: 100\ngen 2\n", 0, nil, "before the first #ST"},
		{"gen 1 # COPY: x\n", 0, nil, "same line"},
		{"gen 1 #ST: 10\n", 0, nil, "same line"},
		{"#ST: ten\n", 0, nil, "invalid points"},
	}
	for _, c := range cases {
		n, st, err := ParseGEN(strings.NewReader(c.gen))
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q: error %v, want %q", c.gen, err, c.err)
			}
			continue
		}
		if err != nil || n != c.n || !reflect.DeepEqual(st, c.subtasks) {
			t.Errorf("%q: %d %v %v, want %d %v", c.gen, n, st, err, c.n, c.subtasks)
		}
	}
}

func TestPyFloat(t *testing.T) {
	for f, want := range map[float64]string{
		50: "50.0", 100.0 / 3: "33.333333333333336", 0: "0.0", 0.5: "0.5", -2: "-2.0",
		1e16: "1e+16", 1e15: "1000000000000000.0", 1e-5: "1e-05", 0.0001: "0.0001", 1.5e-7: "1.5e-07",
	} {
		if got := pyFloat(f); got != want {
			t.Errorf("pyFloat(%v) = %s, want %s", f, got, want)
		}
	}
}

// task writes a task directory with a task.yaml and n testcases.
func task(t testing.TB, yaml string, n int, extra map[string]string) string {
	t.Helper()
	files := map[string]string{"task.yaml": yaml, "statement/statement.pdf": "%PDF statement"}
	for i := range n {
		files["input/input"+itoa(i)+".txt"] = "in " + itoa(i)
		files["output/output"+itoa(i)+".txt"] = "out " + itoa(i)
	}
	for k, v := range extra {
		files[k] = v
	}
	return writeFiles(t, filepath.Join(t.TempDir(), "task"), files)
}

func itoa(i int) string { return strconv.Itoa(i) }

func TestTaskGradersHeadersAndChecker(t *testing.T) {
	dir := task(t, "name: g\ntitle: Graders\nn_input: 2\nmemlimit: 64\ntimeout: 2\ntoken_mode: disabled\n", 2, map[string]string{
		"sol/grader.cpp": "grader", "sol/grader.c": "grader", "sol/g.h": "header", "sol/glib.pas": "pascal header",
		"sol/solution.cpp": "not a manager", "cor/correttore": "checker",
	})
	tk, rep := loadTask(t, dir)
	if tk == nil {
		t.Fatal(issues(rep))
	}
	d := tk.Datasets[0]
	if want := []string{"checker", "g.h", "glib.pas", "grader.c", "grader.cpp"}; !slices.Equal(managers(d), want) {
		t.Errorf("managers %v, want %v", managers(d), want)
	}
	if !sameJSON(t, d.TaskTypeParameters, `["grader", ["input.txt", "output.txt"], "comparator"]`) {
		t.Errorf("params %s", d.TaskTypeParameters)
	}
	if *d.MemoryLimit != 64*importer.MiB || *d.TimeLimit != 2 {
		t.Errorf("limits %v %v", *d.TimeLimit, *d.MemoryLimit)
	}
	if string(d.ScoreTypeParameters) != "50.0" || d.ScoreType != "Sum" {
		t.Errorf("score %s %s", d.ScoreType, d.ScoreTypeParameters)
	}
	if !hasIssue(rep, importer.SeverityWarning, "grader not found for: ") || !hasIssue(rep, importer.SeverityWarning, "Java / JDK") {
		t.Errorf("no warning about the missing graders:\n%s", issues(rep))
	}
}

func TestTaskStubs(t *testing.T) {
	dir := task(t, "name: s\ntitle: Stubs\nn_input: 1\ntoken_mode: disabled\n", 1, map[string]string{
		"sol/stub.cpp": "stub", "check/manager": "manager",
	})
	tk, rep := loadTask(t, dir)
	if tk == nil {
		t.Fatal(issues(rep))
	}
	d := tk.Datasets[0]
	if d.TaskType != "Communication" || !sameJSON(t, d.TaskTypeParameters, `[1, "grader", "fifo_io"]`) {
		t.Errorf("task type %s %s", d.TaskType, d.TaskTypeParameters)
	}
	if want := []string{"grader.cpp", "manager"}; !slices.Equal(managers(d), want) {
		t.Errorf("managers %v", managers(d))
	}

	// Both graders and stubs.
	dir = task(t, "name: s\ntitle: Stubs\nn_input: 1\ntoken_mode: disabled\n", 1, map[string]string{
		"sol/stub.cpp": "stub", "sol/grader.c": "grader",
	})
	if tk, rep = loadTask(t, dir); tk != nil || !hasIssue(rep, importer.SeverityError, "both sol/grader and sol/stub") {
		t.Errorf("graders and stubs accepted:\n%s", issues(rep))
	}
}

func TestTaskCommunication(t *testing.T) {
	dir := task(t, "name: c\ntitle: Comm\nn_input: 1\nnum_processes: 2\nuser_io: fifo_io\ntoken_mode: disabled\n", 1,
		map[string]string{"cor/manager": "manager"})
	tk, rep := loadTask(t, dir)
	if d := tk.Datasets[0]; d.TaskType != "Communication" || !sameJSON(t, d.TaskTypeParameters, `[2, "alone", "fifo_io"]`) {
		t.Errorf("task type %s %s\n%s", d.TaskType, d.TaskTypeParameters, issues(rep))
	}
	dir = task(t, "name: c\ntitle: Comm\nn_input: 1\nuser_io: pipes\ntoken_mode: disabled\n", 1,
		map[string]string{"check/manager": "manager"})
	tk, rep = loadTask(t, dir)
	if d := tk.Datasets[0]; !sameJSON(t, d.TaskTypeParameters, `[1, "alone", "std_io"]`) || !hasIssue(rep, importer.SeverityWarning, "user_io incorrect") {
		t.Errorf("params %s\n%s", d.TaskTypeParameters, issues(rep))
	}
}

func TestTaskInteractive(t *testing.T) {
	dir := task(t, "name: i\ntitle: Inter\nn_input: 1\ncontroller_process_limit: 3\ninteractive_concurrent: no\n"+
		"controller_memory_limit: 256\ncontroller_time_limit: 10\ncontroller_wall_time_limit: 20.5\ntoken_mode: disabled\n", 1,
		map[string]string{"check/controller": "controller"})
	tk, rep := loadTask(t, dir)
	d := tk.Datasets[0]
	if d.TaskType != "Interactive" || !sameJSON(t, d.TaskTypeParameters, `[3, "alone", false, 256, 10, 20.5]`) ||
		!slices.Equal(managers(d), []string{"controller"}) {
		t.Errorf("task type %s %s %v\n%s", d.TaskType, d.TaskTypeParameters, managers(d), issues(rep))
	}
	importer.ValidateTask(tk, nil, rep)
	if rep.HasErrors() {
		t.Errorf("invalid: %s", issues(rep))
	}

	// Without the limits of the controller CMS cannot judge it.
	dir = task(t, "name: i\ntitle: Inter\nn_input: 1\ntoken_mode: disabled\n", 1, map[string]string{"cor/controller": "controller"})
	tk, rep = loadTask(t, dir)
	if !sameJSON(t, tk.Datasets[0].TaskTypeParameters, `[200, "alone", true, null, null, null]`) ||
		!hasIssue(rep, importer.SeverityError, "controller_memory_limit, controller_time_limit, controller_wall_time_limit") {
		t.Errorf("params %s\n%s", tk.Datasets[0].TaskTypeParameters, issues(rep))
	}

	// At most one of controller, manager, output_only.
	dir = task(t, "name: i\ntitle: Inter\nn_input: 1\noutput_only: true\ntoken_mode: disabled\n", 1, map[string]string{"cor/manager": "m"})
	if tk, rep = loadTask(t, dir); tk != nil || !hasIssue(rep, importer.SeverityError, "at most one") {
		t.Errorf("accepted:\n%s", issues(rep))
	}
}

func TestTaskBatchAndOutput(t *testing.T) {
	dir := task(t, "name: bo\ntitle: BO\nn_input: 4\noutput_only_testcases: \"3, 1\"\noutput_optional_testcases: 2\ntoken_mode: disabled\n", 4, nil)
	tk, rep := loadTask(t, dir)
	d := tk.Datasets[0]
	if d.TaskType != "BatchAndOutput" || !sameJSON(t, d.TaskTypeParameters, `["alone", ["input.txt", "output.txt"], "diff", "001,003"]`) {
		t.Errorf("task type %s %s\n%s", d.TaskType, d.TaskTypeParameters, issues(rep))
	}
	if want := []string{"bo.%l", "output_001.txt", "output_002.txt", "output_003.txt"}; !slices.Equal(tk.SubmissionFormat, want) {
		t.Errorf("submission format %v", tk.SubmissionFormat)
	}
	var att []string
	for _, a := range tk.Attachments {
		att = append(att, a.Filename)
	}
	if want := []string{"input_001.txt", "input_002.txt", "input_003.txt"}; !slices.Equal(att, want) {
		t.Errorf("attachments %v", att)
	}
	importer.ValidateTask(tk, nil, rep)
	if rep.HasErrors() {
		t.Errorf("invalid: %s", issues(rep))
	}
}

func TestTaskScoreTypes(t *testing.T) {
	// Explicit score type, with all three keys.
	dir := task(t, "name: st\ntitle: ST\nn_input: 2\nscore_type: GroupMul\nscore_type_parameters: [[50.0, \"00[01]\"]]\ntoken_mode: disabled\n", 2, nil)
	tk, rep := loadTask(t, dir)
	if d := tk.Datasets[0]; d.ScoreType != "GroupMul" || string(d.ScoreTypeParameters) != `[[50.0, "00[01]"]]` {
		t.Errorf("score %s %s\n%s", d.ScoreType, d.ScoreTypeParameters, issues(rep))
	}

	// Without n_input it is ignored, with a warning; total_value shares.
	dir = task(t, "name: st\ntitle: ST\nscore_type: GroupMul\ntotal_value: 30\ntoken_mode: disabled\n", 3,
		map[string]string{"gen/GEN": "a\nb\nc\n"})
	tk, rep = loadTask(t, dir)
	if d := tk.Datasets[0]; d.ScoreType != "Sum" || string(d.ScoreTypeParameters) != "10.0" || len(d.Testcases) != 3 ||
		!hasIssue(rep, importer.SeverityWarning, "must specify all") {
		t.Errorf("score %s %s\n%s", d.ScoreType, d.ScoreTypeParameters, issues(rep))
	}

	// gen/GEN with subtasks that do not add up to 100.
	dir = task(t, "name: st\ntitle: ST\ntoken_mode: disabled\n", 2, map[string]string{"gen/GEN": "#ST: 10\na\n#ST: 20\nb\n"})
	if tk, rep = loadTask(t, dir); tk != nil || !hasIssue(rep, importer.SeverityError, "add up to 30") {
		t.Errorf("accepted:\n%s", issues(rep))
	}

	// n_input must agree with gen/GEN.
	dir = task(t, "name: st\ntitle: ST\nn_input: 3\ntoken_mode: disabled\n", 2, map[string]string{"gen/GEN": "a\nb\n"})
	if tk, rep = loadTask(t, dir); tk != nil || !hasIssue(rep, importer.SeverityError, "n_input is 3 but gen/GEN has 2") {
		t.Errorf("accepted:\n%s", issues(rep))
	}

	// Neither gen/GEN nor n_input.
	dir = task(t, "name: st\ntitle: ST\ntoken_mode: disabled\n", 2, nil)
	if tk, rep = loadTask(t, dir); tk != nil || !hasIssue(rep, importer.SeverityError, "n_input is missing") {
		t.Errorf("accepted:\n%s", issues(rep))
	}

	// No testcases: 0.0 points each.
	dir = task(t, "name: st\ntitle: ST\nn_input: 0\ntoken_mode: disabled\n", 0, nil)
	if tk, rep = loadTask(t, dir); tk == nil || string(tk.Datasets[0].ScoreTypeParameters) != "0.0" {
		t.Errorf("no testcases:\n%s", issues(rep))
	}
}

func TestTaskPublicTestcases(t *testing.T) {
	for yaml, want := range map[string][]bool{
		"public_testcases: all":         {true, true, true},
		"risultati: \"0, 2\"":           {true, false, true},
		"public_testcases: 1":           {false, true, false},
		"public_testcases: \"-1\"":      {false, false, true},
		"public_testcases: [0, 1]":      {true, true, false},
		"public_testcases: \"\"":        {false, false, false},
		"public_testcases:":             {false, false, false},
		"public_testcases: \"0, five\"": nil,
		"public_testcases: \"3\"":       nil,
	} {
		dir := task(t, "name: p\ntitle: P\nn_input: 3\ntoken_mode: disabled\n"+yaml+"\n", 3, nil)
		tk, rep := loadTask(t, dir)
		if want == nil {
			if !rep.HasErrors() {
				t.Errorf("%s: accepted", yaml)
			}
			continue
		}
		var got []bool
		for _, tc := range tk.Datasets[0].Testcases {
			got = append(got, tc.Public)
		}
		if !slices.Equal(got, want) || rep.HasErrors() {
			t.Errorf("%s: %v, want %v\n%s", yaml, got, want, issues(rep))
		}
	}
}

func TestTaskStatements(t *testing.T) {
	// testo/ with one file per language; testo.pdf, not a link, is
	// ignored with a warning.
	dir := writeFiles(t, filepath.Join(t.TempDir(), "multi"), map[string]string{
		"task.yaml":         "nome_breve: multi\nnome: Multi\nn_input: 0\nprimary_language: en\ntoken_mode: disabled\n",
		"testo/testo.pdf":   "single",
		"testo/english.pdf": "en", "testo/italian.pdf": "it", "testo/spanish.pdf": "es",
	})
	tk, rep := loadTask(t, dir)
	var langs []string
	for _, s := range tk.Statements {
		langs = append(langs, s.Language)
	}
	if !slices.Equal(langs, []string{"en", "es", "it"}) || !slices.Equal(tk.PrimaryStatements, []string{"en"}) ||
		!hasIssue(rep, importer.SeverityWarning, "not a symlink") {
		t.Errorf("statements %v %v\n%s", langs, tk.PrimaryStatements, issues(rep))
	}
	// A link is fine.
	if err := os.Remove(filepath.Join(dir, "testo", "testo.pdf")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("english.pdf", filepath.Join(dir, "testo", "testo.pdf")); err != nil {
		t.Fatal(err)
	}
	if _, rep = loadTask(t, dir); len(rep.Issues()) != 0 {
		t.Errorf("issues with a link:\n%s", issues(rep))
	}
	// The primary language needs a statement.
	if err := os.Remove(filepath.Join(dir, "testo", "english.pdf")); err != nil {
		t.Fatal(err)
	}
	if _, rep = loadTask(t, dir); !hasIssue(rep, importer.SeverityError, "primary language en") {
		t.Errorf("no error:\n%s", issues(rep))
	}
	// -S does not read them.
	opts, rep := load()
	opts.SkipStatements = true
	if tk, err := (Loader{}).LoadTask(dir, opts); err != nil || len(tk.Statements) != 0 || len(tk.PrimaryStatements) != 0 || rep.HasErrors() {
		t.Errorf("skip statements: %v %v\n%s", tk, err, issues(rep))
	}

	// Both folders; none.
	dir = task(t, "name: b\ntitle: B\nn_input: 0\ntoken_mode: disabled\n", 0, map[string]string{"testo/testo.pdf": "x"})
	if _, rep = loadTask(t, dir); !hasIssue(rep, importer.SeverityError, "both testo/ and statement/") {
		t.Errorf("both folders:\n%s", issues(rep))
	}
	dir = writeFiles(t, filepath.Join(t.TempDir(), "n"), map[string]string{"task.yaml": "name: n\ntitle: N\nn_input: 0\ntoken_mode: disabled\n"})
	if _, rep = loadTask(t, dir); !hasIssue(rep, importer.SeverityError, "statement folder not found") {
		t.Errorf("no folder:\n%s", issues(rep))
	}
}

func TestTaskDeprecatedLocationAndTokens(t *testing.T) {
	root := writeFiles(t, t.TempDir(), map[string]string{
		"old.yaml": "name: old\ntitle: Old\nn_input: 1\ntoken_initial: 3\ntoken_total: 10\ntoken_gen_number: 1\n" +
			"token_gen_time: 0\ntoken_max: 5\ntoken_min_interval: 1.5\nmax_submission_number: 7\nmin_user_test_interval: 60\n" +
			"feedback_level: full\nscore_mode: max_subtask\nscore_precision: 2\nversion: v2\n",
		"old/statement/statement.pdf": "x", "old/input/input0.txt": "", "old/output/output0.txt": "",
		"old/att/sample.txt": "s",
	})
	tk, rep := loadTask(t, filepath.Join(root, "old"))
	if tk == nil {
		t.Fatal(issues(rep))
	}
	want := importer.Tokens{Mode: "infinite", MaxNumber: importer.Ptr(int32(10)), MinInterval: 1500 * time.Millisecond,
		GenInitial: 3, GenNumber: 1, GenInterval: time.Minute, GenMax: importer.Ptr(int32(5))}
	if !reflect.DeepEqual(tk.Tokens, want) {
		t.Errorf("tokens %+v, want %+v", tk.Tokens, want)
	}
	if !hasIssue(rep, importer.SeverityWarning, "deprecated location") || !hasIssue(rep, importer.SeverityWarning, "deprecated format for token") {
		t.Errorf("warnings:\n%s", issues(rep))
	}
	if *tk.MaxSubmissionNumber != 7 || *tk.MinUserTestInterval != time.Minute || tk.FeedbackLevel != "full" ||
		tk.ScoreMode != "max_subtask" || tk.ScorePrecision != 2 || tk.Datasets[0].Description != "v2" ||
		len(tk.Attachments) != 1 || tk.Attachments[0].Filename != "sample.txt" {
		t.Errorf("task %+v", tk)
	}

	// The old format: no token_initial disables the tokens.
	dir := task(t, "name: d\ntitle: D\nn_input: 0\n", 0, nil)
	tk, _ = loadTask(t, dir)
	if want := (importer.Tokens{Mode: "disabled", GenInterval: time.Minute}); !reflect.DeepEqual(tk.Tokens, want) {
		t.Errorf("tokens %+v, want %+v", tk.Tokens, want)
	}
	// token_initial with a generation time: finite.
	dir = task(t, "name: d\ntitle: D\nn_input: 0\ntoken_initial: 1\ntoken_gen_time: 120\n", 0, nil)
	tk, _ = loadTask(t, dir)
	if tk.Tokens.Mode != "finite" || tk.Tokens.GenInterval != 2*time.Minute {
		t.Errorf("tokens %+v", tk.Tokens)
	}
}

func TestTaskErrors(t *testing.T) {
	if _, err := (Loader{}).LoadTask(t.TempDir(), nil); err == nil || !strings.Contains(err.Error(), "file missing") {
		t.Errorf("missing task.yaml: %v", err)
	}
	dir := writeFiles(t, filepath.Join(t.TempDir(), "x"), map[string]string{"task.yaml": "- a list\n"})
	if _, err := (Loader{}).LoadTask(dir, nil); err == nil || !strings.Contains(err.Error(), "mapping") {
		t.Errorf("not a mapping: %v", err)
	}
	dir = writeFiles(t, filepath.Join(t.TempDir(), "x"), map[string]string{"task.yaml": "name: [\n"})
	if _, err := (Loader{}).LoadTask(dir, nil); err == nil {
		t.Errorf("invalid YAML accepted")
	}
	dir = task(t, "title: No name\n", 0, nil)
	if tk, rep := loadTask(t, dir); tk != nil || !hasIssue(rep, importer.SeverityError, "no name") {
		t.Errorf("no name:\n%s", issues(rep))
	}
	dir = task(t, "name: x\ntitle: x\nn_input: 1\ntimeout: fast\nmemlimit: 1.0000001\ninfile:\ntoken_mode: disabled\n", 1, nil)
	tk, rep := loadTask(t, dir)
	for _, s := range []string{"timeout: expected a number", "not a whole number of bytes", "infile is null"} {
		if !hasIssue(rep, importer.SeverityError, s) {
			t.Errorf("no error %q:\n%s", s, issues(rep))
		}
	}
	if tk == nil || !hasIssue(rep, importer.SeverityWarning, "short name equals long name") {
		t.Errorf("no warning about the title:\n%s", issues(rep))
	}
}

func TestContestGroupsAndPeople(t *testing.T) {
	dir := writeFiles(t, t.TempDir(), map[string]string{"contest.yaml": `
nome_breve: c
nome: Contest
token_mode: disabled
timezone: Europe/Rome
max_submission_number: 50
min_submission_interval: 30
allow_registration: yes
ip_autologin: true
score_precision: 1
allowed_localizations: [it, en]
problemi: [a, b]
main_group: late
groups:
  - name: early
    start: 2026-03-01 09:00:00
    stop: 2026-03-01T14:00:00+01:00
    analysis_enabled: true
    analysis_start: 1772384400
    analysis_stop: 1772391600
  - name: late
    start: 1772384400
    stop: 1772402400
    per_user_time: 3600
utenti:
  - username: u1
    password: 1234
    nome: Uno
    cognome: Primo
    team: t1
    ip: 10.0.0.0/8, 192.168.1.1
    hidden: true
    delay: 90
    group: early
  - username: u2
    team:
teams:
  - code: t1
    name: Team One
`})
	opts, rep := load()
	c, err := Loader{}.LoadContest(dir, opts)
	if err != nil || c == nil || rep.HasErrors() {
		t.Fatalf("%v\n%s", err, issues(rep))
	}
	if c.Name != "c" || c.Description != "Contest" || *c.Timezone != "Europe/Rome" || *c.MaxSubmissionNumber != 50 ||
		*c.MinSubmissionInterval != 30*time.Second || !c.AllowRegistration || !c.IPAutologin || c.ScorePrecision != 1 ||
		!slices.Equal(c.AllowedLocalizations, []string{"it", "en"}) || !slices.Equal(c.Tasks, []string{"a", "b"}) ||
		c.Tokens.Mode != "disabled" || c.MainGroup != "late" {
		t.Errorf("contest %+v", c)
	}
	early := importer.NewGroup("early")
	early.Start = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	early.Stop = time.Date(2026, 3, 1, 13, 0, 0, 0, time.UTC)
	early.AnalysisEnabled = true
	early.AnalysisStart = time.Unix(1772384400, 0).UTC()
	early.AnalysisStop = time.Unix(1772391600, 0).UTC()
	late := importer.NewGroup("late")
	late.Start, late.Stop = time.Unix(1772384400, 0).UTC(), time.Unix(1772402400, 0).UTC()
	late.PerUserTime = importer.Ptr(time.Hour)
	if !reflect.DeepEqual(c.Groups, []importer.Group{early, late}) {
		t.Errorf("groups\n got %+v\nwant %+v", c.Groups, []importer.Group{early, late})
	}
	pw := "plaintext:1234"
	wantP := []importer.Participation{
		{Username: "u1", Group: "early", Team: importer.Ptr("t1"), Password: &pw,
			IP:     []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("192.168.1.1/32")},
			Hidden: importer.Ptr(true), DelayTime: importer.Ptr(90 * time.Second)},
		{Username: "u2", Team: importer.Ptr("")},
	}
	if !reflect.DeepEqual(c.Participations, wantP) {
		t.Errorf("participations\n got %+v\nwant %+v", c.Participations, wantP)
	}
	wantU := []importer.User{{Username: "u1", FirstName: "Uno", LastName: "Primo", Password: pw}, {Username: "u2", LastName: "u2"}}
	if !reflect.DeepEqual(c.Users, wantU) || !reflect.DeepEqual(c.Teams, []importer.Team{{Code: "t1", Name: "Team One"}}) {
		t.Errorf("users %+v teams %+v", c.Users, c.Teams)
	}
	importer.ValidateContest(c, nil, rep)
	if rep.HasErrors() {
		t.Errorf("invalid contest:\n%s", issues(rep))
	}

	// Users and teams, all of them or the one named by the path.
	users, err := Loader{}.LoadUsers(dir, nil)
	if err != nil || !reflect.DeepEqual(users, wantU) {
		t.Errorf("LoadUsers: %v %v", users, err)
	}
	users, err = Loader{}.LoadUsers(filepath.Join(dir, "u2"), nil)
	if err != nil || !reflect.DeepEqual(users, wantU[1:]) {
		t.Errorf("LoadUsers(u2): %v %v", users, err)
	}
	if _, err = (Loader{}).LoadUsers(filepath.Join(dir, "nobody"), nil); err == nil {
		t.Error("LoadUsers(nobody) succeeded")
	}
	teams, err := Loader{}.LoadTeams(filepath.Join(dir, "t1"), nil)
	if err != nil || len(teams) != 1 || teams[0].Name != "Team One" {
		t.Errorf("LoadTeams: %v %v", teams, err)
	}
	if _, err = (Loader{}).LoadUsers(t.TempDir(), nil); err == nil {
		t.Error("LoadUsers without contest.yaml succeeded")
	}
}

func TestContestErrors(t *testing.T) {
	// Times of the contest together with groups.
	dir := writeFiles(t, t.TempDir(), map[string]string{"contest.yaml": "name: c\nstart: 0\ngroups:\n  - name: a\n  - name: b\n  - {}\nusers:\n  - password: x\n  - 3\n"})
	opts, rep := load()
	c, err := Loader{}.LoadContest(dir, opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"when using groups", "the group has no name", "the user has no username", "expected a mapping"} {
		if !hasIssue(rep, importer.SeverityError, s) {
			t.Errorf("no error %q:\n%s", s, issues(rep))
		}
	}
	// Several groups and no main_group: "main", which does not exist.
	if c.MainGroup != "main" {
		t.Errorf("main group %q", c.MainGroup)
	}
	if _, err := (Loader{}).LoadContest(t.TempDir(), nil); err == nil || !strings.Contains(err.Error(), "file missing") {
		t.Errorf("missing contest.yaml: %v", err)
	}
	dir = writeFiles(t, t.TempDir(), map[string]string{"contest.yaml": "description: no name\nstart: soon\nusers:\n  - username: a\n    ip: 10.0.0.1/8\n"})
	opts, rep = load()
	if c, err := (Loader{}).LoadContest(dir, opts); err != nil || c != nil {
		t.Errorf("contest without a name: %v %v", c, err)
	}
	for _, s := range []string{"no name", "start: expected a time", "host bits"} {
		if !hasIssue(rep, importer.SeverityError, s) {
			t.Errorf("no error %q:\n%s", s, issues(rep))
		}
	}
}
