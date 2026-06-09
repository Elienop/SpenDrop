package api

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/elienop/spendrop/internal/database"
	"github.com/elienop/spendrop/internal/push"
)

// recordingSender captures every Send call so a test can assert fan-out
// count and decode the payload. It satisfies the same Send signature the
// real *push.Sender exposes; Handler.pushSender is the concrete *push.Sender,
// so evaluateBudgetAlerts must reach the sender through an interface seam
// (pushDispatcher) that both *push.Sender and this fake implement.
type recordingSender struct {
	mu       sync.Mutex
	payloads [][]byte
	opts     []push.Options // transport opts (Topic/Urgency) per Send — asserted by T05/T24
	prune    bool           // when true, every Send reports prune
	err      error
}

func (s *recordingSender) Send(ctx context.Context, sub push.Subscription, payload []byte, opts push.Options) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]byte, len(payload))
	copy(cp, payload)
	s.payloads = append(s.payloads, cp)
	s.opts = append(s.opts, opts)
	return s.prune, s.err
}

func (s *recordingSender) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.payloads)
}

func TestPushOptionsFor(t *testing.T) {
	for _, tc := range []struct {
		notifType   string
		wantTag     string
		wantTopic   string
		wantUrgency push.Urgency
	}{
		{"over_budget", "budget", "ob", push.UrgencyNormal},
		{"txn_added", "activity", "act", push.UrgencyLow},
		{"txn_edited", "activity", "act", push.UrgencyLow},
		{"txn_deleted", "activity", "act", push.UrgencyLow},
		{"large_txn", "activity", "act", push.UrgencyLow},
		{"mystery", "", "", push.UrgencyLow},
	} {
		tag, topic, urgency := pushOptionsFor(tc.notifType)
		if tag != tc.wantTag || topic != tc.wantTopic || urgency != tc.wantUrgency {
			t.Errorf("pushOptionsFor(%q) = (%q,%q,%q), want (%q,%q,%q)",
				tc.notifType, tag, topic, urgency, tc.wantTag, tc.wantTopic, tc.wantUrgency)
		}
	}
}

