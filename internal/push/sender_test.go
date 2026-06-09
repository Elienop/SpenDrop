// internal/push/sender_test.go
package push

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// testKeys generates a throwaway VAPID keypair and a syntactically valid
// browser subscription keypair so webpush.SendNotificationWithContext can
// encrypt+sign without erroring before the HTTP round-trip we want to assert.
func testKeys(t *testing.T) (vapidPub, vapidPriv string) {
	t.Helper()
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("generate vapid keys: %v", err)
	}
	return pub, priv
}

// A real, well-formed browser subscription keypair (raw base64url): a 65-byte
// P-256 public point on the curve (p256dh) and a 16-byte auth secret. These are
// webpush-go's own test vectors, so the library's ECDH encryption succeeds and
// we reach the HTTP round-trip we actually want to assert. The plan's original
// placeholder vector is NOT a valid curve point and fails decodeSubscriptionKey
// before any HTTP call.
const (
	testP256dh = "BNNL5ZaTfK81qhXOx23-wewhigUeFb632jN6LvRWCFH1ubQr77FE_9qV1FuojuRmHP42zmf34rXgW80OvUVDgTk"
	testAuth   = "zqbxT6JKstKSY9JKibZLSQ"
)

func newTestSender(t *testing.T, client *http.Client) *Sender {
	t.Helper()
	pub, priv := testKeys(t)
	return NewSender(pub, priv, "mailto:test@example.com", client)
}

func TestSend_PrunesOn410(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusGone) // 410
	}))
	defer srv.Close()

	s := newTestSender(t, srv.Client())
	sub := Subscription{Endpoint: srv.URL, P256dh: testP256dh, Auth: testAuth}
	prune, err := s.Send(context.Background(), sub, []byte(`{"title":"t"}`), Options{})
	if err != nil {
		t.Fatalf("Send returned error on 410: %v", err)
	}
	if !prune {
		t.Errorf("prune = false on HTTP 410, want true")
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Errorf("server hit %d times, want 1", hits)
	}
}

func TestSend_PrunesOn404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound) // 404
	}))
	defer srv.Close()

	s := newTestSender(t, srv.Client())
	sub := Subscription{Endpoint: srv.URL, P256dh: testP256dh, Auth: testAuth}
	prune, err := s.Send(context.Background(), sub, []byte(`{"title":"t"}`), Options{})
	if err != nil {
		t.Fatalf("Send returned error on 404: %v", err)
	}
	if !prune {
		t.Errorf("prune = false on HTTP 404, want true")
	}
}

func TestSend_KeepsOn401And429(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusTooManyRequests} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
		}))
		s := newTestSender(t, srv.Client())
		sub := Subscription{Endpoint: srv.URL, P256dh: testP256dh, Auth: testAuth}
		prune, err := s.Send(context.Background(), sub, []byte(`{"title":"t"}`), Options{})
		srv.Close()
		if prune {
			t.Errorf("code %d: prune = true, want false (transient/auth error must not delete the row)", code)
		}
		if err == nil {
			t.Errorf("code %d: err = nil, want non-nil so the caller can log the failure", code)
		}
	}
}

func TestSend_DrainsAndClosesBody(t *testing.T) {
	// A RoundTripper that hands back a body whose Close we can observe.
	var closed atomic.Bool
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusCreated,
			Body:       &observableBody{onClose: func() { closed.Store(true) }},
			Header:     make(http.Header),
			Request:    r,
		}, nil
	})
	s := newTestSender(t, &http.Client{Transport: rt})
	sub := Subscription{Endpoint: "https://push.example/x", P256dh: testP256dh, Auth: testAuth}
	if _, err := s.Send(context.Background(), sub, []byte(`{"title":"t"}`), Options{}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !closed.Load() {
		t.Error("response body was not closed")
	}
}

func TestSend_SetsTopicAndUrgencyHeaders(t *testing.T) {
	var gotTopic, gotUrgency string
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotTopic = r.Header.Get("Topic")
		gotUrgency = r.Header.Get("Urgency")
		return &http.Response{
			StatusCode: http.StatusCreated,
			Body:       &observableBody{onClose: func() {}},
			Header:     make(http.Header),
			Request:    r,
		}, nil
	})
	s := newTestSender(t, &http.Client{Transport: rt})
	sub := Subscription{Endpoint: "https://push.example/x", P256dh: testP256dh, Auth: testAuth}
	if _, err := s.Send(context.Background(), sub, []byte(`{"title":"t"}`), Options{Topic: "act", Urgency: UrgencyLow}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotTopic != "act" {
		t.Errorf("Topic header = %q, want act", gotTopic)
	}
	if gotUrgency != "low" {
		t.Errorf("Urgency header = %q, want low", gotUrgency)
	}
}

func TestSend_EmptyUrgencyDefaultsToNormal(t *testing.T) {
	var gotUrgency string
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotUrgency = r.Header.Get("Urgency")
		return &http.Response{
			StatusCode: http.StatusCreated,
			Body:       &observableBody{onClose: func() {}},
			Header:     make(http.Header),
			Request:    r,
		}, nil
	})
	s := newTestSender(t, &http.Client{Transport: rt})
	sub := Subscription{Endpoint: "https://push.example/x", P256dh: testP256dh, Auth: testAuth}
	if _, err := s.Send(context.Background(), sub, []byte(`{"title":"t"}`), Options{}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotUrgency != "normal" {
		t.Errorf("empty Urgency must map to normal, got %q", gotUrgency)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type observableBody struct {
	onClose func()
	read    bool
}

func (b *observableBody) Read(p []byte) (int, error) {
	if b.read {
		return 0, io.EOF
	}
	b.read = true
	n := copy(p, []byte("ok"))
	return n, nil
}
func (b *observableBody) Close() error {
	b.onClose()
	return nil
}
