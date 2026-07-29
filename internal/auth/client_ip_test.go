package auth

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func withTrustedProxies(t *testing.T, trusted bool, hops int) {
	t.Helper()
	trustProxyHeadersMu.RLock()
	prevTrust, prevHops := trustProxyHeaders, trustedProxyHops
	trustProxyHeadersMu.RUnlock()
	SetTrustProxyHeaders(trusted, hops)
	t.Cleanup(func() { SetTrustProxyHeaders(prevTrust, prevHops) })
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
	for _, xff := range []string{"not-an-ip", "  ", "evil.example.com", "1.2.3.4:99"} {
		if got := ClientIPForRateLimit(reqWithXFF(xff, "10.0.0.5:5000")); got != "10.0.0.5" {
			t.Errorf("xff=%q: got %q, want the socket-address fallback", xff, got)
		}
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
