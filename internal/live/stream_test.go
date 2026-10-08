// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package live

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/httpx"
)

// streamServer serves hub.Stream on /events with opts; the topics come
// from the "t" query parameters.
func streamServer(t testing.TB, h *Hub, opts StreamOptions) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(streamHandler(h, opts))
	t.Cleanup(srv.Close)
	return srv
}

func streamHandler(h *Hub, opts StreamOptions) http.Handler {
	mux := httpx.NewRouter()
	mux.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) {
		o := opts
		for _, tp := range r.URL.Query()["t"] {
			o.Topics = append(o.Topics, Topic(tp))
		}
		h.Stream(w, r, o)
	})
	return mux
}

func TestStreamHeadersAndFirstPing(t *testing.T) {
	h := NewLocalHub(quiet, nil, HubOptions{})
	srv := streamServer(t, h, StreamOptions{RetryMin: 1500 * time.Millisecond, RetryMax: 2500 * time.Millisecond})
	start := time.Now()
	resp, rd := open(t, srv.URL+"/events?t=p:1", nil)
	for k, want := range map[string]string{
		"Content-Type":      "text/event-stream; charset=utf-8",
		"Cache-Control":     "no-cache, no-transform",
		"X-Accel-Buffering": "no",
	} {
		if got := resp.Header.Get(k); got != want {
			t.Errorf("%s: %q, want %q", k, got, want)
		}
	}
	if resp.Header.Get("Content-Encoding") != "" || resp.ContentLength != -1 {
		t.Errorf("encoding %q, length %d", resp.Header.Get("Content-Encoding"), resp.ContentLength)
	}

	first := readWithin(t, time.Second, rd.next)
	if ms, err := strconv.Atoi(first.Retry); err != nil || ms < 1500 || ms > 2500 || first.Event != "" {
		t.Errorf("first block %v: want retry in [1500, 2500]", first)
	}
	ping := readWithin(t, time.Second, rd.next)
	if ping.Event != EventPing || ping.ID != h.LastID() {
		t.Fatalf("second block %v: want a ping with id %s", ping, h.LastID())
	}
	if d := ping.At.Sub(start); d > 500*time.Millisecond {
		t.Errorf("first ping after %v", d)
	}
	var p PingPayload
	ping.json(t, &p)
	if p.V != 1 || p.Interval != 15000 || !p.Live || abs(time.Now().UnixMilli()-p.Time) > 5000 {
		t.Errorf("ping %+v", p)
	}
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

func TestStreamPingInterval(t *testing.T) {
	h := NewLocalHub(quiet, nil, HubOptions{})
	const interval = 40 * time.Millisecond
	srv := streamServer(t, h, StreamOptions{PingInterval: interval})
	start := time.Now()
	_, rd := open(t, srv.URL+"/events", nil)
	var at []time.Time
	for len(at) < 10 {
		ev := readWithin(t, time.Second, func() (sseEvent, error) { return rd.nextEvent(true) })
		if ev.Event != EventPing {
			t.Fatalf("unexpected %v", ev)
		}
		var p PingPayload
		ev.json(t, &p)
		if p.Interval != interval.Milliseconds() {
			t.Errorf("interval %d", p.Interval)
		}
		if len(at) > 0 && ev.ID != "" {
			t.Errorf("periodic ping with an id: %v", ev)
		}
		at = append(at, ev.At)
	}
	if d := at[0].Sub(start); d > 200*time.Millisecond {
		t.Errorf("first ping after %v", d)
	}
	for i := 1; i < len(at); i++ {
		if gap := at[i].Sub(at[i-1]); gap < interval/2 || gap > 3*interval {
			t.Errorf("gap %d: %v (interval %v)", i, gap, interval)
		}
	}
	if mean := at[9].Sub(at[1]) / 8; mean < interval*3/4 || mean > interval*2 {
		t.Errorf("mean gap %v", mean)
	}

	// The interval never exceeds 15 s, whatever the site asks.
	if got := (StreamOptions{PingInterval: time.Hour}).withDefaults().PingInterval; got != MaxPingInterval {
		t.Errorf("ping interval %v", got)
	}
}

func TestStreamDeliversTopicsInOrder(t *testing.T) {
	h := NewLocalHub(quiet, nil, HubOptions{})
	srv := streamServer(t, h, StreamOptions{})
	_, rd := open(t, srv.URL+"/events?t=p:1&t=c:1", nil)
	readWithin(t, time.Second, func() (sseEvent, error) { return rd.nextEvent(true) }) // hello

	h.Publish(Submission{Contest: 1, Participation: 2, ID: 9, Status: SubmissionCompiling, ActiveDataset: true}.Event())
	h.Publish(Submission{Contest: 1, Participation: 1, Task: 3, Dataset: 4, ID: 5, Status: SubmissionScored, ActiveDataset: true}.Event())
	h.Publish(Announcement{Contest: 1, ID: 7}.Event())
	h.PublishRaw("p:1", "score", []byte("u1 t1 100.0\nsecond line"))

	ev := readWithin(t, time.Second, func() (sseEvent, error) { return rd.nextEvent(false) })
	var s Submission
	ev.json(t, &s)
	if ev.Event != EventSubmission || s.ID != 5 || s.Status != SubmissionScored || s.V != 1 {
		t.Errorf("first %v", ev)
	}
	ev2 := readWithin(t, time.Second, func() (sseEvent, error) { return rd.nextEvent(false) })
	if ev2.Event != EventAnnouncement || ev2.Data != `{"v":1,"contest":1,"announcement":7}` {
		t.Errorf("second %v", ev2)
	}
	ev3 := readWithin(t, time.Second, func() (sseEvent, error) { return rd.nextEvent(false) })
	if ev3.Event != "score" || ev3.Data != "u1 t1 100.0\nsecond line" {
		t.Errorf("third %v", ev3)
	}
	id1, _ := ParseID(ev.ID)
	id2, _ := ParseID(ev2.ID)
	id3, _ := ParseID(ev3.ID)
	if !(id1 < id2 && id2 < id3) || ev3.ID != h.LastID() {
		t.Errorf("ids %s %s %s (last %s)", ev.ID, ev2.ID, ev3.ID, h.LastID())
	}
}

func TestStreamLastEventID(t *testing.T) {
	h := NewLocalHub(quiet, nil, HubOptions{})
	srv := streamServer(t, h, StreamOptions{ResyncEvent: "reload"})
	var ids []string
	for i := range 4 {
		h.PublishRaw(Topic("p:"+strconv.Itoa(1+i%2)), "x", []byte{byte('a' + i)})
		ids = append(ids, h.LastID())
	}
	last := h.LastID()

	collect := func(rd *sseReader) (evs []sseEvent) {
		for {
			ev := readWithin(t, time.Second, func() (sseEvent, error) { return rd.nextEvent(true) })
			if ev.Event == EventPing {
				if ev.ID != last {
					t.Errorf("first ping id %s, want %s", ev.ID, last)
				}
				return evs
			}
			evs = append(evs, ev)
		}
	}

	// The header (as browsers send it) replays p:1's events after ids[0].
	_, rd := open(t, srv.URL+"/events?t=p:1", http.Header{"Last-Event-Id": {ids[0]}})
	if evs := collect(rd); len(evs) != 1 || evs[0].Data != "c" || evs[0].ID != ids[2] {
		t.Errorf("replayed %v", evs)
	}
	// The query parameter (cmsranking) works too; the header wins.
	_, rd = open(t, srv.URL+"/events?t=p:2&last_event_id="+ids[0], nil)
	if evs := collect(rd); len(evs) != 2 || evs[0].Data != "b" || evs[1].Data != "d" {
		t.Errorf("replayed %v", evs)
	}
	_, rd = open(t, srv.URL+"/events?t=p:2&last_event_id="+ids[0], http.Header{"Last-Event-Id": {ids[3]}})
	if evs := collect(rd); len(evs) != 0 {
		t.Errorf("replayed %v after the latest id", evs)
	}
	// An unknown id is a gap: resync (named as the site asks).
	_, rd = open(t, srv.URL+"/events?t=p:1", http.Header{"Last-Event-Id": {"zzz"}})
	evs := collect(rd)
	if len(evs) != 1 || evs[0].Event != "reload" || evs[0].ID != last || evs[0].Data != `{"v":1,"reason":"gap","spread":2000}` {
		t.Errorf("got %v, want a reload marker", evs)
	}
}

// gatedWriter is a ResponseWriter whose writes can be held, like a
// client that stopped reading.
type gatedWriter struct {
	mu      sync.Mutex
	cond    *sync.Cond
	hold    bool
	waiting bool
	buf     bytes.Buffer
	header  http.Header
}

func newGatedWriter() *gatedWriter {
	g := &gatedWriter{header: http.Header{}}
	g.cond = sync.NewCond(&g.mu)
	return g
}

func (g *gatedWriter) Header() http.Header { return g.header }
func (g *gatedWriter) WriteHeader(int)     {}
func (g *gatedWriter) Flush()              {}

func (g *gatedWriter) Write(p []byte) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for g.hold {
		g.waiting = true
		g.cond.Wait()
	}
	g.waiting = false
	return g.buf.Write(p)
}

