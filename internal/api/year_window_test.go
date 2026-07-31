package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elienop/spendrop/internal/database"
)

// --- The year-window contract ---
//
// Two windows, two jobs (internal/api/limits.go):
//
//   - [MinDataYear, MaxDataYear] bounds a transaction DATE. Every endpoint
//     that READS transaction data by year must accept the whole of it, or the
//     ledger can hold rows no report can display.
//   - [PlanningMinYear, PlanningMaxYear] bounds the budget / savings-goal year
//     params. Those are configuration, not data, and stay narrow.
//
// The tests below pin both halves. Widening MinDataYear without repointing a
// read endpoint is silent — the endpoint just 400s on a year the picker is
// entitled to offer — so every one of the ten sites is driven here.

// yearWindowHandler builds a handler with a frozen 2026 clock plus one seeded
// member, so the year-param cases below never depend on the wall clock.
func yearWindowHandler(t *testing.T) (*Handler, *database.Queries, database.User) {
	t.Helper()
	q, db := setupTestDB(t)
	h := NewHandlerWithClock(q, db, fixedClock{t: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)})
	user := seedTestUser(t, q, "alice", "member")
	return h, q, user
}

// TestYearParamEndpoints_AcceptLedgerYears drives every endpoint that
// validates a year against the DATA window at the oldest year the ledger may
// hold. A 400 here means the write side can store a row the read side refuses
// to report on.
//
// Not vacuous: each case asserts != 400 specifically, and every one of these
// handlers returns 400 for an out-of-window year today, so the whole table
// fails before the constants are split.
func TestYearParamEndpoints_AcceptLedgerYears(t *testing.T) {
	const boundary = MinDataYear

	cases := []struct {
		name string
		// drive issues the request against a handler and returns the status.
		drive func(t *testing.T, h *Handler, user database.User) *httptest.ResponseRecorder
	}{
		{
			name: "reports/year-over-year",
			drive: func(t *testing.T, h *Handler, user database.User) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/reports/year-over-year?year=%d", boundary), nil)
				rec := httptest.NewRecorder()
				h.handleReportYoY(rec, withUser(req, user))
				return rec
			},
		},
		{
			name: "reports/budget-vs-actual",
			drive: func(t *testing.T, h *Handler, user database.User) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/reports/budget-vs-actual?year=%d", boundary), nil)
				rec := httptest.NewRecorder()
				h.handleBudgetVsActual(rec, withUser(req, user))
				return rec
			},
		},
		{
			name: "reports/spending-heatmap",
			drive: func(t *testing.T, h *Handler, user database.User) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/reports/spending-heatmap?year=%d", boundary), nil)
				rec := httptest.NewRecorder()
				h.handleSpendingHeatmap(rec, withUser(req, user))
				return rec
			},
		},
		{
			name: "reports/recurring",
			drive: func(t *testing.T, h *Handler, user database.User) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/reports/recurring?year=%d", boundary), nil)
				rec := httptest.NewRecorder()
				h.handleRecurring(rec, withUser(req, user))
				return rec
			},
		},
		{
			name: "reports/recurring/dismiss",
			drive: func(t *testing.T, h *Handler, user database.User) *httptest.ResponseRecorder {
				body := strings.NewReader(fmt.Sprintf(`{"year":%d,"description":"Netflix"}`, boundary))
				req := httptest.NewRequest(http.MethodPost, "/api/reports/recurring/dismiss", body)
				rec := httptest.NewRecorder()
				h.handleDismissRecurring(rec, withUser(req, user))
				return rec
			},
		},
		{
			name: "reports/tag-breakdown",
			drive: func(t *testing.T, h *Handler, user database.User) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/reports/tag-breakdown?year=%d", boundary), nil)
				rec := httptest.NewRecorder()
				h.handleTagBreakdown(rec, withUser(req, user))
				return rec
			},
		},
		{
			name: "dashboard/summary",
			drive: func(t *testing.T, h *Handler, user database.User) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/dashboard/summary?year=%d&month=6", boundary), nil)
				rec := httptest.NewRecorder()
				h.handleDashboardSummary(rec, withUser(req, user))
				return rec
			},
		},
		{
			name: "dashboard/categories",
			drive: func(t *testing.T, h *Handler, user database.User) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/dashboard/categories?year=%d&month=6", boundary), nil)
				rec := httptest.NewRecorder()
				h.handleDashboardCategories(rec, withUser(req, user))
				return rec
			},
		},
		{
			name: "export/monthly",
			drive: func(t *testing.T, h *Handler, user database.User) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/export/monthly/%d/06", boundary), nil)
				req = withUserAndURLParams(req, user, map[string]string{"year": fmt.Sprintf("%d", boundary), "month": "06"})
				rec := httptest.NewRecorder()
				h.handleExportMonthly(rec, req)
				return rec
			},
		},
		{
			name: "export/yearly",
			drive: func(t *testing.T, h *Handler, user database.User) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/export/yearly/%d", boundary), nil)
				req = withUserAndURLParam(req, user, "year", fmt.Sprintf("%d", boundary))
				rec := httptest.NewRecorder()
				h.handleExportYearly(rec, req)
				return rec
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _, user := yearWindowHandler(t)
			rec := tc.drive(t, h, user)
			if rec.Code == http.StatusBadRequest {
				t.Errorf("year=%d returned 400 — the ledger may hold %d rows, so this endpoint must report on them; body: %s",
					boundary, boundary, rec.Body.String())
			}
		})
	}
}

