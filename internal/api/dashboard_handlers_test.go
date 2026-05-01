package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/elienop/spendrop/internal/database"
)

// --- handleDashboardSummary ---

func TestHandleDashboardSummary_DefaultsToCurrentMonth(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/summary", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleDashboardSummary(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	if resp["year"] == nil {
		t.Error("expected year in response")
	}
	if resp["month"] == nil {
		t.Error("expected month in response")
	}
}

func TestHandleDashboardSummary_WithExplicitYearMonth(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/summary?year=2026&month=3", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleDashboardSummary(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	if int(resp["year"].(float64)) != 2026 {
		t.Errorf("expected year=2026, got %v", resp["year"])
	}
	if int(resp["month"].(float64)) != 3 {
		t.Errorf("expected month=3, got %v", resp["month"])
	}
}

func TestHandleDashboardSummary_CalculatesTotals(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	// Category 1 = Food (expense), Category 15 = Paycheck (income) from seed
	seedTestTransaction(t, q, user.ID, 1, "2026-04-06", 50.0, "Groceries")
	seedTestTransaction(t, q, user.ID, 1, "2026-04-10", 100.0, "Restaurant")
	seedTestTransaction(t, q, user.ID, 15, "2026-04-01", 3000.0, "Salary")

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/summary?year=2026&month=4", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleDashboardSummary(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)

	totalSpent := resp["total_spent"].(float64)
	if totalSpent != 150.0 {
		t.Errorf("expected total_spent=150, got %v", totalSpent)
	}

	totalIncome := resp["total_income"].(float64)
	if totalIncome != 3000.0 {
		t.Errorf("expected total_income=3000, got %v", totalIncome)
	}

	savingsThisMonth := resp["savings_this_month"].(float64)
	if savingsThisMonth != 2850.0 {
		t.Errorf("expected savings_this_month=2850, got %v", savingsThisMonth)
	}
}

func TestHandleDashboardSummary_BudgetFromMonthlyBudget(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	// Set a monthly budget for April 2026. Phase 3.1a: dual-write cents.
	err := q.UpsertBudget(context.Background(), database.UpsertBudgetParams{
		Year: 2026, Month: 4, Amount: 2500, AmountCents: dollarsToCents(2500),
	})
	if err != nil {
		t.Fatalf("seed budget: %v", err)
	}

	seedTestTransaction(t, q, user.ID, 1, "2026-04-06", 500.0, "Expense")

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/summary?year=2026&month=4", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleDashboardSummary(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)

	budget := resp["budget"].(float64)
	if budget != 2500.0 {
		t.Errorf("expected budget=2500, got %v", budget)
	}

	remaining := resp["remaining"].(float64)
	if remaining != 2000.0 {
		t.Errorf("expected remaining=2000, got %v", remaining)
	}
}

func TestHandleDashboardSummary_BudgetFallbackToDefault(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	// Seed data already has default_budget = "2000"
	// No monthly budget set for April 2026

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/summary?year=2026&month=4", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleDashboardSummary(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)

	budget := resp["budget"].(float64)
	if budget != 2000.0 {
		t.Errorf("expected budget=2000 (from default), got %v", budget)
	}
}

func TestHandleDashboardSummary_SavingsGoalProgress(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	// Set savings goal for 2026. Phase 3.1a: dual-write cents.
	err := q.UpsertSavingsGoal(context.Background(), database.UpsertSavingsGoalParams{
		Year: 2026, TargetAmount: 10000, TargetAmountCents: dollarsToCents(10000),
	})
	if err != nil {
		t.Fatalf("seed savings goal: %v", err)
	}

	// Jan income 5000, expense 2000 -> savings 3000
	seedTestTransaction(t, q, user.ID, 15, "2026-01-01", 5000.0, "Jan salary")
	seedTestTransaction(t, q, user.ID, 1, "2026-01-15", 2000.0, "Jan expense")
	// Feb income 5000, expense 1000 -> savings 4000
	seedTestTransaction(t, q, user.ID, 15, "2026-02-01", 5000.0, "Feb salary")
	seedTestTransaction(t, q, user.ID, 1, "2026-02-15", 1000.0, "Feb expense")

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/summary?year=2026&month=2", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleDashboardSummary(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)

	savingsGoal := resp["savings_goal"].(float64)
	if savingsGoal != 10000.0 {
		t.Errorf("expected savings_goal=10000, got %v", savingsGoal)
	}

	savingsYTD := resp["savings_ytd"].(float64)
	// Jan: 5000-2000=3000, Feb: 5000-1000=4000, total YTD = 7000
	if savingsYTD != 7000.0 {
		t.Errorf("expected savings_ytd=7000, got %v", savingsYTD)
	}

	progress := resp["savings_goal_progress"].(float64)
	// 7000/10000*100 = 70
	if progress != 70.0 {
		t.Errorf("expected savings_goal_progress=70, got %v", progress)
	}
}

