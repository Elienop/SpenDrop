package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The tests in this file cover the distinction between "we have never
// successfully read BACKUP_DIR" and "we read it and found nothing".
//
// Both used to be encoded as TrustedCount == 0, and /healthz/data degrades on
// that value. So a directory the process could not read reported the same
// thing as an empty one — an alarm saying there is no restore point, raised
// against a volume full of them.
//
// The state is reachable without anything exotic: a BACKUP_DIR that is
// writable and traversable but NOT readable lets MkdirAll, VACUUM INTO, Verify
// and WriteSidecar all succeed, because none of them needs the read bit. Only
// os.ReadDir fails, with EACCES rather than fs.ErrNotExist, which is the one
// branch that deliberately declines to record anything. Every tick then writes
// a good restore point and every scrape claims there are none.

// TestSchedulerStatus_UnreadableDirectoryOnTheFirstTickIsUnknownNotZero is the
// blocker. A fresh process — a container that has just restarted — has taken
// no readable observation yet, so the guard that protects a previous
// observation from a transient fault has nothing to protect and the counts sit
// at their zero value while claiming to describe the directory.
//
// Seeding real trusted backups is what makes the assertion non-vacuous: the
// directory demonstrably holds restore points, so any encoding that says
// "zero" here is saying something false rather than merely unhelpful.
func TestSchedulerStatus_UnreadableDirectoryOnTheFirstTickIsUnknownNotZero(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions do not prevent reads")
	}
	t.Parallel()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.db")
	srcDB := populateSourceDB(t, src)
	defer srcDB.Close()
	dir := filepath.Join(tmp, "backups")

	for i := 0; i < 3; i++ {
		seedTrustedBackup(t, dir, time.Date(2026, 4, 10+i, 1, 0, 0, 0, time.UTC))
	}
	// Write + execute, no read. Snapshot, Verify and WriteSidecar all work;
	// only ReadDir fails, and it fails with EACCES.
	if err := os.Chmod(dir, 0o300); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

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
	if st.LastOutcome != OutcomeSuccess {
		t.Fatalf("LastOutcome = %q, want %q — the tick is supposed to SUCCEED here; "+
			"that self-contradiction beside a zero count is the whole defect",
			st.LastOutcome, OutcomeSuccess)
	}
	if st.DirObserved {
		t.Errorf("DirObserved = true, want false — no tick in this process has ever " +
			"read the directory, so the counts describe nothing")
	}
	if !st.DirUnreadable {
		t.Error("DirUnreadable = false, want true — a BACKUP_DIR the process cannot " +
			"read means retention is not running, and that fact reaches an operator " +
			"nowhere except the container log")
	}
}

// TestSchedulerStatus_ReadableDirectoryIsMarkedObserved is the other half of
// the pair: without it, "never report observed" would satisfy every assertion
// above and silence the health endpoint permanently.
func TestSchedulerStatus_ReadableDirectoryIsMarkedObserved(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.db")
	srcDB := populateSourceDB(t, src)
	defer srcDB.Close()
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
	if !st.DirObserved {
		t.Error("DirObserved = false after a tick that read the directory; the counts " +
			"are real and suppressing them would hide a genuinely empty BACKUP_DIR")
	}
	if st.DirUnreadable {
		t.Error("DirUnreadable = true for a directory that was read without error")
	}
}

// TestSchedulerStatus_VanishedDirectoryIsObservedNotUnknown keeps the
// asymmetry between the two error branches explicit.
//
// "Does not exist" is a DEFINITE fact about the directory — there is provably
// no restore point in it — so it must stay a real observation of zero and go
// on degrading the endpoint. Only an unreadable directory is an unknown. If
// this collapsed into the unknown case, deleting the backup volume would stop
// raising an alarm.
func TestSchedulerStatus_VanishedDirectoryIsObservedNotUnknown(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
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
	if !st.DirObserved {
		t.Error("DirObserved = false for a directory that provably does not exist; " +
			"that is a definite zero, not an unknown, and it must keep degrading")
	}
	if st.DirUnreadable {
		t.Error("DirUnreadable = true for a missing directory; a fresh install has " +
			"not created BACKUP_DIR either and it is not a permission fault")
	}
	if st.TrustedCount != 0 {
		t.Errorf("TrustedCount = %d, want 0", st.TrustedCount)
	}
}

