// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package contestweb

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/web"
)

// exhaustAddr spends the login tokens of an address (with a limit that
// does not refill during the test).
func exhaustAddr(ts *testSite, ip string) {
	ts.S.loginByIP = web.NewRateLimiter(time.Hour, 1, 0)
	ts.S.loginByIP.Allow(addrKey(netip.MustParseAddr(ip)))
}

// TestLoginLimitAddressFirst: attempts the address limit refuses touch no
// per-user bucket (they used to spend the victim's tokens and keep one
// key per user name, of any length, in memory).
func TestLoginLimitAddressFirst(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{Configure: func(c *config.Config) { c.ContestWeb.NumProxies = 1 }})
	attacker := ts.Client()
	attacker.Header.Set("X-Forwarded-For", "10.9.9.9")
	exhaustAddr(ts, "10.9.9.9")
	for i := 0; i < 10; i++ {
		r := attacker.Post("/demo/login", url.Values{"username": {"alice"}, "password": {"x"}})
		if r.Status != http.StatusTooManyRequests {
			t.Fatalf("attacker %d: %d", i, r.Status)
		}
		r = attacker.Post("/demo/api/login", url.Values{"username": {strings.Repeat("u", 100<<10) + string(rune('a'+i))}, "password": {"x"}})
		if r.Status != http.StatusTooManyRequests {
			t.Fatalf("API attacker %d: %d", i, r.Status)
		}
	}
	if n := ts.S.loginByUser.Len(); n != 0 {
		t.Errorf("refused attempts left %d per-user buckets", n)
	}
	victim := ts.Client()
	victim.Header.Set("X-Forwarded-For", "10.0.0.1")
	victim.Login("alice", "demo")
}

// TestLoginLimitKeys: user names are hashed, IPv6 addresses count by
// their /64.
func TestLoginLimitKeys(t *testing.T) {
	if k := userKey("demo", strings.Repeat("a", 1<<20)); len(k) != 32 {
		t.Errorf("user key of %d bytes", len(k))
	}
	if userKey("demo", "a\x00b") == userKey("demo\x00a", "b") {
		t.Error("ambiguous user keys")
	}
	a := addrKey(netip.MustParseAddr("2001:db8:1:2:aaaa::1"))
	b := addrKey(netip.MustParseAddr("2001:db8:1:2:bbbb::2"))
	c := addrKey(netip.MustParseAddr("2001:db8:1:3::1"))
	if a != b || a == c {
		t.Errorf("IPv6 keys: %q %q %q", a, b, c)
	}
	if k := addrKey(netip.MustParseAddr("10.0.0.1")); k != "10.0.0.1" {
		t.Errorf("IPv4 key %q", k)
	}
}

// TestLoginLimitCountsFailuresOnly: right passwords give their token
// back, so a participant logging in often (several devices, logouts) is
// never refused.
func TestLoginLimitCountsFailuresOnly(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{})
	c := ts.Client()
	for i := 0; i < 25; i++ {
		c.Login("alice", "demo")
	}
	for i := 0; i < 25; i++ {
		r := c.Post("/demo/api/login", url.Values{"username": {"bob"}, "password": {"demo"}})
		if r.Status != http.StatusOK && r.Status != http.StatusBadRequest {
			t.Fatalf("API login %d: %d %s", i, r.Status, r.Body)
		}
		c.Post("/demo/logout", nil)
	}
}

// TestRegisterSharesUserLimit: registering as an existing user checks
// their password, so it spends the per-user tokens of the logins (it was
// a password oracle limited only per address).
func TestRegisterSharesUserLimit(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{})
	ts.Exec(`UPDATE contests SET allow_registration = true`)
	c := ts.Client()
	for i := 0; i < 10; i++ {
		r := c.Post("/demo/register", url.Values{"new_user": {"false"}, "username": {"alice"}, "password": {"guess"}})
		if r.Status != http.StatusForbidden {
			t.Fatalf("guess %d: %d", i, r.Status)
		}
	}
	r := c.Post("/demo/register", url.Values{"new_user": {"false"}, "username": {"alice"}, "password": {"demo"}})
	if r.Status != http.StatusTooManyRequests || r.Header.Get("Retry-After") == "" {
		t.Fatalf("11th guess: %d (Retry-After %q)", r.Status, r.Header.Get("Retry-After"))
	}
	if r := c.Post("/demo/login", url.Values{"username": {"alice"}, "password": {"demo"}}); r.Status != http.StatusTooManyRequests {
		t.Fatalf("login after the guesses: %d", r.Status)
	}
}

