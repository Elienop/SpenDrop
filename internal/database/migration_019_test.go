package database

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// txnFingerprint019 renders every column of the PRE-019 transactions table as
// one SQL-quoted string per row. quote() is used deliberately: it round-trips
// the value SQLite actually stored (a NULL and an empty string stay distinct,
// and a date keeps its exact text) without the driver's coercion getting a
// vote, so comparing the string before and after the rebuild is a byte-level
// claim about the copy rather than a claim about Go's scan conversions.
//
// booked_rate is absent because the pre-019 table has no such column — the
// copied rows are checked for it separately.
const txnFingerprint019 = `
SELECT id,
       quote(user_id) || '|' || quote(date) || '|' || quote(original_currency) || '|' ||
       quote(description) || '|' || quote(category_id) || '|' || quote(tags) || '|' ||
       quote(notes) || '|' || quote(created_at) || '|' || quote(updated_at) || '|' ||
       quote(deleted_at) || '|' || quote(amount_cents) || '|' ||
       quote(original_amount_cents) || '|' || quote(content_hash) || '|' ||
       quote(idempotency_key)
FROM transactions ORDER BY id`

// wantIndexes019 is every index migration 019's rebuild must put back. Six
// come from migration 010's block (originally 002, 005 and 008); the seventh,
// idx_transactions_idempotency_key, was added by 017 AFTER 010 was written and
// is the one a copy-the-old-migration rebuild silently drops.
var wantIndexes019 = []string{
	"idx_transactions_date",
	"idx_transactions_category_id",
	"idx_transactions_user_id",
	"idx_transactions_deleted_at_null",
	"idx_transactions_deleted_at",
	"idx_transactions_content_hash",
	"idx_transactions_idempotency_key",
}

func readFingerprints019(t *testing.T, db *sql.DB) map[int64]string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), txnFingerprint019)
	if err != nil {
		t.Fatalf("read transaction fingerprints: %v", err)
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var (
			id          int64
			fingerprint string
		)
		if err := rows.Scan(&id, &fingerprint); err != nil {
			t.Fatalf("scan fingerprint: %v", err)
		}
		out[id] = fingerprint
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate fingerprints: %v", err)
	}
	return out
}

