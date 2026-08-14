package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elienop/spendrop/internal/database"
)

// B10 signed amounts: the HTTP-level negative-side coverage the write path had
// none of. Before this file, no test posted a negative amount over HTTP at all
// — so the suite stayed green whether the three positivity gates
// (validateTransactionRequest, validateMoneyAmount, resolveCurrency) were
// relaxed correctly, partially, or not at all, and equally green if the
// relaxation overshot into accepting zero or an unbounded negative.

// createTxnRaw posts one create body and returns the recorder, whatever the
// status. Tests that expect a rejection need the failing response, which the
// 201-asserting helpers in this package deliberately do not hand back.
func createTxnRaw(t *testing.T, h *Handler, user database.User, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal create body: %v", err)
	}
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/transactions", bytes.NewReader(b)), user)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.handleCreateTransaction(rec, req)
	return rec
}

// createdTxnID posts a create body, requires 201, and returns the new row's id.
func createdTxnID(t *testing.T, h *Handler, user database.User, body map[string]any) int64 {
	t.Helper()
	rec := createTxnRaw(t, h, user, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	decodeResponse(t, rec, &created)
	return created.ID
}

// assertNoRowsStored is the half of a rejection test that does not depend on the
// status code. A gate that 400s but writes anyway, or one that 500s at the CHECK
// after writing, both fail here.
func assertNoRowsStored(t *testing.T, h *Handler) {
	t.Helper()
	counts, err := h.queries.CountAllTransactions(context.Background())
	if err != nil {
		t.Fatalf("count transactions: %v", err)
	}
	if counts.Live != 0 || counts.Deleted != 0 {
		var cents int64
		_ = h.db.QueryRow(`SELECT amount_cents FROM transactions LIMIT 1`).Scan(&cents)
		t.Errorf("a rejected amount was stored anyway: live=%d deleted=%d, first amount_cents=%d",
			counts.Live, counts.Deleted, cents)
	}
}

// TestHandleCreateTransaction_NegativeBaseAmount_IsStoredSigned pins the core of
// the model: a refund is a negative amount on an expense row, stored as it was
// sent. The sign is the whole feature — a gate that accepted the request and
// stored math.Abs of it would pass a status-only assertion.
func TestHandleCreateTransaction_NegativeBaseAmount_IsStoredSigned(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "owner", "member")
	catID := seedExpenseCategory(t, h.queries, "Groceries")

	id := createdTxnID(t, h, user, map[string]any{
		"date":        "2026-04-06",
		"amount":      -20.00,
		"description": "Returned the second bag",
		"category_id": catID,
	})

	amountCents, origAmtCents, origCur, rate := moneyOf(t, h, id)
	if amountCents != -2000 {
		t.Errorf("amount_cents = %d, want -2000 — a refund is a negative amount, not its magnitude", amountCents)
	}
	if origAmtCents.Valid || origCur.Valid {
		t.Errorf("original_* = (%v, %v), want both NULL on a base-currency row", origAmtCents, origCur)
	}
	// No division happened, so no rate exists to record. A rate of 1 here would
	// invent a distinction between "no currency selected" and "base currency
	// selected" that storage has never made.
	if rate.Valid {
		t.Errorf("booked_rate = %+v, want NULL — nothing priced this row", rate)
	}
}

