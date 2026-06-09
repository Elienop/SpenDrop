package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/elienop/spendrop/internal/database"
	"github.com/elienop/spendrop/internal/push"
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

// cancelOnSendSender cancels the supplied context the moment it is asked to
// Send, modelling the production regression where a stalled push gateway
// exhausts the per-run digest budget DURING the serial fan-out — leaving the
// outer context Done by the time the cursor write runs.
type cancelOnSendSender struct{ cancel context.CancelFunc }

func (s *cancelOnSendSender) Send(ctx context.Context, sub push.Subscription, payload []byte, opts push.Options) (bool, error) {
	s.cancel()
	return false, nil
}

// TestRunDigestTick_AdvancesCursorWhenFanOutExhaustsContext guards the real
// regression: the daily cursor write must advance even when the fan-out has
// already burned the per-run context budget. If the cursor write shares the
// exhausted context, database/sql returns DeadlineExceeded BEFORE running the
// UPDATE, last_digest_at never advances, and the digest re-fires every tick
// until recovery. The fix routes SetLastDigestAt through a context independent
// of the fan-out budget.
func TestRunDigestTick_AdvancesCursorWhenFanOutExhaustsContext(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandlerWithClock(q, db, fixedClock{t: time.Date(2026, 1, 2, 7, 30, 0, 0, time.UTC)})
	ctx, cancel := context.WithCancel(context.Background())
	h.pushTesterForBudgetAlerts = &cancelOnSendSender{cancel: cancel}

	user := seedTestUser(t, q, "alice", RoleAdmin)
	cat := seedExpenseCategory(t, q, "Groceries")
	seedPushSub(t, q, user.ID, "https://push.example/ep-cancel")
	seedExpenseRow(t, q, user.ID, cat, "2026-01-02", 1500)

	if _, err := db.ExecContext(context.Background(),
		`UPDATE notification_settings SET digest_mode='daily', digest_time='07:00', quiet_start='22:00', quiet_end='08:00', quiet_tz='UTC' WHERE id=1`); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	h.RunDigestTick(ctx)

	var advanced bool
	row := db.QueryRowContext(context.Background(),
		`SELECT last_digest_at IS NOT NULL FROM notification_settings WHERE id=1`)
	if err := row.Scan(&advanced); err != nil {
		t.Fatalf("read last_digest_at: %v", err)
	}
	if !advanced {
		t.Fatal("last_digest_at must advance even when the fan-out exhausts the context")
	}
}

func TestRunDigestTick_SendsOncePerDay(t *testing.T) {
	q, db := setupTestDB(t)
	// 07:30 UTC, 30 min past the 07:00 quiet_end boundary.
	h := NewHandlerWithClock(q, db, fixedClock{t: time.Date(2026, 1, 2, 7, 30, 0, 0, time.UTC)})
	rec := &recordingSender{}
	h.pushTesterForBudgetAlerts = rec
	ctx := context.Background()

	user := seedTestUser(t, q, "alice", RoleAdmin)
	cat := seedExpenseCategory(t, q, "Groceries")
	seedPushSub(t, q, user.ID, "https://push.example/ep-d")
	seedExpenseRow(t, q, user.ID, cat, "2026-01-02", 1500)
	seedExpenseRow(t, q, user.ID, cat, "2026-01-02", 2500)

	if _, err := db.ExecContext(ctx,
		`UPDATE notification_settings SET digest_mode='daily', digest_time='07:00', quiet_start='22:00', quiet_end='08:00', quiet_tz='UTC' WHERE id=1`); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	h.RunDigestTick(ctx)
	if rec.count() != 1 {
		t.Fatalf("first tick: want 1 digest push, got %d", rec.count())
	}
	// Second tick same day: last_digest_at now past today's boundary -> no resend.
	h.RunDigestTick(ctx)
	if rec.count() != 1 {
		t.Fatalf("second tick same day: want still 1, got %d", rec.count())
	}
}

