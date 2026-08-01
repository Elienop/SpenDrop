package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elienop/spendrop/internal/database"
)

// TestImportFieldLengths_MatchTheWritePath is the most important test in this
// file, because the way this gate fails is not by being absent but by being
// subtly different from the check it stands in front of.
//
// If the preview measures one quantity and the write path measures another,
// the gate passes rows the ledger refuses — and the user then meets the failure
// as an unexplained count at confirm time instead of a flag they could act on.
// A gate that disagrees with its write path is worse than no gate.
//
// So this asserts agreement directly, on values straddling every boundary,
// rather than asserting either side in isolation.
func TestImportFieldLengths_MatchTheWritePath(t *testing.T) {
	// Multi-byte on purpose: this household writes Arabic, and len() counts
	// bytes, so these are exactly the values where a rune-based
	// reimplementation would diverge from the write path.
	arabic := "ب"
	if len(arabic) != 2 {
		t.Fatalf("expected a 2-byte rune, got %d bytes", len(arabic))
	}

	cases := []struct {
		name  string
		field string
		value string
	}{
		{"description at the limit", importFieldDescription, strings.Repeat("d", MaxDescriptionLength)},
		{"description one byte over", importFieldDescription, strings.Repeat("d", MaxDescriptionLength+1)},
		{"description multi-byte at the byte limit", importFieldDescription, strings.Repeat(arabic, MaxDescriptionLength/2)},
		{"description multi-byte one rune over", importFieldDescription, strings.Repeat(arabic, MaxDescriptionLength/2+1)},
		{"tags at the limit", importFieldTags, strings.Repeat("t", MaxTagsLength)},
		{"tags one byte over", importFieldTags, strings.Repeat("t", MaxTagsLength+1)},
		{"tags multi-byte one rune over", importFieldTags, strings.Repeat(arabic, MaxTagsLength/2+1)},
		{"notes at the limit", importFieldNotes, strings.Repeat("n", MaxNotesLength)},
		{"notes one byte over", importFieldNotes, strings.Repeat("n", MaxNotesLength+1)},
		{"notes multi-byte one rune over", importFieldNotes, strings.Repeat(arabic, MaxNotesLength/2+1)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := importRow{RowID: 0, Description: "ok", Amount: 1}
			req := transactionRequest{
				Date:        "2026-01-15",
				Description: "ok",
				Amount:      1,
				CategoryID:  1,
			}
			switch tc.field {
			case importFieldDescription:
				row.Description = tc.value
				req.Description = tc.value
			case importFieldTags:
				row.Tags = tc.value
				req.Tags = &tc.value
			case importFieldNotes:
				row.Notes = tc.value
				req.Notes = &tc.value
			}

			previewRejects := len(checkImportRowLengths([]importRow{row})) > 0
			writeRejects := validateTransactionRequest(req, noStoredDate) != nil

			if previewRejects != writeRejects {
				t.Errorf("preview rejects=%v but the write path rejects=%v for %d bytes of %s — the import gate and the ledger disagree",
					previewRejects, writeRejects, len(tc.value), tc.field)
			}
		})
	}
}

// TestCheckImportRowLengths_ExemptsSkippedRows pins that skipping is a remedy.
// If a skipped row kept reporting, the one action the UI offers to clear a
// too-long row would not clear it, and confirm would stay blocked with no way
// forward short of editing a cell the user wanted to drop.
func TestCheckImportRowLengths_ExemptsSkippedRows(t *testing.T) {
	tooLong := strings.Repeat("d", MaxDescriptionLength+1)
	rows := []importRow{
		{RowID: 0, Description: tooLong, Skip: true},
		{RowID: 1, Description: tooLong, Skip: false},
	}
	got := checkImportRowLengths(rows)
	if len(got) != 1 || got[0].RowID != 1 {
		t.Fatalf("field errors = %+v, want exactly row 1 — a skipped row is still being reported", got)
	}
}

