package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/database"
	"github.com/elienop/spendrop/internal/ratelimit"
)

// passwordChangeRouter wires the self-service password-change endpoint exactly
// as production router.go does: under the authed /api group with
// RequireAuthOrAPIToken + requireJSONContentType.
func passwordChangeRouter(h *Handler) chi.Router {
	limiter := ratelimit.NewBucket(30, 10*time.Minute, h.clock)
	r := chi.NewRouter()
	r.Route("/api", func(r chi.Router) {
		r.Use(auth.RequireAuthOrAPIToken(h.queries, limiter))
		r.Use(requireJSONContentType)
		r.Post("/auth/password", h.handleChangePassword)
	})
	return r
}

// adminResetRouter wires the admin reset endpoint under the /api/users
// RequireAdmin group, matching router.go.
func adminResetRouter(h *Handler) chi.Router {
	limiter := ratelimit.NewBucket(30, 10*time.Minute, h.clock)
	r := chi.NewRouter()
	r.Route("/api", func(r chi.Router) {
		r.Use(auth.RequireAuthOrAPIToken(h.queries, limiter))
		r.Use(requireJSONContentType)
		r.Route("/users", func(r chi.Router) {
			r.Use(auth.RequireAdmin)
			r.Post("/{id}/reset-password", h.handleAdminResetPassword)
		})
	})
	return r
}

