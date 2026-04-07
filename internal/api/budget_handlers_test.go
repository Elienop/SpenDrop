package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elienop/spendrop/internal/database"
)

// --- handleGetBudgets ---

func TestHandleGetBudgets_ReturnsBudgetsForYear(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	// Seed some budgets
	err := q.UpsertBudget(context.Background(), database.UpsertBudgetParams{
		Year: 2026, Month: 1, Amount: 2500,
	})
	if err != nil {
		t.Fatalf("seed budget: %v", err)
	}
	err = q.UpsertBudget(context.Background(), database.UpsertBudgetParams{
		Year: 2026, Month: 2, Amount: 3000,
	})
	if err != nil {
		t.Fatalf("seed budget: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/budgets?year=2026", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleGetBudgets(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp []map[string]any
	decodeResponse(t, rec, &resp)
	if len(resp) != 2 {
		t.Errorf("expected 2 budgets, got %d", len(resp))
	}
}

func TestHandleGetBudgets_DefaultsToCurrentYear(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	req := httptest.NewRequest(http.MethodGet, "/api/budgets", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleGetBudgets(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleGetBudgets_NoAuth_Returns401(t *testing.T) {
	h := setupHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/budgets", nil)
	rec := httptest.NewRecorder()

	h.handleGetBudgets(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// --- handleSetBudget ---

func TestHandleSetBudget_UpsertsBudget(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	body := strings.NewReader(`{"amount":2500}`)
	req := httptest.NewRequest(http.MethodPut, "/api/budgets/2026/4", body)
	req = withUserAndURLParams(req, user, map[string]string{"year": "2026", "month": "4"})
	rec := httptest.NewRecorder()

	h.handleSetBudget(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	// Verify in DB
	b, err := q.GetBudget(context.Background(), database.GetBudgetParams{Year: 2026, Month: 4})
	if err != nil {
		t.Fatalf("get budget: %v", err)
	}
	if b.Amount != 2500 {
		t.Errorf("expected amount 2500, got %v", b.Amount)
	}
}

func TestHandleSetBudget_InvalidYear_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	body := strings.NewReader(`{"amount":2500}`)
	req := httptest.NewRequest(http.MethodPut, "/api/budgets/abc/4", body)
	req = withUserAndURLParams(req, user, map[string]string{"year": "abc", "month": "4"})
	rec := httptest.NewRecorder()

	h.handleSetBudget(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleSetBudget_InvalidMonth_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	body := strings.NewReader(`{"amount":2500}`)
	req := httptest.NewRequest(http.MethodPut, "/api/budgets/2026/13", body)
	req = withUserAndURLParams(req, user, map[string]string{"year": "2026", "month": "13"})
	rec := httptest.NewRecorder()

	h.handleSetBudget(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleSetBudget_ZeroAmount_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	body := strings.NewReader(`{"amount":0}`)
	req := httptest.NewRequest(http.MethodPut, "/api/budgets/2026/4", body)
	req = withUserAndURLParams(req, user, map[string]string{"year": "2026", "month": "4"})
	rec := httptest.NewRecorder()

	h.handleSetBudget(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleSetBudget_NoAuth_Returns401(t *testing.T) {
	h := setupHandler(t)

	body := strings.NewReader(`{"amount":2500}`)
	req := httptest.NewRequest(http.MethodPut, "/api/budgets/2026/4", body)
	rctx := chiRouteContext(map[string]string{"year": "2026", "month": "4"})
	req = req.WithContext(context.WithValue(req.Context(), chiRouteCtxKey(), rctx))
	rec := httptest.NewRecorder()

	h.handleSetBudget(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// --- handleDefaultBudget ---

func TestHandleDefaultBudget_GET_ReturnsCurrentDefault(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	req := httptest.NewRequest(http.MethodGet, "/api/settings/default-budget", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleDefaultBudget(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	// Seed data has default_budget = "2000"
	if resp["amount"] == nil {
		t.Error("expected amount in response")
	}
}

func TestHandleDefaultBudget_PUT_UpdatesDefault(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	body := strings.NewReader(`{"amount":3000}`)
	req := httptest.NewRequest(http.MethodPut, "/api/settings/default-budget", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleDefaultBudget(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	// Verify via GET
	getReq := httptest.NewRequest(http.MethodGet, "/api/settings/default-budget", nil)
	getReq = withUser(getReq, user)
	getRec := httptest.NewRecorder()
	h.handleDefaultBudget(getRec, getReq)

	var resp map[string]any
	decodeResponse(t, getRec, &resp)
	if resp["amount"].(float64) != 3000 {
		t.Errorf("expected amount 3000, got %v", resp["amount"])
	}
}

func TestHandleDefaultBudget_NoAuth_Returns401(t *testing.T) {
	h := setupHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/default-budget", nil)
	rec := httptest.NewRecorder()

	h.handleDefaultBudget(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}
