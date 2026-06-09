package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elienop/spendrop/internal/database"
	"github.com/elienop/spendrop/internal/push"
)

// explodingSender always errors on Send; used to prove a push failure never
// rolls back or errors the already-committed transaction mutation.
type explodingSender struct{ calls int }

func (s *explodingSender) Send(ctx context.Context, sub push.Subscription, payload []byte, opts push.Options) (bool, error) {
	s.calls++
	return false, errors.New("transport boom")
}

func TestCreateTransaction_SendFailureDoesNotErrorCommittedMutation(t *testing.T) {
	q, db := setupTestDB(t)
	boom := &explodingSender{}
	h := NewHandler(q, db)
	h.pushTesterForBudgetAlerts = boom

	user := seedTestUser(t, q, "alice", RoleMember)
	catID := seedExpenseCategory(t, q, "Groceries")
	seedPushSub(t, q, user.ID, "https://push.example/ep-boom")
	if err := q.UpsertCategoryBudget(context.Background(), database.UpsertCategoryBudgetParams{
		Year: 2026, Month: 5, CategoryID: catID, AmountCents: 10000,
	}); err != nil {
		t.Fatalf("budget: %v", err)
	}

	body, _ := json.Marshal(transactionRequest{
		Date: "2026-05-10", Amount: 150, Description: "big shop", CategoryID: catID,
	})
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/transactions", bytes.NewReader(body)), user)
	rec := httptest.NewRecorder()
	h.handleCreateTransaction(rec, req)

	// The transport blew up, but the HTTP response is still 201 and the row
	// is committed — the hook is strictly best-effort.
	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201 despite send failure, got %d: %s", rec.Code, rec.Body.String())
	}
	if boom.calls == 0 {
		t.Fatalf("expected the failing sender to have been invoked")
	}
	counts, err := q.CountAllTransactions(context.Background())
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if counts.Live != 1 {
		t.Fatalf("row must be committed despite push failure, got live count=%d", counts.Live)
	}
}

func TestUpdateTransaction_BackDatedEvaluatesPriorMonth(t *testing.T) {
	q, db := setupTestDB(t)
	rec := &recordingSender{}
	h := NewHandler(q, db)
	h.pushTesterForBudgetAlerts = rec

	user := seedTestUser(t, q, "alice", RoleMember)
	catID := seedExpenseCategory(t, q, "Groceries")
	seedPushSub(t, q, user.ID, "https://push.example/ep-1")
	// Limit only set for APRIL (the prior month), none for May.
	if err := q.UpsertCategoryBudget(context.Background(), database.UpsertCategoryBudgetParams{
		Year: 2026, Month: 4, CategoryID: catID, AmountCents: 10000,
	}); err != nil {
		t.Fatalf("budget: %v", err)
	}

	// Create a May row (no April limit hit yet).
	txn := seedExpenseRow(t, q, user.ID, catID, "2026-05-10", 15000)

	// Back-date it into April via update -> the April cell (prior month)
	// must be evaluated and trip the alert.
	body, _ := json.Marshal(transactionRequest{
		Date: "2026-04-10", Amount: 150, Description: "big shop", CategoryID: catID,
	})
	req := withUserAndURLParam(
		httptest.NewRequest(http.MethodPut, "/api/transactions/x", bytes.NewReader(body)),
		user, "id", itoa(txn.ID))
	w := httptest.NewRecorder()
	h.handleUpdateTransaction(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update: want 200, got %d: %s", w.Code, w.Body.String())
	}
	if rec.count() != 1 {
		t.Fatalf("back-dated edit must evaluate the APRIL cell and alert; got %d sends", rec.count())
	}
	var p pushAlertPayload
	if err := json.Unmarshal(rec.payloads[0], &p); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if p.Year != 2026 || p.Month != 4 {
		t.Fatalf("alert must be for prior month 2026-04, got %04d-%02d", p.Year, p.Month)
	}
}
