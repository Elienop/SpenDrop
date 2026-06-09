package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elienop/spendrop/internal/sse"
)

// invalidateFrame is the JSON object carried in the SSE `data:` line.
type invalidateFrame struct {
	Resources []string `json:"resources"`
}

// drainResources reads SSE frames from a test client channel until either the
// timeout elapses or a frame arrives, then returns the union of all resource
// names seen across every frame received within a short settle window.
func drainResources(t *testing.T, ch <-chan []byte) []string {
	t.Helper()
	seen := map[string]struct{}{}
	deadline := time.After(500 * time.Millisecond)
	got := false
	collect := func(msg []byte) {
		for _, line := range strings.Split(string(msg), "\n") {
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			var f invalidateFrame
			if err := json.Unmarshal([]byte(payload), &f); err != nil {
				t.Fatalf("decode SSE data line %q: %v", payload, err)
			}
			for _, r := range f.Resources {
				seen[r] = struct{}{}
			}
		}
	}
	for {
		select {
		case msg := <-ch:
			got = true
			collect(msg)
			// Give any coalesced follow-up frame a brief moment, then return.
			select {
			case extra := <-ch:
				collect(extra)
			case <-time.After(50 * time.Millisecond):
			}
			out := make([]string, 0, len(seen))
			for r := range seen {
				out = append(out, r)
			}
			return out
		case <-deadline:
			if !got {
				t.Fatalf("no SSE frame delivered within deadline")
			}
		}
	}
}

// assertResources fails unless got contains exactly the want set (order-free).
func assertResources(t *testing.T, got, want []string) {
	t.Helper()
	gotSet := map[string]struct{}{}
	for _, r := range got {
		gotSet[r] = struct{}{}
	}
	for _, w := range want {
		if _, ok := gotSet[w]; !ok {
			t.Errorf("missing resource %q in broadcast; got %v want %v", w, got, want)
		}
	}
	if len(got) != len(want) {
		t.Errorf("resource count mismatch; got %v want %v", got, want)
	}
}

// newHubWithSubscriber starts a Hub on a cancelable context, registers one
// buffered test client owned by the given user id, and returns the client's
// channel. The per-client buffer (16) means our single-event tests never drop.
func newHubWithSubscriber(t *testing.T, userID int64) (*sse.Hub, <-chan []byte) {
	t.Helper()
	hub := sse.NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go hub.Run(ctx)

	client := sse.NewClient(userID)
	if err := hub.Register(client); err != nil {
		t.Fatalf("register test subscriber: %v", err)
	}
	t.Cleanup(func() { hub.Unregister(client) })
	return hub, client.Events()
}

