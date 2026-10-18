// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2010-2019 Giovanni Mascellani, Stefano Maggiolo, Luca Wehrstedt and the CMS contributors
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/service/ProxyService.py of CMS (http://cms-dev.github.io/).

package dispatcher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"math/rand/v2"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/scoretype"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/rankingweb"
)

// RankingSink is the ScoreSink that keeps the public scoreboards up to
// date (CMS ProxyService, R7.2): it sends contests, tasks, teams, users,
// submissions and subchanges to every ranking of dispatcher.rankings with
// HTTP PUT and basic authentication, in the formats of CMS, so it works
// with ranking-web and with a CMS RankingWebServer.
//
// Nothing is lost across restarts or failures without needing a durable
// queue, because what it sends is state, not deltas, and every PUT is
// idempotent:
//
//   - Push only marks submissions and tasks as dirty (it never blocks the
//     loop of the dispatcher); one goroutine per ranking reads their
//     current state from the database and sends it;
//   - when this dispatcher becomes the active one (start, failover), after
//     any failure (with backoff 1 s to 1 min) and every fullResync, the
//     whole state of the contests is sent again: a crash between a commit
//     and its push, or a ranking that lost its data, is repaired then;
//   - contests, tasks, teams and users are read again every
//     dispatcher.ranking_refresh (edits of the admin site do not notify
//     the dispatcher) and only what changed since the last successful send
//     goes out (ranking-web also ignores unchanged entities, so a full
//     resend costs bandwidth, not events).
//
// Like CMS, hidden participations and unofficial submissions are never
// sent, scores are sent once a submission is scored on the active dataset
// (an invalidated one keeps its old score on the scoreboard until scored
// again). Unlike CMS, what is no longer sent (removed contests, tasks,
// teams and users, users with only hidden participations) is deleted from
// the rankings at each refresh, unless the ranking's prune is false
// (docs/*/scoreboard.md).
type RankingSink struct {
	pool    *pgxpool.Pool
	log     *slog.Logger
	refresh time.Duration
	targets []*rankingTarget

	mu      sync.Mutex
	leading func() bool

	pushes *prometheus.CounterVec
	up     *prometheus.GaugeVec
}

// fullResync is how often everything is sent again even without errors.
const fullResync = 10 * time.Minute

// rankingBackoffMax bounds the wait after failures (CMS FAILURE_WAIT).
const rankingBackoffMax = time.Minute

type rankingTarget struct {
	name     string // URL without credentials, for logs and metrics
	base     *url.URL
	user     string
	pass     string
	contests []string // names; empty: all
	freeze   time.Duration
	prune    bool
	client   *http.Client

	mu    sync.Mutex
	subs  map[int32]struct{}
	tasks map[int32]struct{}
	meta  bool
	full  bool
	wake  chan struct{}

	// Owned by the goroutine of the target.
	contestIDs []int32
	metaDone   bool
	sent       map[string]map[string]string // path -> key -> JSON last sent
}

