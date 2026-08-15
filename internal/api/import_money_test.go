package api

import (
	"database/sql"
	"math"
	"testing"

	"github.com/elienop/spendrop/internal/database"
)

// testImportCurrencies is the household's currencies table as migration 001
// seeds it: USD base, LBP at 89,000, EUR at 0.92. Every matrix case below is
// resolved against this snapshot, and the codes are quoted in MIXED case on
// purpose — the sheet's cell is whatever the user typed, and the stored value
// has to be the canonical one.
func testImportCurrencies() importCurrencies {
	return newImportCurrencies([]database.Currency{
		{Code: "USD", Name: "US Dollar", Symbol: "$", RateToBase: 1, IsBase: true},
		{Code: "LBP", Name: "Lebanese Pound", Symbol: "ل.ل", RateToBase: 89000},
		{Code: "EUR", Name: "Euro", Symbol: "€", RateToBase: 0.92},
	})
}

// TestResolveImportMoney_Matrix walks the design's per-row semantics table
// (§2, rows #1–#12), one case per row, asserting BOTH halves of the contract:
// what a usable row will STORE, and — for a blocked row — which field is
// flagged, with which server-authored sentence, and which last-ditch skip
// reason guards confirm behind it.
//
// The message is asserted verbatim rather than by substring. Four surfaces
// render these strings (upload, PATCH, GET, confirm's 409) and the frontend
// composes none of them, so a reworded message is a wire change; a substring
// assertion would let one drift while claiming the family is pinned.
func TestResolveImportMoney_Matrix(t *testing.T) {
	cur := testImportCurrencies()

	cases := []struct {
		name string
		row  importRow

		wantAmountCents int64
		wantOriginal    sql.NullInt64
		wantCurrency    sql.NullString
		wantRate        sql.NullFloat64
		wantDerived     bool

		wantField   string // "" when the row is usable
		wantMessage string
		wantReason  importSkipReason
	}{
		{
			// #1 — a plain base-currency row. Nothing about this shape may
			// change: it is what every existing sheet is made of.
			name:            "#1 usd only",
			row:             importRow{Amount: 42.50},
			wantAmountCents: 4250,
		},
		{
			// #2 — a label row. The sheet quoted no rate, so none is booked;
			// the code is stored canonicalised even though the cell said "lbp".
			name: "#2 usd and original, no rate, is a label",
			row: importRow{
				Amount:           16.85,
				OriginalAmount:   1500000,
				OriginalCurrency: "lbp",
			},
			wantAmountCents: 1685,
			wantOriginal:    sql.NullInt64{Int64: 150000000, Valid: true},
			wantCurrency:    sql.NullString{String: "LBP", Valid: true},
		},
		{
			// #3 — the rate is the source of the USD.
			name: "#3 original and rate derive the amount",
			row: importRow{
				OriginalAmount:   1500000,
				OriginalCurrency: "LbP",
				Rate:             89000,
				RawRate:          "89000",
			},
			wantAmountCents: 1685,
			wantOriginal:    sql.NullInt64{Int64: 150000000, Valid: true},
			wantCurrency:    sql.NullString{String: "LBP", Valid: true},
			wantRate:        sql.NullFloat64{Float64: 89000, Valid: true},
			wantDerived:     true,
		},
		{
			// #3 with a refund: the sign comes from the original, and the
			// implied rate stays positive.
			name: "#3 negative original derives negative cents",
			row: importRow{
				OriginalAmount:   -1500000,
				OriginalCurrency: "LBP",
				Rate:             89000,
				RawRate:          "89000",
			},
			wantAmountCents: -1685,
			wantOriginal:    sql.NullInt64{Int64: -150000000, Valid: true},
			wantCurrency:    sql.NullString{String: "LBP", Valid: true},
			wantRate:        sql.NullFloat64{Float64: 89000, Valid: true},
			wantDerived:     true,
		},
		{
			// #4 equal — the sheet's own USD agrees to the cent, so the row
			// resolves exactly like #3 and the rate is booked.
			name: "#4 usd agreeing with original over rate",
			row: importRow{
				Amount:           16.85,
				OriginalAmount:   1500000,
				OriginalCurrency: "LBP",
				Rate:             89000,
				RawRate:          "89000",
			},
			wantAmountCents: 1685,
			wantOriginal:    sql.NullInt64{Int64: 150000000, Valid: true},
			wantCurrency:    sql.NullString{String: "LBP", Valid: true},
			wantRate:        sql.NullFloat64{Float64: 89000, Valid: true},
			wantDerived:     true,
		},
		{
			// #4 off by one cent — the whole point of the check. One cent is
			// the smallest disagreement that exists, so it is the one worth
			// pinning: a tolerance of any width would swallow it.
			name: "#4 usd one cent off the derived value",
			row: importRow{
				Amount:           16.84,
				OriginalAmount:   1500000,
				OriginalCurrency: "LBP",
				Rate:             89000,
				RawRate:          "89000",
			},
			wantField:   importFieldAmount,
			wantMessage: "16.84 ≠ 1,500,000 ÷ 89,000 = 16.85. Fix the amount, the original or the rate — SpenDrop stores what the rate produces.",
			wantReason:  skipReasonAmountDisagrees,
		},
		{
			// #5 — a foreign original with no rate. Never a silent fallback:
			// today's rate is OFFERED in the sentence, not applied.
			name: "#5 original without a rate",
			row: importRow{
				OriginalAmount:   1500000,
				OriginalCurrency: "LBP",
			},
			wantField:   importFieldRate,
			wantMessage: "No rate for 1,500,000 LBP — enter the rate this row was booked at, or apply today's 89,000.",
			wantReason:  skipReasonRateMissing,
		},
		{
			// #6 — an unknown code blocks regardless of what else the row
			// carries, because nothing can be decided about money in a
			// currency the household has not set up.
			name: "#6 unknown currency",
			row: importRow{
				Amount:           16.85,
				OriginalAmount:   1500000,
				OriginalCurrency: "LBX",
				Rate:             89000,
				RawRate:          "89000",
			},
			wantField:   importFieldOriginalCurrency,
			wantMessage: "LBX isn't set up — add it under Settings → Currencies.",
			wantReason:  skipReasonUnknownCurrency,
		},
		{
			// #7 — the base currency named explicitly. Parity with the API's
			// IsBase branch: the row stores no original at all, which is a
			// deliberate change from today's verbatim label.
			name: "#7 base currency label collapses",
			row: importRow{
				Amount:           42.50,
				OriginalAmount:   42.50,
				OriginalCurrency: "usd",
			},
			wantAmountCents: 4250,
		},
		{
			// #8 — a rate on the base currency converts nothing.
			name: "#8 rate on the base currency",
			row: importRow{
				Amount:           42.50,
				OriginalCurrency: "USD",
				Rate:             2,
				RawRate:          "2",
			},
			wantField:   importFieldRate,
			wantMessage: "USD is the base currency, so a rate does nothing here. Clear the rate, or name the currency this row was really in.",
			wantReason:  skipReasonRateOnBase,
		},
		{
			// #9 — an unparseable rate cell. Distinct from #5: the fix is to
			// correct a value the user already typed, not to supply one.
			name: "#9 unparseable rate",
			row: importRow{
				Amount:           16.85,
				OriginalAmount:   1500000,
				OriginalCurrency: "LBP",
				RawRate:          "abc",
			},
			wantField:   importFieldRate,
			wantMessage: "That rate is not a positive, finite number. Enter the rate this row was booked at, or clear the cell.",
			wantReason:  skipReasonRateInvalid,
		},
		{
			// #10 — a populated rate with nothing to convert.
			name: "#10 rate with nothing to apply",
			row: importRow{
				Rate:    89000,
				RawRate: "89000",
			},
			wantField:   importFieldRate,
			wantMessage: "This row has a rate but nothing to convert — a rate needs both an original amount and an original currency. Add them, or clear the rate.",
			wantReason:  skipReasonRateWithoutCurrency,
		},
		{
			// #10 again, reached from the other side: an original amount with
			// a rate but no currency naming what the rate converts.
			name: "#10 original and rate without a currency",
			row: importRow{
				OriginalAmount: 1500000,
				Rate:           89000,
				RawRate:        "89000",
			},
			wantField:   importFieldRate,
			wantMessage: "This row has a rate but nothing to convert — a rate needs both an original amount and an original currency. Add them, or clear the rate.",
			wantReason:  skipReasonRateWithoutCurrency,
		},
		{
			// #9 again, from the half a "positive number" test would miss: a
			// cell reading 1e999 parses to +Inf, which IS positive. It must
			// report as an unusable RATE — not be treated as absent (which
			// would silently make the row a #5 or a #2), and not be blamed on
			// the amount (an infinite rate rounds every figure to zero, and
			// the figures are fine).
			name: "#9 infinite rate",
			row: importRow{
				OriginalAmount:   1500000,
				OriginalCurrency: "LBP",
				Rate:             math.Inf(1),
				RawRate:          "1e999",
			},
			wantField:   importFieldRate,
			wantMessage: "That rate is not a positive, finite number. Enter the rate this row was booked at, or clear the cell.",
			wantReason:  skipReasonRateInvalid,
		},
		{
			// #12's FIRST half: the original is not a storable figure to begin
			// with, so no division happens and the message must not claim one
			// did. 1,000,000,001 ÷ 89,000 is $11,236 — perfectly storable — so
			// a sentence blaming the quotient would be arithmetically false.
			//
			// The converter checks the original before the rate, so this case
			// is also what stops the row's fault being read off whichever
			// error the helper happened to return.
			name: "#12 original amount out of range",
			row: importRow{
				OriginalAmount:   MaxTransactionAmount + 1,
				OriginalCurrency: "LBP",
				Rate:             89000,
				RawRate:          "89000",
			},
			wantField:   importFieldAmount,
			wantMessage: "That original amount is not a figure SpenDrop can store — it has to be at least one cent and no more than 1,000,000,000 in its own currency. Fix the original amount.",
			wantReason:  skipReasonAmountInvalid,
		},
		{
			// #12, and the shape that actually reaches production: the upload
			// parser ZEROES an Original Amount cell it cannot use (2 billion
			// LBP is past MaxTransactionAmount in its own currency), so by the
			// time the resolver sees the row the figure is gone and only the
			// raw cell says the sheet ever stated one.
			//
			// Without the raw, this row is indistinguishable from "no original
			// at all" and gets diagnosed as rate_without_currency — "nothing to
			// convert", about a sheet that plainly has both halves.
			name: "#12 original amount cell the parser could not use",
			row: importRow{
				OriginalCurrency:  "LBP",
				RawOriginalAmount: "2000000000",
				Rate:              89000,
				RawRate:           "89000",
			},
			wantField:   importFieldAmount,
			wantMessage: "That original amount is not a figure SpenDrop can store — it has to be at least one cent and no more than 1,000,000,000 in its own currency. Fix the original amount.",
			wantReason:  skipReasonAmountInvalid,
		},
		{
			// The same row with its rate cleared. It must stay a diagnosis of
			// the ORIGINAL cell — the old behaviour let it fall through to
			// zero_amount, a silent skip of a row the sheet filled in.
			name: "#12 unusable original with no rate is still the original's fault",
			row: importRow{
				OriginalCurrency:  "LBP",
				RawOriginalAmount: "2000000000",
			},
			wantField:   importFieldAmount,
			wantMessage: "That original amount is not a figure SpenDrop can store — it has to be at least one cent and no more than 1,000,000,000 in its own currency. Fix the original amount.",
			wantReason:  skipReasonAmountInvalid,
		},
		{
			// #10's third arm: a currency and a rate, and no original for the
			// rate to convert. The raw original cell is EMPTY, which is what
			// separates this from the two cases above — absent, not wrong.
			name: "#10 currency and rate with no original",
			row: importRow{
				Amount:           16.85,
				OriginalCurrency: "LBP",
				Rate:             89000,
				RawRate:          "89000",
			},
			wantField:   importFieldRate,
			wantMessage: "This row has a rate but nothing to convert — a rate needs both an original amount and an original currency. Add them, or clear the rate.",
			wantReason:  skipReasonRateWithoutCurrency,
		},
		{
			// A currency named with no foreign money behind it stores NO
			// original at all — the same collapse as #7. A lone
			// original_currency beside a NULL original_amount_cents is the
			// half-pair shape the app treats as corruption and strips on the
			// next save, so import must not create it.
			name: "bare currency with nothing behind it collapses",
			row: importRow{
				Amount:           42.50,
				OriginalCurrency: "lbp",
			},
			wantAmountCents: 4250,
		},
		{
			// #12 lower bound — the division rounds away to nothing, which
			// the ledger's CHECK(amount_cents != 0) would answer with a 500.
			name: "#12 derived amount rounds to zero",
			row: importRow{
				OriginalAmount:   1,
				OriginalCurrency: "LBP",
				Rate:             89000,
				RawRate:          "89000",
			},
			wantField:   importFieldAmount,
			wantMessage: "1 ÷ 89,000 is not an amount SpenDrop can store — it has to be at least one cent and no more than 1,000,000,000. Fix the original amount or the rate.",
			wantReason:  skipReasonAmountInvalid,
		},
		{
			// #12 upper bound — a small rate multiplies an in-range original
			// out of range.
			name: "#12 derived amount exceeds the ledger bound",
			row: importRow{
				OriginalAmount:   1_000_000_000,
				OriginalCurrency: "EUR",
				Rate:             0.5,
				RawRate:          "0.5",
			},
			wantField:   importFieldAmount,
			wantMessage: "1,000,000,000 ÷ 0.5 is not an amount SpenDrop can store — it has to be at least one cent and no more than 1,000,000,000. Fix the original amount or the rate.",
			wantReason:  skipReasonAmountInvalid,
		},
		{
			// The EUR fixture earns its place: a sub-1 rate MULTIPLIES, and a
			// resolver that assumed rates are always large would still pass
			// every LBP case above.
			name: "#3 with a sub-one rate multiplies",
			row: importRow{
				OriginalAmount:   92,
				OriginalCurrency: "eur",
				Rate:             0.92,
				RawRate:          "0.92",
			},
			wantAmountCents: 10000,
			wantOriginal:    sql.NullInt64{Int64: 9200, Valid: true},
			wantCurrency:    sql.NullString{String: "EUR", Valid: true},
			wantRate:        sql.NullFloat64{Float64: 0.92, Valid: true},
			wantDerived:     true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			money, fieldErr, reason := resolveImportMoney(tc.row, cur)

			if tc.wantField == "" {
				if fieldErr != nil {
					t.Fatalf("resolveImportMoney flagged %+v, want a usable row", *fieldErr)
				}
				if reason != "" {
					t.Fatalf("resolveImportMoney returned reason %q, want none for a usable row", reason)
				}
				if money.AmountCents != tc.wantAmountCents {
					t.Errorf("AmountCents = %d, want %d", money.AmountCents, tc.wantAmountCents)
				}
				if money.OriginalAmountCents != tc.wantOriginal {
					t.Errorf("OriginalAmountCents = %+v, want %+v", money.OriginalAmountCents, tc.wantOriginal)
				}
				if money.OriginalCurrency != tc.wantCurrency {
					t.Errorf("OriginalCurrency = %+v, want %+v", money.OriginalCurrency, tc.wantCurrency)
				}
				if money.BookedRate != tc.wantRate {
					t.Errorf("BookedRate = %+v, want %+v", money.BookedRate, tc.wantRate)
				}
				if money.Derived != tc.wantDerived {
					t.Errorf("Derived = %v, want %v", money.Derived, tc.wantDerived)
				}
				return
			}

			if fieldErr == nil {
				t.Fatalf("resolveImportMoney returned no flag, want %s: %q (money=%+v)",
					tc.wantField, tc.wantMessage, money)
			}
			if fieldErr.Field != tc.wantField {
				t.Errorf("Field = %q, want %q", fieldErr.Field, tc.wantField)
			}
			if fieldErr.Message != tc.wantMessage {
				t.Errorf("Message  = %q\nwant     = %q", fieldErr.Message, tc.wantMessage)
			}
			if reason != tc.wantReason {
				t.Errorf("reason = %q, want %q", reason, tc.wantReason)
			}
			// A blocked row must carry nothing that could be stored: the
			// caller decides on the flag, and a half-filled importMoney beside
			// it invites a path that inserts anyway.
			if money != (importMoney{}) {
				t.Errorf("blocked row returned money %+v, want the zero value", money)
			}
		})
	}
}

