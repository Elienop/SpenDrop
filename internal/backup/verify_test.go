package backup

import (
	"context"
	"database/sql"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// snapshotWithoutTransactionsTable builds a source DB holding a table that
// is NOT "transactions", snapshots it, and returns the backup path. It is
// the shape of a first boot: migration 001 is what creates "transactions",
// and the pre-migration snapshot runs before any migration applies.
func snapshotWithoutTransactionsTable(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "pre-migration-src.db")
	dst := filepath.Join(tmp, "pre-migration-backup.db")

	srcDB, err := sql.Open("sqlite3", "file:"+src+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open src: %v", err)
	}
	defer srcDB.Close()
	// schema_migrations is what ensureMigrationsTable creates before
	// RunMigrations takes its snapshot, so this is literally the on-disk
	// shape of a first-boot source.
	if _, err := srcDB.Exec(`CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at DATETIME)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if err := Snapshot(context.Background(), src, testBusyTimeout, dst); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	return dst
}

// sparseFile creates a file of the given apparent size without allocating
// blocks for it. RunTimeout only ever STATS its argument, so a sparse file
// is a complete stand-in for a huge database and costs nothing — no valid
// SQLite header is needed or wanted here.
func sparseFile(t *testing.T, path string, size int64) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	if err := f.Truncate(size); err != nil {
		f.Close()
		t.Fatalf("truncate %s to %d: %v", path, size, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close %s: %v", path, err)
	}
}

// TestRunTimeout pins the caller-facing cap in both directions.
//
// The floor half matters because the CLI used to hard-code five minutes and
// every household-sized database must still land on a generous constant
// rather than on something the file size can shrink. The SCALING half is the
// entire reason RunTimeout exists at all — a version that ignored its
// argument and returned the floor would satisfy the floor cases alone, which
// is exactly the mutant the large-source subtests kill.
func TestRunTimeout(t *testing.T) {
	t.Run("missing source falls back to the floor", func(t *testing.T) {
		// The case that could silently produce a zero cap: a context
		// already expired before VACUUM INTO even starts.
		missing := filepath.Join(t.TempDir(), "does-not-exist.db")
		if got := RunTimeout(missing); got != 2*verifyBudgetFloor {
			t.Errorf("RunTimeout(missing) = %v, want %v", got, 2*verifyBudgetFloor)
		}
	})

	t.Run("household-scale source gets the floor", func(t *testing.T) {
		src := filepath.Join(t.TempDir(), "src.db")
		srcDB := populateSourceDB(t, src)
		defer srcDB.Close()
		if got := RunTimeout(src); got != 2*verifyBudgetFloor {
			t.Errorf("RunTimeout(household source) = %v, want the floor %v", got, 2*verifyBudgetFloor)
		}
	})

	t.Run("large source scales past the floor", func(t *testing.T) {
		src := filepath.Join(t.TempDir(), "big.db")
		sparseFile(t, src, 1<<30) // 1 GiB → 16 min of budget
		want := 2 * (16 * time.Minute)
		if got := RunTimeout(src); got != want {
			t.Errorf("RunTimeout(1 GiB source) = %v, want %v — the cap must grow with the data", got, want)
		}
	})

	t.Run("the -wal counts toward the size", func(t *testing.T) {
		// liveDBSize sums the main file and its -wal because
		// uncheckpointed pages live there and VACUUM INTO's output
		// includes them. A version that stats only the main file
		// returns half this, so the two sizes are chosen to give
		// distinct answers (1 GiB alone → 32 min; 2 GiB total → 64 min).
		dir := t.TempDir()
		src := filepath.Join(dir, "big.db")
		sparseFile(t, src, 1<<30)
		sparseFile(t, src+"-wal", 1<<30)
		want := 2 * (32 * time.Minute)
		if got := RunTimeout(src); got != want {
			t.Errorf("RunTimeout(1 GiB db + 1 GiB wal) = %v, want %v — the -wal must be counted", got, want)
		}
	})
}

// TestVerify_ExpectTransactionsAbsent_AcceptsBackupWithoutTable is the
// positive half of the absent-table arm: a caller that probed the source and
// found no "transactions" table gets a passing verify for a backup that
// likewise has none. Without this, Verify's hard-coded COUNT(*) would reject
// every first-boot pre-migration snapshot and the server would refuse to
// migrate a perfectly healthy new install.
func TestVerify_ExpectTransactionsAbsent_AcceptsBackupWithoutTable(t *testing.T) {
	dst := snapshotWithoutTransactionsTable(t)
	if err := Verify(dst, VerifyParams{ExpectTransactionsAbsent: true}); err != nil {
		t.Errorf("Verify with absence expectation: %v", err)
	}
}

// TestVerify_ExpectTransactionsAbsent_RejectsBackupWithTable is the whole
// reason ExpectTransactionsAbsent is an EXPECTATION and not a skip flag.
//
// A skip would make the third check vanish whenever the source happened to
// lack the table — and a caller that mis-probed the source (or a source
// that gained the table between the probe and the snapshot) would then get
// a sidecar on a file nobody checked. Here the backup demonstrably HAS a
// transactions table while the params say it should not, and Verify must
// fail. Flip the assertion in Verify to a no-op and only this test notices;
// every other test in this file still passes.
func TestVerify_ExpectTransactionsAbsent_RejectsBackupWithTable(t *testing.T) {
	// snapshotFromFixture's source has a "transactions" table with 3 rows,
	// so the backup has one too — the exact mismatch under test.
	_, dst := snapshotFromFixture(t)

	err := Verify(dst, VerifyParams{ExpectTransactionsAbsent: true})
	if err == nil {
		t.Fatal("expected error: backup has a transactions table the source did not")
	}
	if !strings.Contains(err.Error(), "transactions") {
		t.Errorf("error = %v, want mention of the transactions table", err)
	}
}

// TestVerify_ContradictoryParamsRejected pins the fail-loud guard on a
// caller that both claims the source had no transactions table AND reports
// a row count from it. The two can only come from different observations,
// so neither describes the source — picking one silently would be the
// "silently skipped a check" failure mode this file exists to prevent.
func TestVerify_ContradictoryParamsRejected(t *testing.T) {
	_, dst := snapshotFromFixture(t)
	err := Verify(dst, VerifyParams{ExpectTransactionsAbsent: true, ExpectedTxCount: 3})
	if err == nil {
		t.Fatal("expected error for contradictory params")
	}
	if !strings.Contains(err.Error(), "contradictory") {
		t.Errorf("error = %v, want 'contradictory'", err)
	}
}

// TestVerify_QueryBudgetIsHonoured proves VerifyParams.QueryBudget actually
// reaches the query context rather than being an ignored field.
//
// The assertion is only meaningful because TestVerify_Healthy proves this
// exact fixture verifies clean under the default budget: with the field
// ignored, this call would take the 30s default and PASS. A one-nanosecond
// budget is expired before the first query runs, so the only way to get an
// error here is for the field to be plumbed through.
func TestVerify_QueryBudgetIsHonoured(t *testing.T) {
	_, dst := snapshotFromFixture(t)
	err := Verify(dst, VerifyParams{ExpectedTxCount: 3, QueryBudget: time.Nanosecond})
	if err == nil {
		t.Fatal("expected error: a 1ns query budget cannot complete integrity_check")
	}
	if !strings.Contains(err.Error(), "deadline exceeded") {
		t.Errorf("error = %v, want a context deadline error", err)
	}
}

// TestVerify_ZeroQueryBudgetUsesDefault is the sentinel's positive control:
// zero must mean "use the default", not "no time at all". Reading the field
// straight into context.WithTimeout without the zero check would give every
// scheduler tick an already-expired context and fail every backup.
func TestVerify_ZeroQueryBudgetUsesDefault(t *testing.T) {
	_, dst := snapshotFromFixture(t)
	if err := Verify(dst, VerifyParams{ExpectedTxCount: 3, QueryBudget: 0}); err != nil {
		t.Errorf("Verify with zero budget: %v", err)
	}
}

// TestBudgetForSize pins the sizing rule a fail-closed caller depends on.
// The invariant it defends: no legitimately large database can outgrow the
// budget, and no input — including a nonsensical one — can produce a budget
// that is zero or negative, because either would fail closed instantly and
// turn a boot-path check into a crash loop.
func TestBudgetForSize(t *testing.T) {
	cases := []struct {
		name string
		size int64
		want time.Duration
	}{
		{"unmeasured source falls back to the floor", 0, verifyBudgetFloor},
		{"negative size cannot shrink the budget", -1, verifyBudgetFloor},
		{"household-scale DB gets the floor", 10 << 20, verifyBudgetFloor},
		{"exactly at the floor's break-even", 5 * verifyBudgetBytesPerMinute, verifyBudgetFloor},
		{"one byte past break-even rounds up a minute", 5*verifyBudgetBytesPerMinute + 1, 6 * time.Minute},
		{"1 GiB scales past the floor", 1 << 30, 16 * time.Minute},
		{"ceiling clamps an absurd size", 1 << 50, verifyBudgetCeiling},
		{"max int64 cannot overflow into a negative budget", math.MaxInt64, verifyBudgetCeiling},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := budgetForSize(c.size)
			if got != c.want {
				t.Errorf("budgetForSize(%d) = %v, want %v", c.size, got, c.want)
			}
			if got <= 0 {
				t.Errorf("budgetForSize(%d) = %v, must always be positive", c.size, got)
			}
		})
	}
}
