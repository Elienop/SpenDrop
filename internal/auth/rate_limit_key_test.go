package auth

import (
	"fmt"
	"strings"
	"testing"
)

// TestClientIPForRateLimit_CollapsesAnIPv6Prefix is THE regression test for the
// rate-limit key being the full address.
//
// Every ordinary residential IPv6 customer is delegated a routed /64, so a
// single host chooses freely among 2^64 source addresses. Keying the limiter on
// the full address therefore minted a fresh bucket per request: measured with
// TRUST_PROXY_HEADERS=false, five addresses out of one /64 produced five
// distinct keys. h.loginFailureLimiter is the ONLY throttle on password
// guessing and every guess costs a bcrypt at the configured cost, so that was
// simultaneously an unlimited-guessing bypass and a CPU-exhaustion primitive —
// and it also let Bucket.hits grow without bound, one entry per request.
//
// The whole /64 must be one bucket. This is invisible to IPv4-only testing,
// which is why it survived several reviews.
func TestClientIPForRateLimit_CollapsesAnIPv6Prefix(t *testing.T) {
	withTrustedProxyCIDRs(t, false, nil)

	keys := map[string]bool{}
	for i := 0; i < 25; i++ {
		remote := fmt.Sprintf("[2001:db8:1:2::%x]:40000", i+1)
		got := ClientIPForRateLimit(reqWithXFF("", remote))
		keys[got] = true
		if got != "2001:db8:1:2::/64" {
			t.Errorf("RemoteAddr=%q: keyed on %q, want the /64 prefix 2001:db8:1:2::/64", remote, got)
		}
	}
	if len(keys) != 1 {
		t.Errorf("one /64 minted %d distinct rate-limit keys; want 1 — the login limiter "+
			"is bypassable by any IPv6 client, and it is the only throttle on password guessing",
			len(keys))
	}
}

// TestClientIPForRateLimit_KeepsSeparateIPv6PrefixesApart is the other half of
// the contract: masking must not merge unrelated households into one bucket,
// because Exhausted short-circuits before the password check and a shared
// bucket lets one attacker pin every co-keyed victim at 429.
func TestClientIPForRateLimit_KeepsSeparateIPv6PrefixesApart(t *testing.T) {
	withTrustedProxyCIDRs(t, false, nil)

	cases := map[string]string{
		"[2001:db8:1:2::1]:40000": "2001:db8:1:2::/64",
		"[2001:db8:1:3::1]:40000": "2001:db8:1:3::/64",
		"[2001:db8:2:2::1]:40000": "2001:db8:2:2::/64",
		"[2001:db9:1:2::1]:40000": "2001:db9:1:2::/64",
	}
	seen := map[string]bool{}
	for remote, want := range cases {
		got := ClientIPForRateLimit(reqWithXFF("", remote))
		if got != want {
			t.Errorf("RemoteAddr=%q: got %q, want %q", remote, got, want)
		}
		seen[got] = true
	}
	if len(seen) != len(cases) {
		t.Errorf("%d distinct /64s collapsed into %d keys; want %d — unrelated "+
			"households share a bucket and one can lock the others out",
			len(cases), len(seen), len(cases))
	}
}

// TestClientIPForRateLimit_IPv4IsUnchanged pins the deliberate asymmetry.
//
// IPv4 masks to /32, i.e. the host itself — no behaviour change at all. A
// wider IPv4 mask (/24 was considered) would group up to 254 unrelated
// customers, and behind CGNAT an entire ISP pool of households: because
// Exhausted short-circuits before the password check, one attacker in that
// pool could then pin every other household at 429 permanently. IPv6 has the
// bypass precisely because ONE customer owns the whole /64; IPv4 does not.
func TestClientIPForRateLimit_IPv4IsUnchanged(t *testing.T) {
	withTrustedProxyCIDRs(t, false, nil)

	keys := map[string]bool{}
	for i := 1; i <= 5; i++ {
		remote := fmt.Sprintf("203.0.113.%d:40000", i)
		want := fmt.Sprintf("203.0.113.%d/32", i)
		got := ClientIPForRateLimit(reqWithXFF("", remote))
		if got != want {
			t.Errorf("RemoteAddr=%q: got %q, want %q", remote, got, want)
		}
		keys[got] = true
	}
	if len(keys) != 5 {
		t.Errorf("5 distinct IPv4 hosts produced %d keys; want 5 — masking IPv4 wider "+
			"than /32 makes one attacker able to lock out unrelated households", len(keys))
	}
}

