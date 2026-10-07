// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package kv_test

import (
	"context"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/kv"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
)

func TestKeyPrefixAndIsolation(t *testing.T) {
	a, b := testutil.Redis(t), testutil.Redis(t)
	ctx := context.Background()
	if !strings.HasPrefix(a.Key("jobs", "eval"), a.Prefix()) || a.Key("jobs", "eval") != a.Prefix()+"jobs:eval" {
		t.Fatalf("key %q", a.Key("jobs", "eval"))
	}
	if err := a.Redis().Set(ctx, a.Key("x"), "1", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if n, err := b.Redis().Exists(ctx, b.Key("x")).Result(); err != nil || n != 0 {
		t.Errorf("b sees a's key: n=%d err=%v", n, err)
	}
	// Streams (job queues) are available on the server in use.
	if err := a.Redis().XAdd(ctx, &redis.XAddArgs{Stream: a.Key("s"), Values: []string{"k", "v"}}).Err(); err != nil {
		t.Fatalf("XADD: %v", err)
	}
	if err := a.DeletePrefix(ctx); err != nil {
		t.Fatal(err)
	}
	if n, _ := a.Redis().Exists(ctx, a.Key("x"), a.Key("s")).Result(); n != 0 {
		t.Errorf("%d keys survived DeletePrefix", n)
	}
}

func TestDeletePrefixRefusesEmptyPrefix(t *testing.T) {
	c := kv.New(nil, "")
	if err := c.DeletePrefix(context.Background()); err == nil {
		t.Fatal("deleting with an empty prefix must be refused")
	}
}
