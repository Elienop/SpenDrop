package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elienop/spendrop/internal/database"
)

// Checkpoint-hook coverage for the two bulk-restore paths
// (handleBatchRestoreTransactions and handleRestoreAllTransactions).
//
// Both used to hand verifyAffectedCheckpoints a zero time.Time, which means
// "re-verify EVERY checkpoint": correct but unbounded, so the cost of undoing
// three rows grew with the size of the checkpoint table instead of with the
// batch. Both now accumulate the earliest date among the rows they actually
// restored and walk from there.
//
// The gate matrix each handler needs, mirroring the batch-update tests in
// checkpoint_handlers_test.go:
//
//   1. a category-scoped "fires" test and a tag-scoped one — restoring a row
//      moves a scoped total with no date change, and the walk has to reach it;
//   2. a date-floor negative test — a checkpoint dated before every restored
//      row must NOT be walked. Seeded inverted (stored 'mismatch' while the
//      real total matches) so a stray walk would CHANGE the row; an
//      "ok stays ok" assertion would pass with the floor deleted;
//   3. a degrade test — a restored row that supplies no usable date must widen
//      the walk back to everything, never narrow it to the rows that did.
//
// Every "fires" fixture seeds the checkpoint so its stored 'ok' is TRUE while
// the row sits in the trash (expected == the tombstoned total), so it is the
// restore, not the fixture, that makes the stored status wrong.

// seedTombstonedTestTransactionWithTags is the tags-carrying twin of
// seedTombstonedTestTransaction. The tag-scoped tests need it because
// VerifyCheckpointTotal matches a tag scope with a LIKE over t.tags, so the
// restored row has to carry the tag for the scoped total to move at all.
func seedTombstonedTestTransactionWithTags(t *testing.T, q *database.Queries, userID, categoryID int64, date string, amount float64, desc, tags string) database.Transaction {
	t.Helper()
	txn := seedTestTransactionWithTags(t, q, userID, categoryID, date, amount, desc, tags)
	if err := q.SoftDeleteTransaction(context.Background(), txn.ID); err != nil {
		t.Fatalf("soft-delete seeded tagged transaction: %v", err)
	}
	return txn
}

func postRestoreBatch(t *testing.T, h *Handler, user database.User, ids ...int64) *httptest.ResponseRecorder {
	t.Helper()
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, fmt.Sprint(id))
	}
	body := fmt.Sprintf(`{"ids":[%s]}`, strings.Join(parts, ","))
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/restore-batch", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleBatchRestoreTransactions(rec, req)
	return rec
}

func postRestoreAll(t *testing.T, h *Handler, user database.User) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/restore-all", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleRestoreAllTransactions(rec, req)
	return rec
}

// assertCheckpointStatus reads last_verification_status back off the row.
// why is appended to the failure so a red test says which half of the gate
// matrix broke rather than just naming two status strings.
func assertCheckpointStatus(t *testing.T, q *database.Queries, id int64, want, why string) {
	t.Helper()
	got, err := q.GetCheckpoint(context.Background(), id)
	if err != nil {
		t.Fatalf("GetCheckpoint(%d): %v", id, err)
	}
	if !got.LastVerificationStatus.Valid || got.LastVerificationStatus.String != want {
		t.Errorf("checkpoint %d status=%v, want %q — %s", id, got.LastVerificationStatus, want, why)
	}
}

// mustRestoredCount decodes a bulk-restore response and asserts the row count,
// so a fixture that silently restored nothing fails as a fixture problem
// instead of masquerading as "the hook did not fire".
func mustRestoredCount(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Restored int `json:"restored"`
	}
	decodeResponse(t, rec, &resp)
	if resp.Restored != want {
		t.Fatalf("Restored=%d, want %d; body=%s", resp.Restored, want, rec.Body.String())
	}
}

// --- handleBatchRestoreTransactions ---

func TestHandleBatchRestore_CategoryScopedCheckpoint_FiresCheckpointHook(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	member := seedTestUser(t, q, "member_brcat", "member")
	cat := seedTestCategory(t, q, "BatchRestoreHookCat", "expense")

	row := seedTombstonedTestTransaction(t, q, member.ID, cat.ID, "2026-04-01", 50.00, "undo me")
	// Expected $0 is the truth while the row is tombstoned, so the stored
	// 'ok' below is honest until the restore lands.
	cp := seedCheckpoint(t, q, member.ID, "category", cat.ID, "", "2026-04-30", 0)
	mustUpdateCheckpointStatus(t, db, cp.ID, "ok")

	mustRestoredCount(t, postRestoreBatch(t, h, member, row.ID), 1)

	assertCheckpointStatus(t, q, cp.ID, "mismatch",
		"the restore put $50 back into this category, so the scoped total no longer matches the expected $0")
}

