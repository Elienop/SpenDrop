package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
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
	req := httptest.NewRequest(http.MethodGet, "/api/reports/years", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleReportYears(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	decodeResponse(t, rec, &resp)
	return resp
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
}

// TestReportYears_HidesTombstoned is the soft-delete invariant and the test
// that catches a missing `deleted_at IS NULL`. The tombstoned row carries a
// sentinel amount so it is unmistakable in a failure dump.
func TestReportYears_HidesTombstoned(t *testing.T) {
	h, q, user, cat := reportYearsFixture(t)

	seedTestTransaction(t, q, user.ID, cat.ID, "2019-03-15", 42, "live")
	seedTombstonedTestTransaction(t, q, user.ID, cat.ID, "1984-06-01", 999, "trashed")

	years := yearsField(t, getReportYears(t, h, user), "years")

	if containsYear(years, 1984) {
		t.Errorf("years = %v — a tombstoned 1984 row must not put 1984 in the picker; "+
			"selecting it renders an empty report the user cannot explain", years)
	}
	if !containsYear(years, 2019) {
		t.Errorf("years = %v, want 2019 present", years)
	}
	// It is not an out-of-RANGE year either: it does not exist at all.
	if out := yearsField(t, getReportYears(t, h, user), "out_of_range_years"); containsYear(out, 1984) {
		t.Errorf("out_of_range_years = %v — a tombstoned row is deleted, not unreachable; "+
			"reporting it would tell the user data is hidden when they threw it away", out)
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
	if out := yearsField(t, resp, "out_of_range_years"); !containsYear(out, 2030) {
		t.Errorf("out_of_range_years = %v, want 2030 — a filtered year must be REPORTED, "+
			"or the planned row silently vanishes from the UI's view of the ledger", out)
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
	// the future rule.
	if len(out) != 2 || out[0] != 3021 || out[1] != 1850 {
		t.Errorf("out_of_range_years = %v, want [3021 1850] exactly (newest first, no duplicates)", out)
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