// TestSchedulerStatus_ReadFailureClearsOnceTheDirectoryIsReadableAgain pins
// that DirUnreadable describes the LATEST tick rather than accumulating.
//
// If it were sticky, a permission fault that the operator has since repaired
// would keep the endpoint at 503 until the process restarted — the operator
// fixes the mount, watches the health check stay red, and has no way to tell
// the fix did not take. The counts are what carry forward across ticks; the
// fault flag is not.
func TestSchedulerStatus_ReadFailureClearsOnceTheDirectoryIsReadableAgain(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions do not prevent reads")
	}
	t.Parallel()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.db")
	srcDB := populateSourceDB(t, src)
	defer srcDB.Close()
	dir := filepath.Join(tmp, "backups")

	seedTrustedBackup(t, dir, time.Date(2026, 4, 11, 1, 0, 0, 0, time.UTC))
	if err := os.Chmod(dir, 0o300); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	// Distinct instants per tick so the two backups do not collide on filename.
	ticks := []time.Time{
		time.Date(2026, 4, 13, 3, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 14, 3, 0, 0, 0, time.UTC),
	}
	var i int
	s := &Scheduler{
		Enabled: true, Dir: dir, Interval: time.Hour,
		KeepDaily: 30, KeepWeekly: 4, KeepMonthly: 12, KeepCorrupt: 2,
		DBPath: src, BusyTimeout: testBusyTimeout,
		Now:    func() time.Time { return ticks[i] },
		Logger: newSilentLogger(nil),
	}
	s.runOnce(context.Background(), newSilentLogger(nil))
	if !s.Status().DirUnreadable {
		t.Fatal("DirUnreadable = false on the unreadable tick; the fixture never " +
			"reaches the state this test is about")
	}

	// The operator repairs the permissions.
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod back: %v", err)
	}
	i = 1
	s.runOnce(context.Background(), newSilentLogger(nil))

	st := s.Status()
	if st.DirUnreadable {
		t.Error("DirUnreadable = true after a tick that read the directory without " +
			"error; a repaired fault that never clears leaves the operator watching " +
			"a red health check with nothing left to fix")
	}
	if !st.DirObserved {
		t.Error("DirObserved = false after a successful scan")
	}
	if st.TrustedCount == 0 {
		t.Error("TrustedCount = 0 after a readable tick over a directory holding " +
			"backups; the recovered scan must publish real counts")
	}
}

// TestSchedulerStatus_TransientReadFailureAfterAGoodObservationKeepsTheCount is
// the defence that already existed and must survive this change. Once a
// readable observation has been taken, a later read failure falls back to it
// rather than zeroing it — otherwise a single EACCES or EIO manufactures the
// very alarm this file is about.
func TestSchedulerStatus_TransientReadFailureAfterAGoodObservationKeepsTheCount(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions do not prevent reads")
	}
	t.Parallel()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.db")
	if err := os.Mkdir(src, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	dir := filepath.Join(tmp, "backups")

	ts := time.Date(2026, 4, 12, 1, 0, 0, 0, time.UTC)
	seedTrustedBackup(t, dir, ts)

	now := time.Date(2026, 4, 13, 3, 0, 0, 0, time.UTC)
	s := &Scheduler{
		Enabled: true, Dir: dir, Interval: time.Hour,
		KeepDaily: 30, KeepWeekly: 4, KeepMonthly: 12, KeepCorrupt: 2,
		DBPath: src, BusyTimeout: testBusyTimeout,
		Now:    func() time.Time { return now },
		Logger: newSilentLogger(nil),
	}
	s.runOnce(context.Background(), newSilentLogger(nil))
	if got := s.Status(); got.TrustedCount != 1 || !got.DirObserved {
		t.Fatalf("after the seeding tick: TrustedCount = %d, DirObserved = %v; want 1, true",
			got.TrustedCount, got.DirObserved)
	}

	if err := os.Chmod(dir, 0o300); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	s.runOnce(context.Background(), newSilentLogger(nil))

	st := s.Status()
	if !st.DirObserved {
		t.Error("DirObserved = false after a read failure that followed a good " +
			"observation; the previous scan is still the last thing we actually saw")
	}
	if st.TrustedCount != 1 {
		t.Errorf("TrustedCount = %d, want 1 — a transient read failure must not zero "+
			"a count taken from a directory whose backups are all still there", st.TrustedCount)
	}
	if !st.DirUnreadable {
		t.Error("DirUnreadable = false; the read failure is still worth reporting even " +
			"though the counts survive it")
	}
}
