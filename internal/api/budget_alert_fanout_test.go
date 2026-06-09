package api

import (
	"context"
	"sort"
	"sync"
	"testing"

	"github.com/elienop/spendrop/internal/database"
	"github.com/elienop/spendrop/internal/push"
)

// endpointRecorder records the endpoint of every subscription a Send touched,
// so a test can assert exactly which devices received a fan-out.
type endpointRecorder struct {
	mu   sync.Mutex
	seen []string
}

func (s *endpointRecorder) Send(ctx context.Context, sub push.Subscription, payload []byte, opts push.Options) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, sub.Endpoint)
	return false, nil
}

func (s *endpointRecorder) endpoints() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]string(nil), s.seen...)
	sort.Strings(out)
	return out
}

// pruningSender reports prune==true for one designated endpoint (simulating an
// HTTP 410 Gone) and success for the rest, so a test can assert the pruned
// endpoint's row is deleted while the others survive.
type pruningSender struct {
	mu      sync.Mutex
	pruneEP string
	seen    []push.Subscription
}

func (s *pruningSender) Send(ctx context.Context, sub push.Subscription, payload []byte, opts push.Options) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, sub)
	return sub.Endpoint == s.pruneEP, nil
}

func TestFanOutPush_MultipleSubsPrunesGoneRow(t *testing.T) {
	q, db := setupTestDB(t)
	gone := "https://push.example/gone-410"
	ps := &pruningSender{pruneEP: gone}
	h := NewHandler(q, db)
	h.pushTesterForBudgetAlerts = ps

	alice := seedTestUser(t, q, "alice", RoleMember)
	bob := seedTestUser(t, q, "bob", RoleMember)
	seedPushSub(t, q, alice.ID, "https://push.example/alice-ok")
	seedPushSub(t, q, bob.ID, gone)
	seedPushSub(t, q, bob.ID, "https://push.example/bob-ok")

	h.fanOutPush(context.Background(), "over_budget", []byte(`{"type":"budget_over"}`), 0, pushOpts{})

	// All three subs were attempted (household-wide, not per-user).
	if len(ps.seen) != 3 {
		t.Fatalf("want fan-out to all 3 subs, got %d", len(ps.seen))
	}
	// The 410 endpoint's row was pruned; the other two remain.
	all, err := q.ListAllPushSubscriptions(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("want 2 rows after pruning the 410, got %d", len(all))
	}
	for _, s := range all {
		if s.Endpoint == gone {
			t.Fatalf("the 410 endpoint should have been pruned but is still present")
		}
	}
}

// enableTxnAdded flips the txn_added type ON (it defaults OFF in migration 015)
// so an activity fan-out is not short-circuited by the household gate.
func enableTxnAdded(t *testing.T, q *database.Queries) {
	t.Helper()
	if err := q.UpdateNotificationSettings(context.Background(), database.UpdateNotificationSettingsParams{
		OverBudget:             true,
		TxnAdded:               true,
		TxnDeleted:             true,
		TxnEdited:              true,
		LargeTxn:               false,
		LargeTxnThresholdCents: 0,
	}); err != nil {
		t.Fatalf("enable notification types: %v", err)
	}
}

// TestFanOutPush_ExcludesActorAccount proves an ACTIVITY fan-out skips every
// subscription belonging to the acting user (exclude-by-account), while still
// delivering to other household members — the WhatsApp "don't echo the sender"
// rule. The actor's two devices must BOTH be skipped, the other member's one
// device must receive.
func TestFanOutPush_ExcludesActorAccount(t *testing.T) {
	q, db := setupTestDB(t)
	rec := &endpointRecorder{}
	h := NewHandler(q, db)
	h.pushTesterForBudgetAlerts = rec
	enableTxnAdded(t, q)

	actor := seedTestUser(t, q, "actor", RoleMember)
	other := seedTestUser(t, q, "other", RoleMember)
	seedPushSub(t, q, actor.ID, "https://push.example/actor-phone")
	seedPushSub(t, q, actor.ID, "https://push.example/actor-laptop")
	seedPushSub(t, q, other.ID, "https://push.example/other-phone")

	h.fanOutPush(context.Background(), "txn_added", []byte(`{"type":"txn_added"}`), actor.ID, pushOpts{})

	got := rec.endpoints()
	want := []string{"https://push.example/other-phone"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("activity fan-out must reach only the other member's device; want %v, got %v", want, got)
	}
}

// TestFanOutPush_ExcludeZeroSendsToEveryone is the regression guard for
// over_budget (a state alert, not an echo): excludeUserID==0 means exclude
// nobody, so every device — including the actor's — receives.
func TestFanOutPush_ExcludeZeroSendsToEveryone(t *testing.T) {
	q, db := setupTestDB(t)
	rec := &endpointRecorder{}
	h := NewHandler(q, db)
	h.pushTesterForBudgetAlerts = rec

	actor := seedTestUser(t, q, "actor", RoleMember)
	other := seedTestUser(t, q, "other", RoleMember)
	seedPushSub(t, q, actor.ID, "https://push.example/actor-phone")
	seedPushSub(t, q, other.ID, "https://push.example/other-phone")

	// over_budget defaults ON; excludeUserID 0 -> both devices receive.
	h.fanOutPush(context.Background(), "over_budget", []byte(`{"type":"budget_over"}`), 0, pushOpts{})

	got := rec.endpoints()
	want := []string{"https://push.example/actor-phone", "https://push.example/other-phone"}
	if len(got) != len(want) {
		t.Fatalf("exclude-nobody fan-out must reach all devices; want %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("want %v, got %v", want, got)
		}
	}
}

func TestFanOutPush_NilDispatcherIsNoOp(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db) // pushSender nil, no test override
	alice := seedTestUser(t, q, "alice", RoleMember)
	seedPushSub(t, q, alice.ID, "https://push.example/ep-1")

	// Must not panic and must not prune anything when the feature is off.
	h.fanOutPush(context.Background(), "over_budget", []byte(`{}`), 0, pushOpts{})
	all, err := q.ListAllPushSubscriptions(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("no-op fan-out must not delete rows, got %d", len(all))
	}
}
