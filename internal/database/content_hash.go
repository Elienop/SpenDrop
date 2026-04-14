package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"time"
)

// ComputeContentHash returns the SHA-256 hex digest of the normalized
// identity of a transaction row. It is the single source of truth for
// Phase 3.4's idempotent-import behaviour — both the import path (when
// inserting a row) and the startup backfill (when hashing a legacy row)
// go through this function, so their hashes agree byte-for-byte.
//
// The normalized form is:
//
//	YYYY-MM-DD|amount_cents|lower(trim(description))|lower(trim(category_name))
//
// Design notes on each field:
//
//   - Date is formatted in ISO-8601 so a transaction imported under any
//     timezone-offset quirk still maps to the same string (the import
//     path stores dates as UTC midnight, and the Format call here
//     re-renders them the same way regardless of the time.Time's
//     Location).
//   - amount_cents is the integer cents column (Phase 3.1a), not the
//     legacy REAL amount. The REAL column drifts under float rounding
//     and two successive imports of the same float can produce
//     different bytes; the INTEGER column is exact.
//   - Description and category name are trimmed of surrounding
//     whitespace and lowercased. Users fat-finger "  Starbucks " vs
//     "Starbucks" and shouldn't get a double entry for it; the same
//     goes for case (Excel auto-capitalizes the first letter of a
//     cell on some locales).
//   - The pipe delimiter is unambiguous because the fields it separates
//     have fixed shapes (date is exactly 10 chars, amount_cents is a
//     decimal integer, and the final two fields — description and
//     category name — do contain user text but collisions between
//     "a|b" and "a","|b" would require the description to end with a
//     literal pipe, which is rare and does not harm correctness: a
//     false-positive dedupe collapses two rows that already looked
//     identical to a human reading the spreadsheet.
//
// The function does not touch the database; the caller passes in the
// category name (resolved from category_id via a join) because the
// content hash must be stable even if the category is later renamed.
// A rename should not retroactively change a row's identity.
//
// The date is normalized to UTC before formatting. The current callers
// (import path, backfill) both store or read UTC midnight already, so
// this is a no-op for them — but a future caller that passes a
// non-UTC time.Time with a near-midnight value could otherwise straddle
// a date boundary under Format and silently produce a different hash
// for what the user sees as the same day.
func ComputeContentHash(date time.Time, amountCents int64, description, categoryName string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%d|%s|%s",
		date.UTC().Format("2006-01-02"),
		amountCents,
		strings.ToLower(strings.TrimSpace(description)),
		strings.ToLower(strings.TrimSpace(categoryName)),
	)
	return hex.EncodeToString(h.Sum(nil))
}

// backfillPageSize is the number of rows BackfillContentHashes pulls
// per iteration. Small enough that a single page fits comfortably in
// RAM, large enough that a 10K-row household database finishes in ~10
// iterations. The value is not a tuning knob — the backfill runs once
// per process lifetime per row (rows with content_hash already set
// are skipped by the WHERE clause), so page size only influences the
// first-boot-after-migration latency, not steady-state cost.
const backfillPageSize = 1000

// backfillPerRowBudget is the deadline allowance per pending row. SQLite
// WAL typically delivers a per-row autocommit UPDATE in ~0.5-1 ms at
// household scale, so 5 ms is a generous safety factor that still
// prevents the backfill from spinning forever on a corrupted or
// pathological row.
const backfillPerRowBudget = 5 * time.Millisecond

// backfillFloorBudget is the minimum wall-clock deadline the backfill
// gets regardless of how few rows are pending. A freshly-created DB
// with a handful of rows should still get a bounded deadline so a
// hanging query cannot stall boot forever.
const backfillFloorBudget = 5 * time.Minute

