package auth

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

// These tests cover SELECTION: which of the addresses a request carries belongs
// to the client. That is ClientIP's whole job. The rate-limit key is
// rateLimitKey(ClientIP(r)) — masking is covered separately in
// rate_limit_key_test.go, so a test here that says "N sources produced one
// rate-limit key" is still exact: one selected address can only mask to one key.

// withTrustedProxyCIDRs configures address-set selection, the only mode there is.
func withTrustedProxyCIDRs(t *testing.T, trusted bool, cidrs []string) {
	t.Helper()
	trustProxyHeadersMu.RLock()
	prevTrust, prevCIDRs := trustProxyHeaders, trustedProxyCIDRs
	trustProxyHeadersMu.RUnlock()

	var parsed []netip.Prefix
	for _, raw := range cidrs {
		p, err := netip.ParsePrefix(raw)
		if err != nil {
			addr, aerr := netip.ParseAddr(raw)
			if aerr != nil {
				t.Fatalf("bad fixture CIDR %q: %v", raw, err)
			}
			p = netip.PrefixFrom(addr.Unmap(), addr.Unmap().BitLen())
		}
		parsed = append(parsed, p.Masked())
	}
	SetTrustProxyHeaders(trusted, parsed)
	t.Cleanup(func() { SetTrustProxyHeaders(prevTrust, prevCIDRs) })
}

func reqWithXFF(xff, remoteAddr string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	r.RemoteAddr = remoteAddr
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	return r
}

// TestClientIP_WithoutCIDRsIgnoresTheHeader is THE regression test
// for the deleted hop-count mode.
//
// With TRUST_PROXY_HEADERS=true and no CIDRs configured, selection used to fall
// into clientIPByHopCount, which had NO immediate-peer check — the CIDR path's
// own comment says that without one "the whole scheme is decorative". At the
// DEFAULT hops=1 that made every direct peer a full bypass of the login limiter,
// with no misconfiguration required: a LAN host, a sibling container or an
// exposed port simply sent its own X-Forwarded-For and the rightmost entry was
// taken as the client. Measured before the deletion: 25 requests from one source
// produced 25 distinct rate-limit keys.
//
// The same primitive aimed at someone else's address pinned a victim's bucket at
// 429, and because Exhausted short-circuits before the password check the victim
// could never reset it.
//
// Without CIDRs there is nothing that can establish a proxy is in front, so the
// only safe answer is the socket address. Config now refuses to boot in this
// combination; this pins the behaviour if it is ever reached anyway.
func TestClientIP_WithoutCIDRsIgnoresTheHeader(t *testing.T) {
	withTrustedProxyCIDRs(t, true, nil)

	const direct = "203.0.113.66"
	keys := map[string]bool{}
	for i := 0; i < 25; i++ {
		forged := fmt.Sprintf("10.0.%d.%d", i/256, i%256)
		got := ClientIP(reqWithXFF(forged, direct+":40000"))
		keys[got] = true
		if got != direct {
			t.Errorf("forged %q: keyed on %q, want the socket address %q", forged, got, direct)
		}
	}
	if len(keys) != 1 {
		t.Errorf("one source minted %d distinct rate-limit keys; want 1 — the login "+
			"limiter is bypassable, and it is the only throttle on password guessing", len(keys))
	}

	// A padded header must not help either: that is what defeated the old
	// mismatch warning, which fired on honest traffic and never on the attack.
	if got := ClientIP(reqWithXFF("1.1.1.1, 2.2.2.2, 3.3.3.3", direct+":40000")); got != direct {
		t.Errorf("padded header: keyed on %q, want the socket address %q", got, direct)
	}
}

