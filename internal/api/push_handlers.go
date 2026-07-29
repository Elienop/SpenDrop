package api

import (
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/database"
	"github.com/elienop/spendrop/internal/push"
)

// pushKeys mirrors the browser PushSubscription.toJSON() "keys" object.
type pushKeys struct {
	P256dh string `json:"p256dh"`
	Auth   string `json:"auth"`
}

// pushSubscribeRequest is the browser PushSubscription.toJSON() shape. We read
// ONLY endpoint + keys; any user_id-ish field in the body is ignored — the
// owner is always the authenticated caller (no-IDOR invariant).
type pushSubscribeRequest struct {
	Endpoint string   `json:"endpoint"`
	Keys     pushKeys `json:"keys"`
}

type pushUnsubscribeRequest struct {
	Endpoint string `json:"endpoint"`
}

// pushEnabled reports whether the feature is live: config flag on AND a sender
// was installed at startup. The two move together (main.go only calls
// SetPushSender when cfg.Push.Enabled) but checking both is cheap defence.
func (h *Handler) pushEnabled() bool {
	return h.pushSender != nil && getPushEnabled()
}

// handleGetVAPIDPublicKey serves the application-server public key the browser
// needs to subscribe. PUBLIC route (no auth) — the public key is not a secret;
// the browser fetches it before any session exists. Returns 404 when the
// feature is disabled so a probing client cannot distinguish "disabled" from
// "route absent."
func (h *Handler) handleGetVAPIDPublicKey(w http.ResponseWriter, r *http.Request) {
	if !h.pushEnabled() {
		writeError(w, http.StatusNotFound, "push not enabled")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"publicKey": getVAPIDPublicKey()})
}

// validatePushEndpoint checks that a subscription endpoint is a URL this
// server is willing to make outbound requests to.
//
// The endpoint is stored and later fetched by the push sender, so an
// authenticated user could otherwise aim the server at any host reachable from
// it — the classic SSRF shape, and on a self-hosted box the interesting
// targets (router admin pages, other containers, cloud metadata endpoints) all
// sit on private ranges the server can reach and the attacker cannot.
//
// Real push services are all public HTTPS endpoints, so the restriction costs
// nothing legitimate. DNS names are not resolved here: resolution happens at
// send time and could differ (DNS rebinding), so this is a cheap first filter,
// not a complete SSRF defence.
func validatePushEndpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("endpoint is not a valid URL")
	}
	if u.Scheme != "https" {
		return fmt.Errorf("endpoint must use https")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("endpoint must include a host")
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
			ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("endpoint must not target a private or loopback address")
		}
	}
	return nil
}

