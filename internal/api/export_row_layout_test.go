package api

import (
	"bytes"
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/elienop/spendrop/internal/database"
)

// These tests pin the row-layout invariants of writeExportTxnRows, which
// reuses one []any across every transaction. They exist because both halves of
// that reuse were previously correct and completely unguarded: an adversarial
// review deleted the nullable-column reset and the ENTIRE internal/api suite
// stayed green while the export silently mixed rows together — a transaction
// with no tags emerging carrying the previous transaction's currency, tags and
// notes, with its own date and amount intact so the workbook still reconciled
// against the ledger. That is the worst shape a data bug can take: wrong, and
// self-consistent enough that nobody checks.
//
// Read by ABSOLUTE CELL REFERENCE throughout, never GetRows. GetRows trims
// trailing blank cells off a row, which would hide exactly the assertions these
// tests are making — an empty I3 and an absent I3 are indistinguishable there.

func seedExportLayoutRow(t *testing.T, q *database.Queries, userID, catID int64,
	date string, cents int64, desc string,
	origCents sql.NullInt64, origCur sql.NullString, rate sql.NullFloat64,
	tags, notes sql.NullString) int64 {
	t.Helper()
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		t.Fatalf("parse date %s: %v", date, err)
	}
	txn, err := q.CreateTransaction(context.Background(), database.CreateTransactionParams{
		UserID:              userID,
		Date:                d,
		AmountCents:         cents,
		OriginalAmountCents: origCents,
		OriginalCurrency:    origCur,
		BookedRate:          rate,
		Description:         desc,
		CategoryID:          catID,
		Tags:                tags,
		Notes:               notes,
	})
	if err != nil {
		t.Fatalf("seed %s: %v", desc, err)
	}
	return txn.ID
}

func nullStr(s string) sql.NullString     { return sql.NullString{String: s, Valid: true} }
func nullInt(i int64) sql.NullInt64       { return sql.NullInt64{Int64: i, Valid: true} }
func nullFloat(f float64) sql.NullFloat64 { return sql.NullFloat64{Float64: f, Valid: true} }

// TestExportTxnRows_NullColumnsDoNotLeakAcrossRows seeds one transaction that
// fills every nullable column and an OLDER one that fills none. Because the
// export sorts date DESC, the full row is written first and the bare row
// second, so a slice that is not cleared between iterations shows up as the
// bare row inheriting the full row's values.
//
// Mutation-tested: removing the `for i := range vals { vals[i] = nil }` loop
// makes F3/G3/H3/I3/J3 come back as 7500/LBP/89000/rent,fixed/paid in cash.
func TestExportTxnRows_NullColumnsDoNotLeakAcrossRows(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "leakcheck", "member")
	cat := seedExpenseCategory(t, h.queries, "Groceries")

	seedExportLayoutRow(t, h.queries, user.ID, cat, "2026-03-20", 5000, "FULL ROW",
		nullInt(750000), nullStr("LBP"), nullFloat(89000), nullStr("rent,fixed"), nullStr("paid in cash"))
	seedExportLayoutRow(t, h.queries, user.ID, cat, "2026-03-10", 2500, "BARE ROW",
		sql.NullInt64{}, sql.NullString{}, sql.NullFloat64{}, sql.NullString{}, sql.NullString{})

	rec := httptest.NewRecorder()
	h.handleExportTransactions(rec,
		withUser(httptest.NewRequest(http.MethodGet, "/api/export/transactions", nil), user))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	f, err := excelize.OpenReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("parse xlsx: %v", err)
	}
	defer f.Close()

	get := func(cell string) string {
		v, err := f.GetCellValue("Transactions", cell)
		if err != nil {
			t.Fatalf("read %s: %v", cell, err)
		}
		return v
	}

	catName := "Groceries-" + t.Name()

	// The full row, so a failure below cannot be blamed on the export being
	// broken generally.
	for cell, want := range map[string]string{
		"A2": "2026-03-20", "B2": "FULL ROW", "C2": catName, "D2": "expense",
		"E2": "50", "F2": "7500", "G2": "LBP", "H2": "89000",
		"I2": "rent,fixed", "J2": "paid in cash",
	} {
		if got := get(cell); got != want {
			t.Errorf("full row %s = %q, want %q", cell, got, want)
		}
	}

	// The bare row. The five nullable columns must be EMPTY.
	for cell, want := range map[string]string{
		"A3": "2026-03-10", "B3": "BARE ROW", "C3": catName, "D3": "expense",
		"E3": "25", "F3": "", "G3": "", "H3": "", "I3": "", "J3": "",
	} {
		if got := get(cell); got != want {
			t.Errorf("bare row %s = %q, want %q — a value leaked from the previous "+
				"row's reused slice, so this transaction is reported carrying another "+
				"transaction's data", cell, got, want)
		}
	}
}

