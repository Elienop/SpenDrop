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
// category name (resolved from category_id via a join) so that the import
// path and the startup backfill can both reach it without a lookup and
// still agree byte-for-byte.
//
// A rename DOES move the identity of every row filed under that category,
// and that is deliberate. The import path hashes the category's CURRENT
// name (import_handlers.go resolves the row's category and then reads
// CatIDToName), so a stored digest computed from the old name would no
// longer match anything the importer can produce — every row in the
// renamed category would silently re-import as new. handleUpdateCategory
// therefore clears content_hash for the affected rows (gated on
// CategoryRenameChangesHashes) and lets BackfillContentHashes re-anchor
// them under the new name on the next boot.
//
// This paragraph previously claimed the opposite — that the hash "must be
// stable even if the category is later renamed". That was never true of
// the import path it describes, and it is the reason the staleness went
// unnoticed. Do not restore it without changing the importer to match.
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
		normalizeHashDate(date),
		amountCents,
		normalizeHashText(description),
		normalizeHashText(categoryName),
	)
	return hex.EncodeToString(h.Sum(nil))
}

// normalizeHashDate and normalizeHashText are the two field normalizations
// ComputeContentHash applies. They are factored out so hashInputsMoved can
// answer "would ComputeContentHash produce a different digest?" using the
// SAME normalization rather than a hand-copied approximation that drifts the
// first time either rule changes.
func normalizeHashDate(d time.Time) string { return d.UTC().Format("2006-01-02") }

