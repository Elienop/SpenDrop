package api

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/elienop/spendrop/internal/database"
)

// Phase 3.5 property tests for the import pipeline.
//
// These tests exercise `processImportRows` — the refactored, HTTP-free
// core of `handleImportConfirm` — against randomized mixes of valid and
// invalid `importRow` inputs. Each property asserts a single invariant
// that must hold over every possible input, and a deliberate regression
// in any of them would fail within a handful of shrinks rather than
// lying in wait for a hand-crafted test case.
//
// The four properties are:
//
//  1. Conservation: every input row lands in exactly one of Inserted,
//     Skipped, or Errored. Guards against refactors that forget to
//     account for a branch and silently drop a row from the count.
//
//  2. No silent drops: every Skipped row carries a reason drawn from
//     the closed `importSkipReason` set. Guards against a future
//     `Skipped = append(... reason: "")` slip that would look fine on
//     the wire but leave the user without a "why".
//
//  3. Date sanity: every Inserted row has a date in the household
//     ledger window [1900-01-01, 2100-12-31]. The year-range check
//     inside processImportRows routes out-of-range dates to
//     `skipReasonUnparseableDate`, so an Inserted row outside the
//     window is a direct contract violation.
//
//  4. Amount sanity: every Inserted row has `AmountCents > 0`. The
//     zero-amount skip branch combined with `math.Abs` on parse
//     guarantees this, but a refactor that moves the zero-check
//     *after* the insert would regress silently — this property
//     catches that class of bug immediately.
//
// Performance budget: `rapid.Check` runs 100 samples by default. Each
// sample opens a short-lived SQLite transaction on a file-backed WAL
// DB, calls processImportRows, and rolls back. A single fixture is
// built once per property test so the ~100 ms migration cost is not
// multiplied by 100. On a modest laptop the full file runs in well
// under 2 seconds, matching the data-stewardship plan's Phase 3.5
// acceptance criterion.

// propertyFixture bundles the long-lived test state shared across
// every rapid sample for a single property test. A fresh file-backed
// DB with migrations + seeded user + category lookups is built once
// in `newPropertyFixture`; every rapid.Check closure then opens its
// own tx and rolls back so samples stay independent.
type propertyFixture struct {
	db          *sql.DB
	q           *database.Queries
	userID      int64
	catNameToID map[string]int64
	catIDToName map[int64]string
	knownCats   []string
}

// newPropertyFixture sets up the per-property-test fixture: migrated
// DB, seeded user, and resolved category lookups drawn from the 19
// default categories seeded in migration 001. The caller keeps the
// returned fixture for the lifetime of the enclosing test function.
func newPropertyFixture(t *testing.T) *propertyFixture {
	t.Helper()
	q, db := setupTestDB(t)
	user := seedTestUser(t, q, "importprop", "admin")

	cats, err := q.ListAllCategories(context.Background())
	if err != nil {
		t.Fatalf("ListAllCategories: %v", err)
	}
	if len(cats) == 0 {
		t.Fatalf("expected seeded default categories from migration 001, got 0")
	}
	nameToID := make(map[string]int64, len(cats))
	idToName := make(map[int64]string, len(cats))
	knownCats := make([]string, 0, len(cats))
	for _, c := range cats {
		nameToID[strings.ToLower(c.Name)] = c.ID
		idToName[c.ID] = c.Name
		knownCats = append(knownCats, c.Name)
	}

	return &propertyFixture{
		db:          db,
		q:           q,
		userID:      user.ID,
		catNameToID: nameToID,
		catIDToName: idToName,
		knownCats:   knownCats,
	}
}

// runProcess opens a fresh sqlc transaction on the fixture DB, calls
// processImportRows with the sampled rows, and rolls back so the next
// sample sees an empty transactions table. Rolling back (rather than
// committing and truncating) is cheaper in WAL mode and sidesteps the
// foreign-key / audit-table fan-out that a commit would trigger.
//
// DefaultCategoryID is intentionally 0 so the "missing category" skip
// branch can fire for rows whose spreadsheet category is empty or
// unrecognized. CategoryMap is nil because the properties do not need
// to exercise the explicit mapping path to assert conservation and
// reason discipline.
func (f *propertyFixture) runProcess(t *rapid.T, rows []importRow) importResult {
	tx, err := f.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback()
	qtx := f.q.WithTx(tx)
	store := database.NewTransactionStore(f.db, f.q)

	result, _ := processImportRows(context.Background(), qtx, tx, store, importProcessInput{
		UserID:            f.userID,
		Rows:              rows,
		CategoryMap:       nil,
		DefaultCategoryID: 0,
		CatNameToID:       f.catNameToID,
		CatIDToName:       f.catIDToName,
	})
	return result
}

