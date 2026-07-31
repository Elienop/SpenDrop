package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/database"
)

// --- GET /api/reports/years ---
//
// The picker's source of truth: which years does the household's ledger
// actually hold, and which of those can the reports display?
//
// Every property below was flagged as fatal-if-missing:
//
//   - a year outside [MinDataYear, MaxDataYear] would 400 every tab at once
//     (legacy rows from the era before validateDate had a bound can be
//     anything);
//   - a FUTURE year is worse than useless — SavingsTab's
//     monthsToCoverYear(year, currentYear) goes <= 0 and floors at 24,
//     rendering a trailing 24-month window as "0% of goal" for a year that
//     holds real data: a confidently wrong number, not an empty state;
//   - an empty list would render an empty dropdown, so the current year is
//     always in it;
//   - anything filtered out has to be REPORTED, or a legacy 3021 row vanishes
//     silently, which is the exact bug class this work exists to kill.

// getReportYears drives the handler with an authenticated member and returns
// the decoded body as a map, so a MISSING key is distinguishable from a zero
// value (a typed decode would just zero-fill it).
func getReportYears(t *testing.T, h *Handler, user database.User) map[string]any {
	t.Helper()
	resp, _ := getReportYearsRaw(t, h, user)
	return resp
}

// getReportYearsRaw additionally returns the response body verbatim, which is
// the only way to tell an empty JSON array apart from `null`: both decode into
// a `map[string]any` entry, and `null` is the one that unmounts the Reports
// page when the consumer reads `.length` off it.
func getReportYearsRaw(t *testing.T, h *Handler, user database.User) (map[string]any, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/reports/years", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleReportYears(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	var resp map[string]any
	decodeResponse(t, rec, &resp)
	return resp, body
}

// yearsField pulls a []int out of the decoded body, failing loudly when the
// key is absent.
func yearsField(t *testing.T, resp map[string]any, key string) []int {
	t.Helper()
	raw, ok := resp[key]
	if !ok {
		t.Fatalf("response has no %q key: %v", key, resp)
	}
	list, ok := raw.([]any)
	if !ok {
		t.Fatalf("%s is %T, want a JSON array: %v", key, raw, raw)
	}
	out := make([]int, 0, len(list))
	for _, v := range list {
		n, ok := v.(float64)
		if !ok {
			t.Fatalf("%s contains %T, want JSON numbers: %v", key, v, v)
		}
		out = append(out, int(n))
	}
	return out
}

func containsYear(years []int, want int) bool {
	for _, y := range years {
		if y == want {
			return true
		}
	}
	return false
}

// reportYearsFixture builds a handler with a frozen 2026 clock.
func reportYearsFixture(t *testing.T) (*Handler, *database.Queries, database.User, database.Category) {
	t.Helper()
	q, db := setupTestDB(t)
	h := NewHandlerWithClock(q, db, fixedClock{t: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)})
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "Groceries "+t.Name(), "expense")
	return h, q, user, cat
}

// TestReportYears_ListsLedgerYearsNewestFirst is the headline case.
func TestReportYears_ListsLedgerYearsNewestFirst(t *testing.T) {
	h, q, user, cat := reportYearsFixture(t)

	// Seeded out of chronological order so a query returning "insertion order"
	// rather than a sort would fail.
	seedTestTransaction(t, q, user.ID, cat.ID, "2019-03-15", 10, "a")
	seedTestTransaction(t, q, user.ID, cat.ID, "2026-01-04", 10, "b")
	seedTestTransaction(t, q, user.ID, cat.ID, "1984-06-01", 10, "c")
	seedTestTransaction(t, q, user.ID, cat.ID, "2019-11-02", 10, "d") // same year twice

	resp := getReportYears(t, h, user)
	years := yearsField(t, resp, "years")

	want := []int{2026, 2019, 1984}
	if len(years) != len(want) {
		t.Fatalf("years = %v, want %v (DISTINCT, newest first)", years, want)
	}
	for i := range want {
		if years[i] != want[i] {
			t.Fatalf("years = %v, want %v (DISTINCT, newest first)", years, want)
		}
	}

	if got, ok := resp["current_year"].(float64); !ok || int(got) != 2026 {
		t.Errorf("current_year = %v, want 2026", resp["current_year"])
	}
	if has, ok := resp["has_transactions"].(bool); !ok || !has {
		t.Errorf("has_transactions = %v, want true", resp["has_transactions"])
	}
	if out := yearsField(t, resp, "out_of_range_years"); len(out) != 0 {
		t.Errorf("out_of_range_years = %v, want empty — every seeded year is reportable", out)
	}
	if fut := yearsField(t, resp, "future_years"); len(fut) != 0 {
		t.Errorf("future_years = %v, want empty — every seeded year is in the past", fut)
	}
}

