//go:build sandbox

// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package dispatcher

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/steps"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/steps/stepstest"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/tasktype"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/queue"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/worker"
)

// Run with `make test-sandbox` (root, isolate, PostgreSQL and Valkey);
// CMS_SANDBOX_CGROUPS=0 without cgroups v2.

// liveLog collects the live events a web process receives: a hub on
// Valkey (what contest-web and admin-web run) subscribed to every topic
// of the contest.
type liveLog struct {
	mu   sync.Mutex
	msgs []*live.Message
}

func (l *liveLog) run(ctx context.Context, sub *live.Subscription) {
	for {
		m, err := sub.Next(ctx)
		if err != nil {
			return
		}
		l.mu.Lock()
		l.msgs = append(l.msgs, m)
		l.mu.Unlock()
	}
}

func (l *liveLog) all() []*live.Message {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.msgs)
}

// decode returns the payloads of the events named event on topic.
func liveEvents[T any](t testing.TB, msgs []*live.Message, topic live.Topic, event string) []T {
	t.Helper()
	var out []T
	for _, m := range msgs {
		if m.Topic != topic || m.Event != event {
			continue
		}
		var v T
		if err := json.Unmarshal(m.Data, &v); err != nil {
			t.Fatalf("event %s on %s: %v: %s", event, topic, err, m.Data)
		}
		out = append(out, v)
	}
	return out
}

