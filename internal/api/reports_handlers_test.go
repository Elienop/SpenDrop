package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
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
