package auth

import (
	"context"
	"database/sql"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
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
			ip := ClientIPForRateLimit(r)

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

			// ON DELETE CASCADE makes this branch unreachable in practice
			// (the FK from api_tokens.user_id ensures GetAPITokenByHash
			// returns no row before we get here). Consume the bucket
			// anyway — the invariant "every DB-path failure consumes
			// uniformly" must not depend on runtime constraint-timing
			// to hold.
			user, err := queries.GetUserByID(r.Context(), tok.UserID)
			if err != nil {
				authFailLimiter.Consume(ip)
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

var (
	trustProxyHeadersMu sync.RWMutex
	trustProxyHeaders   bool
	trustedProxyHops    = 1
)

// SetTrustProxyHeaders mirrors the api package's TRUST_PROXY_HEADERS runtime
// flag into this package. Called from api.ApplyConfig; the two packages cannot
// share state directly because internal/auth must not import internal/api.
func SetTrustProxyHeaders(v bool, hops int) {
	trustProxyHeadersMu.Lock()
	defer trustProxyHeadersMu.Unlock()
	trustProxyHeaders = v
	if hops < 1 {
		hops = 1
	}
	trustedProxyHops = hops
}

// ClientIPForRateLimit resolves the address a rate limiter should key on.
//
// Exported and shared: internal/api keys its login/register limiter on the
// same value, and the second copy of this logic drifted from its own doc
// comment within a single commit. One implementation, one set of semantics.
//
// Behind the reverse proxy the README documents, r.RemoteAddr is the PROXY's
// address for every request, so the whole household shares one bucket and one
// attacker's failed logins lock everyone out. Reading X-Forwarded-For fixes
// that — but only when a proxy is really in front, because on a directly
// exposed server the header is attacker-controlled and would let anyone mint a
// fresh identity per request. Hence TRUST_PROXY_HEADERS, defaulting to off.
//
// Which entry to take is a COUNT: each appending proxy adds the address it
// saw, so with N trusted hops the client is N-from-the-right. Everything
// further left is client-supplied and is never trusted.
//
// The count MUST match the real chain, and getting it wrong fails in two
// directions. Too high, and honest clients — who send no X-Forwarded-For at
// all — produce a header shorter than the count and fall back to the socket
// address, putting everyone in one bucket. Worse, an attacker who PREPENDS an
// entry makes the header exactly long enough that the selected index lands on
// their own forged value, giving them unlimited buckets and defeating the
// limiter entirely. That is a misconfiguration rather than a design flaw, but
// it is silent, so the short-header case warns.
func ClientIPForRateLimit(r *http.Request) string {
	trustProxyHeadersMu.RLock()
	trusted := trustProxyHeaders
	hops := trustedProxyHops
	trustProxyHeadersMu.RUnlock()

	if trusted {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			idx := len(parts) - hops
			if idx < 0 {
				// Fewer entries than configured hops. Under any overcount this
				// fires on the FIRST ordinary request, because honest clients
				// send no X-Forwarded-For — which makes it a reliable detector
				// for both failure modes described above.
				warnProxyHopMismatch(hops, len(parts))
			} else if idx < len(parts) {
				candidate := strings.TrimSpace(parts[idx])
				// Must parse as an address: a garbage or empty entry would
				// otherwise become a rate-limit key in its own right.
				if candidate != "" && net.ParseIP(candidate) != nil {
					return candidate
				}
			}
		}
	}
	return extractRemoteIP(r.RemoteAddr)
}

// proxyHopWarnInterval throttles the misconfiguration warning. The condition is
// evaluated per request and is permanent once tripped, so an unthrottled log
// line would be one per request forever.
const proxyHopWarnInterval = 10 * time.Minute

var (
	proxyWarnMu   sync.Mutex
	proxyWarnLast time.Time
)

func warnProxyHopMismatch(hops, got int) {
	proxyWarnMu.Lock()
	defer proxyWarnMu.Unlock()
	if !proxyWarnLast.IsZero() && time.Since(proxyWarnLast) < proxyHopWarnInterval {
		return
	}
	proxyWarnLast = time.Now()
	log.Printf("rate-limit: TRUSTED_PROXY_HOPS=%d but X-Forwarded-For carried %d entries; "+
		"falling back to the socket address, so every client now shares one rate-limit "+
		"bucket. Check how many proxies actually append to the header.", hops, got)
}
