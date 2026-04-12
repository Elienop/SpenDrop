package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/elienop/spendrop/internal/database"
)

// --- Cookie Secure flag ---
//
// The Secure flag is controlled by COOKIE_SECURE with three modes:
//   - "true"  → always Secure
//   - "false" → never Secure (plain-HTTP deployments)
//   - "auto"  → follow the request scheme (default)
// Deprecated alias: SPENDROP_INSECURE=true == COOKIE_SECURE=false.

// resetCookieSecureEnv clears every env var that shouldMarkCookieSecure or
// insecureModeEnabled reads, so each test starts from a hermetic baseline
// even when the CI runner has one of them exported globally. Call it at the
// top of any test that exercises cookie security or HSTS.
func resetCookieSecureEnv(t *testing.T) {
	t.Helper()
	t.Setenv("COOKIE_SECURE", "")
	t.Setenv("SPENDROP_INSECURE", "")
	t.Setenv("TRUST_PROXY", "")
}

func sessionCookieFromRegister(t *testing.T, h *Handler) *http.Cookie {
	t.Helper()
	body := strings.NewReader(`{"username":"alice","password":"longpassword"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register", body)
	rec := httptest.NewRecorder()
	h.handleRegister(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == "session" {
			return c
		}
	}
	t.Fatal("expected session cookie to be set")
	return nil
}

func TestSetSessionCookie_CookieSecureTrue_AlwaysSecure(t *testing.T) {
	resetCookieSecureEnv(t)
	t.Setenv("COOKIE_SECURE", "true")

	h := setupHandler(t)
	c := sessionCookieFromRegister(t, h)
	if !c.Secure {
		t.Error("session cookie should have Secure flag when COOKIE_SECURE=true")
	}
}

func TestSetSessionCookie_CookieSecureFalse_NeverSecure(t *testing.T) {
	resetCookieSecureEnv(t)
	t.Setenv("COOKIE_SECURE", "false")

	h := setupHandler(t)
	c := sessionCookieFromRegister(t, h)
	if c.Secure {
		t.Error("session cookie should NOT have Secure flag when COOKIE_SECURE=false")
	}
}

func TestSetSessionCookie_AutoMode_PlainHTTPRequestNotSecure(t *testing.T) {
	resetCookieSecureEnv(t)

	h := setupHandler(t)
	c := sessionCookieFromRegister(t, h)
	if c.Secure {
		t.Error("auto mode on plain HTTP should not set Secure (browsers drop it otherwise)")
	}
}

func TestSetSessionCookie_AutoMode_TrustedProxyXFPHttps_IsSecure(t *testing.T) {
	resetCookieSecureEnv(t)
	t.Setenv("TRUST_PROXY", "true")

	h := setupHandler(t)

	body := strings.NewReader(`{"username":"alice","password":"longpassword"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register", body)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	h.handleRegister(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var c *http.Cookie
	for _, cc := range rec.Result().Cookies() {
		if cc.Name == "session" {
			c = cc
			break
		}
	}
	if c == nil {
		t.Fatal("expected session cookie")
	}
	if !c.Secure {
		t.Error("TRUST_PROXY=true with X-Forwarded-Proto=https should mark cookie Secure")
	}
}

func TestIsSecureRequest_MultiValueXFP(t *testing.T) {
	resetCookieSecureEnv(t)
	t.Setenv("TRUST_PROXY", "true")

	cases := []struct {
		xfp      string
		expected bool
		note     string
	}{
		{"https", true, "single value https"},
		{"https, http", true, "client https, later hop http — leftmost wins"},
		{"http, https", false, "client http, later hop https — leftmost wins"},
		{"  https  ", true, "whitespace tolerance"},
		{"HTTPS", true, "case insensitive"},
		{"", false, "empty header"},
	}

	for _, tc := range cases {
		t.Run(tc.note, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.xfp != "" {
				req.Header.Set("X-Forwarded-Proto", tc.xfp)
			}
			got := isSecureRequest(req)
			if got != tc.expected {
				t.Errorf("XFP=%q: expected %v, got %v", tc.xfp, tc.expected, got)
			}
		})
	}
}

func TestSetSessionCookie_AutoMode_UntrustedProxyXFP_NotSecure(t *testing.T) {
	resetCookieSecureEnv(t) // TRUST_PROXY unset — XFP must NOT be trusted

	h := setupHandler(t)

	body := strings.NewReader(`{"username":"alice","password":"longpassword"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register", body)
	req.Header.Set("X-Forwarded-Proto", "https") // attacker-controllable
	rec := httptest.NewRecorder()
	h.handleRegister(rec, req)

	var c *http.Cookie
	for _, cc := range rec.Result().Cookies() {
		if cc.Name == "session" {
			c = cc
			break
		}
	}
	if c == nil {
		t.Fatal("expected session cookie")
	}
	if c.Secure {
		t.Error("X-Forwarded-Proto must not be trusted without TRUST_PROXY=true")
	}
}

func TestSetSessionCookie_DeprecatedSpendropInsecure_NotSecure(t *testing.T) {
	resetCookieSecureEnv(t)
	t.Setenv("SPENDROP_INSECURE", "true")

	h := setupHandler(t)
	c := sessionCookieFromRegister(t, h)
	if c.Secure {
		t.Error("deprecated SPENDROP_INSECURE=true should disable Secure flag")
	}
}

func TestLogoutCookie_RespectsCookieSecure(t *testing.T) {
	resetCookieSecureEnv(t)
	t.Setenv("COOKIE_SECURE", "true")

	h := setupHandler(t)

	// Register to get a session
	regBody := strings.NewReader(`{"username":"alice","password":"longpassword"}`)
	regReq := httptest.NewRequest(http.MethodPost, "/api/auth/register", regBody)
	regRec := httptest.NewRecorder()
	h.handleRegister(regRec, regReq)

	var sessionToken string
	for _, c := range regRec.Result().Cookies() {
		if c.Name == "session" {
			sessionToken = c.Value
			break
		}
	}

	logoutReq := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	logoutReq.Header.Set("Content-Type", "application/json")
	logoutReq.AddCookie(&http.Cookie{Name: "session", Value: sessionToken})
	logoutRec := httptest.NewRecorder()
	h.handleLogout(logoutRec, logoutReq)

	var c *http.Cookie
	for _, cc := range logoutRec.Result().Cookies() {
		if cc.Name == "session" {
			c = cc
			break
		}
	}
	if c == nil {
		t.Fatal("expected session cookie in logout response")
	}
	if !c.Secure {
		t.Error("logout cookie should respect COOKIE_SECURE=true")
	}
}

// --- Registration gate ---

func TestHandleRegister_SecondUser_BlockedByDefault(t *testing.T) {
	h := setupHandler(t)

	// Register first user (always allowed)
	body1 := strings.NewReader(`{"username":"alice","password":"longpassword"}`)
	req1 := httptest.NewRequest(http.MethodPost, "/api/auth/register", body1)
	rec1 := httptest.NewRecorder()
	h.handleRegister(rec1, req1)
	if rec1.Code != http.StatusCreated {
		t.Fatalf("first register failed: %d; body: %s", rec1.Code, rec1.Body.String())
	}

	// Second user should be blocked (registration disabled by default)
	body2 := strings.NewReader(`{"username":"bob","password":"longpassword"}`)
	req2 := httptest.NewRequest(http.MethodPost, "/api/auth/register", body2)
	rec2 := httptest.NewRecorder()
	h.handleRegister(rec2, req2)

	if rec2.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d; body: %s", rec2.Code, rec2.Body.String())
	}

	var resp map[string]string
	decodeResponse(t, rec2, &resp)
	if !strings.Contains(resp["error"], "registration is disabled") {
		t.Errorf("expected 'registration is disabled' error, got %q", resp["error"])
	}
}

func TestHandleRegister_SecondUser_AllowedWhenEnabled(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)

	// Register first user
	body1 := strings.NewReader(`{"username":"alice","password":"longpassword"}`)
	req1 := httptest.NewRequest(http.MethodPost, "/api/auth/register", body1)
	rec1 := httptest.NewRecorder()
	h.handleRegister(rec1, req1)
	if rec1.Code != http.StatusCreated {
		t.Fatalf("first register failed: %d", rec1.Code)
	}

	// Enable registration
	err := q.UpsertSetting(context.Background(), database.UpsertSettingParams{
		Key:   "registration_enabled",
		Value: "true",
	})
	if err != nil {
		t.Fatalf("upsert setting: %v", err)
	}

	// Second user should now succeed
	body2 := strings.NewReader(`{"username":"bob","password":"longpassword"}`)
	req2 := httptest.NewRequest(http.MethodPost, "/api/auth/register", body2)
	rec2 := httptest.NewRecorder()
	h.handleRegister(rec2, req2)

	if rec2.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d; body: %s", rec2.Code, rec2.Body.String())
	}
}

// --- CSRF Content-Type enforcement ---

func TestRequireJSONContentType_PostWithoutJSON_Returns415(t *testing.T) {
	handler := requireJSONContentType(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/test", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("expected 415, got %d", rec.Code)
	}
}

func TestRequireJSONContentType_PostWithJSON_Passes(t *testing.T) {
	handler := requireJSONContentType(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/test", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestRequireJSONContentType_GetRequest_Passes(t *testing.T) {
	handler := requireJSONContentType(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestRequireJSONContentType_MultipartFormData_Passes(t *testing.T) {
	handler := requireJSONContentType(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/import/upload", strings.NewReader(""))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=something")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestRequireJSONContentType_OptionsRequest_Passes(t *testing.T) {
	handler := requireJSONContentType(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodOptions, "/api/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

// --- Search LIKE wildcard escaping ---

func TestBuildTransactionWhereClause_SearchEscapesWildcards(t *testing.T) {
	tests := []struct {
		search        string
		expectClause  string
		expectArgLike string
	}{
		{"hello", `t.description LIKE ? ESCAPE '\'`, "%hello%"},
		{"100%", `t.description LIKE ? ESCAPE '\'`, `%100\%%`},
		{"under_score", `t.description LIKE ? ESCAPE '\'`, `%under\_score%`},
		{`back\slash`, `t.description LIKE ? ESCAPE '\'`, `%back\\slash%`},
	}

	for _, tc := range tests {
		t.Run(tc.search, func(t *testing.T) {
			vals := url.Values{}
			vals.Set("search", tc.search)
			clause, args := buildTransactionWhereClause(vals)
			if !strings.Contains(clause, "ESCAPE") {
				t.Errorf("expected ESCAPE clause, got %q", clause)
			}
			if len(args) != 1 {
				t.Fatalf("expected 1 arg, got %d", len(args))
			}
			argStr, ok := args[0].(string)
			if !ok {
				t.Fatalf("expected string arg, got %T", args[0])
			}
			if argStr != tc.expectArgLike {
				t.Errorf("expected arg %q, got %q", tc.expectArgLike, argStr)
			}
		})
	}
}

// --- Login rate limiting ---

func TestHandleLogin_RateLimited_After10Attempts(t *testing.T) {
	h := setupHandler(t)

	// Register a user
	regBody := strings.NewReader(`{"username":"alice","password":"longpassword"}`)
	regReq := httptest.NewRequest(http.MethodPost, "/api/auth/register", regBody)
	regRec := httptest.NewRecorder()
	h.handleRegister(regRec, regReq)
	if regRec.Code != http.StatusCreated {
		t.Fatalf("register failed: %d", regRec.Code)
	}

	// Reset the rate limiter before our test
	rateLimitMu.Lock()
	loginAttempts = make(map[string]int)
	rateLimitMu.Unlock()

	// Make 10 failed attempts
	for i := 0; i < 10; i++ {
		body := strings.NewReader(`{"username":"alice","password":"wrongpasswd"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", body)
		rec := httptest.NewRecorder()
		h.handleLogin(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401, got %d", i+1, rec.Code)
		}
	}

	// 11th attempt should be rate limited
	body := strings.NewReader(`{"username":"alice","password":"wrongpasswd"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", body)
	rec := httptest.NewRecorder()
	h.handleLogin(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d; body: %s", rec.Code, rec.Body.String())
	}

	// Clean up
	rateLimitMu.Lock()
	loginAttempts = make(map[string]int)
	rateLimitMu.Unlock()
}

// --- handleUpdateUser validation ---

func TestHandleUpdateUser_EmptyFields_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")
	target := seedTestUser(t, q, "bob", "member")

	body := strings.NewReader(`{}`)
	req := httptest.NewRequest(http.MethodPut, "/api/users/"+fmt.Sprintf("%d", target.ID), body)
	req = withUserAndURLParam(req, admin, "id", fmt.Sprintf("%d", target.ID))
	rec := httptest.NewRecorder()

	h.handleUpdateUser(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]string
	decodeResponse(t, rec, &resp)
	if !strings.Contains(resp["error"], "display_name or role is required") {
		t.Errorf("expected 'display_name or role is required', got %q", resp["error"])
	}
}

// --- parseYearMonth month range validation ---

func TestParseYearMonth_InvalidMonthRange_ReturnsError(t *testing.T) {
	tests := []struct {
		month string
	}{
		{"0"},
		{"13"},
		{"-1"},
		{"99"},
	}

	for _, tc := range tests {
		t.Run("month="+tc.month, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/test?month="+tc.month, nil)
			_, _, err := parseYearMonth(req)
			if err == nil {
				t.Errorf("expected error for month=%s, got nil", tc.month)
			}
		})
	}
}

func TestParseYearMonth_ValidMonth_NoError(t *testing.T) {
	for m := 1; m <= 12; m++ {
		t.Run(fmt.Sprintf("month=%d", m), func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/test?month=%d", m), nil)
			_, month, err := parseYearMonth(req)
			if err != nil {
				t.Errorf("expected no error for month=%d, got %v", m, err)
			}
			if month != m {
				t.Errorf("expected month=%d, got %d", m, month)
			}
		})
	}
}

// --- Dashboard trend months upper bound ---

func TestHandleDashboardTrend_MonthsCappedAt120(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/trend?months=999", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleDashboardTrend(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Trend []map[string]any `json:"trend"`
	}
	decodeResponse(t, rec, &resp)
	if len(resp.Trend) != 120 {
		t.Errorf("expected 120 trend entries (capped), got %d", len(resp.Trend))
	}
}

