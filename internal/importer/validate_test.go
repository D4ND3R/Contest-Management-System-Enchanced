// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package importer

import (
	"encoding/json"
	"errors"
	"net/netip"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestValidTaskHasNoErrors(t *testing.T) {
	rep := &Report{}
	ValidateTask(batchTask(t, t.TempDir(), "sum", 4), nil, rep)
	if len(rep.Issues()) != 0 {
		t.Errorf("issues:\n%s", issues(rep))
	}
	if rep.Err() != nil {
		t.Errorf("Err() = %v", rep.Err())
	}
}

func TestValidateTask(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name   string
		change func(*Task)
		sev    Severity
		where  string
		msg    string
	}{
		{"bad name", func(tk *Task) { tk.Name = "a b" }, SeverityError, "task a b", "invalid task name"},
		{"no title", func(tk *Task) { tk.Title = " " }, SeverityWarning, "task sum", "no title"},
		{"bad submission format", func(tk *Task) { tk.SubmissionFormat = []string{"../x"} }, SeverityError, "task sum", "invalid submission format file"},
		{"duplicate submission format", func(tk *Task) { tk.SubmissionFormat = []string{"a.%l", "a.%l"} }, SeverityError, "task sum", `duplicate submission format file "a.%l"`},
		{"unknown allowed language", func(tk *Task) { tk.AllowedLanguages = []string{"Cobol"} }, SeverityError, "task sum", `unknown allowed language "Cobol"`},
		{"token mode", func(tk *Task) { tk.Tokens.Mode = "some" }, SeverityError, "task sum", "invalid token mode"},
		{"token gen max", func(tk *Task) { tk.Tokens.GenMax = Ptr(int32(1)) }, SeverityError, "task sum", "token_gen_initial (2) is larger than token_gen_max (1)"},
		{"token interval", func(tk *Task) { tk.Tokens.GenInterval = 0 }, SeverityError, "task sum", "token_gen_interval must be positive"},
		{"max submissions", func(tk *Task) { tk.MaxSubmissionNumber = Ptr(int32(0)) }, SeverityError, "task sum", "max_submission_number must be positive"},
		{"min interval", func(tk *Task) { tk.MinSubmissionInterval = Ptr(time.Duration(0)) }, SeverityError, "task sum", "min_submission_interval must be positive"},
		{"feedback level", func(tk *Task) { tk.FeedbackLevel = "all" }, SeverityError, "task sum", "invalid feedback level"},
		{"score mode", func(tk *Task) { tk.ScoreMode = "best" }, SeverityError, "task sum", "invalid score mode"},
		{"precision", func(tk *Task) { tk.ScorePrecision = -1 }, SeverityError, "task sum", "negative score precision"},
		{"duplicate statement", func(tk *Task) { tk.Statements[1].Language = "en" }, SeverityError, "task sum", `duplicate statement language "en"`},
		{"primary statement", func(tk *Task) { tk.PrimaryStatements = []string{"es"} }, SeverityWarning, "task sum", `primary statement language "es" has no statement`},
		{"missing file", func(tk *Task) { tk.Statements[0].File = FileFromPath(dir + "/nope.pdf") }, SeverityError, "task sum: statement en", "cannot read"},
		{"nil file", func(tk *Task) { tk.Attachments[0].File = nil }, SeverityError, "task sum: attachment sample.txt", "no content"},
		{"bad attachment", func(tk *Task) { tk.Attachments[0].Filename = ".." }, SeverityError, "task sum", "invalid attachment file name"},
		{"bad digest", func(tk *Task) { tk.Attachments[0].File = FileFromDigest("x", "abc") }, SeverityError, "task sum: attachment", `invalid digest "abc"`},
		{"no dataset", func(tk *Task) { tk.Datasets = nil }, SeverityError, "task sum", "no dataset"},
		{"active dataset", func(tk *Task) { tk.ActiveDataset = "v9" }, SeverityError, "task sum", `the active dataset "v9" does not exist`},
		{"duplicate dataset", func(tk *Task) { tk.Datasets = append(tk.Datasets, tk.Datasets[0]) }, SeverityError, "task sum", `duplicate dataset "Default"`},
		{"time limit", func(tk *Task) { tk.Datasets[0].TimeLimit = Ptr(0.0) }, SeverityError, `dataset "Default"`, "invalid time limit"},
		{"no time limit", func(tk *Task) { tk.Datasets[0].TimeLimit = nil }, SeverityWarning, `dataset "Default"`, "no time limit"},
		{"memory limit", func(tk *Task) { tk.Datasets[0].MemoryLimit = Ptr(int64(1000)) }, SeverityError, `dataset "Default"`, "multiple of 1 MiB"},
		{"unknown task type", func(tk *Task) { tk.Datasets[0].TaskType = "Batchy" }, SeverityError, `dataset "Default"`, `unknown task type "Batchy"`},
		{"task type parameters", func(tk *Task) { tk.Datasets[0].TaskTypeParameters = json.RawMessage(`["alone"]`) },
			SeverityError, `dataset "Default"`, `invalid parameters ["alone"] for task type Batch: Task type Batch should have 3 parameters, received 1`},
		{"missing checker", func(tk *Task) { tk.Datasets[0].Managers = nil }, SeverityError, `dataset "Default"`, `task type Batch needs the manager "checker"`},
		{"missing grader", func(tk *Task) {
			tk.Datasets[0].TaskTypeParameters = json.RawMessage(`["grader", ["", ""], "comparator"]`)
		}, SeverityError, `dataset "Default"`, "needs grader.%l"},
		{"unknown score type", func(tk *Task) { tk.Datasets[0].ScoreType = "Max" }, SeverityError, `dataset "Default"`, `unknown score type "Max"`},
		{"score type parameters", func(tk *Task) { tk.Datasets[0].ScoreTypeParameters = json.RawMessage(`[[40, 3], [60, 3]]`) },
			SeverityError, `dataset "Default"`, "invalid parameters [[40,3],[60,3]] for score type GroupMin"},
		{"duplicate testcase", func(tk *Task) { tk.Datasets[0].Testcases[1].Codename = "000" }, SeverityError, `dataset "Default"`, `duplicate testcase "000"`},
		{"bad codename", func(tk *Task) { tk.Datasets[0].Testcases[1].Codename = "a.b" }, SeverityError, `dataset "Default"`, `invalid testcase codename "a.b"`},
		{"bad manager", func(tk *Task) {
			tk.Datasets[0].Managers = append(tk.Datasets[0].Managers, Manager{"a/b", tk.Datasets[0].Managers[0].File})
		},
			SeverityError, `dataset "Default"`, `invalid manager file name "a/b"`},
		{"no testcases", func(tk *Task) {
			tk.Datasets[0].Testcases = nil
			tk.Datasets[0].ScoreType, tk.Datasets[0].ScoreTypeParameters = "Sum", json.RawMessage(`1`)
		}, SeverityWarning, `dataset "Default"`, "no testcases"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tk := batchTask(t, dir, "sum", 4)
			tc.change(tk)
			rep := &Report{}
			ValidateTask(tk, nil, rep)
			if !hasIssue(rep, tc.sev, tc.where, tc.msg) {
				t.Errorf("no %s at %q with %q; issues:\n%s", tc.sev, tc.where, tc.msg, issues(rep))
			}
			if tc.sev == SeverityError {
				var ve *ValidationError
				if err := rep.Err(); !errors.As(err, &ve) || ve.Count == 0 || !strings.Contains(err.Error(), tc.msg) && ve.Count == 1 {
					t.Errorf("Err() = %v", err)
				}
			}
		})
	}
}

