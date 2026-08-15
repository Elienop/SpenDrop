package api

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/elienop/spendrop/internal/database"
)

// TestConvertForeignMoney_AgreesWithResolveCurrency is the parity test for the
// single-divisor invariant: a row typed into the app and a row imported from a
// spreadsheet, carrying the same (original amount, rate) pair, must store
// byte-identical cents — forever.
//
// It asserts three things per case, and all three are needed:
//
//   - the helper returns the EXPECTED cents (a literal in the table). Without
//     this arm the test is tautological once resolveCurrency routes through the
//     helper: mutating the division would move both sides together and parity
//     would still hold. The literal is what fails when the arithmetic drifts.
//   - resolveCurrency STORES those same cents for the same pair, read through
//     the same dollarsToCents the write paths use.
//   - a refusal on one side is a refusal on the other, and for the money faults
//     it is the same message, so the two doors cannot disagree about which
//     field the user has to fix.
//
// The wire `amount` is left at 0 on purpose: it is decorative on the foreign
// branch (resolveCurrency recomputes it) and a zero is never a sign
// disagreement, so the sign gate — which belongs to the REQUEST, not to the
// conversion — stays out of the way of what is being compared here.
func TestConvertForeignMoney_AgreesWithResolveCurrency(t *testing.T) {
	// The conversions under test are pure, so one migrated database serves
	// every case; each one upserts the rate onto the same non-base code
	// before it runs. XTS is the ISO 4217 code reserved for testing.
	q, _ := setupTestDB(t)
	ctx := context.Background()

	for _, tc := range []struct {
		name     string
		original float64
		rate     float64
		// wantCents is the stored value both doors must produce. Read it only
		// when wantErr is empty and wantRateInvalid is false.
		wantCents int64
		// wantErr is the substring both doors' error messages must contain.
		wantErr string
		// wantRateInvalid marks the cases where the helper must answer with the
		// errRateInvalid sentinel. resolveCurrency wraps it with the currency
		// code, so only the sentinel — not the message — is compared there.
		wantRateInvalid bool
	}{
		{
			name:      "the household's own shape: 1,500,000 LBP at 89,000",
			original:  1_500_000,
			rate:      89_000,
			wantCents: 1685,
		},
		{
			name:      "the same shape as a refund — sign carries through the division",
			original:  -1_500_000,
			rate:      89_000,
			wantCents: -1685,
		},
		{
			name:     "a sub-cent original is refused before any division happens",
			original: 0.004,
			rate:     1,
			wantErr:  "original_amount must not be zero",
		},
		{
			// The original is rounded to cents BEFORE the division, so the
			// amount is a function of the value that will be STORED — not of
			// a figure the ledger is about to forget. 100.005 stores as
			// 10001 cents, and 100.01 ÷ 0.92 is 108.71, not the 108.70 the
			// raw value gives.
			//
			// Without the pre-round, a row like this cannot survive its own
			// export: the file states the rounded original, and re-importing
			// it computes an amount one cent away from the stored one — so
			// the row does not dedupe and imports a second time.
			name:      "a sub-cent original is rounded before it is divided",
			original:  100.005,
			rate:      0.92,
			wantCents: 10871,
		},
		{
			name:     "an in-range original the rate carries out of range",
			original: MaxTransactionAmount,
			rate:     0.5,
			wantErr:  "converted amount exceeds the maximum allowed value",
		},
		{
			name:     "a rate large enough to round the whole amount away",
			original: 1,
			rate:     1e12,
			wantErr:  "converted amount must not be zero",
		},
		{
			name:            "a zero rate cannot divide",
			original:        100,
			rate:            0,
			wantRateInvalid: true,
		},
		{
			name:            "a negative rate would flip the sign the user chose",
			original:        100,
			rate:            -1,
			wantRateInvalid: true,
		},
		{
			name:            "an infinite rate rounds every amount to nothing",
			original:        100,
			rate:            math.Inf(1),
			wantRateInvalid: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seedTestCurrency(t, q, "XTS", tc.rate, false)

			gotDollars, helperErr := convertForeignMoney(tc.original, tc.rate)

			original := tc.original
			money, resolveErr := resolveCurrency(ctx, q, transactionRequest{
				OriginalAmount:   &original,
				OriginalCurrency: "XTS",
			})

			switch {
			case tc.wantRateInvalid:
				if !errors.Is(helperErr, errRateInvalid) {
					t.Fatalf("convertForeignMoney(%v, %v) error = %v, want errRateInvalid",
						tc.original, tc.rate, helperErr)
				}
				if resolveErr == nil {
					t.Fatalf("resolveCurrency accepted rate %v and stored %v — a rate the helper "+
						"refuses must not price a row through the manual door either", tc.rate, money.Amount)
				}
				if !errors.Is(resolveErr, errRateInvalid) {
					t.Errorf("resolveCurrency error = %q, want it to wrap errRateInvalid — a caller "+
						"cannot tell an unusable rate from a bad amount without it", resolveErr)
				}
				if !strings.Contains(resolveErr.Error(), "XTS") {
					t.Errorf("resolveCurrency error = %q, want it to name the currency whose rate is "+
						"unusable — the helper cannot, so the caller must", resolveErr)
				}
			case tc.wantErr != "":
				if helperErr == nil {
					t.Fatalf("convertForeignMoney(%v, %v) = %v, want error %q",
						tc.original, tc.rate, gotDollars, tc.wantErr)
				}
				if !strings.Contains(helperErr.Error(), tc.wantErr) {
					t.Errorf("convertForeignMoney error = %q, want it to contain %q", helperErr, tc.wantErr)
				}
				if resolveErr == nil {
					t.Fatalf("resolveCurrency accepted (%v, %v) and stored %v, want error %q",
						tc.original, tc.rate, money.Amount, tc.wantErr)
				}
				if !strings.Contains(resolveErr.Error(), tc.wantErr) {
					t.Errorf("resolveCurrency error = %q, want it to contain %q — the two doors must "+
						"name the same fault", resolveErr, tc.wantErr)
				}
			default:
				if helperErr != nil {
					t.Fatalf("convertForeignMoney(%v, %v) error = %v, want %d cents",
						tc.original, tc.rate, helperErr, tc.wantCents)
				}
				if got := dollarsToCents(gotDollars); got != tc.wantCents {
					t.Errorf("convertForeignMoney(%v, %v) = %v (%d cents), want %d cents",
						tc.original, tc.rate, gotDollars, got, tc.wantCents)
				}
				if resolveErr != nil {
					t.Fatalf("resolveCurrency(%v @ %v) error = %v, want %d cents",
						tc.original, tc.rate, resolveErr, tc.wantCents)
				}
				if got := dollarsToCents(money.Amount); got != tc.wantCents {
					t.Errorf("resolveCurrency(%v @ %v) stored %d cents, want %d — the manual door "+
						"and the helper have drifted apart", tc.original, tc.rate, got, tc.wantCents)
				}
				if !money.BookedRate.Valid || money.BookedRate.Float64 != tc.rate {
					t.Errorf("booked_rate = %+v, want valid %v — the rate that divided must still be "+
						"captured by the caller", money.BookedRate, tc.rate)
				}
			}
		})
	}
}

