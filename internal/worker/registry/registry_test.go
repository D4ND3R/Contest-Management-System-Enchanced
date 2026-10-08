// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package registry

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
)

func info(name string) Info {
	h, _ := os.Hostname()
	return Info{Name: name, Host: h, PID: os.Getpid(), Version: "test", Concurrency: 2, Boxes: 10,
		StartedAt: time.Now().Truncate(time.Millisecond)}
}

func TestRegisterUpdateListClose(t *testing.T) {
	c := testutil.Redis(t)
	ctx := context.Background()
	if l, err := List(ctx, c); err != nil || len(l) != 0 {
		t.Fatalf("empty list: %v %v", l, err)
	}
	a, err := Register(ctx, c, info("judge-b"), 0)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Register(ctx, c, info("judge-a"), 0)
	if err != nil {
		t.Fatal(err)
	}
	ttl, err := c.Redis().PTTL(ctx, entryKey(c, "judge-b")).Result()
	if err != nil || ttl <= 0 || ttl > TTL {
		t.Errorf("ttl %v, %v", ttl, err)
	}

	in := info("judge-b")
	op := job.Operation{Type: job.Evaluation, ObjectID: 12, DatasetID: 3, TestcaseCodename: "tc01"}
	in.Jobs = []Job{{Slot: 1, ID: "j1", Operation: op, Description: op.String(), Attempt: 2, StartedAt: time.Now()}}
	in.Done = 7
	if err := a.Update(ctx, in); err != nil {
		t.Fatal(err)
	}
	l, err := List(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(l) != 2 || l[0].Name != "judge-a" || l[1].Name != "judge-b" {
		t.Fatalf("list: %+v", l)
	}
	got := l[1]
	if len(got.Jobs) != 1 || got.Jobs[0].Operation != op || got.Jobs[0].Attempt != 2 || got.Done != 7 ||
		got.Concurrency != 2 || got.Version != "test" || got.Heartbeat.IsZero() {
		t.Errorf("entry: %+v", got)
	}
	if l[0].Jobs == nil {
		t.Error("an idle worker has nil jobs (JSON null)")
	}

	if err := a.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if l, err := List(ctx, c); err != nil || len(l) != 0 {
		t.Fatalf("after close: %+v %v", l, err)
	}
	if n, _ := c.Redis().Exists(ctx, entryKey(c, "judge-a"), c.Key(indexKey)).Result(); n != 0 {
		t.Errorf("%d keys left", n)
	}
}

func TestNameTaken(t *testing.T) {
	c := testutil.Redis(t)
	ctx := context.Background()
	first, err := Register(ctx, c, info("judge"), 0)
	if err != nil {
		t.Fatal(err)
	}
	// The same process (same host and PID: the holder is alive).
	start := time.Now()
	_, err = Register(ctx, c, info("judge"), 300*time.Millisecond)
	if !errors.Is(err, ErrNameTaken) {
		t.Fatalf("second registration: %v", err)
	}
	if d := time.Since(start); d < 300*time.Millisecond {
		t.Errorf("gave up after %v without waiting", d)
	}
	// Another host: its PID means nothing here.
	other := info("judge")
	other.Host = "elsewhere"
	if _, err := Register(ctx, c, other, 0); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("other host: %v", err)
	}
	// The first registration is untouched.
	if err := first.Update(ctx, info("judge")); err != nil {
		t.Fatal(err)
	}
	if _, err := Register(ctx, c, Info{Name: "bad name"}, 0); err == nil {
		t.Error("invalid name accepted")
	}
}

func TestWaitForExpiry(t *testing.T) {
	c := testutil.Redis(t)
	ctx := context.Background()
	if _, err := Register(ctx, c, info("judge"), 0); err != nil {
		t.Fatal(err)
	}
	// The holder crashed: its entry expires.
	if err := c.Redis().PExpire(ctx, entryKey(c, "judge"), 300*time.Millisecond).Err(); err != nil {
		t.Fatal(err)
	}
	r, err := Register(ctx, c, info("judge"), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Update(ctx, info("judge")); err != nil {
		t.Fatal(err)
	}
}

func TestTakeOverDeadProcess(t *testing.T) {
	c := testutil.Redis(t)
	ctx := context.Background()
	// A PID that existed and no longer runs.
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Skip("cannot run true:", err)
	}
	old := info("judge")
	old.PID = cmd.Process.Pid
	prev, err := Register(ctx, c, old, 0)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	r, err := Register(ctx, c, info("judge"), 0)
	if err != nil {
		t.Fatalf("take over: %v", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("took %v", d)
	}
	l, err := List(ctx, c)
	if err != nil || len(l) != 1 || l[0].PID != os.Getpid() {
		t.Fatalf("list: %+v %v", l, err)
	}
	// The old process, had it only stalled, learns it lost the name, and
	// its Close leaves the new entry alone.
	if err := prev.Update(ctx, old); !errors.Is(err, ErrNameTaken) {
		t.Errorf("stale update: %v", err)
	}
	if err := prev.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if l, _ := List(ctx, c); len(l) != 1 {
		t.Errorf("stale close removed the entry: %+v", l)
	}
	if err := r.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateRewritesLostEntry(t *testing.T) {
	c := testutil.Redis(t)
	ctx := context.Background()
	r, err := Register(ctx, c, info("judge"), 0)
	if err != nil {
		t.Fatal(err)
	}
	// Valkey lost the keys (restart without persistence).
	if err := c.Redis().Del(ctx, entryKey(c, "judge"), c.Key(indexKey)).Err(); err != nil {
		t.Fatal(err)
	}
	if l, _ := List(ctx, c); len(l) != 0 {
		t.Fatalf("list: %+v", l)
	}
	if err := r.Update(ctx, info("judge")); err != nil {
		t.Fatal(err)
	}
	if l, _ := List(ctx, c); len(l) != 1 {
		t.Fatalf("list after update: %+v", l)
	}
}

func TestListForgetsExpired(t *testing.T) {
	c := testutil.Redis(t)
	ctx := context.Background()
	for _, n := range []string{"a", "b"} {
		if _, err := Register(ctx, c, info(n), 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Redis().Del(ctx, entryKey(c, "a")).Err(); err != nil {
		t.Fatal(err)
	}
	l, err := List(ctx, c)
	if err != nil || len(l) != 1 || l[0].Name != "b" {
		t.Fatalf("list: %+v %v", l, err)
	}
	if n, _ := c.Redis().ZCard(ctx, c.Key(indexKey)).Result(); n != 1 {
		t.Errorf("index has %d names", n)
	}
}
