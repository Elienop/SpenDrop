package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/config"
	"github.com/elienop/spendrop/internal/database"
	"github.com/elienop/spendrop/internal/push"
	"github.com/elienop/spendrop/internal/ratelimit"
)

// pushRouter wires the public vapid-key route OUTSIDE the auth group and the
// subscribe/unsubscribe/test routes INSIDE it, matching router.go. cfg carries
// the enabled-gated VAPID key the public handler echoes.
func pushRouter(t *testing.T, h *Handler, cfg *config.Config) chi.Router {
	t.Helper()
	ApplyConfig(cfg)
	limiter := ratelimit.NewBucket(30, 10*time.Minute, h.clock)
	r := chi.NewRouter()
	r.Get("/api/push/vapid-public-key", h.handleGetVAPIDPublicKey)
	r.Route("/api", func(r chi.Router) {
		r.Use(auth.RequireAuthOrAPIToken(h.queries, limiter))
		r.Use(requireJSONContentType)
		r.Route("/push", func(r chi.Router) {
			r.Post("/subscriptions", h.handleCreatePushSubscription)
			r.Delete("/subscriptions", h.handleDeletePushSubscription)
			r.Post("/test", h.handlePushTest)
		})
	})
	return r
}

func enabledPushCfg() *config.Config {
	d := config.Defaults()
	d.Push = config.PushConfig{
		Enabled:         true,
		VAPIDPublicKey:  "BNcRdreALRFXTkOOUHK1EtK2wtazFZWxRP9rsB5XF8XlS6KbBJg",
		VAPIDPrivateKey: "on6X5KGB6Xms6Abz0Tdq8h2gXZ5l9Y6Xqg0Z1aB2cd",
		VAPIDSubject:    "mailto:ops@example.com",
	}
	return &d
}

