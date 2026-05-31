// Package sse implements an in-memory Server-Sent Events broker (Hub) that
// fans out coarse "invalidate" hints to every connected household device.
//
// The Hub holds NO application state and crosses NO money/row data — events
// are content-free invalidation signals ({"resources":[...]}), so the
// soft-delete / *_cents / DTO disciplines that govern the REST surface never
// apply to this path. Clients re-fetch through the normal authed REST queries
// on receipt.
//
// Concurrency model: a single goroutine (Run) owns the client set and is the
// only mutator, so there is no mutex and no data race. Register / Unregister /
// Publish are safe to call from any goroutine; each marshals its request onto
// a channel the loop drains. A wedged (slow) client never stalls the loop:
// the broadcast send is non-blocking and a full client is dropped + closed,
// to reconnect and re-sync on its own.
package sse

import (
	"context"
	"encoding/json"
	"fmt"
)

// clientBufferSize is the per-client buffered-channel depth. 16 absorbs a
// short burst (e.g. a CSV import that triggers several near-simultaneous
// publishes) without blocking the hub; a consumer that cannot keep up past
// this depth is treated as wedged and dropped.
const clientBufferSize = 16

// maxConnsPerUser caps simultaneous SSE connections per authenticated user.
// Household devices share a NAT IP, so the cap is per-user (not per-IP); 5
// covers phone + laptop + a stray duplicate tab without letting a runaway
// client open unbounded streams. Register returns an error past this cap and
// the handler maps that to 503.
const maxConnsPerUser = 5

// Client is one connected SSE stream. ch is the buffered outbound frame
// queue the endpoint goroutine reads and flushes to the wire. userID scopes
// the per-user connection cap. Construct via NewClient.
type Client struct {
	userID int64
	ch     chan []byte
}

// NewClient builds a Client owned by userID with a buffered outbound channel
// of clientBufferSize. Its channel is closed by the hub when the client is
// unregistered, dropped as a slow consumer, or when Run shuts down.
func NewClient(userID int64) *Client {
	return &Client{
		userID: userID,
		ch:     make(chan []byte, clientBufferSize),
	}
}

// Events returns the receive end of the client's outbound frame queue for the
// endpoint goroutine to range/select over. Reading from a closed channel
// signals the hub has dropped this client.
func (c *Client) Events() <-chan []byte {
	return c.ch
}

// registerReq carries a Register call into the loop along with a reply channel
// for the cap-exceeded error.
type registerReq struct {
	client *Client
	err    chan error
}

// Hub is the in-memory SSE broker. Construct via NewHub and drive with Run.
// All exported methods are goroutine-safe.
type Hub struct {
	register   chan registerReq
	unregister chan *Client
	broadcast  chan []byte
	// done is closed by Run when it exits (via ctx.Done()). After that, Run no
	// longer drains register/unregister/broadcast, so producer-side blocking
	// sends would deadlock. Every such send selects on done as an escape hatch.
	done chan struct{}
}

// NewHub allocates a Hub. The hub does nothing until Run is started on a
// goroutine. The broadcast channel is buffered so Publish stays non-blocking
// under a brief backlog; if even that buffer is full the hint is dropped (the
// next event or a client reconnect heals it).
func NewHub() *Hub {
	return &Hub{
		register:   make(chan registerReq),
		unregister: make(chan *Client),
		broadcast:  make(chan []byte, 64),
		done:       make(chan struct{}),
	}
}

// Run is the hub's single owning goroutine. It owns the client set for its
// whole lifetime and is the only goroutine that mutates it, so no mutex is
// needed. It exits on ctx.Done(), closing every client channel so the
// endpoint goroutines unblock and return (graceful shutdown tears down all
// SSE connections before the HTTP server's Shutdown completes).
func (h *Hub) Run(ctx context.Context) {
	// Signal producers (Register/Unregister) that the loop has stopped draining
	// its channels, whether Run returns via ctx.Done() or any future path.
	defer close(h.done)

	clients := make(map[*Client]struct{})
	perUser := make(map[int64]int)

	for {
		select {
		case req := <-h.register:
			if perUser[req.client.userID] >= maxConnsPerUser {
				req.err <- fmt.Errorf("sse: per-user connection cap of %d reached for user %d", maxConnsPerUser, req.client.userID)
				continue
			}
			clients[req.client] = struct{}{}
			perUser[req.client.userID]++
			req.err <- nil

		case c := <-h.unregister:
			if _, ok := clients[c]; ok {
				delete(clients, c)
				perUser[c.userID]--
				if perUser[c.userID] <= 0 {
					delete(perUser, c.userID)
				}
				close(c.ch)
			}

		case msg := <-h.broadcast:
			for c := range clients {
				// Non-blocking send: a client whose buffer is full is a
				// wedged consumer. Drop and close it rather than stall the
				// whole household's fan-out; it reconnects and re-syncs.
				select {
				case c.ch <- msg:
				default:
					delete(clients, c)
					perUser[c.userID]--
					if perUser[c.userID] <= 0 {
						delete(perUser, c.userID)
					}
					close(c.ch)
				}
			}

		case <-ctx.Done():
			for c := range clients {
				close(c.ch)
			}
			return
		}
	}
}

// Register adds a client to the broadcast set. It blocks until the loop
// processes the request and returns nil on success or a non-nil error when
// the caller's user is already at the per-user connection cap (the endpoint
// maps that error to HTTP 503). Safe to call from any goroutine.
func (h *Hub) Register(c *Client) error {
	req := registerReq{client: c, err: make(chan error, 1)}
	select {
	case h.register <- req:
		return <-req.err
	case <-h.done:
		// Run has exited and closed every client channel; there is no loop to
		// accept the registration. The endpoint maps this to HTTP 503.
		return fmt.Errorf("sse: hub is shut down")
	}
}

// Unregister removes a client from the broadcast set and closes its channel.
// Idempotent: unregistering a client the hub has already dropped is a no-op.
// Safe to call from any goroutine (e.g. the endpoint's deferred cleanup).
func (h *Hub) Unregister(c *Client) {
	select {
	case h.unregister <- c:
	case <-h.done:
		// Run already exited and closed every client channel; nothing to do.
		// Without this escape hatch the deferred cleanup in the SSE endpoint
		// would block forever during graceful shutdown, leaking the goroutine
		// and stalling srv.Shutdown until the grace deadline crashes main.
	}
}

// Publish serializes one SSE "invalidate" frame for the given resource names
// and queues it for broadcast to every connected client. Non-blocking: if the
// broadcast buffer is momentarily full the hint is dropped (best-effort — the
// next event or a reconnect sweep heals it), so Publish never blocks a request
// handler that calls it post-commit.
func (h *Hub) Publish(resources ...string) {
	frame, err := buildFrame(resources)
	if err != nil {
		return // unreachable for a []string, but never panic on a publish path
	}
	select {
	case h.broadcast <- frame:
	default:
	}
}

// buildFrame renders the wire frame:
//
//	event: invalidate\ndata: {"resources":[...]}\n\n
//
// The JSON is marshalled (not hand-built) so resource names are always
// correctly escaped.
func buildFrame(resources []string) ([]byte, error) {
	body, err := json.Marshal(struct {
		Resources []string `json:"resources"`
	}{Resources: resources})
	if err != nil {
		return nil, err
	}
	return []byte(fmt.Sprintf("event: invalidate\ndata: %s\n\n", body)), nil
}
