// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package dump

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
)

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func at(sec float64) time.Time {
	s, us := micros(sec)
	return time.Unix(s, us*1000).UTC()
}

// demoCounts are the rows of the demo dump.
var demoCounts = map[string]int{
	"admins": 0, "contests": 1, "groups": 1, "announcements": 0, "users": 3, "teams": 0, "participations": 3,
	"messages": 0, "questions": 0, "tasks": 3, "statements": 3, "attachments": 2, "datasets": 3, "managers": 1,
	"testcases": 9, "submissions": 2, "files": 2, "tokens": 0, "submission_results": 1, "executables": 0,
	"evaluations": 0, "user_tests": 0, "user_test_files": 0, "user_test_managers": 0, "user_test_results": 0,
	"user_test_executables": 0, "task_scores": 2,
}

func checkCounts(t *testing.T, got, want map[string]int) {
	t.Helper()
	for _, table := range cmsTables {
		if got[table] != want[table] {
			t.Errorf("%s: %d rows, want %d", table, got[table], want[table])
		}
	}
}

func TestImportDemo(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	res := e.imp(t, demoDump, Options{})
	if len(res.Report.Issues()) > 0 {
		t.Errorf("issues:\n%s", issues(res.Report))
	}
	checkCounts(t, e.counts(t), demoCounts)

	// What the result says.
	var contestID int32
	if err := e.pool.QueryRow(ctx, `SELECT id FROM contests WHERE name = 'demo'`).Scan(&contestID); err != nil {
		t.Fatal(err)
	}
	if res.ContestID != contestID || len(res.Events) != 1 ||
		res.Events[0].Kind != judging.EventSweep || res.Events[0].ContestID != contestID {
		t.Errorf("contest %d, events %+v", res.ContestID, res.Events)
	}
	if len(res.TaskIDs) != 3 || res.Count("task", importer.Created) != 3 || res.Count("user", importer.Created) != 3 {
		t.Errorf("result %+v", res)
	}
	if c := res.Changes[0]; c.Kind != "contest" || !strings.Contains(c.Details, "2 submissions (1 scored)") {
		t.Errorf("change %s", c)
	}

	// Ids follow the order of the original installation: tasks by
	// position, submissions by time.
	var names []string
	rows, _ := e.pool.Query(ctx, `SELECT name FROM tasks ORDER BY id`)
	for rows.Next() {
		var n string
		_ = rows.Scan(&n)
		names = append(names, n)
	}
	if rows.Err() != nil || !slices.Equal(names, []string{"sum", "half", "quiz"}) {
		t.Errorf("tasks by id: %v %v", names, rows.Err())
	}

	// Values: timestamps (naive UTC seconds), intervals, JSON, opaque ids.
	var (
		start, stop, analysis time.Time
		genInterval           time.Duration
		tokenMode             string
		tokenMax              int32
	)
	if err := e.pool.QueryRow(ctx, `SELECT g.start, g.stop, g.analysis_start, c.token_gen_interval, c.token_mode, c.token_max_number
		FROM contests c JOIN groups g ON g.id = c.main_group_id`).Scan(&start, &stop, &analysis, &genInterval, &tokenMode, &tokenMax); err != nil {
		t.Fatal(err)
	}
	if !start.Equal(at(1735689600)) || !stop.Equal(at(1893456000)) || !analysis.Equal(at(4102444800)) ||
		genInterval != 30*time.Second || tokenMode != "finite" || tokenMax != 10 {
		t.Errorf("contest: %v %v %v %v %s %d", start, stop, analysis, genInterval, tokenMode, tokenMax)
	}
	var ttp, stp string
	var mem int64
	if err := e.pool.QueryRow(ctx, `SELECT task_type_parameters::text, score_type_parameters::text, memory_limit
		FROM datasets d JOIN tasks t ON t.active_dataset_id = d.id WHERE t.name = 'sum'`).Scan(&ttp, &stp, &mem); err != nil {
		t.Fatal(err)
	}
	if ttp != `["alone", ["", ""], "diff"]` || stp != `[[40, 2], [60, 2]]` || mem != 256<<20 {
		t.Errorf("dataset: %s %s %d", ttp, stp, mem)
	}
	var (
		opaque  int64
		ts      time.Time
		score   float64
		ranking []string
		scored  time.Time
		ctext   []string
	)
	if err := e.pool.QueryRow(ctx, `SELECT s.opaque_id, s.timestamp, r.score, r.ranking_score_details, r.scored_at, r.compilation_text
		FROM submissions s JOIN submission_results r ON r.submission_id = s.id`).Scan(&opaque, &ts, &score, &ranking, &scored, &ctext); err != nil {
		t.Fatal(err)
	}
	if opaque != 4926046975063954292 || !ts.Equal(at(1791365124.544329)) || ts.Nanosecond() != 544329000 || score != 100 ||
		!slices.Equal(ranking, []string{"40", "60"}) || !scored.Equal(at(1791365135.922147)) ||
		!slices.Equal(ctext, []string{"Compilation succeeded"}) {
		t.Errorf("result: %d %v %g %v %v %v", opaque, ts, score, ranking, scored, ctext)
	}

	// The task score cache: alice scored 100, bob is not judged yet.
	type ts2 struct {
		user    string
		score   float64
		partial bool
	}
	var got []ts2
	rows, _ = e.pool.Query(ctx, `SELECT u.username, s.score, s.partial FROM task_scores s
		JOIN participations p ON p.id = s.participation_id JOIN users u ON u.id = p.user_id ORDER BY u.username`)
	for rows.Next() {
		var x ts2
		_ = rows.Scan(&x.user, &x.score, &x.partial)
		got = append(got, x)
	}
	if rows.Err() != nil || !slices.Equal(got, []ts2{{"alice", 100, false}, {"bob", 0, true}}) {
		t.Errorf("task scores %v %v", got, rows.Err())
	}

	// Digests are SHA-256, in the blob store.
	if n := e.checkDigests(t, demoDump); n != 0 {
		t.Errorf("%d tombstones", n)
	}
	if n := e.blobCount(t); n != 23 {
		t.Errorf("%d blobs, want the 23 files of the dump", n)
	}

	// The identity sequences are past the imported ids.
	for _, q := range []string{
		`INSERT INTO contests (name, description) VALUES ('after', 'x') RETURNING id`,
		`INSERT INTO users (first_name, last_name, username, password) VALUES ('a', 'b', 'after', 'plaintext:x') RETURNING id`,
		`INSERT INTO tasks (name, title) VALUES ('after', 'x') RETURNING id`,
		`INSERT INTO submissions (opaque_id, participation_id, task_id, timestamp)
			SELECT 1, p.id, t.id, now() FROM participations p, tasks t LIMIT 1 RETURNING id`,
		`INSERT INTO testcases (dataset_id, codename, input, output) SELECT id, 'after', 'x', 'x' FROM datasets LIMIT 1 RETURNING id`,
	} {
		var id int32
		if err := e.pool.QueryRow(ctx, q).Scan(&id); err != nil {
			t.Errorf("%s: %v", q, err)
		}
	}
}

