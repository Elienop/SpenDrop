package push

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestIsPubliclyRoutable covers the address forms a hand-rolled private-IP
// check typically misses.
func TestIsPubliclyRoutable(t *testing.T) {
	nonPublic := []string{
		"127.0.0.1", "::1",
		"10.0.0.5", "192.168.1.1", "172.16.0.1",
		// IPv4-mapped IPv6: passes every naive v4 predicate unless normalised.
		"::ffff:10.0.0.1", "::ffff:127.0.0.1",
		"169.254.169.254", // cloud metadata
		"fe80::1",         // link-local v6
		"fc00::1",         // unique-local v6
		"100.64.0.1",      // CGNAT — routable-looking, not public
		"0.0.0.0", "::",
		"224.0.0.1", // multicast
	}
	for _, s := range nonPublic {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("bad fixture %q", s)
		}
		if IsPubliclyRoutable(ip) {
			t.Errorf("%s was treated as publicly routable", s)
		}
	}

	public := []string{"8.8.8.8", "1.1.1.1", "2606:4700::1111", "203.0.113.5"}
	for _, s := range public {
		if !IsPubliclyRoutable(net.ParseIP(s)) {
			t.Errorf("%s should be publicly routable", s)
		}
	}
}

// TestGuardedTransport_RefusesNonPublicHost is the regression test for the
// hostname bypass.
//
// The original validator checked only net.ParseIP(host), which returns nil for
// ANY DNS name — so "https://router.lan/admin", or an attacker-controlled name
// with an A record pointing at 192.168.1.1, went straight through. The control
// therefore has to live at the dial, where the name has actually been resolved.
func TestGuardedTransport_RefusesNonPublicHost(t *testing.T) {
	// localhost resolves to loopback: stands in for any hostname whose DNS
	// answer is non-public, which is the bypass a name-blind check permits.
	client := &http.Client{Transport: GuardedTransport(http.DefaultTransport.(*http.Transport))}

	_, err := client.Get("http://localhost:1/")
	if err == nil {
		t.Fatal("connection to a loopback-resolving hostname was allowed")
	}
	if !strings.Contains(err.Error(), "non-public address") {
		t.Errorf("blocked for the wrong reason: %v", err)
	}
}

// TestGuardedTransport_RefusesRedirect is the regression test for the second
// bypass: a push endpoint on an attacker-controlled PUBLIC host that 302s the
// sender at an internal address. Validation at subscribe time cannot see this
// at all — the endpoint it was shown is perfectly legitimate.
func TestGuardedTransport_RefusesRedirect(t *testing.T) {
	defer AllowNonPublicDialForTesting()()

	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("internal service"))
	}))
	defer internal.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internal.URL, http.StatusFound)
	}))
	defer redirector.Close()

	s := NewSender("pub", "priv", "mailto:a@b.c", http.DefaultClient)
	resp, err := s.client.Get(redirector.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("the sender followed a redirect — a public endpoint can bounce it inward")
	}
	if !strings.Contains(err.Error(), "refusing redirect") {
		t.Errorf("failed for the wrong reason: %v", err)
	}
}

// TestNewSender_WiresTheDialGuard proves the guard is actually INSTALLED by
// NewSender, not merely available.
//
// TestGuardedTransport_RefusesNonPublicHost builds the transport directly, so
// it keeps passing if the wiring in NewSender is deleted — exactly the trap
// that let an earlier, ineffective SSRF fix look tested.
func TestNewSender_WiresTheDialGuard(t *testing.T) {
	s := NewSender("pub", "priv", "mailto:a@b.c", http.DefaultClient)

	resp, err := s.client.Get("http://localhost:1/")
	if err == nil {
		resp.Body.Close()
		t.Fatal("NewSender's client reached a loopback host — the dial guard is not wired in")
	}
	if !strings.Contains(err.Error(), "non-public address") {
		t.Errorf("failed for the wrong reason: %v", err)
	}
}
