/* Contest Management System (Go fork)
 * SPDX-License-Identifier: AGPL-3.0-or-later
 * Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
 *
 * live.js: live updates of the pages of the three sites (R5.8, R6.9), the
 * browser side of internal/live (see "Browser contract" in its doc.go).
 * No framework: it works with or without jQuery.
 *
 * Configuration, on the first element with data-live-url (usually the
 * connection indicator next to the user name):
 *
 *   data-live-url          the event stream (server-sent events)
 *   data-live-since        id of the last event before the page read its
 *                          data: the first connection replays what came
 *                          after it
 *   data-live-title-green, data-live-title-yellow, data-live-title-red
 *                          the indicator's tooltip in each state
 *
 * The indicator gets the Bootstrap 2 classes badge-success (green),
 * badge-warning (yellow) and badge-important (red), or label-* if it is a
 * .label. Green: the stream is open, a ping came less than 2 intervals
 * ago and said the server receives events. Yellow: connecting, or the
 * server lost its event source, or the last ping is 2-3 intervals old.
 * Red: no ping for 3 intervals or the stream was closed; live.js then
 * opens a new one with jittered exponential backoff (1 s to 60 s).
 *
 * Live regions: elements with an id and data-live="event1 event2". When
 * one of those events arrives (or after a resync, or every 15-30 s while
 * the indicator is not green and the tab is visible) live.js fetches the
 * page again and replaces
 * each affected region with its new version (at most one fetch per
 * second). data-live-match-<field>="value" limits a region to the events
 * whose data has that value (data-live-match-task="3").
 *
 * Page code:
 *
 *   CMS.live.on("announcement message resync", function (name, data) {...})
 *       runs for those events ("resync": after events were lost);
 *   CMS.live.polling()  true while the page must poll (not green);
 *   CMS.live.state()    "green", "yellow", "red" or "off" (no stream);
 *   CMS.live.refresh()  reloads every live region now;
 *   CMS.live.reload_page()  reloads the page after a random delay (up to
 *       10 s, so that a whole contest does not reload at once), waiting
 *       while the user is filling in a form.
 *
 * Hidden tabs close their stream after 60 s (browsers allow few
 * connections per host on HTTP/1.1) and reopen it, with the last event id,
 * when shown again.
 */

"use strict";

var CMS = window.CMS = window.CMS || {};

