package push

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
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

// TestNewSender_WiresSomeGuard proves NewSender installs A guard rather than
// leaving the caller's transport untouched.
//
// Scope, stated precisely because the previous name and comment overclaimed:
// there is NO proxy configured here, so the dial guard alone refuses this
// request and the endpoint guard is never needed. The test therefore catches
// TOTAL REMOVAL of the wiring and nothing finer — in particular it stays green
// if GuardedRoundTripper is downgraded to GuardedTransport, which silently
// removes endpoint validation and restores the proxy bypass. That downgrade is
// what TestNewSender_ValidatesTheEndpointThroughAProxy and
// TestNewSender_WiresBothGuardLayers exist to catch.
//
// TestGuardedTransport_RefusesNonPublicHost builds the transport directly, so
// it keeps passing if the wiring in NewSender is deleted — exactly the trap
// that let an earlier, ineffective SSRF fix look tested.
func TestNewSender_WiresSomeGuard(t *testing.T) {
	s := NewSender("pub", "priv", "mailto:a@b.c", http.DefaultClient)

	resp, err := s.client.Get("http://localhost:1/")
	if err == nil {
		resp.Body.Close()
		t.Fatal("NewSender's client reached a loopback host — no guard is wired in at all")
	}
	if !strings.Contains(err.Error(), "non-public address") {
		t.Errorf("failed for the wrong reason: %v", err)
	}
}

// TestNewSender_ValidatesTheEndpointThroughAProxy is the regression test for
// NewSender wiring only HALF the SSRF control.
//
// GuardedRoundTripper is two layers: endpoint-URL validation wrapped around a
// guarded dialer. Downgrading NewSender to GuardedTransport keeps the dialer and
// drops the endpoint check — and the entire push suite stayed green, because
// every other NewSender test connects DIRECTLY, where the dial guard alone
// refuses the address and the missing layer never shows. Measured under that
// downgrade: a real fetch of http://10.0.0.5/fcm/send/abc through a proxy
// returned status 200 with body "internal secret".
//
// A proxy MUST therefore be configured here. That is the whole design of the
// test: with a proxy in play the address being dialled is the PROXY — which
// GuardedTransport exempts as operator-chosen — so the dial guard would happily
// allow the connection and ONLY the endpoint guard can refuse it. That is what
// separates the two layers.
//
// Both assertions matter. The error proves the request was refused; the silent
// proxy proves it was refused BEFORE any bytes left the process, rather than
// being fetched and then failing for some unrelated reason.
func TestNewSender_ValidatesTheEndpointThroughAProxy(t *testing.T) {
	proxyURL, reached := startRecordingProxy(t)

	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = func(*http.Request) (*url.URL, error) { return proxyURL, nil }

	s := NewSender("pub", "priv", "mailto:a@b.c",
		&http.Client{Transport: base, Timeout: 3 * time.Second})

	resp, err := s.client.Get("http://10.0.0.5/fcm/send/abc")
	if err == nil {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("NewSender's client fetched an internal endpoint through the proxy "+
			"(status %d, body %q) — it is wiring only the dial guard, so configuring "+
			"a proxy disables the SSRF control entirely", resp.StatusCode, body)
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

// TestNewSender_WiresBothGuardLayers pins BOTH switch arms of NewSender's
// transport wiring, structurally.
//
// The behavioural test above can only drive the *http.Transport arm: exercising
// the nil arm the same way would need a proxy on http.DefaultTransport, and
// neither route there is sound — ProxyFromEnvironment latches its env lookup
// behind a sync.Once (so t.Setenv is order-dependent and usually a no-op), and
// mutating the shared http.DefaultTransport is a data race under -race.
//
// So the nil arm is pinned by shape instead. Being in-package, this can name
// endpointGuard directly, which is exactly the layer a GuardedTransport
// downgrade removes: GuardedTransport returns a bare *http.Transport, so the
// assertion fails on whichever arm was downgraded. The inner check keeps the
// dial layer honest too, so this cannot pass with the endpoint guard wrapped
// around an unguarded transport.
func TestNewSender_WiresBothGuardLayers(t *testing.T) {
	cases := map[string]*http.Client{
		"nil transport arm":   {},
		"*http.Transport arm": {Transport: http.DefaultTransport.(*http.Transport).Clone()},
	}
	for name, client := range cases {
		t.Run(name, func(t *testing.T) {
			s := NewSender("pub", "priv", "mailto:a@b.c", client)

			g, ok := s.client.Transport.(endpointGuard)
			if !ok {
				t.Fatalf("transport is %T, not endpointGuard — endpoint validation is not "+
					"wired, so a configured egress proxy bypasses the SSRF control entirely",
					s.client.Transport)
			}
			inner, ok := g.next.(*http.Transport)
			if !ok {
				t.Fatalf("endpointGuard wraps %T, not *http.Transport", g.next)
			}
			if inner.DialContext == nil {
				t.Error("the wrapped transport has no DialContext — the dial guard is not " +
					"installed, so DNS rebinding is unguarded")
			}
		})
	}
}

// startRecordingProxy runs a stand-in egress proxy on loopback and returns its
// URL plus a channel carrying every target it was asked to fetch. A silent
// channel is the proof that a refusal happened before the request was sent.
func startRecordingProxy(t *testing.T) (*url.URL, <-chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	reached := make(chan string, 4)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached <- r.Host + r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("internal secret"))
	})}
	t.Cleanup(func() { _ = srv.Close() })
	go func() { _ = srv.Serve(ln) }()

	u, err := url.Parse("http://" + ln.Addr().String())
	if err != nil {
		t.Fatalf("parse proxy URL: %v", err)
	}
	return u, reached
}

// TestGuardedTransport_FallsBackAcrossAddresses is the regression test for
// losing multi-address failover.
//
// Resolving the host ourselves is what closes the DNS-rebinding window, but it
// also takes over the standard dialer's job of walking the address list. Real
// push services publish 8-16 addresses each so a single dead endpoint is
// survivable; dialing only the first would turn one unreachable address — or a
// broken IPv6 route on a dual-stack host, where RFC 6724 puts AAAA first —
// into total delivery failure.
func TestGuardedTransport_FallsBackAcrossAddresses(t *testing.T) {
	defer AllowNonPublicDialForTesting()()

	// A live server, and a port that nothing is listening on.
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer live.Close()
	_, livePort, err := net.SplitHostPort(strings.TrimPrefix(live.URL, "http://"))
	if err != nil {
		t.Fatalf("split: %v", err)
	}

	tr := GuardedTransport(http.DefaultTransport.(*http.Transport))
	// Resolve "localhost" — on a dual-stack machine this yields ::1 AND
	// 127.0.0.1. httptest listens on 127.0.0.1 only, so if ::1 sorts first the
	// dial must fall through to the second address rather than giving up.
	conn, err := tr.DialContext(context.Background(), "tcp", net.JoinHostPort("localhost", livePort))
	if err != nil {
		t.Fatalf("dial gave up instead of trying every resolved address: %v", err)
	}
	conn.Close()
}