// handleCreatePushSubscription registers (or refreshes) a browser subscription
// for the authenticated caller. The owner is taken from auth.GetUser — NEVER
// from the body — so a forged user_id cannot plant a subscription under
// another user. Enforces MaxSubscriptionsPerUser. The endpoint is a capability
// secret (anyone holding it can push to the device), so it is never logged.
func (h *Handler) handleCreatePushSubscription(w http.ResponseWriter, r *http.Request) {
	if !h.pushEnabled() {
		writeError(w, http.StatusNotFound, "push not enabled")
		return
	}
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req pushSubscribeRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Endpoint == "" || req.Keys.P256dh == "" || req.Keys.Auth == "" {
		writeError(w, http.StatusBadRequest, "endpoint and keys are required")
		return
	}
	if err := validatePushEndpoint(req.Endpoint); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Reject a cross-user re-home. The upsert's ON CONFLICT(endpoint) would
	// silently re-assign user_id to the caller, so a user who learns another
	// user's endpoint out-of-band could steal the device. If the endpoint
	// already exists under a DIFFERENT user we bail with 409; if it is unknown
	// (ErrNoRows) or already owned by the caller, the upsert proceeds (refresh).
	existing, err := h.queries.GetPushSubscriptionByEndpoint(r.Context(), req.Endpoint)
	if err != nil && err != sql.ErrNoRows {
		writeError(w, http.StatusInternalServerError, "failed to check subscription")
		return
	}
	if err == nil && existing.UserID != user.ID {
		writeError(w, http.StatusConflict, "endpoint already registered to another account")
		return
	}

	// Cap check. An upsert of an endpoint this user already owns is a refresh,
	// not a new row, so it must not be blocked — but a brand-new endpoint past
	// the cap is. We approximate by counting existing rows and only gating when
	// the endpoint is not already present for this user.
	count, err := h.queries.CountPushSubscriptionsByUser(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count subscriptions")
		return
	}
	if count >= MaxSubscriptionsPerUser {
		existing, err := h.queries.ListPushSubscriptionsByUser(r.Context(), user.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read subscriptions")
			return
		}
		isRefresh := false
		for _, s := range existing {
			if s.Endpoint == req.Endpoint {
				isRefresh = true
				break
			}
		}
		if !isRefresh {
			writeError(w, http.StatusTooManyRequests, "subscription limit reached")
			return
		}
	}

	ua := r.Header.Get("User-Agent")
	if len(ua) > 500 {
		ua = ua[:500]
	}
	if err := h.queries.UpsertPushSubscription(r.Context(), database.UpsertPushSubscriptionParams{
		UserID:    user.ID,
		Endpoint:  req.Endpoint,
		P256dh:    req.Keys.P256dh,
		Auth:      req.Keys.Auth,
		UserAgent: toNullString(ua),
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save subscription")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]bool{"ok": true})
}

// handleDeletePushSubscription removes a subscription the authenticated caller
// owns, matched by endpoint AND user_id. This route is always authenticated
// (registered inside the auth group), so the delete is user-scoped: a user who
// learns another user's endpoint out-of-band cannot delete that user's row
// (no IDOR). The unscoped DeletePushSubscriptionByEndpoint is reserved for the
// internal dead-endpoint prune path. 204 No Content on success; idempotent —
// a delete that matches no row the caller owns still returns 204.
func (h *Handler) handleDeletePushSubscription(w http.ResponseWriter, r *http.Request) {
	if !h.pushEnabled() {
		writeError(w, http.StatusNotFound, "push not enabled")
		return
	}
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req pushUnsubscribeRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Endpoint == "" {
		writeError(w, http.StatusBadRequest, "endpoint is required")
		return
	}
	if err := h.queries.DeletePushSubscriptionByEndpointAndUser(r.Context(), database.DeletePushSubscriptionByEndpointAndUserParams{
		Endpoint: req.Endpoint,
		UserID:   user.ID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete subscription")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handlePushTest fans a test notification to every subscription the caller
// owns. Per-user rate limited (5/hour) so a user mashing "test" cannot abuse
// the push service quota. Dead subscriptions (410/404) are pruned as a side
// effect of the send — this is the same prune path the budget evaluator uses.
func (h *Handler) handlePushTest(w http.ResponseWriter, r *http.Request) {
	if !h.pushEnabled() {
		writeError(w, http.StatusNotFound, "push not enabled")
		return
	}
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	userKey := strconv.FormatInt(user.ID, 10)
	if h.pushTestLimiter.Exhausted(userKey) {
		w.Header().Set("Retry-After", h.pushTestLimiter.RetryAfter(userKey))
		writeError(w, http.StatusTooManyRequests, "test rate limit exceeded")
		return
	}
	// Count the attempt up front, before the network fan-out. This makes the
	// 5/hour cap count attempts (not completed sends): concurrent requests can
	// no longer all pass the Exhausted gate before any Consume lands, and an
	// early error return below still records the attempt. Mirrors how the
	// login/token buckets count the attempt rather than the success.
	h.pushTestLimiter.Consume(userKey)

	subs, err := h.queries.ListPushSubscriptionsByUser(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list subscriptions")
		return
	}

	payload := []byte(`{"title":"SpenDrop","body":"Test notification — push is working."}`)
	for _, sub := range subs {
		prune, err := h.pushSender.Send(r.Context(), push.Subscription{
			Endpoint: sub.Endpoint,
			P256dh:   sub.P256dh,
			Auth:     sub.Auth,
		}, payload, push.Options{})
		if prune {
			// Best-effort: a failed prune is not fatal to the request.
			_ = h.queries.DeletePushSubscriptionByEndpoint(r.Context(), sub.Endpoint)
		}
		_ = err // transient send errors are swallowed; the row stays for retry.
	}
	writeJSON(w, http.StatusOK, map[string]int{"sent": len(subs)})
}
