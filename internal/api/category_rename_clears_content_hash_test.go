package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/elienop/spendrop/internal/database"
)

// putCategory drives handleUpdateCategory the way the settings UI does: a full
// replace of {name, icon} against PUT /api/categories/{id}.
func putCategory(t *testing.T, h *Handler, admin database.User, id int64, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal category body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/categories/x", bytes.NewReader(raw))
	req = withUserAndURLParam(req, admin, "id", strconv.FormatInt(id, 10))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.handleUpdateCategory(rec, req)
	return rec
}

// categoryNameOf reads the stored name back so a "hash preserved" assertion
// cannot pass vacuously against a rename that never landed.
func categoryNameOf(t *testing.T, h *Handler, id int64) string {
	t.Helper()
	cat, err := h.queries.GetCategoryByID(context.Background(), id)
	if err != nil {
		t.Fatalf("load category %d: %v", id, err)
	}
	return cat.Name
}

// categoryIconOf mirrors categoryNameOf for the non-name-field negative test.
func categoryIconOf(t *testing.T, h *Handler, id int64) sql.NullString {
	t.Helper()
	cat, err := h.queries.GetCategoryByID(context.Background(), id)
	if err != nil {
		t.Fatalf("load category %d: %v", id, err)
	}
	return cat.Icon
}

// TestUpdateCategory_Rename_ClearsContentHashesInThatCategoryOnly is the
// regression test for a category rename stranding every transaction in it with
// a permanently stale dedupe identity.
//
// database.ComputeContentHash mixes in the category NAME. handleUpdateCategory
// rewrote that name and touched no transaction row, so every row in the
// renamed category kept claiming the identity of content under the OLD name.
// BackfillContentHashes could not heal it either — it only processes rows
// WHERE content_hash IS NULL — so the staleness was permanent.
//
// The fix clears content_hash for the live rows in the renamed category, in
// the same SQL transaction as the rename. Rows in other categories are
// untouched: their identity did not move.
func TestUpdateCategory_Rename_ClearsContentHashesInThatCategoryOnly(t *testing.T) {
	h := setupHandler(t)
	admin := seedTestUser(t, h.queries, "admin", RoleAdmin)
	renamed := seedTestCategory(t, h.queries, "Renamed-"+t.Name(), "expense")
	untouched := seedTestCategory(t, h.queries, "Untouched-"+t.Name(), "expense")

	inRenamed := seedHashedTransaction(t, h, admin.ID, renamed.ID, "2026-04-01", "Coffee", 10.0)
	alsoInRenamed := seedHashedTransaction(t, h, admin.ID, renamed.ID, "2026-04-02", "Bagel", 4.0)
	inOther := seedHashedTransaction(t, h, admin.ID, untouched.ID, "2026-04-01", "Coffee", 10.0)

	for _, id := range []int64{inRenamed, alsoInRenamed, inOther} {
		if !hashOf(t, h, id).Valid {
			t.Fatalf("precondition failed: row %d must anchor a content_hash", id)
		}
	}
	otherBefore := hashOf(t, h, inOther)

	rec := putCategory(t, h, admin, renamed.ID, map[string]any{"name": "Groceries-" + t.Name()})
	if rec.Code != http.StatusOK {
		t.Fatalf("rename: status = %d; body = %s", rec.Code, rec.Body.String())
	}
	if got := categoryNameOf(t, h, renamed.ID); got != "Groceries-"+t.Name() {
		t.Fatalf("category name = %q — the rename never landed, so the hash assertions prove nothing", got)
	}

	for _, id := range []int64{inRenamed, alsoInRenamed} {
		if hash := hashOf(t, h, id); hash.Valid {
			t.Errorf("row %d kept content_hash %q — it still claims an identity computed from the OLD category name",
				id, hash.String)
		}
	}
	if after := hashOf(t, h, inOther); !after.Valid || after.String != otherBefore.String {
		t.Errorf("row in an unrelated category had its content_hash changed: before %v, after %v",
			otherBefore, after)
	}
}

