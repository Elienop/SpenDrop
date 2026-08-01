package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/elienop/spendrop/internal/push"
)

// These tests drive the real HANDLERS, not the truncation helper, because the
// helper was never the thing that was broken.
//
// internal/database.TruncateUserAgent was fixed to cut on characters — the unit
// its column's CHECK(length(actor_user_agent) <= 500) is written in — and it has
// unit tests proving it, in both directions, on multi-byte input. It was still a
// no-op in production: newActorContext byte-cut the header to 500 bytes BEFORE
// handing it over, so the helper only ever saw an already-shortened string. A
// 433-character Arabic agent reached the store as 267 characters and the column,
// which would have taken all 433, still received half.
//
// That is the wiring seam. A control can be correct, and fully covered in
// isolation, while the code that feeds it hands over the weaker variant. Testing
// the helper directly can never see it. Only a request can.

// uaArabicWord is 6 characters / 12 bytes, so byte and character counts diverge
// by exactly 2x and a 500-byte cut lands at 250 characters.
const uaArabicWord = "متصفح"

// arabicUserAgent returns a plausible User-Agent of n characters, prefixed with
// ASCII the way a real one is. The prefix length is deliberately odd so that a
// naive byte cut has to land mid-rune rather than getting lucky on a boundary.
func arabicUserAgent(n int) string {
	// 33 ASCII characters, and ODD on purpose. Arabic is 2 bytes per character,
	// so a 500-byte cut after an EVEN prefix lands exactly on a character
	// boundary and produces valid UTF-8 — a fixture built that way passes
	// whether or not the byte cut is present. An odd prefix forces the cut
	// inside a character. (Verified: this test passed under the byte-cut mutant
	// with a 32-character prefix.)
	const prefix = "Mozilla/5.0 (X11; Linux x86_64)  "
	body := strings.Repeat(uaArabicWord, (n/len([]rune(uaArabicWord)))+2)
	r := []rune(prefix + body)
	return string(r[:n])
}

// TestAuditUserAgent_NonASCIIReachesTheColumnIntact drives POST /api/api-tokens/
// with a 433-character Arabic User-Agent. 433 is under the 500-character column
// bound, so NOTHING should be truncated: the audit row must carry all 433.
func TestAuditUserAgent_NonASCIIReachesTheColumnIntact(t *testing.T) {
	h := setupHandler(t)
	user, _, cookie := seedTokenTestUser(t, h, "ua-alice")

	const wantChars = 433
	ua := arabicUserAgent(wantChars)
	if got := utf8.RuneCountInString(ua); got != wantChars {
		t.Fatalf("fixture is %d characters, want %d", got, wantChars)
	}
	if len(ua) <= 500 {
		t.Fatalf("fixture is only %d bytes; it must EXCEED 500 bytes or a byte cut "+
			"would not fire and this test would pass vacuously", len(ua))
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(map[string]any{"name": "phone"}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/api-tokens/", &buf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", ua)
	req.RemoteAddr = "203.0.113.10:5678"
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	tokenRouter(h).ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var stored string
	if err := h.db.QueryRow(
		`SELECT actor_user_agent FROM api_token_audit WHERE user_id = ? ORDER BY id DESC LIMIT 1`,
		user.ID).Scan(&stored); err != nil {
		t.Fatalf("read audit row: %v", err)
	}

	if !utf8.ValidString(stored) {
		t.Error("stored User-Agent is not valid UTF-8: a cut landed mid-character, " +
			"so this is malformed data at rest, not a display artifact")
	}
	if got := utf8.RuneCountInString(stored); got != wantChars {
		t.Errorf("stored %d characters, want %d.\n"+
			"The column bound is %d CHARACTERS and this agent is under it, so nothing "+
			"should have been cut. A result near %d means something upstream of "+
			"database.TruncateUserAgent is still cutting on BYTES — the helper is "+
			"correct in isolation and is being handed an already-shortened string.",
			got, wantChars, 500, len(ua)/2)
	}
}

// TestPushUserAgent_NonASCIIIsStoredAsValidUTF8 covers the second byte cut.
// push_subscriptions.user_agent has no CHECK constraint, so there is no
// stricter-than-the-column defect here — only the mid-rune half, which writes
// invalid UTF-8 into a TEXT column exactly as the old sanitizeLogValue did.
func TestPushUserAgent_NonASCIIIsStoredAsValidUTF8(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	cfg := enabledPushCfg()
	h.SetPushSender(push.NewSender(cfg.Push.VAPIDPublicKey, cfg.Push.VAPIDPrivateKey, cfg.Push.VAPIDSubject, http.DefaultClient))
	user, _, cookie := seedTokenTestUser(t, h, "ua-push")

	// Long enough that a 500-byte cut fires, and built on an odd-length ASCII
	// prefix so the cut lands INSIDE a 2-byte character rather than on a
	// boundary — see arabicUserAgent.
	ua := arabicUserAgent(600)
	if len(ua) <= 500 {
		t.Fatalf("fixture is %d bytes, must exceed 500", len(ua))
	}

	body := []byte(`{"endpoint":"https://push.example/ua-utf8-check","keys":{"p256dh":"p","auth":"a"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/push/subscriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", ua)
	req.RemoteAddr = "203.0.113.11:5678"
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	pushRouter(t, h, cfg).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("subscribe: got %d: %s", rec.Code, rec.Body.String())
	}

	var stored string
	if err := h.db.QueryRow(
		`SELECT COALESCE(user_agent, '') FROM push_subscriptions WHERE user_id = ? ORDER BY id DESC LIMIT 1`,
		user.ID).Scan(&stored); err != nil {
		t.Fatalf("read subscription: %v", err)
	}
	if stored == "" {
		t.Fatal("no user_agent stored; the fixture never reached the column")
	}
	if !utf8.ValidString(stored) {
		t.Errorf("stored User-Agent is not valid UTF-8 (%d bytes): the cut split a "+
			"character, writing malformed text into the column", len(stored))
	}
}
