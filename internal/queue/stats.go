// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package queue

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
)

// PriorityStats describes one priority of a queue.
type PriorityStats struct {
	Priority int
	// Ready messages wait for a consumer.
	Ready int64
	// Pending messages were delivered and are not acknowledged yet (in
	// flight, or held by a dead consumer until reclaimed).
	Pending int64
	// OldestReady is the age of the oldest ready message (0: none).
	OldestReady time.Duration
}

// Stats describes a queue (judges page, metrics).
type Stats struct {
	Queue      string
	Priorities []PriorityStats
	// Totals over the priorities.
	Ready, Pending int64
	// OldestReady is the age of the oldest ready message of any priority.
	OldestReady time.Duration
	// Delayed messages wait for their retry backoff (or Delay).
	Delayed int64
	// Dead letters (see ListDead).
	Dead int64
	// Dedup keys currently held.
	DedupKeys int64
	// Consumers known to the group of the busiest stream (including
	// consumers of processes that are gone).
	Consumers int64
}

// Stats reads the state of the queue in two round trips.
func (q *Queue) Stats(ctx context.Context) (Stats, error) {
	st := Stats{Queue: q.name, Priorities: make([]PriorityStats, len(q.streams))}
	pipe := q.rdb.Pipeline()
	now := pipe.Time(ctx)
	lens := make([]*redis.IntCmd, len(q.streams))
	groups := make([]*redis.XInfoGroupsCmd, len(q.streams))
	for i, s := range q.streams {
		lens[i] = pipe.XLen(ctx, s)
		groups[i] = pipe.XInfoGroups(ctx, s)
	}
	delayed := pipe.ZCard(ctx, q.delayed)
	dead := pipe.XLen(ctx, q.dead)
	dedup := pipe.HLen(ctx, q.dedup)
	_, _ = pipe.Exec(ctx)
	for _, c := range []redis.Cmder{now, delayed, dead, dedup} {
		if err := c.Err(); err != nil {
			return st, fmt.Errorf("queue %s: stats: %w", q.name, err)
		}
	}
	st.Delayed, st.Dead, st.DedupKeys = delayed.Val(), dead.Val(), dedup.Val()

	pipe = q.rdb.Pipeline()
	oldest := make([]*redis.XMessageSliceCmd, len(q.streams))
	for i, s := range q.streams {
		ps := &st.Priorities[i]
		ps.Priority = i
		if err := lens[i].Err(); err != nil {
			return st, fmt.Errorf("queue %s: stats: %w", q.name, err)
		}
		last := "0-0"
		// A stream or group that vanished counts as empty; the next
		// operation recreates it.
		if err := groups[i].Err(); err == nil {
			for _, g := range groups[i].Val() {
				if g.Name == Group {
					ps.Pending = g.Pending
					last = g.LastDeliveredID
					st.Consumers = max(st.Consumers, g.Consumers)
				}
			}
		}
		// Acknowledged entries are deleted, so the stream holds exactly the
		// ready and the pending ones.
		ps.Ready = max(lens[i].Val()-ps.Pending, 0)
		if ps.Ready > 0 {
			oldest[i] = pipe.XRangeN(ctx, s, "("+last, "+", 1)
		}
	}
	if pipe.Len() > 0 {
		_, _ = pipe.Exec(ctx)
	}
	for i := range q.streams {
		ps := &st.Priorities[i]
		if c := oldest[i]; c != nil && c.Err() == nil && len(c.Val()) > 0 {
			if t, err := idTime(c.Val()[0].ID); err == nil {
				ps.OldestReady = max(now.Val().Sub(t), 0)
			}
		}
		st.Ready += ps.Ready
		st.Pending += ps.Pending
		st.OldestReady = max(st.OldestReady, ps.OldestReady)
	}
	return st, nil
}

// InFlight is a delivered message that is not acknowledged yet.
type InFlight struct {
	ID       string
	Priority int
	Consumer string
	// Idle is the time since it was delivered or last extended.
	Idle       time.Duration
	Deliveries int
}

// InFlight lists up to limit unacknowledged messages per priority, oldest
// first.
func (q *Queue) InFlight(ctx context.Context, limit int64) ([]InFlight, error) {
	pipe := q.rdb.Pipeline()
	cmds := make([]*redis.XPendingExtCmd, len(q.streams))
	for i, s := range q.streams {
		cmds[i] = pipe.XPendingExt(ctx, &redis.XPendingExtArgs{Stream: s, Group: Group, Start: "-", End: "+", Count: limit})
	}
	_, _ = pipe.Exec(ctx)
	var out []InFlight
	for i, c := range cmds {
		if err := c.Err(); err != nil {
			if missingGroup(err) {
				continue
			}
			return nil, fmt.Errorf("queue %s: in flight: %w", q.name, err)
		}
		for _, p := range c.Val() {
			out = append(out, InFlight{ID: p.ID, Priority: i, Consumer: p.Consumer, Idle: p.Idle, Deliveries: int(p.RetryCount)})
		}
	}
	return out, nil
}