// TestUpdateCategory_Rename_ThenReimport_DetectsDuplicates is the end-to-end
// harm case: with a stale hash the second import of the very same spreadsheet
// hashes under the NEW category name, matches nothing, and silently doubles
// every row in the renamed category.
//
// After the fix the renamed category's rows leave dedupe, the startup backfill
// re-anchors them under the new name, and the re-import is recognised as a
// duplicate (409 UNRESOLVED_COLLISIONS with a db_match group, zero inserts) —
// the same shape TestHandleImport_DoubleImport_SkipsDuplicates pins for the
// un-renamed case.
func TestUpdateCategory_Rename_ThenReimport_DetectsDuplicates(t *testing.T) {
	clearImportStore()
	h := setupHandler(t)
	admin := seedTestUser(t, h.queries, "admin", RoleAdmin)
	foodID := categoryIDByName(t, h, "Food")

	sheet := createTestXLSX(t, "Transactions", []string{
		"Date", "Description", "Amount", "Category",
	}, [][]string{
		{"2026-01-15", "Coffee", "42.50", "Food"},
	})

	first := uploadAndConfirmImport(t, h, admin, sheet)
	if n := int(first["imported"].(float64)); n != 1 {
		t.Fatalf("first import: imported = %d, want 1; body = %v", n, first)
	}

	newName := "Groceries-" + t.Name()
	if rec := putCategory(t, h, admin, foodID, map[string]any{"name": newName}); rec.Code != http.StatusOK {
		t.Fatalf("rename: status = %d; body = %s", rec.Code, rec.Body.String())
	}

	// The next boot re-anchors the cleared rows under the new name.
	if err := database.BackfillContentHashes(t.Context(), h.db); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	// Re-import the ORIGINAL spreadsheet. Its Category cell still says "Food",
	// so the user maps that label onto the (now renamed) category.
	uploadReq := withUser(postMultipartFile(t, "/api/import/upload", sheet), admin)
	uploadRec := httptest.NewRecorder()
	h.handleImportUpload(uploadRec, uploadReq)
	if uploadRec.Code != http.StatusOK {
		t.Fatalf("second upload: status = %d; body = %s", uploadRec.Code, uploadRec.Body.String())
	}
	var uploadResp map[string]any
	decodeResponse(t, uploadRec, &uploadResp)

	confirmBody, _ := json.Marshal(map[string]any{
		"import_id":           uploadResp["import_id"].(string),
		"default_category_id": foodID,
		"category_map":        map[string]float64{"Food": float64(foodID)},
	})
	confirmReq := withUser(
		httptest.NewRequest(http.MethodPost, "/api/import/confirm", bytes.NewReader(confirmBody)), admin)
	confirmRec := httptest.NewRecorder()
	h.handleImportConfirm(confirmRec, confirmReq)

	if confirmRec.Code != http.StatusConflict {
		t.Errorf("re-import of the original sheet: status = %d, want 409 UNRESOLVED_COLLISIONS; body = %s",
			confirmRec.Code, confirmRec.Body.String())
	} else {
		var confirmErr map[string]any
		decodeResponse(t, confirmRec, &confirmErr)
		if code, _ := confirmErr["code"].(string); code != "UNRESOLVED_COLLISIONS" {
			t.Errorf("code = %v, want UNRESOLVED_COLLISIONS", confirmErr["code"])
		}
	}

	if got := countTransactionsForUser(t, h.db, admin.ID); got != 1 {
		t.Errorf("live rows = %d, want 1 — the rename let a re-import of the same sheet duplicate the ledger", got)
	}
}

// TestUpdateCategory_NonNameEdit_PreservesContentHashes is the mirror
// regression: clearing on an update that did NOT move the category name.
//
// PUT /api/categories/{id} is a full replace of {name, icon}, but a full
// replace of the columns is not a change of their values. The settings UI
// resends the current name on every icon change, so an icon-only edit hands
// the handler an identical name. Clearing there de-anchors every row in the
// category from import dedupe for nothing, and a re-import of the file they
// came from doubles all of them.
func TestUpdateCategory_NonNameEdit_PreservesContentHashes(t *testing.T) {
	h := setupHandler(t)
	admin := seedTestUser(t, h.queries, "admin", RoleAdmin)
	cat := seedTestCategory(t, h.queries, "Cat-"+t.Name(), "expense")
	id := seedHashedTransaction(t, h, admin.ID, cat.ID, "2026-04-01", "Coffee", 10.0)

	before := hashOf(t, h, id)
	if !before.Valid {
		t.Fatal("precondition failed: the seeded row must anchor a content_hash")
	}

	rec := putCategory(t, h, admin, cat.ID, map[string]any{"name": cat.Name, "icon": "shopping-cart"})
	if rec.Code != http.StatusOK {
		t.Fatalf("icon-only update: status = %d; body = %s", rec.Code, rec.Body.String())
	}
	if icon := categoryIconOf(t, h, cat.ID); icon.String != "shopping-cart" {
		t.Fatalf("icon = %q — the edit never landed, so the hash assertion proves nothing", icon.String)
	}

	after := hashOf(t, h, id)
	if !after.Valid || after.String != before.String {
		t.Errorf("an icon-only category edit cleared content_hash (before %v, after %v) — the row silently left import dedupe",
			before, after)
	}
}