func TestEmit_ActivityCarriesTagTopicUrgency(t *testing.T) {
	q, db := setupTestDB(t)
	rec := &recordingSender{}
	h := NewHandler(q, db)
	h.pushTesterForBudgetAlerts = rec

	user := seedTestUser(t, q, "alice", RoleMember)
	seedPushSub(t, q, user.ID, "https://push.example/act")
	enableNotif(t, q, func(p *database.UpdateNotificationSettingsParams) { p.TxnAdded = true })

	h.emit(context.Background(), "txn_added", "Transaction added", "$1.00 in X — y", "/transactions", 0)

	if rec.count() != 1 {
		t.Fatalf("want 1 send, got %d", rec.count())
	}
	var p pushAlertPayload
	if err := json.Unmarshal(rec.payloads[0], &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.Tag != "activity" {
		t.Errorf("payload tag: got %q want activity", p.Tag)
	}
	if rec.opts[0].Topic != "act" {
		t.Errorf("topic: got %q want act", rec.opts[0].Topic)
	}
	if rec.opts[0].Urgency != push.UrgencyLow {
		t.Errorf("urgency: got %q want low", rec.opts[0].Urgency)
	}
}

// seedPushSub inserts one subscription row for userID via the same query the
// production code uses, so the fan-out reads it back through ListAllPushSubscriptions.
func seedPushSub(t *testing.T, q *database.Queries, userID int64, endpoint string) {
	t.Helper()
	if err := q.UpsertPushSubscription(context.Background(), database.UpsertPushSubscriptionParams{
		UserID:    userID,
		Endpoint:  endpoint,
		P256dh:    "p256dh-" + endpoint,
		Auth:      "auth-" + endpoint,
		UserAgent: toNullString("test-agent"),
	}); err != nil {
		t.Fatalf("seed push sub: %v", err)
	}
}

func TestFanOutPush_NoOpWhenTypeDisabled(t *testing.T) {
	q, db := setupTestDB(t)
	rec := &recordingSender{}
	h := NewHandler(q, db)
	h.pushTesterForBudgetAlerts = rec

	user := seedTestUser(t, q, "alice", RoleMember)
	seedPushSub(t, q, user.ID, "https://push.example/ep-disabled")

	// txn_added defaults OFF (migration 015) — fan-out must not send.
	h.fanOutPush(context.Background(), "txn_added", []byte(`{"title":"x","body":"y","url":"/","type":"txn_added"}`), 0, pushOpts{})
	if rec.count() != 0 {
		t.Fatalf("disabled type: want 0 sends, got %d", rec.count())
	}
}

func TestFanOutPush_SendsWhenTypeEnabled(t *testing.T) {
	q, db := setupTestDB(t)
	rec := &recordingSender{}
	h := NewHandler(q, db)
	h.pushTesterForBudgetAlerts = rec

	user := seedTestUser(t, q, "alice", RoleMember)
	seedPushSub(t, q, user.ID, "https://push.example/ep-enabled")

	// over_budget defaults ON — fan-out must send to the one subscription.
	h.fanOutPush(context.Background(), "over_budget", []byte(`{"title":"x","body":"y","url":"/budgets","type":"budget_over"}`), 0, pushOpts{})
	if rec.count() != 1 {
		t.Fatalf("enabled type: want 1 send, got %d", rec.count())
	}
}

func TestEvaluateBudgetAlerts_LatchSendsOnceThenDedups(t *testing.T) {
	q, db := setupTestDB(t)
	rec := &recordingSender{}
	h := NewHandler(q, db)
	h.pushTesterForBudgetAlerts = rec // test seam: see budget_alert.go

	user := seedTestUser(t, q, "alice", RoleMember)
	catID := seedExpenseCategory(t, q, "Groceries")
	seedPushSub(t, q, user.ID, "https://push.example/ep-1")

	// Limit 100.00; spend 150.00 -> over.
	if err := q.UpsertCategoryBudget(context.Background(), database.UpsertCategoryBudgetParams{
		Year: 2026, Month: 5, CategoryID: catID, AmountCents: 10000,
	}); err != nil {
		t.Fatalf("budget: %v", err)
	}
	seedExpenseRow(t, q, user.ID, catID, "2026-05-10", 15000)

	cell := budgetCell{CategoryID: catID, Year: 2026, Month: 5}
	h.evaluateBudgetAlerts(context.Background(), []budgetCell{cell})
	if rec.count() != 1 {
		t.Fatalf("first eval: want 1 send, got %d", rec.count())
	}
	// Second eval, same over-state: latch row already present -> no send.
	h.evaluateBudgetAlerts(context.Background(), []budgetCell{cell})
	if rec.count() != 1 {
		t.Fatalf("second eval (dedup): want 1 send total, got %d", rec.count())
	}
}

func TestEvaluateBudgetAlerts_DropUnderClearsThenReCrossSendsAgain(t *testing.T) {
	q, db := setupTestDB(t)
	rec := &recordingSender{}
	h := NewHandler(q, db)
	h.pushTesterForBudgetAlerts = rec

	user := seedTestUser(t, q, "alice", RoleMember)
	catID := seedExpenseCategory(t, q, "Groceries")
	seedPushSub(t, q, user.ID, "https://push.example/ep-1")
	if err := q.UpsertCategoryBudget(context.Background(), database.UpsertCategoryBudgetParams{
		Year: 2026, Month: 5, CategoryID: catID, AmountCents: 10000,
	}); err != nil {
		t.Fatalf("budget: %v", err)
	}
	cell := budgetCell{CategoryID: catID, Year: 2026, Month: 5}

	// Cross over: send 1, latch set.
	over := seedExpenseRow(t, q, user.ID, catID, "2026-05-10", 15000)
	h.evaluateBudgetAlerts(context.Background(), []budgetCell{cell})
	if rec.count() != 1 {
		t.Fatalf("cross: want 1, got %d", rec.count())
	}

	// Drop back under (soft-delete the over row), eval clears the latch.
	if err := h.txnStore.Delete(context.Background(), user.ID, over.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	h.evaluateBudgetAlerts(context.Background(), []budgetCell{cell})
	if rec.count() != 1 {
		t.Fatalf("drop-under: want still 1 (no new send), got %d", rec.count())
	}

	// Re-cross: latch was cleared, so this sends AGAIN.
	seedExpenseRow(t, q, user.ID, catID, "2026-05-12", 20000)
	h.evaluateBudgetAlerts(context.Background(), []budgetCell{cell})
	if rec.count() != 2 {
		t.Fatalf("re-cross: want 2 sends total, got %d", rec.count())
	}
}

func TestEvaluateBudgetAlerts_HidesTombstoned(t *testing.T) {
	q, db := setupTestDB(t)
	rec := &recordingSender{}
	h := NewHandler(q, db)
	h.pushTesterForBudgetAlerts = rec

	user := seedTestUser(t, q, "alice", RoleMember)
	catID := seedExpenseCategory(t, q, "Groceries")
	seedPushSub(t, q, user.ID, "https://push.example/ep-1")
	if err := q.UpsertCategoryBudget(context.Background(), database.UpsertCategoryBudgetParams{
		Year: 2026, Month: 5, CategoryID: catID, AmountCents: 10000,
	}); err != nil {
		t.Fatalf("budget: %v", err)
	}

	// One live row (50.00, under limit) and one tombstoned sentinel (999.99).
	seedExpenseRow(t, q, user.ID, catID, "2026-05-10", 5000)
	ghost := seedExpenseRow(t, q, user.ID, catID, "2026-05-11", 99900)
	if err := h.txnStore.Delete(context.Background(), user.ID, ghost.ID); err != nil {
		t.Fatalf("tombstone: %v", err)
	}

	h.evaluateBudgetAlerts(context.Background(),
		[]budgetCell{{CategoryID: catID, Year: 2026, Month: 5}})
	if rec.count() != 0 {
		t.Fatalf("tombstoned 999 row must not trip alert: got %d sends", rec.count())
	}
}

func TestEvaluateBudgetAlerts_NoSubscribersNoOp(t *testing.T) {
	q, db := setupTestDB(t)
	rec := &recordingSender{}
	h := NewHandler(q, db)
	h.pushTesterForBudgetAlerts = rec

	user := seedTestUser(t, q, "alice", RoleMember)
	catID := seedExpenseCategory(t, q, "Groceries")
	// No seedPushSub call: zero subscribers.
	if err := q.UpsertCategoryBudget(context.Background(), database.UpsertCategoryBudgetParams{
		Year: 2026, Month: 5, CategoryID: catID, AmountCents: 10000,
	}); err != nil {
		t.Fatalf("budget: %v", err)
	}
	seedExpenseRow(t, q, user.ID, catID, "2026-05-10", 15000)

	h.evaluateBudgetAlerts(context.Background(),
		[]budgetCell{{CategoryID: catID, Year: 2026, Month: 5}})
	if rec.count() != 0 {
		t.Fatalf("no subscribers: want 0 sends, got %d", rec.count())
	}
	// Latch must still be set so a later subscribe + re-eval does not double-fire.
	cleared, err := q.ClearBudgetAlertState(context.Background(), database.ClearBudgetAlertStateParams{
		CategoryID: catID, Year: 2026, Month: 5,
	})
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if n, _ := cleared.RowsAffected(); n != 1 {
		t.Fatalf("latch should have been set even with no subscribers; cleared %d rows", n)
	}
}

func TestEvaluateBudgetAlerts_PayloadIsDollarsNotCents(t *testing.T) {
	q, db := setupTestDB(t)
	rec := &recordingSender{}
	h := NewHandler(q, db)
	h.pushTesterForBudgetAlerts = rec

	user := seedTestUser(t, q, "alice", RoleMember)
	catID := seedExpenseCategory(t, q, "Groceries")
	seedPushSub(t, q, user.ID, "https://push.example/ep-1")
	if err := q.UpsertCategoryBudget(context.Background(), database.UpsertCategoryBudgetParams{
		Year: 2026, Month: 5, CategoryID: catID, AmountCents: 10000, // 100.00
	}); err != nil {
		t.Fatalf("budget: %v", err)
	}
	seedExpenseRow(t, q, user.ID, catID, "2026-05-10", 15000) // 150.00

	h.evaluateBudgetAlerts(context.Background(),
		[]budgetCell{{CategoryID: catID, Year: 2026, Month: 5}})
	if rec.count() != 1 {
		t.Fatalf("want 1 send, got %d", rec.count())
	}
	var p pushAlertPayload
	if err := json.Unmarshal(rec.payloads[0], &p); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}
	if p.LimitDollars != 100 {
		t.Errorf("limit: want 100 dollars, got %v (cents leaked?)", p.LimitDollars)
	}
	if p.SpentDollars != 150 {
		t.Errorf("spent: want 150 dollars, got %v (cents leaked?)", p.SpentDollars)
	}
	// Title/Body must be populated, or the SW renders a blank "SpenDrop"
	// notification with no description (regression guard). The seed helper makes
	// the category name unique per test, so match on shape, not an exact name.
	if !strings.HasPrefix(p.Title, "Over budget: ") || len(p.Title) <= len("Over budget: ") {
		t.Errorf("title: want %q + a category name, got %q", "Over budget: ", p.Title)
	}
	if !strings.Contains(p.Body, "$150.00") || !strings.Contains(p.Body, "$100.00") {
		t.Errorf("body must state spent ($150.00) and limit ($100.00), got %q", p.Body)
	}
	if p.URL != "/budgets" {
		t.Errorf("url: want /budgets for the click deep-link, got %q", p.URL)
	}
}

func TestBudgetCellTagTopicAndBound(t *testing.T) {
	if got := budgetCellTag(7, 2026, 5); got != "budget-7-202605" {
		t.Errorf("budgetCellTag = %q, want budget-7-202605", got)
	}
	if got := budgetCellTopic(7, 2026, 5); got != "ob-7-202605" {
		t.Errorf("budgetCellTopic = %q, want ob-7-202605", got)
	}
	if budgetSummaryTag != "budget-summary" {
		t.Errorf("budgetSummaryTag = %q, want budget-summary", budgetSummaryTag)
	}
	if budgetSummaryTopic != "ob-summary" {
		t.Errorf("budgetSummaryTopic = %q, want ob-summary", budgetSummaryTopic)
	}
	// Web Push Topic must stay <=32 url-safe chars even for a max-width category id.
	if got := budgetCellTopic(9223372036854775807, 2026, 12); len(got) > 32 {
		t.Errorf("topic %q exceeds 32 chars (len %d)", got, len(got))
	}
}
