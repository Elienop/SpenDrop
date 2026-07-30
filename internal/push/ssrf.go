package push

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
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

// allowNonPublicDial relaxes the address checks for tests that must reach an
// httptest server on 127.0.0.1. Deliberately UNEXPORTED and package-private: it
// cannot be set from outside internal/push, so no production caller and no other
// package's tests can switch the control off. It does not weaken
// IsPubliclyRoutable, so the API-layer validator is unaffected by it.
//
// Atomic because the dialer runs on the transport's own goroutine: a plain bool
// written by a test helper and read there is a data race, which `go test -race`
// reports as a failure rather than a warning.
var allowNonPublicDial atomic.Bool

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
	prev := allowNonPublicDial.Load()
	allowNonPublicDial.Store(true)
	return func() { allowNonPublicDial.Store(prev) }
}

// validateHostIsPublic resolves host and requires every answer to be publicly
// routable. A literal address skips the lookup.
//
// Rejecting on ANY non-public answer, rather than filtering to the good ones, is
// deliberate: filtering would let an attacker publish one public and one private
// record and let resolver ordering decide.
func validateHostIsPublic(ctx context.Context, host string) error {
	if allowNonPublicDial.Load() {
		return nil
	}
	if host == "" {
		return fmt.Errorf("push: refusing a request with no host")
	}
	if ip := net.ParseIP(host); ip != nil {
		if !IsPubliclyRoutable(ip) {
			return fmt.Errorf("push: refusing to connect to non-public address %s (host %q)", ip, host)
		}
		return nil
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("push: resolve %q: %w", host, err)
	}
	return allAddrsPublic(host, ips)
}

// allAddrsPublic requires EVERY answer to be routable, and is split out so the
// mixed public/private case can be tested — no real hostname resolves that way,
// and it is the case the rule exists for.
func allAddrsPublic(host string, ips []net.IPAddr) error {
	if len(ips) == 0 {
		return fmt.Errorf("push: %q resolved to no addresses", host)
	}
	for _, ip := range ips {
		if !IsPubliclyRoutable(ip.IP) {
			return fmt.Errorf("push: refusing to connect to non-public address %s (host %q)", ip.IP, host)
		}
	}
	return nil
}

// endpointGuard validates the REQUEST URL's host before the request is sent.
//
// The dial-time guard alone is not sufficient, because it can only judge the
// address being dialled — and when an egress proxy is configured that address is
// the PROXY, never the endpoint. The consequence was that configuring
// HTTP(S)_PROXY silently switched the SSRF control off entirely: a subscription
// registered against http://10.0.0.5/... was handed to the proxy and fetched,
// with the guard none the wiser. Measured: status 200, and the proxy logged the
// internal target.
//
// So the two checks answer different questions and both are needed. This one
// asks "is the endpoint we were asked for allowed?", which survives a proxy. The
// dial guard asks "is the address we are about to connect to allowed?", which is
// what closes the DNS-rebinding window, since it runs after resolution and dials
// the exact address it validated.
type endpointGuard struct{ next http.RoundTripper }

func (g endpointGuard) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := validateHostIsPublic(req.Context(), req.URL.Hostname()); err != nil {
		return nil, err
	}
	return g.next.RoundTrip(req)
}