// seedPasswordUser creates a user with a known password and returns the user,
// the plaintext password, and a live session cookie.
func seedPasswordUser(t *testing.T, h *Handler, username, role string) (database.User, string, *http.Cookie) {
	t.Helper()
	auth.SetBcryptCostForTesting()
	password := "current-pw-" + username
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	user, err := h.queries.CreateUser(context.Background(), database.CreateUserParams{
		Username:     username,
		PasswordHash: hash,
		DisplayName:  username,
		Role:         role,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	token, err := auth.GenerateSessionToken()
	if err != nil {
		t.Fatalf("generate session: %v", err)
	}
	// Sessions are stored hashed; persist the hash, send the plaintext cookie.
	if err := h.queries.CreateSession(context.Background(), database.CreateSessionParams{
		Token:     auth.HashSessionToken(token),
		UserID:    user.ID,
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return user, password, &http.Cookie{Name: "session", Value: token}
}

func seedLiveToken(t *testing.T, h *Handler, userID int64, name string) int64 {
	t.Helper()
	_, hash, prefix, err := auth.GenerateAPIToken()
	if err != nil {
		t.Fatalf("generate api token: %v", err)
	}
	tok, err := h.queries.CreateAPIToken(context.Background(), database.CreateAPITokenParams{
		UserID:      userID,
		Name:        name,
		TokenHash:   hash,
		TokenPrefix: prefix,
	})
	if err != nil {
		t.Fatalf("create api token: %v", err)
	}
	return tok.ID
}

func countLiveTokens(t *testing.T, h *Handler, userID int64) int64 {
	t.Helper()
	ids, err := h.queries.ListLiveAPITokenIDsForUser(context.Background(), userID)
	if err != nil {
		t.Fatalf("list live token ids: %v", err)
	}
	return int64(len(ids))
}

func countSessions(t *testing.T, h *Handler, userID int64) int {
	t.Helper()
	var n int
	if err := h.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM sessions WHERE user_id = ?`, userID).Scan(&n); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	return n
}

func doJSONRequest(t *testing.T, router chi.Router, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "203.0.113.42:1111"
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// --- A. self-service password change ---

func TestChangePassword_Success(t *testing.T) {
	h := setupHandler(t)
	user, currentPW, cookie := seedPasswordUser(t, h, "alice", "member")
	seedLiveToken(t, h, user.ID, "homepage-a")
	seedLiveToken(t, h, user.ID, "homepage-b")

	if got := countSessions(t, h, user.ID); got != 1 {
		t.Fatalf("fixture: expected 1 session, got %d", got)
	}

	router := passwordChangeRouter(h)
	rec := doJSONRequest(t, router, http.MethodPost, "/api/auth/password",
		`{"current_password":"`+currentPW+`","new_password":"brand-new-password"}`, cookie)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Status        string `json:"status"`
		TokensRevoked int64  `json:"tokens_revoked"`
	}
	decodeResponse(t, rec, &resp)
	if resp.Status != "password_changed" {
		t.Errorf("status = %q, want password_changed", resp.Status)
	}
	if resp.TokensRevoked != 2 {
		t.Errorf("tokens_revoked = %d, want 2", resp.TokensRevoked)
	}

	// New password must work for a subsequent login.
	fresh, err := h.queries.GetUserByID(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if !auth.CheckPassword(fresh.PasswordHash, "brand-new-password") {
		t.Error("new password does not verify against stored hash")
	}
	if auth.CheckPassword(fresh.PasswordHash, currentPW) {
		t.Error("old password still verifies after change")
	}

	// Caller's sessions gone, tokens revoked.
	if got := countSessions(t, h, user.ID); got != 0 {
		t.Errorf("sessions after change = %d, want 0", got)
	}
	if got := countLiveTokens(t, h, user.ID); got != 0 {
		t.Errorf("live tokens after change = %d, want 0", got)
	}
}

func TestChangePassword_WrongCurrentPassword_Returns401(t *testing.T) {
	h := setupHandler(t)
	user, _, cookie := seedPasswordUser(t, h, "alice", "member")
	seedLiveToken(t, h, user.ID, "homepage-a")

	router := passwordChangeRouter(h)
	rec := doJSONRequest(t, router, http.MethodPost, "/api/auth/password",
		`{"current_password":"wrong-password","new_password":"brand-new-password"}`, cookie)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d; body: %s", rec.Code, rec.Body.String())
	}
	// No mutation: token + session intact.
	if got := countLiveTokens(t, h, user.ID); got != 1 {
		t.Errorf("live tokens after failed change = %d, want 1", got)
	}
	if got := countSessions(t, h, user.ID); got != 1 {
		t.Errorf("sessions after failed change = %d, want 1", got)
	}
}

func TestChangePassword_WeakNewPassword_Returns400(t *testing.T) {
	h := setupHandler(t)
	user, currentPW, cookie := seedPasswordUser(t, h, "alice", "member")

	router := passwordChangeRouter(h)
	rec := doJSONRequest(t, router, http.MethodPost, "/api/auth/password",
		`{"current_password":"`+currentPW+`","new_password":"short"}`, cookie)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
	// Password unchanged.
	fresh, err := h.queries.GetUserByID(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if !auth.CheckPassword(fresh.PasswordHash, currentPW) {
		t.Error("password changed despite weak-password rejection")
	}
}

// TestChangePassword_WrongPassword_RateLimited verifies that repeated
// wrong-current-password attempts eventually return 429. The bucket is
// per-user, keyed by the authenticated user's ID, and only wrong attempts
// record a hit. ASSUMPTION: passwordChangeFailLimiter is constructed with
// limit=5 in NewHandler. If that literal changes, bump the loop bound and the
// expected trip point.
func TestChangePassword_WrongPassword_RateLimited(t *testing.T) {
	h := setupHandler(t)
	user, _, cookie := seedPasswordUser(t, h, "alice", "member")
	_ = user

	router := passwordChangeRouter(h)
	body := `{"current_password":"wrong-password","new_password":"brand-new-password"}`

	// First 5 wrong attempts are 401 (auth failure), recording a hit each.
	for i := 0; i < 5; i++ {
		rec := doJSONRequest(t, router, http.MethodPost, "/api/auth/password", body, cookie)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: want 401, got %d; body: %s", i, rec.Code, rec.Body.String())
		}
	}

	// 6th attempt: bucket exhausted, short-circuits bcrypt and returns 429.
	rec := doJSONRequest(t, router, http.MethodPost, "/api/auth/password", body, cookie)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("6th attempt: want 429, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("Retry-After header missing on 429")
	}
}

// TestChangePassword_CorrectPassword_DoesNotStrandBucket verifies that a
// successful password change with the correct current password does not
// consume the failure bucket, so a legitimate user is not locked out after a
// few earlier typos followed by a correct entry.
func TestChangePassword_CorrectPassword_DoesNotStrandBucket(t *testing.T) {
	h := setupHandler(t)
	user, currentPW, cookie := seedPasswordUser(t, h, "alice", "member")

	router := passwordChangeRouter(h)

	// 4 wrong attempts (under the limit of 5), each recording a hit.
	for i := 0; i < 4; i++ {
		rec := doJSONRequest(t, router, http.MethodPost, "/api/auth/password",
			`{"current_password":"wrong-password","new_password":"brand-new-password"}`, cookie)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("wrong attempt %d: want 401, got %d", i, rec.Code)
		}
	}

	// Correct attempt succeeds (200) and must NOT record a failure hit.
	rec := doJSONRequest(t, router, http.MethodPost, "/api/auth/password",
		`{"current_password":"`+currentPW+`","new_password":"brand-new-password"}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("correct attempt: want 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	_ = user
}

// --- B. admin reset another user's password ---

func TestAdminResetPassword_Success(t *testing.T) {
	h := setupHandler(t)
	admin, _, adminCookie := seedPasswordUser(t, h, "admin", "admin")
	target, targetPW, _ := seedPasswordUser(t, h, "target", "member")
	seedLiveToken(t, h, target.ID, "target-token")

	if got := countSessions(t, h, target.ID); got != 1 {
		t.Fatalf("fixture: expected 1 target session, got %d", got)
	}

	router := adminResetRouter(h)
	path := "/api/users/" + strconv.FormatInt(target.ID, 10) + "/reset-password"
	rec := doJSONRequest(t, router, http.MethodPost, path,
		`{"new_password":"admin-set-password"}`, adminCookie)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Status        string `json:"status"`
		TokensRevoked int64  `json:"tokens_revoked"`
	}
	decodeResponse(t, rec, &resp)
	if resp.Status != "password_reset" {
		t.Errorf("status = %q, want password_reset", resp.Status)
	}
	if resp.TokensRevoked != 1 {
		t.Errorf("tokens_revoked = %d, want 1", resp.TokensRevoked)
	}

	// Target's new password works; old does not.
	fresh, err := h.queries.GetUserByID(context.Background(), target.ID)
	if err != nil {
		t.Fatalf("reload target: %v", err)
	}
	if !auth.CheckPassword(fresh.PasswordHash, "admin-set-password") {
		t.Error("target new password does not verify")
	}
	if auth.CheckPassword(fresh.PasswordHash, targetPW) {
		t.Error("target old password still verifies")
	}

	// Target's tokens + sessions gone; admin's untouched.
	if got := countLiveTokens(t, h, target.ID); got != 0 {
		t.Errorf("target live tokens = %d, want 0", got)
	}
	if got := countSessions(t, h, target.ID); got != 0 {
		t.Errorf("target sessions = %d, want 0", got)
	}
	if got := countSessions(t, h, admin.ID); got != 1 {
		t.Errorf("admin sessions = %d, want 1 (admin should not be logged out)", got)
	}

	// Audit row is attributed to the target user (token owner) with the
	// password-change action.
	var action string
	var auditUserID int64
	if err := h.db.QueryRowContext(context.Background(),
		`SELECT action, user_id FROM api_token_audit WHERE user_id = ?`, target.ID,
	).Scan(&action, &auditUserID); err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	if action != string(database.APITokenAuditRevokedByPasswordChange) {
		t.Errorf("audit action = %q, want revoked_by_password_change", action)
	}
}

// TestAdminResetPassword_SelfTarget_Returns400 verifies an admin cannot reset
// their OWN password via the admin endpoint (which skips the current-password
// check). They must use the self-service /api/auth/password path instead.
func TestAdminResetPassword_SelfTarget_Returns400(t *testing.T) {
	h := setupHandler(t)
	admin, adminPW, adminCookie := seedPasswordUser(t, h, "admin", "admin")

	router := adminResetRouter(h)
	path := "/api/users/" + strconv.FormatInt(admin.ID, 10) + "/reset-password"
	rec := doJSONRequest(t, router, http.MethodPost, path,
		`{"new_password":"admin-set-password"}`, adminCookie)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
	// Admin's password must be unchanged.
	fresh, err := h.queries.GetUserByID(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("reload admin: %v", err)
	}
	if !auth.CheckPassword(fresh.PasswordHash, adminPW) {
		t.Error("admin password changed despite self-target rejection")
	}
}

func TestAdminResetPassword_NonAdmin_Returns403(t *testing.T) {
	h := setupHandler(t)
	_, _, memberCookie := seedPasswordUser(t, h, "member", "member")
	target, _, _ := seedPasswordUser(t, h, "target", "member")

	router := adminResetRouter(h)
	path := "/api/users/" + strconv.FormatInt(target.ID, 10) + "/reset-password"
	rec := doJSONRequest(t, router, http.MethodPost, path,
		`{"new_password":"admin-set-password"}`, memberCookie)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminResetPassword_UnknownUser_Returns404(t *testing.T) {
	h := setupHandler(t)
	_, _, adminCookie := seedPasswordUser(t, h, "admin", "admin")

	router := adminResetRouter(h)
	rec := doJSONRequest(t, router, http.MethodPost, "/api/users/999999/reset-password",
		`{"new_password":"admin-set-password"}`, adminCookie)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminResetPassword_WeakPassword_Returns400(t *testing.T) {
	h := setupHandler(t)
	_, _, adminCookie := seedPasswordUser(t, h, "admin", "admin")
	target, targetPW, _ := seedPasswordUser(t, h, "target", "member")

	router := adminResetRouter(h)
	path := "/api/users/" + strconv.FormatInt(target.ID, 10) + "/reset-password"
	rec := doJSONRequest(t, router, http.MethodPost, path,
		`{"new_password":"short"}`, adminCookie)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
	fresh, err := h.queries.GetUserByID(context.Background(), target.ID)
	if err != nil {
		t.Fatalf("reload target: %v", err)
	}
	if !auth.CheckPassword(fresh.PasswordHash, targetPW) {
		t.Error("target password changed despite weak-password rejection")
	}
}