// TestReportYears_HidesTombstoned is the soft-delete invariant and the test
// that catches a missing `deleted_at IS NULL`. The tombstoned row carries a
// sentinel amount so it is unmistakable in a failure dump.
func TestReportYears_HidesTombstoned(t *testing.T) {
	h, q, user, cat := reportYearsFixture(t)

	seedTestTransaction(t, q, user.ID, cat.ID, "2019-03-15", 42, "live")
	seedTombstonedTestTransaction(t, q, user.ID, cat.ID, "1984-06-01", 999, "trashed")
	// A SECOND tombstone, deliberately out-of-window. partitionReportYears
	// routes each year to exactly one bucket, so an in-window tombstone can
	// only ever exercise the `years` assertion below — leaving the
	// out_of_range_years assertion dead against a tombstone leak in the query.
	// This row is what makes that second assertion load-bearing, and it covers
	// the case nothing else in this file does: a trashed row from a year no
	// report accepts must be invisible, not merely unreachable.
	seedTombstonedTestTransaction(t, q, user.ID, cat.ID, "3021-01-02", 998, "trashed far future")

	resp := getReportYears(t, h, user)
	years := yearsField(t, resp, "years")
	out := yearsField(t, resp, "out_of_range_years")

	if containsYear(years, 1984) {
		t.Errorf("years = %v — a tombstoned 1984 row must not put 1984 in the picker; "+
			"selecting it renders an empty report the user cannot explain", years)
	}
	if !containsYear(years, 2019) {
		t.Errorf("years = %v, want 2019 present", years)
	}
	// Neither tombstone is an out-of-RANGE year: they do not exist at all.
	if containsYear(out, 1984) {
		t.Errorf("out_of_range_years = %v — a tombstoned row is deleted, not unreachable; "+
			"reporting it would tell the user data is hidden when they threw it away", out)
	}
	if containsYear(out, 3021) {
		t.Errorf("out_of_range_years = %v — a tombstoned row outside the reportable "+
			"window is still deleted; the trash is not a source of out-of-range years", out)
	}
	if containsYear(years, 3021) {
		t.Errorf("years = %v — 3021 is both tombstoned and unreportable", years)
	}
}

// TestReportYears_ToleratesUnparseableDate pins that one corrupt date does not
// take down the whole endpoint.
//
// The query scans rows into a bare int64. SQLite returns NULL from strftime
// for a date text it cannot parse, and a row scan — unlike the MIN() aggregate
// this replaced — cannot absorb that: it fails the entire query with
// "converting NULL to int64 is unsupported". Without the strftime IS NOT NULL
// filter, a single such row 500s BOTH this endpoint and the report-year-floor
// shim built on the same query, where the old aggregate returned 200.
//
// No write path can currently produce such a row (every one binds a time.Time,
// and validateDate accepts only 4-digit years), so this is reachable through
// hand-edited databases and foreign tooling. That is precisely the class of row
// this endpoint's out-of-window reporting exists to surface rather than crash
// on, which is why tolerating it is the correct behaviour and not leniency.
func TestReportYears_ToleratesUnparseableDate(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandlerWithClock(q, db, fixedClock{t: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)})
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "Groceries "+t.Name(), "expense")

	seedTestTransaction(t, q, user.ID, cat.ID, "2019-03-15", 42, "live")
	poisoned := seedTestTransaction(t, q, user.ID, cat.ID, "2020-05-05", 7, "corrupt date")

	// Bypass every validator, exactly as sqlite3 surgery or a foreign import
	// tool would. "not-a-date" is unparseable by strftime, which yields NULL.
	if _, err := db.Exec(`UPDATE transactions SET date = ? WHERE id = ?`, "not-a-date", poisoned.ID); err != nil {
		t.Fatalf("seeding the corrupt row failed: %v", err)
	}

	resp := getReportYears(t, h, user)
	years := yearsField(t, resp, "years")

	if !containsYear(years, 2019) {
		t.Errorf("years = %v, want 2019 present — one unparseable date must not "+
			"suppress the years that ARE readable", years)
	}
	if !containsYear(years, 2026) {
		t.Errorf("years = %v, want the current year present", years)
	}
}

