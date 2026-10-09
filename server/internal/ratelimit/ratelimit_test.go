package ratelimit

import (
	"sync"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestLocal() (*Local, *clock) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	l := NewLocal()
	l.now = c.now
	return l, c
}

func TestBurstThenRefill(t *testing.T) {
	l, c := newTestLocal()
	lim := Limit{Rate: 10, Burst: 5}
	for i := range 5 {
		if ok, _ := l.Allow("e", lim, 1); !ok {
			t.Fatalf("message %d within burst was refused", i)
		}
	}
	ok, wait := l.Allow("e", lim, 1)
	if ok || wait != 100*time.Millisecond {
		t.Fatalf("6th message: ok=%v wait=%v, want refused with 100ms", ok, wait)
	}
	c.advance(100 * time.Millisecond)
	if ok, _ := l.Allow("e", lim, 1); !ok {
		t.Fatal("token not refilled after 1/rate")
	}
}

func TestDefaultBurstIsRate(t *testing.T) {
	l, _ := newTestLocal()
	lim := Limit{Rate: 3}
	n := 0
	for range 10 {
		if ok, _ := l.Allow("e", lim, 1); ok {
			n++
		}
	}
	if n != 3 {
		t.Fatalf("passed %d, want burst = rate = 3", n)
	}
	// sub-1 rates still allow one message
	if ok, _ := l.Allow("slow", Limit{Rate: 0.1}, 1); !ok {
		t.Fatal("rate 0.1 must allow the first message")
	}
}

func TestKeysAreIndependentAndUnlimited(t *testing.T) {
	l, _ := newTestLocal()
	lim := Limit{Rate: 1}
	if ok, _ := l.Allow("a", lim, 1); !ok {
		t.Fatal("a")
	}
	if ok, _ := l.Allow("b", lim, 1); !ok {
		t.Fatal("b must not share a's bucket")
	}
	for range 1000 {
		if ok, _ := l.Allow("u", Limit{}, 1); !ok {
			t.Fatal("zero limit means unlimited")
		}
	}
}

func TestBatchTakesNOrNothing(t *testing.T) {
	l, _ := newTestLocal()
	lim := Limit{Rate: 10, Burst: 10}
	if ok, _ := l.Allow("e", lim, 8); !ok {
		t.Fatal("8 of 10")
	}
	if ok, wait := l.Allow("e", lim, 5); ok || wait != 300*time.Millisecond {
		t.Fatalf("5 more with 2 left: ok=%v wait=%v", ok, wait)
	}
	if ok, _ := l.Allow("e", lim, 2); !ok {
		t.Fatal("a refused batch must not consume tokens")
	}
}

func TestIdleBucketsAreEvicted(t *testing.T) {
	l, c := newTestLocal()
	for _, k := range []string{"a", "b", "c"} {
		l.Allow(k, Limit{Rate: 1}, 1)
	}
	c.advance(idleAfter + 2*time.Minute)
	for i := range len(l.shards) * 4 { // touch every shard so each sweeps
		l.Allow(string(rune('A'+i%26))+string(rune('a'+i/26)), Limit{Rate: 1}, 1)
	}
	if n := l.Len(); n > len(l.shards)*4 {
		t.Fatalf("%d buckets, idle ones not evicted", n)
	}
	l.shard("a").mu.Lock()
	_, ok := l.shard("a").buckets["a"]
	l.shard("a").mu.Unlock()
	if ok {
		t.Fatal("idle bucket a still present")
	}
}

func TestConcurrentAllowIsExact(t *testing.T) {
	l := NewLocal() // real clock: rate tiny so refill is negligible
	lim := Limit{Rate: 0.001, Burst: 100}
	var wg sync.WaitGroup
	var mu sync.Mutex
	passed := 0
	for range 20 {
		wg.Go(func() {
			for range 50 {
				if ok, _ := l.Allow("e", lim, 1); ok {
					mu.Lock()
					passed++
					mu.Unlock()
				}
			}
		})
	}
	wg.Wait()
	if passed != 100 {
		t.Fatalf("passed %d of 1000 concurrent, want exactly the burst (100)", passed)
	}
}

func TestOpen(t *testing.T) {
	if _, err := Open("local"); err != nil {
		t.Fatal(err)
	}
	if _, err := Open("valkey"); err == nil {
		t.Fatal("unknown driver must fail")
	}
}
