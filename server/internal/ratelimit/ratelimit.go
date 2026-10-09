// Package ratelimit decides whether a message may pass a rate limit.
//
// The Limiter interface keeps the bucket store pluggable. The "local" driver
// keeps token buckets in memory, per instance: exact for transports where a
// device stays on one instance (WebSocket, gRPC streams, REST behind an
// ingress that hashes on X-Quack-Device), approximate otherwise. A shared
// driver (e.g. Valkey) can be added behind the same interface. See
// docs/refactor/DEVICE_ADAPTERS_PLAN.md §3.3.
package ratelimit

import (
	"fmt"
	"math"
	"sync"
	"time"
)

// Limit is a token bucket: Rate tokens per second, up to Burst tokens.
type Limit struct {
	Rate  float64
	Burst int
}

// Unlimited reports whether the limit lets everything through.
func (l Limit) Unlimited() bool { return l.Rate <= 0 }

func (l Limit) burst() float64 {
	if l.Burst >= 1 {
		return float64(l.Burst)
	}
	return math.Max(1, l.Rate)
}

type Limiter interface {
	// Allow takes n tokens from the bucket of key. When they aren't there it
	// takes nothing and returns how long until they will be.
	Allow(key string, l Limit, n int) (ok bool, retryAfter time.Duration)
}

// Open returns the limiter for a driver name.
func Open(driver string) (Limiter, error) {
	switch driver {
	case "", "local":
		return NewLocal(), nil
	default:
		return nil, fmt.Errorf("ratelimit: unknown driver %q (supported: local)", driver)
	}
}

// Local keeps buckets in memory. Idle buckets are evicted, so memory follows
// the set of recently active keys.
type Local struct {
	shards [64]shard
	now    func() time.Time
}

type shard struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	sweptAt time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// idleAfter is when an untouched bucket is surely full again and can go.
const idleAfter = 10 * time.Minute

func NewLocal() *Local {
	l := &Local{now: time.Now}
	for i := range l.shards {
		l.shards[i].buckets = map[string]*bucket{}
	}
	return l
}

func (l *Local) shard(key string) *shard {
	// FNV-1a
	h := uint32(2166136261)
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= 16777619
	}
	return &l.shards[h%uint32(len(l.shards))]
}

func (l *Local) Allow(key string, lim Limit, n int) (bool, time.Duration) {
	if lim.Unlimited() {
		return true, 0
	}
	now := l.now()
	burst := lim.burst()
	s := l.shard(key)
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.buckets[key]
	if !ok {
		b = &bucket{tokens: burst, last: now}
		s.buckets[key] = b
		s.sweepLocked(now)
	}
	b.tokens = math.Min(burst, b.tokens+now.Sub(b.last).Seconds()*lim.Rate)
	b.last = now
	need := float64(n)
	if b.tokens >= need {
		b.tokens -= need
		return true, 0
	}
	if need > burst { // can never pass in one go
		return false, time.Duration(float64(time.Second) * burst / lim.Rate)
	}
	wait := (need - b.tokens) / lim.Rate
	return false, time.Duration(math.Ceil(wait * float64(time.Second)))
}

func (s *shard) sweepLocked(now time.Time) {
	if now.Sub(s.sweptAt) < time.Minute {
		return
	}
	s.sweptAt = now
	for k, b := range s.buckets {
		if now.Sub(b.last) > idleAfter {
			delete(s.buckets, k)
		}
	}
}

// Len returns the number of live buckets (tests, metrics).
func (l *Local) Len() int {
	n := 0
	for i := range l.shards {
		l.shards[i].mu.Lock()
		n += len(l.shards[i].buckets)
		l.shards[i].mu.Unlock()
	}
	return n
}
