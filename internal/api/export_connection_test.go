package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestHandleExportTransactions_DoesNotDeadlockOnSingleConnection is the
// regression test for the export deadlock: getBaseCurrency must be resolved
// before the transactions cursor is opened, and the cursor must be drained
// before the workbook is built.
//
// The bug manifested as a hang rather than an error, so this asserts on a
// timeout: with getBaseCurrency moved back below the query the handler never
// returns and the select below fires.
func TestHandleExportTransactions_DoesNotDeadlockOnSingleConnection(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "exporter", "admin")
	catID := seedExpenseCategory(t, h.queries, "Groceries")
	seedExpenseRow(t, h.queries, user.ID, catID, "2026-07-01", 1234)
	seedExpenseRow(t, h.queries, user.ID, catID, "2026-07-02", 5678)

	req := withUser(httptest.NewRequest(http.MethodGet, "/api/export/transactions", nil), user)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.handleExportTransactions(rec, req)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("handleExportTransactions deadlocked: it issued a second query " +
			"while holding the pool's only connection")
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() == 0 {
		t.Fatal("empty xlsx body")
	}
}

// TestHandleExportTransactions_ReleasesConnectionBeforeWorkbookBuild asserts
// the cursor is not held across workbook assembly: once the handler returns,
// the single connection must be back in the pool and immediately usable.
func TestHandleExportTransactions_ReleasesConnectionBeforeWorkbookBuild(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "exporter2", "admin")
	catID := seedExpenseCategory(t, h.queries, "Rent")
	seedExpenseRow(t, h.queries, user.ID, catID, "2026-07-03", 999)

	req := withUser(httptest.NewRequest(http.MethodGet, "/api/export/transactions", nil), user)
	rec := httptest.NewRecorder()
	h.handleExportTransactions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if inUse := h.db.Stats().InUse; inUse != 0 {
		t.Errorf("connections still checked out after handler returned: %d", inUse)
	}

	// The connection must be immediately reusable — this blocks if the export
	// leaked it.
	var n int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&n); err != nil {
		t.Fatalf("follow-up query failed: %v", err)
	}
	if n != 1 {
		t.Errorf("transactions count = %d, want 1", n)
	}
}
