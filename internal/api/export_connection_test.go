package api

import (
	"context"
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
//
// Time-boxed for the same reason as its monthly and yearly counterparts: the
// regression it guards makes the handler block forever, so a synchronous call
// would take the whole internal/api binary down with a `panic: test timed out`
// instead of failing here.
func TestHandleExportTransactions_ReleasesConnectionBeforeWorkbookBuild(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "exporter2", "admin")
	catID := seedExpenseCategory(t, h.queries, "Rent")
	seedExpenseRow(t, h.queries, user.ID, catID, "2026-07-03", 999)

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
		t.Fatal("handleExportTransactions never returned: it issued a second query " +
			"while holding the pool's only connection")
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	// Fatal, not Error: the follow-up query below blocks forever on a leaked
	// connection, so a leak must stop the test here.
	if inUse := h.db.Stats().InUse; inUse != 0 {
		t.Fatalf("connections still checked out after handler returned: %d", inUse)
	}

	// The connection must be immediately reusable.
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
// The row count matters, though not for the reason 054d1ee's message gave. A
// single row already catches an UNCONDITIONAL `break` or `return`: the loop
// still exits with the cursor unexhausted, so the connection stays held. What
// multi-row data buys is the position-dependent variants — an early exit that
// only fires from the second row onwards (`if row > 1 { break }`, a filter that
// skips the first row, a scan error the first row cannot trigger) is
// unreachable on a single-row result set and would leave these tests green.
//
// The invariant to preserve when editing this fixture is therefore "every
// cursor the monthly and yearly exports open returns at least two rows". Zero
// rows is the case that asserts nothing at all: an empty cursor exhausts
// immediately, so no early exit inside the loop body is reachable.
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

// TestHandleExportMonthly_DoesNotDeadlockOnSingleConnection is a forward guard
// on a currently-unviolated invariant, not a regression test for a bug that
// shipped: no early exit may leave a report cursor open across a later query.
//
// Be precise about what was and was not broken here. handleExportMonthly calls
// getBaseCurrency after the Summary read, and before the drain-helper refactor
// that read was an inline scan loop. That loop always ran to exhaustion — there
// is no `break` or `continue` anywhere in export_handlers.go — and an exhausted
// *sql.Rows auto-closes, so no deadlock was ever reachable in this handler. The
// one export deadlock that did reach production was handleExportTransactions
// issuing getBaseCurrency with its cursor still open; that is fixed in bfbe6e4
// and guarded by the pair at the top of this file.
//
// What the inline shape lacked was any pin on that safety. Under the production
// pool cap of one connection (SetMaxOpenConns(1), load-bearing) a single `break`
// added to the loop would leave the cursor holding the process's only
// connection, and getBaseCurrency would then wait forever on a connection only
// the blocked handler could release — taking every other request down with it.
// This test makes that hypothetical failure loud instead of silent.
//
// The failure mode it guards is a HANG, not an error, so it must assert on a
// timeout: a plain call that returns 200 proves nothing, because a deadlocked
// handler never returns to be inspected at all.
//
// This test cannot see the drain-helper shape itself — it passes against the
// pre-refactor inline loops. TestDrainHelpers_ReleaseConnectionBeforeReturning
// is what pins that.
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

// TestHandleExportYearly_DoesNotDeadlockOnSingleConnection is the same forward
// guard for the yearly export, which opens two cursors in sequence: the Monthly
// Totals scan is followed by the Category Totals query, so an undrained first
// cursor would deadlock the second query rather than getBaseCurrency.
//
// As above, nothing in the pre-refactor code took such an early exit and this
// handler never deadlocked in production. The test pins that no future one may.
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

// TestHandleExportMonthly_ReleasesConnectionBeforeWorkbookBuild pins the
// after-return half of the same forward guard: no cursor may survive the
// handler, so the single connection must be back in the pool and immediately
// usable once it returns.
//
// The handler runs in a goroutine against a timeout, matching the pair above,
// because under a regression it may never return at all: an early exit in the
// Summary loop strands the cursor and getBaseCurrency then blocks forever. A
// synchronous call would hang the whole internal/api binary to a hard
// `panic: test timed out`, aborting every test ordered after this one instead of
// failing this one.
func TestHandleExportMonthly_ReleasesConnectionBeforeWorkbookBuild(t *testing.T) {
	h := setupHandler(t)
	user := seedMultiSheetExportData(t, h, "monthly-exporter2")

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
		t.Fatal("handleExportMonthly never returned: a cursor left open by an early " +
			"exit holds the pool's only connection while a later query waits for one")
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	// Fatal, not Error: assertConnectionUsable below issues a real query, which
	// blocks forever on a leaked connection. Stopping here keeps a leak a clean
	// failure rather than a test-binary timeout.
	if inUse := h.db.Stats().InUse; inUse != 0 {
		t.Fatalf("connections still checked out after handler returned: %d", inUse)
	}
	assertConnectionUsable(t, h, 3)
}

// TestHandleExportYearly_ReleasesConnectionBeforeWorkbookBuild is the same
// assertion for the yearly export, time-boxed for the same reason: an early
// exit in the Monthly Totals loop strands the cursor and the Category Totals
// query then blocks forever inside the handler.
func TestHandleExportYearly_ReleasesConnectionBeforeWorkbookBuild(t *testing.T) {
	h := setupHandler(t)
	user := seedMultiSheetExportData(t, h, "yearly-exporter2")

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
		t.Fatal("handleExportYearly never returned: a cursor left open by an early " +
			"exit holds the pool's only connection while a later query waits for one")
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	// Fatal, not Error: see the monthly test above.
	if inUse := h.db.Stats().InUse; inUse != 0 {
		t.Fatalf("connections still checked out after handler returned: %d", inUse)
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

// TestDrainHelpers_ReleaseConnectionBeforeReturning pins the drain-helper SHAPE,
// which none of the handler-level tests above can.
//
// An inline `for rows.Next()` loop run to exhaustion is observationally
// identical to a drain helper from outside the handler: the cursor auto-closes
// either way. Every test above therefore passes verbatim against the
// pre-refactor inline loops — folding the helpers back into the handlers would
// leave the rest of this file green.
//
// This test names drainCategoryTotals, drainMonthlyTotals and drainExportTxnRows
// directly, so that revert has to delete a symbol this package references and
// internal/api stops compiling. It also asserts the property the helpers exist
// for: once one returns, the pool's single connection is free.
func TestDrainHelpers_ReleaseConnectionBeforeReturning(t *testing.T) {
	h := setupHandler(t)
	seedMultiSheetExportData(t, h, "drain-helper-user")
	ctx := context.Background()

	totals, err := h.drainCategoryTotals(ctx, `SELECT c.name, c.type, COALESCE(SUM(t.amount_cents), 0) AS total_cents
		FROM categories c
		LEFT JOIN transactions t ON t.category_id = c.id AND t.deleted_at IS NULL
		GROUP BY c.id
		HAVING total_cents > 0`)
	if err != nil {
		t.Fatalf("drainCategoryTotals: %v", err)
	}
	if len(totals) != 2 {
		t.Errorf("category totals = %d rows, want 2 (one expense, one income)", len(totals))
	}
	assertConnectionFree(t, h, "drainCategoryTotals")

	months, err := h.drainMonthlyTotals(ctx, `SELECT CAST(strftime('%m', t.date) AS INTEGER) AS month_num,
		COALESCE(SUM(CASE WHEN c.type = 'expense' THEN t.amount_cents ELSE 0 END), 0) AS expenses_cents,
		COALESCE(SUM(CASE WHEN c.type = 'income' THEN t.amount_cents ELSE 0 END), 0) AS income_cents
		FROM transactions t
		JOIN categories c ON t.category_id = c.id
		WHERE t.deleted_at IS NULL
		GROUP BY month_num
		ORDER BY month_num`)
	if err != nil {
		t.Fatalf("drainMonthlyTotals: %v", err)
	}
	if got := months[6]; got.expensesCents != 1234 || got.incomeCents != 500000 {
		t.Errorf("July totals = %+v, want {expensesCents:1234 incomeCents:500000}", got)
	}
	if got := months[7]; got.expensesCents != 4321 {
		t.Errorf("August expenses = %d, want 4321", got.expensesCents)
	}
	assertConnectionFree(t, h, "drainMonthlyTotals")

	txns, err := h.drainExportTxnRows(ctx, `SELECT t.date, t.description, c.name, c.type, t.amount_cents,
		t.original_amount_cents, t.original_currency, t.tags, t.notes
		FROM transactions t
		JOIN categories c ON t.category_id = c.id
		WHERE t.deleted_at IS NULL
		ORDER BY t.date DESC, t.id DESC LIMIT ?`, MaxExportRows)
	if err != nil {
		t.Fatalf("drainExportTxnRows: %v", err)
	}
	if len(txns) != 3 {
		t.Errorf("transaction rows = %d, want 3", len(txns))
	}
	assertConnectionFree(t, h, "drainExportTxnRows")
}

// assertConnectionFree checks the pool's only connection is back before issuing
// a real follow-up query on it. The Stats check is fatal and comes first on
// purpose: the query below blocks forever on a leaked connection, so letting it
// run after a known leak would turn a clean failure into a test-binary timeout.
func assertConnectionFree(t *testing.T, h *Handler, after string) {
	t.Helper()
	if inUse := h.db.Stats().InUse; inUse != 0 {
		t.Fatalf("%s returned with %d connection(s) still checked out: the cursor "+
			"was not drained", after, inUse)
	}
	var n int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&n); err != nil {
		t.Fatalf("follow-up query after %s failed: %v", after, err)
	}
}
