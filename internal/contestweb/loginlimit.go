// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package contestweb

// Limits on password checks (new; CMS has none). Three layers, in this
// order, so that no layer can be spent by requests another one refuses:
//
//  1. loginByIP, per client address (an IPv6 address counts by its /64,
//     which one client usually owns whole): generous, since a whole
//     contest hall may share one NAT address.
//  2. loginByUser, per contest and user name, keyed by a hash (the name
//     is the client's text, of any length): 10 attempts, then one every
//     6 s. Only failed attempts count (a right password gives its token
//     back). Wrong guesses still lock a user name out for a while, which
//     is the point, but only guesses the address limit let through. The
//     registration of an existing user, which checks their password,
//     shares these tokens.
//  3. bcryptGate, the CPU: one bcrypt check per core at a time (≈ 250 ms
//     each at cost 12), the waiting ones served in turn by address, so
//     that one address queueing many checks delays its own requests and
//     not those of others; a bounded queue answers 429 at once when full.

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"net/netip"
	"slices"
	"sync"
	"time"
)

// Bounds of the bcrypt queue: waiting checks per address and in total.
const (
	bcryptQueuePerAddr = 128
	bcryptQueueTotal   = 2048
	// bcryptBusyRetry is the Retry-After of a 429 for a full queue.
	bcryptBusyRetry = 5 * time.Second
)

// errBcryptBusy: the queue of password checks is full.
var errBcryptBusy = errors.New("too many password checks waiting")

// addrKey is the key of a client address in the address limits: IPv6
// addresses by their /64.
func addrKey(a netip.Addr) string {
	if a.Is6() {
		return netip.PrefixFrom(a, 64).Masked().String()
	}
	return a.String()
}

// userKey is the key of a user name of a contest in loginByUser: a hash,
// so a long name does not stay in memory.
func userKey(contest, username string) string {
	h := sha256.New()
	h.Write(binary.BigEndian.AppendUint64(nil, uint64(len(contest))))
	h.Write([]byte(contest))
	h.Write([]byte(username))
	return string(h.Sum(nil))
}

// clip shortens a client's text for the logs.
func clip(s string) string {
	const n = 64
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// takeLoginTokens applies the address limit and then the user limit; it
// returns the user key (to refund when the password was right or could
// not be checked), or how long to wait.
func (s *Server) takeLoginTokens(contest, username string, addr netip.Addr) (string, time.Duration, bool) {
	if ok, wait := s.loginByIP.Allow(addrKey(addr)); !ok {
		s.log.Warn("too many login attempts from the address", "contest", contest, "username", clip(username), "ip", addr.String())
		return "", wait, false
	}
	k := userKey(contest, username)
	if ok, wait := s.loginByUser.Allow(k); !ok {
		s.log.Warn("too many login attempts for the user", "contest", contest, "username", clip(username), "ip", addr.String())
		return "", wait, false
	}
	return k, 0, true
}

// bcryptGate runs at most a number of bcrypt computations at a time; the
// waiting ones are served one address after another.
type bcryptGate struct {
	mu   sync.Mutex
	free int
	// queues holds the waiting checks of each address, order the
	// addresses with waiting checks, turn the next one served.
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

func newBcryptGate(slots, maxPerKey, maxTotal int) *bcryptGate {
	return &bcryptGate{free: max(1, slots), queues: map[string][]*gateWaiter{},
		maxPerKey: maxPerKey, maxTotal: maxTotal}
}

// acquire takes a slot for key, waiting for its turn; errBcryptBusy if
// the queue (of key, or in total) is full, ctx's error if it ends first.
// A nil error must be followed by release.
func (g *bcryptGate) acquire(ctx context.Context, key string) error {
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
		return errBcryptBusy
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

// release frees a slot: the next address in turn gets it.
func (g *bcryptGate) release() {
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
		g.dropKey(key) // the next address moves to this turn
	} else {
		g.queues[key] = q
		g.turn++
	}
	w.granted = true
	close(w.ch)
}

// dropKey forgets an address without waiting checks.
func (g *bcryptGate) dropKey(key string) {
	delete(g.queues, key)
	if i := slices.Index(g.order, key); i >= 0 {
		g.order = slices.Delete(g.order, i, i+1)
		if i < g.turn {
			g.turn--
		}
	}
}

// withBcrypt runs f in a bcrypt slot of the client address addr.
func (s *Server) withBcrypt(ctx context.Context, addr netip.Addr, f func()) error {
	if err := s.bcrypt.acquire(ctx, addrKey(addr)); err != nil {
		return err
	}
	defer s.bcrypt.release()
	f()
	return nil
}
