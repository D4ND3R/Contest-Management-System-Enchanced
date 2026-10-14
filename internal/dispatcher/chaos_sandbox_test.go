//go:build sandbox

// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package dispatcher

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/steps/stepstest"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/kv"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/live"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/queue"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
)

// The chaos test of R4.6 and N3 (make test-sandbox, or make chaos for
// this test alone): the real `cms` binary runs a dispatcher and two
// workers as separate processes on isolate, PostgreSQL and Valkey, while
// this process plays the contestant site. CMS_TEST_CHAOS_SUBMISSIONS
// (default 44) sets the number of submissions and CMS_TEST_CHAOS_SEED the
// seed of the kills (default: random, logged).

const (
	chaosWorkers = 2
	// chaosLongDown is how long one killed worker stays down: its job must
	// go to the other worker by the visibility timeout (N3: < 60s).
	chaosLongDown = 40 * time.Second
)

// TestChaos judges the same submissions twice: once undisturbed, once
// while the dispatcher and the workers are killed with SIGKILL at random
// moments (mid-evaluation) and restarted, one worker stays down long
// enough for its job to be reclaimed by the visibility timeout, and the
// Valkey connections of every process are killed once (CLIENT KILL). In
// the end every submission must be judged exactly once per dataset (one
// evaluation per testcase, one score, never scored twice) with the same
// outcomes and scores as the undisturbed run, the task score cache must
// be the same, and the queues empty.
func TestChaos(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("the chaos test runs isolate: run it as root (make test-sandbox, make chaos)")
	}
	if _, err := exec.LookPath(isolatePath()); err != nil {
		t.Skipf("isolate not installed: %v", err)
	}
	n := envInt("CMS_TEST_CHAOS_SUBMISSIONS", 44)
	seed := uint64(time.Now().UnixNano())
	if v, err := strconv.ParseUint(os.Getenv("CMS_TEST_CHAOS_SEED"), 10, 64); err == nil {
		seed = v
	}
	t.Logf("seed %d (CMS_TEST_CHAOS_SEED), %d submissions", seed, n)
	bin := buildCMS(t)

	// The same submissions in both runs: participant and solution drawn
	// with a fixed seed, slow solutions less often.
	plan := chaosPlan(n)
	clean := runChaos(t, bin, plan, nil)
	t.Logf("undisturbed run: %d submissions judged in %v", n, clean.took.Round(time.Millisecond))
	chaos := runChaos(t, bin, plan, rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)))
	t.Logf("chaos run: judged in %v", chaos.took.Round(time.Millisecond))
	for _, line := range chaos.report {
		t.Log(line)
	}

	// Same outcomes and scores as the undisturbed run.
	for i := range plan {
		a, b := clean.results[i], chaos.results[i]
		if diff := compareJudged(a, b); diff != "" {
			t.Errorf("submission %d (%s of %s): undisturbed and chaos runs differ: %s", i, plan[i].sol.name, plan[i].user, diff)
		}
	}
	for user, s := range clean.taskScores {
		if c := chaos.taskScores[user]; c.Score != s.Score || c.Public != s.Public || c.Partial != s.Partial {
			t.Errorf("task score of %s: %+v undisturbed, %+v with chaos", user, s, c)
		}
	}
	if len(clean.taskScores) != len(chaos.taskScores) {
		t.Errorf("%d task scores undisturbed, %d with chaos", len(clean.taskScores), len(chaos.taskScores))
	}
}

// chaosSubmission is a submission of the plan.
type chaosSubmission struct {
	user string
	sol  solution
}

func chaosPlan(n int) []chaosSubmission {
	rng := rand.New(rand.NewPCG(42, 4242))
	var users []string
	for i := 1; i <= 12; i++ {
		users = append(users, fmt.Sprintf("u%02d", i))
	}
	var pool []solution
	for _, s := range solutions {
		w := 3
		if s.verdict == vTLE {
			w = 1 // about 7s of judging each
		}
		for range w {
			pool = append(pool, s)
		}
	}
	plan := make([]chaosSubmission, n)
	for i := range plan {
		plan[i] = chaosSubmission{users[rng.IntN(len(users))], pool[rng.IntN(len(pool))]}
	}
	return plan
}