func TestHandleBatchRestore_TagScopedCheckpoint_FiresCheckpointHook(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	member := seedTestUser(t, q, "member_brtag", "member")
	cat := seedTestCategory(t, q, "BatchRestoreHookTagCat", "expense")

	row := seedTombstonedTestTransactionWithTags(t, q, member.ID, cat.ID, "2026-04-01", 50.00, "undo me", "holiday")
	cp := seedCheckpoint(t, q, member.ID, "tag", 0, "holiday", "2026-04-30", 0)
	mustUpdateCheckpointStatus(t, db, cp.ID, "ok")

	mustRestoredCount(t, postRestoreBatch(t, h, member, row.ID), 1)

	assertCheckpointStatus(t, q, cp.ID, "mismatch",
		"the restored row carries the 'holiday' tag, so the tag-scoped total moved to $50 against an expected $0")
}

// TestHandleBatchRestore_CheckpointBeforeEarliestRestoredDate_NotWalked is the
// bound itself: a checkpoint dated before every restored row sums none of them
// and must be left alone.
//
// All three fixtures are load-bearing. The early checkpoint is seeded inverted
// — stored 'mismatch' while its real total matches — so a walk would CORRECT
// it to 'ok' and the assertion catches the walk. The mid checkpoint sits
// between the two restored rows, so it proves the hook fired AND that the
// floor is the EARLIEST restored date: take the last row's date, or the
// latest, and the April row's cents land in a checkpoint that never gets
// re-verified.
func TestHandleBatchRestore_CheckpointBeforeEarliestRestoredDate_NotWalked(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	member := seedTestUser(t, q, "member_brfloor", "member")
	cat := seedTestCategory(t, q, "BatchRestoreFloorCat", "expense")

	// A live row in January that no restore touches, so the early checkpoint
	// has a real total to be right about.
	seedTestTransaction(t, q, member.ID, cat.ID, "2026-01-15", 10.00, "january, still live")
	april := seedTombstonedTestTransaction(t, q, member.ID, cat.ID, "2026-04-01", 50.00, "undo me")
	june := seedTombstonedTestTransaction(t, q, member.ID, cat.ID, "2026-06-01", 70.00, "undo me too")

	early := seedCheckpoint(t, q, member.ID, "total", 0, "", "2026-01-31", 1000)
	mustUpdateCheckpointStatus(t, db, early.ID, "mismatch")
	mid := seedCheckpoint(t, q, member.ID, "total", 0, "", "2026-05-15", 1000)
	mustUpdateCheckpointStatus(t, db, mid.ID, "ok")

	mustRestoredCount(t, postRestoreBatch(t, h, member, april.ID, june.ID), 2)

	assertCheckpointStatus(t, q, early.ID, "mismatch",
		"a checkpoint dated before every restored row was re-verified — the walk lost its date floor and fell back to visiting every checkpoint")
	assertCheckpointStatus(t, q, mid.ID, "mismatch",
		"the checkpoint between the two restored rows was not re-verified — the floor is not the earliest restored date (or the hook did not fire at all)")
}

// TestHandleBatchRestore_OnlyRowWithoutUsableDate_StillWalks pins the liveness
// gate, which CLAUDE.md flags as the thing not to "unify" across bulk paths.
// The gate is `restored > 0`, not `!minRestoredDate.IsZero()`: this loop can
// restore a row that never contributes a date, and here that is the ONLY row,
// so a date-derived gate would skip the hook entirely — the exact failure the
// unbounded fallback exists to prevent.
func TestHandleBatchRestore_OnlyRowWithoutUsableDate_StillWalks(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	member := seedTestUser(t, q, "member_brgate", "member")
	cat := seedTestCategory(t, q, "BatchRestoreGateCat", "expense")

	zeroDated := seedTombstonedTestTransaction(t, q, member.ID, cat.ID, "0001-01-01", 40.00, "undated")
	assertStoredDateIsZero(t, q, zeroDated.ID)

	cp := seedCheckpoint(t, q, member.ID, "total", 0, "", "2020-06-30", 0)
	mustUpdateCheckpointStatus(t, db, cp.ID, "ok")

	mustRestoredCount(t, postRestoreBatch(t, h, member, zeroDated.ID), 1)

	assertCheckpointStatus(t, q, cp.ID, "mismatch",
		"the only restored row supplied no date, and the hook was skipped instead of walking every checkpoint")
}

