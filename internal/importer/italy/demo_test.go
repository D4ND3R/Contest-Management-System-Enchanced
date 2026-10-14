// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package italy

import (
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
)

// The rows the original CMS (cmsImportContest -L italy_yaml, schema 49)
// stored for testdata/contests/demo, read from its database. Digests are
// SHA-1 there: the test hashes the files the loader picked with SHA-1 to
// check they are the same ones.

type cmsTestcase struct {
	codename      string
	public        bool
	input, output string
}

type cmsTask struct {
	name, title         string
	submissionFormat    []string
	tokenMode           string
	timeLimit           *float64
	memoryLimit         *int64
	taskType, ttParams  string
	scoreType, stParams string
	testcases           []cmsTestcase
	managers            map[string]string
	statement           string
	attachments         map[string]string
}

var cmsDemoTasks = []cmsTask{
	{
		name: "sum", title: "A plus B", submissionFormat: []string{"sum.%l"}, tokenMode: "infinite",
		timeLimit: importer.Ptr(1.0), memoryLimit: importer.Ptr(int64(268435456)),
		taskType: "Batch", ttParams: `["alone", ["", ""], "diff"]`,
		scoreType: "GroupMin", stParams: `[[40, 2], [60, 2]]`,
		testcases: []cmsTestcase{
			{"000", true, "32d3c3db7c8157c3bb576fa546eb6e41322fb89e", "a3db5c13ff90a36963278c6a39e4ee3c22e2a436"},
			{"001", false, "3a475d9719aa56accc500a31662fbdb275980cb5", "97ea7ec8a6bb8ab9049d86bc39b5be2b0800b14b"},
			{"002", false, "969d74a8873fb7eb7b604a9501ff7cfe9bcac4ee", "a4685dd1a208792fe9d51ac4a08732495cd09dbb"},
			{"003", false, "991ff2709e0ea3b6d1760f577f7bc7e064f091e4", "c4e815b66718b3dcb762f05acfec36fac0d7908b"},
		},
		managers:    map[string]string{},
		statement:   "daf6ff17831183bc926bfa5932420be3c3f3b59d",
		attachments: map[string]string{},
	},
	{
		name: "half", title: "Half credit", submissionFormat: []string{"half.%l"}, tokenMode: "infinite",
		timeLimit: importer.Ptr(1.0), memoryLimit: importer.Ptr(int64(268435456)),
		taskType: "Batch", ttParams: `["alone", ["", ""], "comparator"]`,
		scoreType: "Sum", stParams: `33.333333333333336`,
		testcases: []cmsTestcase{
			{"000", false, "a3db5c13ff90a36963278c6a39e4ee3c22e2a436", "e5fa44f2b31c1fb553b6021e7360d07d5d91ff5e"},
			{"001", false, "4143d3a341877154d6e95211464e1df1015b74bd", "5d9474c0309b7ca09a182d888f73b37a8fe1362c"},
			{"002", false, "05df01e327042694f79bbbaafea1d617bee8f968", "d0758565fd06c37aa66b071160d156f5628cd518"},
		},
		managers:    map[string]string{"checker": "f156ff125418de45ff525bf9067d9199d0ef9830"},
		statement:   "ab7bae902ada0f1967b9b5543012d9fd768f6b94",
		attachments: map[string]string{},
	},
	{
		name: "quiz", title: "Quiz", submissionFormat: []string{"output_000.txt", "output_001.txt"}, tokenMode: "disabled",
		taskType: "OutputOnly", ttParams: `["diff"]`,
		scoreType: "Sum", stParams: `50.0`,
		testcases: []cmsTestcase{
			{"000", false, "f147fe71b93a767bbb8cb68f6da67b1dd92e5104", "34973274ccef6ab4dfaaf86599792fa9c3fe4689"},
			{"001", false, "e5cd484af2581a8170e90996d8565421bc505d2e", "a6310e1fe8acfe10532cc4311e3cc81af00f5eb5"},
		},
		managers:  map[string]string{},
		statement: "fb16ae4726db7264b793547b52a64654514e1981",
		attachments: map[string]string{
			"input_000.txt": "f147fe71b93a767bbb8cb68f6da67b1dd92e5104",
			"input_001.txt": "e5cd484af2581a8170e90996d8565421bc505d2e",
		},
	},
}

// TestDemoContestMatchesCMS loads the demo contest and compares it with
// what the original cmsImportContest stored.
func TestDemoContestMatchesCMS(t *testing.T) {
	dir, _ := copyDemo(t)
	opts, rep := load()
	c, err := Loader{}.LoadContest(dir, opts)
	if err != nil || c == nil {
		t.Fatalf("LoadContest: %v\n%s", err, issues(rep))
	}
	if n := len(rep.Issues()); n != 0 {
		t.Errorf("unexpected issues:\n%s", issues(rep))
	}
	want := importer.NewContest("demo")
	want.Description = "Demo Contest"
	want.Languages = []string{"C++17 / g++", "C11 / gcc", "Python 3 / CPython", "Java / JDK"}
	want.Tokens = importer.Tokens{Mode: "finite", MaxNumber: importer.Ptr(int32(10)), GenInitial: 2, GenNumber: 1,
		GenInterval: 30 * time.Second, GenMax: importer.Ptr(int32(5))}
	g := importer.NewGroup("default")
	g.Start = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	g.Stop = time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	want.Groups = []importer.Group{g}
	want.MainGroup = "default"
	want.Tasks = []string{"sum", "half", "quiz"}
	pw := "plaintext:demo"
	for _, u := range [][3]string{{"alice", "Alice", "Liddell"}, {"bob", "Bob", "Builder"}, {"carol", "Carol", "Danvers"}} {
		want.Users = append(want.Users, importer.User{Username: u[0], FirstName: u[1], LastName: u[2], Password: pw})
		want.Participations = append(want.Participations, importer.Participation{Username: u[0], Password: &pw})
	}
	if !reflect.DeepEqual(c, want) {
		t.Errorf("contest:\n got %+v\nwant %+v", c, want)
	}
}

