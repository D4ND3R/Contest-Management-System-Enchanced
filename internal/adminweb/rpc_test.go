// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package adminweb

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/queue"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/worker/registry"
)

// rpcAnswer decodes {"data": ..., "error": ...}.
func rpcAnswer(t *testing.T, r *testResponse, data any) {
	t.Helper()
	var out struct {
		Data  json.RawMessage `json:"data"`
		Error *string         `json:"error"`
	}
	if r.Status != http.StatusOK {
		t.Fatalf("rpc: %d %s", r.Status, r.Body)
	}
	if err := json.Unmarshal([]byte(r.Body), &out); err != nil || out.Error != nil {
		t.Fatalf("rpc answer %s: %v", r.Body, err)
	}
	if err := json.Unmarshal(out.Data, data); err != nil {
		t.Fatalf("rpc data %s: %v", out.Data, err)
	}
}

// TestRPC checks the RPC endpoint of web_rpc.js: permissions, errors and
// the methods of the overview.
func TestRPC(t *testing.T) {
	ts := newTestSite(t, testOptions{})
	ro := ts.Client()
	ro.Login("ro")
	admin := ts.Client()
	admin.Login("admin")

	// Permissions and errors as CMS's RPCMiddleware.
	if r := ro.RPC("EvaluationService", "enable_worker", map[string]any{"shard": "w"}); r.Status != http.StatusForbidden {
		t.Errorf("enable_worker as read-only: %d", r.Status)
	}
	if r := admin.RPC("EvaluationService", "enable_worker", map[string]any{"shard": "w"}); r.Status != http.StatusOK {
		t.Errorf("enable_worker: %d", r.Status)
	}
	if r := ro.RPC("LogService", "last_messages", map[string]any{}); r.Status != http.StatusNotFound {
		t.Errorf("unknown method: %d", r.Status)
	}
	req, _ := http.NewRequest("POST", ts.URL+"/rpc/EvaluationService/0/queue_status", strings.NewReader("{}"))
	req.Header.Set("X-XSRFToken", ro.XSRF())
	if r := ro.Do(req); r.Status != http.StatusUnsupportedMediaType {
		t.Errorf("not JSON: %d", r.Status)
	}
	req, _ = http.NewRequest("POST", ts.URL+"/rpc/EvaluationService/0/queue_status", strings.NewReader("[1"))
	req.Header.Set("X-XSRFToken", ro.XSRF())
	req.Header.Set("Content-Type", "application/json")
	if r := ro.Do(req); r.Status != http.StatusBadRequest {
		t.Errorf("bad JSON: %d", r.Status)
	}

	// submissions_status: the demo contest has two submissions.
	var st map[string]int64
	rpcAnswer(t, ro.RPC("AdminWebServer", "submissions_status", map[string]any{"contest_id": ts.Contest}), &st)
	if st["total"] != 2 || st["scored"]+st["compiling"]+st["evaluating"]+st["scoring"]+st["compilation_fail"] != 2 {
		t.Errorf("submissions_status: %v", st)
	}

	// queue_status: the waiting jobs, highest priority first.
	var q0 []map[string]any
	rpcAnswer(t, ro.RPC("EvaluationService", "queue_status", map[string]any{}), &q0)
	if len(q0) != 0 {
		t.Errorf("empty queue: %v", q0)
	}
	jq, err := queue.Open(t.Context(), ts.KV, job.JobQueue, queue.Options{Priorities: job.JobPriorities})
	if err != nil {
		t.Fatal(err)
	}
	for i, op := range []job.Operation{
		{Type: job.Evaluation, ObjectID: 5, DatasetID: 3, TestcaseCodename: "tc01"},
		{Type: job.Compilation, ObjectID: 6, DatasetID: 3},
	} {
		j := &job.Job{ID: "j" + itoa(i), Operation: op, Attempt: 1, TaskType: "Batch",
			TaskTypeParameters: json.RawMessage(`["alone",["",""],"diff"]`), Files: map[string]string{"a.%l": strings.Repeat("a", 64)},
			Executables: map[string]string{}, Managers: map[string]string{}}
		if op.Type == job.Evaluation {
			j.Executables = map[string]string{"a": strings.Repeat("b", 64)}
			j.Input, j.Output = strings.Repeat("c", 64), strings.Repeat("d", 64)
		}
		payload, err := j.Encode()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := jq.Enqueue(t.Context(), job.PriorityLow-i, payload, queue.EnqueueOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	var q1 []struct {
		Item      rpcJob  `json:"item"`
		Priority  int     `json:"priority"`
		Timestamp float64 `json:"timestamp"`
	}
	rpcAnswer(t, ro.RPC("EvaluationService", "queue_status", map[string]any{}), &q1)
	if len(q1) != 2 || q1[0].Item.Type != "compile" || q1[0].Item.ObjectID != 6 || q1[1].Item.TestcaseCodename != "tc01" ||
		q1[0].Priority >= q1[1].Priority || q1[0].Timestamp <= 0 {
		t.Errorf("queue_status: %+v", q1)
	}

	// workers_status: the workers of the registry.
	reg, err := registry.Register(t.Context(), ts.KV, registry.Info{Name: "w1", Host: "judge1", PID: 1, Concurrency: 2,
		StartedAt: time.Now(), Jobs: []registry.Job{{Slot: 0, ID: "x", Operation: job.Operation{Type: job.Compilation, ObjectID: 9, DatasetID: 3},
			StartedAt: time.Now()}}}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close(t.Context())
	var ws map[string]struct {
		Connected  bool            `json:"connected"`
		Operations json.RawMessage `json:"operations"`
		StartTime  *float64        `json:"start_time"`
	}
	rpcAnswer(t, ro.RPC("EvaluationService", "workers_status", map[string]any{}), &ws)
	w := ws["w1"]
	if len(ws) != 1 || !w.Connected || w.StartTime == nil || !strings.Contains(string(w.Operations), `"object_id":9`) {
		t.Errorf("workers_status: %+v", ws)
	}
	// The Resource usage page lists the host of the worker.
	if r := ro.Get("/resources/all"); !strings.Contains(r.Body, "judge1") || !strings.Contains(r.Body, "worker w1") {
		t.Errorf("resources without the worker")
	}
}