// BackfillContentHashes hashes every transaction row whose content_hash
// column is still NULL and writes the result back into the same row. It
// is called from main.go on every startup after RunMigrations completes,
// and is the reason migration 008 ships the partial unique index with a
// `WHERE content_hash IS NOT NULL` clause: on the first boot after the
// migration applies, legacy rows still have NULL hashes and would
// collide under a non-partial index.
//
// The function is idempotent: the query filter `content_hash IS NULL`
// skips any row that has already been hashed, so a crash mid-sweep
// resumes cleanly on the next boot. It is also safe to run after the
// backfill has finished — the first page comes back empty and the loop
// exits without touching the database.
//
// Deadline: the function sizes its own timeout from the pre-count
// rather than accepting a caller-supplied deadline. A fixed five-minute
// wall-clock (the original shape of this call) was a boot loop hazard
// on a genuinely large legacy DB: at ~500 rows/sec the ceiling was
// around 150K rows, and a multi-year household import can cross that
// line. The proportional budget (5 ms/row, floor 5 min) scales with
// the workload, so a million-row DB gets ~83 minutes — well above any
// realistic household, comfortably absorbing a slow disk, and still
// bounded in case a query truly hangs.
//
// The backfill runs serially, not in a transaction per page: SQLite's
// single-writer rule means a large transaction would block legitimate
// app writes (from the HTTP handlers on the same db handle) for the
// duration of the sweep. Per-row autocommit adds write amplification
// but keeps the latency of concurrent handler writes bounded to a few
// milliseconds. At household scale the whole sweep is sub-second.
//
// Errors are grouped: an UpdateTransactionContentHash failure on any
// single row aborts the sweep with that row's id in the error message,
// so operators can diagnose (e.g. a corrupted row that can't be
// updated) without having silently half-completed state. The partial
// unique index means any hash collision — which would be a semantic
// bug in ComputeContentHash or a genuine byte-identical duplicate —
// surfaces as a UNIQUE constraint error on the UPDATE, which is the
// right place for it: the index is doing its job, and the operator
// has to deduplicate the legacy rows by hand before the next boot.
//
// Progress is logged once at start (with the computed deadline so an
// operator can see it in startup logs and grep for a slow run) and
// once at finish with the row count. No per-page logging — on a
// fresh container every boot would hit the backfill once (n=0 after
// completion) and per-page log noise would drown the real signal.
func BackfillContentHashes(ctx context.Context, db *sql.DB) error {
	q := New(db)

	// Quick pre-check: are there any rows to backfill at all? This
	// lets us skip the start/finish log lines (and the COUNT query is
	// cheap) on the steady-state boot where the backfill is a no-op.
	// We do not rely on this as an optimization for correctness — the
	// main loop below terminates on its own when the page comes back
	// empty — it only silences the logs.
	var pending int64
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM transactions WHERE content_hash IS NULL`,
	).Scan(&pending); err != nil {
		return fmt.Errorf("backfill content_hash: count pending: %w", err)
	}
	if pending == 0 {
		return nil
	}

	// Size the deadline from the pending row count. A caller-supplied
	// timeout is deliberately ignored here — callers pass context.Background()
	// and we own our own deadline, because the pre-count is the only
	// number that knows how much work is actually waiting.
	budget := time.Duration(pending) * backfillPerRowBudget
	if budget < backfillFloorBudget {
		budget = backfillFloorBudget
	}
	log.Printf("Backfill content_hash: %d rows pending, deadline=%s", pending, budget)

	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	var done int64
	for {
		rows, err := q.ListTransactionsForHashBackfill(ctx, backfillPageSize)
		if err != nil {
			return fmt.Errorf("backfill content_hash: list page: %w", err)
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			hash := ComputeContentHash(row.Date, row.AmountCents, row.Description, row.CategoryName)
			if err := q.UpdateTransactionContentHash(ctx, UpdateTransactionContentHashParams{
				ContentHash: sql.NullString{String: hash, Valid: true},
				ID:          row.ID,
			}); err != nil {
				return fmt.Errorf("backfill content_hash: update row id=%d: %w", row.ID, err)
			}
			done++
		}
		// If the page came back short (fewer than the limit), there
		// are no more rows to process. Break before querying an empty
		// page to save one round-trip.
		if int64(len(rows)) < backfillPageSize {
			break
		}
	}

	log.Printf("Backfill content_hash: hashed %d rows", done)
	return nil
}