// NewRankingSink returns the sink of cfg.Dispatcher.Rankings (nil when
// there is none). Call SetLeading and run Run.
func NewRankingSink(cfg *config.Config, pool *pgxpool.Pool, log *slog.Logger, reg prometheus.Registerer) (*RankingSink, error) {
	if len(cfg.Dispatcher.Rankings) == 0 {
		return nil, nil
	}
	s := &RankingSink{
		pool: pool, log: log.With("component", "rankings"), refresh: cfg.Dispatcher.RankingRefresh.D(),
		leading: func() bool { return false },
		pushes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cms_dispatcher_ranking_requests_total",
			Help: "Requests sent to the rankings, by ranking and result (ok, error).",
		}, []string{"ranking", "result"}),
		up: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cms_dispatcher_ranking_up",
			Help: "1 when the last request to the ranking succeeded.",
		}, []string{"ranking"}),
	}
	if reg != nil {
		reg.MustRegister(s.pushes, s.up)
	}
	for i, tc := range cfg.Dispatcher.Rankings {
		u, err := url.Parse(tc.URL)
		if err != nil {
			return nil, fmt.Errorf("dispatcher.rankings[%d].url: %w", i, err)
		}
		user, pass := tc.Credentials(cfg)
		var urlPass string
		if u.User != nil {
			urlPass, _ = u.User.Password()
		}
		if tc.Password == "" && urlPass == "" {
			if err := cfg.RequireSecretKey(); err != nil {
				return nil, fmt.Errorf("dispatcher.rankings[%d] has no password, which is then derived from secret_key: %w", i, err)
			}
		}
		u.User = nil
		if !strings.HasSuffix(u.Path, "/") {
			u.Path += "/"
		}
		s.targets = append(s.targets, &rankingTarget{
			name: u.String(), base: u, user: user, pass: pass,
			contests: tc.Contests, freeze: tc.Freeze.D(), prune: tc.PruneEnabled(),
			client: &http.Client{Timeout: time.Minute},
			subs:   map[int32]struct{}{}, tasks: map[int32]struct{}{},
			wake: make(chan struct{}, 1),
			sent: map[string]map[string]string{},
		})
	}
	return s, nil
}

// SetLeading sets how the sink knows whether this dispatcher is the
// active one: only the active one sends.
func (s *RankingSink) SetLeading(f func() bool) {
	s.mu.Lock()
	s.leading = f
	s.mu.Unlock()
}

func (s *RankingSink) isLeading() bool {
	s.mu.Lock()
	f := s.leading
	s.mu.Unlock()
	return f()
}

// Push implements ScoreSink (CMS submission_scored, submission_tokened
// and dataset_updated). It never blocks.
func (s *RankingSink) Push(_ context.Context, u ScoreUpdate) {
	for _, t := range s.targets {
		t.mu.Lock()
		for _, c := range u.Submissions {
			if c.Scored {
				t.subs[c.SubmissionID] = struct{}{}
			}
		}
		for _, id := range u.Tokens {
			t.subs[id] = struct{}{}
		}
		for _, id := range u.Tasks {
			t.tasks[id] = struct{}{}
		}
		t.mu.Unlock()
		t.signal()
	}
}

func (t *rankingTarget) signal() {
	select {
	case t.wake <- struct{}{}:
	default:
	}
}

// Run sends to every ranking until ctx ends.
func (s *RankingSink) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	for _, t := range s.targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.loop(ctx, t)
		}()
	}
	wg.Wait()
	return nil
}

func (s *RankingSink) loop(ctx context.Context, t *rankingTarget) {
	log := s.log.With("ranking", t.name)
	var (
		wasLeading bool
		lastFull   time.Time
		lastMeta   time.Time
		backoff    time.Duration
		failing    bool
	)
	for ctx.Err() == nil {
		if !s.isLeading() {
			wasLeading = false
			sleep(ctx, t.wake, time.Second)
			continue
		}
		now := time.Now()
		t.mu.Lock()
		if !wasLeading || now.Sub(lastFull) >= fullResync {
			t.full = true
		}
		if now.Sub(lastMeta) >= s.refresh {
			t.meta = true
		}
		t.mu.Unlock()
		wasLeading = true

		full, err := s.sync(ctx, t)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			if !failing {
				log.Warn("cannot update the ranking; everything will be sent again when it works", "err", err)
			}
			failing = true
			s.up.WithLabelValues(t.name).Set(0)
			t.mu.Lock()
			t.full = true
			t.mu.Unlock()
			backoff = min(max(time.Second, 2*backoff), rankingBackoffMax)
			// Jitter, so that several dispatchers' rankings do not retry together.
			sleep(ctx, nil, backoff-time.Duration(rand.Int64N(int64(backoff/5)+1)))
			continue
		}
		if failing {
			log.Info("the ranking is up to date again")
		}
		failing, backoff = false, 0
		s.up.WithLabelValues(t.name).Set(1)
		now = time.Now()
		if full {
			lastFull, lastMeta = now, now
		}
		if t.metaDone {
			lastMeta = now
			t.metaDone = false
		}
		wait := s.refresh - now.Sub(lastMeta)
		sleep(ctx, t.wake, max(wait, 10*time.Millisecond))
	}
}