CMS.live = (function () {
    // Events the servers send (internal/live); page code may add others
    // with on().
    var KNOWN = ["submission", "user_test", "task_score", "announcement",
                 "message", "answer", "question", "contest", "participation",
                 "scoreboard", "judging", "worker"];
    var BACKOFF_MIN = 1000, BACKOFF_MAX = 60000;
    var HIDDEN_CLOSE = 60000;

    var cfg = null;          // the configuration element
    var es = null;           // the EventSource
    var lastId = "";         // id of the last event received
    var interval = 15000;    // ping interval announced by the server
    var lastPing = 0;        // when the last ping came (ms)
    var openedAt = 0;        // when the stream was opened (ms)
    var serverLive = false;  // the last ping said live: true
    var current = "off";     // indicator state
    var backoff = BACKOFF_MIN;
    var reconnectTimer = null, pollTimer = null, hiddenTimer = null;
    var pausedHidden = false;
    var handlers = {};       // event name -> [functions]
    var listening = {};      // event names with a listener on es
    var pending = {};        // region ids waiting for a refresh
    var pendingAll = false;
    var refreshTimer = null, lastRefresh = 0, refreshing = false;
    var reloadTimer = null;

    function now() { return Date.now(); }
    function jitter(lo, hi) { return lo + Math.random() * (hi - lo); }

    function names(s) {
        return String(s).split(/\s+/).filter(function (x) { return x !== ""; });
    }

    function call(name, data) {
        (handlers[name] || []).slice().forEach(function (fn) {
            try {
                fn(name, data);
            } catch (e) {
                if (window.console) { console.error(e); }
            }
        });
    }

    // Indicator.

    function setState(s) {
        if (s === current) {
            return;
        }
        var was = current;
        current = s;
        if (cfg !== null) {
            var label = cfg.classList.contains("label");
            var p = label ? "label-" : "badge-";
            cfg.classList.remove(p + "success", p + "warning", p + "important");
            if (s !== "off") {
                cfg.classList.add(p + (s === "green" ? "success" : s === "yellow" ? "warning" : "important"));
            }
            var title = cfg.getAttribute("data-live-title-" + (s === "off" ? "red" : s));
            if (title !== null) {
                cfg.setAttribute("title", title);
            }
        }
        if (s === "green") {
            stopPolling();
        } else if (was === "green" || pollTimer === null) {
            startPolling();
        }
    }

    function evaluate() {
        if (es === null) {
            setState(pausedHidden ? "yellow" : "red");
            return;
        }
        var age = now() - (lastPing > 0 ? lastPing : openedAt);
        if (es.readyState === 2 /* CLOSED */ || age > 3 * interval) {
            setState("red");
        } else if (es.readyState === 1 /* OPEN */ && lastPing > 0 && serverLive && age < 2 * interval) {
            setState("green");
        } else {
            setState("yellow");
        }
    }

    // Polling while not green: the live regions every 15-30 s (the pages
    // poll their own data, such as CMS's notifications, with polling()).

    function startPolling() {
        stopPolling();
        pollTimer = setTimeout(function tick() {
            // A hidden tab does not poll its regions: when shown it
            // reconnects, and the missed events (or a resync) refresh
            // them.
            if (!document.hidden) {
                refresh();
            }
            pollTimer = setTimeout(tick, jitter(15000, 30000));
        }, jitter(15000, 30000));
    }

    function stopPolling() {
        if (pollTimer !== null) {
            clearTimeout(pollTimer);
            pollTimer = null;
        }
    }

    // Stream.

    function streamURL() {
        var url = cfg.getAttribute("data-live-url");
        if (lastId !== "") {
            url += (url.indexOf("?") < 0 ? "?" : "&") + "last_event_id=" + encodeURIComponent(lastId);
        }
        return url;
    }

    function listen(name) {
        if (es === null || listening[name]) {
            return;
        }
        listening[name] = true;
        es.addEventListener(name, function (e) {
            if (e.lastEventId) {
                lastId = e.lastEventId;
            }
            var data = null;
            try {
                data = JSON.parse(e.data);
            } catch (err) {
                return;
            }
            event(name, data);
        });
    }

    function open() {
        if (typeof window.EventSource === "undefined" || cfg === null) {
            setState("red");
            return;
        }
        close();
        lastPing = 0;
        openedAt = now();
        es = new EventSource(streamURL());
        listening = {};
        es.addEventListener("ping", function (e) {
            if (e.lastEventId) {
                lastId = e.lastEventId;
            }
            var d = {};
            try {
                d = JSON.parse(e.data);
            } catch (err) {
                // Keep the previous values.
            }
            if (d.interval > 0) {
                interval = d.interval;
            }
            serverLive = d.live !== false;
            lastPing = now();
            backoff = BACKOFF_MIN;
            evaluate();
        });
        es.addEventListener("resync", function (e) {
            if (e.lastEventId) {
                lastId = e.lastEventId;
            }
            var d = {};
            try {
                d = JSON.parse(e.data);
            } catch (err) {
                // Resync anyway.
            }
            resync(d.spread > 0 ? d.spread : 2000);
        });
        es.addEventListener("open", evaluate);
        es.addEventListener("error", function () {
            if (es !== null && es.readyState === 2 /* CLOSED */) {
                // An error status or a proxy error: browsers give up.
                reconnect();
            }
            evaluate();
        });
        KNOWN.forEach(listen);
        Object.keys(handlers).forEach(listen);
        regionEvents().forEach(listen);
        evaluate();
    }

    function close() {
        if (es !== null) {
            es.close();
            es = null;
        }
    }

    function reconnect() {
        close();
        if (reconnectTimer !== null) {
            return;
        }
        var delay = jitter(backoff / 2, backoff);
        backoff = Math.min(backoff * 2, BACKOFF_MAX);
        reconnectTimer = setTimeout(function () {
            reconnectTimer = null;
            if (!pausedHidden) {
                open();
            }
        }, delay);
        evaluate();
    }

    function watchdog() {
        // No ping for 3 intervals (or none since the stream opened): the
        // connection is silently dead or buffered by a proxy, or the
        // laptop slept. Start over.
        if (es !== null && now() - (lastPing > 0 ? lastPing : openedAt) > 3 * interval) {
            reconnect();
        }
        evaluate();
    }

    function visibility() {
        if (document.hidden) {
            if (hiddenTimer === null) {
                hiddenTimer = setTimeout(function () {
                    hiddenTimer = null;
                    pausedHidden = true;
                    close();
                    evaluate();
                }, HIDDEN_CLOSE);
            }
            return;
        }
        if (hiddenTimer !== null) {
            clearTimeout(hiddenTimer);
            hiddenTimer = null;
        }
        if (pausedHidden) {
            pausedHidden = false;
            backoff = BACKOFF_MIN;
            open();
        }
    }

    // Events and regions.

    function regionEvents() {
        var out = [];
        Array.prototype.forEach.call(document.querySelectorAll("[data-live]"), function (el) {
            names(el.getAttribute("data-live")).forEach(function (n) {
                if (out.indexOf(n) < 0) {
                    out.push(n);
                }
            });
        });
        return out;
    }

    function matches(el, data) {
        for (var i = 0; i < el.attributes.length; i++) {
            var a = el.attributes[i];
            if (a.name.indexOf("data-live-match-") === 0) {
                var field = a.name.substring("data-live-match-".length);
                if (data === null || data[field] === undefined || String(data[field]) !== a.value) {
                    return false;
                }
            }
        }
        return true;
    }

    function event(name, data) {
        Array.prototype.forEach.call(document.querySelectorAll("[data-live]"), function (el) {
            if (el.id && names(el.getAttribute("data-live")).indexOf(name) >= 0 && matches(el, data)) {
                pending[el.id] = true;
                scheduleRefresh(0);
            }
        });
        call(name, data);
    }

    function resync(spread) {
        setTimeout(function () {
            refresh();
            call("resync", null);
        }, Math.random() * spread);
    }

    function scheduleRefresh(delay) {
        if (refreshTimer !== null) {
            return;
        }
        // At most one fetch per second, whatever the number of events.
        var wait = Math.max(delay, lastRefresh + 1000 - now(), 0);
        refreshTimer = setTimeout(function () {
            refreshTimer = null;
            doRefresh();
        }, wait);
    }

    function refresh() {
        if (document.querySelector("[data-live]") === null) {
            return;
        }
        pendingAll = true;
        scheduleRefresh(0);
    }

    function doRefresh() {
        if (refreshing) {
            scheduleRefresh(1000);
            return;
        }
        var all = pendingAll;
        var ids = Object.keys(pending);
        pending = {};
        pendingAll = false;
        if (all) {
            ids = [];
            Array.prototype.forEach.call(document.querySelectorAll("[data-live]"), function (el) {
                if (el.id) {
                    ids.push(el.id);
                }
            });
        }
        if (ids.length === 0 || typeof window.fetch === "undefined") {
            return;
        }
        refreshing = true;
        lastRefresh = now();
        fetch(window.location.href, {credentials: "same-origin", cache: "no-store",
                                     headers: {"X-CMS-Live": "1"}})
            .then(function (r) {
                if (!r.ok) {
                    throw new Error("live: HTTP " + r.status);
                }
                return r.text();
            })
            .then(function (html) {
                var doc = new DOMParser().parseFromString(html, "text/html");
                var done = [];
                ids.forEach(function (id) {
                    var old = document.getElementById(id);
                    var neu = doc.getElementById(id);
                    if (old !== null && neu !== null && neu.hasAttribute("data-live")) {
                        old.parentNode.replaceChild(document.importNode(neu, true), old);
                        done.push(id);
                    }
                });
                if (done.length > 0) {
                    document.dispatchEvent(new CustomEvent("cms:live-refreshed", {detail: {ids: done}}));
                }
            })
            .catch(function (e) {
                if (window.console) { console.warn(e); }
            })
            .then(function () {
                refreshing = false;
            });
    }

    // Page reloads (contest times changed...).

    function editing() {
        var a = document.activeElement;
        if (a && /^(INPUT|TEXTAREA|SELECT)$/.test(a.tagName) && a.id !== "lang") {
            return true;
        }
        var busy = false;
        Array.prototype.forEach.call(document.querySelectorAll("textarea, input[type=file]"), function (el) {
            if ((el.type === "file" && el.files && el.files.length > 0) || (el.tagName === "TEXTAREA" && el.value !== "")) {
                busy = true;
            }
        });
        return busy;
    }

    function reloadPage() {
        if (reloadTimer !== null) {
            return;
        }
        reloadTimer = setTimeout(function tryReload() {
            if (editing()) {
                reloadTimer = setTimeout(tryReload, 5000);
                return;
            }
            window.location.reload();
        }, jitter(0, 10000));
    }

    function start() {
        if (cfg !== null) {
            return;
        }
        cfg = document.querySelector("[data-live-url]");
        if (cfg === null) {
            return;
        }
        lastId = cfg.getAttribute("data-live-since") || "";
        document.addEventListener("visibilitychange", visibility);
        setInterval(watchdog, 1000);
        open();
    }

    if (document.readyState === "loading") {
        document.addEventListener("DOMContentLoaded", start);
    } else {
        start();
    }

    return {
        on: function (events, fn) {
            names(events).forEach(function (n) {
                (handlers[n] = handlers[n] || []).push(fn);
                if (n !== "resync") {
                    listen(n);
                }
            });
        },
        polling: function () { return current !== "green"; },
        state: function () { return current; },
        refresh: refresh,
        reload_page: reloadPage,
        start: start
    };
})();
