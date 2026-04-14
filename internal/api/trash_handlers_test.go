package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elienop/spendrop/internal/database"
)

// The tests in this file cover the Phase 2.2 admin trash endpoints:
// handleListDeletedTransactions, handleRestoreTransaction,
// handlePurgeTransaction, and handleBatchRestoreTransactions.
//
// Two invariants dominate the assertions:
//
//  1. Tombstoned rows are visible ONLY through the trash endpoints.
//     handleListDeletedTransactions must never leak a live row, and the
//     single-row and batch restore endpoints must never touch a row that
//     is already live.
//
//  2. Audit coverage is asymmetric: restore writes a "restore" audit row
//     in the same SQL tx as the data change, but purge writes NO audit
//     row (see the long comment on TransactionStore.Purge for why — the
//     original delete audit row and the CHECK constraint together are
//     the source of truth). Every purge test therefore asserts the audit
//     row count is unchanged relative to what delete+purge together
//     should have written.

// --- handleListDeletedTransactions ---

func TestHandleListDeletedTransactions_HidesLiveRows(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	// Seed one tombstoned and one live row with distinctive amounts so
	// the assertion can key off amount rather than id.
	tombstoned := seedTombstonedTestTransaction(t, q, admin.ID, 1, "2026-04-01", 999.0, "gone")
	live := seedTestTransaction(t, q, admin.ID, 1, "2026-04-02", 100.0, "still here")

	req := httptest.NewRequest(http.MethodGet, "/api/transactions/deleted", nil)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()
	h.handleListDeletedTransactions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list deleted: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp deletedTransactionListResponse
	decodeResponse(t, rec, &resp)

	// Exactly one row: the tombstoned one. A regression that widened the
	// WHERE clause would surface here as a count of 2.
	if resp.Total != 1 {
		t.Errorf("Total=%d, want 1", resp.Total)
	}
	if len(resp.Transactions) != 1 {
		t.Fatalf("len(Transactions)=%d, want 1", len(resp.Transactions))
	}
	got := resp.Transactions[0]
	if got.ID != tombstoned.ID {
		t.Errorf("returned id=%d, want %d (tombstoned row)", got.ID, tombstoned.ID)
	}
	if got.ID == live.ID {
		t.Errorf("returned id matches live row %d — trash view leaked a live row", live.ID)
	}
	// DeletedAt must be a non-empty RFC3339 string — the whole point of
	// the deleted_transactionResponse shape is that this field is always
	// populated (no omitempty).
	if got.DeletedAt == "" {
		t.Errorf("DeletedAt is empty, want RFC3339 timestamp")
	}
}

func TestHandleListDeletedTransactions_OrdersByDeletedAtDesc(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	// Soft-delete three rows in a known order. The query orders by
	// deleted_at DESC then id DESC — since we're calling SoftDelete
	// sequentially on the same DB clock, id DESC is the tiebreaker that
	// keeps the order deterministic.
	t1 := seedTombstonedTestTransaction(t, q, admin.ID, 1, "2026-04-01", 10.0, "first deleted")
	t2 := seedTombstonedTestTransaction(t, q, admin.ID, 1, "2026-04-02", 20.0, "second deleted")
	t3 := seedTombstonedTestTransaction(t, q, admin.ID, 1, "2026-04-03", 30.0, "third deleted")

	req := httptest.NewRequest(http.MethodGet, "/api/transactions/deleted", nil)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()
	h.handleListDeletedTransactions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list deleted: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp deletedTransactionListResponse
	decodeResponse(t, rec, &resp)
	if len(resp.Transactions) != 3 {
		t.Fatalf("len=%d, want 3", len(resp.Transactions))
	}

	// The SoftDeleteTransaction query stamps deleted_at = CURRENT_TIMESTAMP
	// for each call. Per-second clock resolution in CI means the three
	// rows may share a deleted_at value; the ORDER BY tiebreaker is
	// t.id DESC, so t3.id > t2.id > t1.id should put t3 first even when
	// timestamps tie. The assertion pins that fallback ordering.
	wantOrder := []int64{t3.ID, t2.ID, t1.ID}
	for i, want := range wantOrder {
		if resp.Transactions[i].ID != want {
			t.Errorf("position %d: id=%d, want %d", i, resp.Transactions[i].ID, want)
		}
	}
}