func TestHandleDashboardTrend_NegativeMonthsClamped(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/trend?months=-5", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleDashboardTrend(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Trend []map[string]any `json:"trend"`
	}
	decodeResponse(t, rec, &resp)
	if len(resp.Trend) != 1 {
		t.Errorf("expected 1 trend entry (clamped from -5), got %d", len(resp.Trend))
	}
}

// --- Batch transaction array size limit ---

func TestHandleBatchCreateTransactions_ExceedsMaxSize_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	// Build an array of 501 items
	var items []string
	for i := 0; i < 501; i++ {
		items = append(items, fmt.Sprintf(`{"date":"2026-04-06","amount":10,"description":"Item %d","category_id":1}`, i))
	}
	bodyStr := "[" + strings.Join(items, ",") + "]"

	body := strings.NewReader(bodyStr)
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/batch", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleBatchCreateTransactions(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]string
	decodeResponse(t, rec, &resp)
	if !strings.Contains(resp["error"], "500") {
		t.Errorf("expected error mentioning 500 limit, got %q", resp["error"])
	}
}

// --- Category reorder array size limit ---

func TestHandleReorderCategories_ExceedsMaxSize_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	// Build an array of 201 items
	var items []string
	for i := 0; i < 201; i++ {
		items = append(items, fmt.Sprintf(`{"id":%d,"sort_order":%d}`, i+1, i))
	}
	bodyStr := "[" + strings.Join(items, ",") + "]"

	body := strings.NewReader(bodyStr)
	req := httptest.NewRequest(http.MethodPost, "/api/categories/reorder", body)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()

	h.handleReorderCategories(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]string
	decodeResponse(t, rec, &resp)
	if !strings.Contains(resp["error"], "200") {
		t.Errorf("expected error mentioning 200 limit, got %q", resp["error"])
	}
}