// seedFieldLengthUpload uploads a workbook whose row 2 carries an over-long
// value in the named column, returning the decoded preview.
func seedFieldLengthUpload(t *testing.T, h *Handler, user database.User, column, value string) map[string]any {
	t.Helper()
	headers := []string{"Date", "Description", "Amount", "Category", "Tags", "Notes"}
	good := []string{"2026-01-15", "Groceries", "42.50", "Food", "weekly", "a note"}
	bad := []string{"2026-01-16", "Coffee", "5.75", "Food", "daily", "another note"}
	for i, h := range headers {
		if strings.EqualFold(h, column) {
			bad[i] = value
		}
	}
	payload := createTestXLSX(t, "Transactions", headers, [][]string{good, bad})

	req := withUser(postMultipartFile(t, "/api/import/upload", payload), user)
	rec := httptest.NewRecorder()
	h.handleImportUpload(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: status=%d, want 200 — an over-long field must NOT refuse the file; body=%s",
			rec.Code, rec.Body.String())
	}
	var preview map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &preview); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	return preview
}

// TestHandleImportUpload_FlagsOverLongFieldsWithoutRefusingTheFile is the
// shape decision, asserted rather than assumed: refusing the upload would fail
// every good row in the file for the sake of one bad one, and the household's
// only recourse would be to go back to Google Sheets and hunt for it.
func TestHandleImportUpload_FlagsOverLongFieldsWithoutRefusingTheFile(t *testing.T) {
	for _, tc := range []struct {
		column string
		limit  int
		field  string
	}{
		{"Description", MaxDescriptionLength, importFieldDescription},
		{"Tags", MaxTagsLength, importFieldTags},
		{"Notes", MaxNotesLength, importFieldNotes},
	} {
		t.Run(tc.column, func(t *testing.T) {
			clearImportStore()
			q, db := setupTestDB(t)
			h := NewHandler(q, db)
			user := seedTestUser(t, q, "flag-"+tc.field, "admin")

			preview := seedFieldLengthUpload(t, h, user, tc.column, strings.Repeat("x", tc.limit+1))

			// The good row survives: this is the whole point of flagging.
			if n, _ := preview["row_count"].(float64); int(n) != 2 {
				t.Errorf("row_count=%v, want 2 — good rows were dropped along with the bad one", preview["row_count"])
			}

			raw, ok := preview["field_errors"]
			if !ok {
				t.Fatal("no field_errors key in the preview; the frontend has nothing to flag")
			}
			var fieldErrors []importFieldError
			b, _ := json.Marshal(raw)
			if err := json.Unmarshal(b, &fieldErrors); err != nil {
				t.Fatalf("field_errors is not the agreed shape: %v", err)
			}
			if len(fieldErrors) != 1 {
				t.Fatalf("field_errors=%+v, want exactly one", fieldErrors)
			}
			if fieldErrors[0].Field != tc.field {
				t.Errorf("field=%q, want %q", fieldErrors[0].Field, tc.field)
			}
			if fieldErrors[0].RowID != 1 {
				t.Errorf("row_id=%d, want 1 (the second row)", fieldErrors[0].RowID)
			}
		})
	}
}

// TestHandleImportUpload_CleanFileHasNoFieldErrors is the accept side. The key
// assertion is that the KEY IS PRESENT and empty rather than absent: the
// frontend spreads this object into state, so an absent key and an empty list
// are different values there.
func TestHandleImportUpload_CleanFileHasNoFieldErrors(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "clean-file", "admin")

	preview := seedFieldLengthUpload(t, h, user, "Description", "well within the limit")

	raw, ok := preview["field_errors"]
	if !ok {
		t.Fatal("field_errors key missing on a clean file; it must be present and empty")
	}
	list, ok := raw.([]any)
	if !ok {
		t.Fatalf("field_errors = %T, want an array", raw)
	}
	if len(list) != 0 {
		t.Errorf("field_errors=%v, want empty for a file with no over-long values", list)
	}
}

