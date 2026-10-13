// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package queue_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/kv"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/queue"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
)

func open(t testing.TB, c *kv.Client, name string, o queue.Options) *queue.Queue {
	t.Helper()
	q, err := queue.Open(context.Background(), c, name, o)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func enqueue(t testing.TB, q *queue.Queue, prio int, payload string, o queue.EnqueueOptions) queue.Enqueued {
	t.Helper()
	e, err := q.Enqueue(context.Background(), prio, []byte(payload), o)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func consume(t testing.TB, q *queue.Queue, consumer string, block time.Duration) *queue.Message {
	t.Helper()
	m, err := q.Consume(context.Background(), consumer, block)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func ack(t testing.TB, q *queue.Queue, m *queue.Message) {
	t.Helper()
	if err := q.Ack(context.Background(), m); err != nil {
		t.Fatal(err)
	}
}

func stats(t testing.TB, q *queue.Queue) queue.Stats {
	t.Helper()
	st, err := q.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestRoundTrip(t *testing.T) {
	c := testutil.Redis(t)
	q := open(t, c, "jobs", queue.Options{Priorities: 3})
	payload := "job\x00\n\xff\xfe binary\r\n"
	e := enqueue(t, q, 1, payload, queue.EnqueueOptions{})
	if !e.Added || e.ID == "" {
		t.Fatalf("enqueue: %+v", e)
	}
	m := consume(t, q, "w1", 0)
	if m == nil {
		t.Fatal("no message")
	}
	if string(m.Payload) != payload || m.ID != e.ID || m.Priority != 1 || m.Attempt != 1 ||
		m.Deliveries != 1 || m.Consumer != "w1" || m.DedupKey != "" || m.LastError != "" {
		t.Fatalf("message: %+v", m)
	}
	if d := time.Since(m.EnqueuedAt); d < -5*time.Second || d > 5*time.Second {
		t.Errorf("enqueued at %v", m.EnqueuedAt)
	}
	if m2 := consume(t, q, "w2", 0); m2 != nil {
		t.Fatalf("delivered twice: %+v", m2)
	}
	if st := stats(t, q); st.Ready != 0 || st.Pending != 1 {
		t.Errorf("in flight: %+v", st)
	}
	ack(t, q, m)
	ack(t, q, m) // harmless
	if st := stats(t, q); st.Ready != 0 || st.Pending != 0 || st.DedupKeys != 0 {
		t.Errorf("after ack: %+v", st)
	}
	if m2 := consume(t, q, "w1", 0); m2 != nil {
		t.Fatalf("acknowledged message delivered again: %+v", m2)
	}
}

func TestStrictPriorityFIFO(t *testing.T) {
	c := testutil.Redis(t)
	q := open(t, c, "jobs", queue.Options{Priorities: 3})
	ctx := context.Background()
	// Lower priorities are enqueued first; half go through the batch path.
	var batch []queue.Item
	for i := range 60 {
		prio := 2 - i%3
		p := fmt.Sprintf("%d-%03d", prio, i)
		if i%2 == 0 {
			enqueue(t, q, prio, p, queue.EnqueueOptions{})
			continue
		}
		batch = append(batch, queue.Item{Priority: prio, Payload: []byte(p)})
		if _, err := q.EnqueueBatch(ctx, batch); err != nil {
			t.Fatal(err)
		}
		batch = batch[:0]
	}
	var got []string
	for {
		m := consume(t, q, "w", 0)
		if m == nil {
			break
		}
		if !strings.HasPrefix(string(m.Payload), fmt.Sprint(m.Priority)) {
			t.Fatalf("priority %d for %q", m.Priority, m.Payload)
		}
		got = append(got, string(m.Payload))
		ack(t, q, m)
	}
	if len(got) != 60 {
		t.Fatalf("got %d messages", len(got))
	}
	for i := 1; i < len(got); i++ {
		// "0-..." < "1-..." < "2-...", and FIFO (by index) within each.
		if got[i-1] >= got[i] {
			t.Fatalf("out of order: %q before %q in %v", got[i-1], got[i], got)
		}
	}

	// A high priority message enqueued after low priority ones that are
	// already waiting is still delivered first.
	enqueue(t, q, 2, "low", queue.EnqueueOptions{})
	enqueue(t, q, 1, "mid", queue.EnqueueOptions{})
	enqueue(t, q, 0, "high", queue.EnqueueOptions{})
	for _, want := range []string{"high", "mid", "low"} {
		m := consume(t, q, "w", 0)
		if m == nil || string(m.Payload) != want {
			t.Fatalf("want %s, got %+v", want, m)
		}
		ack(t, q, m)
	}
}

// Many producers and consumers at once: every message is processed exactly
// once and nothing is left behind.
func TestConcurrentConsumersEachMessageOnce(t *testing.T) {
	c := testutil.Redis(t)
	q := open(t, c, "jobs", queue.Options{Priorities: 3, PollInterval: 20 * time.Millisecond})
	const producers, perProducer, consumers = 4, 400, 16
	const total = producers * perProducer
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var mu sync.Mutex
	seen := map[string]int{}
	var processed atomic.Int64
	var wg sync.WaitGroup
	errs := make(chan error, producers+consumers)
	for p := range producers {
		wg.Go(func() {
			for i := range perProducer {
				_, err := q.Enqueue(ctx, i%3, fmt.Appendf(nil, "%d/%d", p, i), queue.EnqueueOptions{})
				if err != nil {
					errs <- err
					return
				}
			}
		})
	}
	for w := range consumers {
		wg.Go(func() {
			name := fmt.Sprintf("w%02d", w)
			for processed.Load() < total && ctx.Err() == nil {
				m, err := q.Consume(ctx, name, 100*time.Millisecond)
				if err != nil {
					errs <- err
					return
				}
				if m == nil {
					continue
				}
				if m.Deliveries != 1 {
					errs <- fmt.Errorf("%s: redelivered: %+v", name, m)
				}
				mu.Lock()
				seen[string(m.Payload)]++
				mu.Unlock()
				if err := q.Ack(ctx, m); err != nil {
					errs <- err
					return
				}
				processed.Add(1)
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if len(seen) != total {
		t.Fatalf("processed %d distinct messages, want %d", len(seen), total)
	}
	for p, n := range seen {
		if n != 1 {
			t.Errorf("%s processed %d times", p, n)
		}
	}
	if st := stats(t, q); st.Ready != 0 || st.Pending != 0 {
		t.Errorf("left behind: %+v", st)
	}
}

// A consumer that dies before acknowledging loses its message to another
// consumer after the visibility timeout, and can no longer act on it.
func TestCrashedConsumerMessageRedelivered(t *testing.T) {
	c := testutil.Redis(t)
	vis := 300 * time.Millisecond
	jobs := open(t, c, "jobs", queue.Options{Priorities: 2, Visibility: vis, PollInterval: 20 * time.Millisecond})
	results := open(t, c, "results", queue.Options{})
	ctx := context.Background()
	e := enqueue(t, jobs, 1, "job", queue.EnqueueOptions{DedupKey: "k"})
	a := consume(t, jobs, "A", 0)
	if a == nil {
		t.Fatal("no message")
	}
	// A crashes here. Nobody else gets the message before the timeout.
	if m := consume(t, jobs, "B", 0); m != nil {
		t.Fatalf("delivered while held: %+v", m)
	}
	start := time.Now()
	b := consume(t, jobs, "B", 5*time.Second)
	if b == nil {
		t.Fatal("the message of the dead consumer was not redelivered")
	}
	if el := time.Since(start); el < vis-50*time.Millisecond {
		t.Errorf("reclaimed after %v, before the visibility timeout %v", el, vis)
	}
	if b.ID != e.ID || b.Deliveries != 2 || b.Attempt != 1 || string(b.Payload) != "job" || b.DedupKey != "k" || b.Consumer != "B" {
		t.Fatalf("redelivered: %+v", b)
	}
	// The stale consumer cannot extend, retry or give back the message.
	if err := jobs.Extend(ctx, a); !errors.Is(err, queue.ErrNotOwner) {
		t.Errorf("extend by the old consumer: %v", err)
	}
	if _, err := jobs.Retry(ctx, a, errors.New("x")); !errors.Is(err, queue.ErrNotOwner) {
		t.Errorf("retry by the old consumer: %v", err)
	}
	if err := jobs.Nack(ctx, a); !errors.Is(err, queue.ErrNotOwner) {
		t.Errorf("nack by the old consumer: %v", err)
	}
	if ok, err := jobs.AckAndEnqueue(ctx, b, results, 0, []byte("result"), queue.EnqueueOptions{}); err != nil || !ok {
		t.Fatalf("ack and enqueue by the owner: %v %v", ok, err)
	}
	// A comes back to life and finishes too: its result is dropped.
	if ok, err := jobs.AckAndEnqueue(ctx, a, results, 0, []byte("late result"), queue.EnqueueOptions{}); err != nil || ok {
		t.Fatalf("ack and enqueue of an acknowledged message: %v %v", ok, err)
	}
	r := consume(t, results, "dispatcher", 0)
	if r == nil || string(r.Payload) != "result" {
		t.Fatalf("result: %+v", r)
	}
	ack(t, results, r)
	if r := consume(t, results, "dispatcher", 0); r != nil {
		t.Fatalf("duplicate result: %+v", r)
	}
	if st := stats(t, jobs); st.Ready+st.Pending+st.Delayed+st.DedupKeys != 0 {
		t.Errorf("left behind: %+v", st)
	}
}

// A message whose consumers keep dying is dead-lettered after
// MaxDeliveries instead of killing workers forever.
func TestPoisonMessageDeadLettered(t *testing.T) {
	c := testutil.Redis(t)
	vis := 100 * time.Millisecond
	q := open(t, c, "jobs", queue.Options{Visibility: vis, MaxDeliveries: 2, PollInterval: 10 * time.Millisecond})
	enqueue(t, q, 0, "poison", queue.EnqueueOptions{DedupKey: "p"})
	if m := consume(t, q, "A", 0); m == nil || m.Deliveries != 1 {
		t.Fatalf("first delivery: %+v", m)
	}
	if m := consume(t, q, "B", 2*time.Second); m == nil || m.Deliveries != 2 {
		t.Fatalf("second delivery: %+v", m)
	}
	time.Sleep(vis + 50*time.Millisecond)
	if m := consume(t, q, "C", 0); m != nil {
		t.Fatalf("delivered a third time: %+v", m)
	}
	dead, err := q.ListDead(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(dead) != 1 || string(dead[0].Payload) != "poison" || dead[0].Deliveries != 2 ||
		!strings.Contains(dead[0].Reason, "abandoned") || !strings.Contains(dead[0].Reason, "B") {
		t.Fatalf("dead letters: %+v", dead)
	}
	if st := stats(t, q); st.Dead != 1 || st.Pending != 0 || st.DedupKeys != 0 {
		t.Errorf("stats: %+v", st)
	}
}

// A restarted worker gives back what its previous incarnation held.
func TestReleaseConsumer(t *testing.T) {
	c := testutil.Redis(t)
	q := open(t, c, "jobs", queue.Options{Priorities: 2, Visibility: time.Hour})
	for i := range 3 {
		enqueue(t, q, i%2, fmt.Sprint(i), queue.EnqueueOptions{})
	}
	for range 3 {
		if consume(t, q, "worker-1", 0) == nil {
			t.Fatal("no message")
		}
	}
	if m := consume(t, q, "worker-2", 0); m != nil {
		t.Fatalf("unexpected: %+v", m)
	}
	n, err := q.Release(context.Background(), "worker-1")
	if err != nil || n != 3 {
		t.Fatalf("release: %d %v", n, err)
	}
	for range 3 {
		m := consume(t, q, "worker-2", 0)
		if m == nil || m.Deliveries != 2 {
			t.Fatalf("after release: %+v", m)
		}
		ack(t, q, m)
	}
}

// Nack gives a message back right away without counting a delivery.
func TestNack(t *testing.T) {
	c := testutil.Redis(t)
	q := open(t, c, "jobs", queue.Options{Visibility: time.Hour, PollInterval: 10 * time.Millisecond})
	enqueue(t, q, 0, "x", queue.EnqueueOptions{})
	a := consume(t, q, "A", 0)
	if err := q.Nack(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	b := consume(t, q, "B", time.Second)
	if b == nil || b.ID != a.ID || b.Deliveries != 1 || b.Attempt != 1 {
		t.Fatalf("after nack: %+v", b)
	}
	if err := q.Nack(context.Background(), a); !errors.Is(err, queue.ErrNotOwner) {
		t.Errorf("second nack: %v", err)
	}
	ack(t, q, b)
}

// Extending a long job keeps it away from other consumers; losing it
// cancels the job context.
func TestKeepAlive(t *testing.T) {
	c := testutil.Redis(t)
	vis := 300 * time.Millisecond
	q := open(t, c, "jobs", queue.Options{Visibility: vis, PollInterval: 20 * time.Millisecond})
	ctx := context.Background()
	enqueue(t, q, 0, "long", queue.EnqueueOptions{})
	a := consume(t, q, "A", 0)
	jctx, stop := q.KeepAlive(ctx, a)
	if m := consume(t, q, "B", 4*vis); m != nil {
		t.Fatalf("an extended message was reclaimed: %+v", m)
	}
	if jctx.Err() != nil {
		t.Fatalf("job context cancelled: %v", context.Cause(jctx))
	}
	stop()
	ack(t, q, a)

	// Another consumer takes the message (as if A had stalled): the job
	// context of A is cancelled with ErrNotOwner.
	e := enqueue(t, q, 0, "stolen", queue.EnqueueOptions{})
	a = consume(t, q, "A", 0)
	jctx, stop = q.KeepAlive(ctx, a)
	defer stop()
	if err := c.Redis().XClaim(ctx, &redis.XClaimArgs{
		Stream: c.Key("queue", "jobs", "p0"), Group: queue.Group, Consumer: "thief", Messages: []string{e.ID},
	}).Err(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-jctx.Done():
		if !errors.Is(context.Cause(jctx), queue.ErrNotOwner) {
			t.Fatalf("cause: %v", context.Cause(jctx))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the job context was not cancelled after losing the message")
	}
}

func TestRetryBackoffAndDeadLetter(t *testing.T) {
	c := testutil.Redis(t)
	ctx := context.Background()
	// A long poll interval: delayed messages must wake the consumer at
	// their due time by themselves.
	q := open(t, c, "jobs", queue.Options{
		Priorities: 2, MaxAttempts: 3, BackoffBase: 200 * time.Millisecond, BackoffMax: 300 * time.Millisecond,
		PollInterval: 10 * time.Second,
	})
	for attempt, want := range map[int]time.Duration{1: 200 * time.Millisecond, 2: 300 * time.Millisecond, 3: 300 * time.Millisecond} {
		if got := q.Backoff(attempt); got != want {
			t.Errorf("backoff(%d) = %v, want %v", attempt, got, want)
		}
	}
	enqueue(t, q, 1, "flaky", queue.EnqueueOptions{DedupKey: "k"})
	m := consume(t, q, "w", 0)
	for attempt := 1; attempt <= 3; attempt++ {
		if m == nil || m.Attempt != attempt || m.Priority != 1 || string(m.Payload) != "flaky" || m.DedupKey != "k" {
			t.Fatalf("attempt %d: %+v", attempt, m)
		}
		if attempt > 1 && m.LastError != fmt.Sprintf("boom %d", attempt-1) {
			t.Errorf("last error %q", m.LastError)
		}
		dead, err := q.Retry(ctx, m, fmt.Errorf("boom %d", attempt))
		if err != nil {
			t.Fatal(err)
		}
		if dead != (attempt == 3) {
			t.Fatalf("attempt %d: dead = %v", attempt, dead)
		}
		if attempt == 3 {
			break
		}
		if again := consume(t, q, "w", 0); again != nil {
			t.Fatalf("retried without backoff: %+v", again)
		}
		if st := stats(t, q); st.Delayed != 1 || st.Pending != 0 {
			t.Errorf("waiting for a retry: %+v", st)
		}
		// The dedup key stays taken while the retry waits.
		if e := enqueue(t, q, 1, "dup", queue.EnqueueOptions{DedupKey: "k"}); e.Added {
			t.Error("dedup key free during the backoff")
		}
		start := time.Now()
		m = consume(t, q, "w", 5*time.Second)
		el := time.Since(start)
		if want := q.Backoff(attempt); el < want-30*time.Millisecond || el > want+2*time.Second {
			t.Errorf("attempt %d came back after %v, want about %v", attempt+1, el, want)
		}
	}
	if m := consume(t, q, "w", 0); m != nil {
		t.Fatalf("dead message delivered: %+v", m)
	}
	st := stats(t, q)
	if st.Dead != 1 || st.Delayed != 0 || st.Pending != 0 || st.Ready != 0 || st.DedupKeys != 0 {
		t.Errorf("after the last attempt: %+v", st)
	}
	dead, err := q.ListDead(ctx, 10)
	if err != nil || len(dead) != 1 {
		t.Fatalf("dead letters: %+v %v", dead, err)
	}
	d := dead[0]
	if d.Attempt != 3 || d.Priority != 1 || d.Reason != "boom 3" || d.DedupKey != "k" || string(d.Payload) != "flaky" || d.Source == "" {
		t.Errorf("dead letter: %+v", d)
	}
	// Requeued by an admin: it starts again at attempt 1.
	e, err := q.RequeueDead(ctx, d.ID)
	if err != nil || !e.Added {
		t.Fatalf("requeue: %+v %v", e, err)
	}
	if _, err := q.RequeueDead(ctx, d.ID); !errors.Is(err, queue.ErrNoDeadLetter) {
		t.Errorf("requeueing twice: %v", err)
	}
	m = consume(t, q, "w", 0)
	if m == nil || m.Attempt != 1 || m.Priority != 1 || m.DedupKey != "k" {
		t.Fatalf("requeued: %+v", m)
	}
	ack(t, q, m)
	if st := stats(t, q); st.Dead != 0 || st.DedupKeys != 0 {
		t.Errorf("after requeue: %+v", st)
	}
}

func TestBackoffJitter(t *testing.T) {
	c := testutil.Redis(t)
	q := open(t, c, "jobs", queue.Options{BackoffBase: time.Second, BackoffMax: time.Minute, BackoffJitter: 0.5})
	for range 100 {
		if d := q.Backoff(3); d <= 2*time.Second || d > 4*time.Second {
			t.Fatalf("backoff(3) with jitter 0.5 = %v", d)
		}
	}
	if d := q.Backoff(30); d > time.Minute {
		t.Fatalf("backoff above the maximum: %v", d)
	}
}

func TestDedup(t *testing.T) {
	c := testutil.Redis(t)
	vis := 100 * time.Millisecond
	q := open(t, c, "jobs", queue.Options{Priorities: 2, Visibility: vis, PollInterval: 10 * time.Millisecond})
	ctx := context.Background()
	first := enqueue(t, q, 0, "a", queue.EnqueueOptions{DedupKey: "k"})
	if second := enqueue(t, q, 1, "b", queue.EnqueueOptions{DedupKey: "k"}); second.Added || second.ID != first.ID {
		t.Fatalf("duplicate enqueued: %+v vs %+v", second, first)
	}
	enqueue(t, q, 0, "other", queue.EnqueueOptions{DedupKey: "other"})
	res, err := q.EnqueueBatch(ctx, []queue.Item{
		{Payload: []byte("c"), Options: queue.EnqueueOptions{DedupKey: "batch"}},
		{Payload: []byte("d"), Options: queue.EnqueueOptions{DedupKey: "batch"}},
	})
	if err != nil || !res[0].Added || res[1].Added || res[1].ID != res[0].ID {
		t.Fatalf("duplicates inside a batch: %+v %v", res, err)
	}

	m := consume(t, q, "A", 0)
	if m == nil || string(m.Payload) != "a" {
		t.Fatalf("got %+v", m)
	}
	// Still taken while in flight; free after the ack.
	if e := enqueue(t, q, 0, "a2", queue.EnqueueOptions{DedupKey: "k"}); e.Added {
		t.Fatal("dedup key free while in flight")
	}
	ack(t, q, m)
	if e := enqueue(t, q, 0, "a3", queue.EnqueueOptions{DedupKey: "k"}); !e.Added {
		t.Fatal("dedup key still taken after the ack")
	}
	// Start over with an empty priority 0.
	if err := c.Redis().Del(ctx, c.Key("queue", "jobs", "p0"), c.Key("queue", "jobs", "dedup")).Err(); err != nil {
		t.Fatal(err)
	}

	// A late acknowledgement of a reclaimed copy does not free the key of
	// a newer message.
	enqueue(t, q, 0, "v1", queue.EnqueueOptions{DedupKey: "late"})
	old := consume(t, q, "A", 0)
	b := consume(t, q, "B", 2*time.Second) // A stalled
	if b == nil || b.ID != old.ID {
		t.Fatalf("reclaim: %+v", b)
	}
	ack(t, q, b)
	if e := enqueue(t, q, 0, "v2", queue.EnqueueOptions{DedupKey: "late"}); !e.Added {
		t.Fatal("key not freed")
	}
	ack(t, q, old) // A wakes up
	if e := enqueue(t, q, 0, "v3", queue.EnqueueOptions{DedupKey: "late"}); e.Added {
		t.Fatal("a late ack freed the key of a newer message")
	}
}

func TestHolding(t *testing.T) {
	c := testutil.Redis(t)
	q := open(t, c, "jobs", queue.Options{Priorities: 2, BackoffBase: time.Hour, BackoffMax: time.Hour})
	ctx := context.Background()
	if h, err := q.Holding(ctx, nil); err != nil || len(h) != 0 {
		t.Fatalf("no keys: %v %v", h, err)
	}
	enqueue(t, q, 0, "a", queue.EnqueueOptions{DedupKey: "queued"})
	enqueue(t, q, 1, "b", queue.EnqueueOptions{DedupKey: "delayed", Delay: time.Hour})
	enqueue(t, q, 0, "c", queue.EnqueueOptions{DedupKey: "flight"})
	enqueue(t, q, 0, "d", queue.EnqueueOptions{DedupKey: "acked"})
	enqueue(t, q, 0, "e", queue.EnqueueOptions{DedupKey: "retried"})
	for range 4 {
		m := consume(t, q, "A", 0)
		switch string(m.Payload) {
		case "d":
			ack(t, q, m)
		case "e":
			if _, err := q.Retry(ctx, m, errors.New("failed")); err != nil {
				t.Fatal(err)
			}
		}
	}
	keys := []string{"queued", "delayed", "flight", "acked", "retried", "never"}
	// More keys than one round trip takes.
	for i := range 2500 {
		keys = append(keys, fmt.Sprintf("x%d", i))
	}
	h, err := q.Holding(ctx, keys)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"queued": true, "delayed": true, "flight": true, "retried": true}
	if len(h) != len(keys) {
		t.Fatalf("%d answers for %d keys", len(h), len(keys))
	}
	for i, k := range keys {
		if h[i] != want[k] {
			t.Errorf("Holding(%q) = %v", k, h[i])
		}
	}
}

func TestDelayedEnqueue(t *testing.T) {
	c := testutil.Redis(t)
	q := open(t, c, "jobs", queue.Options{PollInterval: 10 * time.Second})
	delay := 300 * time.Millisecond
	e := enqueue(t, q, 0, "later", queue.EnqueueOptions{Delay: delay, DedupKey: "d", Attempt: 2})
	if !e.Added {
		t.Fatal("not added")
	}
	if m := consume(t, q, "w", 0); m != nil {
		t.Fatalf("delivered before its delay: %+v", m)
	}
	if e2 := enqueue(t, q, 0, "dup", queue.EnqueueOptions{DedupKey: "d"}); e2.Added || e2.ID != e.ID {
		t.Fatalf("dedup of a delayed message: %+v", e2)
	}
	start := time.Now()
	m := consume(t, q, "w", 5*time.Second)
	if el := time.Since(start); m == nil || el < delay-50*time.Millisecond || el > delay+2*time.Second {
		t.Fatalf("delayed message after %v: %+v", el, m)
	}
	if m.Attempt != 2 || m.DedupKey != "d" || string(m.Payload) != "later" {
		t.Fatalf("delayed message: %+v", m)
	}
	ack(t, q, m)
	if st := stats(t, q); st.DedupKeys != 0 || st.Delayed != 0 {
		t.Errorf("left behind: %+v", st)
	}
}

// Blocked consumers wake up as soon as messages arrive, one per message.
func TestBlockedConsumersWakeUp(t *testing.T) {
	c := testutil.Redis(t)
	q := open(t, c, "jobs", queue.Options{Priorities: 2, PollInterval: 30 * time.Second})
	const n = 4
	ctx := context.Background()
	type got struct {
		m  *queue.Message
		at time.Time
	}
	out := make(chan got, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			m, err := q.Consume(ctx, fmt.Sprint("w", i), 10*time.Second)
			if err != nil {
				t.Error(err)
			}
			out <- got{m, time.Now()}
		})
	}
	time.Sleep(200 * time.Millisecond) // let them block
	sent := time.Now()
	for i := range n {
		enqueue(t, q, i%2, fmt.Sprint(i), queue.EnqueueOptions{})
	}
	wg.Wait()
	close(out)
	seen := map[string]bool{}
	for g := range out {
		if g.m == nil {
			t.Fatal("a blocked consumer got nothing")
		}
		if d := g.at.Sub(sent); d > 2*time.Second {
			t.Errorf("woke up after %v", d)
		}
		seen[string(g.m.Payload)] = true
	}
	if len(seen) != n {
		t.Fatalf("messages: %v", seen)
	}
}

func TestAckAndEnqueueNeedsSameClient(t *testing.T) {
	c1, c2 := testutil.Redis(t), testutil.Redis(t)
	q1 := open(t, c1, "jobs", queue.Options{})
	q2 := open(t, c2, "results", queue.Options{})
	enqueue(t, q1, 0, "x", queue.EnqueueOptions{})
	m := consume(t, q1, "w", 0)
	if _, err := q1.AckAndEnqueue(context.Background(), m, q2, 0, nil, queue.EnqueueOptions{}); !errors.Is(err, queue.ErrDifferentServer) {
		t.Fatalf("err = %v", err)
	}
	if _, err := q1.AckAndEnqueue(context.Background(), m, q1, 3, nil, queue.EnqueueOptions{}); err == nil {
		t.Fatal("priority out of range accepted")
	}
}

// If Valkey loses its data (or the keys are deleted), the queue recreates
// its streams and group instead of failing forever.
func TestRecreatesVanishedGroups(t *testing.T) {
	c := testutil.Redis(t)
	q := open(t, c, "jobs", queue.Options{Priorities: 2})
	ctx := context.Background()
	if err := c.DeletePrefix(ctx); err != nil {
		t.Fatal(err)
	}
	if m := consume(t, q, "w", 0); m != nil {
		t.Fatalf("unexpected: %+v", m)
	}
	if err := c.DeletePrefix(ctx); err != nil {
		t.Fatal(err)
	}
	// Enqueued before any consumer recreated the group: still delivered.
	enqueue(t, q, 1, "after", queue.EnqueueOptions{})
	m := consume(t, q, "w", 0)
	if m == nil || string(m.Payload) != "after" {
		t.Fatalf("after recreation: %+v", m)
	}
	ack(t, q, m)
	if err := c.DeletePrefix(ctx); err != nil {
		t.Fatal(err)
	}
	if err := q.Extend(ctx, m); !errors.Is(err, queue.ErrNotOwner) {
		t.Errorf("extend after data loss: %v", err)
	}
	if _, err := q.Stats(ctx); err != nil {
		t.Errorf("stats after data loss: %v", err)
	}
	if _, err := q.InFlight(ctx, 10); err != nil {
		t.Errorf("in flight after data loss: %v", err)
	}
}

// Entries not written by Enqueue are dead-lettered instead of blocking the
// queue.
func TestMalformedEntryDeadLettered(t *testing.T) {
	c := testutil.Redis(t)
	q := open(t, c, "jobs", queue.Options{})
	ctx := context.Background()
	if err := c.Redis().XAdd(ctx, &redis.XAddArgs{Stream: c.Key("queue", "jobs", "p0"), Values: []string{"x", "y"}}).Err(); err != nil {
		t.Fatal(err)
	}
	enqueue(t, q, 0, "good", queue.EnqueueOptions{})
	m := consume(t, q, "w", 0)
	if m == nil || string(m.Payload) != "good" {
		t.Fatalf("got %+v", m)
	}
	dead, err := q.ListDead(ctx, 10)
	if err != nil || len(dead) != 1 || !strings.Contains(dead[0].Reason, "malformed") {
		t.Fatalf("dead letters: %+v %v", dead, err)
	}
	if err := q.DeleteDead(ctx, dead[0].ID); err != nil {
		t.Fatal(err)
	}
	if st := stats(t, q); st.Dead != 0 {
		t.Errorf("dead letters after delete: %d", st.Dead)
	}
}

func TestStatsAndMetrics(t *testing.T) {
	c := testutil.Redis(t)
	q := open(t, c, "jobs", queue.Options{Priorities: 3, BackoffBase: time.Hour, BackoffMax: time.Hour, MaxAttempts: 1})
	ctx := context.Background()
	if st := stats(t, q); st.Queue != "jobs" || len(st.Priorities) != 3 || st.Ready != 0 || st.OldestReady != 0 {
		t.Fatalf("empty: %+v", st)
	}
	for i := range 2 {
		enqueue(t, q, 0, fmt.Sprint("p0-", i), queue.EnqueueOptions{DedupKey: fmt.Sprint(i)})
	}
	for i := range 3 {
		enqueue(t, q, 2, fmt.Sprint("p2-", i), queue.EnqueueOptions{})
	}
	time.Sleep(20 * time.Millisecond)
	m := consume(t, q, "worker-1", 0)
	st := stats(t, q)
	p := st.Priorities
	if p[0].Ready != 1 || p[0].Pending != 1 || p[1].Ready != 0 || p[2].Ready != 3 || p[2].Pending != 0 ||
		st.Ready != 4 || st.Pending != 1 || st.DedupKeys != 2 || st.Consumers != 1 {
		t.Fatalf("stats: %+v", st)
	}
	if p[0].OldestReady < 20*time.Millisecond || p[2].OldestReady < 20*time.Millisecond || p[1].OldestReady != 0 ||
		st.OldestReady < p[2].OldestReady || st.OldestReady > time.Minute {
		t.Errorf("ages: %+v", st)
	}
	fl, err := q.InFlight(ctx, 10)
	if err != nil || len(fl) != 1 || fl[0].ID != m.ID || fl[0].Consumer != "worker-1" || fl[0].Deliveries != 1 || fl[0].Priority != 0 {
		t.Fatalf("in flight: %+v %v", fl, err)
	}
	if dead, err := q.Retry(ctx, m, errors.New("no more")); err != nil || !dead {
		t.Fatalf("retry with MaxAttempts 1: %v %v", dead, err)
	}
	m = consume(t, q, "worker-1", 0)
	if _, err := q.Retry(ctx, m, nil); err != nil {
		t.Fatal(err)
	}
	if err := q.Ack(ctx, &queue.Message{ID: "1-1", Priority: 7}); err == nil {
		t.Error("ack of a foreign message accepted")
	}

	reg := prometheus.NewRegistry()
	reg.MustRegister(q.Collector(), q.StatsCollector(time.Second))
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]float64{}
	for _, mf := range mfs {
		for _, m := range mf.GetMetric() {
			key := mf.GetName()
			for _, l := range m.GetLabel() {
				if l.GetName() == "priority" {
					key += "/" + l.GetValue()
				}
			}
			got[key] = m.GetGauge().GetValue() + m.GetCounter().GetValue()
		}
	}
	for k, want := range map[string]float64{
		"cms_queue_enqueued_total":      5,
		"cms_queue_delivered_total":     2,
		"cms_queue_dead_lettered_total": 2,
		"cms_queue_ready/2":             3,
		"cms_queue_ready/0":             0,
		"cms_queue_pending/0":           0,
		"cms_queue_dead_letters":        2,
		"cms_queue_delayed":             0,
		"cms_queue_stats_up":            1,
	} {
		if got[k] != want {
			t.Errorf("%s = %v, want %v", k, got[k], want)
		}
	}
}

func TestOptionsValidation(t *testing.T) {
	c := testutil.Redis(t)
	ctx := context.Background()
	for _, name := range []string{"", "Jobs", "-x", "a b", "a:b", strings.Repeat("a", 65)} {
		if _, err := queue.Open(ctx, c, name, queue.Options{}); err == nil {
			t.Errorf("name %q accepted", name)
		}
	}
	for _, o := range []queue.Options{
		{Priorities: -1}, {Priorities: queue.MaxPriorities + 1}, {Visibility: time.Millisecond},
		{MaxAttempts: -1}, {MaxDeliveries: -2}, {BackoffBase: time.Minute, BackoffMax: time.Second},
		{BackoffJitter: 1}, {BackoffJitter: -0.1}, {PollInterval: time.Microsecond}, {DeadMaxLen: -1},
	} {
		if _, err := queue.Open(ctx, c, "jobs", o); err == nil {
			t.Errorf("options %+v accepted", o)
		}
	}
	q := open(t, c, "jobs_1", queue.Options{})
	if o := q.Options(); o.Priorities != 1 || o.Visibility != 30*time.Second || o.MaxAttempts != 3 || q.Name() != "jobs_1" {
		t.Errorf("defaults: %+v", o)
	}
	if _, err := q.Enqueue(ctx, 1, nil, queue.EnqueueOptions{}); err == nil {
		t.Error("priority out of range accepted")
	}
	if _, err := q.Enqueue(ctx, 0, nil, queue.EnqueueOptions{Delay: -time.Second}); err == nil {
		t.Error("negative delay accepted")
	}
	if _, err := q.Consume(ctx, "", 0); err == nil {
		t.Error("empty consumer accepted")
	}
	// An empty payload is a valid message.
	enqueue(t, q, 0, "", queue.EnqueueOptions{})
	if m := consume(t, q, "w", 0); m == nil || len(m.Payload) != 0 {
		t.Fatalf("empty payload: %+v", m)
	}
}

func TestConsumeHonoursContext(t *testing.T) {
	c := testutil.Redis(t)
	q := open(t, c, "jobs", queue.Options{PollInterval: 100 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	start := time.Now()
	m, err := q.Consume(ctx, "w", time.Minute)
	if m != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("got %+v %v", m, err)
	}
	if el := time.Since(start); el > 2*time.Second {
		t.Errorf("returned %v after the cancellation", el)
	}
}

func TestLargeBatch(t *testing.T) {
	c := testutil.Redis(t)
	q := open(t, c, "jobs", queue.Options{Priorities: 2})
	items := make([]queue.Item, 600)
	for i := range items {
		items[i] = queue.Item{Priority: i % 2, Payload: bytes.Repeat([]byte{byte(i)}, 100)}
	}
	res, err := q.EnqueueBatch(context.Background(), items)
	if err != nil || len(res) != len(items) {
		t.Fatalf("%d results, %v", len(res), err)
	}
	if st := stats(t, q); st.Ready != 600 || st.Priorities[0].Ready != 300 {
		t.Fatalf("stats: %+v", st)
	}
}

// Chaos at the queue level (R4.6): consumers crash (drop the message and
// come back under a new name) or stall past the visibility timeout and
// finish late, while workers publish results with AckAndEnqueue. Every job
// still yields exactly one result and nothing is left behind.
func TestCrashingConsumersExactlyOneResult(t *testing.T) {
	c := testutil.Redis(t)
	vis := 150 * time.Millisecond
	jobs := open(t, c, "jobs", queue.Options{Priorities: 2, Visibility: vis, MaxDeliveries: 1000, PollInterval: 10 * time.Millisecond})
	results := open(t, c, "results", queue.Options{PollInterval: 10 * time.Millisecond})
	const total, workers = 300, 8
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	items := make([]queue.Item, total)
	for i := range items {
		items[i] = queue.Item{Priority: i % 2, Payload: fmt.Appendf(nil, "job-%03d", i), Options: queue.EnqueueOptions{DedupKey: fmt.Sprint(i)}}
	}
	if _, err := jobs.EnqueueBatch(ctx, items); err != nil {
		t.Fatal(err)
	}

	var crashes, stalls, lateDropped atomic.Int64
	done := make(chan struct{})
	var wg sync.WaitGroup
	for w := range workers {
		wg.Go(func() {
			rng := rand.New(rand.NewPCG(uint64(w), 42))
			incarnation := 0
			for {
				select {
				case <-done:
					return
				default:
				}
				name := fmt.Sprintf("w%d-%d", w, incarnation)
				m, err := jobs.Consume(ctx, name, 50*time.Millisecond)
				if err != nil {
					if ctx.Err() == nil {
						t.Error(err)
					}
					return
				}
				if m == nil {
					continue
				}
				switch p := rng.Float64(); {
				case p < 0.15: // crash: the message stays pending under the old name
					crashes.Add(1)
					incarnation++
					continue
				case p < 0.20: // stall past the visibility timeout, then finish
					stalls.Add(1)
					time.Sleep(2 * vis)
				}
				result := append([]byte("result-of-"), m.Payload...)
				acked, err := jobs.AckAndEnqueue(ctx, m, results, 0, result, queue.EnqueueOptions{})
				if err != nil {
					t.Error(err)
					return
				}
				if !acked {
					lateDropped.Add(1)
				}
			}
		})
	}

	got := map[string]int{}
	for len(got) < total && ctx.Err() == nil {
		r, err := results.Consume(ctx, "dispatcher", 100*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		if r == nil {
			continue
		}
		got[string(r.Payload)]++
		ack(t, results, r)
	}
	close(done)
	wg.Wait()
	// Stragglers that were still finishing late copies.
	for {
		r := consume(t, results, "dispatcher", 0)
		if r == nil {
			break
		}
		got[string(r.Payload)]++
		ack(t, results, r)
	}
	if len(got) != total {
		t.Fatalf("%d distinct results, want %d", len(got), total)
	}
	for p, n := range got {
		if n != 1 {
			t.Errorf("%s: %d results", p, n)
		}
	}
	if st := stats(t, jobs); st.Ready+st.Pending+st.Delayed+st.DedupKeys+st.Dead != 0 {
		t.Errorf("jobs left behind: %+v", st)
	}
	if crashes.Load() == 0 || stalls.Load() == 0 {
		t.Errorf("the chaos did not happen: %d crashes, %d stalls", crashes.Load(), stalls.Load())
	}
	t.Logf("%d crashes, %d stalls, %d late results dropped", crashes.Load(), stalls.Load(), lateDropped.Load())
}
