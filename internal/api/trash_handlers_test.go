package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elienop/spendrop/internal/database"
)

// seedContentHashRow creates a transaction carrying a content_hash derived
// from its own fields, mirroring how the import path populates the column.
// tombstoned controls whether the row is immediately soft-deleted. It is
// used by the trash-restore collision tests: a tombstoned row keeps its
// content_hash (SoftDeleteTransaction never clears it) and is invisible to
// the deleted_at-filtered dedup lookup, so a separately-seeded LIVE row can
// legitimately own the same hash. Restoring the tombstoned row then re-enters
// the partial unique index and collides.
func seedContentHashRow(t *testing.T, q *database.Queries, userID, categoryID int64, date string, amountCents int64, desc, categoryName string, tombstoned bool) database.Transaction {
	t.Helper()
	d, _ := time.Parse("2006-01-02", date)
	hash := database.ComputeContentHash(d, amountCents, desc, categoryName)
	txn, err := q.CreateTransaction(context.Background(), database.CreateTransactionParams{
		UserID:      userID,
		Date:        d,
		AmountCents: amountCents,
		Description: desc,
		CategoryID:  categoryID,
		ContentHash: sql.NullString{String: hash, Valid: true},
	})
	if err != nil {
		t.Fatalf("seed content-hash row: %v", err)
	}
	if tombstoned {
		if err := q.SoftDeleteTransaction(context.Background(), txn.ID); err != nil {
			t.Fatalf("soft-delete content-hash row: %v", err)
		}
	}
	return txn
}

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

func TestHandleRestoreTransaction_MemberCannotRestoreOthersRow(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")
	member := seedTestUser(t, q, "member", "member")
	row := seedTombstonedTestTransaction(t, q, admin.ID, 1, "2026-04-01", 999.0, "not the members row")

	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/transactions/%d/restore", row.ID), nil)
	req = withUserAndURLParam(req, member, "id", fmt.Sprintf("%d", row.ID))
	rec := httptest.NewRecorder()
	h.handleRestoreTransaction(rec, req)
	// 404, not 403: a row sitting in another member's trash is, from this
	// member's perspective, not in the trash at all — the same answer the
	// scoped list gives. A 403 would turn the endpoint into an id oracle
	// over tombstones the member cannot otherwise see.
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404; body=%s", rec.Code, rec.Body.String())
	}

	// The row must STAY tombstoned — a status-only assertion would pass
	// even if the handler restored the row before rejecting.
	got, err := q.GetTransactionByID(context.Background(), row.ID)
	if err != nil {
		t.Fatalf("reload row: %v", err)
	}
	if !got.DeletedAt.Valid {
		t.Error("row was restored despite the 404")
	}
}

