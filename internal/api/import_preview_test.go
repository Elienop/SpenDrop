package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elienop/spendrop/internal/database"
)

// uploadImportSheet uploads a workbook and returns the decoded preview plus
// the session id. Every test in this file starts here, so the session under
// test is built by the real parser rather than by a hand-written row slice —
// the Rate cell has to survive header discovery and cell parsing to matter.
func uploadImportSheet(t *testing.T, h *Handler, user database.User, xlsxData []byte) (map[string]any, string) {
	t.Helper()
	req := postMultipartFile(t, "/api/import/upload", xlsxData)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleImportUpload(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal upload response: %v", err)
	}
	id, _ := resp["import_id"].(string)
	if id == "" {
		t.Fatalf("upload response carries no import_id: %s", rec.Body.String())
	}
	return resp, id
}

// getImportSession calls the resume endpoint and returns the raw body, so a
// caller can compare it BYTE for byte against another surface's.
func getImportSession(t *testing.T, h *Handler, user database.User, importID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/import/"+importID, nil)
	req = withUserAndURLParam(req, user, "importID", importID)
	rec := httptest.NewRecorder()
	h.handleImportGetSession(rec, req)
	return rec
}

// fieldErrorsByRow indexes a preview's field_errors as row_id -> field ->
// message, which is how every assertion below reads them.
func fieldErrorsByRow(t *testing.T, preview map[string]any) map[int]map[string]string {
	t.Helper()
	out := map[int]map[string]string{}
	raw, ok := preview["field_errors"]
	if !ok {
		t.Fatal("preview carries no field_errors key")
	}
	list, ok := raw.([]any)
	if !ok {
		t.Fatalf("field_errors = %T, want an array", raw)
	}
	for _, item := range list {
		entry, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("field_errors entry = %T, want an object", item)
		}
		rowID := int(entry["row_id"].(float64))
		if out[rowID] == nil {
			out[rowID] = map[string]string{}
		}
		out[rowID][entry["field"].(string)] = entry["message"].(string)
	}
	return out
}

func previewRows(t *testing.T, preview map[string]any) []map[string]any {
	t.Helper()
	raw, ok := preview["rows"].([]any)
	if !ok {
		t.Fatalf("rows = %T, want an array", preview["rows"])
	}
	out := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		row, ok := r.(map[string]any)
		if !ok {
			t.Fatalf("row = %T, want an object", r)
		}
		out = append(out, row)
	}
	return out
}

// moneySheet is the fixture behind most of this file: one row per interesting
// money shape, all of them otherwise valid, so nothing but the money decides
// what happens to them.
func moneySheet(t *testing.T) []byte {
	t.Helper()
	return createTestXLSX(t, "Transactions",
		[]string{"Date", "Description", "Amount", "Category", "Original Amount", "Original Currency", "Rate"},
		[][]string{
			// row 0 — #5: a foreign original with no rate.
			{"2026-01-15", "Souk run", "", "Food", "1500000", "LBP", ""},
			// row 1 — #6: a currency the household has not set up.
			{"2026-01-16", "Duty free", "10.00", "Food", "100", "LBX", ""},
			// row 2 — #4: the sheet's own USD contradicts its rate.
			{"2026-01-17", "Pharmacy", "16.00", "Food", "1500000", "LBP", "89000"},
			// row 3 — #3: the rate is the source of the USD.
			{"2026-01-18", "Bakery", "", "Food", "1500000", "LBP", "89000"},
		})
}

// TestBuildImportPreview_ThreeSurfacesAgree is the anti-drift test for the
// whole preview contract: upload, a no-op PATCH and a GET resume must return
// the SAME JSON for the same session, byte for byte.
//
// Byte equality rather than a field-by-field comparison, because the failure
// this guards against is a field that exists on one surface and not another —
// which is precisely what a comparison written field by field cannot see. The
// history is on record: three handlers hand-built this map, and the PATCH copy
// silently omitted import_id, row_count, columns and unique_categories, so
// every edit after the first went to /import/undefined/rows/N.
//
// The fixture carries money flags on purpose. A preview with nothing to report
// agrees trivially.
func TestBuildImportPreview_ThreeSurfacesAgree(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "surfaces", "admin")

	uploadReq := postMultipartFile(t, "/api/import/upload", moneySheet(t))
	uploadReq = withUser(uploadReq, user)
	uploadRec := httptest.NewRecorder()
	h.handleImportUpload(uploadRec, uploadReq)
	if uploadRec.Code != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d; body: %s", uploadRec.Code, uploadRec.Body.String())
	}
	uploadBody := uploadRec.Body.String()

	var uploaded map[string]any
	if err := json.Unmarshal([]byte(uploadBody), &uploaded); err != nil {
		t.Fatalf("unmarshal upload: %v", err)
	}
	importID := uploaded["import_id"].(string)

	// A PATCH that changes nothing: row 0 is already un-skipped. Its response
	// is the full snapshot, so it must match the upload's.
	patchRec := patchImportRow(t, h, user, importID, 0, "skip", false)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch: expected 200, got %d; body: %s", patchRec.Code, patchRec.Body.String())
	}
	patchBody := patchRec.Body.String()

	getRec := getImportSession(t, h, user, importID)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get: expected 200, got %d; body: %s", getRec.Code, getRec.Body.String())
	}
	getBody := getRec.Body.String()

	if patchBody != uploadBody {
		t.Errorf("PATCH response differs from upload's.\nupload = %s\npatch  = %s", uploadBody, patchBody)
	}
	if getBody != uploadBody {
		t.Errorf("GET response differs from upload's.\nupload = %s\nget    = %s", uploadBody, getBody)
	}

	// A guard on the guard: if the fixture stopped producing flags, the three
	// surfaces above would agree about nothing worth agreeing on.
	if len(fieldErrorsByRow(t, uploaded)) == 0 {
		t.Fatal("the fixture produced no field_errors, so byte equality proves nothing about the money family")
	}
}

