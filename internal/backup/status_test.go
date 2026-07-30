package backup

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The tests in this file cover Scheduler.Status — the read model that lets
// /healthz/data report on the backup subsystem instead of leaving every
// failure mode confined to the container log.
//
// The invariants worth defending:
//
//  1. "Newest GOOD backup" means newest TRUSTED backup: a .db whose name
//     parses AND which has a .sha256 sidecar. A quarantined or sidecar-less
//     file is not a restore point and must not make the age look healthy.
//
//  2. The reported state survives a failing tick. A run that never produces
//     a file still has to report the backup that IS on disk, because "the
//     last good backup is eleven days old" is the whole signal.
//
//  3. verify_failed is distinct from error. They have different operator
//     responses — one means the snapshot is being written and rejected, the
//     other means it is not being written at all.
//
//  4. Nothing here re-implements retention. The counts come from the same
//     directory walk previousBackupSize already used and from Prune's own
//     return values.

// tinyPageSizeSourceDB writes a source database whose page_size is 512, which
// makes VACUUM INTO emit a ~1 KB file. That is below Verify's one-page floor
// (minBackupSize = 4096), so the snapshot is written, fails verification and
// is quarantined — a deterministic verify failure with no timing games.
//
// A "point DBPath somewhere broken" fixture cannot produce this: that fails at
// step 1 or step 3 and never reaches Verify at all.
func tinyPageSizeSourceDB(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:"+path+"?_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open tiny source: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`PRAGMA page_size=512`); err != nil {
		t.Fatalf("set page_size: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE transactions (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO transactions (id) VALUES (1)`); err != nil {
		t.Fatalf("seed row: %v", err)
	}
}

// seedTrustedBackup writes a backup file and its sidecar, i.e. a restore point
// the scheduler is entitled to count.
func seedTrustedBackup(t *testing.T, dir string, ts time.Time) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	name := FormatFilename(ts)
	if err := os.WriteFile(filepath.Join(dir, name), make([]byte, minBackupSize), 0o600); err != nil {
		t.Fatalf("seed backup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".sha256"), []byte("deadbeef\n"), 0o600); err != nil {
		t.Fatalf("seed sidecar: %v", err)
	}
}

func TestSchedulerStatus_DisabledSchedulerReportsNoObservation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := &Scheduler{
		Enabled: false, Dir: dir, Interval: time.Hour,
		KeepDaily: 7, KeepWeekly: 4, KeepMonthly: 12, KeepCorrupt: 2,
		DBPath: filepath.Join(dir, "src.db"), BusyTimeout: testBusyTimeout,
		Logger: newSilentLogger(nil),
	}
	s.RunLoop(context.Background())

	st := s.Status()
	if st.Enabled {
		t.Error("Enabled = true for a scheduler that was configured off")
	}
	if !st.LastRunAt.IsZero() {
		t.Errorf("LastRunAt = %v, want zero — a disabled scheduler never ticks, and "+
			"anything reading this must be able to tell that apart from a failure", st.LastRunAt)
	}
	if st.LastOutcome != OutcomeUnknown {
		t.Errorf("LastOutcome = %q, want %q", st.LastOutcome, OutcomeUnknown)
	}
	if st.Interval != time.Hour {
		t.Errorf("Interval = %v, want 1h — the consumer needs it to judge staleness "+
			"without hard-coding a threshold", st.Interval)
	}
}

func TestSchedulerStatus_SuccessfulTickReportsATrustedBackup(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.db")
	dir := filepath.Join(tmp, "backups")
	srcDB := populateSourceDB(t, src)
	defer srcDB.Close()

	now := time.Date(2026, 4, 13, 3, 0, 0, 0, time.UTC)
	s := &Scheduler{
		Enabled: true, Dir: dir, Interval: time.Hour,
		KeepDaily: 7, KeepWeekly: 4, KeepMonthly: 12, KeepCorrupt: 2,
		DBPath: src, BusyTimeout: testBusyTimeout,
		Now:    func() time.Time { return now },
		Logger: newSilentLogger(nil),
	}
	s.runOnce(context.Background(), newSilentLogger(nil))

	st := s.Status()
	if st.LastOutcome != OutcomeSuccess {
		t.Fatalf("LastOutcome = %q, want %q", st.LastOutcome, OutcomeSuccess)
	}
	if !st.LastRunAt.Equal(now) {
		t.Errorf("LastRunAt = %v, want %v", st.LastRunAt, now)
	}
	if st.TrustedCount != 1 {
		t.Errorf("TrustedCount = %d, want 1", st.TrustedCount)
	}
	if !st.NewestBackupAt.Equal(now) {
		t.Errorf("NewestBackupAt = %v, want %v", st.NewestBackupAt, now)
	}
	if st.QuarantinedCount != 0 {
		t.Errorf("QuarantinedCount = %d, want 0", st.QuarantinedCount)
	}
	if st.PruneFailedCount != 0 {
		t.Errorf("PruneFailedCount = %d, want 0", st.PruneFailedCount)
	}
}

