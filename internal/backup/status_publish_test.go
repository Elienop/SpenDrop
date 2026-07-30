package backup

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestSchedulerStatus_TickPublishesAtomically pins that ONE tick reaches
// Status() as ONE observation.
//
// The snapshot is written from runOnce's deferred publish. When that publish
// was split across two statusMu sections — LastRunAt+LastOutcome from one
// defer, the directory scan from another — a Status() read landing between
// them saw a tick that had demonstrably run, had succeeded, and reported zero
// restore points. /healthz/data maps exactly that triple to 503, so a scrape
// in the gap invents a "nothing to restore" alarm for a directory holding a
// full retention set.
//
// Only the FIRST tick of a process can show it: after that the counts are
// already populated from the previous tick and the stale value happens to be
// correct. That is why each iteration below builds a fresh Scheduler against
// the same already-populated directory — which is also the production shape,
// a container restarting onto a volume that already holds backups.
//
// Deterministic by construction rather than by timing: the reader is released
// only once it is confirmed spinning, and it samples Status() in a tight loop
// for the whole tick, so it takes thousands of samples inside a window
// measured in hundreds of microseconds. The failure condition is a single
// counter, not a wall-clock threshold.
//
// NOTE: swapping the two defers does NOT satisfy this test's intent even
// though it would move the window. It lets a reader pair fresh counts with the
// PREVIOUS tick's outcome, which briefly HIDES a failure instead of briefly
// inventing one. The invariant asserted here is atomicity, not an order.
func TestSchedulerStatus_TickPublishesAtomically(t *testing.T) {
	if runtime.GOMAXPROCS(0) < 2 {
		t.Skip("needs at least 2 Ps for the reader to run alongside the tick")
	}
	tmp := t.TempDir()

	// A directory at DBPath makes every tick fail fast and deterministically
	// (go-sqlite3 would otherwise create a missing file and succeed). The tick's
	// own fate is irrelevant here — the invariant under test is that the counts
	// and the run stamp appear together, whatever the outcome.
	src := filepath.Join(tmp, "src.db")
	if err := os.Mkdir(src, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	dir := filepath.Join(tmp, "backups")

	// A steady-state retention set: enough trusted backups that Prune plus the
	// health scan take real work to complete, and spread over distinct days so
	// the daily bucket keeps every one of them on every tick.
	now := time.Date(2026, 4, 13, 3, 0, 0, 0, time.UTC)
	const seeded = 120
	for i := 0; i < seeded; i++ {
		seedTrustedBackup(t, dir, now.AddDate(0, 0, -i))
	}

	const iterations = 20
	var violations, samples int64
	for i := 0; i < iterations; i++ {
		// Fresh Scheduler per iteration: a zero snapshot is the only state in
		// which a half-published tick is observable.
		s := &Scheduler{
			Enabled: true, Dir: dir, Interval: time.Hour,
			KeepDaily: seeded + 10, KeepWeekly: 4, KeepMonthly: 12, KeepCorrupt: 2,
			DBPath: src, BusyTimeout: testBusyTimeout,
			Now:    func() time.Time { return now },
			Logger: newSilentLogger(nil),
		}

		spinning := make(chan struct{})
		stop := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			close(spinning)
			for {
				select {
				case <-stop:
					return
				default:
				}
				st := s.Status()
				atomic.AddInt64(&samples, 1)
				// The tick has run (LastRunAt is stamped) but the directory
				// holding 120 restore points reports none. No single
				// observation of this scheduler is ever both of those things.
				if !st.LastRunAt.IsZero() && st.TrustedCount == 0 {
					atomic.AddInt64(&violations, 1)
				}
			}
		}()

		<-spinning
		s.runOnce(context.Background(), newSilentLogger(nil))
		close(stop)
		wg.Wait()

		if got := s.Status().TrustedCount; got != seeded {
			t.Fatalf("iteration %d: TrustedCount = %d after the tick, want %d; the "+
				"fixture is not exercising a populated directory", i, got, seeded)
		}
	}

	if samples < iterations {
		t.Fatalf("reader took %d samples across %d iterations; it never ran alongside "+
			"a tick and the test proves nothing", samples, iterations)
	}
	if violations != 0 {
		t.Errorf("%d of %d Status() reads saw a tick that had run and reported zero "+
			"restore points, against a directory holding %d. The tick is published in "+
			"two statusMu sections, so a /healthz/data scrape landing between them "+
			"returns 503 claiming there is nothing to restore.",
			violations, samples, seeded)
	}
}
