// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package web

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"sync"
)

// ErrBcryptBusy: the queue of password checks is full (answer 429).
var ErrBcryptBusy = errors.New("too many password checks waiting")

// AddrKey is the key of a client address in per-address limits: IPv6
// addresses count by their /64, which one client usually owns whole.
func AddrKey(a netip.Addr) string {
	if a.Is6() {
		return netip.PrefixFrom(a, 64).Masked().String()
	}
	return a.String()
}

// BcryptGate runs at most a number of bcrypt computations at a time (one
// per core: ≈ 250 ms of CPU each at cost 12); the waiting ones are served
// one key (client address) after another, so that an address queueing
// many checks delays its own requests and not those of others, and a
// bounded queue refuses at once when full (D42). The contestant site has
// its own copy (internal/contestweb/loginlimit.go).
type BcryptGate struct {
	mu   sync.Mutex
	free int
	// queues holds the waiting checks of each key, order the keys with
	// waiting checks, turn the next one served.
	queues  map[string][]*gateWaiter
	order   []string
	turn    int
	waiting int

	maxPerKey, maxTotal int
}

type gateWaiter struct {
	ch      chan struct{}
	granted bool
}

// NewBcryptGate returns a gate with slots concurrent computations and at
// most maxPerKey waiting checks per key and maxTotal in all.
func NewBcryptGate(slots, maxPerKey, maxTotal int) *BcryptGate {
	return &BcryptGate{free: max(1, slots), queues: map[string][]*gateWaiter{},
		maxPerKey: maxPerKey, maxTotal: maxTotal}
}

// Do runs f in a slot of key, waiting for its turn: ErrBcryptBusy if the
// queue (of key, or in total) is full, ctx's error if it ends first.
func (g *BcryptGate) Do(ctx context.Context, key string, f func()) error {
	if err := g.acquire(ctx, key); err != nil {
		return err
	}
	defer g.release()
	f()
	return nil
}

func (g *BcryptGate) acquire(ctx context.Context, key string) error {
	g.mu.Lock()
	if g.free > 0 {
		// Free slots only exist while nobody waits.
		g.free--
		g.mu.Unlock()
		return nil
	}
	q := g.queues[key]
	if g.waiting >= g.maxTotal || len(q) >= g.maxPerKey {
		g.mu.Unlock()
		return ErrBcryptBusy
	}
	if len(q) == 0 {
		g.order = append(g.order, key)
	}
	w := &gateWaiter{ch: make(chan struct{})}
	g.queues[key] = append(q, w)
	g.waiting++
	g.mu.Unlock()

	select {
	case <-w.ch:
		return nil
	case <-ctx.Done():
	}
	g.mu.Lock()
	if w.granted {
		// Granted while giving up: pass the slot on.
		g.mu.Unlock()
		g.release()
		return ctx.Err()
	}
	q = g.queues[key]
	if i := slices.Index(q, w); i >= 0 {
		q = slices.Delete(q, i, i+1)
		g.waiting--
		if len(q) == 0 {
			g.dropKey(key)
		} else {
			g.queues[key] = q
		}
	}
	g.mu.Unlock()
	return ctx.Err()
}

// release frees a slot: the next key in turn gets it.
func (g *BcryptGate) release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.waiting == 0 {
		g.free++
		return
	}
	if g.turn >= len(g.order) {
		g.turn = 0
	}
	key := g.order[g.turn]
	q := g.queues[key]
	w := q[0]
	q[0] = nil
	q = q[1:]
	g.waiting--
	if len(q) == 0 {
		g.dropKey(key) // the next key moves to this turn
	} else {
		g.queues[key] = q
		g.turn++
	}
	w.granted = true
	close(w.ch)
}

// dropKey forgets a key without waiting checks.
func (g *BcryptGate) dropKey(key string) {
	delete(g.queues, key)
	if i := slices.Index(g.order, key); i >= 0 {
		g.order = slices.Delete(g.order, i, i+1)
		if i < g.turn {
			g.turn--
		}
	}
}