func normalizeHashText(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// HashInputs is the minimal set of columns ComputeContentHash actually
// consumes. It exists so a caller that does NOT hold a full transaction row —
// the api layer's bulk update-by-filter path enumerates rows with its own
// hand-written SELECT — can still ask the canonical question through
// HashInputsMoved instead of reimplementing the comparison.
//
// CategoryID rather than the category name: the hash mixes in the NAME, but a
// rename must NOT retroactively change a row's identity (see the note on
// ComputeContentHash), so id is the correct proxy for "this row now belongs to
// different content".
type HashInputs struct {
	Date        time.Time
	AmountCents int64
	Description string
	CategoryID  int64
}

// HashInputsMoved reports whether before and after differ in any input to
// ComputeContentHash — i.e. whether the digest would change. It is the single
// predicate behind every "should this write clear content_hash?" decision in
// the codebase; add a caller, not a second implementation.
//
// The comparison runs through ComputeContentHash's OWN normalizers
// (normalizeHashDate, normalizeHashText) rather than comparing raw values, so
// a change that the digest would erase — re-casing or re-padding a
// description, restating a date in a different location — correctly reports
// "not moved". Any future change to the hash's normalization rules therefore
// updates this predicate for free, which a hand-copied approximation would not.
func HashInputsMoved(before, after HashInputs) bool {
	switch {
	case before.CategoryID != after.CategoryID:
		return true
	case before.AmountCents != after.AmountCents:
		return true
	case normalizeHashDate(before.Date) != normalizeHashDate(after.Date):
		return true
	default:
		return normalizeHashText(before.Description) != normalizeHashText(after.Description)
	}
}

// hashInputsMoved adapts HashInputsMoved to the full-replace UpdateTransaction
// shape, and is the sole source of UpdateTransactionParams.ClearContentHash.
//
// UpdateTransaction rewrites every column on every call, but rewriting a
// column is not changing its value: a tags-only edit, a notes-only edit and a
// literal no-op Save all resend identical date / amount / description /
// category. Clearing the identity for those de-anchors the row from import
// dedupe for nothing, and a re-import of the file the row came from then
// creates a second copy — permanently, because BackfillContentHashes runs only
// at boot and only WHERE content_hash IS NULL, so once the duplicate owns the
// hash the partial unique index makes the backfill skip the edited row forever.
func hashInputsMoved(before GetTransactionByIDRow, after UpdateTransactionParams) bool {
	return HashInputsMoved(
		HashInputs{
			Date:        before.Date,
			AmountCents: before.AmountCents,
			Description: before.Description,
			CategoryID:  before.CategoryID,
		},
		HashInputs{
			Date:        after.Date,
			AmountCents: after.AmountCents,
			Description: after.Description,
			CategoryID:  after.CategoryID,
		},
	)
}

// CategoryRenameChangesHashes reports whether renaming a category from
// oldName to newName actually MOVES the ComputeContentHash digest of the
// transactions filed under it. It is the sole gate on
// Queries.ClearContentHashForCategory.
//
// It exists so the api layer cannot hand-copy the normalization. The hash
// mixes in lower(trim(category_name)), so "Food" -> "  FOOD  " renders
// different bytes in categories.name and an IDENTICAL digest: nothing about
// any row's identity moved, and clearing there would de-anchor the whole
// category from import dedupe for nothing — letting a re-import of the file
// those rows came from double all of them until the next boot re-anchors
// them. Comparing the raw strings would do exactly that. This is the same
// discipline as hashInputsMoved: decide from the prior VALUE, never from the
// shape of the statement.
func CategoryRenameChangesHashes(oldName, newName string) bool {
	return normalizeHashText(oldName) != normalizeHashText(newName)
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
// Error policy. The backfill distinguishes two error shapes:
//
//  1. A UNIQUE constraint violation on the partial index
//     idx_transactions_content_hash. This happens when a pre-existing
//     legacy row has the same (date, amount_cents, description,
//     category_name) as an earlier row that was already hashed in this
//     sweep — i.e. a legitimate duplicate-looking entry in the user's
//     own history (two coffees on the same day, for example). The
//     caller tolerates these: the earliest row (lowest id) keeps the
//     hash as the canonical anchor for future deduped imports, later
//     colliding rows keep content_hash = NULL and remain in the ledger
//     untouched. Skipping the hash on a legacy row only means future
//     imports of the same content dedupe against the canonical row,
//     not this skipped one — which is correct: both rows already exist,
//     and the user does not expect a retroactive identity change.
//
//  2. Any other error (a corrupted row, a disk failure, a timeout,
//     an unexpected constraint on another column) aborts the sweep
//     with the offending row's id in the message. A half-completed
//     backfill is safe — the query filter `content_hash IS NULL`
//     resumes cleanly on the next boot — so early abort is the right
//     behaviour: operators diagnose the real failure instead of silently
//     masking it.
//
// The original Phase 3.4 design (commit see migration 008) treated
// all UNIQUE collisions as operator errors that had to be
// hand-deduplicated before boot. That assumption proved wrong in
// practice: real household spreadsheets contain legitimate
// same-date/same-amount/same-description rows that normalize to the
// same hash, and the hard-abort turned a first-boot-after-upgrade
// into a crash loop the user could not escape without SQL surgery.
// The skip-on-collision behaviour preserves the partial index's value
// (future imports still dedupe against the canonical row) without
// making legacy data a boot blocker.
//
// Progress is logged once at start (with the computed deadline so an
// operator can see it in startup logs and grep for a slow run) and
// once at finish with the hashed-row count. If any rows were skipped
// on collision, an additional informational line lists a bounded
// preview of their ids so an operator can eyeball them — without
// flooding the log on a large legacy DB that contains many duplicates.
// No per-page logging — on a fresh container every boot would hit the
// backfill once (n=0 after completion) and per-page log noise would
// drown the real signal.
func BackfillContentHashes(ctx context.Context, db *sql.DB) error {
	q := New(db)

	// Quick pre-check: are there any rows to backfill at all? This
	// lets us skip the start/finish log lines (and the COUNT query is
	// cheap) on the steady-state boot where the backfill is a no-op.
	// We do not rely on this as an optimization for correctness — the
	// main loop below terminates on its own when the page comes back
	// empty — it only silences the logs.
	// Counts ALL un-hashed rows including tombstoned ones: the partial unique
	// index idx_transactions_content_hash covers tombstoned rows, so they must
	// be backfilled too. Deliberately no deleted_at filter (see the exemption
	// note in queries.sql).
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

	// cursor drives the id-based pagination. Every processed row
	// advances the cursor past itself, whether the UPDATE succeeded or
	// was skipped on UNIQUE. The query uses `t.id > ?` so pages are
	// monotonic in id, and a row left NULL on collision does not
	// reappear at the head of the next page (which would otherwise
	// spin the loop forever).
	var (
		cursor  int64
		done    int64
		skipped []int64
	)
	for {
		rows, err := q.ListTransactionsForHashBackfill(ctx, ListTransactionsForHashBackfillParams{
			ID:    cursor,
			Limit: backfillPageSize,
		})
		if err != nil {
			return fmt.Errorf("backfill content_hash: list page: %w", err)
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			hash := ComputeContentHash(row.Date, row.AmountCents, row.Description, row.CategoryName)
			err := q.UpdateTransactionContentHash(ctx, UpdateTransactionContentHashParams{
				ContentHash: sql.NullString{String: hash, Valid: true},
				ID:          row.ID,
			})
			switch {
			case err == nil:
				done++
			case IsContentHashUniqueViolation(err):
				skipped = append(skipped, row.ID)
			default:
				return fmt.Errorf("backfill content_hash: update row id=%d: %w", row.ID, err)
			}
			cursor = row.ID
		}
		// If the page came back short (fewer than the limit), there
		// are no more rows to process. Break before querying an empty
		// page to save one round-trip.
		if int64(len(rows)) < backfillPageSize {
			break
		}
	}

	log.Printf("Backfill content_hash: hashed %d rows", done)
	if len(skipped) > 0 {
		// A collision-skipped row is intact in the ledger — we only
		// withheld the derived hash. Print a bounded preview of ids so
		// operators can inspect specific rows without flooding the log
		// on a large legacy DB with many duplicates. This is
		// informational, not an error: the app is healthy.
		const maxIDsToLog = 20
		preview := skipped
		suffix := ""
		if len(preview) > maxIDsToLog {
			preview = preview[:maxIDsToLog]
			suffix = fmt.Sprintf(" (+%d more)", len(skipped)-maxIDsToLog)
		}
		log.Printf(
			"Backfill content_hash: %d legacy duplicate row(s) not hashed%s; ids=%v. "+
				"These rows match earlier rows byte-for-byte (date, amount, description, category) "+
				"and remain intact; future imports of the same content will dedupe against the "+
				"canonical row.",
			len(skipped), suffix, preview,
		)
	}
	return nil
}

// IsContentHashUniqueViolation reports whether err is a UNIQUE constraint
// violation on the partial index idx_transactions_content_hash. This is
// the exact-and-only error shape the backfill tolerates: any other
// UNIQUE violation (e.g. on users.username or categories.name) would
// indicate a real bug and must surface.
//
// The match is a substring check on the driver's error message —
// "UNIQUE constraint failed: transactions.content_hash" — because the
// qualifying column name is the whole point of the decision and
// sqlite3.Error does not expose it as a struct field. Even with
// errors.As against sqlite3.Error and sqlite3.ErrConstraintUnique,
// the only way to tell this index apart from users.username is to
// read the message, so the typed check would add ceremony without
// tightening the scope. The message format itself is stable: upstream
// SQLite emits it from sqlite3.c as "UNIQUE constraint failed: %s.%s",
// not driver-side, so a mattn version bump cannot silently change it.
//
// It is exported so the api layer can reuse this single detection
// chokepoint on the trash-restore path: restoring a tombstoned row whose
// content_hash now collides with a separately re-imported live row must
// be skipped-and-reported, not surfaced as an opaque 500 / batch rollback.
// The substring check survives the fmt.Errorf("restore transaction: %w", …)
// wrap in TransactionStore.Restore/RestoreTx because err.Error() includes
// the wrapped text.
func IsContentHashUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed: transactions.content_hash")
}
