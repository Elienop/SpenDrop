package api

// Boundary-day end-to-end tests for date-range filters in exports and the
// transactions list.
//
// The bug these tests pin: transactions.date is stored as RFC3339 text
// (e.g. "2026-03-31T14:22:00Z") by mattn/go-sqlite3's default time.Time
// encoder. A raw "t.date <= ?" comparison against a YYYY-MM-DD bound
// (e.g. "2026-03-31") is a LEXICAL comparison, and the 'T' separator in
// RFC3339 sorts AFTER the end of the bound string, so any boundary-day
// row with a non-zero time-of-day is silently dropped. The fix wraps the
// column side with date(t.date) so the comparison happens on YYYY-MM-DD.
//
// Each test seeds exactly ONE transaction on 2026-03-31 at 14:22:00Z with
// a distinctive amount ($777.00 / 77700 cents) and asserts the row shows
// up in the relevant endpoint's output. A failure message explicitly
// names the regression so future maintainers see the root cause in the
// test output without having to re-derive it.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/elienop/spendrop/internal/database"
)

// seedBoundaryTransaction inserts a transaction on 2026-03-31 at 14:22:00Z
// (non-zero time-of-day) with a distinctive $777.00 amount. This is the
// canonical "end-of-boundary-day" row: it must survive every date-range
// filter that uses dateTo = "2026-03-31".
func seedBoundaryTransaction(t *testing.T, q *database.Queries, userID, categoryID int64) database.Transaction {
	t.Helper()
	d := time.Date(2026, 3, 31, 14, 22, 0, 0, time.UTC)
	const amount = 777.00
	txn, err := q.CreateTransaction(context.Background(), database.CreateTransactionParams{
		UserID:      userID,
		Date:        d,
		Amount:      amount,
		AmountCents: dollarsToCents(amount),
		Description: "boundary-day row",
		CategoryID:  categoryID,
	})
	if err != nil {
		t.Fatalf("seed boundary transaction: %v", err)
	}
	return txn
}

// rowsContainAmount returns true if any row in rows (excluding the first /
// header row) contains the expected dollar string in any cell. The xlsx
// writer emits numeric cells as plain decimal strings (e.g. "777" for a
// whole-dollar amount), so we match either the plain integer form or the
// "777.00" form defensively.
func rowsContainAmount(rows [][]string, expected ...string) bool {
	for _, row := range rows[1:] {
		for _, cell := range row {
			for _, want := range expected {
				if cell == want {
					return true
				}
			}
		}
	}
	return false
}

// TestExports_IncludeRowAtEndOfBoundaryDay_CSVExport exercises the
// top-level transactions xlsx export (/api/export/transactions) with a
// date-to boundary. The endpoint funnels through
// buildTransactionWhereClause, which today emits "t.date <= ?" — the
// lexical-comparison trap.
func TestExports_IncludeRowAtEndOfBoundaryDay_CSVExport(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	seedBoundaryTransaction(t, q, user.ID, 1)

	req := httptest.NewRequest(http.MethodGet, "/api/export/transactions?date_from=2026-03-01&date_to=2026-03-31", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleExportTransactions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	f, err := excelize.OpenReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("parse xlsx: %v", err)
	}
	defer f.Close()

	rows, err := f.GetRows("Transactions")
	if err != nil {
		t.Fatalf("get Transactions sheet: %v", err)
	}
	if !rowsContainAmount(rows, "777", "777.00") {
		t.Fatalf("boundary-day row dropped from /api/export/transactions — the t.date <= ? comparison trap is back. rows=%v", rows)
	}
}