// TestHandleCreateTransaction_ForeignRefund_KeepsSignAgreementAndRecordsRate is
// the LBP half. The household books in LBP daily, so a refund that works in the
// base currency and 400s in a foreign one is a half-shipped feature — and the
// gate that would do that (resolveCurrency's own `*req.OriginalAmount <= 0`) is
// a different line in a different function from the base-currency one.
//
// It also pins the invariant that makes the money triple readable at all:
// sign(amount_cents) == sign(original_amount_cents), which holds by arithmetic
// because every rate_to_base is positive.
func TestHandleCreateTransaction_ForeignRefund_KeepsSignAgreementAndRecordsRate(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "owner", "member")
	catID := seedExpenseCategory(t, h.queries, "Groceries")

	// Seeded LBP rate is 89,000 (migration 001).
	id := createdTxnID(t, h, user, map[string]any{
		"date":              "2026-04-06",
		"original_amount":   -1_500_000,
		"original_currency": "LBP",
		"description":       "Spinneys refund",
		"category_id":       catID,
	})

	amountCents, origAmtCents, origCur, rate := moneyOf(t, h, id)
	if amountCents != -1685 {
		t.Errorf("amount_cents = %d, want -1685 (-1,500,000 / 89,000)", amountCents)
	}
	if !origAmtCents.Valid || origAmtCents.Int64 != -150_000_000 {
		t.Errorf("original_amount_cents = %v, want -150000000 — the foreign figure keeps its sign too", origAmtCents)
	}
	if !origCur.Valid || origCur.String != "LBP" {
		t.Errorf("original_currency = %v, want LBP", origCur)
	}
	if (amountCents < 0) != (origAmtCents.Int64 < 0) {
		t.Errorf("signs disagree: amount_cents=%d original_amount_cents=%d — the two halves of one "+
			"money pair must point the same way", amountCents, origAmtCents.Int64)
	}
	if !rate.Valid || rate.Float64 != 89_000 {
		t.Errorf("booked_rate = %+v, want valid 89000 — the create must record the divisor it used, "+
			"because currencies.rate_to_base is overwritten in place with no history", rate)
	}
}

// TestHandleCreateTransaction_ZeroAmount_IsRejected pins the one part of the old
// gate that does NOT relax (design decision 1: zero stays illegal everywhere).
//
// The sub-cent cases are the reason this checks four inputs rather than two.
// 0.004 is not zero as a float but stores as zero cents, and the table's
// CHECK(amount_cents != 0) answers that with a 500 — a validation failure
// arriving as a server error. Rejecting the whole rounds-to-zero class at the
// boundary is what keeps the response a 400.
func TestHandleCreateTransaction_ZeroAmount_IsRejected(t *testing.T) {
	for _, tc := range []struct {
		name string
		body func(catID int64) map[string]any
	}{
		{"base zero", func(catID int64) map[string]any {
			return map[string]any{"date": "2026-04-06", "amount": 0, "description": "nothing", "category_id": catID}
		}},
		{"base rounds to zero", func(catID int64) map[string]any {
			return map[string]any{"date": "2026-04-06", "amount": 0.004, "description": "dust", "category_id": catID}
		}},
		{"base negative rounds to zero", func(catID int64) map[string]any {
			return map[string]any{"date": "2026-04-06", "amount": -0.004, "description": "dust", "category_id": catID}
		}},
		{"foreign zero", func(catID int64) map[string]any {
			return map[string]any{"date": "2026-04-06", "original_amount": 0, "original_currency": "LBP",
				"description": "nothing", "category_id": catID}
		}},
		{"foreign converts to zero", func(catID int64) map[string]any {
			// 1 LBP is worth 0.0000112 dollars: a legal foreign amount whose
			// CONVERTED value rounds to no cents at all.
			return map[string]any{"date": "2026-04-06", "original_amount": 1, "original_currency": "LBP",
				"description": "one lira", "category_id": catID}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := setupHandler(t)
			user := seedTestUser(t, h.queries, "owner", "member")
			catID := seedExpenseCategory(t, h.queries, "Groceries")

			rec := createTxnRaw(t, h, user, tc.body(catID))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
			}
			assertNoRowsStored(t, h)
		})
	}
}

