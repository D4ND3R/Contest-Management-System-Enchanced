// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package tps

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
)

// The files in testdata/upstream are the rows that the TPS loader of the
// original CMS (schema 49) stored for the tasks of testdata, dumped with
// dump_task.sql (see testdata/README.md). The tests compare them with what this loader reads
// (upstreamOfModel) and with what the importer stores (db_test.go). File
// digests are SHA-1 there: ours are translated by content.

// upTask is the dump of a task.
type upTask struct {
	Name                  string            `json:"name"`
	Title                 string            `json:"title"`
	SubmissionFormat      []string          `json:"submission_format"`
	PrimaryStatements     []string          `json:"primary_statements"`
	AllowedLanguages      []string          `json:"allowed_languages"`
	TokenMode             string            `json:"token_mode"`
	TokenMaxNumber        *int32            `json:"token_max_number"`
	TokenMinInterval      float64           `json:"token_min_interval"`
	TokenGenInitial       int32             `json:"token_gen_initial"`
	TokenGenNumber        int32             `json:"token_gen_number"`
	TokenGenInterval      float64           `json:"token_gen_interval"`
	TokenGenMax           *int32            `json:"token_gen_max"`
	MaxSubmissionNumber   *int32            `json:"max_submission_number"`
	MaxUserTestNumber     *int32            `json:"max_user_test_number"`
	MinSubmissionInterval *float64          `json:"min_submission_interval"`
	MinUserTestInterval   *float64          `json:"min_user_test_interval"`
	FeedbackLevel         string            `json:"feedback_level"`
	ScorePrecision        int32             `json:"score_precision"`
	ScoreMode             string            `json:"score_mode"`
	ActiveDataset         string            `json:"active_dataset"`
	Statements            map[string]string `json:"statements"`
	Attachments           map[string]string `json:"attachments"`
	Datasets              []upDataset       `json:"datasets"`
}

type upDataset struct {
	Description         string            `json:"description"`
	Autojudge           bool              `json:"autojudge"`
	TimeLimit           *float64          `json:"time_limit"`
	MemoryLimit         *int64            `json:"memory_limit"`
	TaskType            string            `json:"task_type"`
	TaskTypeParameters  string            `json:"task_type_parameters"`
	ScoreType           string            `json:"score_type"`
	ScoreTypeParameters string            `json:"score_type_parameters"`
	Managers            map[string]string `json:"managers"`
	Testcases           []upTestcase      `json:"testcases"`
}

// upTestcase is [codename, public, input, output].
type upTestcase struct {
	Codename      string
	Public        bool
	Input, Output string
}

func (tc upTestcase) MarshalJSON() ([]byte, error) {
	return json.Marshal([]any{tc.Codename, tc.Public, tc.Input, tc.Output})
}

func (tc *upTestcase) UnmarshalJSON(b []byte) error {
	var a []any
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	if len(a) != 4 {
		return fmt.Errorf("testcase %s: want 4 items", b)
	}
	var ok [4]bool
	tc.Codename, ok[0] = a[0].(string)
	tc.Public, ok[1] = a[1].(bool)
	tc.Input, ok[2] = a[2].(string)
	tc.Output, ok[3] = a[3].(string)
	if ok != [4]bool{true, true, true, true} {
		return fmt.Errorf("testcase %s: bad types", b)
	}
	return nil
}

func readUpstream(t *testing.T, path string) *upTask {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var u upTask
	if err := json.Unmarshal(b, &u); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	u.normalize(t)
	return &u
}

// normalize makes dumps comparable: parameters as canonical JSON (the
// numbers keep their text: 50 and 50.0 differ, as in jsonb), datasets by
// description, empty collections empty.
func (u *upTask) normalize(t *testing.T) {
	t.Helper()
	for i := range u.Datasets {
		d := &u.Datasets[i]
		d.TaskTypeParameters = canonicalJSON(t, d.TaskTypeParameters)
		d.ScoreTypeParameters = canonicalJSON(t, d.ScoreTypeParameters)
		if d.Managers == nil {
			d.Managers = map[string]string{}
		}
		slices.SortFunc(d.Testcases, func(a, b upTestcase) int { return strings.Compare(a.Codename, b.Codename) })
	}
	slices.SortFunc(u.Datasets, func(a, b upDataset) int { return strings.Compare(a.Description, b.Description) })
	if u.Statements == nil {
		u.Statements = map[string]string{}
	}
	if u.Attachments == nil {
		u.Attachments = map[string]string{}
	}
}

