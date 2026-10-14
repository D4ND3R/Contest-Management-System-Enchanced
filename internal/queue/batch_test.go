// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package queue_test

import (
	"context"
	"fmt"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/queue"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
)

func consumeBatch(t testing.TB, q *queue.Queue, consumer string, n int, block time.Duration) []*queue.Message {
	t.Helper()
	msgs, err := q.ConsumeBatch(context.Background(), consumer, n, block)
	if err != nil {
		t.Fatal(err)
	}
	return msgs
}

func payloads(msgs []*queue.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = string(m.Payload)
	}
	return out
}

// roundTrips counts the commands sent to Valkey.
type roundTrips struct{ n atomic.Int64 }

func (r *roundTrips) DialHook(next redis.DialHook) redis.DialHook { return next }
func (r *roundTrips) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error { r.n.Add(1); return next(ctx, cmd) }
}
func (r *roundTrips) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error { r.n.Add(1); return next(ctx, cmds) }
}

// ConsumeBatch returns, in one round trip, what Consume would return one
// by one: by priority, abandoned messages first, oldest first; AckBatch
// acknowledges them in one more and frees their dedup keys.
func TestConsumeBatch(t *testing.T) {
	c := testutil.Redis(t)
	vis := 100 * time.Millisecond
	q := open(t, c, "jobs", queue.Options{Priorities: 3, Visibility: vis, PollInterval: 10 * time.Millisecond})
	ctx := context.Background()
	for i := range 9 {
		enqueue(t, q, 2-i%3, fmt.Sprintf("%d-%d", 2-i%3, i), queue.EnqueueOptions{DedupKey: fmt.Sprint("k", i)})
	}
	// Two of priority 0 are abandoned by a consumer that died.
	if m := consume(t, q, "dead", 0); m == nil || string(m.Payload) != "0-2" {
		t.Fatalf("first: %+v", m)
	}
	if m := consume(t, q, "dead", 0); m == nil || string(m.Payload) != "0-5" {
		t.Fatalf("second: %+v", m)
	}
	time.Sleep(vis + 20*time.Millisecond)

	var rt roundTrips
	c.Redis().AddHook(&rt)
	got := consumeBatch(t, q, "w", 5, 0)
	if n := rt.n.Load(); n != 1 {
		t.Errorf("%d round trips for a batch", n)
	}
	want := []string{"0-2", "0-5", "0-8", "1-1", "1-4"}
	if !slices.Equal(payloads(got), want) {
		t.Fatalf("batch %v, want %v", payloads(got), want)
	}
	if got[0].Deliveries != 2 || got[2].Deliveries != 1 || got[3].Priority != 1 || got[0].Consumer != "w" || got[0].DedupKey != "k2" {
		t.Errorf("messages: %+v %+v %+v", got[0], got[2], got[3])
	}
	rest := consumeBatch(t, q, "w", 100, 0)
	if want := []string{"1-7", "2-0", "2-3", "2-6"}; !slices.Equal(payloads(rest), want) {
		t.Fatalf("rest %v, want %v", payloads(rest), want)
	}
	if more := consumeBatch(t, q, "w", 100, 0); len(more) != 0 {
		t.Fatalf("more: %v", payloads(more))
	}

	rt.n.Store(0)
	if err := q.AckBatch(ctx, append(got, rest...)); err != nil {
		t.Fatal(err)
	}
	if n := rt.n.Load(); n != 1 {
		t.Errorf("%d round trips for an acknowledgement", n)
	}
	// Twice is harmless.
	if err := q.AckBatch(ctx, got); err != nil {
		t.Fatal(err)
	}
	if st := stats(t, q); st.Ready+st.Pending+st.Delayed+st.DedupKeys+st.Dead != 0 {
		t.Errorf("left behind: %+v", st)
	}
	// The keys are free again.
	if e := enqueue(t, q, 0, "again", queue.EnqueueOptions{DedupKey: "k2"}); !e.Added {
		t.Error("dedup key still held")
	}
}

// ConsumeBatch waits for the first message, promotes delayed ones,
// dead-letters the messages abandoned MaxDeliveries times, and leaves no
// wake-up token behind for the messages it took.
func TestConsumeBatchWaitsAndDeadLetters(t *testing.T) {
	c := testutil.Redis(t)
	vis := 50 * time.Millisecond
	q := open(t, c, "results", queue.Options{Visibility: vis, MaxDeliveries: 2, PollInterval: 10 * time.Millisecond})
	ctx := context.Background()

	start := time.Now()
	if got := consumeBatch(t, q, "w", 10, 100*time.Millisecond); len(got) != 0 || time.Since(start) < 90*time.Millisecond {
		t.Fatalf("empty queue: %v after %v", payloads(got), time.Since(start))
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		_, _ = q.EnqueueBatch(ctx, []queue.Item{{Payload: []byte("a")}, {Payload: []byte("b")}})
	}()
	got := consumeBatch(t, q, "w", 10, 2*time.Second)
	if !slices.Equal(payloads(got), []string{"a", "b"}) {
		t.Fatalf("after waiting: %v", payloads(got))
	}
	if n := c.Redis().LLen(ctx, c.Key("queue", "results", "signal")).Val(); n != 0 {
		t.Errorf("%d wake-up tokens left for messages already taken", n)
	}
	if err := q.AckBatch(ctx, got); err != nil {
		t.Fatal(err)
	}

	// Delayed.
	enqueue(t, q, 0, "later", queue.EnqueueOptions{Delay: 50 * time.Millisecond})
	if got := consumeBatch(t, q, "w", 10, 2*time.Second); !slices.Equal(payloads(got), []string{"later"}) {
		t.Fatalf("delayed: %v", payloads(got))
	} else if err := q.AckBatch(ctx, got); err != nil {
		t.Fatal(err)
	}

	// Poison: delivered twice without acknowledgement, then dead.
	enqueue(t, q, 0, "poison", queue.EnqueueOptions{DedupKey: "p"})
	enqueue(t, q, 0, "fine", queue.EnqueueOptions{})
	if got := consumeBatch(t, q, "A", 1, 0); !slices.Equal(payloads(got), []string{"poison"}) {
		t.Fatalf("first delivery: %v", payloads(got))
	}
	time.Sleep(vis + 20*time.Millisecond)
	if got := consumeBatch(t, q, "B", 1, 0); len(got) != 1 || got[0].Deliveries != 2 {
		t.Fatalf("second delivery: %+v", got)
	}
	time.Sleep(vis + 20*time.Millisecond)
	got = consumeBatch(t, q, "C", 10, 0)
	if !slices.Equal(payloads(got), []string{"fine"}) {
		t.Fatalf("after the poison: %v", payloads(got))
	}
	if st := stats(t, q); st.Dead != 1 || st.DedupKeys != 0 {
		t.Errorf("stats: %+v", st)
	}
}