func TestHandleGetVAPIDPublicKey_PublicWhenEnabled(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	// A non-nil sender is required for the feature to be considered enabled.
	cfg := enabledPushCfg()
	h.SetPushSender(push.NewSender(cfg.Push.VAPIDPublicKey, cfg.Push.VAPIDPrivateKey, cfg.Push.VAPIDSubject, http.DefaultClient))
	r := pushRouter(t, h, cfg)

	// No auth header / cookie at all — the route is public.
	req := httptest.NewRequest(http.MethodGet, "/api/push/vapid-public-key", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (public route)", rec.Code)
	}
	var body struct {
		PublicKey string `json:"publicKey"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.PublicKey != "BNcRdreALRFXTkOOUHK1EtK2wtazFZWxRP9rsB5XF8XlS6KbBJg" {
		t.Errorf("publicKey = %q", body.PublicKey)
	}
}

func TestHandleGetVAPIDPublicKey_404WhenDisabled(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	d := config.Defaults() // Push.Enabled=false
	r := pushRouter(t, h, &d)

	req := httptest.NewRequest(http.MethodGet, "/api/push/vapid-public-key", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when push disabled", rec.Code)
	}
}

func TestHandleCreatePushSubscription_401WithoutAuth(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	cfg := enabledPushCfg()
	h.SetPushSender(push.NewSender(cfg.Push.VAPIDPublicKey, cfg.Push.VAPIDPrivateKey, cfg.Push.VAPIDSubject, http.DefaultClient))
	r := pushRouter(t, h, cfg)

	body := []byte(`{"endpoint":"https://push.example/a","keys":{"p256dh":"p","auth":"a"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/push/subscriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 without auth", rec.Code)
	}
}

func TestHandleCreatePushSubscription_StoresUnderCallerNoIDOR(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	cfg := enabledPushCfg()
	h.SetPushSender(push.NewSender(cfg.Push.VAPIDPublicKey, cfg.Push.VAPIDPrivateKey, cfg.Push.VAPIDSubject, http.DefaultClient))
	r := pushRouter(t, h, cfg)
	userA, _, cookieA := seedTokenTestUser(t, h, "alice")
	userB, _, _ := seedTokenTestUser(t, h, "bob")

	// Body carries a forged user_id-equivalent field — the handler must ignore
	// it and store under the authenticated caller (alice), never bob.
	body := []byte(`{"endpoint":"https://push.example/alice","keys":{"p256dh":"p","auth":"a"},"user_id":` +
		itoa(userB.ID) + `}`)
	req := httptest.NewRequest(http.MethodPost, "/api/push/subscriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookieA)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}

	subs, err := h.queries.ListPushSubscriptionsByUser(context.Background(), userA.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(subs) != 1 || subs[0].Endpoint != "https://push.example/alice" {
		t.Fatalf("alice should own exactly 1 subscription, got %+v", subs)
	}
	bobSubs, _ := h.queries.ListPushSubscriptionsByUser(context.Background(), userB.ID)
	if len(bobSubs) != 0 {
		t.Errorf("bob must own 0 subscriptions (no IDOR via body user_id), got %d", len(bobSubs))
	}
}

func TestHandleDeletePushSubscription_UserScopedNoIDOR(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	cfg := enabledPushCfg()
	h.SetPushSender(push.NewSender(cfg.Push.VAPIDPublicKey, cfg.Push.VAPIDPrivateKey, cfg.Push.VAPIDSubject, http.DefaultClient))
	r := pushRouter(t, h, cfg)
	userA, _, _ := seedTokenTestUser(t, h, "alice")
	_, _, cookieB := seedTokenTestUser(t, h, "bob")

	// Alice owns an endpoint.
	const aliceEndpoint = "https://push.example/alice-device"
	if err := h.queries.UpsertPushSubscription(context.Background(), database.UpsertPushSubscriptionParams{
		UserID:   userA.ID,
		Endpoint: aliceEndpoint,
		P256dh:   "p",
		Auth:     "a",
	}); err != nil {
		t.Fatalf("seed alice sub: %v", err)
	}

	// Bob, knowing alice's endpoint out-of-band, tries to delete it.
	body := []byte(`{"endpoint":"` + aliceEndpoint + `"}`)
	req := httptest.NewRequest(http.MethodDelete, "/api/push/subscriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookieB)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	// Idempotent semantics: bob's scoped delete matches no row he owns -> 204.
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (idempotent)", rec.Code)
	}

	// Alice's subscription must still exist — bob could not delete it.
	subs, err := h.queries.ListPushSubscriptionsByUser(context.Background(), userA.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(subs) != 1 {
		t.Errorf("alice's subscription deleted via IDOR: %d rows remain, want 1", len(subs))
	}
}

func TestHandleDeletePushSubscription_DeletesOwnSubscription(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	cfg := enabledPushCfg()
	h.SetPushSender(push.NewSender(cfg.Push.VAPIDPublicKey, cfg.Push.VAPIDPrivateKey, cfg.Push.VAPIDSubject, http.DefaultClient))
	r := pushRouter(t, h, cfg)
	user, _, cookie := seedTokenTestUser(t, h, "owner")

	const endpoint = "https://push.example/owner-device"
	if err := h.queries.UpsertPushSubscription(context.Background(), database.UpsertPushSubscriptionParams{
		UserID:   user.ID,
		Endpoint: endpoint,
		P256dh:   "p",
		Auth:     "a",
	}); err != nil {
		t.Fatalf("seed sub: %v", err)
	}

	body := []byte(`{"endpoint":"` + endpoint + `"}`)
	req := httptest.NewRequest(http.MethodDelete, "/api/push/subscriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}

	subs, _ := h.queries.ListPushSubscriptionsByUser(context.Background(), user.ID)
	if len(subs) != 0 {
		t.Errorf("owner's own subscription not deleted: %d rows remain", len(subs))
	}
}

func TestHandleCreatePushSubscription_RejectsCrossUserReHome(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	cfg := enabledPushCfg()
	h.SetPushSender(push.NewSender(cfg.Push.VAPIDPublicKey, cfg.Push.VAPIDPrivateKey, cfg.Push.VAPIDSubject, http.DefaultClient))
	r := pushRouter(t, h, cfg)
	userA, _, _ := seedTokenTestUser(t, h, "alice")
	_, _, cookieB := seedTokenTestUser(t, h, "bob")

	const endpoint = "https://push.example/shared-endpoint"
	if err := h.queries.UpsertPushSubscription(context.Background(), database.UpsertPushSubscriptionParams{
		UserID:   userA.ID,
		Endpoint: endpoint,
		P256dh:   "p",
		Auth:     "a",
	}); err != nil {
		t.Fatalf("seed alice sub: %v", err)
	}

	// Bob POSTs alice's endpoint — must NOT re-home the row to bob.
	body := []byte(`{"endpoint":"` + endpoint + `","keys":{"p256dh":"p2","auth":"a2"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/push/subscriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookieB)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (endpoint owned by another user)", rec.Code)
	}

	// The row must still belong to alice with her original keys.
	aliceSubs, _ := h.queries.ListPushSubscriptionsByUser(context.Background(), userA.ID)
	if len(aliceSubs) != 1 || aliceSubs[0].P256dh != "p" {
		t.Errorf("alice's subscription was re-homed/mutated: %+v", aliceSubs)
	}
}

func TestHandlePushTest_CountsAttemptBeforeFanOut(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	cfg := enabledPushCfg()
	h.SetPushSender(push.NewSender(cfg.Push.VAPIDPublicKey, cfg.Push.VAPIDPrivateKey, cfg.Push.VAPIDSubject, http.DefaultClient))
	r := pushRouter(t, h, cfg)
	user, _, cookie := seedTokenTestUser(t, h, "tester")

	// No subscriptions seeded: the fan-out loop is empty, so the only way the
	// attempt gets counted is if Consume runs up front (before the fan-out).
	req := httptest.NewRequest(http.MethodPost, "/api/push/test", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	userKey := itoa(user.ID)
	// Exhaust the remaining budget; pushTestLimiter caps at 5/hour. If the first
	// call counted, only 4 more should be permitted before exhaustion.
	permitted := 0
	for i := 0; i < 5; i++ {
		if h.pushTestLimiter.Exhausted(userKey) {
			break
		}
		h.pushTestLimiter.Consume(userKey)
		permitted++
	}
	if permitted != 4 {
		t.Errorf("attempt not counted up front: %d further permits before exhaustion, want 4", permitted)
	}
}

func TestHandleCreatePushSubscription_EnforcesMaxPerUser(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	cfg := enabledPushCfg()
	h.SetPushSender(push.NewSender(cfg.Push.VAPIDPublicKey, cfg.Push.VAPIDPrivateKey, cfg.Push.VAPIDSubject, http.DefaultClient))
	r := pushRouter(t, h, cfg)
	_, _, cookie := seedTokenTestUser(t, h, "cap")

	post := func(endpoint string) int {
		body := []byte(`{"endpoint":"` + endpoint + `","keys":{"p256dh":"p","auth":"a"}}`)
		req := httptest.NewRequest(http.MethodPost, "/api/push/subscriptions", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}
	for i := 0; i < MaxSubscriptionsPerUser; i++ {
		if code := post("https://push.example/cap-" + itoa(int64(i))); code != http.StatusCreated {
			t.Fatalf("subscription %d: status %d, want 201", i, code)
		}
	}
	// One past the cap must be rejected.
	if code := post("https://push.example/cap-overflow"); code != http.StatusTooManyRequests {
		t.Errorf("over-cap status = %d, want 429", code)
	}
}

func TestHandlePushTest_PrunesDeadSubscriptionOn410(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)

	// Stand up a mock push service that always 410s, and point the sender at it.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	defer srv.Close()
	// A REAL VAPID keypair so webpush-go can sign the JWT and reach the mock;
	// the enabledPushCfg fake keys would fail signing before the HTTP round-trip.
	vapidPriv, vapidPub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("generate vapid keys: %v", err)
	}
	cfg := enabledPushCfg()
	h.SetPushSender(push.NewSender(vapidPub, vapidPriv, cfg.Push.VAPIDSubject, srv.Client()))
	r := pushRouter(t, h, cfg)

	user, _, cookie := seedTokenTestUser(t, h, "tester")
	// Seed a subscription whose endpoint hits the 410 mock, with a real-shaped keypair.
	if err := h.queries.UpsertPushSubscription(context.Background(), database.UpsertPushSubscriptionParams{
		UserID: user.ID,
		// A real, valid P-256 public point + 16-byte auth secret (same vector as
		// internal/push/sender_test.go) so webpush-go's ECDH encryption succeeds
		// and the send reaches the 410 mock to exercise the prune path.
		Endpoint: srv.URL,
		P256dh:   "BNNL5ZaTfK81qhXOx23-wewhigUeFb632jN6LvRWCFH1ubQr77FE_9qV1FuojuRmHP42zmf34rXgW80OvUVDgTk",
		Auth:     "zqbxT6JKstKSY9JKibZLSQ",
	}); err != nil {
		t.Fatalf("seed subscription: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/push/test", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	// The 410 must have pruned the dead subscription row.
	subs, _ := h.queries.ListPushSubscriptionsByUser(context.Background(), user.ID)
	if len(subs) != 0 {
		t.Errorf("dead subscription not pruned on 410: %d rows remain", len(subs))
	}
}