// TestHandleCreateTransaction_OutOfRangeMagnitude_IsRejected is the laundering
// test, and it is the reason the sign relax and the two-sided bound had to ship
// in the same change.
//
// While the gates read `<= 0`, the negative half of every magnitude bound was
// unreachable and a one-sided `> MaxTransactionAmount` looked complete. Once
// sign is legal, -1e307 has a clear run at dollarsToCents, which converts it to
// int64 minimum — a row storing -92,233,720,368,547,758.08 from a request that
// no gate objected to.
func TestHandleCreateTransaction_OutOfRangeMagnitude_IsRejected(t *testing.T) {
	for _, tc := range []struct {
		name string
		body func(catID int64) map[string]any
	}{
		{"negative 1e307", func(catID int64) map[string]any {
			return map[string]any{"date": "2026-04-06", "amount": -1e307, "description": "laundered", "category_id": catID}
		}},
		{"just past the negative bound", func(catID int64) map[string]any {
			return map[string]any{"date": "2026-04-06", "amount": -(float64(MaxTransactionAmount) + 1),
				"description": "over", "category_id": catID}
		}},
		{"just past the positive bound", func(catID int64) map[string]any {
			return map[string]any{"date": "2026-04-06", "amount": float64(MaxTransactionAmount) + 1,
				"description": "over", "category_id": catID}
		}},
		{"negative foreign past the bound", func(catID int64) map[string]any {
			return map[string]any{"date": "2026-04-06", "original_amount": -1e307, "original_currency": "LBP",
				"description": "laundered", "category_id": catID}
		}},
		// The next two reach a bound that has no second line of defence behind
		// it. On a foreign row the server discards the wire `amount` entirely,
		// and on a base row it never looks at `original_amount` — so for each of
		// those fields, validateTransactionRequest's own two-sided bound is the
		// only thing standing between a wild value and a 201.
		{"ignored amount out of range on a foreign row", func(catID int64) map[string]any {
			return map[string]any{"date": "2026-04-06", "amount": -1e307,
				"original_amount": -1_500_000, "original_currency": "LBP",
				"description": "wild ignored amount", "category_id": catID}
		}},
		{"ignored original_amount out of range on a base row", func(catID int64) map[string]any {
			return map[string]any{"date": "2026-04-06", "amount": -20.00, "original_amount": -1e307,
				"description": "wild ignored original", "category_id": catID}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := setupHandler(t)
			user := seedTestUser(t, h.queries, "owner", "member")
			catID := seedExpenseCategory(t, h.queries, "Groceries")

			rec := createTxnRaw(t, h, user, tc.body(catID))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
			}
			assertNoRowsStored(t, h)
		})
	}

	// Control: the bound itself is still acceptable in both directions, so the
	// rejections above are about range and not about the sign or the size of
	// the number in general.
	for _, amount := range []float64{MaxTransactionAmount, -MaxTransactionAmount} {
		h := setupHandler(t)
		user := seedTestUser(t, h.queries, "owner", "member")
		catID := seedExpenseCategory(t, h.queries, "Groceries")
		rec := createTxnRaw(t, h, user, map[string]any{
			"date": "2026-04-06", "amount": amount, "description": "at the bound", "category_id": catID,
		})
		if rec.Code != http.StatusCreated {
			t.Errorf("amount %v: status = %d, want 201 — the bound is inclusive; body = %s",
				amount, rec.Code, rec.Body.String())
		}
	}
}

// TestHandleCreateTransaction_RejectsNegativeOverflowingConvertedAmount is the
// negative twin of TestHandleCreateTransaction_RejectsOverflowingConvertedAmount,
// and it is the one case where validateMoneyAmount's own magnitude bound is the
// only guard in the request's path.
//
// Both request fields are comfortably inside MaxTransactionAmount, so
// validateTransactionRequest passes the request through untouched; the value
// that goes out of range is the one the DIVISION produces, which exists only
// inside resolveCurrency. A one-sided `d > MaxTransactionAmount` there sees
// -1e18 as small, and dollarsToCents converts it to int64 minimum.
func TestHandleCreateTransaction_RejectsNegativeOverflowingConvertedAmount(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "owner", "member")
	catID := seedExpenseCategory(t, h.queries, "Groceries")
	// A tiny rate multiplies the original amount up by 1e9 on conversion.
	seedTestCurrency(t, h.queries, "XXX", 1e-9, false)

	rec := createTxnRaw(t, h, user, map[string]any{
		"date":              "2026-04-06",
		"amount":            -1,
		"original_currency": "XXX",
		"original_amount":   -float64(MaxTransactionAmount),
		"description":       "converted negative overflow",
		"category_id":       catID,
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	assertNoRowsStored(t, h)
}

// TestHandleCreateTransaction_NegativeIncome_IsAccepted pins design decision 2:
// a negative income row is DEFINED as an income reversal, not rejected.
//
// It is deliberately not gated on the category's type. A join-time check at
// write time would make the row's legality depend on a column a later bulk
// category-move can change, so a perfectly legal row could become an
// unrepresentable one without anyone editing its amount.
func TestHandleCreateTransaction_NegativeIncome_IsAccepted(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "owner", "member")
	catID := seedIncomeCategory(t, h.queries, "Salary")

	id := createdTxnID(t, h, user, map[string]any{
		"date":        "2026-04-06",
		"amount":      -100.00,
		"description": "Clawed-back bonus",
		"category_id": catID,
	})

	if amountCents, _, _, _ := moneyOf(t, h, id); amountCents != -10000 {
		t.Errorf("amount_cents = %d, want -10000 — an income reversal is a negative income row", amountCents)
	}
}

