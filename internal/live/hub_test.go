// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package live

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/kv"
)

func raw(h *Hub, topic Topic, data string) { h.PublishRaw(topic, "x", []byte(data)) }

func TestLocalHubDeliversByTopicInOrder(t *testing.T) {
	h := NewLocalHub(quiet, nil, HubOptions{})
	before := nowMicros()
	a := h.Subscribe([]Topic{"p:1", "c:1", "p:1"}, "")
	b := h.Subscribe([]Topic{"p:2"}, "")
	defer a.Close()
	defer b.Close()
	if len(a.Topics()) != 2 || h.Subscribers() != 2 {
		t.Fatalf("topics %v, subscribers %d", a.Topics(), h.Subscribers())
	}
	h.Publish(TaskScore{Contest: 1, Participation: 1, Task: 3}.Event(), Announcement{Contest: 1, ID: 4}.Event())
	raw(h, "p:2", "for b")

	m1 := nextWithin(t, a, time.Second)
	m2 := nextWithin(t, a, time.Second)
	if m1.Event != EventTaskScore || m1.Topic != "p:1" || string(m1.Data) != `{"v":1,"contest":1,"participation":1,"task":3}` {
		t.Errorf("first %s %s %s", m1.Event, m1.Topic, m1.Data)
	}
	if m2.Event != EventAnnouncement || m2.Topic != "c:1" {
		t.Errorf("second %s %s", m2.Event, m2.Topic)
	}
	// Local ids are microseconds since the epoch and always grow.
	if m1.Seq < before || m2.Seq <= m1.Seq {
		t.Errorf("ids %d %d (before %d)", m1.Seq, m2.Seq, before)
	}
	expectNothing(t, a, 50*time.Millisecond)
	mb := nextWithin(t, b, time.Second)
	if string(mb.Data) != "for b" || mb.Seq <= m2.Seq {
		t.Errorf("b got %s (%d)", mb.Data, mb.Seq)
	}
	if h.LastID() != mb.ID() {
		t.Errorf("LastID %s, want %s", h.LastID(), mb.ID())
	}
	a.Close()
	a.Close()
	if h.Subscribers() != 1 {
		t.Errorf("subscribers after close: %d", h.Subscribers())
	}
	raw(h, "p:1", "nobody")
}

func TestSlowSubscriberGetsResync(t *testing.T) {
	reg := prometheus.NewRegistry()
	h := NewLocalHub(quiet, reg, HubOptions{Buffer: 4})
	s := h.Subscribe([]Topic{"p:1"}, "")
	defer s.Close()
	var ids []uint64
	for range 10 {
		raw(h, "p:1", "e")
		ids = append(ids, h.lastSeq.Load())
	}
	// Four events fit; six are dropped. The marker covers all ten: the
	// buffered ones are discarded too.
	m := nextWithin(t, s, time.Second)
	if m.Event != EventResync || m.Reason != ReasonOverflow || m.Seq != ids[9] {
		t.Fatalf("got %s %s %d, want resync up to %d", m.Event, m.Reason, m.Seq, ids[9])
	}
	expectNothing(t, s, 30*time.Millisecond)
	raw(h, "p:1", "after")
	if m := nextWithin(t, s, time.Second); string(m.Data) != "after" {
		t.Errorf("after the resync: %s %s", m.Event, m.Data)
	}
	if got := metric(t, reg, "cms_live_dropped_total", nil); got != 6 {
		t.Errorf("dropped %v, want 6", got)
	}
	if got := metric(t, reg, "cms_live_resyncs_total", map[string]string{"reason": ReasonOverflow}); got != 1 {
		t.Errorf("resyncs %v", got)
	}
}

// TestResyncKeepsNewerEvents: events that arrive after the overflow are
// delivered after the marker, never discarded.
func TestResyncKeepsNewerEvents(t *testing.T) {
	h := NewLocalHub(quiet, nil, HubOptions{Buffer: 4})
	s := h.Subscribe([]Topic{"p:1"}, "")
	defer s.Close()
	for range 5 { // 1-4 buffered, 5 dropped
		raw(h, "p:1", "old")
	}
	dropped := h.lastSeq.Load()
	// The consumer had taken event 1 (as Next does) when 6 arrives.
	s.held = <-s.ch
	raw(h, "p:1", "new")
	m := nextWithin(t, s, time.Second)
	if m.Event != EventResync || m.Seq != dropped {
		t.Fatalf("got %s %d, want resync %d", m.Event, m.Seq, dropped)
	}
	if m := nextWithin(t, s, time.Second); string(m.Data) != "new" || m.Seq <= dropped {
		t.Errorf("after the resync: %s %d", m.Data, m.Seq)
	}
	expectNothing(t, s, 30*time.Millisecond)
}

func TestSubscribeReplaysFromLastEventID(t *testing.T) {
	h := NewLocalHub(quiet, nil, HubOptions{Buffer: 3, History: 8})
	var ids []string
	for i := range 6 {
		topic := Topic("p:1")
		if i%2 == 1 {
			topic = "p:2"
		}
		raw(h, topic, string(rune('a'+i)))
		ids = append(ids, h.LastID())
	}
	// p:1 got a, c, e; the client has a.
	s := h.Subscribe([]Topic{"p:1"}, ids[0])
	for _, want := range []string{"c", "e"} {
		if m := nextWithin(t, s, time.Second); string(m.Data) != want {
			t.Errorf("replayed %q, want %q", m.Data, want)
		}
	}
	expectNothing(t, s, 20*time.Millisecond)
	s.Close()

	// Up to date: nothing to replay, no resync.
	s = h.Subscribe([]Topic{"p:1"}, ids[5])
	expectNothing(t, s, 20*time.Millisecond)
	s.Close()

	resync := func(name, lastID string) {
		t.Helper()
		s := h.Subscribe([]Topic{"p:1", "p:2"}, lastID)
		defer s.Close()
		m := nextWithin(t, s, time.Second)
		if m.Event != EventResync || m.Reason != ReasonGap || m.ID() != h.LastID() {
			t.Errorf("%s: got %s %s %s", name, m.Event, m.Reason, m.ID())
		}
		expectNothing(t, s, 20*time.Millisecond)
	}
	resync("garbage id", "not-hex")
	resync("id from the future", FormatID(nowMicros()+uint64(time.Minute.Microseconds())))
	// Five events after ids[0] do not fit in a buffer of three.
	resync("too many to replay", ids[0])

	// The history keeps 8 events: after 10 more the first ids are gone.
	for range 10 {
		raw(h, "p:9", "x")
	}
	resync("older than the history", ids[1])
}