// TestResolveImportMoney_FlagCarriesTheRowID pins the one field the matrix
// above cannot see: every case there uses RowID 0, so a resolver that hard-
// coded the id would look correct. The frontend routes a flag to a cell by
// row_id, so a wrong one flags the wrong row.
func TestResolveImportMoney_FlagCarriesTheRowID(t *testing.T) {
	row := importRow{RowID: 7, OriginalAmount: 1500000, OriginalCurrency: "LBP"}
	_, fieldErr, _ := resolveImportMoney(row, testImportCurrencies())
	if fieldErr == nil {
		t.Fatal("expected a rate_missing flag")
	}
	if fieldErr.RowID != 7 {
		t.Errorf("RowID = %d, want 7", fieldErr.RowID)
	}
}

// TestResolveImportMoney_RateMissingWithoutAUsableTodaysRate covers the offer
// half of #5's sentence. A currency row may carry a rate of 0 (the table
// allows it and the app's own currency picker treats it as "no rate known"),
// and offering "apply today's 0" would send the user to apply a divisor the
// converter refuses.
func TestResolveImportMoney_RateMissingWithoutAUsableTodaysRate(t *testing.T) {
	cur := newImportCurrencies([]database.Currency{
		{Code: "USD", RateToBase: 1, IsBase: true},
		{Code: "ZRO", RateToBase: 0},
	})
	row := importRow{OriginalAmount: 1500, OriginalCurrency: "zro"}

	_, fieldErr, reason := resolveImportMoney(row, cur)
	if fieldErr == nil {
		t.Fatal("expected a rate_missing flag")
	}
	if reason != skipReasonRateMissing {
		t.Errorf("reason = %q, want %q", reason, skipReasonRateMissing)
	}
	want := "No rate for 1,500 ZRO — enter the rate this row was booked at."
	if fieldErr.Message != want {
		t.Errorf("Message = %q\nwant    = %q", fieldErr.Message, want)
	}
}