// TestHandleBatchRestore_RestoredRowWithoutUsableDate_WalksEveryCheckpoint
// pins the fallback direction. earliestDate treats the zero time.Time as
// "unset", so a restored row carrying it cannot lower the floor — fold it in
// naively and the floor lands on some LATER row's date, silently dropping
// every checkpoint in between. The handler degrades the whole request to the
// unbounded walk instead.
//
// The zero date is the reachable half of that condition; the other half (a
// restored row whose in-tx read failed, which only an admin can reach) shares
// the same branch and the same consequence, but cannot be provoked from a test
// because RestoreTx re-runs the identical read on the identical tx one
// statement later.
func TestHandleBatchRestore_RestoredRowWithoutUsableDate_WalksEveryCheckpoint(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	member := seedTestUser(t, q, "member_brnodate", "member")
	cat := seedTestCategory(t, q, "BatchRestoreNoDateCat", "expense")

	// 0001-01-01 parses to exactly time.Time{}, the value earliestDate reads
	// as "no date supplied".
	zeroDated := seedTombstonedTestTransaction(t, q, member.ID, cat.ID, "0001-01-01", 40.00, "undated")
	assertStoredDateIsZero(t, q, zeroDated.ID)
	late := seedTombstonedTestTransaction(t, q, member.ID, cat.ID, "2026-04-01", 50.00, "undo me too")

	// Dated between the two restored rows: it sums the zero-dated row and
	// nothing else, and expected $0 is the truth while both sit in the trash.
	cp := seedCheckpoint(t, q, member.ID, "total", 0, "", "2020-06-30", 0)
	mustUpdateCheckpointStatus(t, db, cp.ID, "ok")

	mustRestoredCount(t, postRestoreBatch(t, h, member, zeroDated.ID, late.ID), 2)

	assertCheckpointStatus(t, q, cp.ID, "mismatch",
		"a checkpoint the zero-dated restored row moves was skipped — the floor was narrowed to the other row's date instead of degrading to the unbounded walk")
}

// --- handleRestoreAllTransactions ---

func TestHandleRestoreAll_CategoryScopedCheckpoint_FiresCheckpointHook(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin_racat", "admin")
	cat := seedTestCategory(t, q, "RestoreAllHookCat", "expense")

	seedTombstonedTestTransaction(t, q, admin.ID, cat.ID, "2026-04-01", 50.00, "undo me")
	cp := seedCheckpoint(t, q, admin.ID, "category", cat.ID, "", "2026-04-30", 0)
	mustUpdateCheckpointStatus(t, db, cp.ID, "ok")

	mustRestoredCount(t, postRestoreAll(t, h, admin), 1)

	assertCheckpointStatus(t, q, cp.ID, "mismatch",
		"restore-all put $50 back into this category, so the scoped total no longer matches the expected $0")
}

func TestHandleRestoreAll_TagScopedCheckpoint_FiresCheckpointHook(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin_ratag", "admin")
	cat := seedTestCategory(t, q, "RestoreAllHookTagCat", "expense")

	seedTombstonedTestTransactionWithTags(t, q, admin.ID, cat.ID, "2026-04-01", 50.00, "undo me", "holiday")
	cp := seedCheckpoint(t, q, admin.ID, "tag", 0, "holiday", "2026-04-30", 0)
	mustUpdateCheckpointStatus(t, db, cp.ID, "ok")

	mustRestoredCount(t, postRestoreAll(t, h, admin), 1)

	assertCheckpointStatus(t, q, cp.ID, "mismatch",
		"the restored row carries the 'holiday' tag, so the tag-scoped total moved to $50 against an expected $0")
}