// TestHandleCreateTransaction_BaseCurrencySelected_RecordsNoRate covers the
// branch a base-currency CODE takes (as opposed to no currency key at all).
// Both must store the same NULLs: they are indistinguishable in the row today,
// and a rate recorded on one of them would be a distinction no reader can act
// on and no writer maintains.
func TestHandleCreateTransaction_BaseCurrencySelected_RecordsNoRate(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "owner", "member")
	catID := seedExpenseCategory(t, h.queries, "Groceries")

	id := createdTxnID(t, h, user, map[string]any{
		"date":              "2026-04-06",
		"amount":            -20.00,
		"original_currency": "USD", // the seeded base code
		"description":       "Refund, base currency selected explicitly",
		"category_id":       catID,
	})

	amountCents, origAmtCents, origCur, rate := moneyOf(t, h, id)
	if amountCents != -2000 {
		t.Errorf("amount_cents = %d, want -2000", amountCents)
	}
	if origAmtCents.Valid || origCur.Valid || rate.Valid {
		t.Errorf("original_amount_cents=%v original_currency=%v booked_rate=%+v — all three must be NULL "+
			"when the selected currency IS the base currency", origAmtCents, origCur, rate)
	}
}

// TestHandleBatchCreateTransactions_ForeignRefund_RecordsRate covers the third
// CreateTransactionParams construction. The batch handler builds its own params
// literal, so the rate reaching the single-create insert says nothing about this
// one — a dropped field there is invisible to every other test in this file.
func TestHandleBatchCreateTransactions_ForeignRefund_RecordsRate(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "owner", "member")
	catID := seedExpenseCategory(t, h.queries, "Groceries")

	body, err := json.Marshal([]map[string]any{
		{
			"date":              "2026-04-06",
			"original_amount":   -1_500_000,
			"original_currency": "LBP",
			"description":       "Spinneys refund (batch)",
			"category_id":       catID,
		},
		{
			"date":        "2026-04-06",
			"amount":      -20.00,
			"description": "Base refund (batch)",
			"category_id": catID,
		},
	})
	if err != nil {
		t.Fatalf("marshal batch body: %v", err)
	}
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/transactions/batch", bytes.NewReader(body)), user)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.handleBatchCreateTransactions(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("batch create: status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}

	var created []struct {
		ID int64 `json:"id"`
	}
	decodeResponse(t, rec, &created)
	if len(created) != 2 {
		t.Fatalf("created %d rows, want 2", len(created))
	}

	amountCents, origAmtCents, _, rate := moneyOf(t, h, created[0].ID)
	if amountCents != -1685 || !origAmtCents.Valid || origAmtCents.Int64 != -150_000_000 {
		t.Errorf("foreign batch item: amount_cents=%d original_amount_cents=%v, want -1685 and -150000000",
			amountCents, origAmtCents)
	}
	if !rate.Valid || rate.Float64 != 89_000 {
		t.Errorf("foreign batch item: booked_rate = %+v, want valid 89000", rate)
	}

	baseAmount, _, _, baseRate := moneyOf(t, h, created[1].ID)
	if baseAmount != -2000 {
		t.Errorf("base batch item: amount_cents = %d, want -2000", baseAmount)
	}
	if baseRate.Valid {
		t.Errorf("base batch item: booked_rate = %+v, want NULL", baseRate)
	}
}

