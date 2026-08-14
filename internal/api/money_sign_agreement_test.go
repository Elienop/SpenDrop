package api

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The wire half of B10's sign-agreement rule. Import has refused a mixed money
// pair since WP3 (skipReasonSignMismatch), but the API accepted the identical
// shape and answered 201: the foreign branch of resolveCurrency discards the
// wire `amount` and recomputes it from original_amount, so a body that says
// "refund -5" alongside a positive original booked a PURCHASE. Measured on this
// branch before the gate landed:
//
//	{"amount": -5, "original_currency": "LBP", "original_amount": 900000}
//	-> 201 Created, amount_cents = +1011
//
// One spreadsheet row must not land differently depending on whether it arrives
// through import or through POST, so both doors now ask moneySignsDisagree.

// assertStoredMoney is the "and nothing moved" half of the rejection tests on
// the UPDATE path, where assertNoRowsStored cannot help: the row under test is
// supposed to still be there, unchanged.
func assertStoredMoney(t *testing.T, h *Handler, id int64, wantCents, wantOrigCents int64) {
	t.Helper()
	amountCents, origAmtCents, _, _ := moneyOf(t, h, id)
	if amountCents != wantCents {
		t.Errorf("amount_cents = %d, want %d — a rejected edit moved the stored money", amountCents, wantCents)
	}
	if !origAmtCents.Valid || origAmtCents.Int64 != wantOrigCents {
		t.Errorf("original_amount_cents = %v, want %d — a rejected edit moved the foreign figure",
			origAmtCents, wantOrigCents)
	}
}

// TestHandleCreateTransaction_SignDisagreeingMoneyPair_IsRejected is the direct
// regression test for the audit finding.
//
// Both directions are covered because the two halves fail differently for a
// reader: a negative wire amount with a positive original silently books a
// purchase the user meant to reverse, and the mirror silently books a refund
// they did not intend. The stored row looks perfectly consistent afterwards —
// the discarded field is the only evidence anything was wrong — so nothing
// downstream can detect it.
func TestHandleCreateTransaction_SignDisagreeingMoneyPair_IsRejected(t *testing.T) {
	for _, tc := range []struct {
		name           string
		amount         float64
		originalAmount float64
	}{
		{"refund on the wire, purchase in the original", -5, 900_000},
		{"purchase on the wire, refund in the original", 5, -900_000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := setupHandler(t)
			user := seedTestUser(t, h.queries, "owner", "member")
			catID := seedExpenseCategory(t, h.queries, "Groceries")

			rec := createTxnRaw(t, h, user, map[string]any{
				"date":              "2026-04-06",
				"amount":            tc.amount,
				"original_amount":   tc.originalAmount,
				"original_currency": "LBP",
				"description":       "Contradictory money pair",
				"category_id":       catID,
			})
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
			}
			// The message has to name the rule, not just fail: the client sent
			// two fields and only their RELATIONSHIP is wrong, so "amount is
			// invalid" would send the user hunting the wrong one.
			if body := rec.Body.String(); !strings.Contains(body, "same sign") {
				t.Errorf("body = %s, want it to say the two amounts must carry the same sign", body)
			}
			assertNoRowsStored(t, h)
		})
	}
}

// TestHandleCreateTransaction_SubCentOriginal_ReportsZeroNotSignMismatch pins the
// ORDER of the two gates inside resolveCurrency's foreign branch.
//
// 0.004 is not zero as a float, so a sign check placed first sees a positive
// number disagreeing with the negative wire amount and blames the relationship —
// when the actual fault is that the original stores as no cents at all. Both
// answers are a 400, so only the message tells the two apart, and only the
// message tells the user which field to fix. This is the single bad-original
// case that reaches here: validateTransactionRequest has already refused NaN,
// Inf and anything past MaxTransactionAmount on all three write paths.
func TestHandleCreateTransaction_SubCentOriginal_ReportsZeroNotSignMismatch(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "owner", "member")
	catID := seedExpenseCategory(t, h.queries, "Groceries")

	rec := createTxnRaw(t, h, user, map[string]any{
		"date":              "2026-04-06",
		"amount":            -5,
		"original_amount":   0.004,
		"original_currency": "LBP",
		"description":       "Sub-cent original",
		"category_id":       catID,
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "must not be zero") {
		t.Errorf("body = %s, want it to name the zero original_amount", body)
	}
	if strings.Contains(body, "same sign") {
		t.Errorf("body = %s, want the zero fault, not a sign one — the sign gate must run second", body)
	}
	assertNoRowsStored(t, h)
}

