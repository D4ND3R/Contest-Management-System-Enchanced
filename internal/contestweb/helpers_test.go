// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package contestweb

// Test helpers shared by the tests of the whole package (the core and the
// task, submission, communication and API pages):
//
//	ts := newTestSite(t, testOptions{})       // demo contest, served over HTTP
//	c := ts.Client()                          // a browser: cookies, no redirects
//	c.Login("alice", "demo")                  // POST /demo/login
//	r := c.Get("/demo")                       // r.Status, r.Header, r.Body
//	r = c.Post("/demo/question", url.Values{"question_subject": {"x"}}) // _xsrf added
//	ts.Exec(`UPDATE contests SET allow_registration = true`)
//	ts.Clock.Set(time.Date(...))              // the time of the site
//	ts.Counter.Reset(); ...; ts.Counter.Count() // with testOptions{Counting: true}
//	ts.Events.Events()                        // live events the site published
//
// The demo contest is testdata/dumps/demo, a dump of the contest of the
// reference CMS used for the side-by-side comparisons: tasks sum, half
// (with a comparator) and quiz (output only), users alice, bob and carol
// (password "demo", plaintext), two submissions of alice on sum (one
// scored), contest from 2025-01-01 to 2030-01-01 UTC, tokens finite.

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/crypto/bcrypt"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/db"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/httpx"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/importer/dump"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/kv"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/web"
)

func init() {
	// Cost 12 (BcryptCost) takes seconds per hash under the race
	// detector; TestPasswords checks the real cost once.
	bcryptCost = bcrypt.MinCost
}

// demoDump is the dump of the demo contest.
const demoDump = "../../testdata/dumps/demo"

// demoNow is the default time of the test sites: the demo contest is
// running (2025-01-01 to 2030-01-01).
var demoNow = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

// testClock is the settable clock of a test site.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

