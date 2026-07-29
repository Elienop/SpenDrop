// internal/push/sender.go
//
// Package push wraps github.com/SherClockHolmes/webpush-go behind a small,
// testable Sender. The rest of the app depends only on this package's types
// (Sender, Subscription) and never imports webpush-go directly, so the
// encryption library is a single swappable seam.
package push

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// defaultHTTPTimeout bounds a single push round-trip. A push gateway that
// accepts the connection but never responds would otherwise block the serial
// fan-out / digest send loop forever, permanently wedging all future digests.
// 30s is generous for a one-shot POST while still guaranteeing the loop makes
// progress. Applied only when the caller's client has no Timeout of its own.
const defaultHTTPTimeout = 30 * time.Second

// defaultTTL is the seconds a push service should retain an undelivered
// message before discarding it. Six hours: long enough to survive a phone
// that is asleep overnight-ish, short enough that a stale budget alert does
// not surface a day later.
const defaultTTL = 6 * 60 * 60

// payloadRecordSlack is added to the payload length to size the HTTP/ECE
// record. webpush-go frames each record as salt(16) + rs(4) + 1 +
// localPublicKey(65) header bytes plus a 1-byte plaintext delimiter, and the
// usable plaintext budget is RecordSize-16. So RecordSize must be at least
// len(payload)+103 or webpush-go returns ErrMaxPadExceeded before the send.
// 128 gives comfortable headroom over that framing floor while still keeping
// the encrypted body small instead of padding to the 4 KB maximum on every
// send.
const payloadRecordSlack = 128

// Subscription is the minimal browser PushSubscription the sender needs: the
// push service endpoint plus the two ECDH/auth secrets. It is intentionally
// decoupled from database.PushSubscription so callers map at the boundary.
type Subscription struct {
	Endpoint string
	P256dh   string
	Auth     string
}

// Sender holds the VAPID identity and a shared *http.Client. Construct one per
// process via NewSender and reuse it — the client's connection pool is the
// whole point of sharing.
type Sender struct {
	publicKey  string
	privateKey string
	subject    string
	client     *http.Client
}

// NewSender builds a Sender. client must be non-nil; pass http.DefaultClient
// (or a tuned client) — the caller owns its lifetime.
//
// If the supplied client has no Timeout, NewSender shallow-copies it and
// applies defaultHTTPTimeout so a stalled gateway cannot wedge the send loop.
// The copy shares the original Transport, so a shared http.DefaultClient keeps
// its connection pool while its own Timeout stays zero for every other caller.
func NewSender(publicKey, privateKey, subject string, client *http.Client) *Sender {
	// Always copy: we install an SSRF-guarded transport and a redirect refusal,
	// and both must be scoped to this sender rather than mutating a client the
	// caller (often http.DefaultClient) shares with the rest of the process.
	c := *client
	if c.Timeout == 0 {
		c.Timeout = defaultHTTPTimeout
	}
	// The endpoint is attacker-influenced: any authenticated household member
	// can register one, and the sender then fetches it repeatedly. Validating
	// the URL at subscribe time cannot hold — DNS resolves at connect time and
	// can change afterwards — so the guarantee is enforced here, at the dial.
	// Wrap only a real network transport. A caller that supplied its own
	// RoundTripper (the tests' stubs) is not talking to the network at all, so
	// there is nothing to guard and replacing it would defeat the seam. The
	// production path — main.go passes http.DefaultClient, whose Transport is
	// nil — always lands in the wrapped branch.
	switch base := c.Transport.(type) {
	case nil:
		c.Transport = GuardedTransport(http.DefaultTransport.(*http.Transport))
	case *http.Transport:
		c.Transport = GuardedTransport(base)
	}
	// Redirect refusal is transport-independent, so it applies unconditionally.
	c.CheckRedirect = refuseRedirects
	client = &c
	return &Sender{
		publicKey:  publicKey,
		privateKey: privateKey,
		subject:    subject,
		client:     client,
	}
}

// Urgency hints to the push service how to trade delivery promptness against the
// device's battery. It is an ALIAS of webpush.Urgency so the api package can set
// push.UrgencyLow / push.UrgencyNormal WITHOUT importing webpush-go — the transport
// library stays confined to this package (single swappable seam).
type Urgency = webpush.Urgency

const (
	UrgencyLow    = webpush.UrgencyLow    // "low": deliver when on power or Wi-Fi (activity)
	UrgencyNormal = webpush.UrgencyNormal // "normal": deliver unless battery is low (over_budget)
)

// Options carries the per-send Web Push transport knobs the caller controls. Topic
// is the server-side collapse key: a later message with the same Topic REPLACES an
// earlier still-undelivered one at the push service (<=32 url-safe chars). An empty
// Urgency is normalised to UrgencyNormal in Send.
type Options struct {
	Topic   string
	Urgency Urgency
}

// Send encrypts payload for sub and POSTs it to the push service. It returns
// prune==true ONLY when the push service reports the subscription is
// permanently gone (HTTP 404 Not Found or 410 Gone) — the caller should then
// delete the stored subscription row. Every other non-2xx (401/403 auth,
// 429 rate limit, 5xx) returns prune==false plus a non-nil error so the caller
// can log-and-retry-later without destroying a still-valid subscription.
//
// The response body is ALWAYS drained and closed so the underlying connection
// can be reused by the shared client — a leaked body pins the connection and
// eventually exhausts the pool under fan-out.
func (s *Sender) Send(ctx context.Context, sub Subscription, payload []byte, opts Options) (prune bool, err error) {
	wsub := &webpush.Subscription{
		Endpoint: sub.Endpoint,
		Keys: webpush.Keys{
			P256dh: sub.P256dh,
			Auth:   sub.Auth,
		},
	}

	// #nosec G115 -- payload length is a small JSON blob, never near uint32 max.
	recordSize := uint32(len(payload) + payloadRecordSlack)

	urgency := opts.Urgency
	if urgency == "" {
		urgency = UrgencyNormal
	}
	resp, err := webpush.SendNotificationWithContext(ctx, payload, wsub, &webpush.Options{
		HTTPClient:      s.client,
		Subscriber:      s.subject,
		VAPIDPublicKey:  s.publicKey,
		VAPIDPrivateKey: s.privateKey,
		TTL:             defaultTTL,
		Topic:           opts.Topic,
		Urgency:         urgency,
		RecordSize:      recordSize,
	})
	if err != nil {
		return false, fmt.Errorf("send push notification: %w", err)
	}
	// Drain + close unconditionally so the connection returns to the pool.
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	switch resp.StatusCode {
	case http.StatusNotFound, http.StatusGone: // 404, 410: subscription is dead.
		return true, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Errorf("push service returned status %d", resp.StatusCode)
	}
	return false, nil
}
