package database

import (
	"context"
	"strings"
	"testing"
)

// TestMigration014_BudgetAlertState_FreshRunIsClean runs 001→014 on a fresh
// DB and asserts budget_alert_state exists, the UNIQUE(category_id,year,month)
// latch is enforced, the month CHECK rejects out-of-range months, and PRAGMA
// foreign_key_check is empty.
func TestMigration014_BudgetAlertState_FreshRunIsClean(t *testing.T) {
	db, dbPath := openTestDB(t)
	if err := RunMigrations(db, defaultMigrationOptions(t, dbPath)); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	ctx := context.Background()
	const catID = 1 // migration-seeded "Food" expense category.

	if _, err := db.ExecContext(ctx,
		`INSERT INTO budget_alert_state (category_id, year, month) VALUES (?, 2026, 5)`, catID); err != nil {
		t.Fatalf("insert latch: %v", err)
	}

	// UNIQUE(category_id, year, month): the latch is set-once per cell.
	_, err := db.ExecContext(ctx,
		`INSERT INTO budget_alert_state (category_id, year, month) VALUES (?, 2026, 5)`, catID)
	if err == nil {
		t.Error("expected UNIQUE violation on duplicate (category_id, year, month)")
	} else if !strings.Contains(err.Error(), "UNIQUE constraint failed") {
		t.Errorf("expected UNIQUE violation, got: %v", err)
	}

	// CHECK(month BETWEEN 1 AND 12) rejects bad months.
	for _, bad := range []int{0, 13} {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO budget_alert_state (category_id, year, month) VALUES (?, 2027, ?)`, catID, bad); err == nil {
			t.Errorf("expected CHECK violation for month=%d", bad)
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

// TestMigration014_BudgetAlertState_CascadesOnCategoryDelete verifies the
// ON DELETE CASCADE FK: deleting a category drops its alert-state latches.
func TestMigration014_BudgetAlertState_CascadesOnCategoryDelete(t *testing.T) {
	db, dbPath := openTestDB(t)
	if err := RunMigrations(db, defaultMigrationOptions(t, dbPath)); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable fk: %v", err)
	}

	var catID int64
	if err := db.QueryRowContext(ctx,
		`INSERT INTO categories (name, type, sort_order) VALUES ('AlertCascade', 'expense', 99) RETURNING id`).Scan(&catID); err != nil {
		t.Fatalf("create category: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO budget_alert_state (category_id, year, month) VALUES (?, 2026, 5)`, catID); err != nil {
		t.Fatalf("insert latch: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM categories WHERE id = ?`, catID); err != nil {
		t.Fatalf("delete category: %v", err)
	}

	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM budget_alert_state WHERE category_id = ?`, catID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 latches after category delete (cascade), got %d", n)
	}
}