// TestClientIP_NormalisesTheSocketFallback is the regression test
// for the two code paths disagreeing about what one host is called.
//
// The header path returns ip.String() from parseXFFAddr, which is unmapped and
// zone-stripped. The socket fallback returned r.RemoteAddr's host verbatim. So
// the SAME host got two different rate-limit keys depending on which path
// produced it — "::ffff:10.0.0.1" from the fallback versus "10.0.0.1" from the
// header, and "fe80::1%eth0" versus "fe80::1". Two keys means two buckets, which
// doubles the real attempt allowance and makes the limiter's accounting depend
// on routing rather than on identity.
func TestClientIP_NormalisesTheSocketFallback(t *testing.T) {
	t.Run("untrusted deployment", func(t *testing.T) {
		withTrustedProxyCIDRs(t, false, nil)
		cases := map[string]string{
			"[::ffff:10.0.0.1]:5000": "10.0.0.1",
			"10.0.0.1:5000":          "10.0.0.1",
			"[fe80::1%eth0]:5000":    "fe80::1",
			"[fe80::1]:5000":         "fe80::1",
			"[2001:db8::1]:5000":     "2001:db8::1",
		}
		for remote, want := range cases {
			if got := ClientIP(reqWithXFF("", remote)); got != want {
				t.Errorf("RemoteAddr=%q: got %q, want %q", remote, got, want)
			}
		}
	})

	t.Run("untrusted peer falls back normalised", func(t *testing.T) {
		withTrustedProxyCIDRs(t, true, []string{"172.18.0.0/16"})
		// A direct peer whose header is refused: the fallback is still a key.
		if got := ClientIP(reqWithXFF("1.1.1.1", "[::ffff:203.0.113.9]:5000")); got != "203.0.113.9" {
			t.Errorf("got %q, want the normalised socket address 203.0.113.9", got)
		}
	})

	t.Run("all-entries-trusted falls back normalised", func(t *testing.T) {
		withTrustedProxyCIDRs(t, true, []string{"172.18.0.0/16"})
		if got := ClientIP(reqWithXFF("172.18.0.2", "[::ffff:172.18.0.1]:5000")); got != "172.18.0.1" {
			t.Errorf("got %q, want the normalised socket address 172.18.0.1", got)
		}
	})

	// The property the whole fix is for: one host, one key, whichever path ran.
	t.Run("both paths agree on one host", func(t *testing.T) {
		withTrustedProxyCIDRs(t, true, []string{"172.18.0.0/16"})

		viaHeader := ClientIP(reqWithXFF("::ffff:10.0.0.1", "172.18.0.1:5000"))
		withTrustedProxyCIDRs(t, false, nil)
		viaSocket := ClientIP(reqWithXFF("", "[::ffff:10.0.0.1]:5000"))

		if viaHeader != viaSocket {
			t.Errorf("the same host keys as %q through the header and %q through the socket "+
				"fallback — it occupies two rate-limit buckets", viaHeader, viaSocket)
		}
	})

	// A shape SplitHostPort cannot parse (unix socket, pathological client) must
	// still yield the raw value rather than an empty key, which would merge every
	// such client into one bucket.
	t.Run("unparseable remote addresses are passed through", func(t *testing.T) {
		withTrustedProxyCIDRs(t, false, nil)
		if got := ClientIP(reqWithXFF("", "/tmp/spendrop.sock")); got != "/tmp/spendrop.sock" {
			t.Errorf("got %q, want the raw value passed through", got)
		}
	})
}

// TestClientIP_UntrustedIgnoresHeader pins the default: on a
// directly exposed server X-Forwarded-For is attacker-controlled, so trusting
// it would let anyone mint a fresh bucket per request.
func TestClientIP_UntrustedIgnoresHeader(t *testing.T) {
	withTrustedProxyCIDRs(t, false, nil)
	got := ClientIP(reqWithXFF("1.2.3.4", "10.0.0.5:5000"))
	if got != "10.0.0.5" {
		t.Errorf("got %q, want the socket address 10.0.0.5", got)
	}
}

// TestClientIP_RejectsNonAddressEntry guards against a garbage or
// injected entry becoming a rate-limit key of its own.
//
// The peer is inside the trusted range so the header IS read — otherwise every
// case would fall back for the trivial reason that no proxy is configured, and
// the test would pass without exercising entry parsing at all.
func TestClientIP_RejectsNonAddressEntry(t *testing.T) {
	withTrustedProxyCIDRs(t, true, []string{"172.18.0.0/16"})
	for _, xff := range []string{"not-an-ip", "  ", "evil.example.com"} {
		if got := ClientIP(reqWithXFF(xff, "172.18.0.1:5000")); got != "172.18.0.1" {
			t.Errorf("xff=%q: got %q, want the socket-address fallback", xff, got)
		}
	}
	// Control: a well-formed entry from the same peer IS taken, proving the
	// fallbacks above are caused by the garbage and not by the configuration.
	if got := ClientIP(reqWithXFF("203.0.113.5", "172.18.0.1:5000")); got != "203.0.113.5" {
		t.Errorf("well-formed entry: got %q, want 203.0.113.5", got)
	}
}