func canonicalJSON(t *testing.T, s string) string {
	t.Helper()
	d := json.NewDecoder(strings.NewReader(s))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		t.Fatalf("parameters %q: %v", s, err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// sha1Of returns the SHA-1 of the content of a file of the model.
func sha1Of(t *testing.T, f *importer.File) string {
	t.Helper()
	rc, err := f.Open()
	if err != nil {
		t.Fatalf("%s: %v", f.Name(), err)
	}
	defer rc.Close()
	h := sha1.New()
	if _, err := io.Copy(h, rc); err != nil {
		t.Fatalf("%s: %v", f.Name(), err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// upstreamOfModel describes a loaded task as a dump would.
func upstreamOfModel(t *testing.T, task *importer.Task) *upTask {
	t.Helper()
	u := &upTask{
		Name: task.Name, Title: task.Title,
		SubmissionFormat: task.SubmissionFormat, PrimaryStatements: task.PrimaryStatements,
		AllowedLanguages: task.AllowedLanguages,
		TokenMode:        task.Tokens.Mode, TokenMaxNumber: task.Tokens.MaxNumber,
		TokenMinInterval: task.Tokens.MinInterval.Seconds(), TokenGenInitial: task.Tokens.GenInitial,
		TokenGenNumber: task.Tokens.GenNumber, TokenGenInterval: task.Tokens.GenInterval.Seconds(),
		TokenGenMax: task.Tokens.GenMax, MaxSubmissionNumber: task.MaxSubmissionNumber,
		MaxUserTestNumber: task.MaxUserTestNumber, FeedbackLevel: task.FeedbackLevel,
		ScorePrecision: task.ScorePrecision, ScoreMode: task.ScoreMode,
		Statements: map[string]string{}, Attachments: map[string]string{},
	}
	if task.MinSubmissionInterval != nil {
		u.MinSubmissionInterval = importer.Ptr(task.MinSubmissionInterval.Seconds())
	}
	if task.MinUserTestInterval != nil {
		u.MinUserTestInterval = importer.Ptr(task.MinUserTestInterval.Seconds())
	}
	if a := task.Active(); a != nil {
		u.ActiveDataset = a.Description
	}
	for _, s := range task.Statements {
		u.Statements[s.Language] = sha1Of(t, s.File)
	}
	for _, a := range task.Attachments {
		u.Attachments[a.Filename] = sha1Of(t, a.File)
	}
	for _, d := range task.Datasets {
		ud := upDataset{
			Description: d.Description, Autojudge: d.Autojudge, TimeLimit: d.TimeLimit, MemoryLimit: d.MemoryLimit,
			TaskType: d.TaskType, TaskTypeParameters: string(d.TaskTypeParameters),
			ScoreType: d.ScoreType, ScoreTypeParameters: string(d.ScoreTypeParameters),
			Managers: map[string]string{},
		}
		for _, m := range d.Managers {
			ud.Managers[m.Filename] = sha1Of(t, m.File)
		}
		for _, tc := range d.Testcases {
			ud.Testcases = append(ud.Testcases, upTestcase{tc.Codename, tc.Public, sha1Of(t, tc.Input), sha1Of(t, tc.Output)})
		}
		u.Datasets = append(u.Datasets, ud)
	}
	u.normalize(t)
	return u
}

// compareUpstream compares a dump with the golden one. The managers
// compiled at import time (compiled) can differ with the compiler: only
// their presence is compared, and a different binary is logged.
func compareUpstream(t *testing.T, got, want *upTask, compiled ...string) {
	t.Helper()
	got, want = clone(t, got), clone(t, want)
	for _, u := range []*upTask{got, want} {
		for i := range u.Datasets {
			for _, name := range compiled {
				if _, ok := u.Datasets[i].Managers[name]; ok {
					u.Datasets[i].Managers[name] = "(compiled)"
				}
			}
		}
	}
	if !reflect.DeepEqual(got, want) {
		g, _ := json.MarshalIndent(got, "", "  ")
		w, _ := json.MarshalIndent(want, "", "  ")
		t.Errorf("task %s differs from upstream:\n%s", want.Name, lineDiff(string(w), string(g)))
	}
}

func clone(t *testing.T, u *upTask) *upTask {
	b, err := json.Marshal(u)
	if err != nil {
		t.Fatal(err)
	}
	var c upTask
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	return &c
}

// lineDiff shows the lines of want and got that differ.
func lineDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	var b bytes.Buffer
	for i := 0; i < max(len(w), len(g)); i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			fmt.Fprintf(&b, "line %d:\n  upstream: %s\n  ours:     %s\n", i+1, wl, gl)
		}
	}
	return b.String()
}

// fixture is the path of a task of testdata.
func fixture(name string) string { return filepath.Join("testdata", name) }
