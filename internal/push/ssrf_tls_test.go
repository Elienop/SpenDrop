package push

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestGuardedTransport_PreservesTLSHostnameVerification is the regression test
// for the one way this SSRF fix could have broken every push it was meant to
// protect.
//
// The guarded dialer resolves the host itself and hands the dialer a raw IP.
// If the TLS layer took its ServerName — and therefore its certificate
// check — from the address actually dialed, every HTTPS push would fail
// verification against FCM, Mozilla and Apple, whose certificates name
// hostnames and not IPs. Nothing else in the suite would notice: the unit tests
// all use plaintext httptest servers, so TLS is never exercised.
//
// The certificate here covers the DNS name "localhost" and deliberately carries
// NO IP SANs, which is what makes both halves of this test load-bearing:
//
//   - Reaching https://localhost:PORT succeeds ONLY if the handshake used the
//     URL's hostname. Had it used 127.0.0.1, verification would fail.
//   - Reaching https://127.0.0.1:PORT must FAIL. If it succeeds, verification
//     has been disabled outright (InsecureSkipVerify), and the first assertion
//     would have passed for the wrong reason.
//
// Asserting only the first would be vacuous against that second mutation.
func TestGuardedTransport_PreservesTLSHostnameVerification(t *testing.T) {
	defer AllowNonPublicDialForTesting()()

	cert, roots := newLocalhostOnlyCert(t)

	// Records the SNI value the server was actually shown.
	var mu sync.Mutex
	var sniSeen []string

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			mu.Lock()
			sniSeen = append(sniSeen, hello.ServerName)
			mu.Unlock()
			return nil, nil
		},
	}

	// Bind 127.0.0.1 only. "localhost" may also resolve to ::1, so this
	// simultaneously exercises the multi-address fallback in the dial loop.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("ok"))
		}),
		TLSConfig: tlsCfg,
	}
	defer srv.Close()
	go func() { _ = srv.ServeTLS(ln, "", "") }()

	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split: %v", err)
	}

	tr := GuardedTransport(http.DefaultTransport.(*http.Transport))
	// Add the test root WITHOUT replacing the config. Assigning a fresh
	// tls.Config here would discard anything GuardedTransport had set, so a
	// future InsecureSkipVerify or pinned ServerName in production code would
	// be invisible to this test — which is precisely what half 2 exists to catch.
	if tr.TLSClientConfig == nil {
		tr.TLSClientConfig = &tls.Config{}
	}
	tr.TLSClientConfig.RootCAs = roots
	client := &http.Client{Transport: tr, Timeout: 10 * time.Second}

	// Half 1: the hostname must verify, through a dial to a raw IP.
	resp, err := client.Get("https://localhost:" + port + "/")
	if err != nil {
		t.Fatalf("HTTPS through the guarded dialer failed — every push over TLS is broken: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got status %d, want 200", resp.StatusCode)
	}

	mu.Lock()
	got := append([]string(nil), sniSeen...)
	mu.Unlock()
	if len(got) == 0 {
		t.Fatal("server observed no TLS handshake")
	}
	if got[0] != "localhost" {
		t.Errorf("SNI was %q, want \"localhost\" — the handshake is using the dialed address "+
			"instead of the URL host, so certificates will not verify against real push endpoints", got[0])
	}

	// Half 2: the same certificate must NOT satisfy a request to the IP. This is
	// what proves half 1 passed because hostname verification works, rather than
	// because verification is switched off.
	resp2, err := client.Get("https://127.0.0.1:" + port + "/")
	if err == nil {
		resp2.Body.Close()
		t.Fatal("a certificate with no IP SAN was accepted for an IP-literal URL — " +
			"TLS verification is disabled, which also invalidates the hostname assertion above")
	}
	if !strings.Contains(err.Error(), "certificate") && !strings.Contains(err.Error(), "x509") {
		t.Errorf("IP-literal request failed for the wrong reason: %v", err)
	}
}

// newLocalhostOnlyCert returns a self-signed certificate valid for the DNS name
// "localhost" and for no IP address at all, plus a pool trusting it.
func newLocalhostOnlyCert(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "localhost"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost"},
		// IPAddresses intentionally empty — see the test doc comment.
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("createcert: %v", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parsecert: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)

	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, pool
}
