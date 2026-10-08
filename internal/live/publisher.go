// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package live

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/kv"
)

// Sink receives events to publish. Publish never blocks on the network
// and never fails: live events may be lost by design (D1), pages resync.
// Callers publish after their transaction commits, so a page that
// reloads on an event reads the new state (Store.InTx may run its
// closure several times: collect the events in it and publish after).
type Sink interface {
	Publish(evs ...Event)
}

// Discard is a Sink that drops every event.
var Discard Sink = discard{}

type discard struct{}

func (discard) Publish(...Event) {}

// Recorder is a Sink that keeps the events, for tests.
type Recorder struct {
	mu     sync.Mutex
	events []Event
}

// Publish records evs.
func (r *Recorder) Publish(evs ...Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, evs...)
}

// Events returns a copy of the recorded events.
func (r *Recorder) Events() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.events)
}

// Reset forgets the recorded events.
func (r *Recorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = nil
}

// PublisherOptions configures a Publisher. The zero value of each field
// means its default.
type PublisherOptions struct {
	// Queue bounds the events waiting to be sent (default 8192). When it
	// is full (Valkey down or too slow) Publish drops events.
	Queue int
	// GroupInterval: a throttled event (Event.Throttle) goes out at most
	// once per interval per (name, topics) (default 1s: R5.8, at most one
	// scoreboard event per second per contest).
	GroupInterval time.Duration
	// Timeout bounds each round trip to Valkey (default 2s).
	Timeout time.Duration
}

func (o PublisherOptions) withDefaults() PublisherOptions {
	if o.Queue <= 0 {
		o.Queue = 8192
	}
	if o.GroupInterval <= 0 {
		o.GroupInterval = time.Second
	}
	if o.Timeout <= 0 {
		o.Timeout = 2 * time.Second
	}
	return o
}

// maxPerCall bounds the messages sent by one script call.
const maxPerCall = 256

// publishScript numbers and publishes messages. KEYS: the sequence
// counter. ARGV: now in µs (initial value of a missing counter), then
// triples channel, publish time µs, "<event>\n<data>". Each message gets
// the next number of the installation-wide sequence, so a hub notices
// lost messages (a gap) and every hub gives the same ids. Returns the
// last number.
var publishScript = redis.NewScript(`
redis.call('SET', KEYS[1], ARGV[1], 'NX')
local seq = 0
for i = 2, #ARGV, 3 do
  seq = redis.call('INCR', KEYS[1])
  redis.call('PUBLISH', ARGV[i], string.format('%d', seq) .. '\n' .. ARGV[i + 1] .. '\n' .. ARGV[i + 2])
end
return seq
`)

// Publisher publishes events on Valkey. Publish only queues them; Run
// sends them in batches, one script call (one round trip) per batch.
type Publisher struct {
	kv         *kv.Client
	log        *slog.Logger
	opts       PublisherOptions
	queue      chan Event
	chanPrefix string
	seqKey     string
	m          *pubMetrics

	// Owned by Run.
	groups map[string]*group

	lastDropLog atomic.Int64 // unix ns of the last "queue full" log
	lastErrLog  time.Time
}

// group holds the throttling state of one (name, topics).
type group struct {
	sent    time.Time // last time an event of the group went out
	pending *Event    // merged events waiting for the interval to pass
}

// NewPublisher returns a publisher on the Valkey of kvc. reg may be nil
// (no metrics). Run must be running for events to go out.
func NewPublisher(kvc *kv.Client, log *slog.Logger, reg prometheus.Registerer, opts PublisherOptions) *Publisher {
	opts = opts.withDefaults()
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	p := &Publisher{
		kv:         kvc,
		log:        log,
		opts:       opts,
		queue:      make(chan Event, opts.Queue),
		chanPrefix: kvc.Key("live") + ":",
		seqKey:     seqKey(kvc),
		groups:     map[string]*group{},
	}
	p.m = newPubMetrics(reg, func() float64 { return float64(len(p.queue)) })
	return p
}

// Publish queues evs without blocking. Invalid events (a programming
// error) and events that do not fit in the queue are dropped, logged and
// counted.
func (p *Publisher) Publish(evs ...Event) {
	now := time.Now()
	for _, ev := range evs {
		if !validName(ev.Name) || reserved(ev.Name) || len(ev.Topics) == 0 {
			p.m.dropped.Inc()
			p.log.Error("live: invalid event", "event", ev.Name)
			continue
		}
		bad := false
		for _, t := range ev.Topics {
			bad = bad || !validTopic(t)
		}
		if bad {
			p.m.dropped.Inc()
			p.log.Error("live: invalid topic", "event", ev.Name, "topics", ev.Topics)
			continue
		}
		ev.at = now
		select {
		case p.queue <- ev:
		default:
			p.m.dropped.Inc()
			// At most one log line every 10 seconds.
			last := p.lastDropLog.Load()
			if now.UnixNano()-last > int64(10*time.Second) && p.lastDropLog.CompareAndSwap(last, now.UnixNano()) {
				p.log.Warn("live: event queue full, dropping events", "queue", cap(p.queue))
			}
		}
	}
}

