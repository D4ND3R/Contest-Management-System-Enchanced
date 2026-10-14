// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package tps

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
)

func testWriter(t *testing.T) *importer.Writer {
	t.Helper()
	pool := testutil.DB(t)
	bs, err := blob.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return importer.NewWriter(pool, bs, nil)
}

// dumpTask runs testdata/upstream/dump_task.sql (the query that made the
// golden files) on our database and translates the digests to SHA-1.
func dumpTask(t *testing.T, w *importer.Writer, name string) *upTask {
	t.Helper()
	ctx := context.Background()
	sql, err := os.ReadFile(filepath.Join("testdata", "upstream", "dump_task.sql"))
	if err != nil {
		t.Fatal(err)
	}
	var out string
	if err := w.Pool.QueryRow(ctx, strings.ReplaceAll(string(sql), ":'task'", "$1"), name).Scan(&out); err != nil {
		t.Fatalf("dump %s: %v", name, err)
	}
	var u upTask
	if err := json.Unmarshal([]byte(out), &u); err != nil {
		t.Fatal(err)
	}
	sha := func(digest string) string {
		rc, err := w.Blobs.Open(ctx, digest)
		if err != nil {
			t.Fatalf("blob %s: %v", digest, err)
		}
		defer rc.Close()
		h := sha1.New()
		if _, err := io.Copy(h, rc); err != nil {
			t.Fatal(err)
		}
		return hex.EncodeToString(h.Sum(nil))
	}
	for _, m := range []map[string]string{u.Statements, u.Attachments} {
		for k, v := range m {
			m[k] = sha(v)
		}
	}
	for _, d := range u.Datasets {
		for k, v := range d.Managers {
			d.Managers[k] = sha(v)
		}
		for i := range d.Testcases {
			d.Testcases[i].Input, d.Testcases[i].Output = sha(d.Testcases[i].Input), sha(d.Testcases[i].Output)
		}
	}
	u.normalize(t)
	return &u
}

// TestImportTasks imports the tasks of testdata, compares what is stored
// with what the original CMS stored, and imports them again.
func TestImportTasks(t *testing.T) {
	needCompiler(t)
	w := testWriter(t)
	ctx := context.Background()
	for name, compiled := range fixtures {
		res, err := w.ImportTask(ctx, Loader{}, fixture(name), importer.TaskImport{})
		if err != nil {
			t.Fatalf("import %s: %v\n%s", name, err, messages(res.Report.Issues()))
		}
		want := readUpstream(t, filepath.Join("testdata", "upstream", name+".json"))
		compareUpstream(t, dumpTask(t, w, name), want, compiled...)

		// Again: nothing changed (the binaries are the same).
		res, err = w.ImportTask(ctx, Loader{}, fixture(name), importer.TaskImport{TaskOptions: importer.TaskOptions{Update: true}})
		if err != nil {
			t.Fatalf("update %s: %v", name, err)
		}
		if n := res.Count("task", importer.Unchanged); n != 1 {
			t.Errorf("%s: not unchanged: %v", name, res.Changes)
		}
	}
}