// Peek returns up to limit ready messages of each priority (not yet
// delivered to a consumer), highest priority first and oldest first
// within a priority, without taking them: the queue listings of the
// administration (CMS queue_status).
func (q *Queue) Peek(ctx context.Context, limit int64) ([]*Message, error) {
	pipe := q.rdb.Pipeline()
	groups := make([]*redis.XInfoGroupsCmd, len(q.streams))
	for i, s := range q.streams {
		groups[i] = pipe.XInfoGroups(ctx, s)
	}
	_, _ = pipe.Exec(ctx)
	pipe = q.rdb.Pipeline()
	ranges := make([]*redis.XMessageSliceCmd, len(q.streams))
	for i, s := range q.streams {
		last := "0-0"
		if err := groups[i].Err(); err == nil {
			for _, g := range groups[i].Val() {
				if g.Name == Group {
					last = g.LastDeliveredID
				}
			}
		}
		ranges[i] = pipe.XRangeN(ctx, s, "("+last, "+", limit)
	}
	_, _ = pipe.Exec(ctx)
	var out []*Message
	for i, c := range ranges {
		if err := c.Err(); err != nil {
			if errors.Is(err, redis.Nil) {
				continue
			}
			return nil, fmt.Errorf("queue %s: peek: %w", q.name, err)
		}
		for _, m := range c.Val() {
			fields := make([]any, 0, 2*len(m.Values))
			for k, v := range m.Values {
				fields = append(fields, k, v)
			}
			msg, err := q.parse(i, m.ID, fields, 0, "")
			if err != nil {
				continue
			}
			out = append(out, msg)
		}
	}
	return out, nil
}

// DeadLetter is a message that exhausted its attempts or deliveries.
type DeadLetter struct {
	// ID identifies the dead letter (RequeueDead, DeleteDead).
	ID       string
	Priority int
	// Attempt is the last attempt made.
	Attempt  int
	Payload  []byte
	DedupKey string
	// Reason is the cause of the last failure.
	Reason string
	// Source is the id the message had in its stream.
	Source     string
	Deliveries int
	DeadAt     time.Time
}

// ListDead returns up to count dead letters, oldest first.
func (q *Queue) ListDead(ctx context.Context, count int64) ([]DeadLetter, error) {
	msgs, err := q.rdb.XRangeN(ctx, q.dead, "-", "+", count).Result()
	if err != nil {
		return nil, fmt.Errorf("queue %s: listing dead letters: %w", q.name, err)
	}
	out := make([]DeadLetter, 0, len(msgs))
	for _, m := range msgs {
		d := DeadLetter{ID: m.ID}
		d.DeadAt, _ = idTime(m.ID)
		get := func(k string) string { s, _ := m.Values[k].(string); return s }
		d.Priority, _ = strconv.Atoi(get("q"))
		d.Attempt, _ = strconv.Atoi(get("a"))
		d.Deliveries, _ = strconv.Atoi(get("n"))
		d.Payload, d.DedupKey, d.Reason, d.Source = []byte(get("p")), get("d"), get("e"), get("src")
		out = append(out, d)
	}
	return out, nil
}

// ErrNoDeadLetter: RequeueDead found no dead letter with that id.
var ErrNoDeadLetter = errors.New("queue: no such dead letter")

// RequeueDead puts a dead letter back in its priority with attempt 1 and
// deletes it from the dead letters. If its dedup key is taken by a newer
// message, nothing is enqueued (Added is false) and the dead letter is
// deleted anyway.
func (q *Queue) RequeueDead(ctx context.Context, id string) (Enqueued, error) {
	v, err := q.run(ctx, requeueDeadScript, q.block, id, len(q.streams))
	if err != nil {
		return Enqueued{}, fmt.Errorf("queue %s: requeueing a dead letter: %w", q.name, err)
	}
	vals, _ := v.([]any)
	if len(vals) != 2 {
		return Enqueued{}, fmt.Errorf("queue %s: requeue: unexpected reply %v", q.name, v)
	}
	switch num(vals[1]) {
	case -1:
		return Enqueued{}, ErrNoDeadLetter
	case 1:
		q.n.enqueued.Add(1)
		return Enqueued{ID: str(vals[0]), Added: true}, nil
	}
	q.n.duplicates.Add(1)
	return Enqueued{ID: str(vals[0])}, nil
}

