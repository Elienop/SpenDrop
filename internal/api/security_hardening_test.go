package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elienop/spendrop/internal/config"
	"github.com/elienop/spendrop/internal/database"
	"github.com/elienop/spendrop/internal/push"
)

func postRegister(t *testing.T, h *Handler, username, remoteAddr string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{
		"username": username,
		"password": "correct-horse-battery",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = remoteAddr
	rec := httptest.NewRecorder()
	h.handleRegister(rec, req)
	return rec
}

// TestHandleRegister_ClosedInstanceRejectsAndThrottles covers 15a.
//
// Registration used to hash the password with bcrypt BEFORE checking whether
// registration was open, and the attempt counter was only incremented when
// CreateUser failed. So probing a closed instance cost the attacker nothing,
// cost the server a full bcrypt each time, and never tripped the limiter.
func TestHandleRegister_ClosedInstanceRejectsAndThrottles(t *testing.T) {
	h := setupHandler(t)
	// An existing user means this is no longer the bootstrap case, and the
	// registration-enabled setting is absent, so registration is closed.
	seedTestUser(t, h.queries, "existing", "admin")

	resetRegisterAttempts()

	first := postRegister(t, h, "attacker1", "203.0.113.9:1234")
	if first.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 on a closed instance; body = %s",
			first.Code, first.Body.String())
	}

	// The rejection must be counted, or the abuse path never throttles.
	var throttled bool
	for i := 0; i < getRateLimitMax()+2; i++ {
		rec := postRegister(t, h, "attacker", "203.0.113.9:1234")
		if rec.Code == http.StatusTooManyRequests {
			throttled = true
			break
		}
	}
	if !throttled {
		t.Error("repeated rejected registrations never produced a 429 — " +
			"the limiter is not counting the abuse path")
	}
}

// TestClientIPForRateLimit_TrustsProxyOnlyWhenConfigured covers 15b.
func TestClientIPForRateLimit_TrustsProxyOnlyWhenConfigured(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	req.RemoteAddr = "10.0.0.5:5000" // the reverse proxy
	req.Header.Set("X-Forwarded-For", "198.51.100.7, 203.0.113.4")

	setTrustProxyHeadersForTest(t, false)
	if got := clientIPForRateLimit(req); got != "10.0.0.5" {
		t.Errorf("untrusted: got %q, want the socket address 10.0.0.5 — "+
			"trusting a forgeable header would let anyone mint a new bucket per request", got)
	}

	setTrustProxyHeadersForTest(t, true)
	// RIGHTMOST, not leftmost: that is the entry our own proxy appended. The
	// leftmost is whatever the client claimed.
	if got := clientIPForRateLimit(req); got != "203.0.113.4" {
		t.Errorf("trusted: got %q, want 203.0.113.4 (rightmost XFF entry)", got)
	}

	// With no header at all, fall back to the socket address either way.
	bare := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	bare.RemoteAddr = "198.51.100.20:9999"
	if got := clientIPForRateLimit(bare); got != "198.51.100.20" {
		t.Errorf("no XFF: got %q, want 198.51.100.20", got)
	}
}

// TestValidatePushEndpoint covers 15e: the stored endpoint is fetched by the
// push sender, so an unvalidated value lets an authenticated user aim the
// server at hosts only it can reach.
func TestValidatePushEndpoint(t *testing.T) {
	bad := map[string]string{
		"plain http":      "http://push.example.com/x",
		"loopback":        "https://127.0.0.1/x",
		"loopback name":   "https://[::1]/x",
		"private 10/8":    "https://10.0.0.5/x",
		"private 192.168": "https://192.168.1.1/admin",
		"link-local":      "https://169.254.169.254/latest/meta-data",
		"no host":         "https:///x",
		"not a url":       "::::",
		"file scheme":     "file:///etc/passwd",
	}
	for name, endpoint := range bad {
		if err := validatePushEndpoint(endpoint); err == nil {
			t.Errorf("%s: %q was accepted", name, endpoint)
		}
	}

	good := []string{
		"https://fcm.googleapis.com/fcm/send/abc123",
		"https://updates.push.services.mozilla.com/wpush/v2/xyz",
		"https://web.push.apple.com/QW",
	}
	for _, endpoint := range good {
		if err := validatePushEndpoint(endpoint); err != nil {
			t.Errorf("legitimate endpoint %q rejected: %v", endpoint, err)
		}
	}
}

