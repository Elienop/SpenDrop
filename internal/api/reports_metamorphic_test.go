package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/elienop/spendrop/internal/database"
)

// Phase 3.6 metamorphic property test for handleReportIncomeExpenses.
//
// Why metamorphic (vs another rapid property): there is no simple
// round-trip or algebraic identity to assert on a monthly rollup
// handler. The strongest statement we can make is "the SQL handler
// agrees with a dumb pure-Go reimplementation on every random input",
// and that is exactly a metamorphic equivalence check.
//
// Why handleReportIncomeExpenses as the canary: it is the most date-
// heavy reports handler. Its output depends entirely on (a) the SQL
// window predicate in SumByMonthRange, (b) the driver's date encoding,
// and (c) the Go-side loop that fills missing months with zeros. The
// date() wrapper bug fixed on this same branch — RFC3339 storage
// silently dropping rows dated on the upper-bound month — would have
// been caught immediately by this test had it existed at the time, and
// now serves as the regression guard against any future reintroduction.
//
// Acceptance criteria (from data-stewardship-plan.md Phase 3.6):
//
//  1. 100 samples run in roughly one second on a developer laptop.
//  2. Deliberately breaking SumByMonthRange (e.g., flipping `>=` to
//     `>` on date_from) causes rapid to find a counterexample within
//     ~5 shrink iterations and to print the offending input set. The
//     generators below are shaped so the shrinker converges on day=1
//     of the current month, which is where that exact break fires.
//
// Performance shape: one file-backed SQLite + migrations + seeded user
// + category lookups are built once in newReportsFixture. Each rapid
// sample runs `DELETE FROM transactions`, seeds up to 20 rows, drives
// the handler through its HTTP surface, decodes the JSON, and
// reflect.DeepEqual-compares against dumbIncomeExpenses. DELETE (as
// opposed to the qtx+Rollback pattern in import_handlers_property_test)
// is required because the handler reads through h.queries which is
// bound to the top-level *sql.DB — a scoped *sql.Tx would not be
// visible to the handler's own query context.

// metamorphicClockInstant is the fixed "now" every sample runs
// against. Chosen mid-month (April 15, 2026) so time.AddDate does not
// overflow across month boundaries: the handler uses
// `now.AddDate(0, -i, 0)` inside the slot-filling loop, and on a
// 31-day source month with a 30-day target (e.g. now=March 31,
// i=1 → February 31 → March 3) that quirk would produce duplicate or
// skipped slots in the handler's output.
//
// Why this matters for a METAMORPHIC test: the dumb reference
// (reports_dumb_go_test.go) runs the IDENTICAL `now.AddDate(0, -i, 0)`
// loop, so a quirk in one side is mirrored exactly on the other. The
// danger is NOT a false failure — it's a shared blindspot that makes
// the test silently pass on a corrupted slot list. Mid-month sidesteps
// the quirk entirely so both sides compute a clean, correct month
// sequence and the equality check is meaningful.
//
// If this test is ever parameterized over `now`, the clock must remain
// in [1, 28] of its month or the handler's AddDate quirk has to be
// fixed first — otherwise both sides drift the same way and the test
// keeps passing while the production handler emits garbage.
//
// TREAT AS CONST. Go does not allow `time.Time` in a const declaration
// because time.Time is a struct, so this has to be a var at the
// package level. Do not reassign it at runtime — every sample and
// every derived computation (generator offset, handler clock, dumb
// reference `now`) depends on all three seeing the same instant.
var metamorphicClockInstant = time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)

// reportsFixture holds the long-lived state shared by every rapid
// sample. Built once per test in newReportsFixture. The seeded user
// and category IDs come from migration 001's default seed — expense
// IDs 1-14 and income IDs 15-19 — so no extra seeding is required.
type reportsFixture struct {
	db            *sql.DB
	q             *database.Queries
	user          database.User
	expenseCatIDs []int64
	incomeCatIDs  []int64
	handler       *Handler
}

// newReportsFixture sets up the per-test fixture: migrated file-backed
// DB, seeded user, resolved income/expense category IDs (from the
// default seed in migration 001), and a Handler wired to a fixedClock
// pinned at metamorphicClockInstant so every handler call in the test
// sees the same "now".
func newReportsFixture(t *testing.T) *reportsFixture {
	t.Helper()
	q, db := setupTestDB(t)
	user := seedTestUser(t, q, "reportsprop", "admin")

	cats, err := q.ListAllCategories(context.Background())
	if err != nil {
		t.Fatalf("ListAllCategories: %v", err)
	}
	if len(cats) == 0 {
		t.Fatalf("expected seeded default categories from migration 001, got 0")
	}
	var expenseIDs, incomeIDs []int64
	for _, c := range cats {
		switch c.Type {
		case "expense":
			expenseIDs = append(expenseIDs, c.ID)
		case "income":
			incomeIDs = append(incomeIDs, c.ID)
		}
	}
	if len(expenseIDs) == 0 || len(incomeIDs) == 0 {
		t.Fatalf("expected both expense and income categories in default seed; got %d expense / %d income", len(expenseIDs), len(incomeIDs))
	}

	handler := NewHandlerWithClock(q, db, fixedClock{t: metamorphicClockInstant})

	return &reportsFixture{
		db:            db,
		q:             q,
		user:          user,
		expenseCatIDs: expenseIDs,
		incomeCatIDs:  incomeIDs,
		handler:       handler,
	}
}