// TestHandleImportUpload_MoneyFlags pins the money family on the rail: which
// field each condition lands on, the exact sentence it carries, and the
// derived amount that replaces the sheet's own cell.
//
// The messages are asserted verbatim. The frontend renders them and composes
// nothing, so the string IS the contract.
func TestHandleImportUpload_MoneyFlags(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "moneyflags", "admin")

	preview, _ := uploadImportSheet(t, h, user, moneySheet(t))
	errs := fieldErrorsByRow(t, preview)

	want := []struct {
		rowID   int
		field   string
		message string
	}{
		{0, importFieldRate, "No rate for 1,500,000 LBP — enter the rate this row was booked at, or apply today's 89,000."},
		{1, importFieldOriginalCurrency, "LBX isn't set up — add it under Settings → Currencies."},
		{2, importFieldAmount, "16.00 ≠ 1,500,000 ÷ 89,000 = 16.85. Fix the amount, the original or the rate — SpenDrop stores what the rate produces."},
	}
	for _, w := range want {
		got, ok := errs[w.rowID][w.field]
		if !ok {
			t.Errorf("row %d carries no %s error; got %v", w.rowID, w.field, errs[w.rowID])
			continue
		}
		if got != w.message {
			t.Errorf("row %d %s message =\n  %q\nwant\n  %q", w.rowID, w.field, got, w.message)
		}
	}

	// Row 3 resolves cleanly and must NOT be flagged — without this arm a
	// build that flagged every foreign row would pass every assertion above.
	if flags, flagged := errs[3]; flagged {
		t.Errorf("row 3 resolves through its rate and must carry no flag, got %v", flags)
	}

	rows := previewRows(t, preview)
	if len(rows) != 4 {
		t.Fatalf("preview has %d rows, want 4", len(rows))
	}

	// The wire `amount` is what the row WILL STORE, and amount_derived is how
	// a reader tells a computed amount from a typed one.
	if got := rows[3]["amount"]; got != 16.85 {
		t.Errorf("row 3 amount = %v, want 16.85 — the preview must show the money it is about to write", got)
	}
	if got := rows[3]["amount_derived"]; got != true {
		t.Errorf("row 3 amount_derived = %v, want true", got)
	}
	if got := rows[3]["rate"]; got != 89000.0 {
		t.Errorf("row 3 rate = %v, want 89000", got)
	}
	if got := rows[3]["original_currency"]; got != "LBP" {
		t.Errorf("row 3 original_currency = %v, want LBP", got)
	}

	// Row 2's amount is the sheet's own, untouched: it is blocked precisely
	// because the two disagree, so silently showing the derived value would
	// erase the disagreement the user has to resolve.
	if got := rows[2]["amount"]; got != 16.0 {
		t.Errorf("row 2 amount = %v, want the sheet's own 16", got)
	}
	if _, present := rows[2]["amount_derived"]; present {
		t.Errorf("row 2 carries amount_derived, but its amount was not derived: %v", rows[2])
	}

	// A row with no money story at all keeps the wire it has always had.
	if _, present := rows[1]["amount_derived"]; present {
		t.Errorf("row 1 carries amount_derived on a non-derived row: %v", rows[1])
	}
}