func TestHubConcurrentPublishAndSubscribe(t *testing.T) {
	h := NewLocalHub(quiet, nil, HubOptions{Buffer: 16, History: 64})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	const subscribers = 40
	errs := make(chan string, subscribers)
	for i := range subscribers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := h.Subscribe([]Topic{"c:1", ParticipationTopic(int64(i % 4))}, "")
			defer s.Close()
			var last uint64
			for {
				m, err := s.Next(ctx)
				if err != nil {
					return
				}
				if m.Seq <= last && !m.marker {
					errs <- "ids out of order"
					return
				}
				last = max(last, m.Seq)
			}
		}()
	}
	var pubs sync.WaitGroup
	for i := range 8 {
		pubs.Add(1)
		go func() {
			defer pubs.Done()
			for j := range 500 {
				if j%3 == 0 {
					raw(h, "c:1", "all")
				} else {
					raw(h, ParticipationTopic(int64(i%4)), "one")
				}
			}
		}()
	}
	pubs.Wait()
	time.Sleep(50 * time.Millisecond)
	cancel()
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	if h.Subscribers() != 0 {
		t.Errorf("%d subscriptions left", h.Subscribers())
	}
}

func TestValkeyHubIgnoresLocalPublish(t *testing.T) {
	// Events of a Valkey hub come from Valkey only (a Publisher); Publish
	// on it must neither panic nor deliver.
	h := NewHub(kv.New(nil, "t:"), quiet, nil, HubOptions{})
	s := h.Subscribe([]Topic{"p:1"}, "")
	defer s.Close()
	h.Publish(TaskScore{Contest: 1, Participation: 1, Task: 1}.Event())
	raw(h, "p:1", "x")
	expectNothing(t, s, 20*time.Millisecond)
	if h.Connected() {
		t.Error("a hub without a subscription is not connected")
	}
	if err := h.Check(context.Background()); err == nil {
		t.Error("Check must fail while not subscribed")
	}
	if h.chanPrefix != "t:live:" || h.pattern != "t:live:*" || h.seqKey != "t:live-seq" {
		t.Errorf("channels %q %q %q", h.chanPrefix, h.pattern, h.seqKey)
	}
}

// TestHubDropsMessagesBeforeItsBase: a message published between the
// subscription and the read of the counter arrives after the resync that
// covers it; delivering it would move the client's Last-Event-ID back.
func TestHubDropsMessagesBeforeItsBase(t *testing.T) {
	h := NewHub(kv.New(nil, "t:"), quiet, nil, HubOptions{})
	s := h.Subscribe([]Topic{"p:1"}, "")
	defer s.Close()
	h.connect(100)
	if m := nextWithin(t, s, time.Second); m.Event != EventResync || m.Reason != ReasonReconnect || m.Seq != 100 {
		t.Fatalf("got %s %s %d, want the resync of the connection", m.Event, m.Reason, m.Seq)
	}
	h.receive("t:live:p:1", "99\n0\nx\nlate")
	h.receive("t:live:p:1", "100\n0\nx\nlate")
	expectNothing(t, s, 20*time.Millisecond)
	h.receive("t:live:p:1", "101\n0\nx\nnew")
	if m := nextWithin(t, s, time.Second); m.Seq != 101 || string(m.Data) != "new" {
		t.Errorf("got %s %d %s", m.Event, m.Seq, m.Data)
	}
	if h.LastID() != FormatID(101) {
		t.Errorf("LastID %s", h.LastID())
	}
}

// TestLocalHubAcceptsLaterTimes: cmsranking connects with the time of its
// data as last_event_id, usually later than the last event.
func TestLocalHubAcceptsLaterTimes(t *testing.T) {
	h := NewLocalHub(quiet, nil, HubOptions{})
	raw(h, "r", "before")
	time.Sleep(2 * time.Millisecond)
	loaded := nowMicros()
	s := h.Subscribe([]Topic{"r"}, FormatID(loaded))
	defer s.Close()
	if s.start != loaded {
		t.Errorf("start %d, want %d", s.start, loaded)
	}
	expectNothing(t, s, 20*time.Millisecond)
	raw(h, "r", "after")
	if m := nextWithin(t, s, time.Second); string(m.Data) != "after" || m.Seq <= loaded {
		t.Errorf("got %s %s %d", m.Event, m.Data, m.Seq)
	}

	// A Valkey hub has not seen ids above its last one: a gap.
	v := NewHub(kv.New(nil, "t:"), quiet, nil, HubOptions{})
	v.connect(100)
	s2 := v.Subscribe([]Topic{"r"}, FormatID(101))
	defer s2.Close()
	if m := nextWithin(t, s2, time.Second); m.Event != EventResync || m.Seq != 100 {
		t.Errorf("valkey hub: %s %d", m.Event, m.Seq)
	}
}
