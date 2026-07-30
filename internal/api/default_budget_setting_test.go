package api

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/elienop/spendrop/internal/database"
)

// The four handlers below all resolve a monthly budget the same way: an
// explicit budgets row, else the default_budget app_settings value, else zero.
// The setting is a free-text column, and the fallback parsed it with
// strconv.ParseFloat and handed the result straight to the unguarded
// dollarsToCents.
//
// ParseFloat accepts "NaN", "Inf" and "-Inf" — the JSON decoder that guards the
// PUT side cannot produce any of them, but the column can hold them from a
// hand-edited database, a restored backup or an older build. int64(NaN * 100)
// is undefined in Go and lands on int64 minimum (-9223372036854775808) on
// amd64, so the budget surfaced as roughly -9.2e16 dollars and every derived
// figure (remaining, pace) went with it.
//
// The fix routes all four through safeDollarsToCents and falls back to the
// documented default of zero, logging the rejected value. It is admin-only and
// cosmetic, but it is genuinely reachable in a way the JSON-body handlers are
// not.

// badDefaultBudgetValues are the strings ParseFloat accepts and
// dollarsToCents cannot represent.
var badDefaultBudgetValues = []string{"NaN", "Inf", "+Inf", "-Inf", "1e308", "-1e308"}

func seedDefaultBudget(t *testing.T, q *database.Queries, value string) {
	t.Helper()
	if err := q.UpsertSetting(context.Background(), database.UpsertSettingParams{
		Key:   SettingDefaultBudget,
		Value: value,
	}); err != nil {
		t.Fatalf("seed default_budget=%q: %v", value, err)
	}
}

// assertSaneBudget fails if the reported budget is anywhere near int64 minimum
// in dollars, which is what an unguarded conversion produces.
func assertSaneBudget(t *testing.T, label string, got float64) {
	t.Helper()
	if got != 0 {
		t.Errorf("%s: budget = %v, want the documented fallback 0 — an "+
			"unrepresentable default_budget must not reach the wire", label, got)
	}
	if math.Abs(got) > MaxTransactionAmount {
		t.Errorf("%s: budget = %v, which is past MaxTransactionAmount (%v) — "+
			"dollarsToCents laundered a non-finite setting into int64 minimum",
			label, got, MaxTransactionAmount)
	}
}

func TestHandleDashboardSummary_UnrepresentableDefaultBudgetFallsBackToZero(t *testing.T) {
	for _, value := range badDefaultBudgetValues {
		t.Run(value, func(t *testing.T) {
			q, db := setupTestDB(t)
			h := NewHandler(q, db)
			user := seedTestUser(t, q, "alice", "member")
			seedDefaultBudget(t, q, value)

			req := httptest.NewRequest(http.MethodGet, "/api/dashboard/summary?year=2026&month=4", nil)
			req = withUser(req, user)
			rec := httptest.NewRecorder()
			h.handleDashboardSummary(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("got %d, want 200; body: %s", rec.Code, rec.Body.String())
			}
			var resp map[string]any
			decodeResponse(t, rec, &resp)
			budget, ok := resp["budget"].(float64)
			if !ok {
				t.Fatalf("budget missing or not a number: %#v", resp["budget"])
			}
			assertSaneBudget(t, "dashboard summary", budget)

			// remaining is derived from the same cents value, so a laundered
			// budget poisons it too.
			remaining, ok := resp["remaining"].(float64)
			if !ok {
				t.Fatalf("remaining missing or not a number: %#v", resp["remaining"])
			}
			if math.Abs(remaining) > MaxTransactionAmount {
				t.Errorf("remaining = %v, past MaxTransactionAmount — the garbage budget "+
					"propagated into the derived figure", remaining)
			}
		})
	}
}

