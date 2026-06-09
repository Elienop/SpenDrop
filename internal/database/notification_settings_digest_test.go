package database

import (
	"context"
	"testing"
)

// TestUpdateNotificationSettings_RoundTripsDigestQuiet asserts the hand-written
// UPDATE persists the five new digest/quiet fields and GET reads them back
// (last_digest_at is NOT part of Update — it has its own setter, T20).
func TestUpdateNotificationSettings_RoundTripsDigestQuiet(t *testing.T) {
	db, dbPath := openTestDB(t)
	if err := RunMigrations(db, defaultMigrationOptions(t, dbPath)); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	q := New(db)
	ctx := context.Background()

	if err := q.UpdateNotificationSettings(ctx, UpdateNotificationSettingsParams{
		OverBudget:             true,
		TxnAdded:               false,
		TxnDeleted:             false,
		TxnEdited:              false,
		LargeTxn:               false,
		LargeTxnThresholdCents: 50000,
		DigestMode:             "daily",
		QuietStart:             "22:00",
		QuietEnd:               "07:00",
		QuietTz:                "America/New_York",
		QuietAllowOverBudget:   false,
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	s, err := q.GetNotificationSettings(ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if s.DigestMode != "daily" || s.QuietStart != "22:00" || s.QuietEnd != "07:00" ||
		s.QuietTz != "America/New_York" || s.QuietAllowOverBudget {
		t.Fatalf("digest/quiet not round-tripped: %+v", s)
	}
}
