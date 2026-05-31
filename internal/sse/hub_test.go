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
