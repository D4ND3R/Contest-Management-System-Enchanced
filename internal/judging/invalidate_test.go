// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package judging

import (
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
)

func (f *fx) invalidate(scope Scope, level Level) *Invalidation {
	f.t.Helper()
	inv, err := f.j.Invalidate(f.ctx, scope, level)
	if err != nil {
		f.t.Fatal(err)
	}
	return inv
}

func TestInvalidateLevels(t *testing.T) {
	f := newFx(t)
	task := f.task(taskOpts{})
	p := f.participant("alice")
	s := f.submit(p, task, time.Minute)
	f.judgeAll(Targets{SubmissionIDs: []int32{s}}, allCorrect)
	judged := f.result(s, task.dataset)
	scored := TaskScore{DatasetID: task.dataset, Score: 30, PublicScore: 10}
	f.checkCache(p, task, scored)
	key := ResultKey{s, task.dataset}

	// Rescore: only the score goes, the generation stays.
	inv := f.invalidate(Scope{SubmissionIDs: []int32{s}}, LevelScore)
	if !slices.Equal(inv.Results, []ResultKey{key}) || !slices.Equal(inv.Submissions, []int32{s}) ||
		len(inv.TaskScores) != 1 || !inv.TaskScores[0].New.Partial {
		t.Fatalf("invalidation %+v", inv)
	}
	r := f.result(s, task.dataset)
	if r.status() != StatusScoring || r.Generation != 0 || r.Evaluations != 3 || r.Executables != 1 {
		t.Fatalf("after rescore invalidation: %+v", r)
	}
	f.checkCache(p, task, TaskScore{DatasetID: task.dataset, Partial: true})
	w, err := f.j.Pending(f.ctx, Targets{SubmissionIDs: inv.Submissions})
	if err != nil || len(w.Ops) != 0 || !slices.Equal(w.ToScore, []ResultKey{key}) {
		t.Fatalf("pending %+v %v", w, err)
	}
	if _, err := f.j.Score(f.ctx, w.ToScore); err != nil {
		t.Fatal(err)
	}
	if r := f.result(s, task.dataset); *r.Score != 30 {
		t.Errorf("rescored: %+v", r)
	}
	f.checkCache(p, task, scored)
	// Nothing to rescore: nothing changes.
	if inv := f.invalidate(Scope{TaskID: task.id, DatasetID: f.task(taskOpts{}).dataset}, LevelScore); len(inv.Results) != 0 {
		t.Errorf("another task's dataset: %+v", inv)
	}

	// Reevaluate one testcase.
	tc1 := scalar[int32](f, `SELECT id FROM testcases WHERE dataset_id = $1 AND codename = '001'`, task.dataset)
	inv = f.invalidate(Scope{SubmissionIDs: []int32{s}, TestcaseID: tc1}, LevelEvaluation)
	r = f.result(s, task.dataset)
	if len(inv.Results) != 1 || r.status() != StatusEvaluating || r.Evaluations != 2 || r.Generation != 1 ||
		r.EvaluationTries != 0 || r.Executables != 1 {
		t.Fatalf("after reevaluating a testcase: %+v %+v", inv, r)
	}
	w, _ = f.j.Pending(f.ctx, Targets{SubmissionIDs: inv.Submissions})
	sameKeys(t, "one testcase", opKeys(w.Ops), []string{fmt.Sprintf("evaluate/%d/%d/001 p%d a1", s, task.dataset, PriorityMedium)})
	f.judge(w.Ops, allCorrect)
	f.checkCache(p, task, scored)

	// Reevaluate everything.
	inv = f.invalidate(Scope{SubmissionIDs: []int32{s}}, LevelEvaluation)
	r = f.result(s, task.dataset)
	if r.status() != StatusEvaluating || r.Evaluations != 0 || r.Generation != 2 || r.Executables != 1 ||
		*r.CompilationOutcome != "ok" || r.CompilationTries != judged.CompilationTries {
		t.Fatalf("after reevaluation: %+v", r)
	}
	f.judgeAll(Targets{SubmissionIDs: inv.Submissions}, allCorrect)
	f.checkCache(p, task, scored)

	// Recompile.
	inv = f.invalidate(Scope{SubmissionIDs: []int32{s}}, LevelCompilation)
	r = f.result(s, task.dataset)
	if r.status() != StatusCompiling || r.Evaluations != 0 || r.Executables != 0 || r.Generation != 3 ||
		r.CompilationTries != 0 || r.Score != nil {
		t.Fatalf("after recompilation: %+v", r)
	}
	row := scalar[string](f, `SELECT jsonb_build_array(compilation_text, compilation_stdout, compilation_time,
		compilation_sandbox_paths, scored_at IS NOT NULL)::text FROM submission_results WHERE submission_id = $1`, s)
	if row != `[[], null, null, null, true]` {
		t.Errorf("compilation not blanked: %s", row)
	}
	f.judgeAll(Targets{SubmissionIDs: inv.Submissions}, allCorrect)
	f.checkCache(p, task, scored)

	// Errors.
	if _, err := f.j.Invalidate(f.ctx, Scope{}, LevelCompilation); !errors.Is(err, ErrEmptyScope) {
		t.Errorf("empty scope: %v", err)
	}
	if _, err := f.j.Invalidate(f.ctx, Scope{TaskID: task.id}, "everything"); !errors.Is(err, ErrLevel) {
		t.Errorf("unknown level: %v", err)
	}
	if _, err := f.j.Invalidate(f.ctx, Scope{TestcaseID: tc1}, LevelCompilation); !errors.Is(err, ErrLevel) {
		t.Errorf("testcase with the compilation level: %v", err)
	}
}

