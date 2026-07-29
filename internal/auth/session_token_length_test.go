package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestAuthenticateSession_AcceptsConfiguredTokenLengths is the regression test
// for the SESSION_TOKEN_BYTES trap.
//
// Config accepts SESSION_TOKEN_BYTES >= 16, so the session cookie is 2*N hex
// characters for whatever N the operator picks. The middleware hardcoded an
// equality check on 64 (32 bytes), so ANY other value bricked the deployment
// in the most confusing way available: login succeeded and set a cookie, then
// every subsequent request returned 401, with nothing in the logs pointing at
// the setting.
//
// The authoritative check is the session lookup — a token of the wrong length
// simply hashes to something no row holds — so the length test only needs to
// reject input too short to be any legal token, and cap absurdly long values.
func TestAuthenticateSession_AcceptsConfiguredTokenLengths(t *testing.T) {
	q, _ := setupTestDB(t)
	user := createTestUser(t, q, "member")

	// 16, 24 and 32 bytes — all legal under config's SESSION_TOKEN_BYTES >= 16.
	for _, tokenBytes := range []int{16, 24, 32} {
		token := strings.Repeat("a", tokenBytes*2)
		createTestSession(t, q, user.ID, token, time.Now().Add(time.Hour))

		req := httptest.NewRequest(http.MethodGet, "/api/whatever", nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		rec := httptest.NewRecorder()

		RequireAuth(q)(okHandler()).ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("SESSION_TOKEN_BYTES=%d (%d hex chars): status = %d, want 200 — "+
				"this configuration authenticates nobody",
				tokenBytes, len(token), rec.Code)
		}
	}
}

// TestAuthenticateSession_RejectsAbsurdTokenLengths keeps the relaxed bound
// honest: it must still reject values that cannot be any legal token, so we
// never hash unbounded attacker-supplied input.
func TestAuthenticateSession_RejectsAbsurdTokenLengths(t *testing.T) {
	q, _ := setupTestDB(t)

	for _, token := range []string{
		"",
		"short",
		strings.Repeat("a", minSessionTokenHexLen-1),
		strings.Repeat("a", maxSessionTokenHexLen+1),
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/whatever", nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		rec := httptest.NewRecorder()

		RequireAuth(q)(okHandler()).ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("token of length %d: status = %d, want 401", len(token), rec.Code)
		}
	}
}
