// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package judging

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
)

// goldenCase is a case of the score types golden file: the output of the
// score types of CMS itself (internal/grading/scoretype/testdata).
type goldenCase struct {
	Name        string          `json:"name"`
	ScoreType   string          `json:"score_type"`
	Parameters  json.RawMessage `json:"parameters"`
	Public      map[string]bool `json:"public"`
	Precision   int             `json:"precision"`
	TimeLimit   *float64        `json:"time_limit"`
	Evaluated   *bool           `json:"evaluated"`
	Evaluations []struct {
		Codename string   `json:"codename"`
		Outcome  string   `json:"outcome"`
		Text     []string `json:"text"`
		Time     *float64 `json:"time"`
		Memory   *int64   `json:"memory"`
	} `json:"evaluations"`
	Error          string          `json:"error"`
	Score          float64         `json:"score"`
	Details        json.RawMessage `json:"details"`
	PublicScore    float64         `json:"public_score"`
	PublicDetails  json.RawMessage `json:"public_details"`
	RankingDetails []string        `json:"ranking_details"`
}

// TestScoreMatchesUpstream scores, through the database, the golden cases
// generated with the score types of CMS (Sum, GroupMin, GroupMul,
// GroupThreshold): the five stored columns must be what CMS stores.
func TestScoreMatchesUpstream(t *testing.T) {
	b, err := os.ReadFile("../grading/scoretype/testdata/upstream_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []goldenCase
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	parent := newFx(t)
	p := parent.participant("alice")
	n := 0
	for _, c := range cases {
		if c.Error != "" || c.Details == nil {
			continue
		}
		n++
		t.Run(c.Name, func(t *testing.T) {
			f := *parent
			f.t = t
			defer func() { parent.n = f.n }()
			task := f.id(`INSERT INTO tasks (num, contest_id, name, title, score_precision, submission_format)
				VALUES ($1, $2, $3, $3, $4, '{sum.%l}') RETURNING id`, 100+n, f.contest, strings.ReplaceAll(c.Name, "/", "_"), c.Precision)
			ds := f.id(`INSERT INTO datasets (task_id, description, time_limit, task_type, task_type_parameters,
					score_type, score_type_parameters)
				VALUES ($1, 'golden', $2, 'Batch', '["alone", ["", ""], "diff"]', $3, $4) RETURNING id`,
				task, c.TimeLimit, c.ScoreType, string(c.Parameters))
			f.exec(`UPDATE tasks SET active_dataset_id = $1 WHERE id = $2`, ds, task)
			for code, public := range c.Public {
				f.exec(`INSERT INTO testcases (dataset_id, codename, public, input, output) VALUES ($1, $2, $3, $4, $4)`,
					ds, code, public, digestOf(code))
			}
			s := f.submit(p, tk{task, ds}, time.Minute)
			outcome := job.CompilationOK
			if c.Evaluated != nil && !*c.Evaluated {
				outcome = job.CompilationFail
			}
			f.exec(`INSERT INTO submission_results (submission_id, dataset_id, compilation_outcome) VALUES ($1, $2, $3)`, s, ds, outcome)
			for _, e := range c.Evaluations {
				f.exec(`INSERT INTO evaluations (submission_id, dataset_id, testcase_id, outcome, text, execution_time, execution_memory)
					SELECT $1, $2, id, $4, $5, $6, $7 FROM testcases WHERE dataset_id = $2 AND codename = $3`,
					s, ds, e.Codename, e.Outcome, e.Text, e.Time, e.Memory)
			}
			// The sweep finds it (to finalize or to score) and Score does
			// the rest.
			w, err := f.j.Pending(f.ctx, Targets{SubmissionIDs: []int32{s}})
			if err != nil || len(w.ToScore) != 1 || len(w.Ops) != 0 {
				t.Fatalf("pending %+v, %v", w, err)
			}
			ch, err := f.j.Score(f.ctx, w.ToScore)
			if err != nil {
				t.Fatal(err)
			}
			if len(ch.ScoreErrors) != 0 || len(ch.Submissions) != 1 || ch.Submissions[0].Status != StatusScored &&
				ch.Submissions[0].Status != StatusCompilationFailed {
				t.Fatalf("changes %+v", ch)
			}
			var score, public float64
			var details, publicDetails string
			var ranking []string
			if err := f.pool.QueryRow(f.ctx, `SELECT score, public_score, score_details::text, public_score_details::text,
					ranking_score_details FROM submission_results WHERE submission_id = $1`, s).Scan(
				&score, &public, &details, &publicDetails, &ranking); err != nil {
				t.Fatal(err)
			}
			if score != c.Score || public != c.PublicScore {
				t.Errorf("score %v public %v, want %v %v", score, public, c.Score, c.PublicScore)
			}
			if !sameJSON(details, string(c.Details)) {
				t.Errorf("score_details\n got  %s\n want %s", details, c.Details)
			}
			if !sameJSON(publicDetails, string(c.PublicDetails)) {
				t.Errorf("public_score_details\n got  %s\n want %s", publicDetails, c.PublicDetails)
			}
			if mustJSON(ranking) != mustJSON(c.RankingDetails) && !(len(ranking) == 0 && len(c.RankingDetails) == 0) {
				t.Errorf("ranking_score_details %q, want %q", ranking, c.RankingDetails)
			}
		})
	}
	if n < 20 {
		t.Fatalf("only %d golden cases", n)
	}
}

func TestScoreErrors(t *testing.T) {
	f := newFx(t)
	task := f.task(taskOpts{dsOpts: dsOpts{scoreType: "GroupMin", scoreParams: `[[100, "no-such-testcase"]]`}})
	p := f.participant("alice")
	s := f.submit(p, task, time.Minute)
	all := f.judgeAll(Targets{SubmissionIDs: []int32{s}}, allCorrect)
	last := all[len(all)-1]
	if len(last.ScoreErrors) != 1 || last.ScoreErrors[0].SubmissionID != s {
		t.Fatalf("score errors %+v", last.ScoreErrors)
	}
	if r := f.result(s, task.dataset); r.status() != StatusScoring {
		t.Fatalf("result %+v", r)
	}
	if got := f.cached(p, task); got != nil && !got.Partial {
		t.Errorf("task score %+v", got)
	}
	// An evaluation without outcome (an old dump) cannot be scored either.
	f.exec(`UPDATE datasets SET score_type_parameters = '[[100, ".*"]]' WHERE id = $1`, task.dataset)
	f.exec(`UPDATE evaluations SET outcome = NULL WHERE submission_id = $1 AND testcase_id =
		(SELECT min(testcase_id) FROM evaluations WHERE submission_id = $1)`, s)
	ch, err := f.j.Score(f.ctx, []ResultKey{{s, task.dataset}})
	if err != nil || len(ch.ScoreErrors) != 1 || !strings.Contains(ch.ScoreErrors[0].Err.Error(), "no outcome") {
		t.Fatalf("changes %+v, %v", ch, err)
	}
	// Fixed: the sweep scores it.
	f.exec(`UPDATE evaluations SET outcome = '1.0' WHERE outcome IS NULL`)
	w, err := f.j.Pending(f.ctx, Targets{SubmissionIDs: []int32{s}})
	if err != nil || len(w.ToScore) != 1 {
		t.Fatalf("pending %+v %v", w, err)
	}
	if ch, err = f.j.Score(f.ctx, w.ToScore); err != nil || len(ch.ScoreErrors) != 0 {
		t.Fatalf("changes %+v, %v", ch, err)
	}
	if r := f.result(s, task.dataset); r.status() != StatusScored || *r.Score != 100 {
		t.Errorf("result %+v", r)
	}
	if got := f.cached(p, task); got == nil || got.Partial || got.Score != 100 {
		t.Errorf("task score %+v", got)
	}
	// Scoring again changes nothing.
	if ch, err = f.j.Score(f.ctx, w.ToScore); err != nil || len(ch.Submissions) != 0 || len(ch.TaskScores) != 0 {
		t.Errorf("changes %+v, %v", ch, err)
	}
}