// --- generators ---

// genValidDateString draws a date in the [1900, 2100] household ledger
// window, formatted as YYYY-MM-DD. The day is capped at 28 so the
// generator never needs to reason about month lengths or leap years —
// properties do not care whether Feb 29 is valid, only that dates in
// the sanity window land in Inserted and dates outside it do not.
func genValidDateString() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		year := rapid.IntRange(1900, 2100).Draw(t, "year")
		month := rapid.IntRange(1, 12).Draw(t, "month")
		day := rapid.IntRange(1, 28).Draw(t, "day")
		return fmt.Sprintf("%04d-%02d-%02d", year, month, day)
	})
}

// genOutOfRangeDateString draws a syntactically valid YYYY-MM-DD date
// whose year is outside [1900, 2100]. Splitting the year into a
// below-window and above-window half lets rapid shrink toward either
// edge cleanly on failure — a single IntRange that straddled the gap
// would shrink toward the middle (inside the window) and hide the
// real failing case.
func genOutOfRangeDateString() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		year := rapid.OneOf(
			rapid.IntRange(1800, 1899),
			rapid.IntRange(2101, 2500),
		).Draw(t, "year")
		month := rapid.IntRange(1, 12).Draw(t, "month")
		day := rapid.IntRange(1, 28).Draw(t, "day")
		return fmt.Sprintf("%04d-%02d-%02d", year, month, day)
	})
}

// genNonEmptyDescription draws a 1-30 char alphanumeric description.
// The charset is deliberately narrow: properties do not depend on
// unicode coverage, and keeping the alphabet small lets rapid shrink
// failing descriptions to the minimal identifying example quickly.
// StringMatching can emit a run of spaces that trims to "", so the
// Filter step keeps the "valid" label honest.
func genNonEmptyDescription() *rapid.Generator[string] {
	return rapid.StringMatching(`[A-Za-z0-9 ]{1,30}`).Filter(func(s string) bool {
		return strings.TrimSpace(s) != ""
	})
}

// genNonZeroAmount draws a dollar amount in a realistic household
// range [$0.01, $1,000,000.00], with a random sign. `math.Abs` inside
// processImportRows flips negatives to positive at insert time, so
// properties only need to see "will this amount round to zero cents"
// — and the integer-cents draw guarantees it will not.
func genNonZeroAmount() *rapid.Generator[float64] {
	return rapid.Custom(func(t *rapid.T) float64 {
		cents := rapid.IntRange(1, 100_000_000).Draw(t, "abs_cents")
		negative := rapid.Bool().Draw(t, "negative")
		f := float64(cents) / 100.0
		if negative {
			f = -f
		}
		return f
	})
}

