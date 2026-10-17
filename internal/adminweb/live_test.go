// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package adminweb

import (
	"bufio"
	"context"
	"log/slog"
	"net/http"
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

// openStream opens path as the client and reads its events.
func openStream(t *testing.T, c *testClient, path string) *sseStream {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.Site.URL+path, nil)
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

// TestEvents streams the admin topics (R6.9): "a" on every page, and
// "a:<contest>" on the pages of that contest only; the page carries the
// indicator configuration; the stream does not refresh the session.
func TestEvents(t *testing.T) {
	ts := newTestSite(t, testOptions{Hub: true})
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
	c.Login("ro")
	cid := itoa(ts.Contest)
	page := c.Get("/contest/" + cid + "/overview")
	since := regexp1(page.Body, `data-live-since="([0-9a-f]+)"`)
	if since == "" || !strings.Contains(page.Body, `data-live-url="../../contest/`+cid+`/events"`) ||
		!strings.Contains(page.Body, `id="live_indicator"`) {
		t.Fatalf("no live configuration in the page")
	}
	global := openStream(t, c, "/events")
	contest := openStream(t, c, "/contest/"+cid+"/events?last_event_id="+since)
	h := contest.resp.Header
	if contest.resp.StatusCode != http.StatusOK || h.Get("Content-Type") != "text/event-stream; charset=utf-8" ||
		h.Get("X-Accel-Buffering") != "no" || strings.Contains(h.Get("Set-Cookie"), cookieName) {
		t.Fatalf("stream: %d %v", contest.resp.StatusCode, h)
	}
	pub.Publish(
		live.Question{Contest: int64(ts.Contest) + 1000, Participation: 1, ID: 1, Change: live.QuestionNew}.Event(),
		live.Question{Contest: int64(ts.Contest), Participation: 1, ID: 2, Change: live.QuestionNew}.Event(),
		live.Worker{Name: "w1", State: "up"}.Event(),
	)
	if ev := contest.next(t); ev.Event != live.EventQuestion || !strings.Contains(ev.Data, `"question":2`) {
		t.Fatalf("contest stream (the other contest's question must not arrive): %+v", ev)
	}
	if ev := contest.next(t); ev.Event != live.EventWorker {
		t.Fatalf("contest stream, worker: %+v", ev)
	}
	if ev := global.next(t); ev.Event != live.EventWorker {
		t.Fatalf("global stream (no question of a contest): %+v", ev)
	}

	if r := ts.Client().Get("/events"); r.Status != http.StatusFound {
		t.Errorf("anonymous stream: %d", r.Status)
	}
	if r := c.Get("/contest/999999/events"); r.Status != http.StatusNotFound {
		t.Errorf("unknown contest: %d", r.Status)
	}
}

// TestEventsWithoutHub: without live updates the stream answers 503, the
// page has no indicator and polls as CMS.
func TestEventsWithoutHub(t *testing.T) {
	ts := newTestSite(t, testOptions{})
	c := ts.Client()
	c.Login("ro")
	if r := c.Get("/events"); r.Status != http.StatusServiceUnavailable {
		t.Errorf("events without a hub: %d", r.Status)
	}
	body := c.Get("/").Body
	if strings.Contains(body, "live_indicator") || !strings.Contains(body, "setInterval(function() { utils.update_notifications(); }, 15000);") {
		t.Error("a page without a hub")
	}
}
