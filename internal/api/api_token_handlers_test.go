package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/database"
	"github.com/elienop/spendrop/internal/ratelimit"
)

// seedTokenTestUser inserts a user and returns (user, plaintextPassword,
// sessionCookie). Handlers consume auth.GetUser from the request context, so
// every test fires requests through the session-auth wrapped handler.
// plaintextPassword is returned for tests that need it to exercise the login
// path; the token-create handler itself no longer requires a password.
func seedTokenTestUser(t *testing.T, h *Handler, username string) (database.User, string, *http.Cookie) {
	t.Helper()
	auth.SetBcryptCostForTesting()
	password := "hunter2-" + username
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	user, err := h.queries.CreateUser(context.Background(), database.CreateUserParams{
		Username:     username,
		PasswordHash: hash,
		DisplayName:  username,
		Role:         "member",
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

// tokenRouter wraps the caller-provided *Handler in RequireAuthOrAPIToken +
// requireJSONContentType, matching the production router.go wiring. Reusing
// `h` across requests is load-bearing: rate-limit tests depend on per-Handler
// bucket state accumulating. NewRouter would construct a fresh Handler per
// call and silently neuter every bucket assertion.
func tokenRouter(h *Handler) chi.Router {
	limiter := ratelimit.NewBucket(30, 10*time.Minute, h.clock)
	r := chi.NewRouter()
	r.Route("/api", func(r chi.Router) {
		r.Use(auth.RequireAuthOrAPIToken(h.queries, limiter))
		r.Use(requireJSONContentType)
		r.Route("/api-tokens", func(r chi.Router) {
			r.Post("/", h.handleCreateAPIToken)
			r.Get("/", h.handleListAPITokens)
			r.Delete("/", h.handleRevokeAllAPITokens)
			r.Delete("/{id}", h.handleRevokeAPIToken)
		})
	})
	return r
}

// tokenRequest fires an authenticated request through RequireAuth → the
// target handler. Returns the recorder so the caller can assert status +
// body. `body` may be nil for GET/DELETE.
func tokenRequest(t *testing.T, h *Handler, method, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "203.0.113.10:5678"
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	tokenRouter(h).ServeHTTP(rec, req)
	return rec
}

func TestAPITokens_Create_ReturnsPlaintextOnceInResponse(t *testing.T) {
	h := setupHandler(t)
	_, _, cookie := seedTokenTestUser(t, h, "alice")

	rec := tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{
		"name": "Homepage",
	}, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp apiTokenCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !strings.HasPrefix(resp.Token, "spdr_") {
		t.Errorf("token missing spdr_ prefix: %q", resp.Token)
	}
	if resp.TokenPrefix != resp.Token[:15] {
		t.Errorf("token_prefix mismatch: prefix=%q token=%q", resp.TokenPrefix, resp.Token[:15])
	}
	// Second GET must NOT echo the plaintext back.
	listRec := tokenRequest(t, h, http.MethodGet, "/api/api-tokens/", nil, cookie)
	if strings.Contains(listRec.Body.String(), resp.Token) {
		t.Error("plaintext leaked to list response")
	}
}

// TestCreateAPIToken_NoPasswordRequired pins the new contract: no password
// field is required on create, and a body that omits it must return 201 with
// the plaintext token in the response.
func TestCreateAPIToken_NoPasswordRequired(t *testing.T) {
	h := setupHandler(t)
	_, _, cookie := seedTokenTestUser(t, h, "alice")

	rec := tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{
		"name": "Homepage",
	}, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create without password: want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp apiTokenCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !strings.HasPrefix(resp.Token, "spdr_") {
		t.Errorf("token missing spdr_ prefix: %q", resp.Token)
	}
}

func TestAPITokens_Create_EmitsAuditRowWithCreatedAction(t *testing.T) {
	h := setupHandler(t)
	user, _, cookie := seedTokenTestUser(t, h, "alice")
	rec := tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{
		"name": "Homepage",
	}, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d", rec.Code)
	}
	var action string
	row := h.db.QueryRowContext(context.Background(),
		`SELECT action FROM api_token_audit WHERE user_id = ? ORDER BY id DESC LIMIT 1`, user.ID)
	if err := row.Scan(&action); err != nil {
		t.Fatalf("scan audit: %v", err)
	}
	if action != string(database.APITokenAuditCreated) {
		t.Errorf("audit action: want %q, got %q", database.APITokenAuditCreated, action)
	}
}

// TestCreateAPIToken_RateLimitStillEnforced ensures the per-user 5/hour
// createTokenLimiter still fires after the password reconfirm was dropped.
// Without this the only remaining abuse control is the bucket; losing it
// silently would let a compromised session mint unlimited tokens.
func TestCreateAPIToken_RateLimitStillEnforced(t *testing.T) {
	h := setupHandler(t)
	_, _, cookie := seedTokenTestUser(t, h, "alice")
	// ASSUMPTION: the createTokenLimiter is hardcoded to `limit=5` in
	// NewHandler (`ratelimit.NewBucket(5, time.Hour, clock)`). If that
	// literal changes, bump the loop bound and the expected 429 trip point.
	for i := 0; i < 5; i++ {
		rec := tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{
			"name": fmt.Sprintf("t%d", i),
		}, cookie)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %d: want 201, got %d: %s", i, rec.Code, rec.Body.String())
		}
	}
	rec := tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{
		"name": "t6",
	}, cookie)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("6th create: want 429, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("Retry-After header missing on 429")
	}
}