// TestClientIP_NormalisesAddressForms pins the shapes an entry
// legitimately arrives in.
//
// This used to reject "1.2.3.4:99" outright, because net.ParseIP returns nil for
// it. That is wrong once selection is by ADDRESS: several load balancers emit
// host:port, and a trusted proxy written that way would fail the CIDR comparison,
// be mistaken for the client, and become the rate-limit key — or, worse, cause
// the scan to give up and put the whole household in one bucket. Extracting the
// IP grants an attacker nothing, since "1.2.3.4:99" and "1.2.3.4" were equally
// forgeable to begin with.
func TestClientIP_NormalisesAddressForms(t *testing.T) {
	// The peer must be a trusted proxy for the header to be read at all.
	withTrustedProxyCIDRs(t, true, []string{"172.18.0.0/16"})
	cases := map[string]string{
		"1.2.3.4:99":       "1.2.3.4",
		"1.2.3.4":          "1.2.3.4",
		"[2001:db8::1]:99": "2001:db8::1",
		"2001:db8::1":      "2001:db8::1",
		// IPv4-mapped IPv6 must normalise, or the same host counts as two
		// different rate-limit identities.
		"::ffff:1.2.3.4": "1.2.3.4",
	}
	for xff, want := range cases {
		if got := ClientIP(reqWithXFF(xff, "172.18.0.1:5000")); got != want {
			t.Errorf("xff=%q: got %q, want %q", xff, got, want)
		}
	}
}

// TestClientIP_CIDRModeResistsPrependedEntries is THE regression
// test for the rate-limiter bypass.
//
// Selection used to be positional: idx := len(parts) - hops. That is correct only
// when hops equals the real chain length, and nothing enforced it. With hops
// overcounted — which README actively instructed for a Cloudflare Tunnel, so
// removing the tunnel silently produced it — an attacker who PREPENDS one entry
// makes the header long enough that the selected index lands on their own forged
// value. Measured before the fix: 5 requests from one address produced 5 distinct
// rate-limit keys, so RATE_LIMIT_MAX never engaged and password guessing was
// unlimited. The previous attempt at a fix warned only on the honest-traffic
// branch and left the attack path untouched.
//
// Address-based selection has no count to get wrong: the scan stops at the first
// entry from the right that is not a known proxy, and prepended junk sits further
// left where it is never reached.
func TestClientIP_CIDRModeResistsPrependedEntries(t *testing.T) {
	// One appending proxy on the Docker network.
	withTrustedProxyCIDRs(t, true, []string{"172.18.0.0/16"})

	const attacker = "198.51.100.7"
	keys := map[string]bool{}
	for _, forged := range []string{"10.0.0.1", "10.0.0.2", "203.0.113.9", "172.18.0.1", "not-an-ip"} {
		// The real proxy appends the attacker's true address after whatever they sent.
		got := ClientIP(reqWithXFF(forged+", "+attacker, "172.18.0.1:5000"))
		keys[got] = true
		if got != attacker {
			t.Errorf("forged %q: keyed on %q, want the proxy-observed %q", forged, got, attacker)
		}
	}
	if len(keys) != 1 {
		t.Errorf("attacker minted %d distinct rate-limit keys from one address; want 1", len(keys))
	}
}

// TestClientIP_CIDRModeRequiresATrustedPeer is the regression test
// for X-Forwarded-For being believed from a peer that is not a proxy.
//
// Selecting the client by address is not sufficient on its own. A client that
// reaches the server directly — a LAN host, a sibling container, a port exposed
// next to the proxy — can send its own header, and because the forged entry is
// not in a trusted range it wins on the very first step of the walk. Measured
// before this check: one direct peer minted a fresh rate-limit key for every
// value it chose, which is the same total bypass the hop count allowed.
//
// Only the socket address can establish that a proxy is in front; the header
// cannot vouch for itself.
func TestClientIP_CIDRModeRequiresATrustedPeer(t *testing.T) {
	withTrustedProxyCIDRs(t, true, []string{"172.18.0.0/16"})

	const direct = "203.0.113.66"
	keys := map[string]bool{}
	for _, forged := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		got := ClientIP(reqWithXFF(forged, direct+":40000"))
		keys[got] = true
		if got != direct {
			t.Errorf("direct peer forged %q: keyed on %q, want the socket address %q",
				forged, got, direct)
		}
	}
	if len(keys) != 1 {
		t.Errorf("a non-proxy peer minted %d distinct rate-limit keys; want 1", len(keys))
	}

	// Control: the SAME header through the real proxy is honoured, so the check
	// above is a peer restriction and not a blanket refusal to read the header.
	if got := ClientIP(reqWithXFF("203.0.113.5", "172.18.0.1:5000")); got != "203.0.113.5" {
		t.Errorf("through a trusted proxy: got %q, want 203.0.113.5", got)
	}
}