// TestHandleImportUpload_CollisionsUseDerivedCents is the identity test for
// the whole stage: a row hand-typed in the app and the same row arriving in a
// sheet that quotes the SAME rate are one transaction, so the preview must
// predict the duplicate rather than let the user import a second copy.
//
// It can only pass if the collision hash is taken over the DERIVED cents. The
// sheet's Amount cell is empty, so a grouping pass that hashed it would hash
// zero — and drop the row from grouping entirely, reporting no collision at
// all right up until confirm skipped it as a duplicate.
func TestHandleImportUpload_CollisionsUseDerivedCents(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "derivedcollide", "admin")
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

	// The manual row: typed in the app, in LBP, at the household's rate. The
	// API divides by currencies.rate_to_base (89,000) and stores 1685 cents
	// with the hash over that value.
	body, _ := json.Marshal(map[string]any{
		"date":              "2026-02-01",
		"amount":            16.85,
		"original_amount":   1500000,
		"original_currency": "LBP",
		"description":       "Souk run",
		"category_id":       foodID,
	})
	createRec := httptest.NewRecorder()
	createReq := withUser(httptest.NewRequest(http.MethodPost, "/api/transactions", bytes.NewReader(body)), user)
	createReq.Header.Set("Content-Type", "application/json")
	h.handleCreateTransaction(createRec, createReq)
	if createRec.Code != http.StatusCreated && createRec.Code != http.StatusOK {
		t.Fatalf("manual create: status %d: %s", createRec.Code, createRec.Body.String())
	}

	// The sheet: the same money, stated the other way round — no USD cell at
	// all, just the original and the rate that produced it.
	xlsxData := createTestXLSX(t, "Transactions",
		[]string{"Date", "Description", "Amount", "Category", "Original Amount", "Original Currency", "Rate"},
		[][]string{
			{"2026-02-01", "Souk run", "", "Food", "1500000", "LBP", "89000"},
		})

	preview, _ := uploadImportSheet(t, h, user, xlsxData)

	groups, _ := preview["collision_groups"].([]any)
	if len(groups) != 1 {
		t.Fatalf("collision_groups = %v, want exactly one db_match group", preview["collision_groups"])
	}
	group := groups[0].(map[string]any)
	if group["reason"] != "db_match" {
		t.Errorf("group reason = %v, want db_match", group["reason"])
	}
	match, ok := group["db_match"].(map[string]any)
	if !ok {
		t.Fatalf("group carries no db_match payload: %v", group)
	}
	if got := int64(match["amount_cents"].(float64)); got != 1685 {
		t.Errorf("db_match amount_cents = %d, want 1685 — the manual row and the sheet row must resolve to the same cents", got)
	}
}

// TestHandleImportUpload_CollisionsUseDerivedCents_OtherRateIsNotACollision is
// the control for the test above, and the reason the hash formula did not need
// to change. The same original quoted at a DIFFERENT rate is different money
// and therefore a different booking — so it must NOT be predicted as a
// duplicate.
func TestHandleImportUpload_CollisionsUseDerivedCents_OtherRateIsNotACollision(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "otherrate", "admin")
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

	body, _ := json.Marshal(map[string]any{
		"date":              "2026-02-01",
		"amount":            16.85,
		"original_amount":   1500000,
		"original_currency": "LBP",
		"description":       "Souk run",
		"category_id":       foodID,
	})
	createRec := httptest.NewRecorder()
	createReq := withUser(httptest.NewRequest(http.MethodPost, "/api/transactions", bytes.NewReader(body)), user)
	createReq.Header.Set("Content-Type", "application/json")
	h.handleCreateTransaction(createRec, createReq)
	if createRec.Code != http.StatusCreated && createRec.Code != http.StatusOK {
		t.Fatalf("manual create: status %d: %s", createRec.Code, createRec.Body.String())
	}

	// 89,500 instead of 89,000: 1,500,000 LBP is 16.76 at that rate, not
	// 16.85. Same evening, same shop, a different booking.
	xlsxData := createTestXLSX(t, "Transactions",
		[]string{"Date", "Description", "Amount", "Category", "Original Amount", "Original Currency", "Rate"},
		[][]string{
			{"2026-02-01", "Souk run", "", "Food", "1500000", "LBP", "89500"},
		})

	preview, _ := uploadImportSheet(t, h, user, xlsxData)
	if groups, _ := preview["collision_groups"].([]any); len(groups) != 0 {
		t.Errorf("collision_groups = %v, want none — a row booked at another rate is different money", groups)
	}
	rows := previewRows(t, preview)
	if got := rows[0]["amount"]; got != 16.76 {
		t.Errorf("row 0 amount = %v, want 16.76 (1,500,000 ÷ 89,500)", got)
	}
}

