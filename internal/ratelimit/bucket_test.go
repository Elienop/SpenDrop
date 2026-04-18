package ratelimit

import (
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)}
}

func TestBucket_ConsumeUpToLimit_Then429(t *testing.T) {
	clk := newFakeClock()
	b := NewBucket(3, time.Minute, clk)
	defer b.Stop()

	for i := 0; i < 3; i++ {
		if over := b.Consume("k"); over {
			t.Fatalf("consume %d: expected under limit, got over", i)
		}
	}
	if over := b.Consume("k"); !over {
		t.Fatal("consume 4: expected over limit, got under")
	}
	if !b.Exhausted("k") {
		t.Fatal("Exhausted: expected true after 4 consumes, got false")
	}
}

func TestBucket_RollingWindow_RefillsAfterWindow(t *testing.T) {
	clk := newFakeClock()
	b := NewBucket(2, time.Minute, clk)
	defer b.Stop()

	b.Consume("k")
	b.Consume("k")
	if !b.Exhausted("k") {
		t.Fatal("expected exhausted after 2 hits")
	}

	clk.advance(61 * time.Second)

	if b.Exhausted("k") {
		t.Fatal("expected not exhausted after window elapsed")
	}
	if over := b.Consume("k"); over {
		t.Fatal("expected under limit after window elapsed")
	}
}

func TestBucket_SeparateKeysDoNotInterfere(t *testing.T) {
	clk := newFakeClock()
	b := NewBucket(1, time.Minute, clk)
	defer b.Stop()

	if over := b.Consume("k1"); over {
		t.Fatal("k1 first consume should not be over limit")
	}
	if b.Exhausted("k2") {
		t.Fatal("k2 should not be affected by k1")
	}
	if over := b.Consume("k2"); over {
		t.Fatal("k2 first consume should not be over limit")
	}
}

func TestBucket_Reset_DropsRecordedHits(t *testing.T) {
	clk := newFakeClock()
	b := NewBucket(2, time.Minute, clk)
	defer b.Stop()

	b.Consume("k")
	b.Consume("k")
	b.Reset("k")
	if b.Exhausted("k") {
		t.Fatal("expected not exhausted after Reset")
	}
}

func TestBucket_RetryAfter_RoundsUpToSecond(t *testing.T) {
	clk := newFakeClock()
	b := NewBucket(1, time.Minute, clk)
	defer b.Stop()

	b.Consume("k")
	if !b.Exhausted("k") {
		t.Fatal("expected exhausted after 1 hit")
	}
	// Zero elapsed → header should be ~60s.
	if got := b.RetryAfter("k"); got != "60" {
		t.Fatalf("RetryAfter at T+0: want 60, got %s", got)
	}
	clk.advance(10 * time.Second)
	if got := b.RetryAfter("k"); got != "50" {
		t.Fatalf("RetryAfter at T+10s: want 50, got %s", got)
	}
}

func TestBucket_Stop_Idempotent(t *testing.T) {
	clk := newFakeClock()
	b := NewBucket(1, time.Minute, clk)
	b.Stop()
	b.Stop() // must not panic or block
}

func TestBucket_Cleanup_DropsIdleBucketsAfter2xWindow(t *testing.T) {
	// Exercises the package-private runCleanupOnce path so the test
	// does not depend on the wall-clock ticker. Matches the
	// Cleanup_DropsIdleBucketsAfter2xWindow invariant from spec §9.1.
	clk := newFakeClock()
	b := NewBucket(2, time.Minute, clk)
	defer b.Stop()

	b.Consume("k")
	if _, ok := b.hits["k"]; !ok {
		t.Fatal("expected key to exist after Consume")
	}

	// Advance past the rolling window; the entries go stale but are still
	// in the map until cleanup runs.
	clk.advance(61 * time.Second)
	b.runCleanupOnce()

	b.mu.Lock()
	_, stillThere := b.hits["k"]
	b.mu.Unlock()
	if stillThere {
		t.Fatal("expected idle key to be GC'd by runCleanupOnce")
	}
}
