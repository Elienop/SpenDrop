package push

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestGuardedTransport_ClearsCustomTLSDialers is the regression test for the two
// lines in GuardedTransport that clear DialTLS and DialTLSContext.
//
// Those lines look like tidying and are actually the whole control for https://.
// net/http checks hasCustomTLSDialer() FIRST: when either field is set on a
// transport, an https request is handed straight to it and DialContext — the
// only place the address guard lives — is never consulted at all. Clone()
// preserves both fields, so a base transport carrying one would produce a
// transport that looks guarded and is not.
//
// Deleting both lines leaves the rest of the push suite green, because every
// other test builds its base from http.DefaultTransport, which sets neither
// field. Measured with them deleted: this test's custom dialer was invoked and
// reached a loopback HTTPS service, returning 200 "internal secret".
//
// Not reachable from production today — main.go passes http.DefaultClient, whose
// Transport is nil — so this is defence-in-depth against a future caller that
// supplies a tuned transport. That is exactly the kind of guarantee that needs a
// test, since nothing else would notice it being lost.
//
// The two fields are pinned in separate subtests because net/http prefers
// DialTLSContext and falls back to DialTLS, so a single case could leave one of
// the two lines free to be deleted.
func TestGuardedTransport_ClearsCustomTLSDialers(t *testing.T) {
	internalAddr, roots := startInternalTLSService(t)

	// Dials the internal HTTPS service no matter what address it is handed. This
	// stands in for any custom TLS dialer on a caller-supplied transport; if the
	// guard lets it survive, it is a complete bypass of the address check.
	var called atomic.Int32
	reachInternal := func() (net.Conn, error) {
		called.Add(1)
		return tls.Dial("tcp", internalAddr, &tls.Config{
			RootCAs:    roots,
			ServerName: "localhost",
		})
	}

	cases := map[string]func(*http.Transport){
		"DialTLSContext": func(tr *http.Transport) {
			tr.DialTLSContext = func(context.Context, string, string) (net.Conn, error) {
				return reachInternal()
			}
		},
		"DialTLS": func(tr *http.Transport) {
			tr.DialTLS = func(string, string) (net.Conn, error) { return reachInternal() }
		},
	}

	for name, install := range cases {
		t.Run(name, func(t *testing.T) {
			called.Store(0)

			base := &http.Transport{}
			install(base)

			c := &http.Client{Transport: GuardedTransport(base), Timeout: 5 * time.Second}

			// A non-public endpoint over https. The guarded DialContext must refuse
			// it; the custom TLS dialer would happily connect instead.
			resp, err := c.Get("https://10.0.0.5/fcm/send/abc")
			if err == nil {
				resp.Body.Close()
				t.Fatalf("an internal HTTPS service was reached (status %d) — GuardedTransport "+
					"kept the base transport's %s, so https bypasses the address guard entirely",
					resp.StatusCode, name)
			}
			if !strings.Contains(err.Error(), "non-public address") {
				t.Errorf("refused for the wrong reason: %v", err)
			}
			// The strong assertion: the bypass path was never even entered.
			if n := called.Load(); n != 0 {
				t.Errorf("the custom %s was invoked %d time(s); it must be cleared so every "+
					"https dial goes through the guarded DialContext", name, n)
			}
		})
	}
}

// startInternalTLSService runs an HTTPS service on loopback standing in for
// something only this host can reach, and returns its address plus a pool
// trusting its certificate.
func startInternalTLSService(t *testing.T) (addr string, roots *x509.CertPool) {
	t.Helper()

	cert, pool := newLocalhostOnlyCert(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("internal secret"))
		}),
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}},
	}
	t.Cleanup(func() { _ = srv.Close() })
	go func() { _ = srv.ServeTLS(ln, "", "") }()

	return ln.Addr().String(), pool
}
