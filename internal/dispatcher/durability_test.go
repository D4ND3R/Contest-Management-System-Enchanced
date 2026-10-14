// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package dispatcher

import (
	"context"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/queue"
)

// expireLease makes the local deadline of the lease pass, as when Valkey
// lost the key and a standby took it: fence fails from now on.
func expireLease(l *Lease) {
	l.mu.Lock()
	l.deadline = time.Now().Add(-time.Millisecond)
	l.mu.Unlock()
}

// TestNextOperationsSurviveLeaseLoss: the operations that follow a
// committed batch of results (here the evaluations of a compiled
// submission) are enqueued even when the lease is lost (and the loop
// stopped) between the commit and the enqueue. The results are
// acknowledged, and a result received again would be a duplicate with no
// next operations, so nothing else would enqueue them before a periodic
// sweep.
func TestNextOperationsSurviveLeaseLoss(t *testing.T) {
	e := newEnv(t)
	task, ds := e.task(3)
	sub := e.submit(e.participant("lena"), task, 1)[0]
	l := e.manual(e.options("a"))
	e.notify(judging.Event{Kind: judging.EventSubmission, SubmissionIDs: []int32{sub}})
	msgs, err := judging.ReadEvents(e.ctx, e.kv, EventsConsumer, 10, 0, false)
	if err != nil || len(msgs) != 1 {
		t.Fatal(msgs, err)
	}
	if err := l.handleEvents(e.ctx, msgs); err != nil {
		t.Fatal(err)
	}
	if n := e.work(allCorrect); n != 1 {
		t.Fatalf("%d compilations", n)
	}

	ctx, cancel := context.WithCancel(e.ctx)
	defer cancel()
	l.d.hooks.afterCommit = func() bool {
		expireLease(l.d.lease)
		cancel() // what Lease.Keep does when it notices
		return false
	}
	if n, _, _, err := l.consumeResults(ctx, 0, time.Time{}); n != 1 || err != nil {
		t.Fatal(n, err)
	}
	if r := e.result(sub, ds); r == nil || r.CompilationOutcome == nil {
		t.Fatalf("compilation not stored: %+v", r)
	}
	st, err := e.jobs.Stats(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Ready != 3 {
		t.Errorf("%d evaluations enqueued after the lease was lost, want 3", st.Ready)
	}
	if rs, err := e.results.Stats(e.ctx); err != nil || rs.Ready+rs.Pending != 0 {
		t.Errorf("the result was not acknowledged: %+v %v", rs, err)
	}
}

// TestSweepAfterDeadLetter: a job its workers abandoned MaxDeliveries
// times gives its dedup key up when it is dead-lettered. A sweep before
// the periodic handling of dead letters must not enqueue the same attempt
// again (it would kill MaxDeliveries more workers, and the retry of the
// converted failure would then be a duplicate): the dead letter becomes a
// failed attempt first, and only its retry is enqueued.
func TestSweepAfterDeadLetter(t *testing.T) {
	e := newEnv(t)
	task, ds := e.task(1)
	sub := e.submit(e.participant("mona"), task, 1)[0]
	opts := e.options("a")
	opts.DeadLetterInterval = time.Hour
	l := e.manual(opts)
	e.notify(judging.Event{Kind: judging.EventSubmission, SubmissionIDs: []int32{sub}})
	msgs, err := judging.ReadEvents(e.ctx, e.kv, EventsConsumer, 10, 0, false)
	if err != nil || len(msgs) != 1 {
		t.Fatal(msgs, err)
	}
	if err := l.handleEvents(e.ctx, msgs); err != nil {
		t.Fatal(err)
	}
	dieHolding(t, e, 1)

	l.sweep.request(0)
	for l.sweep.active || len(l.sweep.queued) > 0 {
		if err := l.sweepStep(e.ctx); err != nil {
			t.Fatal(err)
		}
	}
	if r := e.result(sub, ds); r == nil || r.CompilationTries != 1 {
		t.Errorf("the dead letter is not a failed attempt: %+v", r)
	}
	if st, err := e.jobs.Stats(e.ctx); err != nil || st.Dead != 0 {
		t.Errorf("dead letters left: %+v %v", st, err)
	}
	m, err := e.jobs.Consume(e.ctx, "check", 2*time.Second)
	if err != nil || m == nil {
		t.Fatal("no job enqueued", err)
	}
	jb, err := job.DecodeJob(m.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if jb.Attempt != 2 {
		t.Errorf("the sweep enqueued attempt %d, want the retry (2)", jb.Attempt)
	}
	if m, err := e.jobs.Consume(e.ctx, "check", 100*time.Millisecond); err != nil || m != nil {
		t.Errorf("a second job: %+v %v", m, err)
	}
}

// dieHolding takes n jobs from the job queue with a consumer that dies
// holding each of them MaxDeliveries times, so the queue dead-letters them
// (a job that kills its workers).
func dieHolding(t *testing.T, e *env, n int) {
	t.Helper()
	po := JobQueueOptions
	po.Visibility, po.PollInterval = 20*time.Millisecond, 10*time.Millisecond
	poison, err := queue.Open(e.ctx, e.kv, job.JobQueue, po)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; ; i++ {
		if i > 500 {
			t.Fatal("the jobs never reached the dead letters")
		}
		st, err := poison.Stats(e.ctx)
		if err != nil {
			t.Fatal(err)
		}
		if st.Dead >= int64(n) {
			break
		}
		if _, err := poison.Consume(e.ctx, "dying", 50*time.Millisecond); err != nil {
			t.Fatal(err)
		}
	}
	if st, err := poison.Stats(e.ctx); err != nil || st.Ready+st.Pending+st.Delayed+st.DedupKeys != 0 {
		t.Fatalf("jobs left after the dead letters: %+v %v", st, err)
	}
}

// cutProxy forwards TCP connections to target until Cut: then it closes
// them and every new one (a network partition between one dispatcher and
// PostgreSQL).
type cutProxy struct {
	ln     net.Listener
	target string

	mu    sync.Mutex
	cut   bool
	conns []net.Conn
}

func newCutProxy(t *testing.T, target string) *cutProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &cutProxy{ln: ln, target: target}
	go p.serve()
	t.Cleanup(func() { _ = ln.Close(); p.Cut() })
	return p
}

func (p *cutProxy) serve() {
	for {
		c, err := p.ln.Accept()
		if err != nil {
			return
		}
		p.mu.Lock()
		if p.cut {
			p.mu.Unlock()
			_ = c.Close()
			continue
		}
		up, err := net.Dial("tcp", p.target)
		if err != nil {
			p.mu.Unlock()
			_ = c.Close()
			continue
		}
		p.conns = append(p.conns, c, up)
		p.mu.Unlock()
		go func() { _, _ = io.Copy(up, c); _ = up.Close() }()
		go func() { _, _ = io.Copy(c, up); _ = c.Close() }()
	}
}

// Cut closes every connection and refuses the next ones.
func (p *cutProxy) Cut() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cut = true
	for _, c := range p.conns {
		_ = c.Close()
	}
	p.conns = nil
}