// TestHandleImportGetSession_UnknownCurrencyClearsAfterUpsert covers the one
// remedy that lives OUTSIDE the import session. An unknown currency is fixed
// in Settings, and the user must not have to re-upload the file afterwards:
// every surface re-resolves against the currencies table as it stands, so a
// plain resume clears the flag.
func TestHandleImportGetSession_UnknownCurrencyClearsAfterUpsert(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "lbxadder", "admin")
	ctx := context.Background()

	xlsxData := createTestXLSX(t, "Transactions",
		[]string{"Date", "Description", "Amount", "Category", "Original Amount", "Original Currency", "Rate"},
		[][]string{
			{"2026-03-01", "Airport", "", "Food", "1000", "LBX", "50"},
		})

	preview, importID := uploadImportSheet(t, h, user, xlsxData)
	if _, flagged := fieldErrorsByRow(t, preview)[0][importFieldOriginalCurrency]; !flagged {
		t.Fatalf("upload did not flag the unknown currency: %v", preview["field_errors"])
	}

	if err := q.UpsertCurrency(ctx, database.UpsertCurrencyParams{
		Code:       "LBX",
		Name:       "Test Coin",
		Symbol:     "X",
		RateToBase: 50,
		IsBase:     false,
	}); err != nil {
		t.Fatalf("UpsertCurrency: %v", err)
	}

	getRec := getImportSession(t, h, user, importID)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get: expected 200, got %d; body: %s", getRec.Code, getRec.Body.String())
	}
	var resumed map[string]any
	if err := json.Unmarshal(getRec.Body.Bytes(), &resumed); err != nil {
		t.Fatalf("unmarshal resume: %v", err)
	}

	if errs := fieldErrorsByRow(t, resumed); len(errs) != 0 {
		t.Errorf("resume still flags the row after the currency was added: %v", errs)
	}
	rows := previewRows(t, resumed)
	if got := rows[0]["amount"]; got != 20.0 {
		t.Errorf("row 0 amount = %v, want 20 (1,000 ÷ 50) once the currency resolves", got)
	}
	if got := rows[0]["amount_derived"]; got != true {
		t.Errorf("row 0 amount_derived = %v, want true", got)
	}
}

// TestHandleImportUpload_CurrenciesSummary pins the rate the preview OFFERS.
//
// It rides on the preview rather than being read from the currencies endpoint
// because the number the user is offered and the number the import records
// have to be the same one: "apply today's 89,000" turns into a PATCH carrying
// that literal value, which is then stored as the row's booked_rate. Two
// sources would disagree for as long as either cache was staler.
func TestHandleImportUpload_CurrenciesSummary(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "cursummary", "admin")

	xlsxData := createTestXLSX(t, "Transactions",
		[]string{"Date", "Description", "Amount", "Category"},
		[][]string{{"2026-01-15", "Groceries", "42.50", "Food"}})

	preview, _ := uploadImportSheet(t, h, user, xlsxData)

	raw, ok := preview["currencies"]
	if !ok {
		t.Fatal("preview carries no currencies key; the rate offer has no source")
	}
	list, ok := raw.([]any)
	if !ok {
		t.Fatalf("currencies = %T, want an array", raw)
	}
	got := map[string]map[string]any{}
	for _, item := range list {
		entry := item.(map[string]any)
		got[entry["code"].(string)] = entry
	}

	for _, want := range []struct {
		code   string
		rate   float64
		isBase bool
	}{
		{"USD", 1, true},
		{"LBP", 89000, false},
		{"EUR", 0.92, false},
	} {
		entry, ok := got[want.code]
		if !ok {
			t.Errorf("currencies is missing %s: %v", want.code, list)
			continue
		}
		if entry["rate_to_base"] != want.rate {
			t.Errorf("%s rate_to_base = %v, want %v", want.code, entry["rate_to_base"], want.rate)
		}
		if entry["is_base"] != want.isBase {
			t.Errorf("%s is_base = %v, want %v", want.code, entry["is_base"], want.isBase)
		}
	}
}

// TestHandleImportUpload_InfiniteRateCellIsInvalidNotAbsent walks the one
// wrong-looking rate a "positive number" check lets through. A cell reading
// 1e999 parses to +Inf — positive, and useless: an infinite rate rounds every
// amount to zero.
//
// The failure it guards against is not a bad message but a SILENT one: if the
// parser reported such a cell as absent, the row would quietly become a
// rate-less foreign row and the user would be asked to supply a rate they can
// see they already typed.
func TestHandleImportUpload_InfiniteRateCellIsInvalidNotAbsent(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "infrate", "admin")

	xlsxData := createTestXLSX(t, "Transactions",
		[]string{"Date", "Description", "Amount", "Category", "Original Amount", "Original Currency", "Rate"},
		[][]string{
			{"2026-01-15", "Souk run", "", "Food", "1500000", "LBP", "1e999"},
		})

	preview, _ := uploadImportSheet(t, h, user, xlsxData)
	got, flagged := fieldErrorsByRow(t, preview)[0][importFieldRate]
	if !flagged {
		t.Fatalf("an infinite rate cell was not flagged at all: %v", preview["field_errors"])
	}
	want := "That rate is not a positive, finite number. Enter the rate this row was booked at, or clear the cell."
	if got != want {
		t.Errorf("message = %q\nwant    = %q\n(a rate the user typed and got wrong is a different fix from one they never typed)", got, want)
	}
	rows := previewRows(t, preview)
	if r, present := rows[0]["rate"]; present {
		t.Errorf("the preview carries rate = %v; an unusable rate must not reach the wire as a number", r)
	}
}

