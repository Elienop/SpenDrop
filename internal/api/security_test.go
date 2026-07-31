package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

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

// TestHandleRegister_ReadFailure_Is503Not403 is the regression test for a
// transient database error being reported as a policy decision.
//
// registrationOpen folded every read error into "closed", so a momentary
// SQLITE_BUSY answered 403 "registration is disabled". On a fresh install that
// is actively misleading: there is nothing for the operator to switch back on,
// because no migration seeds registration_enabled, so the message points at a
// setting that does not exist while the real cause would have cleared on retry.
//
// Both branches still REFUSE to create an account — the security posture is
// unchanged — so the assertion is on the status and the message, not on whether
// a user was created. TestHandleRegister_SecondUser_BlockedByDefault is the
// companion control: an ABSENT setting must stay a plain 403, since that is the
// ordinary closed state rather than a failure.
func TestHandleRegister_ReadFailure_Is503Not403(t *testing.T) {
	for _, tc := range []struct {
		name    string
		breakDB func(t *testing.T, db *sql.DB)
	}{
		{
			name: "settings read fails",
			breakDB: func(t *testing.T, db *sql.DB) {
				if _, err := db.Exec(`DROP TABLE app_settings`); err != nil {
					t.Fatalf("drop app_settings: %v", err)
				}
			},
		},
		{
			name: "user list read fails",
			breakDB: func(t *testing.T, db *sql.DB) {
				// The register above left a session row pointing at the user;
				// clear it so the DROP is not an FK violation instead.
				if _, err := db.Exec(`DELETE FROM sessions`); err != nil {
					t.Fatalf("clear sessions: %v", err)
				}
				if _, err := db.Exec(`DROP TABLE users`); err != nil {
					t.Fatalf("drop users: %v", err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, db := setupTestDB(t)
			h := NewHandler(q, db)

			// A first user must exist, or the bootstrap shortcut returns early
			// and never reaches the failing read.
			rec := httptest.NewRecorder()
			h.handleRegister(rec, httptest.NewRequest(http.MethodPost, "/api/auth/register",
				strings.NewReader(`{"username":"alice","password":"longpassword"}`)))
			if rec.Code != http.StatusCreated {
				t.Fatalf("seed register failed: %d; body: %s", rec.Code, rec.Body.String())
			}

			tc.breakDB(t, db)

			rec2 := httptest.NewRecorder()
			h.handleRegister(rec2, httptest.NewRequest(http.MethodPost, "/api/auth/register",
				strings.NewReader(`{"username":"bob","password":"longpassword"}`)))

			if rec2.Code != http.StatusServiceUnavailable {
				t.Errorf("got %d, want 503 — a failed read is being reported as a policy "+
					"decision; body: %s", rec2.Code, rec2.Body.String())
			}
			var resp map[string]string
			decodeResponse(t, rec2, &resp)
			if strings.Contains(resp["error"], "registration is disabled") {
				t.Errorf("error says registration is disabled, but nothing disabled it: %q", resp["error"])
			}
		})
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

// --- CSRF Bearer-auth exemption (spec §6.3) ---

func TestRequireJSONContentType_BearerRequest_BypassesContentTypeCheck(t *testing.T) {
	called := false
	handler := requireJSONContentType(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	// POST without Content-Type, but with Bearer — would 415 absent the skip.
	req := httptest.NewRequest(http.MethodPost, "/api/homepage/summary", strings.NewReader(""))
	req.Header.Set("Authorization", "Bearer spdr_aB3xQ9z7kLmN3pRsTv2wXyZfG9_abc123")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 (bearer bypass), got %d", rec.Code)
	}
	if !called {
		t.Error("next handler should have been called on bearer-authorized POST without Content-Type")
	}
}

func TestRequireJSONContentType_CookieRequest_StillRequiresJSON(t *testing.T) {
	handler := requireJSONContentType(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Session-cookie auth must remain under CSRF protection — skip is
	// specific to Authorization: Bearer.
	req := httptest.NewRequest(http.MethodPost, "/api/transactions", strings.NewReader(`{}`))
	req.AddCookie(&http.Cookie{Name: "session", Value: strings.Repeat("a", 64)})
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("cookie request without JSON Content-Type must still 415; got %d", rec.Code)
	}
}

func TestRequireJSONContentType_BearerAndCookieBothPresent_BearerWins_NoCSRF(t *testing.T) {
	called := false
	handler := requireJSONContentType(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	// Both auth schemes present (curl user, test harness). Bearer wins.
	req := httptest.NewRequest(http.MethodPost, "/api/homepage/summary", strings.NewReader(""))
	req.Header.Set("Authorization", "Bearer spdr_aB3xQ9z7kLmN3pRsTv2wXyZfG9_abc123")
	req.AddCookie(&http.Cookie{Name: "session", Value: strings.Repeat("a", 64)})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("bearer+cookie should behave as bearer-only and pass through; got %d", rec.Code)
	}
	if !called {
		t.Error("next handler should have been called")
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

	// Bucket is fresh per setupHandler — no manual reset needed.

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
	h := setupHandler(t)
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
			_, _, err := h.parseYearMonth(req)
			if err == nil {
				t.Errorf("expected error for month=%s, got nil", tc.month)
			}
		})
	}
}

func TestParseYearMonth_ValidMonth_NoError(t *testing.T) {
	h := setupHandler(t)
	for m := 1; m <= 12; m++ {
		t.Run(fmt.Sprintf("month=%d", m), func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/test?month=%d", m), nil)
			_, month, err := h.parseYearMonth(req)
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

func TestHandleDashboardTrend_MonthsCappedAtMaxTrendMonths(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/dashboard/trend?months=%d", MaxTrendMonths*10), nil)
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
	// Asserted against the constant, not a literal: MaxTrendMonths is derived
	// as (MaxDataYear - MinDataYear + 1) * 12, so the Savings tab's window can
	// always reach January of the oldest selectable year — which is
	// MinDataYear, the oldest year GET /api/reports/years will ever offer. It
	// must be free to move with those bounds without a test pinning it to a
	// stale number.
	if len(resp.Trend) != MaxTrendMonths {
		t.Errorf("expected %d trend entries (capped), got %d", MaxTrendMonths, len(resp.Trend))
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

// --- MaxTrendMonths cross-boundary contract ---
//
// MaxTrendMonths is not a free parameter: the Reports → Savings tab derives
// its `?months=` from the year the user picked and the server clamps anything
// larger to MaxTrendMonths, so a cap that is too small silently truncates the
// oldest selectable years with nothing on screen to say so. The tests below
// pin the contract itself rather than restating the current number, because
// the number is expected to grow and the *relationship* is what must hold.

// TestMaxTrendMonths_ReachesOldestSelectableYear is the Go half of
// web/src/components/reports/savingsWindow.test.ts: whichever year the UI
// offers, the window must still reach January of it, and the server must not
// clamp that window away.
//
// This used to compare against a Go-local `historicalYearStart = 2024` that
// mirrored HISTORICAL_YEAR_START in web/src/lib/dates.ts — exactly the
// cross-boundary drift its sibling test was written to prevent. The floor is
// no longer a TypeScript constant: it is derived from the ledger, and
// GET /api/reports/years filters what it offers to [MinDataYear, MaxDataYear].
// MinDataYear is therefore the real worst-case oldest selectable year, and it
// is already defined right here in Go.
func TestMaxTrendMonths_ReachesOldestSelectableYear(t *testing.T) {
	// The frontend test pins the first five clocks; time.Now() is added so the
	// list going stale cannot make the assertion vacuous, and MaxDataYear is added
	// because it is the last year this application models at all — past it,
	// every year-param endpoint 400s, so nothing wider can ever be asked for.
	currentYears := []int{2026, 2033, 2034, 2040, 2060, time.Now().Year(), MaxDataYear}

	for _, currentYear := range currentYears {
		// Worst case: the window ends at the current month, so reaching
		// January of the oldest offered year takes every month from that
		// January through December of the current year. The oldest year the
		// picker can offer is MinDataYear — /api/reports/years drops anything
		// below it, so it can never offer a year the year-param endpoints reject.
		minRequired := (currentYear - MinDataYear + 1) * 12
		if MaxTrendMonths < minRequired {
			t.Errorf("MaxTrendMonths=%d is too small for a %d clock: the report year floor "+
				"bottoms out at MinDataYear=%d, which needs a %d-month window to reach January — "+
				"the server would clamp and truncate it",
				MaxTrendMonths, currentYear, MinDataYear, minRequired)
		}
	}
}

// TestMaxTrendMonths_MatchesFrontendMaxReportMonths pins the claim both
// comments make — internal/api/limits.go and web/src/components/reports/
// utils.ts each say the two constants MUST stay in step — in the only place
// it can actually be checked. Drift either way is a silent bug: a larger
// client value is truncated server-side, a larger server value means the UI
// never asks for the window it is allowed to have.
func TestMaxTrendMonths_MatchesFrontendMaxReportMonths(t *testing.T) {
	const relPath = "../../web/src/components/reports/utils.ts"

	src, err := os.ReadFile(relPath)
	if err != nil {
		t.Fatalf("read %s: %v (if the file moved, repoint this test — the constants must stay in step)", relPath, err)
	}

	m := regexp.MustCompile(`MAX_REPORT_MONTHS\s*=\s*(\d+)`).FindSubmatch(src)
	if m == nil {
		t.Fatalf("no `MAX_REPORT_MONTHS = <number>` found in %s (if it was renamed, repoint this test)", relPath)
	}
	frontend, err := strconv.Atoi(string(m[1]))
	if err != nil {
		t.Fatalf("parse MAX_REPORT_MONTHS %q: %v", m[1], err)
	}

	if frontend != MaxTrendMonths {
		t.Errorf("MAX_REPORT_MONTHS=%d in %s but MaxTrendMonths=%d in internal/api/limits.go; "+
			"they must match or one side silently truncates the report window",
			frontend, relPath, MaxTrendMonths)
	}
}

// TestHandleReportIncomeExpenses_ServesTheFullCappedWindow drives the endpoint
// the Savings tab actually calls, end to end, at the widest window the server
// admits. Without it the whole range above 12 months was untested: a handler
// that capped `months` at 12 — which would break the Savings tab today, not in
// some future year — left the suite green.
func TestHandleReportIncomeExpenses_ServesTheFullCappedWindow(t *testing.T) {
	anchor := time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC)

	// Earliest bucket, computed with plain month arithmetic rather than
	// AddDate so the expectation does not mirror the handler's own walk.
	idx := anchor.Year()*12 + int(anchor.Month()) - 1 - (MaxTrendMonths - 1)
	wantFirstYear, wantFirstMonth := idx/12, idx%12+1

	tests := []struct {
		name   string
		months int
	}{
		{"at the cap", MaxTrendMonths},
		{"above the cap is clamped, not truncated further", MaxTrendMonths * 10},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q, db := setupTestDB(t)
			h := NewHandlerWithClock(q, db, fixedClock{t: anchor})
			user := seedTestUser(t, q, "alice", "member")

			req := httptest.NewRequest(http.MethodGet,
				fmt.Sprintf("/api/reports/income-expenses?months=%d", tc.months), nil)
			req = withUser(req, user)
			rec := httptest.NewRecorder()

			h.handleReportIncomeExpenses(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
			}

			var resp struct {
				Data []struct {
					Year  int `json:"year"`
					Month int `json:"month"`
				} `json:"data"`
			}
			decodeResponse(t, rec, &resp)

			if len(resp.Data) != MaxTrendMonths {
				t.Fatalf("got %d buckets for months=%d, want %d (the server must serve the whole window it advertises)",
					len(resp.Data), tc.months, MaxTrendMonths)
			}
			if first := resp.Data[0]; first.Year != wantFirstYear || first.Month != wantFirstMonth {
				t.Errorf("earliest bucket = %d-%02d, want %d-%02d", first.Year, first.Month, wantFirstYear, wantFirstMonth)
			}
			if last := resp.Data[len(resp.Data)-1]; last.Year != anchor.Year() || last.Month != int(anchor.Month()) {
				t.Errorf("latest bucket = %d-%02d, want %d-%02d (the window must end at the current month)",
					last.Year, last.Month, anchor.Year(), int(anchor.Month()))
			}
		})
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
	err := validateTransactionRequest(req, noStoredDate)
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
		Tags:        ptr(strings.Repeat("x", 501)),
	}
	err := validateTransactionRequest(req, noStoredDate)
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
		Notes:       ptr(strings.Repeat("x", 2001)),
	}
	err := validateTransactionRequest(req, noStoredDate)
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
		Tags:        ptr(strings.Repeat("x", 500)),
		Notes:       ptr(strings.Repeat("x", 2000)),
	}
	err := validateTransactionRequest(req, noStoredDate)
	if err != nil {
		t.Errorf("expected no error for valid lengths, got %v", err)
	}
}
