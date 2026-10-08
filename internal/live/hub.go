// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package live

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/kv"
)

// HubOptions configures a hub. The zero value of each field means its
// default.
type HubOptions struct {
	// Buffer is the number of events a subscriber may have pending
	// (default 64). One that falls further behind loses them and gets a
	// resync marker instead, so a slow client never holds more memory.
	Buffer int
	// History is the number of recent events kept to replay to a client
	// that reconnects with Last-Event-ID (default 4096, ~1 MB).
	History int
	// ProbeInterval: after this long without traffic on its Valkey
	// subscription the hub sends a PING, and without a reply within
	// another interval it reconnects (default 5s).
	ProbeInterval time.Duration
	// ReconnectMin and ReconnectMax bound the backoff between attempts
	// to resubscribe (defaults 100ms and 5s, with jitter).
	ReconnectMin time.Duration
	ReconnectMax time.Duration
}

func (o HubOptions) withDefaults() HubOptions {
	if o.Buffer <= 0 {
		o.Buffer = 64
	}
	if o.History < 0 {
		o.History = 0
	} else if o.History == 0 {
		o.History = 4096
	}
	if o.ProbeInterval <= 0 {
		o.ProbeInterval = 5 * time.Second
	}
	if o.ReconnectMin <= 0 {
		o.ReconnectMin = 100 * time.Millisecond
	}
	if o.ReconnectMax < o.ReconnectMin {
		o.ReconnectMax = max(5*time.Second, o.ReconnectMin)
	}
	return o
}

// Hub multiplexes the live events of a process to its local subscribers
// (the open event streams). A Valkey hub (NewHub) receives them through a
// single pattern subscription; a local hub (NewLocalHub) through Publish
// and PublishRaw.
type Hub struct {
	log        *slog.Logger
	opts       HubOptions
	kv         *kv.Client // nil for a local hub
	chanPrefix string     // <key prefix>live:
	pattern    string     // the escaped chanPrefix and "*"
	seqKey     string
	m          *hubMetrics

	live    atomic.Bool   // receiving events (always true for a local hub)
	lastSeq atomic.Uint64 // mirror of last, read without the lock
	nsubs   atomic.Int64

	mu     sync.Mutex
	topics map[Topic]map[*Subscription]struct{}
	all    map[*Subscription]struct{}
	hist   history
	// last is the sequence number of the latest event; together with
	// hist.floor it decides which Last-Event-IDs can be served.
	last uint64

	warnOnce sync.Once
}

// NewHub returns a hub fed by the Valkey of kvc; Run receives the
// events. reg may be nil (no metrics).
func NewHub(kvc *kv.Client, log *slog.Logger, reg prometheus.Registerer, opts HubOptions) *Hub {
	h := newHub(log, reg, opts)
	h.kv = kvc
	h.chanPrefix = kvc.Key("live") + ":"
	h.pattern = globEscape(h.chanPrefix) + "*"
	h.seqKey = seqKey(kvc)
	return h
}

// NewLocalHub returns a hub fed by its own Publish and PublishRaw, for a
// process that needs no Valkey (the ranking, D14) and for tests. Its ids
// are microseconds since the epoch, as in CMS RankingWebServer.
func NewLocalHub(log *slog.Logger, reg prometheus.Registerer, opts HubOptions) *Hub {
	h := newHub(log, reg, opts)
	h.last = nowMicros()
	h.lastSeq.Store(h.last)
	h.hist.floor = h.last
	h.live.Store(true)
	h.m.connected.Set(1)
	return h
}

func newHub(log *slog.Logger, reg prometheus.Registerer, opts HubOptions) *Hub {
	opts = opts.withDefaults()
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Hub{
		log:    log,
		opts:   opts,
		m:      newHubMetrics(reg),
		topics: map[Topic]map[*Subscription]struct{}{},
		all:    map[*Subscription]struct{}{},
		hist:   history{buf: make([]*Message, opts.History)},
	}
}

func seqKey(kvc *kv.Client) string { return kvc.Key("live-seq") }