// TestHandleImportUpload_OutOfRangeOriginalNamesTheOriginalCell walks the row
// the parser mutilates on the way in. 2,000,000,000 LBP is about $22,000 — a
// perfectly ordinary Beirut supermarket run — but it is past
// MaxTransactionAmount as WRITTEN, so parseImportAmount zeroes the cell.
//
// The row then has an original the resolver cannot see. Diagnosed off the
// parsed value alone it reads as "a rate with nothing to convert", which is a
// sentence about a sheet that plainly has both halves; and clearing the rate to
// try to satisfy that message drops the row into a silent zero_amount skip.
// This is the reachability test for the raw cell that tells the two apart.
func TestHandleImportUpload_OutOfRangeOriginalNamesTheOriginalCell(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "bigoriginal", "admin")

	xlsxData := createTestXLSX(t, "Transactions",
		[]string{"Date", "Description", "Amount", "Category", "Original Amount", "Original Currency", "Rate"},
		[][]string{
			{"2026-01-15", "Souk run", "", "Food", "2000000000", "LBP", "89000"},
		})

	preview, importID := uploadImportSheet(t, h, user, xlsxData)
	want := "That original amount is not a figure SpenDrop can store — it has to be at least one cent and no more than 1,000,000,000 in its own currency. Fix the original amount."
	got, flagged := fieldErrorsByRow(t, preview)[0][importFieldAmount]
	if !flagged {
		t.Fatalf("the row was not flagged on the amount: %v", preview["field_errors"])
	}
	if got != want {
		t.Errorf("message = %q\nwant    = %q", got, want)
	}

	// Clearing the rate must not turn the diagnosis into silence.
	rec := patchImportRow(t, h, user, importID, 0, "rate", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("clear rate: %d %s", rec.Code, rec.Body.String())
	}
	var patched map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &patched); err != nil {
		t.Fatalf("unmarshal patch: %v", err)
	}
	if got := fieldErrorsByRow(t, patched)[0][importFieldAmount]; got != want {
		t.Errorf("after clearing the rate, message = %q\nwant %q", got, want)
	}
}

// TestHandleImportUpload_UnusableRateCellTravelsAsRateRaw covers the other
// half of "the server keeps the raw cell": a message that says "clear the
// cell" is unhelpful beside a table showing an empty one. The parsed rate is
// absent (it is unusable), so without this field the preview has nothing to
// display and the user cannot see what they are being asked to fix.
//
// It is emitted ONLY for a cell that is both non-empty and unusable — a usable
// rate travels as the number, and an empty one as nothing at all.
func TestHandleImportUpload_UnusableRateCellTravelsAsRateRaw(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "rateraw", "admin")

	xlsxData := createTestXLSX(t, "Transactions",
		[]string{"Date", "Description", "Amount", "Category", "Original Amount", "Original Currency", "Rate"},
		[][]string{
			{"2026-01-15", "Bad rate", "", "Food", "1500000", "LBP", "eighty nine thousand"},
			{"2026-01-16", "Good rate", "", "Food", "1500000", "LBP", "89000"},
			{"2026-01-17", "No rate", "16.85", "Food", "1500000", "LBP", ""},
		})

	preview, _ := uploadImportSheet(t, h, user, xlsxData)
	rows := previewRows(t, preview)

	if got := rows[0]["rate_raw"]; got != "eighty nine thousand" {
		t.Errorf("row 0 rate_raw = %v, want the cell's own text", got)
	}
	if _, present := rows[0]["rate"]; present {
		t.Errorf("row 0 carries a parsed rate as well: %v", rows[0])
	}
	if _, present := rows[1]["rate_raw"]; present {
		t.Errorf("row 1 (usable rate) carries rate_raw: %v", rows[1])
	}
	if _, present := rows[2]["rate_raw"]; present {
		t.Errorf("row 2 (empty cell) carries rate_raw: %v", rows[2])
	}
}

