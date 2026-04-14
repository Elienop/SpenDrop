package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestBuildTransactionWhereClause_Empty(t *testing.T) {
	q := url.Values{}
	where, args := buildTransactionWhereClause(q)
	if where != "" {
		t.Errorf("expected empty where clause, got %q", where)
	}
	if len(args) != 0 {
		t.Errorf("expected no args, got %d", len(args))
	}
}

func TestBuildTransactionWhereClause_DateFrom(t *testing.T) {
	q := url.Values{"date_from": {"2025-01-01"}}
	where, args := buildTransactionWhereClause(q)
	if where != " WHERE t.date >= ?" {
		t.Errorf("unexpected where clause: %q", where)
	}
	if len(args) != 1 || args[0] != "2025-01-01" {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestBuildTransactionWhereClause_DateTo(t *testing.T) {
	q := url.Values{"date_to": {"2025-12-31"}}
	where, args := buildTransactionWhereClause(q)
	if where != " WHERE t.date <= ?" {
		t.Errorf("unexpected where clause: %q", where)
	}
	if len(args) != 1 || args[0] != "2025-12-31" {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestBuildTransactionWhereClause_DateRange(t *testing.T) {
	q := url.Values{
		"date_from": {"2025-01-01"},
		"date_to":   {"2025-12-31"},
	}
	where, args := buildTransactionWhereClause(q)
	if where != " WHERE t.date >= ? AND t.date <= ?" {
		t.Errorf("unexpected where clause: %q", where)
	}
	if len(args) != 2 {
		t.Errorf("expected 2 args, got %d", len(args))
	}
}

func TestBuildTransactionWhereClause_InvalidDate(t *testing.T) {
	q := url.Values{"date_from": {"not-a-date"}}
	where, args := buildTransactionWhereClause(q)
	if where != "" {
		t.Errorf("expected empty where for invalid date, got %q", where)
	}
	if len(args) != 0 {
		t.Errorf("expected no args for invalid date, got %d", len(args))
	}
}

func TestBuildTransactionWhereClause_CategoryID(t *testing.T) {
	q := url.Values{"category_id": {"5"}}
	where, args := buildTransactionWhereClause(q)
	if where != " WHERE t.category_id = ?" {
		t.Errorf("unexpected where clause: %q", where)
	}
	if len(args) != 1 || args[0] != int64(5) {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestBuildTransactionWhereClause_CategoryIDIgnoredWhenCategoryIDs(t *testing.T) {
	q := url.Values{
		"category_id":  {"5"},
		"category_ids": {"1,2,3"},
	}
	where, args := buildTransactionWhereClause(q)
	// category_id should be ignored when category_ids is present
	if where != " WHERE t.category_id IN (?,?,?)" {
		t.Errorf("unexpected where clause: %q", where)
	}
	if len(args) != 3 {
		t.Errorf("expected 3 args, got %d: %v", len(args), args)
	}
}

func TestBuildTransactionWhereClause_Type(t *testing.T) {
	q := url.Values{"type": {"expense"}}
	where, args := buildTransactionWhereClause(q)
	if where != " WHERE c.type = ?" {
		t.Errorf("unexpected where clause: %q", where)
	}
	if len(args) != 1 || args[0] != "expense" {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestBuildTransactionWhereClause_TypeInvalid(t *testing.T) {
	q := url.Values{"type": {"invalid"}}
	where, args := buildTransactionWhereClause(q)
	if where != "" {
		t.Errorf("expected empty where for invalid type, got %q", where)
	}
	if len(args) != 0 {
		t.Errorf("expected no args for invalid type, got %d", len(args))
	}
}

func TestBuildTransactionWhereClause_Search(t *testing.T) {
	q := url.Values{"search": {"groceries"}}
	where, args := buildTransactionWhereClause(q)
	if where != " WHERE t.description LIKE ? ESCAPE '\\'" {
		t.Errorf("unexpected where clause: %q", where)
	}
	if len(args) != 1 || args[0] != "%groceries%" {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestBuildTransactionWhereClause_AmountRange(t *testing.T) {
	q := url.Values{
		"amount_min": {"10.50"},
		"amount_max": {"100.00"},
	}
	where, args := buildTransactionWhereClause(q)
	if where != " WHERE t.amount >= ? AND t.amount <= ?" {
		t.Errorf("unexpected where clause: %q", where)
	}
	if len(args) != 2 {
		t.Errorf("expected 2 args, got %d", len(args))
	}
}

func TestBuildTransactionWhereClause_CategoryIDs(t *testing.T) {
	q := url.Values{"category_ids": {"1,3,5"}}
	where, args := buildTransactionWhereClause(q)
	if where != " WHERE t.category_id IN (?,?,?)" {
		t.Errorf("unexpected where clause: %q", where)
	}
	if len(args) != 3 {
		t.Errorf("expected 3 args, got %d", len(args))
	}
}

func TestBuildTransactionWhereClause_Tags(t *testing.T) {
	q := url.Values{"tags": {"food"}}
	where, args := buildTransactionWhereClause(q)
	if where != " WHERE t.tags LIKE ? ESCAPE '\\'" {
		t.Errorf("unexpected where clause: %q", where)
	}
	if len(args) != 1 || args[0] != "%food%" {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestBuildTransactionWhereClause_TagsEscapesSpecialChars(t *testing.T) {
	q := url.Values{"tags": {"100%"}}
	where, args := buildTransactionWhereClause(q)
	if where != " WHERE t.tags LIKE ? ESCAPE '\\'" {
		t.Errorf("unexpected where clause: %q", where)
	}
	if len(args) != 1 || args[0] != "%100\\%%" {
		t.Errorf("unexpected args: %v (expected %%100\\%%%%)", args)
	}
}

func TestBuildTransactionWhereClause_MultipleFilters(t *testing.T) {
	q := url.Values{
		"date_from":  {"2025-01-01"},
		"date_to":    {"2025-12-31"},
		"type":       {"expense"},
		"search":     {"rent"},
		"amount_min": {"500"},
	}
	where, args := buildTransactionWhereClause(q)
	expected := " WHERE t.date >= ? AND t.date <= ? AND c.type = ? AND t.description LIKE ? ESCAPE '\\' AND t.amount >= ?"
	if where != expected {
		t.Errorf("unexpected where clause:\ngot:  %q\nwant: %q", where, expected)
	}
	if len(args) != 5 {
		t.Errorf("expected 5 args, got %d: %v", len(args), args)
	}
}

// --- handleExportTransactions ---

func TestHandleExportTransactions_Unauthorized(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)

	req := httptest.NewRequest(http.MethodGet, "/api/export/transactions", nil)
	rec := httptest.NewRecorder()

	h.handleExportTransactions(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestHandleExportTransactions_ReturnsXLSX(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)

	user := seedTestUser(t, q, "alice", "member")
	// category_id=1 exists from seed migrations (Food/expense)
	seedTestTransaction(t, q, user.ID, 1, "2026-03-15", 42.50, "Lunch")
	seedTestTransaction(t, q, user.ID, 1, "2026-03-16", 18.00, "Coffee")

	req := httptest.NewRequest(http.MethodGet, "/api/export/transactions", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleExportTransactions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" {
		t.Errorf("unexpected content-type: %s", ct)
	}

	cd := rec.Header().Get("Content-Disposition")
	if cd == "" {
		t.Error("expected Content-Disposition header")
	}

	// Parse the xlsx to verify contents
	f, err := excelize.OpenReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("failed to parse xlsx: %v", err)
	}
	defer f.Close()

	// Should have a Transactions sheet
	rows, err := f.GetRows("Transactions")
	if err != nil {
		t.Fatalf("failed to get Transactions sheet: %v", err)
	}
	// Header + 2 data rows
	if len(rows) != 3 {
		t.Errorf("expected 3 rows (1 header + 2 data), got %d", len(rows))
	}
	// Verify header
	if len(rows) > 0 && rows[0][0] != "Date" {
		t.Errorf("expected first header 'Date', got %q", rows[0][0])
	}
}

func TestHandleExportTransactions_WithDateFilter(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)

	user := seedTestUser(t, q, "alice", "member")
	seedTestTransaction(t, q, user.ID, 1, "2026-01-15", 10.00, "January")
	seedTestTransaction(t, q, user.ID, 1, "2026-03-15", 20.00, "March")

	req := httptest.NewRequest(http.MethodGet, "/api/export/transactions?date_from=2026-03-01&date_to=2026-03-31", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleExportTransactions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	f, err := excelize.OpenReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("parse xlsx: %v", err)
	}
	defer f.Close()

	rows, _ := f.GetRows("Transactions")
	// Header + 1 filtered row (only March)
	if len(rows) != 2 {
		t.Errorf("expected 2 rows (1 header + 1 data), got %d", len(rows))
	}
}

// --- handleExportMonthly ---

func TestHandleExportMonthly_Unauthorized(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)

	req := httptest.NewRequest(http.MethodGet, "/api/export/monthly/2026/03", nil)
	rec := httptest.NewRecorder()

	h.handleExportMonthly(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestHandleExportMonthly_InvalidYear(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)

	user := seedTestUser(t, q, "alice", "member")
	req := httptest.NewRequest(http.MethodGet, "/api/export/monthly/abc/03", nil)
	req = withUserAndURLParams(req, user, map[string]string{"year": "abc", "month": "03"})
	rec := httptest.NewRecorder()

	h.handleExportMonthly(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleExportMonthly_InvalidMonth(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)

	user := seedTestUser(t, q, "alice", "member")
	req := httptest.NewRequest(http.MethodGet, "/api/export/monthly/2026/13", nil)
	req = withUserAndURLParams(req, user, map[string]string{"year": "2026", "month": "13"})
	rec := httptest.NewRecorder()

	h.handleExportMonthly(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleExportMonthly_ReturnsXLSXWithTwoSheets(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)

	user := seedTestUser(t, q, "alice", "member")
	seedTestTransaction(t, q, user.ID, 1, "2026-03-10", 50.00, "Groceries")
	seedTestTransaction(t, q, user.ID, 1, "2026-03-20", 30.00, "Takeout")

	req := httptest.NewRequest(http.MethodGet, "/api/export/monthly/2026/03", nil)
	req = withUserAndURLParams(req, user, map[string]string{"year": "2026", "month": "03"})
	rec := httptest.NewRecorder()

	h.handleExportMonthly(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	f, err := excelize.OpenReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("parse xlsx: %v", err)
	}
	defer f.Close()

	sheets := f.GetSheetList()
	if len(sheets) != 2 {
		t.Fatalf("expected 2 sheets, got %d: %v", len(sheets), sheets)
	}

	// Summary sheet should have at least header + 1 category row
	summaryRows, _ := f.GetRows("Summary")
	if len(summaryRows) < 2 {
		t.Errorf("expected at least 2 rows in Summary sheet, got %d", len(summaryRows))
	}

	// Transactions sheet should have header + 2 data rows
	txnRows, _ := f.GetRows("Transactions")
	if len(txnRows) != 3 {
		t.Errorf("expected 3 rows in Transactions sheet, got %d", len(txnRows))
	}
}

// --- handleExportYearly ---

func TestHandleExportYearly_Unauthorized(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)

	req := httptest.NewRequest(http.MethodGet, "/api/export/yearly/2026", nil)
	rec := httptest.NewRecorder()

	h.handleExportYearly(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestHandleExportYearly_InvalidYear(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)

	user := seedTestUser(t, q, "alice", "member")
	req := httptest.NewRequest(http.MethodGet, "/api/export/yearly/abc", nil)
	req = withUserAndURLParams(req, user, map[string]string{"year": "abc"})
	rec := httptest.NewRecorder()

	h.handleExportYearly(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleExportYearly_ReturnsXLSXWithTwoSheets(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)

	user := seedTestUser(t, q, "alice", "member")
	// Seed transactions in different months
	seedTestTransaction(t, q, user.ID, 1, "2026-01-15", 100.00, "January expense")
	seedTestTransaction(t, q, user.ID, 1, "2026-03-15", 200.00, "March expense")
	seedTestTransaction(t, q, user.ID, 1, "2026-06-15", 150.00, "June expense")

	req := httptest.NewRequest(http.MethodGet, "/api/export/yearly/2026", nil)
	req = withUserAndURLParams(req, user, map[string]string{"year": "2026"})
	rec := httptest.NewRecorder()

	h.handleExportYearly(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	f, err := excelize.OpenReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("parse xlsx: %v", err)
	}
	defer f.Close()

	sheets := f.GetSheetList()
	if len(sheets) != 2 {
		t.Fatalf("expected 2 sheets, got %d: %v", len(sheets), sheets)
	}

	// Monthly Totals: header + 12 month rows
	monthlyRows, _ := f.GetRows("Monthly Totals")
	if len(monthlyRows) != 13 {
		t.Errorf("expected 13 rows in Monthly Totals (header + 12 months), got %d", len(monthlyRows))
	}
	// Verify first month name
	if len(monthlyRows) > 1 && monthlyRows[1][0] != "January" {
		t.Errorf("expected first month 'January', got %q", monthlyRows[1][0])
	}

	// Category Totals: header + at least 1 category
	catRows, _ := f.GetRows("Category Totals")
	if len(catRows) < 2 {
		t.Errorf("expected at least 2 rows in Category Totals, got %d", len(catRows))
	}
}

// --- Phase 2.1 soft-delete invariant: export read paths must hide tombstoned rows ---

func TestHandleExportTransactions_HidesTombstoned(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	// One live row plus one tombstoned row in the same date window. If the
	// liveClause helper is ever dropped from handleExportTransactions, the
	// xlsx will contain 2 data rows instead of 1.
	seedTestTransaction(t, q, user.ID, 1, "2026-03-15", 42.50, "live Lunch")
	seedTombstonedTestTransaction(t, q, user.ID, 1, "2026-03-16", 999.00, "tombstoned")

	req := httptest.NewRequest(http.MethodGet, "/api/export/transactions", nil)
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
	// Header + 1 data row (tombstoned must be excluded).
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows (1 header + 1 live), got %d", len(rows))
	}
	// Verify the surviving row is the live one, not the tombstoned one.
	if len(rows[1]) < 2 || rows[1][1] != "live Lunch" {
		t.Errorf("expected live row description 'live Lunch', got %v", rows[1])
	}
}

func TestHandleExportMonthly_HidesTombstoned(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	// Category 1 = Food (expense) from seed.
	seedTestTransaction(t, q, user.ID, 1, "2026-03-10", 50.00, "live")
	seedTombstonedTestTransaction(t, q, user.ID, 1, "2026-03-20", 999.00, "tombstoned")

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

	// Transactions sheet: header + 1 live row.
	txnRows, _ := f.GetRows("Transactions")
	if len(txnRows) != 2 {
		t.Fatalf("expected 2 rows in Transactions sheet (1 header + 1 live), got %d", len(txnRows))
	}

	// Summary sheet: Food category total must be 50, not 1049.
	summaryRows, _ := f.GetRows("Summary")
	if len(summaryRows) < 2 {
		t.Fatalf("expected at least 2 rows in Summary sheet, got %d", len(summaryRows))
	}
	// Find the Food row.
	foundFood := false
	for _, row := range summaryRows[1:] {
		if len(row) >= 3 && row[0] == "Food" {
			foundFood = true
			if row[2] != "50" {
				t.Errorf("Food total=%q, want 50 (tombstoned 999 must be excluded)", row[2])
			}
		}
	}
	if !foundFood {
		t.Errorf("Food row missing from Summary sheet")
	}
}

func TestHandleExportYearly_HidesTombstoned(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	seedTestTransaction(t, q, user.ID, 1, "2026-03-15", 200.00, "live")
	seedTombstonedTestTransaction(t, q, user.ID, 1, "2026-03-20", 999.00, "tombstoned")

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

	// Monthly Totals: March (index 4 = header + March=3) expenses must be 200.
	monthlyRows, _ := f.GetRows("Monthly Totals")
	if len(monthlyRows) != 13 {
		t.Fatalf("expected 13 rows in Monthly Totals, got %d", len(monthlyRows))
	}
	// Row index 3 (1-based row 4) = March (January=row 2, February=row 3, March=row 4).
	march := monthlyRows[3]
	if len(march) < 2 || march[0] != "March" {
		t.Fatalf("expected March row at index 3, got %v", march)
	}
	if march[1] != "200" {
		t.Errorf("March expenses=%q, want 200 (tombstoned 999 must be excluded)", march[1])
	}

	// Category Totals: Food row must total 200, not 1199.
	catRows, _ := f.GetRows("Category Totals")
	foundFood := false
	for _, row := range catRows[1:] {
		if len(row) >= 3 && row[0] == "Food" {
			foundFood = true
			if row[2] != "200" {
				t.Errorf("Food total=%q, want 200 (tombstoned 999 must be excluded)", row[2])
			}
		}
	}
	if !foundFood {
		t.Errorf("Food row missing from Category Totals sheet")
	}
}