// testSample carries one randomized transaction's fate: the
// category_id to seed into the DB (so the FK target exists) and the
// parallel dumbRow fed to the reference implementation. Bundling them
// into a single generator output guarantees the dumbRow's
// CategoryType always matches the type of the seeded category — a
// split "draw category, draw type" design would let them drift.
type testSample struct {
	CategoryID int64
	DumbRow    dumbRow
}

// lastDayOfMonth returns the number of days in the given calendar
// month. time.Date(y, m+1, 0, ...) rolls day-zero of the next month
// back to the last day of this month, which is the standard Go idiom
// for month-end computation without hard-coding month lengths or
// worrying about leap years.
func lastDayOfMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// drawTestSample draws one randomized transaction shaped to exercise
// every edge of the SumByMonthRange window predicate.
//
// The shrink path is deliberately engineered so the deliberate break
// that motivates this test — flipping `>=` to `>` on date_from in
// SumByMonthRange — converges on a minimal counterexample within a
// handful of rapid shrinks:
//
//   - `monthOffset` draws from [-(months+2), 2]. Zero is inside the
//     range and rapid.IntRange shrinks toward zero when it is
//     reachable, so failing samples move toward "now's month".
//     Including `-(months+2)` and `+2` lets rows land outside the
//     window on either side, which the non-break path also needs.
//
//   - `dayKind` draws from [0, 9]. Zero → day 1 of month, one → last
//     day of month, two through nine → uniform [2, 27]. The shrinker
//     pushes this toward zero, which is exactly day 1 — the date that
//     a `>` break at the lower bound rejects but a `>=` predicate
//     accepts, so the failing row shrinks straight onto the bug.
//
//   - `isIncome` is a fair coin. Shrinker drifts toward false
//     (expense), which does not affect whether the bug fires — both
//     sides of the rollup have the same window predicate.
//
//   - `amountCents` is in [1, 1_000_000] (1¢ to $10,000). Non-zero so
//     it is visible in the rollup after cents→dollars round-tripping,
//     and bounded so per-sample sums stay inside float64 precision.
//
//   - `deletedKind` draws from [0, 9] and only 0 means "deleted". That
//     gives ~10% tombstoned rows in the population (enough to
//     exercise the deleted_at IS NULL filter) while letting the
//     shrinker push toward non-deleted on failure — a mismatch on a
//     deleted row would be misleading, since the bug we care about is
//     in the date predicate, not the tombstone filter.
func drawTestSample(rt *rapid.T, fix *reportsFixture, months int) testSample {
	offset := rapid.IntRange(-(months+2), 2).Draw(rt, "monthOffset")
	shifted := metamorphicClockInstant.AddDate(0, offset, 0)
	y := shifted.Year()
	m := shifted.Month()

	dayKind := rapid.IntRange(0, 9).Draw(rt, "dayKind")
	var day int
	switch {
	case dayKind == 0:
		day = 1
	case dayKind == 1:
		day = lastDayOfMonth(y, m)
	default:
		day = rapid.IntRange(2, 27).Draw(rt, "midDay")
	}
	date := time.Date(y, m, day, 0, 0, 0, 0, time.UTC)

	isIncome := rapid.Bool().Draw(rt, "isIncome")
	var catID int64
	var catType string
	if isIncome {
		catID = rapid.SampledFrom(fix.incomeCatIDs).Draw(rt, "incomeCatID")
		catType = "income"
	} else {
		catID = rapid.SampledFrom(fix.expenseCatIDs).Draw(rt, "expenseCatID")
		catType = "expense"
	}

	amountCents := int64(rapid.IntRange(1, 1_000_000).Draw(rt, "amountCents"))
	deleted := rapid.IntRange(0, 9).Draw(rt, "deletedKind") == 0

	return testSample{
		CategoryID: catID,
		DumbRow: dumbRow{
			Date:         date,
			AmountCents:  amountCents,
			CategoryType: catType,
			Deleted:      deleted,
		},
	}
}

