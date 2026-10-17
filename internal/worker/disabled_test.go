// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package worker

import (
	"context"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/worker/registry"
)

// TestDisabledWorkerTakesNoJobs: a worker an administrator disabled
// (registry.SetDisabled, the judges page) reports it in the registry and
// takes no job until it is enabled again.
func TestDisabledWorkerTakesNoJobs(t *testing.T) {
	h := newHarness(t, 5*time.Second)
	ctx := context.Background()
	if _, err := registry.SetDisabled(ctx, h.kv, "judge", true); err != nil {
		t.Fatal(err)
	}
	exec := &fakeExec{}
	r := h.start("judge", exec, Options{})
	waitFor(t, "the registry to show the worker disabled", func() bool {
		l, _ := registry.List(ctx, h.kv)
		return len(l) == 1 && l[0].Disabled
	})
	h.enqueue(evalJob(80, "tc1"), 0)
	h.noResult(500 * time.Millisecond)
	if exec.ncalls() != 0 {
		t.Fatal("a disabled worker ran a job")
	}
	if _, err := registry.SetDisabled(ctx, h.kv, "judge", false); err != nil {
		t.Fatal(err)
	}
	if res := h.result(10 * time.Second); !res.Success {
		t.Fatalf("result after enabling: %+v", res)
	}
	waitFor(t, "the registry to show the worker enabled", func() bool {
		l, _ := registry.List(ctx, h.kv)
		return len(l) == 1 && !l[0].Disabled
	})
	if err := r.stop(t); err != nil {
		t.Fatal(err)
	}
}