// chaosOutcome is what a run left in the database.
type chaosOutcome struct {
	took time.Duration
	// results by index in the plan: dataset ("main", "alt") -> state.
	results    []map[string]*judged
	taskScores map[string]cachedScore
	report     []string
}

// compareJudged compares the results of a submission in two runs.
func compareJudged(a, b map[string]*judged) string {
	for _, ds := range []string{"main", "alt"} {
		x, y := a[ds], b[ds]
		if x == nil || y == nil {
			return fmt.Sprintf("%s: missing result", ds)
		}
		if fmt.Sprint(deref(x.Compilation)) != fmt.Sprint(deref(y.Compilation)) || x.Evaluated != y.Evaluated ||
			fmt.Sprint(deref(x.Score)) != fmt.Sprint(deref(y.Score)) || fmt.Sprint(deref(x.Public)) != fmt.Sprint(deref(y.Public)) ||
			x.Rows != y.Rows {
			return fmt.Sprintf("%s: compilation %v/%v, evaluated %v/%v, score %v/%v, public %v/%v, rows %d/%d", ds,
				deref(x.Compilation), deref(y.Compilation), x.Evaluated, y.Evaluated, deref(x.Score), deref(y.Score),
				deref(x.Public), deref(y.Public), x.Rows, y.Rows)
		}
		for tc, ex := range x.Evaluations {
			ey := y.Evaluations[tc]
			// The time limit can be noticed by the CPU or the wall clock.
			norm := func(s string) string { return strings.TrimSuffix(s, " (wall clock limit exceeded)") }
			if ex[0] != ey[0] || norm(ex[1]) != norm(ey[1]) {
				return fmt.Sprintf("%s testcase %s: %q %q / %q %q", ds, tc, ex[0], ex[1], ey[0], ey[1])
			}
		}
	}
	return ""
}

// isolatePath is the isolate binary of the sandbox tests.
func isolatePath() string {
	if p := os.Getenv("CMS_ISOLATE"); p != "" {
		return p
	}
	return "isolate"
}

// buildCMS builds the cms binary into a temporary directory.
func buildCMS(t *testing.T) string {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		goBin = filepath.Join(runtime.GOROOT(), "bin", "go")
	}
	bin := filepath.Join(t.TempDir(), "cms")
	cmd := exec.Command(goBin, "build", "-o", bin, "github.com/d4nd3r/contest-management-system-enchanced/cmd/cms")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building cms: %v\n%s", err, out)
	}
	return bin
}

// proc is a service of the installation run from the cms binary.
type proc struct {
	name    string // dispatcher, w1, w2
	service string
	env     []string
	ops     string // address of the ops listener
	logPath string

	mu     sync.Mutex
	cmd    *exec.Cmd
	done   chan struct{}
	starts int
	kills  int
	// expected is set before this test kills or stops the process;
	// unexpected lists the exits it did not cause (crashes).
	expected   bool
	unexpected []string
}

func (p *proc) start(t testing.TB, bin, config string) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	f, err := os.OpenFile(p.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "--config", config, p.service)
	cmd.Env = p.env
	cmd.Stdout, cmd.Stderr = f, f
	// Its own process group: the final cleanup kills what is left of
	// it (isolate processes of a killed worker).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		f.Close()
		t.Fatalf("starting %s: %v", p.name, err)
	}
	done := make(chan struct{})
	go func() {
		err := cmd.Wait()
		f.Close()
		p.mu.Lock()
		if !p.expected {
			p.unexpected = append(p.unexpected, fmt.Sprintf("exited by itself: %v", err))
		}
		p.mu.Unlock()
		close(done)
	}()
	p.cmd, p.done, p.expected = cmd, done, false
	p.starts++
}

// kill sends SIGKILL to the process alone (not to its isolate children:
// the worst case, like the OOM killer) and waits for it to die.
func (p *proc) kill() {
	p.mu.Lock()
	cmd, done := p.cmd, p.done
	p.kills++
	p.expected = true
	p.mu.Unlock()
	_ = cmd.Process.Signal(syscall.SIGKILL)
	<-done
}