// TestClientIPForRateLimit_V4MappedKeysAsPlainV4 keeps the "one host, one key"
// invariant across the mapped/unmapped spellings. Masking the mapped form as an
// IPv6 /64 would put ::ffff:0:0/64 — every IPv4 client that happens to arrive
// mapped — into a single shared bucket, and would give the same host a second
// bucket depending on which path produced the key.
func TestClientIPForRateLimit_V4MappedKeysAsPlainV4(t *testing.T) {
	withTrustedProxyCIDRs(t, false, nil)

	mapped := ClientIPForRateLimit(reqWithXFF("", "[::ffff:10.0.0.1]:5000"))
	plain := ClientIPForRateLimit(reqWithXFF("", "10.0.0.1:5000"))
	if mapped != plain {
		t.Errorf("::ffff:10.0.0.1 keys as %q but 10.0.0.1 keys as %q — the same host "+
			"occupies two rate-limit buckets", mapped, plain)
	}
	if plain != "10.0.0.1/32" {
		t.Errorf("got %q, want 10.0.0.1/32", plain)
	}
	// A different mapped host must still be its own bucket, i.e. the mapped
	// form is not being collapsed into one IPv6 prefix.
	other := ClientIPForRateLimit(reqWithXFF("", "[::ffff:10.0.0.2]:5000"))
	if other == mapped {
		t.Errorf("::ffff:10.0.0.1 and ::ffff:10.0.0.2 share the key %q — mapped IPv4 "+
			"is being masked as an IPv6 prefix", other)
	}
}