// TestLoginBcryptBusy: when the queue of bcrypt checks is full the login
// answers 429 at once (instead of waiting behind it), and the attempt
// does not count against the user.
func TestLoginBcryptBusy(t *testing.T) {
	t.Parallel()
	ts := newTestSite(t, testOptions{})
	hashed, err := HashPassword("secret")
	if err != nil {
		t.Fatal(err)
	}
	ts.Exec(`UPDATE participations SET password = $1 WHERE id = $2`, hashed, ts.Participation("alice"))
	ts.S.bcrypt = newBcryptGate(1, 0, 0) // nobody may wait
	if err := ts.S.bcrypt.acquire(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
	c := ts.Client()
	for i := 0; i < 12; i++ {
		r := c.Post("/demo/login", url.Values{"username": {"alice"}, "password": {"secret"}})
		if r.Status != http.StatusTooManyRequests || r.Header.Get("Retry-After") != "5" {
			t.Fatalf("busy login %d: %d (Retry-After %q)", i, r.Status, r.Header.Get("Retry-After"))
		}
	}
	r := c.Post("/demo/api/login", url.Values{"username": {"alice"}, "password": {"secret"}})
	if r.Status != http.StatusTooManyRequests || r.Body != `{"error":"Too many login attempts"}` {
		t.Fatalf("busy API login: %d %s", r.Status, r.Body)
	}
	ts.S.bcrypt.release()
	c.Login("alice", "secret")
}

// TestBcryptGateFair: the waiting checks are served one address after
// another, so an address with a long queue delays its own requests only.
func TestBcryptGateFair(t *testing.T) {
	g := newBcryptGate(1, 100, 1000)
	ctx := context.Background()
	if err := g.acquire(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	served := make(chan string, 100)
	wait := func(key string) {
		before := waiting(g)
		go func() {
			if err := g.acquire(ctx, key); err != nil {
				t.Error(err)
				return
			}
			served <- key
		}()
		// Queue in a known order.
		deadline := time.Now().Add(5 * time.Second)
		for waiting(g) == before {
			if time.Now().After(deadline) {
				t.Fatal("waiter not queued")
			}
			time.Sleep(time.Millisecond)
		}
	}
	for i := 0; i < 20; i++ {
		wait("attacker")
	}
	wait("victim")
	var order []string
	for i := 0; i < 21; i++ {
		g.release()
		order = append(order, <-served)
	}
	if order[0] != "attacker" || order[1] != "victim" {
		t.Errorf("served %v: the victim waited behind the whole queue", order[:3])
	}
	g.release()
	if g.free != 1 || g.waiting != 0 || len(g.order) != 0 || len(g.queues) != 0 {
		t.Errorf("state after the queue: free %d waiting %d order %v", g.free, g.waiting, g.order)
	}
}

func waiting(g *bcryptGate) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.waiting
}

// TestBcryptGateBounds: full queues answer errBcryptBusy, and a waiter
// whose request ends leaves the queue.
func TestBcryptGateBounds(t *testing.T) {
	g := newBcryptGate(1, 2, 3)
	bg := context.Background()
	if err := g.acquire(bg, "a"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(bg)
	errs := make(chan error, 4)
	for _, k := range []string{"a", "a", "b"} {
		go func() { errs <- g.acquire(ctx, k) }()
	}
	for waiting(g) < 3 {
		time.Sleep(time.Millisecond)
	}
	if err := g.acquire(bg, "a"); !errors.Is(err, errBcryptBusy) {
		t.Errorf("third waiter of an address: %v", err)
	}
	if err := g.acquire(bg, "c"); !errors.Is(err, errBcryptBusy) {
		t.Errorf("waiter over the total: %v", err)
	}
	cancel()
	for i := 0; i < 3; i++ {
		if err := <-errs; !errors.Is(err, context.Canceled) {
			t.Errorf("cancelled waiter: %v", err)
		}
	}
	if g.waiting != 0 || len(g.order) != 0 || len(g.queues) != 0 {
		t.Errorf("cancelled waiters left: waiting %d order %v", g.waiting, g.order)
	}
	g.release()
	if g.free != 1 {
		t.Errorf("free %d", g.free)
	}
}
