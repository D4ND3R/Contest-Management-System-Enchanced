// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package live

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/kv"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
)

// startHub runs a Valkey hub until the end of the test and waits for its
// subscription.
func startHub(t testing.TB, kvc *kv.Client, reg prometheus.Registerer, opts HubOptions) *Hub {
	t.Helper()
	h := NewHub(kvc, quiet, reg, opts)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := h.Run(ctx); err != nil {
			t.Errorf("hub: %v", err)
		}
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	waitFor(t, 5*time.Second, "the hub to subscribe", h.Connected)
	return h
}

func startPublisher(t testing.TB, kvc *kv.Client, reg prometheus.Registerer, opts PublisherOptions) *Publisher {
	t.Helper()
	p := NewPublisher(kvc, quiet, reg, opts)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = p.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return p
}

func TestValkeyPublishSubscribe(t *testing.T) {
	kvc := testutil.Redis(t)
	before := nowMicros()
	hreg, preg := prometheus.NewRegistry(), prometheus.NewRegistry()
	h := startHub(t, kvc, hreg, HubOptions{})
	p := startPublisher(t, kvc, preg, PublisherOptions{})
	sub := h.Subscribe([]Topic{ParticipationTopic(2), AdminContestTopic(1)}, "")
	defer sub.Close()

	in := Submission{Contest: 1, Participation: 2, Task: 3, Dataset: 4, ID: 5, Opaque: 1<<62 + 1,
		Status: SubmissionEvaluating, New: true, ActiveDataset: true}
	p.Publish(in.Event())
	m1 := nextWithin(t, sub, 2*time.Second)
	m2 := nextWithin(t, sub, 2*time.Second)
	// One message per topic, in the order of the event's topics, with
	// consecutive numbers of the installation counter (started at the
	// time in µs).
	if m1.Topic != "a:1" || m2.Topic != "p:2" || m2.Seq != m1.Seq+1 || m1.Seq < before {
		t.Fatalf("got %s/%d and %s/%d (before %d)", m1.Topic, m1.Seq, m2.Topic, m2.Seq, before)
	}
	var got Submission
	if err := json.Unmarshal(m1.Data, &got); err != nil {
		t.Fatal(err)
	}
	in.V, in.ActiveDataset = 1, false
	if got != in || m1.Event != EventSubmission || string(m1.Data) != string(m2.Data) {
		t.Errorf("payload %+v (%s)", got, m1.Data)
	}
	if h.LastID() != m2.ID() {
		t.Errorf("LastID %s, want %s", h.LastID(), m2.ID())
	}

	// Other participations' events do not arrive.
	p.Publish(TaskScore{Contest: 1, Participation: 3, Task: 3}.Event())
	expectNothing(t, sub, 100*time.Millisecond)

	// Another web process (hub) gets the same ids: a page rendered by
	// one replica can open its stream on another.
	h2 := startHub(t, kvc, nil, HubOptions{})
	rendered := h.LastID() // read before the page's data
	p.Publish(Announcement{Contest: 1, ID: 7}.Event())
	m3 := nextWithin(t, sub, 2*time.Second)
	waitFor(t, 2*time.Second, "the second hub", func() bool { return h2.LastID() == h.LastID() })
	sub2 := h2.Subscribe([]Topic{AdminContestTopic(1)}, rendered)
	defer sub2.Close()
	if m := nextWithin(t, sub2, time.Second); m.Seq != m3.Seq || m.Event != EventAnnouncement {
		t.Errorf("replay on the other hub: %s %d, want %d", m.Event, m.Seq, m3.Seq)
	}

	if got := metric(t, preg, "cms_live_published_total", map[string]string{"event": EventSubmission}); got != 2 {
		t.Errorf("published submission %v", got)
	}
	if got := metric(t, hreg, "cms_live_connected", nil); got != 1 {
		t.Errorf("connected %v", got)
	}
}

