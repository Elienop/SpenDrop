package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elienop/spendrop/internal/database"
)

// categoryIDByName resolves a migration-seeded category by its canonical name.
func categoryIDByName(t *testing.T, h *Handler, name string) int64 {
	t.Helper()
	cats, err := h.queries.ListAllCategories(context.Background())
	if err != nil {
		t.Fatalf("list categories: %v", err)
	}
	for _, c := range cats {
		if c.Name == name {
			return c.ID
		}
	}
	t.Fatalf("category %q not found", name)
	return 0
}

// TestUpdateTransaction_ClearsContentHash_SoAGenuineImportStillLands is the
// regression test for a stale dedupe identity swallowing real data.
//
// Since 13f0deb every hand-typed row carries a content_hash, and those are
// exactly the rows users edit. UpdateTransaction used to leave content_hash
// untouched, so an edited row kept the identity of the content it USED to
// hold: type "Coffee", rename it to "Starbucks latte", then import a
// genuinely new "Coffee" for the same date and the import is rejected as a
// duplicate of a row that no longer says "Coffee". The new row never lands
// and the user is told it was already there.
//
// The fix clears content_hash on any update that touches a hash input; the
// row leaves dedupe rather than lying, and the startup backfill re-anchors it.
func TestUpdateTransaction_ClearsContentHash_SoAGenuineImportStillLands(t *testing.T) {
	clearImportStore()
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "editor", "member")
	food := categoryIDByName(t, h, "Food")

	createBody, _ := json.Marshal(map[string]any{
		"date":        "2026-01-15",
		"amount":      42.50,
		"description": "Coffee",
		"category_id": food,
	})
	createReq := withUser(
		httptest.NewRequest(http.MethodPost, "/api/transactions", bytes.NewReader(createBody)), user)
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	h.handleCreateTransaction(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d; body = %s", createRec.Code, createRec.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if !hashOf(t, h, created.ID).Valid {
		t.Fatal("precondition failed: a manual create should anchor a content_hash")
	}

	updRec := putTransaction(t, h, user, created.ID, map[string]any{
		"date":        "2026-01-15",
		"amount":      42.50,
		"description": "Starbucks latte",
		"category_id": food,
	})
	if updRec.Code != http.StatusOK {
		t.Fatalf("update: status = %d; body = %s", updRec.Code, updRec.Body.String())
	}

	if hash := hashOf(t, h, created.ID); hash.Valid {
		t.Errorf("the edited row kept content_hash %q — it now claims the identity of content it no longer holds",
			hash.String)
	}

	// A genuinely new "Coffee" on that date must import cleanly. With the
	// stale hash still attached to the renamed row, the confirm is rejected
	// with 409 UNRESOLVED_COLLISIONS and the row is silently never created.
	xlsxData := createTestXLSX(t, "Transactions", []string{
		"Date", "Description", "Amount", "Category",
	}, [][]string{
		{"2026-01-15", "Coffee", "42.50", "Food"},
	})
	uploadReq := withUser(postMultipartFile(t, "/api/import/upload", xlsxData), user)
	uploadRec := httptest.NewRecorder()
	h.handleImportUpload(uploadRec, uploadReq)
	if uploadRec.Code != http.StatusOK {
		t.Fatalf("upload: status = %d; body = %s", uploadRec.Code, uploadRec.Body.String())
	}
	var uploadResp map[string]any
	decodeResponse(t, uploadRec, &uploadResp)

	confirmBody, _ := json.Marshal(map[string]any{
		"import_id":           uploadResp["import_id"].(string),
		"default_category_id": food,
		"category_map":        map[string]float64{"Food": float64(food)},
	})
	confirmReq := withUser(
		httptest.NewRequest(http.MethodPost, "/api/import/confirm", bytes.NewReader(confirmBody)), user)
	confirmRec := httptest.NewRecorder()
	h.handleImportConfirm(confirmRec, confirmReq)
	if confirmRec.Code != http.StatusOK {
		t.Fatalf("a genuinely new row was rejected against a stale hash: confirm status = %d; body = %s",
			confirmRec.Code, confirmRec.Body.String())
	}
	var confirmResp map[string]any
	decodeResponse(t, confirmRec, &confirmResp)
	if n := int(confirmResp["imported"].(float64)); n != 1 {
		t.Errorf("imported = %d, want 1; body = %v", n, confirmResp)
	}

	if got := countTransactionsForUser(t, h.db, user.ID); got != 2 {
		t.Errorf("live rows = %d, want 2 (the renamed row plus the imported one)", got)
	}
}

// TestUpdateTransaction_ClearedHashIsReanchoredByTheBackfill pins the other
// half of the "leave dedupe rather than lie" policy: an edited row with a
// NULL hash is not orphaned forever — the Phase 3.4 startup backfill gives it
// the identity of its CURRENT content on the next boot, so a later import of
// the edited content still dedupes correctly.
func TestUpdateTransaction_ClearedHashIsReanchoredByTheBackfill(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "editor", "member")
	food := categoryIDByName(t, h, "Food")

	createBody, _ := json.Marshal(map[string]any{
		"date":        "2026-01-15",
		"amount":      42.50,
		"description": "Coffee",
		"category_id": food,
	})
	createReq := withUser(
		httptest.NewRequest(http.MethodPost, "/api/transactions", bytes.NewReader(createBody)), user)
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	h.handleCreateTransaction(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d; body = %s", createRec.Code, createRec.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}

	if rec := putTransaction(t, h, user, created.ID, map[string]any{
		"date":        "2026-01-15",
		"amount":      42.50,
		"description": "Starbucks latte",
		"category_id": food,
	}); rec.Code != http.StatusOK {
		t.Fatalf("update: status = %d; body = %s", rec.Code, rec.Body.String())
	}

	if err := database.BackfillContentHashes(t.Context(), h.db); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	hash := hashOf(t, h, created.ID)
	if !hash.Valid {
		t.Fatal("the backfill left the edited row without a content_hash")
	}
	want := database.ComputeContentHash(
		mustParseDate(t, "2026-01-15"), 4250, "Starbucks latte", "Food")
	if hash.String != want {
		t.Errorf("content_hash = %s, want %s (the identity of the CURRENT content)", hash.String, want)
	}
}

