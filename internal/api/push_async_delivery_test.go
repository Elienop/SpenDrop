package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elienop/spendrop/internal/database"
	"github.com/elienop/spendrop/internal/push"
)

// waitPush drains every background push fan-out h has queued, failing the test
// if delivery has not finished within a generous bound.
//
// Delivery is asynchronous (see deliverInBackground), so ANY assertion on a
// recording dispatcher must be preceded by this call — without it the test is
// racing the delivery goroutine and will pass or fail on scheduling. The
// WaitGroup's Add happens on the emitting goroutine, so a test that has finished
// emitting is guaranteed to see the fan-outs it started.
func waitPush(t *testing.T, h *Handler) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.WaitForPushDelivery(ctx); err != nil {
		t.Fatalf("background push delivery did not finish: %v", err)
	}
}

// blockingSender parks inside Send until the test releases it, modelling a push
// gateway that accepts the connection and never answers — the ordinary case on
// the household's connectivity, and the one that used to hold the HTTP response
// hostage for 30 s per unreachable device.
type blockingSender struct {
	entered chan struct{} // closed the first time Send is called
	release chan struct{} // closed by the test to let every parked Send finish
	once    sync.Once
	mu      sync.Mutex
	sends   int
}

func newBlockingSender() *blockingSender {
	return &blockingSender{entered: make(chan struct{}), release: make(chan struct{})}
}

func (s *blockingSender) Send(ctx context.Context, sub push.Subscription, payload []byte, opts push.Options) (bool, error) {
	s.once.Do(func() { close(s.entered) })
	select {
	case <-s.release:
	case <-ctx.Done():
		return false, ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sends++
	return false, nil
}

func (s *blockingSender) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sends
}

// waitClosed blocks until ch is closed, failing the test with `what` on timeout.
func waitClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal(what)
	}
}

// TestCreateTransaction_ResponseDoesNotWaitOnPushDelivery is the regression
// guard for the production defect: a save committed the row and then contacted
// every subscribed device in sequence, inside the request, with a 30 s per-device
// timeout. One unreachable device meant a 30 s spinner; two exceeded the server's
// 60 s write timeout and the save appeared to FAIL even though the row was
// already committed.
//
// The push gateway here never answers until the test says so. The response must
// arrive anyway, and the notification must still be delivered afterwards.
func TestCreateTransaction_ResponseDoesNotWaitOnPushDelivery(t *testing.T) {
	q, db := setupTestDB(t)
	sender := newBlockingSender()
	h := NewHandler(q, db)
	h.pushTesterForBudgetAlerts = sender

	user := seedTestUser(t, q, "alice", RoleMember)
	other := seedTestUser(t, q, "bob", RoleMember)
	cat := seedExpenseCat(t, q, "Groceries")
	// The actor's own devices are excluded from an activity fan-out, so the
	// unreachable device has to belong to the other household member.
	seedPushSub(t, q, other.ID, "https://push.example/unreachable")
	enableNotif(t, q, func(p *database.UpdateNotificationSettingsParams) { p.TxnAdded = true })

	body := strings.NewReader(`{"date":"2026-05-10","amount":12.34,"description":"milk","category_id":` + itoa(cat.ID) + `}`)
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/transactions", body), user)
	w := httptest.NewRecorder()

	responded := make(chan struct{})
	go func() {
		h.handleCreateTransaction(w, req)
		close(responded)
	}()

	// Wait until the gateway is genuinely mid-send before judging the response.
	// Asserting first would let a fan-out that simply had not started yet look
	// non-blocking, which is the vacuous version of this test.
	waitClosed(t, sender.entered, "push delivery never started")
	waitClosed(t, responded,
		"the save is still waiting on an unreachable push gateway — the response "+
			"must be written without waiting for Web Push delivery")

	if w.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d: %s", w.Code, w.Body.String())
	}

	// The send was still parked when the response landed; releasing it now must
	// complete delivery, so moving the work off the request path did not quietly
	// delete the notification.
	close(sender.release)
	waitPush(t, h)
	if n := sender.count(); n != 1 {
		t.Fatalf("the queued notification must still be delivered; sends = %d, want 1", n)
	}
}

// ctxProbeSender parks until released and then reports whether the context it
// was handed was still live at the moment the send actually happened. A real
// *push.Sender fails the HTTP round-trip outright on a cancelled context, so a
// cancelled probe counts as a lost notification, never a delivered one.
type ctxProbeSender struct {
	entered   chan struct{}
	release   chan struct{}
	once      sync.Once
	mu        sync.Mutex
	delivered int
	cancelled int
}

