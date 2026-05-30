package database

import (
	"context"
	"strings"
	"testing"
)

// TestMigration015_NotificationSettings_FreshRunIsClean runs 001→015 on a
// fresh DB and asserts notification_settings exists, the id=1 household row is
// seeded with the spec defaults, the CHECK(id=1) rejects any other id, and
// PRAGMA foreign_key_check is empty.
func TestMigration015_NotificationSettings_FreshRunIsClean(t *testing.T) {
	db, dbPath := openTestDB(t)
	if err := RunMigrations(db, defaultMigrationOptions(t, dbPath)); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	ctx := context.Background()

	// Exactly one seeded row, at id=1, with the documented defaults:
	// over_budget on; the four activity types off; large threshold 50000 cents.
	var (
		id                                                    int64
		overBudget, txnAdded, txnDeleted, txnEdited, largeTxn bool
		thresholdCents                                        int64
	)
	if err := db.QueryRowContext(ctx, `
		SELECT id, over_budget, txn_added, txn_deleted, txn_edited, large_txn, large_txn_threshold_cents
		FROM notification_settings WHERE id = 1`).
		Scan(&id, &overBudget, &txnAdded, &txnDeleted, &txnEdited, &largeTxn, &thresholdCents); err != nil {
		t.Fatalf("read seeded row: %v", err)
	}
	if id != 1 {
		t.Errorf("id: got %d want 1", id)
	}
	if !overBudget {
		t.Error("over_budget default should be on (1)")
	}
	if txnAdded || txnDeleted || txnEdited || largeTxn {
		t.Error("activity types should default off (0)")
	}
	if thresholdCents != 50000 {
		t.Errorf("large_txn_threshold_cents: got %d want 50000", thresholdCents)
	}

	// Exactly one row total — the seed must not double-insert.
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notification_settings`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("expected exactly 1 settings row, got %d", n)
	}

	// CHECK(id = 1): no second household row may exist.
	_, err := db.ExecContext(ctx,
		`INSERT INTO notification_settings (id, over_budget) VALUES (2, 1)`)
	if err == nil {
		t.Error("expected CHECK violation inserting id=2")
	} else if !strings.Contains(err.Error(), "CHECK constraint failed") {
		t.Errorf("expected CHECK violation, got: %v", err)
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