// TestRunDigestTick_UsesOwnCollapseIdentity guards against the digest sharing
// the "activity" collapse key: the service worker rolls up tag=="activity" into
// "N new activities" and would swallow the digest. The digest must carry its own
// tag/topic ("digest") so it renders standalone with its real body.
func TestRunDigestTick_UsesOwnCollapseIdentity(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandlerWithClock(q, db, fixedClock{t: time.Date(2026, 1, 2, 7, 30, 0, 0, time.UTC)})
	rec := &recordingSender{}
	h.pushTesterForBudgetAlerts = rec
	ctx := context.Background()

	user := seedTestUser(t, q, "alice", RoleAdmin)
	cat := seedExpenseCategory(t, q, "Groceries")
	seedPushSub(t, q, user.ID, "https://push.example/ep-dt")
	seedExpenseRow(t, q, user.ID, cat, "2026-01-02", 1500)

	if _, err := db.ExecContext(ctx,
		`UPDATE notification_settings SET digest_mode='daily', digest_time='07:00', quiet_start='22:00', quiet_end='08:00', quiet_tz='UTC' WHERE id=1`); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	h.RunDigestTick(ctx)

	if rec.count() != 1 {
		t.Fatalf("want 1 digest push, got %d", rec.count())
	}
	var p pushAlertPayload
	if err := json.Unmarshal(rec.payloads[0], &p); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if p.Tag != "digest" {
		t.Errorf("payload tag = %q, want \"digest\"", p.Tag)
	}
	if p.Tag == "activity" {
		t.Error("payload tag must not be \"activity\" (SW would roll it up)")
	}
	if rec.opts[0].Topic != "digest" {
		t.Errorf("send Topic = %q, want \"digest\"", rec.opts[0].Topic)
	}
}

// TestRunDigestTick_NotSuppressedDuringQuietHours proves the user-scheduled
// digest pierces quiet hours: the fanOutPush quiet gate suppresses only activity
// types (and over_budget via its toggle), never the "digest" type. Here the clock
// (23:30) is INSIDE the 22:00->07:00 quiet window yet past the 08:00 digest_time,
// so the digest must still send.
func TestRunDigestTick_NotSuppressedDuringQuietHours(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandlerWithClock(q, db, fixedClock{t: time.Date(2026, 1, 2, 23, 30, 0, 0, time.UTC)})
	rec := &recordingSender{}
	h.pushTesterForBudgetAlerts = rec
	ctx := context.Background()

	user := seedTestUser(t, q, "alice", RoleAdmin)
	cat := seedExpenseCategory(t, q, "Groceries")
	seedPushSub(t, q, user.ID, "https://push.example/ep-quiet")
	seedExpenseRow(t, q, user.ID, cat, "2026-01-02", 1500)

	if _, err := db.ExecContext(ctx,
		`UPDATE notification_settings SET digest_mode='daily', digest_time='08:00', quiet_start='22:00', quiet_end='07:00', quiet_tz='UTC' WHERE id=1`); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	h.RunDigestTick(ctx)
	if rec.count() != 1 {
		t.Fatalf("digest must pierce quiet hours: want 1 push, got %d", rec.count())
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
	// The daily anchor is digest_time, decoupled from quiet hours: quiet_end is
	// deliberately set to a different time to prove it has no effect on firing.
	base := database.NotificationSettings{DigestMode: "daily", DigestTime: "07:00", QuietEnd: "23:00", QuietTz: "UTC"}

	// Past today's 07:00 digest_time, never digested -> fire.
	if !shouldSendDigest(time.Date(2026, 1, 2, 7, 30, 0, 0, time.UTC), base) {
		t.Error("want fire: past 07:00, never digested")
	}
	// Before today's digest_time -> skip.
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

// TestShouldSendDigest_OutOfBoxDefaultFires proves the digest works with the
// shipped defaults alone: digest_mode "daily" + the default digest_time "08:00"
// and NO quiet hours configured (quiet_end empty). Past 08:00 it must fire —
// the digest owns its own schedule and does not depend on a quiet window.
func TestShouldSendDigest_OutOfBoxDefaultFires(t *testing.T) {
	s := database.NotificationSettings{DigestMode: "daily", DigestTime: "08:00", QuietTz: "UTC"}
	if !shouldSendDigest(time.Date(2026, 1, 2, 8, 30, 0, 0, time.UTC), s) {
		t.Error("want fire: daily + default digest_time 08:00, no quiet hours")
	}
	if shouldSendDigest(time.Date(2026, 1, 2, 7, 30, 0, 0, time.UTC), s) {
		t.Error("want skip: before the 08:00 digest_time")
	}
}
