package auth

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/elienop/spendrop/internal/database"
	"github.com/elienop/spendrop/internal/ratelimit"
)

type contextKey string

const UserContextKey contextKey = "user"

// RequireAuth middleware checks for a valid session cookie and injects the
// authenticated user into the request context.
func RequireAuth(queries *database.Queries) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authed, ok := authenticateSession(w, r, queries)
			if !ok {
				return
			}
			next.ServeHTTP(w, authed)
		})
	}
}

// RequireAuthOrAPIToken accepts either a Bearer token OR a session cookie.
//
// Bearer is exclusive when present: if the request carries an
// `Authorization: Bearer ` header, Bearer validation runs and there is NO
// fallback to the session cookie. This preserves the Bearer surface's
// rate-limit semantics (the shared authFailLimiter is the only backstop
// against hash enumeration) and prevents a probe from silently falling back
// to cookie auth after the bearer path 401s.
//
// If no Bearer header is present, the session cookie path runs (identical to
// RequireAuth).
//
// If neither is present, 401.
//
// authFailLimiter is the SAME bucket used by the dedicated Bearer-only
// /api/homepage/summary mount — sharing it keeps the 30-per-10-min quota
// coherent across every endpoint a bearer can reach.
func RequireAuthOrAPIToken(
	queries *database.Queries,
	authFailLimiter *ratelimit.Bucket,
) func(http.Handler) http.Handler {
	bearerMW := RequireAPIToken(queries, authFailLimiter)
	return func(next http.Handler) http.Handler {
		bearerChain := bearerMW(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
				// Bearer present → delegate entirely to the Bearer validator.
				// Success runs `next`; failure returns 401/429 with the opaque
				// body. Never falls back to the cookie path.
				bearerChain.ServeHTTP(w, r)
				return
			}
			authed, ok := authenticateSession(w, r, queries)
			if !ok {
				return
			}
			next.ServeHTTP(w, authed)
		})
	}
}

// authenticateSession validates the session cookie and returns a new
// *http.Request whose context carries the authenticated user under
// UserContextKey. On failure it writes the appropriate 401 JSON body to w
// and returns (nil, false). Factored out of RequireAuth so
// RequireAuthOrAPIToken can reuse the exact same validation path without
// duplicating it.
func authenticateSession(w http.ResponseWriter, r *http.Request, queries *database.Queries) (*http.Request, bool) {
	cookie, err := r.Cookie("session")
	if err != nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return nil, false
	}

	// Valid tokens are exactly 64 hex characters (32 bytes)
	if len(cookie.Value) != 64 {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return nil, false
	}

	session, err := queries.GetSession(r.Context(), cookie.Value)
	if err != nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return nil, false
	}

	if session.ExpiresAt.Before(time.Now()) {
		queries.DeleteSession(r.Context(), cookie.Value)
		http.Error(w, `{"error":"session expired"}`, http.StatusUnauthorized)
		return nil, false
	}

	user, err := queries.GetUserByID(r.Context(), session.UserID)
	if err != nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return nil, false
	}

	ctx := context.WithValue(r.Context(), UserContextKey, user)
	return r.WithContext(ctx), true
}

// RequireAdmin middleware checks that the authenticated user has the admin role.
// Must be used after RequireAuth or RequireAuthOrAPIToken — it reads the user
// from UserContextKey, which either auth path populates identically.
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := r.Context().Value(UserContextKey).(database.User)
		if !ok || user.Role != "admin" {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// GetUser extracts the authenticated user from the request context.
func GetUser(r *http.Request) (database.User, bool) {
	user, ok := r.Context().Value(UserContextKey).(database.User)
	return user, ok
}