func TestAPITokens_Create_ExpiresAtInPast_400(t *testing.T) {
	h := setupHandler(t)
	_, _, cookie := seedTokenTestUser(t, h, "alice")
	past := time.Now().Add(-time.Hour)
	rec := tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{
		"name":       "t",
		"expires_at": past,
	}, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

func TestAPITokens_Create_ExpiresAtBeyond10Years_400(t *testing.T) {
	h := setupHandler(t)
	_, _, cookie := seedTokenTestUser(t, h, "alice")
	far := time.Now().AddDate(11, 0, 0)
	rec := tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{
		"name":       "t",
		"expires_at": far,
	}, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

func TestAPITokens_Create_EmptyName_400(t *testing.T) {
	h := setupHandler(t)
	_, _, cookie := seedTokenTestUser(t, h, "alice")
	rec := tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{
		"name": "   ",
	}, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("whitespace-only name: want 400, got %d", rec.Code)
	}
}

func TestAPITokens_Create_NameOver100Chars_400(t *testing.T) {
	h := setupHandler(t)
	_, _, cookie := seedTokenTestUser(t, h, "alice")
	rec := tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{
		"name": strings.Repeat("x", 101),
	}, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("101-char name: want 400, got %d", rec.Code)
	}
}

func TestAPITokens_List_ExcludesHashAndPlaintext(t *testing.T) {
	h := setupHandler(t)
	_, _, cookie := seedTokenTestUser(t, h, "alice")
	_ = tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{
		"name": "t",
	}, cookie)
	rec := tokenRequest(t, h, http.MethodGet, "/api/api-tokens/", nil, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: want 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	for _, banned := range []string{`"token"`, `"token_hash"`, "plaintext"} {
		if strings.Contains(body, banned) {
			t.Errorf("list body contains forbidden substring %q: %s", banned, body)
		}
	}
}

func TestAPITokens_List_OnlyReturnsOwnTokens(t *testing.T) {
	h := setupHandler(t)
	_, _, aliceCookie := seedTokenTestUser(t, h, "alice")
	_, _, bobCookie := seedTokenTestUser(t, h, "bob")
	_ = tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{"name": "alice-tok"}, aliceCookie)
	_ = tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{"name": "bob-tok"}, bobCookie)

	rec := tokenRequest(t, h, http.MethodGet, "/api/api-tokens/", nil, aliceCookie)
	var out apiTokenListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Tokens) != 1 || out.Tokens[0].Name != "alice-tok" {
		t.Errorf("alice saw %+v; want exactly her own token", out.Tokens)
	}
}

func TestAPITokens_List_HidesRevokedByDefault(t *testing.T) {
	h := setupHandler(t)
	_, _, cookie := seedTokenTestUser(t, h, "alice")
	// Create two tokens, revoke one, assert list returns exactly one.
	createRec := tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{"name": "to-revoke"}, cookie)
	var created apiTokenCreateResponse
	_ = json.Unmarshal(createRec.Body.Bytes(), &created)
	_ = tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{"name": "keep"}, cookie)

	revokeRec := tokenRequest(t, h, http.MethodDelete, fmt.Sprintf("/api/api-tokens/%d", created.ID), nil, cookie)
	if revokeRec.Code != http.StatusOK {
		t.Fatalf("revoke: want 200, got %d", revokeRec.Code)
	}
	rec := tokenRequest(t, h, http.MethodGet, "/api/api-tokens/", nil, cookie)
	var out apiTokenListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if len(out.Tokens) != 1 || out.Tokens[0].Name != "keep" {
		t.Errorf("want only 'keep'; got %+v", out.Tokens)
	}
}

