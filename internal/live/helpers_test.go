// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package live

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var quiet = slog.New(slog.DiscardHandler)

// sseEvent is one event of a stream as a browser sees it.
type sseEvent struct {
	ID    string // the id field of this event ("" if none)
	Event string // "" for a block with only retry or comments
	Data  string
	Retry string
	At    time.Time
}

func (e sseEvent) String() string {
	return fmt.Sprintf("{id=%s event=%s data=%s retry=%s}", e.ID, e.Event, e.Data, e.Retry)
}

func (e sseEvent) json(t testing.TB, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(e.Data), v); err != nil {
		t.Fatalf("data of %v: %v", e, err)
	}
}

// sseReader parses a text/event-stream (enough of the spec for tests).
type sseReader struct {
	r *bufio.Reader
}

func newSSEReader(r io.Reader) *sseReader { return &sseReader{r: bufio.NewReader(r)} }

// next returns the next block (event, or retry-only block).
func (s *sseReader) next() (sseEvent, error) {
	var ev sseEvent
	var data []string
	seen := false
	for {
		line, err := s.r.ReadString('\n')
		if err != nil {
			return ev, err
		}
		line = strings.TrimSuffix(line, "\n")
		if line == "" {
			if !seen {
				continue
			}
			ev.Data = strings.Join(data, "\n")
			ev.At = time.Now()
			return ev, nil
		}
		seen = true
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "id":
			ev.ID = value
		case "event":
			ev.Event = value
		case "data":
			data = append(data, value)
		case "retry":
			ev.Retry = value
		}
	}
}

// nextEvent skips retry-only blocks and, unless pings is set, pings.
func (s *sseReader) nextEvent(pings bool) (sseEvent, error) {
	for {
		ev, err := s.next()
		if err != nil || (ev.Event != "" && (pings || ev.Event != EventPing)) {
			return ev, err
		}
	}
}

// open starts a stream request and returns its reader; the body is closed
// at the end of the test.
func open(t testing.TB, url string, header http.Header) (*http.Response, *sseReader) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	return resp, newSSEReader(resp.Body)
}

// readWithin runs next with a deadline.
func readWithin(t testing.TB, d time.Duration, next func() (sseEvent, error)) sseEvent {
	t.Helper()
	type res struct {
		ev  sseEvent
		err error
	}
	ch := make(chan res, 1)
	go func() {
		ev, err := next()
		ch <- res{ev, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("reading the stream: %v", r.err)
		}
		return r.ev
	case <-time.After(d):
		t.Fatalf("no event within %v", d)
	}
	return sseEvent{}
}

// waitFor polls cond until it holds or d passes.
func waitFor(t testing.TB, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// nextWithin returns the next message of a subscription.
func nextWithin(t testing.TB, s *Subscription, d time.Duration) *Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	m, err := s.Next(ctx)
	if err != nil {
		t.Fatalf("no message within %v: %v", d, err)
	}
	return m
}

// expectNothing checks that a subscription stays quiet for d.
func expectNothing(t testing.TB, s *Subscription, d time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	if m, err := s.Next(ctx); err == nil {
		t.Fatalf("unexpected message %s %s %s", m.ID(), m.Event, m.Data)
	}
}

// metric returns the value of a counter or gauge of reg with the given
// labels (nil: the only series).
func metric(t testing.TB, reg *prometheus.Registry, name string, labels map[string]string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
	series:
		for _, m := range mf.GetMetric() {
			for _, lp := range m.GetLabel() {
				if labels[lp.GetName()] != lp.GetValue() {
					continue series
				}
			}
			switch {
			case m.GetCounter() != nil:
				return m.GetCounter().GetValue()
			case m.GetGauge() != nil:
				return m.GetGauge().GetValue()
			}
		}
	}
	t.Fatalf("metric %s %v not found", name, labels)
	return 0
}
