// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Package registry is the list of running workers in Valkey: each worker
// registers itself (Register) and refreshes its entry every few seconds
// with what it is doing; the judges page and the monitor read the list
// (List). It replaces the worker list of CMS's cms.toml and the status
// that AdminWebServer asked EvaluationService for: workers come and go
// without any configuration (D1).
//
// Keys, under the installation prefix:
//
//	worker:<name>  hash {instance, info}: the entry, which expires TTL
//	               after the last refresh
//	workers        sorted set of names (score: last refresh, Unix ms),
//	               the index List reads
//
// A name belongs to one running process at a time (instance, a random
// id): the queue consumers of a worker are named after it, and two
// processes using the same consumer names would take each other's jobs.
// A worker that finds its name held waits for the entry to expire (the
// previous process crashed) or takes it over at once when the holder is a
// process of the same host that no longer exists (a restart).
package registry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/job"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/kv"
)

// Interval is how often a worker refreshes its entry and TTL how long the
// entry outlives the last refresh: a worker missing for TTL is gone.
const (
	Interval = 5 * time.Second
	TTL      = 3 * Interval
)

// ErrNameTaken: another running worker has the name.
var ErrNameTaken = errors.New("registry: another running worker has this name")

// Info describes a running worker.
type Info struct {
	Name string `json:"name"`
	// Host and PID identify the process.
	Host string `json:"host"`
	PID  int    `json:"pid"`
	// Version is the version of the cms binary (buildinfo.String).
	Version string `json:"version"`
	// Concurrency is the number of jobs it runs in parallel and Boxes the
	// number of sandbox boxes they share.
	Concurrency int `json:"concurrency"`
	Boxes       int `json:"boxes"`
	// Languages are the names of the languages it can compile.
	Languages []string  `json:"languages,omitempty"`
	StartedAt time.Time `json:"started_at"`
	// Heartbeat is when the entry was last written (the worker's clock).
	Heartbeat time.Time `json:"heartbeat"`
	// Draining: the worker is stopping and takes no new jobs.
	Draining bool `json:"draining,omitempty"`
	// Error says why the worker takes no jobs (its sandbox does not
	// work).
	Error string `json:"error,omitempty"`
	// Jobs are the jobs running now, by slot.
	Jobs []Job `json:"jobs"`
	// Done counts the jobs it finished with a result, Failed those whose
	// result was a failure (success false), since it started.
	Done   int64 `json:"done"`
	Failed int64 `json:"failed"`
}

// Job is a running job.
type Job struct {
	// Slot is the index of the parallel job slot running it.
	Slot      int           `json:"slot"`
	ID        string        `json:"id"`
	Operation job.Operation `json:"operation"`
	// Description is upstream's Job.info ("evaluate submission 12 on
	// testcase tc01 (dataset 3)").
	Description string    `json:"description"`
	Attempt     int       `json:"attempt"`
	StartedAt   time.Time `json:"started_at"`
}

const indexKey = "workers"

func entryKey(c *kv.Client, name string) string { return c.Key("worker", name) }

// acquireScript writes the entry of ARGV[1] unless another instance holds
// it. KEYS: entry, index. ARGV: instance, instance that may be replaced
// ("" none), info, ttl ms, now ms, name. Returns nil on success, else the
// instance holding the name.
var acquireScript = redis.NewScript(`
local cur = redis.call('HGET', KEYS[1], 'instance')
if cur and cur ~= ARGV[1] and cur ~= ARGV[2] then
  return cur
end
redis.call('HSET', KEYS[1], 'instance', ARGV[1], 'info', ARGV[3])
redis.call('PEXPIRE', KEYS[1], ARGV[4])
redis.call('ZADD', KEYS[2], ARGV[5], ARGV[6])
return false
`)

// releaseScript removes the entry if ARGV[1] holds it. KEYS: entry,
// index. ARGV: instance, name.
var releaseScript = redis.NewScript(`
if redis.call('HGET', KEYS[1], 'instance') == ARGV[1] then
  redis.call('DEL', KEYS[1])
  redis.call('ZREM', KEYS[2], ARGV[2])
  return 1
end
return 0
`)

// Registration is the entry of a running worker.
type Registration struct {
	kv       *kv.Client
	name     string
	instance string
}

// Name returns the registered name.
func (r *Registration) Name() string { return r.name }

// Register writes info as the entry of info.Name. If another process holds
// the name, it takes it over when that process is gone (same host, PID no
// longer running) and otherwise waits up to wait for the entry to expire;
// it fails with ErrNameTaken if the name is still held after that.
func Register(ctx context.Context, c *kv.Client, info Info, wait time.Duration) (*Registration, error) {
	if !config.ValidWorkerName(info.Name) {
		return nil, fmt.Errorf("registry: invalid worker name %q", info.Name)
	}
	r := &Registration{kv: c, name: info.Name, instance: newInstance()}
	deadline := time.Now().Add(wait)
	takeover := ""
	for {
		holder, err := r.write(ctx, info, takeover)
		if err != nil {
			return nil, err
		}
		if holder == "" {
			return r, nil
		}
		other, err := r.holderInfo(ctx)
		if err != nil {
			return nil, err
		}
		if other != nil && takeover != holder && gone(other) {
			takeover = holder
			continue
		}
		if time.Now().After(deadline) {
			if other != nil {
				return nil, fmt.Errorf("%w: %q runs on %s (pid %d) since %s; give each worker its own worker.name",
					ErrNameTaken, info.Name, other.Host, other.PID, other.StartedAt.Format(time.RFC3339))
			}
			return nil, fmt.Errorf("%w: %q", ErrNameTaken, info.Name)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(min(time.Second, max(time.Until(deadline), time.Millisecond))):
		}
	}
}

