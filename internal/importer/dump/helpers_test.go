// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package dump

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/db"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
)

// The dumps of the tests:
//
//   - testdata/dumps/demo (repository root): the demo contest exported by
//     cmsDumpExporter of CMS (schema 49): 3 tasks, 3 users, 2 submissions,
//     one of them scored;
//   - testdata/full and testdata/full.tar.gz: an export of a whole
//     installation of CMS (schema 49) by cmsDumpExporter, with an object
//     of every class (teams, admins, announcements, messages, questions,
//     tokens, evaluations, executables, user tests...) and six tasks in no
//     contest, whose files cmsDumpExporter does not export. Importing it
//     with cmsDumpImporter of CMS and with Import gives the same rows (by
//     natural keys, with the digests mapped from SHA-1 to SHA-256);
//   - testdata/multi: cmsDumpExporter -c <demo> of an installation where
//     alice also took part (with a submission) in a second contest, "other"
//     (task polyaplusb): the dump has both contests (the exporter reaches
//     "other" through the participations of alice) but only the files of
//     demo, the root of the dump (_objects). cmsDumpImporter of CMS
//     imports it.
var (
	demoDump  = filepath.Join("..", "..", "..", "testdata", "dumps", "demo")
	fullDump  = filepath.Join("testdata", "full")
	fullTGZ   = filepath.Join("testdata", "full.tar.gz")
	multiDump = filepath.Join("testdata", "multi")
)

// env is a database and a blob store for imports.
type env struct {
	pool  *pgxpool.Pool
	qc    *db.QueryCounter
	blobs blob.Store
	w     *importer.Writer
}

func newEnv(t testing.TB) *env {
	t.Helper()
	pool, qc := testutil.CountingDB(t)
	bs, err := blob.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &env{pool: pool, qc: qc, blobs: bs, w: importer.NewWriter(pool, bs, nil)}
}

// imp imports a dump and fails the test on errors, logging the report.
func (e *env) imp(t testing.TB, path string, opts Options) *importer.Result {
	t.Helper()
	res, err := Import(context.Background(), e.w, path, opts)
	if err != nil {
		if res != nil {
			for _, i := range res.Report.Issues() {
				t.Log(i)
			}
		}
		t.Fatalf("import %s: %v", path, err)
	}
	return res
}

func (e *env) count(t testing.TB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// counts returns the number of rows of the tables of the CMS model.
func (e *env) counts(t testing.TB) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, table := range cmsTables {
		out[table] = e.count(t, "SELECT count(*) FROM "+table)
	}
	return out
}

var cmsTables = []string{"admins", "contests", "groups", "announcements", "users", "teams", "participations",
	"messages", "questions", "tasks", "statements", "attachments", "datasets", "managers", "testcases",
	"submissions", "files", "tokens", "submission_results", "executables", "evaluations", "user_tests",
	"user_test_files", "user_test_managers", "user_test_results", "user_test_executables", "task_scores"}

// blobCount counts the blobs of the store.
func (e *env) blobCount(t testing.TB) int {
	t.Helper()
	n := 0
	if err := e.blobs.Walk(context.Background(), func(string, int64) error { n++; return nil }); err != nil {
		t.Fatal(err)
	}
	return n
}

