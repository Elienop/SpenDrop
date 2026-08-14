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
	if len(rows) == 0 {
		return false
	}
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
// the Summary sheet Food row shows total=777; if dropped, the Food row is
// omitted entirely — B10 changed the gate to HAVING COUNT(t.id) > 0, which
// still hides a category whose rows all fell outside the window, because the
// LEFT JOIN then matches nothing and the count over the joined column is 0.
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

// Seeds on end-of-year so the <= 2026-12-31 upper bound is actually exercised.
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

// --- Upper-bound exclusion: a row at YYYY-MM-DD+1 must NOT leak in ---
//
// Symmetric to the inclusion tests above. A future refactor that replaced
// date(t.date) <= ? with something like t.date <= datetime(?, '+1 day')
// (an inclusive open-bound) would pass the inclusion tests but silently
// leak the *next* day into the result. Each exclusion test seeds a
// single row at the start of the day AFTER the boundary and asserts it
// is not present in the endpoint's output.

// seedNextDayTransaction inserts a transaction at the start of the day
// immediately after the boundary-day, with the same distinctive $777.00
// amount. Used by the exclusion tests to prove that upper bounds stay
// strictly exclusive of YYYY-MM-DD+1.
func seedNextDayTransaction(t *testing.T, q *database.Queries, userID, categoryID int64, d time.Time) database.Transaction {
	t.Helper()
	const amount = 777.00
	txn, err := q.CreateTransaction(context.Background(), database.CreateTransactionParams{
		UserID:      userID,
		Date:        d,
		AmountCents: dollarsToCents(amount),
		Description: "next-day row",
		CategoryID:  categoryID,
	})
	if err != nil {
		t.Fatalf("seed next-day transaction: %v", err)
	}
	return txn
}

func TestExports_ExcludeNextDayRow_CSVExport(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	// 2026-04-01T00:00:00Z is the start of the day AFTER the 2026-03-31 bound.
	seedNextDayTransaction(t, q, user.ID, 1, time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))

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
	if rowsContainAmount(rows, "777", "777.00") {
		t.Fatalf("next-day row leaked into /api/export/transactions — upper-bound exclusion broke. rows=%v", rows)
	}
}

func TestExports_ExcludeNextDayRow_ListFilter(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	seedNextDayTransaction(t, q, user.ID, 1, time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))

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
	if resp.Total != 0 || len(resp.Transactions) != 0 {
		t.Fatalf("next-day row leaked into /api/transactions list filter — upper-bound exclusion broke. total=%d transactions=%v", resp.Total, resp.Transactions)
	}
}

func TestExports_ExcludeNextDayRow_MonthlySummarySheet(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	seedNextDayTransaction(t, q, user.ID, 1, time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))

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
	// Food row should be absent entirely. B10's gate is HAVING COUNT(t.id) > 0
	// — "has live rows in the window" — and the only Food row falls outside
	// the window, so the LEFT JOIN matches nothing and the count is 0. If the
	// next-day row leaked in, Food would show total=777.
	for _, row := range summaryRows[1:] {
		if len(row) >= 3 && row[0] == "Food" {
			t.Fatalf("next-day row leaked into monthly Summary sheet — upper-bound exclusion broke. Food row present: %v", row)
		}
	}
}

func TestExports_ExcludeNextDayRow_MonthlyTransactionsSheet(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	seedNextDayTransaction(t, q, user.ID, 1, time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))

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
	if rowsContainAmount(txnRows, "777", "777.00") {
		t.Fatalf("next-day row leaked into monthly Transactions sheet — upper-bound exclusion broke. rows=%v", txnRows)
	}
}

