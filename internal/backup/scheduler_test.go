package backup

import (
	"bytes"
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newSilentLogger returns a logger that writes to the given buffer (or
// io.Discard if buf is nil) so tests can capture or suppress the scheduler's
// output. Using a *log.Logger here instead of a custom interface keeps the
// scheduler's surface area minimal: one optional dependency, no extra types.
func newSilentLogger(buf io.Writer) *log.Logger {
	if buf == nil {
		buf = io.Discard
	}
	return log.New(buf, "", 0)
}

// syncBuffer wraps a bytes.Buffer with a mutex so the main test goroutine
// can read the captured log output concurrently with the scheduler
// goroutine that writes it. log.Logger holds its own mutex for output, but
// that mutex is private — we need our own lock to race-freely call String
// while writes are in flight.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// TestScheduler_DisabledReturnsImmediately asserts that Enabled=false is a
// hard short-circuit: RunLoop returns before any tick fires and no files are
// written. This is the contract that makes BACKUP_ENABLED=false safe to ship
// as a kill-switch — operators rely on it.
func TestScheduler_DisabledReturnsImmediately(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.db")
	dir := filepath.Join(tmp, "backups")

	srcDB := populateSourceDB(t, src)
	defer srcDB.Close()

	s := &Scheduler{
		Enabled:     false,
		Dir:         dir,
		Interval:    10 * time.Millisecond,
		KeepDaily:   7,
		KeepWeekly:  4,
		KeepMonthly: 12,
		DBPath:      src,
		BusyTimeout: testBusyTimeout,
		Logger:      newSilentLogger(nil),
	}

	// A never-cancelled context — RunLoop must not need cancellation to
	// exit when disabled.
	done := make(chan struct{})
	go func() {
		s.RunLoop(context.Background())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RunLoop(Enabled=false) did not return promptly")
	}

	// No files should have been written (including no directory).
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("backup dir created despite Enabled=false: %v", err)
	}
}

// TestScheduler_FiresOnStartupAndThenInterval asserts the scheduler fires one
// backup immediately on RunLoop entry, then one per tick. Counting files
// after N ticks is the only deterministic check here — timing-based
// assertions are brittle.
func TestScheduler_FiresOnStartupAndThenInterval(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.db")
	dir := filepath.Join(tmp, "backups")

	srcDB := populateSourceDB(t, src)
	defer srcDB.Close()

	// Fake clock: advance a large step on every Now() call so each backup
	// lands at a distinct minute and FormatFilename produces unique names.
	// Using a monotonic counter avoids coupling the test to any wall-clock
	// assumption.
	var step int64
	now := func() time.Time {
		n := atomic.AddInt64(&step, 1)
		return time.Date(2026, 4, 13, 3, int(n), 0, 0, time.UTC)
	}

	s := &Scheduler{
		Enabled:     true,
		Dir:         dir,
		Interval:    50 * time.Millisecond,
		KeepDaily:   100, // keep everything so the test can count files
		KeepWeekly:  0,
		KeepMonthly: 0,
		DBPath:      src,
		BusyTimeout: testBusyTimeout,
		Now:         now,
		Logger:      newSilentLogger(nil),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.RunLoop(ctx)
	}()

	// Wait for at least 3 files to appear, then cancel. Polling instead of
	// sleeping means we exit as soon as the scheduler has produced enough
	// evidence, instead of racing a wall-clock deadline.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		entries, _ := os.ReadDir(dir)
		count := 0
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".db") {
				count++
			}
		}
		if count >= 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	wg.Wait()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read backup dir: %v", err)
	}
	dbCount := 0
	sidecarCount := 0
	for _, e := range entries {
		switch {
		case strings.HasSuffix(e.Name(), ".sha256"):
			sidecarCount++
		case strings.HasSuffix(e.Name(), ".db"):
			dbCount++
		}
	}
	if dbCount < 3 {
		t.Errorf("dbCount = %d, want >= 3 (scheduler did not fire enough ticks)", dbCount)
	}
	if dbCount != sidecarCount {
		t.Errorf("dbCount=%d != sidecarCount=%d (backup/sidecar pairs unbalanced)", dbCount, sidecarCount)
	}
}