// TestReportYears_ExcludesFutureYears is the single most dangerous property.
// SavingsTab's monthsToCoverYear(year, currentYear) computes
// (currentYear - year + 1) * 12, which goes <= 0 for a future year and floors
// at 24 — producing a trailing 24-month window that renders "0% of goal" and
// $0 for a year that holds real data. A confidently wrong number.
func TestReportYears_ExcludesFutureYears(t *testing.T) {
	h, q, user, cat := reportYearsFixture(t)

	seedTestTransaction(t, q, user.ID, cat.ID, "2026-01-04", 10, "this year")
	seedTestTransaction(t, q, user.ID, cat.ID, "2030-01-01", 10, "planned")

	resp := getReportYears(t, h, user)
	years := yearsField(t, resp, "years")

	if containsYear(years, 2030) {
		t.Errorf("years = %v — 2030 is in the future; SavingsTab would render a trailing "+
			"24-month window as though it were that year and report 0%% of goal", years)
	}
	if !containsYear(years, 2026) {
		t.Errorf("years = %v, want the current year present", years)
	}
	if fut := yearsField(t, resp, "future_years"); !containsYear(fut, 2030) {
		t.Errorf("future_years = %v, want 2030 — a filtered year must be REPORTED, "+
			"or the planned row silently vanishes from the UI's view of the ledger", fut)
	}
	if out := yearsField(t, resp, "out_of_range_years"); containsYear(out, 2030) {
		t.Errorf("out_of_range_years = %v — 2030 is inside [%d, %d]; calling it out of range "+
			"tells the user their deliberate plan is corrupt data", out, MinDataYear, MaxDataYear)
	}
}

// --- the two causes, kept apart ---
//
// `out_of_range_years` and `future_years` used to be one list, and that list
// conflated a DEFECT with a FEATURE. Measured against a live server (binary
// built from this tree, throwaway DB, rows seeded past the API's own
// validator):
//
//	GET /api/reports/years
//	  {"years":[2026],"current_year":2026,"has_transactions":true,
//	   "out_of_range_years":[3021,2027,1850]}
//
//	              1850      3021      2027
//	  budget-vs-actual?year=   400       400       200, actual=999 in month 3
//	  dashboard/summary?year=  400       400       200, savings_ytd=-999
//	  spending-heatmap?year=   400       400       200, contains 999
//
// So 1850 and 3021 are unreachable in a way 2027 is not: every year-param
// endpoint REFUSES them, and no future clock will change that. 2027 is refused
// by nothing — only the picker's own cap keeps it out, and that cap lifts on
// its own when 2027 arrives (TestReportYears_FutureYearBecomesOfferableWhenItArrives).
//
// Reporting them under one key is what made the Reports notice tell a user
// their deliberate 2027 bill was a data problem.

