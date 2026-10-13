// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package dispatcher

import (
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/queue"
)

func itoa(n int32) string { return strconv.Itoa(int(n)) }

// TestJudgeSubmissions: a notified submission is compiled, evaluated on
// every testcase and scored; the live events, the scoreboard hook and the
// metrics say so.
func TestJudgeSubmissions(t *testing.T) {
	e := newEnv(t)
	task, ds := e.task(5)
	alice := e.participant("alice")
	subs := e.submit(alice, task, 3)
	// Notified before the dispatcher starts: it handles the notification
	// before its first sweep, with the contestant priorities.
	e.notify(judging.Event{Kind: judging.EventSubmission, SubmissionIDs: subs})
	opts := e.options("a")
	r := e.start(opts)
	w := e.startWorkers(2, allCorrect)
	e.waitScored(ds, 3, 10*time.Second)

	for _, s := range subs {
		res := e.result(s, ds)
		if res.Score == nil || *res.Score != 100 || res.Evaluations != 5 || res.Generation != 0 {
			t.Errorf("submission %d: %+v", s, res)
		}
		st := e.live.submissionStatuses(s, ds)
		if !slices.Equal(st, []string{live.SubmissionEvaluating, live.SubmissionScored}) {
			t.Errorf("submission %d: live statuses %v", s, st)
		}
	}
	if got := w.count(); got != 3*6 {
		t.Errorf("%d jobs ran, want 18", got)
	}
	if twice := w.ranTwice(); len(twice) > 0 {
		t.Errorf("jobs ran twice: %v", twice)
	}
	// The task score: partial events, then the final one, and a
	// scoreboard event for the contest.
	var taskScores, boards int
	for _, s := range e.live.all() {
		switch p := s.ev.Data.(type) {
		case live.TaskScore:
			taskScores++
			if p.Contest != int64(e.contest) || p.Participation != int64(alice) || p.Task != int64(task) {
				t.Errorf("task score event %+v", p)
			}
		case live.ScoreboardChange:
			boards++
			if p.Contest != int64(e.contest) || !slices.Contains(p.Participations, int64(alice)) {
				t.Errorf("scoreboard event %+v", p)
			}
		}
	}
	if taskScores == 0 || boards == 0 {
		t.Errorf("%d task score and %d scoreboard events", taskScores, boards)
	}
	if got := scalar[float64](e, `SELECT score FROM task_scores WHERE participation_id = $1 AND task_id = $2`, alice, task); got != 100 {
		t.Errorf("cached task score %v", got)
	}
	// The scoreboard hook got every scored submission.
	scored := map[int32]bool{}
	for _, u := range e.scores.all() {
		for _, s := range u.Submissions {
			if s.Scored && s.Official && s.Active {
				scored[s.SubmissionID] = true
			}
		}
	}
	if len(scored) != 3 {
		t.Errorf("scoreboard hook: %v", scored)
	}
	reg := opts.Metrics
	e.eventually(5*time.Second, "metrics", func() bool {
		return counter(t, reg, "cms_dispatcher_results_total", "outcome", "applied") == 18
	})
	if n := counter(t, reg, "cms_dispatcher_jobs_enqueued_total", "priority", "0"); n != 3 {
		t.Errorf("%v compilations enqueued at priority 0", n)
	}
	if n := counter(t, reg, "cms_dispatcher_jobs_enqueued_total", "priority", "1"); n != 15 {
		t.Errorf("%v evaluations enqueued at priority 1", n)
	}
	if n := counter(t, reg, "cms_dispatcher_submission_score_seconds"); n != 3 {
		t.Errorf("%v latencies observed", n)
	}
	if n := counter(t, reg, "cms_dispatcher_leader"); n != 1 || !r.d.Leading() {
		t.Errorf("leader gauge %v", n)
	}
	// The admin counters went out, and end at zero.
	e.eventually(5*time.Second, "judging counters back to zero", func() bool {
		var last *live.Judging
		for _, s := range e.live.all() {
			if p, ok := s.ev.Data.(live.Judging); ok && p.Contest == 0 {
				last = &p
			}
		}
		return last != nil && last.Counters[live.CounterCompiling] == 0 && last.Counters[live.CounterEvaluating] == 0 &&
			last.Counters[live.CounterQueued] == 0 && last.Counters[live.CounterInFlight] == 0
	})
}