func testLogger() *slog.Logger {
	if os.Getenv("CMS_TEST_DISPATCHER_LOG") != "" {
		return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestEndToEnd judges a contest with the dispatcher and a worker running
// in this process on real isolate boxes: submissions in C++ and Python 3
// that are correct, partially correct (GroupMin), wrong, too slow, killed
// by a signal, exit with an error or do not compile, and user tests. It
// checks the stored results and scores, the task score cache, the queues
// left empty and the live events a web process receives through Valkey.
func TestEndToEnd(t *testing.T) {
	sb := stepstest.Sandbox(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := testutil.DB(t)
	kvc := testutil.Redis(t)
	// The dispatcher has a pool and a Valkey client of its own, which
	// count its round trips.
	dpool, dqc := countingPool(t, pool)
	dkv, dkc := countingKV(t, kvc)
	blobs := must[*blob.Local](t)(blob.NewLocal(t.TempDir()))
	log := testLogger()
	langs := language.Builtin()
	users := []string{"alice", "bob", "carol", "dave", "erin", "frank", "grace", "heidi", "ivan"}
	c := newSumContest(t, pool, kvc, blobs, false, users...)

	jobs := must[*queue.Queue](t)(queue.Open(ctx, dkv, job.JobQueue, JobQueueOptions))
	results := must[*queue.Queue](t)(queue.Open(ctx, dkv, job.ResultQueue, ResultQueueOptions))
	wjobs := must[*queue.Queue](t)(queue.Open(ctx, kvc, job.JobQueue, JobQueueOptions))
	wresults := must[*queue.Queue](t)(queue.Open(ctx, kvc, job.ResultQueue, ResultQueueOptions))

	// What a web process runs: a hub on Valkey, subscribed to the topics
	// of the contest's pages. The dispatcher publishes through a real
	// Publisher.
	pub := live.NewPublisher(dkv, log, nil, live.PublisherOptions{})
	hub := live.NewHub(kvc, log, nil, live.HubOptions{Buffer: 1 << 14})
	var bg sync.WaitGroup
	liveCtx, stopLive := context.WithCancel(context.Background())
	bg.Go(func() { _ = pub.Run(liveCtx) })
	bg.Go(func() { _ = hub.Run(liveCtx) })
	t.Cleanup(func() { stopLive(); bg.Wait() })
	deadline := time.Now().Add(10 * time.Second)
	for !hub.Connected() {
		if time.Now().After(deadline) {
			t.Fatal("the hub does not connect to Valkey")
		}
		time.Sleep(10 * time.Millisecond)
	}
	topics := []live.Topic{live.AdminTopic, live.AdminContestTopic(int64(c.contest.ID)),
		live.ScoreboardTopic(int64(c.contest.ID)), live.ContestTopic(int64(c.contest.ID))}
	for _, u := range users {
		topics = append(topics, live.ParticipationTopic(int64(c.parts[u].ID)))
	}
	events := &liveLog{}
	sub := hub.Subscribe(topics, "")
	bg.Go(func() { events.run(liveCtx, sub) })

	// The dispatcher.
	reg := prometheus.NewRegistry()
	d, err := New(dpool, dkv, jobs, results, judging.New(dpool, judging.Options{Languages: langs, Logger: log}), Options{
		Instance: "e2e", IdleWait: 20 * time.Millisecond, ProgressInterval: 100 * time.Millisecond,
		DeadLetterInterval: time.Second, Live: pub, Log: log, Metrics: reg,
	})
	if err != nil {
		t.Fatal(err)
	}
	dctx, stopDispatcher := context.WithCancel(ctx)
	dDone := make(chan error, 1)
	go func() { dDone <- d.Run(dctx) }()
	t.Cleanup(func() { stopDispatcher(); <-dDone })
	for !d.Leading() {
		if time.Now().After(deadline) {
			t.Fatal("the dispatcher does not take the lease")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Let it publish its first worker list (the baseline of the "worker"
	// events) before the worker starts.
	waitFor(t, 10*time.Second, "the first judging counters", func() bool {
		return len(liveEvents[live.Judging](t, events.all(), live.AdminTopic, live.EventJudging)) > 0
	})

	// The worker, as `cms worker` builds it.
	stepsCfg := steps.ConfigFrom(config.Default())
	stepsCfg.Cgroups = stepstest.Cgroups()
	stepsCfg.Log = log
	env := &tasktype.Env{Boxes: tasktype.SandboxBoxes(sb), Store: blobs, Languages: langs, Steps: &stepsCfg, Log: log}
	cache := worker.NewCache(kvc, blobs, langs, worker.CacheOptions{TTL: time.Hour,
		Limits: config.Default().Sandbox.Compilation, Cgroups: stepsCfg.Cgroups, Log: log})
	const workerName = "e2e-judge"
	w, err := worker.New(kvc, wjobs, wresults, worker.TaskTypes(env), worker.Options{
		Name: workerName, Concurrency: 3, Boxes: 6, Cache: cache, Ready: sb.SelfTest,
		PollTimeout: 200 * time.Millisecond, ShutdownTimeout: 5 * time.Second, Log: log, Metrics: prometheus.NewRegistry(),
	})
	if err != nil {
		t.Fatal(err)
	}
	wctx, stopWorker := context.WithCancel(ctx)
	wDone := make(chan error, 1)
	go func() { wDone <- w.Run(wctx) }()
	workerStopped := false
	t.Cleanup(func() {
		if !workerStopped {
			stopWorker()
			<-wDone
		}
	})

	// The contestants submit.
	plan := []struct{ user, sol string }{
		{"alice", "cpp_ok"}, {"bob", "py_ok"}, {"carol", "cpp_partial"}, {"dave", "cpp_tle"},
		{"erin", "py_exit"}, {"frank", "cpp_ce"}, {"grace", "cpp_segv"}, {"heidi", "py_ce"},
		{"alice", "cpp_wrong"}, {"heidi", "py_partial"}, {"ivan", "py_wrong"},
	}
	start := time.Now()
	queries0, trips0 := dqc.Count(), dkc.RoundTrips()
	subs := map[int32]solution{}
	var order []int32
	submitted := map[string][]solution{}
	for i, p := range plan {
		sol := solutionNamed(t, p.sol)
		id := c.submit(p.user, sol, i)
		subs[id] = sol
		order = append(order, id)
		submitted[p.user] = append(submitted[p.user], sol)
	}
	utOK := c.userTest("bob", solutionNamed(t, "py_ok"), "20 22\n")
	utCE := c.userTest("frank", solutionNamed(t, "cpp_ce"), "1 1\n")

	waitFor(t, 5*time.Minute, "every submission and user test judged", func() bool {
		return c.unfinished() == 0 && scalarOf[int64](t, c, `SELECT count(*) FROM user_test_results
			WHERE compilation_outcome = 'fail' OR evaluation_outcome IS NOT NULL`) == 2
	})
	elapsed := time.Since(start)
	queries, trips := dqc.Count()-queries0, dkc.RoundTrips()-trips0

	// Results and scores.
	state := c.snapshot()
	for _, id := range order {
		for _, e := range c.checkJudged(state[id], subs[id]) {
			t.Errorf("submission %d (%s): %s", id, subs[id].name, e)
		}
	}
	// The task score cache: the best submission of each participant.
	gotScores, wantScores := c.taskScores(), c.wantTaskScores(submitted)
	for _, u := range sortedKeys(wantScores) {
		g, ok := gotScores[u]
		want := wantScores[u]
		if !ok || g.Dataset == nil || *g.Dataset != *want.Dataset || g.Score != want.Score || g.Public != want.Public ||
			g.Tokened != 0 || g.Partial {
			t.Errorf("task score of %s: %+v (dataset %v), want %+v", u, g, deref(g.Dataset), want)
		}
	}
	if len(gotScores) != len(wantScores) {
		t.Errorf("%d cached task scores for %d participants", len(gotScores), len(wantScores))
	}
	// User tests: the output of the correct one, the compilation error of
	// the other.
	var out, evalText, compOK, compFail string
	if err := pool.QueryRow(ctx, `SELECT coalesce(output, ''), coalesce(evaluation_text[1], ''), compilation_outcome
		FROM user_test_results WHERE user_test_id = $1`, utOK).Scan(&out, &evalText, &compOK); err != nil {
		t.Fatal(err)
	}
	if compOK != "ok" || evalText != "Execution completed successfully" || out == "" || c.readBlob(out) != "42\n" {
		t.Errorf("user test: compilation %q, text %q, output %q", compOK, evalText, out)
	}
	if err := pool.QueryRow(ctx, `SELECT compilation_outcome FROM user_test_results WHERE user_test_id = $1`, utCE).
		Scan(&compFail); err != nil || compFail != "fail" {
		t.Errorf("user test with a compilation error: %q %v", compFail, err)
	}

	// Nothing is left in the queues.
	waitFor(t, 10*time.Second, "empty queues", func() bool {
		js, err1 := jobs.Stats(ctx)
		rs, err2 := results.Stats(ctx)
		return err1 == nil && err2 == nil && js.Ready+js.Pending+js.Delayed+js.Dead == 0 &&
			rs.Ready+rs.Pending+rs.Delayed+rs.Dead == 0
	})

	// The live events a web process received.
	contest := int64(c.contest.ID)
	waitFor(t, 10*time.Second, "the final judging counters", func() bool {
		js := liveEvents[live.Judging](t, events.all(), live.AdminTopic, live.EventJudging)
		if len(js) == 0 {
			return false
		}
		last := js[len(js)-1].Counters
		return last[live.CounterCompiling]+last[live.CounterEvaluating]+last[live.CounterScoring]+
			last[live.CounterQueued]+last[live.CounterInFlight] == 0 && last[live.CounterWorkers] == 1
	})
	msgs := events.all()
	for _, m := range msgs {
		if m.Event == live.EventResync {
			t.Errorf("resync marker received (%s): events were lost", m.Reason)
		}
	}
	for _, id := range order {
		sol := subs[id]
		p := int64(scalarOf[int32](t, c, `SELECT participation_id FROM submissions WHERE id = $1`, id))
		statuses := func(topic live.Topic) []string {
			var st []string
			for _, ev := range liveEvents[live.Submission](t, msgs, topic, live.EventSubmission) {
				if ev.ID == int64(id) {
					if ev.Dataset != int64(c.main.ID) || ev.Contest != contest || ev.Participation != p || ev.V != live.Version {
						t.Errorf("submission %d: event %+v", id, ev)
					}
					st = append(st, ev.Status)
				}
			}
			return st
		}
		want := []string{live.SubmissionEvaluating, live.SubmissionScored}
		if sol.verdict == vCE {
			want = []string{live.SubmissionCompilationFailed}
		}
		for _, topic := range []live.Topic{live.ParticipationTopic(p), live.AdminContestTopic(contest)} {
			if got := statuses(topic); !slices.Equal(got, want) {
				t.Errorf("submission %d (%s) on %s: statuses %v, want %v", id, sol.name, topic, got, want)
			}
		}
	}
	for _, u := range users {
		p := int64(c.parts[u].ID)
		if len(liveEvents[live.TaskScore](t, msgs, live.ParticipationTopic(p), live.EventTaskScore)) == 0 {
			t.Errorf("no task_score event for %s", u)
		}
	}
	// Scoreboard events are throttled (merged, at most one per second):
	// the last merge goes out up to a second after the last score.
	board := func() []int64 {
		var ids []int64
		for _, ev := range liveEvents[live.ScoreboardChange](t, events.all(), live.ScoreboardTopic(contest), live.EventScoreboard) {
			ids = append(ids, ev.Participations...)
		}
		slices.Sort(ids)
		return slices.Compact(ids)
	}
	waitFor(t, 5*time.Second, "scoreboard events naming every participant", func() bool { return len(board()) == len(users) })
	for ut, want := range map[int32]string{utOK: live.UserTestEvaluated, utCE: live.UserTestCompilationFailed} {
		var got []string
		for _, ev := range liveEvents[live.UserTest](t, msgs, live.AdminContestTopic(contest), live.EventUserTest) {
			if ev.ID == int64(ut) {
				got = append(got, ev.Status)
			}
		}
		if len(got) == 0 || got[len(got)-1] != want {
			t.Errorf("user test %d: statuses %v, want ending with %s", ut, got, want)
		}
	}
	workerStates := func() []string {
		var st []string
		for _, ev := range liveEvents[live.Worker](t, events.all(), live.AdminTopic, live.EventWorker) {
			if ev.Name == workerName {
				st = append(st, ev.State)
			}
		}
		return st
	}
	if st := workerStates(); !slices.Contains(st, live.WorkerUp) {
		t.Errorf("worker events %v, want up", st)
	}

	// Metrics of the dispatcher.
	if n := counter(t, reg, "cms_dispatcher_submission_score_seconds"); int(n) != len(order) {
		t.Errorf("%v verdict latencies observed for %d submissions", n, len(order))
	}
	for _, outcome := range []string{"duplicate", "stale", "error", "invalid"} {
		if n := counter(t, reg, "cms_dispatcher_results_total", "outcome", outcome); n != 0 {
			t.Errorf("%v results with outcome %s", n, outcome)
		}
	}

	// A stopping worker leaves the registry: the admin pages hear it.
	stopWorker()
	if err := <-wDone; err != nil {
		t.Errorf("worker: %v", err)
	}
	workerStopped = true
	if err := judging.Notify(ctx, kvc, judging.Event{Kind: judging.EventSweep}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, "the worker down event", func() bool {
		st := workerStates()
		return len(st) > 0 && st[len(st)-1] == live.WorkerDown
	})

	evs := 0
	for _, id := range order {
		if subs[id].verdict != vCE {
			evs += len(mainTestcases)
		}
	}
	t.Logf("%d submissions (%d evaluations) and 2 user tests judged in %v; %d live events received",
		len(order), evs, elapsed.Round(time.Millisecond), len(msgs))
	t.Logf("dispatcher round trips meanwhile (idle polling included): %d database queries, %d Valkey commands "+
		"(%.1f and %.1f per submission or user test)", queries, trips,
		float64(queries)/float64(len(order)+2), float64(trips)/float64(len(order)+2))
	t.Logf("verdict latency (notification to score): mean %.0f ms over %v submissions",
		1000*histogramSum(t, reg, "cms_dispatcher_submission_score_seconds")/counter(t, reg, "cms_dispatcher_submission_score_seconds"),
		counter(t, reg, "cms_dispatcher_submission_score_seconds"))
}

// waitFor waits until cond holds.
func waitFor(t testing.TB, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %v waiting for %s", timeout, what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// scalarOf runs a query that returns one value.
func scalarOf[T any](t testing.TB, c *sumContest, sql string, args ...any) T {
	t.Helper()
	var v T
	if err := c.st.Pool.QueryRow(c.ctx, sql, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", strings.TrimSpace(sql), err)
	}
	return v
}
