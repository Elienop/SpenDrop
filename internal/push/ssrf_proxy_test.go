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
