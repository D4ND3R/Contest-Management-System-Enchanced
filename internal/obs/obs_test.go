// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package obs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
)

func TestLoggerJSONCarriesService(t *testing.T) {
	var buf bytes.Buffer
	l := NewLogger(&buf, config.Log{Level: "info", Format: "json"}, "dispatcher")
	l.Debug("hidden")
	l.Info("hello", "n", 3)
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("not one JSON record: %q", buf.String())
	}
	if rec["service"] != "dispatcher" || rec["msg"] != "hello" || rec["n"] != float64(3) {
		t.Errorf("record %v", rec)
	}
}

func TestRegistryHasBuildInfo(t *testing.T) {
	mfs, err := NewRegistry("worker").Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, mf := range mfs {
		if mf.GetName() != "cms_build_info" {
			continue
		}
		for _, lp := range mf.GetMetric()[0].GetLabel() {
			if lp.GetName() == "service" && lp.GetValue() == "worker" {
				found = true
			}
		}
	}
	if !found {
		t.Error("cms_build_info{service=worker} missing")
	}
}

func TestHealth(t *testing.T) {
	h := NewHealth()
	h.Timeout = 50 * time.Millisecond
	h.Register("db", func(context.Context) error { return nil })
	if rep := h.Run(context.Background()); rep.Status != "ok" {
		t.Fatalf("report %+v", rep)
	}
	h.Register("kv", func(context.Context) error { return errors.New("connection refused") })
	h.Register("slow", func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })

	rec := httptest.NewRecorder()
	h.Handler(true).ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code %d", rec.Code)
	}
	var rep Report
	_ = json.Unmarshal(rec.Body.Bytes(), &rep)
	if rep.Checks["db"] != "ok" || rep.Checks["kv"] != "connection refused" || !strings.Contains(rep.Checks["slow"], "deadline") {
		t.Errorf("checks %v", rep.Checks)
	}

	rec = httptest.NewRecorder()
	h.Handler(false).ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if strings.Contains(rec.Body.String(), "refused") {
		t.Errorf("public health leaks details: %s", rec.Body)
	}
}