// TestReportYears_FutureInWindowYear_IsFutureNotOutOfRange is the split itself.
//
// SEEDED WITH THE FUTURE ROW ALONE, deliberately. Adding a current-year row
// would make `years` contain 2026 whether the clamp ran or not, and would let
// `has_transactions` pass on the wrong row — the assertions below only mean
// something on a ledger whose ONLY row is the future one.
func TestReportYears_FutureInWindowYear_IsFutureNotOutOfRange(t *testing.T) {
	h, q, user, cat := reportYearsFixture(t)

	seedTestTransaction(t, q, user.ID, cat.ID, "2027-03-04", 999, "planned bill")

	resp := getReportYears(t, h, user)

	if fut := yearsField(t, resp, "future_years"); len(fut) != 1 || fut[0] != 2027 {
		t.Errorf("future_years = %v, want [2027]", fut)
	}
	if out := yearsField(t, resp, "out_of_range_years"); len(out) != 0 {
		t.Errorf("out_of_range_years = %v, want empty — 2027 is inside [%d, %d], so it is "+
			"planning, not corruption; the notice words the two differently", out, MinDataYear, MaxDataYear)
	}
	if years := yearsField(t, resp, "years"); len(years) != 1 || years[0] != 2026 {
		t.Errorf("years = %v, want [2026] — the future year is still not OFFERABLE today", years)
	}
	if has, ok := resp["has_transactions"].(bool); !ok || !has {
		t.Errorf("has_transactions = %v, want true — the planned row exists", resp["has_transactions"])
	}
}

// TestReportYears_OutOfWindowFutureYear_IsOutOfRangeOnly pins the PRECEDENCE
// rule. 3021 qualifies under both causes; the window violation is the more
// actionable fact (every year-param endpoint 400s on it, forever), so it is
// reported there and nowhere else. Listing it twice would name the same year
// in two sentences of one notice, one of which — "reports cover up to 2026,
// this will appear later" — is a promise the endpoint can never keep.
func TestReportYears_OutOfWindowFutureYear_IsOutOfRangeOnly(t *testing.T) {
	h, q, user, cat := reportYearsFixture(t)

	seedTestTransaction(t, q, user.ID, cat.ID, "3021-01-02", 888, "legacy, above the window")

	resp := getReportYears(t, h, user)

	if out := yearsField(t, resp, "out_of_range_years"); len(out) != 1 || out[0] != 3021 {
		t.Errorf("out_of_range_years = %v, want [3021]", out)
	}
	if fut := yearsField(t, resp, "future_years"); len(fut) != 0 {
		t.Errorf("future_years = %v, want empty — 3021 is out of range FIRST; it must be "+
			"named once, not in both halves of the notice", fut)
	}
}

// TestReportYears_BothYearArraysAreEmptyNotNull guards the wire shape for the
// ordinary household. A nil Go slice marshals to `null`, the hook's `?? []`
// only covers a MISSING key, and `null.length` throws — React then unmounts
// the whole Reports page with no error on screen.
//
// Asserted against the RAW body: `null` and `[]` both land in a
// `map[string]any` and only the bytes tell them apart.
func TestReportYears_BothYearArraysAreEmptyNotNull(t *testing.T) {
	h, q, user, cat := reportYearsFixture(t)

	seedTestTransaction(t, q, user.ID, cat.ID, "2026-02-02", 10, "ordinary")

	resp, body := getReportYearsRaw(t, h, user)

	for _, key := range []string{"out_of_range_years", "future_years"} {
		raw, ok := resp[key]
		if !ok {
			t.Fatalf("response has no %q key: %s", key, body)
		}
		if raw == nil {
			t.Errorf("%s is JSON null — the consumer reads .length off it and unmounts the page; body: %s", key, body)
			continue
		}
		if list, ok := raw.([]any); !ok || len(list) != 0 {
			t.Errorf("%s = %v, want an empty array; body: %s", key, raw, body)
		}
		if strings.Contains(body, `"`+key+`":null`) {
			t.Errorf("body encodes %s as null: %s", key, body)
		}
	}
}

// TestReportYears_TombstonedFutureRow_IsInNeitherArray — a planned row that was
// deleted is gone, not "unreachable". Naming it would tell the user their
// reports are hiding something they threw away themselves.
func TestReportYears_TombstonedFutureRow_IsInNeitherArray(t *testing.T) {
	h, q, user, cat := reportYearsFixture(t)

	seedTestTransaction(t, q, user.ID, cat.ID, "2026-02-02", 10, "live")
	seedTombstonedTestTransaction(t, q, user.ID, cat.ID, "2027-03-04", 999, "cancelled plan")

	resp := getReportYears(t, h, user)

	if fut := yearsField(t, resp, "future_years"); containsYear(fut, 2027) {
		t.Errorf("future_years = %v — the 2027 row is tombstoned; it does not exist to the user", fut)
	}
	if out := yearsField(t, resp, "out_of_range_years"); containsYear(out, 2027) {
		t.Errorf("out_of_range_years = %v — a tombstoned row is deleted, not out of range", out)
	}
	if years := yearsField(t, resp, "years"); containsYear(years, 2027) {
		t.Errorf("years = %v — 2027 is both tombstoned and in the future", years)
	}
}

