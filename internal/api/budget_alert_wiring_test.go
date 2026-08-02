package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elienop/spendrop/internal/database"
)

// seedTombstonedExpenseRow creates an expense row and soft-deletes it through
// the store so it lands in the trash, returning the tombstoned row's id. Used
// by the restore-path hook tests.
func seedTombstonedExpenseRow(t *testing.T, h *Handler, q *database.Queries, userID, categoryID int64, date string, cents int64) int64 {
	t.Helper()
	txn := seedExpenseRow(t, q, userID, categoryID, date, cents)
	if err := h.txnStore.Delete(context.Background(), userID, txn.ID); err != nil {
		t.Fatalf("soft-delete for tombstone setup: %v", err)
	}
	return txn.ID
}

// TestRestoreTransaction_FiresOverBudgetAlert proves the single-restore path
// fires the over-budget hook: restoring a tombstoned row re-adds its cents to
// the live SUM, which can push a category over its limit.
func TestRestoreTransaction_FiresOverBudgetAlert(t *testing.T) {
	q, db := setupTestDB(t)
	rec := &recordingSender{}
	h := NewHandler(q, db)
	h.pushTesterForBudgetAlerts = rec

	user := seedTestUser(t, q, "alice", RoleMember)
	catID := seedExpenseCategory(t, q, "Groceries")
	seedPushSub(t, q, user.ID, "https://push.example/ep-restore")
	if err := q.UpsertCategoryBudget(context.Background(), database.UpsertCategoryBudgetParams{
		Year: 2026, Month: 5, CategoryID: catID, AmountCents: 10000,
	}); err != nil {
		t.Fatalf("budget: %v", err)
	}

	// Tombstoned row of $150 — over the $100 limit once restored.
	id := seedTombstonedExpenseRow(t, h, q, user.ID, catID, "2026-05-10", 15000)

	req := withUserAndURLParam(
		httptest.NewRequest(http.MethodPost, "/api/transactions/x/restore", nil),
		user, "id", itoa(id))
	w := httptest.NewRecorder()
	h.handleRestoreTransaction(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("restore: want 200, got %d: %s", w.Code, w.Body.String())
	}
	waitPush(t, h)
	if rec.count() != 1 {
		t.Fatalf("restore must fire the over-budget alert; got %d sends", rec.count())
	}
	var p pushAlertPayload
	if err := json.Unmarshal(rec.payloads[0], &p); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if p.Year != 2026 || p.Month != 5 || p.CategoryID != catID {
		t.Fatalf("alert must be for 2026-05 cat %d, got %04d-%02d cat %d", catID, p.Year, p.Month, p.CategoryID)
	}
}

// TestRestoreTransaction_DeleteThenRestoreReArmsLatch proves the
// delete-then-restore sequence re-alerts: the wired delete clears the latch
// when spend drops under, then the restore re-crosses and must alert again.
func TestRestoreTransaction_DeleteThenRestoreReArmsLatch(t *testing.T) {
	q, db := setupTestDB(t)
	rec := &recordingSender{}
	h := NewHandler(q, db)
	h.pushTesterForBudgetAlerts = rec

	user := seedTestUser(t, q, "alice", RoleMember)
	catID := seedExpenseCategory(t, q, "Groceries")
	seedPushSub(t, q, user.ID, "https://push.example/ep-rearm")
	if err := q.UpsertCategoryBudget(context.Background(), database.UpsertCategoryBudgetParams{
		Year: 2026, Month: 5, CategoryID: catID, AmountCents: 10000,
	}); err != nil {
		t.Fatalf("budget: %v", err)
	}

	// Create a $150 row over the limit -> first alert.
	body, _ := json.Marshal(transactionRequest{
		Date: "2026-05-10", Amount: 150, Description: "big shop", CategoryID: catID,
	})
	createReq := withUser(httptest.NewRequest(http.MethodPost, "/api/transactions", bytes.NewReader(body)), user)
	createRec := httptest.NewRecorder()
	h.handleCreateTransaction(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d: %s", createRec.Code, createRec.Body.String())
	}
	var created transactionResponse
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	waitPush(t, h)
	if rec.count() != 1 {
		t.Fatalf("create over budget must alert once; got %d", rec.count())
	}

	// Delete it -> spend drops to 0, latch clears (no new alert).
	delReq := withUserAndURLParam(
		httptest.NewRequest(http.MethodDelete, "/api/transactions/x", nil),
		user, "id", itoa(created.ID))
	delRec := httptest.NewRecorder()
	h.handleDeleteTransaction(delRec, delReq)
	if delRec.Code != http.StatusOK {
		t.Fatalf("delete: want 200, got %d: %s", delRec.Code, delRec.Body.String())
	}
	waitPush(t, h)
	if rec.count() != 1 {
		t.Fatalf("delete must not add an alert; got %d", rec.count())
	}

	// Restore it -> re-crosses over budget with the latch cleared, must alert.
	restoreReq := withUserAndURLParam(
		httptest.NewRequest(http.MethodPost, "/api/transactions/x/restore", nil),
		user, "id", itoa(created.ID))
	restoreRec := httptest.NewRecorder()
	h.handleRestoreTransaction(restoreRec, restoreReq)
	if restoreRec.Code != http.StatusOK {
		t.Fatalf("restore: want 200, got %d: %s", restoreRec.Code, restoreRec.Body.String())
	}
	waitPush(t, h)
	if rec.count() != 2 {
		t.Fatalf("delete-then-restore must re-alert; got %d sends total", rec.count())
	}
}

