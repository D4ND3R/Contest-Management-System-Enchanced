// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package adminweb

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/queue"
)

// TestSubmissionForms checks the comment and official forms of the
// submission page and the reevaluation RPC.
func TestSubmissionForms(t *testing.T) {
	ts, f := newFixtureSite(t, testOptions{})
	c := ts.Client()
	c.Login("admin")
	sid := itoa(f.Scored)
	dsid := itoa(f.Datasets["sum"])

	n := ts.AuditCount()
	if r := c.Post("/submission/"+sid+"/comment", url.Values{"comment": {"looks copied"}}); !wantRedirect(r, "submission/"+sid) {
		t.Fatalf("comment: %d %q", r.Status, r.Location())
	}
	if ts.QueryInt(`SELECT count(*) FROM submissions WHERE id = $1 AND comment = 'looks copied'`, f.Scored) != 1 {
		t.Fatal("the comment was not saved")
	}
	audited(t, ts, n, "submission.comment", int64(f.Scored))
	if r := c.Post("/submission/"+sid+"/"+dsid+"/comment", url.Values{"comment": {""}}); !wantRedirect(r, "submission/"+sid+"/"+dsid) {
		t.Fatalf("comment with dataset: %d %q", r.Status, r.Location())
	}
	if r := c.Post("/submission/999999/comment", url.Values{"comment": {"x"}}); r.Status != http.StatusNotFound {
		t.Fatalf("comment on a missing submission: %d", r.Status)
	}
	// The form of the page (multipart, as in CMS) round-trips.
	form := c.mustForm("/submission/"+sid, "submission/"+sid+"/"+dsid+"/comment")
	form.Set("comment", "again")
	if r := c.PostFiles("/submission/"+sid+"/"+dsid+"/comment", form); r.Status != http.StatusFound {
		t.Fatalf("multipart comment: %d", r.Status)
	}
	if ts.QueryInt(`SELECT count(*) FROM submissions WHERE id = $1 AND comment = 'again'`, f.Scored) != 1 {
		t.Fatal("the comment form of the page did not work")
	}

	// Unofficial: the task score of alice drops to 0.
	n = ts.AuditCount()
	if r := c.Post("/submission/"+sid+"/official", url.Values{"official": {"no"}}); !wantRedirect(r, "submission/"+sid) {
		t.Fatalf("official: %d %q", r.Status, r.Location())
	}
	if ts.QueryInt(`SELECT count(*) FROM submissions WHERE id = $1 AND NOT official`, f.Scored) != 1 {
		t.Fatal("the submission is still official")
	}
	if ts.QueryInt(`SELECT count(*) FROM task_scores WHERE participation_id = $1 AND task_id = $2 AND score = 0`,
		f.Part["alice"], f.Tasks["sum"]) != 1 {
		t.Fatal("the task score did not follow")
	}
	audited(t, ts, n, "submission.official", int64(f.Scored))
	c.Post("/submission/"+sid+"/official", url.Values{"official": {"yes"}})
	if ts.QueryInt(`SELECT count(*) FROM task_scores WHERE participation_id = $1 AND task_id = $2 AND score = 100`,
		f.Part["alice"], f.Tasks["sum"]) != 1 {
		t.Fatal("the task score did not come back")
	}

	// Reevaluation: the evaluations of the submission go away.
	n = ts.AuditCount()
	var none any
	rpcAnswer(t, c.RPC("EvaluationService", "invalidate_submission",
		map[string]any{"submission_id": f.Scored, "dataset_id": f.Datasets["sum"], "level": "evaluation"}), &none)
	if ts.QueryInt(`SELECT count(*) FROM evaluations WHERE submission_id = $1 AND dataset_id = $2`, f.Scored, f.Datasets["sum"]) != 0 {
		t.Fatal("reevaluate: the evaluations are still there")
	}
	audited(t, ts, n, "submission.reevaluate", int64(f.Scored))
	if e := ts.LastAudit(); e.ContestID == nil || *e.ContestID != ts.Contest {
		t.Fatalf("reevaluate audit without the contest: %+v", e)
	}
	// Recompile a whole contest, rescore a task.
	n = ts.AuditCount()
	rpcAnswer(t, c.RPC("EvaluationService", "invalidate_submission", map[string]any{"contest_id": ts.Contest}), &none)
	audited(t, ts, n, "submission.recompile", int64(ts.Contest))
	if ts.QueryInt(`SELECT count(*) FROM submission_results WHERE compilation_outcome IS NOT NULL AND submission_id IN
		(SELECT id FROM submissions WHERE task_id = ANY($1))`, []int32{f.Tasks["sum"], f.Tasks["half"]}) != 0 {
		t.Fatal("recompile: compilation outcomes remain")
	}
	n = ts.AuditCount()
	rpcAnswer(t, c.RPC("ScoringService", "invalidate_submission", map[string]any{"task_id": f.Tasks["sum"]}), &none)
	audited(t, ts, n, "submission.rescore", int64(f.Tasks["sum"]))
	// A bad level is an error of the RPC, not a change.
	n = ts.AuditCount()
	if r := c.RPC("EvaluationService", "invalidate_submission", map[string]any{"submission_id": f.Scored, "level": "x"}); !strings.Contains(r.Body, `"error"`) || ts.AuditCount() != n {
		t.Fatalf("bad level: %d %s", r.Status, r.Body)
	}

	// Neither the problem preparer nor the messaging role.
	for _, name := range []string{"prep", "msg"} {
		o := ts.Client()
		o.Login(name)
		if r := o.Post("/submission/"+sid+"/comment", url.Values{"comment": {"x"}}); r.Status != http.StatusForbidden {
			t.Errorf("comment as %s: %d", name, r.Status)
		}
		if r := o.Post("/submission/"+sid+"/official", url.Values{"official": {"no"}}); r.Status != http.StatusForbidden {
			t.Errorf("official as %s: %d", name, r.Status)
		}
		if r := o.RPC("EvaluationService", "invalidate_submission", map[string]any{"submission_id": f.Scored}); r.Status != http.StatusForbidden {
			t.Errorf("reevaluate as %s: %d", name, r.Status)
		}
	}
}