// TestDemoTasksMatchCMS loads the tasks of the demo contest and compares
// them with what the original cmsImportContest stored, files included.
func TestDemoTasksMatchCMS(t *testing.T) {
	dir, realChecker := copyDemo(t)
	for _, w := range cmsDemoTasks {
		t.Run(w.name, func(t *testing.T) {
			tk, rep := loadTask(t, filepath.Join(dir, w.name))
			if tk == nil {
				t.Fatalf("no task:\n%s", issues(rep))
			}
			if n := len(rep.Issues()); n != 0 {
				t.Errorf("unexpected issues:\n%s", issues(rep))
			}
			importer.ValidateTask(tk, nil, rep)
			if rep.HasErrors() {
				t.Errorf("invalid task:\n%s", issues(rep))
			}
			if tk.Name != w.name || tk.Title != w.title || !slices.Equal(tk.SubmissionFormat, w.submissionFormat) {
				t.Errorf("task %q %q %v", tk.Name, tk.Title, tk.SubmissionFormat)
			}
			wantTokens := importer.Tokens{Mode: w.tokenMode, GenInitial: 2, GenNumber: 2, GenInterval: 30 * time.Minute}
			if !reflect.DeepEqual(tk.Tokens, wantTokens) {
				t.Errorf("tokens %+v, want %+v", tk.Tokens, wantTokens)
			}
			if tk.FeedbackLevel != "restricted" || tk.ScoreMode != "max_tokened_last" || tk.ScorePrecision != 0 ||
				tk.AllowedLanguages != nil || tk.MaxSubmissionNumber != nil || tk.MinSubmissionInterval != nil {
				t.Errorf("settings %+v", tk)
			}
			if !slices.Equal(tk.PrimaryStatements, []string{"it"}) || len(tk.Statements) != 1 ||
				tk.Statements[0].Language != "it" || sha1Of(t, tk.Statements[0].File) != w.statement {
				t.Errorf("statements %v %+v", tk.PrimaryStatements, tk.Statements)
			}
			att := map[string]string{}
			for _, a := range tk.Attachments {
				att[a.Filename] = sha1Of(t, a.File)
			}
			if !reflect.DeepEqual(att, w.attachments) {
				t.Errorf("attachments %v, want %v", att, w.attachments)
			}
			if len(tk.Datasets) != 1 || tk.Active() != tk.Datasets[0] {
				t.Fatalf("datasets %v", tk.Datasets)
			}
			d := tk.Datasets[0]
			if d.Description != "Default" || d.Autojudge {
				t.Errorf("dataset %q autojudge %v", d.Description, d.Autojudge)
			}
			if !reflect.DeepEqual(d.TimeLimit, w.timeLimit) || !reflect.DeepEqual(d.MemoryLimit, w.memoryLimit) {
				t.Errorf("limits %v %v", d.TimeLimit, d.MemoryLimit)
			}
			if d.TaskType != w.taskType || !sameJSON(t, d.TaskTypeParameters, w.ttParams) {
				t.Errorf("task type %s %s, want %s %s", d.TaskType, d.TaskTypeParameters, w.taskType, w.ttParams)
			}
			// The same text as Python's json.dumps, not only the value.
			if d.ScoreType != w.scoreType || string(d.ScoreTypeParameters) != w.stParams {
				t.Errorf("score type %s %s, want %s %s", d.ScoreType, d.ScoreTypeParameters, w.scoreType, w.stParams)
			}
			var tcs []cmsTestcase
			for _, tc := range d.Testcases {
				tcs = append(tcs, cmsTestcase{tc.Codename, tc.Public, sha1Of(t, tc.Input), sha1Of(t, tc.Output)})
			}
			if !reflect.DeepEqual(tcs, w.testcases) {
				t.Errorf("testcases\n got %v\nwant %v", tcs, w.testcases)
			}
			mgr := map[string]string{}
			for _, m := range d.Managers {
				mgr[m.Filename] = sha1Of(t, m.File)
			}
			if !realChecker {
				for k := range w.managers {
					if _, ok := mgr[k]; ok {
						mgr[k] = w.managers[k] // a placeholder: only the name is compared
					}
				}
			}
			if !reflect.DeepEqual(mgr, w.managers) {
				t.Errorf("managers %v, want %v", mgr, w.managers)
			}
		})
	}
}