func TestInvalidateScopes(t *testing.T) {
	f := newFx(t)
	t1 := f.task(taskOpts{})
	auto := f.dataset(t1.id, dsOpts{autojudge: true})
	t2 := f.task(taskOpts{})
	alice, bob := f.participant("alice"), f.participant("bob")
	// alice also takes part in another contest.
	other := f.id(`INSERT INTO contests (name, description) VALUES ('other', 'Other') RETURNING id`)
	og := f.id(`INSERT INTO groups (name, contest_id) VALUES ('main', $1) RETURNING id`, other)
	aliceOther := f.id(`INSERT INTO participations (contest_id, user_id, group_id)
		SELECT $1, user_id, $2 FROM participations WHERE id = $3 RETURNING id`, other, og, alice)
	t3 := f.task(taskOpts{})
	f.exec(`UPDATE tasks SET contest_id = $1 WHERE id = $2`, other, t3.id)

	type sub struct {
		id   int32
		p    int32
		task tk
	}
	var subs []sub
	for _, s := range []sub{{0, alice, t1}, {0, alice, t2}, {0, bob, t1}, {0, bob, t2}, {0, aliceOther, t3}, {0, alice, t1}} {
		s.id = f.submit(s.p, s.task, time.Minute)
		subs = append(subs, s)
	}
	var ids []int32
	for _, s := range subs[:5] {
		ids = append(ids, s.id)
	}
	f.judgeAll(Targets{SubmissionIDs: ids}, allCorrect) // the last one has no results

	datasetsOf := func(task tk) []int32 {
		if task == t1 {
			return []int32{t1.dataset, auto}
		}
		return []int32{task.dataset}
	}
	expect := func(match func(sub) bool, dataset int32) (submissions []int32, results []ResultKey) {
		for i, s := range subs {
			if !match(s) {
				continue
			}
			if dataset != 0 && !slices.Contains(datasetsOf(s.task), dataset) {
				continue
			}
			submissions = append(submissions, s.id)
			if i == 5 {
				continue // no results
			}
			for _, ds := range datasetsOf(s.task) {
				if dataset == 0 || ds == dataset {
					results = append(results, ResultKey{s.id, ds})
				}
			}
		}
		return
	}
	userAlice := scalar[int32](f, `SELECT user_id FROM participations WHERE id = $1`, alice)
	cases := []struct {
		name    string
		scope   Scope
		match   func(sub) bool
		dataset int32
	}{
		{"contest", Scope{ContestID: f.contest}, func(s sub) bool { return s.task != t3 }, 0},
		{"task", Scope{TaskID: t1.id}, func(s sub) bool { return s.task == t1 }, 0},
		{"dataset", Scope{DatasetID: auto}, func(s sub) bool { return s.task == t1 }, auto},
		{"participation", Scope{ParticipationID: bob}, func(s sub) bool { return s.p == bob }, 0},
		{"user", Scope{UserID: userAlice}, func(s sub) bool { return s.p == alice || s.p == aliceOther }, 0},
		{"user in a contest", Scope{UserID: userAlice, ContestID: other}, func(s sub) bool { return s.p == aliceOther }, 0},
		{"participation and task", Scope{ParticipationID: alice, TaskID: t2.id}, func(s sub) bool { return s.p == alice && s.task == t2 }, 0},
		{"submissions", Scope{SubmissionIDs: []int32{subs[0].id, subs[3].id, subs[0].id}},
			func(s sub) bool { return s.id == subs[0].id || s.id == subs[3].id }, 0},
		{"submission on a dataset", Scope{SubmissionIDs: []int32{subs[0].id}, DatasetID: t1.dataset},
			func(s sub) bool { return s.id == subs[0].id }, t1.dataset},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f.t = t
			wantSubs, wantResults := expect(c.match, c.dataset)
			inv := f.invalidate(c.scope, LevelScore)
			if !slices.Equal(inv.Submissions, wantSubs) {
				t.Errorf("submissions %v, want %v", inv.Submissions, wantSubs)
			}
			if !sameResultKeys(inv.Results, wantResults) {
				t.Errorf("results %v, want %v", inv.Results, wantResults)
			}
			// Score them again for the next case.
			w, err := f.j.Pending(f.ctx, Targets{SubmissionIDs: ids})
			if err != nil {
				t.Fatal(err)
			}
			if !sameResultKeys(w.ToScore, wantResults) {
				t.Errorf("to score %v, want %v", w.ToScore, wantResults)
			}
			if _, err := f.j.Score(f.ctx, w.ToScore); err != nil {
				t.Fatal(err)
			}
		})
	}
	f.t = t
	for _, s := range subs[:5] {
		// alice's last submission to t1 is not judged.
		partial := s.p == alice && s.task == t1
		if p := f.cached(s.p, s.task); p == nil || p.Partial != partial || p.Score != 30 {
			t.Errorf("%+v: task score %+v", s, p)
		}
	}
}

