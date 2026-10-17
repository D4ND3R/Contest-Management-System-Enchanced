// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// enable_worker and disable_worker are ported from cms/service/workerpool.py
// and cms/service/EvaluationService.py of CMS:
// Copyright © 2010-2014 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2018 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2013-2015 Luca Wehrstedt <luca.wehrstedt@gmail.com>

package adminweb

// The judges page (work C, R6.7; new, CMS shows a part of it on the
// overview): the workers of the registry with their state and running
// jobs, the job queue by priority (ready, in progress, oldest), the next
// jobs, the jobs in progress, the dead letters, and the actions on them:
// enable or disable a worker (the RPC methods enable_worker and
// disable_worker of the overview, registry.SetDisabled), requeue or drop
// a job that is stuck, requeue or delete a dead letter. Every action
// needs permission_all and is audited. The page is made of live regions
// that follow the "judging" and "worker" events of the dispatcher (R6.9).
// It reads only Valkey: without it the page says so.

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"strconv"
	"strings"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/i18n"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/queue"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/worker/registry"
)

// Bounds of the lists of the judges page.
const (
	judgesNextJobs = 20  // per priority
	judgesInFlight = 200 // per priority
	judgesDead     = 50
)

// priorityNames name the priorities of the job queue (job.Priority*).
var priorityNames = []string{N("Extra high"), N("High"), N("Medium"), N("Low"), N("Extra low")}

// jobHTML describes an operation with links, as repr_job of aws_utils.js
// does on the overview.
func jobHTML(c *Ctx, op job.Operation) template.HTML {
	kind := N("Compiling")
	if op.Type.IsEvaluation() {
		kind = N("Evaluating")
	}
	var s string
	if op.Type.ForSubmission() {
		s = i18n.Format(c.T(`%(job)s the <a href="%(result_url)s">result</a> of <a href="%(submission_url)s">submission %(submission)s</a> on <a href="%(dataset_url)s">dataset %(dataset)s</a>`),
			map[string]any{"job": c.T(kind), "result_url": c.URL("submission", op.ObjectID, op.DatasetID),
				"submission_url": c.URL("submission", op.ObjectID), "submission": op.ObjectID,
				"dataset_url": c.URL("dataset", op.DatasetID), "dataset": op.DatasetID})
		if op.TestcaseCodename != "" {
			s += i18n.Format(c.T(" [testcase: `%(codename)s']"), map[string]any{"codename": template.HTMLEscapeString(op.TestcaseCodename)})
		}
	} else {
		s = i18n.Format(c.T(`%(job)s the result of user_test %(user_test)s on <a href="%(dataset_url)s">dataset %(dataset)s</a>`),
			map[string]any{"job": c.T(kind), "user_test": op.ObjectID, "dataset_url": c.URL("dataset", op.DatasetID),
				"dataset": op.DatasetID})
	}
	return template.HTML(s)
}

// payloadHTML describes the job of a queue message (its payload as it
// is when it cannot be read).
func payloadHTML(c *Ctx, payload []byte) template.HTML {
	j, err := job.DecodeJob(payload)
	if err != nil {
		p := string(payload)
		if len(p) > 200 {
			p = p[:200] + "…"
		}
		return template.HTML(template.HTMLEscapeString(p))
	}
	return jobHTML(c, j.Operation)
}

// judgeJob is a job running on a worker.
type judgeJob struct {
	Slot    int
	HTML    template.HTML
	Attempt int
	// Age is how long it has been running.
	Age time.Duration
}

// AgeText is Age as the page shows it.
func (j judgeJob) AgeText() string { return formatAge(j.Age) }

// judgeWorker is a row of the workers table.
type judgeWorker struct {
	registry.Info
	// InRegistry: the worker runs (false: it is only in the disabled
	// set).
	InRegistry bool
	// State is "running", "draining", "broken", "disabled" or "stopped".
	State   string
	Current []judgeJob
	// LastSeen is the age of its heartbeat.
	LastSeen time.Duration
}

// LastSeenText is LastSeen as the page shows it.
func (w judgeWorker) LastSeenText() string { return formatAge(w.LastSeen) }

// judgePriority is a row of the queue table.
type judgePriority struct {
	Priority      int
	Name          string
	Ready, Active int64
	Oldest        time.Duration
}

// OldestText is Oldest as the page shows it ("" when nothing waits).
func (p judgePriority) OldestText() string {
	if p.Ready == 0 {
		return ""
	}
	return formatAge(p.Oldest)
}

// judgeQueued is a job of the queue (waiting, in progress or dead).
type judgeQueued struct {
	ID       string
	Priority int
	HTML     template.HTML
	Attempt  int
	// Age is the time it has waited (ready), run without news (in
	// progress: idle) or been dead.
	Age time.Duration
	// Consumer is the worker slot holding it ("name/slot").
	Consumer   string
	Deliveries int
	Reason     string
}