func TestValidateOutputOnlyAndGraders(t *testing.T) {
	dir := t.TempDir()
	tk := NewTask("quiz", "Quiz")
	tk.SubmissionFormat = []string{"output_000.txt"}
	d := NewDataset("Default")
	d.TaskType, d.TaskTypeParameters = "OutputOnly", json.RawMessage(`["diff"]`)
	d.ScoreType, d.ScoreTypeParameters = "Sum", json.RawMessage(`50`)
	for _, c := range []string{"000", "001"} {
		d.Testcases = append(d.Testcases, Testcase{Codename: c, Input: file(t, dir, c+".in", c), Output: file(t, dir, c+".out", c)})
	}
	tk.Datasets = []*Dataset{d}
	rep := &Report{}
	ValidateTask(tk, nil, rep)
	if rep.HasErrors() || !hasIssue(rep, SeverityWarning, `dataset "Default"`, "testcase 001: output_001.txt is not in the submission format") ||
		hasIssue(rep, SeverityWarning, "", "no time limit") {
		t.Errorf("issues:\n%s", issues(rep))
	}

	// A grader for some languages only: fine for the task, a warning for
	// the languages of the contest without one.
	g := batchTask(t, dir, "g", 2)
	g.Datasets[0].TaskTypeParameters = json.RawMessage(`["grader", ["", ""], "comparator"]`)
	g.Datasets[0].Managers = append(g.Datasets[0].Managers, Manager{"grader.cpp", file(t, dir, "grader.cpp", "int main(){}")})
	rep = &Report{}
	ValidateTask(g, nil, rep)
	if len(rep.Issues()) != 0 {
		t.Errorf("issues:\n%s", issues(rep))
	}
	c := demoContest("c", "g")
	ValidateTaskInContest(c, g, nil, rep)
	if rep.HasErrors() || !hasIssue(rep, SeverityWarning, `task g: dataset "Default"`, "no grader.%l for Python 3 / CPython") {
		t.Errorf("issues:\n%s", issues(rep))
	}
	// With the allowed languages of the task, only those count.
	g.AllowedLanguages = []string{"C++17 / g++"}
	rep = &Report{}
	ValidateTaskInContest(c, g, nil, rep)
	if len(rep.Issues()) != 0 {
		t.Errorf("issues:\n%s", issues(rep))
	}
}