// TestUserTestsAndCompilationErrors: a user test is compiled and run; a
// submission that does not compile is scored 0 without evaluations.
func TestUserTestsAndCompilationErrors(t *testing.T) {
	e := newEnv(t)
	task, ds := e.task(3)
	p := e.participant("bob")
	bad := e.submit(p, task, 1)[0]
	ut := e.userTest(p, task)
	e.start(e.options("a"))
	e.startWorkers(1, func(j *job.Job) string {
		if j.Operation.Type == job.Compilation {
			return job.CompilationFail
		}
		return allCorrect(j)
	})
	e.notify(judging.Event{Kind: judging.EventSubmission, SubmissionIDs: []int32{bad}},
		judging.Event{Kind: judging.EventUserTest, UserTestIDs: []int32{ut}})
	e.waitScored(ds, 1, 10*time.Second)
	if r := e.result(bad, ds); *r.CompilationOutcome != job.CompilationFail || *r.Score != 0 || r.Evaluations != 0 {
		t.Errorf("compilation error: %+v", r)
	}
	e.eventually(10*time.Second, "the user test", func() bool {
		return scalar[bool](e, `SELECT count(*) = 1 FROM user_test_results WHERE user_test_id = $1 AND evaluation_outcome IS NOT NULL`, ut)
	})
	var statuses []string
	for _, s := range e.live.all() {
		if u, ok := s.ev.Data.(live.UserTest); ok && u.ID == int64(ut) {
			statuses = append(statuses, u.Status)
		}
	}
	if !slices.Equal(statuses, []string{live.UserTestEvaluating, live.UserTestEvaluated}) {
		t.Errorf("user test statuses %v", statuses)
	}
	if st := e.live.submissionStatuses(bad, ds); !slices.Equal(st, []string{live.SubmissionCompilationFailed}) {
		t.Errorf("compilation error statuses %v", st)
	}
}

// TestRetryThenSuccess: infrastructure failures are retried with a
// backoff, counted as tries, and the submission ends scored.
func TestRetryThenSuccess(t *testing.T) {
	e := newEnv(t)
	task, ds := e.task(3)
	sub := e.submit(e.participant("carol"), task, 1)[0]
	opts := e.options("a")
	e.start(opts)
	// The first attempt of the compilation and of testcase 002 fail.
	e.startWorkers(2, func(j *job.Job) string {
		if j.Attempt == 1 && (j.Operation.Type == job.Compilation || j.Operation.TestcaseCodename == "002") {
			return ""
		}
		return allCorrect(j)
	})
	e.notify(judging.Event{Kind: judging.EventSubmission, SubmissionIDs: []int32{sub}})
	e.waitScored(ds, 1, 10*time.Second)
	// compilation_tries is the attempt that compiled (as upstream counts
	// every attempt), evaluation_tries the one that failed.
	r := e.result(sub, ds)
	if r.CompilationTries != 2 || r.EvaluationTries != 1 || *r.Score != 100 || r.Evaluations != 3 {
		t.Errorf("result %+v", r)
	}
	if n := counter(t, opts.Metrics, "cms_dispatcher_results_total", "outcome", "failed"); n != 2 {
		t.Errorf("%v failed results counted", n)
	}
}

// TestMaxTries: a compilation that always fails is tried
// max_compilation_tries times, then left "compiling" as upstream does;
// reevaluating it (an admin) judges it again.
func TestMaxTries(t *testing.T) {
	e := newEnv(t)
	task, ds := e.task(2)
	sub := e.submit(e.participant("dave"), task, 1)[0]
	opts := e.options("a")
	e.start(opts)
	var broken atomic.Bool
	broken.Store(true)
	w := e.startWorkers(1, func(j *job.Job) string {
		if broken.Load() {
			return ""
		}
		return allCorrect(j)
	})
	e.notify(judging.Event{Kind: judging.EventSubmission, SubmissionIDs: []int32{sub}})
	e.eventually(10*time.Second, "giving up", func() bool {
		return counter(t, opts.Metrics, "cms_dispatcher_gave_up_total", "operation", string(job.Compilation)) == 1
	})
	time.Sleep(300 * time.Millisecond) // nothing else happens
	if r := e.result(sub, ds); r.CompilationTries != judging.MaxCompilationTries || r.CompilationOutcome != nil {
		t.Errorf("result %+v", r)
	}
	if n := w.count(); n != judging.MaxCompilationTries {
		t.Errorf("%d attempts", n)
	}
	// A sweep does not retry it either.
	e.notify(judging.Event{Kind: judging.EventSweep})
	time.Sleep(300 * time.Millisecond)
	if n := w.count(); n != judging.MaxCompilationTries {
		t.Errorf("%d attempts after a sweep", n)
	}
	// The admin reevaluates it once the judges work again.
	broken.Store(false)
	inv, err := e.judge().Invalidate(e.ctx, judging.Scope{SubmissionIDs: []int32{sub}}, judging.LevelCompilation)
	if err != nil {
		t.Fatal(err)
	}
	e.notify(inv.Events(false)...)
	e.waitScored(ds, 1, 10*time.Second)
	if r := e.result(sub, ds); *r.Score != 100 || r.Generation != 1 {
		t.Errorf("after reevaluation %+v", r)
	}
}