func TestImportArchives(t *testing.T) {
	e := newEnv(t)
	reversed := func(files []string) []string {
		slices.Reverse(files) // contest.json last: the files are stored before the model is known
		return files
	}
	paths := map[string]string{
		"zip in a directory": zipDump(t, demoDump, "demo/"),
		"zip at the top":     zipDump(t, demoDump, ""),
		"tar":                tarDump(t, demoDump, "demo/", false, nil),
		"tar.gz, model last": tarDump(t, demoDump, "dump/", true, reversed),
		"tar.gz at the top":  tarDump(t, demoDump, "", true, nil),
	}
	if _, err := exec.LookPath("bzip2"); err == nil {
		p := tarDump(t, demoDump, "demo/", false, nil)
		if out, err := exec.Command("bzip2", p).CombinedOutput(); err != nil {
			t.Fatalf("bzip2: %v %s", err, out)
		}
		paths["tar.bz2"] = p + ".bz2"
	}
	for name, p := range paths {
		res := e.imp(t, p, Options{Drop: true})
		if len(res.Report.Errors()) > 0 {
			t.Errorf("%s:\n%s", name, issues(res.Report))
		}
		checkCounts(t, e.counts(t), demoCounts)
	}
	if n := e.checkDigests(t, demoDump); n != 0 {
		t.Errorf("%d tombstones", n)
	}

	// An extracted archive: the directory holds the dump directory.
	parent := t.TempDir()
	if err := os.Rename(copyDir(t, demoDump), filepath.Join(parent, "demo")); err != nil {
		t.Fatal(err)
	}
	e.imp(t, parent, Options{Drop: true})
	checkCounts(t, e.counts(t), demoCounts)

	// Not dumps.
	for _, p := range []string{t.TempDir(), filepath.Join(demoDump, modelName)} {
		if _, err := Import(context.Background(), e.w, p, Options{}); !errors.Is(err, ErrNotDump) {
			t.Errorf("%s: %v", p, err)
		}
	}
	if _, err := Import(context.Background(), e.w, filepath.Join(t.TempDir(), "missing"), Options{}); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing: %v", err)
	}
}

// fullCounts are the rows of testdata/full.
var fullCounts = map[string]int{
	"admins": 2, "contests": 1, "groups": 2, "announcements": 2, "users": 3, "teams": 2, "participations": 3,
	"messages": 1, "questions": 2, "tasks": 9, "statements": 9, "attachments": 3, "datasets": 11, "managers": 12,
	"testcases": 38, "submissions": 3, "files": 3, "tokens": 1, "submission_results": 2, "executables": 1,
	"evaluations": 4, "user_tests": 1, "user_test_files": 1, "user_test_managers": 1, "user_test_results": 1,
	"user_test_executables": 1, "task_scores": 3,
}