// Now returns the time of the site.
func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// Set moves the clock.
func (c *testClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

// Add moves the clock forward by d.
func (c *testClock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// testOptions configure newTestSite.
type testOptions struct {
	// Configure changes the configuration before the site is built
	// (contest_web.contest, num_proxies, admin_token...).
	Configure func(*config.Config)
	// Counting counts the queries of the pool (testSite.Counter).
	Counting bool
	// Hub runs a live hub on the test Valkey (the events stream works);
	// without it <contest>/events answers 503.
	Hub bool
	// NoImport leaves the database empty (no demo contest).
	NoImport bool
}

// testSite is a contestant site on its own database and Valkey keyspace,
// served by an httptest.Server through the middleware of the real
// service (CSP nonce, client address, body limit).
type testSite struct {
	T       testing.TB
	S       *Server
	Pool    *pgxpool.Pool
	Counter *db.QueryCounter // nil unless testOptions.Counting
	KV      *kv.Client
	Blobs   blob.Store
	Hub     *live.Hub // nil unless testOptions.Hub
	Events  *live.Recorder
	Clock   *testClock
	Config  *config.Config
	// URL is the root of the server ("http://127.0.0.1:port").
	URL string
	// Contest is the id of the demo contest.
	Contest int32
}

// newTestSite builds a test site with the demo contest.
func newTestSite(t testing.TB, o testOptions) *testSite {
	t.Helper()
	ctx := context.Background()
	ts := &testSite{T: t, Events: &live.Recorder{}, Clock: &testClock{t: demoNow}}
	if o.Counting {
		ts.Pool, ts.Counter = testutil.CountingDB(t)
	} else {
		ts.Pool = testutil.DB(t)
	}
	ts.KV = testutil.Redis(t)
	bs, err := blob.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ts.Blobs = bs
	if !o.NoImport {
		w := importer.NewWriter(ts.Pool, bs, nil)
		if _, err := dump.Import(ctx, w, demoDump, dump.Options{}); err != nil {
			t.Fatalf("importing the demo dump: %v", err)
		}
		if err := ts.Pool.QueryRow(ctx, `SELECT id FROM contests WHERE name = 'demo'`).Scan(&ts.Contest); err != nil {
			t.Fatal(err)
		}
	}

	cfg := config.Default()
	cfg.SecretKey = strings.Repeat("5e", 32)
	cfg.ContestWeb.DocsPath = ""
	if o.Configure != nil {
		o.Configure(cfg)
	}
	ts.Config = cfg
	log := slog.New(slog.DiscardHandler)
	if o.Hub {
		ts.Hub = live.NewHub(ts.KV, log, prometheus.NewRegistry(), live.HubOptions{})
		hctx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = ts.Hub.Run(hctx)
		}()
		t.Cleanup(func() {
			cancel()
			<-done
		})
		deadline := time.Now().Add(5 * time.Second)
		for !ts.Hub.Connected() {
			if time.Now().After(deadline) {
				t.Fatal("the live hub did not connect to Valkey")
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	ts.S, err = New(ctx, Options{
		Config:    cfg,
		Log:       log,
		Pool:      ts.Pool,
		KV:        ts.KV,
		Blobs:     bs,
		Hub:       ts.Hub,
		Publisher: ts.Events,
		Clock:     ts.Clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	h := httpx.Chain(ts.S.Handler(),
		httpx.Recover(log),
		httpx.Observe(log, prometheus.NewRegistry(), cfg.ContestWeb.NumProxies),
		httpx.SecureHeaders,
		httpx.LimitBody(MaxBody),
	)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	ts.URL = srv.URL
	return ts
}

// Exec runs a statement on the test database (to change the contest,
// a participation...).
func (ts *testSite) Exec(sql string, args ...any) {
	ts.T.Helper()
	if _, err := ts.Pool.Exec(context.Background(), sql, args...); err != nil {
		ts.T.Fatalf("%s: %v", sql, err)
	}
}

// Participation returns the id of a user's participation in the demo
// contest.
func (ts *testSite) Participation(username string) int32 {
	ts.T.Helper()
	var id int32
	err := ts.Pool.QueryRow(context.Background(), `SELECT p.id FROM participations p
		JOIN users u ON u.id = p.user_id WHERE u.username = $1 AND p.contest_id = $2`,
		username, ts.Contest).Scan(&id)
	if err != nil {
		ts.T.Fatalf("participation of %s: %v", username, err)
	}
	return id
}

// testClient is a browser of a test site: it keeps cookies and does not
// follow redirects.
type testClient struct {
	T    testing.TB
	Site *testSite
	HTTP *http.Client
	// Header is added to every request (X-Forwarded-For,
	// Accept-Language...).
	Header http.Header
}

// testResponse is an answer to a testClient.
type testResponse struct {
	Status int
	Header http.Header
	Body   string
}

// Location returns the Location header.
func (r *testResponse) Location() string { return r.Header.Get("Location") }

// Client returns a new browser without cookies.
func (ts *testSite) Client() *testClient {
	jar, _ := cookiejar.New(nil)
	return &testClient{
		T:    ts.T,
		Site: ts,
		HTTP: &http.Client{
			Jar: jar,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
			Timeout: 30 * time.Second,
		},
		Header: http.Header{},
	}
}

// Do sends a request; path is relative to the root of the site.
func (c *testClient) Do(req *http.Request) *testResponse {
	c.T.Helper()
	for k, v := range c.Header {
		req.Header[k] = v
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		c.T.Fatalf("%s %s: %v", req.Method, req.URL, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.T.Fatalf("%s %s: reading the body: %v", req.Method, req.URL, err)
	}
	return &testResponse{Status: resp.StatusCode, Header: resp.Header, Body: string(body)}
}

// Get sends GET path; headers are name, value pairs.
func (c *testClient) Get(path string, headers ...string) *testResponse {
	c.T.Helper()
	req, err := http.NewRequest(http.MethodGet, c.Site.URL+path, nil)
	if err != nil {
		c.T.Fatal(err)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	return c.Do(req)
}

// XSRF returns the _xsrf cookie of the client, getting one from the
// contest page if it has none.
func (c *testClient) XSRF() string {
	c.T.Helper()
	u, _ := url.Parse(c.Site.URL + "/")
	for _, ck := range c.HTTP.Jar.Cookies(u) {
		if ck.Name == web.XSRFCookie {
			return ck.Value
		}
	}
	c.Get("/demo")
	for _, ck := range c.HTTP.Jar.Cookies(u) {
		if ck.Name == web.XSRFCookie {
			return ck.Value
		}
	}
	c.T.Fatal("no _xsrf cookie")
	return ""
}

// Post sends a form to path with the _xsrf token.
func (c *testClient) Post(path string, form url.Values) *testResponse {
	c.T.Helper()
	if form == nil {
		form = url.Values{}
	}
	if !form.Has("_xsrf") {
		form.Set("_xsrf", c.XSRF())
	}
	return c.PostRaw(path, form)
}

// PostRaw sends a form as it is (no _xsrf added).
func (c *testClient) PostRaw(path string, form url.Values) *testResponse {
	c.T.Helper()
	req, err := http.NewRequest(http.MethodPost, c.Site.URL+path, strings.NewReader(form.Encode()))
	if err != nil {
		c.T.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.Do(req)
}

// Login logs in to the demo contest and fails the test if it does not
// work.
func (c *testClient) Login(username, password string) {
	c.T.Helper()
	r := c.Post("/demo/login", url.Values{"username": {username}, "password": {password}})
	if r.Status != http.StatusFound || strings.Contains(r.Location(), "login_error") {
		c.T.Fatalf("login as %s: %d %s", username, r.Status, r.Location())
	}
}

// Cookie returns the value of a cookie of the client ("" if none).
func (c *testClient) Cookie(name string) string {
	u, _ := url.Parse(c.Site.URL + "/")
	for _, ck := range c.HTTP.Jar.Cookies(u) {
		if ck.Name == name {
			return ck.Value
		}
	}
	return ""
}
