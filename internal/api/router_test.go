package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elienop/spendrop/internal/database"
)

func TestNewRouter_HealthEndpoint_Returns200(t *testing.T) {
	q, db := setupTestDB(t)
	router := NewRouter(q, db, nil) // nil cfg → config.Defaults()

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
	router := NewRouter(q, db, nil) // nil cfg → config.Defaults()

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
	router := NewRouter(q, db, nil) // nil cfg → config.Defaults()

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
	router := NewRouter(q, db, nil) // nil cfg → config.Defaults()

	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestNewRouter_AuthMe_WithoutSession_Returns401(t *testing.T) {
	q, db := setupTestDB(t)
	router := NewRouter(q, db, nil) // nil cfg → config.Defaults()

	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestNewRouter_AuthMe_WithSession_Returns200(t *testing.T) {
	q, db := setupTestDB(t)
	router := NewRouter(q, db, nil) // nil cfg → config.Defaults()

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
	router := NewRouter(q, db, nil) // nil cfg → config.Defaults()

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

func TestNewRouter_AdminRoutes_WithoutAuth_Returns401(t *testing.T) {
	q, db := setupTestDB(t)
	router := NewRouter(q, db, nil) // nil cfg → config.Defaults()

	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestNewRouter_AdminRoutes_AsAdmin_Succeeds(t *testing.T) {
	q, db := setupTestDB(t)
	router := NewRouter(q, db, nil) // nil cfg → config.Defaults()

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
	if sessionCookie == nil {
		t.Fatal("no session cookie from register")
	}

	// Admin should be able to list users
	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	req.AddCookie(sessionCookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestNewRouter_AdminRoutes_AsMember_Returns403(t *testing.T) {
	q, db := setupTestDB(t)
	router := NewRouter(q, db, nil) // nil cfg → config.Defaults()

	// Register first user (admin)
	regBody := strings.NewReader(`{"username":"admin","password":"longpassword"}`)
	regReq := httptest.NewRequest(http.MethodPost, "/api/auth/register", regBody)
	regRec := httptest.NewRecorder()
	router.ServeHTTP(regRec, regReq)
	if regRec.Code != http.StatusCreated {
		t.Fatalf("first register failed: %d; body: %s", regRec.Code, regRec.Body.String())
	}

	// Enable registration so second user can register
	if err := q.UpsertSetting(context.Background(), database.UpsertSettingParams{
		Key:   "registration_enabled",
		Value: "true",
	}); err != nil {
		t.Fatalf("upsert setting: %v", err)
	}

	// Register second user (member)
	regBody2 := strings.NewReader(`{"username":"member","password":"longpassword"}`)
	regReq2 := httptest.NewRequest(http.MethodPost, "/api/auth/register", regBody2)
	regRec2 := httptest.NewRecorder()
	router.ServeHTTP(regRec2, regReq2)
	if regRec2.Code != http.StatusCreated {
		t.Fatalf("second register failed: %d; body: %s", regRec2.Code, regRec2.Body.String())
	}

	var sessionCookie *http.Cookie
	for _, c := range regRec2.Result().Cookies() {
		if c.Name == "session" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("no session cookie from member register")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	req.AddCookie(sessionCookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d; body: %s", rec.Code, rec.Body.String())
	}
}
