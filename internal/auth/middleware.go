package auth

import (
	"context"
	"net/http"
	"time"

	"github.com/elienop/spendrop/internal/database"
)

type contextKey string

const UserContextKey contextKey = "user"

// RequireAuth middleware checks for a valid session cookie and injects the
// authenticated user into the request context.
func RequireAuth(queries *database.Queries) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie("session")
			if err != nil {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}

			// Valid tokens are exactly 64 hex characters (32 bytes)
			if len(cookie.Value) != 64 {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}

			session, err := queries.GetSession(r.Context(), cookie.Value)
			if err != nil {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}

			if session.ExpiresAt.Before(time.Now()) {
				queries.DeleteSession(r.Context(), cookie.Value)
				http.Error(w, `{"error":"session expired"}`, http.StatusUnauthorized)
				return
			}

			user, err := queries.GetUserByID(r.Context(), session.UserID)
			if err != nil {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}

			ctx := context.WithValue(r.Context(), UserContextKey, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireAdmin middleware checks that the authenticated user has the admin role.
// Must be used after RequireAuth.
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