func sleep(ctx context.Context, wake <-chan struct{}, d time.Duration) {
	tm := time.NewTimer(d)
	defer tm.Stop()
	select {
	case <-ctx.Done():
	case <-wake:
	case <-tm.C:
	}
}

// sync sends what is dirty; it reports whether it was a full resend.
func (s *RankingSink) sync(ctx context.Context, t *rankingTarget) (bool, error) {
	t.mu.Lock()
	full, meta := t.full, t.meta
	subs := slices.Sorted(maps.Keys(t.subs))
	tasks := slices.Sorted(maps.Keys(t.tasks))
	if full {
		subs, tasks = nil, nil
	}
	t.subs, t.tasks = map[int32]struct{}{}, map[int32]struct{}{}
	t.full, t.meta = false, false
	t.mu.Unlock()
	if !full && !meta && len(subs) == 0 && len(tasks) == 0 {
		return false, nil
	}
	err := s.send(ctx, t, full, meta, subs, tasks)
	if err != nil {
		// Send everything again (full) once it works.
		t.mu.Lock()
		t.full = true
		t.mu.Unlock()
	}
	return full, err
}

func (s *RankingSink) send(ctx context.Context, t *rankingTarget, full, meta bool, subs, tasks []int32) error {
	if full {
		clear(t.sent)
	}
	if full || meta || len(tasks) > 0 || t.contestIDs == nil {
		if err := s.sendMeta(ctx, t); err != nil {
			return err
		}
		t.metaDone = true
	}
	if len(t.contestIDs) == 0 {
		return nil
	}
	var rows []rankingSubmission
	var err error
	switch {
	case full:
		rows, err = s.querySubmissions(ctx, t.contestIDs, "", nil)
	default:
		if len(tasks) > 0 {
			rows, err = s.querySubmissions(ctx, t.contestIDs, "s.task_id = ANY($2)", tasks)
		}
		if err == nil && len(subs) > 0 {
			var more []rankingSubmission
			more, err = s.querySubmissions(ctx, t.contestIDs, "s.id = ANY($2)", subs)
			rows = append(rows, more...)
		}
	}
	if err != nil {
		return err
	}
	return s.sendSubmissions(ctx, t, rows)
}

// rankingSubmission is an official submission of a visible participation
// with a score on the active dataset or a token.
type rankingSubmission struct {
	id        int32
	timestamp time.Time
	username  string
	task      string
	score     *float64
	details   []string
	token     *time.Time
}

func (s *RankingSink) querySubmissions(ctx context.Context, contests []int32, filter string, ids []int32) ([]rankingSubmission, error) {
	q := `SELECT s.id, s.timestamp, u.username, t.name, sr.score, sr.ranking_score_details, tk.timestamp
FROM submissions s
JOIN participations p ON p.id = s.participation_id
JOIN users u ON u.id = p.user_id
JOIN tasks t ON t.id = s.task_id
LEFT JOIN submission_results sr ON sr.submission_id = s.id AND sr.dataset_id = t.active_dataset_id
	AND sr.score IS NOT NULL AND sr.ranking_score_details IS NOT NULL
LEFT JOIN tokens tk ON tk.submission_id = s.id
WHERE s.official AND NOT p.hidden AND t.contest_id = ANY($1)
	AND (sr.submission_id IS NOT NULL OR tk.id IS NOT NULL)`
	args := []any{contests}
	if filter != "" {
		q += " AND " + filter
		args = append(args, ids)
	}
	q += " ORDER BY s.id"
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (rankingSubmission, error) {
		var x rankingSubmission
		err := r.Scan(&x.id, &x.timestamp, &x.username, &x.task, &x.score, &x.details, &x.token)
		return x, err
	})
}

