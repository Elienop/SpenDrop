package api

// B10: the export summary sheets are gated on "this category HAS live rows in
// the window" (HAVING COUNT(t.id) > 0), not on a positive total.
//
// Before B10 the gate was HAVING total_cents > 0, which was indistinguishable
// from "has rows" while every amount was positive. Signed amounts split the two
// apart: a category whose purchases are cancelled by refunds nets to zero or
// below while still holding rows. Under the old gate that category vanished
// from the Summary sheet while its rows kept printing on the Transactions sheet
// of the SAME workbook — one file, two sheets, disagreeing about whether a
// category exists.
//
// Two mutants have to die here, and they fail different tests on purpose:
//
//   - reverting to `HAVING total_cents > 0` is killed by the net-zero and
//     net-negative tests, which assert the category is PRESENT with its signed
//     total;
//   - weakening `COUNT(t.id)` to `COUNT(*)` is killed by the empty-category and
//     tombstone-only tests, which assert the sheet holds ONLY the categories
//     that have rows. Over a LEFT JOIN an unmatched category still yields one
//     NULL-extended row, so COUNT(*) counts it as 1 and all nineteen seeded
//     categories reappear as zero-total lines.
//
// What these tests deliberately do NOT claim to pin: the ON-vs-WHERE placement
// of `t.deleted_at IS NULL`. Measured against SQLite across live-only,
// tombstone-only, mixed, net-zero, empty and out-of-window categories, both
// placements produce identical output under this HAVING — `t.deleted_at IS
// NULL` is the one predicate shape a NULL-extended row satisfies, and a
// tombstone-only category loses its whole group to a WHERE rather than
// surviving with a zero count. The placement earns its keep only against a
// FUTURE relaxation of the HAVING, which is exactly what the comment at the
// query says and is not something a test of today's output can observe.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/elienop/spendrop/internal/database"
)

// sheetRowsByCategory indexes a Category/Type/Total sheet by category name,
// skipping the header row.
func sheetRowsByCategory(t *testing.T, f *excelize.File, sheet string) map[string][]string {
	t.Helper()
	rows, err := f.GetRows(sheet)
	if err != nil {
		t.Fatalf("get %s sheet: %v", sheet, err)
	}
	if len(rows) == 0 {
		t.Fatalf("%s sheet is empty (not even a header row)", sheet)
	}
	out := make(map[string][]string, len(rows)-1)
	for _, row := range rows[1:] {
		if len(row) > 0 {
			out[row[0]] = row
		}
	}
	return out
}

// categoryName reads a seeded category's stored name back. seedExpenseCategory
// suffixes names with t.Name() to dodge UNIQUE collisions with the migration
// seed, so the sheet cell never matches the literal passed in.
func categoryName(t *testing.T, q *database.Queries, id int64) string {
	t.Helper()
	cat, err := q.GetCategoryByID(context.Background(), id)
	if err != nil {
		t.Fatalf("look up category %d: %v", id, err)
	}
	return cat.Name
}

