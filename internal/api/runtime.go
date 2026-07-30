package api

import (
	"log"
	"sync"
	"time"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/config"
)

// Package-level runtime knobs, loaded from *config.Config in ApplyConfig.
// Initialized at package init from config.Defaults() so tests that use
// NewHandler directly (without going through NewRouter) see sensible limits
// that match the previously hard-coded values. Keeping init values as a
// function of config.Defaults() means the two can never drift.
//
// The RWMutex is load-bearing only on the write path — it protects against a
// data race if ApplyConfig is called concurrently with a request. In
// production ApplyConfig is called once during startup, before any handler
// can run, so the locks are effectively free on the hot path.
//
// Note: RateLimit.Window is read exactly once by startRateLimitReset when
// the ticker goroutine first fires. Later calls to ApplyConfig update the
// stored value but do not restart the ticker — production only calls
// ApplyConfig once, so this is intentional.
var (
	runtimeMu sync.RWMutex

	// The default Config is resolved once at package init and used to seed
	// every runtime knob below, so there is a single source of truth.
	runtimeDefaults = config.Defaults()

	// maxJSONBodyBytes is the size cap applied by decodeJSON.
	maxJSONBodyBytes = runtimeDefaults.Upload.MaxJSONBytes

	// maxUploadBodyBytes is the size cap applied to multipart file uploads.
	maxUploadBodyBytes = runtimeDefaults.Upload.MaxFileBytes

	// rateLimitMax is the per-IP attempt threshold before login/register
	// return 429. The reset ticker runs in startRateLimitReset.
	rateLimitMax = runtimeDefaults.RateLimit.MaxAttempts

	// rateLimitTickerWindow is how often rate limit counters are reset.
	rateLimitTickerWindow = runtimeDefaults.RateLimit.Window

	// sessionTTL is the lifetime of a newly minted session cookie.
	sessionTTL = runtimeDefaults.Session.TTL

	// passwordMinLength / passwordMaxLength enforce the byte-length bounds
	// for user-supplied passwords. Max must stay ≤ 72 (bcrypt input limit).
	passwordMinLength = runtimeDefaults.Password.MinLength
	passwordMaxLength = runtimeDefaults.Password.MaxLength

	// pushEnabled toggles the Web Push feature surface. When false, the public
	// vapid route 404s and the subscribe/test handlers report disabled.
	pushEnabled = runtimeDefaults.Push.Enabled

	// vapidPublicKey is the base64url application-server public key echoed to
	// the browser by /api/push/vapid-public-key. Never a secret.
	vapidPublicKey = runtimeDefaults.Push.VAPIDPublicKey
)

// ApplyConfig mutates the package-level runtime knobs from cfg. Called
// exactly once from NewRouter during application startup. Safe to call in
// tests to hermetically reset state between runs, but note that the rate
// limit ticker window is latched by startRateLimitReset on first fire and
// will not update after that — restart the process to change it.
func ApplyConfig(cfg *config.Config) {
	runtimeMu.Lock()
	defer runtimeMu.Unlock()
	maxJSONBodyBytes = cfg.Upload.MaxJSONBytes
	maxUploadBodyBytes = cfg.Upload.MaxFileBytes
	rateLimitMax = cfg.RateLimit.MaxAttempts
	// internal/auth keys its own auth-failure limiter by IP and must make the
	// same trust decision; it cannot import internal/api, so mirror the flag.
	//
	// Validate already parsed these successfully, so an error here can only mean
	// the Config was mutated after Load. Trust nothing in that case: an empty set
	// falls back to the socket address, which is the safe direction.
	trustedCIDRs, err := cfg.ParsedTrustedProxyCIDRs()
	if err != nil {
		log.Printf("rate-limit: ignoring unparseable TRUSTED_PROXY_CIDRS (%v); "+
			"keying the limiter on the socket address", err)
		trustedCIDRs = nil
	}
	auth.SetTrustProxyHeaders(cfg.RateLimit.TrustProxyHeaders, cfg.RateLimit.TrustedProxyHops, trustedCIDRs)
	rateLimitTickerWindow = cfg.RateLimit.Window
	sessionTTL = cfg.Session.TTL
	passwordMinLength = cfg.Password.MinLength
	passwordMaxLength = cfg.Password.MaxLength
	pushEnabled = cfg.Push.Enabled
	vapidPublicKey = cfg.Push.VAPIDPublicKey
}

// getMaxJSONBytes returns the current JSON body cap under the read lock so
// we avoid racing with ApplyConfig.
func getMaxJSONBytes() int64 {
	runtimeMu.RLock()
	defer runtimeMu.RUnlock()
	return maxJSONBodyBytes
}

// getMaxUploadBytes returns the current multipart upload cap.
func getMaxUploadBytes() int64 {
	runtimeMu.RLock()
	defer runtimeMu.RUnlock()
	return maxUploadBodyBytes
}

// getRateLimitMax returns the current rate limit threshold.
func getRateLimitMax() int {
	runtimeMu.RLock()
	defer runtimeMu.RUnlock()
	return rateLimitMax
}

// getSessionTTL returns the current session cookie lifetime.
func getSessionTTL() time.Duration {
	runtimeMu.RLock()
	defer runtimeMu.RUnlock()
	return sessionTTL
}

// getPasswordBounds returns the current min/max password byte length.
// Returned as a pair so callers do two validations without releasing and
// reacquiring the read lock.
func getPasswordBounds() (minLen, maxLen int) {
	runtimeMu.RLock()
	defer runtimeMu.RUnlock()
	return passwordMinLength, passwordMaxLength
}

// getPushEnabled reports whether the Web Push config flag is on.
func getPushEnabled() bool {
	runtimeMu.RLock()
	defer runtimeMu.RUnlock()
	return pushEnabled
}

// getVAPIDPublicKey returns the configured base64url VAPID public key.
func getVAPIDPublicKey() string {
	runtimeMu.RLock()
	defer runtimeMu.RUnlock()
	return vapidPublicKey
}
