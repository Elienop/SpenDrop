package api

import (
	"context"
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

// The tests in this file cover the backup block of GET /healthz/data.
//
// Every backup failure mode the scheduler knows about — a snapshot that never
// ran, one that was written and rejected, a retention sweep that could not
// delete anything, a directory with no restore point left in it — previously
// reached the container log and nothing else. An operator who does not grep
// the log finds out at restore time.
//
// The unhealthy/informational split this file pins:
//
//   - DEGRADE on "backups are on but there is no restore point on disk" and on
//     "the last tick did not produce a trusted backup". Both mean a restore
//     right now is impossible or is about to become impossible, which is what
//     the endpoint's 503 is for.
//
//   - REPORT ONLY for quarantined files, prune removal failures and a stale
//     backup. A .corrupt file is forensic evidence that legitimately outlives
//     the failure that produced it (KeepCorrupt retains the newest samples on
//     purpose), so alerting on it would pin the endpoint at 503 long after
//     backups recovered. Prune failures mean the volume grows without bound —
//     serious, but the existing backups are intact and nothing about serving
//     requests is degraded. And staleness has no correct universal threshold:
//     BACKUP_INTERVAL is operator-configured from minutes to weeks, so the
//     payload emits the age AND the configured interval and lets the alert
//     rule pick the multiple, rather than baking in a number that is wrong for
//     everyone who tuned it.
//
//   - NEVER degrade when the operator turned backups off, or before the first
//     tick has completed. A container restart would otherwise flap 503 for the
//     first few seconds of every boot.

// backupStatusFixture is a healthy status; each test mutates the one field it
// is about, so a test named for prune failures cannot accidentally pass
// because of an unrelated zero value.
func backupStatusFixture(now time.Time) backup.Status {
	return backup.Status{
		Enabled:        true,
		Interval:       24 * time.Hour,
		LastRunAt:      now.Add(-5 * time.Minute),
		LastOutcome:    backup.OutcomeSuccess,
		NewestBackupAt: now.Add(-5 * time.Minute),
		TrustedCount:   7,
	}
}

// healthzWithBackupStatus runs one request against a handler wired to the
// given status and returns the decoded payload plus the HTTP code.
func healthzWithBackupStatus(t *testing.T, st backup.Status, now time.Time) (int, healthzDataResponse) {
	t.Helper()
	q, db := setupTestDB(t)
	h := NewHandlerWithClock(q, db, fixedClock{t: now})
	h.SetIntegrityResult(now, database.IntegrityOK)
	h.SetBackupStatusSource(func() backup.Status { return st })

	req := httptest.NewRequest(http.MethodGet, "/healthz/data", nil)
	rec := httptest.NewRecorder()
	h.handleHealthzData(rec, req)

	var resp healthzDataResponse
	decodeResponse(t, rec, &resp)
	return rec.Code, resp
}

func TestHealthzBackups_HealthyRecentBackup_Returns200WithDetail(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	code, resp := healthzWithBackupStatus(t, backupStatusFixture(now), now)

	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if resp.Backups == nil {
		t.Fatal("backups block is absent; monitoring cannot alert on a field that is not there")
	}
	b := resp.Backups
	if !b.Enabled {
		t.Error("Enabled = false")
	}
	if b.LastOutcome != string(backup.OutcomeSuccess) {
		t.Errorf("LastOutcome = %q, want %q", b.LastOutcome, backup.OutcomeSuccess)
	}
	if b.TrustedCount != 7 {
		t.Errorf("TrustedCount = %d, want 7", b.TrustedCount)
	}
	if b.NewestBackupAgeSeconds == nil {
		t.Fatal("NewestBackupAgeSeconds is nil for a backup that exists")
	}
	if *b.NewestBackupAgeSeconds != 300 {
		t.Errorf("NewestBackupAgeSeconds = %d, want 300", *b.NewestBackupAgeSeconds)
	}
	if b.IntervalSeconds != 86400 {
		t.Errorf("IntervalSeconds = %d, want 86400 — without it an alert rule has to "+
			"hard-code a staleness threshold", b.IntervalSeconds)
	}
	if b.NewestBackupAt == nil || !b.NewestBackupAt.Equal(now.Add(-5*time.Minute)) {
		t.Errorf("NewestBackupAt = %v, want %v", b.NewestBackupAt, now.Add(-5*time.Minute))
	}
}

// TestHealthzBackups_NoBackupsOnDisk_Returns503 is the loudest state the
// subsystem has: backups are switched on, a tick has run, and there is nothing
// to restore from.
func TestHealthzBackups_NoBackupsOnDisk_Returns503(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	st := backupStatusFixture(now)
	st.TrustedCount = 0
	st.NewestBackupAt = time.Time{}

	code, resp := healthzWithBackupStatus(t, st, now)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 — an install with no restore point is not healthy", code)
	}
	if resp.Status != "degraded" {
		t.Errorf("Status = %q, want %q", resp.Status, "degraded")
	}
	if resp.Backups.NewestBackupAgeSeconds != nil {
		t.Errorf("NewestBackupAgeSeconds = %d, want the field ABSENT — emitting 0 for "+
			"'no backup' reads as a backup taken this second",
			*resp.Backups.NewestBackupAgeSeconds)
	}
	if resp.Backups.NewestBackupAt != nil {
		t.Errorf("NewestBackupAt = %v, want absent", resp.Backups.NewestBackupAt)
	}
}

