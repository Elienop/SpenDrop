package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/elienop/spendrop/internal/database"
)

// seedTransactionWithNote creates a transaction carrying a note. Notes have no
// editor in the UI — they arrive via import (import_handlers.go stores the
// spreadsheet's notes column) and leave via export — which is exactly why
// wiping one is invisible to the user.
func seedTransactionWithNote(t *testing.T, h *Handler, userID, categoryID int64, note string) database.Transaction {
	t.Helper()
	date, _ := time.Parse("2006-01-02", "2026-07-01")
	txn, err := h.queries.CreateTransaction(t.Context(), database.CreateTransactionParams{
		UserID:      userID,
		Date:        date,
		AmountCents: 1500,
		Description: "Pharmacy",
		CategoryID:  categoryID,
		Tags:        sql.NullString{String: "health", Valid: true},
		Notes:       sql.NullString{String: note, Valid: true},
	})
	if err != nil {
		t.Fatalf("seed transaction with note: %v", err)
	}
	return txn
}

func putTransaction(t *testing.T, h *Handler, user database.User, id int64, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/transactions/"+strconv.FormatInt(id, 10), bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req = withUserAndURLParam(req, user, "id", strconv.FormatInt(id, 10))
	rec := httptest.NewRecorder()
	h.handleUpdateTransaction(rec, req)
	return rec
}

