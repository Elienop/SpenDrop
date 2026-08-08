package backup

// Verify is the Phase 1.3 "don't trust a backup until you've checked it"
// primitive. EVERY path that writes a sidecar runs Verify between Snapshot
// and WriteSidecar — the scheduler inline (Scheduler.runOnce) and Run for
// the CLI and pre-migration snapshots. A file that fails any check never
// gets a sidecar written, which is what every later phase uses as the
// "trusted" marker. A verify failure in the scheduler path also causes the
// file to be renamed to *.corrupt for operator forensics; Run instead
// removes the file, because the two paths deliberately differ in failure
// handling (see Run's doc comment) — never in whether they verify.
//
// The three checks are deliberately ordered by cost:
//
//  1. Size sanity (pure stat, microseconds).
//  2. PRAGMA integrity_check (CPU-bound, O(file size), still milliseconds
//     on hobbyist-scale DBs — acceptance criterion is <500 ms on a 10 MB
//     DB on a Pi 4).
//  3. Row parity against the source: either a COUNT(*) against
//     ExpectedTxCount (full-table scan, cheap with the implicit index on
//     INTEGER PRIMARY KEY), or — when the source had no "transactions"
//     table — a sqlite_master probe asserting the backup has none either.
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

// VerifyParams configures the checks Verify runs. Verify has no "skip the
// row count" sentinel because silently skipping a check is exactly the
// failure mode this file is built to prevent: a source with no
// "transactions" table is described by ExpectTransactionsAbsent, which is
// an ASSERTION (the backup must not have the table either), not a skip.
type VerifyParams struct {
	// ExpectedTxCount is the caller's measured row count of the live
	// "transactions" table just before the backup was taken. Verify fails
	// if the backup's count differs by more than RowCountTolerance.
	// Ignored — and required to be zero — when ExpectTransactionsAbsent
	// is set.
	ExpectedTxCount int64

	// ExpectTransactionsAbsent says the source had no "transactions"
	// table when the caller measured it, so the backup must not have one
	// either. This is NOT a skip sentinel and must never be treated as
	// one: with it set, Verify still runs a third check, it just asserts
	// parity of ABSENCE instead of parity of count. A backup that grew a
	// "transactions" table the source did not have is not a copy of that
	// source, and Verify fails.
	//
	// It exists for one real state: migration 001 creates "transactions",
	// and the pre-migration snapshot runs before any migration applies,
	// so on a first boot the source legitimately has no such table. Only
	// a caller that has probed the source may set this — inferring it
	// from a failed COUNT on the BACKUP would let a backup missing the
	// table excuse itself.
	ExpectTransactionsAbsent bool

	// RowCountTolerance is the maximum allowed difference (in absolute
	// terms) between the backup's count and ExpectedTxCount. Zero is
	// interpreted as 1, which permits at most one write to land between
	// the caller measuring the live count and VACUUM INTO taking its
	// snapshot — these two operations are not atomic with respect to one
	// another, so a tiny drift is normal, not corruption. Callers should
	// usually leave this at zero; a larger tolerance hides bugs.
	RowCountTolerance int64

	// MaxSize is the upper bound in bytes. Zero means no upper bound,
	// which is the correct value when the caller could not measure the
	// source. Every caller that can measure it sets this to 10× the live
	// source's on-disk size (main file + -wal), so a runaway growth (DB
	// corruption writing garbage pages, or a rogue import) gets caught.
	// The bound is deliberately NOT derived from the previous backup —
	// see the long comment in Scheduler.runOnce step 2 for the wedge that
	// caused.
	MaxSize int64

	// QueryBudget caps the wall-clock time the integrity_check and
	// row-count queries may take. Zero means defaultVerifyQueryTimeout.
	//
	// It is a parameter rather than a constant because the same 30s that
	// is generous for the scheduler (which logs a failure and tries again
	// next tick) is a boot-loop hazard on the pre-migration path, which
	// fails CLOSED: a legitimately large legacy database whose
	// integrity_check needs more than the budget would refuse to migrate,
	// every restart, forever. Callers on a fail-closed path size this
	// from the data via budgetForSize.
	QueryBudget time.Duration
}

// minBackupSize is one SQLite page. Anything smaller than this can only be
// a truncated or zero-byte file; it is not a user-adjustable knob — SQLite
// itself cannot produce a valid DB file smaller than this.
const minBackupSize = 4096

