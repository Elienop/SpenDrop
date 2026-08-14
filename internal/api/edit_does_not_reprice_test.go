package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elienop/spendrop/internal/database"
)

// moneyOf reads the four money columns straight from the row. A decode of the
// handler's response would not do: the update handler answers
// {"status":"updated"} and every read path converts to dollars, so only the
// stored cents can settle whether an edit moved money. booked_rate is not on
// the wire at all (B10 decision 5), so raw SQL is the only way to see it.
func moneyOf(t *testing.T, h *Handler, id int64) (
	amountCents int64, origAmtCents sql.NullInt64, origCur sql.NullString, bookedRate sql.NullFloat64,
) {
	t.Helper()
	if err := h.db.QueryRow(
		`SELECT amount_cents, original_amount_cents, original_currency, booked_rate FROM transactions WHERE id = ?`, id,
	).Scan(&amountCents, &origAmtCents, &origCur, &bookedRate); err != nil {
		t.Fatalf("read money columns: %v", err)
	}
	return amountCents, origAmtCents, origCur, bookedRate
}

// setCurrencyRate changes an exchange rate the way handleUpdateCurrency does —
// rate only, every other column untouched. Written as raw SQL rather than
// through the admin handler so these tests fail for exactly one reason.
func setCurrencyRate(t *testing.T, h *Handler, code string, rate float64) {
	t.Helper()
	res, err := h.db.Exec(`UPDATE currencies SET rate_to_base = ? WHERE code = ?`, rate, code)
	if err != nil {
		t.Fatalf("set %s rate: %v", code, err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("set %s rate: %d rows affected, want 1", code, n)
	}
}

// createForeignTransaction posts a foreign-currency row through the real create
// handler, so the row under test is priced exactly the way production prices
// one: original amount and currency on the wire, no client-supplied base value.
func createForeignTransaction(
	t *testing.T, h *Handler, user database.User, catID int64, date, description, currency string, origAmount float64,
) int64 {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"date":              date,
		"original_amount":   origAmount,
		"original_currency": currency,
		"description":       description,
		"category_id":       catID,
	})
	if err != nil {
		t.Fatalf("marshal create body: %v", err)
	}
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/transactions", bytes.NewReader(body)), user)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.handleCreateTransaction(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d; body = %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	decodeResponse(t, rec, &created)
	return created.ID
}

// TestHandleUpdateTransaction_DescriptionEdit_DoesNotRepriceAtTodaysRate is the
// regression test for backlog B1.
//
// A transaction stores the foreign amount and what it was worth, but not the
// rate that produced that number. resolveCurrency divides by the CURRENT rate,
// the update handler calls it on every save, and PUT is a full replace — so the
// inline row editor resending an untouched original_amount was enough to
// re-price the row. Reproduced against production data shapes: 1,500,000 LBP
// booked at 89,000 was $16.85; after the rate moved to 100,000, editing only
// the description turned it into $15.00 with the LBP figure unchanged.
//
// Saving an edit must never move money the user did not touch.
func TestHandleUpdateTransaction_DescriptionEdit_DoesNotRepriceAtTodaysRate(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "owner", "member")
	catID := seedExpenseCategory(t, h.queries, "Groceries")

	// Seed rate: LBP 89,000 to the dollar (migration 001).
	id := createForeignTransaction(t, h, user, catID, "2026-04-06", "Spinneys", "LBP", 1_500_000)

	amountCents, origAmtCents, origCur, rate := moneyOf(t, h, id)
	if amountCents != 1685 {
		t.Fatalf("precondition: stored amount_cents = %d, want 1685 (1,500,000 / 89,000)", amountCents)
	}
	if !origAmtCents.Valid || origAmtCents.Int64 != 150_000_000 || origCur.String != "LBP" {
		t.Fatalf("precondition: stored original = (%v, %v), want (150000000, LBP)", origAmtCents, origCur)
	}
	if !rate.Valid || rate.Float64 != 89_000 {
		t.Fatalf("precondition: booked_rate = %+v, want valid 89000 — the create must record the rate it divided by", rate)
	}

	setCurrencyRate(t, h, "LBP", 100_000)

	// Exactly what the inline row editor sends for a description-only edit: the
	// stored foreign amount and currency round-tripped unchanged (toEditDefaults),
	// plus a base amount the client recomputed at today's rate (toCreatePayload).
	// The server must honour neither the new rate nor the client's arithmetic.
	rec := putTransaction(t, h, user, id, map[string]any{
		"date":              "2026-04-06",
		"amount":            15.00,
		"original_amount":   1_500_000,
		"original_currency": "LBP",
		"description":       "Spinneys — weekly shop",
		"category_id":       catID,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update: status = %d; body = %s", rec.Code, rec.Body.String())
	}

	amountCents, origAmtCents, origCur, rate = moneyOf(t, h, id)
	if amountCents != 1685 {
		t.Errorf("a description-only edit re-priced the row: amount_cents = %d, want 1685 — "+
			"the rate changed under it and the stored value followed", amountCents)
	}
	if !origAmtCents.Valid || origAmtCents.Int64 != 150_000_000 {
		t.Errorf("original_amount_cents = %v, want 150000000 — the foreign figure must survive the edit", origAmtCents)
	}
	if !origCur.Valid || origCur.String != "LBP" {
		t.Errorf("original_currency = %v, want LBP", origCur)
	}
	// The rate is frozen WITH the amount, as one fact. Letting the freshly
	// resolved 100,000 land here while amount_cents stays at the 89,000 value
	// leaves a row that claims $16.85 was 1,500,000 LBP at 100,000 — three
	// money columns describing two different bookings, and the wrong one is
	// the one a future re-price would trust.
	if !rate.Valid || rate.Float64 != 89_000 {
		t.Errorf("booked_rate = %+v, want valid 89000 — a frozen amount must keep the rate that produced it", rate)
	}

	// Not vacuous: the edit really did land.
	var desc string
	if err := h.db.QueryRow(`SELECT description FROM transactions WHERE id = ?`, id).Scan(&desc); err != nil {
		t.Fatalf("read description: %v", err)
	}
	if desc != "Spinneys — weekly shop" {
		t.Fatalf("description = %q — the edit never landed, so the money assertions above prove nothing", desc)
	}
}