// TestScheduler_SurvivesFailingRun asserts the scheduler does not crash or
// exit its loop when a single backup attempt fails. The brief's acceptance
// criterion is: "Backup loop survives a single failed iteration (logs the
// error, continues on next tick) — does not crash the server."
//
// We drive the failure by pre-creating a *directory* at DBPath. SQLite's
// Open will not reject that up-front (it lazily opens the file), but the
// first query against it — Run uses PingContext — will fail with "unable
// to open database file". After observing the failure we atomically replace
// the directory with a real SQLite file and assert subsequent backups
// succeed. Keeping DBPath stable across the test avoids mutating a
// Scheduler field while RunLoop is concurrently reading it — the -race
// detector correctly flags any such mid-flight mutation, and the scheduler
// intentionally does not take a mutex.
//
// NOTE: a naive "point DBPath at a missing file" does not work: go-sqlite3
// CREATES the file on open, so a missing file silently becomes a valid
// empty database and the "failure" never fires.
func TestScheduler_SurvivesFailingRun(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.db") // single stable path
	dir := filepath.Join(tmp, "backups")

	// Pre-create src as a directory so SQLite's Ping fails on every tick
	// until we remove it and replace it with a real file.
	if err := os.Mkdir(src, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}

	var step int64
	now := func() time.Time {
		n := atomic.AddInt64(&step, 1)
		return time.Date(2026, 4, 13, 3, int(n), 0, 0, time.UTC)
	}

	logBuf := &syncBuffer{}
	s := &Scheduler{
		Enabled:     true,
		Dir:         dir,
		Interval:    30 * time.Millisecond,
		KeepDaily:   100,
		KeepWeekly:  0,
		KeepMonthly: 0,
		DBPath:      src,
		BusyTimeout: testBusyTimeout,
		Now:         now,
		Logger:      newSilentLogger(logBuf),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.RunLoop(ctx)
	}()

	// Wait for at least one failed attempt (a log line containing "error").
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(logBuf.String(), "error") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(logBuf.String(), "error") {
		cancel()
		wg.Wait()
		t.Fatalf("scheduler did not log a failure in %s: %q", 3*time.Second, logBuf.String())
	}

	// Repair the underlying filesystem: remove the blocking directory and
	// create a real SQLite file at the (unchanged) DBPath. The scheduler
	// should pick it up on its next tick.
	if err := os.Remove(src); err != nil {
		cancel()
		wg.Wait()
		t.Fatalf("remove blocking dir: %v", err)
	}
	srcDB := populateSourceDB(t, src)
	defer srcDB.Close()

	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".db") {
				// Found a successful backup.
				cancel()
				wg.Wait()
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	wg.Wait()
	t.Fatalf("scheduler never produced a successful backup after src was created; log: %q", logBuf.String())
}

// TestScheduler_CancelStopsLoop asserts ctx.Done() is honored between ticks
// and the goroutine exits promptly. This is the graceful-shutdown contract
// from main.go: cleanupCancel must stop the scheduler alongside session
// cleanup.
func TestScheduler_CancelStopsLoop(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.db")
	dir := filepath.Join(tmp, "backups")

	srcDB := populateSourceDB(t, src)
	defer srcDB.Close()

	s := &Scheduler{
		Enabled:     true,
		Dir:         dir,
		Interval:    time.Hour, // long interval — only the startup fire + cancel matters
		KeepDaily:   100,
		DBPath:      src,
		BusyTimeout: testBusyTimeout,
		Logger:      newSilentLogger(nil),
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.RunLoop(ctx)
		close(done)
	}()

	// Give the startup fire a moment to complete, then cancel.
	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunLoop did not exit within 2s of cancel")
	}

	// The startup tick should have produced at least one file.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) == 0 {
		t.Errorf("startup tick produced no files")
	}
}