// TestJudgesActions checks the actions of the judges page on fake jobs:
// requeue and drop a job in progress, requeue and delete dead letters.
func TestJudgesActions(t *testing.T) {
	ts, _ := newFixtureSite(t, testOptions{})
	ctx := t.Context()
	jq, err := queue.Open(ctx, ts.KV, job.JobQueue, queue.Options{Priorities: job.JobPriorities, MaxAttempts: 1,
		Visibility: job.JobVisibility})
	if err != nil {
		t.Fatal(err)
	}
	enqueue := func(id string) {
		t.Helper()
		j := &job.Job{ID: id, Operation: job.Operation{Type: job.Compilation, ObjectID: 6, DatasetID: 3}, Attempt: 1,
			TaskType: "Batch", TaskTypeParameters: json.RawMessage(`["alone",["",""],"diff"]`),
			Files: map[string]string{"a.%l": strings.Repeat("a", 64)}, Executables: map[string]string{}, Managers: map[string]string{}}
		payload, err := j.Encode()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := jq.Enqueue(ctx, job.PriorityLow, payload, queue.EnqueueOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	consume := func() *queue.Message {
		t.Helper()
		m, err := jq.Consume(ctx, "w1", 10*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	// Two dead letters and one job in progress.
	for _, id := range []string{"d1", "d2"} {
		enqueue(id)
		if dead, err := jq.Retry(ctx, consume(), errors.New("worker crashed")); err != nil || !dead {
			t.Fatalf("retry: %v %v", dead, err)
		}
	}
	enqueue("busy")
	busy := consume()

	c := ts.Client()
	c.Login("admin")
	if r := c.Get("/judges"); r.Status != http.StatusOK || !strings.Contains(r.Body, "worker crashed") {
		t.Fatalf("judges page: %d", r.Status)
	}
	prio := itoa(busy.Priority)

	n := ts.AuditCount()
	if r := c.Post("/judges/job", url.Values{"action": {"requeue"}, "id": {busy.ID}, "priority": {prio}}); !wantRedirect(r, "judges") {
		t.Fatalf("requeue: %d %q", r.Status, r.Location())
	}
	audited(t, ts, n, "job.requeue", 0)
	// Another worker gets it at once.
	if m, err := jq.Consume(ctx, "w2", 10*time.Millisecond); err != nil || m.ID != busy.ID {
		t.Fatalf("requeued job: %+v %v", m, err)
	}
	n = ts.AuditCount()
	c.Post("/judges/job", url.Values{"action": {"drop"}, "id": {busy.ID}, "priority": {prio}})
	audited(t, ts, n, "job.drop", 0)
	if _, err := jq.Get(ctx, busy.Priority, busy.ID); !errors.Is(err, queue.ErrNoMessage) {
		t.Fatalf("dropped job: %v", err)
	}
	// Again: the job is gone, nothing is audited.
	n = ts.AuditCount()
	c.Post("/judges/job", url.Values{"action": {"drop"}, "id": {busy.ID}, "priority": {prio}})
	if ts.AuditCount() != n || !strings.Contains(c.notices(), "not in the queue") {
		t.Fatal("dropping a missing job")
	}
	if r := c.Post("/judges/job", url.Values{"action": {"explode"}, "id": {busy.ID}, "priority": {prio}}); r.Status != http.StatusBadRequest {
		t.Fatalf("bad action: %d", r.Status)
	}

	dead, err := jq.ListDead(ctx, 10)
	if err != nil || len(dead) != 2 {
		t.Fatalf("dead letters: %d %v", len(dead), err)
	}
	n = ts.AuditCount()
	c.Post("/judges/dead", url.Values{"action": {"requeue"}, "id": {dead[0].ID}})
	audited(t, ts, n, "dead_letter.requeue", 0)
	n = ts.AuditCount()
	c.Post("/judges/dead", url.Values{"action": {"delete"}, "id": {dead[1].ID}})
	audited(t, ts, n, "dead_letter.delete", 0)
	if left, _ := jq.ListDead(ctx, 10); len(left) != 0 {
		t.Fatalf("dead letters left: %d", len(left))
	}
	if st, err := jq.Stats(ctx); err != nil || st.Ready != 1 {
		t.Fatalf("the requeued dead letter is not ready: %+v %v", st, err)
	}

	// Only permission_all.
	for _, name := range []string{"prep", "msg", "ro"} {
		o := ts.Client()
		o.Login(name)
		if r := o.Post("/judges/dead", url.Values{"action": {"delete"}, "id": {"1-1"}}); r.Status != http.StatusForbidden {
			t.Errorf("dead letter as %s: %d", name, r.Status)
		}
	}
}