// TestHandleUpdateTransaction_CorrectedForeignAmount_IsRepriced is the other
// half of the pair, and the reason the guard keys on the foreign amount rather
// than on "is this an edit".
//
// Correcting 1,500,000 LBP to 1,600,000 LBP is the user changing the money, so
// the base value has to follow. A guard that froze every edit would leave the
// row claiming a dollar figure that matches neither the old nor the new foreign
// amount — as wrong as re-pricing a description edit, and invisible in exactly
// the same way.
//
// The rate is deliberately left alone here: the recomputation must happen
// because the AMOUNT moved, not because anything else did. It uses today's rate
// because the rate the row was booked at is recorded nowhere — a known
// limitation, resolved by B1 step 2 (snapshot the rate per row).
func TestHandleUpdateTransaction_CorrectedForeignAmount_IsRepriced(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "owner", "member")
	catID := seedExpenseCategory(t, h.queries, "Groceries")

	id := createForeignTransaction(t, h, user, catID, "2026-04-06", "Spinneys", "LBP", 1_500_000)
	if amountCents, _, _, _ := moneyOf(t, h, id); amountCents != 1685 {
		t.Fatalf("precondition: stored amount_cents = %d, want 1685", amountCents)
	}

	// "amount" is deliberately nonsense: the server must derive 17.98 from the
	// foreign amount and the rate, never take the client's arithmetic. Sending
	// the right answer here would let this test pass against a server that
	// simply echoed req.Amount.
	rec := putTransaction(t, h, user, id, map[string]any{
		"date":              "2026-04-06",
		"amount":            1.00,
		"original_amount":   1_600_000,
		"original_currency": "LBP",
		"description":       "Spinneys",
		"category_id":       catID,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update: status = %d; body = %s", rec.Code, rec.Body.String())
	}

	amountCents, origAmtCents, _, rate := moneyOf(t, h, id)
	if amountCents != 1798 {
		t.Errorf("a corrected foreign amount did not re-price the row: amount_cents = %d, want 1798 "+
			"(1,600,000 / 89,000)", amountCents)
	}
	if !origAmtCents.Valid || origAmtCents.Int64 != 160_000_000 {
		t.Errorf("original_amount_cents = %v, want 160000000", origAmtCents)
	}
	// Same rate as the create here (this test deliberately leaves it alone), so
	// this pins only that a re-price still RECORDS one — the
	// *_AfterARateChange_ test below is what pins which rate it records.
	if !rate.Valid || rate.Float64 != 89_000 {
		t.Errorf("booked_rate = %+v, want valid 89000 — a re-priced row must still record what priced it", rate)
	}
}