// TestRunPasswordResetCascade_RevokesPushSubscriptions covers 15d. A push
// subscription is a standing capability to receive household activity, so
// leaving it alive after a credential rotation means a compromised device
// keeps getting notifications from an account it can no longer log into.
func TestRunPasswordResetCascade_RevokesPushSubscriptions(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "victim", "member")

	if err := h.queries.UpsertPushSubscription(t.Context(), database.UpsertPushSubscriptionParams{
		UserID:   user.ID,
		Endpoint: "https://fcm.googleapis.com/fcm/send/compromised-device",
		P256dh:   "key",
		Auth:     "auth",
	}); err != nil {
		t.Fatalf("seed push subscription: %v", err)
	}

	before, err := h.queries.CountPushSubscriptionsByUser(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("count before: %v", err)
	}
	if before != 1 {
		t.Fatalf("fixture wrong: %d subscriptions", before)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/auth/password", nil)
	req = withUser(req, user)
	actor := newActorContext(req, user)
	if _, err := h.runPasswordResetCascade(req, user.ID, "new-hash", actor); err != nil {
		t.Fatalf("cascade: %v", err)
	}

	after, err := h.queries.CountPushSubscriptionsByUser(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("count after: %v", err)
	}
	if after != 0 {
		t.Errorf("%d push subscriptions survived the password change — "+
			"a compromised device still receives household activity", after)
	}

	// The rest of the cascade must still have run.
	u, err := h.queries.GetUserByID(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if u.PasswordHash != "new-hash" {
		t.Errorf("password was not updated: %q", u.PasswordHash)
	}
}

func resetRegisterAttempts() {
	rateLimitMu.Lock()
	registerAttempts = make(map[string]int)
	rateLimitMu.Unlock()
}

func setTrustProxyHeadersForTest(t *testing.T, v bool) {
	t.Helper()
	runtimeMu.Lock()
	prev := trustProxyHeaders
	trustProxyHeaders = v
	runtimeMu.Unlock()
	t.Cleanup(func() {
		runtimeMu.Lock()
		trustProxyHeaders = prev
		runtimeMu.Unlock()
	})
}

// TestHandleCreatePushSubscription_RejectsPrivateEndpoint proves the validator
// is actually WIRED INTO the handler, not merely present.
//
// TestValidatePushEndpoint alone is not enough: it exercises the function
// directly, so it still passes with the handler's call removed — which is
// exactly what mutation testing revealed.
func TestHandleCreatePushSubscription_RejectsPrivateEndpoint(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	cfg := enabledPushCfg()
	h.SetPushSender(push.NewSender(cfg.Push.VAPIDPublicKey, cfg.Push.VAPIDPrivateKey, cfg.Push.VAPIDSubject, http.DefaultClient))
	ApplyConfig(cfg)
	t.Cleanup(func() { d := config.Defaults(); ApplyConfig(&d) })

	user := seedTestUser(t, q, "subscriber", "member")

	body, _ := json.Marshal(map[string]any{
		// The classic SSRF target: only the server can reach it.
		"endpoint": "https://169.254.169.254/latest/meta-data",
		"keys":     map[string]string{"p256dh": "k", "auth": "a"},
	})
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/push/subscribe", bytes.NewReader(body)), user)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.handleCreatePushSubscription(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}

	n, err := q.CountPushSubscriptionsByUser(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("a private-address endpoint was stored anyway (%d rows)", n)
	}
}
