package push

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestGuardedTransport_AllowsAnOperatorConfiguredProxy is the regression test
// for the SSRF guard shutting off push delivery on any host with an egress proxy.
//
// http.DefaultTransport carries ProxyFromEnvironment, and Clone preserves it, so
// HTTP_PROXY/HTTPS_PROXY stay live through GuardedTransport. When a proxy is
// selected the transport dials the PROXY — which on a self-hosted box is very
// often a LAN address — so the guard saw 192.168.1.50 and refused it:
//
//	proxyconnect tcp: push: refusing to connect to non-public address 192.168.1.50
//
// Total silent push outage, caused by the control rather than the threat. Every
// other test in this file connects directly, so none of them could see it.
func TestGuardedTransport_AllowsAnOperatorConfiguredProxy(t *testing.T) {
	base := http.DefaultTransport.(*http.Transport).Clone()
	// A LAN address like 192.168.1.50:3128 is the real-world case, but dialling
	// one costs the full 10s dialer timeout and depends on what is on the host's
	// network. Loopback is equally non-public — which is the entire point of the
	// test — and refuses instantly, keeping this hermetic and fast.
	proxyURL, err := url.Parse("http://127.0.0.1:1")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	base.Proxy = func(*http.Request) (*url.URL, error) { return proxyURL, nil }

	tr := GuardedTransport(base)
	if tr.Proxy == nil {
		t.Fatal("GuardedTransport dropped the transport's Proxy — proxied deployments cannot work at all")
	}

	c := &http.Client{Transport: tr}
	// The dial to the proxy will fail with "connection refused" — that is fine
	// and expected. What must NOT happen is a refusal from the GUARD: the
	// address was chosen by the operator.
	_, err = c.Get("https://fcm.googleapis.com/fcm/send/abc")
	if err == nil {
		t.Skip("something answered on 127.0.0.1:1; cannot distinguish outcomes here")
	}
	if strings.Contains(err.Error(), "refusing to connect to non-public address") {
		t.Errorf("the guard refused the operator's own egress proxy, so every push fails: %v", err)
	}
}

// TestGuardedTransport_StillGuardsEndpointsWhenNoProxyMatches is the
// non-vacuousness partner to the test above.
//
// Exempting proxy addresses must not become a blanket exemption. With a proxy
// configured for one address, a direct dial to a DIFFERENT non-public address
// (the NO_PROXY case, where the transport bypasses the proxy entirely) must
// still be refused. Without this, "allow the proxy" could be implemented as
// "allow everything" and the test above would still pass.
func TestGuardedTransport_StillGuardsEndpointsWhenNoProxyMatches(t *testing.T) {
	// A real internal service the guard must never be talked into reaching.
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("internal"))
	}))
	defer internal.Close()

	internalHost := strings.TrimPrefix(internal.URL, "http://")

	base := http.DefaultTransport.(*http.Transport).Clone()
	// Same port-1 proxy as above; the internal server is on a DIFFERENT port, so
	// the exemption must not match it.
	proxyURL, err := url.Parse("http://127.0.0.1:1")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// Proxy everything EXCEPT the internal host, mirroring NO_PROXY.
	base.Proxy = func(r *http.Request) (*url.URL, error) {
		if r.URL.Host == internalHost {
			return nil, nil
		}
		return proxyURL, nil
	}

	c := &http.Client{Transport: GuardedTransport(base)}

	// Prime the proxy set, so the exemption is populated when the direct dial
	// below happens. Its outcome is irrelevant.
	if resp, gerr := c.Get("https://fcm.googleapis.com/fcm/send/abc"); gerr == nil {
		resp.Body.Close()
	}

	resp, err := c.Get(internal.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("a direct dial to a loopback address succeeded — exempting the proxy has " +
			"disabled the guard for everything")
	}
	if !strings.Contains(err.Error(), "non-public address") {
		t.Errorf("blocked for the wrong reason: %v", err)
	}
}

