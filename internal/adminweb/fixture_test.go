// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package adminweb

// The fixture of the page tests: the demo contest imported from
// testdata/contests/demo by the italy_yaml loader, plus a team, a few
// submissions (scored, compiling, compilation failed, unofficial), a user
// test, a question, an announcement and a message.

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer/italy"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
)

// italyDemo is the italy_yaml demo contest.
const italyDemo = "../../testdata/contests/demo"

// fixture holds the ids of the seeded rows.
type fixture struct {
	Users        map[string]int32 // alice, bob, carol
	Tasks        map[string]int32 // sum, half, quiz
	Datasets     map[string]int32 // the active dataset of each task
	Part         map[string]int32 // participations by username
	Group        int32            // main group
	Team         int32
	Testcase     int32 // a testcase of the active dataset of sum
	Scored       int32 // alice on sum, scored 100
	Compiling    int32 // bob on sum, no result
	Failed       int32 // carol on half, compilation failed
	Unofficial   int32 // alice on sum, unofficial
	FileID       int32 // file of Scored
	FileDigest   string
	UserTest     int32
	UserTestFile int32
	Question     int32
	Announcement int32
	Message      int32
}

// copyDir copies a directory tree.
func copyDir(t testing.TB, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		out := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(out, data, 0o755)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// newFixtureSite builds a test site with the italy_yaml demo contest and
// the seeded rows.
func newFixtureSite(t testing.TB, o testOptions) (*testSite, *fixture) {
	t.Helper()
	o.NoImport = true
	ts := newTestSite(t, o)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "demo")
	copyDir(t, italyDemo, dir)
	// The comparator of half is built by build.sh.
	checker := filepath.Join(dir, "half", "check", "checker")
	if _, err := os.Stat(checker); err != nil {
		if err := os.WriteFile(checker, []byte("#!/bin/sh\necho 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	w := importer.NewWriter(ts.Pool, ts.Blobs, nil)
	if _, err := w.ImportContest(ctx, italy.Loader{}, dir, importer.ContestOptions{ImportTasks: true, ImportUsers: true}); err != nil {
		t.Fatalf("importing the demo contest: %v", err)
	}
	ts.Contest = int32(ts.QueryInt(`SELECT id FROM contests WHERE name = 'demo'`))
	f := &fixture{Users: map[string]int32{}, Tasks: map[string]int32{}, Datasets: map[string]int32{}, Part: map[string]int32{}}
	id := func(sql string, args ...any) int32 { return int32(ts.QueryInt(sql, args...)) }
	for _, u := range []string{"alice", "bob", "carol"} {
		f.Users[u] = id(`SELECT id FROM users WHERE username = $1`, u)
		f.Part[u] = id(`SELECT id FROM participations WHERE user_id = $1`, f.Users[u])
	}
	for _, tk := range []string{"sum", "half", "quiz"} {
		f.Tasks[tk] = id(`SELECT id FROM tasks WHERE name = $1`, tk)
		f.Datasets[tk] = id(`SELECT active_dataset_id FROM tasks WHERE name = $1`, tk)
	}
	f.Group = id(`SELECT main_group_id FROM contests WHERE id = $1`, ts.Contest)
	f.Team = id(`INSERT INTO teams (code, name) VALUES ('ita', 'Italy') RETURNING id`)
	ts.Exec(`UPDATE participations SET team_id = $1 WHERE id = $2`, f.Team, f.Part["alice"])
	f.Testcase = id(`SELECT min(id) FROM testcases WHERE dataset_id = $1`, f.Datasets["sum"])

	src := []byte("#include <cstdio>\nint main() { long a, b; scanf(\"%ld %ld\", &a, &b); printf(\"%ld\\n\", a + b); }\n")
	digest, err := ts.Blobs.PutBytes(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	f.FileDigest = digest
	sub := func(part, task int32, opaque int, official bool, minute int) int32 {
		return id(`INSERT INTO submissions (opaque_id, participation_id, task_id, timestamp, language, official)
			VALUES ($1, $2, $3, '2026-10-15 10:00:00+00'::timestamptz + make_interval(mins => $4), 'C++17 / g++', $5) RETURNING id`,
			opaque, part, task, minute, official)
	}
	file := func(s int32, name string) int32 {
		return id(`INSERT INTO files (submission_id, filename, digest) VALUES ($1, $2, $3) RETURNING id`, s, name, digest)
	}
	f.Scored = sub(f.Part["alice"], f.Tasks["sum"], 1, true, 1)
	f.FileID = file(f.Scored, "sum.%l")
	ts.Exec(`INSERT INTO submission_results (submission_id, dataset_id, compilation_outcome, compilation_text,
		compilation_tries, compilation_stdout, compilation_stderr, compilation_time, compilation_wall_clock_time,
		compilation_memory, compilation_shard, evaluation_outcome, evaluation_tries, score, score_details,
		scored_at, public_score, public_score_details, ranking_score_details)
		VALUES ($1, $2, 'ok', '{Compilation succeeded}', 1, '', '', 0.5, 0.6, 1048576, 0, 'ok', 1, 100, '[]',
		'2026-10-15 10:02:00+00', 100, '[]', '{100}')`, f.Scored, f.Datasets["sum"])
	ts.Exec(`INSERT INTO evaluations (submission_id, dataset_id, testcase_id, outcome, text, execution_time,
		execution_wall_clock_time, execution_memory, evaluation_shard)
		SELECT $1, $2, id, '1.0', '{Output is correct}', 0.01, 0.02, 1048576, 0 FROM testcases WHERE dataset_id = $2`,
		f.Scored, f.Datasets["sum"])
	f.Compiling = sub(f.Part["bob"], f.Tasks["sum"], 2, true, 2)
	file(f.Compiling, "sum.%l")
	f.Failed = sub(f.Part["carol"], f.Tasks["half"], 3, true, 3)
	file(f.Failed, "half.%l")
	ts.Exec(`INSERT INTO submission_results (submission_id, dataset_id, compilation_outcome, compilation_text,
		compilation_tries, compilation_stdout, compilation_stderr, evaluation_tries, score, score_details, scored_at,
		public_score, public_score_details, ranking_score_details)
		VALUES ($1, $2, 'fail', '{Compilation failed}', 1, '', 'error: expected ;', 0, 0, '[]', '2026-10-15 10:04:00+00',
		0, '[]', '{0}')`, f.Failed, f.Datasets["half"])
	f.Unofficial = sub(f.Part["alice"], f.Tasks["sum"], 4, false, 4)
	file(f.Unofficial, "sum.%l")

	f.UserTest = id(`INSERT INTO user_tests (participation_id, task_id, timestamp, language, input)
		VALUES ($1, $2, '2026-10-15 10:05:00+00', 'C++17 / g++', $3) RETURNING id`, f.Part["alice"], f.Tasks["sum"], digest)
	f.UserTestFile = id(`INSERT INTO user_test_files (user_test_id, filename, digest) VALUES ($1, 'sum.%l', $2) RETURNING id`,
		f.UserTest, digest)
	ts.Exec(`INSERT INTO user_test_results (user_test_id, dataset_id, output, compilation_outcome, compilation_tries,
		evaluation_outcome, evaluation_text, evaluation_tries, execution_time, execution_memory)
		VALUES ($1, $2, $3, 'ok', 1, 'ok', '{Execution completed successfully}', 1, 0.01, 1048576)`,
		f.UserTest, f.Datasets["sum"], digest)

	f.Question = id(`INSERT INTO questions (question_timestamp, subject, text, participation_id)
		VALUES ('2026-10-15 11:00:00+00', 'Input size', 'How big is N?', $1) RETURNING id`, f.Part["bob"])
	f.Announcement = id(`INSERT INTO announcements (timestamp, subject, text, contest_id)
		VALUES ('2026-10-15 09:00:00+00', 'Welcome', 'Good luck', $1) RETURNING id`, ts.Contest)
	f.Message = id(`INSERT INTO messages (timestamp, subject, text, participation_id)
		VALUES ('2026-10-15 09:30:00+00', 'Hi', 'Hello alice', $1) RETURNING id`, f.Part["alice"])

	// The task_scores cache of the rankings.
	tx, err := ts.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	for _, tk := range f.Tasks {
		if _, err := judging.RecomputeTask(ctx, tx, tk); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return ts, f
}
