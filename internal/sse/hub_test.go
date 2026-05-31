package sse

import (
	"context"
	"testing"
	"time"
)

// drainOne reads a single frame from a client channel with a timeout so a
// hung hub fails the test loudly instead of blocking forever.
func drainOne(t *testing.T, c *Client) []byte {
	t.Helper()
	select {
	case b, ok := <-c.ch:
		if !ok {
			t.Fatalf("client channel closed unexpectedly")
		}
		return b
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for a frame")
		return nil
	}
}

func TestHub_BroadcastReachesAllClients(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := NewHub()
	go h.Run(ctx)

	a := NewClient(1)
	b := NewClient(2)
	if err := h.Register(a); err != nil {
		t.Fatalf("register a: %v", err)
	}
	if err := h.Register(b); err != nil {
		t.Fatalf("register b: %v", err)
	}

	h.Publish("transactions", "dashboard")

	want := "event: invalidate\ndata: {\"resources\":[\"transactions\",\"dashboard\"]}\n\n"
	if got := string(drainOne(t, a)); got != want {
		t.Errorf("client a frame = %q, want %q", got, want)
	}
	if got := string(drainOne(t, b)); got != want {
		t.Errorf("client b frame = %q, want %q", got, want)
	}
}

func TestHub_SlowConsumerDroppedWithoutBlocking(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := NewHub()
	go h.Run(ctx)

	// slow never drains its channel; fast does.
	slow := NewClient(1)
	fast := NewClient(2)
	if err := h.Register(slow); err != nil {
		t.Fatalf("register slow: %v", err)
	}
	if err := h.Register(fast); err != nil {
		t.Fatalf("register fast: %v", err)
	}

	// Overflow slow's buffer (cap 16) plus the broadcast that triggers the
	// non-blocking drop. fast keeps draining so we can prove the hub never
	// wedged on slow.
	done := make(chan struct{})
	go func() {
		for i := 0; i < clientBufferSize+5; i++ {
			h.Publish("transactions")
			<-fast.ch // fast always drains
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("hub blocked on a slow consumer")
	}

	// slow's channel must eventually be closed (dropped by the hub).
	deadline := time.After(time.Second)
	for {
		select {
		case _, ok := <-slow.ch:
			if !ok {
				return // closed: slow was dropped, test passes
			}
		case <-deadline:
			t.Fatal("slow consumer was never dropped/closed")
		}
	}
}

func TestHub_RunClosesAllClientsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	h := NewHub()
	go h.Run(ctx)

	a := NewClient(1)
	if err := h.Register(a); err != nil {
		t.Fatalf("register a: %v", err)
	}

	cancel()

	select {
	case _, ok := <-a.ch:
		if ok {
			t.Fatal("expected client channel to be closed after Run exits")
		}
	case <-time.After(time.Second):
		t.Fatal("client channel not closed after ctx cancel")
	}
}

func TestHub_PerUserCapReturnsErrorOnSixth(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := NewHub()
	go h.Run(ctx)

	const userID int64 = 7
	for i := 0; i < maxConnsPerUser; i++ {
		if err := h.Register(NewClient(userID)); err != nil {
			t.Fatalf("register %d: unexpected error %v", i, err)
		}
	}
	if err := h.Register(NewClient(userID)); err == nil {
		t.Fatalf("expected error registering connection over the per-user cap of %d", maxConnsPerUser)
	}

	// A different user is unaffected by the first user's cap.
	if err := h.Register(NewClient(99)); err != nil {
		t.Fatalf("register for a different user should succeed, got %v", err)
	}
}

// TestHub_UnregisterAfterShutdownDoesNotBlock pins the graceful-shutdown
// invariant: once Run exits (ctx cancelled, every client channel closed, the
// loop no longer draining its channels), a deferred Unregister from a wakened
// endpoint goroutine must return immediately instead of blocking forever on a
// send nobody reads. A blocking Unregister here would leak the endpoint
// goroutine, stall srv.Shutdown, and crash main on every shutdown that has an
// open SSE connection.
func TestHub_UnregisterAfterShutdownDoesNotBlock(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	h := NewHub()
	go h.Run(ctx)

	a := NewClient(1)
	if err := h.Register(a); err != nil {
		t.Fatalf("register a: %v", err)
	}

	cancel()

	// Wait for Run to exit by observing the client channel close (its drain
	// signal), so we know the loop is no longer reading h.unregister.
	select {
	case _, ok := <-a.ch:
		if ok {
			t.Fatal("expected client channel to be closed after Run exits")
		}
	case <-time.After(time.Second):
		t.Fatal("client channel not closed after ctx cancel")
	}

	done := make(chan struct{})
	go func() {
		h.Unregister(a)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Unregister blocked after hub shutdown")
	}

	// Register after shutdown must also return promptly, with an error, rather
	// than block — the endpoint maps any non-nil Register error to HTTP 503.
	regDone := make(chan error, 1)
	go func() { regDone <- h.Register(NewClient(2)) }()
	select {
	case err := <-regDone:
		if err == nil {
			t.Fatal("expected an error registering against a shut-down hub")
		}
	case <-time.After(time.Second):
		t.Fatal("Register blocked after hub shutdown")
	}
}

// TestHub_UnregisterFreesCapSlot proves cap accounting is decremented on
// Unregister: saturate one user's cap, drop one connection, and a fresh
// Register for that same user must now succeed.
func TestHub_UnregisterFreesCapSlot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := NewHub()
	go h.Run(ctx)

	const userID int64 = 42
	clients := make([]*Client, 0, maxConnsPerUser)
	for i := 0; i < maxConnsPerUser; i++ {
		c := NewClient(userID)
		if err := h.Register(c); err != nil {
			t.Fatalf("register %d: unexpected error %v", i, err)
		}
		clients = append(clients, c)
	}

	// At the cap: a new connection must be rejected.
	if err := h.Register(NewClient(userID)); err == nil {
		t.Fatalf("expected error at the per-user cap of %d", maxConnsPerUser)
	}

	// Free one slot.
	h.Unregister(clients[0])

	// A new connection for the same user must now succeed. Register is
	// synchronous (round-trips through the loop), so the Unregister above is
	// fully applied before this Register is processed.
	if err := h.Register(NewClient(userID)); err != nil {
		t.Fatalf("expected a freed cap slot to allow a new registration, got %v", err)
	}
}

// TestHub_UnregisterIsIdempotent pins that unregistering the same client twice
// is a no-op: the hub must not double-close c.ch (which would panic the loop
// goroutine). Register is synchronous, so each Unregister is fully applied
// before the next call's send is processed.
func TestHub_UnregisterIsIdempotent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := NewHub()
	go h.Run(ctx)

	a := NewClient(1)
	if err := h.Register(a); err != nil {
		t.Fatalf("register a: %v", err)
	}

	h.Unregister(a)
	h.Unregister(a) // second time must be a no-op, not a double-close panic.

	// The channel must be closed exactly once and the loop must still be alive:
	// a subsequent Register round-trips, proving the loop did not panic.
	if _, ok := <-a.ch; ok {
		t.Fatal("expected client channel to be closed after Unregister")
	}
	if err := h.Register(NewClient(2)); err != nil {
		t.Fatalf("hub loop should still be alive after idempotent Unregister, got %v", err)
	}
}
