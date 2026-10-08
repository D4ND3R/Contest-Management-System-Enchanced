// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

//go:build linux

package live

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/httpx"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/kv"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
)

// The fan-out tests run the SSE server (a Valkey hub behind the httpx
// server and middleware) in a child process, limited to 2 CPUs like the
// target machine (N1), so that its memory and CPU are measured alone;
// the clients and the publisher run in the test process.

const (
	envFanoutServer = "CMS_LIVE_FANOUT_SERVER"
	envFanoutPrefix = "CMS_LIVE_FANOUT_PREFIX"
	// CMS_LIVE_FANOUT_CPUPROFILE=file writes a CPU profile of the server
	// process (go tool pprof file).
	envFanoutProfile = "CMS_LIVE_FANOUT_CPUPROFILE"
	fanoutCPUs       = 2
)

// fanoutStats is what the child reports.
type fanoutStats struct {
	RSS        uint64        `json:"rss"`
	HeapInuse  uint64        `json:"heap_inuse"`
	StackInuse uint64        `json:"stack_inuse"`
	Goroutines int           `json:"goroutines"`
	CPU        time.Duration `json:"cpu"`
	Subs       int           `json:"subs"`
}

// TestFanoutServer is not a test: it is the server process of TestFanout
// and BenchmarkFanout.
func TestFanoutServer(t *testing.T) {
	if os.Getenv(envFanoutServer) != "1" {
		t.Skip("helper process of TestFanout and BenchmarkFanout")
	}
	if err := fanoutServer(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "fan-out server:", err)
		os.Exit(1)
	}
	os.Exit(0)
}

func fanoutServer(in io.Reader, out io.Writer) error {
	if path := os.Getenv(envFanoutProfile); path != "" {
		pf, err := os.Create(path)
		if err != nil {
			return err
		}
		if err := pprof.StartCPUProfile(pf); err != nil {
			return err
		}
		defer func() {
			pprof.StopCPUProfile()
			_ = pf.Close()
		}()
	}
	opt, err := redis.ParseURL(os.Getenv(testutil.EnvRedisURL))
	if err != nil {
		return err
	}
	rdb := redis.NewClient(opt)
	defer rdb.Close()
	h := NewHub(kv.New(rdb, os.Getenv(envFanoutPrefix)), quiet, nil, HubOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()
	for start := time.Now(); !h.Connected(); time.Sleep(5 * time.Millisecond) {
		if time.Since(start) > 10*time.Second {
			return fmt.Errorf("the hub did not subscribe")
		}
	}

	mux := httpx.NewRouter()
	mux.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) {
		topics := []Topic{ContestTopic(1)}
		if p := r.URL.Query().Get("p"); p != "" {
			topics = append(topics, Topic("p:"+p))
		}
		h.Stream(w, r, StreamOptions{Topics: topics})
	})
	handler := httpx.Chain(mux, httpx.Recover(quiet), httpx.Observe(quiet, prometheus.NewRegistry(), 0),
		httpx.SecureHeaders, httpx.LimitBody(1<<20))
	ln, err := httpx.Listen("127.0.0.1:0")
	if err != nil {
		return err
	}
	served := make(chan error, 1)
	go func() { served <- httpx.Serve(ctx, quiet, "fanout", ln, handler) }()
	fmt.Fprintf(out, "ADDR %s\n", ln.Addr())

	sc := bufio.NewScanner(in)
	for sc.Scan() {
		if sc.Text() != "stats" {
			continue
		}
		var ru syscall.Rusage
		_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
		runtime.GC()
		debug.FreeOSMemory()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		st := fanoutStats{
			RSS: selfRSS(), HeapInuse: ms.HeapInuse, StackInuse: ms.StackInuse,
			Goroutines: runtime.NumGoroutine(),
			CPU:        time.Duration(ru.Utime.Nano() + ru.Stime.Nano()),
			Subs:       h.Subscribers(),
		}
		b, _ := json.Marshal(st)
		fmt.Fprintf(out, "STATS %s\n", b)
	}
	cancel()
	return <-served
}

// selfRSS returns the resident memory of the process.
func selfRSS() uint64 {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "VmRSS:"); ok {
			kb, _ := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(v), " kB"), 10, 64)
			return kb << 10
		}
	}
	return 0
}

type benchPayload struct {
	N int64 `json:"n"`
	T int64 `json:"t"` // publish time, Unix ns
}

// fanout is a server process and its clients.
type fanout struct {
	t     testing.TB
	n     int
	cmd   *exec.Cmd
	stdin io.WriteCloser
	lines chan string
	addr  string
	pub   *Publisher
	conns []net.Conn

	hello   atomic.Int64 // clients that got their first ping
	resyncs atomic.Int64
	got     []atomic.Int32 // deliveries of event n
	mu      sync.Mutex
	lat     []time.Duration
}

