package backup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPrune_KeepCorruptZeroKeepsNone is the regression test for an operator's
// explicit BACKUP_KEEP_CORRUPT=0 being silently overridden.
//
// Prune mapped any non-positive keepCorrupt to the default of 2, while config
// validation accepts 0 and the README documents the knob. So an operator on a
// tight volume — exactly the audience for a quarantine bound — asked to retain
// nothing and still got two full-size copies of the database, with no validation
// error and no log line revealing the override.
func TestPrune_KeepCorruptZeroKeepsNone(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

	for i := 0; i < 4; i++ {
		name := FormatFilename(now.AddDate(0, 0, -i)) + ".corrupt"
		if err := os.WriteFile(filepath.Join(dir, name), []byte("corrupt"), 0o600); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	if _, _, _, err := Prune(dir, now, 7, 4, 12, 0); err != nil {
		t.Fatalf("prune: %v", err)
	}

	if n := countSuffix(t, dir, ".corrupt"); n != 0 {
		t.Errorf("%d quarantined files survived BACKUP_KEEP_CORRUPT=0 — the operator's "+
			"explicit request to retain none was silently replaced with the default", n)
	}
}

// TestPrune_NegativeKeepCorruptUsesTheDefault keeps the above from becoming
// "zero means default" again by accident: a negative value is the only input
// that still means "unset".
func TestPrune_NegativeKeepCorruptUsesTheDefault(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

	for i := 0; i < 5; i++ {
		name := FormatFilename(now.AddDate(0, 0, -i)) + ".corrupt"
		if err := os.WriteFile(filepath.Join(dir, name), []byte("corrupt"), 0o600); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	if _, _, _, err := Prune(dir, now, 7, 4, 12, -1); err != nil {
		t.Fatalf("prune: %v", err)
	}

	if n := countSuffix(t, dir, ".corrupt"); n != defaultKeepCorrupt {
		t.Errorf("got %d quarantined files, want the default %d", n, defaultKeepCorrupt)
	}
}

// TestPrune_ReportsFilesItCouldNotRemove is the regression test for the
// quarantine bound failing silently.
//
// Every per-file os.Remove error was dropped with a bare `continue`, and
// pruneAndLog only logged when something HAD been removed. So on a volume where
// unlink fails — a read-only remount after an I/O error, a share holding the
// file open, which is precisely when backups matter — retention quietly stopped
// working and produced not one line of output. Silence there is indistinguishable
// from a healthy directory that needed no pruning.
func TestPrune_ReportsFilesItCouldNotRemove(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions do not prevent unlink")
	}
	t.Parallel()
	dir := t.TempDir()
	now := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

	for i := 0; i < 6; i++ {
		name := FormatFilename(now.AddDate(0, 0, -i)) + ".corrupt"
		if err := os.WriteFile(filepath.Join(dir, name), []byte("corrupt"), 0o600); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	// Remove the write bit on the DIRECTORY: entries stay readable, so Prune
	// still selects victims, but unlink is refused.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	_, removed, failed, err := Prune(dir, now, 7, 4, 12, 2)
	if err != nil {
		t.Fatalf("prune returned a hard error: %v", err)
	}
	if len(removed) != 0 {
		t.Errorf("removed = %v, but nothing can be unlinked here", removed)
	}
	if len(failed) != 4 {
		t.Fatalf("failed = %d, want 4 — the files it could not remove are not being "+
			"reported, so an unenforceable retention policy looks like a healthy one", len(failed))
	}
}

// TestPrune_ReportsOrdinaryBackupsItCouldNotRemove covers the GFS half of
// failure reporting.
//
// Prune has two removal loops — quarantined `.corrupt` files, then ordinary
// backups falling out of the GFS buckets — and each appends to `failed`
// independently. Every other reporting test seeds only `.corrupt` files, so
// deleting the ordinary loop's append left the package green while the far more
// common case (a read-only or full BACKUP_DIR full of ordinary backups)
// silently reported an enforced retention policy that was not being enforced.
//
// keepDaily/Weekly/Monthly are all 1 and the fixtures sit on five consecutive
// days inside one ISO week and one month, so all four buckets resolve to the
// same single newest file and exactly four ordinary backups become removal
// candidates.
func TestPrune_ReportsOrdinaryBackupsItCouldNotRemove(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions do not prevent unlink")
	}
	t.Parallel()
	dir := t.TempDir()
	now := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC) // Friday

	for i := 0; i < 5; i++ {
		name := FormatFilename(now.AddDate(0, 0, -i)) // no .corrupt suffix
		if err := os.WriteFile(filepath.Join(dir, name), []byte("backup"), 0o600); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	// Drop the write bit on the DIRECTORY: entries stay readable, so Prune still
	// selects victims, but unlink is refused.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	kept, removed, failed, err := Prune(dir, now, 1, 1, 1, 2)
	if err != nil {
		t.Fatalf("prune returned a hard error: %v", err)
	}
	if len(kept) != 1 {
		t.Fatalf("kept = %v, want exactly the newest backup", kept)
	}
	if len(removed) != 0 {
		t.Errorf("removed = %v, but nothing can be unlinked here", removed)
	}
	if len(failed) != 4 {
		t.Fatalf("failed = %d (%v), want 4 — ordinary backups it could not remove are "+
			"not being reported, so an unenforceable retention policy looks like a "+
			"healthy one", len(failed), failed)
	}
	// Guard against this test being satisfied by the quarantine loop: these are
	// the GFS candidates, and nothing here carries a .corrupt suffix.
	for _, name := range failed {
		if strings.HasSuffix(name, ".corrupt") {
			t.Errorf("failed contains a quarantined file %q — this test must exercise "+
				"the ordinary-backup loop, not the quarantine one", name)
		}
	}
	if n := countSuffix(t, dir, ".db"); n != 5 {
		t.Errorf("%d backups on disk, want all 5 still present", n)
	}
}

// TestPruneAndLog_LogsRemovalFailures pins the operator-visible half: the
// failures must reach the log even though nothing was successfully removed.
func TestPruneAndLog_LogsRemovalFailures(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions do not prevent unlink")
	}
	t.Parallel()
	dir := t.TempDir()
	now := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

	for i := 0; i < 6; i++ {
		name := FormatFilename(now.AddDate(0, 0, -i)) + ".corrupt"
		if err := os.WriteFile(filepath.Join(dir, name), []byte("corrupt"), 0o600); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	logBuf := &syncBuffer{}
	s := &Scheduler{
		Enabled: true, Dir: dir, Interval: time.Hour,
		KeepDaily: 7, KeepCorrupt: 2,
		DBPath: filepath.Join(dir, "nonexistent.db"), BusyTimeout: testBusyTimeout,
		Now:    func() time.Time { return now },
		Logger: newSilentLogger(logBuf),
	}
	s.pruneAndLog(now, newSilentLogger(logBuf))

	out := logBuf.String()
	if !strings.Contains(out, "could not remove") {
		t.Errorf("nothing in the log reports the failed removals, so retention silently "+
			"stopped being enforced; got: %q", out)
	}
}

// TestScheduler_StartupLogReportsKeepCorrupt makes the effective quarantine
// bound observable. It was the one retention value absent from the startup line,
// so an operator had no way to see what was actually in force.
func TestScheduler_StartupLogReportsKeepCorrupt(t *testing.T) {
	t.Parallel()
	logBuf := &syncBuffer{}
	dir := t.TempDir()
	s := &Scheduler{
		Enabled: true, Dir: dir, Interval: time.Hour,
		KeepDaily: 7, KeepWeekly: 4, KeepMonthly: 12, KeepCorrupt: 3,
		DBPath: filepath.Join(dir, "nonexistent.db"), BusyTimeout: testBusyTimeout,
		Now:    func() time.Time { return time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC) },
		Logger: newSilentLogger(logBuf),
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // stop immediately; the startup line is emitted before the first tick
	s.RunLoop(ctx)

	if out := logBuf.String(); !strings.Contains(out, "3 quarantined") {
		t.Errorf("startup log does not report the effective KeepCorrupt; got: %q", out)
	}
}

func countSuffix(t *testing.T, dir, suffix string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	n := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), suffix) {
			n++
		}
	}
	return n
}
