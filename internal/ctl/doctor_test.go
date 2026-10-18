// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package ctl

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/cli"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/grading/language"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/worker/registry"
)

// TestDoctor runs the command against the test database and Valkey: an
// installation without administrators, judges or contests fails, and the
// report says how to fix each problem.
func TestDoctor(t *testing.T) {
	pool := testutil.DB(t)
	redis := testutil.Redis(t)
	t.Setenv("CMS_DATABASE_URL", pool.Config().ConnString())
	t.Setenv("CMS_REDIS_URL", testutil.RedisURL(t))
	t.Setenv("CMS_REDIS_KEY_PREFIX", redis.Prefix())
	t.Setenv("CMS_SECRET_KEY", strings.Repeat("ab", 16))
	t.Setenv("CMS_STORAGE_BACKEND", "local")
	t.Setenv("CMS_STORAGE_LOCAL_PATH", t.TempDir())
	root := &cli.Command{Name: "ctl", Sub: []*cli.Command{doctorCommand()}}
	run := func(args ...string) (string, error) {
		var out, errb bytes.Buffer
		err := root.Execute(context.Background(), &cli.Env{Stdout: &out, Stderr: &errb, Prog: "cms"}, "cms ctl", args)
		return out.String(), err
	}
	out, err := run("doctor")
	if err == nil {
		t.Fatalf("doctor passed an installation without administrators or judges:\n%s", out)
	}
	for _, want := range []string{
		"PASS  PostgreSQL: connected", "PASS  migrations:", "FAIL  administrators: there is no administrator",
		"fix: sudo cmsctl add-admin admin", "PASS  Valkey: connected", "FAIL  judges: no judge is connected",
		"WARN  contests: there is no contest yet", "checks:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report lacks %q:\n%s", want, out)
		}
	}
	out, _ = run("doctor", "--json")
	var rep struct {
		Checks []doctorCheck `json:"checks"`
		Failed int           `json:"failed"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil || rep.Failed == 0 || len(rep.Checks) == 0 {
		t.Fatalf("--json: %v\n%s", err, out)
	}
}

func TestDoctorWorkersAndGraders(t *testing.T) {
	d := &doctor{}
	now := time.Now()
	d.checkWorkers([]registry.Info{
		{Name: "ok", Host: "h", Heartbeat: now, Concurrency: 1},
		{Name: "broken", Heartbeat: now, Error: "isolate: no cgroups"},
		{Name: "silent", Heartbeat: now.Add(-5 * time.Minute)},
	}, now)
	var levels []string
	for _, c := range d.checks {
		levels = append(levels, c.Level+" "+c.Name)
	}
	if want := []string{"PASS judge ok", "FAIL judge broken", "WARN judge silent"}; !slices.Equal(levels, want) {
		t.Errorf("workers: %v, want %v", levels, want)
	}

	langs := language.Builtin()
	allowed := []string{"C++17 / g++", "C11 / gcc", "Pascal / fpc"}
	if m := missingGraders([]string{"checker"}, allowed, langs); m != nil {
		t.Errorf("a task without graders needs none: %v", m)
	}
	if m := missingGraders([]string{"grader.cpp", "grader.h"}, allowed, langs); !slices.Equal(m, []string{"C11 / gcc", "Pascal / fpc"}) {
		t.Errorf("missing graders %v", m)
	}
}

func TestLocalURL(t *testing.T) {
	for addr, want := range map[string]string{
		"127.0.0.1:9188": "http://127.0.0.1:9188/healthz",
		":8000":          "http://127.0.0.1:8000/healthz",
		"0.0.0.0:8001":   "http://127.0.0.1:8001/healthz",
		"[::]:8002":      "http://127.0.0.1:8002/healthz",
		"bad":            "",
	} {
		if got := localURL(addr, "/healthz"); got != want {
			t.Errorf("localURL(%q) = %q, want %q", addr, got, want)
		}
	}
}