// TestReportYears_FutureYearBecomesOfferableWhenItArrives is the measurement
// behind the notice's one forward-looking claim: that a future-dated row shows
// up on its own once that year begins. Same ledger, clock advanced one year —
// 2027 moves out of `future_years` and into `years`.
//
// The other half of that claim was measured against the live server: with the
// 2027 sentinel seeded, budget-vs-actual?year=2027 already returns
// actual=999 in month 3 and dashboard/summary?year=2027 returns
// savings_ytd=-999. So nothing but the picker's cap is withholding the row,
// and this test is what proves the cap lifts.
func TestReportYears_FutureYearBecomesOfferableWhenItArrives(t *testing.T) {
	q, db := setupTestDB(t)
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "Groceries "+t.Name(), "expense")
	seedTestTransaction(t, q, user.ID, cat.ID, "2027-03-04", 999, "planned bill")

	before := NewHandlerWithClock(q, db, fixedClock{t: time.Date(2026, 12, 31, 23, 0, 0, 0, time.UTC)})
	if fut := yearsField(t, getReportYears(t, before, user), "future_years"); !containsYear(fut, 2027) {
		t.Fatalf("future_years = %v on 2026-12-31, want 2027 — the premise of this test", fut)
	}

	after := NewHandlerWithClock(q, db, fixedClock{t: time.Date(2027, 1, 1, 1, 0, 0, 0, time.UTC)})
	resp := getReportYears(t, after, user)

	if years := yearsField(t, resp, "years"); !containsYear(years, 2027) {
		t.Errorf("years = %v on 2027-01-01, want 2027 offered — the notice promises the row "+
			"appears once the year begins, and this is the only thing that keeps that promise", years)
	}
	if fut := yearsField(t, resp, "future_years"); len(fut) != 0 {
		t.Errorf("future_years = %v on 2027-01-01, want empty — 2027 is no longer in the future", fut)
	}
}

// TestReportYears_ExcludesOutOfWindowYears covers rows the unbounded POST could
// create before validateDate was bounded. Offering one makes every tab 400 at
// the same moment.
func TestReportYears_ExcludesOutOfWindowYears(t *testing.T) {
	h, q, user, cat := reportYearsFixture(t)

	seedTestTransaction(t, q, user.ID, cat.ID, "2019-03-15", 10, "reportable")
	seedTestTransaction(t, q, user.ID, cat.ID, "1850-06-01", 10, "legacy, below the window")
	// Also out of window AND in the future — it must be reported exactly once.
	seedTestTransaction(t, q, user.ID, cat.ID, "3021-01-02", 10, "legacy, above the window")

	resp := getReportYears(t, h, user)
	years := yearsField(t, resp, "years")

	for _, bad := range []int{1850, 3021} {
		if containsYear(years, bad) {
			t.Errorf("years = %v — %d is outside [%d, %d]; every year-param endpoint 400s on it",
				years, bad, MinDataYear, MaxDataYear)
		}
	}
	if !containsYear(years, 2019) {
		t.Errorf("years = %v, want 2019 present", years)
	}

	out := yearsField(t, resp, "out_of_range_years")
	for _, bad := range []int{1850, 3021} {
		if !containsYear(out, bad) {
			t.Errorf("out_of_range_years = %v, want %d — cutting this makes a legacy row vanish silently",
				out, bad)
		}
	}
	// Newest first, and no duplicates: 3021 qualifies on both the window and
	// the future rule, and the window wins (see
	// TestReportYears_OutOfWindowFutureYear_IsOutOfRangeOnly).
	if len(out) != 2 || out[0] != 3021 || out[1] != 1850 {
		t.Errorf("out_of_range_years = %v, want [3021 1850] exactly (newest first, no duplicates)", out)
	}
	if fut := yearsField(t, resp, "future_years"); len(fut) != 0 {
		t.Errorf("future_years = %v, want empty — 3021 is out of range, not merely planned", fut)
	}
}

