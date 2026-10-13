// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package dispatcher

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/app"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/blob"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/judging"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
)

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

// syncBuffer is a log destination shared by goroutines.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// TestRun runs the service as `cms dispatcher` does: it judges a
// notified submission (with simulated workers on the real queues), serves
// the blob store to remote judges, and reports health and metrics.
func TestRun(t *testing.T) {
	e := newEnv(t)
	task, ds := e.task(3)
	sub := e.submit(e.participant("rita"), task, 1)[0]

	cfg := config.Default()
	cfg.SecretKey = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
	cfg.Database.URL = e.pool.Config().ConnString()
	cfg.Database.MaxConns = 4
	cfg.Redis.URL = testutil.RedisURL(t)
	cfg.Redis.KeyPrefix = e.kv.Prefix()
	cfg.Storage.Local.Path = t.TempDir()
	cfg.Dispatcher.OpsListen = freePort(t)
	cfg.Dispatcher.BlobListen = freePort(t)
	cfg.Dispatcher.SweepInterval = config.Duration(time.Second)
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	logs := &syncBuffer{}
	rt := app.New(Name, cfg, "", logs)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, rt) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run: %v", err)
			}
		case <-time.After(15 * time.Second):
			t.Error("Run did not stop")
		}
		if t.Failed() {
			t.Logf("logs:\n%s", logs)
		}
	}()

	e.startWorkers(1, allCorrect)
	e.notify(judging.Event{Kind: judging.EventSubmission, SubmissionIDs: []int32{sub}})
	e.waitScored(ds, 1, 20*time.Second)

	// Remote judges read and add blobs with the derived token only.
	client, err := blob.NewClient("http://"+cfg.Dispatcher.BlobListen, blob.Token(cfg.SecretKeyBytes()))
	if err != nil {
		t.Fatal(err)
	}
	d, err := client.PutBytes(ctx, []byte("executable"))
	if err != nil {
		t.Fatal(err)
	}
	rc, err := client.Open(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if string(got) != "executable" {
		t.Errorf("blob %q", got)
	}
	if bad, err := blob.NewClient("http://"+cfg.Dispatcher.BlobListen, strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	} else if _, err := bad.Stat(ctx, d); err == nil {
		t.Error("a wrong token reads blobs")
	}

	get := func(path string) string {
		resp, err := http.Get("http://" + cfg.Dispatcher.OpsListen + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: %d %s", path, resp.StatusCode, b)
		}
		return string(b)
	}
	health := get("/healthz")
	for _, check := range []string{`"database":"ok"`, `"valkey":"ok"`, `"queues":"ok"`, `"blob":"ok"`, `"dispatcher":"ok"`} {
		if !strings.Contains(health, check) {
			t.Errorf("healthz lacks %s: %s", check, health)
		}
	}
	metrics := get("/metrics")
	for _, m := range []string{"cms_dispatcher_leader 1", `cms_queue_ready{priority="0",queue="jobs"}`,
		`cms_dispatcher_results_total{outcome="applied"} 4`, "cms_dispatcher_submission_score_seconds_count 1",
		"cms_live_"} {
		if !strings.Contains(metrics, m) {
			t.Errorf("metrics lack %s", m)
		}
	}
	if !strings.Contains(logs.String(), `"msg":"active dispatcher"`) {
		t.Errorf("no JSON log of the lease:\n%s", logs)
	}
}