// drawSamples draws 0 to 20 testSamples through rapid.SliceOfN so the
// built-in slice shrinker can drop individual elements during
// minimization. A hand-rolled `make([]testSample, n)` with a loop
// also works at draw time, but its only shrink axis is `n` itself —
// the shrinker cannot delete a specific no-op row from the middle of
// the slice. SliceOfN's native element-level reduction lets a failing
// reproduction shrink to exactly the offending row, which is the
// difference between "1 live row + 4 tombstoned fillers" and "1 live
// row" in the failure output. `months` is closed over from the outer
// draw so the nested monthOffset range stays consistent with the
// window the handler will compute.
//
// The upper bound of 20 keeps per-sample cost bounded so 100 samples
// fit inside the plan's ~1-second budget while still exercising
// multi-row aggregation. The lower bound of 0 covers the empty-input
// case where the handler must still emit `months` zero entries,
// which rapid will reach almost immediately on failure because the
// slice shrinker targets the empty slice first.
//
// Note on density: the upper bound of 20 is independent of `months`,
// so average per-month density scales inversely with the window
// length (roughly 10 rows/month at months=1 down to ~1.5 rows/month
// at months=12). This is intentional — the bug class we care about
// is the date-window predicate, and a single row on the correct day
// is enough to exhibit it. Making the bound scale with months would
// roughly hold per-month density constant but would blow up
// per-sample cost on large windows for no additional bug coverage.
func drawSamples(rt *rapid.T, fix *reportsFixture, months int) []testSample {
	sampleGen := rapid.Custom(func(inner *rapid.T) testSample {
		return drawTestSample(inner, fix, months)
	})
	return rapid.SliceOfN(sampleGen, 0, 20).Draw(rt, "samples")
}

// TestHandleReportIncomeExpenses_Metamorphic is the Phase 3.6 canary
// test. For every randomized sample (up to 20 transactions plus a
// random `months` window), it seeds the DB, drives the real HTTP
// handler, and asserts the decoded JSON matches dumbIncomeExpenses
// byte-for-byte.
//
// Passing: the SQL path and the pure-Go reference agree on every
// random input. This is the guarantee we shipped the date() wrapper
// fix to earn.
//
// Failing: rapid shrinks to a minimal counterexample and prints the
// seed, the `months` value, and the failing transaction shape. That
// output is enough to reconstruct the exact SQL that diverged, which
// is the fast-path for diagnosing a regression in any of the query
// body, the sqlc binding, or the handler's slot-filling loop.
func TestHandleReportIncomeExpenses_Metamorphic(t *testing.T) {
	fix := newReportsFixture(t)

	rapid.Check(t, func(rt *rapid.T) {
		// Wipe the transactions table from the previous sample. The
		// handler reads through h.queries which is bound to the
		// top-level *sql.DB, so we cannot isolate samples with a
		// scoped *sql.Tx + Rollback the way Phase 3.5's property
		// test does — a delete-per-sample is the cheapest isolation
		// available here. seedTestTransaction below bypasses
		// TransactionStore and writes directly via
		// q.CreateTransaction, so no transaction_audit rows are
		// generated and nothing needs to be cleaned from that table.
		if _, err := fix.db.ExecContext(context.Background(), "DELETE FROM transactions"); err != nil {
			rt.Fatalf("cleanup transactions: %v", err)
		}

		months := rapid.IntRange(1, 12).Draw(rt, "months")
		samples := drawSamples(rt, fix, months)

		// Seed every sample into the DB and track the parallel dumb
		// rows. seedTestTransaction dual-writes amount_cents, which
		// is what the reports query reads, so the handler and the
		// dumb impl see byte-identical amounts.
		rows := make([]dumbRow, len(samples))
		for i, s := range samples {
			dateStr := s.DumbRow.Date.Format("2006-01-02")
			amount := centsToDollars(s.DumbRow.AmountCents)
			txn := seedTestTransaction(t, fix.q,
				fix.user.ID, s.CategoryID, dateStr, amount,
				fmt.Sprintf("metamorphic-sample-%d", i))
			if s.DumbRow.Deleted {
				if err := fix.q.SoftDeleteTransaction(context.Background(), txn.ID); err != nil {
					rt.Fatalf("soft-delete sample %d: %v", i, err)
				}
			}
			rows[i] = s.DumbRow
		}

		// Drive the real handler.
		url := fmt.Sprintf("/api/reports/income-expenses?months=%d", months)
		req := httptest.NewRequest(http.MethodGet, url, nil)
		req = withUser(req, fix.user)
		rec := httptest.NewRecorder()
		fix.handler.handleReportIncomeExpenses(rec, req)
		if rec.Code != http.StatusOK {
			rt.Fatalf("handler returned %d; body=%s", rec.Code, rec.Body.String())
		}

		var resp struct {
			Data []incomeExpenseEntry `json:"data"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			rt.Fatalf("decode handler response: %v", err)
		}

		// dumbIncomeExpenses runs against the same fixed clock the
		// handler was constructed with, so both sides compute the
		// same window bounds and the same slot list.
		expected := dumbIncomeExpenses(rows, metamorphicClockInstant, months)

		if !reflect.DeepEqual(resp.Data, expected) {
			// On mismatch, dump both slices so the rapid test log
			// includes everything needed to reconstruct the failing
			// query. rapid will automatically print the minimized
			// draw above this line.
			rt.Fatalf("metamorphic mismatch on months=%d, rows=%d\nhandler:  %+v\nexpected: %+v",
				months, len(rows), resp.Data, expected)
		}
	})
}
