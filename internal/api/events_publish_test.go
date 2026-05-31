package api

import (
	"context"
	"testing"
	"time"

	"github.com/elienop/spendrop/internal/sse"
)

// TestPublishInvalidate_NoOpWhenBrokerNil proves the best-effort publish
// helper is safe to call on a Handler that was never wired to a broker — the
// 50+ NewHandler-based test sites construct exactly such a Handler, so a nil
// dereference here would crash the whole suite the moment a publish site is
// added to a mutation handler.
func TestPublishInvalidate_NoOpWhenBrokerNil(t *testing.T) {
	h := &Handler{} // broker stays nil
	// Must not panic and must return nothing observable.
	h.publishInvalidate("transactions", "dashboard")
}

// TestPublishInvalidate_DeliversWhenBrokerSet proves a wired broker receives
// the published resources. We register a client on a running hub, publish via
// the Handler helper, and assert the frame arrives.
func TestPublishInvalidate_DeliversWhenBrokerSet(t *testing.T) {
	ctx, cancel := contextWithCancel()
	defer cancel()

	hub := newRunningHub(ctx)
	h := &Handler{}
	h.SetEventBroker(hub)

	c := registerTestClient(t, hub, 1)
	h.publishInvalidate("transactions", "reports")

	want := "event: invalidate\ndata: {\"resources\":[\"transactions\",\"reports\"]}\n\n"
	if got := readFrame(t, c); got != want {
		t.Errorf("frame = %q, want %q", got, want)
	}
}

func contextWithCancel() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}

func newRunningHub(ctx context.Context) *sse.Hub {
	hub := sse.NewHub()
	go hub.Run(ctx)
	return hub
}

func registerTestClient(t *testing.T, hub *sse.Hub, userID int64) *sse.Client {
	t.Helper()
	c := sse.NewClient(userID)
	if err := hub.Register(c); err != nil {
		t.Fatalf("register test client: %v", err)
	}
	return c
}

func readFrame(t *testing.T, c *sse.Client) string {
	t.Helper()
	select {
	case b, ok := <-c.Events():
		if !ok {
			t.Fatalf("client channel closed before a frame arrived")
		}
		return string(b)
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for a published frame")
		return ""
	}
}