// globEscape escapes the glob characters of a Valkey pattern.
func globEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '*', '?', '[', ']', '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// Connected reports whether the hub receives events. Pings carry it as
// "live" so that pages poll while it is false.
func (h *Hub) Connected() bool { return h.live.Load() }

// Check is a health check: it fails while the Valkey subscription is
// down.
func (h *Hub) Check(context.Context) error {
	if h.live.Load() {
		return nil
	}
	return errors.New("not subscribed to live events")
}

// LastID returns the id of the latest event the hub has seen. A page
// reads it before reading its data and passes it as last_event_id when
// it opens its stream: events published meanwhile are replayed.
func (h *Hub) LastID() string { return FormatID(h.lastSeq.Load()) }

// Subscribers returns the number of open subscriptions.
func (h *Hub) Subscribers() int { return int(h.nsubs.Load()) }

// Message is an event handed to a subscriber, or a resync marker.
type Message struct {
	// Seq orders the events of an installation; its event id is
	// FormatID(Seq).
	Seq   uint64
	Topic Topic
	Event string
	Data  []byte
	// Reason is set on resync markers (Event is EventResync): the
	// subscriber must reload its state, which then includes every event
	// up to Seq.
	Reason string

	frame  []byte // the server-sent event, shared by every subscriber
	marker bool
}

// ID returns the event id.
func (m *Message) ID() string { return FormatID(m.Seq) }

func newMessage(seq uint64, topic Topic, event string, data []byte) *Message {
	b := appendFrame(make([]byte, 0, frameLen(event, data)), seq, event, data)
	m := &Message{Seq: seq, Topic: topic, Event: event, frame: b}
	if end := len(b) - 2; end-len(data) >= 0 && string(b[end-len(data):end]) == string(data) {
		// One line of data: share the bytes of the frame.
		m.Data = b[end-len(data) : end : end]
	} else {
		m.Data = append([]byte(nil), data...)
	}
	return m
}

// Subscription receives the events of some topics. Its methods are
// meant for one consumer goroutine; Close may be called from any.
type Subscription struct {
	hub    *Hub
	topics []Topic
	ch     chan *Message
	notify chan struct{}
	resync atomic.Uint64 // pending resync marker (its seq); 0: none
	reason atomic.Pointer[string]
	start  uint64   // hub.last when it subscribed
	held   *Message // taken from ch, not handed out yet (consumer only)
	closed bool     // guarded by hub.mu
}

