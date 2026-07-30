package backup

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestPrune_DailyWindowIsIntervalIndependent is the regression test for the
// daily tier counting FILES instead of calendar days.
//
// keepDaily used to mean "keep N files", so the recent-recovery window was
// keepDaily × BACKUP_INTERVAL rather than keepDaily days. Turning the backup
// dial toward "safer" made recovery dramatically worse: measured worst-case
// at-least-daily coverage with the default 7/4/12 was 144h at a 24h interval,
// but 72h at 12h (which the README recommends as a common adjustment) and 6h
// at the 1h minimum.
//
// The assertion is on the TIMESTAMP DISTRIBUTION, not the file count. Counts
// are already comparable across intervals (10 files at 24h vs 12 at 1h) — it
// is the spacing that collapsed, which is why a count-based test would be
// vacuous.
func TestPrune_DailyWindowIsIntervalIndependent(t *testing.T) {
	t.Parallel()

	const (
		keepDaily   = 7
		keepWeekly  = 4
		keepMonthly = 12
		// Depth of contiguous at-least-daily restore coverage we require.
		wantMinCoverage = 120 * time.Hour
		// A gap larger than this breaks "I can restore to roughly any day".
		maxGap = 25 * time.Hour
	)

	for _, interval := range []time.Duration{24 * time.Hour, 12 * time.Hour, 6 * time.Hour, time.Hour} {
		// Sweep the run's phase across a full week so the ISO-week boundary
		// lands in every position — the worst case is a run ending late on a
		// Sunday, when the current week's anchor is also the newest file.
		for shift := 0; shift < 168; shift += 24 {
			dir := t.TempDir()
			start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(shift) * time.Hour)

			// Roll forward 60 days exactly as the scheduler does: write a
			// backup, then prune, then advance.
			for now := start; now.Before(start.Add(60 * 24 * time.Hour)); now = now.Add(interval) {
				name := FormatFilename(now)
				if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
					t.Fatalf("write %s: %v", name, err)
				}
				if _, _, _, err := Prune(dir, now, keepDaily, keepWeekly, keepMonthly, 2); err != nil {
					t.Fatalf("prune: %v", err)
				}
			}

			// Walk the survivors newest-first while each consecutive gap is
			// small enough to count as continuous daily coverage.
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatalf("readdir: %v", err)
			}
			var stamps []time.Time
			for _, e := range entries {
				if ts, perr := ParseFilename(e.Name()); perr == nil {
					stamps = append(stamps, ts)
				}
			}
			if len(stamps) == 0 {
				t.Fatalf("interval=%s shift=%dh: no backups survived", interval, shift)
			}
			sort.Slice(stamps, func(i, j int) bool { return stamps[i].After(stamps[j]) })

			coverage := time.Duration(0)
			for i := 1; i < len(stamps); i++ {
				gap := stamps[i-1].Sub(stamps[i])
				if gap > maxGap {
					break
				}
				coverage += gap
			}

			if coverage < wantMinCoverage {
				t.Errorf("BACKUP_INTERVAL=%s (phase +%dh): contiguous daily coverage is only %s, want >= %s — "+
					"the daily tier is counting files, so its window scales with the interval",
					interval, shift, coverage, wantMinCoverage)
			}
		}
	}
}

