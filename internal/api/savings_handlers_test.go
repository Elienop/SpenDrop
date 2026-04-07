package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elienop/spendrop/internal/database"
)

// --- handleGetSavingsGoals ---

func TestHandleGetSavingsGoals_ReturnsAll(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	// Seed savings goals
	err := q.UpsertSavingsGoal(context.Background(), database.UpsertSavingsGoalParams{
		Year: 2026, TargetAmount: 12000,
	})
	if err != nil {
		t.Fatalf("seed savings goal: %v", err)
	}
	err = q.UpsertSavingsGoal(context.Background(), database.UpsertSavingsGoalParams{
		Year: 2025, TargetAmount: 10000,
	})
	if err != nil {
		t.Fatalf("seed savings goal: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/savings-goals", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleGetSavingsGoals(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp []map[string]any
	decodeResponse(t, rec, &resp)
	if len(resp) != 2 {
		t.Errorf("expected 2 savings goals, got %d", len(resp))
	}
}

func TestHandleGetSavingsGoals_NoAuth_Returns401(t *testing.T) {
	h := setupHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/savings-goals", nil)
	rec := httptest.NewRecorder()

	h.handleGetSavingsGoals(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// --- handleSetSavingsGoal ---

func TestHandleSetSavingsGoal_UpsertsGoal(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	body := strings.NewReader(`{"target_amount":15000}`)
	req := httptest.NewRequest(http.MethodPut, "/api/savings-goals/2026", body)
	req = withUserAndURLParam(req, user, "year", "2026")
	rec := httptest.NewRecorder()

	h.handleSetSavingsGoal(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	// Verify in DB
	goal, err := q.GetSavingsGoal(context.Background(), 2026)
	if err != nil {
		t.Fatalf("get savings goal: %v", err)
	}
	if goal.TargetAmount != 15000 {
		t.Errorf("expected target_amount 15000, got %v", goal.TargetAmount)
	}
}

func TestHandleSetSavingsGoal_InvalidYear_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	body := strings.NewReader(`{"target_amount":15000}`)
	req := httptest.NewRequest(http.MethodPut, "/api/savings-goals/abc", body)
	req = withUserAndURLParam(req, user, "year", "abc")
	rec := httptest.NewRecorder()

	h.handleSetSavingsGoal(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleSetSavingsGoal_ZeroAmount_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	body := strings.NewReader(`{"target_amount":0}`)
	req := httptest.NewRequest(http.MethodPut, "/api/savings-goals/2026", body)
	req = withUserAndURLParam(req, user, "year", "2026")
	rec := httptest.NewRecorder()

	h.handleSetSavingsGoal(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleSetSavingsGoal_NoAuth_Returns401(t *testing.T) {
	h := setupHandler(t)

	body := strings.NewReader(`{"target_amount":15000}`)
	req := httptest.NewRequest(http.MethodPut, "/api/savings-goals/2026", body)
	req = withURLParam(req, "year", "2026")
	rec := httptest.NewRecorder()

	h.handleSetSavingsGoal(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}