// genImportRow picks a row "shape" and then draws fields consistent
// with that shape. The shape labels match the rejection branches in
// processImportRows one-to-one — a shape that should get Inserted
// draws all valid fields, an empty-desc shape pairs a valid date/
// amount with `Description: ""`, and so on. Valid rows are weighted
// higher (shapes 0-2 all produce valid rows) so the conservation and
// sanity properties still see Inserted samples on the vast majority
// of iterations; rejection shapes exist mainly to exercise the
// reason-set property and to force a mixed-outcome distribution that
// tests the Skipped branches.
//
// Coverage note: three skip reasons are NOT exercised by this
// generator and are instead covered by the companion deterministic
// test `TestProcessImportRows_AllReasonsReachable`:
//
//   - `skipReasonDuplicate` would require either a pre-seeded row or
//     an in-batch collision that rapid's random input is extraordinarily
//     unlikely to produce with a 1e8-cent amount space and 1-30 char
//     descriptions.
//   - `importErrored` (entire bucket) requires a DB fault or a
//     client-supplied category_id that is missing from the lookup
//     map — neither of which the generator can plausibly synthesize.
//
// The companion test pre-seeds duplicates, wires a tiny
// catNameToID/catIDToName pair with a deliberate mismatch, and
// verifies that each rejection branch lands with the expected reason.
// The property test here still runs 100 samples × up-to-20 rows per
// sample across the branches the generator *can* reach, which is
// where the conservation and sanity invariants matter most.
func genImportRow(knownCats []string) *rapid.Generator[importRow] {
	return rapid.Custom(func(t *rapid.T) importRow {
		shape := rapid.IntRange(0, 7).Draw(t, "shape")
		switch shape {
		case 0, 1, 2:
			// Valid row (weighted higher so most rows hit the
			// Inserted path).
			return importRow{
				Date:        genValidDateString().Draw(t, "date"),
				Description: genNonEmptyDescription().Draw(t, "desc"),
				Amount:      genNonZeroAmount().Draw(t, "amount"),
				Category:    rapid.SampledFrom(knownCats).Draw(t, "cat"),
			}
		case 3:
			// Garbled date string — hits skipReasonUnparseableDate
			// via the parseImportDate error path.
			return importRow{
				Date: rapid.SampledFrom([]string{
					"", "not a date", "2026-13-40", "tomorrow", "Jan 99 2026",
				}).Draw(t, "bad_date"),
				Description: genNonEmptyDescription().Draw(t, "desc"),
				Amount:      genNonZeroAmount().Draw(t, "amount"),
				Category:    rapid.SampledFrom(knownCats).Draw(t, "cat"),
			}
		case 4:
			// Empty description — hits skipReasonEmptyDescription.
			return importRow{
				Date:        genValidDateString().Draw(t, "date"),
				Description: "",
				Amount:      genNonZeroAmount().Draw(t, "amount"),
				Category:    rapid.SampledFrom(knownCats).Draw(t, "cat"),
			}
		case 5:
			// Zero amount — hits skipReasonZeroAmount. Exact-zero
			// rather than a near-zero float so the `amount == 0`
			// compare is deterministic.
			return importRow{
				Date:        genValidDateString().Draw(t, "date"),
				Description: genNonEmptyDescription().Draw(t, "desc"),
				Amount:      0,
				Category:    rapid.SampledFrom(knownCats).Draw(t, "cat"),
			}
		case 6:
			// Year outside [1900, 2100] — hits the year-range
			// branch that also maps to skipReasonUnparseableDate.
			return importRow{
				Date:        genOutOfRangeDateString().Draw(t, "oor_date"),
				Description: genNonEmptyDescription().Draw(t, "desc"),
				Amount:      genNonZeroAmount().Draw(t, "amount"),
				Category:    rapid.SampledFrom(knownCats).Draw(t, "cat"),
			}
		case 7:
			// Unresolved spreadsheet category — hits
			// skipReasonMissingCategory. The test fixture passes
			// DefaultCategoryID=0, so resolveCategoryID falls
			// through both the map lookup and the nil categoryMap
			// path and returns 0, which the processor treats as
			// "no category available".
			//
			// The "noncat_" prefix plus a random suffix is
			// astronomically unlikely to collide with a real
			// seeded category name (all of which are ordinary
			// household categories like "Food", "Transportation",
			// etc), so the generator reliably lands in the
			// missing-category branch.
			return importRow{
				Date:        genValidDateString().Draw(t, "date"),
				Description: genNonEmptyDescription().Draw(t, "desc"),
				Amount:      genNonZeroAmount().Draw(t, "amount"),
				Category:    "noncat_" + rapid.StringMatching(`[a-z]{4,8}`).Draw(t, "catsuf"),
			}
		}
		// Unreachable — IntRange(0, 7) guarantees shape is in [0, 7].
		// If a future refactor widens the range without adding a
		// matching case, the default importRow{} here lands in the
		// empty-date branch and is counted as skipReasonUnparseableDate,
		// which still satisfies conservation and the reason set but
		// silently folds the new shape into the wrong bucket — please
		// add a matching case instead of relying on this fallback.
		return importRow{}
	})
}