func TestInvalidateUserTests(t *testing.T) {
	f := newFx(t)
	task := f.task(taskOpts{})
	auto := f.dataset(task.id, dsOpts{autojudge: true})
	p := f.participant("alice")
	u := f.userTest(p, task, time.Minute, nil)
	ok := func(j *job.Job) string {
		if j.Operation.Type.IsCompilation() {
			return job.CompilationOK
		}
		return "ok"
	}
	f.judgeAll(Targets{UserTestIDs: []int32{u}}, ok)
	state := func(ds int32) string {
		return scalar[string](f, `SELECT jsonb_build_array(compilation_outcome, evaluation_outcome, output IS NOT NULL, generation,
			(SELECT count(*) FROM user_test_executables e WHERE e.user_test_id = r.user_test_id AND e.dataset_id = r.dataset_id))::text
			FROM user_test_results r WHERE user_test_id = $1 AND dataset_id = $2`, u, ds)
	}
	if s := state(auto); s != `["ok", "ok", true, 0, 1]` {
		t.Fatalf("judged: %s", s)
	}
	// Score: nothing to do for user tests.
	if inv := f.invalidate(Scope{UserTestIDs: []int32{u}}, LevelScore); len(inv.UserTestResults) != 0 {
		t.Errorf("score level: %+v", inv)
	}
	inv := f.invalidate(Scope{UserTestIDs: []int32{u}, DatasetID: auto}, LevelEvaluation)
	if len(inv.Submissions) != 0 || !slices.Equal(inv.UserTestResults, []UserTestKey{{u, auto}}) {
		t.Fatalf("evaluation level: %+v", inv)
	}
	if s := state(auto); s != `["ok", null, false, 1, 1]` {
		t.Errorf("reevaluated: %s", s)
	}
	if s := state(task.dataset); s != `["ok", "ok", true, 0, 1]` {
		t.Errorf("the other dataset changed: %s", s)
	}
	inv = f.invalidate(Scope{UserTestIDs: []int32{u}}, LevelCompilation)
	if len(inv.UserTestResults) != 2 {
		t.Fatalf("compilation level: %+v", inv)
	}
	if s := state(auto); s != `[null, null, false, 2, 0]` {
		t.Errorf("recompiled: %s", s)
	}
	w, err := f.j.Pending(f.ctx, Targets{UserTestIDs: inv.UserTests})
	if err != nil || len(w.Ops) != 2 {
		t.Fatalf("pending %+v %v", w, err)
	}
	f.judge(w.Ops, ok)
	if s := state(auto); s != `["ok", "ok", true, 2, 1]` {
		t.Errorf("judged again: %s", s)
	}
}

