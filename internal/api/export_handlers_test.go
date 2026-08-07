package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

// TestBuildTransactionWhereClause covers every supported query-parameter
// shape plus the composite case. The table is the single source of truth —
// adding a new filter means adding one row. Expected args are compared
// with reflect.DeepEqual, which is strictly stronger than the original
// length-only checks: a drift in argument order or type would fail here
// but slip past the old per-test length assertions.
//
// Phase 3.1a: the amount filter compares against t.amount_cents (int64),
// not t.amount (float64). The float bound is converted to cents at parse
// time so SQLite never sees a float in the comparison.
func TestBuildTransactionWhereClause(t *testing.T) {
	cases := []struct {
		name      string
		q         url.Values
		wantWhere string
		wantArgs  []any
	}{
		{
			name:      "empty",
			q:         url.Values{},
			wantWhere: "",
			wantArgs:  nil,
		},
		{
			name:      "date_from",
			q:         url.Values{"date_from": {"2025-01-01"}},
			wantWhere: " WHERE date(t.date) >= ?",
			wantArgs:  []any{"2025-01-01"},
		},
		{
			name:      "date_to",
			q:         url.Values{"date_to": {"2025-12-31"}},
			wantWhere: " WHERE date(t.date) <= ?",
			wantArgs:  []any{"2025-12-31"},
		},
		{
			name: "date range",
			q: url.Values{
				"date_from": {"2025-01-01"},
				"date_to":   {"2025-12-31"},
			},
			wantWhere: " WHERE date(t.date) >= ? AND date(t.date) <= ?",
			wantArgs:  []any{"2025-01-01", "2025-12-31"},
		},
		{
			name:      "invalid date is dropped",
			q:         url.Values{"date_from": {"not-a-date"}},
			wantWhere: "",
			wantArgs:  nil,
		},
		{
			name:      "category_id",
			q:         url.Values{"category_id": {"5"}},
			wantWhere: " WHERE t.category_id = ?",
			wantArgs:  []any{int64(5)},
		},
		{
			name: "category_id ignored when category_ids present",
			q: url.Values{
				"category_id":  {"5"},
				"category_ids": {"1,2,3"},
			},
			wantWhere: " WHERE t.category_id IN (?,?,?)",
			wantArgs:  []any{int64(1), int64(2), int64(3)},
		},
		{
			name:      "type",
			q:         url.Values{"type": {"expense"}},
			wantWhere: " WHERE c.type = ?",
			wantArgs:  []any{"expense"},
		},
		{
			name:      "invalid type is dropped",
			q:         url.Values{"type": {"invalid"}},
			wantWhere: "",
			wantArgs:  nil,
		},
		{
			// B6i: search spans description, notes and category name. Tags
			// stay out — they have their own filter.
			name:      "search",
			q:         url.Values{"search": {"groceries"}},
			wantWhere: " WHERE (t.description LIKE ? ESCAPE '\\' OR t.notes LIKE ? ESCAPE '\\' OR c.name LIKE ? ESCAPE '\\')",
			wantArgs:  []any{"%groceries%", "%groceries%", "%groceries%"},
		},
		{
			// A bare number adds the foreign-amount arm on top of the text
			// ones, matching original_amount_cents exactly.
			name:      "numeric search adds the foreign amount arm",
			q:         url.Values{"search": {"1500000"}},
			wantWhere: " WHERE (t.description LIKE ? ESCAPE '\\' OR t.notes LIKE ? ESCAPE '\\' OR c.name LIKE ? ESCAPE '\\' OR t.original_amount_cents = ?)",
			wantArgs:  []any{"%1500000%", "%1500000%", "%1500000%", int64(150000000)},
		},
		{
			// Thousands separators are accepted; the text arms still match
			// the term exactly as typed.
			name:      "numeric search accepts thousands separators",
			q:         url.Values{"search": {"1,500,000"}},
			wantWhere: " WHERE (t.description LIKE ? ESCAPE '\\' OR t.notes LIKE ? ESCAPE '\\' OR c.name LIKE ? ESCAPE '\\' OR t.original_amount_cents = ?)",
			wantArgs:  []any{"%1,500,000%", "%1,500,000%", "%1,500,000%", int64(150000000)},
		},
		{
			// Anything that is not a bare number gets no amount arm, so an
			// ordinary text search cannot pick up rows by amount.
			name:      "mixed text search gets no amount arm",
			q:         url.Values{"search": {"1500000 LBP"}},
			wantWhere: " WHERE (t.description LIKE ? ESCAPE '\\' OR t.notes LIKE ? ESCAPE '\\' OR c.name LIKE ? ESCAPE '\\')",
			wantArgs:  []any{"%1500000 LBP%", "%1500000 LBP%", "%1500000 LBP%"},
		},
		{
			// Past MaxTransactionAmount there is no storable row to find, and
			// clamping would compare against a nonsense value.
			name:      "out-of-range number gets no amount arm",
			q:         url.Values{"search": {"9999999999"}},
			wantWhere: " WHERE (t.description LIKE ? ESCAPE '\\' OR t.notes LIKE ? ESCAPE '\\' OR c.name LIKE ? ESCAPE '\\')",
			wantArgs:  []any{"%9999999999%", "%9999999999%", "%9999999999%"},
		},
		// The next three are the anchors on searchAmountPattern earning their
		// keep. ParseFloat alone is NOT the guard: it happily accepts every one
		// of these, so an unanchored pattern would hand the amount arm a value
		// the user never typed — 100,000 for "1e5", a negative for "-100", and
		// 123 for a term whose comma groups are not thousands.
		{
			name:      "exponent notation gets no amount arm",
			q:         url.Values{"search": {"1e5"}},
			wantWhere: " WHERE (t.description LIKE ? ESCAPE '\\' OR t.notes LIKE ? ESCAPE '\\' OR c.name LIKE ? ESCAPE '\\')",
			wantArgs:  []any{"%1e5%", "%1e5%", "%1e5%"},
		},
		{
			name:      "signed number gets no amount arm",
			q:         url.Values{"search": {"-100"}},
			wantWhere: " WHERE (t.description LIKE ? ESCAPE '\\' OR t.notes LIKE ? ESCAPE '\\' OR c.name LIKE ? ESCAPE '\\')",
			wantArgs:  []any{"%-100%", "%-100%", "%-100%"},
		},
		{
			name:      "malformed comma groups get no amount arm",
			q:         url.Values{"search": {"1,2,3"}},
			wantWhere: " WHERE (t.description LIKE ? ESCAPE '\\' OR t.notes LIKE ? ESCAPE '\\' OR c.name LIKE ? ESCAPE '\\')",
			wantArgs:  []any{"%1,2,3%", "%1,2,3%", "%1,2,3%"},
		},
		{
			name: "amount range is compared in cents",
			q: url.Values{
				"amount_min": {"10.50"},
				"amount_max": {"100.00"},
			},
			wantWhere: " WHERE t.amount_cents >= ? AND t.amount_cents <= ?",
			wantArgs:  []any{int64(1050), int64(10000)},
		},
		{
			name:      "category_ids",
			q:         url.Values{"category_ids": {"1,3,5"}},
			wantWhere: " WHERE t.category_id IN (?,?,?)",
			wantArgs:  []any{int64(1), int64(3), int64(5)},
		},
		{
			name:      "tags",
			q:         url.Values{"tags": {"food"}},
			wantWhere: " WHERE t.tags LIKE ? ESCAPE '\\'",
			wantArgs:  []any{"%food%"},
		},
		{
			name:      "tags escapes LIKE metacharacters",
			q:         url.Values{"tags": {"100%"}},
			wantWhere: " WHERE t.tags LIKE ? ESCAPE '\\'",
			wantArgs:  []any{"%100\\%%"},
		},
		{
			name: "multiple filters compose in implementation order",
			q: url.Values{
				"date_from":  {"2025-01-01"},
				"date_to":    {"2025-12-31"},
				"type":       {"expense"},
				"search":     {"rent"},
				"amount_min": {"500"},
			},
			// The search group is parenthesised so it composes with AND
			// against the surrounding filters instead of rebinding them.
			wantWhere: " WHERE date(t.date) >= ? AND date(t.date) <= ? AND c.type = ? AND (t.description LIKE ? ESCAPE '\\' OR t.notes LIKE ? ESCAPE '\\' OR c.name LIKE ? ESCAPE '\\') AND t.amount_cents >= ?",
			wantArgs:  []any{"2025-01-01", "2025-12-31", "expense", "%rent%", "%rent%", "%rent%", int64(50000)},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			where, args := buildTransactionWhereClause(tc.q)
			if where != tc.wantWhere {
				t.Errorf("where = %q, want %q", where, tc.wantWhere)
			}
			if !reflect.DeepEqual(args, tc.wantArgs) {
				t.Errorf("args = %#v, want %#v", args, tc.wantArgs)
			}
		})
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

// FuzzSearchAmountCents drives the ?search= numeric parser with arbitrary
// input. The parser sits at an unauthenticated-reachable boundary (any
// authenticated household member can put any string in the querystring), and it
// feeds a value straight into a SQL comparison, so the two properties that
// matter are:
//
//   - it never panics, and
//   - when it reports ok, the cents value is in range: 0 <= cents <=
//     100*MaxTransactionAmount. A negative or overflowed value would compare
//     against original_amount_cents as nonsense, and the amount_min/amount_max
//     incident (see safeDollarsToCents) is the precedent for how int64 minimum
//     gets laundered in through a float.
//
// The lower bound is 0 rather than 1 because "0" is a legal parse; no row can
// carry a zero original amount (CHECK(amount_cents > 0) and the handler's own
// validation), so it simply matches nothing.
func FuzzSearchAmountCents(f *testing.F) {
	seeds := []string{
		// The unit-table cases, so the corpus starts from known behaviour.
		"1500000", "1,500,000", "1500000.50", "1,500,000.50", "1500", "0",
		"1500000 LBP", "9999999999", "Coffee", "",
		// Nasties from the security review.
		"1e5", "-100", "1,2,3", "١٥٠٠", "999999999999999999999999",
		strings.Repeat("9", 400),
		// Shapes the regex alternation has to decide between.
		"1,500", "12,34", ".50", "1500000.", "+1500", "1_500", "  1500  ",
		"NaN", "Inf", "-Inf", "0x1p4", "1500000.005",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, term string) {
		cents, ok := searchAmountCents(term)
		if !ok {
			// The contract for a rejected term is only that cents is unused;
			// the caller adds no arm at all.
			return
		}
		if cents < 0 {
			t.Fatalf("searchAmountCents(%q) accepted a NEGATIVE cents value %d", term, cents)
		}
		if cents > 100*MaxTransactionAmount {
			t.Fatalf("searchAmountCents(%q) accepted %d, above the %d cents cap",
				term, cents, int64(100*MaxTransactionAmount))
		}
	})
}
