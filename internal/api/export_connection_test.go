package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/elienop/spendrop/internal/database"
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

// seedMultiSheetExportData seeds two categories and three transactions spread
// over two months of 2026, so every cursor the monthly and yearly exports open
// returns MORE THAN ONE row.
//
// The row count matters. These tests exist to catch a cursor that is left open
// across a later query, and the classic way that gets introduced is a `break`
// or an early `return` in the scan loop. On a single-row result set the loop
// exits at the same point either way, so the undrained state is unreachable and
// the test would pass against the very bug it is written for.
func seedMultiSheetExportData(t *testing.T, h *Handler, username string) database.User {
	t.Helper()
	user := seedTestUser(t, h.queries, username, "admin")
	expenseCat := seedExpenseCategory(t, h.queries, "Groceries")
	incomeCat := seedIncomeCategory(t, h.queries, "Salary")

	// Two categories in July => two Summary rows for the monthly export and
	// two Category Totals rows for the yearly one.
	seedExpenseRow(t, h.queries, user.ID, expenseCat, "2026-07-01", 1234)
	seedExpenseRow(t, h.queries, user.ID, incomeCat, "2026-07-02", 500000)
	// A second month => two Monthly Totals rows for the yearly export.
	seedExpenseRow(t, h.queries, user.ID, expenseCat, "2026-08-05", 4321)
	return user
}

// TestHandleExportMonthly_DoesNotDeadlockOnSingleConnection pins the monthly
// export against the same single-connection deadlock the transactions export
// already guards.
//
// handleExportMonthly calls getBaseCurrency after the Summary scan loop but
// before the deferred summaryRows.Close() can fire. That only ever worked
// because a loop run to exhaustion auto-closes its *sql.Rows — nothing pinned
// it, so adding one `break` to the loop made getBaseCurrency wait forever on a
// connection only the handler itself could release, taking every other request
// in the process down with it.
//
// The failure mode is a HANG, not an error, so this must assert on a timeout: a
// plain call that returns 200 proves nothing, because a deadlocked handler
// never returns to be inspected at all.
func TestHandleExportMonthly_DoesNotDeadlockOnSingleConnection(t *testing.T) {
	h := setupHandler(t)
	user := seedMultiSheetExportData(t, h, "monthly-exporter")

	req := withUserAndURLParams(
		httptest.NewRequest(http.MethodGet, "/api/export/monthly/2026/7", nil),
		user, map[string]string{"year": "2026", "month": "7"})
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.handleExportMonthly(rec, req)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("handleExportMonthly deadlocked: an undrained summary cursor holds " +
			"the pool's only connection while a later query waits for one")
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() == 0 {
		t.Fatal("empty xlsx body")
	}
	if inUse := h.db.Stats().InUse; inUse != 0 {
		t.Errorf("connections still checked out after handler returned: %d", inUse)
	}
}

// TestHandleExportYearly_DoesNotDeadlockOnSingleConnection is the same guard
// for the yearly export, which opens two cursors in sequence: the Monthly
// Totals scan is followed by the Category Totals query, so an undrained first
// cursor deadlocks the second query rather than getBaseCurrency.
func TestHandleExportYearly_DoesNotDeadlockOnSingleConnection(t *testing.T) {
	h := setupHandler(t)
	user := seedMultiSheetExportData(t, h, "yearly-exporter")

	req := withUserAndURLParams(
		httptest.NewRequest(http.MethodGet, "/api/export/yearly/2026", nil),
		user, map[string]string{"year": "2026"})
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.handleExportYearly(rec, req)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("handleExportYearly deadlocked: an undrained cursor holds the " +
			"pool's only connection while a later query waits for one")
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() == 0 {
		t.Fatal("empty xlsx body")
	}
	if inUse := h.db.Stats().InUse; inUse != 0 {
		t.Errorf("connections still checked out after handler returned: %d", inUse)
	}
}

// TestHandleExportMonthly_ReleasesConnectionBeforeWorkbookBuild asserts no
// cursor survives the handler: the single connection must be back in the pool
// and immediately usable once it returns.
func TestHandleExportMonthly_ReleasesConnectionBeforeWorkbookBuild(t *testing.T) {
	h := setupHandler(t)
	user := seedMultiSheetExportData(t, h, "monthly-exporter2")

	req := withUserAndURLParams(
		httptest.NewRequest(http.MethodGet, "/api/export/monthly/2026/7", nil),
		user, map[string]string{"year": "2026", "month": "7"})
	rec := httptest.NewRecorder()
	h.handleExportMonthly(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if inUse := h.db.Stats().InUse; inUse != 0 {
		t.Errorf("connections still checked out after handler returned: %d", inUse)
	}
	assertConnectionUsable(t, h, 3)
}

// TestHandleExportYearly_ReleasesConnectionBeforeWorkbookBuild is the same
// assertion for the yearly export.
func TestHandleExportYearly_ReleasesConnectionBeforeWorkbookBuild(t *testing.T) {
	h := setupHandler(t)
	user := seedMultiSheetExportData(t, h, "yearly-exporter2")

	req := withUserAndURLParams(
		httptest.NewRequest(http.MethodGet, "/api/export/yearly/2026", nil),
		user, map[string]string{"year": "2026"})
	rec := httptest.NewRecorder()
	h.handleExportYearly(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if inUse := h.db.Stats().InUse; inUse != 0 {
		t.Errorf("connections still checked out after handler returned: %d", inUse)
	}
	assertConnectionUsable(t, h, 3)
}

// assertConnectionUsable issues a follow-up query on the shared pool. It blocks
// (and the enclosing test times out) if the handler leaked the connection.
func assertConnectionUsable(t *testing.T, h *Handler, wantRows int) {
	t.Helper()
	var n int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&n); err != nil {
		t.Fatalf("follow-up query failed: %v", err)
	}
	if n != wantRows {
		t.Errorf("transactions count = %d, want %d", n, wantRows)
	}
}