// --- Security headers ---

func TestSecurityHeaders_SetOnResponse(t *testing.T) {
	handler := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if v := rec.Header().Get("X-Content-Type-Options"); v != "nosniff" {
		t.Errorf("expected X-Content-Type-Options 'nosniff', got %q", v)
	}
	if v := rec.Header().Get("X-Frame-Options"); v != "DENY" {
		t.Errorf("expected X-Frame-Options 'DENY', got %q", v)
	}
	if v := rec.Header().Get("Referrer-Policy"); v != "strict-origin-when-cross-origin" {
		t.Errorf("expected Referrer-Policy 'strict-origin-when-cross-origin', got %q", v)
	}
	if v := rec.Header().Get("Content-Security-Policy"); v == "" {
		t.Error("expected Content-Security-Policy header to be set")
	}
}

func TestSecurityHeaders_CSPContentsCorrect(t *testing.T) {
	handler := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	csp := rec.Header().Get("Content-Security-Policy")
	expectedParts := []string{"default-src 'self'", "script-src 'self'", "frame-ancestors 'none'"}
	for _, part := range expectedParts {
		if !strings.Contains(csp, part) {
			t.Errorf("CSP header missing %q; got %q", part, csp)
		}
	}
}

func TestSecurityHeaders_HSTSSetByDefault(t *testing.T) {
	// HSTS should be set when no insecure mode is active.
	resetCookieSecureEnv(t)

	handler := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	hsts := rec.Header().Get("Strict-Transport-Security")
	if hsts == "" {
		t.Error("expected Strict-Transport-Security header to be set by default")
	}
	if !strings.Contains(hsts, "max-age=31536000") {
		t.Errorf("expected max-age=31536000, got %q", hsts)
	}
}

