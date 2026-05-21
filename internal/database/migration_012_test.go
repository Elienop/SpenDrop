package database

import (
	"context"
	"strings"
	"testing"
)

// TestMigration012_CategoryBudgets_FreshRunIsClean runs every migration
// (001→012) on a fresh DB and asserts the category_budgets table exists, the
// UNIQUE upsert target works, the CHECK(amount_cents > 0) rejects non-positive
// amounts, and PRAGMA foreign_key_check is empty.
func TestMigration012_CategoryBudgets_FreshRunIsClean(t *testing.T) {
	db, dbPath := openTestDB(t)
	if err := RunMigrations(db, defaultMigrationOptions(t, dbPath)); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	ctx := context.Background()

	// category_id=1 is the migration-seeded "Food" expense category.
	const catID = 1

	// Insert a limit.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO category_budgets (year, month, category_id, amount_cents) VALUES (2026, 4, ?, 25000)`, catID); err != nil {
		t.Fatalf("insert limit: %v", err)
	}

	// UNIQUE(year, month, category_id): a second bare INSERT for the same key
	// must fail.
	_, err := db.ExecContext(ctx,
		`INSERT INTO category_budgets (year, month, category_id, amount_cents) VALUES (2026, 4, ?, 99999)`, catID)
	if err == nil {
		t.Error("expected UNIQUE violation on duplicate (year, month, category_id)")
	} else if !strings.Contains(err.Error(), "UNIQUE constraint failed") {
		t.Errorf("expected UNIQUE violation, got: %v", err)
	}

	// ON CONFLICT upsert replaces the amount in place.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO category_budgets (year, month, category_id, amount_cents) VALUES (2026, 4, ?, 30000)
		 ON CONFLICT(year, month, category_id) DO UPDATE SET amount_cents = excluded.amount_cents`, catID); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	var amt int64
	if err := db.QueryRowContext(ctx,
		`SELECT amount_cents FROM category_budgets WHERE year=2026 AND month=4 AND category_id=?`, catID).Scan(&amt); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if amt != 30000 {
		t.Errorf("amount_cents after upsert: got %d want 30000", amt)
	}

	// CHECK(amount_cents > 0) rejects zero and negative.
	for _, bad := range []int64{0, -1} {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO category_budgets (year, month, category_id, amount_cents) VALUES (2027, 1, ?, ?)`, catID, bad); err == nil {
			t.Errorf("expected CHECK violation for amount_cents=%d", bad)
		}
	}

	// FK integrity is clean.
	rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Error("foreign_key_check returned a row — FK integrity broken")
	}
}

// TestMigration012_CategoryBudgets_CascadesOnCategoryDelete verifies the
// ON DELETE CASCADE FK: deleting a category drops its limits.
func TestMigration012_CategoryBudgets_CascadesOnCategoryDelete(t *testing.T) {
	db, dbPath := openTestDB(t)
	if err := RunMigrations(db, defaultMigrationOptions(t, dbPath)); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	ctx := context.Background()

	// Enable FK enforcement on this connection so the cascade fires.
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable fk: %v", err)
	}

	var catID int64
	if err := db.QueryRowContext(ctx,
		`INSERT INTO categories (name, type, sort_order) VALUES ('CascadeTest', 'expense', 99) RETURNING id`).Scan(&catID); err != nil {
		t.Fatalf("create category: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO category_budgets (year, month, category_id, amount_cents) VALUES (2026, 4, ?, 1000)`, catID); err != nil {
		t.Fatalf("insert limit: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM categories WHERE id = ?`, catID); err != nil {
		t.Fatalf("delete category: %v", err)
	}

	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM category_budgets WHERE category_id = ?`, catID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 limits after category delete (cascade), got %d", n)
	}
}