// TestClientIPForRateLimit_MasksEveryPath is the invariant that makes the fix
// complete: the socket fallback, the header walk and the all-entries-trusted
// fallback must all produce the SAME key for one host. If any one of them
// skipped the mask, that host would land in two buckets and its real attempt
// allowance would double depending on routing.
func TestClientIPForRateLimit_MasksEveryPath(t *testing.T) {
	const want = "2001:db8:1:2::/64"

	t.Run("header walk", func(t *testing.T) {
		withTrustedProxyCIDRs(t, true, []string{"172.18.0.0/16"})
		keys := map[string]bool{}
		for i := 0; i < 5; i++ {
			xff := fmt.Sprintf("2001:db8:1:2::%x", i+1)
			got := ClientIPForRateLimit(reqWithXFF(xff, "172.18.0.1:5000"))
			keys[got] = true
			if got != want {
				t.Errorf("xff=%q: got %q, want %q", xff, got, want)
			}
		}
		if len(keys) != 1 {
			t.Errorf("the header path minted %d keys from one /64; want 1", len(keys))
		}
	})

	t.Run("untrusted peer falls back masked", func(t *testing.T) {
		withTrustedProxyCIDRs(t, true, []string{"172.18.0.0/16"})
		if got := ClientIPForRateLimit(reqWithXFF("1.1.1.1", "[2001:db8:1:2::9]:5000")); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("all entries trusted falls back masked", func(t *testing.T) {
		withTrustedProxyCIDRs(t, true, []string{"2001:db8:1:2::/64"})
		if got := ClientIPForRateLimit(reqWithXFF("2001:db8:1:2::9", "[2001:db8:1:2::1]:5000")); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("untrusted deployment falls back masked", func(t *testing.T) {
		withTrustedProxyCIDRs(t, false, nil)
		if got := ClientIPForRateLimit(reqWithXFF("2001:db8:9:9::1", "[2001:db8:1:2::7]:5000")); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

// TestClientIPForRateLimit_PassesThroughUnkeyableAddresses keeps the unix-socket
// escape hatch: an address netip cannot parse must still yield the raw value
// rather than an empty key, which would merge every such client into one bucket.
func TestClientIPForRateLimit_PassesThroughUnkeyableAddresses(t *testing.T) {
	withTrustedProxyCIDRs(t, false, nil)
	if got := ClientIPForRateLimit(reqWithXFF("", "/tmp/spendrop.sock")); got != "/tmp/spendrop.sock" {
		t.Errorf("got %q, want the raw value passed through", got)
	}
}

// TestRateLimitKey pins the helper directly, on inputs ClientIP cannot
// currently hand it.
//
// ClientIP normalises through parseXFFAddr, which already unmaps and strips
// zones, so the equivalent assertions driven through ClientIPForRateLimit
// cannot fail if rateLimitKey's own Unmap is deleted — mutation-tested and
// confirmed green. That makes the unmap a FORWARD guard rather than a
// regression fix, and an unpinned guard is one nobody is holding: if ClientIP
// ever stops normalising, "::ffff:10.0.0.1" would be masked as IPv6 and every
// mapped IPv4 client on the internet would share the single bucket
// ::ffff:0:0/64. This is the test that would catch that.
func TestRateLimitKey(t *testing.T) {
	cases := map[string]string{
		// IPv4: the host itself, unchanged.
		"203.0.113.9": "203.0.113.9/32",
		"10.0.0.1":    "10.0.0.1/32",
		// IPv4-mapped IPv6 is an IPv4 host and must key as one.
		"::ffff:10.0.0.1": "10.0.0.1/32",
		"::ffff:10.0.0.2": "10.0.0.2/32",
		// IPv6: the delegated /64.
		"2001:db8:1:2::1":    "2001:db8:1:2::/64",
		"2001:db8:1:2::ffff": "2001:db8:1:2::/64",
		"2001:db8:1:3::1":    "2001:db8:1:3::/64",
		// A zone must not defeat the mask. netip.PrefixFrom drops the zone
		// itself, so nothing in rateLimitKey handles this — the case is here to
		// pin that stdlib behaviour, since a future change that stopped
		// relying on it would silently return the address unmasked.
		"fe80::1%eth0": "fe80::/64",
		// Not an address: passed through, because an empty key would merge
		// every unix-socket client into one bucket.
		"/tmp/spendrop.sock": "/tmp/spendrop.sock",
		"":                   "",
	}
	for in, want := range cases {
		if got := rateLimitKey(in); got != want {
			t.Errorf("rateLimitKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRateLimitKeyPrefixWidths pins the two constants in range.
//
// netip.PrefixFrom returns an INVALID Prefix when bits exceeds the address's
// BitLen, and Masked().String() on that is the literal "invalid Prefix" — one
// key for every client on earth, i.e. a household-wide 429 the moment anyone
// fails a login. rateLimitKey falls back to the unmasked address rather than
// emit that, but the real defence is the constants never leaving range.
func TestRateLimitKeyPrefixWidths(t *testing.T) {
	if rateLimitPrefixV4 < 0 || rateLimitPrefixV4 > 32 {
		t.Errorf("rateLimitPrefixV4 = %d, must be within 0..32", rateLimitPrefixV4)
	}
	if rateLimitPrefixV6 < 0 || rateLimitPrefixV6 > 128 {
		t.Errorf("rateLimitPrefixV6 = %d, must be within 0..128", rateLimitPrefixV6)
	}
	// A key must never be the stringification of an invalid prefix, whatever
	// the constants are set to.
	for _, in := range []string{"203.0.113.9", "2001:db8:1:2::1", "::ffff:10.0.0.1"} {
		if got := rateLimitKey(in); strings.Contains(got, "invalid") {
			t.Errorf("rateLimitKey(%q) = %q — an invalid prefix is being used as a key", in, got)
		}
	}
}

// TestClientIP_KeepsTheWholeAddress is the forensic half of the split.
//
// api_tokens.last_used_ip is shown to the operator in the token list so they can
// recognise a token being used from somewhere unexpected. It used to be written
// from the rate-limit key, so masking that key without splitting the two uses
// would have degraded a real address into "2001:db8:1:2::/64" and destroyed the
// only per-token forensic signal there is.
func TestClientIP_KeepsTheWholeAddress(t *testing.T) {
	withTrustedProxyCIDRs(t, false, nil)

	cases := map[string]string{
		"[2001:db8:1:2::7]:5000": "2001:db8:1:2::7",
		"203.0.113.9:5000":       "203.0.113.9",
		"[::ffff:10.0.0.1]:5000": "10.0.0.1",
		"[fe80::1%eth0]:5000":    "fe80::1",
		"/tmp/spendrop.sock":     "/tmp/spendrop.sock",
	}
	for remote, want := range cases {
		if got := ClientIP(reqWithXFF("", remote)); got != want {
			t.Errorf("RemoteAddr=%q: got %q, want the unmasked %q", remote, got, want)
		}
	}

	// The header path must stay unmasked too.
	withTrustedProxyCIDRs(t, true, []string{"172.18.0.0/16"})
	if got := ClientIP(reqWithXFF("2001:db8:1:2::7", "172.18.0.1:5000")); got != "2001:db8:1:2::7" {
		t.Errorf("via the header: got %q, want the unmasked 2001:db8:1:2::7", got)
	}
}