// TestCanonicalProxyAddr pins the scheme defaults, because a mismatch between
// the recorded address and the one net/http dials would silently reinstate the
// outage: the exemption would never match and the guard would refuse the proxy
// again.
func TestCanonicalProxyAddr(t *testing.T) {
	cases := map[string]string{
		"http://p.lan:3128":  "p.lan:3128",
		"http://p.lan":       "p.lan:80",
		"https://p.lan":      "p.lan:443",
		"socks5://p.lan":     "p.lan:1080",
		"socks5h://p.lan":    "p.lan:1080",
		"http://10.0.0.1":    "10.0.0.1:80",
		"http://[fd00::1]":   "[fd00::1]:80",
		"http://[fd00::1]:8": "[fd00::1]:8",
	}
	for raw, want := range cases {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		if got := canonicalProxyAddr(u); got != want {
			t.Errorf("canonicalProxyAddr(%q) = %q, want %q", raw, got, want)
		}
	}
}

// TestCanonicalProxyAddr_IDN is the regression test for an internationalised
// proxy hostname re-triggering the total push outage.
//
// net/http's canonicalAddr runs the hostname through idna.Lookup.ToASCII before
// dialling, so HTTP_PROXY=http://präxy.lan:3128 is dialled as
// "xn--prxy-moa.lan:3128". Recording the unconverted "präxy.lan:3128" means the
// exemption key never matches the address handed to DialContext, the proxy is
// treated as an ordinary target, and — being on the LAN — it is refused as
// non-public. That is exactly the silent, total push outage the proxy exemption
// was added to fix, reachable again by nothing worse than an accented hostname.
func TestCanonicalProxyAddr_IDN(t *testing.T) {
	cases := map[string]string{
		"http://präxy.lan:3128": "xn--prxy-moa.lan:3128",
		"http://präxy.lan":      "xn--prxy-moa.lan:80",
		"https://präxy.lan":     "xn--prxy-moa.lan:443",
		"socks5://präxy.lan":    "xn--prxy-moa.lan:1080",
		"http://пример.тест":    "xn--e1afmkfd.xn--e1aybc:80",
		// Already-punycode input must pass through unchanged, not be re-encoded.
		"http://xn--prxy-moa.lan:3128": "xn--prxy-moa.lan:3128",
	}
	for raw, want := range cases {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		if got := canonicalProxyAddr(u); got != want {
			t.Errorf("canonicalProxyAddr(%q) = %q, want %q — the recorded exemption key "+
				"will not match the address net/http actually dials", raw, got, want)
		}
	}
}

// TestCanonicalProxyAddr_LeavesASCIIHostsVerbatim pins the ASCII shortcut, which
// is load-bearing rather than an optimisation.
//
// net/http only calls idna.Lookup.ToASCII when the host is NOT all-ASCII. Calling
// it unconditionally would be a plausible-looking fix that introduces the very
// mismatch it is meant to remove, because ToASCII is not the identity on ASCII:
//
//   - "P.LAN" is lowercased to "p.lan", while net/http dials "P.LAN" verbatim.
//   - "p_x.lan" and the IPv6 literal "fd00::1" are REJECTED outright
//     ("disallowed rune"), so an error-swallowing version silently does nothing
//     while a returns-error version breaks every IPv6 proxy.
//
// Each case here therefore fails under the naive implementation.
func TestCanonicalProxyAddr_LeavesASCIIHostsVerbatim(t *testing.T) {
	cases := map[string]string{
		"http://P.LAN:3128":    "P.LAN:3128",
		"http://Proxy.Lan":     "Proxy.Lan:80",
		"http://p_x.lan:3128":  "p_x.lan:3128",
		"http://[fd00::1]:311": "[fd00::1]:311",
	}
	for raw, want := range cases {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		if got := canonicalProxyAddr(u); got != want {
			t.Errorf("canonicalProxyAddr(%q) = %q, want %q — an ASCII host must be "+
				"recorded exactly as net/http dials it", raw, got, want)
		}
	}
}