// exportMonthlyWorkbook drives handleExportMonthly for 2026-05 and parses the
// result.
func exportMonthlyWorkbook(t *testing.T, h *Handler, user database.User) *excelize.File {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/export/monthly/2026/05", nil)
	req = withUserAndURLParams(req, user, map[string]string{"year": "2026", "month": "05"})
	rec := httptest.NewRecorder()
	h.handleExportMonthly(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	f, err := excelize.OpenReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("parse xlsx: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

// exportYearlyWorkbook drives handleExportYearly for 2026 and parses the result.
func exportYearlyWorkbook(t *testing.T, h *Handler, user database.User) *excelize.File {
	t.Helper()
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
	t.Cleanup(func() { f.Close() })
	return f
}

// The headline B10 export test: a category refunded to exactly zero must appear
// on the Summary sheet with total 0 AND have its rows on the Transactions sheet
// of the same workbook. Reverting to `HAVING total_cents > 0` deletes the
// Summary row and this fails.
func TestHandleExportMonthly_NetZeroCategoryOnBothSheets(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", RoleMember)
	cat := seedExpenseCategory(t, q, "Electronics")
	catName := categoryName(t, q, cat)

	seedTestTransaction(t, q, user.ID, cat, "2026-05-04", 250.00, "Headphones")
	seedTestTransaction(t, q, user.ID, cat, "2026-05-18", -250.00, "Headphones returned")

	f := exportMonthlyWorkbook(t, h, user)

	summary := sheetRowsByCategory(t, f, "Summary")
	row, ok := summary[catName]
	if !ok {
		t.Fatalf("refund-netted category %q missing from the Summary sheet — its rows print on the Transactions sheet of this same workbook, so the two sheets now contradict each other. Summary held: %v", catName, summary)
	}
	if len(row) < 3 || row[2] != "0" {
		t.Errorf("Summary total for %q = %v, want 0", catName, row)
	}

	txnRows, err := f.GetRows("Transactions")
	if err != nil {
		t.Fatalf("get Transactions sheet: %v", err)
	}
	// Header + the purchase + the refund.
	if len(txnRows) != 3 {
		t.Fatalf("want 3 rows on the Transactions sheet (header + 2), got %d: %v", len(txnRows), txnRows)
	}
	if !rowsContainAmount(txnRows, "250") {
		t.Errorf("purchase row missing from the Transactions sheet: %v", txnRows)
	}
	if !rowsContainAmount(txnRows, "-250") {
		t.Errorf("refund row missing from the Transactions sheet: %v", txnRows)
	}
}

// Net-negative is the other side of the same gate, and it also pins that the
// signed total reaches the cell rather than being clamped at zero.
func TestHandleExportMonthly_NetNegativeCategoryOnSummarySheet(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", RoleMember)
	cat := seedExpenseCategory(t, q, "Electronics")
	catName := categoryName(t, q, cat)

	seedTestTransaction(t, q, user.ID, cat, "2026-05-04", 40.00, "Cable")
	seedTestTransaction(t, q, user.ID, cat, "2026-05-18", -900.00, "Laptop returned")

	f := exportMonthlyWorkbook(t, h, user)
	summary := sheetRowsByCategory(t, f, "Summary")
	row, ok := summary[catName]
	if !ok {
		t.Fatalf("net-negative category %q missing from the Summary sheet: %v", catName, summary)
	}
	if len(row) < 3 || row[2] != "-860" {
		t.Errorf("Summary total for %q = %v, want -860 (40 - 900)", catName, row)
	}
}

// COUNT(t.id) mutant killer: a category with no rows in the window must stay
// off the sheet. Migration 001 seeds nineteen categories, so under COUNT(*)
// every one of them would appear as a zero-total line.
func TestHandleExportMonthly_RowlessCategoriesStayOffSummarySheet(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", RoleMember)
	cat := seedExpenseCategory(t, q, "Groceries")
	catName := categoryName(t, q, cat)

	seedTestTransaction(t, q, user.ID, cat, "2026-05-04", 60.00, "Shop")

	f := exportMonthlyWorkbook(t, h, user)
	rows, err := f.GetRows("Summary")
	if err != nil {
		t.Fatalf("get Summary sheet: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want exactly 2 rows on the Summary sheet (header + the one category with rows), got %d — a COUNT(*) over the LEFT JOIN counts the NULL-extended row and resurrects every empty category: %v", len(rows), rows)
	}
	if rows[1][0] != catName {
		t.Errorf("surviving Summary row = %q, want %q", rows[1][0], catName)
	}
}

// Same mutant, the case that matters most: a category whose ONLY rows in the
// window are tombstoned has a real joined row waiting to be miscounted.
func TestHandleExportMonthly_TombstoneOnlyCategoryStaysOffSummarySheet(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", RoleMember)
	live := seedExpenseCategory(t, q, "Groceries")
	buried := seedExpenseCategory(t, q, "Electronics")
	liveName := categoryName(t, q, live)
	buriedName := categoryName(t, q, buried)

	seedTestTransaction(t, q, user.ID, live, "2026-05-04", 60.00, "Shop")
	seedTombstonedTestTransaction(t, q, user.ID, buried, "2026-05-06", 999.00, "deleted")

	f := exportMonthlyWorkbook(t, h, user)
	summary := sheetRowsByCategory(t, f, "Summary")
	if _, ok := summary[buriedName]; ok {
		t.Errorf("tombstone-only category %q appeared on the Summary sheet: %v", buriedName, summary[buriedName])
	}
	if _, ok := summary[liveName]; !ok {
		t.Fatalf("live category %q missing from the Summary sheet: %v", liveName, summary)
	}
	if len(summary) != 1 {
		t.Errorf("want exactly 1 Summary category, got %d: %v", len(summary), summary)
	}
}

// The yearly Category Totals sheet carries the identical gate. Same three
// shapes, driven through the other handler so a half-applied change is caught.
func TestHandleExportYearly_NetZeroCategoryOnCategoryTotals(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", RoleMember)
	cat := seedExpenseCategory(t, q, "Electronics")
	catName := categoryName(t, q, cat)

	seedTestTransaction(t, q, user.ID, cat, "2026-02-04", 250.00, "Headphones")
	seedTestTransaction(t, q, user.ID, cat, "2026-09-18", -250.00, "Headphones returned")

	f := exportYearlyWorkbook(t, h, user)
	cats := sheetRowsByCategory(t, f, "Category Totals")
	row, ok := cats[catName]
	if !ok {
		t.Fatalf("refund-netted category %q missing from the yearly Category Totals sheet: %v", catName, cats)
	}
	if len(row) < 3 || row[2] != "0" {
		t.Errorf("Category Totals row for %q = %v, want total 0", catName, row)
	}
}

func TestHandleExportYearly_RowlessCategoriesStayOffCategoryTotals(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", RoleMember)
	live := seedExpenseCategory(t, q, "Groceries")
	buried := seedExpenseCategory(t, q, "Electronics")
	liveName := categoryName(t, q, live)

	seedTestTransaction(t, q, user.ID, live, "2026-05-04", 60.00, "Shop")
	seedTombstonedTestTransaction(t, q, user.ID, buried, "2026-05-06", 999.00, "deleted")

	f := exportYearlyWorkbook(t, h, user)
	rows, err := f.GetRows("Category Totals")
	if err != nil {
		t.Fatalf("get Category Totals: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want exactly 2 rows on Category Totals (header + the one category with live rows), got %d: %v", len(rows), rows)
	}
	if rows[1][0] != liveName {
		t.Errorf("surviving Category Totals row = %q, want %q", rows[1][0], liveName)
	}
}

// The yearly monthlyQuery CASE-splits expense and income; both halves net, and
// Net = income - expenses stays the arithmetic it always was.
func TestHandleExportYearly_MonthlyTotalsSignedNet(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", RoleMember)
	spend := seedExpenseCategory(t, q, "Groceries")
	earn := seedIncomeCategory(t, q, "Salary")

	seedTestTransaction(t, q, user.ID, spend, "2026-05-04", 300.00, "Shop")
	seedTestTransaction(t, q, user.ID, spend, "2026-05-19", -50.00, "Returned")
	seedTestTransaction(t, q, user.ID, earn, "2026-05-01", 2000.00, "Pay")
	seedTestTransaction(t, q, user.ID, earn, "2026-05-08", -200.00, "Clawback")

	f := exportYearlyWorkbook(t, h, user)
	rows, err := f.GetRows("Monthly Totals")
	if err != nil {
		t.Fatalf("get Monthly Totals: %v", err)
	}
	if len(rows) != 13 {
		t.Fatalf("want 13 rows (header + 12 months), got %d", len(rows))
	}
	may := rows[5] // header, Jan, Feb, Mar, Apr, May
	if may[0] != "May" {
		t.Fatalf("expected May at index 5, got %v", may)
	}
	if may[1] != "250" {
		t.Errorf("May expenses = %q, want 250 (300 - 50)", may[1])
	}
	if may[2] != "1800" {
		t.Errorf("May income = %q, want 1800 (2000 - 200)", may[2])
	}
	if may[3] != "1550" {
		t.Errorf("May net = %q, want 1550 (1800 - 250)", may[3])
	}
}

// A month whose refunds outweigh its purchases prints a NEGATIVE expenses cell,
// and Net then exceeds income. Both are legal; neither was reachable before.
func TestHandleExportYearly_MonthlyTotalsCanGoNegative(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", RoleMember)
	spend := seedExpenseCategory(t, q, "Electronics")

	seedTestTransaction(t, q, user.ID, spend, "2026-05-04", 40.00, "Cable")
	seedTestTransaction(t, q, user.ID, spend, "2026-05-19", -900.00, "Laptop returned")

	f := exportYearlyWorkbook(t, h, user)
	rows, _ := f.GetRows("Monthly Totals")
	may := rows[5]
	if may[1] != "-860" {
		t.Errorf("May expenses = %q, want -860", may[1])
	}
	if may[3] != "860" {
		t.Errorf("May net = %q, want 860 (0 income - (-860) expenses)", may[3])
	}
}

// --- Heatmap wire contract ---

// WP6 keys cell paint, labels and the day-sheet fetch on txn_count, so the name
// and type are a contract, not an implementation detail.
func TestHandleSpendingHeatmap_EmitsTxnCountAlongsideSignedTotal(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", RoleMember)
	cat := seedExpenseCategory(t, q, "Groceries")

	seedTestTransaction(t, q, user.ID, cat, "2026-05-03", 40.00, "Shop")
	seedTestTransaction(t, q, user.ID, cat, "2026-05-10", 30.00, "Shop")
	seedTestTransaction(t, q, user.ID, cat, "2026-05-10", -30.00, "Returned")

	req := httptest.NewRequest(http.MethodGet, "/api/reports/heatmap?year=2026", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleSpendingHeatmap(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Decode into map[string]any, not a typed struct: a typed decode zero-fills
	// a renamed or missing key and the assertion passes on a broken wire.
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	byDate := make(map[string]map[string]any, len(resp.Data))
	for _, d := range resp.Data {
		date, _ := d["date"].(string)
		byDate[date] = d
	}

	ordinary, ok := byDate["2026-05-03"]
	if !ok {
		t.Fatalf("2026-05-03 missing: %+v", resp.Data)
	}
	if _, present := ordinary["txn_count"]; !present {
		t.Fatalf("heatmap entry has no txn_count key — WP6 reads exactly this name: %+v", ordinary)
	}
	if n, _ := ordinary["txn_count"].(float64); n != 1 {
		t.Errorf("2026-05-03 txn_count = %v, want 1", ordinary["txn_count"])
	}
	if v, _ := ordinary["total"].(float64); v != 40 {
		t.Errorf("2026-05-03 total = %v, want 40", ordinary["total"])
	}

	netZero, ok := byDate["2026-05-10"]
	if !ok {
		t.Fatalf("net-zero day 2026-05-10 missing from the heatmap: %+v", resp.Data)
	}
	if v, _ := netZero["total"].(float64); v != 0 {
		t.Errorf("2026-05-10 total = %v, want 0", netZero["total"])
	}
	if n, _ := netZero["txn_count"].(float64); n != 2 {
		t.Errorf("2026-05-10 txn_count = %v, want 2 — total alone cannot tell this day from an empty one", netZero["txn_count"])
	}
}

// The soft-delete arm for the NEW column specifically. The existing
// TestHandleSpendingHeatmap_HidesTombstoned in reports_new_handlers_test.go
// asserts only on `total`, so it stays green with txn_count counting
// tombstones; this one is what makes the new column obey the same discipline.
func TestHandleSpendingHeatmap_TxnCountHidesTombstoned(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", RoleMember)
	cat := seedExpenseCategory(t, q, "Groceries")

	seedTestTransaction(t, q, user.ID, cat, "2026-05-03", 40.00, "live")
	seedTombstonedTestTransaction(t, q, user.ID, cat, "2026-05-03", 999.00, "tombstoned")

	req := httptest.NewRequest(http.MethodGet, "/api/reports/heatmap?year=2026", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleSpendingHeatmap(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("want 1 heatmap day, got %d: %+v", len(resp.Data), resp.Data)
	}
	if v, _ := resp.Data[0]["total"].(float64); v != 40 {
		t.Errorf("total = %v, want 40 (tombstoned 999 must be excluded)", resp.Data[0]["total"])
	}
	if n, _ := resp.Data[0]["txn_count"].(float64); n != 1 {
		t.Errorf("txn_count = %v, want 1 — the tombstoned row must not be counted either", resp.Data[0]["txn_count"])
	}
}
