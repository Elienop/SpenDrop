package auth

import (
	"context"
	"database/sql"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/elienop/spendrop/internal/database"
	"github.com/elienop/spendrop/internal/ratelimit"
)

// Opaque bodies — enumeration by error text is structurally impossible.
// See spec §3.8 guardrail 11.
const opaqueBearerFailureBody = `{"error":"invalid or missing token"}`
const opaqueRateLimitBody = `{"error":"rate limit"}`

// RequireAPIToken validates `Authorization: Bearer <token>`, attaches the
// owning user to the request context under UserContextKey, and debounces a
// last-used touch. The touch is synchronous best-effort — the SQL-level
// 60s debounce in TouchAPITokenLastUsed caps write frequency, and any
// error is intentionally swallowed so a flaky DB never blocks the
// request. authFailLimiter is consumed only on valid-shape-but-unknown-
// hash misses, never on malformed gibberish.
func RequireAPIToken(
	queries *database.Queries,
	authFailLimiter *ratelimit.Bucket,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := extractRemoteIP(r.RemoteAddr)

			authz := r.Header.Get("Authorization")
			if !strings.HasPrefix(authz, "Bearer ") {
				writeBearerFailure(w)
				return
			}
			plaintext := strings.TrimPrefix(authz, "Bearer ")

			// Shape pre-filter rejects malformed gibberish before the DB
			// AND before the rate-limit bucket (spec §3.7, §3.8 #1).
			if !IsValidTokenFormat(plaintext) {
				writeBearerFailure(w)
				return
			}

			if authFailLimiter.Exhausted(ip) {
				w.Header().Set("Retry-After", authFailLimiter.RetryAfter(ip))
				writeRateLimit(w)
				return
			}

			// GetAPITokenByHash's WHERE already rejects revoked/expired rows
			// (Chunk 2). Unknown hash, revoked, expired, and DB errors all
			// produce the same opaque 401 — the bucket is consumed in every
			// case so a flaky DB cannot become an enumeration oracle.
			hash := HashAPIToken(plaintext)
			tok, err := queries.GetAPITokenByHash(r.Context(), hash)
			if err != nil {
				authFailLimiter.Consume(ip)
				writeBearerFailure(w)
				return
			}

			// ON DELETE CASCADE means api_tokens.user_id normally can't
			// dangle, but this covers races (user delete mid-request) and
			// DB errors — same opaque 401.
			user, err := queries.GetUserByID(r.Context(), tok.UserID)
			if err != nil {
				writeBearerFailure(w)
				return
			}

			// Debounced last-used touch. Synchronous best-effort; the SQL
			// WHERE clause in TouchAPITokenLastUsed discards no-op writes
			// inside the 60s window, and the error return is intentionally
			// swallowed so a flaky DB never blocks the request (spec §3.5).
			if shouldTouch(tok.LastUsedAt) {
				_ = queries.TouchAPITokenLastUsed(r.Context(), database.TouchAPITokenLastUsedParams{
					ID:         tok.ID,
					LastUsedIp: sql.NullString{String: ip, Valid: true},
				})
			}

			// By-value (not pointer) matches RequireAuth at
			// internal/auth/middleware.go:50. Handlers type-assert to
			// database.User — a pointer would panic.
			ctx := context.WithValue(r.Context(), UserContextKey, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func writeBearerFailure(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(opaqueBearerFailureBody))
}

func writeRateLimit(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	_, _ = w.Write([]byte(opaqueRateLimitBody))
}

// shouldTouch is the Go-side fast-path skip; the SQL WHERE in
// TouchAPITokenLastUsed is the authoritative gate.
func shouldTouch(lastUsed sql.NullTime) bool {
	if !lastUsed.Valid {
		return true
	}
	return time.Since(lastUsed.Time) > 60*time.Second
}

// extractRemoteIP returns the IP portion of "host:port". Duplicated from
// internal/api/auth_handlers.go:17 to avoid an internal/auth ↔ internal/api
// import cycle. If SplitHostPort fails (unix socket, pathological client),
// the raw value is returned.
func extractRemoteIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}
