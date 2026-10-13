// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package dispatcher

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/db"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/kv"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/testutil"
)

// The round trips of the dispatcher alone (the load and end-to-end tests
// report them per submission): a pool of its own on the test database
// and a Valkey client of its own under the test prefix, each counting.

// countingPool returns a new pool on the database of base that counts its
// queries (db.QueryCounter: a batch counts once).
func countingPool(t testing.TB, base *pgxpool.Pool) (*pgxpool.Pool, *db.QueryCounter) {
	t.Helper()
	cfg := base.Config()
	qc := new(db.QueryCounter)
	qc.Install(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool, qc
}

// commandCounter is a go-redis hook that counts round trips: a command
// or a pipeline (a Lua script call is one command), and how many of each
// kind (Breakdown).
type commandCounter struct {
	commands, pipelines atomic.Int64

	// prefix is the key prefix, left out of Breakdown.
	prefix string
	mu     sync.Mutex
	byKind map[string]int64
}

func (c *commandCounter) note(kind string) {
	c.mu.Lock()
	if c.byKind == nil {
		c.byKind = map[string]int64{}
	}
	c.byKind[kind]++
	c.mu.Unlock()
}

// kind names a command for Breakdown: its name and, for a script call,
// its hash and first key (which queue).
func (c *commandCounter) kind(cmd redis.Cmder) string {
	name := cmd.Name()
	args := cmd.Args()
	if (name == "evalsha" || name == "eval") && len(args) >= 4 {
		key := strings.TrimPrefix(fmt.Sprint(args[3]), c.prefix)
		return fmt.Sprintf("%s %.8s %s", name, fmt.Sprint(args[1]), key)
	}
	return name
}

// Breakdown returns the kinds of round trips, most frequent first.
func (c *commandCounter) Breakdown(limit int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	kinds := make([]string, 0, len(c.byKind))
	for k := range c.byKind {
		kinds = append(kinds, k)
	}
	slices.SortFunc(kinds, func(a, b string) int { return cmp.Compare(c.byKind[b], c.byKind[a]) })
	var out []string
	for _, k := range kinds[:min(limit, len(kinds))] {
		out = append(out, fmt.Sprintf("%s ×%d", k, c.byKind[k]))
	}
	return out
}

func (c *commandCounter) DialHook(next redis.DialHook) redis.DialHook { return next }

func (c *commandCounter) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		c.commands.Add(1)
		c.note(c.kind(cmd))
		return next(ctx, cmd)
	}
}

func (c *commandCounter) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		c.pipelines.Add(1)
		c.note(fmt.Sprintf("pipeline of %d", len(cmds)))
		return next(ctx, cmds)
	}
}

// RoundTrips returns the commands and pipelines sent so far.
func (c *commandCounter) RoundTrips() int64 { return c.commands.Load() + c.pipelines.Load() }

// countingKV returns a new Valkey client with the key prefix of base that
// counts its round trips.
func countingKV(t testing.TB, base *kv.Client) (*kv.Client, *commandCounter) {
	t.Helper()
	opt, err := redis.ParseURL(testutil.RedisURL(t))
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(opt)
	cc := &commandCounter{prefix: base.Prefix()}
	rdb.AddHook(cc)
	t.Cleanup(func() { _ = rdb.Close() })
	return kv.New(rdb, base.Prefix()), cc
}

var _ redis.Hook = (*commandCounter)(nil)
