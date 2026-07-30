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

// TestUpdateCategory_UnknownID_Returns404 pins the not-found contract across
// the change of mechanism. Before the content_hash clear landed, "no such
// category" was inferred from UpdateCategory's RowsAffected() == 0; the clear
// needs the row's PRIOR name, so the handler now reads it up front and the 404
// comes from that read's sql.ErrNoRows instead. Same status, different source
// — and nothing else in the suite covers it.
func TestUpdateCategory_UnknownID_Returns404(t *testing.T) {
	h := setupHandler(t)
	admin := seedTestUser(t, h.queries, "admin", RoleAdmin)

	rec := putCategory(t, h, admin, 999999, map[string]any{"name": "Ghost"})
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
}

// TestUpdateCategory_RejectedRename_LeavesContentHashesIntact pins the other
// half of the atomicity requirement: the clear must not survive a rename that
// never committed.
//
// categories.name is UNIQUE, so renaming onto another category's name is a
// real, reachable rejection. An implementation that clears the hashes on its
// own connection — or clears first and renames second — leaves every row in
// the category un-anchored from import dedupe while the category still carries
// its original name, so a re-import of the file those rows came from doubles
// all of them. Sharing one transaction makes the rollback undo both.
func TestUpdateCategory_RejectedRename_LeavesContentHashesIntact(t *testing.T) {
	h := setupHandler(t)
	admin := seedTestUser(t, h.queries, "admin", RoleAdmin)
	alpha := seedTestCategory(t, h.queries, "Alpha-"+t.Name(), "expense")
	beta := seedTestCategory(t, h.queries, "Beta-"+t.Name(), "expense")
	id := seedHashedTransaction(t, h, admin.ID, alpha.ID, "2026-04-01", "Coffee", 10.0)

	before := hashOf(t, h, id)
	if !before.Valid {
		t.Fatal("precondition failed: the seeded row must anchor a content_hash")
	}

	// Rename Alpha onto Beta's name: UNIQUE constraint failed, nothing commits.
	rec := putCategory(t, h, admin, alpha.ID, map[string]any{"name": beta.Name})
	if rec.Code == http.StatusOK {
		t.Fatalf("precondition failed: renaming onto a taken name must be rejected; got 200")
	}
	if got := categoryNameOf(t, h, alpha.ID); got != alpha.Name {
		t.Fatalf("category name = %q, want %q — the rejected rename leaked through", got, alpha.Name)
	}

	after := hashOf(t, h, id)
	if !after.Valid || after.String != before.String {
		t.Errorf("a rejected rename still cleared content_hash (before %v, after %v) — the clear did not share the rename's transaction",
			before, after)
	}
}

// soleTransactionIDOf returns the id of the user's only transaction row
// (tombstoned or not). It exists so a test can reach the row an import created
// without depending on the list handler's soft-delete filter.
func soleTransactionIDOf(t *testing.T, h *Handler, userID int64) int64 {
	t.Helper()
	var id int64
	if err := h.db.QueryRow(
		`SELECT id FROM transactions WHERE user_id = ?`, userID,
	).Scan(&id); err != nil {
		t.Fatalf("read sole transaction id: %v", err)
	}
	return id
}

// deletedAtOf reads a row's tombstone column directly, bypassing every
// soft-delete filter in the query layer.
func deletedAtOf(t *testing.T, h *Handler, id int64) sql.NullTime {
	t.Helper()
	var deletedAt sql.NullTime
	if err := h.db.QueryRow(
		`SELECT deleted_at FROM transactions WHERE id = ?`, id,
	).Scan(&deletedAt); err != nil {
		t.Fatalf("read deleted_at: %v", err)
	}
	return deletedAt
}

// TestUpdateCategory_Rename_ThenRestoreFromTrash_ThenReimport_DetectsDuplicates
// is the soft-delete half of the stale-identity harm case, and the reason the
// clear may NOT stop at live rows.
//
// A tombstoned row's content_hash is DORMANT, not inert. It sits outside the
// partial unique index and outside GetTransactionByContentHash only for as
// long as the row stays in the trash — and the app ships three endpoints that
// take it back out (POST /transactions/{id}/restore, restore-batch,
// restore-all; router.go). RestoreTransaction flips deleted_at back to NULL
// and never touches content_hash, so a restore RE-ARMS whatever digest the row
// was holding.
//
// Skipping tombstoned rows during a rename therefore let a row re-enter the
// live set, the unique index and the dedupe lookup carrying a digest computed
// from a category name that no longer exists — permanently, because
// BackfillContentHashes only ever processes rows WHERE content_hash IS NULL and
// there is no purge worker to bound the window. Re-importing the very sheet the
// row came from then hashed under the NEW name, matched nothing, and inserted a
// second live copy: HTTP 200, imported=1, no collision group, no warning.
//
// After the fix the clear reaches the tombstoned row too, the next boot
// re-anchors it under the new name, and the re-import is recognised as a
// duplicate — the same 409 UNRESOLVED_COLLISIONS shape the never-trashed case
// gets in TestUpdateCategory_Rename_ThenReimport_DetectsDuplicates.
func TestUpdateCategory_Rename_ThenRestoreFromTrash_ThenReimport_DetectsDuplicates(t *testing.T) {
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
	id := soleTransactionIDOf(t, h, admin.ID)

	// Trash the row through the endpoint the Trash page drives.
	delRec := httptest.NewRecorder()
	h.handleDeleteTransaction(delRec, withUserAndURLParam(
		httptest.NewRequest(http.MethodDelete, "/api/transactions/x", nil),
		admin, "id", strconv.FormatInt(id, 10)))
	if delRec.Code != http.StatusOK {
		t.Fatalf("delete: status = %d; body = %s", delRec.Code, delRec.Body.String())
	}
	if !deletedAtOf(t, h, id).Valid {
		t.Fatal("precondition failed: the row must be tombstoned before the rename")
	}

	// Rename the category while the row sits in the trash.
	newName := "Groceries-" + t.Name()
	if rec := putCategory(t, h, admin, foodID, map[string]any{"name": newName}); rec.Code != http.StatusOK {
		t.Fatalf("rename: status = %d; body = %s", rec.Code, rec.Body.String())
	}

	// The clear is an unqualified UPDATE ... WHERE category_id = ?, so it must
	// leave the tombstone itself alone: dropping content_hash out of dedupe is
	// derived-column hygiene, resurrecting a trashed row is a ledger change.
	if !deletedAtOf(t, h, id).Valid {
		t.Fatal("the rename resurrected a tombstoned transaction")
	}
	if hash := hashOf(t, h, id); hash.Valid {
		t.Errorf("tombstoned row kept content_hash %q across the rename — a restore re-arms that stale digest",
			hash.String)
	}

	// Restore it: the row re-enters the live set, the partial unique index and
	// GetTransactionByContentHash.
	restoreRec := httptest.NewRecorder()
	h.handleRestoreTransaction(restoreRec, withUserAndURLParam(
		httptest.NewRequest(http.MethodPost, "/api/transactions/x/restore", nil),
		admin, "id", strconv.FormatInt(id, 10)))
	if restoreRec.Code != http.StatusOK {
		t.Fatalf("restore: status = %d; body = %s", restoreRec.Code, restoreRec.Body.String())
	}
	if deletedAtOf(t, h, id).Valid {
		t.Fatal("precondition failed: the restore did not clear the tombstone")
	}

	// The next boot re-anchors the cleared row under the new name.
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
		t.Errorf("live rows = %d, want 1 — a row restored from the trash across a rename let a re-import of the same sheet duplicate the ledger", got)
	}
}
