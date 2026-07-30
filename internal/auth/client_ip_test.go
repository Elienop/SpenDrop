package auth

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// withTrustedProxies configures deprecated hop-count mode (no CIDRs).
func withTrustedProxies(t *testing.T, trusted bool, hops int) {
	t.Helper()
	withTrustedProxyCIDRs(t, trusted, hops, nil)
}

// withTrustedProxyCIDRs configures the address-set mode that supersedes hops.
func withTrustedProxyCIDRs(t *testing.T, trusted bool, hops int, cidrs []string) {
	t.Helper()
	trustProxyHeadersMu.RLock()
	prevTrust, prevHops, prevCIDRs := trustProxyHeaders, trustedProxyHops, trustedProxyCIDRs
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
	SetTrustProxyHeaders(trusted, hops, parsed)
	t.Cleanup(func() { SetTrustProxyHeaders(prevTrust, prevHops, prevCIDRs) })
}

func reqWithXFF(xff, remoteAddr string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	r.RemoteAddr = remoteAddr
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	return r
}

// TestClientIPForRateLimit_HopCount is the regression test for hard-coding
// "rightmost".
//
// Each appending proxy adds the address it saw, so the client sits
// hops-from-the-right. With two hops — the README documents a Cloudflare
// Tunnel in front of Caddy — the rightmost entry is the outer edge, the SAME
// address for every visitor, which collapses the household into one bucket.
func TestClientIPForRateLimit_HopCount(t *testing.T) {
	const header = "198.51.100.7, 203.0.113.4, 192.0.2.9"

	cases := []struct {
		hops int
		want string
		why  string
	}{
		{1, "192.0.2.9", "single proxy: the client is the last entry"},
		{2, "203.0.113.4", "two hops: the last entry is the outer edge, not the client"},
		{3, "198.51.100.7", "three hops walks back to the original client"},
		// More hops than entries means upstream is not appending as configured;
		// falling back to the socket address is the safe reading.
		{4, "10.0.0.5", "more hops than entries falls back to the socket address"},
	}
	for _, tc := range cases {
		withTrustedProxies(t, true, tc.hops)
		if got := ClientIPForRateLimit(reqWithXFF(header, "10.0.0.5:5000")); got != tc.want {
			t.Errorf("hops=%d: got %q, want %q (%s)", tc.hops, got, tc.want, tc.why)
		}
	}
}

// TestClientIPForRateLimit_UntrustedIgnoresHeader pins the default: on a
// directly exposed server X-Forwarded-For is attacker-controlled, so trusting
// it would let anyone mint a fresh bucket per request.
func TestClientIPForRateLimit_UntrustedIgnoresHeader(t *testing.T) {
	withTrustedProxies(t, false, 1)
	got := ClientIPForRateLimit(reqWithXFF("1.2.3.4", "10.0.0.5:5000"))
	if got != "10.0.0.5" {
		t.Errorf("got %q, want the socket address 10.0.0.5", got)
	}
}

// TestClientIPForRateLimit_RejectsNonAddressEntry guards against a garbage or
// injected entry becoming a rate-limit key of its own.
func TestClientIPForRateLimit_RejectsNonAddressEntry(t *testing.T) {
	withTrustedProxies(t, true, 1)
	for _, xff := range []string{"not-an-ip", "  ", "evil.example.com"} {
		if got := ClientIPForRateLimit(reqWithXFF(xff, "10.0.0.5:5000")); got != "10.0.0.5" {
			t.Errorf("xff=%q: got %q, want the socket-address fallback", xff, got)
		}
	}
}

// TestClientIPForRateLimit_NormalisesAddressForms pins the shapes an entry
// legitimately arrives in.
//
// This used to reject "1.2.3.4:99" outright, because net.ParseIP returns nil for
// it. That is wrong once selection is by ADDRESS: several load balancers emit
// host:port, and a trusted proxy written that way would fail the CIDR comparison,
// be mistaken for the client, and become the rate-limit key — or, worse, cause
// the scan to give up and put the whole household in one bucket. Extracting the
// IP grants an attacker nothing, since "1.2.3.4:99" and "1.2.3.4" were equally
// forgeable to begin with.
func TestClientIPForRateLimit_NormalisesAddressForms(t *testing.T) {
	withTrustedProxies(t, true, 1)
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
		if got := ClientIPForRateLimit(reqWithXFF(xff, "10.0.0.5:5000")); got != want {
			t.Errorf("xff=%q: got %q, want %q", xff, got, want)
		}
	}
}