func (g *gatedWriter) set(hold bool) {
	g.mu.Lock()
	g.hold = hold
	g.mu.Unlock()
	g.cond.Broadcast()
}

func (g *gatedWriter) state() (string, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.buf.String(), g.waiting
}

func TestStreamSlowClientGetsResync(t *testing.T) {
	h := NewLocalHub(quiet, nil, HubOptions{Buffer: 4})
	gw := newGatedWriter()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequestWithContext(ctx, "GET", "/events", nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.Stream(gw, req, StreamOptions{Topics: []Topic{"p:1"}})
	}()
	waitFor(t, time.Second, "the first ping", func() bool {
		out, _ := gw.state()
		return strings.Contains(out, "event: ping")
	})

	// The client stops reading while the first event is written.
	gw.set(true)
	h.PublishRaw("p:1", "x", []byte("first"))
	waitFor(t, time.Second, "a blocked write", func() bool { _, w := gw.state(); return w })
	for i := range 20 {
		h.PublishRaw("p:1", "x", []byte(fmt.Sprint("e", i)))
	}
	dropped := h.lastSeq.Load()
	gw.set(false)
	waitFor(t, time.Second, "the resync", func() bool {
		out, _ := gw.state()
		return strings.Contains(out, "event: resync")
	})
	h.PublishRaw("p:1", "x", []byte("after"))
	waitFor(t, time.Second, "the event after the resync", func() bool {
		out, _ := gw.state()
		return strings.Contains(out, "data: after")
	})
	cancel()
	<-done
	if h.Subscribers() != 0 {
		t.Error("the subscription outlived the stream")
	}

	out, _ := gw.state()
	rd := newSSEReader(strings.NewReader(out))
	var resync uint64
	var after []string
	n := 0
	for {
		ev, err := rd.nextEvent(false)
		if err == io.EOF {
			break
		}
		id, _ := ParseID(ev.ID)
		switch {
		case ev.Event == EventResync:
			resync = id
			if ev.Data != `{"v":1,"reason":"overflow","spread":2000}` {
				t.Errorf("resync data %s", ev.Data)
			}
		case resync != 0:
			if id <= resync {
				t.Errorf("event %v after a resync up to %d", ev, resync)
			}
			after = append(after, ev.Data)
		}
		n++
	}
	if resync != dropped {
		t.Errorf("resync up to %d, want %d (the last dropped event)", resync, dropped)
	}
	if len(after) != 1 || after[0] != "after" {
		t.Errorf("after the resync: %v", after)
	}
	if n > 1+4+2 {
		t.Errorf("%d events written: the dropped ones were not dropped", n)
	}
}

