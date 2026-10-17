// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/io/web_rpc.py (RPCMiddleware),
// cms/server/admin/rpc_authorization.py and
// AdminWebServer.submissions_status in cms/server/admin/server.py of CMS:
// Copyright © 2013 Luca Wehrstedt <luca.wehrstedt@gmail.com>
// Copyright © 2010-2017 Stefano Maggiolo <s.maggiolo@gmail.com>

package adminweb

// The RPC endpoint of web_rpc.js (cmsrpc_request): POST
// /rpc/<service>/<shard>/<method> with a JSON object of arguments,
// answered {"data": <result>, "error": null}. CMS proxies these calls to
// its services; here each method of rpcMethods is implemented by the
// admin site itself on the database, Valkey and the queues (there is no
// RPC between services, D1). Status codes as RPCMiddleware: 404 unknown
// method, 403 not allowed, 415 not JSON, 400 invalid JSON, 503 the
// backing service (Valkey) is unreachable.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/queue"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/worker/registry"
)

// RPCMethod is one method of the RPC endpoint.
type RPCMethod struct {
	Service, Method string
	// Perm is the permission the method requires (CMS
	// RPCS_ALLOWED_FOR_*).
	Perm Perm
	// Call runs the method with its JSON arguments; its result is the
	// "data" of the answer. ErrRPCUnavailable answers 503 ("Service not
	// connected."); other errors go in "error".
	Call func(c *Ctx, args json.RawMessage) (any, error)
}

// ErrRPCUnavailable: the service behind a method is unreachable (503).
var ErrRPCUnavailable = errors.New("service unavailable")

// errRPCNotImplemented: the method is not written yet (501).
var errRPCNotImplemented = errors.New("not implemented")

// rpcMethods is the table of methods. CMS's LogService.last_messages and
// ResourceService methods do not exist: logs go to the standard output of
// each service and the Resource usage page is built on the server.
func (s *Server) rpcMethods() []RPCMethod {
	return []RPCMethod{
		{"AdminWebServer", "submissions_status", PermRead, s.rpcSubmissionsStatus},
		{"EvaluationService", "queue_status", PermRead, s.rpcQueueStatus},
		{"EvaluationService", "workers_status", PermRead, s.rpcWorkersStatus},
		// C: judges.go.
		{"EvaluationService", "enable_worker", PermAll, s.rpcEnableWorker},
		{"EvaluationService", "disable_worker", PermAll, s.rpcDisableWorker},
		// C: submissions.go (reevaluation buttons).
		{"EvaluationService", "invalidate_submission", PermAll, s.rpcInvalidateSubmission},
		{"ScoringService", "invalidate_submission", PermAll, s.rpcInvalidateSubmission},
	}
}