// confirmImport posts a confirm for the session and returns the recorder.
func confirmImport(t *testing.T, h *Handler, q *database.Queries, user database.User, importID string) *httptest.ResponseRecorder {
	t.Helper()
	cats, err := q.ListAllCategories(context.Background())
	if err != nil {
		t.Fatalf("list categories: %v", err)
	}
	if len(cats) == 0 {
		t.Fatal("expected the migrations to seed a category")
	}
	body, err := json.Marshal(map[string]any{
		"import_id":           importID,
		"default_category_id": cats[0].ID,
	})
	if err != nil {
		t.Fatalf("marshal confirm body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/import/confirm", bytes.NewReader(body))
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleImportConfirm(rec, req)
	return rec
}

// TestHandleImportConfirm_BlocksOnOverLongField is the gate. Flagging at upload
// is advisory — a client can ignore the preview entirely, and the rows can be
// edited by PATCH after it — so confirm has to re-check, and this is what makes
// the drop non-silent rather than a row quietly vanishing between preview and
// ledger.
func TestHandleImportConfirm_BlocksOnOverLongField(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "confirm-blocked", "admin")

	preview := seedFieldLengthUpload(t, h, user, "Notes", strings.Repeat("n", MaxNotesLength+1))
	importID, _ := preview["import_id"].(string)

	rec := confirmImport(t, h, q, user, importID)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d, want 409; body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode 409: %v", err)
	}
	if code, _ := body["code"].(string); code != "FIELD_TOO_LONG" {
		t.Errorf("code=%q, want FIELD_TOO_LONG", code)
	}
	if _, ok := body["field_errors"]; !ok {
		t.Error("no field_errors in the 409; the frontend cannot say which row")
	}

	// Nothing may have landed. A gate that blocks the response but writes the
	// rows anyway would be the worst outcome available.
	if n := countTransactionsForUser(t, db, user.ID); n != 0 {
		t.Errorf("%d transactions were inserted despite the 409", n)
	}
}

// TestHandleImportConfirm_SkippingTheRowClearsTheBlock pins the escape route.
// The gate is only defensible because there IS a one-click way past it; if
// skipping did not clear it, a user with one long note could never import the
// other forty-seven rows.
func TestHandleImportConfirm_SkippingTheRowClearsTheBlock(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "confirm-skip", "admin")

	preview := seedFieldLengthUpload(t, h, user, "Notes", strings.Repeat("n", MaxNotesLength+1))
	importID, _ := preview["import_id"].(string)

	if rec := patchImportRow(t, h, user, importID, 1, "skip", true); rec.Code != http.StatusOK {
		t.Fatalf("patch skip: status=%d; body=%s", rec.Code, rec.Body.String())
	}

	rec := confirmImport(t, h, q, user, importID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200 after skipping the offending row; body=%s", rec.Code, rec.Body.String())
	}
	// The good row still imports — skipping one row must not cost the others.
	if n := countTransactionsForUser(t, db, user.ID); n != 1 {
		t.Errorf("%d transactions imported, want 1 (the good row)", n)
	}
}

// TestHandleImportPatchRow_ShorteningTheDescriptionClearsTheBlock verifies the
// other escape route, which scope item 5 says already exists rather than
// needing building: validateImportField already bounds description length, so
// PATCH both refuses an over-long edit and accepts a shortened one.
//
// It also pins the contract point that made field_errors necessary on the PATCH
// response: the frontend spreads this object into state, so if the key were
// absent here the flag would vanish the moment the user edited anything while
// confirm went on refusing.
func TestHandleImportPatchRow_ShorteningTheDescriptionClearsTheBlock(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "patch-clears", "admin")

	preview := seedFieldLengthUpload(t, h, user, "Description", strings.Repeat("d", MaxDescriptionLength+1))
	importID, _ := preview["import_id"].(string)

	// PATCH refuses an edit that is still too long.
	rec := patchImportRow(t, h, user, importID, 1, "description", strings.Repeat("d", MaxDescriptionLength+1))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("patch with an over-long value: status=%d, want 400; body=%s", rec.Code, rec.Body.String())
	}

	// A shortened value is accepted, and the response still carries the key.
	rec = patchImportRow(t, h, user, importID, 1, "description", "Short enough")
	if rec.Code != http.StatusOK {
		t.Fatalf("patch with a valid value: status=%d; body=%s", rec.Code, rec.Body.String())
	}
	var patched map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &patched); err != nil {
		t.Fatalf("decode patch response: %v", err)
	}
	raw, ok := patched["field_errors"]
	if !ok {
		t.Fatal("the PATCH response dropped field_errors; the frontend spreads this object, so the flag would go undefined mid-edit")
	}
	if list, _ := raw.([]any); len(list) != 0 {
		t.Errorf("field_errors=%v, want empty after the value was shortened", list)
	}

	if rec := confirmImport(t, h, q, user, importID); rec.Code != http.StatusOK {
		t.Fatalf("confirm after shortening: status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if n := countTransactionsForUser(t, db, user.ID); n != 2 {
		t.Errorf("%d transactions imported, want 2", n)
	}
}