// TestPrune_BoundsCorruptQuarantine is the regression test for the unbounded
// quarantine.
//
// A failed verify renames the backup to `.corrupt`, which ParseFilename
// rejects, so Prune skipped it as an operator file. Each one is a FULL-SIZE
// copy of the database, and BACKUP_DIR shares a volume with the live database,
// so a sustained failure ends in ENOSPC that kills the app and its backups
// together.
func TestPrune_BoundsCorruptQuarantine(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

	// Six quarantined files on six distinct days.
	var corruptNames []string
	for i := 0; i < 6; i++ {
		name := FormatFilename(now.AddDate(0, 0, -i)) + ".corrupt"
		corruptNames = append(corruptNames, name)
		if err := os.WriteFile(filepath.Join(dir, name), []byte("corrupt"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	// Two healthy backups with sidecars.
	for i := 0; i < 2; i++ {
		name := FormatFilename(now.Add(-time.Duration(i) * time.Hour))
		if err := os.WriteFile(filepath.Join(dir, name), []byte("ok"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".sha256"), []byte("h"), 0o600); err != nil {
			t.Fatalf("write sidecar: %v", err)
		}
	}
	// Operator files that must never be touched.
	for _, name := range []string{"README.txt", "manual-export.db"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("keep"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	if _, _, _, err := Prune(dir, now, 7, 4, 12, 2); err != nil {
		t.Fatalf("prune: %v", err)
	}

	surviving := map[string]bool{}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		surviving[e.Name()] = true
	}

	// The two newest quarantined files survive; the four oldest are gone.
	for i, name := range corruptNames {
		if i < 2 && !surviving[name] {
			t.Errorf("newest quarantined file %s was deleted — forensic evidence lost", name)
		}
		if i >= 2 && surviving[name] {
			t.Errorf("stale quarantined file %s survived — the quarantine is still unbounded", name)
		}
	}

	// Load-bearing: proves the strip-and-retry did not widen the parser into
	// deleting things it must never touch.
	for _, name := range []string{"README.txt", "manual-export.db"} {
		if !surviving[name] {
			t.Errorf("operator file %s was deleted", name)
		}
	}
}

// TestPruneAndLog_RunsWhenSnapshotFails is the regression test for the
// quarantine bound being unreachable in the one state it exists to resolve.
//
// pruneAndReport used to be deferred only AFTER Snapshot succeeded. On a full
// BACKUP_DIR volume — the ENOSPC end state a runaway quarantine produces —
// Snapshot is precisely what fails, so retention never ran and the disk stayed
// full forever. An operator upgrading specifically to reclaim the quarantine
// would have seen nothing happen.
func TestPruneAndLog_RunsWhenSnapshotFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

	// Six quarantined files: four are past the keep bound and must be swept.
	for i := 0; i < 6; i++ {
		name := FormatFilename(now.AddDate(0, 0, -i)) + ".corrupt"
		if err := os.WriteFile(filepath.Join(dir, name), []byte("corrupt"), 0o600); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	logBuf := &syncBuffer{}
	s := &Scheduler{
		Enabled:     true,
		Dir:         dir,
		Interval:    time.Hour,
		KeepDaily:   7,
		KeepCorrupt: 2,
		// A source path that does not exist, so Snapshot fails — standing in
		// for the ENOSPC failure without needing a full filesystem.
		DBPath:      filepath.Join(dir, "nonexistent-source.db"),
		BusyTimeout: testBusyTimeout,
		Now:         func() time.Time { return now },
		Logger:      newSilentLogger(logBuf),
	}

	s.runOnce(context.Background(), newSilentLogger(logBuf))

	var corrupt int
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".corrupt") {
			corrupt++
		}
	}
	if corrupt != 2 {
		t.Errorf("got %d quarantined files after a failed snapshot, want 2 — "+
			"retention never ran, so a full volume can never be reclaimed", corrupt)
	}
}

// TestScheduler_BaselineDoesNotFreezeAcrossTicks is the regression test for the
// permanent, silent backup outage.
//
// The cap used to be 10x the newest SIDECAR'D backup, and a failed verify
// writes no sidecar — so the baseline could never advance past a failure. One
// large jump (measured trigger: importing ~2,700 transactions after a fresh
// install's first backup of an empty DB) quarantined every subsequent run
// forever, with the app healthy and /healthz green throughout.
//
// A single-tick assertion would be vacuous: tick 1 always succeeded even with
// the bug. The failure only shows from tick 2 onward, so this drives several.
func TestScheduler_BaselineDoesNotFreezeAcrossTicks(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.db")
	dir := filepath.Join(tmp, "backups")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	db, err := sql.Open("sqlite3", "file:"+src+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE transactions (id INTEGER PRIMARY KEY, pad TEXT)`); err != nil {
		t.Fatalf("create: %v", err)
	}

	tick := 0
	logBuf := &syncBuffer{}
	s := &Scheduler{
		Enabled: true, Dir: dir, Interval: time.Hour,
		KeepDaily: 100, KeepCorrupt: 2,
		DBPath: src, BusyTimeout: testBusyTimeout,
		Now:    func() time.Time { tick++; return time.Date(2026, 4, 13, tick, 0, 0, 0, time.UTC) },
		Logger: newSilentLogger(logBuf),
	}

	// Tick 1: backup of a near-empty DB. This is the anchor that used to
	// freeze everything after it.
	s.runOnce(context.Background(), newSilentLogger(logBuf))

	// The user then imports their spreadsheet history.
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	stmt, err := tx.Prepare(`INSERT INTO transactions (pad) VALUES (?)`)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	pad := strings.Repeat("x", 200)
	for i := 0; i < 5000; i++ {
		if _, err := stmt.Exec(pad); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	stmt.Close()
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Several more ticks. Every one must produce a TRUSTED backup.
	for i := 0; i < 3; i++ {
		s.runOnce(context.Background(), newSilentLogger(logBuf))
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
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
		t.Errorf("%d backups were quarantined after the DB grew — the baseline is frozen, "+
			"so backups have stopped permanently: %s", corrupt, logBuf.String())
	}
	// One sidecar per successful tick: the anchor plus the three post-growth runs.
	if sidecars != 4 {
		t.Errorf("got %d trusted backups, want 4 (one per tick)", sidecars)
	}

	// And the baseline must now track the GROWN database, not the day-one stub.
	size, err := previousBackupSize(dir)
	if err != nil {
		t.Fatalf("previousBackupSize: %v", err)
	}
	if size < 500_000 {
		t.Errorf("newest trusted backup is only %d bytes — still anchored to the empty stub", size)
	}
}