// TestConvertForeignMoney_RejectsEveryUnusableRate covers the rate guard as a
// class rather than through the parity table, because two of these values
// cannot be reached through a currency row at all (see the NaN test below) and
// the sentinel is what later callers — import, which reads a per-row Rate cell
// with no currency row behind it — will match on.
func TestConvertForeignMoney_RejectsEveryUnusableRate(t *testing.T) {
	for _, rate := range []float64{0, -1, -89_000, math.NaN(), math.Inf(1), math.Inf(-1)} {
		got, err := convertForeignMoney(1_500_000, rate)
		if !errors.Is(err, errRateInvalid) {
			t.Errorf("convertForeignMoney(1500000, %v) = (%v, %v), want errRateInvalid", rate, got, err)
		}
	}
}

// TestErrRateInvalid_DescribesTheWholeGate pins the sentinel's wording. The
// message is the only part of this gate a user ever sees — resolveCurrency
// prefixes it with the currency code and hands it back as a 400 — and it
// shipped once saying less than the gate enforces: "a positive number" is a
// true description of +Inf, so an infinite rate was refused with a sentence
// describing a rate the user had got right.
//
// A literal, not a reference to the var: comparing errRateInvalid to itself
// would pass through any rewording, which is the exact drift this guards.
func TestErrRateInvalid_DescribesTheWholeGate(t *testing.T) {
	const want = "rate must be a positive, finite number"
	if got := errRateInvalid.Error(); got != want {
		t.Errorf("errRateInvalid = %q, want %q — the message must name both halves of the gate, "+
			"because zero, negative and infinite rates all arrive here", got, want)
	}
}

