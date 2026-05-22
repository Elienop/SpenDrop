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

	// Seed savings goals. Phase 3.1a: dual-write target_amount_cents.
	err := q.UpsertSavingsGoal(context.Background(), database.UpsertSavingsGoalParams{
		Year: 2026, TargetAmountCents: dollarsToCents(12000),
	})
	if err != nil {
		t.Fatalf("seed savings goal: %v", err)
	}
	err = q.UpsertSavingsGoal(context.Background(), database.UpsertSavingsGoalParams{
		Year: 2025, TargetAmountCents: dollarsToCents(10000),
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
		t.Fatalf("expected 2 savings goals, got %d", len(resp))
	}

	// The wire contract is `target_amount` in DOLLARS, like every other money
	// endpoint — never the raw target_amount_cents column. Regression guard for
	// the Reports → Savings tab (and Settings savings-goals panel) blank screen:
	// migration 010 dropped the REAL target_amount column, so returning the raw
	// database.SavingsGoal row leaked only target_amount_cents and the frontend's
	// goal.target_amount was undefined → fmt(undefined) crashed the page.
	byYear := make(map[float64]map[string]any, len(resp))
	for _, g := range resp {
		year, _ := g["year"].(float64)
		byYear[year] = g
	}
	goal2026, ok := byYear[2026]
	if !ok {
		t.Fatalf("expected a 2026 goal in response; got %+v", resp)
	}
	amount, ok := goal2026["target_amount"].(float64)
	if !ok {
		t.Fatalf("expected target_amount (dollars) in response; goal 2026 = %+v", goal2026)
	}
	if amount != 12000 {
		t.Errorf("expected target_amount 12000 dollars, got %v", amount)
	}
	if _, leaked := goal2026["target_amount_cents"]; leaked {
		t.Errorf("response leaked target_amount_cents; the frontend reads target_amount (dollars)")
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
	user := seedTestUser(t, q, "alice", "admin")

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
	if goal.TargetAmountCents != dollarsToCents(15000) {
		t.Errorf("expected target_amount_cents %d, got %v", dollarsToCents(15000), goal.TargetAmountCents)
	}
}

func TestHandleSetSavingsGoal_InvalidYear_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "admin")

	body := strings.NewReader(`{"target_amount":15000}`)
	req := httptest.NewRequest(http.MethodPut, "/api/savings-goals/abc", body)
	req = withUserAndURLParam(req, user, "year", "abc")
	rec := httptest.NewRecorder()

	h.handleSetSavingsGoal(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleSetSavingsGoal_ZeroAmount_Succeeds(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "admin")

	body := strings.NewReader(`{"target_amount":0}`)
	req := httptest.NewRequest(http.MethodPut, "/api/savings-goals/2026", body)
	req = withUserAndURLParam(req, user, "year", "2026")
	rec := httptest.NewRecorder()

	h.handleSetSavingsGoal(rec, req)

	// Zero is valid — means "no savings goal this year"
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleSetSavingsGoal_NegativeAmount_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "admin")

	body := strings.NewReader(`{"target_amount":-100}`)
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
