package api

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/elienop/spendrop/internal/database"
)

// TestCountTransactionsSince_HidesTombstoned seeds one live and one tombstoned
// (sentinel $999) row created after the cutoff and asserts the digest "what
// changed" count never includes the tombstoned row (soft-delete discipline).
func TestCountTransactionsSince_HidesTombstoned(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	ctx := context.Background()

	user := seedTestUser(t, q, "alice", RoleMember)
	cat := seedExpenseCategory(t, q, "Groceries")

	seedExpenseRow(t, q, user.ID, cat, "2026-01-02", 1500)           // live
	ghost := seedExpenseRow(t, q, user.ID, cat, "2026-01-02", 99900) // sentinel $999
	if err := h.txnStore.Delete(ctx, user.ID, ghost.ID); err != nil {
		t.Fatalf("tombstone ghost: %v", err)
	}

	since := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC) // before both rows
	n, err := q.CountTransactionsSince(ctx, since)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("digest count must exclude the tombstoned row: got %d want 1", n)
	}
}

func TestInQuietHours(t *testing.T) {
	cases := []struct {
		name           string
		now            time.Time
		start, end, tz string
		want           bool
	}{
		{"inside same-day window", time.Date(2026, 1, 1, 23, 30, 0, 0, time.UTC), "22:00", "23:59", "UTC", true},
		{"outside same-day window", time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), "22:00", "23:59", "UTC", false},
		{"wrap before midnight", time.Date(2026, 1, 1, 23, 0, 0, 0, time.UTC), "22:00", "07:00", "UTC", true},
		{"wrap after midnight", time.Date(2026, 1, 1, 3, 0, 0, 0, time.UTC), "22:00", "07:00", "UTC", true},
		{"wrap outside at noon", time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), "22:00", "07:00", "UTC", false},
		{"empty start disables", time.Date(2026, 1, 1, 23, 0, 0, 0, time.UTC), "", "07:00", "UTC", false},
		{"empty end disables", time.Date(2026, 1, 1, 23, 0, 0, 0, time.UTC), "22:00", "", "UTC", false},
		{"tz shifts window (NY at 03:00 UTC = 22:00 EST)", time.Date(2026, 1, 1, 3, 0, 0, 0, time.UTC), "22:00", "07:00", "America/New_York", true},
		{"bad tz falls back to UTC", time.Date(2026, 1, 1, 23, 0, 0, 0, time.UTC), "22:00", "07:00", "Not/AZone", true},
	}
	for _, tc := range cases {
		if got := inQuietHours(tc.now, tc.start, tc.end, tc.tz); got != tc.want {
			t.Errorf("%s: inQuietHours=%v want %v", tc.name, got, tc.want)
		}
	}
}

func TestShouldSendDigest(t *testing.T) {
	base := database.NotificationSettings{DigestMode: "daily", QuietEnd: "07:00", QuietTz: "UTC"}

	// Past today's 07:00 boundary, never digested -> fire.
	if !shouldSendDigest(time.Date(2026, 1, 2, 7, 30, 0, 0, time.UTC), base) {
		t.Error("want fire: past 07:00, never digested")
	}
	// Before today's boundary -> skip.
	if shouldSendDigest(time.Date(2026, 1, 2, 6, 30, 0, 0, time.UTC), base) {
		t.Error("want skip: before 07:00")
	}
	// digest_mode off -> skip.
	off := base
	off.DigestMode = "off"
	if shouldSendDigest(time.Date(2026, 1, 2, 7, 30, 0, 0, time.UTC), off) {
		t.Error("want skip: digest off")
	}
	// Already digested today at/after boundary -> skip (fires once per day).
	sent := base
	sent.LastDigestAt = sql.NullTime{Time: time.Date(2026, 1, 2, 7, 5, 0, 0, time.UTC), Valid: true}
	if shouldSendDigest(time.Date(2026, 1, 2, 8, 0, 0, 0, time.UTC), sent) {
		t.Error("want skip: already digested today")
	}
	// Last digest was yesterday -> fire again today.
	y := base
	y.LastDigestAt = sql.NullTime{Time: time.Date(2026, 1, 1, 7, 5, 0, 0, time.UTC), Valid: true}
	if !shouldSendDigest(time.Date(2026, 1, 2, 7, 30, 0, 0, time.UTC), y) {
		t.Error("want fire: last digest was yesterday")
	}
}
