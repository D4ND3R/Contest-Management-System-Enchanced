// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package dispatcher

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/kv"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
)

func TestLease(t *testing.T) {
	c := testutil.Redis(t)
	ctx := context.Background()
	a := NewLease(c, "a", time.Second)
	b := NewLease(c, "b", time.Second)
	if ok, _, err := a.TryAcquire(ctx); !ok || err != nil {
		t.Fatalf("a: %v %v", ok, err)
	}
	if !a.Valid() || a.Epoch() != 1 {
		t.Fatalf("a: valid %v epoch %d", a.Valid(), a.Epoch())
	}
	if ok, holder, err := b.TryAcquire(ctx); ok || err != nil || holder != "a#1" {
		t.Fatalf("b took a held lease: %v %q %v", ok, holder, err)
	}
	if err := a.Renew(ctx); err != nil {
		t.Fatal(err)
	}
	// b cannot release or renew a's lease.
	if err := b.Renew(ctx); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("b renewed: %v", err)
	}
	if err := b.Release(ctx); err != nil {
		t.Fatal(err)
	}
	if ok, _, _ := b.TryAcquire(ctx); ok {
		t.Fatal("b released a's lease")
	}
	if err := a.Release(ctx); err != nil || a.Valid() {
		t.Fatalf("release: %v, valid %v", err, a.Valid())
	}
	if ok, _, err := b.TryAcquire(ctx); !ok || err != nil || b.Epoch() != 2 {
		t.Fatalf("b after the release: %v %v epoch %d", ok, err, b.Epoch())
	}
	// a's late renewal does not touch b's lease.
	if err := a.Renew(ctx); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("a renewed b's lease: %v", err)
	}
	if err := b.Renew(ctx); err != nil {
		t.Fatalf("b lost its lease: %v", err)
	}
}

