package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elienop/spendrop/internal/database"
)

// Phase 3.3 — Balance checkpoints handler tests.
//
// Shape of the test file:
//
//   1. handleCreateCheckpoint: happy paths per scope, mismatch, validation,
//      unknown category, and the Phase 2.1 tombstone invariant (live rows
//      counted, tombstoned rows excluded from the verification SUM).
//   2. handleListCheckpoints / handleDeleteCheckpoint / handleVerifyCheckpoint:
//      CRUD endpoints with ownership semantics.
//   3. Post-mutation hook integration: a seeded checkpoint's stored status
//      flips from ok→mismatch when a transaction CRUD handler commits and
//      the verifyAffectedCheckpoints hook fires.
//   4. /healthz/data checkpoint-count block: counts are populated and the
//      endpoint stays OK in the happy path (stale mismatches that survive
//      the re-verify sweep would flip 503, but that requires verifier-level
//      error injection and is out of scope here).
//
// All monetary amounts flow through the cents helpers so the verification
// SUM reads the same value every writer produces — seedTestTransaction
// already dual-writes amount_cents alongside the legacy REAL amount.

// --- handleCreateCheckpoint: happy paths ---

func TestHandleCreateCheckpoint_TotalScope_OK(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice_total", "member")
	cat := seedTestCategory(t, q, "Groceries", "expense")

	// Two rows, $50 + $75 = $125 total as of 2026-04-30.
	seedTestTransaction(t, q, user.ID, cat.ID, "2026-04-01", 50.00, "bread")
	seedTestTransaction(t, q, user.ID, cat.ID, "2026-04-02", 75.00, "milk")

	body := `{"scope_type":"total","date":"2026-04-30","expected_amount":125.00,"note":"month-end ledger"}`
	req := httptest.NewRequest(http.MethodPost, "/api/checkpoints", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleCreateCheckpoint(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var resp checkpointResponse
	decodeResponse(t, rec, &resp)
	if resp.ScopeType != "total" {
		t.Errorf("ScopeType=%q, want total", resp.ScopeType)
	}
	if resp.LastVerificationStatus != "ok" {
		t.Errorf("LastVerificationStatus=%q, want ok", resp.LastVerificationStatus)
	}
	if resp.ActualAmount == nil || *resp.ActualAmount != 125.00 {
		t.Errorf("ActualAmount=%v, want 125.00", resp.ActualAmount)
	}
	if resp.ExpectedAmount != 125.00 {
		t.Errorf("ExpectedAmount=%v, want 125.00", resp.ExpectedAmount)
	}
	if resp.Note != "month-end ledger" {
		t.Errorf("Note=%q, want %q", resp.Note, "month-end ledger")
	}
}

func TestHandleCreateCheckpoint_CategoryScope_OK(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice_cat", "member")
	food := seedTestCategory(t, q, "TestFood", "expense")
	rent := seedTestCategory(t, q, "Rent", "expense")

	seedTestTransaction(t, q, user.ID, food.ID, "2026-04-01", 40.00, "a")
	seedTestTransaction(t, q, user.ID, food.ID, "2026-04-05", 60.00, "b")
	// Noise in a different category — must not count toward the food sum.
	seedTestTransaction(t, q, user.ID, rent.ID, "2026-04-01", 1000.00, "rent")

	body := fmt.Sprintf(
		`{"scope_type":"category","scope_id":%d,"date":"2026-04-30","expected_amount":100.00}`,
		food.ID,
	)
	req := httptest.NewRequest(http.MethodPost, "/api/checkpoints", strings.NewReader(body))
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleCreateCheckpoint(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var resp checkpointResponse
	decodeResponse(t, rec, &resp)
	if resp.LastVerificationStatus != "ok" {
		t.Errorf("status=%q, want ok", resp.LastVerificationStatus)
	}
	if resp.ActualAmount == nil || *resp.ActualAmount != 100.00 {
		t.Errorf("ActualAmount=%v, want 100.00 (Rent row must be excluded)", resp.ActualAmount)
	}
	if resp.ScopeID == nil || *resp.ScopeID != food.ID {
		t.Errorf("ScopeID=%v, want %d", resp.ScopeID, food.ID)
	}
}

func TestHandleCreateCheckpoint_TagScope_OK(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice_tag", "member")
	cat := seedTestCategory(t, q, "Misc", "expense")

	// Two rows tagged "vacation", one tagged "work" (noise).
	seedTestTransactionWithTags(t, q, user.ID, cat.ID, "2026-03-01", 200.00, "flights", "vacation,travel")
	seedTestTransactionWithTags(t, q, user.ID, cat.ID, "2026-03-05", 300.00, "hotel", "vacation")
	seedTestTransactionWithTags(t, q, user.ID, cat.ID, "2026-03-10", 50.00, "lunch", "work")

	body := `{"scope_type":"tag","scope_label":"vacation","date":"2026-03-31","expected_amount":500.00}`
	req := httptest.NewRequest(http.MethodPost, "/api/checkpoints", strings.NewReader(body))
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleCreateCheckpoint(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var resp checkpointResponse
	decodeResponse(t, rec, &resp)
	if resp.LastVerificationStatus != "ok" {
		t.Errorf("status=%q, want ok", resp.LastVerificationStatus)
	}
	if resp.ActualAmount == nil || *resp.ActualAmount != 500.00 {
		t.Errorf("ActualAmount=%v, want 500.00 (work row must be excluded)", resp.ActualAmount)
	}
	if resp.ScopeLabel != "vacation" {
		t.Errorf("ScopeLabel=%q, want vacation", resp.ScopeLabel)
	}
}

func TestHandleCreateCheckpoint_Mismatch_Returns201(t *testing.T) {
	// A mismatch is a user-visible *signal*, not a 4xx/5xx. The handler still
	// returns 201, the row still commits, and the response carries
	// status=mismatch alongside the real actual_amount so the UI can render
	// a "your ledger disagrees by $X" banner.
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice_mm", "member")
	cat := seedTestCategory(t, q, "Coffee", "expense")

	seedTestTransaction(t, q, user.ID, cat.ID, "2026-04-01", 10.00, "latte")

	body := `{"scope_type":"total","date":"2026-04-30","expected_amount":99.99}`
	req := httptest.NewRequest(http.MethodPost, "/api/checkpoints", strings.NewReader(body))
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleCreateCheckpoint(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var resp checkpointResponse
	decodeResponse(t, rec, &resp)
	if resp.LastVerificationStatus != "mismatch" {
		t.Errorf("status=%q, want mismatch", resp.LastVerificationStatus)
	}
	if resp.ActualAmount == nil || *resp.ActualAmount != 10.00 {
		t.Errorf("ActualAmount=%v, want 10.00", resp.ActualAmount)
	}
	if resp.ExpectedAmount != 99.99 {
		t.Errorf("ExpectedAmount=%v, want 99.99", resp.ExpectedAmount)
	}
}

func TestHandleCreateCheckpoint_UnknownCategory_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice_unk", "member")

	// scope_id=9999 does not exist — handler should short-circuit into 400
	// rather than let the FK violation bubble up as a 500.
	body := `{"scope_type":"category","scope_id":9999,"date":"2026-04-30","expected_amount":10.00}`
	req := httptest.NewRequest(http.MethodPost, "/api/checkpoints", strings.NewReader(body))
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleCreateCheckpoint(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "unknown category") {
		t.Errorf("body=%q, want 'unknown category'", rec.Body.String())
	}
}

func TestHandleCreateCheckpoint_Unauthorized_Returns401(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	_ = seedTestUser(t, q, "alice_unauth", "member")

	body := `{"scope_type":"total","date":"2026-04-30","expected_amount":10.00}`
	req := httptest.NewRequest(http.MethodPost, "/api/checkpoints", strings.NewReader(body))
	// Deliberately omit withUser: handler should return 401.
	rec := httptest.NewRecorder()
	h.handleCreateCheckpoint(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status=%d, want 401; body=%s", rec.Code, rec.Body.String())
	}
}

// --- handleCreateCheckpoint: validation ---

func TestHandleCreateCheckpoint_Validation(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice_val", "member")

	// Table-driven validation cases. Each row is a JSON body that should
	// produce a 400 — the exact error text is not asserted (the error
	// phrasing is allowed to drift as long as the handler keeps rejecting
	// the request), only the HTTP status.
	cases := []struct {
		name string
		body string
	}{
		{"bad_scope_type", `{"scope_type":"wallet","date":"2026-04-30","expected_amount":10.00}`},
		{"missing_date", `{"scope_type":"total","expected_amount":10.00}`},
		{"malformed_date", `{"scope_type":"total","date":"04/30/2026","expected_amount":10.00}`},
		{"category_without_scope_id", `{"scope_type":"category","date":"2026-04-30","expected_amount":10.00}`},
		{"tag_without_label", `{"scope_type":"tag","date":"2026-04-30","expected_amount":10.00}`},
		{"tag_label_with_comma", `{"scope_type":"tag","scope_label":"food,drink","date":"2026-04-30","expected_amount":10.00}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/checkpoints", strings.NewReader(tc.body))
			req = withUser(req, user)
			rec := httptest.NewRecorder()
			h.handleCreateCheckpoint(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status=%d, want 400; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

// --- Phase 2.1 invariant: tombstoned rows must not leak into verification ---

func TestHandleCreateCheckpoint_HidesTombstoned(t *testing.T) {
	// The sentinel amount 999 is deliberately large so a test that
	// accidentally starts counting tombstoned rows flips the ExpectedAmount
	// assertion dramatically and fails loudly.
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice_tomb", "member")
	cat := seedTestCategory(t, q, "Hardware", "expense")

	// One live row, one tombstoned sentinel.
	seedTestTransaction(t, q, user.ID, cat.ID, "2026-02-01", 30.00, "screwdriver")
	seedTombstonedTestTransaction(t, q, user.ID, cat.ID, "2026-02-02", 999.00, "DELETED")

	// Category scope: expected=30.00. If the verification SUM forgets
	// the deleted_at filter, actual jumps to 1029 and the status flips
	// to mismatch.
	body := fmt.Sprintf(
		`{"scope_type":"category","scope_id":%d,"date":"2026-02-28","expected_amount":30.00}`,
		cat.ID,
	)
	req := httptest.NewRequest(http.MethodPost, "/api/checkpoints", strings.NewReader(body))
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleCreateCheckpoint(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var resp checkpointResponse
	decodeResponse(t, rec, &resp)
	if resp.LastVerificationStatus != "ok" {
		t.Errorf("status=%q, want ok (tombstoned 999 must be excluded)", resp.LastVerificationStatus)
	}
	if resp.ActualAmount == nil || *resp.ActualAmount != 30.00 {
		t.Errorf("ActualAmount=%v, want 30.00 (tombstoned 999 must be excluded)", resp.ActualAmount)
	}
}

// --- Money wire-edge: checkpoint response emits dollars, never *_cents ---

func TestHandleCreateCheckpoint_DoesNotLeakCents(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice_cents", "member")
	cat := seedTestCategory(t, q, "CentsFood", "expense")

	// $125 live total so the create's inline verify populates actual_amount.
	seedTestTransaction(t, q, user.ID, cat.ID, "2026-04-01", 50.00, "a")
	seedTestTransaction(t, q, user.ID, cat.ID, "2026-04-02", 75.00, "b")

	body := `{"scope_type":"total","date":"2026-04-30","expected_amount":125.00}`
	req := httptest.NewRequest(http.MethodPost, "/api/checkpoints", strings.NewReader(body))
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleCreateCheckpoint(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Raw map decode: a typed checkpointResponse decode zero-fills and would
	// hide a *_cents leak. Money Wire-Edge DTO discipline mandates this shape.
	var raw map[string]any
	if err := json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&raw); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	if ea, ok := raw["expected_amount"].(float64); !ok || ea != 125.00 {
		t.Errorf("expected_amount=%v (ok=%v), want 125.00", raw["expected_amount"], ok)
	}
	if aa, ok := raw["actual_amount"].(float64); !ok || aa != 125.00 {
		t.Errorf("actual_amount=%v (ok=%v), want 125.00", raw["actual_amount"], ok)
	}
	if _, leaked := raw["expected_amount_cents"]; leaked {
		t.Error("expected_amount_cents must NOT leak — frontend reads expected_amount (dollars)")
	}
	if _, leaked := raw["actual_amount_cents"]; leaked {
		t.Error("actual_amount_cents must NOT leak — frontend reads actual_amount (dollars)")
	}
}

// --- handleListCheckpoints ---

func TestHandleListCheckpoints_ReturnsHouseholdWide(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	alice := seedTestUser(t, q, "alice_list", "member")
	bob := seedTestUser(t, q, "bob_list", "member")

	// Two rows by different users — list must return both.
	seedCheckpoint(t, q, alice.ID, "total", 0, "", "2026-04-30", 10000)
	seedCheckpoint(t, q, bob.ID, "total", 0, "", "2026-05-31", 20000)

	req := httptest.NewRequest(http.MethodGet, "/api/checkpoints", nil)
	req = withUser(req, alice)
	rec := httptest.NewRecorder()
	h.handleListCheckpoints(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Checkpoints []checkpointResponse `json:"checkpoints"`
	}
	decodeResponse(t, rec, &resp)
	if len(resp.Checkpoints) != 2 {
		t.Fatalf("got %d checkpoints, want 2; body=%s", len(resp.Checkpoints), rec.Body.String())
	}
	// Ordering is date DESC, id DESC per ListCheckpoints — bob's 2026-05-31
	// row should come first.
	if resp.Checkpoints[0].UserID != bob.ID {
		t.Errorf("first checkpoint UserID=%d, want %d", resp.Checkpoints[0].UserID, bob.ID)
	}
	if resp.Checkpoints[1].UserID != alice.ID {
		t.Errorf("second checkpoint UserID=%d, want %d", resp.Checkpoints[1].UserID, alice.ID)
	}
}

// --- handleDeleteCheckpoint ---

func TestHandleDeleteCheckpoint_Owner_OK(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	alice := seedTestUser(t, q, "alice_del", "member")
	cp := seedCheckpoint(t, q, alice.ID, "total", 0, "", "2026-04-30", 5000)

	req := httptest.NewRequest(http.MethodDelete, "/api/checkpoints/"+fmt.Sprint(cp.ID), nil)
	req = withUserAndURLParam(req, alice, "id", fmt.Sprint(cp.ID))
	rec := httptest.NewRecorder()
	h.handleDeleteCheckpoint(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if _, err := q.GetCheckpoint(context.Background(), cp.ID); err != sql.ErrNoRows {
		t.Errorf("GetCheckpoint after delete err=%v, want sql.ErrNoRows", err)
	}
}

func TestHandleDeleteCheckpoint_AdminDeletesOther_OK(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	alice := seedTestUser(t, q, "alice_deladmin", "member")
	admin := seedTestUser(t, q, "admin_deladmin", RoleAdmin)
	cp := seedCheckpoint(t, q, alice.ID, "total", 0, "", "2026-04-30", 5000)

	req := httptest.NewRequest(http.MethodDelete, "/api/checkpoints/"+fmt.Sprint(cp.ID), nil)
	req = withUserAndURLParam(req, admin, "id", fmt.Sprint(cp.ID))
	rec := httptest.NewRecorder()
	h.handleDeleteCheckpoint(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleDeleteCheckpoint_NonOwnerMember_Forbidden(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	alice := seedTestUser(t, q, "alice_delowner", "member")
	bob := seedTestUser(t, q, "bob_delowner", "member")
	cp := seedCheckpoint(t, q, alice.ID, "total", 0, "", "2026-04-30", 5000)

	req := httptest.NewRequest(http.MethodDelete, "/api/checkpoints/"+fmt.Sprint(cp.ID), nil)
	req = withUserAndURLParam(req, bob, "id", fmt.Sprint(cp.ID))
	rec := httptest.NewRecorder()
	h.handleDeleteCheckpoint(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status=%d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if _, err := q.GetCheckpoint(context.Background(), cp.ID); err != nil {
		t.Errorf("checkpoint should still exist, got err=%v", err)
	}
}

func TestHandleDeleteCheckpoint_NotFound_Returns404(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice_del404", "member")

	req := httptest.NewRequest(http.MethodDelete, "/api/checkpoints/999999", nil)
	req = withUserAndURLParam(req, user, "id", "999999")
	rec := httptest.NewRecorder()
	h.handleDeleteCheckpoint(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status=%d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

// --- handleVerifyCheckpoint ---

func TestHandleVerifyCheckpoint_Manual_FlipsStatus(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice_verify", "member")
	cat := seedTestCategory(t, q, "Books", "expense")

	// Seed a checkpoint that currently mismatches (no transactions at all,
	// expected=25). handleVerifyCheckpoint should compute actual=0, write
	// status=mismatch, and surface 25 vs 0 to the caller.
	cp := seedCheckpoint(t, q, user.ID, "category", cat.ID, "", "2026-06-30", 2500)

	req := httptest.NewRequest(http.MethodPost, "/api/checkpoints/"+fmt.Sprint(cp.ID)+"/verify", nil)
	req = withUserAndURLParam(req, user, "id", fmt.Sprint(cp.ID))
	rec := httptest.NewRecorder()
	h.handleVerifyCheckpoint(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp checkpointResponse
	decodeResponse(t, rec, &resp)
	if resp.LastVerificationStatus != "mismatch" {
		t.Errorf("status=%q, want mismatch", resp.LastVerificationStatus)
	}
	if resp.ActualAmount == nil || *resp.ActualAmount != 0.00 {
		t.Errorf("ActualAmount=%v, want 0.00", resp.ActualAmount)
	}

	// Now insert a matching transaction and re-verify — status should flip
	// to ok and actual should update to 25.00.
	seedTestTransaction(t, q, user.ID, cat.ID, "2026-06-01", 25.00, "novel")

	req2 := httptest.NewRequest(http.MethodPost, "/api/checkpoints/"+fmt.Sprint(cp.ID)+"/verify", nil)
	req2 = withUserAndURLParam(req2, user, "id", fmt.Sprint(cp.ID))
	rec2 := httptest.NewRecorder()
	h.handleVerifyCheckpoint(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("second verify status=%d, want 200; body=%s", rec2.Code, rec2.Body.String())
	}
	var resp2 checkpointResponse
	decodeResponse(t, rec2, &resp2)
	if resp2.LastVerificationStatus != "ok" {
		t.Errorf("second verify status=%q, want ok", resp2.LastVerificationStatus)
	}
	if resp2.ActualAmount == nil || *resp2.ActualAmount != 25.00 {
		t.Errorf("second verify ActualAmount=%v, want 25.00", resp2.ActualAmount)
	}
}

func TestHandleVerifyCheckpoint_NotFound_Returns404(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice_v404", "member")

	req := httptest.NewRequest(http.MethodPost, "/api/checkpoints/999999/verify", nil)
	req = withUserAndURLParam(req, user, "id", "999999")
	rec := httptest.NewRecorder()
	h.handleVerifyCheckpoint(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status=%d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

// --- Post-mutation hook integration ---
//
// These tests exercise the Phase 3.3 contract that every transaction CRUD
// path fires verifyAffectedCheckpoints after its commit, causing the stored
// last_verification_status of every affected checkpoint to flip. Each test
// seeds an already-ok checkpoint, runs a CRUD handler, and reads the
// checkpoint back to observe the flip.

func TestHandleCreateTransaction_FiresCheckpointHook(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice_hookcreate", "member")
	cat := seedTestCategory(t, q, "HookFood", "expense")

	// Seed an initial transaction and a checkpoint that currently matches.
	seedTestTransaction(t, q, user.ID, cat.ID, "2026-04-01", 50.00, "prev")
	cp := seedCheckpoint(t, q, user.ID, "total", 0, "", "2026-04-30", 5000)
	// Force the stored status to 'ok' as if an earlier verify had succeeded.
	mustUpdateCheckpointStatus(t, db, cp.ID, "ok")

	// POST a new transaction for $20 via the handler. This should push
	// actual up to $70, causing the checkpoint to flip to mismatch.
	body := fmt.Sprintf(
		`{"date":"2026-04-15","amount":20.00,"description":"new","category_id":%d}`,
		cat.ID,
	)
	req := httptest.NewRequest(http.MethodPost, "/api/transactions", strings.NewReader(body))
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleCreateTransaction(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("create status=%d, want 201; body=%s", rec.Code, rec.Body.String())
	}

	got, err := q.GetCheckpoint(context.Background(), cp.ID)
	if err != nil {
		t.Fatalf("GetCheckpoint: %v", err)
	}
	if !got.LastVerificationStatus.Valid || got.LastVerificationStatus.String != "mismatch" {
		t.Errorf("stored status=%v, want mismatch", got.LastVerificationStatus)
	}
}

func TestHandleUpdateTransaction_FiresCheckpointHook(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice_hookupd", "member")
	cat := seedTestCategory(t, q, "UpdFood", "expense")

	txn := seedTestTransaction(t, q, user.ID, cat.ID, "2026-04-01", 50.00, "orig")
	cp := seedCheckpoint(t, q, user.ID, "total", 0, "", "2026-04-30", 5000)
	mustUpdateCheckpointStatus(t, db, cp.ID, "ok")

	// PUT the transaction with a new amount ($75) — checkpoint should flip.
	body := fmt.Sprintf(
		`{"date":"2026-04-01","amount":75.00,"description":"orig","category_id":%d}`,
		cat.ID,
	)
	req := httptest.NewRequest(http.MethodPut, "/api/transactions/"+fmt.Sprint(txn.ID), strings.NewReader(body))
	req = withUserAndURLParam(req, user, "id", fmt.Sprint(txn.ID))
	rec := httptest.NewRecorder()
	h.handleUpdateTransaction(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("update status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	got, err := q.GetCheckpoint(context.Background(), cp.ID)
	if err != nil {
		t.Fatalf("GetCheckpoint: %v", err)
	}
	if !got.LastVerificationStatus.Valid || got.LastVerificationStatus.String != "mismatch" {
		t.Errorf("stored status=%v, want mismatch", got.LastVerificationStatus)
	}
}

// TestHandleBatchUpdate_CategoryOnlyPatch_FiresCheckpointHook covers the gap
// that the batch-update checkpoint hook used to gate solely on
// `patch.Date != nil`. balance_checkpoints.scope_type is total | category |
// tag, and VerifyCheckpointTotal filters on t.category_id for a category
// scope — so a category-only patch (the single most common bulk edit) moves
// a category-scoped checkpoint's real total while leaving every date intact.
// Under the old gate the hook never fired and the checkpoint kept reporting
// 'ok' while its actual sum had gone to zero, including in /healthz/data.
func TestHandleBatchUpdate_CategoryOnlyPatch_FiresCheckpointHook(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice_bhookcat", "member")
	catA := seedTestCategory(t, q, "HookCatA", "expense")
	catB := seedTestCategory(t, q, "HookCatB", "expense")

	txn := seedTestTransaction(t, q, user.ID, catA.ID, "2026-04-01", 50.00, "moves away")
	// Category-scoped checkpoint on catA that currently matches ($50).
	cp := seedCheckpoint(t, q, user.ID, "category", catA.ID, "", "2026-04-30", 5000)
	mustUpdateCheckpointStatus(t, db, cp.ID, "ok")

	body := fmt.Sprintf(`{"ids":[%d],"patch":{"category_id":%d}}`, txn.ID, catB.ID)
	rec := postBatchUpdate(t, h, user, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("batch update status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	got, err := q.GetCheckpoint(context.Background(), cp.ID)
	if err != nil {
		t.Fatalf("GetCheckpoint: %v", err)
	}
	if !got.LastVerificationStatus.Valid || got.LastVerificationStatus.String != "mismatch" {
		t.Errorf("stored status=%v, want mismatch (catA's real total is now 0)", got.LastVerificationStatus)
	}
}

// TestHandleBatchUpdate_TagsOnlyPatch_FiresCheckpointHook is the tag-scope
// twin of the category test: VerifyCheckpointTotal matches a tag scope with
// a LIKE over t.tags, so replacing a row's tags moves a tag-scoped
// checkpoint's total with no date change.
func TestHandleBatchUpdate_TagsOnlyPatch_FiresCheckpointHook(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice_bhooktag", "member")
	cat := seedTestCategory(t, q, "HookTagCat", "expense")

	txn := seedTestTransactionWithTags(t, q, user.ID, cat.ID, "2026-04-01", 50.00, "tagged", "holiday")
	cp := seedCheckpoint(t, q, user.ID, "tag", 0, "holiday", "2026-04-30", 5000)
	mustUpdateCheckpointStatus(t, db, cp.ID, "ok")

	body := fmt.Sprintf(`{"ids":[%d],"patch":{"tags":"everyday"},"tagsMode":"replace"}`, txn.ID)
	rec := postBatchUpdate(t, h, user, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("batch update status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	got, err := q.GetCheckpoint(context.Background(), cp.ID)
	if err != nil {
		t.Fatalf("GetCheckpoint: %v", err)
	}
	if !got.LastVerificationStatus.Valid || got.LastVerificationStatus.String != "mismatch" {
		t.Errorf("stored status=%v, want mismatch (no row carries the 'holiday' tag now)", got.LastVerificationStatus)
	}
}

// TestHandleBatchUpdate_DescriptionOnlyPatch_SkipsCheckpointHook pins the
// NEGATIVE half of the widened gate: description is not a checkpoint scope,
// so a description-only patch must not trigger a verification walk.
//
// The assertion is inverted to stay discriminating: the checkpoint is seeded
// so that a verification WOULD flip it (stored 'mismatch', real total
// matches). If the hook fired it would correct the row to 'ok'; the test
// asserts it stays 'mismatch', proving no walk ran.
func TestHandleBatchUpdate_DescriptionOnlyPatch_SkipsCheckpointHook(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice_bhookdesc", "member")
	cat := seedTestCategory(t, q, "HookDescCat", "expense")

	txn := seedTestTransaction(t, q, user.ID, cat.ID, "2026-04-01", 50.00, "before")
	cp := seedCheckpoint(t, q, user.ID, "total", 0, "", "2026-04-30", 5000)
	mustUpdateCheckpointStatus(t, db, cp.ID, "mismatch")

	body := fmt.Sprintf(`{"ids":[%d],"patch":{"description":"after"}}`, txn.ID)
	rec := postBatchUpdate(t, h, user, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("batch update status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	// Sanity: the patch really landed, so a no-op isn't why the hook stayed quiet.
	var gotDesc string
	if err := db.QueryRow(`SELECT description FROM transactions WHERE id = ?`, txn.ID).Scan(&gotDesc); err != nil {
		t.Fatal(err)
	}
	if gotDesc != "after" {
		t.Fatalf("description = %q, want %q — patch did not land", gotDesc, "after")
	}

	got, err := q.GetCheckpoint(context.Background(), cp.ID)
	if err != nil {
		t.Fatalf("GetCheckpoint: %v", err)
	}
	if !got.LastVerificationStatus.Valid || got.LastVerificationStatus.String != "mismatch" {
		t.Errorf("stored status=%v, want it left at mismatch — a description-only patch must not trigger a checkpoint walk", got.LastVerificationStatus)
	}
}

// TestHandleUpdateByFilter_CategoryOnlyPatch_FiresCheckpointHook is the
// filter-mode twin: handleUpdateTransactionsByFilter carried the identical
// `patch.Date != nil` gate. Its no-tags fast path has no per-row dates, so
// the hook runs with a zero time.Time ("reverify every checkpoint").
func TestHandleUpdateByFilter_CategoryOnlyPatch_FiresCheckpointHook(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice_fhookcat", "member")
	catA := seedTestCategory(t, q, "FHookCatA", "expense")
	catB := seedTestCategory(t, q, "FHookCatB", "expense")

	seedTestTransaction(t, q, user.ID, catA.ID, "2026-04-01", 50.00, "moves away")
	cp := seedCheckpoint(t, q, user.ID, "category", catA.ID, "", "2026-04-30", 5000)
	mustUpdateCheckpointStatus(t, db, cp.ID, "ok")

	body := fmt.Sprintf(`{"patch":{"category_id":%d}}`, catB.ID)
	rec := postUpdateByFilter(t, h, user, fmt.Sprintf("category_id=%d", catA.ID), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("update-by-filter status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	got, err := q.GetCheckpoint(context.Background(), cp.ID)
	if err != nil {
		t.Fatalf("GetCheckpoint: %v", err)
	}
	if !got.LastVerificationStatus.Valid || got.LastVerificationStatus.String != "mismatch" {
		t.Errorf("stored status=%v, want mismatch (catA's real total is now 0)", got.LastVerificationStatus)
	}
}

// TestHandleUpdateByFilter_TagsOnlyPatch_FiresCheckpointHook covers the one
// gate cell with a quirk: filter-mode's tags branch is a read-then-write loop,
// so unlike the no-tags fast path it CAN supply a real per-row date floor. It
// used to accumulate minDate only for date patches, which meant a tags-only
// filter edit fired the hook with a zero time and conservatively reverified
// every checkpoint in the table.
func TestHandleUpdateByFilter_TagsOnlyPatch_FiresCheckpointHook(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice_fhooktag", "member")
	cat := seedTestCategory(t, q, "FHookTagCat", "expense")

	seedTestTransactionWithTags(t, q, user.ID, cat.ID, "2026-04-01", 50.00, "tagged", "holiday")
	cp := seedCheckpoint(t, q, user.ID, "tag", 0, "holiday", "2026-04-30", 5000)
	mustUpdateCheckpointStatus(t, db, cp.ID, "ok")

	// A checkpoint dated BEFORE every touched row cannot be affected by this
	// edit, so the hook must not walk it — which only holds if the tags branch
	// hands back a real date floor instead of the zero-time "reverify all"
	// fallback.
	//
	// The fixture has to be one a walk would CHANGE, or the assertion proves
	// nothing: expected $99.99 against a real total of $0 (the only row is
	// dated after this checkpoint), stored as 'ok'. Re-verifying it flips it
	// to 'mismatch'; skipping it leaves 'ok'.
	early := seedCheckpoint(t, q, user.ID, "total", 0, "", "2026-01-31", 9999)
	mustUpdateCheckpointStatus(t, db, early.ID, "ok")

	body := `{"patch":{"tags":"everyday"},"tagsMode":"replace"}`
	rec := postUpdateByFilter(t, h, user, fmt.Sprintf("category_id=%d", cat.ID), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("update-by-filter status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	got, err := q.GetCheckpoint(context.Background(), cp.ID)
	if err != nil {
		t.Fatalf("GetCheckpoint: %v", err)
	}
	if !got.LastVerificationStatus.Valid || got.LastVerificationStatus.String != "mismatch" {
		t.Errorf("stored status=%v, want mismatch (no row carries the 'holiday' tag now)", got.LastVerificationStatus)
	}

	gotEarly, err := q.GetCheckpoint(context.Background(), early.ID)
	if err != nil {
		t.Fatalf("GetCheckpoint(early): %v", err)
	}
	if !gotEarly.LastVerificationStatus.Valid || gotEarly.LastVerificationStatus.String != "ok" {
		t.Errorf("checkpoint dated before every touched row was reverified (status=%v, want ok) — the tags branch lost its date floor and fell back to walking every checkpoint", gotEarly.LastVerificationStatus)
	}
}

// TestHandleUpdateByFilter_DescriptionOnlyPatch_SkipsCheckpointHook is the
// filter-mode negative case, inverted the same way as its batch-update twin:
// the checkpoint is stored 'mismatch' while its real total matches, so a
// verification walk would correct it to 'ok'. It must stay 'mismatch'.
func TestHandleUpdateByFilter_DescriptionOnlyPatch_SkipsCheckpointHook(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice_fhookdesc", "member")
	cat := seedTestCategory(t, q, "FHookDescCat", "expense")

	txn := seedTestTransaction(t, q, user.ID, cat.ID, "2026-04-01", 50.00, "before")
	cp := seedCheckpoint(t, q, user.ID, "total", 0, "", "2026-04-30", 5000)
	mustUpdateCheckpointStatus(t, db, cp.ID, "mismatch")

	body := `{"patch":{"description":"after"}}`
	rec := postUpdateByFilter(t, h, user, fmt.Sprintf("category_id=%d", cat.ID), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("update-by-filter status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var gotDesc string
	if err := db.QueryRow(`SELECT description FROM transactions WHERE id = ?`, txn.ID).Scan(&gotDesc); err != nil {
		t.Fatal(err)
	}
	if gotDesc != "after" {
		t.Fatalf("description = %q, want %q — patch did not land", gotDesc, "after")
	}

	got, err := q.GetCheckpoint(context.Background(), cp.ID)
	if err != nil {
		t.Fatalf("GetCheckpoint: %v", err)
	}
	if !got.LastVerificationStatus.Valid || got.LastVerificationStatus.String != "mismatch" {
		t.Errorf("stored status=%v, want it left at mismatch — a description-only filter patch must not trigger a checkpoint walk", got.LastVerificationStatus)
	}
}

func TestHandleDeleteTransaction_FiresCheckpointHook(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice_hookdel", "member")
	cat := seedTestCategory(t, q, "DelFood", "expense")

	txn := seedTestTransaction(t, q, user.ID, cat.ID, "2026-04-01", 50.00, "will delete")
	cp := seedCheckpoint(t, q, user.ID, "total", 0, "", "2026-04-30", 5000)
	mustUpdateCheckpointStatus(t, db, cp.ID, "ok")

	req := httptest.NewRequest(http.MethodDelete, "/api/transactions/"+fmt.Sprint(txn.ID), nil)
	req = withUserAndURLParam(req, user, "id", fmt.Sprint(txn.ID))
	rec := httptest.NewRecorder()
	h.handleDeleteTransaction(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("delete status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	got, err := q.GetCheckpoint(context.Background(), cp.ID)
	if err != nil {
		t.Fatalf("GetCheckpoint: %v", err)
	}
	// After delete, live SUM is 0 but expected is 5000 cents → mismatch.
	if !got.LastVerificationStatus.Valid || got.LastVerificationStatus.String != "mismatch" {
		t.Errorf("stored status=%v, want mismatch", got.LastVerificationStatus)
	}
}

// --- /healthz/data checkpoint-count block ---

func TestHandleHealthzData_ReportsCheckpointCounts(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice_hzck", "member")
	cat := seedTestCategory(t, q, "HzFood", "expense")

	// One $100 transaction, then three checkpoints whose post-sweep
	// statuses are deterministic:
	//   cpOK1: total scope, expected=100 → matches → ok
	//   cpOK2: category scope, expected=100 → matches → ok
	//   cpMM:  total scope, expected=50 → mismatches → mismatch
	// After the stale sweep, last_verified_at on every row is fresh, so
	// CountStaleMismatchCheckpoints returns 0 and the status stays "ok"
	// even with the mismatch row present — per the phase plan a user-level
	// mismatch is not a system-degradation signal.
	seedTestTransaction(t, q, user.ID, cat.ID, "2026-04-01", 100.00, "a")
	seedCheckpoint(t, q, user.ID, "total", 0, "", "2026-04-30", 10000)
	seedCheckpoint(t, q, user.ID, "category", cat.ID, "", "2026-04-30", 10000)
	seedCheckpoint(t, q, user.ID, "total", 0, "", "2026-04-30", 5000)

	// Seed the cached integrity result so the "never ran" state does not
	// ambiguate the final status assertion.
	h.SetIntegrityResult(time.Now().UTC(), database.IntegrityOK)

	req := httptest.NewRequest(http.MethodGet, "/healthz/data", nil)
	rec := httptest.NewRecorder()
	h.handleHealthzData(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp healthzDataResponse
	decodeResponse(t, rec, &resp)
	if resp.Status != "ok" {
		t.Errorf("Status=%q, want ok (user-level mismatch must not flip the endpoint)", resp.Status)
	}
	if resp.Checkpoints.OK != 2 {
		t.Errorf("Checkpoints.OK=%d, want 2", resp.Checkpoints.OK)
	}
	if resp.Checkpoints.Mismatch != 1 {
		t.Errorf("Checkpoints.Mismatch=%d, want 1", resp.Checkpoints.Mismatch)
	}
	if resp.Checkpoints.Pending != 0 {
		t.Errorf("Checkpoints.Pending=%d, want 0 (sweep re-verifies every row)", resp.Checkpoints.Pending)
	}
}

func TestHandleHealthzData_EmitsCheckpointCountsFieldEvenWhenZero(t *testing.T) {
	// The Checkpoints field is deliberately NOT tagged omitempty so
	// monitoring dashboards can alert on "counts dropped to zero overnight"
	// without special-casing an absent field. Verify the empty case
	// produces the all-zero nested object in the JSON.
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	h.SetIntegrityResult(time.Now().UTC(), database.IntegrityOK)

	req := httptest.NewRequest(http.MethodGet, "/healthz/data", nil)
	rec := httptest.NewRecorder()
	h.handleHealthzData(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var raw map[string]any
	if err := json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&raw); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	cp, ok := raw["checkpoints"].(map[string]any)
	if !ok {
		t.Fatalf("checkpoints field missing or wrong type: %v", raw["checkpoints"])
	}
	for _, key := range []string{"ok", "mismatch", "pending"} {
		v, present := cp[key]
		if !present {
			t.Errorf("checkpoints.%s missing", key)
			continue
		}
		if f, _ := v.(float64); f != 0 {
			t.Errorf("checkpoints.%s=%v, want 0", key, v)
		}
	}
}

// --- test helpers local to this file ---

// seedCheckpoint inserts a checkpoint directly via the sqlc helper, bypassing
// the HTTP handler and its inline verify step. Used by the tests that need
// to start from a known (id, status) pair without paying the cost of a POST
// and without running an inline verification.
//
// scopeID and scopeLabel are zero-valued fields when unused; the caller
// passes 0 and "" for scope types that do not need them.
func seedCheckpoint(
	t *testing.T,
	q *database.Queries,
	userID int64,
	scopeType string,
	scopeID int64,
	scopeLabel string,
	date string,
	expectedCents int64,
) database.BalanceCheckpoint {
	t.Helper()
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		t.Fatalf("seedCheckpoint: parse date %q: %v", date, err)
	}
	params := database.CreateCheckpointParams{
		UserID:              userID,
		ScopeType:           scopeType,
		Date:                d,
		ExpectedAmountCents: expectedCents,
	}
	if scopeID != 0 {
		params.ScopeID = sql.NullInt64{Int64: scopeID, Valid: true}
	}
	if scopeLabel != "" {
		params.ScopeLabel = sql.NullString{String: scopeLabel, Valid: true}
	}
	cp, err := q.CreateCheckpoint(context.Background(), params)
	if err != nil {
		t.Fatalf("seedCheckpoint: %v", err)
	}
	return cp
}

// mustUpdateCheckpointStatus force-writes last_verification_status on a
// checkpoint row. Used by the mutation-hook tests that need to assert "the
// handler flipped this from ok to mismatch" — without a pre-set ok state
// the assertion would reduce to "the handler wrote *something*", which is
// a weaker guarantee.
//
// Writes via raw UPDATE rather than UpdateCheckpointVerification to avoid
// coupling the fixture to the helper-under-test's side effects (that
// function also bumps last_verified_at via CURRENT_TIMESTAMP, which this
// seed path does not want to simulate).
func mustUpdateCheckpointStatus(t *testing.T, db *sql.DB, id int64, status string) {
	t.Helper()
	_, err := db.ExecContext(
		context.Background(),
		`UPDATE balance_checkpoints SET last_verification_status = ? WHERE id = ?`,
		status, id,
	)
	if err != nil {
		t.Fatalf("mustUpdateCheckpointStatus: %v", err)
	}
}