func TestValidateContest(t *testing.T) {
	rep := &Report{}
	ValidateContest(demoContest("demo", "a", "b"), nil, rep)
	if len(rep.Issues()) != 0 {
		t.Fatalf("issues:\n%s", issues(rep))
	}
	for _, tc := range []struct {
		name   string
		change func(*Contest)
		sev    Severity
		where  string
		msg    string
	}{
		{"name", func(c *Contest) { c.Name = "" }, SeverityError, "contest", "no name"},
		{"language", func(c *Contest) { c.Languages = append(c.Languages, "Go 1.0") }, SeverityError, "contest demo", `unknown language "Go 1.0"`},
		{"no group", func(c *Contest) { c.Groups = nil }, SeverityError, "contest demo", "no group"},
		{"group order", func(c *Contest) { c.Groups[0].Stop = c.Groups[0].Start.Add(-time.Second) }, SeverityError, `contest demo: group "main"`, "not in order"},
		{"analysis order", func(c *Contest) {
			c.Groups[0].AnalysisEnabled = true
			c.Groups[0].AnalysisStart = c.Groups[0].Stop.Add(-time.Minute)
		}, SeverityError, `group "main"`, "not in order"},
		{"duplicate group", func(c *Contest) { c.Groups[1].Name = "main" }, SeverityError, "contest demo", `duplicate group "main"`},
		{"main group", func(c *Contest) { c.MainGroup = "nope" }, SeverityError, "contest demo", `the main group "nope" does not exist`},
		{"duplicate task", func(c *Contest) { c.Tasks = []string{"a", "a"} }, SeverityError, "contest demo", `duplicate task "a"`},
		{"no task", func(c *Contest) { c.Tasks = nil }, SeverityWarning, "contest demo", "no task"},
		{"tokens", func(c *Contest) { c.Tokens.MaxNumber = Ptr(int32(-1)) }, SeverityError, "contest demo", "token_max_number must be positive"},
		{"grace period", func(c *Contest) { c.MinSubmissionIntervalGracePeriod = Ptr(-time.Second) }, SeverityError, "contest demo", "grace_period must be positive"},
		{"timezone", func(c *Contest) { c.Timezone = Ptr("Mars/Olympus") }, SeverityWarning, "contest demo", "unknown timezone"},
		{"participation group", func(c *Contest) { c.Participations[0].Group = "x" }, SeverityWarning, "participation of alice", `group "x" is not in the package`},
		{"participation password", func(c *Contest) { c.Participations[0].Password = Ptr("secret") }, SeverityError, "participation of alice", "is not <method>:<password>"},
		{"participation team", func(c *Contest) { c.Participations[0].Team = Ptr("a b") }, SeverityError, "participation of alice", "invalid team code"},
		{"participation ip", func(c *Contest) {
			c.Participations[0].IP = []netip.Prefix{netip.MustParsePrefix("10.0.0.1/8")}
		}, SeverityError, "participation of alice", "invalid network"},
		{"participation delay", func(c *Contest) { c.Participations[0].DelayTime = Ptr(-time.Minute) }, SeverityError, "participation of alice", "negative delay"},
		{"duplicate participation", func(c *Contest) { c.Participations[1].Username = "alice" }, SeverityError, "contest demo", `duplicate participation of "alice"`},
		{"user password", func(c *Contest) { c.Users[0].Password = "md5:abc" }, SeverityError, "user alice", "unknown password method"},
		{"bcrypt", func(c *Contest) { c.Users[0].Password = "bcrypt:abc" }, SeverityError, "user alice", "not a bcrypt hash"},
		{"username", func(c *Contest) { c.Users[0].Username = "al ice" }, SeverityError, "users", `invalid username "al ice"`},
		{"team code", func(c *Contest) { c.Teams[0].Code = "" }, SeverityError, "teams", `invalid team code ""`},
		{"team name", func(c *Contest) { c.Teams[0].Name = "" }, SeverityWarning, "team ita", "no name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := demoContest("demo", "a", "b")
			tc.change(c)
			rep := &Report{}
			ValidateContest(c, nil, rep)
			if !hasIssue(rep, tc.sev, tc.where, tc.msg) {
				t.Errorf("no %s at %q with %q; issues:\n%s", tc.sev, tc.where, tc.msg, issues(rep))
			}
		})
	}
}

