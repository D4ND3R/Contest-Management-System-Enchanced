// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package ctl

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/cli"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
)

func TestJudgeCommands(t *testing.T) {
	pool := testutil.DB(t)
	redis := testutil.Redis(t)
	t.Setenv("CMS_DATABASE_URL", pool.Config().ConnString())
	t.Setenv("CMS_REDIS_URL", testutil.RedisURL(t))
	t.Setenv("CMS_REDIS_KEY_PREFIX", redis.Prefix())
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	// A contest with one judged submission.
	d := strings.Repeat("a", 64)
	exec(`INSERT INTO contests (id, name, description) VALUES (1, 'ioi', 'IOI')`)
	exec(`INSERT INTO groups (id, name, contest_id) VALUES (1, 'main', 1)`)
	exec(`INSERT INTO tasks (id, num, contest_id, name, title, submission_format) VALUES (1, 1, 1, 'sum', 'Sum', '{sum.%l}')`)
	exec(`INSERT INTO datasets (id, task_id, description, task_type, task_type_parameters, score_type, score_type_parameters)
		VALUES (1, 1, 'default', 'Batch', '["alone", ["", ""], "diff"]', 'Sum', '50')`)
	exec(`UPDATE tasks SET active_dataset_id = 1 WHERE id = 1`)
	exec(`INSERT INTO testcases (id, dataset_id, codename, public, input, output) VALUES (1, 1, 'a', true, $1, $1), (2, 1, 'b', false, $1, $1)`, d)
	exec(`INSERT INTO users (id, first_name, last_name, username, password) VALUES (1, 'A', 'L', 'alice', 'plaintext:x')`)
	exec(`INSERT INTO participations (id, contest_id, user_id, group_id) VALUES (1, 1, 1, 1)`)
	exec(`INSERT INTO submissions (id, opaque_id, participation_id, task_id, timestamp, language)
		VALUES (1, 1, 1, 1, $1, 'C++17 / g++')`, time.Now())
	exec(`INSERT INTO submission_results (submission_id, dataset_id, compilation_outcome, evaluation_outcome,
			score, score_details, public_score, public_score_details, ranking_score_details)
		VALUES (1, 1, 'ok', 'ok', 100, '[]', 50, '[]', '{}')`)
	exec(`INSERT INTO evaluations (submission_id, dataset_id, testcase_id, outcome) VALUES (1, 1, 1, '1.0'), (1, 1, 2, '1.0')`)

	run := func(args ...string) (string, string, error) {
		var out, errb bytes.Buffer
		err := Command().Execute(ctx, &cli.Env{Stdout: &out, Stderr: &errb, Prog: "cms"}, "cms ctl", args)
		return out.String(), errb.String(), err
	}

	out, _, err := run("reevaluate", "--contest", "ioi", "--task", "sum", "--dataset", "1", "--testcase", "b", "--level", "evaluation")
	if err != nil {
		t.Fatal(err)
	}
	if out != "evaluation: 1 results of 1 submissions invalidated; 1 task scores changed\nthe dispatcher was notified\n" {
		t.Errorf("output %q", out)
	}
	var evaluations, generation int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM evaluations), generation FROM submission_results`).Scan(&evaluations, &generation); err != nil {
		t.Fatal(err)
	}
	if evaluations != 1 || generation != 1 {
		t.Errorf("evaluations %d, generation %d", evaluations, generation)
	}
	msgs, err := judging.ReadEvents(ctx, redis, "test", 10, 0, false)
	if err != nil || len(msgs) != 1 || msgs[0].Event.Kind != judging.EventInvalidate ||
		!slices.Equal(msgs[0].Event.SubmissionIDs, []int32{1}) || msgs[0].Event.Level != judging.LevelEvaluation {
		t.Errorf("events %+v %v", msgs, err)
	}

	out, _, err = run("judging-status", "--contest", "1")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(out, "\n")
	if len(lines) < 3 || strings.Join(strings.Fields(lines[2]), " ") != "sum default (active) 1 0 0 1 0 0 0" {
		t.Errorf("status:\n%s", out)
	}
	if out, _, err = run("judging-status"); err != nil || !strings.Contains(out, "sum") {
		t.Errorf("status of every contest: %q %v", out, err)
	}

	// Testcase codenames are often numbers: "001" is the codename in
	// --dataset (testcase 4), not the testcase with id 1 (of dataset 1),
	// which selected nothing.
	exec(`INSERT INTO datasets (id, task_id, description, task_type, task_type_parameters, score_type, score_type_parameters)
		VALUES (2, 1, 'numbered', 'Batch', '["alone", ["", ""], "diff"]', 'Sum', '50')`)
	exec(`INSERT INTO testcases (id, dataset_id, codename, public, input, output) VALUES (3, 2, '000', true, $1, $1), (4, 2, '001', false, $1, $1)`, d)
	exec(`INSERT INTO submission_results (submission_id, dataset_id, compilation_outcome, evaluation_outcome,
			score, score_details, public_score, public_score_details, ranking_score_details)
		VALUES (1, 2, 'ok', 'ok', 100, '[]', 50, '[]', '{}')`)
	exec(`INSERT INTO evaluations (submission_id, dataset_id, testcase_id, outcome) VALUES (1, 2, 3, '1.0'), (1, 2, 4, '1.0')`)
	out, _, err = run("reevaluate", "--dataset", "2", "--testcase", "001", "--level", "evaluation")
	if err != nil || !strings.HasPrefix(out, "evaluation: 1 results of 1 submissions invalidated") {
		t.Errorf("numbered codename: %q %v", out, err)
	}
	var left string
	if err := pool.QueryRow(ctx, `SELECT string_agg(testcase_id::text, ',' ORDER BY testcase_id) FROM evaluations`).Scan(&left); err != nil || left != "1,3" {
		t.Errorf("evaluations left: %q %v (want those of testcases 1 and 3)", left, err)
	}
	// A testcase id still works, without --dataset or in it.
	if out, _, err = run("reevaluate", "--testcase", "3", "--level", "evaluation"); err != nil ||
		!strings.HasPrefix(out, "evaluation: 1 results of 1 submissions invalidated") {
		t.Errorf("testcase id: %q %v", out, err)
	}
	// A name made of digits is a name before an id: contest "1" is the
	// contest with id 2 (without submissions), not the contest with id 1.
	exec(`INSERT INTO contests (id, name, description) VALUES (2, '1', 'Digits')`)
	if out, _, err = run("reevaluate", "--contest", "1", "--level", "score"); err != nil ||
		!strings.HasPrefix(out, "score: 0 results of 0 submissions invalidated") {
		t.Errorf("contest named 1: %q %v", out, err)
	}
	// Ids are still accepted (the results are not scored any more: none
	// is invalidated).
	if out, _, err = run("reevaluate", "--contest", "ioi", "--task", "1", "--user", "1", "--dataset", "1", "--level", "score"); err != nil ||
		!strings.HasPrefix(out, "score: 0 results of 1 submissions invalidated") {
		t.Errorf("ids: %q %v", out, err)
	}

	// Rescoring a user's submissions; the dispatcher is unreachable.
	t.Setenv("CMS_REDIS_URL", "redis://127.0.0.1:1/0")
	out, stderr, err := run("reevaluate", "--user", "alice", "--level", "score")
	if err != nil || !strings.HasPrefix(out, "score: 0 results of 1 submissions invalidated") ||
		!strings.Contains(stderr, "could not notify the dispatcher") || !strings.Contains(stderr, "run this command again") {
		t.Errorf("unreachable dispatcher: %q %q %v", out, stderr, err)
	}

	for _, c := range []struct {
		args  []string
		usage bool
		msg   string
	}{
		{[]string{"reevaluate"}, true, ""},
		{[]string{"reevaluate", "--task", "sum", "--level", "all"}, true, ""},
		{[]string{"reevaluate", "--task", "sum", "--testcase", "b"}, true, ""},
		{[]string{"reevaluate", "--dataset", "1", "--testcase", "b"}, true, ""},
		{[]string{"reevaluate", "--submission", "x"}, true, ""},
		{[]string{"reevaluate", "--contest", "nope"}, false, `contest "nope" not found`},
		{[]string{"reevaluate", "--task", "nope"}, false, `task "nope" not found`},
		{[]string{"reevaluate", "--user", "nope"}, false, `user "nope" not found`},
		{[]string{"reevaluate", "--dataset", "1", "--testcase", "zz", "--level", "evaluation"}, false, `testcase "zz" not found`},
		// Ids that do not exist, or a testcase of another dataset, are
		// errors instead of selecting nothing.
		{[]string{"reevaluate", "--contest", "77"}, false, `contest "77" not found`},
		{[]string{"reevaluate", "--task", "77"}, false, `task "77" not found`},
		{[]string{"reevaluate", "--user", "77"}, false, `user "77" not found`},
		{[]string{"reevaluate", "--dataset", "77"}, false, `dataset 77 not found`},
		{[]string{"reevaluate", "--participation", "77"}, false, `participation 77 not found`},
		{[]string{"reevaluate", "--task", "sum", "--testcase", "77", "--level", "evaluation"}, false, `testcase 77 not found`},
		{[]string{"reevaluate", "--dataset", "1", "--testcase", "3", "--level", "evaluation"}, false, `testcase 3 is in dataset 2`},
		{[]string{"reevaluate", "--dataset", "1", "--testcase", "77", "--level", "evaluation"}, false, `testcase "77" not found in dataset 1`},
		{[]string{"reevaluate", "--task", "sum", "extra"}, true, ""},
		{[]string{"judging-status", "--contest", "nope"}, false, `contest "nope" not found`},
	} {
		_, _, err := run(c.args...)
		if c.usage != errors.Is(err, cli.ErrUsage) || c.msg != "" && (err == nil || !strings.Contains(err.Error(), c.msg)) {
			t.Errorf("%q: %v", c.args, err)
		}
	}
}