func TestImportFull(t *testing.T) {
	for _, p := range []string{fullDump, fullTGZ} {
		t.Run(filepath.Base(p), func(t *testing.T) {
			e := newEnv(t)
			ctx := context.Background()
			res := e.imp(t, p, Options{})
			checkCounts(t, e.counts(t), fullCounts)
			text := issues(res.Report)
			if len(res.Report.Errors()) > 0 || strings.Count(text, "the task is in no contest") != 6 ||
				!strings.Contains(text, "Task 9 (tpscomm): the task is in no contest, and cmsDumpExporter exports only the files "+
					"of the contests it exports: 9 files of the task, its submissions and user tests are imported as deleted "+
					"(tombstones); import the task again from its package (import-task -u)") ||
				!strings.Contains(text, "archived sandboxes of the results are not in the dump (CMS does not export them): 1 reference dropped") {
				t.Errorf("issues:\n%s", text)
			}

			// Only the files of the tasks in no contest are tombstones.
			if n := e.checkDigests(t, fullDump); n == 0 {
				t.Error("no tombstones")
			}
			if n := e.count(t, `SELECT count(*) FROM testcases x JOIN datasets d ON d.id = x.dataset_id
				JOIN tasks t ON t.id = d.task_id WHERE (x.input = 'x' OR x.output = 'x') AND t.contest_id IS NOT NULL`); n != 0 {
				t.Errorf("%d tombstones in tasks of the contest", n)
			}

			// Admins are created disabled, with their permissions.
			if n := e.count(t, `SELECT count(*) FROM admins WHERE NOT enabled AND (username <> 'boss' OR permission_all)`); n != 2 {
				t.Errorf("%d admins disabled", n)
			}
			// People.
			var (
				ip          []string
				team, group string
				delay, xtra time.Duration
				pw          string
				starting    time.Time
				hidden      bool
				perUser     time.Duration
			)
			if err := e.pool.QueryRow(ctx, `SELECT p.ip::text[], t.code, p.delay_time, p.extra_time, p.password, p.starting_time
				FROM participations p JOIN users u ON u.id = p.user_id JOIN teams t ON t.id = p.team_id WHERE u.username = 'alice'`).
				Scan(&ip, &team, &delay, &xtra, &pw, &starting); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(ip, []string{"10.0.0.0/8", "192.168.1.7/32"}) || team != "ITA" || delay != time.Minute ||
				xtra != 120500*time.Millisecond || pw != "plaintext:pp" || !starting.Equal(time.Date(2026, 1, 1, 10, 0, 0, 123456000, time.UTC)) {
				t.Errorf("alice: %v %s %v %v %s %v", ip, team, delay, xtra, pw, starting)
			}
			if err := e.pool.QueryRow(ctx, `SELECT g.name, p.hidden, g.per_user_time FROM participations p JOIN users u ON u.id = p.user_id
				JOIN groups g ON g.id = p.group_id WHERE u.username = 'carol'`).Scan(&group, &hidden, &perUser); err != nil {
				t.Fatal(err)
			}
			if group != "late" || !hidden || perUser != 3*time.Hour {
				t.Errorf("carol: %s %v %v", group, hidden, perUser)
			}
			// Communication, with their authors.
			if n := e.count(t, `SELECT count(*) FROM messages m JOIN admins a ON a.id = m.admin_id WHERE a.username = 'boss'`); n != 1 {
				t.Errorf("message of boss: %d", n)
			}
			if n := e.count(t, `SELECT count(*) FROM announcements WHERE admin_id IS NULL AND subject = 'Anonymous'`); n != 1 {
				t.Errorf("anonymous announcement: %d", n)
			}
			if n := e.count(t, `SELECT count(*) FROM questions q JOIN admins a ON a.id = q.admin_id
				WHERE a.username = 'admin' AND q.reply_text = 'Read the statement' AND q.reply_timestamp IS NOT NULL`); n != 1 {
				t.Errorf("answered question: %d", n)
			}
			// Results: the archived sandbox is dropped, the executable re-hashed.
			var paths, digests []string
			var exe string
			if err := e.pool.QueryRow(ctx, `SELECT r.compilation_sandbox_paths, r.compilation_sandbox_digests, e.digest
				FROM submission_results r JOIN executables e USING (submission_id, dataset_id)`).Scan(&paths, &digests, &exe); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(paths, []string{"/tmp/box1", "/tmp/box2"}) || digests == nil || len(digests) != 0 ||
				exe != sha256Hex("\x7fELF fake executable of alice") {
				t.Errorf("result: %v %v %s", paths, digests, exe)
			}
			if n := e.count(t, `SELECT count(*) FROM evaluations WHERE outcome = '1.0' AND text = '{"Output is correct"}'`); n != 4 {
				t.Errorf("evaluations: %d", n)
			}
			var output string
			var etext []string
			if err := e.pool.QueryRow(ctx, `SELECT output, evaluation_text FROM user_test_results`).Scan(&output, &etext); err != nil {
				t.Fatal(err)
			}
			if output != sha256Hex("3") || !slices.Equal(etext, []string{"Execution completed successfully"}) {
				t.Errorf("user test result: %s %v", output, etext)
			}
			if n := e.count(t, `SELECT count(*) FROM submissions WHERE opaque_id = 77 AND language IS NULL AND NOT official AND comment = 'by hand'`); n != 1 {
				t.Errorf("output-only submission: %d", n)
			}
			// The tokened submission of alice counts.
			if n := e.count(t, `SELECT count(*) FROM task_scores s JOIN tasks t ON t.id = s.task_id
				WHERE t.name = 'sum' AND s.score = 100 AND s.tokened_score = 100 AND NOT s.partial`); n != 1 {
				t.Errorf("task score of alice: %d", n)
			}
		})
	}
}