// seedHashedTransaction seeds a row and anchors a content_hash on it, standing
// in for an imported row (or, since 13f0deb, any hand-typed one).
func seedHashedTransaction(t *testing.T, h *Handler, userID, categoryID int64, date, desc string, amount float64) int64 {
	t.Helper()
	txn := seedTestTransaction(t, h.queries, userID, categoryID, date, amount, desc)
	cat, err := h.queries.GetCategoryByID(t.Context(), categoryID)
	if err != nil {
		t.Fatalf("load category: %v", err)
	}
	hash := database.ComputeContentHash(txn.Date, txn.AmountCents, desc, cat.Name)
	if err := h.queries.UpdateTransactionContentHash(t.Context(), database.UpdateTransactionContentHashParams{
		ContentHash: sql.NullString{String: hash, Valid: true},
		ID:          txn.ID,
	}); err != nil {
		t.Fatalf("anchor content_hash: %v", err)
	}
	return txn.ID
}

// TestBulkRename_ClearsContentHash covers the raw-SQL bulk rename, which does
// not go through UpdateTransaction. It rewrites description — a hash input —
// on every matching row, so every one of them would otherwise be left holding
// a stale identity.
func TestBulkRename_ClearsContentHash(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "alice", "member")
	cat := seedTestCategory(t, h.queries, "Cat-"+t.Name(), "expense")
	id := seedHashedTransaction(t, h, user.ID, cat.ID, "2026-04-01", "mr brown coffee", 10.0)

	req := withUser(httptest.NewRequest(http.MethodPut, "/api/transactions/bulk-rename",
		bytes.NewReader([]byte(`{"search":"mr brown","new_description":"MR BROWN"}`))), user)
	rec := httptest.NewRecorder()
	h.handleBulkRename(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("bulk rename: status = %d; body = %s", rec.Code, rec.Body.String())
	}

	if hash := hashOf(t, h, id); hash.Valid {
		t.Errorf("renamed row kept content_hash %q — it now claims the identity of its old description", hash.String)
	}
}

// TestUpdateByFilter_NoTags_ClearsContentHash covers runUpdateByFilterNoTags,
// the single-statement branch of update-by-filter. category_id is a hash input
// (the identity hashes the category NAME), so moving a row between categories
// invalidates its stored identity.
func TestUpdateByFilter_NoTags_ClearsContentHash(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "alice", "member")
	from := seedTestCategory(t, h.queries, "From-"+t.Name(), "expense")
	to := seedTestCategory(t, h.queries, "To-"+t.Name(), "expense")
	id := seedHashedTransaction(t, h, user.ID, from.ID, "2026-04-01", "Coffee", 10.0)

	body := fmt.Sprintf(`{"patch":{"category_id":%d}}`, to.ID)
	url := fmt.Sprintf("/api/transactions/update-by-filter?category_id=%d", from.ID)
	req := withUser(httptest.NewRequest(http.MethodPost, url, bytes.NewReader([]byte(body))), user)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.handleUpdateTransactionsByFilter(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update-by-filter: status = %d; body = %s", rec.Code, rec.Body.String())
	}

	if hash := hashOf(t, h, id); hash.Valid {
		t.Errorf("recategorised row kept content_hash %q — it now claims the identity it had under the old category",
			hash.String)
	}
}

// TestUpdateByFilter_Tags_ClearsContentHashOnlyForHashInputs covers
// runUpdateByFilterTags, the read-then-write branch. Tags are NOT a hash
// input, so a tags-only edit must LEAVE the identity intact — clearing it
// unconditionally would drop rows out of dedupe for no reason and let a
// re-import double them. Combining tags with a description change must still
// clear.
func TestUpdateByFilter_Tags_ClearsContentHashOnlyForHashInputs(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "alice", "member")
	cat := seedTestCategory(t, h.queries, "Cat-"+t.Name(), "expense")
	id := seedHashedTransaction(t, h, user.ID, cat.ID, "2026-04-01", "Coffee", 10.0)

	url := fmt.Sprintf("/api/transactions/update-by-filter?category_id=%d", cat.ID)

	tagsOnly := withUser(httptest.NewRequest(http.MethodPost, url,
		bytes.NewReader([]byte(`{"patch":{"tags":"health"},"tagsMode":"replace"}`))), user)
	tagsOnly.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.handleUpdateTransactionsByFilter(rec, tagsOnly)
	if rec.Code != http.StatusOK {
		t.Fatalf("tags-only update: status = %d; body = %s", rec.Code, rec.Body.String())
	}
	if hash := hashOf(t, h, id); !hash.Valid {
		t.Error("a tags-only edit dropped the row out of dedupe; tags are not part of the identity")
	}

	withDesc := withUser(httptest.NewRequest(http.MethodPost, url,
		bytes.NewReader([]byte(`{"patch":{"tags":"food","description":"Flat white"},"tagsMode":"add"}`))), user)
	withDesc.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	h.handleUpdateTransactionsByFilter(rec2, withDesc)
	if rec2.Code != http.StatusOK {
		t.Fatalf("tags+description update: status = %d; body = %s", rec2.Code, rec2.Body.String())
	}
	if hash := hashOf(t, h, id); hash.Valid {
		t.Errorf("a description change alongside tags kept content_hash %q", hash.String)
	}
}