func TestPublishInvalidate_Create_BroadcastsTxnResources(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	hub, ch := newHubWithSubscriber(t, user.ID)
	h.SetEventBroker(hub)

	body := strings.NewReader(`{"date":"2026-04-06","amount":50.00,"description":"Groceries","category_id":1}`)
	req := httptest.NewRequest(http.MethodPost, "/api/transactions", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleCreateTransaction(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}
	assertResources(t, drainResources(t, ch),
		[]string{"transactions", "dashboard", "reports", "budgets"})
}

func TestPublishInvalidate_Update_BroadcastsTxnResources(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	hub, ch := newHubWithSubscriber(t, user.ID)
	h.SetEventBroker(hub)

	txn := seedTestTransaction(t, q, user.ID, 1, "2026-04-06", 50.0, "Original")

	body := strings.NewReader(`{"date":"2026-04-07","amount":75.00,"description":"Updated","category_id":1}`)
	req := httptest.NewRequest(http.MethodPut, "/api/transactions/"+fmt.Sprintf("%d", txn.ID), body)
	req = withUserAndURLParam(req, user, "id", fmt.Sprintf("%d", txn.ID))
	rec := httptest.NewRecorder()

	h.handleUpdateTransaction(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("update: expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	assertResources(t, drainResources(t, ch),
		[]string{"transactions", "dashboard", "reports", "budgets"})
}

func TestPublishInvalidate_Delete_BroadcastsTxnResourcesPlusTrash(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	hub, ch := newHubWithSubscriber(t, user.ID)
	h.SetEventBroker(hub)

	txn := seedTestTransaction(t, q, user.ID, 1, "2026-04-06", 50.0, "To delete")

	req := httptest.NewRequest(http.MethodDelete, "/api/transactions/"+fmt.Sprintf("%d", txn.ID), nil)
	req = withUserAndURLParam(req, user, "id", fmt.Sprintf("%d", txn.ID))
	rec := httptest.NewRecorder()

	h.handleDeleteTransaction(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("delete: expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	assertResources(t, drainResources(t, ch),
		[]string{"transactions", "dashboard", "reports", "budgets", "trash"})
}

// A full/slow consumer must never change the HTTP response: the publish path
// is best-effort and non-blocking (drop-on-full). We register a client and
// never read its channel, overflow its buffer with >16 publishes, then run a
// create and assert it still returns 201.
func TestPublishInvalidate_SlowConsumer_DoesNotChangeStatus(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	hub := sse.NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go hub.Run(ctx)
	wedged := sse.NewClient(user.ID)
	if err := hub.Register(wedged); err != nil {
		t.Fatalf("register wedged subscriber: %v", err)
	}
	for i := 0; i < 64; i++ {
		hub.Publish("transactions")
	}
	h.SetEventBroker(hub)

	body := strings.NewReader(`{"date":"2026-04-06","amount":50.00,"description":"Groceries","category_id":1}`)
	req := httptest.NewRequest(http.MethodPost, "/api/transactions", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleCreateTransaction(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("create with wedged subscriber: expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

// With no broker installed the publishInvalidate wrapper must be a pure no-op
// (nil-safe), so the mutation still succeeds.
func TestPublishInvalidate_NilBroker_CreateStillSucceeds(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db) // SetEventBroker NOT called → broker is nil
	user := seedTestUser(t, q, "alice", "member")

	body := strings.NewReader(`{"date":"2026-04-06","amount":50.00,"description":"Groceries","category_id":1}`)
	req := httptest.NewRequest(http.MethodPost, "/api/transactions", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleCreateTransaction(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("nil-broker create: expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestPublishInvalidate_BatchCreate_BroadcastsTxnResources(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	hub, ch := newHubWithSubscriber(t, user.ID)
	h.SetEventBroker(hub)

	body := strings.NewReader(`[
		{"date":"2026-04-06","amount":10.00,"description":"A","category_id":1},
		{"date":"2026-04-07","amount":20.00,"description":"B","category_id":1}
	]`)
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/batch", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleBatchCreateTransactions(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("batch-create: expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}
	assertResources(t, drainResources(t, ch),
		[]string{"transactions", "dashboard", "reports", "budgets"})
}

func TestPublishInvalidate_BatchDelete_BroadcastsTxnResourcesPlusTrash(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	hub, ch := newHubWithSubscriber(t, user.ID)
	h.SetEventBroker(hub)

	t1 := seedTestTransaction(t, q, user.ID, 1, "2026-04-06", 10.0, "A")
	t2 := seedTestTransaction(t, q, user.ID, 1, "2026-04-07", 20.0, "B")

	body := strings.NewReader(fmt.Sprintf(`{"ids":[%d,%d]}`, t1.ID, t2.ID))
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/batch-delete", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleBatchDeleteTransactions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("batch-delete: expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	assertResources(t, drainResources(t, ch),
		[]string{"transactions", "dashboard", "reports", "budgets", "trash"})
}

func TestPublishInvalidate_BatchUpdate_BroadcastsTxnResources(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	hub, ch := newHubWithSubscriber(t, user.ID)
	h.SetEventBroker(hub)

	t1 := seedTestTransaction(t, q, user.ID, 1, "2026-04-06", 10.0, "A")

	body := strings.NewReader(fmt.Sprintf(`{"ids":[%d],"patch":{"description":"Renamed"}}`, t1.ID))
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/batch-update", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleBatchUpdateTransactions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("batch-update: expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	assertResources(t, drainResources(t, ch),
		[]string{"transactions", "dashboard", "reports", "budgets"})
}

func TestPublishInvalidate_DeleteByFilter_BroadcastsTxnResourcesPlusTrash(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	hub, ch := newHubWithSubscriber(t, user.ID)
	h.SetEventBroker(hub)

	seedTestTransaction(t, q, user.ID, 1, "2026-04-06", 10.0, "A")

	req := httptest.NewRequest(http.MethodDelete, "/api/transactions/by-filter", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleDeleteTransactionsByFilter(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("delete-by-filter: expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	assertResources(t, drainResources(t, ch),
		[]string{"transactions", "dashboard", "reports", "budgets", "trash"})
}

func TestPublishInvalidate_UpdateByFilter_BroadcastsTxnResources(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	hub, ch := newHubWithSubscriber(t, user.ID)
	h.SetEventBroker(hub)

	seedTestTransaction(t, q, user.ID, 1, "2026-04-06", 10.0, "A")

	body := strings.NewReader(`{"patch":{"description":"Renamed"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/update-by-filter", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleUpdateTransactionsByFilter(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("update-by-filter: expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	assertResources(t, drainResources(t, ch),
		[]string{"transactions", "dashboard", "reports", "budgets"})
}

func TestPublishInvalidate_BulkRename_BroadcastsTransactionsAndReports(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	hub, ch := newHubWithSubscriber(t, user.ID)
	h.SetEventBroker(hub)

	seedTestTransaction(t, q, user.ID, 1, "2026-04-06", 10.0, "Coffe")

	body := strings.NewReader(`{"search":"Coffe","new_description":"Coffee"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/bulk-rename", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleBulkRename(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("bulk-rename: expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	assertResources(t, drainResources(t, ch),
		[]string{"transactions", "reports"})
}