func TestHandleListDeletedTransactions_Pagination(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	// Seed 5 tombstoned rows; request page=1, per_page=2. Total must
	// reflect the full trash count (5), and the returned slice must be
	// exactly 2 rows. A regression that used per_page for the count
	// query would set Total=2 here.
	for i := 0; i < 5; i++ {
		seedTombstonedTestTransaction(t, q, admin.ID, 1, "2026-04-01", float64(i+1), fmt.Sprintf("gone %d", i))
	}

	req := httptest.NewRequest(http.MethodGet, "/api/transactions/deleted?page=1&per_page=2", nil)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()
	h.handleListDeletedTransactions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list deleted: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp deletedTransactionListResponse
	decodeResponse(t, rec, &resp)
	if resp.Total != 5 {
		t.Errorf("Total=%d, want 5 (full trash count, not page size)", resp.Total)
	}
	if len(resp.Transactions) != 2 {
		t.Errorf("len=%d, want 2 (per_page)", len(resp.Transactions))
	}
	if resp.Page != 1 {
		t.Errorf("Page=%d, want 1", resp.Page)
	}
	if resp.PerPage != 2 {
		t.Errorf("PerPage=%d, want 2", resp.PerPage)
	}
}

func TestHandleListDeletedTransactions_CategoryNamePopulated(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	// The raw-SQL handler projects c.name AS category_name. Seed a row
	// against category id=1 (the seed "Food" row from migrations) and
	// confirm the field is non-empty. A regression that dropped the
	// JOIN would surface here as an empty string.
	seedTombstonedTestTransaction(t, q, admin.ID, 1, "2026-04-01", 10.0, "lunch")

	req := httptest.NewRequest(http.MethodGet, "/api/transactions/deleted", nil)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()
	h.handleListDeletedTransactions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list deleted: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp deletedTransactionListResponse
	decodeResponse(t, rec, &resp)
	if len(resp.Transactions) != 1 {
		t.Fatalf("len=%d, want 1", len(resp.Transactions))
	}
	if resp.Transactions[0].CategoryName == "" {
		t.Errorf("CategoryName empty — expected seeded category name from the JOIN")
	}
	if resp.Transactions[0].CategoryType == "" {
		t.Errorf("CategoryType empty — expected seeded category type from the JOIN")
	}
}

// --- handleRestoreTransaction ---