func TestSecurityHeaders_HSTSSkippedWhenSpendropInsecure(t *testing.T) {
	resetCookieSecureEnv(t)
	t.Setenv("SPENDROP_INSECURE", "true")

	handler := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	hsts := rec.Header().Get("Strict-Transport-Security")
	if hsts != "" {
		t.Errorf("expected no HSTS header when SPENDROP_INSECURE=true, got %q", hsts)
	}
}

func TestSecurityHeaders_HSTSSkippedWhenCookieSecureFalse(t *testing.T) {
	resetCookieSecureEnv(t)
	t.Setenv("COOKIE_SECURE", "false")

	handler := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	hsts := rec.Header().Get("Strict-Transport-Security")
	if hsts != "" {
		t.Errorf("expected no HSTS header when COOKIE_SECURE=false, got %q", hsts)
	}
}

// --- CORS origin from env ---

func TestCorsMiddleware_SetsVaryHeaderAlways(t *testing.T) {
	t.Setenv("CORS_ORIGIN", "")

	handler := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if v := rec.Header().Get("Vary"); v != "Origin" {
		t.Errorf("expected Vary 'Origin' on all responses, got %q", v)
	}
}

func TestCorsMiddleware_UnsetOrigin_NoCORSHeaders(t *testing.T) {
	// Fail-closed: when CORS_ORIGIN is not set, we do not silently whitelist
	// a dev origin. Same-origin deployments (the default) need no CORS.
	t.Setenv("CORS_ORIGIN", "")

	handler := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if v := rec.Header().Get("Access-Control-Allow-Origin"); v != "" {
		t.Errorf("expected no Access-Control-Allow-Origin when CORS_ORIGIN unset, got %q", v)
	}
	if v := rec.Header().Get("Access-Control-Allow-Credentials"); v != "" {
		t.Errorf("expected no Access-Control-Allow-Credentials when CORS_ORIGIN unset, got %q", v)
	}
}

