package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elienop/spendrop/internal/database"
)

func TestHandleBudgetVsActual_Default(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	// Seed a budget for Jan 2026
	q.UpsertBudget(context.Background(), database.UpsertBudgetParams{
		Year: 2026, Month: 1, AmountCents: dollarsToCents(3000),
	})

	// Seed an expense transaction in Jan 2026
	cat := seedTestCategory(t, q, "TestFood", "expense")
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
	cat := seedTestCategory(t, q, "TestFood", "expense")

	// Seed transactions in Jan 2026
	for _, day := range []string{"2026-01-05", "2026-01-10", "2026-01-15"} {
		seedTestTransaction(t, q, user.ID, cat.ID, day, 100, "test")
	}

	// Seed a budget
	q.UpsertBudget(context.Background(), database.UpsertBudgetParams{
		Year: 2026, Month: 1, AmountCents: dollarsToCents(3000),
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

// --- Phase 2.1 soft-delete invariant: expense-velocity must hide tombstoned rows ---

func TestHandleExpenseVelocity_HidesTombstoned(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	// Expense-type category is required: SumExpensesByDayInMonth joins
	// categories and filters c.type='expense', so a non-expense sentinel would
	// be excluded for the wrong reason and the test would pass vacuously.
	cat := seedTestCategory(t, q, "VelFood", "expense")

	// One live $40 row on Jan 5, one tombstoned sentinel $999 on Jan 6.
	// If the deleted_at filter is ever dropped from SumExpensesByDayInMonth,
	// a second daily entry (day=6, daily_total=999) appears and the assertions
	// below fail loudly.
	seedTestTransaction(t, q, user.ID, cat.ID, "2026-01-05", 40, "live")
	seedTombstonedTestTransaction(t, q, user.ID, cat.ID, "2026-01-06", 999, "tombstoned")

	req := httptest.NewRequest(http.MethodGet, "/api/reports/expense-velocity?year=2026&month=1", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleExpenseVelocity(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	current := resp["current"].([]any)
	if len(current) != 1 {
		t.Fatalf("expected 1 daily entry (tombstoned day excluded), got %d: %v", len(current), current)
	}
	day := current[0].(map[string]any)
	if got := day["daily_total"].(float64); got != 40 {
		t.Errorf("daily_total=%v, want 40 (tombstoned 999 must be excluded)", got)
	}
	for _, e := range current {
		if e.(map[string]any)["daily_total"].(float64) == 999 {
			t.Errorf("velocity aggregate leaked tombstoned sentinel 999: %v", e)
		}
	}
}

func TestHandleSpendingHeatmap_Default(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "TestFood", "expense")

	seedTestTransaction(t, q, user.ID, cat.ID, "2026-03-15", 50, "lunch")
	seedTestTransaction(t, q, user.ID, cat.ID, "2026-03-15", 30, "coffee")

	req := httptest.NewRequest(http.MethodGet, "/api/reports/spending-heatmap?year=2026", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleSpendingHeatmap(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	data := resp["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("expected 1 day with spending, got %d", len(data))
	}
	day := data[0].(map[string]any)
	if day["total"].(float64) != 80 {
		t.Errorf("expected total 80, got %v", day["total"])
	}
}

func TestHandleRecurring_DetectsRecurring(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "Subscriptions", "expense")

	// Netflix appears in 4 months — should be detected
	for _, m := range []string{"01", "02", "03", "04"} {
		seedTestTransaction(t, q, user.ID, cat.ID, fmt.Sprintf("2026-%s-15", m), 15, "Netflix")
	}
	// Random expense appears in 2 months — should NOT be detected
	for _, m := range []string{"01", "03"} {
		seedTestTransaction(t, q, user.ID, cat.ID, fmt.Sprintf("2026-%s-10", m), 50, "Random Store")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/reports/recurring?year=2026", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleRecurring(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	data := resp["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("expected 1 recurring entry (Netflix), got %d", len(data))
	}
	entry := data[0].(map[string]any)
	if entry["description"] != "Netflix" {
		t.Errorf("expected Netflix, got %v", entry["description"])
	}
	if entry["month_count"].(float64) != 4 {
		t.Errorf("expected 4 months, got %v", entry["month_count"])
	}
}

func TestHandleDismissRecurring(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "Subscriptions", "expense")

	// Seed Netflix in 3 months
	for _, m := range []string{"01", "02", "03"} {
		seedTestTransaction(t, q, user.ID, cat.ID, fmt.Sprintf("2026-%s-15", m), 15, "Netflix")
	}

	// Dismiss it
	body := strings.NewReader(`{"year":2026,"description":"Netflix"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/reports/recurring/dismiss", body)
	req.Header.Set("Content-Type", "application/json")
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleDismissRecurring(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("dismiss: expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	// GET recurring should now return empty
	req2 := httptest.NewRequest(http.MethodGet, "/api/reports/recurring?year=2026", nil)
	req2 = withUser(req2, user)
	rec2 := httptest.NewRecorder()

	h.handleRecurring(rec2, req2)
	var resp map[string]any
	decodeResponse(t, rec2, &resp)
	data := resp["data"].([]any)
	if len(data) != 0 {
		t.Errorf("expected 0 after dismiss, got %d", len(data))
	}
}

func TestHandleDismissRecurring_DescriptionTooLong_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	longDesc := strings.Repeat("x", 501)
	body := strings.NewReader(`{"year":2026,"description":"` + longDesc + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/reports/recurring/dismiss", body)
	req.Header.Set("Content-Type", "application/json")
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleDismissRecurring(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for description > 500 chars, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleTagBreakdown_GroupsByTag(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "TestFood", "expense")

	// Two transactions sharing tag "groceries", one with "eating-out"
	seedTestTransactionWithTags(t, q, user.ID, cat.ID, "2026-03-10", 100, "Store A", "groceries,weekly")
	seedTestTransactionWithTags(t, q, user.ID, cat.ID, "2026-03-15", 50, "Restaurant", "eating-out")
	seedTestTransactionWithTags(t, q, user.ID, cat.ID, "2026-03-20", 80, "Store B", "groceries")

	req := httptest.NewRequest(http.MethodGet, "/api/reports/tag-breakdown?year=2026&month=3", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleTagBreakdown(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	data := resp["data"].([]any)
	if len(data) != 3 { // groceries, weekly, eating-out
		t.Fatalf("expected 3 tags, got %d", len(data))
	}

	// Find groceries — should be 180 (100 + 80)
	for _, item := range data {
		tag := item.(map[string]any)
		if tag["tag"] == "groceries" {
			if tag["total"].(float64) != 180 {
				t.Errorf("groceries total: expected 180, got %v", tag["total"])
			}
			if tag["count"].(float64) != 2 {
				t.Errorf("groceries count: expected 2, got %v", tag["count"])
			}
		}
	}
}

func TestHandleTagBreakdown_YTD(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "TestFood", "expense")

	seedTestTransactionWithTags(t, q, user.ID, cat.ID, "2026-01-10", 100, "Jan", "groceries")
	seedTestTransactionWithTags(t, q, user.ID, cat.ID, "2026-06-10", 200, "Jun", "groceries")

	// month=0 means YTD
	req := httptest.NewRequest(http.MethodGet, "/api/reports/tag-breakdown?year=2026&month=0", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleTagBreakdown(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	data := resp["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("expected 1 tag, got %d", len(data))
	}
	tag := data[0].(map[string]any)
	if tag["total"].(float64) != 300 {
		t.Errorf("expected YTD total 300, got %v", tag["total"])
	}
}

// --- Phase 2.1 soft-delete invariant: tag/heatmap/recurring/budget read paths must hide tombstoned rows ---

func TestHandleTagBreakdown_HidesTombstoned(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "TestFood", "expense")

	// Two live "groceries" rows (100 + 80 = 180) plus a tombstoned row with
	// the same tag and amount 999. If the filter is dropped, the tag total
	// becomes 1179 or the tag count goes from 2 to 3.
	seedTestTransactionWithTags(t, q, user.ID, cat.ID, "2026-03-10", 100, "Store A", "groceries")
	seedTestTransactionWithTags(t, q, user.ID, cat.ID, "2026-03-20", 80, "Store B", "groceries")
	tombstoned := seedTestTransactionWithTags(t, q, user.ID, cat.ID, "2026-03-15", 999, "Ghost", "groceries")
	if err := q.SoftDeleteTransaction(context.Background(), tombstoned.ID); err != nil {
		t.Fatalf("soft-delete: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/reports/tag-breakdown?year=2026&month=3", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleTagBreakdown(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	data := resp["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("expected 1 tag (groceries), got %d", len(data))
	}
	tag := data[0].(map[string]any)
	if tag["tag"] != "groceries" {
		t.Errorf("tag=%v, want groceries", tag["tag"])
	}
	if got := tag["total"].(float64); got != 180.0 {
		t.Errorf("groceries total=%v, want 180 (tombstoned 999 must be excluded)", got)
	}
	if got := tag["count"].(float64); got != 2 {
		t.Errorf("groceries count=%v, want 2 (tombstoned row must be excluded)", got)
	}
}

func TestHandleSpendingHeatmap_HidesTombstoned(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "TestFood", "expense")

	seedTestTransaction(t, q, user.ID, cat.ID, "2026-03-15", 50, "live")
	seedTombstonedTestTransaction(t, q, user.ID, cat.ID, "2026-03-15", 999, "tombstoned")

	req := httptest.NewRequest(http.MethodGet, "/api/reports/spending-heatmap?year=2026", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleSpendingHeatmap(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	data := resp["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("expected 1 day with spending, got %d", len(data))
	}
	day := data[0].(map[string]any)
	if got := day["total"].(float64); got != 50 {
		t.Errorf("day total=%v, want 50 (tombstoned 999 must be excluded)", got)
	}
}

func TestHandleBudgetVsActual_HidesTombstoned(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	q.UpsertBudget(context.Background(), database.UpsertBudgetParams{
		Year: 2026, Month: 1, AmountCents: dollarsToCents(3000),
	})
	cat := seedTestCategory(t, q, "TestFood", "expense")
	seedTestTransaction(t, q, user.ID, cat.ID, "2026-01-15", 1200, "live")
	seedTombstonedTestTransaction(t, q, user.ID, cat.ID, "2026-01-16", 999, "tombstoned")

	req := httptest.NewRequest(http.MethodGet, "/api/reports/budget-vs-actual?year=2026", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleBudgetVsActual(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	data := resp["data"].([]any)
	jan := data[0].(map[string]any)
	if got := jan["actual"].(float64); got != 1200 {
		t.Errorf("Jan actual=%v, want 1200 (tombstoned 999 must be excluded)", got)
	}
}

func TestHandleRecurring_HidesTombstoned(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "Subscriptions", "expense")

	// Netflix appears in only 2 months live (3rd month is tombstoned).
	// The recurring detector needs at least 3 months — so if the filter is
	// dropped, Netflix will qualify. If the filter holds, Netflix will not.
	seedTestTransaction(t, q, user.ID, cat.ID, "2026-01-15", 15, "Netflix")
	seedTestTransaction(t, q, user.ID, cat.ID, "2026-02-15", 15, "Netflix")
	seedTombstonedTestTransaction(t, q, user.ID, cat.ID, "2026-03-15", 15, "Netflix")

	req := httptest.NewRequest(http.MethodGet, "/api/reports/recurring?year=2026", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleRecurring(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	data := resp["data"].([]any)
	if len(data) != 0 {
		t.Errorf("expected 0 recurring entries (Netflix had only 2 live months), got %d", len(data))
	}
}