func newCtxProbeSender() *ctxProbeSender {
	return &ctxProbeSender{entered: make(chan struct{}), release: make(chan struct{})}
}

func (s *ctxProbeSender) Send(ctx context.Context, sub push.Subscription, payload []byte, opts push.Options) (bool, error) {
	s.once.Do(func() { close(s.entered) })
	<-s.release
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		s.cancelled++
		return false, err
	}
	s.delivered++
	return false, nil
}

func (s *ctxProbeSender) counts() (delivered, cancelled int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.delivered, s.cancelled
}

// TestFanOutPush_DeliversAfterRequestContextIsCancelled pins the context
// lifetime of the background delivery.
//
// net/http cancels r.Context() the instant the response is written. Handing that
// context to the delivery goroutine would abort the very sends that were moved
// off the request path — a "fix" that keeps the response fast by silently
// dropping every notification, and one that every other push test would still
// pass under, because they all use an uncancelled context.Background(). Here the
// request context is cancelled by hand at exactly the point net/http cancels it,
// while the send is parked, so the send only completes if delivery detached from
// it.
func TestFanOutPush_DeliversAfterRequestContextIsCancelled(t *testing.T) {
	q, db := setupTestDB(t)
	sender := newCtxProbeSender()
	h := NewHandler(q, db)
	h.pushTesterForBudgetAlerts = sender

	user := seedTestUser(t, q, "alice", RoleMember)
	other := seedTestUser(t, q, "bob", RoleMember)
	cat := seedExpenseCat(t, q, "Groceries")
	seedPushSub(t, q, other.ID, "https://push.example/other")
	enableNotif(t, q, func(p *database.UpdateNotificationSettingsParams) { p.TxnAdded = true })

	reqCtx, cancelRequest := context.WithCancel(context.Background())
	defer cancelRequest()
	body := strings.NewReader(`{"date":"2026-05-10","amount":12.34,"description":"milk","category_id":` + itoa(cat.ID) + `}`)
	req := withUser(
		httptest.NewRequest(http.MethodPost, "/api/transactions", body).WithContext(reqCtx),
		user)
	w := httptest.NewRecorder()

	h.handleCreateTransaction(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d: %s", w.Code, w.Body.String())
	}

	// The send is parked. Cancel the request context the way net/http does once
	// the response is written, THEN let the send proceed.
	waitClosed(t, sender.entered, "push delivery never started")
	cancelRequest()
	close(sender.release)
	waitPush(t, h)

	delivered, cancelled := sender.counts()
	if delivered != 1 || cancelled != 0 {
		t.Fatalf("delivery must outlive the request context: delivered=%d cancelled=%d, want 1 and 0 "+
			"(a cancelled send is a notification the household never receives)", delivered, cancelled)
	}
}

// TestDeliverInBackground_CapsInFlightFanOuts pins the admission cap. Every
// delivery is a goroutine that can live for pushFanOutTimeout, so a burst of
// mutations against an unreachable gateway must degrade into a bounded number of
// dropped notifications rather than unbounded goroutines and memory.
func TestDeliverInBackground_CapsInFlightFanOuts(t *testing.T) {
	q, db := setupTestDB(t)
	sender := newBlockingSender()
	h := NewHandler(q, db)
	h.pushTesterForBudgetAlerts = sender

	user := seedTestUser(t, q, "alice", RoleMember)
	seedPushSub(t, q, user.ID, "https://push.example/parked")

	// over_budget is enabled by default and excludes nobody, so each call owes
	// exactly one send — and every send parks, so nothing frees a slot.
	for i := 0; i < maxInFlightFanOuts+1; i++ {
		h.fanOutPush(context.Background(), "over_budget", []byte(`{"type":"budget_over"}`), 0, pushOpts{})
	}
	if n := h.inFlightDeliveries(); n != maxInFlightFanOuts {
		t.Fatalf("in-flight fan-outs = %d, want the cap %d — the %dth emit must be dropped, not queued",
			n, maxInFlightFanOuts, maxInFlightFanOuts+1)
	}

	close(sender.release)
	waitPush(t, h)
	if got := sender.count(); got != maxInFlightFanOuts {
		t.Fatalf("sends = %d, want %d (one per admitted fan-out)", got, maxInFlightFanOuts)
	}
	if n := h.inFlightDeliveries(); n != 0 {
		t.Fatalf("in-flight count must return to 0 after the drain, got %d", n)
	}
}

