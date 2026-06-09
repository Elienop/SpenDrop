package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/elienop/spendrop/internal/database"
)

func TestPushAlertPayload_TagOmitemptyMarshal(t *testing.T) {
	// Zero Tag -> key omitted (over_budget payloads stay byte-identical).
	zero, err := json.Marshal(pushAlertPayload{Title: "t", Body: "b", URL: "/", Type: "txn_added"})
	if err != nil {
		t.Fatalf("marshal zero: %v", err)
	}
	if strings.Contains(string(zero), `"tag"`) {
		t.Errorf("zero tag must be omitted, got %s", zero)
	}
	// Set Tag -> key emitted with the right value.
	set, err := json.Marshal(pushAlertPayload{Title: "t", Type: "txn_added", Tag: "activity"})
	if err != nil {
		t.Fatalf("marshal set: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(set, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["tag"] != "activity" {
		t.Errorf("tag: got %v want activity", m["tag"])
	}
}

// enableNotif flips one household type on (defaults are off for activity types).
func enableNotif(t *testing.T, q *database.Queries, mut func(*database.UpdateNotificationSettingsParams)) {
	t.Helper()
	cur, err := q.GetNotificationSettings(context.Background())
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	p := database.UpdateNotificationSettingsParams{
		OverBudget:             cur.OverBudget,
		TxnAdded:               cur.TxnAdded,
		TxnDeleted:             cur.TxnDeleted,
		TxnEdited:              cur.TxnEdited,
		LargeTxn:               cur.LargeTxn,
		LargeTxnThresholdCents: cur.LargeTxnThresholdCents,
	}
	mut(&p)
	if err := q.UpdateNotificationSettings(context.Background(), p); err != nil {
		t.Fatalf("update settings: %v", err)
	}
}

func decodeOnly(t *testing.T, rec *recordingSender) pushAlertPayload {
	t.Helper()
	if rec.count() != 1 {
		t.Fatalf("want exactly 1 send, got %d", rec.count())
	}
	var p pushAlertPayload
	if err := json.Unmarshal(rec.payloads[0], &p); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if p.Title == "" || p.Body == "" || p.URL == "" {
		t.Errorf("payload missing title/body/url: %+v", p)
	}
	return p
}

func TestNotifyTxnAdded_FiresWhenEnabled(t *testing.T) {
	q, db := setupTestDB(t)
	rec := &recordingSender{}
	h := NewHandler(q, db)
	h.pushTesterForBudgetAlerts = rec

	user := seedTestUser(t, q, "alice", RoleMember)
	cat := seedExpenseCategory(t, q, "Groceries")
	seedPushSub(t, q, user.ID, "https://push.example/added")
	enableNotif(t, q, func(p *database.UpdateNotificationSettingsParams) { p.TxnAdded = true })

	txn := seedExpenseRow(t, q, user.ID, cat, "2026-05-10", 1234) // $12.34, below default $500 threshold
	h.notifyTxnAdded(context.Background(), txn, user.DisplayName, 0)

	p := decodeOnly(t, rec)
	if p.Type != "txn_added" {
		t.Errorf("type: got %q want txn_added", p.Type)
	}
	if !strings.HasPrefix(p.Body, user.DisplayName+" ") {
		t.Errorf("body must start with actor display name, got %q", p.Body)
	}
}

func TestNotifyTxnDeleted_And_Edited_FireWithType(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enable  func(*database.UpdateNotificationSettingsParams)
		call    func(h *Handler, txn database.Transaction)
		wantTyp string
	}{
		{"deleted", func(p *database.UpdateNotificationSettingsParams) { p.TxnDeleted = true },
			func(h *Handler, txn database.Transaction) { h.notifyTxnDeleted(context.Background(), txn, "alice", 0) }, "txn_deleted"},
		{"edited", func(p *database.UpdateNotificationSettingsParams) { p.TxnEdited = true },
			func(h *Handler, txn database.Transaction) { h.notifyTxnEdited(context.Background(), txn, "alice", 0) }, "txn_edited"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, db := setupTestDB(t)
			rec := &recordingSender{}
			h := NewHandler(q, db)
			h.pushTesterForBudgetAlerts = rec
			user := seedTestUser(t, q, "alice", RoleMember)
			cat := seedExpenseCategory(t, q, "Groceries")
			seedPushSub(t, q, user.ID, "https://push.example/"+tc.name)
			enableNotif(t, q, tc.enable)

			txn := seedExpenseRow(t, q, user.ID, cat, "2026-05-10", 1234)
			tc.call(h, txn)
			p := decodeOnly(t, rec)
			if p.Type != tc.wantTyp {
				t.Errorf("type: got %q want %q", p.Type, tc.wantTyp)
			}
			if !strings.HasPrefix(p.Body, "alice ") {
				t.Errorf("body must start with actor display name, got %q", p.Body)
			}
		})
	}
}

