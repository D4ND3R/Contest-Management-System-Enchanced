// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package httpx

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/obs"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestObserveUsesRoutePattern(t *testing.T) {
	reg := prometheus.NewRegistry()
	mux := NewRouter()
	mux.HandleFunc("GET /tasks/{name}/submissions", func(w http.ResponseWriter, r *http.Request) {
		// Copying the request must not lose the route label.
		r = r.WithContext(context.WithValue(r.Context(), struct{}{}, 1))
		w.WriteHeader(http.StatusTeapot)
	})
	h := Chain(mux, Recover(quiet), Observe(quiet, reg, 0))

	for _, p := range []string{"/tasks/a/submissions", "/tasks/b/submissions", "/nowhere"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", p, nil))
	}
	if got := counterValue(t, reg, "GET /tasks/{name}/submissions", "418"); got != 2 {
		t.Errorf("route counter = %v, want 2", got)
	}
	if got := counterValue(t, reg, "unmatched", "404"); got != 1 {
		t.Errorf("unmatched counter = %v", got)
	}
}

func counterValue(t *testing.T, reg *prometheus.Registry, route, code string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() != "cms_http_requests_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			labels := map[string]string{}
			for _, lp := range m.GetLabel() {
				labels[lp.GetName()] = lp.GetValue()
			}
			if labels["route"] == route && labels["code"] == code {
				return m.GetCounter().GetValue()
			}
		}
	}
	t.Fatalf("no counter for route=%q code=%q", route, code)
	return 0
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:5555"
	r.Header.Add("X-Forwarded-For", "6.6.6.6, 203.0.113.7")
	r.Header.Add("X-Forwarded-For", "192.168.1.1")
	cases := map[int]string{0: "10.0.0.1", 1: "192.168.1.1", 2: "203.0.113.7", 5: "10.0.0.1"}
	for n, want := range cases {
		if got := clientIP(r, n); got != want {
			t.Errorf("num_proxies=%d: got %s want %s", n, got, want)
		}
	}
}

func TestIsHTTPS(t *testing.T) {
	plain := httptest.NewRequest("GET", "/", nil)
	if isHTTPS(plain, 0) || isHTTPS(plain, 1) {
		t.Error("plain request seen as HTTPS")
	}
	spoofed := httptest.NewRequest("GET", "/", nil)
	spoofed.Header.Set("X-Forwarded-Proto", "https")
	if isHTTPS(spoofed, 0) {
		t.Error("X-Forwarded-Proto trusted without proxies")
	}
	if !isHTTPS(spoofed, 1) || !isHTTPS(spoofed, 2) {
		t.Error("single X-Forwarded-Proto from a trusted proxy ignored")
	}
	chained := httptest.NewRequest("GET", "/", nil)
	chained.Header.Set("X-Forwarded-Proto", "https, http")
	if isHTTPS(chained, 1) || !isHTTPS(chained, 2) {
		t.Error("appended X-Forwarded-Proto: wrong hop")
	}
	tlsReq := httptest.NewRequest("GET", "https://example.org/", nil)
	if !isHTTPS(tlsReq, 0) || !IsHTTPS(tlsReq) {
		t.Error("TLS request not seen as HTTPS")
	}

	var got bool
	h := Observe(quiet, nil, 1)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = IsHTTPS(r)
	}))
	h.ServeHTTP(httptest.NewRecorder(), spoofed)
	if !got {
		t.Error("IsHTTPS after Observe with num_proxies=1")
	}
}

func TestSecureHeadersNonce(t *testing.T) {
	var seen string
	h := SecureHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = Nonce(r.Context())
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	csp := rec.Header().Get("Content-Security-Policy")
	if seen == "" || !strings.Contains(csp, "'nonce-"+seen+"'") {
		t.Fatalf("nonce %q not in CSP %q", seen, csp)
	}
	if !strings.Contains(csp, "frame-ancestors 'none'") || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("headers %v", rec.Header())
	}
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest("GET", "/", nil))
	if rec2.Header().Get("Content-Security-Policy") == csp {
		t.Error("nonce reused across requests")
	}
}

func TestLimitBody(t *testing.T) {
	h := LimitBody(10)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			http.Error(w, "too big", http.StatusRequestEntityTooLarge)
		}
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/", strings.NewReader("0123456789abc")))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("code %d", rec.Code)
	}
	req := httptest.NewRequest("POST", "/", strings.NewReader("0123456789abc"))
	req.ContentLength = -1 // chunked: only the reader limit applies
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("chunked code %d", rec.Code)
	}
}

func TestRecover(t *testing.T) {
	h := Recover(quiet)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("code %d", rec.Code)
	}
}

func TestMountCommon(t *testing.T) {
	mux := NewRouter()
	MountCommon(mux, obs.NewHealth(), "https://github.com/example/fork")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/source", nil))
	if rec.Code != http.StatusFound || !strings.HasPrefix(rec.Header().Get("Location"), "https://github.com/example/fork") {
		t.Errorf("source redirect: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"status":"ok"}` {
		t.Errorf("healthz: %d %s", rec.Code, rec.Body)
	}
}

func TestOpsHandler(t *testing.T) {
	reg := obs.NewRegistry("test")
	h := OpsHandler(quiet, reg, obs.NewHealth())
	for _, p := range []string{"/metrics", "/healthz", "/debug/pprof/"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
}

// TestServeShutdownWithOpenStream checks that an event stream does not
// block the graceful shutdown: it sees Draining and returns.
func TestServeShutdownWithOpenStream(t *testing.T) {
	ln, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	streamDone := make(chan struct{})
	mux := NewRouter()
	mux.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) {
		defer close(streamDone)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, ": ping\n\n")
		_ = http.NewResponseController(w).Flush()
		select {
		case <-Draining(r.Context()):
		case <-r.Context().Done():
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- Serve(ctx, quiet, "test", ln, Chain(mux, Recover(quiet), Observe(quiet, nil, 0))) }()

	resp, err := http.Get("http://" + ln.Addr().String() + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || line != ": ping\n" {
		t.Fatalf("first line %q, %v", line, err)
	}

	start := time.Now()
	cancel()
	select {
	case err := <-served:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown blocked by the open stream")
	}
	<-streamDone
	if time.Since(start) > 2*time.Second {
		t.Errorf("shutdown took %v", time.Since(start))
	}
}
