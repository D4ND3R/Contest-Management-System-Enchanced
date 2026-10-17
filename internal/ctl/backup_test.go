// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package ctl

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/db"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
)

// testConfig is a configuration on the database at url with a local blob
// store in blobDir.
func testConfig(t *testing.T, url, blobDir string) *config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.Database.URL = url
	cfg.DataDir = t.TempDir()
	cfg.Storage.Backend = "local"
	cfg.Storage.Local.Path = blobDir
	return cfg
}

func mustExec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

// seedJudged fills pool with a contest, a judged submission and its task
// score cache.
func seedJudged(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	d := strings.Repeat("a", 64)
	mustExec(t, pool, `INSERT INTO contests (id, name, description) VALUES (1, 'ioi', 'IOI')`)
	mustExec(t, pool, `INSERT INTO groups (id, name, contest_id) VALUES (1, 'main', 1)`)
	mustExec(t, pool, `INSERT INTO tasks (id, num, contest_id, name, title, submission_format) VALUES (1, 1, 1, 'sum', 'Sum', '{sum.%l}')`)
	mustExec(t, pool, `INSERT INTO datasets (id, task_id, description, task_type, task_type_parameters, score_type, score_type_parameters)
		VALUES (1, 1, 'default', 'Batch', '["alone", ["", ""], "diff"]', 'Sum', '50')`)
	mustExec(t, pool, `UPDATE tasks SET active_dataset_id = 1 WHERE id = 1`)
	mustExec(t, pool, `INSERT INTO testcases (id, dataset_id, codename, public, input, output) VALUES (1, 1, 'a', true, $1, $1), (2, 1, 'b', false, $1, $1)`, d)
	mustExec(t, pool, `INSERT INTO users (id, first_name, last_name, username, password) VALUES (1, 'A', 'L', 'alice', 'plaintext:x')`)
	mustExec(t, pool, `INSERT INTO participations (id, contest_id, user_id, group_id) VALUES (1, 1, 1, 1)`)
	mustExec(t, pool, `INSERT INTO submissions (id, opaque_id, participation_id, task_id, timestamp, language)
		VALUES (1, 1, 1, 1, $1, 'C++17 / g++')`, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	mustExec(t, pool, `INSERT INTO submission_results (submission_id, dataset_id, compilation_outcome, evaluation_outcome,
			score, score_details, public_score, public_score_details, ranking_score_details)
		VALUES (1, 1, 'ok', 'ok', 100, '[]', 50, '[]', '{}')`)
	mustExec(t, pool, `INSERT INTO evaluations (submission_id, dataset_id, testcase_id, outcome) VALUES (1, 1, 1, '1.0'), (1, 1, 2, '1.0')`)
	mustExec(t, pool, `INSERT INTO server_settings (key, value) VALUES ('public_url.contest', '":8000"')`)
	err := pgx.BeginFunc(context.Background(), pool, func(tx pgx.Tx) error {
		_, err := judging.RecomputeTask(context.Background(), tx, 1)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, `SELECT count(*) FROM task_scores`); n == 0 {
		t.Fatal("the seed has no task_scores")
	}
}

func count(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

// tableSums returns, for every table of the public schema, its row count
// and a checksum of its rows.
func tableSums(t *testing.T, pool *pgxpool.Pool) map[string]string {
	t.Helper()
	ctx := context.Background()
	rows, err := pool.Query(ctx, `SELECT c.relname::text FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = 'public' AND c.relkind = 'r' ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, name := range names {
		from := pgx.Identifier{"public", name}.Sanitize() + " x"
		if name == "schema_migrations" { // applied_at differs
			from = "(SELECT version, name, checksum FROM schema_migrations) x"
		}
		if name == "task_scores" { // recomputed: updated_at differs
			from = "(SELECT participation_id, task_id, dataset_id, score, public_score, tokened_score, partial FROM task_scores) x"
		}
		var sum string
		q := `SELECT count(*) || ':' || coalesce(md5(string_agg(x::text, E'\n' ORDER BY x::text COLLATE "C")), '') FROM ` + from
		if err := pool.QueryRow(ctx, q).Scan(&sum); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out[name] = sum
	}
	return out
}

func compareSums(t *testing.T, want, got map[string]string) {
	t.Helper()
	for name, w := range want {
		if got[name] != w {
			t.Errorf("table %s: got %q, want %q", name, got[name], w)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("unexpected table %s", name)
		}
	}
}

func openURL(t *testing.T, url string) *pgxpool.Pool {
	t.Helper()
	pool, err := db.Open(context.Background(), config.Database{URL: url, MaxConns: 4})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func blobDigests(t *testing.T, dir string) []string {
	t.Helper()
	store, err := blob.NewLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	if err := store.Walk(context.Background(), func(d string, _ int64) error {
		out = append(out, d)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}

// checkIntegrity checks that user triggers are enabled and every foreign
// key of the source exists and is validated.
func checkIntegrity(t *testing.T, src, dst *pgxpool.Pool) {
	t.Helper()
	const disabled = `SELECT count(*) FROM pg_trigger tg JOIN pg_class c ON c.oid = tg.tgrelid
JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = 'public' AND tg.tgenabled <> 'O'`
	if n := count(t, dst, disabled); n != 0 {
		t.Errorf("%d triggers disabled after the restore", n)
	}
	const fks = `SELECT count(*) FROM pg_constraint c JOIN pg_namespace n ON n.oid = c.connamespace
WHERE c.contype = 'f' AND n.nspname = 'public' AND c.convalidated`
	if w, g := count(t, src, fks), count(t, dst, fks); w == 0 || w != g {
		t.Errorf("validated foreign keys: %d, want %d", g, w)
	}
}

func TestBackupRestore(t *testing.T) {
	ctx := context.Background()
	src := testutil.DB(t)
	seedJudged(t, src)
	srcBlobs := t.TempDir()
	store, err := blob.NewLocal(srcBlobs)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		if _, _, err := store.Put(ctx, strings.NewReader(fmt.Sprintf("blob %d", i))); err != nil {
			t.Fatal(err)
		}
	}
	cfg := testConfig(t, src.Config().ConnString(), srcBlobs)
	cfgPath := filepath.Join(t.TempDir(), "cms.yaml")
	cfgData := []byte("secret_key: not-a-real-one\n")
	if err := os.WriteFile(cfgPath, cfgData, 0o600); err != nil {
		t.Fatal(err)
	}
	outDir := t.TempDir()
	var out bytes.Buffer
	file, err := Backup(ctx, cfg, cfgPath, BackupOptions{Output: outDir + "/", Label: "test run"}, &out)
	if err != nil {
		t.Fatalf("backup: %v\n%s", err, out.String())
	}
	if st, err := os.Stat(file); err != nil || st.Mode().Perm() != 0o600 || !strings.HasSuffix(file, "-test-run"+BackupSuffix) {
		t.Fatalf("archive %s: %v %v", file, st.Mode(), err)
	}
	if left, _ := filepath.Glob(filepath.Join(outDir, ".cms-backup-*")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
	want := tableSums(t, src)
	wantBlobs := blobDigests(t, srcBlobs)

	// --verify-only touches nothing (the database is not even opened).
	bad := testConfig(t, "postgres://nobody@127.0.0.1:1/none?connect_timeout=1", t.TempDir())
	out.Reset()
	if err := Restore(ctx, bad, file, RestoreOptions{VerifyOnly: true}, &out); err != nil || !strings.Contains(out.String(), "archive verified") {
		t.Fatalf("verify-only: %v\n%s", err, out.String())
	}

	t.Run("empty database", func(t *testing.T) {
		url := testutil.EmptyDatabaseURL(t)
		dstBlobs := t.TempDir()
		cfgTo := filepath.Join(t.TempDir(), "restored.yaml")
		var out bytes.Buffer
		if err := Restore(ctx, testConfig(t, url, dstBlobs), file, RestoreOptions{Force: true, ConfigTo: cfgTo}, &out); err != nil {
			t.Fatalf("restore: %v\n%s", err, out.String())
		}
		dst := openURL(t, url)
		compareSums(t, want, tableSums(t, dst))
		checkIntegrity(t, src, dst)
		if got := blobDigests(t, dstBlobs); !slices.Equal(got, wantBlobs) {
			t.Errorf("blobs %v, want %v", got, wantBlobs)
		}
		if got, err := os.ReadFile(cfgTo); err != nil || !bytes.Equal(got, cfgData) {
			t.Errorf("--config-to: %q %v", got, err)
		}
		if err := db.CheckSchema(ctx, dst); err != nil {
			t.Error(err)
		}
		// Sequences continue after the restored ids.
		var id int32
		if err := dst.QueryRow(ctx, `INSERT INTO contests (name, description) VALUES ('new', 'New') RETURNING id`).Scan(&id); err != nil || id <= 1 {
			t.Errorf("new contest: id %d, %v", id, err)
		}
		if err := dst.QueryRow(ctx, `INSERT INTO users (first_name, last_name, username, password) VALUES ('B', 'L', 'bob', 'plaintext:y') RETURNING id`).Scan(&id); err != nil || id <= 1 {
			t.Errorf("new user: id %d, %v", id, err)
		}
		// The database is no longer empty: a second restore needs --clean.
		if err := Restore(ctx, testConfig(t, url, dstBlobs), file, RestoreOptions{Force: true, NoBlobs: true}, io.Discard); err == nil || !strings.Contains(err.Error(), "not empty") {
			t.Errorf("restore into a non-empty database: %v", err)
		}
	})

	t.Run("clean", func(t *testing.T) {
		dst := testutil.DB(t)
		mustExec(t, dst, `INSERT INTO contests (id, name, description) VALUES (7, 'other', 'Other')`)
		mustExec(t, dst, `CREATE TABLE stray (x int)`)
		mustExec(t, dst, `CREATE FUNCTION stray_fn() RETURNS int LANGUAGE sql AS 'SELECT 1'`)
		var out bytes.Buffer
		if err := Restore(ctx, testConfig(t, dst.Config().ConnString(), t.TempDir()), file, RestoreOptions{Clean: true, Force: true, NoBlobs: true}, &out); err != nil {
			t.Fatalf("restore --clean: %v\n%s", err, out.String())
		}
		dst.Reset() // cached statements refer to the dropped tables
		compareSums(t, want, tableSums(t, dst))
		checkIntegrity(t, src, dst)
		if n := count(t, dst, `SELECT count(*) FROM pg_proc WHERE proname = 'stray_fn'`); n != 0 {
			t.Error("--clean kept a function")
		}
	})

	t.Run("refuses other sessions", func(t *testing.T) {
		url := testutil.EmptyDatabaseURL(t)
		openURL(t, url).Ping(ctx)
		err := Restore(ctx, testConfig(t, url, t.TempDir()), file, RestoreOptions{NoBlobs: true}, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "other sessions") {
			t.Errorf("restore with another session connected: %v", err)
		}
	})
}

// rewriteArchive copies the archive src to dst through edit, which may
// change the contents of an entry (SHA256SUMS is copied as is).
func rewriteArchive(t *testing.T, src, dst string, edit func(name string, b []byte) []byte) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	err := readArchive(src, func(e archiveEntry) error {
		b, err := io.ReadAll(e.R)
		if err != nil {
			return err
		}
		b = edit(e.Name, b)
		if err := tw.WriteHeader(&tar.Header{Name: e.Name, Mode: 0o600, Size: int64(len(b)), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		_, err = tw.Write(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreRefusesDamagedArchives(t *testing.T) {
	ctx := context.Background()
	src := testutil.DB(t)
	seedJudged(t, src)
	srcBlobs := t.TempDir()
	if store, err := blob.NewLocal(srcBlobs); err != nil {
		t.Fatal(err)
	} else if _, _, err := store.Put(ctx, strings.NewReader("data")); err != nil {
		t.Fatal(err)
	}
	file, err := Backup(ctx, testConfig(t, src.Config().ConnString(), srcBlobs), "", BackupOptions{Output: t.TempDir() + "/"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	damaged := map[string]string{}
	write := func(name string, b []byte) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		damaged[name] = p
	}
	write("truncated", data[:len(data)/2])
	write("garbage", []byte("not an archive"))
	flip := func(target string) func(string, []byte) []byte {
		return func(name string, b []byte) []byte {
			if name == target && len(b) > 0 {
				b = slices.Clone(b)
				b[len(b)/2] ^= 1
			}
			return b
		}
	}
	damaged["bad table checksum"] = filepath.Join(dir, "table")
	rewriteArchive(t, file, damaged["bad table checksum"], flip(dbEntryPrefix+"contests"+dbEntrySuffix))
	damaged["bad blob checksum"] = filepath.Join(dir, "blob")
	rewriteArchive(t, file, damaged["bad blob checksum"], func(name string, b []byte) []byte {
		if strings.HasPrefix(name, blobEntryPrefix) {
			return []byte("DATA")
		}
		return b
	})
	damaged["no SHA256SUMS"] = filepath.Join(dir, "nosums")
	rewriteArchive(t, file, damaged["no SHA256SUMS"], func(name string, b []byte) []byte {
		if name == sumsName {
			return []byte("")
		}
		return b
	})

	dst := testutil.DB(t)
	mustExec(t, dst, `INSERT INTO contests (id, name, description) VALUES (7, 'other', 'Other')`)
	before := tableSums(t, dst)
	dstBlobs := t.TempDir()
	cfg := testConfig(t, dst.Config().ConnString(), dstBlobs)
	for name, p := range damaged {
		err := Restore(ctx, cfg, p, RestoreOptions{Clean: true, Force: true}, io.Discard)
		if err == nil {
			t.Errorf("%s: restored", name)
			continue
		}
		if err := Restore(ctx, cfg, p, RestoreOptions{VerifyOnly: true}, io.Discard); err == nil {
			t.Errorf("%s: --verify-only accepted it", name)
		}
	}
	compareSums(t, before, tableSums(t, dst))
	if got := blobDigests(t, dstBlobs); len(got) != 0 {
		t.Errorf("blobs written from damaged archives: %v", got)
	}
}

func TestBackupKeep(t *testing.T) {
	src := testutil.DB(t)
	dir := t.TempDir()
	old := []string{"cms-backup-20240101T000000Z.tar.gz", "cms-backup-20250101T000000Z-pre-upgrade.tar.gz", "cms-backup-20250601T000000Z.tar.gz"}
	others := []string{"notes.txt", "cms-backup-20200101T000000Z.tar.gz.sha256"}
	for _, n := range append(slices.Clone(old), others...) {
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	file, err := Backup(context.Background(), testConfig(t, src.Config().ConnString(), t.TempDir()), "", BackupOptions{Output: dir, NoBlobs: true, Keep: 2}, &out)
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	wantNames := append([]string{old[2], filepath.Base(file)}, others...)
	slices.Sort(wantNames)
	if !slices.Equal(got, wantNames) {
		t.Errorf("after --keep 2: %v, want %v\n%s", got, wantNames, out.String())
	}
	m, err := verifyArchive(file)
	if err != nil || m.Blobs {
		t.Errorf("--no-blobs archive: %+v %v", m, err)
	}
}

// writeOldBackup writes, from a database at schema version `version`, the
// archive an older cms would have written.
func writeOldBackup(t *testing.T, pool *pgxpool.Pool, version int, file string) {
	t.Helper()
	ctx := context.Background()
	all, err := db.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	m := backupManifest{Format: archiveFormat, CreatedAt: time.Now().UTC(), CMSVersion: "v0.old", SchemaVersion: version}
	for _, mig := range all[:version] {
		m.Migrations = append(m.Migrations, mig.Checksum)
	}
	f, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if m.Tables, err = publicTables(ctx, tx); err != nil {
			return err
		}
		mj, _ := json.Marshal(m)
		a := newArchiveWriter(f)
		if err := a.addBytes(manifestName, mj); err != nil {
			return err
		}
		for _, tb := range m.Tables {
			if _, err := copyTableOut(ctx, tx, tb, a, t.TempDir()); err != nil {
				return err
			}
		}
		return a.close()
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRestoreOlderSchema(t *testing.T) {
	ctx := context.Background()
	all, err := db.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	version := len(all) - 1
	old := testutil.EmptyDB(t)
	if err := pgx.BeginFunc(ctx, old, func(tx pgx.Tx) error {
		_, err := db.MigrateTx(ctx, tx, version)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	mustExec(t, old, `INSERT INTO contests (id, name, description) VALUES (3, 'old', 'Old')`)
	mustExec(t, old, `INSERT INTO users (id, first_name, last_name, username, password) VALUES (5, 'A', 'L', 'alice', 'plaintext:x')`)
	file := filepath.Join(t.TempDir(), "old"+BackupSuffix)
	writeOldBackup(t, old, version, file)

	url := testutil.EmptyDatabaseURL(t)
	var out bytes.Buffer
	if err := Restore(ctx, testConfig(t, url, t.TempDir()), file, RestoreOptions{Force: true}, &out); err != nil {
		t.Fatalf("restore: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "applied 1 newer migration") {
		t.Errorf("output does not mention the migration:\n%s", out.String())
	}
	dst := openURL(t, url)
	if err := db.CheckSchema(ctx, dst); err != nil {
		t.Fatal(err)
	}
	if n := count(t, dst, `SELECT count(*) FROM contests WHERE id = 3 AND name = 'old'`); n != 1 {
		t.Error("the contest of the old backup is missing")
	}
	if n := count(t, dst, `SELECT count(*) FROM users WHERE id = 5`); n != 1 {
		t.Error("the user of the old backup is missing")
	}

}
