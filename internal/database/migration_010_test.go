package database

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// applyMigrationsThrough applies every embedded migration whose filename
// sorts at or before `last` (inclusive), in filename order, each in its own
// statement batch. It deliberately bypasses RunMigrations (and its snapshot
// machinery) so a test can stand up the schema at an *intermediate* version
// — the state a real database is in immediately before the migration under
// test runs. RunMigrations always applies the full embedded set, which would
// run 010 before the test could seed the pre-010 fixture rows.
func applyMigrationsThrough(t *testing.T, db *sql.DB, last string) {
	t.Helper()
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		if name > last {
			break
		}
		content, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		if _, err := db.Exec(string(content)); err != nil {
			t.Fatalf("apply migration %s: %v", name, err)
		}
	}
}

// readMigration returns the raw SQL text of a single embedded migration file.
func readMigration(t *testing.T, name string) string {
	t.Helper()
	content, err := migrationsFS.ReadFile("migrations/" + name)
	if err != nil {
		t.Fatalf("read migration %s: %v", name, err)
	}
	return string(content)
}

// tableColumns returns the set of column names on a table via
// PRAGMA table_info.
func tableColumns(t *testing.T, db *sql.DB, table string) map[string]bool {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatalf("table_info(%s): %v", table, err)
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan table_info(%s): %v", table, err)
		}
		cols[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate table_info(%s): %v", table, err)
	}
	return cols
}

// indexNames returns the set of index names on a table via
// PRAGMA index_list.
func indexNames(t *testing.T, db *sql.DB, table string) map[string]bool {
	t.Helper()
	rows, err := db.Query("PRAGMA index_list(" + table + ")")
	if err != nil {
		t.Fatalf("index_list(%s): %v", table, err)
	}
	defer rows.Close()
	idx := map[string]bool{}
	for rows.Next() {
		var (
			seq     int
			name    string
			unique  int
			origin  string
			partial int
		)
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			t.Fatalf("scan index_list(%s): %v", table, err)
		}
		idx[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate index_list(%s): %v", table, err)
	}
	return idx
}

