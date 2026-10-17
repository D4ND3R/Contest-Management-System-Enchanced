// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package adminweb

// Test helpers shared by the tests of the whole package (the core and the
// pages of the three works):
//
//	ts := newTestSite(t, testOptions{})       // demo contest + admins, over HTTP
//	c := ts.Client()                          // a browser: cookies, no redirects
//	c.Login("admin")                          // POST /login (password = username)
//	r := c.Get("/contest/" + ts.ContestPath)  // r.Status, r.Header, r.Body
//	r = c.Post("/admins/add", url.Values{...}) // _xsrf added
//	r = c.Delete("/admin/3")                  // X-XSRFToken added
//	ts.Exec(`UPDATE contests SET ...`)
//	ts.Clock.Set(time.Date(...))
//	ts.Counter.Reset(); ...; ts.Counter.Count() // with testOptions{Counting: true}
//	ts.Events.Events()                        // live events the site published
//	ts.AuditCount(); ts.LastAudit()           // audit_log rows
//
// The admins (password = username, plaintext except "admin", bcrypt):
//
//	admin   permission_all
//	msg     permission_messaging
//	prep    permission_tasks (problem preparer)
//	ro      no permission (read-only)
//	off     permission_all, disabled
//
// The demo contest is testdata/dumps/demo (see internal/contestweb's
// helpers): tasks sum, half and quiz, users alice, bob and carol.

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
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
	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/web"
)

func init() {
	// Cost 12 takes seconds per hash under the race detector.
	web.PasswordCost = bcrypt.MinCost
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
	// Configure changes the configuration before the site is built.
	Configure func(*config.Config)
	// Counting counts the queries of the pool (testSite.Counter).
	Counting bool
	// Hub runs a live hub on the test Valkey (the events stream works);
	// without it /events answers 503.
	Hub bool
	// NoImport leaves the database without the demo contest.
	NoImport bool
}

// testSite is an admin site on its own database and Valkey keyspace,
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
	// Contest is the id of the demo contest (0 with NoImport).
	Contest int32
	// Admins are the ids of the test admins by username.
	Admins map[string]int32
}

// newTestSite builds a test site.
func newTestSite(t testing.TB, o testOptions) *testSite {
	t.Helper()
	ctx := context.Background()
	ts := &testSite{T: t, Events: &live.Recorder{}, Clock: &testClock{t: demoNow}, Admins: map[string]int32{}}
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
	qq := q.New(ts.Pool)
	for _, a := range []struct {
		name                 string
		all, msg, tasks, off bool
	}{
		{"admin", true, false, false, false},
		{"msg", false, true, false, false},
		{"prep", false, false, true, false},
		{"ro", false, false, false, false},
		{"off", true, false, false, true},
	} {
		auth := "plaintext:" + a.name
		if a.name == "admin" {
			if auth, err = web.HashPassword(a.name, "bcrypt"); err != nil {
				t.Fatal(err)
			}
		}
		row, err := qq.CreateAdmin(ctx, q.CreateAdminParams{Name: "Admin " + a.name, Username: a.name,
			Authentication: auth, Enabled: !a.off, PermissionAll: a.all, PermissionMessaging: a.msg,
			PermissionTasks: a.tasks})
		if err != nil {
			t.Fatal(err)
		}
		ts.Admins[a.name] = row.ID
	}

	cfg := config.Default()
	cfg.SecretKey = strings.Repeat("5e", 32)
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
		httpx.Observe(log, prometheus.NewRegistry(), cfg.AdminWeb.NumProxies),
		httpx.SecureHeaders,
		httpx.LimitBody(MaxBody),
	)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	ts.URL = srv.URL
	return ts
}

// Exec runs a statement on the test database.
func (ts *testSite) Exec(sql string, args ...any) {
	ts.T.Helper()
	if _, err := ts.Pool.Exec(context.Background(), sql, args...); err != nil {
		ts.T.Fatalf("%s: %v", sql, err)
	}
}

// QueryInt returns the single integer of a query.
func (ts *testSite) QueryInt(sql string, args ...any) int64 {
	ts.T.Helper()
	var n int64
	if err := ts.Pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		ts.T.Fatalf("%s: %v", sql, err)
	}
	return n
}

// AuditCount returns the number of audit_log rows.
func (ts *testSite) AuditCount() int64 {
	ts.T.Helper()
	return ts.QueryInt(`SELECT count(*) FROM audit_log`)
}

// auditEntry is a row of audit_log with its details decoded.
type auditEntry struct {
	Action, TargetType string
	TargetID           *int64
	ContestID          *int32
	AdminID            *int32
	Details            map[string]any
}

// LastAudit returns the newest audit_log row.
func (ts *testSite) LastAudit() auditEntry {
	ts.T.Helper()
	var e auditEntry
	var raw []byte
	err := ts.Pool.QueryRow(context.Background(), `SELECT action, target_type, target_id, contest_id, admin_id, details
		FROM audit_log ORDER BY id DESC LIMIT 1`).Scan(&e.Action, &e.TargetType, &e.TargetID, &e.ContestID, &e.AdminID, &raw)
	if err != nil {
		ts.T.Fatal(err)
	}
	if err := json.Unmarshal(raw, &e.Details); err != nil {
		ts.T.Fatal(err)
	}
	return e
}

// testClient is a browser of a test site: it keeps cookies and does not
// follow redirects.
type testClient struct {
	T    testing.TB
	Site *testSite
	HTTP *http.Client
	// Header is added to every request (Accept-Language...).
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

// Do sends a request.
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

// XSRF returns the _xsrf cookie of the client, getting one from the login
// page if it has none.
func (c *testClient) XSRF() string {
	c.T.Helper()
	if v := c.Cookie(web.XSRFCookie); v != "" {
		return v
	}
	c.Get("/login")
	if v := c.Cookie(web.XSRFCookie); v != "" {
		return v
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

// Delete sends DELETE path with the X-XSRFToken header (ajax_delete).
func (c *testClient) Delete(path string) *testResponse {
	c.T.Helper()
	req, err := http.NewRequest(http.MethodDelete, c.Site.URL+path, nil)
	if err != nil {
		c.T.Fatal(err)
	}
	req.Header.Set("X-XSRFToken", c.XSRF())
	return c.Do(req)
}

// RPC calls the RPC endpoint as web_rpc.js does.
func (c *testClient) RPC(service, method string, args any) *testResponse {
	c.T.Helper()
	b, _ := json.Marshal(args)
	req, err := http.NewRequest(http.MethodPost, c.Site.URL+"/rpc/"+service+"/0/"+method, strings.NewReader(string(b)))
	if err != nil {
		c.T.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-XSRFToken", c.XSRF())
	return c.Do(req)
}

// Login logs in as username (password = username) and fails the test if
// it does not work.
func (c *testClient) Login(username string) {
	c.T.Helper()
	r := c.Post("/login", url.Values{"username": {username}, "password": {username}})
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

// itoa formats an id for paths.
func itoa[T ~int32 | ~int64 | ~int](n T) string { return strconv.FormatInt(int64(n), 10) }

// regexp1 returns the first group of the first match of pattern in s ("" if none).
func regexp1(s, pattern string) string {
	m := regexp.MustCompile(pattern).FindStringSubmatch(s)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}
