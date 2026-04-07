package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- handleListCurrencies ---

func TestHandleListCurrencies_ReturnsAllCurrencies(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	req := httptest.NewRequest(http.MethodGet, "/api/currencies", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleListCurrencies(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp []map[string]any
	decodeResponse(t, rec, &resp)
	// Seed data has 3 currencies: USD, LBP, EUR
	if len(resp) != 3 {
		t.Errorf("expected 3 currencies, got %d", len(resp))
	}
}

func TestHandleListCurrencies_NoAuth_Returns401(t *testing.T) {
	h := setupHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/currencies", nil)
	rec := httptest.NewRecorder()

	h.handleListCurrencies(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// --- handleCreateCurrency ---

func TestHandleCreateCurrency_AdminCanCreate(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	body := strings.NewReader(`{"code":"GBP","name":"British Pound","symbol":"\u00a3","rate_to_base":0.79}`)
	req := httptest.NewRequest(http.MethodPost, "/api/currencies", body)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()

	h.handleCreateCurrency(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	if resp["code"] != "GBP" {
		t.Errorf("expected code 'GBP', got %v", resp["code"])
	}
	if resp["name"] != "British Pound" {
		t.Errorf("expected name 'British Pound', got %v", resp["name"])
	}
}

func TestHandleCreateCurrency_MemberForbidden(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	member := seedTestUser(t, q, "alice", "member")

	body := strings.NewReader(`{"code":"GBP","name":"British Pound","symbol":"\u00a3","rate_to_base":0.79}`)
	req := httptest.NewRequest(http.MethodPost, "/api/currencies", body)
	req = withUser(req, member)
	rec := httptest.NewRecorder()

	h.handleCreateCurrency(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestHandleCreateCurrency_MissingCode_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	body := strings.NewReader(`{"name":"British Pound","symbol":"\u00a3","rate_to_base":0.79}`)
	req := httptest.NewRequest(http.MethodPost, "/api/currencies", body)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()

	h.handleCreateCurrency(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleCreateCurrency_InvalidJSON_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	body := strings.NewReader(`not json`)
	req := httptest.NewRequest(http.MethodPost, "/api/currencies", body)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()

	h.handleCreateCurrency(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// --- handleUpdateCurrency ---

func TestHandleUpdateCurrency_AdminCanUpdateRate(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	body := strings.NewReader(`{"rate_to_base":90000}`)
	req := httptest.NewRequest(http.MethodPut, "/api/currencies/LBP", body)
	req = withUserAndURLParam(req, admin, "code", "LBP")
	rec := httptest.NewRecorder()

	h.handleUpdateCurrency(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleUpdateCurrency_MemberForbidden(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	member := seedTestUser(t, q, "alice", "member")

	body := strings.NewReader(`{"rate_to_base":90000}`)
	req := httptest.NewRequest(http.MethodPut, "/api/currencies/LBP", body)
	req = withUserAndURLParam(req, member, "code", "LBP")
	rec := httptest.NewRecorder()

	h.handleUpdateCurrency(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestHandleUpdateCurrency_NotFound_Returns404(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	body := strings.NewReader(`{"rate_to_base":1.5}`)
	req := httptest.NewRequest(http.MethodPut, "/api/currencies/FAKE", body)
	req = withUserAndURLParam(req, admin, "code", "FAKE")
	rec := httptest.NewRecorder()

	h.handleUpdateCurrency(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d; body: %s", rec.Code, rec.Body.String())
	}
}