// TestLeaderWithoutDatabaseGivesUp: an active dispatcher that cannot
// reach PostgreSQL (while Valkey works, so it keeps renewing its lease)
// reports judging as failing and, after 3 lease TTLs, gives the lease up
// so that a standby that reaches PostgreSQL judges. Before, it retried
// storing its results forever, holding the lease.
func TestLeaderWithoutDatabaseGivesUp(t *testing.T) {
	e := newEnv(t)
	task, ds := e.task(2)
	subs := e.submit(e.participant("nina"), task, 3)

	cfg := e.pool.Config()
	proxy := newCutProxy(t, net.JoinHostPort(cfg.ConnConfig.Host, strconv.Itoa(int(cfg.ConnConfig.Port))))
	cfg.ConnConfig.Host, cfg.ConnConfig.Port, cfg.ConnConfig.Fallbacks = "127.0.0.1", uint16(proxy.ln.Addr().(*net.TCPAddr).Port), nil
	cfg.ConnConfig.ConnectTimeout = time.Second
	poolA, err := pgxpool.NewWithConfig(e.ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(poolA.Close)

	optsA := e.options("a")
	optsA.LeaseTTL = 500 * time.Millisecond
	a, err := New(poolA, e.kv, e.jobs, e.results, judging.New(poolA, judging.Options{Logger: optsA.Log}), optsA)
	if err != nil {
		t.Fatal(err)
	}
	a.failingGrace = 100 * time.Millisecond
	e.run(a)
	e.eventually(5*time.Second, "a active", a.Leading)
	e.eventually(5*time.Second, "the first sweep", func() bool {
		return counter(t, optsA.Metrics, "cms_dispatcher_sweeps_total") >= 1
	})
	e.notify(judging.Event{Kind: judging.EventSubmission, SubmissionIDs: subs})
	e.eventually(5*time.Second, "the compilations enqueued", func() bool {
		st, err := e.jobs.Stats(e.ctx)
		return err == nil && st.Ready == 3
	})

	// a loses PostgreSQL, then receives the compilations.
	proxy.Cut()
	e.startWorkers(1, allCorrect)
	optsB := e.options("b")
	b := e.start(optsB)
	var reported bool
	e.eventually(15*time.Second, "b active", func() bool {
		if !reported {
			reported = a.Health(e.ctx) != nil
		}
		return b.d.Leading()
	})
	if !reported {
		t.Error("a never reported judging as failing")
	}
	if a.Leading() {
		t.Error("two active dispatchers")
	}
	if n := counter(t, optsA.Metrics, "cms_dispatcher_lease_changes_total", "change", "given_up"); n != 1 {
		t.Errorf("a gave the lease up %v times", n)
	}
	e.waitScored(ds, 3, 15*time.Second)
	// a, a standby once it sees b active, is healthy.
	e.eventually(5*time.Second, "a healthy as a standby", func() bool { return a.Health(e.ctx) == nil })
}

// TestNotifiedBeforeCommit: a submission notified before the transaction
// that inserts it commits (Notify called inside Store.InTx) is not left to
// the next periodic sweep: the dispatcher, which saw nothing, looks at it
// again shortly.
func TestNotifiedBeforeCommit(t *testing.T) {
	e := newEnv(t)
	task, ds := e.task(1)
	p := e.participant("olga")
	opts := e.options("a")
	e.start(opts)
	e.eventually(5*time.Second, "the first sweep", func() bool {
		return counter(t, opts.Metrics, "cms_dispatcher_sweeps_total") >= 1
	})

	tx, err := e.pool.Begin(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(e.ctx) }()
	var sub int32
	if err := tx.QueryRow(e.ctx, `INSERT INTO submissions (opaque_id, participation_id, task_id, timestamp, language)
		VALUES (1, $1, $2, now(), 'C++17 / g++') RETURNING id`, p, task).Scan(&sub); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(e.ctx, `INSERT INTO files (submission_id, filename, digest)
		SELECT $1, submission_format[1], $2 FROM tasks WHERE id = $3`, sub, digestOf("source"), task); err != nil {
		t.Fatal(err)
	}
	e.notify(judging.Event{Kind: judging.EventSubmission, SubmissionIDs: []int32{sub}})
	e.eventually(5*time.Second, "the notification handled", func() bool {
		if counter(t, opts.Metrics, "cms_dispatcher_events_total", "kind", "submission") < 1 {
			return false
		}
		pending, err := e.kv.Redis().XPending(e.ctx, e.kv.Key(judging.EventsStream), judging.EventsGroup).Result()
		return err == nil && pending.Count == 0
	})
	if err := tx.Commit(e.ctx); err != nil {
		t.Fatal(err)
	}
	e.startWorkers(1, allCorrect)
	e.waitScored(ds, 1, 10*time.Second)
	if n := counter(t, opts.Metrics, "cms_dispatcher_sweeps_total"); n != 1 {
		t.Errorf("%v sweeps: judged by a sweep, not by the notification", n)
	}
}

// TestResultRoundTrips: a batch of results costs a constant number of
// Valkey round trips (take them, enqueue what follows, acknowledge them),
// not a few per result.
func TestResultRoundTrips(t *testing.T) {
	e := newEnv(t)
	task, ds := e.task(2)
	subs := e.submit(e.participant("pia"), task, 50)
	ckv, cc := countingKV(t, e.kv)
	jobs, err := queue.Open(e.ctx, ckv, job.JobQueue, e.jobs.Options())
	if err != nil {
		t.Fatal(err)
	}
	results, err := queue.Open(e.ctx, ckv, job.ResultQueue, e.results.Options())
	if err != nil {
		t.Fatal(err)
	}
	d, err := New(e.pool, ckv, jobs, results, e.judge(), e.options("a"))
	if err != nil {
		t.Fatal(err)
	}
	if ok, _, err := d.lease.TryAcquire(e.ctx); !ok || err != nil {
		t.Fatal("lease", ok, err)
	}
	l := newLeader(d)
	l.recovering, l.readerCtx = false, e.ctx
	e.notify(judging.Event{Kind: judging.EventSubmission, SubmissionIDs: subs})
	msgs, err := judging.ReadEvents(e.ctx, e.kv, EventsConsumer, 10, 0, false)
	if err != nil || len(msgs) != 1 {
		t.Fatal(msgs, err)
	}
	if err := l.handleEvents(e.ctx, msgs); err != nil {
		t.Fatal(err)
	}
	if n := e.work(allCorrect); n != 50 {
		t.Fatalf("%d compilations", n)
	}

	before := cc.RoundTrips()
	if n, _, _, err := l.consumeResults(e.ctx, 0, time.Time{}); n != 50 || err != nil {
		t.Fatal(n, err)
	}
	if trips := cc.RoundTrips() - before; trips > 3 {
		t.Errorf("%d Valkey round trips for a batch of 50 results: %v", trips, cc.Breakdown(5))
	}
	if n := e.work(allCorrect); n != 100 {
		t.Fatalf("%d evaluations", n)
	}
	if n, _, _, err := l.consumeResults(e.ctx, 0, time.Time{}); n != 100 || err != nil {
		t.Fatal(n, err)
	}
	if n := e.scored(ds); n != 50 {
		t.Errorf("%d scored", n)
	}
	if st, err := e.results.Stats(e.ctx); err != nil || st.Ready+st.Pending != 0 {
		t.Errorf("results left: %+v %v", st, err)
	}
}
