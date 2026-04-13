package backup

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// snapshotFromFixture creates a fresh tempdir, seeds a source DB via
// populateSourceDB (three rows in a "transactions" table, WAL mode), takes a
// VACUUM INTO snapshot to dst, and returns the src/dst paths. Tests that
// want a healthy backup call this as-is; tests that want a corrupted backup
// call this and then mutate dst on disk. The helper closes the source DB
// before returning so callers don't have to juggle a handle they don't use.
func snapshotFromFixture(t *testing.T) (src, dst string) {
	t.Helper()
	tmp := t.TempDir()
	src = filepath.Join(tmp, "src.db")
	dst = filepath.Join(tmp, "backup.db")
	srcDB := populateSourceDB(t, src)
	defer srcDB.Close()
	if err := Snapshot(context.Background(), src, testBusyTimeout, dst); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	return src, dst
}

func TestVerify_Healthy(t *testing.T) {
	_, dst := snapshotFromFixture(t)
	if err := Verify(dst, VerifyParams{ExpectedTxCount: 3}); err != nil {
		t.Errorf("Verify healthy backup: %v", err)
	}
}

func TestVerify_RowCountOffByOneBelow(t *testing.T) {
	_, dst := snapshotFromFixture(t)
	// Backup has 3 rows; expected=2; |diff|=1 is within the default tolerance.
	if err := Verify(dst, VerifyParams{ExpectedTxCount: 2}); err != nil {
		t.Errorf("Verify diff=1 (below expected): %v", err)
	}
}

func TestVerify_RowCountOffByOneAbove(t *testing.T) {
	_, dst := snapshotFromFixture(t)
	// Backup has 3 rows; expected=4; |diff|=1 is within the default tolerance.
	// Covers the diff<0 branch of the absolute-value calculation.
	if err := Verify(dst, VerifyParams{ExpectedTxCount: 4}); err != nil {
		t.Errorf("Verify diff=1 (above expected): %v", err)
	}
}

func TestVerify_RowCountMismatch(t *testing.T) {
	_, dst := snapshotFromFixture(t)
	err := Verify(dst, VerifyParams{ExpectedTxCount: 10})
	if err == nil {
		t.Fatal("expected row count drift error")
	}
	if !strings.Contains(err.Error(), "row count") {
		t.Errorf("error = %v, want mention of row count", err)
	}
}

func TestVerify_ExplicitToleranceRespected(t *testing.T) {
	_, dst := snapshotFromFixture(t)
	// Backup has 3 rows; expected=0; diff=3; tolerance=5 → accept.
	// Confirms the caller can widen tolerance past the default of 1.
	if err := Verify(dst, VerifyParams{ExpectedTxCount: 0, RowCountTolerance: 5}); err != nil {
		t.Errorf("Verify with tolerance=5: %v", err)
	}
}

func TestVerify_TooSmall(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "tiny.db")
	// One byte is unambiguously smaller than one SQLite page. Using a
	// literal single byte instead of an empty file exercises the "file
	// exists but is truncated" branch, which is the realistic failure
	// mode (a truly zero-byte file would be suspicious in its own right).
	if err := os.WriteFile(path, []byte{0}, 0o644); err != nil {
		t.Fatalf("write tiny file: %v", err)
	}
	err := Verify(path, VerifyParams{ExpectedTxCount: 3})
	if err == nil {
		t.Fatal("expected size too small error")
	}
	if !strings.Contains(err.Error(), "size too small") {
		t.Errorf("error = %v, want 'size too small'", err)
	}
}

func TestVerify_TooLarge(t *testing.T) {
	_, dst := snapshotFromFixture(t)
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat dst: %v", err)
	}
	// MaxSize one byte below the actual file size guarantees the
	// too-large branch fires. The snapshot is always at least a few SQLite
	// pages so info.Size()-1 is still comfortably above minBackupSize,
	// and the too-small branch is not triggered.
	err = Verify(dst, VerifyParams{ExpectedTxCount: 3, MaxSize: info.Size() - 1})
	if err == nil {
		t.Fatal("expected size too large error")
	}
	if !strings.Contains(err.Error(), "size too large") {
		t.Errorf("error = %v, want 'size too large'", err)
	}
}