// checkDigests checks that every digest of the database is in the blob
// store (or is the tombstone) and that its content is a file of the dump
// at dir (its SHA-1 is the name of a file of dir/files): the digests were
// rewritten from SHA-1 to SHA-256. It returns the number of tombstones.
func (e *env) checkDigests(t testing.TB, dir string) int {
	t.Helper()
	ctx := context.Background()
	rows, err := e.pool.Query(ctx, `
		SELECT 'statements', digest FROM statements UNION ALL SELECT 'attachments', digest FROM attachments
		UNION ALL SELECT 'managers', digest FROM managers UNION ALL SELECT 'testcases', input FROM testcases
		UNION ALL SELECT 'testcases', output FROM testcases UNION ALL SELECT 'files', digest FROM files
		UNION ALL SELECT 'executables', digest FROM executables UNION ALL SELECT 'user_tests', input FROM user_tests
		UNION ALL SELECT 'user_test_files', digest FROM user_test_files
		UNION ALL SELECT 'user_test_managers', digest FROM user_test_managers
		UNION ALL SELECT 'user_test_results', output FROM user_test_results WHERE output IS NOT NULL
		UNION ALL SELECT 'user_test_executables', digest FROM user_test_executables`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	tombstones := 0
	for rows.Next() {
		var table, d string
		if err := rows.Scan(&table, &d); err != nil {
			t.Fatal(err)
		}
		if d == tombstone {
			tombstones++
			continue
		}
		rc, err := e.blobs.Open(ctx, d)
		if err != nil {
			t.Errorf("%s: digest %s: %v", table, d, err)
			continue
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Errorf("%s: digest %s: %v", table, d, err)
			continue
		}
		s256, s1 := sha256.Sum256(b), sha1.Sum(b)
		if hex.EncodeToString(s256[:]) != d {
			t.Errorf("%s: digest %s is not the SHA-256 of its content", table, d)
		}
		if _, err := os.Stat(filepath.Join(dir, "files", hex.EncodeToString(s1[:]))); err != nil {
			t.Errorf("%s: digest %s: the content is not a file of the dump", table, d)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return tombstones
}

// builder writes a dump in a directory.
type builder struct {
	t       testing.TB
	dir     string
	objs    map[string]any
	version int
	roots   []string
}

func newBuilder(t testing.TB, version int) *builder {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "files"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &builder{t: t, dir: dir, objs: map[string]any{}, version: version}
}

// file stores content in files/ and returns its SHA-1.
func (b *builder) file(content string) string {
	sum := sha1.Sum([]byte(content))
	name := hex.EncodeToString(sum[:])
	if err := os.WriteFile(filepath.Join(b.dir, "files", name), []byte(content), 0o644); err != nil {
		b.t.Fatal(err)
	}
	return name
}

func (b *builder) add(key string, obj map[string]any) { b.objs[key] = obj }

// write writes contest.json and returns the directory.
func (b *builder) write() string {
	b.t.Helper()
	all := map[string]any{"_version": b.version, "_objects": b.roots}
	for k, v := range b.objs {
		all[k] = v
	}
	data, err := json.MarshalIndent(all, "", "    ")
	if err != nil {
		b.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b.dir, modelName), data, 0o644); err != nil {
		b.t.Fatal(err)
	}
	return b.dir
}

// copyDir copies a dump to a new directory.
func copyDir(t testing.TB, src string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(p string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if de.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// dumpFiles lists the files of a dump directory (relative, sorted).
func dumpFiles(t testing.TB, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, de fs.DirEntry, err error) error {
		if err != nil || de.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

// zipDump writes the dump at dir as a zip file, under root ("" or
// "name/").
func zipDump(t testing.TB, dir, root string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "dump.zip")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, rel := range dumpFiles(t, dir) {
		w, err := zw.Create(root + rel)
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(b); err != nil {
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

// tarDump writes the dump at dir as a tar archive (gzipped if gz) under
// root, with the files in the order given by order (nil: sorted).
func tarDump(t testing.TB, dir, root string, gz bool, order func([]string) []string) string {
	t.Helper()
	name := "dump.tar"
	if gz {
		name += ".gz"
	}
	p := filepath.Join(t.TempDir(), name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	var w io.Writer = f
	var zw *gzip.Writer
	if gz {
		zw = gzip.NewWriter(f)
		w = zw
	}
	tw := tar.NewWriter(w)
	files := dumpFiles(t, dir)
	if order != nil {
		files = order(files)
	}
	for _, rel := range files {
		b, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatal(err)
		}
		if err := tw.WriteHeader(&tar.Header{Name: root + rel, Mode: 0o644, Size: int64(len(b)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if zw != nil {
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

// issues returns the report as text.
func issues(rep *importer.Report) string {
	var s string
	for _, i := range rep.Issues() {
		s += i.String() + "\n"
	}
	return s
}