func TestHandleBudgetVsActual_UnrepresentableDefaultBudgetFallsBackToZero(t *testing.T) {
	for _, value := range badDefaultBudgetValues {
		t.Run(value, func(t *testing.T) {
			q, db := setupTestDB(t)
			h := NewHandler(q, db)
			user := seedTestUser(t, q, "alice", "member")
			seedDefaultBudget(t, q, value)

			req := httptest.NewRequest(http.MethodGet, "/api/reports/budget-vs-actual?year=2026", nil)
			req = withUser(req, user)
			rec := httptest.NewRecorder()
			h.handleBudgetVsActual(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("got %d, want 200; body: %s", rec.Code, rec.Body.String())
			}
			var resp map[string]any
			decodeResponse(t, rec, &resp)
			data, ok := resp["data"].([]any)
			if !ok || len(data) == 0 {
				t.Fatalf("data missing: %#v", resp["data"])
			}
			for _, row := range data {
				month, ok := row.(map[string]any)
				if !ok {
					t.Fatalf("row is not an object: %#v", row)
				}
				budget, ok := month["budget"].(float64)
				if !ok {
					t.Fatalf("budget missing or not a number: %#v", month["budget"])
				}
				assertSaneBudget(t, "budget-vs-actual", budget)
			}
		})
	}
}

func TestHandleExpenseVelocity_UnrepresentableDefaultBudgetFallsBackToZero(t *testing.T) {
	for _, value := range badDefaultBudgetValues {
		t.Run(value, func(t *testing.T) {
			q, db := setupTestDB(t)
			h := NewHandler(q, db)
			user := seedTestUser(t, q, "alice", "member")
			seedDefaultBudget(t, q, value)

			req := httptest.NewRequest(http.MethodGet, "/api/reports/expense-velocity?year=2026&month=4", nil)
			req = withUser(req, user)
			rec := httptest.NewRecorder()
			h.handleExpenseVelocity(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("got %d, want 200; body: %s", rec.Code, rec.Body.String())
			}
			var resp map[string]any
			decodeResponse(t, rec, &resp)
			budget, ok := resp["budget"].(float64)
			if !ok {
				t.Fatalf("budget missing or not a number: %#v", resp["budget"])
			}
			assertSaneBudget(t, "expense velocity", budget)
		})
	}
}

func TestHandleHomepageSummary_UnrepresentableDefaultBudgetFallsBackToZero(t *testing.T) {
	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	for _, value := range badDefaultBudgetValues {
		t.Run(value, func(t *testing.T) {
			q, db := setupTestDB(t)
			h := NewHandlerWithClock(q, db, fixedClock{t: now})
			router := newHomepageTestRouter(t, h, q)
			_, bearer := seedUserWithLiveToken(t, q, "alice")
			seedDefaultBudget(t, q, value)

			rec := homepageRequest(t, router, bearer)
			if rec.Code != http.StatusOK {
				t.Fatalf("got %d, want 200; body: %s", rec.Code, rec.Body.String())
			}
			// Decode into a map: a typed decode would zero-fill a missing key
			// and hide the very field under test.
			var resp map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			budget, ok := resp["month_budget"].(float64)
			if !ok {
				t.Fatalf("month_budget missing or not a number: %#v", resp["month_budget"])
			}
			assertSaneBudget(t, "homepage summary", budget)

			remaining, ok := resp["month_remaining"].(float64)
			if !ok {
				t.Fatalf("month_remaining missing or not a number: %#v", resp["month_remaining"])
			}
			if math.Abs(remaining) > MaxTransactionAmount {
				t.Errorf("month_remaining = %v, past MaxTransactionAmount — the garbage "+
					"budget propagated into the derived figure", remaining)
			}
		})
	}
}

// TestHandleDefaultBudget_StillReadsAGoodValue is the control. Without it the
// tests above would pass just as well if the fallback started ignoring every
// setting, good or bad.
func TestHandleDefaultBudget_StillReadsAGoodValue(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	seedDefaultBudget(t, q, "2500.75")

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/summary?year=2026&month=4", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleDashboardSummary(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	decodeResponse(t, rec, &resp)
	if got := resp["budget"].(float64); got != 2500.75 {
		t.Errorf("budget = %v, want 2500.75 — the guard is rejecting legitimate values", got)
	}
}
