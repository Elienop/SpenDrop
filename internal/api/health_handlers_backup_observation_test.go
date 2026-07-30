package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elienop/spendrop/internal/backup"
	"github.com/elienop/spendrop/internal/database"
)

// The tests in this file cover the difference between "BACKUP_DIR holds no
// restore point" and "this process has never managed to read BACKUP_DIR".
//
// Those two used to reach /healthz/data as the same payload, because both were
// encoded as trusted_count == 0 and the endpoint degrades on that value. The
// result was a 503 that told the operator there was nothing to restore from,
// emitted next to last_outcome: "success", against a directory holding a full
// retention set. A payload that contradicts itself sends whoever reads it to
// the wrong problem.
//
// The fields are decoded into map[string]any rather than the typed block on
// purpose: the assertion is about a field being ABSENT, and a typed decode
// zero-fills a missing key into exactly the value under test.

// seedBackupSourceDB writes a minimal source database for the scheduler to
// snapshot. The table must be named "transactions" because Verify's row-count
// parity check is hard-coded to it, and it must hold enough pages to clear
// Verify's one-page floor.
func seedBackupSourceDB(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:"+path+"?_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open backup source: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE transactions (id INTEGER PRIMARY KEY, blob TEXT NOT NULL)`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	for i := 0; i < 64; i++ {
		if _, err := db.Exec(`INSERT INTO transactions (blob) VALUES (?)`, strings.Repeat("x", 512)); err != nil {
			t.Fatalf("seed row: %v", err)
		}
	}
}

// backupBlockFields runs one request and returns the backups block as raw
// decoded JSON alongside the HTTP status.
func backupBlockFields(t *testing.T, st backup.Status, now time.Time) (int, map[string]any) {
	t.Helper()
	q, db := setupTestDB(t)
	h := NewHandlerWithClock(q, db, fixedClock{t: now})
	h.SetIntegrityResult(now, database.IntegrityOK)
	h.SetBackupStatusSource(func() backup.Status { return st })

	req := httptest.NewRequest(http.MethodGet, "/healthz/data", nil)
	rec := httptest.NewRecorder()
	h.handleHealthzData(rec, req)

	var raw struct {
		Backups map[string]any `json:"backups"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v; body = %s", err, rec.Body.String())
	}
	if raw.Backups == nil {
		t.Fatal("backups block absent")
	}
	return rec.Code, raw.Backups
}

// TestHealthzBackups_UnreadableDirectory_DoesNotClaimZeroRestorePoints is the
// blocker. The scheduler wrote and verified a backup this tick — a directory
// that is writable and traversable but not readable lets every step except
// os.ReadDir succeed — so "no restore point" is provably false.
func TestHealthzBackups_UnreadableDirectory_DoesNotClaimZeroRestorePoints(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	st := backupStatusFixture(now)
	st.DirObserved = false
	st.DirUnreadable = true
	st.TrustedCount = 0
	st.NewestBackupAt = time.Time{}

	code, fields := backupBlockFields(t, st, now)

	if v, present := fields["trusted_count"]; present {
		t.Errorf("trusted_count = %v, want the field ABSENT — the directory has never "+
			"been read, so zero is a fabrication and it is the exact value the "+
			"endpoint degrades on", v)
	}
	if fields["dir_unreadable"] != true {
		t.Errorf("dir_unreadable = %v, want true — without a distinct signal an "+
			"operator cannot tell 'no backups' from 'cannot read the backup directory', "+
			"and those have completely different fixes", fields["dir_unreadable"])
	}
	// It still degrades, but on the fault it can actually prove. An unreadable
	// BACKUP_DIR means Prune's ReadDir is failing too, so retention is not
	// running and nothing can confirm a restore is possible.
	if code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 — a BACKUP_DIR the process cannot read is a "+
			"real fault, and README tells operators to alert on the HTTP code", code)
	}
}

// TestHealthzBackups_UnobservedDirectory_DoesNotDegradeOnTheUnknown pins the
// other side: an absent observation with no read failure behind it is an
// unknown, and an unknown must not raise an alarm on its own.
//
// A contract test on the pure mapping rather than a reachable scheduler state
// — every completed tick now reports either a scan or a read failure. It is
// here so a future exit path that publishes a tick without an observation
// cannot quietly reintroduce the fabricated zero.
func TestHealthzBackups_UnobservedDirectory_DoesNotDegradeOnTheUnknown(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	st := backupStatusFixture(now)
	st.DirObserved = false
	st.DirUnreadable = false
	st.TrustedCount = 0
	st.NewestBackupAt = time.Time{}

	code, fields := backupBlockFields(t, st, now)

	if code != http.StatusOK {
		t.Errorf("status = %d, want 200 — nothing has been observed and nothing has "+
			"failed; degrading on an unknown is how a health endpoint trains its "+
			"operator to ignore it", code)
	}
	if v, present := fields["trusted_count"]; present {
		t.Errorf("trusted_count = %v, want the field ABSENT for an unobserved directory", v)
	}
	if _, present := fields["quarantined_count"]; present {
		t.Error("quarantined_count present for an unobserved directory; it comes from " +
			"the same scan and is equally unknown")
	}
	if _, present := fields["prune_failed_count"]; present {
		t.Error("prune_failed_count present for an unobserved directory; the prune that " +
			"would have produced it is the call that failed")
	}
}

