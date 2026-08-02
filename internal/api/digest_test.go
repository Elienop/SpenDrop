package api

import (
	"context"
	"database/sql"
	"encoding/json"
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

// cancelOnFanOutClock is a fixed clock that cancels the supplied context on
// every read AFTER the first. RunDigestTick reads the clock once for its own
// "now"; the next read is fanOutPush's quiet-hours gate, which runs after the
// transaction count and before the cursor write. That makes it a deterministic,
// synchronous seam for "the caller's context died mid-tick" — the shape the
// cursor write must survive.
type cancelOnFanOutClock struct {
	t      time.Time
	cancel context.CancelFunc
	calls  int // not mutex-guarded: every read happens on the tick's own goroutine
}

func (c *cancelOnFanOutClock) Now() time.Time {
	c.calls++
	if c.calls > 1 {
		c.cancel()
	}
	return c.t
}

// TestRunDigestTick_AdvancesCursorWhenContextCancelsDuringFanOut guards the real
// regression: the daily cursor write must advance even when the caller's context
// dies part-way through the tick (the per-run deadline expiring, or shutdown
// cancelling it). If the cursor write shares that context, database/sql returns
// the cancellation BEFORE running the UPDATE, last_digest_at never advances, and
// the digest re-fires every tick until recovery. The fix routes SetLastDigestAt
// through context.WithoutCancel.
//
// The cancellation used to be driven from the sender, which worked while the
// fan-out sent inline. Delivery is asynchronous now, so a sender-driven cancel
// would fire on another goroutine, after the cursor write, and the test would
// pass without ever exercising the hazard. The clock read inside fanOutPush's
// quiet-hours gate is the surviving synchronous seam, and the assertion on
// `calls` below fails loudly rather than going vacuous if that seam ever moves.
func TestRunDigestTick_AdvancesCursorWhenContextCancelsDuringFanOut(t *testing.T) {
	q, db := setupTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clock := &cancelOnFanOutClock{t: time.Date(2026, 1, 2, 7, 30, 0, 0, time.UTC), cancel: cancel}
	h := NewHandlerWithClock(q, db, clock)
	h.pushTesterForBudgetAlerts = &recordingSender{}

	user := seedTestUser(t, q, "alice", RoleAdmin)
	cat := seedExpenseCategory(t, q, "Groceries")
	seedPushSub(t, q, user.ID, "https://push.example/ep-cancel")
	seedExpenseRow(t, q, user.ID, cat, "2026-01-02", 1500)

	if _, err := db.ExecContext(context.Background(),
		`UPDATE notification_settings SET digest_mode='daily', digest_time='07:00', quiet_start='22:00', quiet_end='08:00', quiet_tz='UTC' WHERE id=1`); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	h.RunDigestTick(ctx)
	waitPush(t, h)

	if clock.calls < 2 {
		t.Fatalf("clock was read %d time(s): the fan-out no longer reads it, so nothing "+
			"cancelled the context and this test proves nothing", clock.calls)
	}
	if ctx.Err() == nil {
		t.Fatal("the tick's context should have been cancelled before the cursor write")
	}

	var advanced bool
	row := db.QueryRowContext(context.Background(),
		`SELECT last_digest_at IS NOT NULL FROM notification_settings WHERE id=1`)
	if err := row.Scan(&advanced); err != nil {
		t.Fatalf("read last_digest_at: %v", err)
	}
	if !advanced {
		t.Fatal("last_digest_at must advance even when the tick's context is cancelled mid-run")
	}
}

// TestRunDigestTick_NotBlockedByStalledGateway pins the other half: the digest
// ticker owns a single goroutine, so a gateway that never answers must not hold
// the tick open. Delivery is queued and the tick returns; the push is still sent
// once the gateway comes back.
func TestRunDigestTick_NotBlockedByStalledGateway(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandlerWithClock(q, db, fixedClock{t: time.Date(2026, 1, 2, 7, 30, 0, 0, time.UTC)})
	sender := newBlockingSender()
	h.pushTesterForBudgetAlerts = sender
	ctx := context.Background()

	user := seedTestUser(t, q, "alice", RoleAdmin)
	cat := seedExpenseCategory(t, q, "Groceries")
	seedPushSub(t, q, user.ID, "https://push.example/ep-stalled")
	seedExpenseRow(t, q, user.ID, cat, "2026-01-02", 1500)

	if _, err := db.ExecContext(ctx,
		`UPDATE notification_settings SET digest_mode='daily', digest_time='07:00', quiet_start='22:00', quiet_end='08:00', quiet_tz='UTC' WHERE id=1`); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	returned := make(chan struct{})
	go func() {
		h.RunDigestTick(ctx)
		close(returned)
	}()

	waitClosed(t, sender.entered, "digest never reached the push transport")
	waitClosed(t, returned, "the digest tick is still parked on an unreachable push gateway")

	close(sender.release)
	waitPush(t, h)
	if n := sender.count(); n != 1 {
		t.Fatalf("the queued digest must still be delivered; sends = %d, want 1", n)
	}
}

// digestSettings switches the daily digest on for a household, anchored at
// 07:00 so a 07:30 clock is past today's boundary.
func digestSettings(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(),
		`UPDATE notification_settings SET digest_mode='daily', digest_time='07:00', quiet_start='22:00', quiet_end='08:00', quiet_tz='UTC' WHERE id=1`); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
}