// TestClientIP_CIDRModeSelectionRules covers the rest of the
// address-based contract, including the two ways it must fall back.
func TestClientIP_CIDRModeSelectionRules(t *testing.T) {
	withTrustedProxyCIDRs(t, true, []string{"172.18.0.0/16", "10.9.9.9"})

	cases := []struct {
		name, xff, remote, want string
	}{
		{"single proxy appended the client", "203.0.113.5", "172.18.0.1:1", "203.0.113.5"},
		{"two trusted proxies are both skipped",
			"203.0.113.5, 172.18.0.9", "172.18.0.1:1", "203.0.113.5"},
		{"bare-IP trusted entry is skipped too",
			"203.0.113.5, 10.9.9.9", "172.18.0.1:1", "203.0.113.5"},
		// Every entry is a proxy, so the header names no client at all. The socket
		// address is the only unforgeable value left.
		{"all entries trusted falls back to the socket",
			"172.18.0.2, 172.18.0.3", "172.18.0.1:1", "172.18.0.1"},
		// Garbage must STOP the scan rather than be skipped. Skipping it would let
		// an attacker insert junk to step past their real address onto a forged one.
		{"garbage stops the walk and falls back",
			"203.0.113.5, junk, 172.18.0.9", "172.18.0.1:1", "172.18.0.1"},
		{"a trusted proxy emitting host:port is still recognised",
			"203.0.113.5, 172.18.0.9:4321", "172.18.0.1:1", "203.0.113.5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClientIP(reqWithXFF(tc.xff, tc.remote)); got != tc.want {
				t.Errorf("xff=%q: got %q, want %q", tc.xff, got, tc.want)
			}
		})
	}
}

// TestClientIP_CIDRModeBoundsTheWalk pins the allocation bound. The
// header is entirely attacker-controlled, so an unbounded scan is free work.
func TestClientIP_CIDRModeBoundsTheWalk(t *testing.T) {
	withTrustedProxyCIDRs(t, true, []string{"172.18.0.0/16"})

	// Far more trusted-looking entries than the bound, so the walk cannot reach
	// the leftmost forged value even though every entry parses as trusted.
	var b strings.Builder
	b.WriteString("6.6.6.6")
	for i := 0; i < maxXFFEntries+10; i++ {
		b.WriteString(", 172.18.0.9")
	}
	got := ClientIP(reqWithXFF(b.String(), "172.18.0.1:1"))
	if got == "6.6.6.6" {
		t.Error("the walk reached the forged leftmost entry; maxXFFEntries is not bounding it")
	}
	if got != "172.18.0.1" {
		t.Errorf("got %q, want the socket-address fallback 172.18.0.1", got)
	}
}

// TestClientIP_JoinsRepeatedHeaderLines is the regression test for
// reading only the first X-Forwarded-For field line.
//
// r.Header.Get returns the first line only, and a proxy may append a SECOND line
// instead of extending the client's. Where it does, the attacker's line is the
// first one, so the selection ran entirely on data the proxy had never touched.
// RFC 7230 makes repeated field lines equivalent to one comma-joined value in
// order, which puts the proxy's entry rightmost — where the walk starts.
func TestClientIP_JoinsRepeatedHeaderLines(t *testing.T) {
	withTrustedProxyCIDRs(t, true, []string{"172.18.0.0/16"})

	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	r.RemoteAddr = "172.18.0.1:5000"
	// The attacker's forged line arrives first; the proxy appends its own.
	r.Header.Add("X-Forwarded-For", "10.0.0.99")
	r.Header.Add("X-Forwarded-For", "198.51.100.7")

	if got := ClientIP(r); got != "198.51.100.7" {
		t.Errorf("got %q, want the proxy-observed 198.51.100.7 — only the first "+
			"X-Forwarded-For line is being read, so the forged one wins", got)
	}
}
