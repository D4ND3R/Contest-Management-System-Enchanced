// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package live

import (
	"errors"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/httpx"
)

// MaxPingInterval is the longest ping interval: proxies close idle
// connections (CMS EventSource uses 15 s too).
const MaxPingInterval = 15 * time.Second

// StreamOptions configures an event stream. The zero value of each field
// means its default.
type StreamOptions struct {
	// Topics are the topics the client may see; the site decides them
	// from the session (never from the request alone).
	Topics []Topic
	// PingInterval is the time between pings (default and maximum 15s).
	PingInterval time.Duration
	// WriteTimeout bounds every write: a client that does not read for
	// this long (at least half of it) is disconnected (default 30s, as CMS
	// EventSource).
	WriteTimeout time.Duration
	// ResyncEvent is the name of resync markers (default "resync"). The
	// ranking's JavaScript (cmsranking DataStore.js) listens for
	// "reload".
	ResyncEvent string
	// RetryMin and RetryMax bound the reconnection delay announced to
	// the browser with "retry:", random per connection so that clients
	// do not all come back at once after a restart (defaults 1s and 5s).
	RetryMin time.Duration
	RetryMax time.Duration
}

func (o StreamOptions) withDefaults() StreamOptions {
	if o.PingInterval <= 0 || o.PingInterval > MaxPingInterval {
		o.PingInterval = MaxPingInterval
	}
	if o.WriteTimeout <= 0 {
		o.WriteTimeout = 30 * time.Second
	}
	if !validName(o.ResyncEvent) {
		o.ResyncEvent = EventResync
	}
	if o.RetryMin <= 0 {
		o.RetryMin = time.Second
	}
	if o.RetryMax < o.RetryMin {
		o.RetryMax = max(5*time.Second, o.RetryMin)
	}
	return o
}

// Stream serves a server-sent event stream of opts.Topics until the
// client leaves, a write times out or the server drains
// (httpx.Draining). The site authenticates the request and chooses the
// topics; Stream does the rest (see the package documentation):
//
//   - headers Content-Type text/event-stream, Cache-Control no-cache and
//     X-Accel-Buffering: no (nginx does not buffer the stream);
//   - a ping right away and then every PingInterval;
//   - Last-Event-ID (or the last_event_id query parameter, as CMS)
//     replays missed events, or starts with a resync marker;
//   - bounded memory: the subscription buffer, then resync markers.
func (h *Hub) Stream(w http.ResponseWriter, r *http.Request, opts StreamOptions) {
	opts = opts.withDefaults()
	ctx := r.Context()
	rc := http.NewResponseController(w)
	// Streams outlive Server.ReadTimeout (5 min in httpx): net/http
	// clears the read deadline when it starts watching an idle
	// connection, but only once the request body is consumed; clearing
	// it here does not depend on that.
	_ = rc.SetReadDeadline(time.Time{})

	lastID := r.Header.Get("Last-Event-ID")
	if lastID == "" {
		lastID = r.URL.Query().Get("last_event_id")
	}
	sub := h.Subscribe(opts.Topics, lastID)
	defer sub.Close()
	h.m.streams.Inc()
	defer h.m.streams.Dec()

	hd := w.Header()
	hd.Set("Content-Type", "text/event-stream; charset=utf-8")
	// no-transform: proxies must not compress or rewrite the stream.
	hd.Set("Cache-Control", "no-cache, no-transform")
	hd.Set("X-Accel-Buffering", "no")
	hd.Del("Content-Length")
	w.WriteHeader(http.StatusOK)

	st := &stream{w: w, rc: rc, hub: h, sub: sub, opts: opts, buf: make([]byte, 0, 128)}
	retry := opts.RetryMin + time.Duration(rand.Int64N(int64(opts.RetryMax-opts.RetryMin)+1))
	st.buf = append(st.buf[:0], "retry: "...)
	st.buf = strconv.AppendInt(st.buf, retry.Milliseconds(), 10)
	st.buf = append(st.buf, "\n\n"...)
	st.write(st.buf)
	// Replayed events and a pending resync go before the first ping, which
	// carries the id the client is up to date with.
	st.pump(cap(sub.ch) + 1)
	st.ping(max(sub.start, st.lastID))
	if err := st.flush(); err != nil {
		if errors.Is(err, http.ErrNotSupported) {
			h.log.Error("live: the response writer cannot flush; event streams do not work through it")
		}
		return
	}

	ticker := time.NewTicker(opts.PingInterval)
	defer ticker.Stop()
	done, drain := ctx.Done(), httpx.Draining(ctx)
	for {
		if sub.held == nil {
			select {
			case <-done:
				return
			case <-drain:
				return
			case <-ticker.C:
				st.ping(0)
			case <-sub.notify:
			case m := <-sub.ch:
				sub.held = m
			}
		} else {
			// The last batch stopped at its limit with an event pending.
			select {
			case <-done:
				return
			case <-drain:
				return
			case <-ticker.C:
				st.ping(0)
			default:
			}
		}
		st.pump(2 * cap(sub.ch))
		if h.live.Load() != st.live {
			st.ping(0)
		}
		if st.flush() != nil {
			return
		}
	}
}