func TestLeaseKeep(t *testing.T) {
	c := testutil.Redis(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Renewed while kept: it outlives its TTL several times.
	l := NewLease(c, "a", 150*time.Millisecond)
	if ok, _, err := l.TryAcquire(ctx); !ok || err != nil {
		t.Fatal(ok, err)
	}
	lost := make(chan error, 1)
	kctx, stop := context.WithCancel(ctx)
	go l.Keep(kctx, func(err error) { lost <- err })
	time.Sleep(600 * time.Millisecond)
	if !l.Valid() {
		t.Fatal("kept lease expired")
	}
	select {
	case err := <-lost:
		t.Fatalf("lost: %v", err)
	default:
	}

	// Taken by someone else: lost at the next renewal.
	if err := c.Redis().Set(ctx, c.Key(leaseKey), "intruder#99", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-lost:
		if !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("lost with %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("stolen lease not noticed")
	}
	stop()
	if l.Valid() {
		t.Fatal("still valid after losing it")
	}

	// Valkey unreachable: lost when the deadline passes, before the key
	// can expire on the server.
	if err := c.Redis().Del(ctx, c.Key(leaseKey)).Err(); err != nil {
		t.Fatal(err)
	}
	l = NewLease(c, "b", 300*time.Millisecond)
	if ok, _, err := l.TryAcquire(ctx); !ok || err != nil {
		t.Fatal(ok, err)
	}
	acquired := time.Now()
	l.kv = kv.New(redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1, DialTimeout: 20 * time.Millisecond}), c.Prefix())
	go l.Keep(ctx, func(err error) { lost <- err })
	select {
	case err := <-lost:
		took := time.Since(acquired)
		if !errors.Is(err, ErrLeaseLost) || took >= 300*time.Millisecond || l.Valid() {
			t.Fatalf("lost after %v with %v (valid %v)", took, err, l.Valid())
		}
		if ttl := c.Redis().PTTL(ctx, c.Key(leaseKey)).Val(); ttl <= 0 {
			t.Fatalf("the key expired (%v) before the holder stopped", ttl)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("unreachable Valkey not noticed")
	}
}

// TestStandbyTakesOver: two dispatchers, one active; the active one dies
// in the middle of judging (without releasing the lease); the standby
// takes over within the lease TTL and finishes. No job runs twice and
// every submission is scored.
func TestStandbyTakesOver(t *testing.T) {
	e := newEnv(t)
	task, ds := e.task(5)
	subs := e.submit(e.participant("alice"), task, 20)
	optsA, optsB := e.options("a"), e.options("b")
	a, err := New(e.pool, e.kv, e.jobs, e.results, e.judge(), optsA)
	if err != nil {
		t.Fatal(err)
	}
	var batches atomic.Int32
	var crashed atomic.Bool
	var ra *running
	a.hooks = testHooks{keepLease: true, afterCommit: func() bool {
		if batches.Add(1) == 4 {
			crashed.Store(true)
			ra.cancel() // dies after its 4th batch, before acknowledging it
			return true
		}
		return false
	}}
	ra = e.run(a)
	e.eventually(5*time.Second, "a active", a.Leading)
	b := e.start(optsB)
	time.Sleep(100 * time.Millisecond)
	if b.d.Leading() {
		t.Fatal("two active dispatchers")
	}
	slow := func(j *job.Job) string { time.Sleep(5 * time.Millisecond); return allCorrect(j) }
	w := e.startWorkers(3, slow)
	e.notify(judging.Event{Kind: judging.EventSubmission, SubmissionIDs: subs})
	e.eventually(10*time.Second, "b active", b.d.Leading)
	if a.Leading() {
		t.Fatal("a still active")
	}
	if b.d.Epoch() <= a.Epoch() {
		t.Errorf("epochs: a %d, b %d", a.Epoch(), b.d.Epoch())
	}
	e.waitScored(ds, 20, 20*time.Second)
	if twice := w.ranTwice(); len(twice) > 0 {
		t.Errorf("jobs ran twice: %v", twice)
	}
	if n := w.count(); n != 20*6 {
		t.Errorf("%d jobs ran, want %d", n, 20*6)
	}
	// b received again the results a stored and did not acknowledge (on
	// a loaded machine a may lose its lease before it crashes).
	if n := counter(t, optsB.Metrics, "cms_dispatcher_results_total", "outcome", "duplicate"); n == 0 && crashed.Load() {
		t.Error("no duplicate: the results stored and not acknowledged did not come back")
	}
	for _, s := range subs {
		if r := e.result(s, ds); r == nil || r.Score == nil || *r.Score != 100 {
			t.Errorf("submission %d: %+v", s, r)
		}
	}
}

// TestRestartInTheMiddleOfABatch: a dispatcher that dies holding results
// it did not store, or stored without acknowledging them, loses nothing
// once it (or another) restarts.
func TestRestartInTheMiddleOfABatch(t *testing.T) {
	for _, before := range []bool{true, false} {
		name := "after commit"
		if before {
			name = "before commit"
		}
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			task, ds := e.task(4)
			subs := e.submit(e.participant("bob"), task, 5)
			opts := e.options("first")
			d, err := New(e.pool, e.kv, e.jobs, e.results, e.judge(), opts)
			if err != nil {
				t.Fatal(err)
			}
			var crashed atomic.Bool
			var r *running
			crash := func() bool {
				// Die at the first batch of evaluations.
				if scalar[int64](e, `SELECT count(*) FROM submission_results WHERE compilation_outcome IS NOT NULL`) == 5 &&
					crashed.CompareAndSwap(false, true) {
					r.cancel()
					return true
				}
				return false
			}
			d.hooks.keepLease = true
			if before {
				d.hooks.beforeCommit = crash
			} else {
				d.hooks.afterCommit = crash
			}
			r = e.run(d)
			w := e.startWorkers(2, allCorrect)
			e.notify(judging.Event{Kind: judging.EventSubmission, SubmissionIDs: subs})
			e.eventually(10*time.Second, "the crash", crashed.Load)
			<-r.done
			r.done <- nil
			time.Sleep(100 * time.Millisecond)
			if n := e.scored(ds); n == 5 {
				t.Fatal("everything was judged before the crash")
			}
			// Restarted (here under another name: what matters is the
			// lease and the shared consumer names).
			opts2 := e.options("second")
			e.start(opts2)
			e.waitScored(ds, 5, 20*time.Second)
			if twice := w.ranTwice(); len(twice) > 0 {
				t.Errorf("jobs ran twice: %v", twice)
			}
			dups := counter(t, opts2.Metrics, "cms_dispatcher_results_total", "outcome", "duplicate")
			if before && dups != 0 || !before && dups == 0 {
				t.Errorf("%v duplicates", dups)
			}
			for _, s := range subs {
				if r := e.result(s, ds); *r.Score != 100 || r.Evaluations != 4 {
					t.Errorf("submission %d: %+v", s, r)
				}
			}
		})
	}
}

