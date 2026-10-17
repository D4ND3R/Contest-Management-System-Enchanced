// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package dispatcher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/app"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/rankingweb"
)

const rankingTestKey = "00112233445566778899aabbccddeeff"

type rankingRequest struct {
	method, path, user, pass, ctype, body string
	status                                int
}

// rankingServer records the requests of the sink and hands them to a
// real ranking-web (nil: answers 204), or fails them.
type rankingServer struct {
	t       *testing.T
	srv     *httptest.Server
	ranking http.Handler

	mu   sync.Mutex
	reqs []rankingRequest

	fail    atomic.Int32  // requests to fail with 503
	gate    chan struct{} // when set, requests wait for it to close
	entered chan struct{}
}

func newRankingServer(t *testing.T, ranking http.Handler) *rankingServer {
	rs := &rankingServer{t: t, ranking: ranking}
	rs.srv = httptest.NewServer(http.HandlerFunc(rs.serve))
	t.Cleanup(rs.srv.Close)
	return rs
}

func (rs *rankingServer) serve(w http.ResponseWriter, r *http.Request) {
	rs.mu.Lock()
	gate, entered := rs.gate, rs.entered
	rs.mu.Unlock()
	if gate != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-gate
	}
	b, _ := io.ReadAll(r.Body)
	u, p, _ := r.BasicAuth()
	rec := rankingRequest{method: r.Method, path: r.URL.Path, user: u, pass: p, ctype: r.Header.Get("Content-Type"), body: string(b)}
	if rs.fail.Load() > 0 {
		rs.fail.Add(-1)
		rec.status = http.StatusServiceUnavailable
		w.WriteHeader(rec.status)
	} else if rs.ranking == nil {
		rec.status = http.StatusNoContent
		w.WriteHeader(rec.status)
	} else {
		r.Body = io.NopCloser(bytes.NewReader(b))
		rw := httptest.NewRecorder()
		rs.ranking.ServeHTTP(rw, r)
		rec.status = rw.Code
		w.WriteHeader(rw.Code)
	}
	rs.mu.Lock()
	rs.reqs = append(rs.reqs, rec)
	rs.mu.Unlock()
}

func (rs *rankingServer) requests() []rankingRequest {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return append([]rankingRequest(nil), rs.reqs...)
}

func (rs *rankingServer) count() int { return len(rs.requests()) }

