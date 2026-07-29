package backup

import (
	"os"
	"path/filepath"
	"sort"
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
				if _, _, err := Prune(dir, now, keepDaily, keepWeekly, keepMonthly, 2); err != nil {
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

	if _, _, err := Prune(dir, now, 7, 4, 12, 2); err != nil {
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