// defaultVerifyQueryTimeout is the budget used when VerifyParams.QueryBudget
// is zero. On a hobbyist-scale DB the integrity_check and row-count queries
// take milliseconds; anything approaching 30 seconds means something is
// badly wrong (runaway I/O, unexpectedly huge file) and we want to surface
// it as a failed verify rather than hang the scheduler goroutine. The
// scheduler leaves the budget at zero and gets exactly this: a tick that
// hangs costs one backup, and the next tick tries again.
const defaultVerifyQueryTimeout = 30 * time.Second

// Sizing constants for budgetForSize. They exist because a fail-closed
// caller cannot afford the fixed 30s default: on the pre-migration path a
// timeout is a refusal to migrate, which under Docker's restart policy is a
// boot loop the operator cannot escape without SQL surgery. That is the
// same class of hazard as the B3 migration-backfill deadline, which this
// repo has already shipped once.
const (
	// verifyBudgetFloor is the smallest budget a size-derived caller can
	// end up with. Generous on purpose: over-waiting costs a slow boot,
	// under-waiting costs the boot entirely.
	verifyBudgetFloor = 5 * time.Minute

	// verifyBudgetBytesPerMinute is the throughput the budget assumes.
	// 64 MiB/min is roughly 1 MB/s — one to two orders of magnitude below
	// what integrity_check achieves even on a spinning disk. The point is
	// not to predict the runtime, it is to make the budget grow with the
	// data so no fixed wall clock can be outgrown.
	verifyBudgetBytesPerMinute = 64 << 20

	// verifyBudgetCeiling caps the derived budget. Its job is arithmetic
	// safety, not policy: without it, a nonsense size (a corrupt stat, a
	// filesystem reporting exabytes) overflows time.Duration into a
	// NEGATIVE budget, which expires instantly and fails closed — the
	// exact direction this sizing exists to avoid. It is reached at ~90
	// GiB, far past any SpenDrop database.
	verifyBudgetCeiling = 24 * time.Hour
)

// budgetForSize returns the verification query budget for a source database
// of srcSize bytes: one minute per verifyBudgetBytesPerMinute (rounded up),
// clamped to [verifyBudgetFloor, verifyBudgetCeiling]. A non-positive size
// means "could not measure", which yields the floor rather than zero — an
// unmeasurable source must not silently produce a tighter budget than a
// measurable one.
func budgetForSize(srcSize int64) time.Duration {
	if srcSize <= 0 {
		return verifyBudgetFloor
	}
	minutes := srcSize / verifyBudgetBytesPerMinute
	if srcSize%verifyBudgetBytesPerMinute != 0 {
		minutes++
	}
	if minutes > int64(verifyBudgetCeiling/time.Minute) {
		return verifyBudgetCeiling
	}
	budget := time.Duration(minutes) * time.Minute
	if budget < verifyBudgetFloor {
		return verifyBudgetFloor
	}
	return budget
}

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
	// A caller that says the source had no "transactions" table cannot
	// also have counted rows in it. The combination means the caller
	// derived the two fields from different observations, so neither can
	// be trusted to describe the source — fail loud rather than pick one.
	if params.ExpectTransactionsAbsent && params.ExpectedTxCount != 0 {
		return fmt.Errorf("contradictory verify params: ExpectTransactionsAbsent with ExpectedTxCount=%d", params.ExpectedTxCount)
	}

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
		return fmt.Errorf("size too large: %d bytes > %d (10× the live source)", size, params.MaxSize)
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

	budget := params.QueryBudget
	if budget <= 0 {
		budget = defaultVerifyQueryTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
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

	// 3. Row parity. Two shapes, both assertions.
	//
	// 3a. Parity of ABSENCE. The source had no "transactions" table, so
	// the backup must not have one either. Querying sqlite_master rather
	// than letting COUNT(*) fail is what makes this a check instead of a
	// skip: "no such table" from the backup would otherwise be
	// indistinguishable from the backup having lost the table.
	if params.ExpectTransactionsAbsent {
		var tables int64
		const q = `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'transactions'`
		if err := db.QueryRowContext(ctx, q).Scan(&tables); err != nil {
			return fmt.Errorf("probe backup for transactions table: %w", err)
		}
		if tables != 0 {
			return fmt.Errorf(`backup has a "transactions" table but the source did not: the backup does not match its source`)
		}
		return nil
	}

	// 3b. Parity of COUNT. The tolerance accommodates writes that
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