// TestExportTxnRows_DataWidthMatchesHeaderWidth pins that every data row spans
// exactly the columns the header names.
//
// The slice is now sized from exportTxnHeaders, so a column added to the header
// alone widens both together — but nothing stops a future edit from hard-coding
// the width again, and the previous version of this code did exactly that. A
// header wider than its data is silent: the file opens, the extra column is
// simply blank on every row, and a reader assumes the ledger had no value there.
func TestExportTxnRows_DataWidthMatchesHeaderWidth(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "widthcheck", "member")
	cat := seedExpenseCategory(t, h.queries, "Groceries")

	seedExportLayoutRow(t, h.queries, user.ID, cat, "2026-03-20", 5000, "WIDE ROW",
		nullInt(750000), nullStr("LBP"), nullFloat(89000), nullStr("rent,fixed"), nullStr("paid in cash"))

	rec := httptest.NewRecorder()
	h.handleExportTransactions(rec,
		withUser(httptest.NewRequest(http.MethodGet, "/api/export/transactions", nil), user))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	f, err := excelize.OpenReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("parse xlsx: %v", err)
	}
	defer f.Close()

	rows, err := f.GetRows("Transactions")
	if err != nil {
		t.Fatalf("read rows: %v", err)
	}
	if len(rows) < 2 {
		t.Fatalf("expected a header and at least one data row, got %d rows", len(rows))
	}

	// A row with every column populated is the only one whose width GetRows
	// reports faithfully, which is why the fixture above fills every one.
	wantWidth := len(exportTxnHeaders("USD"))
	if len(rows[0]) != wantWidth {
		t.Errorf("header row spans %d columns, exportTxnHeaders declares %d",
			len(rows[0]), wantWidth)
	}
	if len(rows[1]) != wantWidth {
		t.Errorf("fully-populated data row spans %d columns but the header spans %d: "+
			"a column was added to one and not the other, so every row is blank in "+
			"that column and reads as 'the ledger had no value'", len(rows[1]), wantWidth)
	}
}