func TestPublisherThrottlesGroupedEvents(t *testing.T) {
	kvc := testutil.Redis(t)
	h := startHub(t, kvc, nil, HubOptions{})
	const interval = 300 * time.Millisecond
	p := startPublisher(t, kvc, nil, PublisherOptions{GroupInterval: interval})
	sub := h.Subscribe([]Topic{ScoreboardTopic(1), ScoreboardTopic(2), AdminTopic}, "")
	defer sub.Close()
	ids := func(m *Message) []int64 {
		var sc ScoreboardChange
		if err := json.Unmarshal(m.Data, &sc); err != nil {
			t.Fatal(err)
		}
		return sc.Participations
	}

	p.Publish(ScoreboardChange{Contest: 1, Participations: []int64{5}}.Event())
	first := nextWithin(t, sub, time.Second)
	if !slices.Equal(ids(first), []int64{5}) {
		t.Fatalf("first %s", first.Data)
	}
	t0 := time.Now()
	p.Publish(
		ScoreboardChange{Contest: 1, Participations: []int64{7}}.Event(),
		ScoreboardChange{Contest: 1, Participations: []int64{6, 7}}.Event(),
		ScoreboardChange{Contest: 2, Participations: []int64{1}}.Event(),
	)
	// Contest 2 was quiet: at once. Contest 1: merged, after the interval.
	m := nextWithin(t, sub, time.Second)
	if m.Topic != "s:2" || time.Since(t0) > interval/2 {
		t.Errorf("got %s %s after %v", m.Topic, m.Data, time.Since(t0))
	}
	m = nextWithin(t, sub, time.Second)
	if m.Topic != "s:1" || !slices.Equal(ids(m), []int64{6, 7}) {
		t.Errorf("merged %s %s", m.Topic, m.Data)
	}
	if d := time.Since(t0); d < interval*2/3 {
		t.Errorf("merged event after %v, interval %v", d, interval)
	}

	// Judging counters: the latest snapshot wins.
	for i := range 4 {
		p.Publish(Judging{Counters: map[string]int64{CounterQueued: int64(i)}}.Event())
	}
	if m := nextWithin(t, sub, time.Second); !strings.Contains(string(m.Data), `"queued":0`) {
		t.Errorf("first counters %s", m.Data)
	}
	if m := nextWithin(t, sub, time.Second); !strings.Contains(string(m.Data), `"queued":3`) {
		t.Errorf("merged counters %s", m.Data)
	}
	expectNothing(t, sub, interval+100*time.Millisecond)
}

// TestPublishNeverBlocks: with Valkey unreachable, Publish returns at once
// and Run drops what it cannot send.
func TestPublishNeverBlocks(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond, MaxRetries: -1})
	defer rdb.Close()
	reg := prometheus.NewRegistry()
	p := NewPublisher(kv.New(rdb, "x:"), quiet, reg, PublisherOptions{Queue: 16, Timeout: 100 * time.Millisecond})
	start := time.Now()
	for i := range 1000 {
		p.Publish(TaskScore{Contest: 1, Participation: int64(i), Task: 1}.Event())
	}
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Errorf("1000 Publish calls took %v", d)
	}
	if got := metric(t, reg, "cms_live_publish_dropped_total", nil); got != 1000-16 {
		t.Errorf("dropped %v", got)
	}
	if got := metric(t, reg, "cms_live_publish_queue", nil); got != 16 {
		t.Errorf("queue %v", got)
	}
	// Invalid events are refused.
	p.Publish(Event{Name: EventPing, Topics: []Topic{"p:1"}}, Event{Name: "x"},
		Event{Name: "x", Topics: []Topic{"p:*"}}, Event{Name: "Bad", Topics: []Topic{"p:1"}})
	if got := metric(t, reg, "cms_live_publish_dropped_total", nil); got != 1000-16+4 {
		t.Errorf("dropped %v", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = p.Run(ctx)
	}()
	waitFor(t, 2*time.Second, "the failed sends", func() bool {
		return metric(t, reg, "cms_live_publish_errors_total", nil) == 16
	})
	start = time.Now()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop")
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("Run took %v to stop", d)
	}
}

// TestPublisherGivesUpOnSilentValkey: a Valkey that accepts connections
// and never answers (a network black hole) costs one timeout per batch,
// also when Run flushes the queue at shutdown, not one per script call.
func TestPublisherGivesUpOnSilentValkey(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var conns atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			conns.Add(1)
			go func() { _, _ = io.Copy(io.Discard, c); _ = c.Close() }()
		}
	}()
	const timeout = 200 * time.Millisecond
	rdb := redis.NewClient(&redis.Options{Addr: ln.Addr().String(), MaxRetries: -1,
		ReadTimeout: timeout, WriteTimeout: timeout, DialTimeout: timeout})
	defer rdb.Close()
	reg := prometheus.NewRegistry()
	const n = 16 * maxPerCall // 16 script calls if each were tried
	p := NewPublisher(kv.New(rdb, "x:"), quiet, reg, PublisherOptions{Queue: n, Timeout: timeout})
	for i := range n {
		p.Publish(TaskScore{Contest: 1, Participation: int64(i), Task: 1}.Event())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	// Run takes at most 4*maxPerCall events per batch before it sees the
	// cancellation, then the rest in one final batch: at most 5 batches.
	if d, c := time.Since(start), conns.Load(); d > 10*timeout || c > 5 {
		t.Errorf("Run took %v and %d connections to give up", d, c)
	}
	if got := metric(t, reg, "cms_live_publish_errors_total", nil); got != n {
		t.Errorf("lost %v events, want %d", got, n)
	}
}