func TestHealthzBackups_LastRunFailed_Returns503(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	for _, outcome := range []backup.Outcome{backup.OutcomeError, backup.OutcomeVerifyFailed} {
		t.Run(string(outcome), func(t *testing.T) {
			st := backupStatusFixture(now)
			st.LastOutcome = outcome
			// A stale-but-present backup remains: the 503 must come from the
			// failed run, not from an empty directory.
			st.NewestBackupAt = now.Add(-72 * time.Hour)

			code, resp := healthzWithBackupStatus(t, st, now)
			if code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503 for last_outcome=%q", code, outcome)
			}
			if resp.Backups.LastOutcome != string(outcome) {
				t.Errorf("LastOutcome = %q, want %q — the two failure classes have "+
					"different operator responses and must not be collapsed",
					resp.Backups.LastOutcome, outcome)
			}
		})
	}
}

// TestHealthzBackups_QuarantinedFiles_ReportedButHealthy pins the
// informational half. A .corrupt file is evidence, not an outage, and it
// outlives the failure by design.
func TestHealthzBackups_QuarantinedFiles_ReportedButHealthy(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	st := backupStatusFixture(now)
	st.QuarantinedCount = 2

	code, resp := healthzWithBackupStatus(t, st, now)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — quarantined files left over from a failure "+
			"that has since recovered would pin the endpoint at 503 forever", code)
	}
	if resp.Backups.QuarantinedCount != 2 {
		t.Errorf("QuarantinedCount = %d, want 2", resp.Backups.QuarantinedCount)
	}
}

// TestHealthzBackups_PruneFailures_ReportedButHealthy is the other
// informational signal. Retention not being enforced means the volume grows;
// the backups themselves are intact.
func TestHealthzBackups_PruneFailures_ReportedButHealthy(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	st := backupStatusFixture(now)
	st.PruneFailedCount = 4

	code, resp := healthzWithBackupStatus(t, st, now)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if resp.Backups.PruneFailedCount != 4 {
		t.Errorf("PruneFailedCount = %d, want 4 — retention silently stopped being "+
			"enforced and this is the only place outside the log that says so",
			resp.Backups.PruneFailedCount)
	}
}

// TestHealthzBackups_StaleBackup_ReportedButHealthy: there is no universally
// correct staleness threshold, so the endpoint reports the age and leaves the
// judgement to the alert rule.
func TestHealthzBackups_StaleBackup_ReportedButHealthy(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	st := backupStatusFixture(now)
	st.NewestBackupAt = now.Add(-11 * 24 * time.Hour)

	code, resp := healthzWithBackupStatus(t, st, now)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — BACKUP_INTERVAL ranges from minutes to "+
			"weeks, so a hard-coded staleness threshold is wrong for someone", code)
	}
	if resp.Backups.NewestBackupAgeSeconds == nil {
		t.Fatal("NewestBackupAgeSeconds is nil")
	}
	if want := int64(11 * 24 * 3600); *resp.Backups.NewestBackupAgeSeconds != want {
		t.Errorf("NewestBackupAgeSeconds = %d, want %d", *resp.Backups.NewestBackupAgeSeconds, want)
	}
}

// TestHealthzBackups_Disabled_StaysHealthy: BACKUP_ENABLED=false is a
// configuration choice. Reporting "no backups" as an outage would make the
// kill-switch unusable.
func TestHealthzBackups_Disabled_StaysHealthy(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	st := backup.Status{Enabled: false, Interval: 24 * time.Hour}

	code, resp := healthzWithBackupStatus(t, st, now)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a deliberately disabled scheduler", code)
	}
	if resp.Backups == nil {
		t.Fatal("backups block absent; a consumer must be able to see that backups are OFF")
	}
	if resp.Backups.Enabled {
		t.Error("Enabled = true for a disabled scheduler")
	}
	if resp.Backups.LastOutcome != "" {
		t.Errorf("LastOutcome = %q, want empty — no tick ever ran", resp.Backups.LastOutcome)
	}
}

// TestHealthzBackups_EnabledButNeverRan_StaysHealthy covers the boot window.
// The scheduler fires on entry but the HTTP server binds first, so a scrape
// landing in between must not report an outage — otherwise every restart flaps.
func TestHealthzBackups_EnabledButNeverRan_StaysHealthy(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	st := backup.Status{Enabled: true, Interval: 24 * time.Hour}

	code, resp := healthzWithBackupStatus(t, st, now)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — the first tick has not completed yet, which "+
			"is not the same as a failure", code)
	}
	if resp.Backups.LastRunAt != nil {
		t.Errorf("LastRunAt = %v, want absent", resp.Backups.LastRunAt)
	}
	if resp.Backups.TrustedCount != 0 {
		t.Errorf("TrustedCount = %d, want 0", resp.Backups.TrustedCount)
	}
}