// stop sends SIGTERM and waits for a clean exit.
func (p *proc) stop(timeout time.Duration) error {
	p.mu.Lock()
	cmd, done := p.cmd, p.done
	p.expected = true
	p.mu.Unlock()
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(timeout):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
		return fmt.Errorf("%s did not stop within %v", p.name, timeout)
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		return fmt.Errorf("%s exited with %d", p.name, code)
	}
	return nil
}

func (p *proc) running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	select {
	case <-p.done:
		return false
	default:
		return p.cmd != nil
	}
}

// healthy waits until the ops listener answers /healthz with 200.
func (p *proc) healthy(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + p.ops + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("%s not healthy after %v", p.name, timeout)
}

// tail returns the last lines of the log of p.
func (p *proc) tail(lines int) string {
	b, _ := os.ReadFile(p.logPath)
	all := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	return strings.Join(all[max(0, len(all)-lines):], "\n")
}

// installation is a run: its database, Valkey namespace, blob store and
// processes.
type installation struct {
	t       *testing.T
	ctx     context.Context
	bin     string
	config  string
	pool    *pgxpool.Pool
	kv      *kv.Client
	jobs    *queue.Queue
	results *queue.Queue
	c       *sumContest
	// clientName starts the Valkey client names of the processes (CLIENT
	// KILL picks them by it).
	clientName string
	procs      map[string]*proc

	mu     sync.Mutex
	report []string
}

func newInstallation(t *testing.T, bin string) *installation {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	pool := testutil.DB(t)
	kvc := testutil.Redis(t)
	blobDir := filepath.Join(dir, "blobs")
	blobs := must[*blob.Local](t)(blob.NewLocal(blobDir))
	inst := &installation{t: t, ctx: ctx, bin: bin, pool: pool, kv: kvc, procs: map[string]*proc{},
		clientName: fmt.Sprintf("chaos-%d", time.Now().UnixNano())}
	var users []string
	for i := 1; i <= 12; i++ {
		users = append(users, fmt.Sprintf("u%02d", i))
	}
	inst.c = newSumContest(t, pool, kvc, blobs, true, users...)
	inst.jobs = must[*queue.Queue](t)(queue.Open(ctx, kvc, job.JobQueue, JobQueueOptions))
	inst.results = must[*queue.Queue](t)(queue.Open(ctx, kvc, job.ResultQueue, ResultQueueOptions))

	// The configuration file shared by the processes (JSON is YAML).
	cfg := map[string]any{
		"secret_key": "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff",
		"data_dir":   filepath.Join(dir, "data"),
		"log":        map[string]any{"level": "info", "format": "json"},
		"database":   map[string]any{"url": pool.Config().ConnString(), "max_conns": 8},
		"redis":      map[string]any{"url": testutil.RedisURL(t), "key_prefix": kvc.Prefix()},
		"storage": map[string]any{"backend": "local", "local": map[string]any{"path": blobDir},
			"cache_dir": filepath.Join(dir, "cache")},
		"worker":  map[string]any{"concurrency": 2, "box_count": 8, "shutdown_timeout": "3s"},
		"sandbox": map[string]any{"cgroups": stepstest.Cgroups()},
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	inst.config = filepath.Join(dir, "cms.yaml")
	if err := os.WriteFile(inst.config, b, 0o600); err != nil {
		t.Fatal(err)
	}
	base := slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "CMS_") })
	redisURL := func(name string) string {
		sep := "?"
		if strings.Contains(testutil.RedisURL(t), "?") {
			sep = "&"
		}
		return "CMS_REDIS_URL=" + testutil.RedisURL(t) + sep + "client_name=" + inst.clientName + "-" + name
	}
	add := func(name, service string, env ...string) {
		p := &proc{name: name, service: service, logPath: filepath.Join(dir, name+".log"), ops: freePort(t)}
		p.env = append(slices.Clone(base), redisURL(name))
		p.env = append(p.env, env...)
		if service == "dispatcher" {
			p.env = append(p.env, "CMS_DISPATCHER_OPS_LISTEN="+p.ops)
		} else {
			p.env = append(p.env, "CMS_WORKER_OPS_LISTEN="+p.ops)
		}
		inst.procs[name] = p
	}
	add("dispatcher", "dispatcher")
	for i := 1; i <= chaosWorkers; i++ {
		name := fmt.Sprintf("w%d", i)
		add(name, "worker", "CMS_WORKER_NAME=chaos-"+name, "CMS_WORKER_BOX_ID_BASE="+strconv.Itoa(680+20*i),
			"CMS_WORKER_WORK_DIR="+filepath.Join(dir, name))
	}
	t.Cleanup(func() {
		for _, p := range inst.procs {
			p.mu.Lock()
			cmd := p.cmd
			p.expected = true
			p.mu.Unlock()
			if cmd != nil {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				<-p.done
			}
		}
		if t.Failed() {
			for _, name := range sortedKeys(inst.procs) {
				t.Logf("last lines of the log of %s:\n%s", name, inst.procs[name].tail(40))
			}
		}
	})
	return inst
}

