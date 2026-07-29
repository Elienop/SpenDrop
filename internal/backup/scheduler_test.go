package backup

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
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
//
// Not run in parallel: this is a scheduler-lifecycle test, not a concurrency
// test. Under `-race` on a loaded CI runner, running in parallel with other
// scheduler tests starved each runOnce of CPU and pushed 3 VACUUM INTO
// cycles past the 5s deadline, producing a flake (dbCount=2, want>=3).
// Running serially removes the contention without changing the invariant
// the test guards.
func TestScheduler_FiresOnStartupAndThenInterval(t *testing.T) {
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

// TestCountLiveTransactions_HappyPath asserts the helper opens the live DB
// with a minimal DSN (no WAL pragma side effect) and returns the row count
// of the transactions table. This is the verification baseline runOnce
// passes to Verify — a wrong count here would silently break the parity
// check for every scheduled backup.
func TestCountLiveTransactions_HappyPath(t *testing.T) {
	t.Parallel()
	src := filepath.Join(t.TempDir(), "src.db")
	srcDB := populateSourceDB(t, src)
	defer srcDB.Close()

	got, err := countLiveTransactions(context.Background(), src, testBusyTimeout)
	if err != nil {
		t.Fatalf("countLiveTransactions: %v", err)
	}
	if got != 3 {
		t.Errorf("count = %d, want 3", got)
	}
}

// TestCountLiveTransactions_MissingTable covers the error wrapping on a DB
// that opens cleanly but has no "transactions" table. This is the same
// failure mode Verify's row-count step sees, so the error message should be
// clearly attributable to "live" vs "backup" when it appears in the log.
func TestCountLiveTransactions_MissingTable(t *testing.T) {
	t.Parallel()
	src := filepath.Join(t.TempDir(), "other.db")
	db, err := sql.Open("sqlite3", "file:"+src+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE other (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err = countLiveTransactions(context.Background(), src, testBusyTimeout)
	if err == nil {
		t.Fatal("expected error for missing transactions table")
	}
	if !strings.Contains(err.Error(), "count live transactions") {
		t.Errorf("error = %v, want 'count live transactions' prefix", err)
	}
}

// TestPreviousBackupSize_MissingDir asserts that a not-yet-created backup
// directory maps to (0, nil), not an error. This is the normal state of a
// freshly-deployed container on the very first scheduler tick — Snapshot
// creates the dir via MkdirAll, so previousBackupSize runs against nothing
// and must return cleanly so runOnce can proceed with an unbounded MaxSize.
func TestPreviousBackupSize_MissingDir(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	size, err := previousBackupSize(dir)
	if err != nil {
		t.Errorf("err = %v, want nil for missing dir", err)
	}
	if size != 0 {
		t.Errorf("size = %d, want 0 for missing dir", size)
	}
}

// TestPreviousBackupSize_EmptyDir is the second "no baseline" case: dir
// exists but has nothing in it. Must also map to (0, nil) so Verify runs
// without an upper bound.
func TestPreviousBackupSize_EmptyDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	size, err := previousBackupSize(dir)
	if err != nil {
		t.Errorf("err = %v, want nil for empty dir", err)
	}
	if size != 0 {
		t.Errorf("size = %d, want 0 for empty dir", size)
	}
}

// TestPreviousBackupSize_IgnoresMissingSidecar asserts the sidecar
// invariant: a .db file whose name parses as a backup but which has no
// matching .sha256 companion must NOT be used as the MaxSize baseline. A
// sidecar-less file is by definition untrusted (per Phase 1.1+1.3
// invariants), and propagating its size would let a broken prior run
// silently dictate today's cap.
func TestPreviousBackupSize_IgnoresMissingSidecar(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// File matches ParseFilename but no sidecar.
	orphan := filepath.Join(dir, "spendrop-2026-04-12T0000Z.db")
	if err := os.WriteFile(orphan, make([]byte, 8192), 0o644); err != nil {
		t.Fatalf("write orphan: %v", err)
	}
	size, err := previousBackupSize(dir)
	if err != nil {
		t.Fatalf("previousBackupSize: %v", err)
	}
	if size != 0 {
		t.Errorf("size = %d, want 0 (sidecar-less file should be ignored)", size)
	}
}

// TestPreviousBackupSize_IgnoresCorrupt asserts a *.corrupt file (the
// post-verify-failure rename target) is not treated as a candidate
// previous backup. It should be ignored by ParseFilename's strict-suffix
// check alone, but pinning this behavior keeps the invariant explicit —
// a "previous" corrupt file must never influence today's MaxSize cap.
func TestPreviousBackupSize_IgnoresCorrupt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	corrupt := filepath.Join(dir, "spendrop-2026-04-12T0000Z.db.corrupt")
	if err := os.WriteFile(corrupt, make([]byte, 8192), 0o644); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}
	// Write a sidecar too, just to prove the filter is on the .db name
	// parse, not on sidecar presence alone.
	if err := os.WriteFile(corrupt+".sha256", []byte("fake\n"), 0o644); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}

	size, err := previousBackupSize(dir)
	if err != nil {
		t.Fatalf("previousBackupSize: %v", err)
	}
	if size != 0 {
		t.Errorf("size = %d, want 0 (.corrupt file should be ignored)", size)
	}
}

