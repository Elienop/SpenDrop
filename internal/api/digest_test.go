package api

import (
	"context"
	"testing"
	"time"
)

// TestCountTransactionsSince_HidesTombstoned seeds one live and one tombstoned
// (sentinel $999) row created after the cutoff and asserts the digest "what
// changed" count never includes the tombstoned row (soft-delete discipline).
func TestCountTransactionsSince_HidesTombstoned(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	ctx := context.Background()

	user := seedTestUser(t, q, "alice", RoleMember)
	cat := seedExpenseCategory(t, q, "Groceries")

	seedExpenseRow(t, q, user.ID, cat, "2026-01-02", 1500)           // live
	ghost := seedExpenseRow(t, q, user.ID, cat, "2026-01-02", 99900) // sentinel $999
	if err := h.txnStore.Delete(ctx, user.ID, ghost.ID); err != nil {
		t.Fatalf("tombstone ghost: %v", err)
	}

	since := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC) // before both rows
	n, err := q.CountTransactionsSince(ctx, since)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("digest count must exclude the tombstoned row: got %d want 1", n)
	}
}
