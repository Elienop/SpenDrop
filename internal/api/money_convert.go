package api

import (
	"errors"
	"math"
)

// errRateInvalid is the one answer for a rate that cannot divide: zero,
// negative, NaN or infinite. It is a sentinel rather than a formatted string
// because its callers each have their own context to add — resolveCurrency
// names the currency row, an importer names the row and column the rate came
// from — and both need errors.Is to tell "this rate is unusable" apart from
// "this amount is out of range", which arrive from the same call.
//
// The message says positive, not non-zero, because that is the whole legal
// class: a negative rate would silently flip the direction of the money (a
// purchase booked as a refund), and currency_handlers.go already refuses <= 0
// on both the create and the update path.
var errRateInvalid = errors.New("rate must be a positive number")

// convertForeignMoney divides a signed foreign amount by a rate and returns the
// base-currency value the row will store, in dollars.
//
// It exists so that a number typed into the app and the same number arriving
// through an import cannot drift apart. Both doors reach a transaction's
// amount_cents through THIS division, THIS rounding and THIS bound, so
// "1,500,000 LBP at 89,000" is 1685 cents whichever way it was entered — an
// invariant a second implementation could only preserve by accident, and
// silently, since neither door reports the rate it used.
//
// Pure: no database, no clock, no request. The caller owns everything that is
// about the REQUEST rather than the arithmetic — which currency row was
// selected, whether the wire amount agrees in sign, and capturing the rate onto
// the stored row (resolveCurrency's BookedRate).
//
// It returns, in order:
//   - the original's own validateMoneyAmount error, if the foreign figure is
//     not a storable amount to begin with;
//   - errRateInvalid, if the rate cannot divide;
//   - the converted value's validateMoneyAmount error, if the division lands
//     outside what a row may hold.
func convertForeignMoney(originalAmount, rate float64) (float64, error) {
	if err := validateMoneyAmount(originalAmount, "original_amount"); err != nil {
		return 0, err
	}

	// NaN needs its own check: it compares false against every operator, so
	// `rate <= 0` alone lets it through and 1500000/NaN is NaN, which
	// dollarsToCents converts to int64 minimum. Inf is refused here rather than
	// left to the converted-amount bound below so that every unusable rate
	// reports as a rate fault — an infinite rate makes each amount round to
	// zero, and "amount must not be zero" would send the user to fix a figure
	// that is fine.
	if math.IsNaN(rate) || math.IsInf(rate, 0) || rate <= 0 {
		return 0, errRateInvalid
	}

	converted := originalAmount / rate

	// Round to 2 decimal places. This is redundant with dollarsToCents (the
	// single wire-edge rounding chokepoint, cents.go), which re-rounds *100 on
	// the same scale and always agrees — so it is provably a no-op on the
	// stored cents. Kept deliberately to avoid churning a money path for zero
	// behavior change; see audit item h-resolvecurrency-double-round.
	converted = math.Round(converted*100) / 100

	// The division can carry an in-range original amount out of range: a small
	// rate multiplies it up. Bound the converted value too, or dollarsToCents
	// launders it into int64 minimum at the storage edge. It can equally round
	// the value INTO zero (0.001 LBP is worth no cents at all), which the same
	// gate refuses — the table's CHECK(amount_cents != 0) would otherwise
	// answer a legal-looking request with a 500.
	if err := validateMoneyAmount(converted, "converted amount"); err != nil {
		return 0, err
	}

	return converted, nil
}