// Restore-all twin of the batch date-floor bound. Same inverted seeding on the
// early checkpoint, same mid checkpoint pinning both liveness and the
// earliest-of-all-restored-rows floor.
func TestHandleRestoreAll_CheckpointBeforeEarliestRestoredDate_NotWalked(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin_rafloor", "admin")
	cat := seedTestCategory(t, q, "RestoreAllFloorCat", "expense")

	seedTestTransaction(t, q, admin.ID, cat.ID, "2026-01-15", 10.00, "january, still live")
	seedTombstonedTestTransaction(t, q, admin.ID, cat.ID, "2026-04-01", 50.00, "undo me")
	seedTombstonedTestTransaction(t, q, admin.ID, cat.ID, "2026-06-01", 70.00, "undo me too")

	early := seedCheckpoint(t, q, admin.ID, "total", 0, "", "2026-01-31", 1000)
	mustUpdateCheckpointStatus(t, db, early.ID, "mismatch")
	mid := seedCheckpoint(t, q, admin.ID, "total", 0, "", "2026-05-15", 1000)
	mustUpdateCheckpointStatus(t, db, mid.ID, "ok")

	mustRestoredCount(t, postRestoreAll(t, h, admin), 2)

	assertCheckpointStatus(t, q, early.ID, "mismatch",
		"a checkpoint dated before every restored row was re-verified — the walk lost its date floor and fell back to visiting every checkpoint")
	assertCheckpointStatus(t, q, mid.ID, "mismatch",
		"the checkpoint between the two restored rows was not re-verified — the floor is not the earliest restored date (or the hook did not fire at all)")
}

// Restore-all twin of the liveness-gate test: `restored > 0` decides whether
// to walk, never the accumulated date.
func TestHandleRestoreAll_OnlyRowWithoutUsableDate_StillWalks(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin_ragate", "admin")
	cat := seedTestCategory(t, q, "RestoreAllGateCat", "expense")

	zeroDated := seedTombstonedTestTransaction(t, q, admin.ID, cat.ID, "0001-01-01", 40.00, "undated")
	assertStoredDateIsZero(t, q, zeroDated.ID)

	cp := seedCheckpoint(t, q, admin.ID, "total", 0, "", "2020-06-30", 0)
	mustUpdateCheckpointStatus(t, db, cp.ID, "ok")

	mustRestoredCount(t, postRestoreAll(t, h, admin), 1)

	assertCheckpointStatus(t, q, cp.ID, "mismatch",
		"the only restored row supplied no date, and the hook was skipped instead of walking every checkpoint")
}

// Restore-all twin of the degrade test. The route lives inside the
// auth.RequireAdmin group (router.go), so a non-admin caller cannot reach this
// handler and it needs no ownership branch — a failed per-id read goes straight
// to RestoreTx. Only the zero-date half of the fallback is reachable from a
// test; the unreadable-row half has no seam that produces it here.
func TestHandleRestoreAll_RestoredRowWithoutUsableDate_WalksEveryCheckpoint(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin_ranodate", "admin")
	cat := seedTestCategory(t, q, "RestoreAllNoDateCat", "expense")

	zeroDated := seedTombstonedTestTransaction(t, q, admin.ID, cat.ID, "0001-01-01", 40.00, "undated")
	assertStoredDateIsZero(t, q, zeroDated.ID)
	seedTombstonedTestTransaction(t, q, admin.ID, cat.ID, "2026-04-01", 50.00, "undo me too")

	cp := seedCheckpoint(t, q, admin.ID, "total", 0, "", "2020-06-30", 0)
	mustUpdateCheckpointStatus(t, db, cp.ID, "ok")

	mustRestoredCount(t, postRestoreAll(t, h, admin), 2)

	assertCheckpointStatus(t, q, cp.ID, "mismatch",
		"a checkpoint the zero-dated restored row moves was skipped — the floor was narrowed to the other row's date instead of degrading to the unbounded walk")
}

// assertStoredDateIsZero fails if the seeded row did not round-trip through
// SQLite as the zero time.Time. Without it the degrade tests would quietly
// stop testing the degrade the day the driver or the seeder normalized
// 0001-01-01 into anything else.
func assertStoredDateIsZero(t *testing.T, q *database.Queries, id int64) {
	t.Helper()
	row, err := q.GetTransactionByID(context.Background(), id)
	if err != nil {
		t.Fatalf("reload zero-dated row %d: %v", id, err)
	}
	if !row.Date.IsZero() {
		t.Fatalf("seeded row %d stored date=%v, want the zero time — the fixture no longer exercises the no-usable-date path", id, row.Date)
	}
}
