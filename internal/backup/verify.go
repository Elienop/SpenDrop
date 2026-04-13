package backup

// Verify is the Phase 1.3 "don't trust a backup until you've checked it"
// primitive. Every scheduled backup runs Verify between Snapshot and
// WriteSidecar; a file that fails any check never gets a sidecar written,
// which is what every later phase uses as the "trusted" marker. A verify
// failure in the scheduler path also causes the file to be renamed to
// *.corrupt for operator forensics — see Scheduler.runOnce.
//
// The three checks are deliberately ordered by cost:
//
//  1. Size sanity (pure stat, microseconds).
//  2. PRAGMA integrity_check (CPU-bound, O(file size), still milliseconds
//     on hobbyist-scale DBs — acceptance criterion is <500 ms on a 10 MB
//     DB on a Pi 4).
//  3. Row-count parity against ExpectedTxCount (full-table COUNT(*), cheap
//     with the implicit index on INTEGER PRIMARY KEY).
//
// Failing on the cheap check avoids opening SQLite at all for obviously
// broken files (truncated, zero bytes, wildly oversized).

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// VerifyParams configures the checks Verify runs. All fields are optional
// except ExpectedTxCount — Verify has no "skip the row count" sentinel
// because silently skipping a check is exactly the failure mode this file
// is built to prevent.
type VerifyParams struct {
	// ExpectedTxCount is the caller's measured row count of the live
	// "transactions" table just before the backup was taken. Verify fails
	// if the backup's count differs by more than RowCountTolerance.
	ExpectedTxCount int64

	// RowCountTolerance is the maximum allowed difference (in absolute
	// terms) between the backup's count and ExpectedTxCount. Zero is
	// interpreted as 1, which permits at most one write to land between
	// the caller measuring the live count and VACUUM INTO taking its
	// snapshot — these two operations are not atomic with respect to one
	// another, so a tiny drift is normal, not corruption. Callers should
	// usually leave this at zero; a larger tolerance hides bugs.
	RowCountTolerance int64

	// MaxSize is the upper bound in bytes. Zero means no upper bound,
	// which is the correct value for the first-ever backup in a new
	// directory where no baseline exists to compare against. The
	// scheduler sets this to 10× the previous successful backup's size
	// so a runaway growth (DB corruption writing garbage pages, or a
	// rogue import) gets caught.
	MaxSize int64
}

// minBackupSize is one SQLite page. Anything smaller than this can only be
// a truncated or zero-byte file; it is not a user-adjustable knob — SQLite
// itself cannot produce a valid DB file smaller than this.
const minBackupSize = 4096

// verifyQueryTimeout is the budget for running the integrity_check and
// row-count queries against the backup. On a hobbyist-scale DB these take
// milliseconds; anything approaching 30 seconds means something is badly
// wrong (runaway I/O, unexpectedly huge file) and we want to surface it as
// a failed verify rather than hang the scheduler goroutine.
const verifyQueryTimeout = 30 * time.Second

// Verify runs Phase 1.3's three checks against the backup file at path and
// returns a descriptive error for the first one that fails. It opens the
// backup read-only so no side effect can leak from a corrupt file into the
// caller's environment.
//
// The backup is always in SQLite's default DELETE journal mode — VACUUM
// INTO produces a clean, WAL-less file — so we can safely open with
// mode=ro without worrying about orphan -wal files confusing the journal
// replay logic.
func Verify(path string, params VerifyParams) error {
	// 1. Size sanity. Cheap: one stat call, no SQLite open.
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat backup: %w", err)
	}
	size := info.Size()
	if size < minBackupSize {
		return fmt.Errorf("size too small: %d bytes < %d (one SQLite page)", size, minBackupSize)
	}
	if params.MaxSize > 0 && size > params.MaxSize {
		return fmt.Errorf("size too large: %d bytes > %d (10× previous backup)", size, params.MaxSize)
	}

	// 2. Integrity check. Open the backup read-only. mode=ro tells
	// go-sqlite3 / SQLite to open with SQLITE_OPEN_READONLY and not
	// create the file if missing. We also set _query_only=1 as
	// defense-in-depth so even a programming error in this function
	// cannot mutate the backup file. The busy_timeout is set to a
	// safe constant rather than threaded from VerifyParams because a
	// backup file has no other readers or writers by construction
	// (VACUUM INTO just produced it; the scheduler is its only user)
	// — the timeout exists only so an unexpected NFS/NAS lock would
	// time out cleanly rather than blocking the scheduler goroutine
	// indefinitely.
	dsn := fmt.Sprintf("file:%s?mode=ro&_query_only=1&_busy_timeout=5000", path)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return fmt.Errorf("open backup for verify: %w", err)
	}
	defer db.Close()
	// Pin to a single connection: PRAGMA and SELECT must run against
	// the same connection or SQLite will not see a consistent view.
	db.SetMaxOpenConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), verifyQueryTimeout)
	defer cancel()

	// PRAGMA integrity_check returns a single row "ok" for a healthy
	// file, or a list of error rows for a broken one. We only scan the
	// first row: if SQLite produced more than one, at least one of them
	// is not "ok" by definition, and QueryRow grabs the first. The plan
	// could theoretically want all rows, but a single "not ok" is the
	// signal we care about and short-circuiting is friendlier to the
	// log output.
	var status string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&status); err != nil {
		return fmt.Errorf("run integrity_check: %w", err)
	}
	if status != "ok" {
		return fmt.Errorf("integrity_check returned %q", status)
	}

	// 3. Row count parity. The tolerance accommodates writes that
	// committed between the caller measuring the live count and VACUUM
	// INTO taking its snapshot; default is 1, which is documented as a
	// deliberate-not-sloppy judgement call in the data-stewardship plan.
	tolerance := params.RowCountTolerance
	if tolerance <= 0 {
		tolerance = 1
	}
	var backupCount int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM transactions").Scan(&backupCount); err != nil {
		return fmt.Errorf("count transactions in backup: %w", err)
	}
	diff := backupCount - params.ExpectedTxCount
	if diff < 0 {
		diff = -diff
	}
	if diff > tolerance {
		return fmt.Errorf("row count drift too large: backup=%d expected=%d (diff=%d > tolerance=%d)",
			backupCount, params.ExpectedTxCount, diff, tolerance)
	}

	return nil
}