// TestHandleUpdateTransaction_CurrencySwitch_IsRepriced pins the currency-code
// half of the predicate — one of its only two load-bearing comparisons, and
// unpinned until now.
//
// Re-denominating a row (the same 1,500,000 figure, but EUR instead of LBP) is
// a different amount of money, so the base value must be re-derived. A guard
// that compared only the original amount would match here and freeze the row at
// its LBP value while storing EUR, leaving a row whose two money columns
// describe different transactions.
func TestHandleUpdateTransaction_CurrencySwitch_IsRepriced(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "owner", "member")
	catID := seedExpenseCategory(t, h.queries, "Groceries")

	id := createForeignTransaction(t, h, user, catID, "2026-04-06", "Spinneys", "LBP", 1_500_000)
	if amountCents, _, _, _ := moneyOf(t, h, id); amountCents != 1685 {
		t.Fatalf("precondition: stored amount_cents = %d, want 1685", amountCents)
	}

	// A round rate keeps the expected value readable arithmetic rather than a
	// magic constant; the seeded EUR rate of 0.92 says nothing about the switch.
	setCurrencyRate(t, h, "EUR", 1_000)

	rec := putTransaction(t, h, user, id, map[string]any{
		"date":              "2026-04-06",
		"amount":            1.00,
		"original_amount":   1_500_000,
		"original_currency": "EUR",
		"description":       "Spinneys",
		"category_id":       catID,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update: status = %d; body = %s", rec.Code, rec.Body.String())
	}

	amountCents, origAmtCents, origCur, rate := moneyOf(t, h, id)
	if amountCents != 150_000 {
		t.Errorf("a currency switch did not re-price the row: amount_cents = %d, want 150000 "+
			"(1,500,000 / 1,000)", amountCents)
	}
	if !origCur.Valid || origCur.String != "EUR" {
		t.Errorf("original_currency = %v, want EUR", origCur)
	}
	if !origAmtCents.Valid || origAmtCents.Int64 != 150_000_000 {
		t.Errorf("original_amount_cents = %v, want 150000000 (unchanged figure, new currency)", origAmtCents)
	}
	// The stored rate has to follow the row onto its new currency. Keeping the
	// LBP 89,000 beside a EUR amount would misdescribe the booking by a factor
	// of 89, and nothing downstream could tell.
	if !rate.Valid || rate.Float64 != 1_000 {
		t.Errorf("booked_rate = %+v, want valid 1000 — the re-priced row must record the EUR rate that priced it", rate)
	}
}

// TestHandleUpdateTransaction_SwitchToBaseCurrency_UsesTheRequestAmount pins
// the base-currency arm.
//
// Moving a row off a foreign currency sends a bare `amount` with no original_*
// keys — the shape web/src/lib/currency.ts emits when currency === baseCode —
// and on that path the client's amount IS the authoritative number. A guard
// keyed on the STORED row rather than on the request's converted output would
// freeze here and silently discard the amount the user typed, leaving the row
// at its old foreign value while claiming to be in the base currency.
func TestHandleUpdateTransaction_SwitchToBaseCurrency_UsesTheRequestAmount(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "owner", "member")
	catID := seedExpenseCategory(t, h.queries, "Groceries")

	id := createForeignTransaction(t, h, user, catID, "2026-04-06", "Spinneys", "LBP", 1_500_000)
	if amountCents, _, _, _ := moneyOf(t, h, id); amountCents != 1685 {
		t.Fatalf("precondition: stored amount_cents = %d, want 1685", amountCents)
	}

	rec := putTransaction(t, h, user, id, map[string]any{
		"date":        "2026-04-06",
		"amount":      42.50,
		"description": "Spinneys",
		"category_id": catID,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update: status = %d; body = %s", rec.Code, rec.Body.String())
	}

	amountCents, origAmtCents, origCur, rate := moneyOf(t, h, id)
	if amountCents != 4250 {
		t.Errorf("amount_cents = %d, want 4250 — a base-currency edit stores the amount the user "+
			"typed, and nothing about the row's former currency may override it", amountCents)
	}
	if origAmtCents.Valid || origCur.Valid {
		t.Errorf("original_* = (%v, %v), want both NULL — the row is no longer in a foreign currency",
			origAmtCents, origCur)
	}
	// NULL means "no rate was involved", and no division happened on this
	// branch. Leaving the stale LBP 89,000 behind would make a base-currency
	// row look like it had been converted from something.
	if rate.Valid {
		t.Errorf("booked_rate = %+v, want NULL — a row priced with no conversion records no rate", rate)
	}
}