// AgeText is Age as the page shows it.
func (j judgeQueued) AgeText() string { return formatAge(j.Age) }

// judgesData is the data of judges.html.
type judgesData struct {
	*Page
	// Unavailable: Valkey cannot be reached.
	Unavailable string
	Workers     []judgeWorker
	Stats       queue.Stats
	Priorities  []judgePriority
	Next        []judgeQueued
	InFlight    []judgeQueued
	Dead        []judgeQueued
}

// judgesPage is GET /judges.
func (s *Server) judgesPage(c *Ctx) {
	p, err := c.Page()
	if err != nil {
		c.Fail(err)
		return
	}
	d := judgesData{Page: p}
	if err := s.loadJudges(c, &d); err != nil {
		s.log.Warn("judges page: Valkey unavailable", "err", err)
		d.Unavailable = c.T("The queues and the workers cannot be read: Valkey is not reachable.")
	}
	c.Render("judges.html", d)
}

// loadJudges reads the workers and the queue.
func (s *Server) loadJudges(c *Ctx, d *judgesData) error {
	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()
	jq, err := s.Jobs(ctx)
	if err != nil {
		return err
	}
	infos, err := registry.List(ctx, s.kv)
	if err != nil {
		return err
	}
	disabled, err := registry.DisabledNames(ctx, s.kv)
	if err != nil {
		return err
	}
	now := s.clock()
	// Jobs in progress by consumer ("name/slot"): their description.
	running := map[string]job.Operation{}
	for _, w := range infos {
		jw := judgeWorker{Info: w, InRegistry: true, LastSeen: max(now.Sub(w.Heartbeat), 0)}
		switch {
		case w.Draining:
			jw.State = "draining"
		case w.Error != "":
			jw.State = "broken"
		case w.Disabled || disabled[w.Name]:
			jw.State = "disabled"
		default:
			jw.State = "running"
		}
		for _, j := range w.Jobs {
			jw.Current = append(jw.Current, judgeJob{Slot: j.Slot, HTML: jobHTML(c, j.Operation), Attempt: j.Attempt,
				Age: max(now.Sub(j.StartedAt), 0)})
			running[w.Name+"/"+strconv.Itoa(j.Slot)] = j.Operation
		}
		delete(disabled, w.Name)
		d.Workers = append(d.Workers, jw)
	}
	for name := range disabled {
		d.Workers = append(d.Workers, judgeWorker{Info: registry.Info{Name: name, Disabled: true}, State: "stopped"})
	}
	if d.Stats, err = jq.Stats(ctx); err != nil {
		return err
	}
	for _, ps := range d.Stats.Priorities {
		name := strconv.Itoa(ps.Priority)
		if ps.Priority < len(priorityNames) {
			name = c.T(priorityNames[ps.Priority])
		}
		d.Priorities = append(d.Priorities, judgePriority{Priority: ps.Priority, Name: name, Ready: ps.Ready,
			Active: ps.Pending, Oldest: ps.OldestReady})
	}
	next, err := jq.Peek(ctx, judgesNextJobs)
	if err != nil {
		return err
	}
	for _, m := range next {
		d.Next = append(d.Next, judgeQueued{ID: m.ID, Priority: m.Priority, HTML: payloadHTML(c, m.Payload),
			Attempt: m.Attempt, Age: max(now.Sub(m.EnqueuedAt), 0)})
	}
	inflight, err := jq.InFlight(ctx, judgesInFlight)
	if err != nil {
		return err
	}
	for _, f := range inflight {
		row := judgeQueued{ID: f.ID, Priority: f.Priority, Age: f.Idle, Consumer: f.Consumer, Deliveries: f.Deliveries}
		if op, ok := running[f.Consumer]; ok {
			row.HTML = jobHTML(c, op)
		}
		d.InFlight = append(d.InFlight, row)
	}
	dead, err := jq.ListDead(ctx, judgesDead)
	if err != nil {
		return err
	}
	for _, x := range dead {
		d.Dead = append(d.Dead, judgeQueued{ID: x.ID, Priority: x.Priority, HTML: payloadHTML(c, x.Payload),
			Attempt: x.Attempt, Age: max(now.Sub(x.DeadAt), 0), Deliveries: x.Deliveries, Reason: x.Reason})
	}
	return nil
}