// TestProcessImportRows_FieldTooLongIsADeclaredSkipReason pins the floor.
//
// This branch should be unreachable through the handler, because confirm
// refuses the batch first — so it is tested by calling the processor directly.
// It exists because this loop is the last thing between a preview row and the
// ledger, and because the conservation property requires every skipped row to
// name a declared reason; without it an over-long row reaching here would be
// counted as "not imported" with nothing recording why.
func TestProcessImportRows_FieldTooLongIsADeclaredSkipReason(t *testing.T) {
	q, db := setupTestDB(t)
	user := seedTestUser(t, q, "floor", "admin")
	cats, err := q.ListAllCategories(context.Background())
	if err != nil || len(cats) == 0 {
		t.Fatalf("list categories: %v", err)
	}
	catIDToName := map[int64]string{cats[0].ID: cats[0].Name}

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback()

	rows := []importRow{
		{RowID: 0, Date: "2026-01-15", Description: "Groceries", Amount: 42.50},
		{RowID: 1, Date: "2026-01-16", Description: strings.Repeat("d", MaxDescriptionLength+1), Amount: 5.75},
	}
	result, _ := processImportRows(context.Background(), q.WithTx(tx), tx,
		database.NewTransactionStore(db, q), importProcessInput{
			UserID:            user.ID,
			Rows:              rows,
			DefaultCategoryID: cats[0].ID,
			CatIDToName:       catIDToName,
		})

	if len(result.Inserted) != 1 {
		t.Errorf("inserted=%d, want 1", len(result.Inserted))
	}
	if len(result.Skipped) != 1 {
		t.Fatalf("skipped=%+v, want exactly one", result.Skipped)
	}
	if result.Skipped[0].Reason != skipReasonFieldTooLong {
		t.Errorf("reason=%q, want %q", result.Skipped[0].Reason, skipReasonFieldTooLong)
	}
}

// TestHandleImportGetSession_ResumeKeepsFieldErrors covers the third preview
// surface: the frontend persists import_id and calls GET on mount to rehydrate
// after a reload.
//
// Without field_errors here, refreshing the page during an import clears every
// flag while confirm goes on refusing — the user is blocked with a clean-looking
// preview and no indication of which row is at fault. Same failure as the PATCH
// case, reached by a different route, and it needs its own test because it is a
// different handler.
func TestHandleImportGetSession_ResumeKeepsFieldErrors(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "resume-flags", "admin")

	preview := seedFieldLengthUpload(t, h, user, "Tags", strings.Repeat("t", MaxTagsLength+1))
	importID, _ := preview["import_id"].(string)

	req := httptest.NewRequest(http.MethodGet, "/api/import/"+importID, nil)
	req = withUserAndURLParams(req, user, map[string]string{"importID": importID})
	rec := httptest.NewRecorder()
	h.handleImportGetSession(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resumed map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resumed); err != nil {
		t.Fatalf("decode resume: %v", err)
	}
	raw, ok := resumed["field_errors"]
	if !ok {
		t.Fatal("the resume response dropped field_errors; a page refresh would clear every flag while confirm still blocks")
	}
	var fieldErrors []importFieldError
	b, _ := json.Marshal(raw)
	if err := json.Unmarshal(b, &fieldErrors); err != nil {
		t.Fatalf("field_errors is not the agreed shape: %v", err)
	}
	if len(fieldErrors) != 1 || fieldErrors[0].Field != importFieldTags || fieldErrors[0].RowID != 1 {
		t.Errorf("field_errors=%+v, want one tags error on row 1", fieldErrors)
	}
}