// TestMigration019_Rebuild_PreservesEveryColumn seeds the pre-019 table with
// every column populated — including the two the rebuild can silently lose,
// content_hash and idempotency_key — applies 019, and asserts each row survives
// byte-for-byte with a NULL booked_rate, that all SEVEN indexes came back with
// their exact predicates, and that FK integrity is intact.
//
// The predicates are checked by BEHAVIOUR, not by name: the two partial unique
// indexes diverge on deleted_at on purpose, and a rebuild that copies one
// predicate onto the other keeps both names while breaking both guarantees.
func TestMigration019_Rebuild_PreservesEveryColumn(t *testing.T) {
	db, dbPath := openTestDB(t)
	ctx := context.Background()

	// Stand the schema up at exactly the state a real DB is in the moment
	// before 019 runs.
	applyMigrationsThrough(t, db, "018_health_write_probe.sql")

	mustExec(t, db, `INSERT INTO users (username, password_hash, display_name, role)
		VALUES ('probe', '$2a$10$fake', 'Probe', 'member')`)
	mustExec(t, db, `INSERT INTO users (username, password_hash, display_name, role)
		VALUES ('other', '$2a$10$fake', 'Other', 'member')`)
	var userID, otherID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM users WHERE username='probe'`).Scan(&userID); err != nil {
		t.Fatalf("lookup probe user: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id FROM users WHERE username='other'`).Scan(&otherID); err != nil {
		t.Fatalf("lookup other user: %v", err)
	}

	// Row 1: every nullable column populated, so a dropped name in the copy
	// list shows up as a lost value rather than as a NULL that was always NULL.
	mustExec(t, db, `INSERT INTO transactions
		(user_id, date, original_currency, description, category_id, tags, notes,
		 created_at, updated_at, deleted_at, amount_cents, original_amount_cents,
		 content_hash, idempotency_key)
		VALUES (?, '2026-04-02', 'LBP', 'taxi', 1, 'travel,work', 'airport run',
		        '2026-04-02T09:00:00Z', '2026-04-02T09:30:00Z', NULL, 1000, 89000000,
		        'hash-taxi', 'key-taxi')`, userID)
	// Row 2: tombstoned, and still holding both derived columns. Its key must
	// keep claiming the (user, key) slot forever — that is why the idempotency
	// index has no deleted_at clause.
	mustExec(t, db, `INSERT INTO transactions
		(user_id, date, description, category_id, created_at, updated_at, deleted_at,
		 amount_cents, content_hash, idempotency_key)
		VALUES (?, '2026-04-03', 'coffee', 1, '2026-04-03T08:00:00Z', '2026-04-04T08:00:00Z',
		        '2026-04-04T00:00:00Z', 500, 'hash-coffee', 'key-coffee')`, userID)
	// Row 3: LIVE and reusing row 2's content_hash — legal only because the
	// content_hash index DOES filter deleted_at IS NULL.
	mustExec(t, db, `INSERT INTO transactions
		(user_id, date, description, category_id, amount_cents, content_hash)
		VALUES (?, '2026-04-05', 'coffee again', 1, 500, 'hash-coffee')`, userID)
	// Row 4: a DIFFERENT user holding row 1's key value — legal only because
	// the idempotency index is scoped per user.
	mustExec(t, db, `INSERT INTO transactions
		(user_id, date, description, category_id, amount_cents, idempotency_key)
		VALUES (?, '2026-04-06', 'their lunch', 1, 1200, 'key-taxi')`, otherID)

	before := readFingerprints019(t, db)
	if len(before) != 4 {
		t.Fatalf("fixture should hold 4 rows, got %d", len(before))
	}

	// Apply 019 through the REAL runner, not a bare Exec: production runs each
	// file inside applyPendingMigrations' per-file transaction after a
	// pre-flight snapshot, and this test's job is to mirror the upgrade a
	// seeded database actually takes. applyMigrationsThrough bypasses the
	// runner (it never records tracking rows), so record 001..018 as applied
	// first; RunMigrations then sees exactly 019 pending.
	mustExec(t, db, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY,
		applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	for _, name := range embeddedMigrationNames(t) {
		if name <= "018_health_write_probe.sql" {
			mustExec(t, db, `INSERT INTO schema_migrations (version) VALUES (?)`, name)
		}
	}
	if err := RunMigrations(db, defaultMigrationOptions(t, dbPath)); err != nil {
		t.Fatalf("apply migration 019 via runner: %v", err)
	}

	// 1. Every row survived, byte for byte, under its original id.
	after := readFingerprints019(t, db)
	if len(after) != len(before) {
		t.Fatalf("row count changed across the rebuild: before=%d after=%d", len(before), len(after))
	}
	for id, want := range before {
		got, ok := after[id]
		if !ok {
			t.Errorf("row id=%d vanished in the rebuild", id)
			continue
		}
		if got != want {
			t.Errorf("row id=%d changed across the rebuild:\n before=%s\n  after=%s", id, want, got)
		}
	}

	// 2. The new column exists and is NULL on every copied row — no backfill,
	// lossy or otherwise.
	txCols := tableColumns(t, db, "transactions")
	if !txCols["booked_rate"] {
		t.Fatal("transactions.booked_rate must exist after the rebuild")
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM transactions WHERE booked_rate IS NOT NULL`); n != 0 {
		t.Errorf("%d copied rows carry a booked_rate; every legacy row must be NULL", n)
	}

	// 3. All seven indexes are back.
	idx := indexNames(t, db, "transactions")
	for _, want := range wantIndexes019 {
		if !idx[want] {
			t.Errorf("index %s missing after the rebuild", want)
		}
	}

	// 4. content_hash uniqueness still applies to LIVE rows only. Row 3 already
	// holds 'hash-coffee' live, so a second live row must be rejected...
	_, err := db.ExecContext(ctx, `INSERT INTO transactions
		(user_id, date, description, category_id, amount_cents, content_hash)
		VALUES (?, '2026-04-07', 'coffee dup', 1, 500, 'hash-coffee')`, userID)
	if err == nil {
		t.Error("expected a content_hash UNIQUE violation on a second LIVE row")
	} else if !strings.Contains(err.Error(), "UNIQUE constraint failed: transactions.content_hash") {
		t.Errorf("expected content_hash UNIQUE violation, got: %v", err)
	}
	// ...while a tombstoned row holding the same hash (row 2) does not block a
	// live one. Row 3 proves this survived the copy; assert it directly too.
	var tombstonedHashes int64
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM transactions WHERE content_hash='hash-coffee' AND deleted_at IS NOT NULL`).
		Scan(&tombstonedHashes); err != nil {
		t.Fatalf("count tombstoned hash rows: %v", err)
	}
	if tombstonedHashes != 1 {
		t.Errorf("expected the tombstoned 'hash-coffee' row to survive, found %d", tombstonedHashes)
	}

	// 5. The idempotency index kept its OWN predicate, which omits deleted_at:
	// row 2 is tombstoned and its key must still block a replay.
	_, err = db.ExecContext(ctx, `INSERT INTO transactions
		(user_id, date, description, category_id, amount_cents, idempotency_key)
		VALUES (?, '2026-04-08', 'coffee replay', 1, 500, 'key-coffee')`, userID)
	if err == nil {
		t.Error("expected an idempotency_key UNIQUE violation — a TOMBSTONED row must keep claiming its key")
	} else if !strings.Contains(err.Error(), "UNIQUE constraint failed: transactions.user_id, transactions.idempotency_key") {
		t.Errorf("expected idempotency_key UNIQUE violation, got: %v", err)
	}
	// ...and it is still scoped per user: rows 1 and 4 already share 'key-taxi'
	// across two users, and a third user's row may too.
	mustExec(t, db, `INSERT INTO users (username, password_hash, display_name, role)
		VALUES ('third', '$2a$10$fake', 'Third', 'member')`)
	var thirdID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM users WHERE username='third'`).Scan(&thirdID); err != nil {
		t.Fatalf("lookup third user: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO transactions
		(user_id, date, description, category_id, amount_cents, idempotency_key)
		VALUES (?, '2026-04-09', 'third lunch', 1, 1200, 'key-taxi')`, thirdID); err != nil {
		t.Errorf("a third user must be able to hold the same key value: %v", err)
	}

	// 6. FK integrity. The production migration runner does NOT run this —
	// migrate.go has no foreign_key_check anywhere, so this assertion is the
	// only guard the rebuilt table's copied FKs have.
	fkRows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	defer fkRows.Close()
	if fkRows.Next() {
		t.Error("foreign_key_check returned a row — FK integrity broken after the rebuild")
	}
}

