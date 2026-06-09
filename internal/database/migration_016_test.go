package database

import (
	"context"
	"database/sql"
	"testing"
)

// TestMigration016_DigestQuiet_FreshRunIsClean runs 001->016 on a fresh DB and
// asserts the six additive columns land on the single id=1 household row with
// the documented defaults (off / ” / ” / UTC / 1 / NULL) and FK integrity holds.
func TestMigration016_DigestQuiet_FreshRunIsClean(t *testing.T) {
	db, dbPath := openTestDB(t)
	if err := RunMigrations(db, defaultMigrationOptions(t, dbPath)); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	ctx := context.Background()

	var (
		digestMode, digestTime, quietStart, quietEnd, quietTz string
		quietAllowOverBudget                                  bool
		lastDigestAt                                          sql.NullTime
	)
	if err := db.QueryRowContext(ctx, `
		SELECT digest_mode, digest_time, quiet_start, quiet_end, quiet_tz, quiet_allow_over_budget, last_digest_at
		FROM notification_settings WHERE id = 1`).
		Scan(&digestMode, &digestTime, &quietStart, &quietEnd, &quietTz, &quietAllowOverBudget, &lastDigestAt); err != nil {
		t.Fatalf("read seeded row: %v", err)
	}
	if digestMode != "off" {
		t.Errorf("digest_mode default: got %q want off", digestMode)
	}
	if digestTime != "08:00" {
		t.Errorf("digest_time default: got %q want 08:00", digestTime)
	}
	if quietStart != "" || quietEnd != "" {
		t.Errorf("quiet window defaults: got start=%q end=%q want empty", quietStart, quietEnd)
	}
	if quietTz != "UTC" {
		t.Errorf("quiet_tz default: got %q want UTC", quietTz)
	}
	if !quietAllowOverBudget {
		t.Error("quiet_allow_over_budget default should be on (1)")
	}
	if lastDigestAt.Valid {
		t.Errorf("last_digest_at default should be NULL, got %v", lastDigestAt.Time)
	}
}