// killSubscription closes the pub/sub connection of the clients named
// name, as a Valkey restart or a network cut would.
func killSubscription(t testing.TB, admin *kv.Client, name string) {
	t.Helper()
	ctx := context.Background()
	list, err := admin.Redis().ClientList(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	killed := 0
	for _, line := range strings.Split(list, "\n") {
		if !strings.Contains(line, " name="+name+" ") || !strings.Contains(line, " flags=P") {
			continue
		}
		id, _, _ := strings.Cut(strings.TrimPrefix(line, "id="), " ")
		if err := admin.Redis().Do(ctx, "CLIENT", "KILL", "ID", id).Err(); err != nil {
			t.Fatal(err)
		}
		killed++
	}
	if killed != 1 {
		t.Fatalf("killed %d subscriptions named %s:\n%s", killed, name, list)
	}
}

// namedClient returns a client of the test Valkey under the prefix of
// base, whose connections are named (CLIENT LIST).
func namedClient(t testing.TB, base *kv.Client) (*kv.Client, string) {
	t.Helper()
	opt, err := redis.ParseURL(testutil.RedisURL(t))
	if err != nil {
		t.Fatal(err)
	}
	var b [6]byte
	_, _ = rand.Read(b[:])
	opt.ClientName = "live-test-" + hex.EncodeToString(b[:])
	rdb := redis.NewClient(opt)
	t.Cleanup(func() { _ = rdb.Close() })
	return kv.New(rdb, base.Prefix()), opt.ClientName
}

func TestHubResyncsAfterValkeyDisconnect(t *testing.T) {
	base := testutil.Redis(t)
	hkv, name := namedClient(t, base)
	reg := prometheus.NewRegistry()
	h := startHub(t, hkv, reg, HubOptions{ReconnectMin: 50 * time.Millisecond})
	p := startPublisher(t, base, nil, PublisherOptions{})
	sub := h.Subscribe([]Topic{ContestTopic(1)}, "")
	defer sub.Close()

	p.Publish(ContestChange{Contest: 1}.Event())
	m1 := nextWithin(t, sub, 2*time.Second)
	if m1.Event != EventContest {
		t.Fatalf("got %s", m1.Event)
	}

	killSubscription(t, base, name)
	m := nextWithin(t, sub, 5*time.Second)
	if m.Event != EventResync || m.Reason != ReasonReconnect || m.Seq < m1.Seq {
		t.Fatalf("after the disconnect: %s %s %d", m.Event, m.Reason, m.Seq)
	}
	if !h.Connected() || h.Check(context.Background()) != nil {
		t.Error("the hub did not reconnect")
	}
	p.Publish(ContestChange{Contest: 1}.Event())
	if m2 := nextWithin(t, sub, 2*time.Second); m2.Event != EventContest || m2.Seq <= m.Seq {
		t.Errorf("after the reconnection: %s %d (resync %d)", m2.Event, m2.Seq, m.Seq)
	}
	if got := metric(t, reg, "cms_live_reconnects_total", nil); got != 1 {
		t.Errorf("reconnects %v", got)
	}
	if got := metric(t, reg, "cms_live_resyncs_total", map[string]string{"reason": ReasonReconnect}); got != 1 {
		t.Errorf("resyncs %v", got)
	}
}

func TestHubResyncsOnGap(t *testing.T) {
	kvc := testutil.Redis(t)
	h := startHub(t, kvc, nil, HubOptions{})
	p := startPublisher(t, kvc, nil, PublisherOptions{})
	sub := h.Subscribe([]Topic{ParticipationTopic(1)}, "")
	defer sub.Close()
	score := TaskScore{Contest: 1, Participation: 1, Task: 1}.Event() // one topic
	p.Publish(score)
	m1 := nextWithin(t, sub, 2*time.Second)

	// Five messages this hub never received.
	if err := kvc.Redis().IncrBy(context.Background(), seqKey(kvc), 5).Err(); err != nil {
		t.Fatal(err)
	}
	p.Publish(score)
	m := nextWithin(t, sub, 2*time.Second)
	if m.Event != EventResync || m.Reason != ReasonGap || m.Seq != m1.Seq+5 {
		t.Fatalf("got %s %s %d, want a gap resync at %d", m.Event, m.Reason, m.Seq, m1.Seq+5)
	}
	if m2 := nextWithin(t, sub, 2*time.Second); m2.Seq != m1.Seq+6 || m2.Event != EventTaskScore {
		t.Errorf("then %s %d", m2.Event, m2.Seq)
	}
}

// TestStreamReportsValkeyOutage: while the hub is disconnected the pings
// say live: false (pages poll); on reconnection a resync, then live: true.
func TestStreamReportsValkeyOutage(t *testing.T) {
	base := testutil.Redis(t)
	hkv, name := namedClient(t, base)
	h := startHub(t, hkv, nil, HubOptions{ReconnectMin: 700 * time.Millisecond, ReconnectMax: 700 * time.Millisecond})
	srv := streamServer(t, h, StreamOptions{})
	_, rd := open(t, srv.URL+"/events?t=c:1", http.Header{})
	ping := func() bool {
		t.Helper()
		ev := readWithin(t, 3*time.Second, func() (sseEvent, error) { return rd.nextEvent(true) })
		if ev.Event != EventPing {
			t.Fatalf("got %v, want a ping", ev)
		}
		var p PingPayload
		ev.json(t, &p)
		return p.Live
	}
	if !ping() {
		t.Fatal("first ping: not live")
	}
	killed := time.Now()
	killSubscription(t, base, name)
	if ping() {
		t.Error("ping after the disconnect: live")
	}
	if d := time.Since(killed); d > 500*time.Millisecond {
		t.Errorf("the outage was reported after %v", d)
	}
	ev := readWithin(t, 3*time.Second, func() (sseEvent, error) { return rd.nextEvent(true) })
	if ev.Event != EventResync || ev.Data != `{"v":1,"reason":"reconnect","spread":2000}` || ev.ID != h.LastID() {
		t.Errorf("got %v, want a resync", ev)
	}
	if !ping() {
		t.Error("ping after the reconnection: not live")
	}
}

// TestPublisherFlushesOnShutdown: what is queued when Run stops still
// goes out.
func TestPublisherFlushesOnShutdown(t *testing.T) {
	kvc := testutil.Redis(t)
	h := startHub(t, kvc, nil, HubOptions{})
	sub := h.Subscribe([]Topic{ScoreboardTopic(1), ContestTopic(1)}, "")
	defer sub.Close()
	p := NewPublisher(kvc, quiet, nil, PublisherOptions{GroupInterval: time.Hour})
	p.Publish(ContestChange{Contest: 1}.Event(),
		ScoreboardChange{Contest: 1, Participations: []int64{1}}.Event(),
		ScoreboardChange{Contest: 1, Participations: []int64{2}}.Event())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	var got []string
	for range 3 {
		m := nextWithin(t, sub, 2*time.Second)
		got = append(got, m.Event+" "+string(m.Data))
	}
	// The first scoreboard event of a quiet group goes out at once; the
	// pending one is flushed although its interval has not passed.
	want := []string{`contest {"v":1,"contest":1}`, `scoreboard {"v":1,"contest":1,"participations":[1]}`,
		`scoreboard {"v":1,"contest":1,"participations":[2]}`}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestHubWithoutValkey: the hub keeps retrying, reports itself down and
// stops promptly.
func TestHubWithoutValkey(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond, MaxRetries: -1})
	defer rdb.Close()
	reg := prometheus.NewRegistry()
	h := NewHub(kv.New(rdb, "x:"), quiet, reg, HubOptions{ReconnectMin: 10 * time.Millisecond, ReconnectMax: 20 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = h.Run(ctx)
	}()
	srv := streamServer(t, h, StreamOptions{})
	_, rd := open(t, srv.URL+"/events?t=c:1", nil)
	ev := readWithin(t, time.Second, func() (sseEvent, error) { return rd.nextEvent(true) })
	var p PingPayload
	ev.json(t, &p)
	if p.Live || ev.ID != "" {
		t.Errorf("ping %v while Valkey is unreachable", ev)
	}
	if h.Check(context.Background()) == nil || metric(t, reg, "cms_live_connected", nil) != 0 {
		t.Error("the hub reports itself connected")
	}
	time.Sleep(100 * time.Millisecond) // several attempts
	start := time.Now()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not stop")
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Errorf("Run took %v to stop", d)
	}
}