// genImportRows draws a slice of 0-20 rows. The upper bound keeps
// per-sample cost bounded (so 100 samples finish inside the 2s
// budget) while still exercising batch-level behaviour like the
// qtx-scoped in-transaction duplicate lookup that would fire on a
// spreadsheet with the same row twice. The lower bound of 0 makes
// sure the empty-input case is covered — rapid almost always shrinks
// failures toward the empty slice, and properties that silently
// break on `len(rows) == 0` should fail fast.
func genImportRows(knownCats []string) *rapid.Generator[[]importRow] {
	return rapid.SliceOfN(genImportRow(knownCats), 0, 20)
}

// --- properties ---

// TestImportProperty_Conservation asserts that every input row lands
// in exactly one of Inserted, Skipped, or Errored — no row is
// silently dropped and no row is double-counted. The loop structure
// of processImportRows enforces this by construction (every
// iteration appends to exactly one slice and then `continue`s), so
// a failing sample here points directly at a future refactor that
// forgot to account for a branch.
func TestImportProperty_Conservation(t *testing.T) {
	fix := newPropertyFixture(t)
	rapid.Check(t, func(t *rapid.T) {
		rows := genImportRows(fix.knownCats).Draw(t, "rows")
		result := fix.runProcess(t, rows)

		total := len(result.Inserted) + len(result.Skipped) + len(result.Errored)
		if total != len(rows) {
			t.Fatalf(
				"conservation broken: inserted=%d skipped=%d errored=%d sum=%d != len(rows)=%d",
				len(result.Inserted), len(result.Skipped), len(result.Errored), total, len(rows),
			)
		}
	})
}

// TestImportProperty_NoSilentDrops asserts that every Skipped row
// carries a reason drawn from the closed `importSkipReason` set. A
// future rejection branch that appends to Skipped without a matching
// constant would look fine on the wire (the JSON response still
// shows `"skipped": N`) but strip the user's "why" — this property
// catches that class of slip at review time instead of in production.
func TestImportProperty_NoSilentDrops(t *testing.T) {
	fix := newPropertyFixture(t)
	// The closed set of allowed skip reasons. Any new rejection
	// branch in processImportRows must add a constant to
	// import_handlers.go AND add it here — otherwise the property
	// fails immediately, which is the point.
	validReasons := map[importSkipReason]struct{}{
		skipReasonEmptyDescription: {},
		skipReasonZeroAmount:       {},
		skipReasonUnparseableDate:  {},
		skipReasonMissingCategory:  {},
		skipReasonDuplicate:        {},
	}
	rapid.Check(t, func(t *rapid.T) {
		rows := genImportRows(fix.knownCats).Draw(t, "rows")
		result := fix.runProcess(t, rows)
		for _, s := range result.Skipped {
			if _, ok := validReasons[s.Reason]; !ok {
				t.Fatalf(
					"row %d skipped with unrecognized reason %q — did you add a rejection branch without a matching importSkipReason constant?",
					s.RowIndex, s.Reason,
				)
			}
		}
	})
}

// TestImportProperty_DateSanity asserts that every Inserted row has
// a date in the [1900-01-01, 2100-12-31] household ledger window.
// The year-range check inside processImportRows routes out-of-range
// dates to skipReasonUnparseableDate, so an Inserted row outside the
// window is a contract violation: either the check was removed, the
// bounds were widened without updating this property, or the window
// was applied after insert (too late).
//
// We compare by `Year()` — not by Before/After on a precomputed
// time.Time — to mirror the handler's own guard exactly. A future
// generator that emits fractional-serial dates like 2100-12-31
// 23:59:59.5 would otherwise spuriously fail a seconds-granularity
// maxDate comparison even though the handler's own check passes.
func TestImportProperty_DateSanity(t *testing.T) {
	fix := newPropertyFixture(t)
	rapid.Check(t, func(t *rapid.T) {
		rows := genImportRows(fix.knownCats).Draw(t, "rows")
		result := fix.runProcess(t, rows)
		for _, ins := range result.Inserted {
			if y := ins.Date.Year(); y < 1900 || y > 2100 {
				t.Fatalf(
					"row %d inserted with date %s (year %d) outside [1900, 2100] household ledger window",
					ins.RowIndex, ins.Date.Format("2006-01-02"), y,
				)
			}
		}
	})
}

