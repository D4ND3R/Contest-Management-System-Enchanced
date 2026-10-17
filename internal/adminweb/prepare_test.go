// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package adminweb

import (
	"archive/zip"
	"bytes"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// zipOf builds a zip with the files (path -> content), in order.
func zipOf(t *testing.T, names []string, files map[string][]byte) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for _, n := range names {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(files[n])
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// italyTaskZip is the italy_yaml task sum of the demo renamed to name,
// in a directory of the zip.
func italyTaskZip(t *testing.T, name string, extra ...string) []byte {
	t.Helper()
	src := filepath.Join(italyDemo, "sum")
	files := map[string][]byte{}
	var names []string
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if rel == "task.yaml" {
			data = []byte(strings.Replace(string(data), "name: sum", "name: "+name, 1))
		}
		n := name + "/" + filepath.ToSlash(rel)
		names = append(names, n)
		files[n] = data
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range extra {
		names = append(names, n)
		files[n] = []byte("x")
	}
	return zipOf(t, names, files)
}

// previewError returns the error box of an import preview ("" if none).
func previewError(body string) string {
	return regexp1(body, `(?s)<div class="notification_text"><pre>(.*?)</pre>`)
}

// TestImportPackage imports a zip of the italy_yaml format as a new task
// (preview, then confirm) and refuses archives that escape their
// directory.
func TestImportPackage(t *testing.T) {
	ts, f := newFixtureSite(t, testOptions{})
	c := ts.Client()
	c.Login("prep")

	n := ts.AuditCount()
	r := c.PostFiles("/tasks/import", nil, upload{"package", "sum2.zip", italyTaskZip(t, "sum2")})
	if r.Status != http.StatusOK || previewError(r.Body) != "" {
		t.Fatalf("preview: %d %q", r.Status, previewError(r.Body))
	}
	if ts.QueryInt(`SELECT count(*) FROM tasks WHERE name = 'sum2'`) != 0 || ts.AuditCount() != n {
		t.Fatal("the preview wrote the task")
	}
	if !strings.Contains(r.Body, "italy_yaml") || !strings.Contains(r.Body, "sum2") {
		t.Fatal("the preview does not say the format or the task")
	}
	form, ok := formOf(r.Body, "tasks/import/confirm")
	if !ok || form.Get("digest") == "" {
		t.Fatalf("no confirmation form: %v", form)
	}
	r = c.Post("/tasks/import/confirm", form)
	tid := ts.QueryInt(`SELECT coalesce(max(id), 0) FROM tasks WHERE name = 'sum2'`)
	if tid == 0 || !wantRedirect(r, "task/"+itoa(tid)) {
		t.Fatalf("confirm: %d %q %s", r.Status, r.Location(), previewError(r.Body))
	}
	audited(t, ts, n, "task.import", tid)
	if ts.QueryInt(`SELECT count(*) FROM testcases WHERE dataset_id = (SELECT active_dataset_id FROM tasks WHERE id = $1)`, tid) != 4 {
		t.Fatal("the imported task has not its 4 testcases")
	}
	// A confirmation with a digest that is not a blob.
	form.Set("digest", strings.Repeat("0", 64))
	if r := c.Post("/tasks/import/confirm", form); previewError(r.Body) == "" {
		t.Fatal("a missing package was accepted")
	}

	// Zip slip and other unsafe names: nothing is extracted or written.
	parent := t.TempDir()
	for _, bad := range []string{"../evil.txt", "sum3/../../evil.txt", "/tmp/evil.txt", "sum3\\..\\evil.txt", "C:/evil.txt"} {
		n := ts.AuditCount()
		r := c.PostFiles("/tasks/import", nil, upload{"package", "sum3.zip", italyTaskZip(t, "sum3", bad)})
		if r.Status != http.StatusOK || !strings.Contains(previewError(r.Body), "unsafe zip") {
			t.Errorf("%s: %d %q", bad, r.Status, previewError(r.Body))
		}
		if _, ok := formOf(r.Body, "tasks/import/confirm"); ok {
			t.Errorf("%s: the preview can be confirmed", bad)
		}
		if ts.AuditCount() != n || ts.QueryInt(`SELECT count(*) FROM tasks WHERE name = 'sum3'`) != 0 {
			t.Errorf("%s: written", bad)
		}
	}
	if _, err := os.Stat(filepath.Join(parent, "evil.txt")); err == nil {
		t.Fatal("zip slip")
	}
	if _, err := os.Stat(filepath.Join(os.TempDir(), "evil.txt")); err == nil {
		t.Fatal("zip slip into the temporary directory")
	}
	// Not a zip.
	if r := c.PostFiles("/tasks/import", nil, upload{"package", "x.zip", []byte("not a zip")}); previewError(r.Body) == "" {
		t.Fatal("not a zip: no error")
	}

	// The export of a task imports back over it with no change.
	sid := itoa(f.Tasks["sum"])
	exp := c.Get("/task/" + sid + "/export")
	if exp.Status != http.StatusOK || exp.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("export: %d", exp.Status)
	}
	r = c.PostFiles("/task/"+sid+"/import", nil, upload{"package", "sum.zip", []byte(exp.Body)})
	if r.Status != http.StatusOK || previewError(r.Body) != "" || changesOf(r.Body) != "" {
		t.Fatalf("export and import: %d %q\n%s", r.Status, previewError(r.Body), changesOf(r.Body))
	}

	// The messaging role cannot import.
	o := ts.Client()
	o.Login("msg")
	if r := o.PostFiles("/tasks/import", nil, upload{"package", "sum2.zip", italyTaskZip(t, "sum4")}); r.Status != http.StatusForbidden {
		t.Fatalf("import as msg: %d", r.Status)
	}
}

// changesOf returns the rows of the change table of a preview that are
// changes (not "unchanged"), one per line.
func changesOf(body string) string {
	var out []string
	for _, m := range regexp.MustCompile(`<tr><td>([a-z]+)</td>(.*?)</tr>`).FindAllStringSubmatch(body, -1) {
		if m[1] != "unchanged" {
			out = append(out, m[1]+" "+m[2])
		}
	}
	return strings.Join(out, "\n")
}

// TestProblemYAML checks that problem.yaml round-trips (no change), that
// an edit previews and writes, and that a broken file is refused.
func TestProblemYAML(t *testing.T) {
	ts, f := newFixtureSite(t, testOptions{})
	c := ts.Client()
	c.Login("prep")
	for _, tk := range []string{"sum", "half", "quiz"} {
		tid := itoa(f.Tasks[tk])
		raw := c.Get("/task/" + tid + "/problem.yaml/raw")
		if raw.Status != http.StatusOK || !strings.HasPrefix(raw.Header.Get("Content-Type"), "application/yaml") || raw.Body == "" {
			t.Fatalf("%s: raw: %d", tk, raw.Status)
		}
		// The page shows the same text in its form.
		form := c.mustForm("/task/"+tid+"/problem.yaml", "task/"+tid+"/problem.yaml")
		if form.Get("problem") != raw.Body {
			t.Fatalf("%s: the form differs from the file:\n%q\n%q", tk, form.Get("problem"), raw.Body)
		}
		r := c.Post("/task/"+tid+"/problem.yaml", url.Values{"problem": {raw.Body}})
		if r.Status != http.StatusOK || previewError(r.Body) != "" || strings.Contains(r.Body, `id="title_errors"`) {
			t.Fatalf("%s: preview: %d %q", tk, r.Status, previewError(r.Body))
		}
		if ch := changesOf(r.Body); ch != "" {
			t.Errorf("%s: problem.yaml does not round-trip:\n%s", tk, ch)
		}
	}

	// An edit: the time limit of sum.
	tid := itoa(f.Tasks["sum"])
	raw := c.Get("/task/" + tid + "/problem.yaml/raw").Body
	re := regexp.MustCompile(`(?m)^(\s*time_limit:\s*)\S+`)
	if !re.MatchString(raw) {
		t.Fatalf("no time_limit in\n%s", raw)
	}
	edited := re.ReplaceAllString(raw, "${1}3")
	n := ts.AuditCount()
	r := c.Post("/task/"+tid+"/problem.yaml", url.Values{"problem": {edited}})
	if ch := changesOf(r.Body); !strings.Contains(ch, "updated <td>dataset</td>") || strings.Count(ch, "\n") != 0 {
		t.Fatalf("the preview does not show the change: %q\n%s", previewError(r.Body), changesOf(r.Body))
	}
	if ts.QueryInt(`SELECT count(*) FROM datasets WHERE id = $1 AND time_limit = 3`, f.Datasets["sum"]) != 0 || ts.AuditCount() != n {
		t.Fatal("the preview wrote")
	}
	form, ok := formOf(r.Body, "task/"+tid+"/problem.yaml/confirm")
	if !ok {
		t.Fatal("no confirmation form")
	}
	if r := c.Post("/task/"+tid+"/problem.yaml/confirm", form); !wantRedirect(r, "task/"+tid) {
		t.Fatalf("confirm: %d %q", r.Status, previewError(r.Body))
	}
	if ts.QueryInt(`SELECT count(*) FROM datasets WHERE id = $1 AND time_limit = 3`, f.Datasets["sum"]) != 1 {
		t.Fatal("the time limit was not written")
	}
	audited(t, ts, n, "task.problem_yaml", int64(f.Tasks["sum"]))
	// Uploaded as a file too.
	r = c.PostFiles("/task/"+tid+"/problem.yaml", nil, upload{"problem_file", "problem.yaml", []byte(edited)})
	if r.Status != http.StatusOK || previewError(r.Body) != "" || changesOf(r.Body) != "" {
		t.Fatalf("upload: %d %q\n%s", r.Status, previewError(r.Body), changesOf(r.Body))
	}
	// Broken YAML: an error, no confirmation.
	for _, bad := range []string{"name: [", "time_limit: -1\n" + edited} {
		r := c.Post("/task/"+tid+"/problem.yaml", url.Values{"problem": {bad}})
		if _, ok := formOf(r.Body, "task/"+tid+"/problem.yaml/confirm"); ok || r.Status != http.StatusOK {
			t.Errorf("%q: %d, can confirm", bad, r.Status)
		}
	}
	o := ts.Client()
	o.Login("ro")
	if r := o.Post("/task/"+tid+"/problem.yaml/confirm", form); r.Status != http.StatusForbidden {
		t.Fatalf("confirm as ro: %d", r.Status)
	}
}

// checkItems returns the checklist of the prepare page: text -> state.
func checkItems(body string) map[string]string {
	items := map[string]string{}
	re := regexp.MustCompile(`(?s)<span class="(correct|partiallycorrect|notcorrect)">[^<]*</span></td>\s*<td>([^<]*)</td>`)
	for _, m := range re.FindAllStringSubmatch(body, -1) {
		items[m[2]] = m[1]
	}
	return items
}

// TestPrepareChecklist checks the "what is missing" list of a new task
// and of a complete one.
func TestPrepareChecklist(t *testing.T) {
	ts, f := newFixtureSite(t, testOptions{})
	c := ts.Client()
	c.Login("prep")
	if r := c.Post("/tasks/add", url.Values{"name": {"empty"}}); r.Status != http.StatusFound {
		t.Fatalf("add task: %d", r.Status)
	}
	tid := ts.QueryInt(`SELECT id FROM tasks WHERE name = 'empty'`)
	items := checkItems(c.Get("/task/" + itoa(tid) + "/prepare").Body)
	for text, want := range map[string]string{
		"A statement": "notcorrect",
		"Testcases":   "notcorrect",
		"A test submission expected to be accepted that is": "notcorrect",
		"Every test submission gets its expected verdict":   "correct",
	} {
		if items[text] != want {
			t.Errorf("new task: %q is %q, want %q (%v)", text, items[text], want, items)
		}
	}
	if len(items) < 7 {
		t.Errorf("new task: %d items: %v", len(items), items)
	}
	// The demo task sum has a statement and testcases.
	items = checkItems(c.Get("/task/" + itoa(f.Tasks["sum"]) + "/prepare").Body)
	for _, text := range []string{"A statement", "Testcases", "Every testcase has an expected output",
		"Valid score type parameters (subtasks and their points)", "Managers, checker and parameters needed by the task type"} {
		if items[text] != "correct" {
			t.Errorf("sum: %q is %q (%v)", text, items[text], items)
		}
	}
	// A testcase without output is a warning.
	ts.Exec(`UPDATE testcases SET output = $1 WHERE id = $2`, emptyDigest, f.Testcase)
	items = checkItems(c.Get("/task/" + itoa(f.Tasks["sum"]) + "/prepare").Body)
	if items["Every testcase has an expected output"] != "partiallycorrect" {
		t.Errorf("empty output: %v", items)
	}
}
