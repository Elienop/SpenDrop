package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elienop/spendrop/internal/database"
)

// --- handleListUsers ---

func TestHandleListUsers_ReturnsAllUsers(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	seedTestUser(t, q, "alice", "admin")
	seedTestUser(t, q, "bob", "member")

	admin := mustGetUser(t, q, "alice")
	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()

	h.handleListUsers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp []map[string]any
	decodeResponse(t, rec, &resp)
	if len(resp) != 2 {
		t.Errorf("expected 2 users, got %d", len(resp))
	}
}

func TestHandleListUsers_ExcludesPasswordHash(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "alice", "admin")

	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()

	h.handleListUsers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp []map[string]any
	decodeResponse(t, rec, &resp)
	for _, u := range resp {
		if _, exists := u["password_hash"]; exists {
			t.Error("response should not include password_hash")
		}
	}
}

func TestHandleListUsers_NoAuth_Returns401(t *testing.T) {
	h := setupHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	rec := httptest.NewRecorder()

	h.handleListUsers(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// --- handleCreateUser ---

func TestHandleCreateUser_ValidInput_Returns201(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	body := strings.NewReader(`{
		"username": "newuser",
		"password": "longpassword",
		"display_name": "New User",
		"role": "member"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/users", body)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()

	h.handleCreateUser(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	if resp["username"] != "newuser" {
		t.Errorf("expected username 'newuser', got %v", resp["username"])
	}
	if resp["display_name"] != "New User" {
		t.Errorf("expected display_name 'New User', got %v", resp["display_name"])
	}
	if resp["role"] != "member" {
		t.Errorf("expected role 'member', got %v", resp["role"])
	}
	if _, exists := resp["password_hash"]; exists {
		t.Error("response should not include password_hash")
	}
}

func TestHandleCreateUser_MissingUsername_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	body := strings.NewReader(`{"password":"longpassword","role":"member"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/users", body)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()

	h.handleCreateUser(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleCreateUser_MissingPassword_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	body := strings.NewReader(`{"username":"newuser","role":"member"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/users", body)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()

	h.handleCreateUser(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleCreateUser_ShortPassword_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	body := strings.NewReader(`{"username":"newuser","password":"short","role":"member"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/users", body)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()

	h.handleCreateUser(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleCreateUser_InvalidRole_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	body := strings.NewReader(`{"username":"newuser","password":"longpassword","role":"superadmin"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/users", body)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()

	h.handleCreateUser(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleCreateUser_DuplicateUsername_Returns409(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	body := strings.NewReader(`{"username":"admin","password":"longpassword","role":"member"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/users", body)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()

	h.handleCreateUser(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleCreateUser_NoAuth_Returns401(t *testing.T) {
	h := setupHandler(t)

	body := strings.NewReader(`{"username":"newuser","password":"longpassword","role":"member"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/users", body)
	rec := httptest.NewRecorder()

	h.handleCreateUser(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestHandleCreateUser_InvalidJSON_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	body := strings.NewReader(`not json`)
	req := httptest.NewRequest(http.MethodPost, "/api/users", body)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()

	h.handleCreateUser(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// --- handleUpdateUser ---

func TestHandleUpdateUser_ValidInput_Returns200(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")
	target := seedTestUser(t, q, "bob", "member")

	body := strings.NewReader(`{"display_name":"Robert","role":"admin"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/users/"+fmt.Sprintf("%d", target.ID), body)
	req = withUserAndURLParam(req, admin, "id", fmt.Sprintf("%d", target.ID))
	rec := httptest.NewRecorder()

	h.handleUpdateUser(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	// Verify in DB
	updated, err := q.GetUserByID(context.Background(), target.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if updated.DisplayName != "Robert" {
		t.Errorf("expected display_name 'Robert', got %q", updated.DisplayName)
	}
	if updated.Role != "admin" {
		t.Errorf("expected role 'admin', got %q", updated.Role)
	}
}

func TestHandleUpdateUser_InvalidID_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	body := strings.NewReader(`{"display_name":"Test","role":"member"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/users/abc", body)
	req = withUserAndURLParam(req, admin, "id", "abc")
	rec := httptest.NewRecorder()

	h.handleUpdateUser(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleUpdateUser_InvalidRole_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")
	target := seedTestUser(t, q, "bob", "member")

	body := strings.NewReader(`{"display_name":"Bob","role":"superadmin"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/users/"+fmt.Sprintf("%d", target.ID), body)
	req = withUserAndURLParam(req, admin, "id", fmt.Sprintf("%d", target.ID))
	rec := httptest.NewRecorder()

	h.handleUpdateUser(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleUpdateUser_NoAuth_Returns401(t *testing.T) {
	h := setupHandler(t)

	body := strings.NewReader(`{"display_name":"Test","role":"member"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/users/1", body)
	req = withURLParam(req, "id", "1")
	rec := httptest.NewRecorder()

	h.handleUpdateUser(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// --- handleDeleteUser ---

func TestHandleDeleteUser_ValidInput_Returns200(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")
	target := seedTestUser(t, q, "bob", "member")

	req := httptest.NewRequest(http.MethodDelete, "/api/users/"+fmt.Sprintf("%d", target.ID), nil)
	req = withUserAndURLParam(req, admin, "id", fmt.Sprintf("%d", target.ID))
	rec := httptest.NewRecorder()

	h.handleDeleteUser(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	// Verify deleted from DB
	_, err := q.GetUserByID(context.Background(), target.ID)
	if err != sql.ErrNoRows {
		t.Errorf("expected user to be deleted, got err: %v", err)
	}
}

func TestHandleDeleteUser_CannotDeleteSelf(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	req := httptest.NewRequest(http.MethodDelete, "/api/users/"+fmt.Sprintf("%d", admin.ID), nil)
	req = withUserAndURLParam(req, admin, "id", fmt.Sprintf("%d", admin.ID))
	rec := httptest.NewRecorder()

	h.handleDeleteUser(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}

	// Verify NOT deleted from DB
	_, err := q.GetUserByID(context.Background(), admin.ID)
	if err != nil {
		t.Errorf("admin should still exist: %v", err)
	}
}

func TestHandleDeleteUser_InvalidID_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	req := httptest.NewRequest(http.MethodDelete, "/api/users/abc", nil)
	req = withUserAndURLParam(req, admin, "id", "abc")
	rec := httptest.NewRecorder()

	h.handleDeleteUser(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleDeleteUser_NoAuth_Returns401(t *testing.T) {
	h := setupHandler(t)

	req := httptest.NewRequest(http.MethodDelete, "/api/users/1", nil)
	req = withURLParam(req, "id", "1")
	rec := httptest.NewRecorder()

	h.handleDeleteUser(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// mustGetUser is a test helper that fetches a user by username, failing the test if not found.
func mustGetUser(t *testing.T, q *database.Queries, username string) database.User {
	t.Helper()
	user, err := q.GetUserByUsername(context.Background(), username)
	if err != nil {
		t.Fatalf("get user %s: %v", username, err)
	}
	return user
}

// verifyNoPasswordHash checks that a JSON response map does not contain password_hash.
func verifyNoPasswordHash(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	var raw map[string]any
	if err := json.NewDecoder(strings.NewReader(rec.Body.String())).Decode(&raw); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, exists := raw["password_hash"]; exists {
		t.Error("response should not include password_hash")
	}
}