func TestHandleRestoreTransaction_FlipsDeletedAtToNull(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	txn := seedTombstonedTestTransaction(t, q, admin.ID, 1, "2026-04-01", 15.0, "oops")

	req := httptest.NewRequest(http.MethodPost, "/api/transactions/"+fmt.Sprint(txn.ID)+"/restore", nil)
	req = withUserAndURLParam(req, admin, "id", fmt.Sprint(txn.ID))
	rec := httptest.NewRecorder()
	h.handleRestoreTransaction(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("restore: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// The row must now appear in a normal member-facing list.
	if got := countTransactions(t, db); got != 1 {
		t.Errorf("live count after restore=%d, want 1", got)
	}
	if got := countTombstonedTransactions(t, db); got != 0 {
		t.Errorf("tombstone count after restore=%d, want 0", got)
	}
}

func TestHandleRestoreTransaction_WritesRestoreAuditRow(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	// Seed via the store so we get a clean before-state (no pre-existing
	// audit rows from the raw sqlc seeding helper).
	txn := seedTombstonedTestTransaction(t, q, admin.ID, 1, "2026-04-01", 42.0, "restore me")
	// The seed path bypasses the store, so the audit table should be
	// empty at this point.
	if got := countAuditRows(t, db); got != 0 {
		t.Fatalf("pre-restore audit row count=%d, want 0", got)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/transactions/"+fmt.Sprint(txn.ID)+"/restore", nil)
	req = withUserAndURLParam(req, admin, "id", fmt.Sprint(txn.ID))
	rec := httptest.NewRecorder()
	h.handleRestoreTransaction(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("restore: status=%d body=%s", rec.Code, rec.Body.String())
	}

	rows := listAuditRows(t, db)
	if len(rows) != 1 {
		t.Fatalf("audit row count=%d, want 1 restore row", len(rows))
	}
	r := rows[0]
	if r.Action != database.AuditRestore {
		t.Errorf("action=%q, want %q", r.Action, database.AuditRestore)
	}
	if r.TransactionID != txn.ID {
		t.Errorf("transaction_id=%d, want %d", r.TransactionID, txn.ID)
	}
	// before_json must carry the tombstoned snapshot; after_json must
	// carry the live one. Mirror of the delete audit test — a regression
	// that swapped before/after would show up as a tombstoned after.
	if r.Before == nil {
		t.Fatalf("restore row missing before_json")
	}
	if r.After == nil {
		t.Fatalf("restore row missing after_json")
	}
	if beforeDel, ok := r.Before["deleted_at"].(map[string]any); ok {
		if v, _ := beforeDel["Valid"].(bool); !v {
			t.Errorf("before.deleted_at.Valid=false, want true (tombstone)")
		}
	} else {
		t.Errorf("before.deleted_at missing or wrong shape: %v", r.Before["deleted_at"])
	}
	if afterDel, ok := r.After["deleted_at"].(map[string]any); ok {
		if v, _ := afterDel["Valid"].(bool); v {
			t.Errorf("after.deleted_at.Valid=true, want false (live)")
		}
	} else {
		t.Errorf("after.deleted_at missing or wrong shape: %v", r.After["deleted_at"])
	}
	assertAllActorsEqual(t, rows, admin.ID)
}

func TestHandleRestoreTransaction_NonExistent_Returns404(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	// id 99999 has never existed.
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/99999/restore", nil)
	req = withUserAndURLParam(req, admin, "id", "99999")
	rec := httptest.NewRecorder()
	h.handleRestoreTransaction(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status=%d, want 404", rec.Code)
	}
	// No audit row should be written for a 404.
	if got := countAuditRows(t, db); got != 0 {
		t.Errorf("audit rows after 404=%d, want 0", got)
	}
}

func TestHandleRestoreTransaction_AlreadyLive_Returns404(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	// Seed a LIVE row and try to restore it. From the admin's POV the
	// id "is not in the trash", which is the same user experience as
	// "the id does not exist" → both return 404. This guards the user
	// from accidental no-op 200s after an upstream retry.
	txn := seedTestTransaction(t, q, admin.ID, 1, "2026-04-01", 10.0, "already live")

	req := httptest.NewRequest(http.MethodPost, "/api/transactions/"+fmt.Sprint(txn.ID)+"/restore", nil)
	req = withUserAndURLParam(req, admin, "id", fmt.Sprint(txn.ID))
	rec := httptest.NewRecorder()
	h.handleRestoreTransaction(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status=%d, want 404", rec.Code)
	}
	if got := countAuditRows(t, db); got != 0 {
		t.Errorf("audit rows after 404=%d, want 0", got)
	}
}

func TestHandleRestoreTransaction_InvalidID_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	req := httptest.NewRequest(http.MethodPost, "/api/transactions/not-a-number/restore", nil)
	req = withUserAndURLParam(req, admin, "id", "not-a-number")
	rec := httptest.NewRecorder()
	h.handleRestoreTransaction(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", rec.Code)
	}
}

// --- handlePurgeTransaction ---

func TestHandlePurgeTransaction_RemovesRowPhysically(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	txn := seedTombstonedTestTransaction(t, q, admin.ID, 1, "2026-04-01", 15.0, "nuke me")
	// Pre-purge: 1 physical row (the tombstone), 0 live rows.
	if got := countAllTransactions(t, db); got != 1 {
		t.Fatalf("pre-purge physical count=%d, want 1", got)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/transactions/"+fmt.Sprint(txn.ID)+"/purge", nil)
	req = withUserAndURLParam(req, admin, "id", fmt.Sprint(txn.ID))
	rec := httptest.NewRecorder()
	h.handlePurgeTransaction(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("purge: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Post-purge: the row is physically gone — no tombstone, no live row.
	// This is the only code path in SpenDrop that physically removes a
	// transaction, so the "zero rows" assertion is load-bearing.
	if got := countAllTransactions(t, db); got != 0 {
		t.Errorf("post-purge physical count=%d, want 0", got)
	}
}

func TestHandlePurgeTransaction_DoesNotWriteAuditRow(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	// Route the soft-delete through the handler so a single "delete"
	// audit row lands first — the purge test then asserts that the purge
	// itself added *nothing* new. This matches the documented asymmetry
	// on TransactionStore.Purge: the original delete audit row plus the
	// audit table's ON DELETE SET NULL FK are together the whole audit
	// story for a purge.
	live := seedTestTransaction(t, q, admin.ID, 1, "2026-04-01", 15.0, "first delete then purge")

	delReq := httptest.NewRequest(http.MethodDelete, "/api/transactions/"+fmt.Sprint(live.ID), nil)
	delReq = withUserAndURLParam(delReq, admin, "id", fmt.Sprint(live.ID))
	delRec := httptest.NewRecorder()
	h.handleDeleteTransaction(delRec, delReq)
	if delRec.Code != http.StatusOK {
		t.Fatalf("soft-delete: status=%d body=%s", delRec.Code, delRec.Body.String())
	}
	if got := countAuditRows(t, db); got != 1 {
		t.Fatalf("post-delete audit count=%d, want 1", got)
	}

	purgeReq := httptest.NewRequest(http.MethodDelete, "/api/transactions/"+fmt.Sprint(live.ID)+"/purge", nil)
	purgeReq = withUserAndURLParam(purgeReq, admin, "id", fmt.Sprint(live.ID))
	purgeRec := httptest.NewRecorder()
	h.handlePurgeTransaction(purgeRec, purgeReq)
	if purgeRec.Code != http.StatusOK {
		t.Fatalf("purge: status=%d body=%s", purgeRec.Code, purgeRec.Body.String())
	}

	// CRITICAL: the purge must not have added an audit row. The audit
	// CHECK constraint doesn't even allow a 'purge' action today, so a
	// regression that tried to write one would surface as a constraint
	// error — but we also want to defend against the opposite regression
	// of writing a misnamed 'delete' row on purge.
	if got := countAuditRows(t, db); got != 1 {
		t.Errorf("post-purge audit count=%d, want 1 (only the original delete row)", got)
	}
	// And the one surviving audit row is the delete row, still pointing
	// at the now-gone transaction id. The audit table has no FK to
	// transactions precisely so this row outlives the purge.
	rows := listAuditRows(t, db)
	if rows[0].Action != database.AuditDelete {
		t.Errorf("surviving row action=%q, want %q", rows[0].Action, database.AuditDelete)
	}
	if rows[0].TransactionID != live.ID {
		t.Errorf("surviving row txn_id=%d, want %d", rows[0].TransactionID, live.ID)
	}
}

func TestHandlePurgeTransaction_NonExistent_Returns404(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	req := httptest.NewRequest(http.MethodDelete, "/api/transactions/99999/purge", nil)
	req = withUserAndURLParam(req, admin, "id", "99999")
	rec := httptest.NewRecorder()
	h.handlePurgeTransaction(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status=%d, want 404", rec.Code)
	}
	if got := countAuditRows(t, db); got != 0 {
		t.Errorf("audit rows after 404=%d, want 0", got)
	}
}

func TestHandlePurgeTransaction_AlreadyLive_Returns404(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	// A live row cannot be purged — the trash view is the only UI that
	// can reach the purge endpoint, so a live id means either a stale
	// client state or an attempt to bypass the soft-delete flow. Either
	// way, 404 is the correct signal: "this id is not in the trash."
	txn := seedTestTransaction(t, q, admin.ID, 1, "2026-04-01", 10.0, "live row")

	req := httptest.NewRequest(http.MethodDelete, "/api/transactions/"+fmt.Sprint(txn.ID)+"/purge", nil)
	req = withUserAndURLParam(req, admin, "id", fmt.Sprint(txn.ID))
	rec := httptest.NewRecorder()
	h.handlePurgeTransaction(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status=%d, want 404", rec.Code)
	}
	// The live row must still be there.
	if got := countTransactions(t, db); got != 1 {
		t.Errorf("live row count=%d, want 1 (purge on live must not touch the row)", got)
	}
}

func TestHandlePurgeTransaction_InvalidID_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	req := httptest.NewRequest(http.MethodDelete, "/api/transactions/not-a-number/purge", nil)
	req = withUserAndURLParam(req, admin, "id", "not-a-number")
	rec := httptest.NewRecorder()
	h.handlePurgeTransaction(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", rec.Code)
	}
}

// --- handleBatchRestoreTransactions ---

func TestHandleBatchRestoreTransactions_RestoresAllAndCounts(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	t1 := seedTombstonedTestTransaction(t, q, admin.ID, 1, "2026-04-01", 10.0, "a")
	t2 := seedTombstonedTestTransaction(t, q, admin.ID, 1, "2026-04-02", 20.0, "b")
	t3 := seedTombstonedTestTransaction(t, q, admin.ID, 1, "2026-04-03", 30.0, "c")

	body := strings.NewReader(fmt.Sprintf(`{"ids":[%d,%d,%d]}`, t1.ID, t2.ID, t3.ID))
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/restore-batch", body)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()
	h.handleBatchRestoreTransactions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("batch restore: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp batchRestoreResponse
	decodeResponse(t, rec, &resp)
	if resp.Restored != 3 {
		t.Errorf("Restored=%d, want 3", resp.Restored)
	}
	if got := countTransactions(t, db); got != 3 {
		t.Errorf("live count after batch restore=%d, want 3", got)
	}
	if got := countTombstonedTransactions(t, db); got != 0 {
		t.Errorf("tombstone count after batch restore=%d, want 0", got)
	}

	// Three restore audit rows — one per id — all committed in the same
	// tx. A regression that used separate txs per id would still satisfy
	// the row count but would fail the atomicity tests elsewhere; the
	// important assertion here is the per-id fan-out.
	rows := listAuditRows(t, db)
	if len(rows) != 3 {
		t.Fatalf("audit row count=%d, want 3", len(rows))
	}
	gotIDs := map[int64]bool{}
	for _, r := range rows {
		if r.Action != database.AuditRestore {
			t.Errorf("row %d action=%q, want %q", r.TransactionID, r.Action, database.AuditRestore)
		}
		gotIDs[r.TransactionID] = true
	}
	for _, id := range []int64{t1.ID, t2.ID, t3.ID} {
		if !gotIDs[id] {
			t.Errorf("missing audit row for restored transaction id=%d", id)
		}
	}
	assertAllActorsEqual(t, rows, admin.ID)
}

func TestHandleBatchRestoreTransactions_SkipsLiveAndMissingIDs(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	// Mix three kinds of ids: tombstoned (restorable), live (already
	// out of the trash), and non-existent (999). The response count
	// must reflect only the tombstoned row that actually flipped.
	tomb := seedTombstonedTestTransaction(t, q, admin.ID, 1, "2026-04-01", 10.0, "tombstoned")
	live := seedTestTransaction(t, q, admin.ID, 1, "2026-04-02", 20.0, "live")
	const missing int64 = 999

	body := strings.NewReader(fmt.Sprintf(`{"ids":[%d,%d,%d]}`, tomb.ID, live.ID, missing))
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/restore-batch", body)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()
	h.handleBatchRestoreTransactions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("batch restore: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp batchRestoreResponse
	decodeResponse(t, rec, &resp)
	if resp.Restored != 1 {
		t.Errorf("Restored=%d, want 1 (only the tombstoned row)", resp.Restored)
	}

	// live row stays live, tombstoned row is now live, missing row
	// is still missing — total live count = 2.
	if got := countTransactions(t, db); got != 2 {
		t.Errorf("live count=%d, want 2 (one pre-existing live + one restored)", got)
	}

	// Only the tombstoned-then-restored row wrote an audit row.
	rows := listAuditRows(t, db)
	if len(rows) != 1 {
		t.Errorf("audit row count=%d, want 1 (one restore, nothing else)", len(rows))
	}
	if len(rows) == 1 && rows[0].TransactionID != tomb.ID {
		t.Errorf("audit row id=%d, want %d", rows[0].TransactionID, tomb.ID)
	}
}

func TestHandleBatchRestoreTransactions_EmptyIDs_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	body := strings.NewReader(`{"ids":[]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/restore-batch", body)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()
	h.handleBatchRestoreTransactions(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", rec.Code)
	}
}

func TestHandleBatchRestoreTransactions_TooManyIDs_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	// Build a JSON body with MaxBatchRestoreIDs+1 ids so the validation
	// rejects it before any DB work. The ids don't need to exist — the
	// size check runs first.
	var sb strings.Builder
	sb.WriteString(`{"ids":[`)
	for i := 0; i < MaxBatchRestoreIDs+1; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, "%d", i+1)
	}
	sb.WriteString(`]}`)

	req := httptest.NewRequest(http.MethodPost, "/api/transactions/restore-batch", strings.NewReader(sb.String()))
	req = withUser(req, admin)
	rec := httptest.NewRecorder()
	h.handleBatchRestoreTransactions(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", rec.Code)
	}
}

func TestHandleBatchRestoreTransactions_InvalidJSON_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	req := httptest.NewRequest(http.MethodPost, "/api/transactions/restore-batch", strings.NewReader(`{not json`))
	req = withUser(req, admin)
	rec := httptest.NewRecorder()
	h.handleBatchRestoreTransactions(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", rec.Code)
	}
}

// --- router-level admin gating ---
//
// The tests above exercise the handlers directly and bypass RequireAdmin
// by setting the auth.UserContextKey themselves. These tests round-trip
// through the router so that auth.RequireAdmin has to sign off on the
// request — exactly the surface that guards the trash view from ordinary
// members in production.

func TestNewRouter_TrashEndpoints_WithoutAuth_Return401(t *testing.T) {
	q, db := setupTestDB(t)
	router := NewRouter(q, db, nil)

	// POST/DELETE require a JSON Content-Type to clear the JSON-type
	// middleware, so the 401 is surfaced by the auth middleware rather
	// than the content-type middleware.
	cases := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/transactions/deleted"},
		{http.MethodPost, "/api/transactions/1/restore"},
		{http.MethodDelete, "/api/transactions/1/purge"},
		{http.MethodPost, "/api/transactions/restore-batch"},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			req := httptest.NewRequest(c.method, c.path, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status=%d, want 401; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestNewRouter_TrashEndpoints_AsMember_Return403(t *testing.T) {
	q, db := setupTestDB(t)
	router := NewRouter(q, db, nil)

	// First user registers as admin automatically. Second user needs
	// registration_enabled=true and then becomes a member.
	regBody := strings.NewReader(`{"username":"admin","password":"longpassword"}`)
	regReq := httptest.NewRequest(http.MethodPost, "/api/auth/register", regBody)
	regReq.Header.Set("Content-Type", "application/json")
	regRec := httptest.NewRecorder()
	router.ServeHTTP(regRec, regReq)
	if regRec.Code != http.StatusCreated {
		t.Fatalf("first register: %d body=%s", regRec.Code, regRec.Body.String())
	}
	if err := q.UpsertSetting(context.Background(), database.UpsertSettingParams{
		Key:   "registration_enabled",
		Value: "true",
	}); err != nil {
		t.Fatalf("upsert setting: %v", err)
	}

	memberBody := strings.NewReader(`{"username":"member","password":"longpassword"}`)
	memberReq := httptest.NewRequest(http.MethodPost, "/api/auth/register", memberBody)
	memberReq.Header.Set("Content-Type", "application/json")
	memberRec := httptest.NewRecorder()
	router.ServeHTTP(memberRec, memberReq)
	if memberRec.Code != http.StatusCreated {
		t.Fatalf("second register: %d body=%s", memberRec.Code, memberRec.Body.String())
	}
	var memberCookie *http.Cookie
	for _, c := range memberRec.Result().Cookies() {
		if c.Name == "session" {
			memberCookie = c
			break
		}
	}
	if memberCookie == nil {
		t.Fatal("no session cookie for member")
	}

	// Every non-GET case has to carry a JSON Content-Type to clear
	// requireJSONContentType — otherwise the 415 from that middleware
	// shadows the 403 we are trying to exercise. DELETE /…/purge in
	// particular needs a body and Content-Type even though the handler
	// itself reads nothing from either.
	cases := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/transactions/deleted", ""},
		{http.MethodPost, "/api/transactions/1/restore", `{}`},
		{http.MethodDelete, "/api/transactions/1/purge", `{}`},
		{http.MethodPost, "/api/transactions/restore-batch", `{"ids":[1]}`},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			var r *http.Request
			if c.body == "" {
				r = httptest.NewRequest(c.method, c.path, nil)
			} else {
				r = httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
				r.Header.Set("Content-Type", "application/json")
			}
			r.AddCookie(memberCookie)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, r)
			if rec.Code != http.StatusForbidden {
				t.Errorf("status=%d, want 403; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestNewRouter_TrashEndpoints_AsAdmin_RoutesResolve(t *testing.T) {
	// Smoke test only: with a valid admin session the trash endpoints
	// must at least route past auth.RequireAdmin. We don't assert the
	// body — the per-handler tests above do that — only that the 403
	// we saw in the member test above is not emitted for an admin.
	q, db := setupTestDB(t)
	router := NewRouter(q, db, nil)

	regBody := strings.NewReader(`{"username":"admin","password":"longpassword"}`)
	regReq := httptest.NewRequest(http.MethodPost, "/api/auth/register", regBody)
	regReq.Header.Set("Content-Type", "application/json")
	regRec := httptest.NewRecorder()
	router.ServeHTTP(regRec, regReq)
	if regRec.Code != http.StatusCreated {
		t.Fatalf("register: %d body=%s", regRec.Code, regRec.Body.String())
	}
	var adminCookie *http.Cookie
	for _, c := range regRec.Result().Cookies() {
		if c.Name == "session" {
			adminCookie = c
			break
		}
	}
	if adminCookie == nil {
		t.Fatal("no session cookie for admin")
	}

	// GET /api/transactions/deleted as admin → 200 (empty trash is fine).
	listReq := httptest.NewRequest(http.MethodGet, "/api/transactions/deleted", nil)
	listReq.AddCookie(adminCookie)
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Errorf("list as admin: status=%d body=%s", listRec.Code, listRec.Body.String())
	}
	var body deletedTransactionListResponse
	if err := json.NewDecoder(listRec.Body).Decode(&body); err != nil {
		t.Errorf("decode body: %v", err)
	}
	if body.Total != 0 {
		t.Errorf("empty trash Total=%d, want 0", body.Total)
	}
}
