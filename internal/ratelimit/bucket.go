// Package ratelimit implements a per-key rolling-window rate limiter used by
// the login handler and the Bearer-token auth middleware. The same package
// backs both so "N failed password reconfirmations feed the login bucket"
// (spec §3.7) is a single-line test assertion instead of a cross-package
// coupling problem.
//
// Callers construct one Bucket per purpose (login failures, auth failures,
// token creations). Keys are caller-defined opaque strings — IPs for
// per-IP buckets, user IDs stringified for per-user buckets.
package ratelimit

import (
	"fmt"
	"math"
	"sync"
	"time"
)

// Clock is the abstraction the bucket uses to read the current instant.
// Production code passes RealClock(); tests pass a fake that advances under
// their control. Defined here, not imported from internal/api, to avoid a
// dependency cycle (internal/auth imports this package).
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// RealClock returns the production Clock. Safe to share across buckets.
func RealClock() Clock { return realClock{} }

// Bucket is a per-key rolling-window counter. Each key (IP address, user ID)
// gets its own sliding list of hit timestamps; Consume records a hit and
// returns whether the key is now over the limit; Exhausted answers the same
// question without recording a hit.
//
// Memory: idle keys are dropped by a background goroutine (Cleanup) so a
// long-running process doesn't accumulate dead buckets forever. The
// cleanup cadence is 2× window; an idle key lives at most 2× window after
// its last hit before being GC'd.
type Bucket struct {
	limit  int
	window time.Duration
	clock  Clock

	mu       sync.Mutex
	hits     map[string][]time.Time // key → timestamps within window
	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// NewBucket constructs a Bucket that allows up to `limit` hits per key per
// rolling `window`. `clock` is the time source — tests pass a fake; production
// passes ratelimit.RealClock(). Starts a cleanup goroutine that trims idle
// keys at 2× window. Call Stop() at shutdown to end the goroutine.
func NewBucket(limit int, window time.Duration, clock Clock) *Bucket {
	b := &Bucket{
		limit:  limit,
		window: window,
		clock:  clock,
		hits:   make(map[string][]time.Time),
		stop:   make(chan struct{}),
	}
	b.wg.Add(1)
	go b.cleanupLoop()
	return b
}

// Consume records a hit for `key` and returns true if the key is NOW over
// the limit (i.e. this hit crossed the threshold, OR the key was already
// over). Callers use the bool to decide whether to 429 the request.
//
// Consume always records the hit, even if the key is already exhausted.
// That design lets a steady stream of 31 failures/minute keep a limiter
// pinned at 429 instead of oscillating back under the threshold after a
// single pause — an attacker who backs off just long enough to drop one
// timestamp cannot then immediately burst again.
func (b *Bucket) Consume(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.clock.Now()
	b.hits[key] = append(b.trimLocked(key, now), now)
	return len(b.hits[key]) > b.limit
}

// Exhausted returns true if `key` is currently over the limit, without
// recording a hit. Used by middleware to short-circuit DB lookups before
// the failure even happens (the rate-limit check runs BEFORE the SQL
// lookup per spec §6.1).
func (b *Bucket) Exhausted(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.clock.Now()
	return len(b.trimLocked(key, now)) >= b.limit
}

// RetryAfter returns a Retry-After header value (in seconds, rounded up)
// for a currently-exhausted key. Returns "0" if the key is not exhausted.
// Callers should only call this when Exhausted returned true.
func (b *Bucket) RetryAfter(key string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.clock.Now()
	trimmed := b.trimLocked(key, now)
	if len(trimmed) == 0 {
		return "0"
	}
	oldest := trimmed[0]
	secs := math.Ceil(b.window.Seconds() - now.Sub(oldest).Seconds())
	if secs < 1 {
		secs = 1
	}
	return fmt.Sprintf("%d", int64(secs))
}

// Reset drops all recorded hits for `key`. Used by the login handler on
// successful authentication so a shared-IP household doesn't stay pinned
// at the limit from earlier typos.
func (b *Bucket) Reset(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.hits, key)
}

// Stop ends the cleanup goroutine. Safe to call from multiple goroutines
// — sync.Once guarantees the close happens exactly once. Callers should
// defer Stop during graceful shutdown; the background ticker will stop
// cleanly.
func (b *Bucket) Stop() {
	b.stopOnce.Do(func() { close(b.stop) })
	b.wg.Wait()
}

// trimLocked drops timestamps older than b.window from b.hits[key] and
// returns the trimmed slice. Must be called with b.mu held.
func (b *Bucket) trimLocked(key string, now time.Time) []time.Time {
	cutoff := now.Add(-b.window)
	hits := b.hits[key]
	i := 0
	for ; i < len(hits); i++ {
		if hits[i].After(cutoff) {
			break
		}
	}
	if i > 0 {
		hits = hits[i:]
		b.hits[key] = hits
	}
	return hits
}

// cleanupLoop runs until Stop() is called. Every 2× window it calls
// runCleanupOnce. The ticker is wall-clock driven (not the injected Clock)
// because time.NewTicker has no Clock-backed equivalent in the stdlib;
// runCleanupOnce is exposed separately so tests can exercise the GC
// behavior deterministically without needing real time to pass.
func (b *Bucket) cleanupLoop() {
	defer b.wg.Done()
	ticker := time.NewTicker(2 * b.window)
	defer ticker.Stop()
	for {
		select {
		case <-b.stop:
			return
		case <-ticker.C:
			b.runCleanupOnce()
		}
	}
}

// runCleanupOnce is the body of one cleanup tick, extracted so tests can
// drive it directly against a fake clock without waiting 2× window of
// wall time. Package-private — production callers rely on the ticker.
func (b *Bucket) runCleanupOnce() {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.clock.Now()
	for k := range b.hits {
		if trimmed := b.trimLocked(k, now); len(trimmed) == 0 {
			delete(b.hits, k)
		}
	}
}
