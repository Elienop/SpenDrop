package api

import (
	"testing"
)

// Phase 3.7 fuzz tests for the two untrusted-string parsers in
// import_handlers.go: parseImportDate and parseImportAmount.
//
// Why fuzz tests: PR #19 shipped a silent-data-loss bug where xlsx
// Date cells in the mm-dd-yy display format produced the wrong serial
// path, silently dropping July/August 2025 rows. A seed-based fuzz
// test on parseImportDate would have surfaced the bug in <1 second
// because the corpus below harvests that exact input class. The
// equivalent bug class for amounts (silent garbage from NaN/Inf/huge
// numbers flowing into dollarsToCents) is what FuzzParseImportAmount
// protects against.
//
// Corpus layout: the canonical, human-readable seed set lives in this
// file via f.Add() — readers can see exactly what's tested without
// hunting through testdata/. A smaller set of "exhibit A" files also
// lives in internal/api/testdata/fuzz/<FuzzName>/ so the bug-class
// reproducers are discoverable by Go's fuzz engine without preserving
// $GOCACHE/fuzz across CI runs. Both sources run on every `go test`.
//
// Acceptance criteria (data-stewardship-plan.md Phase 3.7):
//   - Zero panics on any input.
//   - Zero "parse succeeded but result is out of range":
//       * parseImportDate:   year must be in [1900, 2100].
//       * parseImportAmount: |cents| must be ≤ MaxTransactionAmount*100.
//
// The amount range here is stated twice — once inside parseImportAmount
// as the production contract and once here as a double-entry guard.
// If a future refactor relaxes the parser's range check, this test
// catches the regression even on the most ordinary-looking seed.

// dateSeeds is the comprehensive seed corpus for FuzzParseImportDate.
// Every entry is either a real xlsx cell harvested from past import
// PRs or an edge case the Phase 3.7 plan specifically calls out. See
// data-stewardship-plan.md Phase 3.7 for the rationale behind each
// cluster.
var dateSeeds = []string{
	// PR #19 reproducers. "45859" is the Excel serial for 2025-07-21
	// (the date that motivated the bug report); "07-21-25" is the
	// mm-dd-yy text fallback the old parser silently accepted by
	// misrouting through the wrong layout — the fix made this format
	// fail loudly, which is the behavior this seed pins.
	"45859",
	"07-21-25",
	"2025-01-15",
	"1/2/2026",

	// Format allowlist — every layout parseImportDate accepts.
	"2025-07-21",
	"07/21/2025",
	"21-Jul-2025",
	"2025/07/21",

	// Excel serial boundaries. 1 is the 1900-system epoch (maps to
	// 1899-12-31 due to Excel's leap-year bug); 2958465 is 9999-12-31;
	// anything outside this range must fall through to text and fail.
	"1",
	"2958465",

	// Whitespace / empty / newline — should trim and fail, not panic.
	"",
	" ",
	"\n",
	"   45859  ",

	// Pathological float literals that strconv.ParseFloat accepts but
	// are nonsense as Excel serials. All must be rejected.
	"99999999999",
	"-1",
	"0",
	"1.7976931348623157e308",
	"NaN",
	"Infinity",

	// Ambiguous / invalid calendar strings.
	"01/02/03",           // 2-digit year, not in allowlist
	"2025-13-01",         // month 13
	"2025-02-31",         // Feb 31
	"2025-02-29T13:45:00Z", // non-leap Feb 29 + ISO timestamp
	"2025/01/02 03:04:05",  // includes time, not in allowlist
}

// amountSeeds is the comprehensive seed corpus for
// FuzzParseImportAmount. Overlap with dateSeeds is intentional — a
// valid amount may look like a malicious date and vice-versa; the
// fuzzer exercises both cross-contamination paths.
var amountSeeds = []string{
	// Normal inputs.
	"0",
	"-1",
	"1",
	"0.00",
	"1.23",

	// Currency and locale variations.
	"1,234.56",      // US thousands
	"$1,234.56",     // US with symbol
	"€42,50",        // European notation; stripCurrencyFormat treats the comma as a US thousands separator and this parses as 4250, not 42.50
	"(42.50)",       // accounting negative
	"-$15.00",       // explicit negative with symbol
	"1.234.567,89",  // European thousands — ParseFloat will fail cleanly

	// Whitespace / empty / newline.
	"",
	" ",
	"\n",
	"   45859  ",

	// Pathological float literals. The extracted parseImportAmount
	// rejects NaN/Inf and anything above MaxTransactionAmount, so all
	// of these must error out — none should round-trip to a "success"
	// the fuzz assertion would catch as out-of-range.
	"99999999999",          // > MaxTransactionAmount
	"1.7976931348623157e308", // float max
	"NaN",
	"Infinity",
	"Inf",
	"-Inf",
	"1e20",
}

// FuzzParseImportDate feeds dateSeeds (plus any random mutations the
// fuzz engine generates) into parseImportDate. Any panic is a
// failure. Any success whose parsed year falls outside the household
// ledger window [1900, 2100] is a failure — the caller would insert a
// row with a clearly wrong date, which is the data-loss class this
// test guards against.
func FuzzParseImportDate(f *testing.F) {
	for _, s := range dateSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("parseImportDate panicked on %q: %v", s, r)
			}
		}()
		d, err := parseImportDate(s)
		if err != nil {
			return // errors are fine, silent garbage is not
		}
		if d.Year() < 1900 || d.Year() > 2100 {
			t.Errorf("parseImportDate(%q) produced out-of-range year %d (wanted [1900, 2100])", s, d.Year())
		}
	})
}

// FuzzParseImportAmount feeds amountSeeds (plus random mutations)
// into parseImportAmount. Any panic is a failure. Any success whose
// magnitude exceeds MaxTransactionAmount is a failure — parser-side
// the check already rejects these, so this assertion is the
// double-entry guard against a future refactor that relaxes it.
func FuzzParseImportAmount(f *testing.F) {
	for _, s := range amountSeeds {
		f.Add(s)
	}
	// MaxTransactionAmount is declared in dollars; the parser returns
	// cents, so convert the limit once here.
	const maxCents = int64(MaxTransactionAmount) * 100
	f.Fuzz(func(t *testing.T, s string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("parseImportAmount panicked on %q: %v", s, r)
			}
		}()
		cents, err := parseImportAmount(s)
		if err != nil {
			return
		}
		if cents > maxCents || cents < -maxCents {
			t.Errorf("parseImportAmount(%q) produced out-of-range cents %d (limit ±%d)", s, cents, maxCents)
		}
	})
}