// TestImportFieldLengthMessage_IsIdenticalAcrossEverySurface is the point of
// putting the message on the server at all.
//
// The condition is reachable four ways — flagged at upload, re-flagged after a
// PATCH, restored by a GET resume, refused by the confirm 409 — and one of them
// (the PATCH 400) already returned a server-authored string before this change.
// If any surface composed its own wording, the same problem would read
// differently depending on how the user arrived, and the two would drift the
// first time either was edited. This asserts all four are byte-identical, so a
// future edit to one of them fails here rather than shipping.
func TestImportFieldLengthMessage_IsIdenticalAcrossEverySurface(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "one-message", "admin")

	want := importFieldLengthMessage(importFieldDescription)
	if want == "" {
		t.Fatal("no message for description")
	}

	// 1. Upload.
	preview := seedFieldLengthUpload(t, h, user, "Description", strings.Repeat("d", MaxDescriptionLength+1))
	importID, _ := preview["import_id"].(string)
	if got := firstFieldErrorMessage(t, preview); got != want {
		t.Errorf("upload message =\n%q\nwant\n%q", got, want)
	}

	// 2. GET resume.
	req := httptest.NewRequest(http.MethodGet, "/api/import/"+importID, nil)
	req = withUserAndURLParams(req, user, map[string]string{"importID": importID})
	rec := httptest.NewRecorder()
	h.handleImportGetSession(rec, req)
	var resumed map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resumed); err != nil {
		t.Fatalf("decode resume: %v", err)
	}
	if got := firstFieldErrorMessage(t, resumed); got != want {
		t.Errorf("resume message =\n%q\nwant\n%q", got, want)
	}

	// 3. Confirm 409.
	confirmRec := confirmImport(t, h, q, user, importID)
	if confirmRec.Code != http.StatusConflict {
		t.Fatalf("confirm status=%d, want 409", confirmRec.Code)
	}
	var conflict map[string]any
	if err := json.Unmarshal(confirmRec.Body.Bytes(), &conflict); err != nil {
		t.Fatalf("decode 409: %v", err)
	}
	if got := firstFieldErrorMessage(t, conflict); got != want {
		t.Errorf("confirm 409 message =\n%q\nwant\n%q", got, want)
	}

	// 4. The PATCH 400, which is where this wording already lived.
	patchRec := patchImportRow(t, h, user, importID, 1, "description",
		strings.Repeat("d", MaxDescriptionLength+1))
	if patchRec.Code != http.StatusBadRequest {
		t.Fatalf("patch status=%d, want 400", patchRec.Code)
	}
	var patchErr patchImportRowErrorBody
	if err := json.Unmarshal(patchRec.Body.Bytes(), &patchErr); err != nil {
		t.Fatalf("decode patch 400: %v", err)
	}
	if patchErr.Message != want {
		t.Errorf("patch 400 message =\n%q\nwant\n%q", patchErr.Message, want)
	}
}

// firstFieldErrorMessage pulls the message off the first field error in a
// decoded response body.
func firstFieldErrorMessage(t *testing.T, body map[string]any) string {
	t.Helper()
	raw, ok := body["field_errors"]
	if !ok {
		t.Fatal("no field_errors in the body")
	}
	var fieldErrors []importFieldError
	b, _ := json.Marshal(raw)
	if err := json.Unmarshal(b, &fieldErrors); err != nil {
		t.Fatalf("field_errors is not the agreed shape: %v", err)
	}
	if len(fieldErrors) == 0 {
		t.Fatal("field_errors is empty")
	}
	return fieldErrors[0].Message
}