func TestPasswords(t *testing.T) {
	for _, tc := range []struct {
		in, method, payload, err string
	}{
		{"plaintext:secret", "plaintext", "secret", ""},
		{"plaintext:", "plaintext", "", ""},
		{"plaintext:a:b", "plaintext", "a:b", ""},
		{"bcrypt:$2b$12$" + strings.Repeat("a", 53), "bcrypt", "$2b$12$" + strings.Repeat("a", 53), ""},
		{"bcrypt:$2b$12$" + strings.Repeat("a", 52), "", "", "not a bcrypt hash"},
		{"bcrypt:$2c$12$" + strings.Repeat("a", 53), "", "", "not a bcrypt hash"},
		{"sha1:abc", "", "", "unknown password method"},
		{"secret:abc", "", "", "unknown password method"},
		{"secret", "", "", "is not <method>:<password>"},
	} {
		m, p, err := ParseAuthentication(tc.in)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("%q: error %v, want %q", tc.in, err, tc.err)
			}
			if strings.Contains(tc.in, "secret") && err != nil && strings.Contains(err.Error(), "secret") {
				t.Errorf("%q: the error shows the password: %v", tc.in, err)
			}
			continue
		}
		if err != nil || m != tc.method || p != tc.payload {
			t.Errorf("%q: %q %q %v", tc.in, m, p, err)
		}
	}
	if pw := RandomPassword(); !regexp.MustCompile(`^plaintext:[a-z]{6}$`).MatchString(pw) {
		t.Errorf("RandomPassword() = %q", pw)
	}
	if got := BuildPassword("x", PasswordBcrypt); got != "bcrypt:x" {
		t.Errorf("BuildPassword = %q", got)
	}
}