func TestHandleDashboardSummary_NoAuth_Returns401(t *testing.T) {
	h := setupHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/summary", nil)
	rec := httptest.NewRecorder()

	h.handleDashboardSummary(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestHandleDashboardSummary_InvalidYear_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/summary?year=abc", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleDashboardSummary(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleDashboardSummary_InvalidMonth_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/summary?month=abc", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleDashboardSummary(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// --- handleDashboardTrend ---

func TestHandleDashboardTrend_DefaultsTo12Months(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/trend", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleDashboardTrend(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Trend []map[string]any `json:"trend"`
	}
	decodeResponse(t, rec, &resp)
	if len(resp.Trend) != 12 {
		t.Errorf("expected 12 trend entries, got %d", len(resp.Trend))
	}
}

func TestHandleDashboardTrend_CustomMonths(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/trend?months=3", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleDashboardTrend(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Trend []map[string]any `json:"trend"`
	}
	decodeResponse(t, rec, &resp)
	if len(resp.Trend) != 3 {
		t.Errorf("expected 3 trend entries, got %d", len(resp.Trend))
	}
}

func TestHandleDashboardTrend_IncludesTotals(t *testing.T) {
	q, db := setupTestDB(t)
	// Pin "now" to April 15, 2026 so the ?months=1 trend window covers the
	// April-dated rows below regardless of the wall clock at test time.
	// See helpers.go:38 for the fixedClock pattern.
	h := NewHandlerWithClock(q, db, fixedClock{t: time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)})
	user := seedTestUser(t, q, "alice", "member")

	// Seed transactions for the current month (anchored at April via fixedClock)
	seedTestTransaction(t, q, user.ID, 1, "2026-04-06", 100.0, "Expense")
	seedTestTransaction(t, q, user.ID, 15, "2026-04-01", 3000.0, "Income")

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/trend?months=1", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleDashboardTrend(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Trend []map[string]any `json:"trend"`
	}
	decodeResponse(t, rec, &resp)

	if len(resp.Trend) != 1 {
		t.Fatalf("expected 1 trend entry, got %d", len(resp.Trend))
	}

	entry := resp.Trend[0]
	if entry["total_spent"].(float64) != 100.0 {
		t.Errorf("expected total_spent=100, got %v", entry["total_spent"])
	}
	if entry["total_income"].(float64) != 3000.0 {
		t.Errorf("expected total_income=3000, got %v", entry["total_income"])
	}
}

func TestHandleDashboardTrend_NoAuth_Returns401(t *testing.T) {
	h := setupHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/trend", nil)
	rec := httptest.NewRecorder()

	h.handleDashboardTrend(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestHandleDashboardTrend_InvalidMonths_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/trend?months=abc", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleDashboardTrend(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// --- handleDashboardCategories ---

func TestHandleDashboardCategories_ReturnsCategoryBreakdown(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	// Category 1 = Food, Category 2 = Gifts (both expense from seed)
	seedTestTransaction(t, q, user.ID, 1, "2026-04-06", 100.0, "Food expense")
	seedTestTransaction(t, q, user.ID, 1, "2026-04-07", 50.0, "More food")
	seedTestTransaction(t, q, user.ID, 2, "2026-04-08", 200.0, "Gift")

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/categories?year=2026&month=4", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleDashboardCategories(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Categories []map[string]any `json:"categories"`
	}
	decodeResponse(t, rec, &resp)

	if len(resp.Categories) != 2 {
		t.Fatalf("expected 2 categories, got %d", len(resp.Categories))
	}

	// Categories ordered by total DESC: Gifts (200), Food (150)
	first := resp.Categories[0]
	if first["name"] != "Gifts" {
		t.Errorf("expected first category 'Gifts', got %v", first["name"])
	}
	if first["total"].(float64) != 200.0 {
		t.Errorf("expected total=200, got %v", first["total"])
	}

	second := resp.Categories[1]
	if second["name"] != "Food" {
		t.Errorf("expected second category 'Food', got %v", second["name"])
	}
	if second["total"].(float64) != 150.0 {
		t.Errorf("expected total=150, got %v", second["total"])
	}
}

func TestHandleDashboardCategories_DefaultsToCurrentMonth(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/categories", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleDashboardCategories(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Categories []map[string]any `json:"categories"`
	}
	decodeResponse(t, rec, &resp)
	// Should return successfully even with no data
	if resp.Categories == nil {
		t.Error("expected categories array (even if empty)")
	}
}