// Update refreshes the entry with info (whose Name must not change). It
// returns ErrNameTaken if another process took the name meanwhile (this
// one stalled for longer than TTL): the worker must stop. If the entry
// expired or Valkey lost it, it is written again.
func (r *Registration) Update(ctx context.Context, info Info) error {
	info.Name = r.name
	holder, err := r.write(ctx, info, "")
	if err != nil {
		return err
	}
	if holder != "" {
		return fmt.Errorf("%w: %q was taken by another process", ErrNameTaken, r.name)
	}
	return nil
}

// Close removes the entry (clean shutdown), unless another process holds
// the name.
func (r *Registration) Close(ctx context.Context) error {
	err := releaseScript.Run(ctx, r.kv.Redis(), []string{entryKey(r.kv, r.name), r.kv.Key(indexKey)},
		r.instance, r.name).Err()
	if err != nil {
		return fmt.Errorf("registry: removing %q: %w", r.name, err)
	}
	return nil
}

// write runs acquireScript; it returns the instance holding the name, or
// "" when the entry was written.
func (r *Registration) write(ctx context.Context, info Info, takeover string) (string, error) {
	now := time.Now()
	info.Heartbeat = now
	if info.Jobs == nil {
		info.Jobs = []Job{}
	}
	b, err := json.Marshal(&info)
	if err != nil {
		return "", err
	}
	v, err := acquireScript.Run(ctx, r.kv.Redis(), []string{entryKey(r.kv, r.name), r.kv.Key(indexKey)},
		r.instance, takeover, b, TTL.Milliseconds(), now.UnixMilli(), r.name).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("registry: writing %q: %w", r.name, err)
	}
	s, _ := v.(string)
	if s == "" {
		return "", fmt.Errorf("registry: writing %q: unexpected reply %v", r.name, v)
	}
	return s, nil
}

// holderInfo reads the entry of the name (nil if it is gone).
func (r *Registration) holderInfo(ctx context.Context) (*Info, error) {
	s, err := r.kv.Redis().HGet(ctx, entryKey(r.kv, r.name), "info").Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("registry: reading %q: %w", r.name, err)
	}
	var info Info
	if err := json.Unmarshal([]byte(s), &info); err != nil {
		return nil, nil // unreadable: wait for it to expire
	}
	return &info, nil
}

// List returns the running workers, sorted by name, and forgets the
// workers whose entry expired.
func List(ctx context.Context, c *kv.Client) ([]Info, error) {
	rdb := c.Redis()
	names, err := rdb.ZRange(ctx, c.Key(indexKey), 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("registry: listing workers: %w", err)
	}
	if len(names) == 0 {
		return nil, nil
	}
	pipe := rdb.Pipeline()
	cmds := make([]*redis.StringCmd, len(names))
	for i, n := range names {
		cmds[i] = pipe.HGet(ctx, entryKey(c, n), "info")
	}
	_, _ = pipe.Exec(ctx)
	var (
		out  []Info
		gone []any
	)
	for i, cmd := range cmds {
		s, err := cmd.Result()
		if errors.Is(err, redis.Nil) {
			gone = append(gone, names[i])
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("registry: reading %q: %w", names[i], err)
		}
		var info Info
		if err := json.Unmarshal([]byte(s), &info); err != nil {
			continue
		}
		out = append(out, info)
	}
	if len(gone) > 0 {
		// Best effort: a worker re-registering meanwhile adds itself again
		// at its next refresh.
		_ = rdb.ZRem(ctx, c.Key(indexKey), gone...).Err()
	}
	slices.SortFunc(out, func(a, b Info) int {
		switch {
		case a.Name < b.Name:
			return -1
		case a.Name > b.Name:
			return 1
		}
		return 0
	})
	return out, nil
}

func newInstance() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// localHost and ownPID identify this process (variables for the tests).
var (
	localHost = func() string { h, _ := os.Hostname(); return h }
	ownPID    = os.Getpid
)

// gone reports whether the worker described by other is a process of this
// host that no longer runs. A process with this process's PID is not
// considered gone: in a restarted container the old and the new process
// may both be PID 1, but so may two containers given the same host name.
func gone(other *Info) bool {
	if other.Host == "" || other.Host != localHost() || other.PID <= 0 || other.PID == ownPID() {
		return false
	}
	return errors.Is(syscall.Kill(other.PID, 0), syscall.ESRCH)
}