// TestImportPartialExport: a dump written with cmsDumpExporter -c has the
// other contests of the contestants of the contests given, but not their
// files. cmsDumpImporter of CMS imports it; so does Import, with the
// missing files as tombstones.
func TestImportPartialExport(t *testing.T) {
	cases := []struct {
		name               string
		opts               Options
		missing, submitted int
	}{
		{"all", Options{}, 11, 3},
		{"no results", Options{SkipGenerated: true}, 11, 3},
		{"no submissions", Options{SkipSubmissions: true}, 10, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			ctx := context.Background()
			res := e.imp(t, multiDump, c.opts)
			text := issues(res.Report)
			if len(res.Report.Errors()) > 0 || strings.Count(text, "imported as deleted (tombstones)") != 1 ||
				!strings.Contains(text, fmt.Sprintf(`Task 38 (polyaplusb): the task is in the contest "other", which is in `+
					"the dump only because its contestants also took part in an exported contest (the dump was written with "+
					"cmsDumpExporter -c), and cmsDumpExporter exports only the files of the contests it exports: %d files "+
					"of the task, its submissions and user tests are imported as deleted (tombstones); export that contest "+
					"too (add its id to -c) to keep them", c.missing)) {
				t.Errorf("issues:\n%s", text)
			}
			got := e.counts(t)
			for table, n := range map[string]int{"contests": 2, "groups": 2, "tasks": 4, "users": 3, "participations": 4,
				"submissions": c.submitted, "testcases": 14, "managers": 2} {
				if got[table] != n {
					t.Errorf("%s: %d rows, want %d", table, got[table], n)
				}
			}
			// The contest of the dump is demo, imported first; "other" has
			// its task and the participation of alice.
			var demo, other int32
			if err := e.pool.QueryRow(ctx, `SELECT (SELECT id FROM contests WHERE name = 'demo'),
				(SELECT id FROM contests WHERE name = 'other')`).Scan(&demo, &other); err != nil {
				t.Fatal(err)
			}
			if demo >= other || res.ContestID != demo || len(res.Events) != 2 {
				t.Errorf("contests demo %d, other %d; result %+v", demo, other, res)
			}
			if n := e.count(t, `SELECT count(*) FROM tasks t JOIN participations p USING (contest_id) JOIN users u ON u.id = p.user_id
				WHERE t.name = 'polyaplusb' AND t.contest_id = $1 AND u.username = 'alice'`, other); n != 1 {
				t.Errorf("polyaplusb in other with alice: %d", n)
			}
			// Only the files of polyaplusb that the dump does not have are
			// tombstones (some of its testcases are also testcases of demo).
			if n := e.checkDigests(t, multiDump); n != c.missing {
				t.Errorf("%d tombstones, want %d", n, c.missing)
			}
			if n := e.count(t, `SELECT count(*) FROM testcases x JOIN datasets d ON d.id = x.dataset_id
				JOIN tasks t ON t.id = d.task_id WHERE (x.input = 'x' OR x.output = 'x') AND t.contest_id = $1`, demo); n != 0 {
				t.Errorf("%d tombstones in the tasks of demo", n)
			}
		})
	}

	// Without the users (-X) nothing reaches the other contest:
	// cmsDumpImporter of CMS leaves it out, with its task.
	e := newEnv(t)
	res := e.imp(t, multiDump, Options{SkipUsers: true})
	if text := issues(res.Report); len(res.Report.Errors()) > 0 || strings.Contains(text, "tombstones") ||
		!strings.Contains(text, `warning: Contest 35 (other): left out with its tasks: the dump has the contest only because`) {
		t.Errorf("issues:\n%s", text)
	}
	got := e.counts(t)
	for table, n := range map[string]int{"contests": 1, "groups": 1, "tasks": 3, "users": 0, "testcases": 9, "managers": 1} {
		if got[table] != n {
			t.Errorf("-X: %s: %d rows, want %d", table, got[table], n)
		}
	}
	if n := e.checkDigests(t, multiDump); n != 0 {
		t.Errorf("-X: %d tombstones", n)
	}

	// The roots of a dump not written by cmsDumpExporter name no contest:
	// the files of every contest are required.
	dir := copyDir(t, multiDump)
	data, err := os.ReadFile(filepath.Join(dir, modelName))
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), `"_objects": [
        "0"
    ],`, `"_objects": [],`, 1))
	if err := os.WriteFile(filepath.Join(dir, modelName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	e = newEnv(t)
	res, err = Import(context.Background(), e.w, dir, Options{})
	if !errors.As(err, new(*importer.ValidationError)) ||
		!strings.Contains(issues(res.Report), "Manager 43 (checker): digest: the file 24d7b3c9d963f854329cc1ec7465b2ff75ecac0e is not in the dump") {
		t.Errorf("%v\n%s", err, issues(res.Report))
	}
}

func TestImportDryRun(t *testing.T) {
	e := newEnv(t)
	e.w.DryRun = true
	dry := e.imp(t, fullDump, Options{})
	checkCounts(t, e.counts(t), map[string]int{})
	if n := e.blobCount(t); n != 0 {
		t.Errorf("a dry run stored %d blobs", n)
	}
	if len(dry.Events) != 0 || len(dry.Changes) == 0 {
		t.Errorf("dry run: %+v", dry)
	}
	// The dry run said exactly what the import does, ids included: it did
	// not move the identity sequences.
	e.w.DryRun = false
	res := e.imp(t, fullDump, Options{})
	if !slices.Equal(dry.Changes, res.Changes) {
		t.Errorf("dry run:\n%v\nimport:\n%v", dry.Changes, res.Changes)
	}
	checkCounts(t, e.counts(t), fullCounts)

	// With --drop the tables are emptied in the transaction, but the
	// sequences go on: the dry run still gives the ids of the import.
	e.w.DryRun = true
	dry = e.imp(t, fullDump, Options{Drop: true})
	checkCounts(t, e.counts(t), fullCounts)
	e.w.DryRun = false
	res = e.imp(t, fullDump, Options{Drop: true})
	if !slices.Equal(dry.Changes, res.Changes) || res.Changes[0].Action != importer.Deleted {
		t.Errorf("dry run with --drop:\n%v\nimport:\n%v", dry.Changes, res.Changes)
	}
	checkCounts(t, e.counts(t), fullCounts)
}

// TestImportDryRunDropUnlocked: a dry run with --drop does not empty the
// tables of the installation (TRUNCATE would wait for every transaction
// that uses them and, until the dry run ends, block every reader: the
// contestants' pages). It runs while another transaction reads them, and
// still says exactly what the import does.
func TestImportDryRunDropUnlocked(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.imp(t, demoDump, Options{})

	// A transaction that read the users and is still open.
	reader, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Rollback(ctx)
	if _, err := reader.Exec(ctx, `SELECT count(*) FROM users`); err != nil {
		t.Fatal(err)
	}
	e.w.DryRun = true
	tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	dry, err := Import(tctx, e.w, fullDump, Options{Drop: true})
	if err != nil {
		t.Fatalf("dry run with --drop while a transaction reads the users: %v\n%s", err, issues(dry.Report))
	}
	if err := reader.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	checkCounts(t, e.counts(t), demoCounts)
	if c := dry.Changes[0]; c.Action != importer.Deleted || c.Details != "1 contest, 3 tasks, 3 users, 0 teams, 2 submissions" {
		t.Errorf("drop: %s", c)
	}
	// The users of the dump are the users of the database: with --drop
	// they are created again, not a conflict.
	if dry.Count("user", importer.Created) != 3 || dry.Count("contest", importer.Created) != 1 {
		t.Errorf("dry run: %v", dry.Changes)
	}

	e.w.DryRun = false
	res := e.imp(t, fullDump, Options{Drop: true})
	if !slices.Equal(dry.Changes, res.Changes) {
		t.Errorf("dry run with --drop:\n%v\nimport:\n%v", dry.Changes, res.Changes)
	}
	checkCounts(t, e.counts(t), fullCounts)
}

func TestImportConflicts(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	first := e.imp(t, demoDump, Options{})
	_, err := Import(ctx, e.w, demoDump, Options{})
	var ce *ConflictError
	if !errors.As(err, &ce) || !slices.Equal(ce.Contests, []string{"demo"}) || !slices.Equal(ce.Tasks, []string{"half", "quiz", "sum"}) ||
		!slices.Equal(ce.Users, []string{"alice", "bob", "carol"}) || !strings.Contains(err.Error(), "--drop") {
		t.Fatalf("second import: %v", err)
	}
	checkCounts(t, e.counts(t), demoCounts)

	// --drop replaces everything; the audit log and the admins stay.
	if _, err := e.pool.Exec(ctx, `INSERT INTO admins (name, username, authentication) VALUES ('Root', 'root', 'plaintext:r');
		INSERT INTO audit_log (action, target_type, contest_id) SELECT 'contest.update', 'contest', id FROM contests`); err != nil {
		t.Fatal(err)
	}
	res := e.imp(t, demoDump, Options{Drop: true})
	checkCounts(t, e.counts(t), map[string]int{"admins": 1, "contests": 1, "groups": 1, "users": 3, "participations": 3,
		"tasks": 3, "statements": 3, "attachments": 2, "datasets": 3, "managers": 1, "testcases": 9, "submissions": 2,
		"files": 2, "submission_results": 1, "task_scores": 2})
	if c := res.Changes[0]; c.Action != importer.Deleted || c.Details != "1 contest, 3 tasks, 3 users, 0 teams, 2 submissions" {
		t.Errorf("drop: %s", c)
	}
	if res.ContestID == first.ContestID {
		t.Errorf("the contest got its old id %d back", res.ContestID)
	}
	if n := e.count(t, `SELECT count(*) FROM audit_log WHERE contest_id IS NULL`); n != 1 {
		t.Errorf("audit log: %d rows", n)
	}

	// --reuse-users: a second contest with the same contestants.
	if _, err := e.pool.Exec(ctx, `UPDATE contests SET name = 'old'; UPDATE tasks SET name = name || '_old'`); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(ctx, e.w, demoDump, Options{}); !errors.As(err, &ce) || len(ce.Contests) != 0 || len(ce.Users) != 3 ||
		!strings.Contains(err.Error(), "--reuse-users") {
		t.Errorf("users only: %v", err)
	}
	res = e.imp(t, demoDump, Options{ReuseUsers: true})
	if res.Count("user", importer.Unchanged) != 3 || res.Count("user", importer.Created) != 0 {
		t.Errorf("reuse: %v", res.Changes)
	}
	if n := e.count(t, `SELECT count(*) FROM users`); n != 3 {
		t.Errorf("%d users", n)
	}
	if n := e.count(t, `SELECT count(DISTINCT user_id) FROM participations`); n != 3 {
		t.Errorf("participations of %d users", n)
	}
	if n := e.count(t, `SELECT count(*) FROM participations`); n != 6 {
		t.Errorf("%d participations", n)
	}
}

func TestImportExistingAdmin(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	var id int32
	if err := e.pool.QueryRow(ctx, `INSERT INTO admins (name, username, authentication, permission_all)
		VALUES ('Me', 'admin', 'plaintext:me', true) RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	res := e.imp(t, fullDump, Options{})
	if res.Count("admin", importer.Unchanged) != 1 || res.Count("admin", importer.Created) != 1 {
		t.Errorf("admins: %v", res.Changes)
	}
	if n := e.count(t, `SELECT count(*) FROM admins WHERE id = $1 AND enabled AND name = 'Me'`, id); n != 1 {
		t.Error("the existing admin changed")
	}
	if n := e.count(t, `SELECT count(*) FROM announcements WHERE admin_id = $1`, id); n != 1 {
		t.Errorf("announcements of the existing admin: %d", n)
	}
}

func TestImportCorrupted(t *testing.T) {
	dir := copyDir(t, demoDump)
	victim := filepath.Join(dir, "files", "32d3c3db7c8157c3bb576fa546eb6e41322fb89e") // input 000 of sum
	if err := os.WriteFile(victim, []byte("1 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{dir, tarDump(t, dir, "demo/", true, nil)} {
		e := newEnv(t)
		res, err := Import(context.Background(), e.w, p, Options{})
		var ve *importer.ValidationError
		if !errors.As(err, &ve) || len(res.Report.Errors()) != 1 ||
			!strings.Contains(err.Error(), "files/32d3c3db7c8157c3bb576fa546eb6e41322fb89e: the content of the file does not match its name") {
			t.Errorf("%s: %v\n%s", p, err, issues(res.Report))
		}
		checkCounts(t, e.counts(t), map[string]int{})
	}
}

func TestImportMissingFiles(t *testing.T) {
	// A file the contest needs.
	dir := copyDir(t, demoDump)
	if err := os.Remove(filepath.Join(dir, "files", "a4685dd1a208792fe9d51ac4a08732495cd09dbb")); err != nil {
		t.Fatal(err)
	}
	e := newEnv(t)
	res, err := Import(context.Background(), e.w, dir, Options{})
	if !errors.As(err, new(*importer.ValidationError)) ||
		!strings.Contains(issues(res.Report), "Testcase 23 (002): output: the file a4685dd1a208792fe9d51ac4a08732495cd09dbb is not in the dump") {
		t.Errorf("%v\n%s", err, issues(res.Report))
	}
	if n := e.blobCount(t); n != 0 {
		t.Errorf("%d blobs stored for a dump that cannot be imported", n)
	}

	// An executable: it is compiled again.
	dir = copyDir(t, fullDump)
	if err := os.Remove(filepath.Join(dir, "files", "52cb8287f6c1ed818d130fbb1621c602bfaaae35")); err != nil {
		t.Fatal(err)
	}
	res = e.imp(t, dir, Options{})
	if !strings.Contains(issues(res.Report), "1 executable or user test output are not in the dump: they are imported as deleted") {
		t.Errorf("issues:\n%s", issues(res.Report))
	}
	if n := e.count(t, `SELECT count(*) FROM executables WHERE digest = 'x'`); n != 1 {
		t.Errorf("%d tombstones", n)
	}
	// A file nothing uses.
	dir = copyDir(t, demoDump)
	if err := os.WriteFile(filepath.Join(dir, "files", strings.Repeat("ab", 20)), []byte("extra"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "files", "README"), []byte("extra"), 0o644); err != nil {
		t.Fatal(err)
	}
	res = e.imp(t, dir, Options{Drop: true})
	if text := issues(res.Report); !strings.Contains(text, "1 file of the dump are not used by its objects") ||
		!strings.Contains(text, "1 file not named by a SHA-1 digest ignored") {
		t.Errorf("issues:\n%s", text)
	}
}

// TestImportWithoutFiles: a dump exported with cmsDumpExporter -F has
// contest.json only. Its files cannot be stored under their SHA-256
// without their content: one error says so.
func TestImportWithoutFiles(t *testing.T) {
	light := copyDir(t, multiDump)
	for _, sub := range []string{"files", "descriptions"} {
		if err := os.RemoveAll(filepath.Join(light, sub)); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(light, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	noDir := copyDir(t, light)
	if err := os.Remove(filepath.Join(noDir, "files")); err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]string{"empty files/": light, "no files/": noDir, "tar.gz": tarDump(t, light, "exp/", true, nil)} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			res, err := Import(context.Background(), e.w, p, Options{})
			var ve *importer.ValidationError
			if !errors.As(err, &ve) || len(res.Report.Errors()) != 1 || !strings.Contains(issues(res.Report),
				"error: contest.json: the dump has none of the 34 files its objects need: it was exported without files "+
					"(cmsDumpExporter -F), and such a dump cannot be imported: each file is stored under its SHA-256, "+
					"which only its content gives (a dump names its files by their SHA-1); export it again without -F") {
				t.Errorf("%v\n%s", err, issues(res.Report))
			}
			checkCounts(t, e.counts(t), map[string]int{})
		})
	}
}

func TestImportSkip(t *testing.T) {
	cases := []struct {
		opts Options
		want map[string]int
	}{
		{Options{SkipGenerated: true}, map[string]int{"submission_results": 0, "executables": 0, "evaluations": 0,
			"user_test_results": 0, "user_test_executables": 0, "submissions": 3, "user_tests": 1}},
		{Options{SkipSubmissions: true}, map[string]int{"submissions": 0, "files": 0, "tokens": 0, "submission_results": 0,
			"evaluations": 0, "executables": 0, "task_scores": 0, "user_tests": 1, "user_test_results": 1}},
		{Options{SkipUserTests: true}, map[string]int{"user_tests": 0, "user_test_files": 0, "user_test_results": 0,
			"user_test_executables": 0, "submissions": 3}},
		{Options{SkipUsers: true}, map[string]int{"users": 0, "teams": 0, "participations": 0, "announcements": 0,
			"messages": 0, "questions": 0, "admins": 0, "submissions": 0, "user_tests": 0, "task_scores": 0,
			"contests": 1, "groups": 2, "tasks": 9}},
	}
	for i, c := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			e := newEnv(t)
			res := e.imp(t, fullDump, c.opts)
			got := e.counts(t)
			for table, n := range c.want {
				if got[table] != n {
					t.Errorf("%s: %d rows, want %d", table, got[table], n)
				}
			}
			if len(res.Report.Errors()) > 0 {
				t.Errorf("issues:\n%s", issues(res.Report))
			}
			e.checkDigests(t, fullDump)
		})
	}
}

// v49 builds a small dump of the current version: a contest with a task
// and a participant.
func v49(t *testing.T) *builder {
	b := newBuilder(t, Version)
	b.roots = []string{"0"}
	b.add("0", map[string]any{"_class": "Contest", "name": "c", "description": "C", "languages": []string{"C++17 / g++"},
		"main_group": "1"})
	b.add("1", map[string]any{"_class": "Group", "name": "main", "contest": "0"})
	b.add("2", map[string]any{"_class": "Task", "name": "t", "title": "T", "contest": "0", "num": 0,
		"submission_format": []string{"t.%l"}, "active_dataset": "3"})
	b.add("3", map[string]any{"_class": "Dataset", "task": "2", "description": "D", "time_limit": 1, "memory_limit": 1 << 28,
		"task_type": "Batch", "task_type_parameters": []any{"alone", []string{"", ""}, "diff"},
		"score_type": "Sum", "score_type_parameters": 100})
	b.add("4", map[string]any{"_class": "Testcase", "dataset": "3", "codename": "1", "input": b.file("1\n"), "output": b.file("2\n")})
	b.add("5", map[string]any{"_class": "User", "username": "u", "first_name": "U", "last_name": "V", "password": "plaintext:p"})
	b.add("6", map[string]any{"_class": "Participation", "contest": "0", "user": "5", "group": "1"})
	b.add("7", map[string]any{"_class": "Submission", "participation": "6", "task": "2", "timestamp": 1e9,
		"language": "C++17 / g++", "opaque_id": 1})
	b.add("8", map[string]any{"_class": "File", "submission": "7", "filename": "t.%l", "digest": b.file("int main() {}\n")})
	return b
}

func TestImportInvalid(t *testing.T) {
	cases := []struct {
		name  string
		edit  func(b *builder)
		wants []string
	}{
		{"valid", func(*builder) {}, nil},
		{"task type", func(b *builder) { b.objs["3"].(map[string]any)["task_type"] = "Batchy" },
			[]string{`task t: dataset "D": unknown task type "Batchy"`}},
		{"task type parameters", func(b *builder) { b.objs["3"].(map[string]any)["task_type_parameters"] = []any{"alone"} },
			[]string{`invalid parameters ["alone"] for task type Batch`}},
		{"score type", func(b *builder) { b.objs["3"].(map[string]any)["score_type_parameters"] = []any{[]any{50, "("}} },
			[]string{"invalid parameters"}},
		{"submission language", func(b *builder) { b.objs["7"].(map[string]any)["language"] = "Brainfuck" },
			[]string{`unknown language "Brainfuck", used by 1 submission or user test`}},
		{"contest language", func(b *builder) { b.objs["0"].(map[string]any)["languages"] = []string{"Brainfuck"} },
			[]string{`unknown language "Brainfuck"`}},
		{"dangling reference", func(b *builder) { b.objs["4"].(map[string]any)["dataset"] = "99" },
			[]string{"Testcase 4 (1): dataset: object 99 is not in the dump"}},
		{"wrong class", func(b *builder) { b.objs["4"].(map[string]any)["dataset"] = "2" },
			[]string{"Testcase 4 (1): dataset: object 2 is a Task, not a Dataset"}},
		{"group of another contest", func(b *builder) {
			b.add("9", map[string]any{"_class": "Contest", "name": "d", "description": "D", "main_group": "10"})
			b.add("10", map[string]any{"_class": "Group", "name": "main", "contest": "9"})
			b.objs["6"].(map[string]any)["group"] = "10"
		}, []string{"group: group 10 belongs to another contest"}},
		{"duplicate task", func(b *builder) {
			b.add("9", map[string]any{"_class": "Task", "name": "t", "title": "T2"})
		}, []string{`task "t" is also object 2`}},
		{"bad digest", func(b *builder) { b.objs["8"].(map[string]any)["digest"] = "nope" },
			[]string{`File 8 (t.%l): digest: invalid digest "nope" (not a SHA-1)`}},
		{"bad time", func(b *builder) { b.objs["7"].(map[string]any)["timestamp"] = "yesterday" },
			[]string{`Submission 7: timestamp: "yesterday" is not a number`}},
		{"bad flag", func(b *builder) { b.objs["7"].(map[string]any)["official"] = "yes" },
			[]string{"object 7: field official: a JSON string where a boolean was expected"}},
		{"missing name", func(b *builder) { delete(b.objs["2"].(map[string]any), "name") },
			[]string{"Task 2: no name"}},
		{"bad outcome", func(b *builder) {
			b.add("9", map[string]any{"_class": "SubmissionResult", "submission": "7", "dataset": "3", "compilation_outcome": "maybe"})
		}, []string{`invalid compilation_outcome "maybe"`}},
		{"bad file name", func(b *builder) { b.objs["8"].(map[string]any)["filename"] = "../t.%l" },
			[]string{`invalid file name "../t.%l"`}},
		{"duplicate opaque id", func(b *builder) {
			b.add("9", map[string]any{"_class": "Submission", "participation": "6", "task": "2", "timestamp": 2e9, "opaque_id": 1})
		}, []string{"opaque_id 1 is used twice by participation 6"}},
		{"bad network", func(b *builder) { b.objs["6"].(map[string]any)["ip"] = []string{"10.0.0.1/8"} },
			[]string{`ip: invalid network "10.0.0.1/8"`}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := v49(t)
			c.edit(b)
			e := newEnv(t)
			res, err := Import(context.Background(), e.w, b.write(), Options{})
			text := issues(res.Report)
			if c.wants == nil {
				if err != nil {
					t.Fatalf("%v\n%s", err, text)
				}
				return
			}
			if err == nil {
				t.Fatalf("no error\n%s", text)
			}
			for _, want := range c.wants {
				if !strings.Contains(text+err.Error(), want) {
					t.Errorf("no %q in\n%s%v", want, text, err)
				}
			}
			checkCounts(t, e.counts(t), map[string]int{})
			if n := e.blobCount(t); n != 0 {
				t.Errorf("%d blobs stored", n)
			}
		})
	}
}

// big returns a dump with n participants who send m submissions each to
// a task of k testcases, all scored with their evaluations.
func big(t *testing.T, n, m, k int) string {
	b := v49(t)
	delete(b.objs, "7")
	delete(b.objs, "8")
	delete(b.objs, "4")
	for i := range k {
		b.add(fmt.Sprintf("tc%d", i), map[string]any{"_class": "Testcase", "dataset": "3", "codename": fmt.Sprintf("%03d", i),
			"input": b.file(fmt.Sprintf("in %d\n", i)), "output": b.file(fmt.Sprintf("out %d\n", i))})
	}
	src := b.file("int main() {}\n")
	for p := range n {
		pk, uk := fmt.Sprintf("p%d", p), fmt.Sprintf("u%d", p)
		b.add(uk, map[string]any{"_class": "User", "username": uk, "first_name": "U", "last_name": "V", "password": "plaintext:p"})
		b.add(pk, map[string]any{"_class": "Participation", "contest": "0", "user": uk, "group": "1"})
		for s := range m {
			sk := fmt.Sprintf("s%d_%d", p, s)
			b.add(sk, map[string]any{"_class": "Submission", "participation": pk, "task": "2", "timestamp": 1e9 + p*100 + s,
				"language": "C++17 / g++", "opaque_id": s})
			b.add(sk+"f", map[string]any{"_class": "File", "submission": sk, "filename": "t.%l", "digest": src})
			b.add(sk+"r", map[string]any{"_class": "SubmissionResult", "submission": sk, "dataset": "3",
				"compilation_outcome": "ok", "evaluation_outcome": "ok", "score": 50, "score_details": []any{},
				"public_score": 0, "public_score_details": []any{}, "ranking_score_details": []string{"50"}, "scored_at": 1e9})
			for i := range k {
				b.add(fmt.Sprintf("%se%d", sk, i), map[string]any{"_class": "Evaluation", "submission_result": sk + "r",
					"testcase": fmt.Sprintf("tc%d", i), "outcome": "0.5", "text": []string{"Partially correct"}})
			}
		}
	}
	return b.write()
}

// TestImportBounded: the number of statements does not depend on the size
// of the dump.
func TestImportBounded(t *testing.T) {
	statements := func(p string) (int64, map[string]int) {
		e := newEnv(t)
		e.qc.Reset()
		e.imp(t, p, Options{})
		return e.qc.Count(), e.counts(t)
	}
	small, sc := statements(big(t, 2, 1, 2))
	large, lc := statements(big(t, 40, 5, 20))
	if lc["evaluations"] != 40*5*20 || lc["submissions"] != 200 || lc["task_scores"] != 40 || sc["evaluations"] != 4 {
		t.Fatalf("counts %v %v", sc, lc)
	}
	if small != large {
		t.Errorf("%d statements for a small dump, %d for a large one", small, large)
	}
	t.Logf("%d statements", large)
}

func TestImportRoots(t *testing.T) {
	// Contests get their ids in the order of _objects.
	b := v49(t)
	b.add("9", map[string]any{"_class": "Contest", "name": "first", "description": "F", "main_group": "10"})
	b.add("10", map[string]any{"_class": "Group", "name": "main", "contest": "9"})
	b.roots = []string{"9", "0", "42"}
	e := newEnv(t)
	res := e.imp(t, b.write(), Options{})
	var first, second int32
	if err := e.pool.QueryRow(context.Background(), `SELECT (SELECT id FROM contests WHERE name = 'first'),
		(SELECT id FROM contests WHERE name = 'c')`).Scan(&first, &second); err != nil {
		t.Fatal(err)
	}
	if first >= second || res.ContestID != 0 || len(res.Events) != 2 {
		t.Errorf("ids %d %d, result %+v", first, second, res)
	}
	if !strings.Contains(issues(res.Report), "_objects names object 42, which is not in the dump") {
		t.Errorf("issues:\n%s", issues(res.Report))
	}
}

func TestImportUnknownClass(t *testing.T) {
	b := v49(t)
	b.add("9", map[string]any{"_class": "PrintJob", "participation": "6"})
	b.add("10", map[string]any{"_class": "PrintJob", "participation": "6"})
	e := newEnv(t)
	res := e.imp(t, b.write(), Options{})
	if !strings.Contains(issues(res.Report), "2 objects of the unknown class PrintJob ignored") {
		t.Errorf("issues:\n%s", issues(res.Report))
	}
}

func TestReadRaw(t *testing.T) {
	for in, want := range map[string]string{
		`[]`:                        "not a dump",
		`{"0": {"_class": "Task"`:   "unexpected EOF",
		`{"_version": "x"}`:         `_version "x" is not an integer`,
		`{"0": {}, "0": {}}`:        `duplicate key "0"`,
		`{"_version": 49} {}`:       "data after the end",
		`{"_version": 49, "_x": 1}`: "",
	} {
		_, err := readRaw(strings.NewReader(in))
		if want == "" && err != nil || want != "" && (err == nil || !strings.Contains(err.Error(), want)) {
			t.Errorf("%s: %v, want %q", in, err, want)
		}
	}
	d, err := readRaw(strings.NewReader(`{"1": {"_class": "User"}, "0": {"_class": "Contest"}, "_objects": ["0"], "_version": 49}`))
	if err != nil || d.version != 49 || !slices.Equal(d.roots, []string{"0"}) || len(d.objects) != 2 {
		t.Errorf("%+v %v", d, err)
	}
	var raw json.RawMessage = d.objects[0].raw
	if string(raw) != `{"_class": "User"}` {
		t.Errorf("raw %s", raw)
	}
}

func TestMicros(t *testing.T) {
	// datetime.fromtimestamp of Python: microseconds rounded half to even.
	for f, want := range map[float64]time.Time{
		1791365124.544329: time.Date(2026, 10, 7, 9, 25, 24, 544329000, time.UTC),
		2.5e-6:            time.Unix(0, 2000).UTC(),
		0.5e-6:            time.Unix(0, 0).UTC(),
		1.5e-6:            time.Unix(0, 2000).UTC(),
		-1.25:             time.Unix(-2, 750000000).UTC(),
		4102444800:        time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC),
	} {
		if got := at(f); !got.Equal(want) {
			t.Errorf("%v: %v, want %v", f, got, want)
		}
	}
}
