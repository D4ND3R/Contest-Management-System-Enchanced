// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package web

import (
	"container/list"
	"hash/maphash"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// RateLimiter is a token bucket per key (client address, user name...)
// for logins and submissions. Memory is bounded: at most MaxKeys
// buckets are kept, the least recently used ones are dropped first (a
// dropped bucket starts full again, which only favours the key that was
// idle). Safe for concurrent use; the keys are spread over shards so
// requests rarely wait on each other.
type RateLimiter struct {
	rate   float64 // tokens per second
	burst  float64
	now    func() time.Time
	seed   maphash.Seed
	shards [rateShards]rateShard
}

const rateShards = 16

type rateShard struct {
	mu  sync.Mutex
	max int
	m   map[string]*list.Element
	lru *list.List // front: most recent
}

type bucket struct {
	key    string
	tokens float64
	last   time.Time
}

// NewRateLimiter allows burst events at once per key and then one every
// interval; maxKeys bounds the number of keys tracked (default 100000).
func NewRateLimiter(interval time.Duration, burst, maxKeys int) *RateLimiter {
	if burst < 1 {
		burst = 1
	}
	if maxKeys <= 0 {
		maxKeys = 100000
	}
	l := &RateLimiter{
		rate:  1 / interval.Seconds(),
		burst: float64(burst),
		now:   time.Now,
		seed:  maphash.MakeSeed(),
	}
	per := (maxKeys + rateShards - 1) / rateShards
	for i := range l.shards {
		l.shards[i] = rateShard{max: per, m: map[string]*list.Element{}, lru: list.New()}
	}
	return l
}

// Allow takes one token from the bucket of key. When the bucket is empty
// it returns false and how long until a token is available.
func (l *RateLimiter) Allow(key string) (bool, time.Duration) {
	now := l.now()
	s := &l.shards[maphash.String(l.seed, key)%rateShards]
	s.mu.Lock()
	defer s.mu.Unlock()
	var b *bucket
	if el, ok := s.m[key]; ok {
		b = el.Value.(*bucket)
		s.lru.MoveToFront(el)
		if dt := now.Sub(b.last).Seconds(); dt > 0 {
			b.tokens = math.Min(l.burst, b.tokens+dt*l.rate)
		}
		b.last = now
	} else {
		if s.lru.Len() >= s.max {
			old := s.lru.Back()
			s.lru.Remove(old)
			delete(s.m, old.Value.(*bucket).key)
		}
		b = &bucket{key: key, tokens: l.burst, last: now}
		s.m[key] = s.lru.PushFront(b)
	}
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
	return false, wait
}

// Len returns the number of keys tracked.
func (l *RateLimiter) Len() int {
	n := 0
	for i := range l.shards {
		s := &l.shards[i]
		s.mu.Lock()
		n += s.lru.Len()
		s.mu.Unlock()
	}
	return n
}

// TooManyRequests sends 429 with Retry-After (whole seconds, at least 1).
func TooManyRequests(w http.ResponseWriter, retryAfter time.Duration) {
	secs := int(math.Ceil(retryAfter.Seconds()))
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
}