// stream is the writing side of one event stream.
type stream struct {
	w      http.ResponseWriter
	rc     *http.ResponseController
	hub    *Hub
	sub    *Subscription
	opts   StreamOptions
	buf    []byte // scratch for pings and resync markers
	lastID uint64 // highest id written
	live   bool   // "live" of the last ping
	dirty  bool   // written since the last flush
	// deadline is the write deadline of the connection.
	deadline time.Time
	err      error
}

func (st *stream) write(p []byte) {
	if st.err != nil {
		return
	}
	if !st.dirty {
		// A client that stops reading blocks the write once the socket
		// buffers are full; the deadline turns that into an error. It is
		// moved only when half of it is gone (setting it costs as much as
		// a tenth of a delivery), so a stuck client is dropped after
		// WriteTimeout/2 to WriteTimeout.
		if now := time.Now(); st.deadline.Sub(now) < st.opts.WriteTimeout/2 {
			st.deadline = now.Add(st.opts.WriteTimeout)
			_ = st.rc.SetWriteDeadline(st.deadline)
		}
		st.dirty = true
	}
	_, st.err = st.w.Write(p)
}

func (st *stream) flush() error {
	if st.err == nil && st.dirty {
		st.err = st.rc.Flush()
		st.dirty = false
	}
	return st.err
}

// pump writes up to limit pending events and resync markers.
func (st *stream) pump(limit int) {
	for i := 0; i < limit && st.err == nil; i++ {
		m := st.sub.poll()
		if m == nil {
			return
		}
		if m.marker {
			spread := ResyncSpread(m.Reason, st.hub.Subscribers())
			st.buf = appendFrame(st.buf[:0], m.Seq, st.opts.ResyncEvent, resyncData(m.Reason, spread))
			st.write(st.buf)
		} else {
			st.write(m.frame)
		}
		st.lastID = max(st.lastID, m.Seq)
	}
}

// ping writes a ping; id > 0 also sets the client's last event id.
func (st *stream) ping(id uint64) {
	st.live = st.hub.live.Load()
	d := make([]byte, 0, 80)
	d = append(d, `{"v":`...)
	d = strconv.AppendInt(d, Version, 10)
	d = append(d, `,"interval":`...)
	d = strconv.AppendInt(d, st.opts.PingInterval.Milliseconds(), 10)
	d = append(d, `,"live":`...)
	d = strconv.AppendBool(d, st.live)
	d = append(d, `,"time":`...)
	d = strconv.AppendInt(d, time.Now().UnixMilli(), 10)
	// How long pages spread what they all do on a broadcast event (the
	// notifications fetched on an announcement), as a resync.
	d = append(d, `,"spread":`...)
	d = strconv.AppendInt(d, ResyncSpread("reconnect", st.hub.Subscribers()).Milliseconds(), 10)
	d = append(d, '}')
	st.buf = appendFrame(st.buf[:0], id, EventPing, d)
	st.write(st.buf)
}

func resyncData(reason string, spread time.Duration) []byte {
	return []byte(`{"v":` + strconv.Itoa(Version) + `,"reason":"` + reason +
		`","spread":` + strconv.FormatInt(spread.Milliseconds(), 10) + `}`)
}
