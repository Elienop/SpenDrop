package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elienop/spendrop/internal/auth"
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

// PUT /api/users/{id} is a MERGE, not a replace: an omitted (or empty) field
// keeps the stored value, and the handler reads the existing row to fill the
// gap. Every other test in this file sends BOTH fields, so the merge itself
// was unpinned — a handler that wrote the empty request value straight through
// would pass all of them while silently blanking a display name or a role.
//
// The two tests below send one field at a time and assert the OTHER column is
// unchanged in the database, which is the seam a display-name editor depends
// on: that UI sends display_name alone and must not demote anyone.
func TestHandleUpdateUser_DisplayNameOnly_KeepsExistingRole(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")
	// The target is an ADMIN so a merge failure shows up as a privilege
	// change: role would silently become "" rather than staying "admin".
	target := seedTestUser(t, q, "bob", "admin")

	body := strings.NewReader(`{"display_name":"Robert"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/users/"+fmt.Sprintf("%d", target.ID), body)
	req = withUserAndURLParam(req, admin, "id", fmt.Sprintf("%d", target.ID))
	rec := httptest.NewRecorder()

	h.handleUpdateUser(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	updated, err := q.GetUserByID(context.Background(), target.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if updated.DisplayName != "Robert" {
		t.Errorf("expected display_name 'Robert', got %q", updated.DisplayName)
	}
	if updated.Role != "admin" {
		t.Errorf("role = %q, want it unchanged at 'admin' — an omitted role must keep the stored value, not overwrite it", updated.Role)
	}
}

func TestHandleUpdateUser_RoleOnly_KeepsExistingDisplayName(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")
	target := seedNamedUser(t, q, "bob", "Bob The Member", RoleMember)

	body := strings.NewReader(`{"role":"admin"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/users/"+fmt.Sprintf("%d", target.ID), body)
	req = withUserAndURLParam(req, admin, "id", fmt.Sprintf("%d", target.ID))
	rec := httptest.NewRecorder()

	h.handleUpdateUser(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	updated, err := q.GetUserByID(context.Background(), target.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if updated.Role != "admin" {
		t.Errorf("expected role 'admin', got %q", updated.Role)
	}
	if updated.DisplayName != "Bob The Member" {
		t.Errorf("display_name = %q, want it unchanged at %q — an omitted display_name must keep the stored value, not blank it",
			updated.DisplayName, "Bob The Member")
	}
}

// seedSessionForUser inserts one live session row for an existing user and
// returns the value stored in sessions.token — the SHA-256 of the cookie, never
// the cookie itself (the repo-wide invariant; see the identical fixtures in
// password_change_handlers_test.go and settings_handlers_test.go). The stored
// hash is what every lookup below uses, because seeding or querying the
// plaintext would make an "is the session gone?" assertion pass against a row
// it was never able to see in the first place.
//
// The plaintext is deliberately not returned: these tests inject the actor with
// withUserAndURLParam rather than exercising the cookie path, so nothing here
// needs it.
func seedSessionForUser(t *testing.T, q *database.Queries, userID int64) string {
	t.Helper()
	token, err := auth.GenerateSessionToken()
	if err != nil {
		t.Fatalf("generate session token: %v", err)
	}
	stored := auth.HashSessionToken(token)
	if err := q.CreateSession(context.Background(), database.CreateSessionParams{
		Token:     stored,
		UserID:    userID,
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("create session for user %d: %v", userID, err)
	}
	// Prove the fixture is findable the way production finds it. Without this,
	// a "the session was destroyed" assertion would pass on a session that was
	// never resolvable to begin with.
	if _, err := q.GetSession(context.Background(), stored); err != nil {
		t.Fatalf("fixture session for user %d is not resolvable by its stored hash: %v", userID, err)
	}
	return stored
}

// The two tests below pin the SESSION consequence of the merge semantics above.
// handleUpdateUser destroys every session belonging to the TARGET when the role
// changes, so a stolen cookie cannot outlive a demotion — and that invalidation
// must stay welded to the role comparison.
//
// Neither direction is covered by the column-level tests: those assert the users
// row, and the users row ends up correct whether or not sessions were dropped.
// A refactor that lifts DeleteSessionsByUserID out of the `if` would log a
// member out of every device because an admin fixed a typo in their display
// name — the weaponized-rename shape — and would pass every other test in this
// file.
func TestHandleUpdateUser_DisplayNameOnly_KeepsSessionsAlive(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")
	target := seedNamedUser(t, q, "bob", "Bob The Member", RoleMember)
	targetSession := seedSessionForUser(t, q, target.ID)

	body := strings.NewReader(`{"display_name":"Robert"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/users/"+fmt.Sprintf("%d", target.ID), body)
	req = withUserAndURLParam(req, admin, "id", fmt.Sprintf("%d", target.ID))
	rec := httptest.NewRecorder()

	h.handleUpdateUser(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	// The rename has to have LANDED, or "the session survived" is only saying
	// that a request which did nothing also deleted nothing.
	updated, err := q.GetUserByID(context.Background(), target.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if updated.DisplayName != "Robert" {
		t.Fatalf("display_name = %q, want 'Robert' — the rename did not land, so the session assertion below would prove nothing",
			updated.DisplayName)
	}
	if updated.Role != RoleMember {
		t.Errorf("role = %q, want it unchanged at %q", updated.Role, RoleMember)
	}

	if _, err := q.GetSession(context.Background(), targetSession); err != nil {
		t.Errorf("the target's session was destroyed by a display-name-only edit (GetSession: %v) — a rename must not log anyone out of anything",
			err)
	}
	if got := countSessions(t, h, target.ID); got != 1 {
		t.Errorf("sessions for the renamed user = %d, want 1", got)
	}
}

func TestHandleUpdateUser_RoleChange_DestroysTargetSessionsOnly(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")
	target := seedNamedUser(t, q, "bob", "Bob The Member", RoleMember)
	targetSession := seedSessionForUser(t, q, target.ID)
	// The ACTOR's own session, to pin the blast radius: the delete is scoped to
	// the target's user_id, and a mutant that dropped every session in the
	// table would otherwise look identical to a correct one.
	adminSession := seedSessionForUser(t, q, admin.ID)

	body := strings.NewReader(`{"role":"admin"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/users/"+fmt.Sprintf("%d", target.ID), body)
	req = withUserAndURLParam(req, admin, "id", fmt.Sprintf("%d", target.ID))
	rec := httptest.NewRecorder()

	h.handleUpdateUser(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	// Same reasoning as the twin: the privilege change must have landed for the
	// absence of sessions to mean anything.
	updated, err := q.GetUserByID(context.Background(), target.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if updated.Role != RoleAdmin {
		t.Fatalf("role = %q, want %q — the promotion did not land, so the session assertions below would prove nothing",
			updated.Role, RoleAdmin)
	}

	if _, err := q.GetSession(context.Background(), targetSession); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("the promoted user's session survived the role change (GetSession error = %v, want sql.ErrNoRows) — a privilege change must not persist on an old cookie",
			err)
	}
	if got := countSessions(t, h, target.ID); got != 0 {
		t.Errorf("sessions for the promoted user = %d, want 0", got)
	}
	if _, err := q.GetSession(context.Background(), adminSession); err != nil {
		t.Errorf("the acting admin's own session was destroyed too (GetSession: %v) — invalidation must be scoped to the target's user_id",
			err)
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