// TestBuildImportPreview_RowRejectedBeforeMoneyCarriesNoFlag pins the
// exemption that keeps the money gate off rows it cannot help.
//
// A trailing "TOTAL 5,000,000 LBP" line has no date. It is going to be skipped
// as unparseable_date whatever happens to its currency, so flagging its money
// would demand a Skip tick — on every such line — to unblock an import that
// row was never going to join. `unresolvedImportCategories` has always taken
// this position for the category gate; money now matches it.
//
// The exemption is narrow on purpose. A row that is merely too long, or whose
// two money halves disagree, DOES keep its money flag: those are fixable in
// the preview (shorten the description, edit the amount), so the user can act
// on both problems in one pass instead of fixing one and meeting the other.
func TestBuildImportPreview_RowRejectedBeforeMoneyCarriesNoFlag(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "footerrow", "admin")

	xlsxData := createTestXLSX(t, "Transactions",
		[]string{"Date", "Description", "Amount", "Category", "Original Amount", "Original Currency"},
		[][]string{
			// row 0 — a footer line: no date, and a currency nothing resolves.
			{"", "TOTAL", "", "Food", "5000000", "LBX"},
			// row 1 — no description, same unknown currency.
			{"2026-01-16", "", "", "Food", "5000000", "LBX"},
			// row 2 — the positive control: an ordinary row with the same
			// unknown currency, which MUST still be flagged.
			{"2026-01-17", "Duty free", "10.00", "Food", "5000000", "LBX"},
		})

	preview, importID := uploadImportSheet(t, h, user, xlsxData)
	errs := fieldErrorsByRow(t, preview)
	if flags, flagged := errs[0]; flagged {
		t.Errorf("the dateless footer row was flagged on its money: %v", flags)
	}
	if flags, flagged := errs[1]; flagged {
		t.Errorf("the description-less row was flagged on its money: %v", flags)
	}
	if _, flagged := errs[2][importFieldOriginalCurrency]; !flagged {
		t.Fatalf("the ordinary row lost its unknown-currency flag: %v", preview["field_errors"])
	}

	// Confirm must take the same view, or the preview clears a row the gate
	// still refuses — the seam this exemption has to be applied at BOTH ends
	// of. Row 2 is skipped so only the exempt rows are left to judge.
	if rec := patchImportRow(t, h, user, importID, 2, "skip", true); rec.Code != http.StatusOK {
		t.Fatalf("skip row 2: %d %s", rec.Code, rec.Body.String())
	}
	rec := confirmImport(t, h, q, user, importID)
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm: expected 200 (both remaining rows are exempt), got %d; body: %s", rec.Code, rec.Body.String())
	}

	// Fixing the date is what surfaces the flag: the exemption is a statement
	// about the row's CURRENT state, recomputed on every surface, not a
	// permanent pass.
	preview2, importID2 := uploadImportSheet(t, h, user, xlsxData)
	if rec := patchImportRow(t, h, user, importID2, 0, "date", "2026-01-15"); rec.Code != http.StatusOK {
		t.Fatalf("patch date: %d %s", rec.Code, rec.Body.String())
	}
	_ = preview2
	getRec := getImportSession(t, h, user, importID2)
	var resumed map[string]any
	if err := json.Unmarshal(getRec.Body.Bytes(), &resumed); err != nil {
		t.Fatalf("unmarshal resume: %v", err)
	}
	if _, flagged := fieldErrorsByRow(t, resumed)[0][importFieldOriginalCurrency]; !flagged {
		t.Errorf("the footer row kept its exemption after its date was fixed: %v", resumed["field_errors"])
	}
}

// TestHandleImportUpload_OverLongCurrencyIsBoundedLikeEveryOtherCell adds the
// one row value that had no bound. An xlsx cell holds 32,767 characters, and
// until this stage an unknown currency was stored verbatim rather than
// reported — now it is reported, per row, on four surfaces.
//
// The row gets ONE flag, not two: a code that long can never be a currency the
// household owns, so the unknown-currency sentence would be noise stacked on
// the actionable one, and both land on the same cell.
func TestHandleImportUpload_OverLongCurrencyIsBoundedLikeEveryOtherCell(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "longcode", "admin")

	xlsxData := createTestXLSX(t, "Transactions",
		[]string{"Date", "Description", "Amount", "Category", "Original Amount", "Original Currency"},
		[][]string{
			{"2026-01-15", "Long code", "10.00", "Food", "1000", strings.Repeat("L", 400)},
			// The positive control: an ordinary 3-letter code is NOT flagged
			// for length, so the cap is a bound and not a blanket.
			{"2026-01-16", "Short code", "10.00", "Food", "1000", "LBX"},
		})

	preview, importID := uploadImportSheet(t, h, user, xlsxData)
	errs := fieldErrorsByRow(t, preview)

	want := "This row's currency code is longer than the 12 characters a currency code can be. Skip this row, or fix it in your spreadsheet and upload again."
	if got := errs[0][importFieldOriginalCurrency]; got != want {
		t.Errorf("row 0 message = %q\nwant          = %q", got, want)
	}
	if n := countFieldErrorsFor(t, preview, 0); n != 1 {
		t.Errorf("row 0 carries %d flags, want exactly 1 — the length sentence is the actionable one and both land on the same cell", n)
	}
	if got := errs[1][importFieldOriginalCurrency]; got != "LBX isn't set up — add it under Settings → Currencies." {
		t.Errorf("row 1 lost its unknown-currency flag: %q", got)
	}

	// And confirm refuses it as a length error, exactly like a long note.
	rec := confirmImport(t, h, q, user, importID)
	if rec.Code != http.StatusConflict {
		t.Fatalf("confirm: expected 409, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if code := decodedCode(t, rec); code != "FIELD_TOO_LONG" {
		t.Errorf("code = %q, want FIELD_TOO_LONG", code)
	}
}