// Large-txn precedence: when amount >= threshold and large_txn is on, a single
// create sends ONE "large_txn" push instead of "txn_added".
func TestNotifyTxnAdded_LargePrecedence(t *testing.T) {
	q, db := setupTestDB(t)
	rec := &recordingSender{}
	h := NewHandler(q, db)
	h.pushTesterForBudgetAlerts = rec
	user := seedTestUser(t, q, "alice", RoleMember)
	cat := seedExpenseCategory(t, q, "Rent")
	seedPushSub(t, q, user.ID, "https://push.example/large")
	enableNotif(t, q, func(p *database.UpdateNotificationSettingsParams) {
		p.TxnAdded = true
		p.LargeTxn = true
		p.LargeTxnThresholdCents = 50000 // $500
	})

	txn := seedExpenseRow(t, q, user.ID, cat, "2026-05-10", 120000) // $1200 >= $500
	h.notifyTxnAdded(context.Background(), txn, user.DisplayName, 0)

	p := decodeOnly(t, rec) // exactly one push, not two
	if p.Type != "large_txn" {
		t.Errorf("type: got %q want large_txn (precedence)", p.Type)
	}
}

// When the type is off the helper is a pure no-op (no send), even with a subscription.
func TestNotifyTxnAdded_NoOpWhenDisabled(t *testing.T) {
	q, db := setupTestDB(t)
	rec := &recordingSender{}
	h := NewHandler(q, db)
	h.pushTesterForBudgetAlerts = rec
	user := seedTestUser(t, q, "alice", RoleMember)
	cat := seedExpenseCategory(t, q, "Groceries")
	seedPushSub(t, q, user.ID, "https://push.example/off")
	// txn_added stays at its default (off); do not enable.

	txn := seedExpenseRow(t, q, user.ID, cat, "2026-05-10", 1234)
	h.notifyTxnAdded(context.Background(), txn, user.DisplayName, 0)
	if rec.count() != 0 {
		t.Fatalf("disabled: want 0 sends, got %d", rec.count())
	}
}

func TestNotifyTxnBatch_AggregatesOneSend(t *testing.T) {
	q, db := setupTestDB(t)
	rec := &recordingSender{}
	h := NewHandler(q, db)
	h.pushTesterForBudgetAlerts = rec
	user := seedTestUser(t, q, "alice", RoleMember)
	seedPushSub(t, q, user.ID, "https://push.example/batch")
	enableNotif(t, q, func(p *database.UpdateNotificationSettingsParams) { p.TxnAdded = true })

	h.notifyTxnBatch(context.Background(), "added", 7, user.DisplayName, 0)
	p := decodeOnly(t, rec) // ONE aggregate push, not 7
	if p.Type != "txn_added" {
		t.Errorf("type: got %q want txn_added", p.Type)
	}
	if !strings.HasPrefix(p.Body, user.DisplayName+" ") {
		t.Errorf("body must start with actor display name, got %q", p.Body)
	}
}