// TestWaitForPushDelivery_ReportsOutstandingOnTimeout covers the shutdown path's
// error branch: main.go bounds the drain by the remaining ShutdownGrace budget
// and must be able to tell the operator how many notifications were abandoned.
func TestWaitForPushDelivery_ReportsOutstandingOnTimeout(t *testing.T) {
	q, db := setupTestDB(t)
	sender := newBlockingSender()
	h := NewHandler(q, db)
	h.pushTesterForBudgetAlerts = sender

	user := seedTestUser(t, q, "alice", RoleMember)
	seedPushSub(t, q, user.ID, "https://push.example/parked")

	h.fanOutPush(context.Background(), "over_budget", []byte(`{"type":"budget_over"}`), 0, pushOpts{})
	waitClosed(t, sender.entered, "push delivery never started")

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := h.WaitForPushDelivery(ctx)
	if err == nil {
		t.Fatal("drain must report a timeout while a send is still parked")
	}
	if !strings.Contains(err.Error(), "1 push fan-out(s) still delivering") {
		t.Errorf("error must name the outstanding count, got %q", err.Error())
	}

	// And it returns cleanly once the parked send completes.
	close(sender.release)
	waitPush(t, h)
}

// TestDeliverInBackground_RefusesWhileDraining pins the flag that makes the
// in-flight counter safe to wait on.
//
// A drain parks on a counter reaching zero. If a fan-out could be admitted while
// it is parked, the counter would climb back off zero underneath the waiter —
// the shape that panics a sync.WaitGroup with "Add called concurrently with
// Wait", and that leaves any hand-rolled equivalent waiting on a condition that
// has already passed. Today nothing queues after srv.Shutdown returns, so the
// window is unreachable from production; refusing outright means it stays
// unreachable no matter what the shutdown sequence does later, and a fan-out
// emitted during shutdown is refused with a log line instead of being accepted
// and abandoned moments afterwards.
func TestDeliverInBackground_RefusesWhileDraining(t *testing.T) {
	q, db := setupTestDB(t)
	sender := newBlockingSender()
	h := NewHandler(q, db)
	h.pushTesterForBudgetAlerts = sender

	user := seedTestUser(t, q, "alice", RoleMember)
	seedPushSub(t, q, user.ID, "https://push.example/parked")

	if got := h.fanOutPush(context.Background(), "over_budget", []byte(`{"type":"budget_over"}`), 0, pushOpts{}); got != fanOutQueued {
		t.Fatalf("first emit = %v, want fanOutQueued", got)
	}
	waitClosed(t, sender.entered, "push delivery never started")

	// A drain that times out leaves the handler draining, which is the state a
	// second emit must be refused in. Reaching that state deterministically is
	// why this uses the timeout path rather than a concurrent drain.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := h.WaitForPushDelivery(ctx); err == nil {
		t.Fatal("drain should have timed out with a send still parked")
	}

	if got := h.fanOutPush(context.Background(), "over_budget", []byte(`{"type":"budget_over"}`), 0, pushOpts{}); got != fanOutDropped {
		t.Fatalf("emit during a drain = %v, want fanOutDropped", got)
	}
	if n := h.inFlightDeliveries(); n != 1 {
		t.Fatalf("in-flight = %d, want 1: the emit during the drain must not have been admitted", n)
	}

	close(sender.release)
	waitPush(t, h)
	if got := sender.count(); got != 1 {
		t.Fatalf("sends = %d, want 1 (only the fan-out admitted before the drain)", got)
	}
}

// TestFanOutPush_ReportsGatedWhenTypeDisabled pins the third outcome. A caller
// with a cursor has to tell "refused, retry me" from "nothing was owed", and a
// gate that reported the same value as a drop would stall that caller forever.
func TestFanOutPush_ReportsGatedWhenTypeDisabled(t *testing.T) {
	q, db := setupTestDB(t)
	rec := &recordingSender{}
	h := NewHandler(q, db)
	h.pushTesterForBudgetAlerts = rec

	user := seedTestUser(t, q, "alice", RoleMember)
	seedPushSub(t, q, user.ID, "https://push.example/gated")

	// txn_added defaults OFF (migration 015).
	if got := h.fanOutPush(context.Background(), "txn_added", []byte(`{"type":"txn_added"}`), 0, pushOpts{}); got != fanOutGated {
		t.Fatalf("emit of a disabled type = %v, want fanOutGated", got)
	}
	// And push being switched off entirely is the same answer.
	bare := NewHandler(q, db)
	if got := bare.fanOutPush(context.Background(), "over_budget", []byte(`{}`), 0, pushOpts{}); got != fanOutGated {
		t.Fatalf("emit with push disabled = %v, want fanOutGated", got)
	}
	waitPush(t, h)
	if rec.count() != 0 {
		t.Fatalf("gated emit must not send, got %d", rec.count())
	}
}