// DeleteDead deletes dead letters.
func (q *Queue) DeleteDead(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	if err := q.rdb.XDel(ctx, q.dead, ids...).Err(); err != nil {
		return fmt.Errorf("queue %s: deleting dead letters: %w", q.name, err)
	}
	return nil
}

// Collector exports the operations of this process on the queue as
// counters labelled with the queue name. Every service using the queue
// can register it.
func (q *Queue) Collector() prometheus.Collector {
	labels := prometheus.Labels{"queue": q.name}
	counter := func(name, help string, v interface{ Load() int64 }) prometheus.Collector {
		return prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: name, Help: help, ConstLabels: labels,
		}, func() float64 { return float64(v.Load()) })
	}
	return multiCollector{
		counter("cms_queue_enqueued_total", "Messages added to the queue by this process.", &q.n.enqueued),
		counter("cms_queue_duplicates_total", "Enqueues skipped because the dedup key was taken.", &q.n.duplicates),
		counter("cms_queue_delivered_total", "First deliveries to consumers of this process.", &q.n.delivered),
		counter("cms_queue_redelivered_total", "Messages reclaimed from dead or stalled consumers by this process.", &q.n.redelivered),
		counter("cms_queue_acked_total", "Messages acknowledged by this process.", &q.n.acked),
		counter("cms_queue_retried_total", "Messages scheduled for another attempt by this process.", &q.n.retried),
		counter("cms_queue_dead_lettered_total", "Messages this process moved to the dead letters after their last attempt.", &q.n.dead),
	}
}

type multiCollector []prometheus.Collector

func (m multiCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, c := range m {
		c.Describe(ch)
	}
}

func (m multiCollector) Collect(ch chan<- prometheus.Metric) {
	for _, c := range m {
		c.Collect(ch)
	}
}

// StatsCollector exports Stats as gauges, read from Valkey at each scrape
// (at most timeout). The state is shared by every process, so only one
// service (the dispatcher) should register it.
func (q *Queue) StatsCollector(timeout time.Duration) prometheus.Collector {
	return &statsCollector{q: q, timeout: timeout}
}

var (
	descReady = prometheus.NewDesc("cms_queue_ready",
		"Messages waiting for a consumer.", []string{"queue", "priority"}, nil)
	descPending = prometheus.NewDesc("cms_queue_pending",
		"Messages delivered and not acknowledged yet.", []string{"queue", "priority"}, nil)
	descOldest = prometheus.NewDesc("cms_queue_oldest_ready_seconds",
		"Age of the oldest message waiting for a consumer.", []string{"queue", "priority"}, nil)
	descDelayed = prometheus.NewDesc("cms_queue_delayed",
		"Messages waiting for their retry backoff.", []string{"queue"}, nil)
	descDead = prometheus.NewDesc("cms_queue_dead_letters",
		"Messages that exhausted their attempts.", []string{"queue"}, nil)
	descConsumers = prometheus.NewDesc("cms_queue_consumers",
		"Consumers known to the consumer group.", []string{"queue"}, nil)
	descUp = prometheus.NewDesc("cms_queue_stats_up",
		"1 if the queue state could be read at this scrape.", []string{"queue"}, nil)
)

type statsCollector struct {
	q       *Queue
	timeout time.Duration
}

func (c *statsCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{descReady, descPending, descOldest, descDelayed, descDead, descConsumers, descUp} {
		ch <- d
	}
}

func (c *statsCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	name := c.q.name
	st, err := c.q.Stats(ctx)
	if err != nil {
		ch <- prometheus.MustNewConstMetric(descUp, prometheus.GaugeValue, 0, name)
		return
	}
	ch <- prometheus.MustNewConstMetric(descUp, prometheus.GaugeValue, 1, name)
	for _, p := range st.Priorities {
		prio := strconv.Itoa(p.Priority)
		ch <- prometheus.MustNewConstMetric(descReady, prometheus.GaugeValue, float64(p.Ready), name, prio)
		ch <- prometheus.MustNewConstMetric(descPending, prometheus.GaugeValue, float64(p.Pending), name, prio)
		ch <- prometheus.MustNewConstMetric(descOldest, prometheus.GaugeValue, p.OldestReady.Seconds(), name, prio)
	}
	ch <- prometheus.MustNewConstMetric(descDelayed, prometheus.GaugeValue, float64(st.Delayed), name)
	ch <- prometheus.MustNewConstMetric(descDead, prometheus.GaugeValue, float64(st.Dead), name)
	ch <- prometheus.MustNewConstMetric(descConsumers, prometheus.GaugeValue, float64(st.Consumers), name)
}