// Subscribe opens a subscription to topics. lastEventID ("" for none) is
// the id of the last event the client has: the events after it are
// replayed from the history, or, when the history does not cover it or
// they do not fit in the buffer, the subscription starts with a resync
// marker.
func (h *Hub) Subscribe(topics []Topic, lastEventID string) *Subscription {
	s := &Subscription{
		hub:    h,
		ch:     make(chan *Message, h.opts.Buffer),
		notify: make(chan struct{}, 1),
	}
	for _, t := range topics {
		if validTopic(t) && !s.wants(t) {
			s.topics = append(s.topics, t)
		}
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	s.start = h.last
	if lastEventID != "" {
		since, ok := ParseID(lastEventID)
		// Ids of a Valkey hub are a sequence: one it has not seen is
		// unknown. Ids of a local hub are times: a later one is a client
		// that loaded its data after the last event (cmsranking passes
		// the time of its data as last_event_id).
		known := h.last
		if h.kv == nil {
			known = max(known, nowMicros())
		}
		switch {
		case !ok || since > known:
			s.markResync(h.last, ReasonGap)
		case since >= h.last:
			s.start = since
		default:
			msgs, ok := h.hist.after(since, s.wants, cap(s.ch))
			if !ok {
				s.markResync(h.last, ReasonGap)
				break
			}
			for _, m := range msgs {
				s.ch <- m
			}
		}
	}
	for _, t := range s.topics {
		set := h.topics[t]
		if set == nil {
			set = map[*Subscription]struct{}{}
			h.topics[t] = set
		}
		set[s] = struct{}{}
	}
	h.all[s] = struct{}{}
	h.nsubs.Add(1)
	h.m.subscriptions.Inc()
	return s
}

// Close ends the subscription.
func (s *Subscription) Close() {
	h := s.hub
	h.mu.Lock()
	defer h.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	for _, t := range s.topics {
		if set := h.topics[t]; set != nil {
			delete(set, s)
			if len(set) == 0 {
				delete(h.topics, t)
			}
		}
	}
	delete(h.all, s)
	h.nsubs.Add(-1)
	h.m.subscriptions.Dec()
}

// Topics returns the topics of the subscription.
func (s *Subscription) Topics() []Topic { return s.topics }

func (s *Subscription) wants(t Topic) bool {
	for _, x := range s.topics {
		if x == t {
			return true
		}
	}
	return false
}

// wake makes a waiting consumer look at the subscription again.
func (s *Subscription) wake() {
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

// markResync asks for a resync marker covering every event up to seq.
func (s *Subscription) markResync(seq uint64, reason string) {
	if seq == 0 {
		return
	}
	for {
		old := s.resync.Load()
		if old >= seq {
			break
		}
		if s.resync.CompareAndSwap(old, seq) {
			s.reason.Store(&reason)
			break
		}
	}
	s.wake()
}

// poll returns the next item without waiting: a resync marker, an event,
// or nil.
func (s *Subscription) poll() *Message {
	if x := s.resync.Swap(0); x != 0 {
		reason := ReasonGap
		if r := s.reason.Load(); r != nil {
			reason = *r
		}
		// Events up to x are covered by the marker: drop them.
		if s.held != nil && s.held.Seq <= x {
			s.held = nil
		}
		for s.held == nil {
			select {
			case m := <-s.ch:
				if m.Seq > x {
					s.held = m
				}
				continue
			default:
			}
			break
		}
		s.hub.m.resyncs.WithLabelValues(reason).Inc()
		return &Message{Seq: x, Event: EventResync, Reason: reason, marker: true}
	}
	if m := s.held; m != nil {
		s.held = nil
		return m
	}
	select {
	case m := <-s.ch:
		return m
	default:
		return nil
	}
}

// Next waits for the next event or resync marker.
func (s *Subscription) Next(ctx context.Context) (*Message, error) {
	for {
		if m := s.poll(); m != nil {
			return m, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.notify:
		case m := <-s.ch:
			s.held = m
		}
	}
}

// deliverLocked records m and hands it to the subscribers of its topic.
// The caller holds h.mu, which keeps every subscriber's events in order.
func (h *Hub) deliverLocked(m *Message) {
	h.m.messages.Inc()
	if m.Seq > h.last {
		h.hist.push(m)
		h.last = m.Seq
		h.lastSeq.Store(m.Seq)
	}
	n := 0
	for s := range h.topics[m.Topic] {
		select {
		case s.ch <- m:
			n++
		default:
			h.m.dropped.Inc()
			s.markResync(m.Seq, ReasonOverflow)
		}
	}
	h.m.deliveries.Add(float64(n))
}

// resyncAllLocked sends a resync marker up to seq to every subscriber.
func (h *Hub) resyncAllLocked(seq uint64, reason string) {
	for s := range h.all {
		s.markResync(seq, reason)
	}
}

// Publish delivers events through a local hub (Sink). On a Valkey hub it
// does nothing: events go through a Publisher.
func (h *Hub) Publish(evs ...Event) {
	for _, ev := range evs {
		if !h.checkLocal() {
			return
		}
		if !validName(ev.Name) || reserved(ev.Name) {
			h.log.Error("live: invalid event name", "event", ev.Name)
			continue
		}
		data, err := json.Marshal(ev.Data)
		if err != nil {
			h.log.Error("live: encoding an event", "event", ev.Name, "err", err)
			continue
		}
		for _, t := range ev.Topics {
			if validTopic(t) {
				h.publishLocal(t, ev.Name, data)
			}
		}
	}
}

// PublishRaw delivers an event with arbitrary data (not necessarily
// JSON) through a local hub; the ranking uses it for the events of CMS
// RankingWebServer ("score" with "user task score"...).
func (h *Hub) PublishRaw(topic Topic, event string, data []byte) {
	if !h.checkLocal() {
		return
	}
	if !validName(event) || reserved(event) || !validTopic(topic) {
		h.log.Error("live: invalid event", "event", event, "topic", string(topic))
		return
	}
	h.publishLocal(topic, event, data)
}

func (h *Hub) checkLocal() bool {
	if h.kv == nil {
		return true
	}
	h.warnOnce.Do(func() {
		h.log.Error("live: Publish on a Valkey hub does nothing; use a Publisher")
	})
	return false
}

func (h *Hub) publishLocal(topic Topic, event string, data []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	seq := max(nowMicros(), h.last+1)
	h.deliverLocked(newMessage(seq, topic, event, data))
}

// Run receives the events of a Valkey hub until ctx ends, reconnecting
// with backoff; every reconnection sends a resync marker to every
// subscriber, since events may have been lost meanwhile (D1). For a local
// hub it just waits for ctx.
func (h *Hub) Run(ctx context.Context) error {
	if h.kv == nil {
		<-ctx.Done()
		return nil
	}
	delay := h.opts.ReconnectMin
	var downSince time.Time
	for {
		connected, err := h.session(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if connected {
			delay = h.opts.ReconnectMin
			downSince = time.Now()
			h.m.reconnects.Inc()
			h.log.Warn("live events: Valkey subscription lost, reconnecting", "err", err.Error())
		} else if downSince.IsZero() {
			downSince = time.Now()
			h.log.Warn("live events: cannot subscribe to Valkey, retrying", "err", err.Error())
		} else {
			h.log.Debug("live events: cannot subscribe to Valkey", "err", err.Error(),
				"down_for", time.Since(downSince).Round(time.Millisecond).String())
		}
		// Up to 20% shorter, so that the processes of an installation do
		// not all come back at once.
		wait := delay - time.Duration(rand.Int64N(int64(delay)/5+1))
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
		delay = min(2*delay, h.opts.ReconnectMax)
	}
}

// session subscribes and receives until the connection fails. connected
// reports whether the subscription was confirmed.
func (h *Hub) session(ctx context.Context) (connected bool, err error) {
	ps := h.kv.Redis().PSubscribe(ctx, h.pattern)
	defer ps.Close()
	stop := context.AfterFunc(ctx, func() { _ = ps.Close() })
	defer stop()

	for {
		msg, err := ps.ReceiveTimeout(ctx, h.opts.ProbeInterval)
		if err != nil {
			return false, fmt.Errorf("subscribing: %w", err)
		}
		if s, ok := msg.(*redis.Subscription); ok && s.Kind == "psubscribe" {
			break
		}
	}
	// Everything published after the subscription arrives; the counter
	// read now tells which earlier events this process may have missed.
	base, err := initSeq(ctx, h.kv)
	if err != nil {
		return false, err
	}
	h.connect(base)
	defer h.disconnect()

	probing := false
	for {
		msg, err := ps.ReceiveTimeout(ctx, h.opts.ProbeInterval)
		if err != nil {
			if !isTimeout(err) {
				return true, err
			}
			if probing {
				return true, errors.New("no reply from Valkey to PING")
			}
			if err := ps.Ping(ctx); err != nil {
				return true, err
			}
			probing = true
			continue
		}
		probing = false
		switch m := msg.(type) {
		case *redis.Message:
			h.receive(m.Channel, m.Payload)
		case *redis.Subscription:
			if m.Kind == "punsubscribe" {
				return true, errors.New("unsubscribed by the server")
			}
		}
	}
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.Is(err, os.ErrDeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout())
}

func (h *Hub) connect(base uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.hist.reset(base)
	h.last = base
	h.lastSeq.Store(base)
	h.live.Store(true)
	h.m.connected.Set(1)
	h.resyncAllLocked(base, ReasonReconnect)
	h.log.Info("live events: subscribed", "last_id", FormatID(base))
}

func (h *Hub) disconnect() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.live.Store(false)
	h.m.connected.Set(0)
	// Streams send a ping saying live: false right away.
	for s := range h.all {
		s.wake()
	}
}

// receive handles a message of the Valkey subscription.
func (h *Hub) receive(channel, payload string) {
	topic, ok := strings.CutPrefix(channel, h.chanPrefix)
	if !ok {
		return
	}
	seq, ts, event, data, ok := parseWire(payload)
	if !ok || !validName(event) || !validTopic(Topic(topic)) {
		h.log.Warn("live: malformed event", "channel", channel)
		return
	}
	if ts > 0 {
		h.m.lag.Observe(max(0, time.Since(time.UnixMicro(ts)).Seconds()))
	}
	m := newMessage(seq, Topic(topic), event, []byte(data))

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.last != 0 && seq > h.last+1 {
		// Events were published that this hub never received.
		h.log.Warn("live events: events lost, resyncing", "after", FormatID(h.last), "next", FormatID(seq))
		h.hist.reset(seq - 1)
		h.last = seq - 1
		h.lastSeq.Store(h.last)
		h.resyncAllLocked(h.last, ReasonGap)
	}
	h.deliverLocked(m)
}

// parseWire splits a Valkey message: "<seq>\n<publish time µs>\n<event>\n<data>".
func parseWire(p string) (seq uint64, ts int64, event, data string, ok bool) {
	a, rest, ok1 := strings.Cut(p, "\n")
	b, rest, ok2 := strings.Cut(rest, "\n")
	event, data, ok3 := strings.Cut(rest, "\n")
	if !ok1 || !ok2 || !ok3 {
		return 0, 0, "", "", false
	}
	seq, err1 := strconv.ParseUint(a, 10, 64)
	ts, err2 := strconv.ParseInt(b, 10, 64)
	if err1 != nil || err2 != nil || seq == 0 {
		return 0, 0, "", "", false
	}
	return seq, ts, event, data, true
}

// initSeqScript creates the sequence counter at the current time in
// microseconds (so that a counter lost with Valkey's data restarts above
// every id given before) and returns it. KEYS: counter. ARGV: now µs.
var initSeqScript = redis.NewScript(`
redis.call('SET', KEYS[1], ARGV[1], 'NX')
return redis.call('GET', KEYS[1])
`)

func initSeq(ctx context.Context, kvc *kv.Client) (uint64, error) {
	s, err := initSeqScript.Run(ctx, kvc.Redis(), []string{seqKey(kvc)}, nowMicros()).Text()
	if err != nil {
		return 0, fmt.Errorf("reading the event counter: %w", err)
	}
	return strconv.ParseUint(s, 10, 64)
}

// history keeps the latest events, in order, to replay them after a
// reconnection. Every event with a sequence number above floor is in it.
type history struct {
	buf      []*Message
	start, n int
	floor    uint64
}

func (h *history) reset(floor uint64) {
	clear(h.buf)
	h.start, h.n, h.floor = 0, 0, floor
}

func (h *history) push(m *Message) {
	switch {
	case len(h.buf) == 0:
		h.floor = m.Seq
	case h.n == len(h.buf):
		h.floor = h.buf[h.start].Seq
		h.buf[h.start] = m
		h.start = (h.start + 1) % len(h.buf)
	default:
		h.buf[(h.start+h.n)%len(h.buf)] = m
		h.n++
	}
}

func (h *history) at(i int) *Message { return h.buf[(h.start+i)%len(h.buf)] }

// after returns the events after seq that want accepts; ok is false when
// the history does not cover seq or there are more than limit.
func (h *history) after(seq uint64, want func(Topic) bool, limit int) (out []*Message, ok bool) {
	if seq < h.floor {
		return nil, false
	}
	i := sort.Search(h.n, func(i int) bool { return h.at(i).Seq > seq })
	for ; i < h.n; i++ {
		if m := h.at(i); want(m.Topic) {
			if len(out) == limit {
				return nil, false
			}
			out = append(out, m)
		}
	}
	return out, true
}

func reserved(name string) bool { return name == EventPing || name == EventResync }