// TestMigration019_SignedAmounts_ConstraintsOnFreshDB runs the full migration
// set and pins the new sign policy on both money columns: negatives are legal,
// zero is not, and an omitted amount still fails loudly through DEFAULT 0
// rather than persisting a silent zero row.
func TestMigration019_SignedAmounts_ConstraintsOnFreshDB(t *testing.T) {
	db, dbPath := openTestDB(t)
	if err := RunMigrations(db, defaultMigrationOptions(t, dbPath)); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	ctx := context.Background()

	mustExec(t, db, `INSERT INTO users (username, password_hash, display_name, role)
		VALUES ('probe', '$2a$10$fake', 'Probe', 'member')`)
	var userID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM users WHERE username='probe'`).Scan(&userID); err != nil {
		t.Fatalf("lookup user: %v", err)
	}

	// A refund: a negative amount on an expense row. This is the whole point of
	// the migration and no test before it could insert one.
	if _, err := db.ExecContext(ctx, `INSERT INTO transactions
		(user_id, date, description, category_id, amount_cents)
		VALUES (?, '2026-05-01', 'grocery refund', 1, -2000)`, userID); err != nil {
		t.Errorf("a negative amount_cents must be insertable: %v", err)
	}
	// A foreign-currency refund: both money columns negative and agreeing.
	if _, err := db.ExecContext(ctx, `INSERT INTO transactions
		(user_id, date, description, category_id, amount_cents, original_amount_cents, original_currency)
		VALUES (?, '2026-05-02', 'taxi refund', 1, -1000, -89000000, 'LBP')`, userID); err != nil {
		t.Errorf("a negative original_amount_cents must be insertable: %v", err)
	}

	// Zero stays illegal on both columns.
	if _, err := db.ExecContext(ctx, `INSERT INTO transactions
		(user_id, date, description, category_id, amount_cents)
		VALUES (?, '2026-05-03', 'zero', 1, 0)`, userID); err == nil {
		t.Error("expected a CHECK violation inserting amount_cents=0")
	} else if !strings.Contains(err.Error(), "CHECK constraint failed") {
		t.Errorf("expected a CHECK violation for amount_cents=0, got: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO transactions
		(user_id, date, description, category_id, amount_cents, original_amount_cents)
		VALUES (?, '2026-05-04', 'orig zero', 1, 100, 0)`, userID); err == nil {
		t.Error("expected a CHECK violation inserting original_amount_cents=0")
	} else if !strings.Contains(err.Error(), "CHECK constraint failed") {
		t.Errorf("expected a CHECK violation for original_amount_cents=0, got: %v", err)
	}
	// The reason zero must stay illegal: an INSERT that forgets the amount
	// takes DEFAULT 0, and that caller bug has to stay a hard error instead of
	// persisting a row every aggregate sums as nothing.
	if _, err := db.ExecContext(ctx, `INSERT INTO transactions
		(user_id, date, description, category_id)
		VALUES (?, '2026-05-05', 'amount omitted', 1)`, userID); err == nil {
		t.Error("expected a CHECK violation for an INSERT that omits amount_cents (DEFAULT 0)")
	} else if !strings.Contains(err.Error(), "CHECK constraint failed") {
		t.Errorf("expected a CHECK violation for an omitted amount_cents, got: %v", err)
	}

	// NULL original_amount_cents stays legal — the base-currency case.
	if _, err := db.ExecContext(ctx, `INSERT INTO transactions
		(user_id, date, description, category_id, amount_cents, original_amount_cents)
		VALUES (?, '2026-05-06', 'orig null', 1, 100, NULL)`, userID); err != nil {
		t.Errorf("NULL original_amount_cents must stay legal: %v", err)
	}

	// booked_rate is writable and nullable, and defaults to NULL.
	if _, err := db.ExecContext(ctx, `INSERT INTO transactions
		(user_id, date, description, category_id, amount_cents, original_amount_cents, original_currency, booked_rate)
		VALUES (?, '2026-05-07', 'taxi', 1, 1000, 89000000, 'LBP', 89000.0)`, userID); err != nil {
		t.Errorf("booked_rate must be writable: %v", err)
	}
	var rate sql.NullFloat64
	if err := db.QueryRowContext(ctx,
		`SELECT booked_rate FROM transactions WHERE description='taxi'`).Scan(&rate); err != nil {
		t.Fatalf("read booked_rate: %v", err)
	}
	if !rate.Valid || rate.Float64 != 89000.0 {
		t.Errorf("booked_rate = %+v, want valid 89000", rate)
	}
	if err := db.QueryRowContext(ctx,
		`SELECT booked_rate FROM transactions WHERE description='grocery refund'`).Scan(&rate); err != nil {
		t.Fatalf("read default booked_rate: %v", err)
	}
	if rate.Valid {
		t.Errorf("booked_rate must default to NULL, got %+v", rate)
	}

	// The identically-named column on category_budgets is a spending LIMIT and
	// must stay strictly positive — a relax pass keyed on the string
	// `amount_cents > 0` rather than on the table would have taken this with it.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO category_budgets (year, month, category_id, amount_cents) VALUES (2026, 5, 1, 0)`); err == nil {
		t.Error("category_budgets.amount_cents=0 must still be rejected")
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO category_budgets (year, month, category_id, amount_cents) VALUES (2026, 6, 1, -100)`); err == nil {
		t.Error("category_budgets.amount_cents=-100 must still be rejected")
	}

	// All seven indexes on a fresh DB too — a rebuild that recreates them only
	// on the migrate-from-018 path would leave a fresh install unindexed.
	idx := indexNames(t, db, "transactions")
	for _, want := range wantIndexes019 {
		if !idx[want] {
			t.Errorf("index %s missing on a freshly migrated DB", want)
		}
	}

	rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Error("foreign_key_check returned a row — FK integrity broken")
	}
}
