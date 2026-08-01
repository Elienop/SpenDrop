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

// These two tests pin the row-layout invariants of writeExportTxnRows, which
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
	origCents sql.NullInt64, origCur, tags, notes sql.NullString) {
	t.Helper()
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		t.Fatalf("parse date %s: %v", date, err)
	}
	if _, err := q.CreateTransaction(context.Background(), database.CreateTransactionParams{
		UserID:              userID,
		Date:                d,
		AmountCents:         cents,
		OriginalAmountCents: origCents,
		OriginalCurrency:    origCur,
		Description:         desc,
		CategoryID:          catID,
		Tags:                tags,
		Notes:               notes,
	}); err != nil {
		t.Fatalf("seed %s: %v", desc, err)
	}
}

func nullStr(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }
func nullInt(i int64) sql.NullInt64   { return sql.NullInt64{Int64: i, Valid: true} }

// TestExportTxnRows_NullColumnsDoNotLeakAcrossRows seeds one transaction that
// fills every nullable column and an OLDER one that fills none. Because the
// export sorts date DESC, the full row is written first and the bare row
// second, so a slice that is not cleared between iterations shows up as the
// bare row inheriting the full row's values.
//
// Mutation-tested: removing the `for i := range vals { vals[i] = nil }` loop
// makes F3/G3/H3/I3 come back as 7500/LBP/rent,fixed/paid in cash.
func TestExportTxnRows_NullColumnsDoNotLeakAcrossRows(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "leakcheck", "member")
	cat := seedExpenseCategory(t, h.queries, "Groceries")

	seedExportLayoutRow(t, h.queries, user.ID, cat, "2026-03-20", 5000, "FULL ROW",
		nullInt(750000), nullStr("LBP"), nullStr("rent,fixed"), nullStr("paid in cash"))
	seedExportLayoutRow(t, h.queries, user.ID, cat, "2026-03-10", 2500, "BARE ROW",
		sql.NullInt64{}, sql.NullString{}, sql.NullString{}, sql.NullString{})

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
		"E2": "50", "F2": "7500", "G2": "LBP", "H2": "rent,fixed", "I2": "paid in cash",
	} {
		if got := get(cell); got != want {
			t.Errorf("full row %s = %q, want %q", cell, got, want)
		}
	}

	// The bare row. The four nullable columns must be EMPTY.
	for cell, want := range map[string]string{
		"A3": "2026-03-10", "B3": "BARE ROW", "C3": catName, "D3": "expense",
		"E3": "25", "F3": "", "G3": "", "H3": "", "I3": "",
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
		nullInt(750000), nullStr("LBP"), nullStr("rent,fixed"), nullStr("paid in cash"))

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
	// reports faithfully, which is why the fixture above fills all nine.
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