func (inst *installation) logf(format string, args ...any) {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	inst.report = append(inst.report, fmt.Sprintf(format, args...))
}

// startAll starts every process and waits for them to be healthy.
func (inst *installation) startAll() {
	for _, name := range sortedKeys(inst.procs) {
		inst.procs[name].start(inst.t, inst.bin, inst.config)
	}
	for _, name := range sortedKeys(inst.procs) {
		if err := inst.procs[name].healthy(60 * time.Second); err != nil {
			inst.t.Fatal(err)
		}
	}
}

// heldBy returns the jobs held by the consumers of a worker.
func (inst *installation) heldBy(worker string) []string {
	held, err := inst.jobs.InFlight(inst.ctx, 1000)
	if err != nil {
		return nil
	}
	var ids []string
	for _, h := range held {
		if strings.HasPrefix(h.Consumer, "chaos-"+worker+"/") {
			ids = append(ids, h.ID)
		}
	}
	return ids
}

// active waits until the dispatcher process holds the lease (its
// cms_dispatcher_leader gauge), so that killing it kills an active one.
func (inst *installation) active(timeout time.Duration) bool {
	p := inst.procs["dispatcher"]
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + p.ops + "/metrics")
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if strings.Contains(string(b), "\ncms_dispatcher_leader 1\n") {
				return true
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// heldResults returns the results the dispatcher took and has not
// acknowledged yet (it is storing them).
func (inst *installation) heldResults() int {
	held, err := inst.results.InFlight(inst.ctx, 1000)
	if err != nil {
		return 0
	}
	n := 0
	for _, h := range held {
		if h.Consumer == ResultsConsumer {
			n++
		}
	}
	return n
}

// killClients kills the Valkey connections of every process.
func (inst *installation) killClients() int {
	list, err := inst.kv.Redis().ClientList(inst.ctx).Result()
	if err != nil {
		inst.t.Errorf("CLIENT LIST: %v", err)
		return 0
	}
	killed := 0
	sc := bufio.NewScanner(strings.NewReader(list))
	for sc.Scan() {
		var id, name string
		for _, f := range strings.Fields(sc.Text()) {
			if v, ok := strings.CutPrefix(f, "id="); ok {
				id = v
			}
			if v, ok := strings.CutPrefix(f, "name="); ok {
				name = v
			}
		}
		if id == "" || !strings.HasPrefix(name, inst.clientName+"-") {
			continue
		}
		if err := inst.kv.Redis().Do(inst.ctx, "CLIENT", "KILL", "ID", id).Err(); err == nil {
			killed++
		}
	}
	return killed
}

// monkey kills and restarts processes at random moments until the plan
// of actions is done; it returns once every process runs again.
func (inst *installation) monkey(rng *rand.Rand, stop <-chan struct{}) {
	t := inst.t
	sleep := func(lo, hi time.Duration) bool {
		d := lo + time.Duration(rng.Int64N(int64(hi-lo)))
		select {
		case <-stop:
			return false
		case <-time.After(d):
			return true
		}
	}
	// waitBusy waits (a while) until a worker runs a job, so that it dies
	// in the middle of one.
	waitBusy := func(w string) []string {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if held := inst.heldBy(w); len(held) > 0 {
				return held
			}
			time.Sleep(20 * time.Millisecond)
		}
		return nil
	}
	restart := func(p *proc) {
		p.start(t, inst.bin, inst.config)
	}
	// killRestart kills processes in the middle of their work and
	// restarts them. A dispatcher is killed once active (not while it
	// waits for the lease of its killed predecessor); with midBatch, while
	// it holds results it has not acknowledged (between taking a batch
	// and acknowledging it after the commit).
	killRestart := func(midBatch bool, names ...string) {
		for _, name := range names {
			p := inst.procs[name]
			if name == "dispatcher" {
				if !inst.active(30 * time.Second) {
					t.Errorf("the dispatcher did not become active within 30s")
				}
				held := 0
				if midBatch {
					for deadline := time.Now().Add(20 * time.Second); held == 0 && time.Now().Before(deadline); {
						if held = inst.heldResults(); held == 0 {
							time.Sleep(time.Millisecond)
						}
					}
				}
				p.kill()
				inst.logf("dispatcher killed while active, holding %d unacknowledged results", held)
				continue
			}
			held := len(waitBusy(name))
			p.kill()
			inst.logf("%s killed while holding %d jobs (running, or given back by a previous kill and not taken yet)", name, held)
		}
		if !sleep(100*time.Millisecond, 1500*time.Millisecond) {
			return
		}
		for _, name := range names {
			restart(inst.procs[name])
		}
	}
	other := func(w string) string {
		if w == "w1" {
			return "w2"
		}
		return "w1"
	}
	pick := func() string { return fmt.Sprintf("w%d", 1+rng.IntN(chaosWorkers)) }

	actions := []string{"worker", "dispatcher", "clients", "long", "batch", "worker", "both", "batch", "worker", "dispatcher"}
	var longDown string
	var longDownAt time.Time
	var reclaim sync.WaitGroup
	defer reclaim.Wait()
	for _, action := range actions {
		if !sleep(time.Second, 4*time.Second) {
			break
		}
		// The worker left down comes back after chaosLongDown.
		if longDown != "" && time.Since(longDownAt) >= chaosLongDown {
			restart(inst.procs[longDown])
			longDown = ""
		}
		w := pick()
		if w == longDown {
			w = other(w)
		}
		switch action {
		case "worker":
			killRestart(false, w)
		case "dispatcher":
			killRestart(false, "dispatcher")
		case "batch":
			killRestart(true, "dispatcher")
		case "both":
			killRestart(false, "dispatcher", w)
		case "clients":
			inst.logf("CLIENT KILL: %d Valkey connections of the processes killed", inst.killClients())
		case "long":
			// One worker dies mid-job and stays down: the other one must
			// get its jobs by the visibility timeout (N3).
			held := waitBusy(w)
			inst.procs[w].kill()
			longDown, longDownAt = w, time.Now()
			inst.logf("%s killed while holding %d jobs and left down for %v", w, len(held), chaosLongDown)
			if len(held) > 0 {
				reclaim.Add(1)
				go func(w string, at time.Time) {
					defer reclaim.Done()
					for time.Since(at) < 2*time.Minute {
						if len(inst.heldBy(w)) == 0 {
							inst.logf("jobs of the dead %s back in the queue and taken again after %v (N3: < 60s)",
								w, time.Since(at).Round(100*time.Millisecond))
							if time.Since(at) >= time.Minute {
								t.Errorf("the jobs of a dead worker came back after %v", time.Since(at))
							}
							return
						}
						time.Sleep(200 * time.Millisecond)
					}
					t.Errorf("the jobs of the dead worker %s never came back", w)
				}(w, longDownAt)
			}
		}
	}
	if longDown != "" {
		if wait := chaosLongDown - time.Since(longDownAt); wait > 0 {
			time.Sleep(wait)
		}
		restart(inst.procs[longDown])
	}
}