// TestBatchRestoreTransactions_FiresOverBudgetAlert proves the batch-restore
// path fires the hook for the cells it re-adds spend to.
func TestBatchRestoreTransactions_FiresOverBudgetAlert(t *testing.T) {
	q, db := setupTestDB(t)
	rec := &recordingSender{}
	h := NewHandler(q, db)
	h.pushTesterForBudgetAlerts = rec

	user := seedTestUser(t, q, "alice", RoleMember)
	catID := seedExpenseCategory(t, q, "Groceries")
	seedPushSub(t, q, user.ID, "https://push.example/ep-batch-restore")
	if err := q.UpsertCategoryBudget(context.Background(), database.UpsertCategoryBudgetParams{
		Year: 2026, Month: 5, CategoryID: catID, AmountCents: 10000,
	}); err != nil {
		t.Fatalf("budget: %v", err)
	}

	id1 := seedTombstonedExpenseRow(t, h, q, user.ID, catID, "2026-05-10", 8000)
	id2 := seedTombstonedExpenseRow(t, h, q, user.ID, catID, "2026-05-11", 8000)

	body, _ := json.Marshal(batchRestoreRequest{IDs: []int64{id1, id2}})
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/transactions/restore-batch", bytes.NewReader(body)), user)
	w := httptest.NewRecorder()
	h.handleBatchRestoreTransactions(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("batch restore: want 200, got %d: %s", w.Code, w.Body.String())
	}
	// Two rows of $80 in the same cell = $160 over the $100 limit; the cell is
	// deduped so the alert fires exactly once.
	waitPush(t, h)
	if rec.count() != 1 {
		t.Fatalf("batch restore must alert once for the deduped over-budget cell; got %d", rec.count())
	}
}

// TestRestoreAllTransactions_FiresOverBudgetAlert proves the restore-all path
// fires the hook.
func TestRestoreAllTransactions_FiresOverBudgetAlert(t *testing.T) {
	q, db := setupTestDB(t)
	rec := &recordingSender{}
	h := NewHandler(q, db)
	h.pushTesterForBudgetAlerts = rec

	user := seedTestUser(t, q, "alice", RoleMember)
	catID := seedExpenseCategory(t, q, "Groceries")
	seedPushSub(t, q, user.ID, "https://push.example/ep-restore-all")
	if err := q.UpsertCategoryBudget(context.Background(), database.UpsertCategoryBudgetParams{
		Year: 2026, Month: 5, CategoryID: catID, AmountCents: 10000,
	}); err != nil {
		t.Fatalf("budget: %v", err)
	}

	seedTombstonedExpenseRow(t, h, q, user.ID, catID, "2026-05-10", 15000)

	req := withUser(httptest.NewRequest(http.MethodPost, "/api/transactions/restore-all", nil), user)
	w := httptest.NewRecorder()
	h.handleRestoreAllTransactions(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("restore all: want 200, got %d: %s", w.Code, w.Body.String())
	}
	waitPush(t, h)
	if rec.count() != 1 {
		t.Fatalf("restore-all must fire the over-budget alert; got %d", rec.count())
	}
}