// TestReportYears_EmptyLedger_ReturnsCurrentYear pins the fallback. A fresh
// install has no rows, and an empty list renders an empty dropdown.
func TestReportYears_EmptyLedger_ReturnsCurrentYear(t *testing.T) {
	h, _, user, _ := reportYearsFixture(t)

	resp := getReportYears(t, h, user)
	years := yearsField(t, resp, "years")

	if len(years) != 1 || years[0] != 2026 {
		t.Errorf("years = %v, want [2026] — an empty ledger must still offer exactly this year", years)
	}
	if has, ok := resp["has_transactions"].(bool); !ok || has {
		t.Errorf("has_transactions = %v, want false on an empty ledger", resp["has_transactions"])
	}
}

// TestReportYears_PastOnlyLedger_StillOffersCurrentYear is the other half of
// property 3. A household whose most recent row is from 2019 must still be
// able to select 2026 — that is where the row they are about to add lands.
func TestReportYears_PastOnlyLedger_StillOffersCurrentYear(t *testing.T) {
	h, q, user, cat := reportYearsFixture(t)

	seedTestTransaction(t, q, user.ID, cat.ID, "2019-03-15", 10, "old")

	resp := getReportYears(t, h, user)
	years := yearsField(t, resp, "years")

	if len(years) != 2 || years[0] != 2026 || years[1] != 2019 {
		t.Errorf("years = %v, want [2026 2019] — the current year is always offered, newest first", years)
	}
	// The union must not fabricate data: the ledger really does hold rows.
	if has, ok := resp["has_transactions"].(bool); !ok || !has {
		t.Errorf("has_transactions = %v, want true", resp["has_transactions"])
	}
}

// TestReportYears_HasTransactionsIsFalseOnlyWhenNoLiveRows pins that
// has_transactions tracks LIVE rows, not "years survived the filters". A
// household whose only row is a legacy 3021 one still HAS transactions — the
// UI must be able to say "you have data, none of it is reportable" rather than
// "you have no data".
func TestReportYears_HasTransactionsIsFalseOnlyWhenNoLiveRows(t *testing.T) {
	t.Run("only an out-of-range row", func(t *testing.T) {
		h, q, user, cat := reportYearsFixture(t)
		seedTestTransaction(t, q, user.ID, cat.ID, "3021-01-02", 10, "legacy")

		resp := getReportYears(t, h, user)
		if has, ok := resp["has_transactions"].(bool); !ok || !has {
			t.Errorf("has_transactions = %v, want true — the row exists, it is just unreportable",
				resp["has_transactions"])
		}
		if years := yearsField(t, resp, "years"); len(years) != 1 || years[0] != 2026 {
			t.Errorf("years = %v, want [2026] — the filtered list still needs the current year", years)
		}
	})

	t.Run("only a tombstoned row", func(t *testing.T) {
		h, q, user, cat := reportYearsFixture(t)
		seedTombstonedTestTransaction(t, q, user.ID, cat.ID, "2019-03-15", 999, "trashed")

		resp := getReportYears(t, h, user)
		if has, ok := resp["has_transactions"].(bool); !ok || has {
			t.Errorf("has_transactions = %v, want false — a tombstoned row does not exist to the user",
				resp["has_transactions"])
		}
	})
}

// TestReportYears_IsHouseholdWide mirrors handleReportYearFloor: the
// transactions list is visible to every authenticated user, so a member scoped
// to their own rows could not select the year another member's import lands in
// while its amounts are already inside every aggregate on the page.
func TestReportYears_IsHouseholdWide(t *testing.T) {
	h, q, alice, cat := reportYearsFixture(t)
	bob := seedTestUser(t, q, "bob", "member")

	seedTestTransaction(t, q, bob.ID, cat.ID, "2019-03-15", 10, "bob's import")

	if years := yearsField(t, getReportYears(t, h, alice), "years"); !containsYear(years, 2019) {
		t.Errorf("years = %v, want 2019 — the picker must span the whole household", years)
	}
}

