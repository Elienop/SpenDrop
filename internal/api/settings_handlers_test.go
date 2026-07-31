package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/database"
)

// --- handleReportYearFloor ---
//
// The Reports year picker used to count down from a hard-coded 2024
// (HISTORICAL_YEAR_START in web/src/lib/dates.ts). Import deliberately accepts
// 1900-2100, so a user could import a 2019 statement, have it stored and
// aggregated, and never be able to select 2019 in any Reports tab. This
// endpoint replaces the constant with the real floor of the ledger.

// floorResponse mirrors the wire contract exactly. Decoded as a map in the
// assertions below (not this struct) wherever a missing key must be
// distinguishable from a zero value.
func decodeFloor(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var resp map[string]any
	decodeResponse(t, rec, &resp)
	return resp
}

// floorYear pulls floor_year out of the decoded body, failing loudly when the
// key is absent — a typed decode would silently zero-fill it.
func floorYear(t *testing.T, resp map[string]any) int {
	t.Helper()
	raw, ok := resp["floor_year"]
	if !ok {
		t.Fatalf("response has no `floor_year` key: %v", resp)
	}
	n, ok := raw.(float64)
	if !ok {
		t.Fatalf("floor_year is %T, want a JSON number: %v", raw, raw)
	}
	return int(n)
}

func floorBool(t *testing.T, resp map[string]any, key string) bool {
	t.Helper()
	raw, ok := resp[key]
	if !ok {
		t.Fatalf("response has no `%s` key: %v", key, resp)
	}
	b, ok := raw.(bool)
	if !ok {
		t.Fatalf("%s is %T, want a JSON boolean: %v", key, raw, raw)
	}
	return b
}

// getFloor drives the handler with an authenticated member and returns the
// decoded body.
func getFloor(t *testing.T, h *Handler, user database.User) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/settings/report-year-floor", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleReportYearFloor(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	return decodeFloor(t, rec)
}

// TestHandleReportYearFloor_EmptyLedger_ReturnsCurrentYear pins the documented
// empty-ledger fallback. MIN() over zero rows is NULL, so the floor has to come
// from somewhere: the current year, which makes the picker offer exactly this
// year rather than 27 empty ones. The clock is frozen so the assertion cannot
// go vacuous on a New Year rollover.
func TestHandleReportYearFloor_EmptyLedger_ReturnsCurrentYear(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandlerWithClock(q, db, fixedClock{t: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)})
	user := seedTestUser(t, q, "alice", "member")

	resp := getFloor(t, h, user)

	if got := floorYear(t, resp); got != 2026 {
		t.Errorf("floor_year: got %d, want 2026 (the frozen current year)", got)
	}
	if floorBool(t, resp, "has_transactions") {
		t.Error("has_transactions: got true, want false on an empty ledger")
	}
	if floorBool(t, resp, "clamped") {
		t.Error("clamped: got true, want false on an empty ledger")
	}
}

// TestHandleReportYearFloor_ReturnsOldestLiveYear is the headline case: a 2019
// statement was imported, so 2019 must be selectable.
func TestHandleReportYearFloor_ReturnsOldestLiveYear(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandlerWithClock(q, db, fixedClock{t: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)})
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "Groceries "+t.Name(), "expense")

	seedTestTransaction(t, q, user.ID, cat.ID, "2019-03-15", 42, "imported statement")
	seedTestTransaction(t, q, user.ID, cat.ID, "2026-01-04", 10, "recent")

	resp := getFloor(t, h, user)

	if got := floorYear(t, resp); got != 2019 {
		t.Errorf("floor_year: got %d, want 2019", got)
	}
	if !floorBool(t, resp, "has_transactions") {
		t.Error("has_transactions: got false, want true")
	}
	if floorBool(t, resp, "clamped") {
		t.Error("clamped: got true, want false — 2019 is above PlanningMinYear")
	}
}

