// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package queue_test

import (
	"context"
	"errors"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/queue"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
)

// TestGetRequeueDrop covers what the judges page does with one job: read
// it ready or in flight, give an in-flight one back (Nack), drop one
// (Ack), freeing its dedup key.
func TestGetRequeueDrop(t *testing.T) {
	ctx := context.Background()
	c := testutil.Redis(t)
	q := open(t, c, "jobs", queue.Options{Priorities: 2})

	ready := enqueue(t, q, 1, "ready", queue.EnqueueOptions{DedupKey: "k1"})
	m, err := q.Get(ctx, 1, ready.ID)
	if err != nil || string(m.Payload) != "ready" || m.Consumer != "" || m.DedupKey != "k1" || m.Priority != 1 {
		t.Fatalf("Get(ready) = %+v, %v", m, err)
	}
	if _, err := q.Get(ctx, 0, ready.ID); !errors.Is(err, queue.ErrNoMessage) {
		t.Fatalf("Get on the wrong priority: %v", err)
	}
	if _, err := q.Get(ctx, 5, ready.ID); err == nil {
		t.Fatal("Get accepted an unknown priority")
	}

	// In flight: the consumer that holds it.
	held := consume(t, q, "w1/0", 0)
	m, err = q.Get(ctx, 1, held.ID)
	if err != nil || m.Consumer != "w1/0" || m.Deliveries != 1 {
		t.Fatalf("Get(in flight) = %+v, %v", m, err)
	}
	// Requeue: another consumer takes it at once.
	if err := q.Nack(ctx, m); err != nil {
		t.Fatal(err)
	}
	again := consume(t, q, "w2/0", 0)
	if again == nil || again.ID != held.ID {
		t.Fatalf("after the requeue w2 got %+v", again)
	}

	// Drop: gone, and the dedup key is free again.
	m, err = q.Get(ctx, 1, again.ID)
	if err != nil {
		t.Fatal(err)
	}
	ack(t, q, m)
	if _, err := q.Get(ctx, 1, again.ID); !errors.Is(err, queue.ErrNoMessage) {
		t.Fatalf("Get after the drop: %v", err)
	}
	if e := enqueue(t, q, 1, "ready", queue.EnqueueOptions{DedupKey: "k1"}); !e.Added {
		t.Fatal("the dedup key of the dropped job is still held")
	}
	// Dropping a ready message.
	r2 := enqueue(t, q, 0, "other", queue.EnqueueOptions{DedupKey: "k2"})
	m, err = q.Get(ctx, 0, r2.ID)
	if err != nil {
		t.Fatal(err)
	}
	ack(t, q, m)
	if st := stats(t, q); st.Priorities[0].Ready != 0 {
		t.Fatalf("the dropped ready message is still there: %+v", st)
	}
}