// TestPreviousBackupSize_PicksNewestByFilenameTimestamp asserts that when
// multiple trusted backups exist, the one with the newest filename
// timestamp wins — regardless of on-disk mtime. Using filename time means
// wall-clock perturbations (NTP step, restore-from-backup of the file,
// filesystem without mtime precision) cannot confuse which backup is the
// baseline.
func TestPreviousBackupSize_PicksNewestByFilenameTimestamp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Three trusted backups: two older same-sized, one newest with a
	// distinctive size. previousBackupSize must return the newest.
	writePair := func(name string, size int) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		if err := os.WriteFile(path+".sha256", []byte("ok\n"), 0o644); err != nil {
			t.Fatalf("write sidecar %s: %v", name, err)
		}
	}
	writePair("spendrop-2026-04-10T0000Z.db", 5000)
	writePair("spendrop-2026-04-11T0000Z.db", 6000)
	writePair("spendrop-2026-04-12T0000Z.db", 7777) // newest

	size, err := previousBackupSize(dir)
	if err != nil {
		t.Fatalf("previousBackupSize: %v", err)
	}
	if size != 7777 {
		t.Errorf("size = %d, want 7777 (newest by filename ts)", size)
	}
}

// TestFormatBytes is a table test pinning the binary-unit formatting so a
// future typo (swapping 1000 for 1024, changing a decimal place) fails
// loudly. The Phase 1.3 acceptance log format specifies the "4.8 MB" shape
// explicitly, and this test is the canonical source of truth for it.
func TestFormatBytes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{1, "1 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{5 * 1024 * 1024, "5.0 MB"},
		{1024*1024*1024 + 500*1024*1024, "1.5 GB"},
	}
	for _, tc := range cases {
		if got := formatBytes(tc.in); got != tc.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestShortenHash pins the helper's contract: exactly 12 chars + ellipsis
// for real 64-char sha256 digests, and unchanged (no ellipsis) for inputs
// that are already at or below the cutoff. The ellipsis is part of the
// helper's output, not something the caller appends — see shortenHash's
// doc comment for why.
func TestShortenHash(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"short", "short"},
		{"abcdef012345", "abcdef012345"},   // exactly 12 chars, unchanged
		{"abcdef0123456", "abcdef012345…"}, // 13 chars, one over
		{
			"a1b2c3d4e5f67890abcdef1234567890abcdef1234567890abcdef1234567890",
			"a1b2c3d4e5f6…",
		}, // real 64-char sha256 digest
	}
	for _, tc := range cases {
		if got := shortenHash(tc.in); got != tc.want {
			t.Errorf("shortenHash(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestScheduler_VerifyFailureRenamesCorrupt drives runOnce into the
// verify-failure rename branch and asserts:
//   - a *.corrupt file appears in the backup dir
//   - no .sha256 sidecar is written for the failed backup
//   - a "verification failed" log line appears
//   - the loop does not exit (the next tick is still scheduled)
//
// We force the failure via the MaxSize check: we pre-seed the backup dir
// with a minimum-sized fake "previous" backup (plus its sidecar) so
// previousBackupSize returns 4096, which makes runOnce set
// MaxSize = 10 × 4096 = 40960. The test source DB is seeded with enough
// rows that VACUUM INTO produces a file comfortably larger than 40960,
// tripping Verify's "size too large" check by a wide margin.
// TestScheduler_LargeGrowthDoesNotFreezeBackups was formerly
// TestScheduler_VerifyFailureRenamesCorrupt: it planted a tiny fake "previous
// backup" so the real one would exceed 10x it and be quarantined.
//
// That setup is now exactly the regression scenario. The size cap used to be
// 10x the newest SIDECAR'D backup, and a failed verify writes no sidecar, so a
// single large jump froze the baseline permanently — every later backup was
// quarantined and the app never recovered, silently. The cap is now derived
// from the live database, which is stateless and cannot freeze.
//
// The rename path this test used to exercise is covered directly by
// TestScheduler_RenameCorrupt below.
func TestScheduler_LargeGrowthDoesNotFreezeBackups(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.db")
	dir := filepath.Join(tmp, "backups")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir backup dir: %v", err)
	}

	// Seed a source DB with ~1500 padded rows. 1500 × ~110 bytes ≈ 165
	// KB raw; VACUUM INTO lands in the ~200 KB range after B-tree
	// overhead — comfortably above the 40960-byte MaxSize cap the fake
	// previous backup will impose.
	srcDB, err := sql.Open("sqlite3", "file:"+src+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open src: %v", err)
	}
	defer srcDB.Close()
	if _, err := srcDB.Exec(`CREATE TABLE transactions (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		value REAL NOT NULL
	)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	tx, err := srcDB.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	stmt, err := tx.Prepare(`INSERT INTO transactions (name, value) VALUES (?, ?)`)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	padding := strings.Repeat("x", 100)
	for i := 0; i < 1500; i++ {
		if _, err := stmt.Exec(fmt.Sprintf("%s-%d", padding, i), float64(i)); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	stmt.Close()
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Pre-seed a fake "previous" backup at the minimum valid size (one
	// SQLite page). previousBackupSize will return 4096; MaxSize becomes
	// 10 × 4096 = 40960.
	fakePrev := filepath.Join(dir, "spendrop-2025-01-01T0000Z.db")
	if err := os.WriteFile(fakePrev, make([]byte, 4096), 0o644); err != nil {
		t.Fatalf("write fake prev: %v", err)
	}
	if err := os.WriteFile(fakePrev+".sha256", []byte("fake\n"), 0o644); err != nil {
		t.Fatalf("write fake prev sidecar: %v", err)
	}

	// Monotonic clock so a fast ticker produces distinct filenames.
	var step int64
	now := func() time.Time {
		n := atomic.AddInt64(&step, 1)
		return time.Date(2026, 4, 13, 3, int(n), 0, 0, time.UTC)
	}

	logBuf := &syncBuffer{}
	s := &Scheduler{
		Enabled:     true,
		Dir:         dir,
		Interval:    time.Hour, // long — only the startup fire matters
		KeepDaily:   100,
		KeepWeekly:  0,
		KeepMonthly: 0,
		DBPath:      src,
		BusyTimeout: testBusyTimeout,
		Now:         now,
		Logger:      newSilentLogger(logBuf),
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.RunLoop(ctx)
		close(done)
	}()

	// Wait for the verify-failure log line. The startup fire should
	// synchronously produce it within a few hundred milliseconds — the
	// generous budget is for CPU-starved CI runners, not for real work.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(logBuf.String(), "verification failed") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done

	logs := logBuf.String()

	// The backup must SUCCEED. Under the old rule it was quarantined for
	// "size too large" against a 4 KB fake previous backup, and because no
	// sidecar is written on failure the baseline stayed 4 KB forever.
	if strings.Contains(logs, "verification failed") {
		t.Fatalf("a legitimate backup was quarantined — the size baseline is frozen again: %q", logs)
	}
	if !strings.Contains(logs, "backup ok:") {
		t.Fatalf("expected a successful backup; got: %q", logs)
	}
	// The growth signal must survive as a warning rather than a quarantine.
	if !strings.Contains(logs, "more than 10x the previous backup") {
		t.Errorf("expected a runaway-growth WARNING; got: %q", logs)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	var corrupt, sidecars int
	for _, e := range entries {
		switch {
		case strings.HasSuffix(e.Name(), ".corrupt"):
			corrupt++
		case strings.HasSuffix(e.Name(), ".sha256"):
			sidecars++
		}
	}
	if corrupt != 0 {
		t.Errorf("got %d .corrupt files, want 0", corrupt)
	}
	// A sidecar proves the backup became TRUSTED, which is what lets the
	// next run's baseline advance.
	if sidecars < 2 {
		t.Errorf("got %d sidecars, want the fake previous one plus the new backup's", sidecars)
	}
}

// TestScheduler_RenameCorrupt covers the quarantine rename directly, replacing
// the coverage the repurposed test above used to provide via an induced size
// failure.
func TestScheduler_RenameCorrupt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dst := filepath.Join(dir, "spendrop-2026-04-13T0301Z.db")
	if err := os.WriteFile(dst, []byte("not a real db"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	logBuf := &syncBuffer{}
	s := &Scheduler{Dir: dir, Logger: newSilentLogger(logBuf)}
	s.renameCorrupt(dst, fmt.Errorf("integrity_check failed"), newSilentLogger(logBuf))

	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Errorf("original file still present after quarantine rename")
	}
	if _, err := os.Stat(dst + ".corrupt"); err != nil {
		t.Errorf("quarantined file missing: %v", err)
	}
	if _, err := os.Stat(dst + ".sha256"); !os.IsNotExist(err) {
		t.Errorf("a sidecar exists for a failed backup — it would be treated as trusted")
	}
	logs := logBuf.String()
	if !strings.Contains(logs, "verification failed") || !strings.Contains(logs, ".corrupt") {
		t.Errorf("rename was not logged usefully: %q", logs)
	}
}