// digestCursorAdvanced reports whether last_digest_at has been written.
func digestCursorAdvanced(t *testing.T, db *sql.DB) bool {
	t.Helper()
	var advanced bool
	if err := db.QueryRowContext(context.Background(),
		`SELECT last_digest_at IS NOT NULL FROM notification_settings WHERE id=1`).Scan(&advanced); err != nil {
		t.Fatalf("read last_digest_at: %v", err)
	}
	return advanced
}

// TestRunDigestTick_HoldsCursorWhenDeliveryIsDropped is the regression guard for
// "the cursor advanced on queued, not on sent".
//
// The cursor means "the household has been told everything up to now", and
// shouldSendDigest goes false for the rest of the day once it moves. Delivery is
// asynchronous, so fanOutPush can now decline a rollup outright — and when it
// does, nothing sent it and nothing else retries it. Advancing on a declined
// rollup deletes the day's digest with no error anywhere: the tick logged
// nothing, the transport was never called, and tomorrow's tick reads a cursor
// that claims the household was already told.
func TestRunDigestTick_HoldsCursorWhenDeliveryIsDropped(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandlerWithClock(q, db, fixedClock{t: time.Date(2026, 1, 2, 7, 30, 0, 0, time.UTC)})
	sender := newBlockingSender()
	h.pushTesterForBudgetAlerts = sender
	ctx := context.Background()

	user := seedTestUser(t, q, "alice", RoleAdmin)
	cat := seedExpenseCategory(t, q, "Groceries")
	seedPushSub(t, q, user.ID, "https://push.example/ep-dropped")
	seedExpenseRow(t, q, user.ID, cat, "2026-01-02", 1500)
	digestSettings(t, db)

	// Fill every delivery slot with parked fan-outs so the digest's own emit is
	// refused before it reaches the transport.
	for i := 0; i < maxInFlightFanOuts; i++ {
		h.fanOutPush(ctx, "over_budget", []byte(`{"type":"budget_over"}`), 0, pushOpts{})
	}
	if n := h.inFlightDeliveries(); n != maxInFlightFanOuts {
		t.Fatalf("setup: in-flight = %d, want the cap %d", n, maxInFlightFanOuts)
	}

	h.RunDigestTick(ctx)

	if digestCursorAdvanced(t, db) {
		t.Fatal("last_digest_at advanced for a digest that was refused before reaching the " +
			"transport — shouldSendDigest now reads false for the rest of the day and the " +
			"rollup is silently gone")
	}

	// Freeing the slots must let the very next tick deliver it: holding the
	// cursor has to mean "retry", not "stuck".
	close(sender.release)
	waitPush(t, h)
	before := sender.count()

	h.RunDigestTick(ctx)
	waitPush(t, h)

	if got := sender.count() - before; got != 1 {
		t.Fatalf("the retry tick sent %d digests, want 1", got)
	}
	if !digestCursorAdvanced(t, db) {
		t.Fatal("last_digest_at must advance once the digest is actually queued")
	}
}

// TestRunDigestTick_AdvancesCursorWhenNothingWasOwed is the other side of the
// same gate, and the reason "advance only when queued" is the wrong rule. When
// no transaction landed in the window there is no rollup to lose, so the pass IS
// complete — a tick that refused to advance here would re-run its count query
// every minute for the rest of the day and never send anything.
func TestRunDigestTick_AdvancesCursorWhenNothingWasOwed(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandlerWithClock(q, db, fixedClock{t: time.Date(2026, 1, 2, 7, 30, 0, 0, time.UTC)})
	rec := &recordingSender{}
	h.pushTesterForBudgetAlerts = rec
	ctx := context.Background()

	user := seedTestUser(t, q, "alice", RoleAdmin)
	seedPushSub(t, q, user.ID, "https://push.example/ep-quiet-day")
	digestSettings(t, db)
	// Deliberately no transactions: CountTransactionsSince returns 0.

	h.RunDigestTick(ctx)
	waitPush(t, h)

	if rec.count() != 0 {
		t.Fatalf("nothing happened today, so nothing should be sent; got %d", rec.count())
	}
	if !digestCursorAdvanced(t, db) {
		t.Fatal("last_digest_at must advance on a pass that owed nothing, or every tick for " +
			"the rest of the day repeats the same query and the cursor never moves")
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
	waitPush(t, h)
	if rec.count() != 1 {
		t.Fatalf("first tick: want 1 digest push, got %d", rec.count())
	}
	// Second tick same day: last_digest_at now past today's boundary -> no resend.
	h.RunDigestTick(ctx)
	waitPush(t, h)
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
	waitPush(t, h)

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
	waitPush(t, h)
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
