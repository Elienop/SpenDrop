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
//
// The fixture is the whole point. This test used to send out-of-range tokens
// that matched no session row, so all of them returned 401 whether or not the
// length check existed — deleting the bound entirely left it green. Here a real
// session is created FOR each out-of-range token, so the lookup would succeed if
// it were reached. 401 can then only mean the request was rejected on length,
// before the hash and before the query.
func TestAuthenticateSession_RejectsAbsurdTokenLengths(t *testing.T) {
	q, _ := setupTestDB(t)
	user := createTestUser(t, q, "member")

	for _, tc := range []struct {
		name  string
		token string
	}{
		{"one char under the floor", strings.Repeat("a", minSessionTokenHexLen-1)},
		{"one char over the ceiling", strings.Repeat("a", maxSessionTokenHexLen+1)},
		{"far over the ceiling", strings.Repeat("a", maxSessionTokenHexLen*10)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A session that WOULD authenticate if the length gate let it through.
			createTestSession(t, q, user.ID, tc.token, time.Now().Add(time.Hour))

			req := httptest.NewRequest(http.MethodGet, "/api/whatever", nil)
			req.AddCookie(&http.Cookie{Name: "session", Value: tc.token})
			rec := httptest.NewRecorder()

			RequireAuth(q)(okHandler()).ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("token of length %d: status = %d, want 401 — an out-of-range token "+
					"reached the session lookup instead of being rejected on length",
					len(tc.token), rec.Code)
			}
		})
	}

	// Absent and obviously-malformed cookies must still 401. These cannot
	// distinguish the length gate from the lookup, and are here only so the
	// ordinary paths stay covered.
	for _, token := range []string{"", "short"} {
		req := httptest.NewRequest(http.MethodGet, "/api/whatever", nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		rec := httptest.NewRecorder()
		RequireAuth(q)(okHandler()).ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("token %q: status = %d, want 401", token, rec.Code)
		}
	}
}
