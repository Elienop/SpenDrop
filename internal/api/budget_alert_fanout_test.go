package api

import (
	"context"
	"sync"
	"testing"

	"github.com/elienop/spendrop/internal/push"
)

// pruningSender reports prune==true for one designated endpoint (simulating an
// HTTP 410 Gone) and success for the rest, so a test can assert the pruned
// endpoint's row is deleted while the others survive.
type pruningSender struct {
	mu      sync.Mutex
	pruneEP string
	seen    []push.Subscription
}

func (s *pruningSender) Send(ctx context.Context, sub push.Subscription, payload []byte) (bool, error) {
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

	h.fanOutPush(context.Background(), "over_budget", []byte(`{"type":"budget_over"}`))

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

func TestFanOutPush_NilDispatcherIsNoOp(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db) // pushSender nil, no test override
	alice := seedTestUser(t, q, "alice", RoleMember)
	seedPushSub(t, q, alice.ID, "https://push.example/ep-1")

	// Must not panic and must not prune anything when the feature is off.
	h.fanOutPush(context.Background(), "over_budget", []byte(`{}`))
	all, err := q.ListAllPushSubscriptions(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("no-op fan-out must not delete rows, got %d", len(all))
	}
}
