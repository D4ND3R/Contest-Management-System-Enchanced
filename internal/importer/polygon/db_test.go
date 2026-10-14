// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package polygon

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

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

func texts(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), sql, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestImportContest imports the Polygon contest of testdata with its
// tasks and users, compares the tasks stored with those the original CMS
// stored, and imports it again.
func TestImportContest(t *testing.T) {
	needCompiler(t)
	w := testWriter(t)
	ctx := context.Background()
	res, err := w.ImportContest(ctx, Loader{}, fixture, importer.ContestOptions{ImportTasks: true, ImportUsers: true})
	if err != nil {
		t.Fatalf("import: %v\n%s", err, messages(res.Report.Issues()))
	}
	for _, name := range []string{"polyaplusb", "polypairs"} {
		want := readUpstream(t, filepath.Join("testdata", "upstream", name+".json"))
		compareUpstream(t, dumpTask(t, w, name), want, "checker")
	}

	got := texts(t, w.Pool, `SELECT c.name || ' ' || c.description || ' ' || g.name || ' ' || g.start || ' ' || g.stop
		FROM contests c JOIN groups g ON g.id = c.main_group_id`)
	if want := []string{"polycontest Polygon Test Round default 1970-01-01 00:00:00+00 1970-01-01 00:00:00+00"}; !slices.Equal(got, want) {
		t.Errorf("contest %q, want %q", got, want)
	}
	got = texts(t, w.Pool, `SELECT t.num || ' ' || t.name FROM tasks t ORDER BY t.num`)
	if want := []string{"0 polyaplusb", "1 polypairs"}; !slices.Equal(got, want) {
		t.Errorf("tasks %q, want %q", got, want)
	}
	got = texts(t, w.Pool, `SELECT u.username || ' ' || u.first_name || ' ' || u.last_name || ' ' || u.password || ' ' ||
			coalesce(p.password, '-') || ' ' || p.hidden
		FROM participations p JOIN users u ON u.id = p.user_id ORDER BY u.username`)
	want := []string{
		"polyalice Alice Liddell plaintext:alicepw plaintext:alicepw false",
		"polybob Bob Builder plaintext:bobpw plaintext:bobpw false",
		"polyjury Ju Ry plaintext:jurypw plaintext:jurypw true",
	}
	if !slices.Equal(got, want) {
		t.Errorf("participations\n%q\nwant\n%q", got, want)
	}

	// Again, updating everything: nothing changed (the checker compiles
	// to the same binary).
	res, err = w.ImportContest(ctx, Loader{}, fixture, importer.ContestOptions{UpdateContest: true, UpdateTasks: true})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if n := res.Count("task", importer.Unchanged); n != 2 {
		t.Errorf("%d tasks unchanged, want 2: %v", n, res.Changes)
	}
}

// TestImportTask imports one task with a new name, as cmsImportTask -n.
func TestImportTask(t *testing.T) {
	w := testWriter(t)
	ctx := context.Background()
	res, err := w.ImportTask(ctx, Loader{}, fixtureTask("polypairs"), importer.TaskImport{Name: "pairs2"})
	if err != nil {
		t.Fatalf("import: %v\n%s", err, messages(res.Report.Issues()))
	}
	got := texts(t, w.Pool, `SELECT t.name || ' ' || d.description || ' ' || array_to_string(t.submission_format, ',')
		FROM tasks t JOIN datasets d ON d.id = t.active_dataset_id`)
	// The submission format keeps the name of the package, as upstream.
	if want := []string{"pairs2 tests polypairs.%l"}; !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}
