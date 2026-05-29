package api

import (
	"fmt"
	"time"
)

// Phase 3.6 metamorphic reference implementation.
//
// DO NOT call from production handlers; this is the metamorphic reference
// implementation.
//
// dumbIncomeExpenses reimplements handleReportIncomeExpenses in pure Go
// against an in-process []dumbRow, with no SQL, no maps keyed on query
// rows, and no performance cleverness. The purpose is that a
// randomized metamorphic test can run this side-by-side with the real
// SQL-backed handler and assert bit-for-bit equivalence on the JSON
// entries. Any disagreement points at one of three places:
//
//   - the SQL window predicate in SumByMonthRange (the Phase 3.6
//     date() wrapper bug — RFC3339 storage vs lexical compare — is the
//     original driver for this test),
//   - the sqlc-generated parameter binding or result scan, or
//   - the Go-side bucket-building loop in the handler.
//
// The implementation is deliberately the simplest code that could
// possibly be correct. O(len(rows) * months) on 20-row samples is
// trivial and the scrutability pays off every time a future reviewer
// has to audit the test's reference.

// dumbRow is the minimal slice of a transaction that the metamorphic
// reference implementation needs: a date (time.Time, to match how sqlc
// decodes the DATE column), an amount in cents, the category type
// ("income" or "expense"), and whether the row is soft-deleted.
//
// No foreign-key ID, no user ID, no description, no tags — those exist
// in the real schema but do not influence the income-vs-expense
// rollup. Keeping this struct narrow makes the rapid generator much
// simpler and guarantees the dumb implementation cannot accidentally
// branch on a field that the handler ignores.
type dumbRow struct {
	Date         time.Time
	AmountCents  int64
	CategoryType string // "income" or "expense"
	Deleted      bool
}

// dumbIncomeExpenses is the metamorphic reference for
// handleReportIncomeExpenses. Given the same `now` the real handler
// reads from its clock and the same `months` the request supplied, it
// walks `rows` directly in Go, mirroring the SQL query plus the Go-side
// bucket-building loop one-to-one.
//
// Contract (must match reports_handlers.go:handleReportIncomeExpenses):
//
//  1. Window lower bound: first day of the month that is months-1
//     calendar months before the first day of `now`'s month, formatted
//     "YYYY-MM-01". `now` is normalized to first-of-month before any
//     AddDate so a day>=29 anchor cannot overflow a shorter target month.
//  2. Window upper bound: last day of `now`'s month, computed as
//     time.Date(year, month+1, 0, ...) and formatted "YYYY-MM-DD".
//  3. A row is in-window iff its date-only representation (NOT its
//     RFC3339 timestamp) sorts into [lowerBound, upperBound]
//     inclusively. This is the exact semantics the handler's
//     date(t.date) wrapper is meant to enforce — treating the stored
//     column as a pure date regardless of how the driver encodes it.
//  4. Soft-deleted rows (Deleted == true) are skipped unconditionally.
//  5. Rows are grouped by (year, month) and by category type; income
//     amounts sum into b.incomeCents, expense amounts sum into
//     b.expenseCents. No other category types are possible — the
//     categories table has a CHECK constraint — so any other value in
//     the test generator is a bug, not runtime data.
//  6. The output has exactly `months` entries, ordered earliest month
//     first, using the same `base.AddDate(0, -i, 0)` loop the handler
//     does (where base is the first day of `now`'s month), so the slot
//     list is bit-for-bit identical.
//  7. Dollars are computed once at the wire edge via centsToDollars,
//     exactly as the handler does, so a JSON round-trip produces
//     identical float values on both sides.
//
// DO NOT call from production handlers; this is the metamorphic
// reference implementation.
func dumbIncomeExpenses(rows []dumbRow, now time.Time, months int) []incomeExpenseEntry {
	// Mirror the handler's window construction exactly. First normalize to
	// the first day of `now`'s month so AddDate cannot overflow a short
	// month (the handler does the same — see reports_handlers.go). earliest
	// is `months-1` calendar months before that base, the first day of that
	// month becomes the inclusive lower bound, and the last day of `now`'s
	// month becomes the inclusive upper bound. Using %d-%02d-01 instead of
	// time.Format so the string is byte-for-byte identical to the handler's
	// fmt.Sprintf call on the same inputs.
	base := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	earliest := base.AddDate(0, -(months - 1), 0)
	dateFrom := fmt.Sprintf("%d-%02d-01", earliest.Year(), earliest.Month())
	// time.Date(y, m+1, 0, ...) rolls the day-zero of the next month
	// back to the last day of this month, independent of month length
	// or leap years — standard Go idiom that the handler also uses.
	dateTo := time.Date(base.Year(), base.Month()+1, 0, 0, 0, 0, 0, time.UTC).Format("2006-01-02")

	type key struct{ y, m int }
	type bucket struct {
		incomeCents  int64
		expenseCents int64
	}
	buckets := make(map[key]bucket)

	for _, r := range rows {
		if r.Deleted {
			continue
		}
		// Date-only comparison. This is the semantics the test is
		// enforcing: the query MUST behave as if every row's date
		// were a pure YYYY-MM-DD string, regardless of how the
		// driver happens to encode it on the wire. Formatting each
		// row here — instead of parsing dateFrom/dateTo to time.Time
		// — mirrors the semantics of SQLite's date() function
		// exactly: normalize both sides to "YYYY-MM-DD" and do a
		// lexical compare.
		d := r.Date.Format("2006-01-02")
		if d < dateFrom || d > dateTo {
			continue
		}
		k := key{y: r.Date.Year(), m: int(r.Date.Month())}
		b := buckets[k]
		switch r.CategoryType {
		case "income":
			b.incomeCents += r.AmountCents
		case "expense":
			b.expenseCents += r.AmountCents
		default:
			// Unreachable in production (categories.type has a CHECK
			// constraint). A test generator that emits a different
			// value is a bug in the test, not runtime data, so we
			// panic hard rather than silently bucketing it somewhere.
			panic(fmt.Sprintf("dumbIncomeExpenses: unknown category type %q", r.CategoryType))
		}
		buckets[k] = b
	}

	entries := make([]incomeExpenseEntry, 0, months)
	for i := months - 1; i >= 0; i-- {
		t := base.AddDate(0, -i, 0)
		y, m := t.Year(), int(t.Month())
		entry := incomeExpenseEntry{Year: y, Month: m}
		if b, ok := buckets[key{y, m}]; ok {
			// Compute net in cents (int64) before converting so the
			// subtraction is exact, then convert once at the edge.
			// Matches reports_handlers.go:224 exactly.
			netCents := b.incomeCents - b.expenseCents
			entry.Income = centsToDollars(b.incomeCents)
			entry.Expenses = centsToDollars(b.expenseCents)
			entry.Net = centsToDollars(netCents)
		}
		entries = append(entries, entry)
	}
	return entries
}