// TestReportYears_NoAuth_Returns401 — the endpoint is household-wide but still
// behind the authenticated group; an anonymous caller learns nothing about how
// far back the ledger goes.
func TestReportYears_NoAuth_Returns401(t *testing.T) {
	h := setupHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/reports/years", nil)
	rec := httptest.NewRecorder()

	h.handleReportYears(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

// TestReportYearsRoute_IsRegisteredAndRequiresAuth drives the REAL router.
//
// Both halves are needed. The AUTHENTICATED half is what proves the route is
// wired up at all: an anonymous request cannot, because the /api group's auth
// middleware runs before chi's NotFound handler, so an unregistered path 401s
// exactly like a registered one. The anonymous half then proves the route sits
// INSIDE that group rather than beside it.
func TestReportYearsRoute_IsRegisteredAndRequiresAuth(t *testing.T) {
	q, db := setupTestDB(t)
	router := NewRouter(q, db, nil)

	user := seedTestUser(t, q, "alice", "member")
	token, err := auth.GenerateSessionToken()
	if err != nil {
		t.Fatalf("generate session token: %v", err)
	}
	// Sessions are stored hashed; persist the hash, send the plaintext cookie.
	if err := q.CreateSession(context.Background(), database.CreateSessionParams{
		Token:     auth.HashSessionToken(token),
		UserID:    user.ID,
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	authed := httptest.NewRequest(http.MethodGet, "/api/reports/years", nil)
	authed.AddCookie(&http.Cookie{Name: "session", Value: token})
	authedRec := httptest.NewRecorder()
	router.ServeHTTP(authedRec, authed)

	if authedRec.Code != http.StatusOK {
		t.Fatalf("authenticated GET /api/reports/years: got %d, want 200 "+
			"(a 404 here means the route is not registered); body: %s",
			authedRec.Code, authedRec.Body.String())
	}
	// Assert the payload, not just the status: a route accidentally pointed at
	// some other handler would also 200.
	var resp map[string]any
	decodeResponse(t, authedRec, &resp)
	if _, ok := resp["years"]; !ok {
		t.Errorf("authenticated response has no `years` key — the route is pointed at the wrong handler")
	}

	anon := httptest.NewRequest(http.MethodGet, "/api/reports/years", nil)
	anonRec := httptest.NewRecorder()
	router.ServeHTTP(anonRec, anon)

	if anonRec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous GET: got %d, want 401 — the route must sit inside the authenticated group; body: %s",
			anonRec.Code, anonRec.Body.String())
	}
}

// TestReportYears_EveryOfferedYearIsAccepted closes the loop the whole PR is
// about: drive a real report endpoint with each year the picker offers and
// assert none of them 400s. A regression in either the endpoint's window or
// this handler's filter breaks it.
func TestReportYears_EveryOfferedYearIsAccepted(t *testing.T) {
	h, q, user, cat := reportYearsFixture(t)

	// Boundary years plus a legacy row that must NOT be offered.
	for _, d := range []string{
		fmt.Sprintf("%d-01-01", MinDataYear),
		fmt.Sprintf("%d-12-31", MaxDataYear),
		"2019-03-15",
		"1850-06-01",
	} {
		seedTestTransaction(t, q, user.ID, cat.ID, d, 10, "row "+d)
	}

	years := yearsField(t, getReportYears(t, h, user), "years")
	if len(years) < 3 {
		t.Fatalf("years = %v, want at least the three in-window seeds", years)
	}

	for _, year := range years {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/reports/year-over-year?year=%d", year), nil)
		rec := httptest.NewRecorder()
		h.handleReportYoY(rec, withUser(req, user))
		if rec.Code == http.StatusBadRequest {
			t.Errorf("the picker offers %d but /reports/year-over-year 400s on it — "+
				"every year offered must be a year accepted; body: %s", year, rec.Body.String())
		}
	}
}
