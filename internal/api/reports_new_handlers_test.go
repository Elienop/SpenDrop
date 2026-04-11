package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elienop/spendrop/internal/database"
)

func TestHandleBudgetVsActual_Default(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	// Seed a budget for Jan 2026
	q.UpsertBudget(context.Background(), database.UpsertBudgetParams{
		Year: 2026, Month: 1, Amount: 3000,
	})

	// Seed an expense transaction in Jan 2026
	cat := seedTestCategory(t, q, "Food", "expense")
	seedTestTransaction(t, q, user.ID, cat.ID, "2026-01-15", 1200, "Groceries")

	req := httptest.NewRequest(http.MethodGet, "/api/reports/budget-vs-actual?year=2026", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleBudgetVsActual(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	data, ok := resp["data"].([]any)
	if !ok || len(data) != 12 {
		t.Fatalf("expected 12 months, got %v", resp["data"])
	}
	jan := data[0].(map[string]any)
	if jan["budget"].(float64) != 3000 {
		t.Errorf("expected budget 3000, got %v", jan["budget"])
	}
	if jan["actual"].(float64) != 1200 {
		t.Errorf("expected actual 1200, got %v", jan["actual"])
	}
}

func TestHandleBudgetVsActual_DefaultBudgetFallback(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	// Set default_budget in app_settings (no explicit monthly budget)
	q.UpsertSetting(context.Background(), database.UpsertSettingParams{
		Key: "default_budget", Value: "2500",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/reports/budget-vs-actual?year=2026", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleBudgetVsActual(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	data := resp["data"].([]any)
	jan := data[0].(map[string]any)
	if jan["budget"].(float64) != 2500 {
		t.Errorf("expected default budget 2500, got %v", jan["budget"])
	}
}

func TestHandleBudgetVsActual_Unauthorized(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)

	req := httptest.NewRequest(http.MethodGet, "/api/reports/budget-vs-actual", nil)
	rec := httptest.NewRecorder()
	h.handleBudgetVsActual(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestHandleExpenseVelocity_Default(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "Food", "expense")

	// Seed transactions in Jan 2026
	for _, day := range []string{"2026-01-05", "2026-01-10", "2026-01-15"} {
		seedTestTransaction(t, q, user.ID, cat.ID, day, 100, "test")
	}

	// Seed a budget
	q.UpsertBudget(context.Background(), database.UpsertBudgetParams{
		Year: 2026, Month: 1, Amount: 3000,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/reports/expense-velocity?year=2026&month=1", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleExpenseVelocity(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	if resp["days_in_month"].(float64) != 31 {
		t.Errorf("expected 31 days in Jan, got %v", resp["days_in_month"])
	}
	if resp["budget"].(float64) != 3000 {
		t.Errorf("expected budget 3000, got %v", resp["budget"])
	}
	current := resp["current"].([]any)
	if len(current) != 3 {
		t.Errorf("expected 3 daily entries, got %d", len(current))
	}
}

func TestHandleExpenseVelocity_Unauthorized(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)

	req := httptest.NewRequest(http.MethodGet, "/api/reports/expense-velocity?year=2026&month=1", nil)
	rec := httptest.NewRecorder()
	h.handleExpenseVelocity(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}