// TestHandleCreateTransaction_AgreeingMoneyPair_IsAccepted is the forward guard:
// the gate must refuse the mixed pair only.
//
// It is not redundant with TestHandleCreateTransaction_ForeignRefund_… , which
// omits the `amount` key entirely and therefore exercises the zero case rather
// than an agreeing PAIR. Every payload the web app actually sends for a foreign
// row carries both fields (toCreatePayload signs the magnitude first, then
// divides by a rate that is always > 0), so this is the shape a gate that
// over-fired would break in production while the existing tests stayed green.
func TestHandleCreateTransaction_AgreeingMoneyPair_IsAccepted(t *testing.T) {
	for _, tc := range []struct {
		name           string
		amount         float64
		originalAmount float64
		wantCents      int64
		wantOrigCents  int64
	}{
		{"both negative — a foreign refund", -16.85, -1_500_000, -1685, -150_000_000},
		{"both positive — an ordinary foreign purchase", 16.85, 1_500_000, 1685, 150_000_000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := setupHandler(t)
			user := seedTestUser(t, h.queries, "owner", "member")
			catID := seedExpenseCategory(t, h.queries, "Groceries")

			// Seeded LBP rate is 89,000 (migration 001).
			id := createdTxnID(t, h, user, map[string]any{
				"date":              "2026-04-06",
				"amount":            tc.amount,
				"original_amount":   tc.originalAmount,
				"original_currency": "LBP",
				"description":       "Spinneys",
				"category_id":       catID,
			})

			amountCents, origAmtCents, _, _ := moneyOf(t, h, id)
			if amountCents != tc.wantCents {
				t.Errorf("amount_cents = %d, want %d (original / 89,000)", amountCents, tc.wantCents)
			}
			if !origAmtCents.Valid || origAmtCents.Int64 != tc.wantOrigCents {
				t.Errorf("original_amount_cents = %v, want %d", origAmtCents, tc.wantOrigCents)
			}
		})
	}
}

// TestHandleCreateTransaction_ZeroWireAmountWithSignedOriginal_IsAccepted pins
// the exemption the predicate encodes, and it is the case a naive
// sign(a) == sign(b) gate would break.
//
// On the foreign branch the wire `amount` is decorative — the server divides
// original_amount by the rate and stores that — so API clients legitimately send
// 0 or omit the key, and Go decodes both to 0. A zero has no direction to
// disagree with, and treating it as positive would 400 every foreign refund
// posted without a base amount.
func TestHandleCreateTransaction_ZeroWireAmountWithSignedOriginal_IsAccepted(t *testing.T) {
	for _, tc := range []struct {
		name           string
		originalAmount float64
		wantCents      int64
	}{
		{"zero wire amount, negative original", -1_500_000, -1685},
		{"zero wire amount, positive original", 1_500_000, 1685},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := setupHandler(t)
			user := seedTestUser(t, h.queries, "owner", "member")
			catID := seedExpenseCategory(t, h.queries, "Groceries")

			id := createdTxnID(t, h, user, map[string]any{
				"date":              "2026-04-06",
				"amount":            0,
				"original_amount":   tc.originalAmount,
				"original_currency": "LBP",
				"description":       "Foreign row with no base amount on the wire",
				"category_id":       catID,
			})

			if amountCents, _, _, _ := moneyOf(t, h, id); amountCents != tc.wantCents {
				t.Errorf("amount_cents = %d, want %d — the original alone decides the stored value",
					amountCents, tc.wantCents)
			}
		})
	}
}

// TestHandleUpdateTransaction_SignDisagreeingMoneyPair_IsRejected covers the
// second of the three resolveCurrency call sites.
//
// PUT is a full replace, so an edit resends both money fields and can turn an
// agreeing pair into a mixed one — flipping the Refund toggle without the
// original following it. The row is a foreign refund to start with, so a gate
// that fired on sign rather than on DISAGREEMENT would fail this test at the
// setup step instead.
func TestHandleUpdateTransaction_SignDisagreeingMoneyPair_IsRejected(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "owner", "member")
	catID := seedExpenseCategory(t, h.queries, "Groceries")

	id := createdTxnID(t, h, user, map[string]any{
		"date":              "2026-04-06",
		"amount":            -16.85,
		"original_amount":   -1_500_000,
		"original_currency": "LBP",
		"description":       "Spinneys refund",
		"category_id":       catID,
	})
	assertStoredMoney(t, h, id, -1685, -150_000_000)

	rec := putTransaction(t, h, user, id, map[string]any{
		"date":              "2026-04-06",
		"amount":            16.85,      // toggled back to a purchase...
		"original_amount":   -1_500_000, // ...while the foreign figure stayed a refund
		"original_currency": "LBP",
		"description":       "Spinneys refund",
		"category_id":       catID,
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "same sign") {
		t.Errorf("body = %s, want it to say the two amounts must carry the same sign", body)
	}
	assertStoredMoney(t, h, id, -1685, -150_000_000)
}