func TestAPITokens_RevokeOne_SoftDeletesAndEmitsAuditWithRevokedByUser(t *testing.T) {
	h := setupHandler(t)
	user, _, cookie := seedTokenTestUser(t, h, "alice")
	createRec := tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{"name": "t"}, cookie)
	var created apiTokenCreateResponse
	_ = json.Unmarshal(createRec.Body.Bytes(), &created)

	rec := tokenRequest(t, h, http.MethodDelete, fmt.Sprintf("/api/api-tokens/%d", created.ID), nil, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke: want 200, got %d", rec.Code)
	}

	var revokedAt *time.Time
	_ = h.db.QueryRowContext(context.Background(),
		`SELECT revoked_at FROM api_tokens WHERE id = ?`, created.ID).Scan(&revokedAt)
	if revokedAt == nil {
		t.Error("revoked_at still NULL after revoke")
	}

	var action string
	_ = h.db.QueryRowContext(context.Background(),
		`SELECT action FROM api_token_audit WHERE token_id = ? AND user_id = ? ORDER BY id DESC LIMIT 1`,
		created.ID, user.ID).Scan(&action)
	if action != string(database.APITokenAuditRevokedByUser) {
		t.Errorf("audit action: want %q, got %q", database.APITokenAuditRevokedByUser, action)
	}
}

func TestAPITokens_RevokeOne_AlreadyRevoked_Idempotent_NoSecondAudit(t *testing.T) {
	h := setupHandler(t)
	user, _, cookie := seedTokenTestUser(t, h, "alice")
	createRec := tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{"name": "t"}, cookie)
	var created apiTokenCreateResponse
	_ = json.Unmarshal(createRec.Body.Bytes(), &created)

	// First DELETE is the happy path; it writes the revoked_by_user audit row.
	// Second DELETE is the one under test — it must NOT write a duplicate audit
	// row, confirming ApiTokenStore.Revoke is idempotent.
	_ = tokenRequest(t, h, http.MethodDelete, fmt.Sprintf("/api/api-tokens/%d", created.ID), nil, cookie)
	_ = tokenRequest(t, h, http.MethodDelete, fmt.Sprintf("/api/api-tokens/%d", created.ID), nil, cookie)

	var n int64
	_ = h.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM api_token_audit WHERE token_id = ? AND action = ? AND user_id = ?`,
		created.ID, database.APITokenAuditRevokedByUser, user.ID).Scan(&n)
	if n != 1 {
		t.Errorf("want exactly 1 revoked_by_user audit row, got %d", n)
	}
}

func TestAPITokens_RevokeOne_OtherUsersToken_404(t *testing.T) {
	h := setupHandler(t)
	_, _, aliceCookie := seedTokenTestUser(t, h, "alice")
	_, _, bobCookie := seedTokenTestUser(t, h, "bob")
	createRec := tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{"name": "t"}, aliceCookie)
	var aliceTok apiTokenCreateResponse
	_ = json.Unmarshal(createRec.Body.Bytes(), &aliceTok)

	rec := tokenRequest(t, h, http.MethodDelete, fmt.Sprintf("/api/api-tokens/%d", aliceTok.ID), nil, bobCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bob revoking alice's token: want 404, got %d", rec.Code)
	}
}

func TestAPITokens_RevokeAll_SoftDeletesAllLiveTokensForUser_EmitsAuditPerToken(t *testing.T) {
	h := setupHandler(t)
	user, _, cookie := seedTokenTestUser(t, h, "alice")
	for i := 0; i < 3; i++ {
		createRec := tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{"name": fmt.Sprintf("t%d", i)}, cookie)
		if createRec.Code != http.StatusCreated {
			t.Fatalf("create t%d: want 201, got %d", i, createRec.Code)
		}
	}
	rec := tokenRequest(t, h, http.MethodDelete, "/api/api-tokens/", nil, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke-all: want 200, got %d", rec.Code)
	}
	var revokedCount int64
	_ = h.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM api_tokens WHERE user_id = ? AND revoked_at IS NOT NULL`, user.ID).Scan(&revokedCount)
	if revokedCount != 3 {
		t.Errorf("want 3 revoked rows, got %d", revokedCount)
	}
	var auditCount int64
	_ = h.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM api_token_audit WHERE user_id = ? AND action = ?`,
		user.ID, database.APITokenAuditRevokedByMassRevoke).Scan(&auditCount)
	if auditCount != 3 {
		t.Errorf("want 3 mass-revoke audit rows, got %d", auditCount)
	}
}

func TestAPITokens_Revoke_EmitsAuditWithCorrectActorSessionHash(t *testing.T) {
	h := setupHandler(t)
	user, _, cookie := seedTokenTestUser(t, h, "alice")
	createRec := tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{"name": "t"}, cookie)
	var created apiTokenCreateResponse
	_ = json.Unmarshal(createRec.Body.Bytes(), &created)
	_ = tokenRequest(t, h, http.MethodDelete, fmt.Sprintf("/api/api-tokens/%d", created.ID), nil, cookie)

	var sessionHash string
	_ = h.db.QueryRowContext(context.Background(),
		`SELECT actor_session_hash FROM api_token_audit WHERE token_id = ? AND action = ? AND user_id = ?`,
		created.ID, database.APITokenAuditRevokedByUser, user.ID).Scan(&sessionHash)
	if want := auth.HashSessionToken(cookie.Value); sessionHash != want {
		t.Errorf("actor_session_hash: want %s, got %s", want, sessionHash)
	}
}
