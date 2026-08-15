package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/xuri/excelize/v2"
)

// TestExportThenImport_PreservesBookedRate closes the loop the whole stage was
// built for: a foreign row leaves SpenDrop through the export and comes back
// through the import as the SAME money, at the SAME rate.
//
// It is the one test that exercises both doors against each other. Everything
// else in this package checks one side against a fixture, and a fixture can
// only encode what its author believed the other side does — this walks the
// bytes: the export writes the booked rate into the Rate column, the import's
// header discovery finds it, the resolver divides by it, and the row that
// lands is identical to the one that left.
//
// Two halves, and both matter:
//
//  1. Re-importing the export of a row SpenDrop already has must not double
//     it. The identity survives the round trip even though the money now
//     arrives through the derived path (the sheet carries the USD, the
//     original AND the rate — matrix #4), which it only can if the derived
//     cents equal the stored ones exactly.
//  2. After the original is trashed, the same workbook re-imports — and the
//     row comes back with its booked rate, not with today's. That is the
//     property no export without a Rate column can have: without it, a
//     back-dated row re-imports valued at whatever the sheet's own USD said,
//     with nothing recording the rate behind it.
func TestExportThenImport_PreservesBookedRate(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "roundtripper", "admin")
	ctx := context.Background()

	cats, err := q.ListAllCategories(ctx)
	if err != nil {
		t.Fatalf("ListAllCategories: %v", err)
	}
	var foodID int64
	for _, c := range cats {
		if c.Name == "Food" {
			foodID = c.ID
		}
	}
	if foodID == 0 {
		t.Fatal("expected a seeded Food category")
	}

	// The row leaves through the front door: created over the API in LBP, so
	// its cents and its booked rate are produced by the write path (the
	// household's LBP rate is 89,000, seeded by migration 001).
	createBody, _ := json.Marshal(map[string]any{
		"date":              "2026-04-10",
		"amount":            0,
		"original_amount":   1500000,
		"original_currency": "LBP",
		"description":       "Souk run",
		"category_id":       foodID,
	})
	createRec := httptest.NewRecorder()
	createReq := withUser(httptest.NewRequest(http.MethodPost, "/api/transactions", bytes.NewReader(createBody)), user)
	createReq.Header.Set("Content-Type", "application/json")
	h.handleCreateTransaction(createRec, createReq)
	if createRec.Code != http.StatusCreated && createRec.Code != http.StatusOK {
		t.Fatalf("create: status %d: %s", createRec.Code, createRec.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created row: %v", err)
	}

	original := readRoundTripRow(t, db, created.ID)
	if original.AmountCents != 1685 || !original.BookedRate.Valid || original.BookedRate.Float64 != 89000 {
		t.Fatalf("the seeded row is not the one this test is about: %+v", original)
	}

	// Export it.
	exportRec := httptest.NewRecorder()
	h.handleExportTransactions(exportRec,
		withUser(httptest.NewRequest(http.MethodGet, "/api/export/transactions", nil), user))
	if exportRec.Code != http.StatusOK {
		t.Fatalf("export: status %d: %s", exportRec.Code, exportRec.Body.String())
	}
	workbook := exportRec.Body.Bytes()

	// Sanity on the fixture itself: if the Rate cell were empty, the import
	// below would resolve the row from its USD alone and every assertion
	// about the booked rate would be measuring the wrong thing.
	assertExportedRate(t, workbook, "89000")

	// Half one: the export re-imports as the SAME transaction.
	preview, importID := uploadImportSheet(t, h, user, workbook)
	rows := previewRows(t, preview)
	if len(rows) != 1 {
		t.Fatalf("preview has %d rows, want the one exported row", len(rows))
	}
	if got := rows[0]["rate"]; got != 89000.0 {
		t.Errorf("preview rate = %v, want 89000 — the Rate column did not survive the round trip", got)
	}
	if got := rows[0]["amount"]; got != 16.85 {
		t.Errorf("preview amount = %v, want 16.85", got)
	}
	if got := rows[0]["amount_derived"]; got != true {
		t.Errorf("amount_derived = %v, want true — the sheet quotes a rate, so the USD is computed from it", got)
	}
	if errs := fieldErrorsByRow(t, preview); len(errs) != 0 {
		t.Fatalf("the export flags itself on re-import: %v", errs)
	}
	groups, _ := preview["collision_groups"].([]any)
	if len(groups) != 1 {
		t.Fatalf("collision_groups = %v, want one db_match — the round trip must land on the same identity",
			preview["collision_groups"])
	}
	if reason := groups[0].(map[string]any)["reason"]; reason != "db_match" {
		t.Errorf("group reason = %v, want db_match", reason)
	}

	// So confirm refuses it. The insert loop's `duplicate` label is not what
	// the user sees here — the preview predicted the match, so the batch is
	// stopped at the collision gate, which is the confirm gate's long-standing
	// shape and not something this stage chose.
	confirmRec := confirmImport(t, h, q, user, importID)
	if confirmRec.Code != http.StatusConflict {
		t.Fatalf("confirm: expected 409, got %d; body: %s", confirmRec.Code, confirmRec.Body.String())
	}
	if n := countTransactionsForUser(t, db, user.ID); n != 1 {
		t.Fatalf("live rows = %d, want 1 — a re-imported export must not double the ledger", n)
	}

	// Half two: trash the original, and the same workbook brings it back.
	if err := q.SoftDeleteTransaction(ctx, created.ID); err != nil {
		t.Fatalf("soft-delete the original: %v", err)
	}

	preview2, importID2 := uploadImportSheet(t, h, user, workbook)
	if groups, _ := preview2["collision_groups"].([]any); len(groups) != 0 {
		t.Fatalf("collision_groups = %v, want none once the original is tombstoned", groups)
	}
	confirmRec2 := confirmImport(t, h, q, user, importID2)
	if confirmRec2.Code != http.StatusOK {
		t.Fatalf("re-import: expected 200, got %d; body: %s", confirmRec2.Code, confirmRec2.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(confirmRec2.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode confirm result: %v", err)
	}
	if got := int(result["imported"].(float64)); got != 1 {
		t.Fatalf("imported = %d, want 1; %v", got, result)
	}

	// And what came back is what left.
	var reimportedID int64
	if err := db.QueryRow(
		`SELECT id FROM transactions WHERE deleted_at IS NULL`,
	).Scan(&reimportedID); err != nil {
		t.Fatalf("find the re-imported row: %v", err)
	}
	got := readRoundTripRow(t, db, reimportedID)

	if got.AmountCents != original.AmountCents {
		t.Errorf("amount_cents = %d, want %d — the round trip changed what the row is worth",
			got.AmountCents, original.AmountCents)
	}
	if got.OriginalCents != original.OriginalCents {
		t.Errorf("original_amount_cents = %+v, want %+v", got.OriginalCents, original.OriginalCents)
	}
	if !got.OriginalCurrency.Valid || got.OriginalCurrency.String != "LBP" {
		t.Errorf("original_currency = %+v, want LBP", got.OriginalCurrency)
	}
	if !got.BookedRate.Valid || got.BookedRate.Float64 != 89000 {
		t.Errorf("booked_rate = %+v, want 89000 — without it the re-imported row has lost the rate it was booked at",
			got.BookedRate)
	}
}

// readRoundTripRow reads the four money columns of one transaction by id.
func readRoundTripRow(t *testing.T, db *sql.DB, id int64) storedRow {
	t.Helper()
	var got storedRow
	if err := db.QueryRow(`
		SELECT amount_cents, original_amount_cents, original_currency, booked_rate
		FROM transactions WHERE id = ?`, id,
	).Scan(&got.AmountCents, &got.OriginalCents, &got.OriginalCurrency, &got.BookedRate); err != nil {
		t.Fatalf("read transaction %d: %v", id, err)
	}
	return got
}

// assertExportedRate reads the Rate cell of the first data row by ABSOLUTE
// reference. GetRows trims a row's empty tail, so an absent rate and a short
// row are indistinguishable through it — see the export layout tests.
func assertExportedRate(t *testing.T, workbook []byte, want string) {
	t.Helper()
	f, err := excelize.OpenReader(bytes.NewReader(workbook))
	if err != nil {
		t.Fatalf("parse the exported workbook: %v", err)
	}
	defer f.Close()

	header, err := f.GetRows("Transactions")
	if err != nil {
		t.Fatalf("read the exported rows: %v", err)
	}
	const rateIdx = 7
	if len(header) == 0 || len(header[0]) <= rateIdx || header[0][rateIdx] != "Rate" {
		t.Fatalf("the export has no Rate column at index %d: %v", rateIdx, header)
	}
	got, err := f.GetCellValue("Transactions", "H2")
	if err != nil {
		t.Fatalf("read H2: %v", err)
	}
	if got != want {
		t.Fatalf("exported Rate cell = %q, want %q", got, want)
	}
}
