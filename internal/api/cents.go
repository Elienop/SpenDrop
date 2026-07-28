package api

import (
	"database/sql"
	"math"
)

// Phase 3.1: every monetary value in the database is stored as INTEGER cents
// (int64) in the *_cents columns. The legacy REAL dollar columns were dropped
// in migration 010 (Phase 3.1b), so _cents is the sole money storage — there
// is no longer any dual-population. Values convert to dollars exactly once, at
// the JSON wire edge, via centsToDollars.
//
// Why helpers instead of inline math: keeping the conversion in a single
// place makes the "convert at the wire edge" rule mechanically searchable
// - every cents->dollars call site is a callable reference to
// centsToDollars, so future migrations that narrow the conversion (e.g.,
// to a decimal type) have one chokepoint to change.
//
// Rounding: dollarsToCents uses math.Round (half-away-from-zero), matching
// the SQL backfill in migration 006 which uses SQLite's ROUND(). Both
// agree that 19.995 -> 2000 cents, not 1999, so a round-trip through the
// backfill and a subsequent edit cannot drift by one cent.

// dollarsToCents converts a float64 dollar amount to int64 cents using
// half-away-from-zero rounding.
//
// It assumes the caller has already rejected non-finite and out-of-range
// input. That assumption is NOT self-enforcing: converting NaN, +/-Inf, or
// anything past ~9.2e16 dollars to int64 is undefined in Go and lands on
// int64 minimum (-9223372036854775808) on amd64 — a silently huge NEGATIVE
// number. Prefer safeDollarsToCents at any boundary fed by user input.
func dollarsToCents(d float64) int64 {
	return int64(math.Round(d * 100))
}

// safeDollarsToCents is dollarsToCents with the precondition enforced. It
// reports false for NaN, +/-Inf, and any magnitude beyond
// MaxTransactionAmount, so an out-of-range value can never be laundered into
// int64 minimum.
//
// Measured before this existed: "1e308", "-1e308", "NaN", "Inf" and even
// "1e17" every one of them produced -9223372036854775808. On a filter that
// turned `amount_cents >= ?` into "match every row"; on a write path it
// stored a transaction with a hugely negative amount.
func safeDollarsToCents(d float64) (int64, bool) {
	if math.IsNaN(d) || math.IsInf(d, 0) {
		return 0, false
	}
	if d > MaxTransactionAmount || d < -MaxTransactionAmount {
		return 0, false
	}
	return int64(math.Round(d * 100)), true
}

// centsToDollars converts int64 cents back to float64 dollars for the
// JSON wire edge. The division is exact for any value that fits in 53
// bits of mantissa (~9e15 cents = ~9e13 dollars), which is well beyond
// MaxTransactionAmount, so the round-trip is lossless for every legal
// transaction.
func centsToDollars(c int64) float64 {
	return float64(c) / 100
}

// nullableDollarsToCents mirrors dollarsToCents for sql.NullFloat64 inputs,
// preserving the NULL pass-through. Used to convert the optional
// original_amount (foreign-currency transactions only) into the paired
// original_amount_cents column during dual-write.
func nullableDollarsToCents(n sql.NullFloat64) sql.NullInt64 {
	if !n.Valid {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: dollarsToCents(n.Float64), Valid: true}
}

// nullableCentsToDollars is the read-side counterpart: converts a nullable
// int64 cents value back into a nullable float64 dollar amount at the JSON
// wire edge. Used by response builders that surface original_amount to the
// frontend.
func nullableCentsToDollars(n sql.NullInt64) sql.NullFloat64 {
	if !n.Valid {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: centsToDollars(n.Int64), Valid: true}
}