// TestDuplicateAndStaleResults: a result delivered twice and a result of
// an older generation change nothing and are counted.
func TestDuplicateAndStaleResults(t *testing.T) {
	e := newEnv(t)
	task, ds := e.task(2)
	sub := e.submit(e.participant("erin"), task, 1)[0]
	opts := e.options("a")
	e.start(opts)
	e.startWorkers(1, allCorrect)
	e.notify(judging.Event{Kind: judging.EventSubmission, SubmissionIDs: []int32{sub}})
	e.waitScored(ds, 1, 10*time.Second)
	before := e.result(sub, ds)

	// Rebuild the jobs as they were and send their results again: the
	// compilation and one evaluation (duplicates), and an evaluation of a
	// generation that does not exist yet (as if from the future: stale).
	jb := &job.Job{ID: "compile", Operation: job.Operation{Type: job.Compilation, ObjectID: sub, DatasetID: ds}, Attempt: 1}
	ev := &job.Job{ID: "evaluate", Operation: job.Operation{Type: job.Evaluation, ObjectID: sub, DatasetID: ds, TestcaseCodename: "001"}, Attempt: 1}
	old := &job.Job{ID: "old", Operation: ev.Operation, Attempt: 1, Generation: 7}
	for _, r := range []*job.Result{resultOf(jb, "w", job.CompilationOK), resultOf(ev, "w", "0.0"), resultOf(old, "w", "0.0")} {
		payload, err := r.Encode()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.results.Enqueue(e.ctx, 0, payload, queue.EnqueueOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	e.eventually(5*time.Second, "the duplicates", func() bool {
		return counter(t, opts.Metrics, "cms_dispatcher_results_total", "outcome", "duplicate") == 2 &&
			counter(t, opts.Metrics, "cms_dispatcher_results_total", "outcome", "stale") == 1
	})
	if after := e.result(sub, ds); *after.Score != *before.Score || after.Evaluations != before.Evaluations {
		t.Errorf("a duplicate changed the result: %+v -> %+v", before, after)
	}
}

// TestInvalidationDropsResultsInFlight: an admin reevaluates a submission
// while its evaluations run; the results of the old generation are
// dropped and the new ones are stored.
func TestInvalidationDropsResultsInFlight(t *testing.T) {
	e := newEnv(t)
	task, ds := e.task(4)
	sub := e.submit(e.participant("frank"), task, 1)[0]
	e.notify(judging.Event{Kind: judging.EventSubmission, SubmissionIDs: []int32{sub}})
	opts := e.options("a")
	e.start(opts)
	gate := make(chan struct{})
	var held atomic.Int32
	w := e.startWorkers(4, func(j *job.Job) string {
		if j.Operation.Type == job.Evaluation && j.Generation == 0 {
			held.Add(1)
			<-gate // evaluations of generation 0 wait for the invalidation
			return "0.0"
		}
		return allCorrect(j)
	})
	e.eventually(10*time.Second, "evaluations in flight", func() bool { return held.Load() == 4 })
	inv, err := e.judge().Invalidate(e.ctx, judging.Scope{SubmissionIDs: []int32{sub}}, judging.LevelEvaluation)
	if err != nil {
		t.Fatal(err)
	}
	e.notify(inv.Events(false)...)
	time.Sleep(100 * time.Millisecond)
	close(gate)
	e.waitScored(ds, 1, 10*time.Second)
	r := e.result(sub, ds)
	if *r.Score != 100 || r.Generation != 1 || r.Evaluations != 4 {
		t.Errorf("result %+v", r)
	}
	e.eventually(5*time.Second, "stale results counted", func() bool {
		return counter(t, opts.Metrics, "cms_dispatcher_results_total", "outcome", "stale") == 4
	})
	if twice := w.ranTwice(); len(twice) > 0 {
		t.Errorf("jobs ran twice: %v", twice)
	}
	// The re-evaluation had the background priority.
	if n := counter(t, opts.Metrics, "cms_dispatcher_jobs_enqueued_total", "priority", "2"); n != 4 {
		t.Errorf("%v re-evaluations at priority 2", n)
	}
}

// TestSweepFindsLostNotification: work nobody was told about is found by
// the sweep (the one at start, and the periodic ones).
func TestSweepFindsLostNotification(t *testing.T) {
	e := newEnv(t)
	task, ds := e.task(2)
	p := e.participant("gina")
	early := e.submit(p, task, 2) // before the dispatcher starts
	opts := e.options("a")
	opts.SweepInterval = 200 * time.Millisecond
	e.start(opts)
	w := e.startWorkers(1, allCorrect)
	e.waitScored(ds, 2, 10*time.Second)
	late := e.submit(p, task, 1) // never notified
	e.waitScored(ds, 3, 10*time.Second)
	for _, s := range append(early, late...) {
		if r := e.result(s, ds); *r.Score != 100 {
			t.Errorf("submission %d: %+v", s, r)
		}
	}
	if n := counter(t, opts.Metrics, "cms_dispatcher_sweep_found_total", "kind", "operations"); n != 3 {
		t.Errorf("sweeps found %v operations, want the 3 compilations", n)
	}
	if twice := w.ranTwice(); len(twice) > 0 {
		t.Errorf("jobs ran twice: %v", twice)
	}
	// The cached task scores of a dataset that is not active any more are
	// repaired by the sweep.
	other := e.dataset(task, 2, false)
	e.exec(`UPDATE tasks SET active_dataset_id = $1 WHERE id = $2`, other, task) // behind the dispatcher's back
	e.waitScored(other, 0, time.Second)
	e.eventually(10*time.Second, "the repaired task score", func() bool {
		return scalar[bool](e, `SELECT dataset_id = $1 AND partial FROM task_scores WHERE participation_id = $2 AND task_id = $3`,
			other, p, task)
	})
}

// TestDatasetChange: a new active dataset is judged after the
// notification, and the scoreboard hook is told to resend the task.
func TestDatasetChange(t *testing.T) {
	e := newEnv(t)
	task, ds := e.task(2)
	p := e.participant("hugo")
	subs := e.submit(p, task, 2)
	e.start(e.options("a"))
	e.startWorkers(2, allCorrect)
	e.notify(judging.Event{Kind: judging.EventSubmission, SubmissionIDs: subs})
	e.waitScored(ds, 2, 10*time.Second)
	auto := e.dataset(task, 3, true)
	next := e.dataset(task, 4, false)
	if _, _, err := e.judge().ActivateDataset(e.ctx, task, next); err != nil {
		t.Fatal(err)
	}
	e.notify(judging.Event{Kind: judging.EventDataset, ContestID: e.contest, TaskID: task})
	e.waitScored(next, 2, 10*time.Second)
	e.waitScored(auto, 2, 10*time.Second)
	if e.scored(ds) != 2 {
		t.Error("results on the old dataset lost")
	}
	tasks := 0
	for _, u := range e.scores.all() {
		if slices.Contains(u.Tasks, task) {
			tasks++
		}
	}
	if tasks != 1 {
		t.Errorf("the scoreboard hook was told %d times to resend the task", tasks)
	}
}

// TestDeadLetteredJobCountsAsTry: a job its workers kept abandoning (dead
// letter) is a failed attempt: it is retried, and the submission judged.
func TestDeadLetteredJobCountsAsTry(t *testing.T) {
	e := newEnv(t)
	task, ds := e.task(1)
	sub := e.submit(e.participant("iris"), task, 1)[0]
	opts := e.options("a")
	e.start(opts)
	e.notify(judging.Event{Kind: judging.EventSubmission, SubmissionIDs: []int32{sub}})
	// A worker takes the compilation and dies holding it, MaxDeliveries
	// times: the queue dead-letters it.
	po := JobQueueOptions
	po.Visibility, po.PollInterval = 20*time.Millisecond, 10*time.Millisecond
	poison, err := queue.Open(e.ctx, e.kv, job.JobQueue, po)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; ; i++ {
		if i > 200 {
			t.Fatal("the job never reached the dead letters")
		}
		if m, err := poison.Consume(e.ctx, "dying", 50*time.Millisecond); err != nil {
			t.Fatal(err)
		} else if m == nil {
			st, err := poison.Stats(e.ctx)
			if err != nil {
				t.Fatal(err)
			}
			if st.Dead > 0 || counter(t, opts.Metrics, "cms_dispatcher_dead_letters_total", "queue", "jobs") > 0 {
				break
			}
		}
	}
	// It may hold the retry already: give it back.
	if _, err := poison.Release(e.ctx, "dying"); err != nil {
		t.Fatal(err)
	}
	e.startWorkers(1, allCorrect)
	e.waitScored(ds, 1, 10*time.Second)
	if r := e.result(sub, ds); r.CompilationTries != 2 || *r.Score != 100 {
		t.Errorf("result %+v", r)
	}
	if n := counter(t, opts.Metrics, "cms_dispatcher_dead_letters_total", "queue", "jobs"); n != 1 {
		t.Errorf("%v dead letters handled", n)
	}
}

// TestPriorities: the jobs of contestant submissions are served before
// user tests and re-evaluations, the oldest submission first.
func TestPriorities(t *testing.T) {
	e := newEnv(t)
	task, ds := e.task(1)
	p := e.participant("jack")
	judged := e.submit(p, task, 1)[0]
	e.start(e.options("a"))
	w := e.startWorkers(1, allCorrect)
	e.notify(judging.Event{Kind: judging.EventSubmission, SubmissionIDs: []int32{judged}})
	e.waitScored(ds, 1, 10*time.Second)
	// From now on no worker runs the jobs.
	w.stop()
	ut := e.userTest(p, task)
	subs := e.submit(p, task, 3)
	inv, err := e.judge().Invalidate(e.ctx, judging.Scope{SubmissionIDs: []int32{judged}}, judging.LevelCompilation)
	if err != nil {
		t.Fatal(err)
	}
	// Notified in the worst order: re-evaluation, user test, then the
	// new submissions newest first.
	e.notify(inv.Events(false)...)
	e.notify(judging.Event{Kind: judging.EventUserTest, UserTestIDs: []int32{ut}})
	for _, s := range []int32{subs[2], subs[1], subs[0]} {
		e.notify(judging.Event{Kind: judging.EventSubmission, SubmissionIDs: []int32{s}})
	}
	e.eventually(5*time.Second, "5 queued jobs", func() bool {
		st, err := e.jobs.Stats(e.ctx)
		return err == nil && st.Ready == 5
	})
	var order []string
	for range 5 {
		m, err := e.jobs.Consume(e.ctx, "inspector", 0)
		if err != nil || m == nil {
			t.Fatal(m, err)
		}
		jb, err := job.DecodeJob(m.Payload)
		if err != nil {
			t.Fatal(err)
		}
		order = append(order, strings.TrimSuffix(jb.Operation.Key(), "/"+itoa(ds)))
	}
	// The submissions first (in the order the notifications were handled:
	// one at a time, or together sorted oldest first), then the user test
	// and the re-evaluation.
	first := slices.Sorted(slices.Values(order[:3]))
	want := slices.Sorted(slices.Values([]string{"compile/" + itoa(subs[0]), "compile/" + itoa(subs[1]), "compile/" + itoa(subs[2])}))
	if !slices.Equal(first, want) {
		t.Errorf("submissions first: %v", order)
	}
	rest := slices.Sorted(slices.Values(order[3:]))
	if !slices.Equal(rest, []string{"compile/" + itoa(judged), "compile_test/" + itoa(ut)}) {
		t.Errorf("then the user test and the re-evaluation: %v", order)
	}
}

// TestLargeReevaluation: a reevaluation of many submissions is read and
// built in chunks, with results stored in between; nothing runs twice.
func TestLargeReevaluation(t *testing.T) {
	defer func(p, s int) { pendingChunk, scheduleChunk = p, s }(pendingChunk, scheduleChunk)
	pendingChunk, scheduleChunk = 7, 10
	e := newEnv(t)
	task, ds := e.task(3)
	var subs []int32
	for i := range 6 {
		subs = append(subs, e.submit(e.participant("user"+itoa(int32(i))), task, 5)...)
	}
	e.notify(judging.Event{Kind: judging.EventSubmission, SubmissionIDs: subs})
	e.start(e.options("a"))
	w := e.startWorkers(3, allCorrect)
	e.waitScored(ds, 30, 20*time.Second)
	inv, err := e.judge().Invalidate(e.ctx, judging.Scope{ContestID: e.contest}, judging.LevelEvaluation)
	if err != nil {
		t.Fatal(err)
	}
	e.notify(inv.Events(false)...)
	e.eventually(20*time.Second, "the reevaluation", func() bool {
		return scalar[int64](e, `SELECT count(*) FROM submission_results WHERE generation = 1 AND score = 100`) == 30
	})
	if n := w.count(); n != 30*4+30*3 {
		t.Errorf("%d jobs ran, want %d", n, 30*4+30*3)
	}
	if twice := w.ranTwice(); len(twice) > 0 {
		t.Errorf("jobs ran twice: %v", twice)
	}
}