// TestHandleReportYearFloor_SpansSeveralYears seeds out of chronological order
// so a query that returned "the first row" rather than the minimum would fail.
func TestHandleReportYearFloor_SpansSeveralYears(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandlerWithClock(q, db, fixedClock{t: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)})
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "Groceries "+t.Name(), "expense")

	// Insertion order deliberately unrelated to date order.
	seedTestTransaction(t, q, user.ID, cat.ID, "2023-06-01", 10, "c")
	seedTestTransaction(t, q, user.ID, cat.ID, "2019-12-31", 10, "a")
	seedTestTransaction(t, q, user.ID, cat.ID, "2021-02-14", 10, "b")
	seedTestTransaction(t, q, user.ID, cat.ID, "2026-05-05", 10, "d")

	if got := floorYear(t, getFloor(t, h, user)); got != 2019 {
		t.Errorf("floor_year: got %d, want 2019 (the minimum across all live rows)", got)
	}
}

// TestHandleReportYearFloor_HidesTombstoned is the soft-delete invariant. A
// tombstoned 2015 row does not exist from the user's perspective, so it must
// not widen the picker to a year with nothing in it. This is the test that
// catches a missing `deleted_at IS NULL`.
func TestHandleReportYearFloor_HidesTombstoned(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandlerWithClock(q, db, fixedClock{t: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)})
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "Groceries "+t.Name(), "expense")

	seedTombstonedTestTransaction(t, q, user.ID, cat.ID, "2015-08-09", 999, "trashed")
	seedTestTransaction(t, q, user.ID, cat.ID, "2019-03-15", 42, "live")

	if got := floorYear(t, getFloor(t, h, user)); got != 2019 {
		t.Errorf("floor_year: got %d, want 2019 — a tombstoned 2015 row must not widen the picker", got)
	}
}

// TestHandleReportYearFloor_ClampsBelowMinYear pins the clamp. Import accepts
// 1900-2100 but every year-param endpoint validates against PlanningMinYear, so an
// unclamped 1995 floor would let the picker offer a year that 400s on every
// request the tab then makes.
func TestHandleReportYearFloor_ClampsBelowMinYear(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandlerWithClock(q, db, fixedClock{t: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)})
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "Groceries "+t.Name(), "expense")

	seedTestTransaction(t, q, user.ID, cat.ID, "1995-06-01", 12, "very old statement")
	seedTestTransaction(t, q, user.ID, cat.ID, "2019-03-15", 42, "old statement")

	resp := getFloor(t, h, user)

	if got := floorYear(t, resp); got != PlanningMinYear {
		t.Errorf("floor_year: got %d, want PlanningMinYear (%d) — an unclamped floor offers years the API rejects", got, PlanningMinYear)
	}
	if !floorBool(t, resp, "clamped") {
		t.Error("clamped: got false, want true — live rows exist below PlanningMinYear")
	}
	if !floorBool(t, resp, "has_transactions") {
		t.Error("has_transactions: got false, want true")
	}
}

// TestHandleReportYearFloor_ClampBoundaryIsInclusive pins that a row dated
// exactly in PlanningMinYear is NOT reported as clamped — the clamp must be `<`, not
// `<=`, or the frontend shows a "data older than 2000 is not reportable"
// hint to a user whose oldest row is perfectly reportable.
func TestHandleReportYearFloor_ClampBoundaryIsInclusive(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandlerWithClock(q, db, fixedClock{t: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)})
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "Groceries "+t.Name(), "expense")

	seedTestTransaction(t, q, user.ID, cat.ID, "2000-01-01", 12, "oldest reportable")

	resp := getFloor(t, h, user)

	if got := floorYear(t, resp); got != PlanningMinYear {
		t.Errorf("floor_year: got %d, want %d", got, PlanningMinYear)
	}
	if floorBool(t, resp, "clamped") {
		t.Error("clamped: got true, want false — a row dated in PlanningMinYear itself is reportable")
	}
}