func TestParseIPNetworks(t *testing.T) {
	got, err := ParseIPNetworks("10.0.0.0/8, 192.168.1.7,::1")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"10.0.0.0/8", "192.168.1.7/32", "::1/128"}
	for i, p := range got {
		if p.String() != want[i] {
			t.Errorf("network %d = %s, want %s", i, p, want[i])
		}
	}
	for _, bad := range []string{"10.0.0.1/8", "", "10.0.0.0/33", "host"} {
		if _, err := ParseIPNetworks(bad); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

func TestDomains(t *testing.T) {
	for _, tc := range []struct {
		s                      string
		codename, file, schema bool
	}{
		{"sum", true, true, true},
		{"a-b_C9", true, true, true},
		{"a.b", false, true, true},
		{"sum.%l", false, false, true},
		{"x.b%l", false, false, true}, // CMS's unescaped dot
		{"a%l", false, false, false},
		{".", false, false, false},
		{"..", false, false, false},
		{"a/b", false, false, false},
		{"", false, false, false},
		{"ñ", false, false, false},
	} {
		if ValidCodename(tc.s) != tc.codename || ValidFilename(tc.s) != tc.file || ValidFilenameSchema(tc.s) != tc.schema {
			t.Errorf("%q: codename %v file %v schema %v", tc.s, ValidCodename(tc.s), ValidFilename(tc.s), ValidFilenameSchema(tc.s))
		}
	}
}

func TestModelDefaults(t *testing.T) {
	c := NewContest("x")
	if c.Tokens.Mode != TokenModeInfinite || c.Tokens.GenInterval != 30*time.Minute || !c.IPRestriction ||
		len(c.Languages) != 3 || c.MainGroupName() != "" {
		t.Errorf("contest defaults %+v", c)
	}
	c.Groups = []Group{NewGroup("a"), NewGroup("b")}
	if c.MainGroupName() != "a" || c.Group("b") == nil || c.Group("z") != nil {
		t.Error("groups")
	}
	g := NewGroup("g")
	if !g.Start.Equal(DefaultGroupStart) || !g.AnalysisStop.Equal(DefaultGroupStop) {
		t.Errorf("group defaults %+v", g)
	}
	tk := NewTask("t", "T")
	if tk.Tokens.Mode != TokenModeDisabled || tk.FeedbackLevel != FeedbackRestricted || tk.ScoreMode != ScoreModeMaxTokenedLast ||
		tk.Active() != nil {
		t.Errorf("task defaults %+v", tk)
	}
	tk.Datasets = []*Dataset{NewDataset("a"), NewDataset("b")}
	if tk.Active() != tk.Datasets[0] {
		t.Error("the first dataset is the active one by default")
	}
	tk.ActiveDataset = "b"
	if tk.Active() != tk.Datasets[1] || tk.Dataset("c") != nil {
		t.Error("ActiveDataset")
	}
	if LanguageCodes["english"] != "en" || LanguageCodes["filipino"] != "fil" || len(LanguageCodes) != 61 {
		t.Errorf("LanguageCodes: %d entries", len(LanguageCodes))
	}
}

func TestReport(t *testing.T) {
	var r Report
	r.Warnf("task a", "w %d", 1)
	r.Errorf("", "e")
	other := &Report{}
	other.Errorf("task b", "e2")
	r.Merge(other)
	r.Merge(&r)
	var b strings.Builder
	if _, err := r.WriteTo(&b); err != nil {
		t.Fatal(err)
	}
	if want := "error: e\nerror: task b: e2\nwarning: task a: w 1\n"; b.String() != want {
		t.Errorf("WriteTo:\n%s\nwant\n%s", b.String(), want)
	}
	err := r.Err()
	if err == nil || err.Error() != "invalid package: 2 errors, the first: error: e" {
		t.Errorf("Err() = %v", err)
	}
	if len(r.Warnings()) != 1 || len(r.Errors()) != 2 {
		t.Error("filters")
	}
}