func TestExports_ExcludeNextDayRow_YearlyMonthlyTotalsSheet(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	// 2027-01-01T00:00:00Z is the start of the day AFTER the 2026-12-31 bound.
	seedNextDayTransaction(t, q, user.ID, 1, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC))

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
	// Every month should be zero. Check all 12 month data rows.
	if len(monthlyRows) < 13 {
		t.Fatalf("expected 13 rows in Monthly Totals, got %d", len(monthlyRows))
	}
	for i := 1; i <= 12; i++ {
		row := monthlyRows[i]
		if len(row) < 4 {
			continue
		}
		// Columns: Month, Expenses, Income, Net. A leaked next-day row would
		// show up as 777 in Expenses for one of the months.
		if row[1] != "0" && row[1] != "0.00" {
			t.Fatalf("next-day row leaked into yearly Monthly Totals sheet — upper-bound exclusion broke. %s expenses=%q want 0", row[0], row[1])
		}
	}
}

func TestExports_ExcludeNextDayRow_YearlyCategoryTotalsSheet(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	seedNextDayTransaction(t, q, user.ID, 1, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC))

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
	// Food row should be absent entirely — same COUNT(t.id) > 0 reasoning as
	// the monthly Summary sheet above. If the next-day row leaked, Food would
	// show total=777.
	for _, row := range catRows[1:] {
		if len(row) >= 3 && row[0] == "Food" {
			t.Fatalf("next-day row leaked into yearly Category Totals sheet — upper-bound exclusion broke. Food row present: %v", row)
		}
	}
}

// --- Tombstone discipline on the boundary day ---
//
// Prove that the date() wrapper didn't accidentally drop the
// t.deleted_at IS NULL predicate. Seeds two rows on the same boundary
// day at non-zero time — one live ($777), one tombstoned ($888) — and
// asserts only the live one appears.

// seedTombstonedBoundaryTransaction inserts a transaction on 2026-03-31
// at 14:22:00Z and immediately soft-deletes it via the sqlc-generated
// SoftDeleteTransaction query. The caller-supplied amount lets the test
// use a distinct sentinel ($888) from the live boundary row ($777) so
// the assertion can distinguish them in the output.
func seedTombstonedBoundaryTransaction(t *testing.T, q *database.Queries, userID, categoryID int64, amount float64) database.Transaction {
	t.Helper()
	d := time.Date(2026, 3, 31, 14, 22, 0, 0, time.UTC)
	txn, err := q.CreateTransaction(context.Background(), database.CreateTransactionParams{
		UserID:      userID,
		Date:        d,
		AmountCents: dollarsToCents(amount),
		Description: "tombstoned boundary",
		CategoryID:  categoryID,
	})
	if err != nil {
		t.Fatalf("seed tombstoned boundary transaction: %v", err)
	}
	if err := q.SoftDeleteTransaction(context.Background(), txn.ID); err != nil {
		t.Fatalf("soft-delete boundary transaction: %v", err)
	}
	return txn
}

func TestExports_ExcludeTombstonedRowAtEndOfBoundaryDay_CSVExport(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	seedBoundaryTransaction(t, q, user.ID, 1)                   // live, 777
	seedTombstonedBoundaryTransaction(t, q, user.ID, 1, 888.00) // tombstoned, 888

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
		t.Fatalf("live boundary row dropped from /api/export/transactions — boundary regression. rows=%v", rows)
	}
	if rowsContainAmount(rows, "888", "888.00") {
		t.Fatalf("tombstoned boundary row leaked into /api/export/transactions — t.deleted_at IS NULL predicate broke. rows=%v", rows)
	}
}

func TestExports_ExcludeTombstonedRowAtEndOfBoundaryDay_ListFilter(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	seedBoundaryTransaction(t, q, user.ID, 1)                   // live, 777
	seedTombstonedBoundaryTransaction(t, q, user.ID, 1, 888.00) // tombstoned, 888

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
		t.Fatalf("expected exactly 1 live transaction (tombstoned must be excluded) — got total=%d transactions=%v", resp.Total, resp.Transactions)
	}
	desc, _ := resp.Transactions[0]["description"].(string)
	if !strings.Contains(desc, "boundary-day") {
		t.Fatalf("tombstoned boundary row leaked into /api/transactions list filter — t.deleted_at IS NULL predicate broke. got description=%q", desc)
	}
}