// TestHealthzBackups_AbsentWhenUnwired keeps the block honest for a binary
// that never installed a status source: claiming enabled=false there would
// assert something we do not know.
func TestHealthzBackups_AbsentWhenUnwired(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	h.SetIntegrityResult(time.Now().UTC(), database.IntegrityOK)

	req := httptest.NewRequest(http.MethodGet, "/healthz/data", nil)
	rec := httptest.NewRecorder()
	h.handleHealthzData(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, present := raw["backups"]; present {
		t.Error("backups key present with no status source wired; an absent block is " +
			"the honest encoding of 'this binary does not know'")
	}
}

// TestHealthzBackups_LeaksNoPaths is the disclosure guard. /healthz/data is
// unauthenticated, so the block must carry counts, timestamps and an enum —
// never BACKUP_DIR, DB_PATH or a filename.
func TestHealthzBackups_LeaksNoPaths(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	q, db := setupTestDB(t)
	h := NewHandlerWithClock(q, db, fixedClock{t: now})
	h.SetIntegrityResult(now, database.IntegrityOK)
	st := backupStatusFixture(now)
	st.QuarantinedCount = 3
	st.PruneFailedCount = 1
	h.SetBackupStatusSource(func() backup.Status { return st })

	req := httptest.NewRequest(http.MethodGet, "/healthz/data", nil)
	rec := httptest.NewRecorder()
	h.handleHealthzData(rec, req)

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	blob, ok := raw["backups"]
	if !ok {
		t.Fatal("backups block absent")
	}

	var fields map[string]any
	if err := json.Unmarshal(blob, &fields); err != nil {
		t.Fatalf("decode backups: %v", err)
	}
	allowed := map[string]bool{
		"enabled": true, "last_run_at": true, "last_outcome": true,
		"trusted_count": true, "quarantined_count": true, "prune_failed_count": true,
		"newest_backup_at": true, "newest_backup_age_seconds": true,
		"interval_seconds": true,
	}
	for k := range fields {
		if !allowed[k] {
			t.Errorf("unexpected field %q in the backups block; every field here is "+
				"served unauthenticated and must be reviewed before it is added", k)
		}
	}
	// A path would arrive as a string containing a separator. The only string
	// field is the outcome enum.
	for k, v := range fields {
		s, isString := v.(string)
		if !isString {
			continue
		}
		if strings.ContainsAny(s, "/\\") {
			t.Errorf("field %q = %q looks like a filesystem path", k, s)
		}
	}
}

// TestHealthzBackups_EndToEndWithARealScheduler wires the actual accessor into
// the actual handler. The fixture-driven tests above would all stay green if
// SetBackupStatusSource were never called with scheduler.Status — this is the
// one that fails if the seam is wired to the wrong thing.
func TestHealthzBackups_EndToEndWithARealScheduler(t *testing.T) {
	tmp := t.TempDir()
	// A directory at DBPath makes every tick fail deterministically:
	// go-sqlite3 creates a missing file on open, so an absent path would
	// silently become a valid empty database and the tick would succeed.
	src := filepath.Join(tmp, "src.db")
	if err := os.Mkdir(src, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	dir := filepath.Join(tmp, "backups")

	tick := time.Date(2026, 5, 1, 11, 0, 0, 0, time.UTC)
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	s := &backup.Scheduler{
		Enabled: true, Dir: dir, Interval: 24 * time.Hour,
		KeepDaily: 7, KeepWeekly: 4, KeepMonthly: 12, KeepCorrupt: 2,
		DBPath: src, BusyTimeout: 5 * time.Second,
		Now:    func() time.Time { return tick },
		Logger: log.New(io.Discard, "", 0),
	}
	// One tick via the public entry point, stopped immediately afterwards.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.RunLoop(ctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && s.Status().LastRunAt.IsZero() {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done

	if s.Status().LastRunAt.IsZero() {
		t.Fatal("the scheduler never completed a tick; the fixture is not exercising anything")
	}

	q, db := setupTestDB(t)
	h := NewHandlerWithClock(q, db, fixedClock{t: now})
	h.SetIntegrityResult(now, database.IntegrityOK)
	h.SetBackupStatusSource(s.Status)

	req := httptest.NewRequest(http.MethodGet, "/healthz/data", nil)
	rec := httptest.NewRecorder()
	h.handleHealthzData(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; a real scheduler that cannot read its source "+
			"database has produced no restore point; body = %s", rec.Code, rec.Body.String())
	}
	var resp healthzDataResponse
	decodeResponse(t, rec, &resp)
	if resp.Backups == nil {
		t.Fatal("backups block absent after wiring a real scheduler")
	}
	if resp.Backups.LastOutcome != string(backup.OutcomeError) {
		t.Errorf("LastOutcome = %q, want %q", resp.Backups.LastOutcome, backup.OutcomeError)
	}
	if resp.Backups.TrustedCount != 0 {
		t.Errorf("TrustedCount = %d, want 0", resp.Backups.TrustedCount)
	}
	if resp.Backups.IntervalSeconds != 86400 {
		t.Errorf("IntervalSeconds = %d, want 86400", resp.Backups.IntervalSeconds)
	}
}
