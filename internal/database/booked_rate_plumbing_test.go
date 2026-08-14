package database

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// readBookedRate reads the column straight out of SQLite rather than through a
// generated struct. A hand-maintained scan list that drifts from its SELECT
// list produces a plausible-looking Go value from the wrong column, and only a
// raw read can contradict it.
func readBookedRate(t *testing.T, db *sql.DB, txnID int64) sql.NullFloat64 {
	t.Helper()
	var rate sql.NullFloat64
	if err := db.QueryRowContext(context.Background(),
		`SELECT booked_rate FROM transactions WHERE id = ?`, txnID).Scan(&rate); err != nil {
		t.Fatalf("read booked_rate for txn %d: %v", txnID, err)
	}
	return rate
}

// TestCreateTransaction_BookedRate_RoundTrips pins the CreateTransaction
// lockstep: the INSERT column list, the params struct, the RETURNING expansion
// and the Scan list are four hand-maintained lists that must agree, and sqlc
// codegen is broken in this repo so nothing regenerates them together.
//
// It also pins the NULL default: a create that resolves no rate — no currency,
// or the base currency — must leave the column empty rather than storing a 0
// that reads as a real (and impossible) rate.
func TestCreateTransaction_BookedRate_RoundTrips(t *testing.T) {
	db, _, q := newTestStore(t)
	ctx := context.Background()

	userID := seedTestStoreUser(t, q, "alice")
	catID := seedTestStoreCategory(t, q, "CategoryA")
	date, err := time.Parse("2006-01-02", "2026-05-01")
	if err != nil {
		t.Fatalf("parse date: %v", err)
	}

	withRate, err := q.CreateTransaction(ctx, CreateTransactionParams{
		UserID:              userID,
		Date:                date,
		AmountCents:         1685,
		OriginalAmountCents: sql.NullInt64{Int64: 150000000, Valid: true},
		OriginalCurrency:    sql.NullString{String: "LBP", Valid: true},
		BookedRate:          sql.NullFloat64{Float64: 89000, Valid: true},
		Description:         "taxi",
		CategoryID:          catID,
	})
	if err != nil {
		t.Fatalf("create with rate: %v", err)
	}
	if !withRate.BookedRate.Valid || withRate.BookedRate.Float64 != 89000 {
		t.Errorf("RETURNING gave BookedRate=%+v, want valid 89000", withRate.BookedRate)
	}
	if stored := readBookedRate(t, db, withRate.ID); !stored.Valid || stored.Float64 != 89000 {
		t.Errorf("stored booked_rate=%+v, want valid 89000", stored)
	}
	// The neighbouring columns must not have shifted: a scan list off by one
	// still produces values, just the wrong ones.
	if withRate.AmountCents != 1685 {
		t.Errorf("AmountCents=%d, want 1685 — scan list may be misaligned", withRate.AmountCents)
	}
	if !withRate.OriginalAmountCents.Valid || withRate.OriginalAmountCents.Int64 != 150000000 {
		t.Errorf("OriginalAmountCents=%+v, want valid 150000000", withRate.OriginalAmountCents)
	}

	noRate, err := q.CreateTransaction(ctx, CreateTransactionParams{
		UserID:      userID,
		Date:        date,
		AmountCents: 500,
		Description: "coffee",
		CategoryID:  catID,
	})
	if err != nil {
		t.Fatalf("create without rate: %v", err)
	}
	if noRate.BookedRate.Valid {
		t.Errorf("a create that resolved no rate returned %+v, want NULL", noRate.BookedRate)
	}
	if stored := readBookedRate(t, db, noRate.ID); stored.Valid {
		t.Errorf("a create that resolved no rate stored %+v, want NULL", stored)
	}
}

// TestGetTransactionByID_SurfacesBookedRate pins the projection the store's
// update path reads its `before` row from. That projection is deliberately
// narrower than t.* — it omits content_hash and idempotency_key — so the rate
// being in it is a choice, and it is the choice that makes carrying a rate
// across a full-replace UPDATE possible at all.
func TestGetTransactionByID_SurfacesBookedRate(t *testing.T) {
	db, _, q := newTestStore(t)
	ctx := context.Background()

	userID := seedTestStoreUser(t, q, "alice")
	catID := seedTestStoreCategory(t, q, "CategoryA")
	txnID := seedTestStoreTransaction(t, q, userID, catID, "2026-05-01", "taxi", 16.85, "")
	mustExec(t, db, `UPDATE transactions SET booked_rate = 89000 WHERE id = ?`, txnID)

	row, err := q.GetTransactionByID(ctx, txnID)
	if err != nil {
		t.Fatalf("GetTransactionByID: %v", err)
	}
	if !row.BookedRate.Valid || row.BookedRate.Float64 != 89000 {
		t.Errorf("GetTransactionByIDRow.BookedRate=%+v, want valid 89000", row.BookedRate)
	}
	if row.CategoryType != "expense" {
		t.Errorf("CategoryType=%q, want \"expense\" — the joined column must not have shifted", row.CategoryType)
	}
}

// TestUpdateTx_TagsPatch_PreservesBookedRate pins the merge in UpdateTx. The
// batch patch shape cannot express money, so every money column is seeded from
// `before`; UpdateTransaction is a FULL REPLACE, so a column left out of that
// merge is not "unchanged" — it is overwritten with the zero value. A tags-only
// edit re-prices nothing, so the rate that produced the stored amount is still
// the true one and must survive.
func TestUpdateTx_TagsPatch_PreservesBookedRate(t *testing.T) {
	db, store, q := newTestStore(t)
	ctx := context.Background()

	userID := seedTestStoreUser(t, q, "alice")
	catID := seedTestStoreCategory(t, q, "CategoryA")
	txnID := seedTestStoreTransaction(t, q, userID, catID, "2026-05-01", "taxi", 16.85, "old")
	mustExec(t, db, `UPDATE transactions SET booked_rate = 89000, original_amount_cents = 150000000,
		original_currency = 'LBP' WHERE id = ?`, txnID)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback() //nolint:errcheck // committed below on the success path

	newTags := "new"
	_, after, err := store.UpdateTx(ctx, tx, Actor{UserID: userID}, txnID, UpdatePatch{Tags: &newTags})
	if err != nil {
		t.Fatalf("UpdateTx: %v", err)
	}
	if !after.BookedRate.Valid || after.BookedRate.Float64 != 89000 {
		t.Errorf("after.BookedRate=%+v, want valid 89000 — a tags patch must not re-price the row", after.BookedRate)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if stored := readBookedRate(t, db, txnID); !stored.Valid || stored.Float64 != 89000 {
		t.Errorf("stored booked_rate=%+v after a tags-only patch, want valid 89000", stored)
	}
	// The tags edit itself must still have landed — otherwise this test would
	// pass on a UpdateTx that did nothing at all.
	var tags sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT tags FROM transactions WHERE id = ?`, txnID).Scan(&tags); err != nil {
		t.Fatalf("read tags: %v", err)
	}
	if tags.String != "new" {
		t.Errorf("tags=%q, want \"new\" — the patch did not land", tags.String)
	}
}