// judgesJobPost is POST /judges/job: requeue (give back to the queue at
// once, for another worker) or drop (remove from the queue; the
// dispatcher's sweep enqueues the work again if it is still needed) a
// job of the queue, ready or in progress.
func (s *Server) judgesJobPost(c *Ctx) {
	action, id := c.ArgDefault("action", ""), c.ArgDefault("id", "")
	prio, perr := strconv.Atoi(c.ArgDefault("priority", ""))
	if (action != "requeue" && action != "drop") || id == "" || perr != nil {
		c.BadRequest()
		return
	}
	err := s.judgesJobAction(c, action, prio, id)
	if err == nil {
		err = c.Write(func(tx *Tx) error {
			return tx.Audit(AuditEntry{Action: "job." + action, TargetType: "job",
				Details: map[string]any{"message": id, "priority": prio}})
		})
	}
	if errors.Is(err, queue.ErrNoMessage) {
		c.NotifyError(N("Operation failed."), c.T("The job is not in the queue any more."))
	} else {
		c.Commit(err)
	}
	c.Redirect(c.URL("judges"))
}

func (s *Server) judgesJobAction(c *Ctx, action string, prio int, id string) error {
	jq, err := s.Jobs(c.Context())
	if err != nil {
		return err
	}
	msg, err := jq.Get(c.Context(), prio, id)
	if err != nil {
		return err
	}
	if action == "drop" {
		return jq.Ack(c.Context(), msg)
	}
	if msg.Consumer == "" {
		// Waiting already: nothing to do.
		return nil
	}
	err = jq.Nack(c.Context(), msg)
	if errors.Is(err, queue.ErrNotOwner) {
		return queue.ErrNoMessage
	}
	return err
}

// judgesDeadPost is POST /judges/dead: requeue or delete a dead letter.
func (s *Server) judgesDeadPost(c *Ctx) {
	action, id := c.ArgDefault("action", ""), c.ArgDefault("id", "")
	if (action != "requeue" && action != "delete") || id == "" {
		c.BadRequest()
		return
	}
	jq, err := s.Jobs(c.Context())
	if err == nil {
		if action == "requeue" {
			_, err = jq.RequeueDead(c.Context(), id)
		} else {
			err = jq.DeleteDead(c.Context(), id)
		}
	}
	if err == nil {
		err = c.Write(func(tx *Tx) error {
			return tx.Audit(AuditEntry{Action: "dead_letter." + action, TargetType: "job",
				Details: map[string]any{"message": id}})
		})
	}
	if errors.Is(err, queue.ErrNoDeadLetter) {
		c.NotifyError(N("Operation failed."), c.T("The job is not in the queue any more."))
	} else {
		c.Commit(err)
	}
	c.Redirect(c.URL("judges"))
}

// rpcEnableWorker is EvaluationService.enable_worker(shard).
func (s *Server) rpcEnableWorker(c *Ctx, args json.RawMessage) (any, error) {
	return s.setWorkerDisabled(c, args, false)
}

// rpcDisableWorker is EvaluationService.disable_worker(shard): the worker
// finishes its running jobs and takes no new ones (CMS gives its jobs to
// other workers at once; here the judges page can requeue them).
func (s *Server) rpcDisableWorker(c *Ctx, args json.RawMessage) (any, error) {
	return s.setWorkerDisabled(c, args, true)
}

func (s *Server) setWorkerDisabled(c *Ctx, args json.RawMessage, disable bool) (any, error) {
	name, err := workerName(args)
	if err != nil {
		return nil, err
	}
	if disable {
		infos, err := registry.List(c.Context(), s.kv)
		if err != nil {
			return nil, ErrRPCUnavailable
		}
		known := false
		for _, w := range infos {
			known = known || w.Name == name
		}
		if !known {
			return nil, errors.New("No worker named " + name + ".")
		}
	}
	changed, err := registry.SetDisabled(c.Context(), s.kv, name, disable)
	if err != nil {
		return nil, ErrRPCUnavailable
	}
	if !changed {
		if disable {
			return nil, errors.New("Trying to disable already disabled worker " + name + ".")
		}
		return nil, errors.New("Trying to enable worker " + name + " which is not disabled.")
	}
	verb, state := "enable", live.WorkerUp
	if disable {
		verb, state = "disable", live.WorkerDisabled
	}
	err = c.Write(func(tx *Tx) error {
		tx.Publish(live.Worker{Name: name, State: state}.Event())
		return tx.Audit(AuditEntry{Action: "worker." + verb, TargetType: "worker", Details: map[string]any{"worker": name}})
	})
	if err != nil {
		s.log.Error("cannot record the audit entry of a worker change", "err", err)
	}
	s.log.Info("worker "+verb+"d", "worker", name, "admin", c.Admin.Username)
	return nil, nil
}

// formatAge writes a duration as the judges page shows it ("1h 2m 3s").
func formatAge(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Second {
		return "0s"
	}
	var b strings.Builder
	if h := d / time.Hour; h > 0 {
		b.WriteString(strconv.Itoa(int(h)) + "h ")
		d -= h * time.Hour
	}
	if m := d / time.Minute; m > 0 || b.Len() > 0 {
		b.WriteString(strconv.Itoa(int(m)) + "m ")
		d -= m * time.Minute
	}
	b.WriteString(strconv.Itoa(int(d/time.Second)) + "s")
	return b.String()
}
