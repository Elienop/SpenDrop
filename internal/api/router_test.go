package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewRouter_HealthEndpoint_Returns200(t *testing.T) {
	q, db := setupTestDB(t)
	router := NewRouter(q, db)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("expected status=ok, got %q", body["status"])
	}
}

func TestNewRouter_AuthRegister_Returns201(t *testing.T) {
	q, db := setupTestDB(t)
	router := NewRouter(q, db)

	body := strings.NewReader(`{"username":"alice","password":"longpassword","display_name":"Alice"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestNewRouter_AuthLogin_Returns200(t *testing.T) {
	q, db := setupTestDB(t)
	router := NewRouter(q, db)

	// Register first
	regBody := strings.NewReader(`{"username":"alice","password":"longpassword"}`)
	regReq := httptest.NewRequest(http.MethodPost, "/api/auth/register", regBody)
	regReq.Header.Set("Content-Type", "application/json")
	regRec := httptest.NewRecorder()
	router.ServeHTTP(regRec, regReq)
	if regRec.Code != http.StatusCreated {
		t.Fatalf("register failed: %d", regRec.Code)
	}

	// Login
	loginBody := strings.NewReader(`{"username":"alice","password":"longpassword"}`)
	loginReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", loginBody)
	loginReq.Header.Set("Content-Type", "application/json")
	loginRec := httptest.NewRecorder()
	router.ServeHTTP(loginRec, loginReq)

	if loginRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", loginRec.Code, loginRec.Body.String())
	}
}

func TestNewRouter_AuthLogout_Returns200(t *testing.T) {
	q, db := setupTestDB(t)
	router := NewRouter(q, db)

	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestNewRouter_AuthMe_WithoutSession_Returns401(t *testing.T) {
	q, db := setupTestDB(t)
	router := NewRouter(q, db)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestNewRouter_AuthMe_WithSession_Returns200(t *testing.T) {
	q, db := setupTestDB(t)
	router := NewRouter(q, db)

	// Register to get session cookie
	regBody := strings.NewReader(`{"username":"alice","password":"longpassword"}`)
	regReq := httptest.NewRequest(http.MethodPost, "/api/auth/register", regBody)
	regRec := httptest.NewRecorder()
	router.ServeHTTP(regRec, regReq)
	if regRec.Code != http.StatusCreated {
		t.Fatalf("register failed: %d", regRec.Code)
	}

	var sessionCookie *http.Cookie
	for _, c := range regRec.Result().Cookies() {
		if c.Name == "session" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("no session cookie from register")
	}

	// Call /me with session
	meReq := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	meReq.AddCookie(sessionCookie)
	meRec := httptest.NewRecorder()
	router.ServeHTTP(meRec, meReq)

	if meRec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d; body: %s", meRec.Code, meRec.Body.String())
	}
}

func TestNewRouter_ProtectedRoute_WithoutAuth_Returns401(t *testing.T) {
	q, db := setupTestDB(t)
	router := NewRouter(q, db)

	// Authenticated endpoints should require auth
	endpoints := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/transactions"},
		{http.MethodGet, "/api/categories"},
		{http.MethodGet, "/api/currencies"},
		{http.MethodGet, "/api/budgets"},
		{http.MethodGet, "/api/savings-goals"},
		{http.MethodGet, "/api/dashboard/summary"},
	}

	for _, ep := range endpoints {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			req := httptest.NewRequest(ep.method, ep.path, nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("expected 401, got %d; body: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestNewRouter_StubRoutes_ReturnNotImplemented(t *testing.T) {
	q, db := setupTestDB(t)
	router := NewRouter(q, db)

	// Register and get session cookie for authenticated requests
	regBody := strings.NewReader(`{"username":"alice","password":"longpassword"}`)
	regReq := httptest.NewRequest(http.MethodPost, "/api/auth/register", regBody)
	regRec := httptest.NewRecorder()
	router.ServeHTTP(regRec, regReq)

	var sessionCookie *http.Cookie
	for _, c := range regRec.Result().Cookies() {
		if c.Name == "session" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("no session cookie from register")
	}

	// Stub endpoints should return 501 Not Implemented when authed
	endpoints := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/transactions"},
		{http.MethodPost, "/api/transactions"},
		{http.MethodPost, "/api/transactions/batch"},
		{http.MethodGet, "/api/categories"},
		{http.MethodPost, "/api/categories"},
		{http.MethodPost, "/api/categories/reorder"},
		{http.MethodGet, "/api/currencies"},
		{http.MethodPost, "/api/currencies"},
		{http.MethodGet, "/api/budgets"},
		{http.MethodGet, "/api/savings-goals"},
		{http.MethodGet, "/api/dashboard/summary"},
		{http.MethodGet, "/api/dashboard/trend"},
		{http.MethodGet, "/api/dashboard/categories"},
		{http.MethodGet, "/api/settings/default-budget"},
		{http.MethodPut, "/api/settings/default-budget"},
	}

	for _, ep := range endpoints {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			req := httptest.NewRequest(ep.method, ep.path, nil)
			req.AddCookie(sessionCookie)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusNotImplemented {
				t.Errorf("expected 501, got %d; body: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestNewRouter_StubRoutes_WithURLParams_ReturnNotImplemented(t *testing.T) {
	q, db := setupTestDB(t)
	router := NewRouter(q, db)

	// Register and get session
	regBody := strings.NewReader(`{"username":"alice","password":"longpassword"}`)
	regReq := httptest.NewRequest(http.MethodPost, "/api/auth/register", regBody)
	regRec := httptest.NewRecorder()
	router.ServeHTTP(regRec, regReq)

	var sessionCookie *http.Cookie
	for _, c := range regRec.Result().Cookies() {
		if c.Name == "session" {
			sessionCookie = c
			break
		}
	}

	endpoints := []struct {
		method string
		path   string
	}{
		{http.MethodPut, "/api/transactions/123"},
		{http.MethodDelete, "/api/transactions/123"},
		{http.MethodPut, "/api/categories/5"},
		{http.MethodPatch, "/api/categories/5"},
		{http.MethodPut, "/api/currencies/EUR"},
		{http.MethodPut, "/api/budgets/2026/04"},
		{http.MethodPut, "/api/savings-goals/2026"},
	}

	for _, ep := range endpoints {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			req := httptest.NewRequest(ep.method, ep.path, nil)
			req.AddCookie(sessionCookie)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusNotImplemented {
				t.Errorf("expected 501, got %d; body: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestNewRouter_AdminRoutes_WithoutAuth_Returns401(t *testing.T) {
	q, db := setupTestDB(t)
	router := NewRouter(q, db)

	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestNewRouter_AdminRoutes_AsAdmin_Returns501(t *testing.T) {
	q, db := setupTestDB(t)
	router := NewRouter(q, db)

	// Register first user (admin)
	regBody := strings.NewReader(`{"username":"admin","password":"longpassword"}`)
	regReq := httptest.NewRequest(http.MethodPost, "/api/auth/register", regBody)
	regRec := httptest.NewRecorder()
	router.ServeHTTP(regRec, regReq)

	var sessionCookie *http.Cookie
	for _, c := range regRec.Result().Cookies() {
		if c.Name == "session" {
			sessionCookie = c
			break
		}
	}

	endpoints := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/users"},
		{http.MethodPost, "/api/users"},
		{http.MethodPut, "/api/users/1"},
		{http.MethodDelete, "/api/users/1"},
	}

	for _, ep := range endpoints {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			req := httptest.NewRequest(ep.method, ep.path, nil)
			req.AddCookie(sessionCookie)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusNotImplemented {
				t.Errorf("expected 501, got %d; body: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestNewRouter_AdminRoutes_AsMember_Returns403(t *testing.T) {
	q, db := setupTestDB(t)
	router := NewRouter(q, db)

	// Register first user (admin)
	regBody := strings.NewReader(`{"username":"admin","password":"longpassword"}`)
	regReq := httptest.NewRequest(http.MethodPost, "/api/auth/register", regBody)
	regRec := httptest.NewRecorder()
	router.ServeHTTP(regRec, regReq)

	// Register second user (member)
	regBody2 := strings.NewReader(`{"username":"member","password":"longpassword"}`)
	regReq2 := httptest.NewRequest(http.MethodPost, "/api/auth/register", regBody2)
	regRec2 := httptest.NewRecorder()
	router.ServeHTTP(regRec2, regReq2)

	var sessionCookie *http.Cookie
	for _, c := range regRec2.Result().Cookies() {
		if c.Name == "session" {
			sessionCookie = c
			break
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	req.AddCookie(sessionCookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d; body: %s", rec.Code, rec.Body.String())
	}
}