// Run sends the queued events until ctx ends, then sends what is left
// (bounded by Timeout) and returns nil.
func (p *Publisher) Run(ctx context.Context) error {
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	var batch, out []Event
	for {
		batch = batch[:0]
		select {
		case <-ctx.Done():
			p.final()
			return nil
		case ev := <-p.queue:
			batch = append(batch, ev)
			batch = p.take(batch, 4*maxPerCall)
		case <-timer.C:
		}
		now := time.Now()
		out = out[:0]
		for _, ev := range batch {
			if ev.Throttle {
				out = p.throttle(out, ev, now)
			} else {
				out = append(out, ev)
			}
		}
		var next time.Time
		out, next = p.due(out, now, false)
		p.send(context.WithoutCancel(ctx), out)
		clear(out) // do not keep payloads alive
		if !next.IsZero() {
			timer.Reset(time.Until(next))
		}
	}
}

// take appends the queued events, up to limit, without waiting.
func (p *Publisher) take(batch []Event, limit int) []Event {
	for len(batch) < limit {
		select {
		case ev := <-p.queue:
			batch = append(batch, ev)
		default:
			return batch
		}
	}
	return batch
}

// final sends everything still queued or pending.
func (p *Publisher) final() {
	batch := p.take(nil, cap(p.queue))
	var out []Event
	for _, ev := range batch {
		if ev.Throttle {
			out = p.throttle(out, ev, time.Now())
		} else {
			out = append(out, ev)
		}
	}
	out, _ = p.due(out, time.Now(), true)
	p.send(context.Background(), out)
}

func groupKey(ev Event) string {
	var b strings.Builder
	b.WriteString(ev.Name)
	for _, t := range ev.Topics {
		b.WriteByte('|')
		b.WriteString(string(t))
	}
	return b.String()
}

// throttle sends ev now if its group is quiet, or merges it into the
// group's pending event.
func (p *Publisher) throttle(out []Event, ev Event, now time.Time) []Event {
	k := groupKey(ev)
	g := p.groups[k]
	if g == nil {
		g = &group{}
		p.groups[k] = g
	}
	if g.pending == nil && now.Sub(g.sent) >= p.opts.GroupInterval {
		g.sent = now
		return append(out, ev)
	}
	if g.pending == nil {
		g.pending = &ev
		return out
	}
	// The merged event keeps the time of the first one (the lag metric
	// includes the wait).
	if m, ok := g.pending.Data.(interface{ merge(any) any }); ok {
		g.pending.Data = m.merge(ev.Data)
	} else {
		g.pending.Data = ev.Data
	}
	return out
}

// due appends the pending events whose interval has passed (all of them
// when flush is set) and returns when the next one is due.
func (p *Publisher) due(out []Event, now time.Time, flush bool) ([]Event, time.Time) {
	var next time.Time
	for k, g := range p.groups {
		switch {
		case g.pending != nil:
			at := g.sent.Add(p.opts.GroupInterval)
			if flush || !now.Before(at) {
				out = append(out, *g.pending)
				g.pending, g.sent = nil, now
			} else if next.IsZero() || at.Before(next) {
				next = at
			}
		case now.Sub(g.sent) > 10*p.opts.GroupInterval:
			delete(p.groups, k)
		}
	}
	return out, next
}

// send publishes evs, one script call per maxPerCall messages.
func (p *Publisher) send(ctx context.Context, evs []Event) {
	if len(evs) == 0 {
		return
	}
	args := make([]any, 1, 1+3*min(maxPerCall, 4*len(evs)))
	args[0] = nowMicros()
	var names []string
	flush := func() {
		if len(names) == 0 {
			return
		}
		cctx, cancel := context.WithTimeout(ctx, p.opts.Timeout)
		err := publishScript.Run(cctx, p.kv.Redis(), []string{p.seqKey}, args...).Err()
		cancel()
		if err != nil {
			p.m.errors.Add(float64(len(names)))
			if time.Since(p.lastErrLog) > 10*time.Second {
				p.lastErrLog = time.Now()
				p.log.Warn("live: cannot publish events", "err", err.Error(), "lost", len(names))
			}
		} else {
			for _, n := range names {
				p.m.published.WithLabelValues(n).Inc()
			}
		}
		args, names = args[:1], names[:0]
	}
	for _, ev := range evs {
		data, err := json.Marshal(ev.Data)
		if err != nil {
			p.m.dropped.Inc()
			p.log.Error("live: encoding an event", "event", ev.Name, "err", err)
			continue
		}
		body := ev.Name + "\n" + string(data)
		ts := strconv.FormatInt(ev.at.UnixMicro(), 10)
		for _, t := range ev.Topics {
			args = append(args, p.chanPrefix+string(t), ts, body)
			names = append(names, ev.Name)
			if len(names) == maxPerCall {
				flush()
			}
		}
	}
	flush()
}
