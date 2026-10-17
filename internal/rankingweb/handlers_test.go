// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package rankingweb

import (
	"bufio"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/app"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
)

const testSecret = "00112233445566778899aabbccddeeff"

type site struct {
	t     *testing.T
	cfg   *config.Config
	store *Store
	hub   *live.Hub
	h     http.Handler
	user  string
	pass  string
}

func newSite(t *testing.T, visibility string) *site {
	t.Helper()
	cfg := config.Default()
	cfg.SecretKey = testSecret
	cfg.RankingWeb.Visibility = visibility
	rt := app.New(Name, cfg, "", io.Discard)
	store, hub := openStore(t, t.TempDir())
	user, pass := cfg.RankingWebCredentials()
	return &site{t: t, cfg: cfg, store: store, hub: hub, user: user, pass: pass,
		h: rt.WebHandler(New(rt, store, hub).Routes(), 0, MaxBody)}
}

type req struct {
	method, path, body, ctype string
	auth                      bool
	user, pass                string
	cookies                   []*http.Cookie
}

func (s *site) do(r req) *http.Response {
	s.t.Helper()
	if r.method == "" {
		r.method = http.MethodGet
	}
	hr := httptest.NewRequest(r.method, r.path, strings.NewReader(r.body))
	if r.body != "" {
		if r.ctype == "" {
			r.ctype = "application/json"
		}
		hr.Header.Set("Content-Type", r.ctype)
	}
	if r.auth {
		hr.SetBasicAuth(s.user, s.pass)
	} else if r.user != "" {
		hr.SetBasicAuth(r.user, r.pass)
	}
	for _, c := range r.cookies {
		hr.AddCookie(c)
	}
	w := httptest.NewRecorder()
	s.h.ServeHTTP(w, hr)
	return w.Result()
}

