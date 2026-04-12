package api

import (
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
)

// xfpWarnOnce prints a one-shot operator warning when we see a reverse-proxy
// deployment that forgot to opt into TRUST_PROXY. See isSecureRequest.
var xfpWarnOnce sync.Once

// isSecureRequest reports whether the incoming request is considered secure
// (HTTPS). A direct TLS connection is always trusted. When the server sits
// behind a trusted reverse proxy (TRUST_PROXY=true), the X-Forwarded-Proto
// header is honored — the header is only safe to trust when the operator has
// explicitly opted in, because an attacker with direct network access to the
// backend port could otherwise spoof it.
func isSecureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if os.Getenv("TRUST_PROXY") != "true" {
		// Helpful one-shot warning for operators who upgraded from a pre-
		// COOKIE_SECURE release: old deployments behind Caddy/nginx were
		// getting Secure cookies unconditionally. After the migration to
		// auto mode, the same deployment silently drops the Secure flag
		// unless TRUST_PROXY=true + COOKIE_SECURE=true are set. If an XFP
		// header shows up here, we're almost certainly behind a proxy —
		// so flag it loudly the first time it happens.
		if r.Header.Get("X-Forwarded-Proto") != "" {
			xfpWarnOnce.Do(func() {
				log.Println("NOTICE: received X-Forwarded-Proto but TRUST_PROXY is not set. " +
					"If SpenDrop is behind a trusted reverse proxy, set COOKIE_SECURE=true and TRUST_PROXY=true " +
					"to restore Secure session cookies. Ignoring the header to prevent spoofing from untrusted networks.")
			})
		}
		return false
	}
	// X-Forwarded-Proto may be a comma-separated list; the first entry is the
	// client-facing protocol per RFC 7239 conventions.
	proto := r.Header.Get("X-Forwarded-Proto")
	if idx := strings.Index(proto, ","); idx != -1 {
		proto = proto[:idx]
	}
	return strings.EqualFold(strings.TrimSpace(proto), "https")
}

// shouldMarkCookieSecure determines whether an outgoing cookie should carry
// the Secure flag. The behavior is controlled by the COOKIE_SECURE env var:
//
//   - "true":           always Secure (reject-on-downgrade, requires HTTPS)
//   - "false":          never Secure (plain-HTTP deployments)
//   - "auto" or unset:  detect from the request scheme (default)
//
// The deprecated SPENDROP_INSECURE=true is honored as an alias for
// COOKIE_SECURE=false so existing deployments keep working.
func shouldMarkCookieSecure(r *http.Request) bool {
	if os.Getenv("SPENDROP_INSECURE") == "true" {
		return false
	}
	switch strings.ToLower(os.Getenv("COOKIE_SECURE")) {
	case "true":
		return true
	case "false":
		return false
	default: // "auto" or unset
		return isSecureRequest(r)
	}
}

// insecureModeEnabled reports whether the operator has explicitly opted out of
// HTTPS-only protections. Used to gate HSTS emission — HSTS should not be sent
// from deployments that are not actually served over HTTPS, so that browsers
// don't pin a broken upgrade.
func insecureModeEnabled() bool {
	if os.Getenv("SPENDROP_INSECURE") == "true" {
		return true
	}
	return strings.ToLower(os.Getenv("COOKIE_SECURE")) == "false"
}
