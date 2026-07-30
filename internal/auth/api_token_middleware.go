package auth

import (
	"context"
	"database/sql"
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
	trustedProxyCIDRs   []netip.Prefix
)

// SetTrustProxyHeaders mirrors the api package's proxy-trust configuration into
// this package. Called from api.ApplyConfig; the two packages cannot share state
// directly because internal/auth must not import internal/api.
//
// cidrs is the only input, because naming the proxies by ADDRESS is the only way
// to establish that a proxy is in front at all. An empty set with v true means
// the header cannot be believed from anyone, so every request keys on its socket
// address; config.Validate refuses that combination at boot.
func SetTrustProxyHeaders(v bool, cidrs []netip.Prefix) {
	trustProxyHeadersMu.Lock()
	defer trustProxyHeadersMu.Unlock()
	trustProxyHeaders = v
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

// socketAddrKey is the rate-limit key for the connection itself: the peer
// address, normalised the same way header entries are.
//
// Normalising matters because the two paths must agree on what one host is
// called. The header path returns parseXFFAddr's ip.String(), which is unmapped
// and zone-stripped; returning RemoteAddr verbatim here gave the SAME host a
// second key — "::ffff:10.0.0.1" against "10.0.0.1", "fe80::1%eth0" against
// "fe80::1" — and therefore a second bucket, doubling its real attempt allowance
// depending on which path produced the key.
//
// A shape SplitHostPort or netip cannot parse (unix socket, pathological client)
// falls through to the raw value: an empty key would merge every such client
// into one bucket.
func socketAddrKey(r *http.Request) string {
	raw := extractRemoteIP(r.RemoteAddr)
	if addr, ok := parseXFFAddr(raw); ok {
		return addr.String()
	}
	return raw
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
// A CIDR set is REQUIRED for any of that to happen. Trusting the header without
// one has no safe reading: nothing but the socket address can establish that a
// proxy is in front, so with no CIDRs every request keys on its socket address
// and config.Validate refuses the combination at boot rather than letting it
// degrade quietly.
//
// This replaced a hop COUNT, deleted outright rather than deprecated because it
// was bypassable in its DEFAULT configuration and protected no released
// deployment — the knob was introduced and removed inside one unreleased branch.
// Counting N from the right is correct exactly when N matches the real chain, and
// nothing enforces that:
//
//   - It had no immediate-peer check at all, so at the default hops=1 any direct
//     peer — a LAN host, a sibling container, an exposed port — simply sent its
//     own X-Forwarded-For and was believed. Measured: 25 requests from one source
//     produced 25 distinct rate-limit keys, with nothing misconfigured.
//   - Overcounted, the same total bypass via a prepended entry: len(parts)-hops
//     lands on the attacker's own forged value.
//   - Undercounted, every visitor selected the same outer-edge address, silently
//     collapsing the household into one bucket.
//
// Either way an attacker could also aim at a victim's address and pin their
// bucket at 429 permanently, since Exhausted short-circuits before the password
// check. None of it is detectable from inside the request, which is why the
// warning that shipped with it could not work: it fired on honest traffic while
// the attack path padded the header and never tripped it. An address set has no
// count to get wrong.
//
// The CIDRs must name the proxies and nothing more. Trusting a range wide enough
// to contain untrusted hosts lets those hosts be skipped over, reaching whatever
// they prepended — the same caveat that applies to nginx's set_real_ip_from.
func ClientIPForRateLimit(r *http.Request) string {
	trustProxyHeadersMu.RLock()
	trusted := trustProxyHeaders
	cidrs := trustedProxyCIDRs
	trustProxyHeadersMu.RUnlock()

	if !trusted {
		return socketAddrKey(r)
	}
	// Values, not Get. Get returns only the FIRST X-Forwarded-For field line, and
	// a proxy is free to append a SECOND line rather than extending the client's.
	// Where it does, the attacker's own line is the first one, so Get handed back
	// a header the proxy had never touched and the walk below started from
	// attacker-controlled data. RFC 7230 says repeated field lines are equivalent
	// to one comma-joined value in order, so joining is both correct and what
	// puts the proxy's entry rightmost where the walk begins.
	xff := strings.Join(r.Header.Values("X-Forwarded-For"), ", ")
	if xff == "" {
		return socketAddrKey(r)
	}

	// The immediate peer must itself be a trusted proxy before ANY part of the
	// header is believed. With no CIDRs configured nothing can be trusted, so the
	// check below fails closed onto the socket address — which is why there is no
	// separate empty-set branch here.
	//
	// Without this the whole scheme is decorative: a client that reaches the
	// server directly — a LAN peer, a container on the same network, an exposed
	// port alongside the proxy — simply sends its own X-Forwarded-For, and since
	// the forged entry is not in a trusted range it is taken as the client on the
	// first step of the walk. Measured: one direct peer minted a fresh rate-limit
	// key for every value it chose. TRUST_PROXY_HEADERS asks "is a proxy in
	// front?", and only the socket address can answer that; the header cannot
	// vouch for itself. This is the same precondition nginx applies by only
	// honouring the header from set_real_ip_from peers.
	peer, ok := parseXFFAddr(extractRemoteIP(r.RemoteAddr))
	if !ok || !isTrustedProxy(peer, cidrs) {
		return socketAddrKey(r)
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
	return socketAddrKey(r)
}
