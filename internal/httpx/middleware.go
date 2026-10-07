// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package httpx

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Middleware wraps a handler.
type Middleware func(http.Handler) http.Handler

// Chain applies mws so that the first one is the outermost.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// reqInfo is shared, by pointer, between the outer middleware and the
// router, so the matched route survives r.WithContext copies.
type reqInfo struct {
	route    string
	clientIP string
	nonce    string
}

type infoKey struct{}

func info(ctx context.Context) *reqInfo {
	if ri, ok := ctx.Value(infoKey{}).(*reqInfo); ok {
		return ri
	}
	return nil
}

func withInfo(r *http.Request) (*http.Request, *reqInfo) {
	if ri := info(r.Context()); ri != nil {
		return r, ri
	}
	ri := &reqInfo{}
	return r.WithContext(context.WithValue(r.Context(), infoKey{}, ri)), ri
}

// Router is an http.ServeMux that records the matched pattern for the
// metrics and the access log (low-cardinality route labels).
type Router struct {
	*http.ServeMux
}

// NewRouter returns an empty router.
func NewRouter() *Router { return &Router{http.NewServeMux()} }

func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ri := info(r.Context())
	rt.ServeMux.ServeHTTP(w, r)
	// ServeMux stores the matched pattern in r.Pattern while dispatching.
	if ri != nil && ri.route == "" {
		ri.route = r.Pattern
	}
}

// statusWriter captures the status code and size of a response. Unwrap lets
// http.ResponseController reach Flush on the real writer (event streams).
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

func (w *statusWriter) Flush() {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) code() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

// Recover turns a panic into a logged 500 instead of a dropped connection.
func Recover(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				v := recover()
				if v == nil {
					return
				}
				if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(v)
				}
				log.Error("panic serving request", "method", r.Method, "path", r.URL.Path,
					"panic", fmt.Sprint(v), "stack", string(debug.Stack()))
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// Observe logs every request and records the HTTP metrics. It must be the
// outermost middleware after Recover; quiet paths (health, metrics,
// static files) are logged at debug level.
func Observe(log *slog.Logger, reg prometheus.Registerer, numProxies int) Middleware {
	requests := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "cms_http_requests_total",
		Help: "HTTP requests by route, method and status code.",
	}, []string{"route", "method", "code"})
	duration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cms_http_request_duration_seconds",
		Help:    "Time to serve HTTP requests (event streams excluded).",
		Buckets: []float64{.001, .0025, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
	}, []string{"route", "method"})
	inflight := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "cms_http_requests_in_flight",
		Help: "HTTP requests being served, event streams included.",
	})
	if reg != nil {
		reg.MustRegister(requests, duration, inflight)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			r, ri := withInfo(r)
			ri.clientIP = clientIP(r, numProxies)
			sw := &statusWriter{ResponseWriter: w}
			inflight.Inc()
			defer inflight.Dec()

			next.ServeHTTP(sw, r)

			elapsed := time.Since(start)
			route := ri.route
			if route == "" {
				route = "unmatched"
			}
			code := sw.code()
			requests.WithLabelValues(route, r.Method, strconv.Itoa(code)).Inc()
			if !strings.HasPrefix(sw.Header().Get("Content-Type"), "text/event-stream") {
				duration.WithLabelValues(route, r.Method).Observe(elapsed.Seconds())
			}
			level := slog.LevelInfo
			if quietPath(r.URL.Path) && code < 400 {
				level = slog.LevelDebug
			} else if code >= 500 {
				level = slog.LevelError
			}
			log.Log(r.Context(), level, "http request",
				"method", r.Method, "path", r.URL.Path, "route", route,
				"status", code, "bytes", sw.bytes,
				"dur_ms", float64(elapsed.Microseconds())/1000, "ip", ri.clientIP)
		})
	}
}

func quietPath(p string) bool {
	return p == "/healthz" || p == "/metrics" || strings.HasPrefix(p, "/static/") ||
		strings.Contains(p, "/static/") || p == "/favicon.ico"
}

// ClientIP returns the address of the client as seen through num_proxies
// reverse proxies (set by Observe).
func ClientIP(r *http.Request) string {
	if ri := info(r.Context()); ri != nil && ri.clientIP != "" {
		return ri.clientIP
	}
	return clientIP(r, 0)
}

func clientIP(r *http.Request, numProxies int) string {
	if numProxies > 0 {
		var hops []string
		for _, h := range r.Header.Values("X-Forwarded-For") {
			for _, p := range strings.Split(h, ",") {
				if p = strings.TrimSpace(p); p != "" {
					hops = append(hops, p)
				}
			}
		}
		// Each trusted proxy appends the address it saw; the client is
		// numProxies entries from the right. Anything further left is
		// controlled by the client and ignored.
		if len(hops) >= numProxies {
			if ip := net.ParseIP(hops[len(hops)-numProxies]); ip != nil {
				return ip.String()
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// CSP is the Content-Security-Policy of the web sites. Scripts need the
// per-request nonce (inline scripts of the ported CMS templates carry it);
// inline style attributes of the original markup are allowed.
const CSP = "default-src 'self'; script-src 'self' 'nonce-%s'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; font-src 'self'; connect-src 'self'; object-src 'none'; " +
	"base-uri 'self'; form-action 'self'; frame-ancestors 'none'"

// SecureHeaders sets the security headers and the CSP nonce of a page.
func SecureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, ri := withInfo(r)
		var b [16]byte
		_, _ = rand.Read(b[:])
		ri.nonce = base64.RawStdEncoding.EncodeToString(b[:])
		h := w.Header()
		h.Set("Content-Security-Policy", fmt.Sprintf(CSP, ri.nonce))
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// Nonce returns the CSP nonce of the current request, for <script nonce>.
func Nonce(ctx context.Context) string {
	if ri := info(ctx); ri != nil {
		return ri.nonce
	}
	return ""
}

// LimitBody caps request bodies. Handlers that accept larger uploads wrap
// their own http.MaxBytesReader with a higher limit before this one is hit.
func LimitBody(n int64) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > n {
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, n)
			next.ServeHTTP(w, r)
		})
	}
}