func TestStreamDropsClientThatStopsReading(t *testing.T) {
	h := NewLocalHub(quiet, nil, HubOptions{})
	srv := streamServer(t, h, StreamOptions{WriteTimeout: 200 * time.Millisecond})
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.(*net.TCPConn).SetReadBuffer(4096)
	fmt.Fprintf(conn, "GET /events?t=r HTTP/1.1\r\nHost: x\r\nAccept: text/event-stream\r\n\r\n")
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("response %v %v", resp, err)
	}
	readWithin(t, time.Second, func() (sseEvent, error) { return newSSEReader(resp.Body).nextEvent(true) })
	if h.Subscribers() != 1 {
		t.Fatalf("%d subscribers", h.Subscribers())
	}

	// The client reads nothing more: once the socket buffers are full the
	// writes block, and the write timeout ends the stream.
	big := bytes.Repeat([]byte("x"), 64<<10)
	deadline := time.Now().Add(15 * time.Second)
	for h.Subscribers() > 0 {
		if time.Now().After(deadline) {
			t.Fatal("the stream of a client that does not read was never closed")
		}
		h.PublishRaw("r", "x", big)
		time.Sleep(time.Millisecond)
	}
}

func TestStreamEndsWhenServerDrains(t *testing.T) {
	h := NewLocalHub(quiet, nil, HubOptions{})
	ln, err := httpx.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan error, 1)
	go func() { served <- httpx.Serve(ctx, quiet, "test", ln, streamHandler(h, StreamOptions{})) }()

	var readers []*sseReader
	for range 3 {
		_, rd := open(t, "http://"+ln.Addr().String()+"/events?t=c:1", nil)
		readWithin(t, time.Second, func() (sseEvent, error) { return rd.nextEvent(true) })
		readers = append(readers, rd)
	}
	start := time.Now()
	cancel()
	for _, rd := range readers {
		errc := make(chan error, 1)
		go func() {
			for {
				if _, err := rd.next(); err != nil {
					errc <- err
					return
				}
			}
		}()
		select {
		case err := <-errc:
			if err != io.EOF && err != io.ErrUnexpectedEOF {
				t.Logf("stream ended with %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("the stream did not end when the server drained")
		}
	}
	select {
	case err := <-served:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the server did not stop")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("shutdown took %v: the streams held it", d)
	}
	waitFor(t, time.Second, "subscriptions to close", func() bool { return h.Subscribers() == 0 })
}

// TestStreamOutlivesReadTimeout: a stream stays open past
// Server.ReadTimeout (5 min in httpx), whose deadline covers the whole
// request.
func TestStreamOutlivesReadTimeout(t *testing.T) {
	h := NewLocalHub(quiet, nil, HubOptions{})
	srv := httptest.NewUnstartedServer(streamHandler(h, StreamOptions{PingInterval: 100 * time.Millisecond}))
	srv.Config.ReadTimeout = 300 * time.Millisecond
	srv.Start()
	t.Cleanup(srv.Close) // after the client's cleanups close its connection
	_, rd := open(t, srv.URL+"/events", nil)
	start := time.Now()
	pings := 0
	for time.Since(start) < 1200*time.Millisecond {
		ev := readWithin(t, time.Second, func() (sseEvent, error) { return rd.nextEvent(true) })
		if ev.Event == EventPing {
			pings++
		}
	}
	if pings < 8 {
		t.Errorf("%d pings in 1.2 s", pings)
	}
	if h.Subscribers() != 1 {
		t.Errorf("%d subscribers", h.Subscribers())
	}
}

// TestStreamThroughMiddleware: the standard middleware of the sites
// (app.Runtime.WebHandler) must not buffer the stream.
func TestStreamThroughMiddleware(t *testing.T) {
	reg := prometheus.NewRegistry()
	h := NewLocalHub(quiet, reg, HubOptions{})
	handler := httpx.Chain(streamHandler(h, StreamOptions{}),
		httpx.Recover(quiet), httpx.Observe(quiet, reg, 0), httpx.SecureHeaders, httpx.LimitBody(1<<20))
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	resp, rd := open(t, srv.URL+"/events?t=c:1", nil)
	if resp.Header.Get("Content-Security-Policy") == "" {
		t.Error("the middleware did not run")
	}
	readWithin(t, time.Second, func() (sseEvent, error) { return rd.nextEvent(true) })
	h.Publish(ContestChange{Contest: 1}.Event())
	if ev := readWithin(t, time.Second, func() (sseEvent, error) { return rd.nextEvent(false) }); ev.Event != EventContest {
		t.Errorf("got %v", ev)
	}
	if got := metric(t, reg, "cms_live_streams", nil); got != 1 {
		t.Errorf("streams gauge %v", got)
	}
}
