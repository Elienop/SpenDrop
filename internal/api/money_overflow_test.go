package api

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// TestSafeDollarsToCents_RejectsUnrepresentable pins the conversion contract.
// Every one of these inputs converts to int64 minimum through the unchecked
// dollarsToCents — a silently huge NEGATIVE number, not an error.
func TestSafeDollarsToCents_RejectsUnrepresentable(t *testing.T) {
	bad := []struct {
		name string
		in   float64
	}{
		{"positive overflow", 1e308},
		{"negative overflow", -1e308},
		{"NaN", math.NaN()},
		{"+Inf", math.Inf(1)},
		{"-Inf", math.Inf(-1)},
		{"past MaxTransactionAmount", MaxTransactionAmount + 1},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := safeDollarsToCents(tc.in); ok {
				t.Errorf("safeDollarsToCents(%v) accepted a value that cannot round-trip", tc.in)
			}
			// Demonstrates why the guard exists.
			if got := dollarsToCents(tc.in); got != math.MinInt64 {
				t.Logf("note: unchecked conversion of %v yielded %d", tc.in, got)
			}
		})
	}

	good := []struct {
		in   float64
		want int64
	}{
		{100.50, 10050},
		{0.01, 1},
		{MaxTransactionAmount, MaxTransactionAmount * 100},
	}
	for _, tc := range good {
		if got, ok := safeDollarsToCents(tc.in); !ok || got != tc.want {
			t.Errorf("safeDollarsToCents(%v) = (%d, %v), want (%d, true)", tc.in, got, ok, tc.want)
		}
	}
}

// TestBuildTransactionWhereClause_IgnoresOverflowingAmountBounds is the
// regression test for the inverted filter. An out-of-range amount_min used to
// become `t.amount_cents >= -9223372036854775808`, i.e. match every row.
func TestBuildTransactionWhereClause_IgnoresOverflowingAmountBounds(t *testing.T) {
	for _, bound := range []string{"amount_min", "amount_max"} {
		for _, v := range []string{"1e308", "-1e308", "NaN", "Inf"} {
			q := url.Values{}
			q.Set(bound, v)
			clause, args := buildTransactionWhereClause(q)

			for _, a := range args {
				if c, isInt := a.(int64); isInt && c == math.MinInt64 {
					t.Errorf("%s=%s produced int64 minimum as a bound (clause %q)", bound, v, clause)
				}
			}
			if len(args) != 0 {
				t.Errorf("%s=%s should have been skipped, got clause %q args %v", bound, v, clause, args)
			}
		}
	}

	// A representable bound must still work.
	q := url.Values{}
	q.Set("amount_min", "10.50")
	_, args := buildTransactionWhereClause(q)
	if len(args) != 1 || args[0].(int64) != 1050 {
		t.Errorf("a valid amount_min was dropped: args=%v", args)
	}
}

// TestHandleCreateTransaction_RejectsOverflowingConvertedAmount targets the
// one money-edge gap the existing validation missed.
//
// validateTransactionRequest already bounds req.Amount and req.OriginalAmount
// for NaN/Inf/MaxTransactionAmount. What it cannot see is the CONVERTED value:
// an original_amount comfortably inside the limit, divided by a small
// rate_to_base, lands far outside int64 range and dollarsToCents turns it into
// int64 minimum — a hugely negative stored amount.
func TestHandleCreateTransaction_RejectsOverflowingConvertedAmount(t *testing.T) {
	h := setupHandler(t)
	user := seedTestUser(t, h.queries, "owner", "member")
	catID := seedExpenseCategory(t, h.queries, "Groceries")
	seedTestCurrency(t, h.queries, "USD", 1, true)
	// A tiny rate multiplies the original amount up by 1e9 on conversion.
	seedTestCurrency(t, h.queries, "XXX", 1e-9, false)

	body, _ := json.Marshal(map[string]any{
		"date":              "2026-07-01",
		"amount":            1,
		"original_currency": "XXX",
		// Inside MaxTransactionAmount, so every existing check passes it.
		"original_amount": float64(MaxTransactionAmount),
		"description":     "converted overflow",
		"category_id":     catID,
	})
	req := withUser(httptest.NewRequest(http.MethodPost, "/api/transactions", bytes.NewReader(body)), user)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.handleCreateTransaction(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}

	// Independent of the status: nothing may have been stored, and certainly
	// nothing with a negative amount.
	counts, err := h.queries.CountAllTransactions(t.Context())
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if counts.Live != 0 {
		var cents int64
		_ = h.db.QueryRow(`SELECT amount_cents FROM transactions LIMIT 1`).Scan(&cents)
		t.Errorf("an overflowing converted amount was stored: %d live rows, amount_cents=%d",
			counts.Live, cents)
	}
}
