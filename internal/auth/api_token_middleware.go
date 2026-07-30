package auth

import (
	"context"
	"database/sql"
	"log"
	"net"
	"net/http"
	"net/netip"
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
	trustedProxyCIDRs   []netip.Prefix
)

// SetTrustProxyHeaders mirrors the api package's proxy-trust configuration into
// this package. Called from api.ApplyConfig; the two packages cannot share state
// directly because internal/auth must not import internal/api.
//
// cidrs is the preferred input. hops is the deprecated fallback, used only when
// cidrs is empty — see ClientIPForRateLimit for why counting is unsafe.
func SetTrustProxyHeaders(v bool, hops int, cidrs []netip.Prefix) {
	trustProxyHeadersMu.Lock()
	defer trustProxyHeadersMu.Unlock()
	trustProxyHeaders = v
	if hops < 1 {
		hops = 1
	}
	trustedProxyHops = hops
	trustedProxyCIDRs = cidrs
}

// maxXFFEntries bounds how far left the scan will walk. The header is entirely
// attacker-controlled, so without a bound a single request carrying tens of
// thousands of commas would make the server do that much parsing work per
// request. Real chains are one or two entries.
const maxXFFEntries = 32

// parseXFFAddr normalises one X-Forwarded-For entry into a comparable address.
//
// Entries are not reliably bare IPs in the wild: proxies emit "1.2.3.4:5678" and
// "[::1]:443", clients inject IPv4-mapped IPv6, and link-local addresses arrive
// with a zone. A prefix comparison fails on all of those unless they are
// normalised first, and a failed comparison here means a trusted proxy is
// mistaken for a client — the exact confusion this whole function exists to
// prevent.
func parseXFFAddr(s string) (netip.Addr, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return netip.Addr{}, false
	}
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr().Unmap().WithZone(""), true
	}
	if a, err := netip.ParseAddr(s); err == nil {
		return a.Unmap().WithZone(""), true
	}
	return netip.Addr{}, false
}

func isTrustedProxy(ip netip.Addr, cidrs []netip.Prefix) bool {
	for _, p := range cidrs {
		if p.Contains(ip) {
			return true
		}
	}
	return false
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
// Which entry to take is decided by ADDRESS, not by position. The scan starts at
// the rightmost entry and walks left while each entry is a known proxy; the first
// entry that is not returns as the client. If every entry is a known proxy the
// socket address is used. Since each appending proxy contributes the address it
// personally observed, the first non-proxy entry from the right is always the
// address the innermost trusted proxy actually saw — and anything an attacker
// prepended sits further left, where the scan never reaches.
//
// This replaced a hop COUNT, which was unsafe in both directions and only
// appeared to work. Counting N from the right is correct exactly when N matches
// the real chain, and nothing enforces that:
//
//   - Overcounted, it was a COMPLETE bypass of the limiter. An attacker who
//     prepends one entry makes the header long enough that len(parts)-hops lands
//     on their own forged value, so every request gets a fresh bucket key.
//     Measured with hops=2 against one appending proxy: 5 requests from a single
//     address produced 5 distinct keys, and the same attacker could aim at the
//     bucket honest traffic falls back to and lock out the household.
//   - Undercounted, every visitor selected the same outer-edge address, silently
//     collapsing the household into one bucket.
//
// Neither failure is detectable from inside the request, which is why the
// previous attempt to fix this — warning when the header was shorter than the
// count — could not work: the warning fired on honest traffic while the attack
// path padded the header and never tripped it. An address set has no count to
// get wrong.
//
// The CIDRs must name the proxies and nothing more. Trusting a range wide enough
// to contain untrusted hosts lets those hosts be skipped over, reaching whatever
// they prepended — the same caveat that applies to nginx's set_real_ip_from.
func ClientIPForRateLimit(r *http.Request) string {
	trustProxyHeadersMu.RLock()
	trusted := trustProxyHeaders
	hops := trustedProxyHops
	cidrs := trustedProxyCIDRs
	trustProxyHeadersMu.RUnlock()

	if !trusted {
		return extractRemoteIP(r.RemoteAddr)
	}
	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" {
		return extractRemoteIP(r.RemoteAddr)
	}

	if len(cidrs) == 0 {
		// Deprecated hop-count mode, kept so an existing deployment configured
		// only with TRUSTED_PROXY_HOPS keeps working rather than silently
		// reverting to one shared bucket on upgrade.
		return clientIPByHopCount(r, xff, hops)
	}

	// Walk right to left WITHOUT splitting: the header is attacker-controlled, so
	// a bounded reverse scan avoids allocating a slice proportional to however
	// many commas were sent.
	rest := xff
	for n := 0; n < maxXFFEntries && rest != ""; n++ {
		entry := rest
		if i := strings.LastIndexByte(rest, ','); i >= 0 {
			entry, rest = rest[i+1:], rest[:i]
		} else {
			rest = ""
		}
		ip, ok := parseXFFAddr(entry)
		if !ok {
			// An unparseable entry means the chain cannot be reasoned about from
			// here leftward. Stopping is essential: continuing would let an
			// attacker insert garbage to step the scan past their real address
			// and onto a forged one.
			break
		}
		if !isTrustedProxy(ip, cidrs) {
			return ip.String()
		}
	}
	// Every entry was a trusted proxy (or the header was unusable). The socket
	// address is the only thing left that no client can forge.
	return extractRemoteIP(r.RemoteAddr)
}

// clientIPByHopCount is the deprecated positional selection. Retained only for
// deployments that set TRUSTED_PROXY_HOPS and no CIDRs; see ClientIPForRateLimit
// for why it cannot be made safe.
func clientIPByHopCount(r *http.Request, xff string, hops int) string {
	warnHopModeDeprecated()
	parts := strings.Split(xff, ",")
	idx := len(parts) - hops
	if idx < 0 {
		// A header shorter than the count proves the count is too high, which is
		// the bypass condition. Honest clients send no X-Forwarded-For, so this
		// fires on the first ordinary request.
		warnProxyHopMismatch(hops, len(parts))
	} else if idx < len(parts) {
		if ip, ok := parseXFFAddr(parts[idx]); ok {
			return ip.String()
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
	log.Printf("SECURITY: TRUSTED_PROXY_HOPS=%d but X-Forwarded-For carried %d entries. "+
		"The count is too high, which means an attacker who prepends an entry can select "+
		"their own value and get a fresh rate-limit bucket per request, bypassing the login "+
		"limiter entirely. Set TRUSTED_PROXY_CIDRS to your proxy's address range instead — "+
		"it needs no count.", hops, got)
}

var (
	hopDeprecationWarnMu   sync.Mutex
	hopDeprecationWarnDone bool
)

// warnHopModeDeprecated fires once per process. Hop-count mode is reachable only
// by explicit configuration, so one line at first use is enough to tell an
// operator they are on the unsafe path without adding per-request log volume.
func warnHopModeDeprecated() {
	hopDeprecationWarnMu.Lock()
	defer hopDeprecationWarnMu.Unlock()
	if hopDeprecationWarnDone {
		return
	}
	hopDeprecationWarnDone = true
	log.Printf("rate-limit: TRUSTED_PROXY_HOPS is deprecated and cannot be made safe — " +
		"if the count is ever higher than the real proxy chain, the login rate limiter is " +
		"bypassable. Set TRUSTED_PROXY_CIDRS to the address range of your reverse proxy " +
		"(e.g. your Docker network) to select the client by address instead of by position.")
}