func startFanout(t testing.TB, n int) *fanout {
	t.Helper()
	kvc := testutil.Redis(t)
	f := &fanout{t: t, n: n, lines: make(chan string, 16), got: make([]atomic.Int32, 1<<16)}

	args := []string{"-test.run=^TestFanoutServer$"}
	if path, err := exec.LookPath("taskset"); err == nil && runtime.NumCPU() >= 2*fanoutCPUs {
		f.cmd = exec.Command(path, append([]string{"-c", fmt.Sprintf("0-%d", fanoutCPUs-1), os.Args[0]}, args...)...)
	} else {
		f.cmd = exec.Command(os.Args[0], args...)
	}
	f.cmd.Env = append(os.Environ(), envFanoutServer+"=1", envFanoutPrefix+"="+kvc.Prefix(),
		"GOMAXPROCS="+strconv.Itoa(fanoutCPUs))
	f.cmd.Stderr = os.Stderr
	var err error
	if f.stdin, err = f.cmd.StdinPipe(); err != nil {
		t.Fatal(err)
	}
	stdout, err := f.cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := f.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			f.lines <- sc.Text()
		}
		close(f.lines)
	}()
	t.Cleanup(f.stop)
	f.addr = strings.TrimPrefix(f.line("ADDR "), "ADDR ")
	f.pub = startPublisher(t, kvc, nil, PublisherOptions{})
	return f
}

func (f *fanout) line(prefix string) string {
	f.t.Helper()
	timeout := time.After(30 * time.Second)
	for {
		select {
		case l, ok := <-f.lines:
			if !ok {
				f.t.Fatal("the server process exited")
			}
			if strings.HasPrefix(l, prefix) {
				return l
			}
		case <-timeout:
			f.t.Fatalf("no %q from the server process", prefix)
		}
	}
}

func (f *fanout) stop() {
	for _, c := range f.conns {
		_ = c.Close()
	}
	_ = f.stdin.Close()
	done := make(chan error, 1)
	go func() { done <- f.cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = f.cmd.Process.Kill()
		<-done
	}
}

func (f *fanout) stats() fanoutStats {
	f.t.Helper()
	fmt.Fprintln(f.stdin, "stats")
	var st fanoutStats
	if err := json.Unmarshal([]byte(strings.TrimPrefix(f.line("STATS "), "STATS ")), &st); err != nil {
		f.t.Fatal(err)
	}
	return st
}

// connect opens n streams (client i also listens to p:i) and waits for
// their first pings.
func (f *fanout) connect() {
	f.t.Helper()
	f.conns = make([]net.Conn, f.n)
	sem := make(chan struct{}, 64)
	var wg sync.WaitGroup
	errs := make(chan error, f.n)
	for i := range f.n {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			c, err := net.Dial("tcp", f.addr)
			if err != nil {
				errs <- err
				return
			}
			f.conns[i] = c
			fmt.Fprintf(c, "GET /events?p=%d HTTP/1.1\r\nHost: cms\r\nAccept: text/event-stream\r\n\r\n", i)
			go f.read(c)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		f.t.Fatal(err)
	}
	f.wait("first pings", func() bool { return f.hello.Load() == int64(f.n) })
}

func (f *fanout) read(c net.Conn) {
	resp, err := http.ReadResponse(bufio.NewReaderSize(c, 1024), nil)
	if err != nil {
		return
	}
	rd := newSSEReader(resp.Body)
	first := true
	for {
		ev, err := rd.next()
		if err != nil {
			return
		}
		switch ev.Event {
		case EventPing:
			if first {
				first = false
				f.hello.Add(1)
			}
		case EventResync:
			f.resyncs.Add(1)
		case "bench":
			var p benchPayload
			if json.Unmarshal([]byte(ev.Data), &p) == nil && p.N < int64(len(f.got)) {
				d := time.Duration(ev.At.UnixNano() - p.T)
				f.mu.Lock()
				f.lat = append(f.lat, d)
				f.mu.Unlock()
				f.got[p.N].Add(1)
			}
		}
	}
}

func (f *fanout) wait(what string, cond func() bool) {
	f.t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			f.t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func (f *fanout) publish(n int64, topic Topic) {
	f.pub.Publish(Event{Name: "bench", Topics: []Topic{topic},
		Data: benchPayload{N: n, T: time.Now().UnixNano()}})
}

// latencies returns the recorded latencies, sorted, and forgets them.
func (f *fanout) latencies() []time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	l := f.lat
	f.lat = nil
	slices.Sort(l)
	return l
}

func pct(l []time.Duration, p float64) time.Duration {
	if len(l) == 0 {
		return 0
	}
	return l[min(len(l)-1, int(float64(len(l))*p))]
}