// TestSchedulerStatus_FreshInstallHasNoBackups is the loudest state: nothing
// to restore from. It must be reported as such rather than as an absent
// measurement.
func TestSchedulerStatus_FreshInstallHasNoBackups(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	// A DIRECTORY at DBPath: go-sqlite3 creates a missing file on open, so a
	// merely-absent path would silently become a valid empty database and the
	// tick would succeed.
	src := filepath.Join(tmp, "src.db")
	if err := os.Mkdir(src, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	dir := filepath.Join(tmp, "backups")

	now := time.Date(2026, 4, 13, 3, 0, 0, 0, time.UTC)
	s := &Scheduler{
		Enabled: true, Dir: dir, Interval: time.Hour,
		KeepDaily: 7, KeepWeekly: 4, KeepMonthly: 12, KeepCorrupt: 2,
		DBPath: src, BusyTimeout: testBusyTimeout,
		Now:    func() time.Time { return now },
		Logger: newSilentLogger(nil),
	}
	s.runOnce(context.Background(), newSilentLogger(nil))

	st := s.Status()
	if st.LastOutcome != OutcomeError {
		t.Errorf("LastOutcome = %q, want %q", st.LastOutcome, OutcomeError)
	}
	if st.TrustedCount != 0 {
		t.Errorf("TrustedCount = %d, want 0", st.TrustedCount)
	}
	if !st.NewestBackupAt.IsZero() {
		t.Errorf("NewestBackupAt = %v, want zero", st.NewestBackupAt)
	}
	if st.LastRunAt.IsZero() {
		t.Error("LastRunAt is zero after a tick actually ran — a failed tick is still " +
			"an observation, and treating it as 'never ran' hides the failure")
	}
}

// TestSchedulerStatus_VerifyFailureIsReportedDistinctlyFromAnError separates
// the two failure classes. They point at different problems: a rejected
// snapshot means the file is being written and is not trustworthy; an error
// means no file was produced at all.
func TestSchedulerStatus_VerifyFailureIsReportedDistinctlyFromAnError(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.db")
	dir := filepath.Join(tmp, "backups")
	tinyPageSizeSourceDB(t, src)

	now := time.Date(2026, 4, 13, 3, 0, 0, 0, time.UTC)
	s := &Scheduler{
		Enabled: true, Dir: dir, Interval: time.Hour,
		KeepDaily: 7, KeepWeekly: 4, KeepMonthly: 12, KeepCorrupt: 2,
		DBPath: src, BusyTimeout: testBusyTimeout,
		Now:    func() time.Time { return now },
		Logger: newSilentLogger(nil),
	}
	s.runOnce(context.Background(), newSilentLogger(nil))

	st := s.Status()
	if st.LastOutcome != OutcomeVerifyFailed {
		t.Fatalf("LastOutcome = %q, want %q — the fixture must reach Verify and be "+
			"rejected, not fail earlier", st.LastOutcome, OutcomeVerifyFailed)
	}
	if st.QuarantinedCount != 1 {
		t.Errorf("QuarantinedCount = %d, want 1", st.QuarantinedCount)
	}
	if st.TrustedCount != 0 {
		t.Errorf("TrustedCount = %d, want 0 — a quarantined file is not a restore point", st.TrustedCount)
	}
	if !st.NewestBackupAt.IsZero() {
		t.Errorf("NewestBackupAt = %v, want zero; a .corrupt file must not make the "+
			"age look healthy", st.NewestBackupAt)
	}
}

// TestSchedulerStatus_FailingTickStillReportsTheBackupOnDisk is the staleness
// case, and the reason the directory is rescanned every tick rather than the
// status being written only on success. An operator whose backups broke eleven
// days ago needs the age of what survives, not an absent field.
func TestSchedulerStatus_FailingTickStillReportsTheBackupOnDisk(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.db")
	if err := os.Mkdir(src, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	dir := filepath.Join(tmp, "backups")

	old := time.Date(2026, 4, 2, 1, 0, 0, 0, time.UTC)
	seedTrustedBackup(t, dir, old)

	now := time.Date(2026, 4, 13, 3, 0, 0, 0, time.UTC)
	s := &Scheduler{
		Enabled: true, Dir: dir, Interval: time.Hour,
		KeepDaily: 30, KeepWeekly: 4, KeepMonthly: 12, KeepCorrupt: 2,
		DBPath: src, BusyTimeout: testBusyTimeout,
		Now:    func() time.Time { return now },
		Logger: newSilentLogger(nil),
	}
	s.runOnce(context.Background(), newSilentLogger(nil))

	st := s.Status()
	if st.LastOutcome != OutcomeError {
		t.Errorf("LastOutcome = %q, want %q", st.LastOutcome, OutcomeError)
	}
	if !st.NewestBackupAt.Equal(old) {
		t.Errorf("NewestBackupAt = %v, want %v — a failing tick must still report the "+
			"last good backup, which is what makes staleness visible", st.NewestBackupAt, old)
	}
	if st.TrustedCount != 1 {
		t.Errorf("TrustedCount = %d, want 1", st.TrustedCount)
	}
}

// TestSchedulerStatus_SidecarlessBackupIsNotTrusted pins the definition of
// "good". A .db without its .sha256 is exactly what a crash between VACUUM
// INTO and the sidecar write leaves behind, and Phase 1.3's whole trust model
// is sidecar presence.
func TestSchedulerStatus_SidecarlessBackupIsNotTrusted(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.db")
	if err := os.Mkdir(src, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	dir := filepath.Join(tmp, "backups")
	ts := time.Date(2026, 4, 12, 1, 0, 0, 0, time.UTC)
	seedTrustedBackup(t, dir, ts)
	if err := os.Remove(filepath.Join(dir, FormatFilename(ts)+".sha256")); err != nil {
		t.Fatalf("remove sidecar: %v", err)
	}

	s := &Scheduler{
		Enabled: true, Dir: dir, Interval: time.Hour,
		KeepDaily: 30, KeepWeekly: 4, KeepMonthly: 12, KeepCorrupt: 2,
		DBPath: src, BusyTimeout: testBusyTimeout,
		Now:    func() time.Time { return time.Date(2026, 4, 13, 3, 0, 0, 0, time.UTC) },
		Logger: newSilentLogger(nil),
	}
	s.runOnce(context.Background(), newSilentLogger(nil))

	st := s.Status()
	if st.TrustedCount != 0 {
		t.Errorf("TrustedCount = %d, want 0 — a backup with no sidecar was never "+
			"verified and must not be counted as a restore point", st.TrustedCount)
	}
	if !st.NewestBackupAt.IsZero() {
		t.Errorf("NewestBackupAt = %v, want zero", st.NewestBackupAt)
	}
}

// TestSchedulerStatus_CountsSurvivingQuarantinedFiles reports the quarantine
// AFTER retention has run, so the number matches what is actually on the
// volume rather than what was produced.
func TestSchedulerStatus_CountsSurvivingQuarantinedFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		name := FormatFilename(now.AddDate(0, 0, -i)) + ".corrupt"
		if err := os.WriteFile(filepath.Join(dir, name), []byte("corrupt"), 0o600); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	s := &Scheduler{
		Enabled: true, Dir: dir, Interval: time.Hour,
		KeepDaily: 7, KeepWeekly: 4, KeepMonthly: 12, KeepCorrupt: 2,
		DBPath: filepath.Join(dir, "nonexistent.db"), BusyTimeout: testBusyTimeout,
		Now:    func() time.Time { return now },
		Logger: newSilentLogger(nil),
	}
	s.pruneAndLog(now, newSilentLogger(nil))

	st := s.Status()
	if st.QuarantinedCount != 2 {
		t.Errorf("QuarantinedCount = %d, want 2 (four seeded, KeepCorrupt=2) — the "+
			"count must reflect the post-prune directory", st.QuarantinedCount)
	}
}

// TestSchedulerStatus_ReportsPruneRemovalFailures surfaces the one condition
// under which retention silently stops being enforced. It already reaches the
// log; without it here, nothing outside the container can see it.
func TestSchedulerStatus_ReportsPruneRemovalFailures(t *testing.T) {
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

	s := &Scheduler{
		Enabled: true, Dir: dir, Interval: time.Hour,
		KeepDaily: 7, KeepWeekly: 4, KeepMonthly: 12, KeepCorrupt: 2,
		DBPath: filepath.Join(dir, "nonexistent.db"), BusyTimeout: testBusyTimeout,
		Now:    func() time.Time { return now },
		Logger: newSilentLogger(nil),
	}
	s.pruneAndLog(now, newSilentLogger(nil))

	st := s.Status()
	if st.PruneFailedCount != 4 {
		t.Errorf("PruneFailedCount = %d, want 4 (six seeded, KeepCorrupt=2, none "+
			"removable) — retention is not being enforced and nothing reports it", st.PruneFailedCount)
	}
}