// TestHandleUpdateTransaction_OmittedNotesArePreserved is the regression test
// for the silent notes wipe.
//
// PUT /api/transactions/{id} is a full replace, and the inline row editor
// builds its payload field-by-field without a notes key because there is no
// notes editor in the UI. transactionRequest.Notes was a plain string, so the
// absent key decoded to "" and the handler wrote NULL — destroying an imported
// note on any edit, with no warning and no UI recovery path.
func TestHandleUpdateTransaction_OmittedNotesArePreserved(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "owner", "member")
	catID := seedExpenseCategory(t, h.queries, "Health")
	txn := seedTransactionWithNote(t, h, user.ID, catID, "reimbursable — submit to HR")

	// A payload shaped exactly like the inline row editor's: no notes key.
	rec := putTransaction(t, h, user, txn.ID, map[string]any{
		"date":        "2026-07-02",
		"amount":      20.0,
		"description": "Pharmacy",
		"category_id": catID,
		"tags":        "health,personal",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	after, err := h.queries.GetTransactionByID(t.Context(), txn.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !after.Notes.Valid || after.Notes.String != "reimbursable — submit to HR" {
		t.Errorf("note was destroyed by an unrelated edit: valid=%v value=%q",
			after.Notes.Valid, after.Notes.String)
	}
	// The edit itself must still have applied.
	if after.Description != "Pharmacy" || !after.Tags.Valid || after.Tags.String != "health,personal" {
		t.Errorf("edit did not apply: description=%q tags=%q", after.Description, after.Tags.String)
	}
}

// TestHandleUpdateTransaction_ExplicitEmptyNotesClears keeps the other half
// honest: a caller that deliberately sends an empty note must still be able to
// clear it, or "preserve on absent" has become "can never clear".
func TestHandleUpdateTransaction_ExplicitEmptyNotesClears(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "owner2", "member")
	catID := seedExpenseCategory(t, h.queries, "Health")
	txn := seedTransactionWithNote(t, h, user.ID, catID, "delete me")

	rec := putTransaction(t, h, user, txn.ID, map[string]any{
		"date":        "2026-07-01",
		"amount":      15.0,
		"description": "Pharmacy",
		"category_id": catID,
		"notes":       "",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	after, err := h.queries.GetTransactionByID(t.Context(), txn.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if after.Notes.Valid && after.Notes.String != "" {
		t.Errorf("explicit empty notes did not clear the note: %q", after.Notes.String)
	}
}

// TestHandleUpdateTransaction_ExplicitNotesOverwrites covers the ordinary case.
func TestHandleUpdateTransaction_ExplicitNotesOverwrites(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "owner3", "member")
	catID := seedExpenseCategory(t, h.queries, "Health")
	txn := seedTransactionWithNote(t, h, user.ID, catID, "old note")

	rec := putTransaction(t, h, user, txn.ID, map[string]any{
		"date":        "2026-07-01",
		"amount":      15.0,
		"description": "Pharmacy",
		"category_id": catID,
		"notes":       "new note",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	after, err := h.queries.GetTransactionByID(t.Context(), txn.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if after.Notes.String != "new note" {
		t.Errorf("notes = %q, want %q", after.Notes.String, "new note")
	}
}

// TestHandleUpdateTransaction_OmittedTagsArePreserved is the same defect that
// was fixed for notes, still live on tags.
//
// The web inline editor always sends a tags key, so this is not reachable from
// the UI — but the fix for notes was justified as covering "every client,
// including the API-token surface", and that surface is exposed. A PUT that
// omits tags decoded "" through a plain string field and silently wiped them.
func TestHandleUpdateTransaction_OmittedTagsArePreserved(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "tagowner", "member")
	catID := seedExpenseCategory(t, h.queries, "Health")
	txn := seedTransactionWithNote(t, h, user.ID, catID, "keep me")

	if !txn.Tags.Valid || txn.Tags.String != "health" {
		t.Fatalf("fixture is wrong: seeded tags = %q (valid=%v)", txn.Tags.String, txn.Tags.Valid)
	}

	// No tags key — the shape an API-token client that has no tag editor sends.
	rec := putTransaction(t, h, user, txn.ID, map[string]any{
		"date":        "2026-07-02",
		"amount":      20.0,
		"description": "Pharmacy",
		"category_id": catID,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	after, err := h.queries.GetTransactionByID(t.Context(), txn.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !after.Tags.Valid || after.Tags.String != "health" {
		t.Errorf("tags were destroyed by an unrelated edit: valid=%v value=%q",
			after.Tags.Valid, after.Tags.String)
	}
	// The edit itself must still have applied.
	if after.Description != "Pharmacy" {
		t.Errorf("edit did not apply: description = %q", after.Description)
	}
}

// TestHandleUpdateTransaction_ExplicitEmptyTagsClears is the over-correction
// guard. Without it, "always keep the stored value" would pass the test above
// while making tags impossible to clear.
func TestHandleUpdateTransaction_ExplicitEmptyTagsClears(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "tagowner2", "member")
	catID := seedExpenseCategory(t, h.queries, "Health")
	txn := seedTransactionWithNote(t, h, user.ID, catID, "keep me")

	rec := putTransaction(t, h, user, txn.ID, map[string]any{
		"date":        "2026-07-01",
		"amount":      15.0,
		"description": "Pharmacy",
		"category_id": catID,
		"tags":        "",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	after, err := h.queries.GetTransactionByID(t.Context(), txn.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if after.Tags.Valid && after.Tags.String != "" {
		t.Errorf("explicit empty tags did not clear them: %q", after.Tags.String)
	}
}

// TestHandleCreateTransaction_OmittedTagsMeansNone pins the create side: on a
// create there is nothing to preserve, so an absent key must still mean "no
// tags" rather than tripping over the new pointer.
func TestHandleCreateTransaction_OmittedTagsMeansNone(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "tagowner3", "member")
	catID := seedExpenseCategory(t, h.queries, "Health")

	body, _ := json.Marshal(map[string]any{
		"date":        "2026-07-01",
		"amount":      15.0,
		"description": "Pharmacy",
		"category_id": catID,
	})
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/transactions", bytes.NewReader(body)), user)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.handleCreateTransaction(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}

	var created struct {
		ID   int64  `json:"id"`
		Tags string `json:"tags"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Tags != "" {
		t.Errorf("tags = %q, want empty", created.Tags)
	}
}