// TestMigration010_DropsLegacyColumns_PreservesData drives Phase 3.1b:
// migration 010 rebuilds transactions without the legacy REAL amount /
// original_amount columns (adding CHECK constraints on the cents columns),
// and drops budgets.amount + savings_goals.target_amount. The test seeds a
// representative mix at the pre-010 schema (a normal row, a foreign-currency
// row with original_amount_cents, a tombstoned row, and two rows that
// content-hash-collide so a tombstoned + live duplicate proves the partial
// unique index survives), applies 010, then asserts column drops, data
// preservation, index survival, FK integrity, and the new CHECK rejection.
func TestMigration010_DropsLegacyColumns_PreservesData(t *testing.T) {
	db, _ := openTestDB(t)
	ctx := context.Background()

	// Stand up the pre-010 schema (001..009). 011 (api_tokens) is unrelated
	// to the transactions rebuild and is left out so the fixture exactly
	// mirrors the state a DB is in right before 010 applies.
	applyMigrationsThrough(t, db, "009_transaction_audit.sql")

	// Pristine user; category id=1 (Food) is seeded by migration 001.
	if _, err := db.ExecContext(ctx, `INSERT INTO users (username, password_hash, display_name, role)
		VALUES ('probe', '$2a$10$fake', 'Probe', 'member')`); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	var userID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM users WHERE username='probe'`).Scan(&userID); err != nil {
		t.Fatalf("lookup user: %v", err)
	}

	// Row 1: a plain domestic-currency row.
	mustExec(t, db, `INSERT INTO transactions
		(user_id, date, amount, amount_cents, description, category_id, content_hash)
		VALUES (?, '2026-04-01', 19.99, 1999, 'groceries', 1, 'hash-groceries')`, userID)
	// Row 2: a foreign-currency row with original_amount + original_amount_cents.
	mustExec(t, db, `INSERT INTO transactions
		(user_id, date, amount, amount_cents, original_amount, original_amount_cents, original_currency, description, category_id, content_hash)
		VALUES (?, '2026-04-02', 10.00, 1000, 890000.00, 89000000, 'LBP', 'taxi', 1, 'hash-taxi')`, userID)
	// Row 3: a tombstoned row carrying a content_hash — proves the soft-delete
	// column and the partial-unique index survive the rebuild.
	mustExec(t, db, `INSERT INTO transactions
		(user_id, date, amount, amount_cents, description, category_id, content_hash, deleted_at)
		VALUES (?, '2026-04-03', 5.00, 500, 'coffee', 1, 'hash-coffee', '2026-04-04T00:00:00Z')`, userID)
	// Row 4: a LIVE row sharing 'hash-coffee' with the tombstoned row 3. The
	// partial-unique index filters deleted_at IS NULL, so a live row may reuse
	// a hash a tombstoned row still holds. This must remain insertable after
	// the rebuild — and a second live row with the same hash must still be
	// rejected (asserted below).
	mustExec(t, db, `INSERT INTO transactions
		(user_id, date, amount, amount_cents, description, category_id, content_hash)
		VALUES (?, '2026-04-05', 5.00, 500, 'coffee', 1, 'hash-coffee')`, userID)

	mustExec(t, db, `INSERT INTO budgets (year, month, amount, amount_cents) VALUES (2026, 4, 2000.00, 200000)`)
	mustExec(t, db, `INSERT INTO savings_goals (year, target_amount, target_amount_cents) VALUES (2026, 12000.00, 1200000)`)

	beforeTxns := countRows(t, db, "SELECT COUNT(*) FROM transactions")

	// Apply migration 010.
	if _, err := db.ExecContext(ctx, readMigration(t, "010_amount_drop_legacy.sql")); err != nil {
		t.Fatalf("apply migration 010: %v", err)
	}

	// 1. Legacy float columns are gone.
	txCols := tableColumns(t, db, "transactions")
	if txCols["amount"] {
		t.Error("transactions.amount should be dropped")
	}
	if txCols["original_amount"] {
		t.Error("transactions.original_amount should be dropped")
	}
	if !txCols["amount_cents"] {
		t.Error("transactions.amount_cents must survive")
	}
	if !txCols["original_amount_cents"] {
		t.Error("transactions.original_amount_cents must survive")
	}
	for _, want := range []string{"deleted_at", "content_hash", "original_currency", "tags", "notes", "created_at", "updated_at"} {
		if !txCols[want] {
			t.Errorf("transactions.%s must survive the rebuild", want)
		}
	}
	if budgetCols := tableColumns(t, db, "budgets"); budgetCols["amount"] {
		t.Error("budgets.amount should be dropped")
	} else if !budgetCols["amount_cents"] {
		t.Error("budgets.amount_cents must survive")
	}
	if sgCols := tableColumns(t, db, "savings_goals"); sgCols["target_amount"] {
		t.Error("savings_goals.target_amount should be dropped")
	} else if !sgCols["target_amount_cents"] {
		t.Error("savings_goals.target_amount_cents must survive")
	}

	// 2. Row count + cents values intact.
	if afterTxns := countRows(t, db, "SELECT COUNT(*) FROM transactions"); afterTxns != beforeTxns {
		t.Errorf("transaction row count changed: before=%d after=%d", beforeTxns, afterTxns)
	}
	var grocCents int64
	if err := db.QueryRowContext(ctx, `SELECT amount_cents FROM transactions WHERE description='groceries'`).Scan(&grocCents); err != nil {
		t.Fatalf("read groceries cents: %v", err)
	}
	if grocCents != 1999 {
		t.Errorf("groceries amount_cents = %d, want 1999", grocCents)
	}
	var taxiOrig sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT original_amount_cents FROM transactions WHERE description='taxi'`).Scan(&taxiOrig); err != nil {
		t.Fatalf("read taxi original cents: %v", err)
	}
	if !taxiOrig.Valid || taxiOrig.Int64 != 89000000 {
		t.Errorf("taxi original_amount_cents = %+v, want valid 89000000", taxiOrig)
	}
	if bc := countRows(t, db, "SELECT amount_cents FROM budgets WHERE year=2026 AND month=4"); bc != 200000 {
		t.Errorf("budget amount_cents = %d, want 200000", bc)
	}
	if sc := countRows(t, db, "SELECT target_amount_cents FROM savings_goals WHERE year=2026"); sc != 1200000 {
		t.Errorf("savings target_amount_cents = %d, want 1200000", sc)
	}

	// 3. All indexes survive the rebuild, including the partial-unique
	// content_hash index.
	idx := indexNames(t, db, "transactions")
	for _, want := range []string{
		"idx_transactions_date",
		"idx_transactions_category_id",
		"idx_transactions_user_id",
		"idx_transactions_deleted_at_null",
		"idx_transactions_deleted_at",
		"idx_transactions_content_hash",
	} {
		if !idx[want] {
			t.Errorf("index %s missing after rebuild", want)
		}
	}

	// content_hash uniqueness still enforced for live rows: a SECOND live row
	// with 'hash-coffee' must be rejected (row 4 already holds it live).
	_, err := db.ExecContext(ctx, `INSERT INTO transactions
		(user_id, date, amount_cents, description, category_id, content_hash)
		VALUES (?, '2026-04-06', 500, 'coffee dup', 1, 'hash-coffee')`, userID)
	if err == nil {
		t.Error("expected UNIQUE violation inserting a second live row with hash-coffee")
	} else if !strings.Contains(err.Error(), "UNIQUE constraint failed: transactions.content_hash") {
		t.Errorf("expected content_hash UNIQUE violation, got: %v", err)
	}

	// 4. FK integrity is clean.
	fkRows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	defer fkRows.Close()
	if fkRows.Next() {
		t.Error("foreign_key_check returned a row — FK integrity broken after rebuild")
	}

	// 5. CHECK(amount_cents > 0) rejects zero and negative amounts.
	if _, err := db.ExecContext(ctx, `INSERT INTO transactions
		(user_id, date, amount_cents, description, category_id)
		VALUES (?, '2026-04-07', 0, 'zero', 1)`, userID); err == nil {
		t.Error("expected CHECK violation inserting amount_cents=0")
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO transactions
		(user_id, date, amount_cents, description, category_id)
		VALUES (?, '2026-04-08', -100, 'neg', 1)`, userID); err == nil {
		t.Error("expected CHECK violation inserting amount_cents=-100")
	}
	// CHECK on original_amount_cents rejects a non-positive foreign value but
	// allows NULL.
	if _, err := db.ExecContext(ctx, `INSERT INTO transactions
		(user_id, date, amount_cents, original_amount_cents, description, category_id)
		VALUES (?, '2026-04-09', 100, 0, 'orig-zero', 1)`, userID); err == nil {
		t.Error("expected CHECK violation inserting original_amount_cents=0")
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO transactions
		(user_id, date, amount_cents, original_amount_cents, description, category_id)
		VALUES (?, '2026-04-10', 100, NULL, 'orig-null', 1)`, userID); err != nil {
		t.Errorf("NULL original_amount_cents must be allowed by CHECK: %v", err)
	}
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func countRows(t *testing.T, db *sql.DB, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return n
}
