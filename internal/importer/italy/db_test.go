// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package italy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
)

// sha256File returns the digest of a file of the package.
func sha256File(t testing.TB, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func testWriter(t testing.TB) (*importer.Writer, *pgxpool.Pool) {
	t.Helper()
	pool := testutil.DB(t)
	bs, err := blob.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return importer.NewWriter(pool, bs, nil), pool
}

// rows runs a query and returns each row as its columns printed with %v.
func rows(t testing.TB, pool *pgxpool.Pool, sql string, args ...any) []string {
	t.Helper()
	r, err := pool.Query(context.Background(), sql, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var out []string
	for r.Next() {
		vals, err := r.Values()
		if err != nil {
			t.Fatal(err)
		}
		parts := make([]string, len(vals))
		for i, v := range vals {
			switch v := v.(type) {
			case nil:
				parts[i] = "NULL"
			case time.Time:
				parts[i] = v.UTC().Format(time.DateTime)
			case pgtype.Interval:
				parts[i] = (time.Duration(v.Microseconds)*time.Microsecond + time.Duration(v.Days)*24*time.Hour).String()
			default:
				parts[i] = fmt.Sprint(v)
			}
		}
		out = append(out, strings.Join(parts, " | "))
	}
	if err := r.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestImportDemoContest imports the demo contest with the writer and
// compares the rows with those the original CMS stored (the digests are
// SHA-256 here); importing it again with -u -U changes nothing.
func TestImportDemoContest(t *testing.T) {
	w, pool := testWriter(t)
	ctx := context.Background()
	dir, _ := copyDemo(t)
	res, err := w.ImportContest(ctx, Loader{}, dir, importer.ContestOptions{ImportTasks: true, ImportUsers: true})
	if err != nil {
		t.Fatalf("%v\n%s", err, issues(res.Report))
	}

	check := func(what string, got, want []string) {
		t.Helper()
		if !slices.Equal(got, want) {
			t.Errorf("%s:\n got %q\nwant %q", what, got, want)
		}
	}
	check("contest", rows(t, pool, `SELECT name, description, allowed_localizations, languages, submissions_download_allowed,
		allow_questions, allow_user_tests, allow_unofficial_submission_before_analysis_mode, block_hidden_participations,
		allow_password_authentication, allow_registration, ip_restriction, ip_autologin, token_mode, token_max_number,
		token_min_interval, token_gen_initial, token_gen_number, token_gen_interval, token_gen_max, timezone,
		max_submission_number, max_user_test_number, min_submission_interval, min_submission_interval_grace_period,
		min_user_test_interval, score_precision FROM contests`), []string{
		"demo | Demo Contest | [] | [C++17 / g++ C11 / gcc Python 3 / CPython Java / JDK] | true | true | true | false | false | " +
			"true | false | true | false | finite | 10 | 0s | 2 | 1 | 30s | 5 | NULL | NULL | NULL | NULL | NULL | NULL | 0",
	})
	check("groups", rows(t, pool, `SELECT g.name, start, stop, analysis_enabled, analysis_start, analysis_stop, per_user_time,
		g.id = c.main_group_id FROM groups g JOIN contests c ON c.id = g.contest_id`), []string{
		"default | 2025-01-01 00:00:00 | 2030-01-01 00:00:00 | false | 2100-01-01 00:00:00 | 2100-01-01 00:00:00 | NULL | true",
	})
	check("tasks", rows(t, pool, `SELECT num, name, title, submission_format, primary_statements, allowed_languages, token_mode,
		token_max_number, token_min_interval, token_gen_initial, token_gen_number, token_gen_interval, token_gen_max,
		max_submission_number, feedback_level, score_precision, score_mode FROM tasks ORDER BY num`), []string{
		"0 | sum | A plus B | [sum.%l] | [it] | NULL | infinite | NULL | 0s | 2 | 2 | 30m0s | NULL | NULL | restricted | 0 | max_tokened_last",
		"1 | half | Half credit | [half.%l] | [it] | NULL | infinite | NULL | 0s | 2 | 2 | 30m0s | NULL | NULL | restricted | 0 | max_tokened_last",
		"2 | quiz | Quiz | [output_000.txt output_001.txt] | [it] | NULL | disabled | NULL | 0s | 2 | 2 | 30m0s | NULL | NULL | restricted | 0 | max_tokened_last",
	})
	type ds struct {
		task, description           string
		autojudge                   bool
		timeLimit                   *float64
		memoryLimit                 *int64
		taskType                    string
		ttParams                    json.RawMessage
		scoreType                   string
		stParams                    json.RawMessage
		active                      bool
		testcases, public, managers int
	}
	r, err := pool.Query(ctx, `SELECT t.name, d.description, d.autojudge, d.time_limit, d.memory_limit, d.task_type,
		d.task_type_parameters, d.score_type, d.score_type_parameters, t.active_dataset_id = d.id,
		(SELECT count(*) FROM testcases WHERE dataset_id = d.id), (SELECT count(*) FROM testcases WHERE dataset_id = d.id AND public),
		(SELECT count(*) FROM managers WHERE dataset_id = d.id)
		FROM datasets d JOIN tasks t ON t.id = d.task_id ORDER BY t.num`)
	if err != nil {
		t.Fatal(err)
	}
	var got []ds
	for r.Next() {
		var d ds
		if err := r.Scan(&d.task, &d.description, &d.autojudge, &d.timeLimit, &d.memoryLimit, &d.taskType, &d.ttParams,
			&d.scoreType, &d.stParams, &d.active, &d.testcases, &d.public, &d.managers); err != nil {
			t.Fatal(err)
		}
		got = append(got, d)
	}
	r.Close()
	if len(got) != len(cmsDemoTasks) {
		t.Fatalf("%d datasets", len(got))
	}
	for i, w := range cmsDemoTasks {
		d := got[i]
		public := 0
		for _, tc := range w.testcases {
			if tc.public {
				public++
			}
		}
		if d.task != w.name || d.description != "Default" || d.autojudge || !d.active ||
			!reflect.DeepEqual(d.timeLimit, w.timeLimit) || !reflect.DeepEqual(d.memoryLimit, w.memoryLimit) ||
			d.taskType != w.taskType || !sameJSON(t, d.ttParams, w.ttParams) ||
			d.scoreType != w.scoreType || !sameJSON(t, d.stParams, w.stParams) ||
			d.testcases != len(w.testcases) || d.public != public || d.managers != len(w.managers) {
			t.Errorf("dataset of %s: %+v (params %s %s)", w.name, d, d.ttParams, d.stParams)
		}
	}

	// The files: the same ones as CMS, hashed with SHA-256.
	var wantFiles []string
	for _, w := range cmsDemoTasks {
		td := filepath.Join(dir, w.name)
		for i, tc := range w.testcases {
			wantFiles = append(wantFiles, fmt.Sprintf("%s testcase %s %v %s %s", w.name, tc.codename, tc.public,
				sha256File(t, filepath.Join(td, "input", fmt.Sprintf("input%d.txt", i))),
				sha256File(t, filepath.Join(td, "output", fmt.Sprintf("output%d.txt", i)))))
		}
		wantFiles = append(wantFiles, fmt.Sprintf("%s statement it %s", w.name, sha256File(t, filepath.Join(td, "statement", "statement.pdf"))))
		if len(w.managers) > 0 {
			wantFiles = append(wantFiles, fmt.Sprintf("%s manager checker %s", w.name, sha256File(t, filepath.Join(td, "check", "checker"))))
		}
		for i := range len(w.attachments) {
			wantFiles = append(wantFiles, fmt.Sprintf("%s attachment input_%03d.txt %s", w.name, i,
				sha256File(t, filepath.Join(td, "input", fmt.Sprintf("input%d.txt", i)))))
		}
	}
	var gotFiles []string
	for _, row := range rows(t, pool, `SELECT t.name, x.kind, x.name, x.digest FROM (
			SELECT d.task_id, 1 AS o, 'testcase' AS kind, codename || ' ' || public || ' ' || input AS name, output AS digest
				FROM testcases JOIN datasets d ON d.id = dataset_id
			UNION ALL SELECT task_id, 2, 'statement', language, digest FROM statements
			UNION ALL SELECT d.task_id, 3, 'manager', filename, digest FROM managers JOIN datasets d ON d.id = dataset_id
			UNION ALL SELECT task_id, 4, 'attachment', filename, digest FROM attachments
		) x JOIN tasks t ON t.id = x.task_id ORDER BY t.num, x.o, x.name`) {
		gotFiles = append(gotFiles, strings.ReplaceAll(row, " | ", " "))
	}
	check("files", gotFiles, wantFiles)

	check("users", rows(t, pool, `SELECT username, first_name, last_name, password, email, timezone, preferred_languages
		FROM users ORDER BY username`), []string{
		"alice | Alice | Liddell | plaintext:demo | NULL | NULL | []",
		"bob | Bob | Builder | plaintext:demo | NULL | NULL | []",
		"carol | Carol | Danvers | plaintext:demo | NULL | NULL | []",
	})
	check("participations", rows(t, pool, `SELECT u.username, p.ip, p.starting_time, p.delay_time, p.extra_time, p.password,
		p.hidden, p.unrestricted, p.group_id = c.main_group_id, p.team_id FROM participations p JOIN users u ON u.id = p.user_id
		JOIN contests c ON c.id = p.contest_id ORDER BY u.username`), []string{
		"alice | NULL | NULL | 0s | 0s | plaintext:demo | false | false | true | NULL",
		"bob | NULL | NULL | 0s | 0s | plaintext:demo | false | false | true | NULL",
		"carol | NULL | NULL | 0s | 0s | plaintext:demo | false | false | true | NULL",
	})

	// Importing it again, updating everything, changes nothing.
	res, err = w.ImportContest(ctx, Loader{}, dir, importer.ContestOptions{UpdateContest: true, UpdateTasks: true})
	if err != nil {
		t.Fatalf("%v\n%s", err, issues(res.Report))
	}
	for _, c := range res.Changes {
		if c.Action != importer.Unchanged {
			t.Errorf("second import: %s", c)
		}
	}
	if n := res.Count("task", importer.Unchanged); n != 3 {
		t.Errorf("%d tasks unchanged, want 3", n)
	}
}