func TestInvalidationEvents(t *testing.T) {
	inv := &Invalidation{Level: LevelEvaluation}
	for i := int32(1); i <= 2500; i++ {
		inv.Submissions = append(inv.Submissions, i)
	}
	inv.UserTests = []int32{7, 8}
	events := inv.Events(true)
	if len(events) != 3 || len(events[0].SubmissionIDs) != 1000 || len(events[2].SubmissionIDs) != 500 ||
		!slices.Equal(events[2].UserTestIDs, []int32{7, 8}) || events[1].Level != LevelEvaluation ||
		!events[0].ArchiveSandbox || events[0].Kind != EventInvalidate {
		t.Errorf("events %+v", events)
	}
	if got := (&Invalidation{}).Events(false); len(got) != 0 {
		t.Errorf("empty invalidation: %+v", got)
	}
}

// TestInvalidateConstantQueries: invalidating the results of 1 or 50
// participants, at any level, takes the same number of queries (N2).
func TestInvalidateConstantQueries(t *testing.T) {
	count := func(n int) map[Level]int64 {
		f := newFx(t)
		task := f.task(taskOpts{})
		f.dataset(task.id, dsOpts{autojudge: true})
		var subs []int32
		for i := 0; i < n; i++ {
			p := f.participant(fmt.Sprintf("user%d", i))
			subs = append(subs, f.submit(p, task, time.Minute), f.submit(p, task, 2*time.Minute))
		}
		uts := []int32{f.userTest(f.id(`SELECT min(id) FROM participations`), task, time.Minute, nil)}
		f.judgeAll(Targets{SubmissionIDs: subs, UserTestIDs: uts}, allCorrect)
		out := map[Level]int64{}
		for _, level := range []Level{LevelScore, LevelEvaluation, LevelCompilation} {
			f.qc.Reset()
			inv := f.invalidate(Scope{TaskID: task.id, UserTestIDs: uts}, level)
			out[level] = f.qc.Count()
			if len(inv.Results) != 4*n || len(inv.TaskScores) != n {
				t.Fatalf("n=%d, %s: %d results, %d task scores", n, level, len(inv.Results), len(inv.TaskScores))
			}
			f.judgeAll(Targets{SubmissionIDs: subs, UserTestIDs: uts}, allCorrect)
		}
		return out
	}
	c1, c50 := count(1), count(50)
	for level, n := range c1 {
		if c50[level] != n {
			t.Errorf("%s: %d queries for 1 participant, %d for 50", level, n, c50[level])
		}
	}
	t.Logf("queries: %v", c1)
}
