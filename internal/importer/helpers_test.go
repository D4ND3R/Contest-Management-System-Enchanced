// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package importer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// sha returns the digest of s.
func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// writeFile writes content at dir/name and returns its path.
func writeFile(t testing.TB, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// file writes content in dir and returns it as a File.
func file(t testing.TB, dir, name, content string) *File {
	return FileFromPath(writeFile(t, dir, name, content))
}

// batchTask returns a valid Batch task with n testcases (GroupMin with two
// subtasks when n is even, else Sum), a statement in English and Italian,
// an attachment and a checker, with its files in dir.
func batchTask(t testing.TB, dir, name string, n int) *Task {
	t.Helper()
	dir = filepath.Join(dir, name)
	tk := NewTask(name, "Task "+name)
	tk.SubmissionFormat = []string{name + ".%l"}
	tk.PrimaryStatements = []string{"en"}
	tk.Statements = []Statement{
		{Language: "en", File: file(t, dir, "statement/english.pdf", "statement of "+name)},
		{Language: "it", File: file(t, dir, "statement/italian.pdf", "testo di "+name)},
	}
	tk.Attachments = []Attachment{{Filename: "sample.txt", File: file(t, dir, "att/sample.txt", "1 2\n")}}
	tk.Tokens.Mode = TokenModeInfinite
	d := NewDataset("Default")
	d.TimeLimit = Ptr(1.5)
	d.MemoryLimit = Ptr(int64(256 * MiB))
	d.TaskType = "Batch"
	d.TaskTypeParameters = json.RawMessage(`["alone", ["", ""], "comparator"]`)
	d.Managers = []Manager{{Filename: "checker", File: file(t, dir, "check/checker", "#!/bin/sh\necho 1\n")}}
	if n%2 == 0 && n > 0 {
		d.ScoreType = "GroupMin"
		d.ScoreTypeParameters = json.RawMessage(fmt.Sprintf(`[[40, %d], [60, %d]]`, n/2, n/2))
	} else {
		d.ScoreType = "Sum"
		d.ScoreTypeParameters = json.RawMessage(`10`)
	}
	for i := range n {
		d.Testcases = append(d.Testcases, Testcase{
			Codename: fmt.Sprintf("%03d", i),
			Public:   i == 0,
			Input:    file(t, dir, fmt.Sprintf("input/input%d.txt", i), fmt.Sprintf("%d %d\n", i, i)),
			Output:   file(t, dir, fmt.Sprintf("output/output%d.txt", i), fmt.Sprintf("%d\n", 2*i)),
		})
	}
	tk.Datasets = []*Dataset{d}
	return tk
}

// demoContest returns a contest with two groups, the given tasks and
// users alice, bob and carol (carol in the team "ita").
func demoContest(name string, tasks ...string) *Contest {
	c := NewContest(name)
	c.Description = "Contest " + name
	c.Languages = []string{"C++17 / g++", "Python 3 / CPython"}
	start := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	main := NewGroup("main")
	main.Start, main.Stop = start, start.Add(5*time.Hour)
	late := NewGroup("late")
	late.Start, late.Stop = start.Add(time.Hour), start.Add(6*time.Hour)
	late.PerUserTime = Ptr(3 * time.Hour)
	c.Groups = []Group{main, late}
	c.MainGroup = "main"
	c.Tasks = tasks
	for _, u := range []string{"alice", "bob", "carol"} {
		c.Users = append(c.Users, User{Username: u, FirstName: strings.ToUpper(u[:1]) + u[1:], LastName: "Test",
			Password: BuildPassword("pw-"+u, PasswordPlaintext)})
		c.Participations = append(c.Participations, Participation{Username: u,
			Password: Ptr(BuildPassword("pw-"+u, PasswordPlaintext))})
	}
	c.Participations[1].Group = "late"
	c.Participations[2].Team = Ptr("ita")
	c.Participations[2].Hidden = Ptr(true)
	c.Teams = []Team{{Code: "ita", Name: "Italy"}}
	return c
}

// fakeLoader serves models built in the test, by directory, and counts
// the loads.
type fakeLoader struct {
	name     string
	contests map[string]*Contest
	tasks    map[string]*Task
	users    map[string][]User
	teams    map[string][]Team
	loads    map[string]int
}

func newFakeLoader(name string) *fakeLoader {
	return &fakeLoader{name: name, contests: map[string]*Contest{}, tasks: map[string]*Task{},
		users: map[string][]User{}, teams: map[string][]Team{}, loads: map[string]int{}}
}

func (f *fakeLoader) Name() string        { return f.name }
func (f *fakeLoader) Description() string { return "fake format " + f.name }

func (f *fakeLoader) Detect(dir string, kind Kind) bool {
	switch kind {
	case KindContest:
		return f.contests[dir] != nil
	case KindTask:
		return f.tasks[dir] != nil
	case KindUsers:
		return f.users[dir] != nil
	case KindTeams:
		return f.teams[dir] != nil
	}
	return false
}

func (f *fakeLoader) LoadContest(dir string, opts *Options) (*Contest, error) {
	f.loads[dir]++
	c := f.contests[dir]
	if c == nil {
		return nil, fmt.Errorf("no contest at %s", dir)
	}
	return c, nil
}

func (f *fakeLoader) TaskLoader(dir, name string) (TaskLoader, string) {
	return f, dir + "/" + name
}

func (f *fakeLoader) LoadTask(dir string, opts *Options) (*Task, error) {
	f.loads[dir]++
	t := f.tasks[dir]
	if t == nil {
		return nil, fmt.Errorf("no task at %s", dir)
	}
	if opts.Languages == nil || opts.Report == nil {
		return nil, fmt.Errorf("options without languages or report")
	}
	if opts.SkipStatements {
		c := *t
		c.Statements, c.PrimaryStatements = nil, nil
		return &c, nil
	}
	return t, nil
}

func (f *fakeLoader) LoadUsers(dir string, opts *Options) ([]User, error) {
	f.loads[dir]++
	return f.users[dir], nil
}

func (f *fakeLoader) LoadTeams(dir string, opts *Options) ([]Team, error) {
	f.loads[dir]++
	return f.teams[dir], nil
}

// taskOnly is a loader that only reads tasks.
type taskOnly struct{ f *fakeLoader }

func (l taskOnly) Name() string                      { return l.f.Name() }
func (l taskOnly) Description() string               { return l.f.Description() }
func (l taskOnly) Detect(dir string, kind Kind) bool { return l.f.Detect(dir, kind) }
func (l taskOnly) LoadTask(dir string, opts *Options) (*Task, error) {
	return l.f.LoadTask(dir, opts)
}

// issues returns the messages of a report, one per line, for error
// messages of the tests.
func issues(r *Report) string {
	var b strings.Builder
	_, _ = r.WriteTo(&b)
	return b.String()
}

// hasIssue reports whether the report has an issue of that severity whose
// location and message contain where and msg.
func hasIssue(r *Report, s Severity, where, msg string) bool {
	for _, i := range r.Issues() {
		if i.Severity == s && strings.Contains(i.Where, where) && strings.Contains(i.Message, msg) {
			return true
		}
	}
	return false
}