// TestExportTransactions_RateColumn pins the Rate column on BOTH sheets that
// carry transactions — the top-level export and the monthly one: its position
// in the header, the value it carries for a converted row, its EMPTINESS for a
// row that was never converted, and that a tombstoned row's rate never reaches
// the workbook.
//
// Position is asserted, not just presence. The import maps columns by header
// name, so a Rate column that landed in the wrong place would still re-import —
// but every human reading the sheet, and every existing fixture keyed on a cell
// reference, reads it positionally. G2 ("LBP") is the control: if Rate were
// written over the currency instead of after it, the header assertion alone
// would still pass.
//
// The empty cell is read by ABSOLUTE REFERENCE (see the file header): a base
// row has no tags and no notes either, so GetRows trims its tail and cannot
// tell "the rate cell is blank" from "the row is only seven columns wide".
func TestExportTransactions_RateColumn(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "ratecolumn", "member")
	cat := seedExpenseCategory(t, h.queries, "Groceries")

	// The tombstone is the NEWEST row, and the export sorts date DESC — so a
	// dropped soft-delete filter puts the sentinel rate in the FIRST data row,
	// where it cannot hide behind the live ones.
	tombID := seedExportLayoutRow(t, h.queries, user.ID, cat, "2026-03-25", 9900, "TOMBSTONED ROW",
		nullInt(88110000), nullStr("LBP"), nullFloat(999999), sql.NullString{}, sql.NullString{})
	if err := h.queries.SoftDeleteTransaction(context.Background(), tombID); err != nil {
		t.Fatalf("soft-delete the sentinel row: %v", err)
	}
	seedExportLayoutRow(t, h.queries, user.ID, cat, "2026-03-20", 1685, "FOREIGN ROW",
		nullInt(15000000), nullStr("LBP"), nullFloat(89000), sql.NullString{}, sql.NullString{})
	seedExportLayoutRow(t, h.queries, user.ID, cat, "2026-03-10", 2500, "BASE ROW",
		sql.NullInt64{}, sql.NullString{}, sql.NullFloat64{}, sql.NullString{}, sql.NullString{})

	rec := httptest.NewRecorder()
	h.handleExportTransactions(rec,
		withUser(httptest.NewRequest(http.MethodGet, "/api/export/transactions", nil), user))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	f, err := excelize.OpenReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("parse xlsx: %v", err)
	}
	defer f.Close()

	get := func(cell string) string {
		v, err := f.GetCellValue("Transactions", cell)
		if err != nil {
			t.Fatalf("read %s: %v", cell, err)
		}
		return v
	}

	rows, err := f.GetRows("Transactions")
	if err != nil {
		t.Fatalf("read rows: %v", err)
	}
	const rateIdx = 7
	if len(rows) == 0 || len(rows[0]) <= rateIdx {
		t.Fatalf("header row is %v: no column at index %d, so the export cannot carry "+
			"a booked rate at all", rows, rateIdx)
	}
	if rows[0][rateIdx] != "Rate" {
		t.Errorf("header[%d] = %q, want %q — an export whose rate column is missing or "+
			"misplaced cannot round-trip a converted row", rateIdx, rows[0][rateIdx], "Rate")
	}

	// The converted row carries the divisor that produced its stored amount.
	if got := get("G2"); got != "LBP" {
		t.Fatalf("G2 = %q, want %q: the layout moved, so the H-column assertions below "+
			"are not reading the Rate column", got, "LBP")
	}
	if got := get("H2"); got != "89000" {
		t.Errorf("converted row H2 (Rate) = %q, want %q — without it a re-import has to "+
			"guess today's rate for a row booked at yesterday's", got, "89000")
	}

	// The base row was never converted, so it has no rate to state. A zero
	// here would re-import as a rate of 0.
	if got := get("H3"); got != "" {
		t.Errorf("base row H3 (Rate) = %q, want empty — a row with no conversion must "+
			"state no rate", got)
	}
	if got := get("B3"); got != "BASE ROW" {
		t.Fatalf("B3 = %q, want %q: row 3 is not the base row, so the H3 assertion above "+
			"checked the wrong row", got, "BASE ROW")
	}

	// The tombstone: neither its description nor its sentinel rate may appear
	// anywhere in the workbook.
	if len(rows) != 3 {
		t.Fatalf("Transactions rows = %d, want 3 (header + 2 live): %v", len(rows), rows)
	}
	for r, row := range rows {
		for c, cell := range row {
			if cell == "999999" || cell == "TOMBSTONED ROW" {
				t.Errorf("row %d column %d = %q: a soft-deleted row reached the export, "+
					"and it brought its booked rate with it", r+1, c+1, cell)
			}
		}
	}

	// The monthly export writes the SAME Transactions sheet through the same
	// two helpers, but from its own SELECT — and that SELECT is the half a
	// shared helper cannot protect. A monthly query that omits the rate, or
	// lists it in the wrong place, is caught here rather than by whichever
	// unrelated test happens to seed a value that will not scan into a float.
	monthRec := httptest.NewRecorder()
	h.handleExportMonthly(monthRec, withUserAndURLParams(
		httptest.NewRequest(http.MethodGet, "/api/export/monthly/2026/3", nil),
		user, map[string]string{"year": "2026", "month": "3"}))
	if monthRec.Code != http.StatusOK {
		t.Fatalf("monthly export status %d: %s", monthRec.Code, monthRec.Body.String())
	}
	mf, err := excelize.OpenReader(bytes.NewReader(monthRec.Body.Bytes()))
	if err != nil {
		t.Fatalf("parse monthly xlsx: %v", err)
	}
	defer mf.Close()

	monthRows, err := mf.GetRows("Transactions")
	if err != nil {
		t.Fatalf("read monthly Transactions sheet: %v", err)
	}
	if len(monthRows) == 0 || len(monthRows[0]) <= rateIdx {
		t.Fatalf("monthly header row is %v: no column at index %d", monthRows, rateIdx)
	}
	if monthRows[0][rateIdx] != "Rate" {
		t.Errorf("monthly header[%d] = %q, want %q", rateIdx, monthRows[0][rateIdx], "Rate")
	}
	monthCell := func(cell string) string {
		v, err := mf.GetCellValue("Transactions", cell)
		if err != nil {
			t.Fatalf("read monthly %s: %v", cell, err)
		}
		return v
	}
	if got := monthCell("B2"); got != "FOREIGN ROW" {
		t.Fatalf("monthly B2 = %q, want %q: the H2 assertion below would read the wrong row",
			got, "FOREIGN ROW")
	}
	if got := monthCell("H2"); got != "89000" {
		t.Errorf("monthly converted row H2 (Rate) = %q, want %q — the monthly sheet drops "+
			"the booked rate the top-level export carries", got, "89000")
	}
	if got := monthCell("H3"); got != "" {
		t.Errorf("monthly base row H3 (Rate) = %q, want empty", got)
	}
}
