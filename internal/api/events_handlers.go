package api

import (
	"net/http"
	"time"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/sse"
)

// sseHeartbeatInterval is the cadence of the `: ping` comment frames that keep
// idle connections (and intermediary proxies) from timing the stream out.
const sseHeartbeatInterval = 20 * time.Second

// handleEvents is the GET /api/events Server-Sent Events endpoint. It holds the
// request open and streams coarse `invalidate` signals published on the hub to
// the connected browser. No transaction data ever crosses this stream — only
// resource-name hints the client uses to invalidate its TanStack Query cache —
// so the soft-delete / *_cents / DTO disciplines do not apply here.
//
// The endpoint is mounted under RequireAuthOrAPIToken, so by the time this runs
// the caller is authenticated and (for the cookie path) auth.GetUser yields the
// user. We use the user id only to key the hub's per-user connection cap.
func (h *Handler) handleEvents(w http.ResponseWriter, r *http.Request) {
	// SSE response headers. X-Accel-Buffering: no disables proxy buffering
	// (nginx/Caddy) so frames flush to the client immediately.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// Clear the write deadline for THIS connection only. The global 60s
	// http.Server WriteTimeout (config.go HTTPConfig.WriteTimeout) would
	// otherwise tear the long-lived stream down after a minute. Every other
	// route keeps the global timeout. If the platform's ResponseWriter does
	// not support deadline control, fall through — the stream still works, it
	// just inherits the 60s cap and the browser's EventSource reconnects.
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})

	// Per-user connection cap key. A missing user (should not happen behind
	// the auth middleware) is keyed as anonymous id 0 — still capped.
	var userID int64
	if u, ok := auth.GetUser(r); ok {
		userID = u.ID
	}

	client := sse.NewClient(userID)
	if err := h.registerEventClient(client); err != nil {
		// Over the per-user cap (or no broker). 503 tells the browser to back
		// off; EventSource will retry per the retry: hint other connections set.
		writeError(w, http.StatusServiceUnavailable, "too many live connections")
		return
	}
	defer h.unregisterEventClient(client)

	// Tell the browser how long to wait before reconnecting after a drop.
	// Emitted once, at open. The browser owns reconnection from here.
	if _, err := w.Write([]byte("retry: 3000\n\n")); err != nil {
		return
	}
	_ = rc.Flush()

	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			// Client disconnected or server is shutting down.
			return
		case msg, ok := <-client.Events():
			if !ok {
				// Hub closed our channel (slow-consumer drop or shutdown).
				return
			}
			if _, err := w.Write(msg); err != nil {
				return
			}
			_ = rc.Flush()
		case <-heartbeat.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			_ = rc.Flush()
		}
	}
}

// registerEventClient is a nil-safe wrapper around the broker's Register. It
// returns an error when the broker is unset (feature wiring absent) or the
// per-user cap is hit, which handleEvents maps to 503.
func (h *Handler) registerEventClient(c *sse.Client) error {
	if h.broker == nil {
		return errNoEventBroker
	}
	return h.broker.Register(c)
}

// unregisterEventClient is the nil-safe teardown counterpart, called on exit.
func (h *Handler) unregisterEventClient(c *sse.Client) {
	if h.broker == nil {
		return
	}
	h.broker.Unregister(c)
}

// errNoEventBroker is returned by registerEventClient when no hub is wired.
// Kept package-private (the sse package exposes no sentinel); the endpoint
// treats it identically to a real per-user-cap error → 503.
var errNoEventBroker = errSentinel("sse: no event broker configured")

// errSentinel is a tiny string-error type so errNoEventBroker is a constant-ish
// package value without pulling in errors.New at package-init for one string.
type errSentinel string

func (e errSentinel) Error() string { return string(e) }