// TestHealthzBackups_UnreadableAfterAGoodObservation_ReportsButStaysHealthy is
// the pre-existing defence, seen from the endpoint. Once a readable scan has
// been taken, a later read failure falls back to it: the counts stay, and the
// fault reports without degrading.
//
// Not degrading here is deliberate and asymmetric with the unobserved case.
// BACKUP_INTERVAL defaults to 24h, so degrading on a single transient EIO
// would pin the endpoint at 503 for a day over a directory whose backups are
// all still present — the same false-alarm shape this file exists to remove.
// The counts are known and healthy; newest_backup_age_seconds is what surfaces
// it if the condition persists.
func TestHealthzBackups_UnreadableAfterAGoodObservation_ReportsButStaysHealthy(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	st := backupStatusFixture(now)
	st.DirObserved = true
	st.DirUnreadable = true

	code, fields := backupBlockFields(t, st, now)

	if code != http.StatusOK {
		t.Errorf("status = %d, want 200 — the last good scan found 7 restore points "+
			"and they have not gone anywhere; a 24h BACKUP_INTERVAL would hold this "+
			"503 for a day over one transient read error", code)
	}
	if got := fields["trusted_count"]; got != float64(7) {
		t.Errorf("trusted_count = %v, want 7 — a read failure after a good observation "+
			"must not discard the count", got)
	}
	if fields["dir_unreadable"] != true {
		t.Errorf("dir_unreadable = %v, want true — the counts survive the failure, so "+
			"this is the only place the failure is visible at all", fields["dir_unreadable"])
	}
}

// TestHealthzBackups_ObservedEmptyDirectory_StillDegrades guards the fix from
// overshooting. A directory that was read and found empty is a definite zero,
// not an unknown, and it must go on being the loudest thing this subsystem
// says.
func TestHealthzBackups_ObservedEmptyDirectory_StillDegrades(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	st := backupStatusFixture(now)
	st.DirObserved = true
	st.DirUnreadable = false
	st.TrustedCount = 0
	st.NewestBackupAt = time.Time{}

	code, fields := backupBlockFields(t, st, now)

	if code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 — BACKUP_DIR was read and holds no restore "+
			"point; a restore right now is impossible", code)
	}
	got, present := fields["trusted_count"]
	if !present {
		t.Fatal("trusted_count absent for a directory that WAS read; an observed zero " +
			"is a fact and must be reported as one")
	}
	if got != float64(0) {
		t.Errorf("trusted_count = %v, want 0", got)
	}
	if _, present := fields["dir_unreadable"]; present {
		t.Error("dir_unreadable present for a directory that was read without error")
	}
}

// TestHealthzBackups_EndToEndUnreadableDirWithBackupsPresent is the reported
// reproduction, run through the real Scheduler.RunLoop into the real
// handleHealthzData with no fixtures in between.
//
// BACKUP_DIR is mode 0300: writable and traversable, not readable. MkdirAll,
// VACUUM INTO, Verify and WriteSidecar need no read bit, so the tick genuinely
// succeeds and deposits a trusted restore point. Only os.ReadDir fails, with
// EACCES rather than fs.ErrNotExist. Before the fix this scraped as HTTP 503
// with {"last_outcome":"success","trusted_count":0} over a directory of real
// backups, for the life of the process.
func TestHealthzBackups_EndToEndUnreadableDirWithBackupsPresent(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions do not prevent reads")
	}
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.db")
	seedBackupSourceDB(t, src)
	dir := filepath.Join(tmp, "backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir backups: %v", err)
	}
	if err := os.Chmod(dir, 0o300); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	tick := time.Date(2026, 5, 1, 11, 0, 0, 0, time.UTC)
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	s := &backup.Scheduler{
		Enabled: true, Dir: dir, Interval: 24 * time.Hour,
		KeepDaily: 7, KeepWeekly: 4, KeepMonthly: 12, KeepCorrupt: 2,
		DBPath: src, BusyTimeout: 5 * time.Second,
		Now:    func() time.Time { return tick },
		Logger: log.New(io.Discard, "", 0),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.RunLoop(ctx); close(done) }()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && s.Status().LastRunAt.IsZero() {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done

	st := s.Status()
	if st.LastOutcome != backup.OutcomeSuccess {
		t.Fatalf("LastOutcome = %q, want %q; the fixture never reached the state this "+
			"test is about — a tick that SUCCEEDS while the directory cannot be read",
			st.LastOutcome, backup.OutcomeSuccess)
	}
	// Prove a restore point really landed, using the one call that does not
	// need the read bit.
	if _, err := os.Stat(filepath.Join(dir, backup.FormatFilename(tick)+".sha256")); err != nil {
		t.Fatalf("no sidecar on disk: %v; the tick did not produce a restore point", err)
	}

	q, db := setupTestDB(t)
	h := NewHandlerWithClock(q, db, fixedClock{t: now})
	h.SetIntegrityResult(now, database.IntegrityOK)
	h.SetBackupStatusSource(s.Status)

	req := httptest.NewRequest(http.MethodGet, "/healthz/data", nil)
	rec := httptest.NewRecorder()
	h.handleHealthzData(rec, req)

	var raw struct {
		Backups map[string]any `json:"backups"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if v, present := raw.Backups["trusted_count"]; present {
		t.Errorf("trusted_count = %v with a trusted backup sitting on disk. The "+
			"endpoint is reporting an unknown as a zero and alarming on it; body = %s",
			v, rec.Body.String())
	}
	if raw.Backups["dir_unreadable"] != true {
		t.Errorf("dir_unreadable = %v, want true; the only actionable fact here is "+
			"that BACKUP_DIR cannot be read, and nothing outside the container log "+
			"says so", raw.Backups["dir_unreadable"])
	}
	if raw.Backups["last_outcome"] != string(backup.OutcomeSuccess) {
		t.Errorf("last_outcome = %v, want %q", raw.Backups["last_outcome"], backup.OutcomeSuccess)
	}
}
