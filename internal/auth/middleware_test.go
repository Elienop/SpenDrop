package auth

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/elienop/spendrop/internal/database"
	_ "github.com/mattn/go-sqlite3"
)

// setupTestDB creates an in-memory SQLite database with migrations applied
// and returns a Queries instance for testing.
func setupTestDB(t *testing.T) (*database.Queries, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := database.RunMigrations(db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return database.New(db), db
}

// createTestUser creates a user and returns it for use in auth tests.
func createTestUser(t *testing.T, q *database.Queries, role string) database.User {
	t.Helper()
	user, err := q.CreateUser(context.Background(), database.CreateUserParams{
		Username:     "testuser-" + role,
		PasswordHash: "$2a$10$fakehash",
		DisplayName:  "Test " + role,
		Role:         role,
	})
	if err != nil {
		t.Fatalf("create test user: %v", err)
	}
	return user
}

// createTestSession creates a session for the given user with the given expiry.
func createTestSession(t *testing.T, q *database.Queries, userID int64, token string, expiresAt time.Time) {
	t.Helper()
	err := q.CreateSession(context.Background(), database.CreateSessionParams{
		Token:     token,
		UserID:    userID,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		t.Fatalf("create test session: %v", err)
	}
}

// okHandler is a simple handler that returns 200 OK.
func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
}

func TestRequireAuth_NoCookie_Returns401(t *testing.T) {
	q, _ := setupTestDB(t)
	handler := RequireAuth(q)(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestRequireAuth_InvalidToken_Returns401(t *testing.T) {
	q, _ := setupTestDB(t)
	handler := RequireAuth(q)(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: "nonexistent-token"})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestRequireAuth_ExpiredSession_Returns401(t *testing.T) {
	q, _ := setupTestDB(t)
	user := createTestUser(t, q, "member")
	// Token must be exactly 64 hex characters to pass the length check in RequireAuth
	expiredTok := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	createTestSession(t, q, user.ID, expiredTok, time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))

	handler := RequireAuth(q)(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: expiredTok})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestRequireAuth_ExpiredSession_DeletesSession(t *testing.T) {
	q, _ := setupTestDB(t)
	user := createTestUser(t, q, "member")
	// Token must be exactly 64 hex characters to pass the length check in RequireAuth
	expiredTokDel := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	createTestSession(t, q, user.ID, expiredTokDel, time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))

	handler := RequireAuth(q)(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: expiredTokDel})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Session should have been deleted
	_, err := q.GetSession(context.Background(), expiredTokDel)
	if err != sql.ErrNoRows {
		t.Errorf("expected expired session to be deleted, got err: %v", err)
	}
}

func TestRequireAuth_ValidSession_SetsUserContext(t *testing.T) {
	q, _ := setupTestDB(t)
	user := createTestUser(t, q, "member")
	// Token must be exactly 64 hex characters to pass the length check in RequireAuth
	validTok := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	createTestSession(t, q, user.ID, validTok, time.Now().Add(24*time.Hour).UTC())

	var gotUser database.User
	var gotOk bool
	innerHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotOk = GetUser(r)
		w.WriteHeader(http.StatusOK)
	})

	handler := RequireAuth(q)(innerHandler)
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: validTok})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !gotOk {
		t.Fatal("expected user in context")
	}
	if gotUser.ID != user.ID {
		t.Errorf("expected user ID %d, got %d", user.ID, gotUser.ID)
	}
	if gotUser.Username != user.Username {
		t.Errorf("expected username %q, got %q", user.Username, gotUser.Username)
	}
}

func TestRequireAuth_ValidSession_CallsNext(t *testing.T) {
	q, _ := setupTestDB(t)
	user := createTestUser(t, q, "admin")
	// Token must be exactly 64 hex characters to pass the length check in RequireAuth
	validTokNext := "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	createTestSession(t, q, user.ID, validTokNext, time.Now().Add(24*time.Hour).UTC())

	handler := RequireAuth(q)(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: validTokNext})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != "ok" {
		t.Errorf("expected body 'ok', got %q", rec.Body.String())
	}
}

func TestRequireAdmin_NoUserInContext_Returns403(t *testing.T) {
	handler := RequireAdmin(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/admin", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestRequireAdmin_NonAdminUser_Returns403(t *testing.T) {
	user := database.User{ID: 1, Username: "member", Role: "member"}
	ctx := context.WithValue(context.Background(), UserContextKey, user)

	handler := RequireAdmin(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/api/admin", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestRequireAdmin_AdminUser_CallsNext(t *testing.T) {
	user := database.User{ID: 1, Username: "admin", Role: "admin"}
	ctx := context.WithValue(context.Background(), UserContextKey, user)

	handler := RequireAdmin(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/api/admin", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != "ok" {
		t.Errorf("expected body 'ok', got %q", rec.Body.String())
	}
}

func TestGetUser_WithUser_ReturnsUserAndTrue(t *testing.T) {
	user := database.User{ID: 42, Username: "tester", Role: "member"}
	ctx := context.WithValue(context.Background(), UserContextKey, user)
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)

	got, ok := GetUser(req)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if got.ID != 42 {
		t.Errorf("expected user ID 42, got %d", got.ID)
	}
}

func TestGetUser_WithoutUser_ReturnsFalse(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	_, ok := GetUser(req)
	if ok {
		t.Error("expected ok=false when no user in context")
	}
}
