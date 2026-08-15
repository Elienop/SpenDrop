package api

import (
	"testing"
	"unicode/utf8"
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
	"01/02/03",             // 2-digit year, not in allowlist
	"2025-13-01",           // month 13
	"2025-02-31",           // Feb 31
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
	"1,234.56",     // US thousands
	"$1,234.56",    // US with symbol
	"€42,50",       // European notation; the comma is not in grouping position, so this is REFUSED rather than read as 4250
	"(42.50)",      // accounting negative
	"-$15.00",      // explicit negative with symbol
	"1.234.567,89", // European thousands — refused, both by the comma rule and by ParseFloat
	"0,92",         // a decimal comma: refused, not read as 92
	"1,5",          // likewise
	"1,00,000",     // mis-grouped
	"1,500,000.50", // the accepted shape: groups of exactly three, comma-free fraction

	// Whitespace / empty / newline.
	"",
	" ",
	"\n",
	"   45859  ",

	// Pathological float literals. The extracted parseImportAmount
	// rejects NaN/Inf and anything above MaxTransactionAmount, so all
	// of these must error out — none should round-trip to a "success"
	// the fuzz assertion would catch as out-of-range.
	"99999999999",            // > MaxTransactionAmount
	"1.7976931348623157e308", // float max
	"NaN",
	"Infinity",
	"Inf",
	"-Inf",
	"1e20",

	// The same magnitudes on the NEGATIVE side. These are not redundant:
	// the parser's bound is `math.Abs(parsed) > MaxTransactionAmount`, and
	// until B10 every seed above the limit was positive — so relaxing the
	// bound to a one-sided `parsed > MaxTransactionAmount` passed the whole
	// suite (measured: that mutant survived). An unbounded negative is the
	// worse half of the pair, because dollarsToCents overflows int64 into
	// -9223372036854775808 and, since B10 preserves the sign, that value now
	// lands in amount_cents instead of being flattened by math.Abs on the
	// way in. See the measurement in internal/api/cents.go's
	// safeDollarsToCents comment for the same failure on other paths.
	"-99999999999",
	"-1e20",
	"-1.7976931348623157e308",
	"(99999999999)", // accounting negative above the limit
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
// rateSeeds is the seed corpus for FuzzParseImportRate. It is short because
// the parser's job is narrow, and every entry names a class rather than a
// value: the empty cell (ABSENCE, and the one input that must return no error
// AND no rate), household formatting, the three non-positive shapes, the two
// non-finite ones a spreadsheet can actually produce, Go literal syntax that
// strconv accepts and no sheet writes, non-ASCII digits, and the smallest
// subnormal — which is positive, finite, usable, and unrenderable at fixed
// decimals.
var rateSeeds = []string{
	"", "   ",
	"89000", "89,000", "$89,000", "89,000.5", "0.92", ".5", "1.",
	"0", "-1", "(89000)",
	"NaN", "Inf", "1e999", "1e400",
	"0x1p10", "1_000", "١٢٣", "89%",
	"5e-324", "1e-7", "1e300",
	// The comma rule: grouping position only. "0,92" is a decimal comma to
	// half the world and was read as 92 — a hundredfold error booked onto
	// the row for good.
	"0,92", "1,5", "1,00,000", "1,500,000.50", "1.234,56", ",92", "1,",
}

// FuzzParseImportRate feeds rateSeeds (plus mutations) into the rate parser.
//
// The invariant is the one the whole stage rests on: a rate the parser
// ACCEPTS is either absence (0, from an empty cell) or a divisor
// convertForeignMoney can use. Anything else — a zero, a negative, a NaN, an
// infinity — reaching a caller as "fine" would be a row silently valued at
// nothing, at a flipped sign, or at int64 minimum.
//
// It is stated here as well as inside the parser on purpose: this is the
// double-entry guard that survives a refactor of the parser's internals.
func FuzzParseImportRate(f *testing.F) {
	for _, s := range rateSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("parseImportRate panicked on %q: %v", s, r)
			}
		}()
		v, err := parseImportRate(s)
		if err != nil {
			if v != 0 {
				t.Errorf("parseImportRate(%q) returned %v alongside its error; a rejected rate must carry nothing", s, v)
			}
			return
		}
		if v == 0 {
			return // absence: an empty cell, which is not a fault
		}
		if !importRateIsUsable(v) {
			t.Errorf("parseImportRate(%q) accepted %v, which cannot divide", s, v)
		}
	})
}

// quantitySeeds is the seed corpus for FuzzFormatImportQuantity: the values
// TestFormatImportQuantity states exact renders for, handed to the fuzzer so
// its two invariants become claims about the FUNCTION rather than about that
// table. The table says what these eleven look like; this says what none of
// them may look like, for every float64 there is.
var quantitySeeds = []float64{
	89000, 0.92, 1500000, 1_000_000_000, 89000.5,
	0.000001, 1e-7, 5e-324, -0.0000001, 1e300, 0,
}

// FuzzFormatImportQuantity pins the two properties of a rendered figure that a
// message depends on, over every float64 rather than over a table.
//
//  1. A non-zero value never renders as zero. The messages put this straight
//     into a sentence — "1,500,000 ÷ 89,000 = 16.85" — so a divisor rendered
//     as "0" states a division by zero about a division that happened, and
//     sends the user to fix a rate the app just used.
//  2. The render stays short enough to read. 1e300 is 301 digits before
//     grouping and 401 characters after, in a string that lands on four
//     preview surfaces and inside a 409 body.
//
// Non-finite inputs are included deliberately: they cannot reach the callers
// today (every rate is checked usable first), but the renderer is a general
// helper and "NaN" must come out as NaN rather than as grouped nonsense.
func FuzzFormatImportQuantity(f *testing.F) {
	for _, v := range quantitySeeds {
		f.Add(v)
	}
	f.Fuzz(func(t *testing.T, v float64) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("formatImportQuantity panicked on %v: %v", v, r)
			}
		}()
		got := formatImportQuantity(v)
		if got == "" {
			t.Fatalf("formatImportQuantity(%v) rendered nothing", v)
		}
		if v != 0 && (got == "0" || got == "-0") {
			t.Errorf("formatImportQuantity(%v) = %q — a message would state a division by zero", v, got)
		}
		if n := utf8.RuneCountInString(got); n > 32 {
			t.Errorf("formatImportQuantity(%v) rendered %d characters; a message must stay readable", v, n)
		}
	})
}

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
