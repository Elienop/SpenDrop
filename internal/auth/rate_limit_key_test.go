package auth

import (
	"fmt"
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
