// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package dispatcher

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/kv"
)

// The lease keys, under the installation prefix.
const (
	leaseKey      = "dispatcher:lease"
	leaseEpochKey = "dispatcher:epoch"
)

// ErrLeaseLost ends the work of a leader whose lease could not be renewed
// in time or was taken by another dispatcher.
var ErrLeaseLost = errors.New("dispatcher: the lease was lost")

// acquireScript takes the lease if nobody holds it, with a new fencing
// epoch. KEYS: lease, epoch counter. ARGV: owner, ttl ms. Returns the
// epoch, or the value of the current holder.
var acquireScript = redis.NewScript(`
if redis.call('SET', KEYS[1], ARGV[1], 'NX', 'PX', ARGV[2]) then
  local e = redis.call('INCR', KEYS[2])
  redis.call('SET', KEYS[1], ARGV[1] .. '#' .. e, 'PX', ARGV[2])
  return e
end
return redis.call('GET', KEYS[1]) or ''
`)

// renewScript extends the lease if the caller still holds it. KEYS: lease.
// ARGV: value, ttl ms. Returns 1, or 0 if it is held by someone else (or
// expired).
var renewScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  redis.call('PEXPIRE', KEYS[1], ARGV[2])
  return 1
end
return 0
`)

// releaseScript deletes the lease if the caller holds it. KEYS: lease.
// ARGV: value.
var releaseScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`)

// Lease makes one dispatcher of an installation the active one: a Valkey
// key set with SET NX PX and renewed every third of its TTL. Each
// acquisition increments a fencing epoch stored with the owner
// ("<owner>#<epoch>"), so a renewal or release by a holder that lost the
// lease never touches the lease of the next one.
//
// The holder also keeps a local deadline: the time of the last successful
// renewal request (taken before sending it) plus the TTL minus a margin.
// The key cannot expire on the server before that, so a holder that stops
// working when its deadline passes, renewed or not, stops before anyone
// else can take over (Valid; Keep cancels the work at the deadline).
type Lease struct {
	kv     *kv.Client
	key    string
	epochK string
	owner  string
	ttl    time.Duration
	margin time.Duration

	mu       sync.Mutex
	value    string // "<owner>#<epoch>" while held
	epoch    int64
	deadline time.Time
}

// NewLease returns the lease of the installation for owner (unique per
// process) with the given TTL.
func NewLease(c *kv.Client, owner string, ttl time.Duration) *Lease {
	return &Lease{
		kv: c, key: c.Key(leaseKey), epochK: c.Key(leaseEpochKey), owner: owner, ttl: ttl,
		margin: max(ttl/10, time.Millisecond),
	}
}

// TryAcquire takes the lease if it is free. It returns the holder when
// someone else has it.
func (l *Lease) TryAcquire(ctx context.Context) (acquired bool, holder string, err error) {
	sent := time.Now()
	v, err := acquireScript.Run(ctx, l.kv.Redis(), []string{l.key, l.epochK}, l.owner, l.ttl.Milliseconds()).Result()
	if err != nil {
		return false, "", fmt.Errorf("dispatcher lease: %w", err)
	}
	if e, ok := v.(int64); ok {
		l.mu.Lock()
		l.epoch, l.value, l.deadline = e, l.owner+"#"+strconv.FormatInt(e, 10), sent.Add(l.ttl-l.margin)
		l.mu.Unlock()
		return true, "", nil
	}
	s, _ := v.(string)
	return false, s, nil
}

// Renew extends the lease. It returns ErrLeaseLost if the lease is not
// ours any more; a transient error leaves the deadline unchanged.
func (l *Lease) Renew(ctx context.Context) error {
	l.mu.Lock()
	value := l.value
	l.mu.Unlock()
	if value == "" {
		return ErrLeaseLost
	}
	sent := time.Now()
	n, err := renewScript.Run(ctx, l.kv.Redis(), []string{l.key}, value, l.ttl.Milliseconds()).Int64()
	if err != nil {
		return fmt.Errorf("dispatcher lease: %w", err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if n != 1 || l.value != value {
		l.value, l.deadline = "", time.Time{}
		return ErrLeaseLost
	}
	l.deadline = sent.Add(l.ttl - l.margin)
	return nil
}

// Release gives the lease up (shutdown), so a standby takes over at once.
func (l *Lease) Release(ctx context.Context) error {
	l.mu.Lock()
	value := l.value
	l.value, l.deadline = "", time.Time{}
	l.mu.Unlock()
	if value == "" {
		return nil
	}
	return releaseScript.Run(ctx, l.kv.Redis(), []string{l.key}, value).Err()
}

// Valid reports whether the lease is held and its local deadline has not
// passed.
func (l *Lease) Valid() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.value != "" && time.Now().Before(l.deadline)
}

// Epoch returns the fencing epoch of the lease held or last held.
func (l *Lease) Epoch() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.epoch
}

// Deadline returns the local deadline (zero when not held).
func (l *Lease) Deadline() time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.deadline
}

// Keep renews the lease every third of its TTL until ctx ends, and calls
// lost (once) as soon as the lease is lost: renewal refused, or the local
// deadline passed without a successful renewal (Valkey unreachable). The
// deadline is enforced by a timer, so a renewal stuck in the network does
// not delay it.
func (l *Lease) Keep(ctx context.Context, lost func(error)) {
	var once sync.Once
	fire := func(err error) { once.Do(func() { lost(err) }) }
	timer := time.AfterFunc(time.Until(l.Deadline()), func() {
		fire(fmt.Errorf("%w: not renewed before its deadline", ErrLeaseLost))
	})
	defer timer.Stop()
	every := l.ttl / 3
	retry := max(l.ttl/20, time.Millisecond)
	wait := every
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		err := l.Renew(ctx)
		switch {
		case err == nil:
			timer.Reset(time.Until(l.Deadline()))
			wait = every
		case errors.Is(err, ErrLeaseLost):
			fire(err)
			return
		default:
			// Valkey unreachable: try again soon; the timer ends the
			// leadership if the deadline passes first.
			if ctx.Err() != nil {
				return
			}
			wait = retry
		}
	}
}