// TestExports_IncludeRowAtEndOfBoundaryDay_ListFilter exercises the
// transactions list endpoint (/api/transactions) — the UI's date-range
// filter path. Same buildTransactionWhereClause helper, same bug.
func TestExports_IncludeRowAtEndOfBoundaryDay_ListFilter(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	seedBoundaryTransaction(t, q, user.ID, 1)

	req := httptest.NewRequest(http.MethodGet, "/api/transactions?date_from=2026-03-01&date_to=2026-03-31", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleListTransactions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Transactions []map[string]any `json:"transactions"`
		Total        int              `json:"total"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if resp.Total != 1 || len(resp.Transactions) != 1 {
		t.Fatalf("boundary-day row dropped from /api/transactions list filter — the t.date <= ? comparison trap is back. total=%d transactions=%v", resp.Total, resp.Transactions)
	}
	if desc, _ := resp.Transactions[0]["description"].(string); !strings.Contains(desc, "boundary-day") {
		t.Fatalf("unexpected row returned: %v", resp.Transactions[0])
	}
}

// TestExports_IncludeRowAtEndOfBoundaryDay_MonthlySummarySheet exercises
// handleExportMonthly's Summary sheet, whose LEFT JOIN ON clause has
// "t.date >= ? AND t.date <= ?" — lexical comparison trap, same shape.
// Food (id=1) is an expense category, so if the boundary row is counted
// the Summary sheet Food row shows total=777; if dropped, the Food row
// is omitted entirely (HAVING total_cents > 0 filters it out).
func TestExports_IncludeRowAtEndOfBoundaryDay_MonthlySummarySheet(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	seedBoundaryTransaction(t, q, user.ID, 1)

	req := httptest.NewRequest(http.MethodGet, "/api/export/monthly/2026/03", nil)
	req = withUserAndURLParams(req, user, map[string]string{"year": "2026", "month": "03"})
	rec := httptest.NewRecorder()
	h.handleExportMonthly(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	f, err := excelize.OpenReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("parse xlsx: %v", err)
	}
	defer f.Close()

	summaryRows, err := f.GetRows("Summary")
	if err != nil {
		t.Fatalf("get Summary sheet: %v", err)
	}
	foundFood := false
	for _, row := range summaryRows[1:] {
		if len(row) >= 3 && row[0] == "Food" {
			foundFood = true
			if row[2] != "777" && row[2] != "777.00" {
				t.Fatalf("boundary-day row dropped from monthly Summary sheet — the t.date <= ? comparison trap is back. Food total=%q want 777", row[2])
			}
		}
	}
	if !foundFood {
		t.Fatalf("boundary-day row dropped from monthly Summary sheet — the t.date <= ? comparison trap is back. Food row absent; rows=%v", summaryRows)
	}
}

// TestExports_IncludeRowAtEndOfBoundaryDay_MonthlyTransactionsSheet
// exercises handleExportMonthly's Transactions sheet, whose WHERE clause
// has "t.date >= ? AND t.date <= ?" — same trap.
func TestExports_IncludeRowAtEndOfBoundaryDay_MonthlyTransactionsSheet(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	seedBoundaryTransaction(t, q, user.ID, 1)

	req := httptest.NewRequest(http.MethodGet, "/api/export/monthly/2026/03", nil)
	req = withUserAndURLParams(req, user, map[string]string{"year": "2026", "month": "03"})
	rec := httptest.NewRecorder()
	h.handleExportMonthly(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	f, err := excelize.OpenReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("parse xlsx: %v", err)
	}
	defer f.Close()

	txnRows, err := f.GetRows("Transactions")
	if err != nil {
		t.Fatalf("get Transactions sheet: %v", err)
	}
	if !rowsContainAmount(txnRows, "777", "777.00") {
		t.Fatalf("boundary-day row dropped from monthly Transactions sheet — the t.date <= ? comparison trap is back. rows=%v", txnRows)
	}
}

// TestExports_IncludeRowAtEndOfBoundaryDay_YearlyMonthlyTotalsSheet
// exercises handleExportYearly's Monthly Totals sheet. Its WHERE clause
// has "t.date >= ? AND t.date <= ?" with dateTo=YYYY-12-31 — same trap,
// end-of-year edition. A boundary row on the last day of December with a
// non-zero time-of-day would be dropped from the December total. We seed
// the row on 2026-03-31 at 14:22:00Z and check the March row (row index 3)
// — dateFrom for yearly export is "2026-01-01", dateTo is "2026-12-31",
// and the >= bound is accidentally safe today (because "2026-01-01" sorts
// before "2026-03-31T14:22:00Z" lexically), but the <= bound isn't the
// one that triggers here — what triggers here is the broader question:
// does the yearly query include our non-midnight row at all? Today "<=
// 2026-12-31" is passed against "2026-03-31T14:22:00Z" which is safe
// (March < December), so this specific test of the YEARLY path is
// *independent* of the bug and would pass today. To actually exercise
// the trap we move the boundary row to the end of the year.
func TestExports_IncludeRowAtEndOfBoundaryDay_YearlyMonthlyTotalsSheet(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	// End-of-year boundary row: 2026-12-31 at 14:22:00Z. Against
	// dateTo="2026-12-31" this is exactly the lexical-comparison trap.
	d := time.Date(2026, 12, 31, 14, 22, 0, 0, time.UTC)
	const amount = 777.00
	if _, err := q.CreateTransaction(context.Background(), database.CreateTransactionParams{
		UserID:      user.ID,
		Date:        d,
		Amount:      amount,
		AmountCents: dollarsToCents(amount),
		Description: "end-of-year boundary",
		CategoryID:  1,
	}); err != nil {
		t.Fatalf("seed end-of-year boundary: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/export/yearly/2026", nil)
	req = withUserAndURLParams(req, user, map[string]string{"year": "2026"})
	rec := httptest.NewRecorder()
	h.handleExportYearly(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	f, err := excelize.OpenReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("parse xlsx: %v", err)
	}
	defer f.Close()

	monthlyRows, err := f.GetRows("Monthly Totals")
	if err != nil {
		t.Fatalf("get Monthly Totals: %v", err)
	}
	// Row index 12 (1-based row 13) = December (header is row 1 / index 0).
	if len(monthlyRows) < 13 {
		t.Fatalf("expected 13 rows in Monthly Totals, got %d", len(monthlyRows))
	}
	dec := monthlyRows[12]
	if len(dec) < 2 || dec[0] != "December" {
		t.Fatalf("expected December row at index 12, got %v", dec)
	}
	if dec[1] != "777" && dec[1] != "777.00" {
		t.Fatalf("boundary-day row dropped from yearly Monthly Totals sheet — the t.date <= ? comparison trap is back. December expenses=%q want 777", dec[1])
	}
}

// TestExports_IncludeRowAtEndOfBoundaryDay_YearlyCategoryTotalsSheet
// exercises handleExportYearly's Category Totals sheet. Its LEFT JOIN ON
// clause has "t.date >= ? AND t.date <= ?" — same trap at the end-of-year
// boundary.
func TestExports_IncludeRowAtEndOfBoundaryDay_YearlyCategoryTotalsSheet(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	d := time.Date(2026, 12, 31, 14, 22, 0, 0, time.UTC)
	const amount = 777.00
	if _, err := q.CreateTransaction(context.Background(), database.CreateTransactionParams{
		UserID:      user.ID,
		Date:        d,
		Amount:      amount,
		AmountCents: dollarsToCents(amount),
		Description: "end-of-year boundary",
		CategoryID:  1,
	}); err != nil {
		t.Fatalf("seed end-of-year boundary: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/export/yearly/2026", nil)
	req = withUserAndURLParams(req, user, map[string]string{"year": "2026"})
	rec := httptest.NewRecorder()
	h.handleExportYearly(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	f, err := excelize.OpenReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("parse xlsx: %v", err)
	}
	defer f.Close()

	catRows, err := f.GetRows("Category Totals")
	if err != nil {
		t.Fatalf("get Category Totals: %v", err)
	}
	foundFood := false
	for _, row := range catRows[1:] {
		if len(row) >= 3 && row[0] == "Food" {
			foundFood = true
			if row[2] != "777" && row[2] != "777.00" {
				t.Fatalf("boundary-day row dropped from yearly Category Totals sheet — the t.date <= ? comparison trap is back. Food total=%q want 777", row[2])
			}
		}
	}
	if !foundFood {
		t.Fatalf("boundary-day row dropped from yearly Category Totals sheet — the t.date <= ? comparison trap is back. Food row absent; rows=%v", catRows)
	}
}