func TestVerify_MaxSizeZeroSkipsUpperBound(t *testing.T) {
	_, dst := snapshotFromFixture(t)
	// MaxSize=0 is the documented "no upper bound" sentinel — it is the
	// correct value for the first-ever backup in a new directory. Verify
	// must not reject a legitimately-sized backup on the first run.
	if err := Verify(dst, VerifyParams{ExpectedTxCount: 3, MaxSize: 0}); err != nil {
		t.Errorf("Verify with MaxSize=0: %v", err)
	}
}

func TestVerify_MissingFile(t *testing.T) {
	tmp := t.TempDir()
	err := Verify(filepath.Join(tmp, "nonexistent.db"), VerifyParams{ExpectedTxCount: 3})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "stat backup") {
		t.Errorf("error = %v, want 'stat backup'", err)
	}
}

func TestVerify_CorruptedPage(t *testing.T) {
	_, dst := snapshotFromFixture(t)

	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat dst: %v", err)
	}
	// We need at least two pages to meaningfully corrupt page 2. A 3-row
	// snapshot is always multiple pages, but be defensive so a future
	// SQLite change to VACUUM INTO layout cannot silently neuter the test.
	if info.Size() < 2*minBackupSize {
		t.Fatalf("backup too small to corrupt page 2: %d bytes", info.Size())
	}

	// Zero out page 2 (offset 4096, one full page). Page 1 always holds
	// the database header and the sqlite_schema B-tree root in SQLite
	// 3.x; on a tiny DB like this fixture, page 2 typically holds the
	// root (or sole page) of the first user table's B-tree. Zeroing a
	// full page in that region is reliably detected by PRAGMA
	// integrity_check or the SELECT COUNT(*) query on every SQLite 3.x
	// we care about; if that ever changes, upgrade this test to damage
	// a leaf page at a later offset instead.
	f, err := os.OpenFile(dst, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open dst: %v", err)
	}
	if _, err := f.WriteAt(make([]byte, 4096), 4096); err != nil {
		f.Close()
		t.Fatalf("zero page 2: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close dst: %v", err)
	}

	// We intentionally do NOT assert which specific branch fires. Which
	// page the corruption lands on and which check notices first is a
	// function of SQLite's internal layout; pinning the test to one error
	// string would couple it to SQLite internals. All that matters for the
	// contract is that corruption produces a non-nil error.
	if err := Verify(dst, VerifyParams{ExpectedTxCount: 3}); err == nil {
		t.Fatal("expected error on corrupted backup")
	}
}

func TestVerify_NotASQLiteFile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "garbage.db")
	// 8 KiB of 0xFF passes the size check but has no valid SQLite
	// header — the first query against it should error out.
	garbage := make([]byte, 2*minBackupSize)
	for i := range garbage {
		garbage[i] = 0xFF
	}
	if err := os.WriteFile(path, garbage, 0o644); err != nil {
		t.Fatalf("write garbage: %v", err)
	}
	err := Verify(path, VerifyParams{ExpectedTxCount: 3})
	if err == nil {
		t.Fatal("expected error for non-SQLite file")
	}
	// Pin the failing step. A non-SQLite header must be caught by either
	// the integrity_check query, the row-count query, or the open/prepare
	// path — but never by the size step (the file is 2*minBackupSize,
	// well above the minimum). If a future go-sqlite3 or SQLite change
	// lets one of the SQLite-touching steps silently pass, this
	// assertion is what flags the regression — a bare `err != nil` would
	// not.
	msg := err.Error()
	if !strings.Contains(msg, "integrity_check") &&
		!strings.Contains(msg, "count transactions") &&
		!strings.Contains(msg, "open backup") {
		t.Errorf("error = %v, want one of: integrity_check / count transactions / open backup", err)
	}
}

func TestVerify_NoTransactionsTable(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "other.db")
	dst := filepath.Join(tmp, "other-backup.db")

	// Build a source DB with a different schema — no "transactions" table.
	// Verify hard-codes that table name; a backup of a differently-shaped
	// DB must fail the row-count query, not silently pass.
	srcDB, err := sql.Open("sqlite3", "file:"+src+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open src: %v", err)
	}
	defer srcDB.Close()
	if _, err := srcDB.Exec(`CREATE TABLE other (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := srcDB.Exec(`INSERT INTO other (id) VALUES (1), (2), (3)`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := Snapshot(context.Background(), src, testBusyTimeout, dst); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	err = Verify(dst, VerifyParams{ExpectedTxCount: 3})
	if err == nil {
		t.Fatal("expected error for missing transactions table")
	}
	if !strings.Contains(err.Error(), "count transactions") {
		t.Errorf("error = %v, want 'count transactions'", err)
	}
}
