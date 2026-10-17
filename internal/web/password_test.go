// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package web

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// TestPasswords checks CMS's authentication strings: bcrypt (with
// Python's 72-byte limit) and plaintext, and the errors for unknown
// methods.
func TestPasswords(t *testing.T) {
	old := PasswordCost
	PasswordCost = bcrypt.MinCost
	defer func() { PasswordCost = old }()
	long := strings.Repeat("x", 72)
	for _, method := range []string{"", "bcrypt", "plaintext"} {
		auth, err := HashPassword(long+"tail", method)
		if err != nil {
			t.Fatal(err)
		}
		want := "bcrypt:"
		if method == "plaintext" {
			want = "plaintext:"
		}
		if !strings.HasPrefix(auth, want) {
			t.Fatalf("%q: %s", method, auth)
		}
		ok, err := ValidatePassword(auth, long+"tail")
		if err != nil || !ok {
			t.Errorf("%q: right password refused: %v", method, err)
		}
		ok, _ = ValidatePassword(auth, "wrong")
		if ok {
			t.Errorf("%q: wrong password accepted", method)
		}
		ok, _ = ValidatePassword(auth, long+"other")
		if ok != (method != "plaintext") {
			t.Errorf("%q: the 72-byte limit of bcrypt", method)
		}
	}
	for _, bad := range []string{"nocolon", "md5:abc"} {
		if _, err := ValidatePassword(bad, "x"); !errors.Is(err, ErrBadAuthentication) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if _, err := HashPassword("x", "md5"); !errors.Is(err, ErrBadAuthentication) {
		t.Errorf("unknown method: %v", err)
	}
	if m, p, err := ParseAuthentication("plaintext:a:b"); err != nil || m != "plaintext" || p != "a:b" {
		t.Errorf("parse: %q %q %v", m, p, err)
	}
}

// TestBcryptGate checks the gate: at most slots at a time, the waiting
// ones served by turns of key, bounded queues.
func TestBcryptGate(t *testing.T) {
	g := NewBcryptGate(1, 2, 3)
	ctx := context.Background()
	release := make(chan struct{})
	started := make(chan struct{})
	go func() {
		_ = g.Do(ctx, "a", func() { close(started); <-release })
	}()
	<-started
	var mu sync.Mutex
	var order []string
	var wg sync.WaitGroup
	run := func(key string) {
		defer wg.Done()
		_ = g.Do(ctx, key, func() {
			mu.Lock()
			order = append(order, key)
			mu.Unlock()
		})
	}
	for _, k := range []string{"a", "a", "b"} {
		wg.Add(1)
		go run(k)
		time.Sleep(20 * time.Millisecond)
	}
	if err := g.Do(ctx, "a", func() {}); !errors.Is(err, ErrBcryptBusy) {
		t.Errorf("full queue of a key: %v", err)
	}
	if err := g.Do(ctx, "c", func() {}); !errors.Is(err, ErrBcryptBusy) {
		t.Errorf("full queue: %v", err)
	}
	cctx, cancel := context.WithTimeout(ctx, time.Millisecond)
	defer cancel()
	close(release)
	wg.Wait()
	if strings.Join(order, ",") != "a,b,a" {
		t.Errorf("order %v, want turns by key", order)
	}
	if err := g.Do(cctx, "d", func() {}); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("after: %v", err)
	}
	if AddrKey(netip.MustParseAddr("2001:db8::1")) != "2001:db8::/64" || AddrKey(netip.MustParseAddr("192.0.2.1")) != "192.0.2.1" {
		t.Error("AddrKey")
	}
}
