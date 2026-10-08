// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package kv

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestRedisLoggerWritesStructuredWarnings(t *testing.T) {
	var buf bytes.Buffer
	l := redisLogger{slog.New(slog.NewJSONHandler(&buf, nil))}
	l.Printf(context.Background(), "redis: %s failed: %v", "dial", "connection refused")
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("not JSON: %q", buf.String())
	}
	if rec["level"] != "WARN" || rec["msg"] != "valkey client" || rec["detail"] != "redis: dial failed: connection refused" {
		t.Errorf("record %v", rec)
	}
}