// TestNotifyTxn_LargeRefund_TriggersLargeTxnWithRefundCopy pins both halves of
// the notification change at once, because they fail together on a real phone:
// a big refund that no longer trips the large-txn threshold is silent, and one
// that does trip it while still formatting the signed value reads "added
// $-600.00".
//
// A mistyped minus is exactly the case a household needs told about, so the
// threshold has to compare magnitude.
func TestNotifyTxn_LargeRefund_TriggersLargeTxnWithRefundCopy(t *testing.T) {
	q, db := setupTestDB(t)
	rec := &recordingSender{}
	h := NewHandler(q, db)
	h.pushTesterForBudgetAlerts = rec

	user := seedTestUser(t, q, "alice", RoleMember)
	cat := seedExpenseCategory(t, q, "Rent")
	seedPushSub(t, q, user.ID, "https://push.example/large-refund")
	// BOTH types on: if the magnitude comparison regresses to a signed one, the
	// refund falls through to txn_added and still sends, so the failure shows up
	// as the wrong type rather than as silence.
	enableNotif(t, q, func(p *database.UpdateNotificationSettingsParams) {
		p.TxnAdded = true
		p.LargeTxn = true
		p.LargeTxnThresholdCents = 50000 // $500
	})

	txn := seedExpenseRow(t, q, user.ID, cat, "2026-05-10", -60000) // a $600 refund
	h.notifyTxnAdded(context.Background(), txn, user.DisplayName, 0)

	p := decodeOnly(t, h, rec)
	if p.Type != "large_txn" {
		t.Errorf("type: got %q want large_txn — the threshold compares magnitude, so a $600 refund is "+
			"as large as a $600 spend", p.Type)
	}
	if !strings.Contains(p.Body, "added a refund of $600.00") {
		t.Errorf("body = %q, want it to name a refund of $600.00", p.Body)
	}
	if strings.Contains(p.Body, "$-") {
		t.Errorf("body = %q, want no signed dollar figure — the direction is carried by the wording", p.Body)
	}
}

// TestNotifyTxnEdited_LargeRefund_TriggersLargeTxn covers the SECOND threshold
// comparison. notifyTxnEdited holds its own copy of the same expression, so the
// create-path test above says nothing about it — editing a row into a large
// refund is the likelier way a mistyped minus actually arrives.
func TestNotifyTxnEdited_LargeRefund_TriggersLargeTxn(t *testing.T) {
	q, db := setupTestDB(t)
	rec := &recordingSender{}
	h := NewHandler(q, db)
	h.pushTesterForBudgetAlerts = rec

	user := seedTestUser(t, q, "alice", RoleMember)
	cat := seedExpenseCategory(t, q, "Rent")
	seedPushSub(t, q, user.ID, "https://push.example/edited-refund")
	enableNotif(t, q, func(p *database.UpdateNotificationSettingsParams) {
		p.TxnEdited = true
		p.LargeTxn = true
		p.LargeTxnThresholdCents = 50000
	})

	txn := seedExpenseRow(t, q, user.ID, cat, "2026-05-10", -60000)
	h.notifyTxnEdited(context.Background(), txn, user.DisplayName, 0)

	p := decodeOnly(t, h, rec)
	if p.Type != "large_txn" {
		t.Errorf("type: got %q want large_txn", p.Type)
	}
	if !strings.Contains(p.Body, "edited a refund of $600.00") {
		t.Errorf("body = %q, want it to name an edited refund of $600.00", p.Body)
	}
}

// TestNotifyTxnAdded_PositiveAmount_KeepsPlainCopy is the control arm. Without
// it, a helper that called everything a refund would pass the test above.
func TestNotifyTxnAdded_PositiveAmount_KeepsPlainCopy(t *testing.T) {
	q, db := setupTestDB(t)
	rec := &recordingSender{}
	h := NewHandler(q, db)
	h.pushTesterForBudgetAlerts = rec

	user := seedTestUser(t, q, "alice", RoleMember)
	cat := seedExpenseCategory(t, q, "Rent")
	seedPushSub(t, q, user.ID, "https://push.example/large-spend")
	enableNotif(t, q, func(p *database.UpdateNotificationSettingsParams) {
		p.TxnAdded = true
		p.LargeTxn = true
		p.LargeTxnThresholdCents = 50000
	})

	txn := seedExpenseRow(t, q, user.ID, cat, "2026-05-10", 60000)
	h.notifyTxnAdded(context.Background(), txn, user.DisplayName, 0)

	p := decodeOnly(t, h, rec)
	if p.Type != "large_txn" {
		t.Errorf("type: got %q want large_txn", p.Type)
	}
	if !strings.Contains(p.Body, "added $600.00") {
		t.Errorf("body = %q, want the unchanged spend wording", p.Body)
	}
	if strings.Contains(p.Body, "refund") {
		t.Errorf("body = %q, want no refund wording on a positive amount", p.Body)
	}
}