func TestCorsMiddleware_SetOrigin_EmitsCORSHeaders(t *testing.T) {
	t.Setenv("CORS_ORIGIN", "https://spendrop.example.com")

	handler := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if v := rec.Header().Get("Access-Control-Allow-Origin"); v != "https://spendrop.example.com" {
		t.Errorf("expected Access-Control-Allow-Origin from env, got %q", v)
	}
	if v := rec.Header().Get("Access-Control-Allow-Credentials"); v != "true" {
		t.Errorf("expected Access-Control-Allow-Credentials true, got %q", v)
	}
	if v := rec.Header().Get("Access-Control-Allow-Methods"); v == "" {
		t.Error("expected Access-Control-Allow-Methods header to be set")
	}
}

func TestCorsMiddleware_OptionsPreflight_ReturnsNoContent(t *testing.T) {
	t.Setenv("CORS_ORIGIN", "https://spendrop.example.com")

	handler := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("OPTIONS should short-circuit before next handler")
	}))

	req := httptest.NewRequest(http.MethodOptions, "/api/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", rec.Code)
	}
}

// --- Input length validation on transaction fields ---

func TestValidateTransactionRequest_DescriptionTooLong_ReturnsError(t *testing.T) {
	req := transactionRequest{
		Date:        "2026-04-06",
		Amount:      50.0,
		Description: strings.Repeat("x", 501),
		CategoryID:  1,
	}
	err := validateTransactionRequest(req)
	if err == nil {
		t.Error("expected error for description > 500 chars")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected error mentioning 500, got %q", err.Error())
	}
}

func TestValidateTransactionRequest_TagsTooLong_ReturnsError(t *testing.T) {
	req := transactionRequest{
		Date:        "2026-04-06",
		Amount:      50.0,
		Description: "Test",
		CategoryID:  1,
		Tags:        strings.Repeat("x", 501),
	}
	err := validateTransactionRequest(req)
	if err == nil {
		t.Error("expected error for tags > 500 chars")
	}
}

func TestValidateTransactionRequest_NotesTooLong_ReturnsError(t *testing.T) {
	req := transactionRequest{
		Date:        "2026-04-06",
		Amount:      50.0,
		Description: "Test",
		CategoryID:  1,
		Notes:       strings.Repeat("x", 2001),
	}
	err := validateTransactionRequest(req)
	if err == nil {
		t.Error("expected error for notes > 2000 chars")
	}
}

func TestValidateTransactionRequest_ValidLengths_NoError(t *testing.T) {
	req := transactionRequest{
		Date:        "2026-04-06",
		Amount:      50.0,
		Description: strings.Repeat("x", 500),
		CategoryID:  1,
		Tags:        strings.Repeat("x", 500),
		Notes:       strings.Repeat("x", 2000),
	}
	err := validateTransactionRequest(req)
	if err != nil {
		t.Errorf("expected no error for valid lengths, got %v", err)
	}
}