// TestBatchUpdateTransactions_FiresOverBudgetAlert proves the ID-list batch
// update path fires the hook for the new cell a relocated row crosses into.
func TestBatchUpdateTransactions_FiresOverBudgetAlert(t *testing.T) {
	q, db := setupTestDB(t)
	rec := &recordingSender{}
	h := NewHandler(q, db)
	h.pushTesterForBudgetAlerts = rec

	user := seedTestUser(t, q, "alice", RoleMember)
	overCat := seedExpenseCategory(t, q, "Groceries")
	otherCat := seedExpenseCategory(t, q, "Misc")
	seedPushSub(t, q, user.ID, "https://push.example/ep-batch-update")
	// Only Groceries has a $100 limit.
	if err := q.UpsertCategoryBudget(context.Background(), database.UpsertCategoryBudgetParams{
		Year: 2026, Month: 5, CategoryID: overCat, AmountCents: 10000,
	}); err != nil {
		t.Fatalf("budget: %v", err)
	}

	// $150 row currently in Misc (no limit -> no alert). Move it into
	// Groceries via batch update -> the new cell crosses over budget.
	txn := seedExpenseRow(t, q, user.ID, otherCat, "2026-05-10", 15000)

	body, _ := json.Marshal(batchUpdateRequest{
		IDs:   []int64{txn.ID},
		Patch: patchRequest{CategoryID: &overCat},
	})
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/transactions/update-batch", bytes.NewReader(body)), user)
	w := httptest.NewRecorder()
	h.handleBatchUpdateTransactions(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("batch update: want 200, got %d: %s", w.Code, w.Body.String())
	}
	waitPush(t, h)
	if rec.count() != 1 {
		t.Fatalf("batch update into over-budget category must alert once; got %d", rec.count())
	}
	var p pushAlertPayload
	if err := json.Unmarshal(rec.payloads[0], &p); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if p.CategoryID != overCat || p.Year != 2026 || p.Month != 5 {
		t.Fatalf("alert must be for the new Groceries cell 2026-05, got cat %d %04d-%02d", p.CategoryID, p.Year, p.Month)
	}
}

// TestImportConfirm_FiresOverBudgetAlert proves the import path fires the hook
// when an imported row pushes a category over its monthly budget.
func TestImportConfirm_FiresOverBudgetAlert(t *testing.T) {
	clearImportStore()
	q, db := setupTestDB(t)
	rec := &recordingSender{}
	h := NewHandler(q, db)
	h.pushTesterForBudgetAlerts = rec

	user := seedTestUser(t, q, "importer", RoleMember)
	seedPushSub(t, q, user.ID, "https://push.example/ep-import")

	xlsxData := createTestXLSX(t, "Transactions", []string{
		"Date", "Description", "Amount", "Category",
	}, [][]string{
		{"2026-01-15", "Big grocery run", "150.00", "Food"},
	})

	uploadReq := postMultipartFile(t, "/api/import/upload", xlsxData)
	uploadReq = withUser(uploadReq, user)
	uploadRec := httptest.NewRecorder()
	h.handleImportUpload(uploadRec, uploadReq)
	if uploadRec.Code != http.StatusOK {
		t.Fatalf("upload: want 200, got %d: %s", uploadRec.Code, uploadRec.Body.String())
	}
	var uploadResp map[string]any
	decodeResponse(t, uploadRec, &uploadResp)
	importID := uploadResp["import_id"].(string)

	cats, err := q.ListAllCategories(context.Background())
	if err != nil {
		t.Fatalf("list categories: %v", err)
	}
	var foodID int64
	catMap := make(map[string]float64)
	for _, c := range cats {
		if c.Name == "Food" {
			foodID = c.ID
		}
		catMap[c.Name] = float64(c.ID)
	}
	if foodID == 0 {
		foodID = cats[0].ID
	}

	// $100 limit on Food for Jan 2026 — the imported $150 row crosses it.
	if err := q.UpsertCategoryBudget(context.Background(), database.UpsertCategoryBudgetParams{
		Year: 2026, Month: 1, CategoryID: foodID, AmountCents: 10000,
	}); err != nil {
		t.Fatalf("budget: %v", err)
	}

	confirmBody, _ := json.Marshal(map[string]any{
		"import_id":           importID,
		"default_category_id": foodID,
		"category_map":        catMap,
	})
	confirmReq := withUser(httptest.NewRequest(http.MethodPost, "/api/import/confirm", bytes.NewReader(confirmBody)), user)
	confirmRec := httptest.NewRecorder()
	h.handleImportConfirm(confirmRec, confirmReq)
	if confirmRec.Code != http.StatusOK {
		t.Fatalf("confirm: want 200, got %d: %s", confirmRec.Code, confirmRec.Body.String())
	}
	waitPush(t, h)
	if rec.count() != 1 {
		t.Fatalf("import crossing the budget must fire the over-budget alert; got %d", rec.count())
	}
}
