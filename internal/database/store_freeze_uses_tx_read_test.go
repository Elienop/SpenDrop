package database

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// seedForeignTestTransaction inserts a foreign-currency row with its content
// hash anchored on the values it is being created with, so a later assertion
// that the hash SURVIVED an update is meaningful rather than vacuous.
func seedForeignTestTransaction(
	t *testing.T, q *Queries, userID, categoryID int64, categoryName, date, desc string,
	amountCents, origAmountCents int64, origCurrency string,
) int64 {
	t.Helper()
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		t.Fatalf("parse seed date %q: %v", date, err)
	}
	tt, err := q.CreateTransaction(context.Background(), CreateTransactionParams{
		UserID:              userID,
		Date:                d,
		AmountCents:         amountCents,
		OriginalAmountCents: sql.NullInt64{Int64: origAmountCents, Valid: true},
		OriginalCurrency:    sql.NullString{String: origCurrency, Valid: true},
		Description:         desc,
		CategoryID:          categoryID,
		ContentHash: sql.NullString{
			String: ComputeContentHash(d, amountCents, desc, categoryName),
			Valid:  true,
		},
	})
	if err != nil {
		t.Fatalf("seedForeignTestTransaction: %v", err)
	}
	return tt.ID
}

// TestUpdate_RestatedForeignMoney_FreezesToTheTxScopedRow pins WHICH read the
// carried-forward amount comes from.
//
// handleUpdateTransaction loads the row outside any transaction (for its
// tombstone and ownership checks) and hands UpdateTransactionParams to the
// store, which loads the row AGAIN inside its own transaction and derives
// ClearContentHash from that second read. The freeze therefore has to be
// decided from the store's read too, or the two disagree: the store would write
// an amount matching neither the request nor the `before` row it just read, and
// hashInputsMoved would see that phantom move and clear content_hash — dropping
// the row out of import dedupe, the exact failure the freeze exists to prevent.
//
// SetMaxOpenConns(1) does not close the window; it serialises statements, not a
// read-then-open-transaction sequence.
//
// The caller here hands in a stale AmountCents (1685) against a row that holds
// 1798, restating the same foreign money. Both assertions fail if the decision
// is made from the caller's copy.
func TestUpdate_RestatedForeignMoney_FreezesToTheTxScopedRow(t *testing.T) {
	db, store, q := newTestStore(t)
	ctx := context.Background()

	userID := seedTestStoreUser(t, q, "owner-"+t.Name())
	catName := "Groceries-" + t.Name()
	catID := seedTestStoreCategory(t, q, catName)
	date, _ := time.Parse("2006-01-02", "2026-04-06")

	// The row as it stands NOW: 1,500,000 LBP recorded as $17.98.
	id := seedForeignTestTransaction(
		t, q, userID, catID, catName, "2026-04-06", "Spinneys", 1798, 150_000_000, "LBP")
	hashBefore := contentHashOf(t, db, id)
	if !hashBefore.Valid {
		t.Fatal("precondition: the seeded row should carry a content_hash")
	}

	// A caller holding a stale snapshot: same foreign money restated, but the
	// base value it carries (1685) predates the row's current 1798.
	err := store.Update(ctx, userID, UpdateTransactionParams{
		ID:                  id,
		Date:                date,
		AmountCents:         1685,
		OriginalAmountCents: sql.NullInt64{Int64: 150_000_000, Valid: true},
		OriginalCurrency:    sql.NullString{String: "LBP", Valid: true},
		Description:         "Spinneys",
		CategoryID:          catID,
		Tags:                sql.NullString{String: "weekly", Valid: true},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	after, err := q.GetTransactionByID(ctx, id)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if after.AmountCents != 1798 {
		t.Errorf("amount_cents = %d, want 1798 — the store wrote the caller's stale amount "+
			"instead of the one its own transaction-scoped read returned", after.AmountCents)
	}
	if hashAfter := contentHashOf(t, db, id); !hashAfter.Valid || hashAfter.String != hashBefore.String {
		t.Errorf("content_hash = %v, want it unchanged at %q — no hash input moved, so a cleared "+
			"hash means the amount decision and the hash decision disagreed",
			hashAfter, hashBefore.String)
	}

	// Not vacuous: the edit really did land.
	if after.Tags.String != "weekly" {
		t.Fatalf("tags = %v — the update never landed, so the assertions above prove nothing", after.Tags)
	}
}

// TestUpdate_CorrectedForeignMoney_KeepsTheCallersAmount is the other side of
// the store-level predicate. When the foreign amount genuinely moved
// (1,500,000 -> 1,600,000 LBP) the caller's freshly converted value is the
// right one and must be written through untouched — a store that froze every
// foreign row would silently discard every amount correction.
func TestUpdate_CorrectedForeignMoney_KeepsTheCallersAmount(t *testing.T) {
	db, store, q := newTestStore(t)
	ctx := context.Background()

	userID := seedTestStoreUser(t, q, "owner-"+t.Name())
	catName := "Groceries-" + t.Name()
	catID := seedTestStoreCategory(t, q, catName)
	date, _ := time.Parse("2006-01-02", "2026-04-06")

	id := seedForeignTestTransaction(
		t, q, userID, catID, catName, "2026-04-06", "Spinneys", 1685, 150_000_000, "LBP")

	err := store.Update(ctx, userID, UpdateTransactionParams{
		ID:                  id,
		Date:                date,
		AmountCents:         1798,
		OriginalAmountCents: sql.NullInt64{Int64: 160_000_000, Valid: true},
		OriginalCurrency:    sql.NullString{String: "LBP", Valid: true},
		Description:         "Spinneys",
		CategoryID:          catID,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	after, err := q.GetTransactionByID(ctx, id)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if after.AmountCents != 1798 {
		t.Errorf("amount_cents = %d, want 1798 — a corrected foreign amount must re-price", after.AmountCents)
	}
	if !after.OriginalAmountCents.Valid || after.OriginalAmountCents.Int64 != 160_000_000 {
		t.Errorf("original_amount_cents = %v, want 160000000", after.OriginalAmountCents)
	}
	// The amount IS a hash input, so a real move must un-anchor the row.
	if hashAfter := contentHashOf(t, db, id); hashAfter.Valid {
		t.Errorf("content_hash = %q, want NULL — the amount moved, so the row must leave dedupe "+
			"rather than keep claiming the identity of content it no longer holds", hashAfter.String)
	}
}

// contentHashOf reads the raw column so a test can tell "anchored" from
// "cleared" without going through a struct that might zero-fill.
func contentHashOf(t *testing.T, db *sql.DB, id int64) sql.NullString {
	t.Helper()
	var h sql.NullString
	if err := db.QueryRow(`SELECT content_hash FROM transactions WHERE id = ?`, id).Scan(&h); err != nil {
		t.Fatalf("read content_hash: %v", err)
	}
	return h
}
