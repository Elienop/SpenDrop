package push

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// IsPubliclyRoutable reports whether ip is an address a push service could
// legitimately live on — i.e. not somewhere only this server can reach.
//
// Exported because the API layer rejects obviously-bad endpoints at
// subscription time with a friendly 400, while this package enforces the same
// predicate at connect time. The API check is convenience; the connect-time
// check is the actual control (see GuardedTransport).
func IsPubliclyRoutable(ip net.IP) bool {
	if ip == nil {
		return false
	}
	// Normalise IPv4-mapped IPv6 (::ffff:10.0.0.1) to its v4 form first —
	// without this, a mapped private address passes every v4 predicate below.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	switch {
	case ip.IsLoopback(),
		ip.IsPrivate(),
		ip.IsUnspecified(),
		ip.IsLinkLocalUnicast(),
		ip.IsLinkLocalMulticast(),
		ip.IsInterfaceLocalMulticast(),
		ip.IsMulticast():
		return false
	}
	// 100.64.0.0/10 — carrier-grade NAT, routable-looking but not public.
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
		return false
	}
	return true
}

// GuardedTransport returns an http.RoundTripper that refuses to connect to any
// address that is not publicly routable.
//
// This is the load-bearing SSRF control, and it lives here rather than in the
// endpoint validator for two reasons the validator cannot address:
//
//   - A validator can only check the hostname it was given. DNS resolves at
//     connect time, so an attacker who controls a name can point it at a public
//     address for validation and a private one for the send (DNS rebinding).
//   - The endpoint is stored once and fetched repeatedly for the lifetime of
//     the subscription; DNS can change at any point in between.
//
// The dialer resolves the host itself, checks every returned address, and then
// dials the specific IP it validated — never a re-resolution — so there is no
// window between the check and the connection.
// allowNonPublicDial relaxes ONLY the dial guard, for tests that must reach an
// httptest server on 127.0.0.1. Deliberately UNEXPORTED and package-private:
// it cannot be set from outside internal/push, so no production caller and no
// other package's tests can switch the control off. It does not weaken
// IsPubliclyRoutable, so the API-layer validator is unaffected by it.
var allowNonPublicDial bool

// AllowNonPublicDialForTesting relaxes the dial guard so a test can reach an
// httptest server on 127.0.0.1, and returns a function that restores it.
//
// Exported ONLY because tests in sibling packages (internal/api) drive the
// sender against a local stub server and cannot reach the package-private
// flag. It follows the same shape as auth.SetBcryptCostForTesting. The name is
// deliberately unwieldy: any production call site should be obvious in review.
// It does not weaken IsPubliclyRoutable, so the API-layer validator is
// unaffected either way.
func AllowNonPublicDialForTesting() func() {
	prev := allowNonPublicDial
	allowNonPublicDial = true
	return func() { allowNonPublicDial = prev }
}

// canonicalProxyAddr renders a proxy URL as the "host:port" the transport will
// actually dial, applying the same scheme defaults net/http uses.
func canonicalProxyAddr(u *url.URL) string {
	port := u.Port()
	if port == "" {
		switch u.Scheme {
		case "https":
			port = "443"
		case "socks5", "socks5h":
			port = "1080"
		default:
			port = "80"
		}
	}
	return net.JoinHostPort(u.Hostname(), port)
}

// maxTrackedProxies bounds the recorded proxy set. HTTP_PROXY/HTTPS_PROXY yield
// one or two entries; only a pathological custom Proxy func returning a fresh
// address per request could grow it, and that must not become a memory leak.
const maxTrackedProxies = 64

func GuardedTransport(base *http.Transport) *http.Transport {
	t := base.Clone()

	// An operator-configured egress proxy is a TRUSTED dial target, and on a
	// self-hosted box it is very often on the LAN.
	//
	// When a proxy is in play the transport dials the PROXY, not the push
	// endpoint — so a guard that only looks at the address being dialled sees
	// "192.168.1.50" and refuses it, killing every push with
	// `proxyconnect tcp: refusing to connect to non-public address`. That is a
	// silent, total outage caused by the security control rather than the
	// threat, and no test using a direct connection can see it.
	//
	// The distinction that matters is WHO CHOSE the address. The SSRF threat is
	// an endpoint chosen by whoever created the subscription; a proxy is chosen
	// by whoever runs the server, exactly like BACKUP_DIR. So the proxy the
	// transport selects is recorded here and exempted at the dial.
	//
	// Note the consequence: while a proxy is configured, this server never
	// connects to endpoints itself, so reachability policy belongs to the proxy.
	// The subscribe-time endpoint validation in the API layer remains the guard
	// on that path, and redirects are still refused.
	var proxyMu sync.RWMutex
	proxyAddrs := make(map[string]bool, 2)
	if inner := t.Proxy; inner != nil {
		t.Proxy = func(r *http.Request) (*url.URL, error) {
			u, err := inner(r)
			if err != nil || u == nil {
				return u, err
			}
			// Ordering is guaranteed: net/http resolves the proxy for a request
			// before dialling for that request, so the address is always
			// recorded by the time DialContext is asked about it.
			proxyMu.Lock()
			if len(proxyAddrs) < maxTrackedProxies {
				proxyAddrs[canonicalProxyAddr(u)] = true
			}
			proxyMu.Unlock()
			return u, nil
		}
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		proxyMu.RLock()
		isProxy := proxyAddrs[addr]
		proxyMu.RUnlock()
		if isProxy {
			return dialer.DialContext(ctx, network, addr)
		}

		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("push: malformed address %q", addr)
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("push: resolve %q: %w", host, err)
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("push: %q resolved to no addresses", host)
		}
		// Reject if ANY answer is non-public. Dialing only the "good" ones
		// would let an attacker mix a public and a private record and rely on
		// resolver ordering.
		for _, ip := range ips {
			if !allowNonPublicDial && !IsPubliclyRoutable(ip.IP) {
				return nil, fmt.Errorf("push: refusing to connect to non-public address %s (host %q)", ip.IP, host)
			}
		}
		// Try every validated address, not just the first.
		//
		// Resolving the host ourselves is what closes the rebinding window,
		// but it also takes over a job the standard dialer was doing: when
		// given a hostname it walks the address list and falls back. The real
		// push services publish 8-16 addresses each (measured: fcm 8,
		// mozilla 8, apple 16) precisely so a single unreachable endpoint is
		// survivable, and RFC 6724 ordering is host-dependent — on a
		// dual-stack box whose IPv6 route is broken, the first address is an
		// AAAA and dialing only it would fail every push. Every address here
		// has already passed the guard, so iterating costs nothing in safety.
		var firstErr error
		for _, ip := range ips {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
			if err == nil {
				return conn, nil
			}
			if firstErr == nil {
				firstErr = err
			}
			// A cancelled or timed-out context means giving up, not trying the
			// next address — otherwise one dead host burns the whole budget.
			if ctx.Err() != nil {
				break
			}
		}
		return nil, firstErr
	}
	return t
}

// refuseRedirects is the second half of the control. Without it, a push
// endpoint on an attacker-controlled public host can 302 the sender straight
// at an internal address: the guarded dialer would then be asked to connect to
// whatever the redirect names, and while it would still refuse a private
// target, allowing redirects at all widens the surface for no benefit — real
// push services (FCM, Mozilla, Apple) answer directly.
func refuseRedirects(_ *http.Request, via []*http.Request) error {
	return fmt.Errorf("push: refusing redirect after %d hop(s); push endpoints must answer directly", len(via))
}
