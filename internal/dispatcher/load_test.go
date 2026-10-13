// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package dispatcher

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"slices"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
)

// envInt reads a positive integer from the environment.
func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return def
}

// TestLoad is the load test of SPEC N1 for the dispatcher, with simulated
// workers that answer at once (so the dispatcher is the bottleneck): 300
// contestants submit 2 submissions/s for CMS_TEST_LOAD_SUSTAINED seconds
// (default 30), then 20/s for CMS_TEST_LOAD_BURST seconds (default 60),
// on a task of 20 testcases. It reports the time from the last
// evaluation of a submission to its verdict (target: under 2s), from the
// notification to the verdict, the throughput and the CPU used. It runs
// only with CMS_TEST_LOAD=1; to emulate the 2 vCPU target, run it with
// taskset -c 0,1 and GOMAXPROCS=2 (and pin PostgreSQL and Valkey too).
func TestLoad(t *testing.T) {
	if os.Getenv("CMS_TEST_LOAD") != "1" {
		t.Skip("set CMS_TEST_LOAD=1 to run the load test")
	}
	sustained := time.Duration(envInt("CMS_TEST_LOAD_SUSTAINED", 30)) * time.Second
	burst := time.Duration(envInt("CMS_TEST_LOAD_BURST", 60)) * time.Second
	testcases := envInt("CMS_TEST_LOAD_TESTCASES", 20)
	nWorkers := envInt("CMS_TEST_LOAD_WORKERS", 16)

	e := newEnv(t)
	task, ds := e.task(testcases)
	e.exec(`INSERT INTO users (first_name, last_name, username, password)
		SELECT 'u', 'u', 'user' || g, 'plaintext:x' FROM generate_series(1, 300) g`)
	e.exec(`INSERT INTO participations (contest_id, user_id, group_id)
		SELECT $1, u.id, $2 FROM users u WHERE u.username LIKE 'user%'`, e.contest, e.group)
	var parts []int32
	rows, err := e.pool.Query(e.ctx, `SELECT id FROM participations ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id int32
		_ = rows.Scan(&id)
		parts = append(parts, id)
	}
	rows.Close()

	opts := e.options("load")
	opts.SweepInterval = time.Minute
	opts.ResultBatch = 128
	opts.IdleWait = 100 * time.Millisecond
	opts.ProgressInterval = time.Second
	opts.Log = e.log
	e.start(opts)
	w := e.startWorkers(nWorkers, allCorrect)

	// The contestant site: insert the submission and notify, at the
	// given rate.
	var mu sync.Mutex
	notified := map[int32]time.Time{}
	submit := func(ctx context.Context, rate int, d time.Duration) int {
		tick := time.NewTicker(time.Second / time.Duration(rate))
		defer tick.Stop()
		end := time.Now().Add(d)
		n := 0
		for time.Now().Before(end) {
			<-tick.C
			p := parts[rand.IntN(len(parts))]
			s := e.id(`INSERT INTO submissions (opaque_id, participation_id, task_id, timestamp, language)
				VALUES (nextval('submissions_id_seq') + 1000000, $1, $2, now(), 'C++17 / g++') RETURNING id`, p, task)
			e.exec(`INSERT INTO files (submission_id, filename, digest) VALUES ($1, 'task1.%l', $2)`, s, digestOf(fmt.Sprint(s)))
			mu.Lock()
			notified[s] = time.Now()
			mu.Unlock()
			if err := judging.Notify(ctx, e.kv, judging.Event{Kind: judging.EventSubmission, SubmissionIDs: []int32{s}}); err != nil {
				t.Error(err)
			}
			n++
		}
		return n
	}
	var ru0 syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru0)
	start := time.Now()
	total := submit(e.ctx, 2, sustained)
	burstStart := time.Now()
	total += submit(e.ctx, 20, burst)
	sent := time.Now()
	e.waitScored(ds, total, 10*time.Minute)
	drained := time.Since(sent)
	elapsed := time.Since(start)
	var ru1 syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru1)
	cpu := time.Duration(syscall.TimevalToNsec(ru1.Utime) - syscall.TimevalToNsec(ru0.Utime) +
		syscall.TimevalToNsec(ru1.Stime) - syscall.TimevalToNsec(ru0.Stime))

	// Verdict times, from the scored events.
	scoredAt := map[int32]time.Time{}
	for _, s := range e.live.all() {
		if p, ok := s.ev.Data.(live.Submission); ok && p.Status == live.SubmissionScored && p.Dataset == int64(ds) {
			if _, seen := scoredAt[int32(p.ID)]; !seen {
				scoredAt[int32(p.ID)] = s.at
			}
		}
	}
	var verdict, endToEnd, burstEnd []time.Duration
	w.mu.Lock()
	for s, at := range scoredAt {
		if last, ok := w.lastResult[s]; ok {
			verdict = append(verdict, at.Sub(last))
		}
		if n, ok := notified[s]; ok {
			endToEnd = append(endToEnd, at.Sub(n))
			if n.After(burstStart) {
				burstEnd = append(burstEnd, at.Sub(n))
			}
		}
	}
	jobs := len(w.ops)
	w.mu.Unlock()
	pct := func(d []time.Duration, p float64) time.Duration {
		if len(d) == 0 {
			return 0
		}
		slices.Sort(d)
		return d[min(len(d)-1, int(p*float64(len(d))))]
	}
	t.Logf("%d submissions × %d testcases = %d jobs in %v (%.0f jobs/s); backlog drained %v after the last submission",
		total, testcases, jobs, elapsed.Round(time.Millisecond), float64(jobs)/elapsed.Seconds(), drained.Round(time.Millisecond))
	t.Logf("last evaluation -> verdict: p50 %v p95 %v p99 %v max %v", pct(verdict, .5), pct(verdict, .95),
		pct(verdict, .99), pct(verdict, 1))
	t.Logf("notification -> verdict: p50 %v p95 %v p99 %v max %v (burst only: p50 %v p95 %v)", pct(endToEnd, .5),
		pct(endToEnd, .95), pct(endToEnd, .99), pct(endToEnd, 1), pct(burstEnd, .5), pct(burstEnd, .95))
	t.Logf("CPU of this process (dispatcher, simulated workers and site): %v, %.2f ms per job; GOMAXPROCS %s",
		cpu.Round(time.Millisecond), float64(cpu.Microseconds())/1000/float64(jobs), os.Getenv("GOMAXPROCS"))
	t.Logf("apply: %.0f transactions, mean %.2f ms; jobs run twice: %d",
		counter(t, opts.Metrics, "cms_dispatcher_apply_seconds"), applyMean(t, opts), len(w.ranTwice()))
	if len(scoredAt) != total {
		t.Errorf("%d verdicts published for %d submissions", len(scoredAt), total)
	}
	if p := pct(verdict, .95); p > 2*time.Second {
		t.Errorf("p95 from the last evaluation to the verdict is %v (target < 2s)", p)
	}
	if twice := w.ranTwice(); len(twice) > 0 {
		t.Errorf("%d jobs ran twice", len(twice))
	}
}

// applyMean is the mean time of the result transactions, in ms.
func applyMean(t testing.TB, opts Options) float64 {
	n := counter(t, opts.Metrics, "cms_dispatcher_apply_seconds")
	if n == 0 {
		return 0
	}
	return 1000 * histogramSum(t, opts.Metrics, "cms_dispatcher_apply_seconds") / n
}