func perClient(after, before uint64, n int) float64 {
	return (float64(after) - float64(before)) / float64(n)
}

func fanoutClients(t testing.TB) int {
	if raceEnabled {
		return 300
	}
	return 1500
}

// TestFanout: 1500 streams on one hub (300 under the race detector);
// every client gets every event, and the latency from Publish to the
// client stays within N1 (p95 < 2 s).
func TestFanout(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	n := fanoutClients(t)
	f := startFanout(t, n)
	s0 := f.stats()
	f.connect()
	s1 := f.stats()
	if s1.Subs != n {
		t.Fatalf("%d subscriptions, want %d", s1.Subs, n)
	}

	// Broadcast: 40 contest events 25 ms apart (40/s; throttled events go
	// out at most once per second).
	const events = 40
	for k := range events {
		f.publish(int64(k), ContestTopic(1))
		time.Sleep(25 * time.Millisecond)
	}
	f.wait("the broadcast", func() bool { return f.got[events-1].Load() == int32(n) })
	s2 := f.stats()
	for k := range events {
		if got := f.got[k].Load(); got != int32(n) {
			t.Errorf("event %d reached %d clients of %d", k, got, n)
		}
	}
	bl := f.latencies()

	// Targeted: one event per contestant (p:i), 500 per second.
	for i := range n {
		f.publish(int64(events+i), Topic("p:"+strconv.Itoa(i)))
		if i%50 == 49 {
			time.Sleep(100 * time.Millisecond)
		}
	}
	f.wait("the targeted events", func() bool {
		for i := range n {
			if f.got[events+i].Load() != 1 {
				return false
			}
		}
		return true
	})
	s3 := f.stats()
	tl := f.latencies()
	if r := f.resyncs.Load(); r != 0 {
		t.Errorf("%d resyncs", r)
	}

	t.Logf("server process: GOMAXPROCS=%d, %d streams, %d goroutines", fanoutCPUs, n, s1.Goroutines)
	t.Logf("memory per stream: RSS %.1f KiB, heap %.1f KiB, stacks %.1f KiB",
		perClient(s1.RSS, s0.RSS, n)/1024, perClient(s1.HeapInuse, s0.HeapInuse, n)/1024,
		perClient(s1.StackInuse, s0.StackInuse, n)/1024)
	t.Logf("broadcast (%d events × %d streams): latency p50 %v p95 %v p99 %v max %v; server CPU %.1f µs per delivery",
		events, n, pct(bl, .5), pct(bl, .95), pct(bl, .99), pct(bl, 1),
		float64((s2.CPU-s1.CPU).Microseconds())/float64(events*n))
	t.Logf("targeted (%d events, 500/s): latency p50 %v p95 %v p99 %v max %v; server CPU %.1f µs per event",
		n, pct(tl, .5), pct(tl, .95), pct(tl, .99), pct(tl, 1),
		float64((s3.CPU-s2.CPU).Microseconds())/float64(n))
	for name, l := range map[string][]time.Duration{"broadcast": bl, "targeted": tl} {
		if p95 := pct(l, .95); p95 > 2*time.Second {
			t.Errorf("%s p95 latency %v > 2 s (N1)", name, p95)
		}
	}
}

// BenchmarkFanout: one broadcast to 1500 streams per iteration (publish,
// then wait until every client has it). Reports the latency from Publish
// to each client, the server's memory per stream and its CPU per
// delivery.
func BenchmarkFanout(b *testing.B) {
	n := fanoutClients(b)
	f := startFanout(b, n)
	s0 := f.stats()
	f.connect()
	s1 := f.stats()
	var k int64
	for b.Loop() {
		f.publish(k, ContestTopic(1))
		f.wait("the broadcast", func() bool { return f.got[k%int64(len(f.got))].Load() == int32(n) })
		k++
		if k%int64(len(f.got)) == 0 {
			for i := range f.got {
				f.got[i].Store(0)
			}
		}
	}
	b.StopTimer()
	s2 := f.stats()
	l := f.latencies()
	b.ReportMetric(float64(n), "streams")
	b.ReportMetric(perClient(s1.RSS, s0.RSS, n), "rss-B/stream")
	b.ReportMetric(perClient(s1.HeapInuse+s1.StackInuse, s0.HeapInuse+s0.StackInuse, n), "heap+stack-B/stream")
	b.ReportMetric(float64(pct(l, .5).Microseconds())/1000, "p50-ms")
	b.ReportMetric(float64(pct(l, .95).Microseconds())/1000, "p95-ms")
	b.ReportMetric(float64(pct(l, .99).Microseconds())/1000, "p99-ms")
	b.ReportMetric(float64((s2.CPU-s1.CPU).Nanoseconds())/float64(k*int64(n)), "server-cpu-ns/delivery")
}