// GuardedRoundTripper is the full control: endpoint validation wrapped around a
// guarded dialer. Production callers want this rather than GuardedTransport,
// which is exported only so tests can drive the dialer directly.
func GuardedRoundTripper(base *http.Transport) http.RoundTripper {
	return endpointGuard{next: GuardedTransport(base)}
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

// Per-address dial bounds for the multi-address walk.
//
// The floor exists because dividing the budget by a long address list can produce
// a timeout too short to complete a legitimate handshake to a healthy host; the
// ceiling keeps a single address from monopolising a generous budget. Between
// them, a 30s request budget tries roughly fifteen addresses instead of three.
const (
	minPerAddrDialTimeout = 2 * time.Second
	maxPerAddrDialTimeout = 10 * time.Second
)

// perAddrDialTimeout divides whatever budget is left among the addresses still
// untried. Without a deadline on ctx there is nothing to divide, so the ceiling
// applies.
func perAddrDialTimeout(ctx context.Context, remainingAddrs int) time.Duration {
	budget := time.Duration(-1)
	if deadline, ok := ctx.Deadline(); ok {
		budget = time.Until(deadline)
	}
	return dialTimeoutFor(budget, remainingAddrs)
}

// dialTimeoutFor is the pure arithmetic, split out so the walk's behaviour over a
// whole address list can be simulated in a test without waiting in real time.
// A negative budget means "no deadline known".
func dialTimeoutFor(budget time.Duration, remainingAddrs int) time.Duration {
	if remainingAddrs < 1 {
		remainingAddrs = 1
	}
	per := maxPerAddrDialTimeout
	if budget > 0 {
		per = budget / time.Duration(remainingAddrs)
	}
	if per > maxPerAddrDialTimeout {
		per = maxPerAddrDialTimeout
	}
	if per < minPerAddrDialTimeout {
		per = minPerAddrDialTimeout
	}
	return per
}

// interleaveByFamily alternates IPv6 and IPv4 answers so one address family
// cannot consume the whole dial budget.
//
// The family the resolver put first is kept first, preserving the RFC 6724
// preference the host expressed; only the ORDER WITHIN the walk changes, so a
// working family is always reached by the second attempt.
func interleaveByFamily(ips []net.IPAddr) []net.IPAddr {
	var v4, v6 []net.IPAddr
	for _, ip := range ips {
		if ip.IP.To4() != nil {
			v4 = append(v4, ip)
		} else {
			v6 = append(v6, ip)
		}
	}
	if len(v4) == 0 || len(v6) == 0 {
		return ips
	}
	first, second := v6, v4
	if ips[0].IP.To4() != nil {
		first, second = v4, v6
	}
	out := make([]net.IPAddr, 0, len(ips))
	for i := 0; i < len(first) || i < len(second); i++ {
		if i < len(first) {
			out = append(out, first[i])
		}
		if i < len(second) {
			out = append(out, second[i])
		}
	}
	return out
}

// maxTrackedProxies bounds the recorded proxy set. HTTP_PROXY/HTTPS_PROXY yield
// one or two entries; only a pathological custom Proxy func returning a fresh
// address per request could grow it, and that must not become a memory leak.
const maxTrackedProxies = 64

// GuardedTransport returns a transport whose DIALER refuses any address that is
// not publicly routable.
//
// This half of the control is what closes the DNS-rebinding window, which an
// endpoint validator cannot do on its own:
//
//   - A validator can only check the hostname it was given. DNS resolves at
//     connect time, so an attacker who controls a name can point it at a public
//     address for validation and a private one for the send.
//   - The endpoint is stored once and fetched repeatedly for the lifetime of
//     the subscription; DNS can change at any point in between.
//
// The dialer resolves the host itself, checks every returned address, and then
// dials the specific IP it validated — never a re-resolution — so there is no
// window between the check and the connection.
//
// It is NOT sufficient alone: with an egress proxy the address dialled is the
// proxy, so the endpoint is never examined. Production should use
// GuardedRoundTripper, which adds that check. This is exported so tests can
// drive the dialer directly.
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
			if !allowNonPublicDial.Load() && !IsPubliclyRoutable(ip.IP) {
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
		//
		// Two things make the walk actually reach a working address rather than
		// merely attempt to. Both matter only when a dial FAILS SLOWLY — a dropped
		// SYN rather than a refusal — which is the normal signature of a broken
		// IPv6 route, and is precisely the case this loop exists for:
		//
		//   - Addresses are interleaved by family, so the second attempt is always
		//     the other family. Taking the resolver's order verbatim meant a host
		//     with several blackholing AAAA records spent the entire budget inside
		//     one family and never reached the A records at all.
		//   - Each dial gets a share of the REMAINING budget instead of a fixed
		//     10s. At a fixed 10s, three slow failures consumed the sender's whole
		//     30s request timeout, the loop hit its own ctx.Err() check, and the
		//     remaining addresses — including every working one — were skipped.
		//     Apple publishes 16.
		ordered := interleaveByFamily(ips)
		var firstErr error
		for i, ip := range ordered {
			perAddr := perAddrDialTimeout(ctx, len(ordered)-i)
			dctx, cancel := context.WithTimeout(ctx, perAddr)
			conn, err := dialer.DialContext(dctx, network, net.JoinHostPort(ip.IP.String(), port))
			cancel()
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