// TestHandleReportYearFloor_FutureOnlyLedger_HeldAtCurrentYear pins the
// ceiling. The picker counts DOWN from the current year, so a ledger holding
// only future-dated rows (planned or scheduled entries) would otherwise report
// a floor above the ceiling and render an empty dropdown.
func TestHandleReportYearFloor_FutureOnlyLedger_HeldAtCurrentYear(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandlerWithClock(q, db, fixedClock{t: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)})
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "Groceries "+t.Name(), "expense")

	seedTestTransaction(t, q, user.ID, cat.ID, "2030-01-01", 12, "planned")

	resp := getFloor(t, h, user)

	if got := floorYear(t, resp); got != 2026 {
		t.Errorf("floor_year: got %d, want 2026 — the floor must never exceed the current year", got)
	}
	if floorBool(t, resp, "clamped") {
		t.Error("clamped: got true, want false — `clamped` reports rows below PlanningMinYear, not future rows")
	}
}

// TestHandleReportYearFloor_NoAuth_Returns401 — the picker is household-wide
// but still behind the authenticated group; an anonymous caller learns nothing
// about how far back the household's ledger goes.
func TestHandleReportYearFloor_NoAuth_Returns401(t *testing.T) {
	h := setupHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/report-year-floor", nil)
	rec := httptest.NewRecorder()

	h.handleReportYearFloor(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

// TestReportYearFloorRoute_IsRegisteredAndRequiresAuth drives the REAL router,
// not the handler in isolation.
//
// Two halves, and both are needed. The authenticated half is what proves the
// route is wired up at all: an anonymous request cannot, because the /api
// group's auth middleware runs before chi's NotFound handler, so an
// unregistered path 401s exactly like a registered one. The anonymous half
// then proves the route sits INSIDE that group rather than beside it.
func TestReportYearFloorRoute_IsRegisteredAndRequiresAuth(t *testing.T) {
	q, db := setupTestDB(t)
	router := NewRouter(q, db, nil)

	// --- registered: an authenticated caller gets the real payload ---
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

	authed := httptest.NewRequest(http.MethodGet, "/api/settings/report-year-floor", nil)
	authed.AddCookie(&http.Cookie{Name: "session", Value: token})
	authedRec := httptest.NewRecorder()
	router.ServeHTTP(authedRec, authed)

	if authedRec.Code != http.StatusOK {
		t.Fatalf("authenticated GET /api/settings/report-year-floor: got %d, want 200 "+
			"(a 404 here means the route is not registered); body: %s",
			authedRec.Code, authedRec.Body.String())
	}
	// Assert the payload, not just the status: a route accidentally pointed at
	// some other handler would also 200.
	if _, ok := decodeFloor(t, authedRec)["floor_year"]; !ok {
		t.Error("authenticated response has no `floor_year` key — the route is pointed at the wrong handler")
	}

	// --- guarded: an anonymous caller is refused ---
	anon := httptest.NewRequest(http.MethodGet, "/api/settings/report-year-floor", nil)
	anonRec := httptest.NewRecorder()
	router.ServeHTTP(anonRec, anon)

	if anonRec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous GET: got %d, want 401 — the route must sit inside the authenticated group; body: %s",
			anonRec.Code, anonRec.Body.String())
	}
}

// TestHandleReportYearFloor_IsHouseholdWide pins that the floor spans every
// member's rows, not just the caller's. The transactions list is deliberately
// household-wide (handleListTransactions), so the picker must be too — a
// member scoped to their own rows would be unable to select the year another
// member's imported statement lands in, while that statement's amounts are
// already inside every aggregate on the page.
func TestHandleReportYearFloor_IsHouseholdWide(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandlerWithClock(q, db, fixedClock{t: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)})
	alice := seedTestUser(t, q, "alice", "member")
	bob := seedTestUser(t, q, "bob", "member")
	cat := seedTestCategory(t, q, "Groceries "+t.Name(), "expense")

	seedTestTransaction(t, q, bob.ID, cat.ID, "2019-03-15", 42, "bob's import")
	seedTestTransaction(t, q, alice.ID, cat.ID, "2026-01-04", 10, "alice's row")

	if got := floorYear(t, getFloor(t, h, alice)); got != 2019 {
		t.Errorf("floor_year: got %d, want 2019 — the floor must span the whole household", got)
	}
}
