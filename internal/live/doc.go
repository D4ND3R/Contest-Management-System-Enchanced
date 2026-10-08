// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Package live carries the live events of the three web sites (R5.8,
// R6.9, R7.2): the dispatcher and the admin site publish small events,
// every web process receives them through one Valkey subscription and
// streams them to the browsers as server-sent events.
//
//	dispatcher, admin-web          contest-web, admin-web (each process)
//	Publisher ──PUBLISH──▶ Valkey ──PSUBSCRIBE──▶ Hub ──▶ Stream ──▶ browsers
//	          <prefix>live:<topic>   <prefix>live:*  (one)   (one per page)
//
// The ranking (D14) has no Valkey: it uses a local hub (NewLocalHub,
// PublishRaw) and the same Stream.
//
// Events may be lost by design (D1): a slow page, a Valkey reconnection
// or a gap in the ids turns into a "resync" marker and the page reloads
// the state of its live regions. Pages never trust events for data: an
// event says what changed (ids) and the page fetches it, with the same
// permission checks as a page load.
//
// # Publishing
//
// NewPublisher(kv, log, metrics, PublisherOptions{}) and app.Run its Run.
// Publish(events...) never blocks and never fails: it queues (bounded;
// overflow is dropped, logged and counted) and Run sends batches with one
// Lua script call that numbers each message from the installation-wide
// counter <prefix>live-seq and publishes it on <prefix>live:<topic>.
// Publish after the transaction commits (collect the events inside
// Store.InTx, which may retry, and publish after it returns nil). Tests
// of other packages use a Recorder.
//
// Throttled events (scoreboard, judging) go out at most once per
// GroupInterval (1 s) per event name and topics: the first one at once,
// the rest merged and sent when the interval ends. The limit holds per
// publishing process (one dispatcher).
//
// # Topics
//
//	p:<participation>  ParticipationTopic  one contestant's pages
//	c:<contest>        ContestTopic        every contestant page of a contest
//	s:<contest>        ScoreboardTopic     pages showing the scoreboard
//	a:<contest>        AdminContestTopic   admin pages of a contest
//	a                  AdminTopic          every admin page (workers, global counters)
//
// The site chooses the topics from the session: a contestant gets its own
// p: and its contest's c: (and s: when the contest shows the scoreboard),
// an administrator gets a and the a:/s: of the contest being viewed.
//
// # Events (version 1)
//
// The "event:" field is the name; "data:" is a JSON object with "v": 1.
// Ids are JSON numbers except opaque (a string: 63-bit). New fields may
// be added without changing "v".
//
//	submission    a:<c>, p:<p> (active dataset only)
//	              {"v":1,"contest":1,"participation":2,"task":3,"dataset":4,
//	               "submission":5,"opaque":"9007199254740993","status":"evaluating","new":true}
//	              status: compiling | compilation_failed | evaluating | scoring | scored
//	              new: the submission was just received (admin lists add a row)
//	user_test     p:<p>, a:<c>
//	              {"v":1,"contest":1,"participation":2,"task":3,"user_test":6,"status":"evaluated"}
//	              status: compiling | compilation_failed | evaluating | evaluated
//	task_score    p:<p>     {"v":1,"contest":1,"participation":2,"task":3}
//	announcement  c:<c>, a:<c>  {"v":1,"contest":1,"announcement":7}
//	message       p:<p>, a:<c>  {"v":1,"contest":1,"participation":2,"message":8}
//	answer        p:<p>     {"v":1,"contest":1,"participation":2,"question":9}
//	question      a:<c>     {"v":1,"contest":1,"participation":2,"question":9,"change":"new"}
//	              change: new | answered | ignored | claimed | released
//	contest       c:<c>, a:<c>  {"v":1,"contest":1}   (times, tasks or settings changed)
//	participation p:<p>, a:<c>  {"v":1,"contest":1,"participation":2}
//	              (extra time, delay, starting time... of one contestant changed)
//	scoreboard    s:<c>, throttled  {"v":1,"contest":1,"participations":[2,5]}
//	              or {"v":1,"contest":1,"all":true}: reload the whole scoreboard
//	judging       a or a:<c>, throttled  {"v":1,"contest":1,"counters":{"queued":3,...}}
//	              counters: queued, in_flight, retrying, dead, compiling,
//	              evaluating, scoring, workers (pages show the keys they know)
//	worker        a         {"v":1,"worker":"w1","state":"up"}
//	              state: up | down | draining | broken
//
// Stream-level events, written by Stream itself:
//
//	ping    {"v":1,"interval":15000,"live":true,"time":1767225600000}
//	        interval: ms between pings; live: false while this server
//	        receives no events (Valkey down); time: server clock (ms).
//	resync  {"v":1,"reason":"reconnect","spread":7500}
//	        reason: overflow | reconnect | gap; spread: ms (ResyncSpread:
//	        2 s for overflow, otherwise 5 ms per open stream of the
//	        server, 2–30 s). The ranking names it "reload", which its CMS
//	        JavaScript knows.
//
// # Ids and Last-Event-ID
//
// Every event and resync marker has an id: the lowercase hexadecimal
// sequence number of the installation (Valkey hubs) or microseconds since
// the epoch (local hubs, as RankingWebServer). Ids grow; the counter
// starts at the time in µs so it also grows across a Valkey that lost
// its data. A reconnecting browser sends Last-Event-ID (a page that
// recreates its EventSource passes ?last_event_id=, as cmsranking does):
// the hub replays the missed events from its history (4096 events), or,
// when the id is unknown, older than the history or the missed events do
// not fit in the subscriber's buffer, starts with a resync. A hub that
// sees a gap in the sequence resyncs every subscriber. A local hub also
// accepts an id up to the current time (cmsranking passes the time of
// its data, usually later than the last event).
//
// A page reads Hub.LastID() before reading its data, renders it (for
// example data-live-since) and passes it as last_event_id when it opens
// the stream: what was published between the render and the connection
// is replayed.
//
// # Stream protocol
//
// A stream starts with "retry: <ms>" (1–5 s, random per connection, so
// clients do not reconnect all at once after a restart), the replayed
// events (or a resync), and a ping carrying the id the client is up to
// date with, all in the first write. Then events as they come, a ping
// every interval (at most 15 s), and an immediate ping when "live"
// changes. Headers: Content-Type text/event-stream; charset=utf-8,
// Cache-Control no-cache, no-transform, X-Accel-Buffering: no.
//
// The stream ends when the client leaves, a write blocks for
// WriteTimeout (15–30 s), or the server drains (httpx.Draining): the
// browser reconnects by itself after "retry". Memory per stream is
// bounded by HubOptions.Buffer (64 events) plus one batch being written:
// a client that does not keep up gets a resync instead.
//
// # Browser contract (live.js, F5–F7)
//
// Connection indicator next to the user name (R5.8):
//
//   - green: the EventSource is open, the last ping is less than
//     2 × interval old and said live: true;
//   - yellow: reconnecting (an error event with readyState CONNECTING),
//     a ping said live: false, or the last ping is 2–3 intervals old;
//   - red: no ping for 3 × interval, or the EventSource is CLOSED (the
//     server answered an error or a proxy a 502: browsers stop
//     retrying); live.js recreates it with jittered exponential backoff
//     (1 s → 60 s) passing ?last_event_id=.
//
// While not green, the page polls its live regions (jittered, every
// 15–30 s); it stops polling at the next ping with live: true (a resync
// comes first if needed). On resync it reloads every live region after a
// random delay below spread; on an event it reloads the affected regions,
// grouped (at most once per second). Regions are marked with data-live
// attributes naming the events that refresh them; events of other
// regions are ignored.
//
// Browsers open at most 6 HTTP/1.1 connections per host, and every open
// stream holds one: a contestant with many tabs would leave the pages
// without connections. Serve the sites over HTTP/2 (the reverse proxy
// does it with TLS), or let live.js close the stream of a hidden tab
// (visibilitychange) and reopen it with ?last_event_id= when the tab is
// shown again.
//
// # Serving
//
//	hub := live.NewHub(kvc, rt.Log, rt.Metrics, live.HubOptions{})
//	rt.Health.Register("live", hub.Check)
//	mux.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) {
//		// authenticate, then:
//		hub.Stream(w, r, live.StreamOptions{Topics: topics})
//	})
//	app.Run(ctx, hub.Run, rt.Serve(...))
//
// Stream works through the internal/httpx middleware (its writer
// supports Flush and deadlines through Unwrap; there is no compression
// in-process). A reverse proxy must not buffer or compress */events
// (X-Accel-Buffering covers nginx buffering; exclude the path from gzip
// in nginx and from encode in Caddy).
package live
