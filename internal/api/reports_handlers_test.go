package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHandleReportYoY_DefaultYear(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	req := httptest.NewRequest(http.MethodGet, "/api/reports/year-over-year", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleReportYoY(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	if resp["current_year"] == nil {
		t.Error("expected current_year in response")
	}
	cur, ok := resp["current"].([]any)
	if !ok || len(cur) != 12 {
		t.Errorf("expected 12 current months, got %v", resp["current"])
	}
	prev, ok := resp["previous"].([]any)
	if !ok || len(prev) != 12 {
		t.Errorf("expected 12 previous months, got %v", resp["previous"])
	}
}

func TestHandleReportYoY_InvalidYear(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	req := httptest.NewRequest(http.MethodGet, "/api/reports/year-over-year?year=abc", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleReportYoY(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleReportYoY_Unauthorized(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)

	req := httptest.NewRequest(http.MethodGet, "/api/reports/year-over-year", nil)
	rec := httptest.NewRecorder()

	h.handleReportYoY(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestHandleReportCategoryTrends_Default(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	req := httptest.NewRequest(http.MethodGet, "/api/reports/category-trends", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleReportCategoryTrends(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	if resp["categories"] == nil {
		t.Error("expected categories in response")
	}
}

func TestHandleReportIncomeExpenses_Default(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	req := httptest.NewRequest(http.MethodGet, "/api/reports/income-expenses", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleReportIncomeExpenses(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	data, ok := resp["data"].([]any)
	if !ok || len(data) != 12 {
		t.Errorf("expected 12 entries, got %v", resp["data"])
	}
}

func TestHandleReportTopMerchants_Default(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	req := httptest.NewRequest(http.MethodGet, "/api/reports/top-merchants", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleReportTopMerchants(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	if resp["merchants"] == nil {
		t.Error("expected merchants in response")
	}
}

func TestHandleReportTopMerchants_LimitCapped(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	req := httptest.NewRequest(http.MethodGet, "/api/reports/top-merchants?limit=999", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleReportTopMerchants(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

// --- Phase 2.1 soft-delete invariant: report read paths must hide tombstoned rows ---

func TestHandleReportYoY_HidesTombstoned(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	// Live expense 100 in April 2026, tombstoned expense 999 same month.
	// The YoY handler groups expenses by month; if the filter is dropped
	// the 999 row leaks into the current-year April bucket.
	seedTestTransaction(t, q, user.ID, 1, "2026-04-06", 100.0, "live")
	seedTombstonedTestTransaction(t, q, user.ID, 1, "2026-04-07", 999.0, "tombstoned")

	req := httptest.NewRequest(http.MethodGet, "/api/reports/year-over-year?year=2026", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleReportYoY(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	cur := resp["current"].([]any)
	if len(cur) != 12 {
		t.Fatalf("current len=%d, want 12", len(cur))
	}
	// Index 3 = April (current is a 12-element array in month order).
	april := cur[3].(map[string]any)
	if got := april["expenses"].(float64); got != 100.0 {
		t.Errorf("April expenses=%v, want 100 (tombstoned 999 must be excluded)", got)
	}
}

func TestHandleReportTopMerchants_HidesTombstoned(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	// handleReportTopMerchants defaults to the current calendar month via
	// parseYearMonth, so seed for `now` rather than a fixed date — otherwise
	// the tombstoned row could land outside the query window for unrelated
	// reasons and the assertion would pass by coincidence.
	now := time.Now()
	y, m := now.Year(), int(now.Month())
	liveDate := fmt.Sprintf("%04d-%02d-05", y, m)
	tombDate := fmt.Sprintf("%04d-%02d-06", y, m)

	seedTestTransaction(t, q, user.ID, 1, liveDate, 50.0, "live merchant")
	seedTombstonedTestTransaction(t, q, user.ID, 1, tombDate, 999.0, "tombstoned leaker")

	req := httptest.NewRequest(http.MethodGet, "/api/reports/top-merchants", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleReportTopMerchants(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	merchants, ok := resp["merchants"].([]any)
	if !ok {
		t.Fatalf("merchants missing or wrong type: %v", resp["merchants"])
	}
	for _, item := range merchants {
		row := item.(map[string]any)
		if row["description"] == "tombstoned leaker" {
			t.Errorf("tombstoned merchant leaked into top-merchants response: %v", row)
		}
		if row["description"] == "live merchant" {
			if got := row["total"].(float64); got != 50.0 {
				t.Errorf("live merchant total=%v, want 50 (tombstoned 999 must not be summed in)", got)
			}
		}
	}
}

func TestHandleReportCategoryTrends_HidesTombstoned(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	// handleReportCategoryTrends uses a rolling 12-month window ending at
	// time.Now(), so seed the current calendar month — a fixed date would
	// risk falling outside the window once the test ages.
	now := time.Now()
	y, m := now.Year(), int(now.Month())
	liveDate := fmt.Sprintf("%04d-%02d-10", y, m)
	tombDate := fmt.Sprintf("%04d-%02d-11", y, m)

	seedTestTransaction(t, q, user.ID, 1, liveDate, 100.0, "live in cat 1")
	seedTombstonedTestTransaction(t, q, user.ID, 1, tombDate, 999.0, "tombstoned in cat 1")

	req := httptest.NewRequest(http.MethodGet, "/api/reports/category-trends", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleReportCategoryTrends(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	cats, ok := resp["categories"].([]any)
	if !ok {
		t.Fatalf("categories missing or wrong type: %v", resp["categories"])
	}
	// Locate category id=1 and its current-month entry. Other categories
	// may also surface (live total > 0 from other months), but only id=1
	// is exercised by this test seed.
	var found bool
	for _, c := range cats {
		cat := c.(map[string]any)
		if int(cat["id"].(float64)) != 1 {
			continue
		}
		data := cat["data"].([]any)
		for _, d := range data {
			entry := d.(map[string]any)
			if int(entry["year"].(float64)) == y && int(entry["month"].(float64)) == m {
				found = true
				if got := entry["total"].(float64); got != 100.0 {
					t.Errorf("cat 1 %d-%02d total=%v, want 100 (tombstoned 999 must be excluded)", y, m, got)
				}
			}
		}
	}
	if !found {
		t.Errorf("no current-month entry for category 1 in trends response: %v", cats)
	}
}

func TestHandleReportIncomeExpenses_HidesTombstoned(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	// The handler uses a rolling `months`-wide window ending at time.Now(),
	// so we have to seed data for the *current* month rather than a fixed
	// date. That way the assertions work regardless of when the suite runs.
	now := time.Now()
	y, m := now.Year(), int(now.Month())
	liveExpenseDate := fmt.Sprintf("%04d-%02d-06", y, m)
	tombExpenseDate := fmt.Sprintf("%04d-%02d-07", y, m)
	liveIncomeDate := fmt.Sprintf("%04d-%02d-01", y, m)
	tombIncomeDate := fmt.Sprintf("%04d-%02d-02", y, m)

	seedTestTransaction(t, q, user.ID, 1, liveExpenseDate, 100.0, "live expense")
	seedTombstonedTestTransaction(t, q, user.ID, 1, tombExpenseDate, 999.0, "tombstoned expense")
	seedTestTransaction(t, q, user.ID, 15, liveIncomeDate, 3000.0, "live income")
	seedTombstonedTestTransaction(t, q, user.ID, 15, tombIncomeDate, 888.0, "tombstoned income")

	req := httptest.NewRequest(http.MethodGet, "/api/reports/income-expenses", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleReportIncomeExpenses(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	data := resp["data"].([]any)
	if len(data) != 12 {
		t.Fatalf("data len=%d, want 12", len(data))
	}
	// The handler writes entries in chronological order ending at the
	// current month, so find the entry whose year/month match the seed
	// rather than trusting a fixed index.
	var current map[string]any
	for _, e := range data {
		entry := e.(map[string]any)
		if int(entry["year"].(float64)) == y && int(entry["month"].(float64)) == m {
			current = entry
			break
		}
	}
	if current == nil {
		t.Fatalf("no entry for current month %d-%02d in response: %v", y, m, data)
	}
	if got := current["expenses"].(float64); got != 100.0 {
		t.Errorf("current-month expenses=%v, want 100 (tombstoned 999 must be excluded)", got)
	}
	if got := current["income"].(float64); got != 3000.0 {
		t.Errorf("current-month income=%v, want 3000 (tombstoned 888 must be excluded)", got)
	}
}
