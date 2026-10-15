// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package contestweb

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
)

// sseEvent is one block of an event stream.
type sseEvent struct {
	ID, Event, Data string
}

// sseStream reads an event stream in the background.
type sseStream struct {
	resp   *http.Response
	events chan sseEvent
}

// openStream opens <path> as the client and reads its events.
func openStream(t *testing.T, c *testClient, path string, header ...string) *sseStream {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.Site.URL+path, nil)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	s := &sseStream{resp: resp, events: make(chan sseEvent, 64)}
	t.Cleanup(func() {
		cancel()
		resp.Body.Close()
	})
	go func() {
		defer close(s.events)
		br := bufio.NewReader(resp.Body)
		var ev sseEvent
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\n")
			switch {
			case line == "":
				if ev.Event != "" {
					s.events <- ev
				}
				ev = sseEvent{}
			case strings.HasPrefix(line, "id: "):
				ev.ID = line[4:]
			case strings.HasPrefix(line, "event: "):
				ev.Event = line[7:]
			case strings.HasPrefix(line, "data: "):
				ev.Data = line[6:]
			}
		}
	}()
	return s
}

// next returns the next event that is not a ping.
func (s *sseStream) next(t *testing.T) sseEvent {
	t.Helper()
	for {
		select {
		case ev, ok := <-s.events:
			if !ok {
				t.Fatal("the stream ended")
			}
			if ev.Event != live.EventPing {
				return ev
			}
		case <-time.After(10 * time.Second):
			t.Fatal("no event")
		}
	}
}

// TestEvents streams the live events of a participant (R5.8): their own
// topic and their contest's, nothing of other participants; the headers
// proxies need; no refresh of the login cookie; 403 without a login.
func TestEvents(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{Hub: true})
	alice, bob := ts.Participation("alice"), ts.Participation("bob")
	pub := live.NewPublisher(ts.KV, slog.New(slog.DiscardHandler), prometheus.NewRegistry(), live.PublisherOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = pub.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	c := ts.Client()
	c.Login("alice", "demo")
	page := c.Get("/demo")
	since := regexpFind(page.Body, `data-live-since="([0-9a-f]+)"`)
	if since == "" || !strings.Contains(page.Body, `data-live-url="./demo/events"`) {
		t.Fatalf("no live configuration in the page:\n%s", page.Body)
	}

	// Published after the page was rendered, before the stream opens:
	// replayed thanks to last_event_id.
	pub.Publish(live.PrivateMessage{Contest: int64(ts.Contest), Participation: int64(alice), ID: 1}.Event())
	waitHub(t, ts.Hub, since)

	s := openStream(t, c, "/demo/events?last_event_id="+since)
	h := s.resp.Header
	if s.resp.StatusCode != http.StatusOK || h.Get("Content-Type") != "text/event-stream; charset=utf-8" ||
		h.Get("X-Accel-Buffering") != "no" || !strings.Contains(h.Get("Cache-Control"), "no-cache") {
		t.Fatalf("stream: %d %v", s.resp.StatusCode, h)
	}
	if strings.Contains(h.Get("Set-Cookie"), "demo_login") {
		t.Error("the stream refreshed the login cookie")
	}
	if ev := s.next(t); ev.Event != live.EventMessage || !strings.Contains(ev.Data, `"message":1`) {
		t.Fatalf("replayed event: %+v", ev)
	}

	pub.Publish(
		live.PrivateMessage{Contest: int64(ts.Contest), Participation: int64(bob), ID: 2}.Event(),
		live.Announcement{Contest: int64(ts.Contest), ID: 3}.Event(),
		live.Announcement{Contest: int64(ts.Contest) + 1000, ID: 4}.Event(),
		live.Answer{Contest: int64(ts.Contest), Participation: int64(alice), Question: 5}.Event(),
	)
	ev := s.next(t)
	var d map[string]any
	if err := json.Unmarshal([]byte(ev.Data), &d); err != nil || ev.Event != live.EventAnnouncement || d["announcement"] != float64(3) {
		t.Fatalf("announcement: %+v", ev)
	}
	if ev = s.next(t); ev.Event != live.EventAnswer || !strings.Contains(ev.Data, `"question":5`) {
		t.Fatalf("answer (bob's message and the other contest's announcement must not arrive): %+v", ev)
	}

	// Without a login: the JSON 403 of the asynchronous requests.
	if r := ts.Client().Get("/demo/events"); r.Status != http.StatusForbidden {
		t.Errorf("anonymous stream: %d", r.Status)
	}
}

// TestEventsWithoutHub: without live updates the stream answers 503 and
// the pages poll as in CMS.
func TestEventsWithoutHub(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{})
	c := ts.Client()
	c.Login("alice", "demo")
	if r := c.Get("/demo/events"); r.Status != http.StatusServiceUnavailable {
		t.Errorf("events without a hub: %d", r.Status)
	}
	if r := c.Get("/demo"); !strings.Contains(r.Body, `data-live-since=""`) {
		t.Error("a page without a hub has a live id")
	}
}

// waitHub waits until the hub has received an event after id.
func waitHub(t *testing.T, h *live.Hub, id string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for h.LastID() == id {
		if time.Now().After(deadline) {
			t.Fatal("the hub received nothing")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func regexpFind(s, re string) string {
	m := regexp.MustCompile(re).FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return m[1]
}
