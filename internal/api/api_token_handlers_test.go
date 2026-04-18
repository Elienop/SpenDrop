package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/database"
)

// seedTokenTestUser inserts a user and returns (user, plaintextPassword,
// sessionCookie). Handlers consume auth.GetUser from the request context, so
// every test fires requests through a RequireAuth-wrapped handler.
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
	if err := h.queries.CreateSession(context.Background(), database.CreateSessionParams{
		Token:     token,
		UserID:    user.ID,
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return user, password, &http.Cookie{Name: "session", Value: token}
}

// tokenRouter wraps the caller-provided *Handler in RequireAuth +
// requireJSONContentType, matching router.go:94-97. Reusing `h` across
// requests is load-bearing: rate-limit tests depend on per-Handler bucket
// state accumulating. NewRouter would construct a fresh Handler per call
// and silently neuter every bucket assertion.
func tokenRouter(h *Handler) chi.Router {
	r := chi.NewRouter()
	r.Route("/api", func(r chi.Router) {
		r.Use(auth.RequireAuth(h.queries))
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
	_, password, cookie := seedTokenTestUser(t, h, "alice")

	rec := tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{
		"name":     "Homepage",
		"password": password,
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

func TestAPITokens_Create_WrongPassword_401(t *testing.T) {
	h := setupHandler(t)
	_, _, cookie := seedTokenTestUser(t, h, "alice")
	rec := tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{
		"name":     "Homepage",
		"password": "wrong",
	}, cookie)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

func TestAPITokens_Create_WrongPassword_DoesNotEmitAuditRow(t *testing.T) {
	h := setupHandler(t)
	user, _, cookie := seedTokenTestUser(t, h, "alice")
	_ = tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{
		"name":     "Homepage",
		"password": "wrong",
	}, cookie)
	// Zero rows in api_token_audit for this user.
	var n int64
	row := h.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM api_token_audit WHERE user_id = ?`, user.ID)
	if err := row.Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("audit row leaked on failed create: %d", n)
	}
}

func TestAPITokens_Create_WrongPassword_ConsumesLoginFailureBucketNotCreationBucket(t *testing.T) {
	h := setupHandler(t)
	_, _, cookie := seedTokenTestUser(t, h, "alice")
	// ASSUMPTION: `config.Defaults().RateLimit.MaxAttempts == 10` — this is the
	// capacity frozen into h.loginFailureLimiter at NewHandler time. If the
	// default moves, bump the loop bound + the expected 429 trip point.
	for i := 0; i < 10; i++ {
		_ = tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{
			"name":     fmt.Sprintf("t%d", i),
			"password": "wrong",
		}, cookie)
	}
	rec := tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{
		"name":     "t11",
		"password": "wrong",
	}, cookie)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("11th failed reconfirm: want 429 from login bucket, got %d", rec.Code)
	}
	// Create bucket is untouched — verify the user key is not exhausted.
	user, _ := h.queries.GetUserByUsername(context.Background(), "alice")
	if h.createTokenLimiter.Exhausted(strconv.FormatInt(user.ID, 10)) {
		t.Error("create bucket was consumed by failed reconfirms (should be login-only)")
	}
}

func TestAPITokens_Create_EmitsAuditRowWithCreatedAction(t *testing.T) {
	h := setupHandler(t)
	user, password, cookie := seedTokenTestUser(t, h, "alice")
	rec := tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{
		"name":     "Homepage",
		"password": password,
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

func TestAPITokens_Create_RateLimitExceeded_429(t *testing.T) {
	h := setupHandler(t)
	_, password, cookie := seedTokenTestUser(t, h, "alice")
	// ASSUMPTION: the createTokenLimiter is hardcoded to `limit=5` in
	// NewHandler (`ratelimit.NewBucket(5, time.Hour, clock)`). If that
	// literal changes, bump the loop bound and the expected 429 trip point.
	// 5 successful creates, then a 6th must 429.
	for i := 0; i < 5; i++ {
		rec := tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{
			"name":     fmt.Sprintf("t%d", i),
			"password": password,
		}, cookie)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %d: want 201, got %d: %s", i, rec.Code, rec.Body.String())
		}
	}
	rec := tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{
		"name":     "t6",
		"password": password,
	}, cookie)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("6th create: want 429, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("Retry-After header missing on 429")
	}
}

// TestAPITokens_Create_ExhaustedBucket_Returns429_EvenOnWrongPassword closes
// the 401-vs-429 oracle: once the create bucket is exhausted, the handler
// must 429 BEFORE doing the password check, so a probe cannot tell "bucket
// exhausted" from "wrong password" (both would be 401 otherwise and leak
// capacity state). Ordering check: the exhaustion gate in handleCreateAPIToken
// runs before decodeJSON + CheckPassword — verify it by exhausting the bucket
// with 5 successful creates, then firing a request with a wrong password and
// asserting 429.
func TestAPITokens_Create_ExhaustedBucket_Returns429_EvenOnWrongPassword(t *testing.T) {
	h := setupHandler(t)
	_, password, cookie := seedTokenTestUser(t, h, "alice")
	for i := 0; i < 5; i++ {
		rec := tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{
			"name":     fmt.Sprintf("t%d", i),
			"password": password,
		}, cookie)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %d: want 201, got %d", i, rec.Code)
		}
	}
	// Now fire with a wrong password. Bucket is exhausted; handler must
	// return 429 without consulting the password (would otherwise be 401).
	rec := tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{
		"name":     "t-wrong",
		"password": "wrong",
	}, cookie)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("exhausted bucket + wrong password: want 429 (not 401), got %d", rec.Code)
	}
}

func TestAPITokens_Create_ExpiresAtInPast_400(t *testing.T) {
	h := setupHandler(t)
	_, password, cookie := seedTokenTestUser(t, h, "alice")
	past := time.Now().Add(-time.Hour)
	rec := tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{
		"name":       "t",
		"password":   password,
		"expires_at": past,
	}, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

func TestAPITokens_Create_ExpiresAtBeyond10Years_400(t *testing.T) {
	h := setupHandler(t)
	_, password, cookie := seedTokenTestUser(t, h, "alice")
	far := time.Now().AddDate(11, 0, 0)
	rec := tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{
		"name":       "t",
		"password":   password,
		"expires_at": far,
	}, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

func TestAPITokens_Create_EmptyName_400(t *testing.T) {
	h := setupHandler(t)
	_, password, cookie := seedTokenTestUser(t, h, "alice")
	rec := tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{
		"name":     "   ",
		"password": password,
	}, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("whitespace-only name: want 400, got %d", rec.Code)
	}
}

func TestAPITokens_Create_NameOver100Chars_400(t *testing.T) {
	h := setupHandler(t)
	_, password, cookie := seedTokenTestUser(t, h, "alice")
	rec := tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{
		"name":     strings.Repeat("x", 101),
		"password": password,
	}, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("101-char name: want 400, got %d", rec.Code)
	}
}

func TestAPITokens_List_ExcludesHashAndPlaintext(t *testing.T) {
	h := setupHandler(t)
	_, password, cookie := seedTokenTestUser(t, h, "alice")
	_ = tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{
		"name": "t", "password": password,
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
	_, aliceP, aliceCookie := seedTokenTestUser(t, h, "alice")
	_, bobP, bobCookie := seedTokenTestUser(t, h, "bob")
	_ = tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{"name": "alice-tok", "password": aliceP}, aliceCookie)
	_ = tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{"name": "bob-tok", "password": bobP}, bobCookie)

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
	_, password, cookie := seedTokenTestUser(t, h, "alice")
	// Create two tokens, revoke one, assert list returns exactly one.
	createRec := tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{"name": "to-revoke", "password": password}, cookie)
	var created apiTokenCreateResponse
	_ = json.Unmarshal(createRec.Body.Bytes(), &created)
	_ = tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{"name": "keep", "password": password}, cookie)

	_ = tokenRequest(t, h, http.MethodDelete, fmt.Sprintf("/api/api-tokens/%d", created.ID), nil, cookie)
	rec := tokenRequest(t, h, http.MethodGet, "/api/api-tokens/", nil, cookie)
	var out apiTokenListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if len(out.Tokens) != 1 || out.Tokens[0].Name != "keep" {
		t.Errorf("want only 'keep'; got %+v", out.Tokens)
	}
}

func TestAPITokens_RevokeOne_SoftDeletesAndEmitsAuditWithRevokedByUser(t *testing.T) {
	h := setupHandler(t)
	user, password, cookie := seedTokenTestUser(t, h, "alice")
	createRec := tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{"name": "t", "password": password}, cookie)
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
	user, password, cookie := seedTokenTestUser(t, h, "alice")
	createRec := tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{"name": "t", "password": password}, cookie)
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
	_, aliceP, aliceCookie := seedTokenTestUser(t, h, "alice")
	_, _, bobCookie := seedTokenTestUser(t, h, "bob")
	createRec := tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{"name": "t", "password": aliceP}, aliceCookie)
	var aliceTok apiTokenCreateResponse
	_ = json.Unmarshal(createRec.Body.Bytes(), &aliceTok)

	rec := tokenRequest(t, h, http.MethodDelete, fmt.Sprintf("/api/api-tokens/%d", aliceTok.ID), nil, bobCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bob revoking alice's token: want 404, got %d", rec.Code)
	}
}

func TestAPITokens_RevokeAll_SoftDeletesAllLiveTokensForUser_EmitsAuditPerToken(t *testing.T) {
	h := setupHandler(t)
	user, password, cookie := seedTokenTestUser(t, h, "alice")
	for i := 0; i < 3; i++ {
		_ = tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{"name": fmt.Sprintf("t%d", i), "password": password}, cookie)
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
	user, password, cookie := seedTokenTestUser(t, h, "alice")
	createRec := tokenRequest(t, h, http.MethodPost, "/api/api-tokens/", map[string]any{"name": "t", "password": password}, cookie)
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
