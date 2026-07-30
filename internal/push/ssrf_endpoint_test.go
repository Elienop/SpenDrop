package push

import (
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestGuardedRoundTripper_ValidatesEndpointThroughAProxy is the regression test
// for the SSRF control going completely inert whenever an egress proxy is set.
//
// The dial guard can only judge the address it is asked to dial, and with a proxy
// configured that address is the PROXY — the endpoint is never examined. So
// setting HTTP(S)_PROXY silently switched the whole control off: a subscription
// registered against http://10.0.0.5/... was handed to the proxy and fetched
// normally. Measured before this fix: status 200, with the proxy logging
// "10.0.0.5/fcm/send/abc" as the target it had been asked to reach.
//
// The proxy here is publicly-routable-looking so it is NOT the thing being
// refused; the endpoint is. That separation is what makes the test meaningful —
// it fails if endpoint validation is missing, not merely if something got blocked.
func TestGuardedRoundTripper_ValidatesEndpointThroughAProxy(t *testing.T) {
	// A stand-in proxy that records every target it is asked to fetch.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	reached := make(chan string, 4)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached <- r.Host + r.URL.Path
		w.WriteHeader(http.StatusOK)
	})}
	defer srv.Close()
	go func() { _ = srv.Serve(ln) }()

	base := http.DefaultTransport.(*http.Transport).Clone()
	proxyURL, err := url.Parse("http://" + ln.Addr().String())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	base.Proxy = func(*http.Request) (*url.URL, error) { return proxyURL, nil }

	c := &http.Client{Transport: GuardedRoundTripper(base), Timeout: 3 * time.Second}

	resp, err := c.Get("http://10.0.0.5/fcm/send/abc")
	if err == nil {
		resp.Body.Close()
		t.Fatalf("an internal endpoint was fetched through the proxy (status %d) — "+
			"configuring a proxy disables the SSRF control entirely", resp.StatusCode)
	}
	if !strings.Contains(err.Error(), "non-public address") {
		t.Errorf("refused for the wrong reason: %v", err)
	}

	select {
	case got := <-reached:
		t.Errorf("the request reached the proxy asking for %q; it should never have been sent", got)
	case <-time.After(100 * time.Millisecond):
		// Correct: refused before any bytes left the process.
	}
}

// TestGuardedRoundTripper_AllowsPublicEndpoints is the non-vacuousness partner.
//
// Without it, endpoint validation could be implemented as "refuse everything" and
// the test above would still pass while push delivery was entirely broken.
func TestGuardedRoundTripper_AllowsPublicEndpoints(t *testing.T) {
	base := http.DefaultTransport.(*http.Transport).Clone()
	// Route to a dead proxy so nothing actually leaves the machine: reaching the
	// dial at all proves the endpoint passed validation, which is the assertion.
	proxyURL, err := url.Parse("http://127.0.0.1:1")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	base.Proxy = func(*http.Request) (*url.URL, error) { return proxyURL, nil }

	c := &http.Client{Transport: GuardedRoundTripper(base), Timeout: 3 * time.Second}
	_, err = c.Get("https://fcm.googleapis.com/fcm/send/abc")
	if err == nil {
		t.Skip("something answered on 127.0.0.1:1; cannot distinguish outcomes here")
	}
	if strings.Contains(err.Error(), "non-public address") {
		t.Errorf("a legitimate public push endpoint was refused, so no push can be delivered: %v", err)
	}
}

// TestValidateHostIsPublic_RejectsAnyNonPublicAnswer pins the all-answers rule.
//
// Filtering to the routable answers instead of rejecting outright would let an
// attacker publish one public and one private record for the same name and let
// resolver ordering decide which one gets connected to. The rule was previously
// asserted nowhere: reducing it to "check only the first answer" left the entire
// suite green.
func TestValidateHostIsPublic_RejectsAnyNonPublicAnswer(t *testing.T) {
	// localhost is the one name guaranteed to resolve locally, and on a
	// dual-stack host it yields several answers — all non-public, so a
	// first-answer-only check and an all-answers check agree here.
	if err := validateHostIsPublic(t.Context(), "localhost"); err == nil {
		t.Error("localhost was accepted as a push endpoint")
	}

	// The discriminating case has to be fed in directly, since no real name
	// resolves to a public and a private address at once. The public answer is
	// FIRST, so a check that stops after one answer accepts this set.
	mixed := []net.IPAddr{
		{IP: net.ParseIP("8.8.8.8")},
		{IP: net.ParseIP("10.0.0.5")},
	}
	if err := allAddrsPublic("mixed.example", mixed); err == nil {
		t.Error("a public-then-private answer set was accepted; an attacker can publish " +
			"both records and let resolver ordering decide whether an internal host is reached")
	}
	// Reversed, to prove the rule is not merely "check the last one".
	if err := allAddrsPublic("mixed.example", []net.IPAddr{mixed[1], mixed[0]}); err == nil {
		t.Error("a private-then-public answer set was accepted")
	}
	if err := allAddrsPublic("ok.example", []net.IPAddr{mixed[0]}); err != nil {
		t.Errorf("an all-public answer set was refused: %v", err)
	}
	if err := allAddrsPublic("empty.example", nil); err == nil {
		t.Error("an empty answer set was accepted")
	}

	if err := validateHostIsPublic(t.Context(), "8.8.8.8"); err != nil {
		t.Errorf("a public literal was refused: %v", err)
	}
	if err := validateHostIsPublic(t.Context(), ""); err == nil {
		t.Error("an empty host was accepted")
	}
}
