package api

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// TestHandleDeleteUser_RefusesWhenUserOwnsTransactions is the regression test
// for the cascade data-loss bug.
//
// transactions.user_id is ON DELETE CASCADE and production runs with
// _foreign_keys=on, so a bare DELETE on users permanently destroyed every
// transaction the target had created — no tombstone, no audit row, no Trash
// entry. This asserts the handler refuses instead, and that not one row moved.
//
// The test is only meaningful with foreign keys enabled; setupTestDB pins
// _foreign_keys=on to match production (see harness_invariants_test.go).
func TestHandleDeleteUser_RefusesWhenUserOwnsTransactions(t *testing.T) {
	h := setupHandler(t)
	admin := seedTestUser(t, h.queries, "admin", "admin")
	member := seedTestUser(t, h.queries, "member", "member")
	catID := seedExpenseCategory(t, h.queries, "Groceries")

	seedExpenseRow(t, h.queries, member.ID, catID, "2026-07-01", 1500)
	seedExpenseRow(t, h.queries, member.ID, catID, "2026-07-02", 2500)

	before, err := h.queries.CountAllTransactions(t.Context())
	if err != nil {
		t.Fatalf("count before: %v", err)
	}

	req := withUserAndURLParam(
		httptest.NewRequest(http.MethodDelete, "/api/users/"+strconv.FormatInt(member.ID, 10), nil),
		admin, "id", strconv.FormatInt(member.ID, 10),
	)
	rec := httptest.NewRecorder()
	h.handleDeleteUser(rec, req)

	// Deliberately non-fatal: the count assertion below must still run when
	// the status is wrong, because it is the assertion that proves the
	// cascade actually destroyed ledger rows rather than merely that the
	// status code drifted.
	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}

	after, err := h.queries.CountAllTransactions(t.Context())
	if err != nil {
		t.Fatalf("count after: %v", err)
	}
	if after.Live != before.Live || after.Deleted != before.Deleted {
		t.Errorf("transaction counts changed: before live=%d deleted=%d, after live=%d deleted=%d",
			before.Live, before.Deleted, after.Live, after.Deleted)
	}

	// The user must still exist — a refused delete must not half-apply.
	if _, err := h.queries.GetUserByID(t.Context(), member.ID); err != nil {
		t.Errorf("member was deleted despite the 409: %v", err)
	}
}

// TestHandleDeleteUser_RefusesWhenUserOwnsOnlyTombstonedTransactions covers the
// subtler half: a member whose rows are all in the Trash still has recoverable
// history, and the cascade destroys tombstoned rows exactly as permanently as
// live ones. A guard that only counted live rows would let this through.
func TestHandleDeleteUser_RefusesWhenUserOwnsOnlyTombstonedTransactions(t *testing.T) {
	h := setupHandler(t)
	admin := seedTestUser(t, h.queries, "admin2", "admin")
	member := seedTestUser(t, h.queries, "member2", "member")
	catID := seedExpenseCategory(t, h.queries, "Rent")

	seedTombstonedExpenseRow(t, h, h.queries, member.ID, catID, "2026-06-01", 4200)

	before, err := h.queries.CountAllTransactions(t.Context())
	if err != nil {
		t.Fatalf("count before: %v", err)
	}
	if before.Deleted == 0 {
		t.Fatal("fixture is wrong: expected a tombstoned row to exist")
	}

	req := withUserAndURLParam(
		httptest.NewRequest(http.MethodDelete, "/api/users/"+strconv.FormatInt(member.ID, 10), nil),
		admin, "id", strconv.FormatInt(member.ID, 10),
	)
	rec := httptest.NewRecorder()
	h.handleDeleteUser(rec, req)

	// Deliberately non-fatal: the count assertion below must still run when
	// the status is wrong, because it is the assertion that proves the
	// cascade actually destroyed ledger rows rather than merely that the
	// status code drifted.
	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}

	after, err := h.queries.CountAllTransactions(t.Context())
	if err != nil {
		t.Fatalf("count after: %v", err)
	}
	if after.Deleted != before.Deleted {
		t.Errorf("tombstoned rows destroyed: before=%d after=%d", before.Deleted, after.Deleted)
	}
}

// TestHandleDeleteUser_SucceedsWhenUserOwnsNothing keeps the guard honest: a
// member with no ledger rows must still be removable, or the fix has simply
// broken user management.
func TestHandleDeleteUser_SucceedsWhenUserOwnsNothing(t *testing.T) {
	h := setupHandler(t)
	admin := seedTestUser(t, h.queries, "admin3", "admin")
	member := seedTestUser(t, h.queries, "member3", "member")

	req := withUserAndURLParam(
		httptest.NewRequest(http.MethodDelete, "/api/users/"+strconv.FormatInt(member.ID, 10), nil),
		admin, "id", strconv.FormatInt(member.ID, 10),
	)
	rec := httptest.NewRecorder()
	h.handleDeleteUser(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if _, err := h.queries.GetUserByID(t.Context(), member.ID); err == nil {
		t.Error("member still exists after a successful delete")
	}
}
