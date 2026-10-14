// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package pkg

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
)

// writeFiles writes files (path relative to dir, "/" separated: content)
// and returns dir. A path ending in "*" is written executable (without
// the star).
func writeFiles(t testing.TB, dir string, files map[string]string) string {
	t.Helper()
	for name, content := range files {
		mode := os.FileMode(0o644)
		if n, ok := strings.CutSuffix(name, "*"); ok {
			name, mode = n, 0o755
		}
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// pkgDir writes a package in a new temporary directory.
func pkgDir(t testing.TB, files map[string]string) string {
	t.Helper()
	return writeFiles(t, filepath.Join(t.TempDir(), "task"), files)
}

// testEntry is a member of a test zip.
type testEntry struct {
	name    string
	content string
	mode    os.FileMode // 0: a regular file 0644
	method  uint16
}

// writeZip writes a zip with the entries in that order.
func writeZip(t testing.TB, entries []testEntry) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "task.zip")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: e.method, Modified: time.Now()}
		if h.Method == 0 && !strings.HasSuffix(e.name, "/") {
			h.Method = zip.Deflate
		}
		if e.mode != 0 {
			h.SetMode(e.mode)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, e.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

// zipOf returns the entries of a package given as files (sorted).
func zipOf(prefix string, files map[string]string) []testEntry {
	var out []testEntry
	for name, content := range files {
		mode := os.FileMode(0)
		if n, ok := strings.CutSuffix(name, "*"); ok {
			name, mode = n, 0o755
		}
		out = append(out, testEntry{name: prefix + name, content: content, mode: mode})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// load reads a package and returns it with its report.
func load(t testing.TB, p string) (*Package, *importer.Report) {
	t.Helper()
	rep := &importer.Report{}
	pk, err := Load(p, &importer.Options{Report: rep})
	if err != nil {
		t.Fatalf("Load: %v\n%s", err, issues(rep))
	}
	return pk, rep
}

// loadValid reads a package that must have no errors (also after the
// validation of the importer) and returns its task.
func loadValid(t testing.TB, p string) (*importer.Task, *importer.Report) {
	t.Helper()
	pk, rep := load(t, p)
	if pk.Task == nil {
		t.Fatalf("no task:\n%s", issues(rep))
	}
	importer.ValidateTask(pk.Task, nil, rep)
	if rep.HasErrors() {
		t.Fatalf("errors:\n%s", issues(rep))
	}
	return pk.Task, rep
}

func issues(rep *importer.Report) string {
	var b strings.Builder
	for _, i := range rep.Issues() {
		b.WriteString("  " + i.String() + "\n")
	}
	return b.String()
}

// hasIssue reports whether rep has an issue of severity s containing
// text.
func hasIssue(rep *importer.Report, s importer.Severity, text string) bool {
	for _, i := range rep.Issues() {
		if i.Severity == s && strings.Contains(i.Message, text) {
			return true
		}
	}
	return false
}

// wantIssue fails unless rep has the issue.
func wantIssue(t testing.TB, rep *importer.Report, s importer.Severity, text string) {
	t.Helper()
	if !hasIssue(rep, s, text) {
		t.Errorf("no %s containing %q:\n%s", s, text, issues(rep))
	}
}

// noIssues fails if rep has any issue.
func noIssues(t testing.TB, rep *importer.Report) {
	t.Helper()
	if n := len(rep.Issues()); n > 0 {
		t.Errorf("unexpected issues:\n%s", issues(rep))
	}
}

// content reads a file of the model.
func content(t testing.TB, f *importer.File) string {
	t.Helper()
	if f == nil {
		return "<nil>"
	}
	rc, err := f.Open()
	if err != nil {
		t.Fatalf("%s: %v", f.Name(), err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("%s: %v", f.Name(), err)
	}
	return string(data)
}

// jsonValue decodes JSON for comparisons by value (50 and 50.0 are the
// same number, as for the writer).
func jsonValue(t testing.TB, raw json.RawMessage) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return v
}

func sameJSON(t testing.TB, got json.RawMessage, want string) bool {
	t.Helper()
	return reflect.DeepEqual(jsonValue(t, got), jsonValue(t, json.RawMessage(want)))
}

// managerNames lists the managers of a dataset.
func managerNames(d *importer.Dataset) []string {
	out := make([]string, len(d.Managers))
	for i, m := range d.Managers {
		out[i] = m.Filename
	}
	return out
}

// testcaseNames lists the codenames of a dataset, with a * on the public
// ones.
func testcaseNames(d *importer.Dataset) []string {
	out := make([]string, len(d.Testcases))
	for i, tc := range d.Testcases {
		out[i] = tc.Codename
		if tc.Public {
			out[i] += "*"
		}
	}
	return out
}

func attachmentNames(t *importer.Task) []string {
	out := make([]string, len(t.Attachments))
	for i, a := range t.Attachments {
		out[i] = a.Filename
	}
	return out
}

func statementNames(t *importer.Task) []string {
	out := make([]string, len(t.Statements))
	for i, s := range t.Statements {
		out[i] = s.Language
	}
	return out
}

// needCompiler skips a test when the C++ compiler is missing.
func needCompiler(t testing.TB) {
	t.Helper()
	if _, err := exec.LookPath(Compiler); err != nil {
		t.Skipf("%s is not installed", Compiler)
	}
}

// tests returns n tests "01".."n" with inputs "in i" and outputs "out i".
func tests(n int) map[string]string {
	files := map[string]string{}
	for i := 1; i <= n; i++ {
		files[fmt.Sprintf("tests/%02d.in", i)] = fmt.Sprintf("in %d\n", i)
		files[fmt.Sprintf("tests/%02d.out", i)] = fmt.Sprintf("out %d\n", i)
	}
	return files
}

// with returns the union of file maps (later ones win).
func with(maps ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, m := range maps {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

// canonTask is a task with the contents of its files instead of the
// files, and its parameters as values, to compare tasks read from
// different places (a directory, a zip, the database).
type canonTask struct {
	Task        importer.Task
	Statements  map[string]string
	Attachments map[string]string
	Datasets    []canonDataset
}

type canonDataset struct {
	Description         string
	Autojudge           bool
	TimeLimit           *float64
	MemoryLimit         *int64
	TaskType            string
	TaskTypeParameters  any
	ScoreType           string
	ScoreTypeParameters any
	Managers            map[string]string
	// Testcases are "<codename>[*] <input> | <output>" (* if public).
	Testcases []string
}

// canonical returns tk as a canonTask; read gives the content of a file.
func canonical(t testing.TB, tk *importer.Task, read func(*importer.File) string) canonTask {
	t.Helper()
	if read == nil {
		read = func(f *importer.File) string { return content(t, f) }
	}
	c := canonTask{Task: *tk, Statements: map[string]string{}, Attachments: map[string]string{}}
	c.Task.Statements, c.Task.Attachments, c.Task.Datasets = nil, nil, nil
	for _, s := range tk.Statements {
		c.Statements[s.Language] = read(s.File)
	}
	for _, a := range tk.Attachments {
		c.Attachments[a.Filename] = read(a.File)
	}
	for _, d := range tk.Datasets {
		cd := canonDataset{
			Description: d.Description, Autojudge: d.Autojudge, TimeLimit: d.TimeLimit, MemoryLimit: d.MemoryLimit,
			TaskType: d.TaskType, TaskTypeParameters: jsonValue(t, d.TaskTypeParameters),
			ScoreType: d.ScoreType, ScoreTypeParameters: jsonValue(t, d.ScoreTypeParameters),
			Managers: map[string]string{},
		}
		for _, m := range d.Managers {
			cd.Managers[m.Filename] = read(m.File)
		}
		for _, tc := range d.Testcases {
			name := tc.Codename
			if tc.Public {
				name += "*"
			}
			cd.Testcases = append(cd.Testcases, name+" "+read(tc.Input)+" | "+read(tc.Output))
		}
		c.Datasets = append(c.Datasets, cd)
	}
	return c
}

// sameTask fails if two tasks differ.
func sameTask(t testing.TB, what string, got, want canonTask) {
	t.Helper()
	if reflect.DeepEqual(got, want) {
		return
	}
	g, w := fmt.Sprintf("%+v", got), fmt.Sprintf("%+v", want)
	if gt, wt := fmt.Sprintf("%+v", got.Task), fmt.Sprintf("%+v", want.Task); gt != wt {
		g, w = gt, wt
	} else {
		for i := range min(len(got.Datasets), len(want.Datasets)) {
			if !reflect.DeepEqual(got.Datasets[i], want.Datasets[i]) {
				g, w = fmt.Sprintf("%+v", got.Datasets[i]), fmt.Sprintf("%+v", want.Datasets[i])
			}
		}
	}
	t.Errorf("%s: the tasks differ\n got %s\nwant %s", what, g, w)
}