// newRankingWeb returns a real ranking-web sharing the secret key.
func newRankingWeb(t *testing.T) http.Handler {
	cfg := config.Default()
	cfg.SecretKey = rankingTestKey
	rt := app.New(rankingweb.Name, cfg, "", io.Discard)
	hub := live.NewLocalHub(nil, prometheus.NewRegistry(), live.HubOptions{})
	store, err := rankingweb.Open(t.TempDir(), hub, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return rt.WebHandler(rankingweb.New(rt, store, hub).Routes(), 0, rankingweb.MaxBody)
}

func newEnvLog() *slog.Logger { return slog.New(slog.DiscardHandler) }

func sameJSON(t *testing.T, what, got, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("%s: %v in %s", what, err, got)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("%s: bad expectation: %v", what, err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("%s:\n got %s\nwant %s", what, got, want)
	}
}

func rankingConfig(url string) *config.Config {
	cfg := config.Default()
	cfg.SecretKey = rankingTestKey
	cfg.Dispatcher.RankingRefresh = config.Duration(time.Hour)
	cfg.Dispatcher.Rankings = []config.RankingTarget{{URL: url, Freeze: config.Duration(time.Hour)}}
	return cfg
}

func TestRankingSink(t *testing.T) {
	e := newEnv(t)
	task, ds := e.task(2)
	other := e.dataset(task, 1, false)
	team := e.id(`INSERT INTO teams (code, name) VALUES ('it-1', 'Italy') RETURNING id`)
	alice, bob, hidden := e.participant("alice"), e.participant("bob"), e.participant("hidden")
	e.exec(`UPDATE participations SET team_id = $1 WHERE id = $2`, team, alice)
	e.exec(`UPDATE participations SET hidden = true WHERE id = $1`, hidden)
	at := func(min int) time.Time { return t0.Add(time.Duration(min) * time.Minute) }
	submit := func(p int32, min int) int32 {
		s := e.submit(p, task, 1)[0]
		e.exec(`UPDATE submissions SET timestamp = $1 WHERE id = $2`, at(min), s)
		return s
	}
	score := func(s, dataset int32, v float64) {
		e.exec(`INSERT INTO submission_results (submission_id, dataset_id, compilation_outcome, evaluation_outcome,
				score, score_details, public_score, public_score_details, ranking_score_details, scored_at)
			VALUES ($1, $2, 'ok', 'ok', $3, '[]', 0, '[]', '{}', now())`, s, dataset, v)
	}
	s1 := submit(alice, 10)
	score(s1, ds, 50)
	s2 := submit(alice, 20) // unofficial
	score(s2, ds, 100)
	e.exec(`UPDATE submissions SET official = false WHERE id = $1`, s2)
	s3 := submit(hidden, 30) // hidden participation
	score(s3, ds, 100)
	s4 := submit(bob, 40) // scored and tokened
	score(s4, ds, 100)
	e.exec(`INSERT INTO tokens (submission_id, timestamp) VALUES ($1, $2)`, s4, at(45))
	submit(bob, 50)         // not scored yet
	s6 := submit(alice, 15) // scored on another dataset only
	score(s6, other, 100)

	rs := newRankingServer(t, newRankingWeb(t))
	cfg := rankingConfig(rs.srv.URL + "/")
	sink, err := NewRankingSink(cfg, e.pool, e.log, prometheus.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	var leading atomic.Bool
	var notLeading atomic.Int64
	sink.SetLeading(func() bool {
		v := leading.Load()
		if !v {
			notLeading.Add(1)
		}
		return v
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = sink.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	wake := func() { sink.Push(ctx, ScoreUpdate{}) }

	// A dispatcher that is not the active one sends nothing.
	e.eventually(5*time.Second, "the sink to check leadership", func() bool { return notLeading.Load() > 0 })
	time.Sleep(50 * time.Millisecond)
	if n := rs.count(); n != 0 {
		t.Fatalf("%d requests while not leading", n)
	}

	// Becoming active: everything is sent, dependencies first.
	leading.Store(true)
	wake()
	e.eventually(10*time.Second, "the full send", func() bool { return rs.count() >= 6 })
	reqs := rs.requests()
	user, pass := cfg.Dispatcher.Rankings[0].Credentials(cfg)
	paths := []string{"/contests/", "/teams/", "/users/", "/tasks/", "/submissions/", "/subchanges/"}
	for i, r := range reqs[:6] {
		if r.method != "PUT" || r.path != paths[i] || r.user != user || r.pass != pass || r.ctype != "application/json" || r.status != 204 {
			t.Fatalf("request %d: %s %s as %s (%s) -> %d, want PUT %s -> 204", i, r.method, r.path, r.user, r.ctype, r.status, paths[i])
		}
	}
	begin := t0.Unix()
	sameJSON(t, "contests", reqs[0].body, fmt.Sprintf(
		`{"ioi":{"name":"IOI","begin":%d,"end":%d,"score_precision":0,"freeze":%d}}`, begin, begin+5*3600, begin+4*3600))
	sameJSON(t, "teams", reqs[1].body, `{"it_2d1":{"name":"Italy"}}`)
	sameJSON(t, "users", reqs[2].body, `{"alice":{"f_name":"alice","l_name":"alice","team":"it_2d1"},`+
		`"bob":{"f_name":"bob","l_name":"bob","team":null}}`)
	sameJSON(t, "tasks", reqs[3].body, `{"task1":{"name":"task1","short_name":"task1","contest":"ioi","max_score":100.0,`+
		`"score_precision":0,"extra_headers":[],"order":1,"score_mode":"max"}}`)
	if !strings.Contains(reqs[3].body, `"max_score":100.0`) {
		t.Errorf("max_score is not sent as a float: %s", reqs[3].body)
	}
	sameJSON(t, "submissions", reqs[4].body, fmt.Sprintf(`{"%d":{"user":"alice","task":"task1","time":%d},`+
		`"%d":{"user":"bob","task":"task1","time":%d}}`, s1, at(10).Unix(), s4, at(40).Unix()))
	sameJSON(t, "subchanges", reqs[5].body, fmt.Sprintf(`{
		"%[1]d%[2]ds":{"submission":"%[2]d","time":%[1]d,"score":50.0,"extra":[]},
		"%[3]d%[4]ds":{"submission":"%[4]d","time":%[3]d,"score":100.0,"extra":[]},
		"%[5]d%[4]dt":{"submission":"%[4]d","time":%[5]d,"token":true}}`,
		at(10).Unix(), s1, at(40).Unix(), s4, at(45).Unix()))

	// A scored submission: only it is sent, and Push does not wait for
	// the ranking.
	s7 := submit(alice, 60)
	score(s7, ds, 80)
	rs.mu.Lock()
	rs.gate, rs.entered = make(chan struct{}), make(chan struct{}, 1)
	gate := rs.gate
	rs.mu.Unlock()
	n := rs.count()
	sink.Push(ctx, ScoreUpdate{Submissions: []judging.SubmissionChange{{SubmissionID: s7, Scored: true}}})
	select {
	case <-rs.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the sink did not send the submission")
	}
	start := time.Now()
	for i := range 1000 {
		sink.Push(ctx, ScoreUpdate{Submissions: []judging.SubmissionChange{{SubmissionID: int32(1_000_000 + i), Scored: true}}})
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("Push blocked for %v while a request was in flight", d)
	}
	rs.mu.Lock()
	rs.gate = nil
	rs.mu.Unlock()
	close(gate)
	e.eventually(10*time.Second, "the incremental send", func() bool { return rs.count() >= n+2 })
	time.Sleep(100 * time.Millisecond)
	reqs = rs.requests()[n:]
	if len(reqs) != 2 || reqs[0].path != "/submissions/" || reqs[1].path != "/subchanges/" {
		t.Fatalf("incremental requests: %+v", reqs)
	}
	sameJSON(t, "new submission", reqs[0].body, fmt.Sprintf(`{"%d":{"user":"alice","task":"task1","time":%d}}`, s7, at(60).Unix()))
	sameJSON(t, "new subchange", reqs[1].body, fmt.Sprintf(`{"%[1]d%[2]ds":{"submission":"%[2]d","time":%[1]d,"score":80.0,"extra":[]}}`,
		at(60).Unix(), s7))
	// An unscored change (an invalidation) sends nothing.
	n = rs.count()
	sink.Push(ctx, ScoreUpdate{Submissions: []judging.SubmissionChange{{SubmissionID: s1, Scored: false}}})
	time.Sleep(100 * time.Millisecond)
	if rs.count() != n {
		t.Errorf("an unscored change was sent: %+v", rs.requests()[n:])
	}

	// A task changed (and a team was renamed meanwhile): what changed in
	// the metadata and every submission of the task.
	e.exec(`UPDATE teams SET name = 'Italia' WHERE id = $1`, team)
	n = rs.count()
	sink.Push(ctx, ScoreUpdate{Tasks: []int32{task}})
	e.eventually(10*time.Second, "the task resend", func() bool { return rs.count() >= n+3 })
	time.Sleep(100 * time.Millisecond)
	reqs = rs.requests()[n:]
	if len(reqs) != 3 || reqs[0].path != "/teams/" || reqs[1].path != "/submissions/" || reqs[2].path != "/subchanges/" {
		t.Fatalf("task resend: %+v", reqs)
	}
	sameJSON(t, "renamed team", reqs[0].body, `{"it_2d1":{"name":"Italia"}}`)
	if !strings.Contains(reqs[2].body, fmt.Sprintf(`"%d%dt"`, at(45).Unix(), s4)) {
		t.Errorf("the token is not sent again: %s", reqs[2].body)
	}

	// After an error everything is sent again.
	s8 := submit(bob, 70)
	score(s8, ds, 60)
	n = rs.count()
	rs.fail.Store(1)
	sink.Push(ctx, ScoreUpdate{Submissions: []judging.SubmissionChange{{SubmissionID: s8, Scored: true}}})
	e.eventually(10*time.Second, "the resend after an error", func() bool { return rs.count() >= n+7 })
	reqs = rs.requests()[n:]
	if reqs[0].status != 503 {
		t.Fatalf("first request %+v", reqs[0])
	}
	for i, r := range reqs[1:7] {
		if r.path != paths[i] || r.status != 204 {
			t.Fatalf("resend request %d: %s -> %d, want %s", i, r.path, r.status, paths[i])
		}
	}
	for _, id := range []int32{s1, s4, s7, s8} {
		if !strings.Contains(reqs[5].body, fmt.Sprintf(`"%d"`, id)) {
			t.Errorf("submission %d missing from the resend: %s", id, reqs[5].body)
		}
	}
	for _, id := range []int32{s2, s3, s6} {
		if strings.Contains(reqs[5].body, fmt.Sprintf(`"%d"`, id)) {
			t.Errorf("submission %d must not be sent: %s", id, reqs[5].body)
		}
	}

	// Losing and regaining the leadership sends everything again.
	time.Sleep(100 * time.Millisecond)
	leading.Store(false)
	before := notLeading.Load()
	wake()
	e.eventually(5*time.Second, "the sink to notice", func() bool { return notLeading.Load() > before })
	n = rs.count()
	leading.Store(true)
	wake()
	e.eventually(10*time.Second, "the resend on becoming active", func() bool { return rs.count() >= n+6 })
	for i, r := range rs.requests()[n : n+6] {
		if r.path != paths[i] || r.status != 204 {
			t.Fatalf("request %d after regaining the leadership: %s -> %d", i, r.path, r.status)
		}
	}
}

func TestRankingSinkBatches(t *testing.T) {
	rs := newRankingServer(t, nil)
	url := strings.Replace(rs.srv.URL, "http://", "http://u:s3cret@", 1) + "/prefix"
	cfg := rankingConfig(url)
	sink, err := NewRankingSink(cfg, nil, newEnvLog(), prometheus.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	tg := sink.targets[0]
	if strings.Contains(tg.name, "s3cret") || tg.user != "u" || tg.pass != "s3cret" {
		t.Fatalf("target %q as %q", tg.name, tg.user)
	}
	data := map[string]any{}
	for i := range rankingweb.MaxBatch + 1 {
		data[fmt.Sprint(i)] = rankingweb.Team{Name: "t"}
	}
	if err := sink.put(context.Background(), tg, "teams", data); err != nil {
		t.Fatal(err)
	}
	reqs := rs.requests()
	if len(reqs) != 2 {
		t.Fatalf("%d requests, want 2", len(reqs))
	}
	total := 0
	for _, r := range reqs {
		var m map[string]any
		if err := json.Unmarshal([]byte(r.body), &m); err != nil {
			t.Fatal(err)
		}
		if len(m) > rankingweb.MaxBatch {
			t.Errorf("a request of %d entities", len(m))
		}
		total += len(m)
		if r.path != "/prefix/teams/" || r.user != "u" || r.pass != "s3cret" {
			t.Errorf("request %s as %s:%s", r.path, r.user, r.pass)
		}
	}
	if total != rankingweb.MaxBatch+1 {
		t.Errorf("%d entities sent, want %d", total, rankingweb.MaxBatch+1)
	}
	// Nothing to send: no request.
	if err := sink.put(context.Background(), tg, "teams", map[string]any{}); err != nil || rs.count() != 2 {
		t.Errorf("empty put: %v, %d requests", err, rs.count())
	}
	// A failure is an error that names the ranking without its password.
	rs.fail.Store(1)
	err = sink.put(context.Background(), tg, "teams", map[string]any{"a": rankingweb.Team{Name: "a"}})
	if err == nil || !strings.Contains(err.Error(), "503") || strings.Contains(err.Error(), "s3cret") {
		t.Errorf("failed put: %v", err)
	}
}

func TestRankingSinkConfig(t *testing.T) {
	cfg := config.Default()
	if s, err := NewRankingSink(cfg, nil, newEnvLog(), nil); s != nil || err != nil {
		t.Fatalf("no rankings: %v %v", s, err)
	}
	// Without a password the one derived from secret_key is used, which
	// needs secret_key.
	cfg.Dispatcher.Rankings = []config.RankingTarget{{URL: "http://r/"}}
	if _, err := NewRankingSink(cfg, nil, newEnvLog(), nil); err == nil {
		t.Error("a derived password without secret_key")
	}
	cfg.Dispatcher.Rankings = []config.RankingTarget{{URL: "http://u:@r/"}}
	if _, err := NewRankingSink(cfg, nil, newEnvLog(), nil); err == nil {
		t.Error("an empty password in the URL without secret_key")
	}
	cfg.Dispatcher.Rankings = []config.RankingTarget{{URL: "http://u:p@r/"}}
	if _, err := NewRankingSink(cfg, nil, newEnvLog(), nil); err != nil {
		t.Errorf("a password in the URL: %v", err)
	}
	cfg.SecretKey = rankingTestKey
	cfg.Dispatcher.Rankings = []config.RankingTarget{{URL: "http://r"}}
	s, err := NewRankingSink(cfg, nil, newEnvLog(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.targets[0].base.String(); got != "http://r/" {
		t.Errorf("base %q", got)
	}
	wu, wp := cfg.RankingWebCredentials()
	if s.targets[0].user != wu || s.targets[0].pass != wp {
		t.Error("the default credentials of the dispatcher and ranking-web differ")
	}
}