// TestImportFieldLengthMessages_NameTheLimitNotTheOverage pins the wording rule
// rather than the exact prose: every message states the limit, and none reports
// how far over the value is. A count would be wrong in any non-ASCII text,
// because these caps are byte comparisons — 500 bytes is about 250 Arabic
// characters — so "you are 43 over" would mislead exactly the household that
// writes Arabic.
func TestImportFieldLengthMessages_NameTheLimitNotTheOverage(t *testing.T) {
	for field, limit := range map[string]int{
		importFieldDescription: MaxDescriptionLength,
		importFieldTags:        MaxTagsLength,
		importFieldNotes:       MaxNotesLength,
	} {
		message := importFieldLengthMessage(field)
		if message == "" {
			t.Errorf("%s: no message", field)
			continue
		}
		if !strings.Contains(message, fmt.Sprint(limit)) {
			t.Errorf("%s message does not name its limit (%d): %q", field, limit, message)
		}
		// Every message must offer a way forward; the whole design rests on
		// the block being clearable.
		if !strings.Contains(strings.ToLower(message), "skip this row") {
			t.Errorf("%s message does not offer the skip remedy: %q", field, message)
		}
	}
}

// TestImportFieldLengths_MeasureTheStoredValueNotTheRawCell pins the OTHER half
// of parity, which the limit comparison alone does not cover.
//
// The two sides agree today for a reason that is easy to lose: the import
// parser trims every cell at parse time, and the write path measures with no
// trimming, so the bytes the gate sees are the bytes confirm inserts. Either
// side changing its trimming breaks that without either limit moving —
// if the parser stopped trimming, a padded value would be flagged that the
// ledger would have accepted; if the gate started trimming a value the parser
// no longer did, it would pass a row the write path refuses.
//
// The payload is a description of exactly the limit wrapped in whitespace: over
// the limit as it appears in the spreadsheet, exactly at it once stored. So the
// file must import cleanly AND the stored value must come back trimmed, and
// only both together pin the agreement.
func TestImportFieldLengths_MeasureTheStoredValueNotTheRawCell(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "trim-parity", "admin")

	exact := strings.Repeat("d", MaxDescriptionLength)
	padded := "   " + exact + "   "
	if len(padded) <= MaxDescriptionLength {
		t.Fatalf("padded value is %d bytes; it must exceed the limit (%d) untrimmed for this to test anything",
			len(padded), MaxDescriptionLength)
	}

	preview := seedFieldLengthUpload(t, h, user, "Description", padded)

	// Not flagged: trimmed, it fits. A parser that stopped trimming would flag
	// it here even though the ledger would have taken it.
	raw, ok := preview["field_errors"]
	if !ok {
		t.Fatal("field_errors key missing")
	}
	if list, _ := raw.([]any); len(list) != 0 {
		t.Errorf("field_errors=%v, want empty — a value that fits once trimmed was flagged", list)
	}

	// And the value the gate measured really is the trimmed one. Without this,
	// a gate that trimmed internally while the parser did not would still look
	// correct above, yet hand the write path a value it refuses.
	rows, _ := preview["rows"].([]any)
	if len(rows) != 2 {
		t.Fatalf("rows=%d, want 2", len(rows))
	}
	row, _ := rows[1].(map[string]any)
	stored, _ := row["description"].(string)
	if stored != exact {
		t.Errorf("stored description is %d bytes and %s the limit; the parser is no longer trimming, so the gate is measuring a different value than the ledger stores",
			len(stored), map[bool]string{true: "exceeds", false: "is within"}[len(stored) > MaxDescriptionLength])
	}

	// The write path agrees on that exact stored value.
	req := transactionRequest{Date: "2026-01-16", Description: stored, Amount: 1, CategoryID: 1}
	if err := validateTransactionRequest(req, noStoredDate); err != nil {
		t.Errorf("the write path refuses the stored value the gate accepted: %v", err)
	}
}