// TestClientIPForRateLimit_CIDRModeResistsPrependedEntries is THE regression
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
func TestClientIPForRateLimit_CIDRModeResistsPrependedEntries(t *testing.T) {
	// One appending proxy on the Docker network. hops is left at a WRONG value on
	// purpose: with CIDRs configured it must be ignored entirely.
	withTrustedProxyCIDRs(t, true, 2, []string{"172.18.0.0/16"})

	const attacker = "198.51.100.7"
	keys := map[string]bool{}
	for _, forged := range []string{"10.0.0.1", "10.0.0.2", "203.0.113.9", "172.18.0.1", "not-an-ip"} {
		// The real proxy appends the attacker's true address after whatever they sent.
		got := ClientIPForRateLimit(reqWithXFF(forged+", "+attacker, "172.18.0.1:5000"))
		keys[got] = true
		if got != attacker {
			t.Errorf("forged %q: keyed on %q, want the proxy-observed %q", forged, got, attacker)
		}
	}
	if len(keys) != 1 {
		t.Errorf("attacker minted %d distinct rate-limit keys from one address; want 1", len(keys))
	}
}

// TestClientIPForRateLimit_CIDRModeSelectionRules covers the rest of the
// address-based contract, including the two ways it must fall back.
func TestClientIPForRateLimit_CIDRModeSelectionRules(t *testing.T) {
	withTrustedProxyCIDRs(t, true, 1, []string{"172.18.0.0/16", "10.9.9.9"})

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
			if got := ClientIPForRateLimit(reqWithXFF(tc.xff, tc.remote)); got != tc.want {
				t.Errorf("xff=%q: got %q, want %q", tc.xff, got, tc.want)
			}
		})
	}
}

// TestClientIPForRateLimit_CIDRModeBoundsTheWalk pins the allocation bound. The
// header is entirely attacker-controlled, so an unbounded scan is free work.
func TestClientIPForRateLimit_CIDRModeBoundsTheWalk(t *testing.T) {
	withTrustedProxyCIDRs(t, true, 1, []string{"172.18.0.0/16"})

	// Far more trusted-looking entries than the bound, so the walk cannot reach
	// the leftmost forged value even though every entry parses as trusted.
	var b strings.Builder
	b.WriteString("6.6.6.6")
	for i := 0; i < maxXFFEntries+10; i++ {
		b.WriteString(", 172.18.0.9")
	}
	got := ClientIPForRateLimit(reqWithXFF(b.String(), "172.18.0.1:1"))
	if got == "6.6.6.6" {
		t.Error("the walk reached the forged leftmost entry; maxXFFEntries is not bounding it")
	}
	if got != "172.18.0.1" {
		t.Errorf("got %q, want the socket-address fallback 172.18.0.1", got)
	}
}

// TestClientIPForRateLimit_WarnsOnShortHeader covers the misconfiguration
// detector.
//
// An overcounted hop value is silent and permanent: honest clients send no
// X-Forwarded-For, so every request falls back and the whole household shares
// one bucket, while an attacker who prepends an entry lands on their own
// forged value and gets unlimited buckets. Because honest traffic ALWAYS
// produces a short header under an overcount, this warning fires on the first
// real request and is the cheapest possible detector.
func TestClientIPForRateLimit_WarnsOnShortHeader(t *testing.T) {
	withTrustedProxies(t, true, 3)

	// Defeat the throttle so the assertion does not depend on test ordering.
	proxyWarnMu.Lock()
	proxyWarnLast = time.Time{}
	proxyWarnMu.Unlock()

	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(prev) })

	ClientIPForRateLimit(reqWithXFF("203.0.113.4", "10.0.0.5:5000"))

	if !strings.Contains(buf.String(), "TRUSTED_PROXY_HOPS=3") {
		t.Errorf("no misconfiguration warning logged; the overcount is silent: %q", buf.String())
	}

	// And it must be throttled, or it is one line per request forever.
	buf.Reset()
	ClientIPForRateLimit(reqWithXFF("203.0.113.4", "10.0.0.5:5000"))
	if buf.Len() != 0 {
		t.Errorf("warning was not throttled: %q", buf.String())
	}
}
