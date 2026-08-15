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
// The message names both halves of the gate, and it has to. "Positive" alone
// covers zero (never a divisor) and negative (it would silently flip the
// direction of the money — a purchase booked as a refund, which
// currency_handlers.go already refuses <= 0 to prevent), but +Inf IS positive,
// so a user handed "rate must be a positive number" for an infinite rate would
// read it as a description of a rate they had got right.
var errRateInvalid = errors.New("rate must be a positive, finite number")

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
// The original is rounded to the cents grid before the division, so the
// returned value is a function of what the ledger will STORE rather than of
// what the caller happened to type. See the comment at the rounding.
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

	// Divide the STORED original, not the one that was typed. amount_cents and
	// original_amount_cents are both written from this call, and the ledger
	// keeps the original on the cents grid — so deriving the amount from a
	// figure with a fraction of a cent in it produces a pair that does not
	// describe itself: 100.005 EUR at 0.92 stored 10001 cents beside an amount
	// of 10870, when 100.01 ÷ 0.92 is 108.71.
	//
	// A pair that disagrees by a cent is not a rounding curiosity, it is a
	// row that cannot survive its own export. The export writes the STORED
	// original (100.01) and the rate; re-importing that file computes 108.71,
	// which is not the 108.70 on the row, so the import reports a
	// disagreement, finds no duplicate, and — if the user takes the computed
	// amount it offers — inserts a second copy of a transaction the household
	// already has. Rounding here makes the stored triple internally
	// consistent, and the round trip an identity.
	//
	// It goes through dollarsToCents, the same chokepoint the storage edge
	// uses, so "the value that will be stored" is not a second opinion about
	// what that value is. It cannot round to zero (validateMoneyAmount above
	// has already refused an original that does) and cannot leave the range
	// (rounding moves the value by less than a cent, and the bound is a whole
	// number of dollars).
	originalAmount = centsToDollars(dollarsToCents(originalAmount))

	converted := originalAmount / rate

	// Round to 2 decimal places. dollarsToCents (the single wire-edge rounding
	// chokepoint, cents.go) re-rounds *100 on the same scale and always agrees,
	// so this line cannot change the CENTS that get stored — rounding an
	// already-rounded value is idempotent.
	//
	// It is not, however, a no-op on the accept/reject DECISION, because it runs
	// before the bound below. Measured: 500,000,000.002 at a rate of 0.5 divides
	// to 1,000,000,000.004, which this line pulls back to exactly
	// MaxTransactionAmount and the bound then admits; without it the same pair
	// is refused as out of range. Both spellings store the identical
	// 100,000,000,000 cents, so what the line decides is who gets a 400 at the
	// last four thousandths of a cent above the cap, not what any accepted row
	// is worth. Kept as it stands: the ordering is inherited from
	// resolveCurrency, and moving or dropping it would move that boundary for
	// every foreign row in the app to buy nothing.
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
