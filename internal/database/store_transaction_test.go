package database

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// newTestStore opens a file-backed SQLite database in t.TempDir() with FK
// enforcement enabled, runs every embedded migration, and returns the raw
// *sql.DB plus a TransactionStore + *Queries bound to it.
//
// FK enforcement is REQUIRED here:
//
//   - The Task 3 rollback test relies on FKs to trigger a SQL error mid-
//     batch. Without enforcement, an UPDATE that points category_id at a
//     nonexistent category would silently succeed (orphan FK) and the
//     test would pass for the wrong reason.
//   - TestUpdateTx_AdminBypassesOwnership relies on the
//     ownership predicate being the ONLY thing standing between actorA
//     and actorB's rows; FKs being on means a typo in the helper can't
//     accidentally paper over a missing scope check.
//
// We belt-and-suspender by setting `_foreign_keys=on` in the DSN AND
// calling `PRAGMA foreign_keys = ON` AND verifying the result. The DSN
// flag is honored by the mattn/go-sqlite3 driver but a future driver swap
// or a subtle DSN typo would silently disable it; the explicit verify
// catches that drift loudly.
//
// The migration path is the production path (RunMigrations + the embedded
// migrations FS) so the schema under test is byte-identical to what ships.
func newTestStore(t *testing.T) (*sql.DB, *TransactionStore, *Queries) {
	t.Helper()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	dsn := dbPath + "?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("ping test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Belt-and-suspenders: PRAGMA explicitly even if DSN includes it. If
	// the driver ever stops honoring the DSN flag, this still wins.
	if _, err := db.Exec(`PRAGMA foreign_keys = ON;`); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}
	var fkOn int
	if err := db.QueryRow(`PRAGMA foreign_keys;`).Scan(&fkOn); err != nil || fkOn != 1 {
		t.Fatalf("FK enforcement is OFF (got %d, err=%v) — tests would pass for the wrong reason", fkOn, err)
	}

	opts := MigrationOptions{
		DBPath:      dbPath,
		SnapshotDir: filepath.Join(dir, "snapshots"),
		BusyTimeout: 5 * time.Second,
	}
	if err := RunMigrations(db, opts); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	q := New(db)
	return db, NewTransactionStore(db, q), q
}

// seedTestStoreUser inserts a member-role user with a placeholder password
// hash and returns its ID. The name is prefixed `seedTestStore` rather
// than `seedTestUser` to avoid colliding with the api package's helper of
// the same name (different package scope but readers grep both).
func seedTestStoreUser(t *testing.T, q *Queries, name string) int64 {
	t.Helper()
	u, err := q.CreateUser(context.Background(), CreateUserParams{
		Username:     name,
		PasswordHash: "$2a$10$fakehash",
		DisplayName:  name,
		Role:         "member",
	})
	if err != nil {
		t.Fatalf("seedTestStoreUser(%q): %v", name, err)
	}
	return u.ID
}

// seedTestStoreCategory inserts an expense-type category and returns its
// ID. Used by tests that need a fresh category distinct from the seeded
// defaults so they can assert "the patch landed on the new id, not the
// pre-existing one."
func seedTestStoreCategory(t *testing.T, q *Queries, name string) int64 {
	t.Helper()
	c, err := q.CreateCategory(context.Background(), CreateCategoryParams{
		Name:      name,
		Type:      "expense",
		SortOrder: 999,
	})
	if err != nil {
		t.Fatalf("seedTestStoreCategory(%q): %v", name, err)
	}
	return c.ID
}