func TestHandleRestoreTransaction_MemberRestoresOwnRow(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	member := seedTestUser(t, q, "member", "member")
	row := seedTombstonedTestTransaction(t, q, member.ID, 1, "2026-04-02", 42.0, "members own row")

	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/transactions/%d/restore", row.ID), nil)
	req = withUserAndURLParam(req, member, "id", fmt.Sprintf("%d", row.ID))
	rec := httptest.NewRecorder()
	h.handleRestoreTransaction(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	got, err := q.GetTransactionByID(context.Background(), row.ID)
	if err != nil {
		t.Fatalf("reload row: %v", err)
	}
	if got.DeletedAt.Valid {
		t.Error("row is still tombstoned after a 200 restore")
	}
}

func TestHandleRestoreTransaction_AdminRestoresMembersRow(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")
	member := seedTestUser(t, q, "member", "member")
	row := seedTombstonedTestTransaction(t, q, member.ID, 1, "2026-04-03", 7.0, "members row, admin restore")

	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/transactions/%d/restore", row.ID), nil)
	req = withUserAndURLParam(req, admin, "id", fmt.Sprintf("%d", row.ID))
	rec := httptest.NewRecorder()
	h.handleRestoreTransaction(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200 (admin bypass); body=%s", rec.Code, rec.Body.String())
	}

	// A 200 alone would also be emitted by a handler that took the admin
	// bypass and then failed to clear the tombstone — reload and check the
	// row actually came back, same as the member-restores-own-row test.
	got, err := q.GetTransactionByID(context.Background(), row.ID)
	if err != nil {
		t.Fatalf("reload row: %v", err)
	}
	if got.DeletedAt.Valid {
		t.Error("row is still tombstoned after a 200 restore")
	}
}

// --- content_hash collision on restore (TRASH-RESTORE-HASH-001) ---
//
// A tombstoned row keeps its content_hash, but the partial unique index
// idx_transactions_content_hash excludes tombstoned rows and the dedup
// lookup filters deleted_at IS NULL — so the same statement can be
// re-imported as a NEW live row carrying the identical hash. Restoring the
// tombstoned row then flips deleted_at back to NULL, re-enters the partial
// index, and hits "UNIQUE constraint failed: transactions.content_hash".
// The handlers must surface this as an actionable 409 (single) and a
// skip-and-count "conflicted" (batch / all), never an opaque 500 or a
// whole-batch rollback.

func TestHandleRestoreTransaction_ContentHashCollision_Returns409(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	// Tombstoned row (keeps its content_hash) + a separately-seeded LIVE
	// row with the SAME content (mirrors a re-import while the row was in
	// the trash). Both use category id=1 ("Food") so the hashes match.
	tomb := seedContentHashRow(t, q, admin.ID, 1, "2026-04-01", 5000, "lunch", "Food", true)
	_ = seedContentHashRow(t, q, admin.ID, 1, "2026-04-01", 5000, "lunch", "Food", false)

	if got := countAuditRows(t, db); got != 0 {
		t.Fatalf("pre-restore audit count=%d, want 0", got)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/transactions/"+fmt.Sprint(tomb.ID)+"/restore", nil)
	req = withUserAndURLParam(req, admin, "id", fmt.Sprint(tomb.ID))
	rec := httptest.NewRecorder()
	h.handleRestoreTransaction(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	// Body must point the user at the existing/duplicate row.
	if body := rec.Body.String(); !strings.Contains(strings.ToLower(body), "already exists") {
		t.Errorf("409 body=%q, want a message mentioning an existing/duplicate transaction", body)
	}
	// The remedies offered must both be reachable by whoever reads them.
	// Restore is member-open but purge is admin-only, so telling the
	// reader to "purge this trashed copy" is an instruction most readers
	// cannot follow — the copy names editing the live row (anyone can) and
	// asking an admin to purge (everyone can do that much).
	body := strings.ToLower(rec.Body.String())
	if strings.Contains(body, "exists. purge this trashed copy") {
		t.Errorf("409 body=%q tells the reader to purge, which is admin-only", body)
	}
	if !strings.Contains(body, "ask an admin to purge") {
		t.Errorf("409 body=%q, want it to route the purge remedy through an admin", body)
	}

	// The trashed row stayed in the trash — restore did not partially apply.
	if got := countTombstonedTransactions(t, db); got != 1 {
		t.Errorf("tombstone count after 409=%d, want 1 (row stays in trash)", got)
	}
	// No restore audit row was written for the failed restore.
	if got := countAuditRows(t, db); got != 0 {
		t.Errorf("audit rows after 409=%d, want 0", got)
	}
}

func TestHandleBatchRestoreTransactions_SkipsContentHashCollisionRestoresRest(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	// Three clean tombstoned rows + one tombstoned row whose hash collides
	// with a separately-seeded live row.
	c1 := seedTombstonedTestTransaction(t, q, admin.ID, 1, "2026-04-01", 10.0, "clean a")
	c2 := seedTombstonedTestTransaction(t, q, admin.ID, 1, "2026-04-02", 20.0, "clean b")
	c3 := seedTombstonedTestTransaction(t, q, admin.ID, 1, "2026-04-03", 30.0, "clean c")
	colliding := seedContentHashRow(t, q, admin.ID, 1, "2026-04-04", 4000, "dup", "Food", true)
	_ = seedContentHashRow(t, q, admin.ID, 1, "2026-04-04", 4000, "dup", "Food", false)

	body := strings.NewReader(fmt.Sprintf(`{"ids":[%d,%d,%d,%d]}`, c1.ID, c2.ID, c3.ID, colliding.ID))
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
	if resp.Conflicted != 1 {
		t.Errorf("Conflicted=%d, want 1", resp.Conflicted)
	}

	// Only the colliding row remains in the trash.
	if got := countTombstonedTransactions(t, db); got != 1 {
		t.Errorf("tombstone count after batch=%d, want 1 (only the colliding row)", got)
	}
	// Exactly three restore audit rows — the colliding id wrote none.
	rows := listAuditRows(t, db)
	if len(rows) != 3 {
		t.Fatalf("audit row count=%d, want 3 (colliding id writes none)", len(rows))
	}
	for _, r := range rows {
		if r.Action != database.AuditRestore {
			t.Errorf("row %d action=%q, want %q", r.TransactionID, r.Action, database.AuditRestore)
		}
		if r.TransactionID == colliding.ID {
			t.Errorf("colliding id %d wrote a restore audit row — it should have been skipped", colliding.ID)
		}
	}
}

func TestHandleBatchRestoreTransactions_CollisionDoesNotPoisonTxForLaterIDs(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	// Order the ids so the colliding id is NOT last. If a UNIQUE violation
	// poisoned the surrounding *sql.Tx (instead of aborting only the failing
	// UPDATE), clean2 and clean3 — restored AFTER the collision — would fail
	// to commit. This pins the SQLite statement-abort assumption.
	clean1 := seedTombstonedTestTransaction(t, q, admin.ID, 1, "2026-04-01", 10.0, "clean 1")
	colliding := seedContentHashRow(t, q, admin.ID, 1, "2026-04-02", 5000, "dup", "Food", true)
	_ = seedContentHashRow(t, q, admin.ID, 1, "2026-04-02", 5000, "dup", "Food", false)
	clean2 := seedTombstonedTestTransaction(t, q, admin.ID, 1, "2026-04-03", 30.0, "clean 2")
	clean3 := seedTombstonedTestTransaction(t, q, admin.ID, 1, "2026-04-04", 40.0, "clean 3")

	body := strings.NewReader(fmt.Sprintf(`{"ids":[%d,%d,%d,%d]}`, clean1.ID, colliding.ID, clean2.ID, clean3.ID))
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
		t.Errorf("Restored=%d, want 3 (clean1, clean2, clean3 — clean2/clean3 follow the collision)", resp.Restored)
	}
	if resp.Conflicted != 1 {
		t.Errorf("Conflicted=%d, want 1", resp.Conflicted)
	}

	// The two clean rows AFTER the collision must be live, proving the tx
	// stayed usable past the constraint error.
	for _, id := range []int64{clean2.ID, clean3.ID} {
		row, err := q.GetTransactionByID(context.Background(), id)
		if err != nil {
			t.Fatalf("get row %d: %v", id, err)
		}
		if row.DeletedAt.Valid {
			t.Errorf("row %d (restored after the collision) is still tombstoned — tx was poisoned", id)
		}
	}
	// Only the colliding row remains trashed.
	if got := countTombstonedTransactions(t, db); got != 1 {
		t.Errorf("tombstone count=%d, want 1 (only the colliding row)", got)
	}
}

func TestHandleRestoreAllTransactions_SkipsContentHashCollision(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	seedTombstonedTestTransaction(t, q, admin.ID, 1, "2026-04-01", 10.0, "clean a")
	seedTombstonedTestTransaction(t, q, admin.ID, 1, "2026-04-02", 20.0, "clean b")
	seedTombstonedTestTransaction(t, q, admin.ID, 1, "2026-04-03", 30.0, "clean c")
	colliding := seedContentHashRow(t, q, admin.ID, 1, "2026-04-04", 4000, "dup", "Food", true)
	_ = seedContentHashRow(t, q, admin.ID, 1, "2026-04-04", 4000, "dup", "Food", false)

	req := httptest.NewRequest(http.MethodPost, "/api/transactions/restore-all", nil)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()
	h.handleRestoreAllTransactions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("restore-all: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp restoreAllResponse
	decodeResponse(t, rec, &resp)
	if resp.Restored != 3 {
		t.Errorf("Restored=%d, want 3", resp.Restored)
	}
	if resp.Conflicted != 1 {
		t.Errorf("Conflicted=%d, want 1", resp.Conflicted)
	}

	if got := countTombstonedTransactions(t, db); got != 1 {
		t.Errorf("tombstone count after restore-all=%d, want 1 (only the colliding row)", got)
	}
	rows := listAuditRows(t, db)
	if len(rows) != 3 {
		t.Fatalf("audit row count=%d, want 3 (colliding id writes none)", len(rows))
	}
	for _, r := range rows {
		if r.TransactionID == colliding.ID {
			t.Errorf("colliding id %d wrote a restore audit row — it should have been skipped", colliding.ID)
		}
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

func TestHandleBatchRestoreTransactions_MemberRestoresOnlyOwnRows(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")
	member := seedTestUser(t, q, "member", "member")

	m1 := seedTombstonedTestTransaction(t, q, member.ID, 1, "2026-04-01", 10.0, "members row 1")
	a1 := seedTombstonedTestTransaction(t, q, admin.ID, 1, "2026-04-02", 999.0, "admins row")
	m2 := seedTombstonedTestTransaction(t, q, member.ID, 1, "2026-04-03", 30.0, "members row 2")

	body := fmt.Sprintf(`{"ids":[%d,%d,%d]}`, m1.ID, a1.ID, m2.ID)
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/restore-batch", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUser(req, member)
	rec := httptest.NewRecorder()
	h.handleBatchRestoreTransactions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp batchRestoreResponse
	decodeResponse(t, rec, &resp)
	if resp.Restored != 2 {
		t.Errorf("Restored=%d, want 2 (only the member's own rows)", resp.Restored)
	}
	// The admin's row was refused by the ownership check, and that refusal
	// is reported rather than silently shrinking Restored — otherwise a
	// member who selected three rows and got "2 restored" has no way to
	// learn the third was skipped on purpose.
	if resp.Skipped != 1 {
		t.Errorf("Skipped=%d, want 1 (the admin's row)", resp.Skipped)
	}

	for _, tc := range []struct {
		id       int64
		wantLive bool
		label    string
	}{
		{m1.ID, true, "member row 1"},
		{m2.ID, true, "member row 2"},
		{a1.ID, false, "admin row"},
	} {
		got, err := q.GetTransactionByID(context.Background(), tc.id)
		if err != nil {
			t.Fatalf("reload %s: %v", tc.label, err)
		}
		if live := !got.DeletedAt.Valid; live != tc.wantLive {
			t.Errorf("%s: live=%v, want %v", tc.label, live, tc.wantLive)
		}
	}
}

// A member's batch that names an id which does not exist must behave like
// the someone-else's-row case: 200, the rest of the batch restored, and
// the unattributable id counted in Skipped. Both outcomes reach the same
// branch (the ownership read came back empty), and neither is an error —
// pasting a stale id from a report must not fail the whole undo.
func TestHandleBatchRestoreTransactions_MemberMissingIDIsSkippedNot500(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	member := seedTestUser(t, q, "member", "member")

	m1 := seedTombstonedTestTransaction(t, q, member.ID, 1, "2026-04-01", 10.0, "members row")
	const missing int64 = 999999

	body := fmt.Sprintf(`{"ids":[%d,%d]}`, m1.ID, missing)
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/restore-batch", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUser(req, member)
	rec := httptest.NewRecorder()
	h.handleBatchRestoreTransactions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var resp batchRestoreResponse
	decodeResponse(t, rec, &resp)
	if resp.Restored != 1 {
		t.Errorf("Restored=%d, want 1 (the member's own row)", resp.Restored)
	}
	if resp.Skipped != 1 {
		t.Errorf("Skipped=%d, want 1 (the missing id)", resp.Skipped)
	}

	got, err := q.GetTransactionByID(context.Background(), m1.ID)
	if err != nil {
		t.Fatalf("reload members row: %v", err)
	}
	if got.DeletedAt.Valid {
		t.Error("member's own row is still tombstoned — a missing id in the batch aborted the restore")
	}
}

// A real database fault while checking ownership is NOT a silent skip.
// Dropping the table makes qtx.GetTransactionByID return "no such table"
// (a non-ErrNoRows error) for every id in the batch; the handler must
// abort with 500 rather than treat "we could not read the row" as "the
// row is not yours" and quietly restore nothing.
func TestHandleBatchRestoreTransactions_MemberOwnershipReadFault_Returns500(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	member := seedTestUser(t, q, "member", "member")
	row := seedTombstonedTestTransaction(t, q, member.ID, 1, "2026-04-01", 10.0, "members row")

	// Confirm the injected fault is the one we mean: a non-ErrNoRows read
	// error. Without this the test would still pass if the read merely
	// returned no rows, which is the silent-skip case, not the fault case.
	if _, err := db.Exec("DROP TABLE transactions"); err != nil {
		t.Fatalf("drop transactions table: %v", err)
	}
	if _, err := q.GetTransactionByID(context.Background(), row.ID); err == nil {
		t.Fatal("read after DROP TABLE returned no error — fault not injected")
	} else if errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("read after DROP TABLE returned ErrNoRows, want a real DB fault: %v", err)
	}

	body := fmt.Sprintf(`{"ids":[%d]}`, row.ID)
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/restore-batch", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUser(req, member)
	rec := httptest.NewRecorder()
	h.handleBatchRestoreTransactions(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d, want 500; body=%s", rec.Code, rec.Body.String())
	}
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

// --- handleRestoreAllTransactions ---
//
// POST /api/transactions/restore-all is the "oops I emptied a whole filter"
// escape hatch — one click to flip every tombstoned row back to live. The
// handler must match the per-row restore's audit contract (one "restore"
// audit row per id) and Phase 3.3 checkpoint reverification, so the tests
// below mirror the batch-restore suite's invariants.

func TestHandleRestoreAllTransactions_RestoresEverythingInTrash(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	// Three tombstoned rows + one live row. A regression that widened the
	// underlying query would pick up the live row as well and the live
	// count would be wrong after the call.
	seedTombstonedTestTransaction(t, q, admin.ID, 1, "2026-04-01", 10.0, "a")
	seedTombstonedTestTransaction(t, q, admin.ID, 1, "2026-04-02", 20.0, "b")
	seedTombstonedTestTransaction(t, q, admin.ID, 1, "2026-04-03", 30.0, "c")
	seedTestTransaction(t, q, admin.ID, 1, "2026-04-04", 40.0, "live")

	req := httptest.NewRequest(http.MethodPost, "/api/transactions/restore-all", nil)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()
	h.handleRestoreAllTransactions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("restore-all: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp restoreAllResponse
	decodeResponse(t, rec, &resp)
	if resp.Restored != 3 {
		t.Errorf("Restored=%d, want 3", resp.Restored)
	}

	// All four rows are now live; no tombstones remain.
	if got := countTransactions(t, db); got != 4 {
		t.Errorf("live count after restore-all=%d, want 4", got)
	}
	if got := countTombstonedTransactions(t, db); got != 0 {
		t.Errorf("tombstone count after restore-all=%d, want 0", got)
	}
}

func TestHandleRestoreAllTransactions_EmptyTrash_ReturnsZero(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	// Only a live row exists — trash is empty.
	seedTestTransaction(t, q, admin.ID, 1, "2026-04-01", 10.0, "live")

	req := httptest.NewRequest(http.MethodPost, "/api/transactions/restore-all", nil)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()
	h.handleRestoreAllTransactions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("restore-all: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp restoreAllResponse
	decodeResponse(t, rec, &resp)
	if resp.Restored != 0 {
		t.Errorf("Restored=%d, want 0 (empty trash)", resp.Restored)
	}
	// An empty-trash call must write no audit rows — otherwise every
	// "peek at the trash" action in the UI would pollute the log.
	if got := countAuditRows(t, db); got != 0 {
		t.Errorf("audit rows after empty-trash restore=%d, want 0", got)
	}
}

func TestHandleRestoreAllTransactions_WritesRestoreAuditRowPerID(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	t1 := seedTombstonedTestTransaction(t, q, admin.ID, 1, "2026-04-01", 10.0, "a")
	t2 := seedTombstonedTestTransaction(t, q, admin.ID, 1, "2026-04-02", 20.0, "b")
	t3 := seedTombstonedTestTransaction(t, q, admin.ID, 1, "2026-04-03", 30.0, "c")

	// Seeding via the raw sqlc helpers bypasses the store, so the audit
	// table is empty at this point. The post-call count then equals
	// exactly the number of restores the endpoint performed.
	if got := countAuditRows(t, db); got != 0 {
		t.Fatalf("pre-call audit count=%d, want 0", got)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/transactions/restore-all", nil)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()
	h.handleRestoreAllTransactions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("restore-all: status=%d body=%s", rec.Code, rec.Body.String())
	}

	rows := listAuditRows(t, db)
	if len(rows) != 3 {
		t.Fatalf("audit row count=%d, want 3 (one per restored id)", len(rows))
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

// --- handlePurgeAllTransactions ---
//
// DELETE /api/transactions/trash is the "nuke the whole trash" escape
// hatch. Unlike restore, purge deliberately writes NO audit rows (same
// asymmetry documented on TransactionStore.Purge): the original delete
// audit rows plus the audit table's ON DELETE SET NULL FK are together
// the whole audit story. The tests below encode that invariant.

func TestHandlePurgeAllTransactions_RemovesAllTombstonedRowsPhysically(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	// Three tombstoned rows + one live row. The live row must survive.
	seedTombstonedTestTransaction(t, q, admin.ID, 1, "2026-04-01", 10.0, "a")
	seedTombstonedTestTransaction(t, q, admin.ID, 1, "2026-04-02", 20.0, "b")
	seedTombstonedTestTransaction(t, q, admin.ID, 1, "2026-04-03", 30.0, "c")
	seedTestTransaction(t, q, admin.ID, 1, "2026-04-04", 40.0, "live")

	req := httptest.NewRequest(http.MethodDelete, "/api/transactions/trash", nil)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()
	h.handlePurgeAllTransactions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("purge-all: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp purgeAllResponse
	decodeResponse(t, rec, &resp)
	if resp.Purged != 3 {
		t.Errorf("Purged=%d, want 3", resp.Purged)
	}

	// Physical count = 1 (only the live row survives). This is the
	// load-bearing assertion — purge-all is the only code path that
	// mass-removes transactions from the DB.
	if got := countAllTransactions(t, db); got != 1 {
		t.Errorf("post-purge-all physical count=%d, want 1 (only the live row)", got)
	}
	if got := countTombstonedTransactions(t, db); got != 0 {
		t.Errorf("tombstone count after purge-all=%d, want 0", got)
	}
}

func TestHandlePurgeAllTransactions_EmptyTrash_ReturnsZero(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	// Only a live row exists — trash is empty.
	seedTestTransaction(t, q, admin.ID, 1, "2026-04-01", 10.0, "live")

	req := httptest.NewRequest(http.MethodDelete, "/api/transactions/trash", nil)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()
	h.handlePurgeAllTransactions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("purge-all: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp purgeAllResponse
	decodeResponse(t, rec, &resp)
	if resp.Purged != 0 {
		t.Errorf("Purged=%d, want 0 (empty trash)", resp.Purged)
	}
	// The lone live row must still be there — purge-all touches only
	// tombstones.
	if got := countAllTransactions(t, db); got != 1 {
		t.Errorf("physical count=%d, want 1 (live row untouched)", got)
	}
}

func TestHandlePurgeAllTransactions_DoesNotWriteAuditRows(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	// Route two soft-deletes through the handler so two "delete" audit
	// rows land first — the purge-all test then asserts that purge-all
	// itself added *nothing* new. Mirror of
	// TestHandlePurgeTransaction_DoesNotWriteAuditRow for the bulk case.
	r1 := seedTestTransaction(t, q, admin.ID, 1, "2026-04-01", 10.0, "first")
	r2 := seedTestTransaction(t, q, admin.ID, 1, "2026-04-02", 20.0, "second")

	for _, id := range []int64{r1.ID, r2.ID} {
		delReq := httptest.NewRequest(http.MethodDelete, "/api/transactions/"+fmt.Sprint(id), nil)
		delReq = withUserAndURLParam(delReq, admin, "id", fmt.Sprint(id))
		delRec := httptest.NewRecorder()
		h.handleDeleteTransaction(delRec, delReq)
		if delRec.Code != http.StatusOK {
			t.Fatalf("soft-delete id=%d: status=%d body=%s", id, delRec.Code, delRec.Body.String())
		}
	}
	if got := countAuditRows(t, db); got != 2 {
		t.Fatalf("pre-purge audit count=%d, want 2 (two delete rows)", got)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/transactions/trash", nil)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()
	h.handlePurgeAllTransactions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("purge-all: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// CRITICAL: purge-all must not have added any audit row. The audit
	// table's CHECK constraint doesn't even allow a 'purge' action today,
	// so a regression that tried to write one would surface as a
	// constraint error — but we also want to defend against the opposite
	// regression of writing misnamed 'delete' rows on purge.
	if got := countAuditRows(t, db); got != 2 {
		t.Errorf("post-purge audit count=%d, want 2 (no new rows from purge-all)", got)
	}
	// Both surviving audit rows must still be the original delete rows.
	rows := listAuditRows(t, db)
	for _, r := range rows {
		if r.Action != database.AuditDelete {
			t.Errorf("surviving row action=%q, want %q", r.Action, database.AuditDelete)
		}
	}
}

// --- router-level gating ---
//
// The tests above exercise the handlers directly and set the
// auth.UserContextKey themselves, so no middleware runs. These tests
// round-trip through the router instead, which is the only place the
// route-level split is visible.
//
// Since B5 that split has two halves, and both need covering:
//
//   - Member-open, scoped inside the handler: GET /transactions/deleted,
//     POST /transactions/{id}/restore, POST /transactions/restore-batch.
//     A member must REACH these (the handler then limits her to her own
//     rows), so the test asserts the handler's own status code rather
//     than a 403.
//   - Admin-only, gated by auth.RequireAdmin at the router: POST
//     /transactions/restore-all, DELETE /transactions/{id}/purge, DELETE
//     /transactions/trash. These are the destructive and household-wide
//     operations, and a member must be stopped before the handler runs.

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
		{http.MethodPost, "/api/transactions/restore-all"},
		{http.MethodDelete, "/api/transactions/trash"},
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
	//
	// Only the destructive / household-wide trash routes stay admin-only;
	// list, single restore, and batch restore are member-reachable since
	// B5 (scoped in the handlers).
	cases := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodDelete, "/api/transactions/1/purge", `{}`},
		{http.MethodPost, "/api/transactions/restore-all", `{}`},
		{http.MethodDelete, "/api/transactions/trash", `{}`},
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

func TestNewRouter_TrashEndpoints_AsMember_OpenRoutesResolve(t *testing.T) {
	q, db := setupTestDB(t)
	router := NewRouter(q, db, nil)

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

	// The exact status codes prove the request reached the HANDLER, not a
	// route-level gate: 200 for the (empty) list and the skip-tolerant
	// batch, 404 for a restore of a row that does not exist.
	cases := []struct {
		method string
		path   string
		body   string
		want   int
	}{
		{http.MethodGet, "/api/transactions/deleted", "", http.StatusOK},
		{http.MethodPost, "/api/transactions/999999/restore", `{}`, http.StatusNotFound},
		{http.MethodPost, "/api/transactions/restore-batch", `{"ids":[999999]}`, http.StatusOK},
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
			if rec.Code != c.want {
				t.Errorf("status=%d, want %d; body=%s", rec.Code, c.want, rec.Body.String())
			}
		})
	}
}

func TestNewRouter_TrashEndpoints_AsAdmin_RoutesResolve(t *testing.T) {
	// Smoke test only: with a valid admin session the trash list routes
	// and answers. It is deliberately thin — the per-handler tests above
	// cover the response shape, and the admin-only routes are covered by
	// the member 403 test. What this pins is that the admin's own path
	// through the (member-open) list route is not broken by whatever
	// gating the member tests are exercising: the endpoint resolves and
	// returns an empty trash rather than a 403 or a 404.
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

// --- member scoping (B5) ---

func TestHandleListDeletedTransactions_MemberSeesOnlyOwnRows(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")
	member := seedTestUser(t, q, "member", "member")

	// The admin's tombstoned row carries the 999 leak-canary amount: if it
	// shows up in the member's trash, the assertion below names it.
	adminRow := seedTombstonedTestTransaction(t, q, admin.ID, 1, "2026-04-01", 999.0, "admins deleted row")
	memberRow := seedTombstonedTestTransaction(t, q, member.ID, 1, "2026-04-02", 42.0, "members deleted row")

	req := httptest.NewRequest(http.MethodGet, "/api/transactions/deleted", nil)
	req = withUser(req, member)
	rec := httptest.NewRecorder()
	h.handleListDeletedTransactions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp deletedTransactionListResponse
	decodeResponse(t, rec, &resp)
	if resp.Total != 1 {
		t.Errorf("Total=%d, want 1 (member-scoped count)", resp.Total)
	}
	if len(resp.Transactions) != 1 {
		t.Fatalf("len(Transactions)=%d, want 1", len(resp.Transactions))
	}
	if resp.Transactions[0].ID != memberRow.ID {
		t.Errorf("returned id=%d, want member's row %d", resp.Transactions[0].ID, memberRow.ID)
	}
	for _, tr := range resp.Transactions {
		if tr.ID == adminRow.ID {
			t.Errorf("admin's tombstoned row %d leaked into the member's trash", adminRow.ID)
		}
	}
}

func TestHandleListDeletedTransactions_AdminStillSeesAllRows(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")
	member := seedTestUser(t, q, "member", "member")

	seedTombstonedTestTransaction(t, q, admin.ID, 1, "2026-04-01", 10.0, "admins row")
	seedTombstonedTestTransaction(t, q, member.ID, 1, "2026-04-02", 20.0, "members row")

	req := httptest.NewRequest(http.MethodGet, "/api/transactions/deleted", nil)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()
	h.handleListDeletedTransactions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp deletedTransactionListResponse
	decodeResponse(t, rec, &resp)
	if resp.Total != 2 {
		t.Errorf("Total=%d, want 2 (admin sees the whole household's trash)", resp.Total)
	}
}

// The member-scoped and admin-scoped trash lists run two DIFFERENT queries
// (ListDeletedTransactionsByUser / ListDeletedTransactions) with two
// separate hand-maintained 17-column Scan blocks in queries.sql.go — sqlc
// codegen is broken here, so nothing regenerates them together. A field
// dropped or reordered in one Scan block but not the other would leave the
// two roles disagreeing about the same row, and every other test in this
// file asserts one field at a time on one role, so none of them would see
// it.
//
// This test seeds a single row with EVERY optional column populated,
// fetches it as its owning member and as an admin, and compares the two
// DTOs whole. The pre-flight assertions matter as much as the comparison:
// without them two Scan blocks that both dropped tags would compare equal
// and the test would pass while proving nothing.
func TestHandleListDeletedTransactions_MemberAndAdminViewsAgreeOnEveryField(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	// Display names deliberately differ from usernames: created_by carries
	// display_name, and users whose two names coincide would let a query that
	// selected u.username pass.
	admin := seedNamedUser(t, q, "admin", "Admin Name", RoleAdmin)
	member := seedNamedUser(t, q, "member", "Member Name", RoleMember)

	date, err := time.Parse("2006-01-02", "2026-04-01")
	if err != nil {
		t.Fatalf("parse date: %v", err)
	}
	row, err := q.CreateTransaction(context.Background(), database.CreateTransactionParams{
		UserID:              member.ID,
		Date:                date,
		AmountCents:         4275,
		OriginalAmountCents: sql.NullInt64{Int64: 3825000, Valid: true},
		OriginalCurrency:    sql.NullString{String: "LBP", Valid: true},
		Description:         "row with every optional column set",
		CategoryID:          1,
		Tags:                sql.NullString{String: "groceries,weekly", Valid: true},
		Notes:               sql.NullString{String: "a note that must survive both Scan blocks", Valid: true},
	})
	if err != nil {
		t.Fatalf("create transaction: %v", err)
	}
	if err := q.SoftDeleteTransaction(context.Background(), row.ID); err != nil {
		t.Fatalf("soft-delete row: %v", err)
	}

	fetch := func(u database.User) deletedTransactionResponse {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/transactions/deleted", nil)
		req = withUser(req, u)
		rec := httptest.NewRecorder()
		h.handleListDeletedTransactions(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("list as %q: status=%d body=%s", u.Role, rec.Code, rec.Body.String())
		}
		var resp deletedTransactionListResponse
		decodeResponse(t, rec, &resp)
		if len(resp.Transactions) != 1 {
			t.Fatalf("list as %q: len(Transactions)=%d, want 1", u.Role, len(resp.Transactions))
		}
		return resp.Transactions[0]
	}

	memberView := fetch(member)
	adminView := fetch(admin)

	// Every field the row carries must be populated on at least one side,
	// or the equality check below is comparing two sets of zero values.
	if memberView.ID != row.ID {
		t.Errorf("ID=%d, want %d", memberView.ID, row.ID)
	}
	if memberView.UserID != member.ID {
		t.Errorf("UserID=%d, want %d", memberView.UserID, member.ID)
	}
	if memberView.Date != "2026-04-01" {
		t.Errorf("Date=%q, want 2026-04-01", memberView.Date)
	}
	if memberView.Amount != 42.75 {
		t.Errorf("Amount=%v, want 42.75", memberView.Amount)
	}
	if memberView.OriginalAmount == nil || *memberView.OriginalAmount != 38250.0 {
		t.Errorf("OriginalAmount=%v, want 38250", memberView.OriginalAmount)
	}
	if memberView.OriginalCurrency != "LBP" {
		t.Errorf("OriginalCurrency=%q, want LBP", memberView.OriginalCurrency)
	}
	if memberView.Description != "row with every optional column set" {
		t.Errorf("Description=%q, want the seeded description", memberView.Description)
	}
	if memberView.CategoryID != 1 {
		t.Errorf("CategoryID=%d, want 1", memberView.CategoryID)
	}
	if memberView.Tags != "groceries,weekly" {
		t.Errorf("Tags=%q, want groceries,weekly", memberView.Tags)
	}
	if memberView.Notes == "" {
		t.Error("Notes is empty, want the seeded note")
	}
	if memberView.CreatedAt == "" || memberView.UpdatedAt == "" || memberView.DeletedAt == "" {
		t.Errorf("timestamps created=%q updated=%q deleted=%q, want all three populated",
			memberView.CreatedAt, memberView.UpdatedAt, memberView.DeletedAt)
	}
	if memberView.CategoryName == "" || memberView.CategoryType == "" {
		t.Errorf("category join name=%q type=%q, want both populated",
			memberView.CategoryName, memberView.CategoryType)
	}
	// The creator join is the newest column in both hand-maintained Scan
	// blocks, so it is the likeliest one to be added to a single side.
	if memberView.CreatedBy != "Member Name" {
		t.Errorf("CreatedBy=%q, want the creator's display name %q (their username is %q)",
			memberView.CreatedBy, "Member Name", "member")
	}
	// ...and its B36 companion, added to both Scan blocks at the same time. The
	// DeepEqual below catches a field added to ONE side; only this pre-flight
	// catches it being absent from BOTH, which would compare equal and pass.
	if memberView.CreatedByUsername != "member" {
		t.Errorf("CreatedByUsername=%q, want the creator's username %q (their display name is %q)",
			memberView.CreatedByUsername, "member", "Member Name")
	}

	// Same row, two queries, two Scan blocks — one DTO.
	if !reflect.DeepEqual(memberView, adminView) {
		t.Errorf("member and admin views of the same row disagree:\n member=%+v\n admin =%+v",
			memberView, adminView)
	}
}

// Money wire-edge discipline: decode into maps so a renamed/missing dollar
// field cannot hide behind a typed struct's zero-fill, and assert no
// *_cents column leaks. created_by is asserted here for the same reason —
// it is a string field the typed DTO would happily zero-fill.
func TestHandleListDeletedTransactions_MemberList_DollarFieldNoCentsLeak(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	member := seedNamedUser(t, q, "member", "Member Name", RoleMember)
	seedTombstonedTestTransaction(t, q, member.ID, 1, "2026-04-02", 42.0, "members deleted row")

	req := httptest.NewRequest(http.MethodGet, "/api/transactions/deleted", nil)
	req = withUser(req, member)
	rec := httptest.NewRecorder()
	h.handleListDeletedTransactions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	rows, ok := resp["transactions"].([]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("transactions=%v, want 1-element array", resp["transactions"])
	}
	row, ok := rows[0].(map[string]any)
	if !ok {
		t.Fatalf("row is %T, want map", rows[0])
	}
	amount, ok := row["amount"].(float64)
	if !ok {
		t.Fatalf("amount missing or not a number: %v", row["amount"])
	}
	if amount != 42.0 {
		t.Errorf("amount=%v, want 42 (dollars, not cents)", amount)
	}
	// created_by must be PRESENT (never omitempty) and carry the display name,
	// not the username: the frontend distinguishes "absent" from "unknown" by
	// the key's presence alone.
	if got := createdByOf(t, row); got != "Member Name" {
		t.Errorf("created_by=%q, want the display name %q (their username is %q)",
			got, "Member Name", "member")
	}
	// created_by_username rides the same rule: ALWAYS present, and it is the
	// field that makes the attribution unambiguous when two members share a
	// display name (B36).
	if got := createdByUsernameOf(t, row); got != "member" {
		t.Errorf("created_by_username=%q, want the username %q (their display name is %q)",
			got, "member", "Member Name")
	}
	for k := range row {
		if strings.Contains(k, "_cents") {
			t.Errorf("raw cents column %q leaked onto the wire", k)
		}
	}
}

// listDeletedRaw performs GET /transactions/deleted as the given user and
// returns the raw rows plus the reported total. Map decoding rather than the
// typed DTO is deliberate and is the repo's rule for every wire-shape test: a
// typed decode zero-fills a missing or renamed key and reports success.
func listDeletedRaw(t *testing.T, h *Handler, user database.User) ([]map[string]any, int) {
	t.Helper()
	req := withUser(httptest.NewRequest(http.MethodGet, "/api/transactions/deleted", nil), user)
	rec := httptest.NewRecorder()
	h.handleListDeletedTransactions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list deleted transactions as %q: expected 200, got %d; body: %s",
			user.Role, rec.Code, rec.Body.String())
	}
	var body struct {
		Transactions []map[string]any `json:"transactions"`
		Total        int              `json:"total"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode deleted list body: %v", err)
	}
	return body.Transactions, body.Total
}

// TestHandleListDeletedTransactions_DeletedCreatorRendersEmptyName is the
// LEFT-join pin for the trash view. transactions.user_id is NOT NULL
// REFERENCES users(id) ON DELETE CASCADE, so an orphaned row cannot arise
// while foreign keys are enforced — but a restored backup, or a connection
// that lost _foreign_keys=on, produces exactly this shape. Under an INNER
// join the row would vanish from the one surface that can still recover it:
// the money would be absent from the ledger AND from the trash, with no error
// raised anywhere. It must stay listed, with an empty creator.
//
// Both trash queries are pinned, because they are separate hand-maintained
// SQL consts with separate Scan blocks (codegen is broken here) and an inner
// join introduced into one of them survives a test that exercises only the
// other:
//   - the ADMIN path reads ListDeletedTransactions;
//   - the MEMBER path reads ListDeletedTransactionsByUser, which filters on
//     t.user_id = <the authenticated user>. Reaching a NULL join partner
//     there requires the AUTHENTICATED user to be the one whose row is
//     missing, so the second half authenticates as the ghost. That is a
//     contrived session, but it is the only row shape that reaches this
//     query's join, and it rests on the same premise as the fixture itself
//     (sessions.user_id also cascades, so a session outliving its user
//     already implies foreign keys were off).
func TestHandleListDeletedTransactions_DeletedCreatorRendersEmptyName(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedNamedUser(t, q, "elienop", "Elie", RoleAdmin)
	ghost := seedNamedUser(t, q, "ghost", "Ghost Name", RoleMember)
	catID := seedExpenseCategory(t, q, "Groceries")

	orphan := seedTombstonedTestTransaction(t, q, ghost.ID, catID, "2026-04-01", 40, "Legacy deleted row")
	adminRow := seedTombstonedTestTransaction(t, q, admin.ID, catID, "2026-04-02", 12, "Admins deleted row")

	deleteUserWithoutCascade(t, h, ghost.ID)

	// Admin path — ListDeletedTransactions. Total comes from a separate
	// COUNT that carries no users join, so an inner join here shows up as a
	// header that promises a row the list does not deliver.
	rows, total := listDeletedRaw(t, h, admin)
	if total != 2 || len(rows) != 2 {
		t.Fatalf("admin view: the orphaned row must still be listed: total=%d rows=%d, want 2 and 2",
			total, len(rows))
	}
	if got := createdByOf(t, rowByID(t, rows, orphan.ID)); got != "" {
		t.Errorf("admin view, orphaned row: created_by=%q, want the empty string", got)
	}
	if got := createdByOf(t, rowByID(t, rows, adminRow.ID)); got != "Elie" {
		t.Errorf("admin view, row with a live creator: created_by=%q, want %q", got, "Elie")
	}

	// Member path — ListDeletedTransactionsByUser, authenticated as the user
	// whose own row is gone (see the header comment).
	memberRows, memberTotal := listDeletedRaw(t, h, ghost)
	if memberTotal != 1 || len(memberRows) != 1 {
		t.Fatalf("member view: the orphaned row must still be listed: total=%d rows=%d, want 1 and 1",
			memberTotal, len(memberRows))
	}
	if got := createdByOf(t, rowByID(t, memberRows, orphan.ID)); got != "" {
		t.Errorf("member view, orphaned row: created_by=%q, want the empty string", got)
	}
}

// TestHandleListDeletedTransactions_CarriesCreatorUsername is the trash half of
// the B36 attribution contract: every row carries the creator's username beside
// their display name, because two members can hold the same display_name and a
// member cannot resolve user_id client-side (GET /api/users is admin-only).
//
// It matters more here than on the live list — restore and purge are decisions
// taken about somebody else's row — and it must be pinned on BOTH queries.
// ListDeletedTransactions (admin) and ListDeletedTransactionsByUser (member)
// are separate hand-maintained SQL consts with separate Scan blocks, because
// sqlc codegen is broken in this repo and nothing regenerates them together.
//
// The fixture gives every user a display_name and a username that differ, and
// differ between users, so a query that selected display_name twice, selected
// username twice, or swapped the two columns goes red.
func TestHandleListDeletedTransactions_CarriesCreatorUsername(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedNamedUser(t, q, "elienop", "Elie", RoleAdmin)
	member := seedNamedUser(t, q, "partner", "Partner Name", RoleMember)
	catID := seedExpenseCategory(t, q, "Groceries-"+t.Name())

	adminRow := seedTombstonedTestTransaction(t, q, admin.ID, catID, "2026-04-01", 40, "Admins deleted row")
	memberRow := seedTombstonedTestTransaction(t, q, member.ID, catID, "2026-04-02", 12, "Members deleted row")

	// Admin view — ListDeletedTransactions, household-wide.
	adminRows, adminTotal := listDeletedRaw(t, h, admin)
	if adminTotal != 2 || len(adminRows) != 2 {
		t.Fatalf("admin view: total=%d rows=%d, want 2 and 2", adminTotal, len(adminRows))
	}
	assertAttribution(t, "admin view, admin's row", rowByID(t, adminRows, adminRow.ID), "Elie", "elienop")
	assertAttribution(t, "admin view, member's row", rowByID(t, adminRows, memberRow.ID), "Partner Name", "partner")

	// Per-row, not per-request: a mutant stamping the reader's own identifier on
	// every row passes the pair above only if the two rows agree.
	if createdByUsernameOf(t, rowByID(t, adminRows, adminRow.ID)) ==
		createdByUsernameOf(t, rowByID(t, adminRows, memberRow.ID)) {
		t.Error("admin view: both rows report the same creator username; attribution is not per-row")
	}

	// Member view — ListDeletedTransactionsByUser, the OTHER const and the
	// OTHER Scan block. A column added to only one of them stops here.
	memberRows, memberTotal := listDeletedRaw(t, h, member)
	if memberTotal != 1 || len(memberRows) != 1 {
		t.Fatalf("member view: total=%d rows=%d, want 1 and 1", memberTotal, len(memberRows))
	}
	assertAttribution(t, "member view", rowByID(t, memberRows, memberRow.ID), "Partner Name", "partner")
}

// TestHandleListDeletedTransactions_DeletedCreatorRendersEmptyUsername is the
// presence half on the trash surface. An orphaned row — creator's user row gone,
// which needs a restored backup or a connection that lost _foreign_keys=on —
// must STAY LISTED with both attribution fields empty. The trash view is the
// only surface that can still recover such a row (see the rationale comment
// above ListDeletedTransactions in queries.sql): dropping it would lose the
// money from the ledger AND from its one recovery path, with no error raised.
//
// Both queries again. The member path filters on t.user_id = <the authenticated
// user>, so reaching a NULL join partner there requires authenticating AS the
// ghost — contrived, but it is the only row shape that reaches that query's
// join, and it rests on the same premise as the fixture (sessions.user_id
// cascades too, so a session outliving its user already implies foreign keys
// were off).
func TestHandleListDeletedTransactions_DeletedCreatorRendersEmptyUsername(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedNamedUser(t, q, "elienop", "Elie", RoleAdmin)
	ghost := seedNamedUser(t, q, "ghost", "Ghost Name", RoleMember)
	catID := seedExpenseCategory(t, q, "Groceries-"+t.Name())

	orphan := seedTombstonedTestTransaction(t, q, ghost.ID, catID, "2026-04-01", 40, "Legacy deleted row")
	adminRow := seedTombstonedTestTransaction(t, q, admin.ID, catID, "2026-04-02", 12, "Admins deleted row")

	deleteUserWithoutCascade(t, h, ghost.ID)

	rows, total := listDeletedRaw(t, h, admin)
	if total != 2 || len(rows) != 2 {
		t.Fatalf("admin view: the orphaned row must still be listed: total=%d rows=%d, want 2 and 2",
			total, len(rows))
	}
	orphanRow := rowByID(t, rows, orphan.ID)
	// Empty TOGETHER: one join, one missing partner.
	if got := createdByUsernameOf(t, orphanRow); got != "" {
		t.Errorf("admin view, orphaned row: created_by_username=%q, want the empty string", got)
	}
	if got := createdByOf(t, orphanRow); got != "" {
		t.Errorf("admin view, orphaned row: created_by=%q, want the empty string", got)
	}
	assertAttribution(t, "admin view, live creator", rowByID(t, rows, adminRow.ID), "Elie", "elienop")

	// Member path — ListDeletedTransactionsByUser, authenticated as the ghost.
	memberRows, memberTotal := listDeletedRaw(t, h, ghost)
	if memberTotal != 1 || len(memberRows) != 1 {
		t.Fatalf("member view: the orphaned row must still be listed: total=%d rows=%d, want 1 and 1",
			memberTotal, len(memberRows))
	}
	if got := createdByUsernameOf(t, rowByID(t, memberRows, orphan.ID)); got != "" {
		t.Errorf("member view, orphaned row: created_by_username=%q, want the empty string", got)
	}
}