func TestHandleDashboardCategories_NoAuth_Returns401(t *testing.T) {
	h := setupHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/categories", nil)
	rec := httptest.NewRecorder()

	h.handleDashboardCategories(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestHandleDashboardCategories_InvalidYear_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/categories?year=abc", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleDashboardCategories(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// --- Phase 2.1 soft-delete invariant: dashboard read paths must hide tombstoned rows ---

func TestHandleDashboardSummary_HidesTombstoned(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	// 1 live expense, 1 tombstoned expense, 1 live income in April 2026.
	// The tombstoned 999 expense would blow total_spent sky-high if the
	// soft-delete filter is ever dropped from SumExpensesByMonth.
	seedTestTransaction(t, q, user.ID, 1, "2026-04-06", 50.0, "live expense")
	seedTombstonedTestTransaction(t, q, user.ID, 1, "2026-04-07", 999.0, "tombstoned expense")
	seedTestTransaction(t, q, user.ID, 15, "2026-04-01", 3000.0, "Salary")

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/summary?year=2026&month=4", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleDashboardSummary(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)

	if got := resp["total_spent"].(float64); got != 50.0 {
		t.Errorf("total_spent=%v, want 50 (tombstoned 999 must be excluded)", got)
	}
	if got := resp["total_income"].(float64); got != 3000.0 {
		t.Errorf("total_income=%v, want 3000", got)
	}
	if got := resp["savings_this_month"].(float64); got != 2950.0 {
		t.Errorf("savings_this_month=%v, want 2950", got)
	}
}

func TestHandleDashboardCategories_HidesTombstoned(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	// Category 1 = Food (expense). One live row at 100, one tombstoned at
	// 999. If the JOIN filter is dropped the tombstoned row will inflate the
	// Food total (or appear as a second row).
	seedTestTransaction(t, q, user.ID, 1, "2026-04-06", 100.0, "live")
	seedTombstonedTestTransaction(t, q, user.ID, 1, "2026-04-07", 999.0, "tombstoned")

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/categories?year=2026&month=4", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleDashboardCategories(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Categories []map[string]any `json:"categories"`
	}
	decodeResponse(t, rec, &resp)

	if len(resp.Categories) != 1 {
		t.Fatalf("categories len=%d, want 1 (only Food with live row)", len(resp.Categories))
	}
	if got := resp.Categories[0]["total"].(float64); got != 100.0 {
		t.Errorf("Food total=%v, want 100 (tombstoned 999 must be excluded)", got)
	}
}

func TestHandleDashboardTrend_HidesTombstoned(t *testing.T) {
	q, db := setupTestDB(t)
	// Pin "now" to April 15, 2026 so the ?months=1 trend window covers the
	// April-dated rows below regardless of the wall clock at test time.
	// See helpers.go:38 for the fixedClock pattern.
	h := NewHandlerWithClock(q, db, fixedClock{t: time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)})
	user := seedTestUser(t, q, "alice", "member")

	seedTestTransaction(t, q, user.ID, 1, "2026-04-06", 100.0, "live")
	seedTombstonedTestTransaction(t, q, user.ID, 1, "2026-04-07", 999.0, "tombstoned")
	seedTestTransaction(t, q, user.ID, 15, "2026-04-01", 3000.0, "income")

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/trend?months=1", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleDashboardTrend(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Trend []map[string]any `json:"trend"`
	}
	decodeResponse(t, rec, &resp)
	if len(resp.Trend) != 1 {
		t.Fatalf("trend len=%d, want 1", len(resp.Trend))
	}
	entry := resp.Trend[0]
	if got := entry["total_spent"].(float64); got != 100.0 {
		t.Errorf("total_spent=%v, want 100 (tombstoned 999 must be excluded)", got)
	}
	if got := entry["total_income"].(float64); got != 3000.0 {
		t.Errorf("total_income=%v, want 3000", got)
	}
}