// runChaos runs the plan on a new installation, with chaos when rng is
// set, and returns what it left.
func runChaos(t *testing.T, bin string, plan []chaosSubmission, rng *rand.Rand) *chaosOutcome {
	t.Helper()
	inst := newInstallation(t, bin)
	c := inst.c
	inst.startAll()

	// A web process listening to the admin events of the contest: every
	// submission result reaches it once per change of state, whatever the
	// dataset.
	hub := live.NewHub(inst.kv, testLogger(), nil, live.HubOptions{Buffer: 1 << 16})
	liveCtx, stopLive := context.WithCancel(context.Background())
	var bg sync.WaitGroup
	bg.Go(func() { _ = hub.Run(liveCtx) })
	defer func() { stopLive(); bg.Wait() }()
	waitFor(t, 10*time.Second, "the hub", hub.Connected)
	events := &liveLog{}
	sub := hub.Subscribe([]live.Topic{live.AdminContestTopic(int64(c.contest.ID))}, "")
	bg.Go(func() { events.run(liveCtx, sub) })

	start := time.Now()
	stopMonkey := make(chan struct{})
	monkeyDone := make(chan struct{})
	if rng != nil {
		go func() {
			defer close(monkeyDone)
			inst.monkey(rng, stopMonkey)
		}()
	} else {
		close(monkeyDone)
	}
	// The site: the submissions arrive over about 30 seconds.
	ids := make([]int32, len(plan))
	submitted := map[string][]solution{}
	gap := 30 * time.Second / time.Duration(len(plan))
	for i, s := range plan {
		ids[i] = c.submit(s.user, s.sol, i+1)
		submitted[s.user] = append(submitted[s.user], s.sol)
		time.Sleep(gap)
	}
	<-monkeyDone
	// Every service comes back healthy (and is past its startup when it
	// is stopped at the end).
	for _, name := range sortedKeys(inst.procs) {
		p := inst.procs[name]
		if !p.running() {
			p.start(t, inst.bin, inst.config)
		}
		if err := p.healthy(time.Minute); err != nil {
			t.Error(err)
		}
	}
	waitFor(t, 10*time.Minute, "every submission judged on every dataset", func() bool { return c.unfinished() == 0 })
	took := time.Since(start)
	close(stopMonkey)
	waitFor(t, 30*time.Second, "empty queues", func() bool {
		js, err1 := inst.jobs.Stats(inst.ctx)
		rs, err2 := inst.results.Stats(inst.ctx)
		return err1 == nil && err2 == nil && js.Ready+js.Pending+js.Delayed+js.Dead == 0 &&
			rs.Ready+rs.Pending+rs.Delayed+rs.Dead == 0
	})

	// Every submission judged exactly once per dataset, as expected.
	out := &chaosOutcome{took: took, taskScores: c.taskScores()}
	state := c.snapshot()
	retried := 0
	for i, id := range ids {
		byName := map[string]*judged{}
		for ds, j := range state[id] {
			name := "main"
			if ds == c.alt.ID {
				name = "alt"
			}
			byName[name] = j
			// Failed attempts are retried (and counted): an attempt that
			// failed because of the chaos is not a wrong verdict.
			if j.CompTries > 0 || j.EvalTries > 0 {
				retried++
				j.CompTries, j.EvalTries = 0, 0
			}
		}
		for _, e := range c.checkJudged(state[id], plan[i].sol) {
			t.Errorf("submission %d (%s of %s): %s", i, plan[i].sol.name, plan[i].user, e)
		}
		out.results = append(out.results, byName)
	}
	want := c.wantTaskScores(submitted)
	for _, u := range sortedKeys(want) {
		if g := out.taskScores[u]; g.Score != want[u].Score || g.Public != want[u].Public || g.Partial {
			t.Errorf("task score of %s: %+v, want %+v", u, g, want[u])
		}
	}
	// Never scored twice: at most one final event per result (one is
	// missing when the dispatcher died between its commit and the
	// publication: live events are not durable).
	final := map[[2]int64]int{}
	for _, ev := range liveEvents[live.Submission](t, events.all(), live.AdminContestTopic(int64(c.contest.ID)), live.EventSubmission) {
		if ev.Status == live.SubmissionScored || ev.Status == live.SubmissionCompilationFailed {
			final[[2]int64{ev.ID, ev.Dataset}]++
		}
	}
	missing := 0
	for _, id := range ids {
		for ds := range c.datasets() {
			switch n := final[[2]int64{int64(id), int64(ds)}]; {
			case n > 1:
				t.Errorf("submission %d on dataset %d: %d final events (scored twice?)", id, ds, n)
			case n == 0:
				missing++
			}
		}
	}

	// Clean shutdown, then what the logs say.
	for _, name := range sortedKeys(inst.procs) {
		if err := inst.procs[name].stop(30 * time.Second); err != nil {
			t.Error(err)
		}
	}
	for _, name := range sortedKeys(inst.procs) {
		p := inst.procs[name]
		p.mu.Lock()
		for _, u := range p.unexpected {
			t.Errorf("%s %s", name, u)
		}
		p.mu.Unlock()
	}
	published, runs := inst.workerRuns()
	twice := 0
	for _, n := range published {
		if n > 1 {
			twice++
		}
	}
	if rng != nil {
		var kills []string
		for _, name := range sortedKeys(inst.procs) {
			p := inst.procs[name]
			kills = append(kills, fmt.Sprintf("%s killed %d times", name, p.kills))
		}
		inst.logf("%s; the workers published %d results for %d jobs (%d published twice; the jobs interrupted by a "+
			"kill ran again), %d results had a failed attempt that was retried; %d of %d final live events lost "+
			"(the dispatcher died between a commit and its publication)",
			strings.Join(kills, ", "), runs, len(published), twice, retried, missing, len(ids)*len(c.datasets()))
		inst.logf("dispatcher: %s", strings.Join(inst.dispatcherNotes(), "; "))
		inst.mu.Lock()
		out.report = slices.Clone(inst.report)
		inst.mu.Unlock()
	} else if retried > 0 || twice > 0 || missing > 0 {
		t.Errorf("undisturbed run: %d retried results, %d results published twice, %d live events missing", retried, twice, missing)
	}
	return out
}