// rpcPost is POST /rpc/<service>/<shard>/<method>.
func (s *Server) rpcPost(c *Ctx) {
	service, method := c.R.PathValue("service"), c.R.PathValue("method")
	if c.R.PathValue("shard") == "all" {
		http.NotFound(c.W, c.R)
		return
	}
	var m *RPCMethod
	for _, x := range s.rpcMethods() {
		if x.Service == service && x.Method == method {
			m = &x
			break
		}
	}
	if m == nil {
		http.NotFound(c.W, c.R)
		return
	}
	if !c.Admin.Can(m.Perm) {
		http.Error(c.W, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}
	if mt, _, _ := mime.ParseMediaType(c.R.Header.Get("Content-Type")); mt != "application/json" {
		http.Error(c.W, http.StatusText(http.StatusUnsupportedMediaType), http.StatusUnsupportedMediaType)
		return
	}
	body, err := io.ReadAll(c.R.Body)
	var args json.RawMessage
	if err == nil {
		err = json.Unmarshal(body, &args)
	}
	if err != nil || len(args) == 0 || args[0] != '{' {
		http.Error(c.W, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	res, err := m.Call(c, args)
	switch {
	case errors.Is(err, ErrRPCUnavailable):
		http.Error(c.W, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		return
	case errors.Is(err, errRPCNotImplemented):
		http.Error(c.W, http.StatusText(http.StatusNotImplemented), http.StatusNotImplemented)
		return
	case err != nil:
		s.log.Warn("rpc failed", "service", service, "method", method, "err", err)
		c.JSON(http.StatusOK, map[string]any{"data": nil, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"data": res, "error": nil})
}

// rpcSubmissionsStatus is AdminWebServer.submissions_status(contest_id):
// the submissions of a contest by status on the active datasets.
func (s *Server) rpcSubmissionsStatus(c *Ctx, raw json.RawMessage) (any, error) {
	var args struct {
		ContestID *int32 `json:"contest_id"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, err
	}
	if args.ContestID == nil {
		return nil, errors.New("contest_id is required")
	}
	tries := s.cfg.Dispatcher
	r, err := s.st.Q.AdminCoreSubmissionsStatus(c.Context(), q.AdminCoreSubmissionsStatusParams{
		ContestID:           *args.ContestID,
		MaxCompilationTries: int32(max(tries.MaxCompilationTries, 1)),
		MaxEvaluationTries:  int32(max(tries.MaxEvaluationTries, 1)),
	})
	if err != nil {
		return nil, err
	}
	return map[string]int64{
		"total": r.Total, "compiling": r.Compiling, "max_compilations": r.MaxCompilations,
		"compilation_fail": r.CompilationFail, "evaluating": r.Evaluating,
		"max_evaluations": r.MaxEvaluations, "scoring": r.Scoring, "scored": r.Scored,
	}, nil
}

// jobQueue opens the job queue of the dispatcher once (lazily: the site
// starts without Valkey).
type jobQueue struct {
	mu sync.Mutex
	q  *queue.Queue
}

// Jobs returns the job queue between the dispatcher and the workers (the
// judges page and the overview read it).
func (s *Server) Jobs(ctx context.Context) (*queue.Queue, error) {
	s.jobs.mu.Lock()
	defer s.jobs.mu.Unlock()
	if s.jobs.q != nil {
		return s.jobs.q, nil
	}
	jq, err := queue.Open(ctx, s.kv, job.JobQueue, queue.Options{Priorities: job.JobPriorities, Visibility: job.JobVisibility})
	if err != nil {
		return nil, err
	}
	s.jobs.q = jq
	return jq, nil
}

// queueLimit bounds the entries of queue_status per priority (CMS lists
// the whole queue).
const queueLimit = 100

// rpcJob is a job as CMS's ESOperation.to_dict (aws_utils.js repr_job).
type rpcJob struct {
	Type             string `json:"type"`
	ObjectID         int32  `json:"object_id"`
	DatasetID        int32  `json:"dataset_id"`
	TestcaseCodename string `json:"testcase_codename,omitempty"`
}

func rpcJobOf(op job.Operation) rpcJob {
	return rpcJob{Type: string(op.Type), ObjectID: op.ObjectID, DatasetID: op.DatasetID, TestcaseCodename: op.TestcaseCodename}
}

// rpcQueueStatus is EvaluationService.queue_status: the jobs waiting in
// the queue, highest priority first (at most queueLimit per priority),
// as {item, priority, timestamp}.
func (s *Server) rpcQueueStatus(c *Ctx, _ json.RawMessage) (any, error) {
	jq, err := s.Jobs(c.Context())
	if err != nil {
		return nil, ErrRPCUnavailable
	}
	msgs, err := jq.Peek(c.Context(), queueLimit)
	if err != nil {
		return nil, ErrRPCUnavailable
	}
	type entry struct {
		Item      rpcJob  `json:"item"`
		Priority  int     `json:"priority"`
		Timestamp float64 `json:"timestamp"`
	}
	out := []entry{}
	for _, m := range msgs {
		j, err := job.DecodeJob(m.Payload)
		if err != nil {
			continue
		}
		out = append(out, entry{Item: rpcJobOf(j.Operation), Priority: m.Priority,
			Timestamp: float64(m.EnqueuedAt.UnixMicro()) / 1e6})
	}
	return out, nil
}

// rpcWorkerStatus is a worker as CMS's workers_status shows it.
type rpcWorkerStatus struct {
	Connected bool `json:"connected"`
	// Operations is the list of running jobs, or "disabled" while the
	// worker drains.
	Operations any      `json:"operations"`
	StartTime  *float64 `json:"start_time"`
}

// rpcWorkersStatus is EvaluationService.workers_status: the workers of
// the registry by name (CMS: by shard), with their running jobs.
func (s *Server) rpcWorkersStatus(c *Ctx, _ json.RawMessage) (any, error) {
	infos, err := registry.List(c.Context(), s.kv)
	if err != nil {
		return nil, ErrRPCUnavailable
	}
	out := map[string]rpcWorkerStatus{}
	for _, w := range infos {
		st := rpcWorkerStatus{Connected: true}
		jobs := append([]registry.Job(nil), w.Jobs...)
		sort.Slice(jobs, func(i, j int) bool { return jobs[i].StartedAt.Before(jobs[j].StartedAt) })
		ops := []rpcJob{}
		for _, j := range jobs {
			ops = append(ops, rpcJobOf(j.Operation))
		}
		if len(jobs) > 0 {
			t := float64(jobs[0].StartedAt.UnixMicro()) / 1e6
			st.StartTime = &t
		}
		st.Operations = ops
		if w.Draining || w.Error != "" {
			// Draining (or broken): it takes no new jobs.
			st.Operations = "disabled"
		}
		out[w.Name] = st
	}
	return out, nil
}

// workerName reads the "shard" argument of enable_worker and
// disable_worker: a worker name here.
func workerName(raw json.RawMessage) (string, error) {
	var args struct {
		Shard json.RawMessage `json:"shard"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", err
	}
	var name string
	if err := json.Unmarshal(args.Shard, &name); err != nil {
		name = strings.TrimSpace(string(args.Shard))
	}
	if name == "" {
		return "", errors.New("shard is required")
	}
	return name, nil
}