// seedTestStoreTransaction inserts a transaction with the given payload
// and returns its ID. The `date` parameter is a "2006-01-02" string for
// caller convenience; it is parsed to time.Time before insert because
// CreateTransactionParams.Date is time.Time.
//
// AmountCents is set via the local dollarsToCents helper from
// queries_test.go so the inserted row satisfies the same invariant
// production rows do (Phase 3.1b: cents is the only money column).
func seedTestStoreTransaction(t *testing.T, q *Queries, userID, categoryID int64, date, desc string, amount float64, tags string) int64 {
	t.Helper()
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		t.Fatalf("parse seed date %q: %v", date, err)
	}
	tt, err := q.CreateTransaction(context.Background(), CreateTransactionParams{
		UserID:      userID,
		Date:        d,
		AmountCents: dollarsToCents(amount),
		Description: desc,
		CategoryID:  categoryID,
		Tags:        sql.NullString{String: tags, Valid: tags != ""},
	})
	if err != nil {
		t.Fatalf("seedTestStoreTransaction: %v", err)
	}
	return tt.ID
}

// listStoreAuditRows returns every row in transaction_audit ordered by id.
// Raw SQL keeps the assertion explicit: tests can grep "transaction_id ==
// X && action == 'update'" without depending on sqlc-generated types that
// might gain or lose columns over time.
func listStoreAuditRows(t *testing.T, db *sql.DB) []map[string]any {
	t.Helper()
	rows, err := db.Query(`SELECT id, transaction_id, action, actor_user_id FROM transaction_audit ORDER BY id ASC`)
	if err != nil {
		t.Fatalf("listStoreAuditRows: %v", err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var (
			id, txnID int64
			action    string
			actor     sql.NullInt64
		)
		if err := rows.Scan(&id, &txnID, &action, &actor); err != nil {
			t.Fatalf("scan audit row: %v", err)
		}
		out = append(out, map[string]any{
			"id":             id,
			"transaction_id": txnID,
			"action":         action,
			"actor_user_id":  actor,
		})
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate audit rows: %v", err)
	}
	return out
}

// TestUpdateTx_HappyPath_AppliesOnlySetFields exercises the common case:
// patch a single field (CategoryID) and assert (a) the targeted field
// flipped, (b) every other field is preserved verbatim, (c) a per-row
// "update" audit row was written tied to the transaction id.
func TestUpdateTx_HappyPath_AppliesOnlySetFields(t *testing.T) {
	db, store, q := newTestStore(t)
	ctx := context.Background()

	userID := seedTestStoreUser(t, q, "alice")
	catA := seedTestStoreCategory(t, q, "CategoryA")
	catB := seedTestStoreCategory(t, q, "CategoryB")
	txnID := seedTestStoreTransaction(t, q, userID, catA, "2026-04-01", "Original", 10.0, "tag1")

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()

	newCat := catB
	patch := UpdatePatch{CategoryID: &newCat}
	before, after, err := store.UpdateTx(ctx, tx, Actor{UserID: userID}, txnID, patch)
	if err != nil {
		t.Fatalf("UpdateTx: %v", err)
	}
	if before.CategoryID != catA {
		t.Errorf("before.CategoryID: %d, want %d", before.CategoryID, catA)
	}
	if after.CategoryID != catB {
		t.Errorf("after.CategoryID: %d, want %d", after.CategoryID, catB)
	}
	if after.Description != "Original" {
		t.Errorf("description leaked: %q (should preserve)", after.Description)
	}
	if after.AmountCents != 1000 {
		t.Errorf("amount_cents leaked: %d (should preserve)", after.AmountCents)
	}
	if !after.Date.Equal(before.Date) {
		t.Errorf("date leaked: %v != %v (should preserve)", after.Date, before.Date)
	}
	if after.Tags != before.Tags {
		t.Errorf("tags leaked: %v != %v (should preserve)", after.Tags, before.Tags)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Verify a per-row audit row exists for this transaction.
	rows := listStoreAuditRows(t, db)
	found := false
	for _, r := range rows {
		if r["transaction_id"].(int64) == txnID && r["action"].(string) == "update" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected per-row update audit for txn=%d, got %v", txnID, rows)
	}
}

// TestUpdateTx_TombstonedRow_ReturnsErrTombstoned asserts that calling
// UpdateTx on a soft-deleted row returns the typed sentinel ErrTombstoned
// (NOT a generic wrapped error). This is what lets the batch handler
// distinguish "skip and continue" from a real internal-server error.
func TestUpdateTx_TombstonedRow_ReturnsErrTombstoned(t *testing.T) {
	db, store, q := newTestStore(t)
	ctx := context.Background()

	userID := seedTestStoreUser(t, q, "alice")
	catA := seedTestStoreCategory(t, q, "CategoryA")
	txnID := seedTestStoreTransaction(t, q, userID, catA, "2026-04-01", "X", 5.0, "")

	if _, err := db.ExecContext(ctx, `UPDATE transactions SET deleted_at = CURRENT_TIMESTAMP WHERE id = ?`, txnID); err != nil {
		t.Fatalf("tombstone: %v", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()

	newCat := catA
	_, _, err = store.UpdateTx(ctx, tx, Actor{UserID: userID}, txnID, UpdatePatch{CategoryID: &newCat})
	if !errors.Is(err, ErrTombstoned) {
		t.Errorf("expected ErrTombstoned, got %v", err)
	}
}

// TestUpdateTx_NonOwnedRow_ReturnsErrNotOwned asserts that a NON-ADMIN user
// trying to patch another user's row gets the typed ErrNotOwned sentinel.
// Admins bypass this check — see TestUpdateTx_AdminBypassesOwnership.
func TestUpdateTx_NonOwnedRow_ReturnsErrNotOwned(t *testing.T) {
	db, store, q := newTestStore(t)
	ctx := context.Background()

	alice := seedTestStoreUser(t, q, "alice")
	bob := seedTestStoreUser(t, q, "bob")
	cat := seedTestStoreCategory(t, q, "Cat")
	txnID := seedTestStoreTransaction(t, q, alice, cat, "2026-04-01", "X", 5.0, "")

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()

	newCat := cat
	_, _, err = store.UpdateTx(ctx, tx, Actor{UserID: bob}, txnID, UpdatePatch{CategoryID: &newCat})
	if !errors.Is(err, ErrNotOwned) {
		t.Errorf("expected ErrNotOwned, got %v", err)
	}
}

// TestUpdateTx_AdminBypassesOwnership asserts that an admin actor may patch a
// row owned by another household member. This is design spec §3.9 ("Admin
// (user.Role == RoleAdmin) bypass matches existing transaction handlers") and
// matches every sibling mutation path: single-row update's RoleAdmin check
// (transaction_handlers.go), batch-delete's `user.Role != RoleAdmin &&
// existing.UserID != user.ID` skip, and update-by-filter's omitted user_id
// predicate for admins. The batch-update path enforced strict ownership
// regardless of role, so an admin bulk-editing a mixed-owner selection
// silently skipped every row they had not personally created.
func TestUpdateTx_AdminBypassesOwnership(t *testing.T) {
	db, store, q := newTestStore(t)
	ctx := context.Background()

	alice := seedTestStoreUser(t, q, "alice")
	admin := seedTestStoreUser(t, q, "admin")
	catA := seedTestStoreCategory(t, q, "CatA")
	catB := seedTestStoreCategory(t, q, "CatB")
	txnID := seedTestStoreTransaction(t, q, alice, catA, "2026-04-01", "X", 5.0, "")

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()

	before, after, err := store.UpdateTx(ctx, tx, Actor{UserID: admin, IsAdmin: true}, txnID, UpdatePatch{CategoryID: &catB})
	if err != nil {
		t.Fatalf("admin patching another member's row: %v", err)
	}
	if before.CategoryID != catA {
		t.Errorf("before.CategoryID = %d, want %d", before.CategoryID, catA)
	}
	if after.CategoryID != catB {
		t.Errorf("after.CategoryID = %d, want %d", after.CategoryID, catB)
	}
	// An admin edit must NOT re-home the row: ownership is data, not a
	// permission artifact, and the audit trail keys off the original owner.
	if after.UserID != alice {
		t.Errorf("after.UserID = %d, want %d (admin edit must not re-home the row)", after.UserID, alice)
	}
}

// TestUpdateTx_AdminDoesNotResurrectTombstoned pins that the admin bypass
// loosens ownership ONLY: the tombstone check still rejects an admin, so an
// admin bulk edit can never silently resurrect a row the household deleted.
//
// Note it does NOT pin the relative ORDER of the tombstone and ownership
// checks — for an admin actor both orderings yield ErrTombstoned. What it
// catches is the tombstone check being weakened or removed for admins, which
// is the failure that matters.
func TestUpdateTx_AdminDoesNotResurrectTombstoned(t *testing.T) {
	db, store, q := newTestStore(t)
	ctx := context.Background()

	alice := seedTestStoreUser(t, q, "alice")
	admin := seedTestStoreUser(t, q, "admin")
	catA := seedTestStoreCategory(t, q, "CatA")
	catB := seedTestStoreCategory(t, q, "CatB")
	txnID := seedTestStoreTransaction(t, q, alice, catA, "2026-04-01", "X", 5.0, "")

	if _, err := db.ExecContext(ctx, `UPDATE transactions SET deleted_at = CURRENT_TIMESTAMP WHERE id = ?`, txnID); err != nil {
		t.Fatalf("tombstone: %v", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()

	_, _, err = store.UpdateTx(ctx, tx, Actor{UserID: admin, IsAdmin: true}, txnID, UpdatePatch{CategoryID: &catB})
	if !errors.Is(err, ErrTombstoned) {
		t.Errorf("expected ErrTombstoned for admin patching a tombstoned row, got %v", err)
	}
}

// TestUpdateTx_MissingID_ReturnsErrNotFound asserts that an id with no
// matching row returns ErrNotFound (NOT a wrapped sql.ErrNoRows). This
// is the primary distinguisher for batch handlers between "skip silently"
// and "this is a bug — bail out."
func TestUpdateTx_MissingID_ReturnsErrNotFound(t *testing.T) {
	db, store, q := newTestStore(t)
	ctx := context.Background()

	user := seedTestStoreUser(t, q, "alice")

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()

	cat := int64(1)
	_, _, err = store.UpdateTx(ctx, tx, Actor{UserID: user}, 99999, UpdatePatch{CategoryID: &cat})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// TestUpdateTx_PreservesUnsetFields is the headline correctness contract
// for the merge step: every UpdateTransactionParams field that is NOT in
// the patch must be copied verbatim from `before` so unset != wipe. This
// test patches Date alone and asserts every other field — Description,
// CategoryID, Tags, Amount — survives unchanged.
func TestUpdateTx_PreservesUnsetFields(t *testing.T) {
	db, store, q := newTestStore(t)
	ctx := context.Background()

	userID := seedTestStoreUser(t, q, "alice")
	cat := seedTestStoreCategory(t, q, "Cat")
	txnID := seedTestStoreTransaction(t, q, userID, cat, "2026-04-01", "Original Desc", 10.0, "tag1,tag2")

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()

	newDate := "2026-05-01"
	_, after, err := store.UpdateTx(ctx, tx, Actor{UserID: userID}, txnID, UpdatePatch{Date: &newDate})
	if err != nil {
		t.Fatalf("UpdateTx: %v", err)
	}
	expectedDate, _ := time.Parse("2006-01-02", newDate)
	if !after.Date.Equal(expectedDate) {
		t.Errorf("Date not updated: got %v, want %v", after.Date, expectedDate)
	}
	if after.Description != "Original Desc" {
		t.Errorf("Description leaked: %q", after.Description)
	}
	if after.CategoryID != cat {
		t.Errorf("Category leaked: %d", after.CategoryID)
	}
	if !after.Tags.Valid || after.Tags.String != "tag1,tag2" {
		t.Errorf("Tags leaked: %+v (want valid string 'tag1,tag2')", after.Tags)
	}
	if after.AmountCents != 1000 {
		t.Errorf("AmountCents leaked: %d", after.AmountCents)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}
