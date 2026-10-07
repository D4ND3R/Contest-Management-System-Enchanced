// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package queue_test

import (
	"bytes"
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/queue"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
)

// A job is about 1 KiB of JSON.
var benchPayload = bytes.Repeat([]byte("x"), 1024)

func BenchmarkEnqueue(b *testing.B) {
	q := open(b, testutil.Redis(b), "jobs", queue.Options{Priorities: 3})
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := q.Enqueue(ctx, 1, benchPayload, queue.EnqueueOptions{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEnqueueDedup(b *testing.B) {
	q := open(b, testutil.Redis(b), "jobs", queue.Options{Priorities: 3})
	ctx := context.Background()
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		i++
		if _, err := q.Enqueue(ctx, 1, benchPayload, queue.EnqueueOptions{DedupKey: fmt.Sprint("evaluate/", i)}); err != nil {
			b.Fatal(err)
		}
	}
}

// The evaluation jobs of one submission (100 test cases) in one call.
func BenchmarkEnqueueBatch100(b *testing.B) {
	q := open(b, testutil.Redis(b), "jobs", queue.Options{Priorities: 3})
	ctx := context.Background()
	items := make([]queue.Item, 100)
	for i := range items {
		items[i] = queue.Item{Priority: 1, Payload: benchPayload}
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := q.EnqueueBatch(ctx, items); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*len(items)), "ns/msg")
}

func fill(b *testing.B, q *queue.Queue, n int) {
	b.Helper()
	items := make([]queue.Item, n)
	for i := range items {
		items[i] = queue.Item{Priority: i % 3, Payload: benchPayload}
	}
	if _, err := q.EnqueueBatch(context.Background(), items); err != nil {
		b.Fatal(err)
	}
}

func BenchmarkConsumeAck(b *testing.B) {
	q := open(b, testutil.Redis(b), "jobs", queue.Options{Priorities: 3})
	fill(b, q, b.N)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m, err := q.Consume(ctx, "w", 0)
		if err != nil || m == nil {
			b.Fatal(m, err)
		}
		if err := q.Ack(ctx, m); err != nil {
			b.Fatal(err)
		}
	}
}

// 16 job slots consuming at once, with 64 messages in flight elsewhere
// (the pending lists that the reclaim check scans).
func BenchmarkConsumeAckParallel(b *testing.B) {
	q := open(b, testutil.Redis(b), "jobs", queue.Options{Priorities: 3})
	fill(b, q, 64)
	ctx := context.Background()
	for range 64 {
		if m, err := q.Consume(ctx, "busy", 0); err != nil || m == nil {
			b.Fatal(m, err)
		}
	}
	fill(b, q, b.N)
	var id atomic.Int64
	b.ReportAllocs()
	b.SetParallelism(4) // 4 x GOMAXPROCS goroutines
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		name := fmt.Sprint("w", id.Add(1))
		for pb.Next() {
			m, err := q.Consume(ctx, name, 0)
			if err != nil || m == nil {
				b.Error(m, err)
				return
			}
			if err := q.Ack(ctx, m); err != nil {
				b.Error(err)
				return
			}
		}
	})
}