// TestDashboardTrend_AnchorsAtLedgerYears covers the tenth site, which does
// NOT 400: handleDashboardTrend silently falls back to the current month when
// the year/month anchor fails validation. A `!= 400` assertion there would be
// vacuous, so this asserts the anchor was actually honoured — the returned
// bucket must be the requested month, not the clock's.
func TestDashboardTrend_AnchorsAtLedgerYears(t *testing.T) {
	h, _, user := yearWindowHandler(t)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/dashboard/trend?year=%d&month=6&months=1", MinDataYear), nil)
	rec := httptest.NewRecorder()
	h.handleDashboardTrend(rec, withUser(req, user))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Trend []struct {
			Year  int `json:"year"`
			Month int `json:"month"`
		} `json:"trend"`
	}
	decodeResponse(t, rec, &resp)

	if len(resp.Trend) != 1 {
		t.Fatalf("expected 1 trend bucket, got %d", len(resp.Trend))
	}
	if resp.Trend[0].Year != MinDataYear || resp.Trend[0].Month != 6 {
		t.Errorf("trend anchored at %d-%02d, want %d-06 — an out-of-window anchor is dropped silently, "+
			"so the tab renders the current month while claiming to show %d",
			resp.Trend[0].Year, resp.Trend[0].Month, MinDataYear, MinDataYear)
	}
}

// TestPlanningEndpoints_RejectPreMinYear proves the split is a split and not a
// blanket widening. Budgets and savings goals are configuration rows keyed by
// year, not ledger data: nothing in the product plans a 1999 budget, and the
// Budgets page's own year Select bottoms out at MIN_YEAR (web/src/lib/dates.ts).
// If this test ever goes green-by-accident because someone pointed these at the
// data window, the Budgets page silently gains a century of empty years.
func TestPlanningEndpoints_RejectPreMinYear(t *testing.T) {
	const belowPlanning = PlanningMinYear - 1 // 1999: inside the DATA window, outside planning

	if belowPlanning < MinDataYear {
		t.Fatalf("test premise broken: %d must sit INSIDE the data window [%d, %d] "+
			"or this proves nothing about the split", belowPlanning, MinDataYear, MaxDataYear)
	}

	year := fmt.Sprintf("%d", belowPlanning)

	cases := []struct {
		name  string
		admin bool
		drive func(t *testing.T, h *Handler, user database.User) *httptest.ResponseRecorder
	}{
		{
			name: "GET /budgets?year=",
			drive: func(t *testing.T, h *Handler, user database.User) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodGet, "/api/budgets?year="+year, nil)
				rec := httptest.NewRecorder()
				h.handleGetBudgets(rec, withUser(req, user))
				return rec
			},
		},
		{
			name:  "PUT /budgets/{year}/{month}",
			admin: true,
			drive: func(t *testing.T, h *Handler, user database.User) *httptest.ResponseRecorder {
				body := strings.NewReader(`{"amount":100}`)
				req := httptest.NewRequest(http.MethodPut, "/api/budgets/"+year+"/1", body)
				req = withUserAndURLParams(req, user, map[string]string{"year": year, "month": "1"})
				rec := httptest.NewRecorder()
				h.handleSetBudget(rec, req)
				return rec
			},
		},
		{
			name: "GET /category-budgets?year=",
			drive: func(t *testing.T, h *Handler, user database.User) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodGet, "/api/category-budgets?year="+year+"&month=1", nil)
				rec := httptest.NewRecorder()
				h.handleGetCategoryBudgets(rec, withUser(req, user))
				return rec
			},
		},
		{
			name:  "PUT /category-budgets/{year}/{month}/{categoryId}",
			admin: true,
			drive: func(t *testing.T, h *Handler, user database.User) *httptest.ResponseRecorder {
				body := strings.NewReader(`{"amount":100}`)
				req := httptest.NewRequest(http.MethodPut, "/api/category-budgets/"+year+"/1/1", body)
				req = withUserAndURLParams(req, user, map[string]string{"year": year, "month": "1", "categoryId": "1"})
				rec := httptest.NewRecorder()
				h.handleSetCategoryBudget(rec, req)
				return rec
			},
		},
		{
			name:  "PUT /savings-goals/{year}",
			admin: true,
			drive: func(t *testing.T, h *Handler, user database.User) *httptest.ResponseRecorder {
				body := strings.NewReader(`{"target_amount":100}`)
				req := httptest.NewRequest(http.MethodPut, "/api/savings-goals/"+year, body)
				req = withUserAndURLParam(req, user, "year", year)
				rec := httptest.NewRecorder()
				h.handleSetSavingsGoal(rec, req)
				return rec
			},
		},
		{
			name:  "DELETE /savings-goals/{year}",
			admin: true,
			drive: func(t *testing.T, h *Handler, user database.User) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodDelete, "/api/savings-goals/"+year, nil)
				req = withUserAndURLParam(req, user, "year", year)
				rec := httptest.NewRecorder()
				h.handleDeleteSavingsGoal(rec, req)
				return rec
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, q, member := yearWindowHandler(t)
			user := member
			if tc.admin {
				user = seedTestUser(t, q, "boss", RoleAdmin)
			}
			rec := tc.drive(t, h, user)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("year=%d returned %d, want 400 — planning years must stay in [%d, %d]; body: %s",
					belowPlanning, rec.Code, PlanningMinYear, PlanningMaxYear, rec.Body.String())
			}
		})
	}
}