// TestConvertForeignMoney_AcceptsEveryUsableRate is the forward guard for the
// gate above: it must refuse the unusable class only. A `rate < 1` or a
// `rate != 1` slip would sail through the table's LBP case and break every
// EUR-shaped row (0.92) and every rate below one.
func TestConvertForeignMoney_AcceptsEveryUsableRate(t *testing.T) {
	for _, tc := range []struct {
		rate float64
		// wantErr says the pair is refused for the AMOUNT it produces. It is a
		// field of its own rather than a wantCents of 0, because "no cents" is
		// a legitimate-looking answer and a reader cannot tell an intended
		// refusal from a forgotten expectation by looking at a zero.
		wantErr   bool
		wantCents int64
	}{
		{rate: 1, wantCents: 10_000},                       // the base-currency rate
		{rate: 0.92, wantCents: 10_870},                    // EUR, a rate below one
		{rate: 89_000, wantErr: true},                      // LBP: 100 / 89,000 rounds to nothing
		{rate: 2, wantCents: 5_000},                        // an exact halving, no rounding involved
		{rate: math.SmallestNonzeroFloat64, wantErr: true}, // finite, positive, and far out of range
	} {
		got, err := convertForeignMoney(100, tc.rate)
		switch {
		case tc.wantErr:
			// Refused, but for the AMOUNT it produces — never as an unusable rate.
			if errors.Is(err, errRateInvalid) {
				t.Errorf("rate %v was refused as invalid; it is usable — the amount is what is wrong", tc.rate)
			}
			if err == nil {
				t.Errorf("convertForeignMoney(100, %v) = %v, want an out-of-range/zero amount error", tc.rate, got)
			}
		default:
			if err != nil {
				t.Errorf("convertForeignMoney(100, %v) error = %v, want %d cents", tc.rate, err, tc.wantCents)
				continue
			}
			if cents := dollarsToCents(got); cents != tc.wantCents {
				t.Errorf("convertForeignMoney(100, %v) = %v (%d cents), want %d cents",
					tc.rate, got, cents, tc.wantCents)
			}
		}
	}
}

// TestConvertForeignMoney_NaNRateCannotComeFromACurrencyRow documents why the
// NaN arm of the rate guard has no parity case: SQLite stores a NaN REAL as
// NULL, and currencies.rate_to_base is NOT NULL, so the row is rejected at the
// INSERT. The guard is still worth having — the import path about to call this
// helper parses its rate from a spreadsheet cell, where NaN is one strtod away
// — but nothing can smuggle one in through resolveCurrency's door, and a
// future reader should not go looking for the missing table row.
func TestConvertForeignMoney_NaNRateCannotComeFromACurrencyRow(t *testing.T) {
	q, _ := setupTestDB(t)
	err := q.UpsertCurrency(context.Background(), database.UpsertCurrencyParams{
		Code:       "XTN",
		Name:       "XTN",
		Symbol:     "$",
		RateToBase: math.NaN(),
	})
	if err == nil {
		t.Fatal("a NaN rate was stored in currencies.rate_to_base; if SQLite has started " +
			"accepting it, the parity table above needs a NaN case")
	}
}