// sendSubmissions sends the submissions and their subchanges (CMS
// operations_for_score and operations_for_token).
func (s *RankingSink) sendSubmissions(ctx context.Context, t *rankingTarget, rows []rankingSubmission) error {
	if len(rows) == 0 {
		return nil
	}
	// A submission of a user or task the ranking does not know yet (added
	// after the last refresh): refresh first.
	for _, r := range rows {
		if _, ok := t.sent["users"][rankingweb.EncodeID(r.username)]; !ok {
			if err := s.sendMeta(ctx, t); err != nil {
				return err
			}
			break
		}
		if _, ok := t.sent["tasks"][rankingweb.EncodeID(r.task)]; !ok {
			if err := s.sendMeta(ctx, t); err != nil {
				return err
			}
			break
		}
	}
	subs := map[string]any{}
	changes := map[string]any{}
	for _, r := range rows {
		id := fmt.Sprintf("%d", r.id)
		subs[id] = rankingweb.Submission{
			User: rankingweb.EncodeID(r.username), Task: rankingweb.EncodeID(r.task), Time: r.timestamp.Unix(),
		}
		if r.score != nil {
			sc := rankingweb.Float(*r.score)
			extra := r.details
			if extra == nil {
				extra = []string{}
			}
			changes[fmt.Sprintf("%d%ss", r.timestamp.Unix(), id)] = rankingweb.Subchange{
				Submission: id, Time: r.timestamp.Unix(), Score: &sc, Extra: &extra,
			}
		}
		if r.token != nil {
			tok := true
			changes[fmt.Sprintf("%d%st", r.token.Unix(), id)] = rankingweb.Subchange{
				Submission: id, Time: r.token.Unix(), Token: &tok,
			}
		}
	}
	if err := s.put(ctx, t, "submissions", subs); err != nil {
		return err
	}
	return s.put(ctx, t, "subchanges", changes)
}

// sendMeta sends the contests, teams, users and tasks that changed since
// they were last sent (CMS initialize).
func (s *RankingSink) sendMeta(ctx context.Context, t *rankingTarget) error {
	names := t.contests
	if names == nil {
		names = []string{}
	}
	rows, err := s.pool.Query(ctx, `SELECT c.id, c.name, c.description, c.score_precision,
	COALESCE(g.start, (SELECT min(start) FROM groups WHERE contest_id = c.id)),
	COALESCE(g.stop, (SELECT max(stop) FROM groups WHERE contest_id = c.id))
FROM contests c LEFT JOIN groups g ON g.id = c.main_group_id
WHERE cardinality($1::text[]) = 0 OR c.name = ANY($1)
ORDER BY c.id`, names)
	if err != nil {
		return err
	}
	type contestRow struct {
		id          int32
		name, descr string
		precision   int32
		start, stop *time.Time
	}
	crows, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (contestRow, error) {
		var c contestRow
		err := r.Scan(&c.id, &c.name, &c.descr, &c.precision, &c.start, &c.stop)
		return c, err
	})
	if err != nil {
		return err
	}
	contests := map[string]any{}
	ids := []int32{}
	for _, c := range crows {
		if c.start == nil || c.stop == nil {
			continue // a contest without groups has no times
		}
		begin, end := c.start.Unix(), c.stop.Unix()
		rc := rankingweb.Contest{Name: c.descr, Begin: begin, End: end, ScorePrecision: int64(c.precision)}
		if t.freeze > 0 {
			f := max(begin, end-int64(t.freeze/time.Second))
			rc.Freeze = &f
		}
		contests[rankingweb.EncodeID(c.name)] = rc
		ids = append(ids, c.id)
	}

	// A user in several contests is one user of the ranking (as CMS).
	users := map[string]any{}
	teams := map[string]any{}
	rows, err = s.pool.Query(ctx, `SELECT DISTINCT ON (u.username) u.username, u.first_name, u.last_name, tm.code, tm.name
FROM participations p
JOIN users u ON u.id = p.user_id
LEFT JOIN teams tm ON tm.id = p.team_id
WHERE p.contest_id = ANY($1) AND NOT p.hidden
ORDER BY u.username, p.contest_id DESC`, ids)
	if err != nil {
		return err
	}
	var username, first, last string
	var code, tname *string
	_, err = pgx.ForEachRow(rows, []any{&username, &first, &last, &code, &tname}, func() error {
		ru := rankingweb.User{FName: first, LName: last}
		if code != nil {
			k := rankingweb.EncodeID(*code)
			ru.Team = &k
			teams[k] = rankingweb.Team{Name: derefString(tname)}
		}
		users[rankingweb.EncodeID(username)] = ru
		return nil
	})
	if err != nil {
		return err
	}

	tasks, err := s.queryTasks(ctx, ids)
	if err != nil {
		return err
	}
	// Dependencies first: contests before tasks, teams before users.
	for _, part := range []struct {
		path string
		data map[string]any
	}{{"contests", contests}, {"teams", teams}, {"users", users}, {"tasks", tasks}} {
		if err := s.putChanged(ctx, t, part.path, part.data); err != nil {
			return err
		}
	}
	if t.prune {
		// Users before teams and tasks before contests: what depends on
		// an entity goes first (ranking-web would delete it anyway).
		for _, part := range []struct {
			path string
			want map[string]any
		}{{"tasks", tasks}, {"users", users}, {"teams", teams}, {"contests", contests}} {
			if err := s.prune(ctx, t, part.path, part.want); err != nil {
				return err
			}
		}
	}
	t.contestIDs = ids
	return nil
}