// TestLeaseLostStopsWriting: a dispatcher whose lease is taken (as after
// a long pause) stops storing results and enqueueing jobs at once, and
// resumes with a new epoch once it gets the lease back.
func TestLeaseLostStopsWriting(t *testing.T) {
	e := newEnv(t)
	task, ds := e.task(2)
	p := e.participant("carl")
	opts := e.options("a")
	r := e.start(opts)
	e.eventually(5*time.Second, "active", r.d.Leading)
	first := r.d.Epoch()
	e.startWorkers(1, allCorrect)
	stolen := time.Now()
	if err := e.kv.Redis().Set(e.ctx, e.kv.Key(leaseKey), "intruder#1000", 4*time.Second).Err(); err != nil {
		t.Fatal(err)
	}
	e.eventually(3*time.Second, "the lease lost", func() bool { return !r.d.Leading() })
	e.eventually(5*time.Second, "the loss counted", func() bool {
		return counter(t, opts.Metrics, "cms_dispatcher_lease_changes_total", "change", "lost") == 1
	})
	subs := e.submit(p, task, 3)
	e.notify(judging.Event{Kind: judging.EventSubmission, SubmissionIDs: subs})
	time.Sleep(500 * time.Millisecond)
	if time.Since(stolen) > 3500*time.Millisecond {
		t.Skip("too slow a machine for this test")
	}
	if n := e.scored(ds); n != 0 {
		t.Fatalf("%d results stored without the lease", n)
	}
	if st, err := e.jobs.Stats(e.ctx); err != nil || st.Ready+st.Pending != 0 {
		t.Fatalf("jobs enqueued without the lease: %+v %v", st, err)
	}
	// The intruder's lease expires: a takes it back and judges.
	e.waitScored(ds, 3, 15*time.Second)
	if r.d.Epoch() <= first {
		t.Errorf("epoch %d after %d", r.d.Epoch(), first)
	}
}

// TestShutdownHandsOver: a dispatcher that stops releases the lease and
// a standby takes over at once.
func TestShutdownHandsOver(t *testing.T) {
	e := newEnv(t)
	optsA := e.options("a")
	optsA.LeaseTTL = time.Minute
	a := e.start(optsA)
	e.eventually(5*time.Second, "a active", a.d.Leading)
	optsB := e.options("b")
	optsB.LeaseTTL = time.Minute
	b := e.start(optsB)
	var wg sync.WaitGroup
	wg.Add(1)
	start := time.Now()
	go func() { defer wg.Done(); a.stop() }()
	e.eventually(5*time.Second, "b active", b.d.Leading)
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("handover took %v", took)
	}
	wg.Wait()
	if b.d.Epoch() != a.d.Epoch()+1 {
		t.Errorf("epochs %d then %d", a.d.Epoch(), b.d.Epoch())
	}
}