// countFieldErrorsFor counts every entry in a preview's field_errors for one
// row, which the row->field->message index cannot report (a second entry on
// the same field overwrites the first).
func countFieldErrorsFor(t *testing.T, preview map[string]any, rowID int) int {
	t.Helper()
	list, _ := preview["field_errors"].([]any)
	n := 0
	for _, item := range list {
		if entry, ok := item.(map[string]any); ok && int(entry["row_id"].(float64)) == rowID {
			n++
		}
	}
	return n
}

// TestHandleImportUpload_ForeignOnlySheetIsAccepted is the flagship sheet of
// this whole stage, and until now it was refused at the door.
//
// A back-dated Lebanese bank statement states its money in LBP and quotes the
// rate it was booked at. It has no USD column at all — that is the point, and
// the reason the rate is on the row. Header discovery demanded an `amount`
// column, so the file never reached the resolver that exists to price it.
//
// The required money column is now satisfied by EITHER header. A row that
// carries no money at all is still nothing: it resolves per the matrix and is
// skipped or flagged there, where the reason can be named per row rather than
// refusing the whole file for its shape.
func TestHandleImportUpload_ForeignOnlySheetIsAccepted(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "foreignonly", "admin")

	xlsxData := createTestXLSX(t, "Transactions",
		[]string{"Date", "Description", "Category", "Original Amount", "Original Currency", "Rate"},
		[][]string{
			{"2026-01-15", "Souk run", "Food", "1500000", "LBP", "89000"},
			{"2026-01-16", "Bakery", "Food", "890000", "LBP", "89000"},
		})

	preview, importID := uploadImportSheet(t, h, user, xlsxData)
	if errs := fieldErrorsByRow(t, preview); len(errs) != 0 {
		t.Fatalf("the sheet was flagged: %v", errs)
	}
	rows := previewRows(t, preview)
	if len(rows) != 2 {
		t.Fatalf("preview has %d rows, want 2", len(rows))
	}
	for i, want := range []float64{16.85, 10} {
		if got := rows[i]["amount"]; got != want {
			t.Errorf("row %d amount = %v, want %v (derived from the rate)", i, got, want)
		}
		if got := rows[i]["amount_derived"]; got != true {
			t.Errorf("row %d amount_derived = %v, want true", i, got)
		}
	}

	if rec := confirmImport(t, h, q, user, importID); rec.Code != http.StatusOK {
		t.Fatalf("confirm: expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var cents int64
	var rate sql.NullFloat64
	if err := db.QueryRow(`
		SELECT amount_cents, booked_rate FROM transactions
		WHERE deleted_at IS NULL AND description = 'Souk run'`).Scan(&cents, &rate); err != nil {
		t.Fatalf("read the imported row: %v", err)
	}
	if cents != 1685 {
		t.Errorf("amount_cents = %d, want 1685", cents)
	}
	if !rate.Valid || rate.Float64 != 89000 {
		t.Errorf("booked_rate = %+v, want 89000", rate)
	}
}

// TestHandleImportUpload_SheetWithNoMoneyColumnNamesBothHeaders is the control
// for the test above. Widening the requirement must not mean accepting a file
// with no money in it at all — and the refusal has to name BOTH headers now,
// or a user with a foreign-only sheet reads "missing amount" and goes off to
// add a column they do not need.
func TestHandleImportUpload_SheetWithNoMoneyColumnNamesBothHeaders(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "nomoney", "admin")

	xlsxData := createTestXLSX(t, "Transactions",
		[]string{"Date", "Description", "Category", "Notes"},
		[][]string{{"2026-01-15", "Souk run", "Food", "no money here"}})

	req := postMultipartFile(t, "/api/import/upload", xlsxData)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleImportUpload(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("upload: expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"date", "description", "amount", "original amount"} {
		if !strings.Contains(body, want) {
			t.Errorf("the refusal does not name %q: %s", want, body)
		}
	}
}

// TestBuildImportPreview_FieldErrorOrderWithinARow pins what the builder's
// comment claims: within one row, the length family comes first and the money
// condition after it.
//
// The order is not decoration. The frontend scrolls to the first blocker and
// renders the errors in the order they arrive, so it decides which sentence a
// user meets first on a row that has two problems — and a stable sort by
// row_id alone leaves the within-row order to whatever the builder happens to
// append, which is exactly the kind of thing that changes silently.
func TestBuildImportPreview_FieldErrorOrderWithinARow(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "flagorder", "admin")

	xlsxData := createTestXLSX(t, "Transactions",
		[]string{"Date", "Description", "Amount", "Category", "Original Amount", "Original Currency"},
		[][]string{
			// One row, two problems, on two different cells.
			{"2026-01-15", strings.Repeat("d", MaxDescriptionLength+1), "10.00", "Food", "1500000", "LBX"},
		})

	preview, _ := uploadImportSheet(t, h, user, xlsxData)
	list, _ := preview["field_errors"].([]any)
	if len(list) != 2 {
		t.Fatalf("field_errors = %v, want two entries on the one row", preview["field_errors"])
	}
	got := []string{
		list[0].(map[string]any)["field"].(string),
		list[1].(map[string]any)["field"].(string),
	}
	want := []string{importFieldDescription, importFieldOriginalCurrency}
	if got[0] != want[0] || got[1] != want[1] {
		t.Errorf("field order = %v, want %v (length family first, then the money condition)", got, want)
	}
}

