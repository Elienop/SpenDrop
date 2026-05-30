package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleGetNotificationSettings_ReturnsDollarsNotCents(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", RoleMember) // ANY authed user may read

	req := httptest.NewRequest(http.MethodGet, "/api/push/preferences", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleGetNotificationSettings(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	// Decode into a map (a typed decode would zero-fill a missing/renamed field
	// and hide the bug) — same regression-guard convention as savings.
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Threshold crosses the wire in DOLLARS (default 50000 cents -> 500.00).
	got, ok := resp["large_txn_threshold_dollars"]
	if !ok {
		t.Fatal("response missing large_txn_threshold_dollars")
	}
	if got.(float64) != 500.0 {
		t.Errorf("threshold dollars: got %v want 500", got)
	}
	if _, leaked := resp["large_txn_threshold_cents"]; leaked {
		t.Error("response leaked large_txn_threshold_cents; the frontend reads dollars")
	}
	// Bool defaults: over_budget on, activity types off.
	if resp["over_budget"] != true {
		t.Errorf("over_budget: got %v want true", resp["over_budget"])
	}
	if resp["txn_added"] != false {
		t.Errorf("txn_added: got %v want false", resp["txn_added"])
	}
}

func TestHandleUpdateNotificationSettings_NonAdmin403(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "bob", RoleMember)

	body := strings.NewReader(`{"over_budget":true,"txn_added":true,"txn_deleted":false,"txn_edited":false,"large_txn":false,"large_txn_threshold_dollars":750}`)
	req := httptest.NewRequest(http.MethodPut, "/api/push/preferences", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleUpdateNotificationSettings(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin PUT: expected 403, got %d", rec.Code)
	}
}

func TestHandleUpdateNotificationSettings_AdminPersistsDollarsAsCents(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "alice", RoleAdmin)

	body := strings.NewReader(`{"over_budget":false,"txn_added":true,"txn_deleted":true,"txn_edited":false,"large_txn":true,"large_txn_threshold_dollars":750.25}`)
	req := httptest.NewRequest(http.MethodPut, "/api/push/preferences", body)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()
	h.handleUpdateNotificationSettings(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("admin PUT: expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	// Stored as cents: $750.25 -> 75025.
	saved, err := q.GetNotificationSettings(req.Context())
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if saved.LargeTxnThresholdCents != dollarsToCents(750.25) {
		t.Errorf("threshold cents: got %d want %d", saved.LargeTxnThresholdCents, dollarsToCents(750.25))
	}
	if saved.OverBudget || !saved.TxnAdded || !saved.TxnDeleted || saved.TxnEdited || !saved.LargeTxn {
		t.Errorf("toggles not persisted: %+v", saved)
	}
}

func TestHandleUpdateNotificationSettings_RejectsNegativeThreshold(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "alice", RoleAdmin)

	body := strings.NewReader(`{"over_budget":true,"txn_added":false,"txn_deleted":false,"txn_edited":false,"large_txn":true,"large_txn_threshold_dollars":-5}`)
	req := httptest.NewRequest(http.MethodPut, "/api/push/preferences", body)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()
	h.handleUpdateNotificationSettings(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("negative threshold: expected 400, got %d", rec.Code)
	}
}