// TestHandleBatchCreateTransactions_SignDisagreeingItem_RejectsWholeBatch covers
// the third call site, where the gate returns mid-transaction.
//
// The valid item is FIRST so the assertion is about the rollback and not about
// the loop stopping early: the batch handler has already handed item 0 to
// CreateTx by the time item 1 is refused, and only the deferred Rollback keeps
// the ledger clean.
func TestHandleBatchCreateTransactions_SignDisagreeingItem_RejectsWholeBatch(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "owner", "member")
	catID := seedExpenseCategory(t, h.queries, "Groceries")

	body, err := json.Marshal([]map[string]any{
		{
			"date":        "2026-04-06",
			"amount":      -20.00,
			"description": "Base refund (batch)",
			"category_id": catID,
		},
		{
			"date":              "2026-04-06",
			"amount":            -5,
			"original_amount":   900_000,
			"original_currency": "LBP",
			"description":       "Contradictory money pair (batch)",
			"category_id":       catID,
		},
	})
	if err != nil {
		t.Fatalf("marshal batch body: %v", err)
	}
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/transactions/batch", bytes.NewReader(body)), user)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.handleBatchCreateTransactions(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	got := rec.Body.String()
	if !strings.Contains(got, "same sign") {
		t.Errorf("body = %s, want it to say the two amounts must carry the same sign", got)
	}
	// Which item failed, not just that one did — a batch error naming no index
	// makes the client guess.
	if !strings.Contains(got, "item 1") {
		t.Errorf("body = %s, want it to name item 1", got)
	}
	assertNoRowsStored(t, h)
}

// TestMagnitudeCents_IsTotalAtInt64Minimum pins the arm that makes magnitudeCents
// a total function.
//
// Negation is not total on int64: -math.MinInt64 overflows back to
// math.MinInt64, so the largest amount the column can hold would have compared
// BELOW every large-transaction threshold — the one row a household most needs
// told about, silenced by its own size. No write path can produce that value
// today (validateMoneyAmount bounds an amount at 1e11 cents), so this is a
// guard against a future caller reading amount_cents straight from a row, not a
// live fix.
func TestMagnitudeCents_IsTotalAtInt64Minimum(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   int64
		want int64
	}{
		{"int64 minimum clamps instead of wrapping", math.MinInt64, math.MaxInt64},
		// The neighbour negates cleanly to exactly MaxInt64. Its presence keeps
		// the test honest about WHICH input needs the special arm.
		{"one above the minimum negates normally", math.MinInt64 + 1, math.MaxInt64},
		{"ordinary refund", -2000, 2000},
		{"ordinary purchase", 2000, 2000},
		{"zero", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := magnitudeCents(tc.in); got != tc.want {
				t.Errorf("magnitudeCents(%d) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}

	// The consequence, stated as the comparison the notification path makes.
	// A magnitude that is negative fails this even if the value above changed.
	const anyThreshold = 500_00
	if magnitudeCents(math.MinInt64) < anyThreshold {
		t.Errorf("magnitudeCents(math.MinInt64) = %d, which sits below a %d-cent large-txn threshold — "+
			"the biggest storable amount would send no alert",
			magnitudeCents(math.MinInt64), anyThreshold)
	}
}

// TestActivityPhrase_Int64Minimum_ReadsAsAPositiveRefund is the rendering half:
// activityPhrase formats the magnitude and carries direction in words, so a
// negation that wrapped would print a NEGATIVE dollar figure inside copy that
// already says "refund" — the exact double-negative the wording exists to avoid.
func TestActivityPhrase_Int64Minimum_ReadsAsAPositiveRefund(t *testing.T) {
	got := activityPhrase("added", math.MinInt64)
	if !strings.Contains(got, "a refund of $") {
		t.Errorf("activityPhrase = %q, want the refund wording", got)
	}
	if strings.Contains(got, "$-") {
		t.Errorf("activityPhrase = %q, want no negative dollar figure — the sign is carried by the words", got)
	}

	// Control: the ordinary refund still reads the same way, so the assertions
	// above are about totality and not about the wording having changed.
	if ordinary := activityPhrase("added", -2000); ordinary != "added a refund of $20.00" {
		t.Errorf("activityPhrase(added, -2000) = %q, want \"added a refund of $20.00\"", ordinary)
	}
}