// TestBuildImportPreview_SignMismatchedRowCarriesNoMoneyFlag completes the
// exemption: money flags are for rows that would OTHERWISE import.
//
// A row whose base amount and foreign original point opposite ways is a
// contradiction the importer will not reconcile — it skips at confirm as
// sign_mismatch whatever its currency cell says. Flagging its money would
// demand a fix on a row that is not going to land, which is the same
// objection that exempts a dateless footer line, and it is how the category
// family has always behaved: any pre-money rejection exempts.
//
// Length rows are the deliberate exception on the other side: they BLOCK
// rather than skip, and both problems are fixable in the preview.
func TestBuildImportPreview_SignMismatchedRowCarriesNoMoneyFlag(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "signexempt", "admin")

	xlsxData := createTestXLSX(t, "Transactions",
		[]string{"Date", "Description", "Amount", "Category", "Original Amount", "Original Currency"},
		[][]string{
			// +10.00 against -1,000: the two halves disagree, and the currency
			// is one the household does not have.
			{"2026-01-15", "Contradiction", "10.00", "Food", "-1000", "LBX"},
		})

	preview, importID := uploadImportSheet(t, h, user, xlsxData)
	if flags, flagged := fieldErrorsByRow(t, preview)[0]; flagged {
		t.Errorf("a sign-mismatched row was flagged on its money: %v", flags)
	}

	// The gate has to take the same view, or the preview clears a row confirm
	// still refuses. The row skips at insert as sign_mismatch, so the import
	// completes with nothing stored.
	rec := confirmImport(t, h, q, user, importID)
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm: expected 200 (the row skips as sign_mismatch), got %d; body: %s", rec.Code, rec.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal confirm result: %v", err)
	}
	reasons, _ := result["skipped_reasons"].(map[string]any)
	if got := reasons[string(skipReasonSignMismatch)]; got != 1.0 {
		t.Errorf("skipped_reasons = %v, want one sign_mismatch — the row must skip by name, not be flagged", reasons)
	}
	if n := countTransactionsForUser(t, db, user.ID); n != 0 {
		t.Errorf("%d rows landed from a contradictory pair", n)
	}

	// The exemption is a statement about the row's CURRENT state. Agree the
	// signs and the row becomes one that WOULD import — so its unknown
	// currency is flagged, on the next surface that recomputes.
	preview2, importID2 := uploadImportSheet(t, h, user, xlsxData)
	_ = preview2
	if rec := patchImportRow(t, h, user, importID2, 0, "amount", "-10.00"); rec.Code != http.StatusOK {
		t.Fatalf("patch amount: %d %s", rec.Code, rec.Body.String())
	}
	getRec := getImportSession(t, h, user, importID2)
	var resumed map[string]any
	if err := json.Unmarshal(getRec.Body.Bytes(), &resumed); err != nil {
		t.Fatalf("unmarshal resume: %v", err)
	}
	if _, flagged := fieldErrorsByRow(t, resumed)[0][importFieldOriginalCurrency]; !flagged {
		t.Errorf("the row kept its exemption after its signs were agreed: %v", resumed["field_errors"])
	}
}

// TestBuildImportPreview_SkippedRowCarriesNoMoneyFlag mirrors the length
// family's exemption: skipping IS the remedy the flag offers, so a skipped row
// must not go on blocking the confirm it was skipped to unblock.
func TestBuildImportPreview_SkippedRowCarriesNoMoneyFlag(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "skipmoney", "admin")

	xlsxData := createTestXLSX(t, "Transactions",
		[]string{"Date", "Description", "Amount", "Category", "Original Amount", "Original Currency", "Rate"},
		[][]string{
			{"2026-01-15", "Souk run", "", "Food", "1500000", "LBP", ""},
		})

	preview, importID := uploadImportSheet(t, h, user, xlsxData)
	if _, flagged := fieldErrorsByRow(t, preview)[0][importFieldRate]; !flagged {
		t.Fatalf("upload did not flag the rate-less row: %v", preview["field_errors"])
	}

	rec := patchImportRow(t, h, user, importID, 0, "skip", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch skip: expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var patched map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &patched); err != nil {
		t.Fatalf("unmarshal patch: %v", err)
	}
	if errs := fieldErrorsByRow(t, patched); len(errs) != 0 {
		t.Errorf("field_errors = %v, want none once the row is skipped", errs)
	}
}