// TestImportProperty_AmountSanity asserts that every Inserted row
// has `AmountCents > 0`. processImportRows calls `math.Abs` on the
// parsed amount and skips rows whose absolute value is zero, so the
// combination should guarantee positive cents on insert. A future
// refactor that moves the zero-check after the insert — or that
// replaces math.Abs with something sign-preserving — would regress
// silently in the absence of this property.
func TestImportProperty_AmountSanity(t *testing.T) {
	fix := newPropertyFixture(t)
	rapid.Check(t, func(t *rapid.T) {
		rows := genImportRows(fix.knownCats).Draw(t, "rows")
		result := fix.runProcess(t, rows)
		for _, ins := range result.Inserted {
			if ins.AmountCents <= 0 {
				t.Fatalf(
					"row %d inserted with non-positive AmountCents=%d — math.Abs + zero-skip should guarantee >0",
					ins.RowIndex, ins.AmountCents,
				)
			}
		}
	})
}

// TestProcessImportRows_AllReasonsReachable is the deterministic
// companion to the rapid-driven properties above. Its job is to close
// the coverage gap that the random generator structurally cannot
// reach: `skipReasonDuplicate` (requires a pre-seeded row or in-batch
// collision that random inputs virtually never produce) and the
// entire `Errored` bucket (requires a client-supplied category_id
// missing from the lookup map).
//
// The test makes the reachability of each reason checkable in CI: a
// future refactor that renames or removes a rejection branch cannot
// slip past both this deterministic test and the property-level
// reason-set assertion. Together they give "no silent drops" real
// teeth on every branch, not just the two the generator happens to
// exercise.
//
// Each subtest builds the minimal input required to land in one
// bucket, asserts the bucket length, and — for Skipped rows — asserts
// the exact reason constant. Errored rows are asserted on RowIndex
// only because the free-form Reason is now scrubbed via
// sanitizeLogValue and the concrete DB error wording is not part of
// the contract.
func TestProcessImportRows_AllReasonsReachable(t *testing.T) {
	fix := newPropertyFixture(t)
	ctx := context.Background()

	// Helper: run processImportRows on a fresh tx and roll back.
	// Separate from propertyFixture.runProcess because this test
	// sometimes passes a custom catIDToName / catNameToID /
	// defaultCategoryID per subtest.
	run := func(t *testing.T, in importProcessInput) importResult {
		t.Helper()
		tx, err := fix.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("begin tx: %v", err)
		}
		defer tx.Rollback()
		in.UserID = fix.userID
		store := database.NewTransactionStore(fix.db, fix.q)
		result, _ := processImportRows(ctx, fix.q.WithTx(tx), tx, store, in)
		return result
	}

	// Every subtest reuses the valid base row below and mutates one
	// field to trigger its target rejection branch. Using a single
	// canonical valid row keeps the failing field obvious in
	// assertions.
	validBase := importRow{
		Date:        "2026-01-15",
		Description: "Coffee",
		Amount:      4.50,
		Category:    fix.knownCats[0],
	}

	t.Run("empty_description", func(t *testing.T) {
		row := validBase
		row.Description = ""
		result := run(t, importProcessInput{
			Rows:        []importRow{row},
			CatNameToID: fix.catNameToID,
			CatIDToName: fix.catIDToName,
		})
		assertSingleSkip(t, result, skipReasonEmptyDescription)
	})

	t.Run("zero_amount", func(t *testing.T) {
		row := validBase
		row.Amount = 0
		result := run(t, importProcessInput{
			Rows:        []importRow{row},
			CatNameToID: fix.catNameToID,
			CatIDToName: fix.catIDToName,
		})
		assertSingleSkip(t, result, skipReasonZeroAmount)
	})

	t.Run("unparseable_date_garbled", func(t *testing.T) {
		row := validBase
		row.Date = "not a date"
		result := run(t, importProcessInput{
			Rows:        []importRow{row},
			CatNameToID: fix.catNameToID,
			CatIDToName: fix.catIDToName,
		})
		assertSingleSkip(t, result, skipReasonUnparseableDate)
	})

	t.Run("unparseable_date_out_of_range_year", func(t *testing.T) {
		row := validBase
		row.Date = "1700-05-01"
		result := run(t, importProcessInput{
			Rows:        []importRow{row},
			CatNameToID: fix.catNameToID,
			CatIDToName: fix.catIDToName,
		})
		assertSingleSkip(t, result, skipReasonUnparseableDate)
	})

	t.Run("missing_category", func(t *testing.T) {
		row := validBase
		row.Category = "NoSuchCategory"
		result := run(t, importProcessInput{
			Rows:              []importRow{row},
			DefaultCategoryID: 0, // no fallback → resolveCategoryID returns 0
			CatNameToID:       fix.catNameToID,
			CatIDToName:       fix.catIDToName,
		})
		assertSingleSkip(t, result, skipReasonMissingCategory)
	})

	t.Run("duplicate_in_batch", func(t *testing.T) {
		// Emit the same canonical row twice in one batch. The
		// qtx-scoped lookup against the first occurrence's insert
		// catches the second as a duplicate within the same commit,
		// without requiring any pre-seeding.
		row := validBase
		result := run(t, importProcessInput{
			Rows:        []importRow{row, row},
			CatNameToID: fix.catNameToID,
			CatIDToName: fix.catIDToName,
		})
		if len(result.Inserted) != 1 {
			t.Fatalf("expected 1 inserted (first occurrence), got %d", len(result.Inserted))
		}
		if len(result.Skipped) != 1 {
			t.Fatalf("expected 1 skipped (second occurrence), got %d", len(result.Skipped))
		}
		if got := result.Skipped[0].Reason; got != skipReasonDuplicate {
			t.Fatalf("expected reason %q, got %q", skipReasonDuplicate, got)
		}
	})

	t.Run("errored_unknown_category_id", func(t *testing.T) {
		// Wire a catNameToID that resolves the spreadsheet category
		// to ID 9999, but leave catIDToName empty. resolveCategoryID
		// returns 9999, and the subsequent `in.CatIDToName[9999]`
		// lookup fails — which routes the row to Errored with the
		// "unknown category_id=9999" reason. This is the only
		// deterministic way to land in the Errored bucket without
		// standing up a fault-injecting fake DB.
		row := validBase
		row.Category = "weirdcat"
		result := run(t, importProcessInput{
			Rows:        []importRow{row},
			CatNameToID: map[string]int64{"weirdcat": 9999},
			CatIDToName: map[int64]string{}, // deliberately empty
		})
		if len(result.Inserted) != 0 {
			t.Fatalf("expected 0 inserted, got %d", len(result.Inserted))
		}
		if len(result.Skipped) != 0 {
			t.Fatalf("expected 0 skipped, got %d", len(result.Skipped))
		}
		if len(result.Errored) != 1 {
			t.Fatalf("expected 1 errored, got %d", len(result.Errored))
		}
		if result.Errored[0].RowIndex != 0 {
			t.Fatalf("expected errored RowIndex=0, got %d", result.Errored[0].RowIndex)
		}
		if !strings.Contains(result.Errored[0].Reason, "9999") {
			t.Fatalf("expected errored reason to mention category_id=9999, got %q", result.Errored[0].Reason)
		}
	})
}

// assertSingleSkip asserts that result contains exactly one row in
// Skipped with the expected reason, no rows in Inserted, and no rows
// in Errored. Used by the per-reason subtests above.
func assertSingleSkip(t *testing.T, result importResult, expected importSkipReason) {
	t.Helper()
	if len(result.Inserted) != 0 {
		t.Fatalf("expected 0 inserted, got %d", len(result.Inserted))
	}
	if len(result.Errored) != 0 {
		t.Fatalf("expected 0 errored, got %d", len(result.Errored))
	}
	if len(result.Skipped) != 1 {
		t.Fatalf("expected 1 skipped, got %d", len(result.Skipped))
	}
	if got := result.Skipped[0].Reason; got != expected {
		t.Fatalf("expected reason %q, got %q", expected, got)
	}
}