// workerRuns reads the logs of the workers: how many times the result of
// each job was published ("job done" or "job failed"), and the total.
func (inst *installation) workerRuns() (map[string]int, int) {
	published := map[string]int{}
	total := 0
	for name, p := range inst.procs {
		if name == "dispatcher" {
			continue
		}
		forEachLogLine(inst.t, p.logPath, func(m map[string]any) {
			if msg := m["msg"]; msg == "job done" || msg == "job failed" {
				id, _ := m["job"].(string)
				published[id]++
				total++
			}
		})
	}
	return published, total
}

// dispatcherNotes summarizes the log of the dispatcher: takeovers,
// results taken over, sweeps that found work and warnings.
func (inst *installation) dispatcherNotes() []string {
	counts := map[string]int{}
	var found, takenOver float64
	forEachLogLine(inst.t, inst.procs["dispatcher"].logPath, func(m map[string]any) {
		msg, _ := m["msg"].(string)
		switch msg {
		case "active dispatcher":
			counts["became active"]++
		case "taking over the results of the previous dispatcher":
			v, _ := m["results"].(float64)
			takenOver += v
		case "sweep done":
			v, _ := m["found"].(float64)
			found += v
		}
		if lvl, _ := m["level"].(string); lvl == "WARN" || lvl == "ERROR" {
			counts[strings.ToLower(lvl)+": "+msg]++
		}
	})
	notes := []string{fmt.Sprintf("%d results held by a killed dispatcher redelivered to the next one", int(takenOver)),
		fmt.Sprintf("sweeps found %d operations or results to score", int(found))}
	for _, k := range sortedKeys(counts) {
		notes = append(notes, fmt.Sprintf("%s ×%d", k, counts[k]))
	}
	return notes
}

func forEachLogLine(t testing.TB, path string, fn func(map[string]any)) {
	f, err := os.Open(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			t.Error(err)
		}
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var m map[string]any
		if json.Unmarshal(sc.Bytes(), &m) == nil {
			fn(m)
		}
	}
}