// TestParseImportRate pins the parser that decides #5 from #9. The empty
// string is the load-bearing case: it must be ABSENCE (0, nil), never an
// error, because a sheet with no Rate column has to keep importing exactly
// as it does today.
func TestParseImportRate(t *testing.T) {
	cases := []struct {
		in      string
		want    float64
		wantErr bool
	}{
		{in: "", want: 0},
		{in: "   ", want: 0},
		{in: "89000", want: 89000},
		{in: "89,000", want: 89000},
		{in: "89,000.5", want: 89000.5},
		{in: "0.92", want: 0.92},
		{in: " 1 ", want: 1},
		{in: "0", wantErr: true},
		{in: "-1", wantErr: true},
		{in: "(89000)", wantErr: true}, // accounting negative
		{in: "abc", wantErr: true},
		{in: "89%", wantErr: true},
		{in: "1e400", wantErr: true},
		{in: "1e999", wantErr: true}, // parses to +Inf: positive, and unusable
		{in: "NaN", wantErr: true},
		{in: "Inf", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := parseImportRate(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseImportRate(%q) = %v, want an error", tc.in, got)
				}
				if got != 0 {
					t.Errorf("parseImportRate(%q) returned %v alongside its error, want 0", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseImportRate(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("parseImportRate(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestImportCurrencies_LookupIsCaseInsensitiveAndCanonical pins the property
// that makes freeze-on-edit work later: store.go's foreignMagnitudeUnchanged
// compares the currency code EXACTLY, so a row stored as "lbp" could never
// freeze against a "LBP" edit.
func TestImportCurrencies_LookupIsCaseInsensitiveAndCanonical(t *testing.T) {
	cur := testImportCurrencies()

	for _, spelling := range []string{"LBP", "lbp", "Lbp", " lbp ", "lBp"} {
		got, ok := cur.lookup(spelling)
		if !ok {
			t.Fatalf("lookup(%q) missed a currency the household has", spelling)
		}
		if got.Code != "LBP" {
			t.Errorf("lookup(%q).Code = %q, want the canonical %q", spelling, got.Code, "LBP")
		}
	}

	if _, ok := cur.lookup("LBX"); ok {
		t.Error("lookup(\"LBX\") found a currency the household has not set up")
	}
	if _, ok := cur.lookup(""); ok {
		t.Error("lookup(\"\") resolved; an empty cell names no currency")
	}
}

// TestPreCategorySkipReason_MoneyRules covers the two rules the matrix cannot
// state on its own, because both are decided by the caller: sign_mismatch is
// evaluated BEFORE money resolution, and zero_amount now means "no usable
// money at all" rather than "the USD cell is zero".
func TestPreCategorySkipReason_MoneyRules(t *testing.T) {
	cur := testImportCurrencies()

	t.Run("sign mismatch beats a money flag", func(t *testing.T) {
		// The pair contradicts itself AND the sheet's USD disagrees with the
		// derived value, so both gates would fire. The sign gate is first:
		// the row is a contradiction, and blocking it on the amount would ask
		// the user to fix the consequence rather than the cause.
		row := importRow{
			Date:             "2026-01-15",
			Description:      "Refund",
			Amount:           16.85,
			OriginalAmount:   -1500000,
			OriginalCurrency: "LBP",
			Rate:             89000,
			RawRate:          "89000",
		}
		_, _, reason, blocked := preCategorySkipReason(row, cur)
		if !blocked {
			t.Fatal("expected the row to be blocked")
		}
		if reason != skipReasonSignMismatch {
			t.Errorf("reason = %q, want %q", reason, skipReasonSignMismatch)
		}
	})

	t.Run("no usd and no rate is rate_missing, not zero_amount", func(t *testing.T) {
		row := importRow{
			Date:             "2026-01-15",
			Description:      "Groceries",
			OriginalAmount:   1500000,
			OriginalCurrency: "LBP",
		}
		_, _, reason, blocked := preCategorySkipReason(row, cur)
		if !blocked {
			t.Fatal("expected the row to be blocked")
		}
		if reason != skipReasonRateMissing {
			t.Errorf("reason = %q, want %q", reason, skipReasonRateMissing)
		}
	})

	t.Run("no usd but a rate resolves and is not zero_amount", func(t *testing.T) {
		row := importRow{
			Date:             "2026-01-15",
			Description:      "Groceries",
			OriginalAmount:   1500000,
			OriginalCurrency: "LBP",
			Rate:             89000,
			RawRate:          "89000",
		}
		_, money, reason, blocked := preCategorySkipReason(row, cur)
		if blocked {
			t.Fatalf("row blocked as %q, want it resolved through the rate", reason)
		}
		if money.AmountCents != 1685 {
			t.Errorf("AmountCents = %d, want 1685", money.AmountCents)
		}
	})

	t.Run("nothing at all is still zero_amount", func(t *testing.T) {
		row := importRow{Date: "2026-01-15", Description: "Header junk"}
		_, _, reason, blocked := preCategorySkipReason(row, cur)
		if !blocked {
			t.Fatal("expected the row to be blocked")
		}
		if reason != skipReasonZeroAmount {
			t.Errorf("reason = %q, want %q", reason, skipReasonZeroAmount)
		}
	})
}
