package database

import (
	"context"
	"strings"
	"testing"
)

// TestMigration013_PushSubscriptions_FreshRunIsClean runs every migration
// (001→013) on a fresh DB and asserts push_subscriptions exists, the UNIQUE
// endpoint constraint is enforced, the upsert-on-endpoint target works, and
// PRAGMA foreign_key_check is empty.
func TestMigration013_PushSubscriptions_FreshRunIsClean(t *testing.T) {
	db, dbPath := openTestDB(t)
	if err := RunMigrations(db, defaultMigrationOptions(t, dbPath)); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	ctx := context.Background()

	var uid int64
	if err := db.QueryRowContext(ctx,
		`INSERT INTO users (username, password_hash, display_name, role) VALUES ('pushu', 'x', 'Push U', 'member') RETURNING id`).Scan(&uid); err != nil {
		t.Fatalf("create user: %v", err)
	}

	if _, err := db.ExecContext(ctx,
		`INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth, user_agent) VALUES (?, 'https://push.example/abc', 'p1', 'a1', 'ua')`, uid); err != nil {
		t.Fatalf("insert subscription: %v", err)
	}

	// UNIQUE(endpoint): a second bare INSERT with the same endpoint must fail.
	_, err := db.ExecContext(ctx,
		`INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth) VALUES (?, 'https://push.example/abc', 'p2', 'a2')`, uid)
	if err == nil {
		t.Error("expected UNIQUE violation on duplicate endpoint")
	} else if !strings.Contains(err.Error(), "UNIQUE constraint failed") {
		t.Errorf("expected UNIQUE violation, got: %v", err)
	}

	// ON CONFLICT(endpoint) upsert replaces the keys in place.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth) VALUES (?, 'https://push.example/abc', 'p3', 'a3')
		 ON CONFLICT(endpoint) DO UPDATE SET p256dh = excluded.p256dh, auth = excluded.auth`, uid); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	var p256 string
	if err := db.QueryRowContext(ctx,
		`SELECT p256dh FROM push_subscriptions WHERE endpoint = 'https://push.example/abc'`).Scan(&p256); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if p256 != "p3" {
		t.Errorf("p256dh after upsert: got %q want p3", p256)
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

// TestMigration013_PushSubscriptions_CascadesOnUserDelete verifies the
// ON DELETE CASCADE FK: deleting a user drops their subscriptions.
func TestMigration013_PushSubscriptions_CascadesOnUserDelete(t *testing.T) {
	db, dbPath := openTestDB(t)
	if err := RunMigrations(db, defaultMigrationOptions(t, dbPath)); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable fk: %v", err)
	}

	var uid int64
	if err := db.QueryRowContext(ctx,
		`INSERT INTO users (username, password_hash, display_name, role) VALUES ('cascadeu', 'x', 'C', 'member') RETURNING id`).Scan(&uid); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth) VALUES (?, 'https://push.example/del', 'p', 'a')`, uid); err != nil {
		t.Fatalf("insert subscription: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, uid); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM push_subscriptions WHERE user_id = ?`, uid).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 subscriptions after user delete (cascade), got %d", n)
	}
}