// TestUpdateCategory_CaseOnlyRename_PreservesContentHashes pins that the
// change detector uses ComputeContentHash's OWN normalization
// (lower(trim(name))), not raw string inequality. "food" -> "  FOOD  " renders
// a different categories.name but an identical digest, so nothing about any
// row's identity moved and nothing may be cleared.
func TestUpdateCategory_CaseOnlyRename_PreservesContentHashes(t *testing.T) {
	h := setupHandler(t)
	admin := seedTestUser(t, h.queries, "admin", RoleAdmin)
	cat := seedTestCategory(t, h.queries, "cat-"+t.Name(), "expense")
	id := seedHashedTransaction(t, h, admin.ID, cat.ID, "2026-04-01", "Coffee", 10.0)

	before := hashOf(t, h, id)
	if !before.Valid {
		t.Fatal("precondition failed: the seeded row must anchor a content_hash")
	}

	// Same digest input, different stored bytes: uppercased and padded.
	loudName := "  " + strings.ToUpper(cat.Name) + "  "
	rec := putCategory(t, h, admin, cat.ID, map[string]any{"name": loudName})
	if rec.Code != http.StatusOK {
		t.Fatalf("case-only rename: status = %d; body = %s", rec.Code, rec.Body.String())
	}
	if got := categoryNameOf(t, h, cat.ID); got != loudName {
		t.Fatalf("category name = %q, want %q — the rename never landed, so the hash assertion proves nothing", got, loudName)
	}

	after := hashOf(t, h, id)
	if !after.Valid || after.String != before.String {
		t.Errorf("a case/whitespace-only rename cleared content_hash (before %v, after %v) — the digest never moved",
			before, after)
	}
}

// TestUpdateCategory_Rename_LeavesTombstonedRowsAlone pins the soft-delete
// boundary. A tombstoned row is outside the partial unique index
// (idx_transactions_content_hash filters deleted_at IS NULL), so its hash is
// inert for dedupe; the audit trail wants its pre-delete state preserved, and
// the clear must not touch deleted_at either.
func TestUpdateCategory_Rename_LeavesTombstonedRowsAlone(t *testing.T) {
	h := setupHandler(t)
	admin := seedTestUser(t, h.queries, "admin", RoleAdmin)
	cat := seedTestCategory(t, h.queries, "Cat-"+t.Name(), "expense")

	live := seedHashedTransaction(t, h, admin.ID, cat.ID, "2026-04-01", "Coffee", 10.0)
	tombstoned := seedHashedTransaction(t, h, admin.ID, cat.ID, "2026-04-02", "Bagel", 4.0)
	if err := h.queries.SoftDeleteTransaction(context.Background(), tombstoned); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	buried := hashOf(t, h, tombstoned)
	if !buried.Valid {
		t.Fatal("precondition failed: the tombstoned row must still hold its content_hash")
	}

	rec := putCategory(t, h, admin, cat.ID, map[string]any{"name": "Groceries-" + t.Name()})
	if rec.Code != http.StatusOK {
		t.Fatalf("rename: status = %d; body = %s", rec.Code, rec.Body.String())
	}

	if hash := hashOf(t, h, live); hash.Valid {
		t.Errorf("live row kept content_hash %q after the rename", hash.String)
	}
	if hash := hashOf(t, h, tombstoned); !hash.Valid || hash.String != buried.String {
		t.Errorf("tombstoned row's content_hash changed: before %v, after %v", buried, hash)
	}

	var deletedAt sql.NullTime
	if err := h.db.QueryRow(`SELECT deleted_at FROM transactions WHERE id = ?`, tombstoned).Scan(&deletedAt); err != nil {
		t.Fatalf("read deleted_at: %v", err)
	}
	if !deletedAt.Valid {
		t.Error("the rename resurrected a tombstoned transaction")
	}
}