func body(t *testing.T, res *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (s *site) expect(r req, status int, wantBody string) *http.Response {
	s.t.Helper()
	res := s.do(r)
	b := body(s.t, res)
	if res.StatusCode != status || (wantBody != "" && b != wantBody) {
		s.t.Fatalf("%s %s: %d %q, want %d %q", r.method, r.path, res.StatusCode, b, status, wantBody)
	}
	return res
}

// fill stores a frozen contest "ioi-2026" (key ioi_2d2026): u1 scored 40
// before the freeze and 90 after it.
func (s *site) fill() {
	s.t.Helper()
	st := s.store
	mustPut(s.t, st, kContest, EncodeID("ioi-2026"), `{"name":"IOI","begin":0,"end":1000,"score_precision":0,"freeze":500}`)
	mustPut(s.t, st, kTeam, "it", jsTeam)
	mustPut(s.t, st, kUser, "u1", jsUser)
	mustPut(s.t, st, kTask, "a", jsTask(EncodeID("ioi-2026"), ScoreModeMax))
	mustPut(s.t, st, kSubmission, "1", sub("u1", "a", 100))
	mustPut(s.t, st, kSubchange, "100_1s", scoreChange("1", 100, 40))
	mustPut(s.t, st, kSubmission, "2", sub("u1", "a", 600))
	mustPut(s.t, st, kSubchange, "600_2s", scoreChange("2", 600, 90))
}

func TestRESTWrites(t *testing.T) {
	s := newSite(t, config.RankingPublic)
	s.expect(req{path: "/contests/"}, 200, "{}")

	// Writes need the credentials (401 with the realm), checked in
	// constant time; invalid keys are refused first (403, as CMS).
	res := s.expect(req{method: "PUT", path: "/contests/c", body: jsContest}, 401, "")
	if got := res.Header.Get("WWW-Authenticate"); got != `Basic realm="Scoreboard"` {
		t.Errorf("WWW-Authenticate %q", got)
	}
	s.expect(req{method: "PUT", path: "/contests/c", body: jsContest, user: s.user, pass: s.pass[:len(s.pass)-1]}, 401, "")
	s.expect(req{method: "PUT", path: "/contests/c", body: jsContest, user: "admin", pass: s.pass}, 401, "")
	s.expect(req{method: "PUT", path: "/contests/", body: `{"c":` + jsContest + `}`}, 401, "")
	s.expect(req{method: "PUT", path: "/contests/a-b", body: jsContest, auth: true}, 403, "")
	s.expect(req{method: "PUT", path: "/contests/c", body: jsContest, ctype: "text/plain", auth: true}, 415, "")
	s.expect(req{method: "PUT", path: "/contests/c", body: `{"name":`, auth: true}, 400, "")
	s.expect(req{method: "PUT", path: "/contests/c", body: `[]`, auth: true}, 400, "")
	s.expect(req{method: "PUT", path: "/contests/c", body: strings.Replace(jsContest, "1000", "1000.0", 1), auth: true}, 400, "")
	s.expect(req{method: "PUT", path: "/tasks/a", body: jsTask("c", "max"), auth: true}, 400, "") // inconsistent

	s.expect(req{method: "PUT", path: "/contests/c", body: jsContest, ctype: "application/json; charset=utf-8", auth: true}, 204, "")
	res = s.expect(req{path: "/contests/c"}, 200, jsContest)
	if res.Header.Get("Content-Type") != "application/json" || res.Header.Get("Timestamp") == "" {
		t.Errorf("headers %v", res.Header)
	}
	s.expect(req{path: "/contests/"}, 200, `{"c":`+jsContest+`}`)
	s.expect(req{path: "/contests/zz"}, 404, "")
	s.expect(req{path: "/contests/a-b"}, 404, "")

	s.expect(req{method: "PUT", path: "/teams/", body: `{"it":` + jsTeam + `,"fr":{"name":"France"}}`, auth: true}, 204, "")
	s.expect(req{method: "PUT", path: "/teams/", body: `["it"]`, auth: true}, 400, "")
	s.expect(req{method: "PUT", path: "/teams/", body: `{"x-y":` + jsTeam + `}`, auth: true}, 400, "")
	s.expect(req{path: "/teams/"}, 200, `{"fr":{"name":"France"},"it":{"name":"Italy"}}`)

	s.expect(req{method: "DELETE", path: "/teams/it"}, 401, "")
	s.expect(req{method: "DELETE", path: "/teams/"}, 401, "")
	s.expect(req{method: "DELETE", path: "/teams/a-b", auth: true}, 404, "")
	s.expect(req{method: "DELETE", path: "/teams/it", auth: true}, 204, "")
	s.expect(req{method: "DELETE", path: "/teams/it", auth: true}, 404, "")
	s.expect(req{method: "DELETE", path: "/teams/", auth: true}, 204, "")
	s.expect(req{path: "/teams/"}, 200, "{}")

	// Other methods: 405, as werkzeug.
	for _, p := range []string{"/contests/", "/contests/c", "/scores", "/events", "/history"} {
		if res := s.do(req{method: "POST", path: p, body: "{}", auth: true}); res.StatusCode != 405 {
			t.Errorf("POST %s: %d, want 405", p, res.StatusCode)
		}
	}
	s.expect(req{path: "/nope/x"}, 404, "")
}

func TestScoresHistorySublist(t *testing.T) {
	s := newSite(t, config.RankingPublic)
	s.fill()
	// The public sees the scores before the freeze; administrators
	// (basic auth) all of them.
	res := s.expect(req{path: "/scores"}, 200, `{"u1":{"a":40.0}}`)
	if res.Header.Get("Timestamp") == "" || res.Header.Get("Cache-Control") != "no-cache" {
		t.Errorf("scores headers %v", res.Header)
	}
	s.expect(req{path: "/scores", auth: true}, 200, `{"u1":{"a":90.0}}`)
	s.expect(req{path: "/history"}, 200, `[["u1","a",100,40.0]]`)
	s.expect(req{path: "/history", auth: true}, 200, `[["u1","a",100,40.0],["u1","a",600,90.0]]`)
	s.expect(req{path: "/sublist/u1"}, 200,
		`[{"key":"1","user":"u1","task":"a","time":100,"score":40.0,"token":false,"extra":[]}]`)
	s.expect(req{path: "/sublist/u1", auth: true}, 200,
		`[{"key":"1","user":"u1","task":"a","time":100,"score":40.0,"token":false,"extra":[]},`+
			`{"key":"2","user":"u1","task":"a","time":600,"score":90.0,"token":false,"extra":[]}]`)
	s.expect(req{path: "/sublist/nobody"}, 200, `[]`)
	// Submissions and subchanges would reveal the frozen scores.
	s.expect(req{path: "/submissions/"}, 403, "")
	s.expect(req{path: "/subchanges/600_2s"}, 403, "")
	s.expect(req{path: "/subchanges/600_2s", auth: true}, 200, scoreChange("2", 600, 90))

	// The cached bodies follow the changes.
	mustPut(t, s.store, kSubchange, "100_1s", scoreChange("1", 100, 45))
	s.expect(req{path: "/scores"}, 200, `{"u1":{"a":45.0}}`)
	s.expect(req{path: "/history"}, 200, `[["u1","a",100,45.0]]`)
	s.expect(req{path: "/config"}, 200, `{"show_id_column":false}`)
}

// The Timestamp header is at least the id of the last event, so that the
// page ignores the events already in its data.
func TestTimestampFollowsEvents(t *testing.T) {
	s := newSite(t, config.RankingPublic)
	s.fill()
	res := s.expect(req{path: "/contests/"}, 200, "")
	ts := res.Header.Get("Timestamp")
	if !regexp.MustCompile(`^\d+\.\d{6}$`).MatchString(ts) {
		t.Fatalf("Timestamp %q", ts)
	}
	last, _ := live.ParseID(s.hub.LastID())
	sec, us, _ := strings.Cut(ts, ".")
	a, _ := strconv.ParseUint(sec, 10, 64)
	b, _ := strconv.ParseUint(us, 10, 64)
	if a*1e6+b < last {
		t.Fatalf("Timestamp %s before the last event %d", ts, last)
	}
}

// sse reads a stream until it has seen want (or times out).
func sse(t *testing.T, url string, want []string, during func()) string {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("events content type %q", ct)
	}
	lines := make(chan string, 100)
	go func() {
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	var got strings.Builder
	deadline := time.After(5 * time.Second)
	started := false
	for {
		text := got.String()
		ok := true
		for _, w := range want {
			ok = ok && strings.Contains(text, w)
		}
		if ok {
			return text
		}
		select {
		case l, open := <-lines:
			if !open {
				t.Fatalf("stream ended:\n%s", text)
			}
			got.WriteString(l + "\n")
			if !started && strings.HasPrefix(l, "event: ping") && during != nil {
				started = true
				during()
			}
		case <-deadline:
			t.Fatalf("stream lacks %q:\n%s", want, text)
		}
	}
}

func TestEvents(t *testing.T) {
	s := newSite(t, config.RankingPublic)
	s.fill()
	srv := httptest.NewServer(s.h)
	defer srv.Close()

	since := s.hub.LastID()
	// Public: entity events and the public scores; a score hidden by the
	// freeze does not reach it.
	text := sse(t, srv.URL+"/events?last_event_id="+since,
		[]string{"event: team\ndata: create fr\n", "event: score\ndata: u1 a 50.0\n"},
		func() {
			mustPut(t, s.store, kSubchange, "600_2s", scoreChange("2", 600, 95))
			mustPut(t, s.store, kTeam, "fr", `{"name":"France"}`)
			mustPut(t, s.store, kSubchange, "100_1s", scoreChange("1", 100, 50))
		})
	if strings.Contains(text, "95.0") {
		t.Errorf("a frozen score reached the public:\n%s", text)
	}
	if !regexp.MustCompile(`(?m)^id: [0-9a-f]+$`).MatchString(text) || !strings.Contains(text, "retry: ") {
		t.Errorf("stream format:\n%s", text)
	}

	// Administrators see every score; missed events are replayed.
	req, _ := http.NewRequest("GET", srv.URL+"/events?last_event_id="+since, nil)
	req.SetBasicAuth(s.user, s.pass)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(res.Body)
	var b strings.Builder
	for sc.Scan() && !strings.Contains(b.String(), "u1 a 95.0") {
		b.WriteString(sc.Text() + "\n")
	}
	res.Body.Close()
	if !strings.Contains(b.String(), "data: u1 a 95.0") {
		t.Errorf("admin stream:\n%s", b.String())
	}

	// An id the hub cannot serve: "reload" (cmsranking DataStore.js).
	sse(t, srv.URL+"/events?last_event_id=1", []string{"event: reload\n"}, nil)
}

func TestVisibility(t *testing.T) {
	t.Run("public", func(t *testing.T) {
		s := newSite(t, config.RankingPublic)
		for _, p := range []string{"/", "/scores", "/history", "/contests/", "/config", "/logo", "/sublist/u1"} {
			s.expect(req{path: p}, 200, "")
		}
	})

	t.Run("admins", func(t *testing.T) {
		s := newSite(t, config.RankingAdmins)
		s.fill()
		for _, p := range []string{"/", "/scores", "/history", "/contests/", "/users/u1", "/config", "/logo", "/faces/u1", "/sublist/u1", "/events"} {
			s.expect(req{path: p}, 403, "")
		}
		// The static files are public.
		s.expect(req{path: "/Ranking.css"}, 200, "")
		// Neither a secret link nor a bad admin token opens it.
		s.expect(req{path: "/?secret=" + s.cfg.RankingLinkToken("ioi-2026")}, 403, "")
		tok := s.cfg.RankingAdminToken(time.Now().Add(time.Minute))
		s.expect(req{path: "/?admin=" + tok[:len(tok)-1] + "0"}, 403, "")
		s.expect(req{path: "/?admin=" + s.cfg.RankingAdminToken(time.Now().Add(-time.Second))}, 403, "")
		// Basic auth works.
		s.expect(req{path: "/scores", auth: true}, 200, `{"u1":{"a":90.0}}`)
		// The admin link sets the cookie and redirects to the clean address.
		res := s.expect(req{path: "/?admin=" + tok}, 303, "")
		if loc := res.Header.Get("Location"); loc != "./" {
			t.Errorf("Location %q", loc)
		}
		if res.Header.Get("Referrer-Policy") != "no-referrer" {
			t.Errorf("Referrer-Policy %q", res.Header.Get("Referrer-Policy"))
		}
		cookies := res.Cookies()
		if len(cookies) != 1 || cookies[0].Name != cookieName || !cookies[0].HttpOnly {
			t.Fatalf("cookies %v", cookies)
		}
		s.expect(req{path: "/", cookies: cookies}, 200, "")
		s.expect(req{path: "/scores", cookies: cookies}, 200, `{"u1":{"a":90.0}}`)
		// A tampered cookie is worth nothing.
		bad := *cookies[0]
		if bad.Value[0] == 'x' {
			bad.Value = "y" + bad.Value[1:]
		} else {
			bad.Value = "x" + bad.Value[1:]
		}
		s.expect(req{path: "/scores", cookies: []*http.Cookie{&bad}}, 403, "")
		// Writes still need the credentials.
		s.expect(req{method: "PUT", path: "/teams/x", body: jsTeam, cookies: cookies}, 401, "")
	})

	t.Run("secret", func(t *testing.T) {
		s := newSite(t, config.RankingSecret)
		s.fill()
		s.expect(req{path: "/"}, 403, "")
		s.expect(req{path: "/scores"}, 403, "")
		s.expect(req{path: "/?secret=" + strings.Repeat("0", 32)}, 403, "")
		s.expect(req{path: "/?secret=" + s.cfg.RankingLinkToken("other")}, 403, "")
		other := *s.cfg
		other.SecretKey = "ffeeddccbbaa99887766554433221100"
		s.expect(req{path: "/?secret=" + other.RankingLinkToken("ioi-2026")}, 403, "")

		res := s.expect(req{path: "/?secret=" + s.cfg.RankingLinkToken("ioi-2026")}, 303, "")
		cookies := res.Cookies()
		if len(cookies) != 1 {
			t.Fatalf("cookies %v", cookies)
		}
		s.expect(req{path: "/", cookies: cookies}, 200, "")
		// A viewer of a secret link sees the public (frozen) scores.
		s.expect(req{path: "/scores", cookies: cookies}, 200, `{"u1":{"a":40.0}}`)
		s.expect(req{path: "/submissions/", cookies: cookies}, 403, "")
		// A link of a contest that is gone stops working.
		if err := s.store.Delete(kContest, EncodeID("ioi-2026")); err != nil {
			t.Fatal(err)
		}
		s.expect(req{path: "/scores", cookies: cookies}, 403, "")
	})
}

func TestStaticAndCSP(t *testing.T) {
	s := newSite(t, config.RankingPublic)
	res := s.expect(req{path: "/"}, 200, "")
	csp := res.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe-eval") ||
		strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
		t.Fatalf("CSP %q", csp)
	}
	if ct := res.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type %q", ct)
	}
	page := body(t, s.do(req{path: "/"}))
	// No inline script or handler: the CSP would block them.
	if m := regexp.MustCompile(`<script(?:\s[^>]*)?>`).FindAllString(page, -1); len(m) == 0 {
		t.Fatal("no scripts in Ranking.html")
	} else {
		for _, tag := range m {
			if !strings.Contains(tag, "src=") {
				t.Errorf("inline script %s", tag)
			}
		}
	}
	if m := regexp.MustCompile(`(?i)\son[a-z]+\s*=|javascript:`).FindString(page); m != "" {
		t.Errorf("inline handler %q in Ranking.html", m)
	}
	// Every file the page loads is served with its type.
	refs := regexp.MustCompile(`(?:src|href)=["']([^"'#:]+)["']`).FindAllStringSubmatch(page, -1)
	for _, m := range refs {
		p := "/" + m[1]
		if m[1] == "source" {
			continue // httpx.MountCommon (AGPL source link)
		}
		res := s.do(req{path: p})
		if res.StatusCode != 200 {
			t.Errorf("%s: %d", p, res.StatusCode)
			continue
		}
		ct, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
		want := map[string]string{".js": "text/javascript", ".css": "text/css", ".png": "image/png"}
		for ext, w := range want {
			if strings.HasSuffix(p, ext) && ct != w {
				t.Errorf("%s: Content-Type %q, want %q", p, ct, w)
			}
		}
		if res.Header.Get("Content-Security-Policy") == "" || res.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: missing security headers", p)
		}
	}
	// The scripts the page loads do not write inline handlers either.
	for _, f := range []string{"DataStore.js", "Ranking.js", "Scoreboard.js", "UserDetail.js", "TeamSearch.js", "Overview.js"} {
		js := body(t, s.do(req{path: "/" + f}))
		if m := regexp.MustCompile(`<[a-z]+[^>]*\son[a-z]+=`).FindString(js); m != "" {
			t.Errorf("%s writes an inline handler: %q", f, m)
		}
	}
	// Images fall back to the defaults of cmsranking.
	for _, p := range []string{"/logo", "/faces/u1", "/flags/it"} {
		res := s.do(req{path: p})
		if res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/png" {
			t.Errorf("%s: %d %q", p, res.StatusCode, res.Header.Get("Content-Type"))
		}
	}
	s.expect(req{path: "/faces/a-b"}, 404, "")
	s.expect(req{path: "/faces/..%2fstate"}, 404, "")
}