// prune deletes from the ranking the entities of path it holds that are
// not in want: removed from the database, or no longer shown (a contest
// without times, a user with only hidden participations). ranking-web
// deletes what depends on them (CMS delete).
func (s *RankingSink) prune(ctx context.Context, t *rankingTarget, path string, want map[string]any) error {
	have, err := s.list(ctx, t, path)
	if err != nil {
		return err
	}
	for _, k := range have {
		if _, ok := want[k]; ok {
			continue
		}
		if err := s.request(ctx, t, http.MethodDelete, path+"/"+k, nil); err != nil {
			s.pushes.WithLabelValues(t.name, "error").Inc()
			return fmt.Errorf("deleting %s/%s from ranking %s: %w", path, k, t.name, err)
		}
		s.pushes.WithLabelValues(t.name, "ok").Inc()
		delete(t.sent[path], k)
		s.log.Info("deleted from the ranking: no longer in the database or hidden", "ranking", t.name, "what", path, "key", k)
	}
	return nil
}

// list returns the keys of the entities of path the ranking holds.
func (s *RankingSink) list(ctx context.Context, t *rankingTarget, path string) ([]string, error) {
	u := t.base.JoinPath(path)
	u.Path += "/"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(t.user, t.pass)
	res, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("listing %s of ranking %s: %w", path, t.name, err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<16))
		return nil, fmt.Errorf("listing %s of ranking %s: status %s", path, t.name, res.Status)
	}
	var items map[string]json.RawMessage
	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<20)).Decode(&items); err != nil {
		return nil, fmt.Errorf("listing %s of ranking %s: %w", path, t.name, err)
	}
	return slices.Sorted(maps.Keys(items)), nil
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (s *RankingSink) queryTasks(ctx context.Context, contests []int32) (map[string]any, error) {
	rows, err := s.pool.Query(ctx, `SELECT t.name, t.title, COALESCE(t.num, 0), t.score_precision, t.score_mode, c.name,
	d.id, d.score_type, d.score_type_parameters
FROM tasks t
JOIN contests c ON c.id = t.contest_id
JOIN datasets d ON d.id = t.active_dataset_id
WHERE t.contest_id = ANY($1)
ORDER BY t.id`, contests)
	if err != nil {
		return nil, err
	}
	type taskRow struct {
		name, title  string
		num          int32
		precision    int32
		mode, cname  string
		dataset      int32
		scoreType    string
		scoreTypePar json.RawMessage
	}
	trows, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (taskRow, error) {
		var x taskRow
		err := r.Scan(&x.name, &x.title, &x.num, &x.precision, &x.mode, &x.cname, &x.dataset, &x.scoreType, &x.scoreTypePar)
		return x, err
	})
	if err != nil {
		return nil, err
	}
	datasets := make([]int32, len(trows))
	for i, x := range trows {
		datasets[i] = x.dataset
	}
	public := map[int32]map[string]bool{}
	rows, err = s.pool.Query(ctx, `SELECT dataset_id, codename, public FROM testcases WHERE dataset_id = ANY($1)`, datasets)
	if err != nil {
		return nil, err
	}
	var ds int32
	var codename string
	var pub bool
	_, err = pgx.ForEachRow(rows, []any{&ds, &codename, &pub}, func() error {
		if public[ds] == nil {
			public[ds] = map[string]bool{}
		}
		public[ds][codename] = pub
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	for _, x := range trows {
		maxScore, headers := 0.0, []string{}
		pt := public[x.dataset]
		if pt == nil {
			pt = map[string]bool{}
		}
		st, err := scoretype.New(x.scoreType, x.scoreTypePar, pt, int(x.precision))
		if err != nil {
			s.log.Warn("cannot compute the maximum score of a task for the rankings", "task", x.name, "err", err)
		} else {
			maxScore = st.MaxScore()
			if h := st.RankingHeaders(); h != nil {
				headers = h
			}
		}
		out[rankingweb.EncodeID(x.name)] = rankingweb.Task{
			Name: x.title, ShortName: x.name, Contest: rankingweb.EncodeID(x.cname),
			MaxScore: rankingweb.Float(maxScore), ScorePrecision: int64(x.precision),
			ExtraHeaders: headers, Order: int64(x.num), ScoreMode: x.mode,
		}
	}
	return out, nil
}

// putChanged sends the entities of data whose JSON differs from what was
// last sent successfully.
func (s *RankingSink) putChanged(ctx context.Context, t *rankingTarget, path string, data map[string]any) error {
	sent := t.sent[path]
	if sent == nil {
		sent = map[string]string{}
		t.sent[path] = sent
	}
	changed := map[string]any{}
	enc := map[string]string{}
	for k, v := range data {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		if sent[k] != string(b) {
			changed[k] = json.RawMessage(b)
			enc[k] = string(b)
		}
	}
	if err := s.put(ctx, t, path, changed); err != nil {
		return err
	}
	maps.Copy(sent, enc)
	return nil
}

// put sends data with PUT <ranking>/<path>/ (CMS safe_put_data), in
// requests of at most rankingweb.MaxBatch entities.
func (s *RankingSink) put(ctx context.Context, t *rankingTarget, path string, data map[string]any) error {
	keys := slices.Sorted(maps.Keys(data))
	for len(keys) > 0 {
		n := min(len(keys), rankingweb.MaxBatch)
		chunk := make(map[string]any, n)
		for _, k := range keys[:n] {
			chunk[k] = data[k]
		}
		keys = keys[n:]
		if err := s.putOnce(ctx, t, path, chunk); err != nil {
			s.pushes.WithLabelValues(t.name, "error").Inc()
			return fmt.Errorf("sending %s to ranking %s: %w", path, t.name, err)
		}
		s.pushes.WithLabelValues(t.name, "ok").Inc()
	}
	return nil
}

func (s *RankingSink) putOnce(ctx context.Context, t *rankingTarget, path string, data map[string]any) error {
	body, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return s.request(ctx, t, http.MethodPut, path+"/", body)
}

// request sends method to <ranking>/<path> (a collection ends with "/")
// with the credentials of the ranking; a 404 of a DELETE is success (the
// entity is already gone).
func (s *RankingSink) request(ctx context.Context, t *rankingTarget, method, path string, body []byte) error {
	u := *t.base
	u.Path += path
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.SetBasicAuth(t.user, t.pass)
	res, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<16))
	if res.StatusCode == http.StatusNotFound && method == http.MethodDelete {
		return nil
	}
	if res.StatusCode >= 400 {
		return fmt.Errorf("status %s", res.Status)
	}
	return nil
}
