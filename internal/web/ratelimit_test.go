// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package web

import (
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestRateLimiter(t *testing.T) {
	l := NewRateLimiter(10*time.Second, 3, 100)
	now := time.Unix(0, 0)
	l.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("1.2.3.4"); !ok {
			t.Fatalf("burst %d denied", i)
		}
	}
	ok, wait := l.Allow("1.2.3.4")
	if ok || wait != 10*time.Second {
		t.Fatalf("4th: %v %v", ok, wait)
	}
	if ok, _ := l.Allow("5.6.7.8"); !ok {
		t.Error("other key denied")
	}
	now = now.Add(5 * time.Second)
	if ok, wait := l.Allow("1.2.3.4"); ok || wait != 5*time.Second {
		t.Errorf("half refilled: %v %v", ok, wait)
	}
	now = now.Add(5 * time.Second)
	if ok, _ := l.Allow("1.2.3.4"); !ok {
		t.Error("refilled token denied")
	}
	// Idle keys refill up to the burst, not beyond.
	now = now.Add(time.Hour)
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("1.2.3.4"); !ok {
			t.Fatalf("after idle, %d denied", i)
		}
	}
	if ok, _ := l.Allow("1.2.3.4"); ok {
		t.Error("burst exceeded after idle")
	}
}

func TestRateLimiterBoundedMemory(t *testing.T) {
	l := NewRateLimiter(time.Minute, 1, 160)
	for i := 0; i < 10000; i++ {
		l.Allow(fmt.Sprintf("k%d", i))
	}
	if n := l.Len(); n > 160 {
		t.Errorf("%d keys kept", n)
	}
	// The most recent key is still limited.
	if ok, _ := l.Allow("k9999"); ok {
		t.Error("recent key forgotten")
	}
}

func TestRateLimiterConcurrent(t *testing.T) {
	l := NewRateLimiter(time.Hour, 50, 1000)
	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if ok, _ := l.Allow("same"); ok {
					mu.Lock()
					allowed++
					mu.Unlock()
				}
				l.Allow(fmt.Sprintf("k%d", i))
			}
		}()
	}
	wg.Wait()
	if allowed != 50 {
		t.Errorf("allowed %d, want the burst of 50", allowed)
	}
}

func TestTooManyRequests(t *testing.T) {
	rec := httptest.NewRecorder()
	TooManyRequests(rec, 1500*time.Millisecond)
	if rec.Code != 429 || rec.Header().Get("Retry-After") != "2" {
		t.Errorf("%d %q", rec.Code, rec.Header().Get("Retry-After"))
	}
}
