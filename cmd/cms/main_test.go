// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
)

// syncBuffer is a goroutine-safe bytes.Buffer for service logs.
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

func runCmd(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(context.Background(), args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestUsageAndAlias(t *testing.T) {
	if code, out, _ := runCmd(t, "cms"); code != 2 || !strings.Contains(out, "contest-web") {
		t.Errorf("bare cms: code %d\n%s", code, out)
	}
	if code, _, errs := runCmd(t, "cms", "frobnicate"); code != 2 || !strings.Contains(errs, "unknown command") {
		t.Errorf("unknown command: code %d %s", code, errs)
	}
	code, out, _ := runCmd(t, "/usr/local/bin/cmsctl", "gen-secret")
	key := strings.TrimSpace(out)
	if b, err := hex.DecodeString(key); code != 0 || err != nil || len(b) != 32 {
		t.Errorf("cmsctl gen-secret: code %d %q", code, out)
	}
	t.Setenv("CMS_CONFIG", "")
	t.Setenv("CMS_SECRET_KEY", "00112233445566778899aabbccddeeff")
	code, out, _ = runCmd(t, "cms", "ctl", "blob-token")
	if tok := strings.TrimSpace(out); code != 0 || len(tok) != 64 || strings.Contains(tok, "00112233") {
		t.Errorf("blob-token: code %d %q", code, out)
	}
	if code, out, _ := runCmd(t, "cms", "version"); code != 0 || !strings.Contains(out, "source: https://") {
		t.Errorf("version: %d %q", code, out)
	}
	code, out, errs := runCmd(t, "cms", "ctl")
	for _, sub := range []string{"backup", "restore", "upgrade", "add-admin", "public-urls"} {
		if !strings.Contains(out+errs, "\n  "+sub+" ") {
			t.Errorf("cms ctl does not list %s: code %d\n%s%s", sub, code, out, errs)
		}
	}
	for _, args := range [][]string{{"restore"}, {"restore", "a", "b"}, {"backup", "extra"}, {"upgrade", "extra"}, {"add-admin"}, {"public-urls", "extra"}} {
		if code, _, _ := runCmd(t, append([]string{"cmsctl"}, args...)...); code != 2 {
			t.Errorf("cmsctl %q: code %d, want 2", args, code)
		}
	}
	if code, _, _ := runCmd(t, "cms", "ctl", "config", "show", "extra"); code != 2 {
		t.Errorf("extra argument accepted: %d", code)
	}
}

func TestWebServicesRequireSecret(t *testing.T) {
	t.Setenv("CMS_CONFIG", "")
	t.Setenv("CMS_SECRET_KEY", "")
	for _, svc := range []string{"contest-web", "admin-web", "ranking-web"} {
		code, _, errs := runCmd(t, "cms", svc)
		if code != 1 || !strings.Contains(errs, "secret_key is not set") {
			t.Errorf("%s without secret: code %d %q", svc, code, errs)
		}
	}
}

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

// waitHealthy waits until url reports healthy. The checks named in
// mayFail are allowed to fail (the sandbox of the worker on machines
// without isolate) as long as there are others and they pass.
func waitHealthy(t *testing.T, url string, logs *syncBuffer, mayFail ...string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			var body map[string]any
			_ = json.NewDecoder(resp.Body).Decode(&body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK || healthyExcept(body, mayFail) {
				return body
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s never became healthy; logs:\n%s", url, logs.String())
	return nil
}

func healthyExcept(report map[string]any, mayFail []string) bool {
	checks, _ := report["checks"].(map[string]any)
	if len(mayFail) == 0 || len(checks) <= len(mayFail) {
		return false
	}
	for name, v := range checks {
		if v != "ok" && !slices.Contains(mayFail, name) {
			return false
		}
	}
	return true
}

// TestServicesSmoke migrates a fresh database and runs every service
// against real PostgreSQL and Valkey: each must become healthy, expose
// metrics and shut down cleanly on cancellation.
func TestServicesSmoke(t *testing.T) {
	dbURL := testutil.EmptyDatabaseURL(t)
	t.Setenv("CMS_CONFIG", "")
	t.Setenv("CMS_DATABASE_URL", dbURL)
	t.Setenv("CMS_REDIS_URL", testutil.RedisURL(t))
	t.Setenv("CMS_REDIS_KEY_PREFIX", fmt.Sprintf("smoke:%d:", time.Now().UnixNano()))
	t.Setenv("CMS_SECRET_KEY", "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	// A relative work directory (made absolute by the worker), in a
	// temporary directory: the worker creates it.
	t.Chdir(t.TempDir())
	t.Setenv("CMS_WORKER_WORK_DIR", "relative/work")
	t.Setenv("CMS_STORAGE_LOCAL_PATH", t.TempDir())
	t.Setenv("CMS_LOG_LEVEL", "debug")

	// Services refuse an outdated schema; `cms migrate` fixes it.
	if code, out, errs := runCmd(t, "cms", "migrate", "status"); code != 0 || !strings.Contains(out, "pending") {
		t.Fatalf("migrate status: %d %s %s", code, out, errs)
	}
	if code, out, errs := runCmd(t, "cms", "migrate"); code != 0 || !strings.Contains(out, "applied") {
		t.Fatalf("migrate: %d %s %s", code, out, errs)
	}
	if code, out, _ := runCmd(t, "cms", "migrate", "status"); code != 0 || !strings.Contains(out, "up to date") {
		t.Fatalf("migrate status after: %d %s", code, out)
	}

	type svc struct {
		name      string
		publicEnv string // listen variable of the public site, if any
		opsEnv    string
	}
	services := []svc{
		{"contest-web", "CMS_CONTEST_WEB_LISTEN", "CMS_CONTEST_WEB_OPS_LISTEN"},
		{"admin-web", "CMS_ADMIN_WEB_LISTEN", "CMS_ADMIN_WEB_OPS_LISTEN"},
		{"ranking-web", "CMS_RANKING_WEB_LISTEN", "CMS_RANKING_WEB_OPS_LISTEN"},
		{"dispatcher", "", "CMS_DISPATCHER_OPS_LISTEN"},
		{"worker", "", "CMS_WORKER_OPS_LISTEN"},
		{"monitor", "", "CMS_MONITOR_LISTEN"},
	}
	public := map[string]string{}
	ops := map[string]string{}
	for _, s := range services {
		if s.publicEnv != "" {
			public[s.name] = freePort(t)
			t.Setenv(s.publicEnv, public[s.name])
		}
		ops[s.name] = freePort(t)
		t.Setenv(s.opsEnv, ops[s.name])
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		name string
		code int
	}
	done := make(chan result, len(services))
	logs := map[string]*syncBuffer{}
	for _, s := range services {
		buf := &syncBuffer{}
		logs[s.name] = buf
		go func(name string) {
			done <- result{name, run(ctx, []string{"cms", name}, buf, buf)}
		}(s.name)
	}

	for _, s := range services {
		var mayFail []string
		if s.name == "worker" {
			// Without isolate (or root, or cgroups v2) the worker runs but
			// takes no jobs, and says so.
			mayFail = []string{"sandbox"}
		}
		rep := waitHealthy(t, "http://"+ops[s.name]+"/healthz", logs[s.name], mayFail...)
		checks, _ := rep["checks"].(map[string]any)
		switch s.name {
		case "ranking-web":
		case "worker":
			if checks["valkey"] != "ok" || checks["blob"] != "ok" || checks["sandbox"] == nil {
				t.Errorf("%s checks: %v", s.name, rep)
			}
		default:
			if checks["database"] != "ok" || checks["valkey"] != "ok" {
				t.Errorf("%s checks: %v", s.name, rep)
			}
		}
		resp, err := http.Get("http://" + ops[s.name] + "/metrics")
		if err != nil {
			t.Fatal(err)
		}
		var body bytes.Buffer
		_, _ = body.ReadFrom(resp.Body)
		resp.Body.Close()
		if !strings.Contains(body.String(), `cms_build_info{commit=`) {
			t.Errorf("%s metrics lack cms_build_info", s.name)
		}
		if addr, ok := public[s.name]; ok {
			waitHealthy(t, "http://"+addr+"/healthz", logs[s.name])
			client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			resp, err := client.Get("http://" + addr + "/source")
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusFound || !strings.HasPrefix(resp.Header.Get("Location"), "https://github.com/") {
				t.Errorf("%s /source: %d %s", s.name, resp.StatusCode, resp.Header.Get("Location"))
			}
			if resp.Header.Get("Content-Security-Policy") == "" {
				t.Errorf("%s: no CSP header", s.name)
			}
		}
	}
	if code, _, errs := runCmd(t, "cms", "healthcheck", "http://"+ops["dispatcher"]+"/healthz"); code != 0 {
		t.Errorf("healthcheck of a healthy service: %d %s", code, errs)
	}
	if code, _, _ := runCmd(t, "cms", "healthcheck", "http://"+ops["dispatcher"]+"/nope"); code != 1 {
		t.Errorf("healthcheck of a 404: %d", code)
	}
	if !strings.Contains(logs["worker"].String(), `"service":"worker"`) {
		t.Errorf("worker logs are not JSON with the service name:\n%s", logs["worker"])
	}

	cancel()
	for range services {
		select {
		case r := <-done:
			if r.code != 0 {
				t.Errorf("%s exited with %d:\n%s", r.name, r.code, logs[r.name])
			}
		case <-time.After(15 * time.Second):
			t.Fatal("services did not stop")
		}
	}
}

// TestWorkerHasNoDatabaseAccess enforces the architecture: workers get
// everything from the job and the blob store, never from PostgreSQL.
func TestWorkerHasNoDatabaseAccess(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "../../internal/worker").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, dep := range strings.Fields(string(out)) {
		if strings.HasPrefix(dep, "github.com/jackc/pgx") || strings.HasSuffix(dep, "/internal/db") {
			t.Errorf("the worker depends on %s", dep)
		}
	}
}