// TestHandleUpdateTransaction_RepriceAfterRateChange_StoresTheNewRate is the
// other half of the freeze/re-price pair for the rate column.
//
// The freeze test proves a carried-forward amount keeps its original rate. This
// proves the opposite arm actually moves: when the user really does change the
// foreign amount, the row is re-priced at today's rate AND records today's rate.
// A carry-forward applied to both arms would leave the row claiming a base value
// that today's rate produced and a rate that produced the old one.
func TestHandleUpdateTransaction_RepriceAfterRateChange_StoresTheNewRate(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "owner", "member")
	catID := seedExpenseCategory(t, h.queries, "Groceries")

	id := createForeignTransaction(t, h, user, catID, "2026-04-06", "Spinneys", "LBP", 1_500_000)
	if _, _, _, rate := moneyOf(t, h, id); !rate.Valid || rate.Float64 != 89_000 {
		t.Fatalf("precondition: booked_rate = %+v, want valid 89000", rate)
	}

	setCurrencyRate(t, h, "LBP", 100_000)

	rec := putTransaction(t, h, user, id, map[string]any{
		"date":              "2026-04-06",
		"amount":            1.00, // nonsense on purpose: the server derives its own
		"original_amount":   1_600_000,
		"original_currency": "LBP",
		"description":       "Spinneys",
		"category_id":       catID,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update: status = %d; body = %s", rec.Code, rec.Body.String())
	}

	amountCents, origAmtCents, _, rate := moneyOf(t, h, id)
	if amountCents != 1600 {
		t.Errorf("amount_cents = %d, want 1600 (1,600,000 / 100,000) — the user moved the money, "+
			"so the new rate applies", amountCents)
	}
	if !origAmtCents.Valid || origAmtCents.Int64 != 160_000_000 {
		t.Errorf("original_amount_cents = %v, want 160000000", origAmtCents)
	}
	if !rate.Valid || rate.Float64 != 100_000 {
		t.Errorf("booked_rate = %+v, want valid 100000 — a re-priced row records the rate that "+
			"produced its new amount, not the one it was first booked at", rate)
	}
}

// TestHandleUpdateTransaction_TagsOnlyEdit_AfterRateChange_KeepsHashAnchored
// pins the content_hash half of the same guarantee.
//
// amount_cents is one of ComputeContentHash's inputs, so TransactionStore
// clears the dedupe identity of any row whose amount moved. A rate change
// therefore used to reach further than the visible dollar figure: a tags-only
// save — which touches no hash input at all — moved the amount, cleared the
// hash, and dropped the row out of import dedupe, so re-importing the
// spreadsheet it came from would create a second copy of it. Freezing the
// amount closes both halves at once, which is why the fix belongs on the amount
// and not on a second hash predicate.
func TestHandleUpdateTransaction_TagsOnlyEdit_AfterRateChange_KeepsHashAnchored(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "owner", "member")
	catID := seedExpenseCategory(t, h.queries, "Groceries")

	id := createForeignTransaction(t, h, user, catID, "2026-04-06", "Spinneys", "LBP", 1_500_000)
	before := hashOf(t, h, id)
	if !before.Valid {
		t.Fatal("precondition failed: a manual create should anchor a content_hash")
	}

	setCurrencyRate(t, h, "LBP", 100_000)

	rec := putTransaction(t, h, user, id, map[string]any{
		"date":              "2026-04-06",
		"amount":            15.00,
		"original_amount":   1_500_000,
		"original_currency": "LBP",
		"description":       "Spinneys",
		"category_id":       catID,
		"tags":              "weekly",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update: status = %d; body = %s", rec.Code, rec.Body.String())
	}

	if amountCents, _, _, rate := moneyOf(t, h, id); amountCents != 1685 || !rate.Valid || rate.Float64 != 89_000 {
		t.Errorf("a tags-only edit re-priced the row: amount_cents = %d, booked_rate = %+v; want 1685 and 89000",
			amountCents, rate)
	}
	after := hashOf(t, h, id)
	if !after.Valid || after.String != before.String {
		t.Errorf("content_hash = %v, want it unchanged at %q — no hash input moved, so the row must "+
			"stay anchored for import dedupe", after, before.String)
	}

	// Not vacuous: the tags really did land.
	tags, _ := tagsAndNotesOf(t, h, id)
	if tags.String != "weekly" {
		t.Fatalf("tags = %v — the edit never landed, so the assertions above prove nothing", tags)
	}
}
