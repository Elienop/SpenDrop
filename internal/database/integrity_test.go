package database

import (
	"context"
	"database/sql"
	"os"
	"testing"
)

// TestIntegrityCheck_HealthyDB asserts that a freshly migrated database
// passes the full integrity check. This is the happy path the startup
// hook in main.go relies on — if this test ever flakes, the startup
// check becomes a boot-loop risk and needs investigation before it
// ships.
func TestIntegrityCheck_HealthyDB(t *testing.T) {
	db, dbPath := openTestDB(t)
	if err := RunMigrations(db, defaultMigrationOptions(t, dbPath)); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	got, err := IntegrityCheck(context.Background(), db)
	if err != nil {
		t.Fatalf("IntegrityCheck: %v", err)
	}
	if got != IntegrityOK {
		t.Errorf("IntegrityCheck on healthy db = %q, want %q", got, IntegrityOK)
	}
}

// TestQuickCheck_HealthyDB mirrors TestIntegrityCheck_HealthyDB for the
// fast pragma that /healthz/data runs per request. This is the same
// code path a monitoring scraper hits tens of thousands of times a day;
// any deviation from "ok" immediately flips the container to 503.
func TestQuickCheck_HealthyDB(t *testing.T) {
	db, dbPath := openTestDB(t)
	if err := RunMigrations(db, defaultMigrationOptions(t, dbPath)); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	got, err := QuickCheck(context.Background(), db)
	if err != nil {
		t.Fatalf("QuickCheck: %v", err)
	}
	if got != IntegrityOK {
		t.Errorf("QuickCheck on healthy db = %q, want %q", got, IntegrityOK)
	}
}

// TestRunIntegrityCheckAtStartup_HealthyDB is the function main.go calls
// during boot. The wrapper exists so callers have a named entry point
// and so tests can target the exact signature production uses; this
// test just confirms the wrapper still returns "ok" for a healthy DB
// after a future refactor might replace its body.
func TestRunIntegrityCheckAtStartup_HealthyDB(t *testing.T) {
	db, dbPath := openTestDB(t)
	if err := RunMigrations(db, defaultMigrationOptions(t, dbPath)); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	got, err := RunIntegrityCheckAtStartup(context.Background(), db)
	if err != nil {
		t.Fatalf("RunIntegrityCheckAtStartup: %v", err)
	}
	if got != IntegrityOK {
		t.Errorf("RunIntegrityCheckAtStartup on healthy db = %q, want %q", got, IntegrityOK)
	}
}

// TestIntegrityCheck_CorruptedDB corrupts a freshly-migrated SQLite file
// by overwriting its first page (the header) with 0xAA bytes and then
// reopening it. The full PRAGMA integrity_check is supposed to surface
// *something* other than "ok" — either the scan itself fails with an
// error, or it returns a multi-line list of errors, or the file fails
// to open at all. Any of those outcomes means RunIntegrityCheckAtStartup
// in main.go will exit non-zero, which is the guarantee this test
// defends.
//
// We cannot assert on the exact corruption message because SQLite's
// quick_check / integrity_check error wording has changed across minor
// versions; asserting on it would be a test-maintenance tax for zero
// additional safety. The contract we defend is:
//
//	err != nil || result != "ok" || open itself fails
//
// which matches exactly what main.go must treat as fatal.
func TestIntegrityCheck_CorruptedDB(t *testing.T) {
	db, dbPath := openTestDB(t)
	if err := RunMigrations(db, defaultMigrationOptions(t, dbPath)); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	// Close the handle before scribbling over the file — SQLite caches
	// the header page and holds the file open, so an in-place overwrite
	// against a live connection would be a race. The t.Cleanup close
	// will become a no-op; that's fine.
	if err := db.Close(); err != nil {
		t.Fatalf("close before corruption: %v", err)
	}

	// Remove the WAL / SHM sidecars so the reopened handle cannot
	// recover the pre-corruption state from the write-ahead log. Without
	// this step the integrity check happily replays the WAL and reports
	// "ok", which would defeat the test.
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(dbPath + suffix)
	}

	// Rewrite the first 4096 bytes (one page) with garbage that no valid
	// SQLite header will survive. 0xAA is chosen so the header magic
	// string "SQLite format 3\000" is unambiguously destroyed.
	f, err := os.OpenFile(dbPath, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("open db file for corruption: %v", err)
	}
	garbage := make([]byte, 4096)
	for i := range garbage {
		garbage[i] = 0xAA
	}
	if _, err := f.WriteAt(garbage, 0); err != nil {
		_ = f.Close()
		t.Fatalf("corrupt header page: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close corrupted file: %v", err)
	}

	// Reopen the corrupted DB without WAL journaling so a corrupted
	// header cannot be masked by a sidecar replay. If the driver
	// refuses to open the file at all that is itself a valid
	// "startup fails loudly" outcome and counts as a pass.
	reopened, err := sql.Open("sqlite3", dbPath+"?_journal_mode=OFF&_busy_timeout=5000")
	if err != nil {
		return
	}
	defer reopened.Close()
	// Ping can expose the bad header immediately on some builds, before
	// we even reach the pragma. A failed ping also counts as a pass.
	if err := reopened.Ping(); err != nil {
		return
	}

	result, err := IntegrityCheck(context.Background(), reopened)
	if err == nil && result == IntegrityOK {
		t.Errorf("IntegrityCheck on corrupted db returned %q; want error or non-ok result", result)
		return
	}
	if err != nil {
		t.Logf("IntegrityCheck surfaced corruption as query error (accepted): %v", err)
		return
	}
	t.Logf("IntegrityCheck surfaced corruption as non-ok result (accepted): %q", result)
}
