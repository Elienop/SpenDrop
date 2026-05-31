package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/database"
	"github.com/elienop/spendrop/internal/sse"
)

// requestWithUser injects a database.User with the given id into the request
// context under auth.UserContextKey, mirroring what RequireAuthOrAPIToken does
// before handleEvents runs. handleEvents reads the user only to derive the
// per-user connection-cap key.
func requestWithUser(req *http.Request, userID int64) *http.Request {
	ctx := context.WithValue(req.Context(), auth.UserContextKey, database.User{ID: userID})
	return req.WithContext(ctx)
}

func TestHandleEvents_SetsSSEHeadersAndRetryFrame(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	hub := sse.NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)
	h.SetEventBroker(hub)

	rec := httptest.NewRecorder()
	ctx2, cancel2 := context.WithCancel(context.Background())
	cancel2()
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx2)
	req = requestWithUser(req, 1)
	h.handleEvents(rec, req)

	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", cc)
	}
	if conn := rec.Header().Get("Connection"); conn != "keep-alive" {
		t.Errorf("Connection = %q, want keep-alive", conn)
	}
	if ab := rec.Header().Get("X-Accel-Buffering"); ab != "no" {
		t.Errorf("X-Accel-Buffering = %q, want no", ab)
	}
	if body := rec.Body.String(); !strings.Contains(body, "retry: 3000\n\n") {
		t.Errorf("open body %q missing retry frame", body)
	}
}

func TestHandleEvents_OverCapReturns503(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	hub := sse.NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)
	h.SetEventBroker(hub)

	// Saturate the per-user cap (5) for user id 7 with long-lived streams,
	// then assert the 6th connection for the same user is rejected 503.
	const userID int64 = 7
	for i := 0; i < 5; i++ {
		c := sse.NewClient(userID)
		if err := hub.Register(c); err != nil {
			t.Fatalf("register %d: %v", i, err)
		}
		defer hub.Unregister(c)
	}

	rec := httptest.NewRecorder()
	streamCtx, streamCancel := context.WithCancel(context.Background())
	streamCancel() // canceled so the handler returns even if it got past Register
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(streamCtx)
	req = requestWithUser(req, userID)
	h.handleEvents(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("over-cap status = %d, want 503; body %q", rec.Code, rec.Body.String())
	}
}

func TestHandleEvents_PublishedEventReachesStream(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	hub := sse.NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)
	h.SetEventBroker(hub)

	srv := httptest.NewServer(http.HandlerFunc(h.handleEvents))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET stream: %v", err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}

	// Give the handler a moment to register on the hub before publishing.
	time.Sleep(50 * time.Millisecond)
	hub.Publish("transactions", "dashboard")

	// Drain the stream, accumulating frames until the invalidate frame arrives.
	// The open frame (retry: 3000) is flushed at connect, so a single Read can
	// return just that — we keep reading until the published frame appears or
	// the deadline expires.
	done := make(chan string, 1)
	go func() {
		var acc strings.Builder
		buf := make([]byte, 256)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				acc.Write(buf[:n])
				if strings.Contains(acc.String(), "event: invalidate") {
					done <- acc.String()
					return
				}
			}
			if err != nil {
				done <- acc.String()
				return
			}
		}
	}()
	select {
	case got := <-done:
		if !strings.Contains(got, "event: invalidate") || !strings.Contains(got, `"transactions"`) {
			t.Errorf("stream frame %q missing invalidate/transactions", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for published event")
	}
}
