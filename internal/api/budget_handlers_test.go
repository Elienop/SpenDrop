package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elienop/spendrop/internal/database"
)

// --- handleGetBudgets ---

func TestHandleGetBudgets_ReturnsBudgetsForYear(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	// Seed some budgets. amount_cents is the only money column (the legacy
	// REAL amount was dropped in migration 010); the get handler reads it
	// back via ListBudgetsByYear.
	err := q.UpsertBudget(context.Background(), database.UpsertBudgetParams{
		Year: 2026, Month: 1, AmountCents: dollarsToCents(2500),
	})
	if err != nil {
		t.Fatalf("seed budget: %v", err)
	}
	err = q.UpsertBudget(context.Background(), database.UpsertBudgetParams{
		Year: 2026, Month: 2, AmountCents: dollarsToCents(3000),
	})
	if err != nil {
		t.Fatalf("seed budget: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/budgets?year=2026", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleGetBudgets(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp []map[string]any
	decodeResponse(t, rec, &resp)
	if len(resp) != 2 {
		t.Fatalf("expected 2 budgets, got %d", len(resp))
	}

	// Wire contract: the budgets DTO emits `amount` in DOLLARS, never the raw
	// `amount_cents` column. January was seeded at $2500.
	jan := resp[0]
	if jan["amount"] == nil {
		t.Fatalf("expected `amount` (dollars) key in budgets response, got %v", jan)
	}
	if jan["amount"].(float64) != 2500 {
		t.Errorf("amount: got %v want 2500 (dollars)", jan["amount"])
	}
	if _, leaked := jan["amount_cents"]; leaked {
		t.Error("amount_cents must NOT leak in budgets response")
	}
}

func TestHandleGetBudgets_DefaultsToCurrentYear(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	req := httptest.NewRequest(http.MethodGet, "/api/budgets", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleGetBudgets(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleGetBudgets_NoAuth_Returns401(t *testing.T) {
	h := setupHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/budgets", nil)
	rec := httptest.NewRecorder()

	h.handleGetBudgets(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// --- handleSetBudget ---

func TestHandleSetBudget_UpsertsBudget(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "admin")

	body := strings.NewReader(`{"amount":2500}`)
	req := httptest.NewRequest(http.MethodPut, "/api/budgets/2026/4", body)
	req = withUserAndURLParams(req, user, map[string]string{"year": "2026", "month": "4"})
	rec := httptest.NewRecorder()

	h.handleSetBudget(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	// Verify in DB
	b, err := q.GetBudget(context.Background(), database.GetBudgetParams{Year: 2026, Month: 4})
	if err != nil {
		t.Fatalf("get budget: %v", err)
	}
	if b.AmountCents != dollarsToCents(2500) {
		t.Errorf("expected amount_cents %d, got %v", dollarsToCents(2500), b.AmountCents)
	}
}

func TestHandleSetBudget_InvalidYear_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "admin")

	body := strings.NewReader(`{"amount":2500}`)
	req := httptest.NewRequest(http.MethodPut, "/api/budgets/abc/4", body)
	req = withUserAndURLParams(req, user, map[string]string{"year": "abc", "month": "4"})
	rec := httptest.NewRecorder()

	h.handleSetBudget(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleSetBudget_InvalidMonth_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "admin")

	body := strings.NewReader(`{"amount":2500}`)
	req := httptest.NewRequest(http.MethodPut, "/api/budgets/2026/13", body)
	req = withUserAndURLParams(req, user, map[string]string{"year": "2026", "month": "13"})
	rec := httptest.NewRecorder()

	h.handleSetBudget(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleSetBudget_ZeroAmount_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "admin")

	body := strings.NewReader(`{"amount":0}`)
	req := httptest.NewRequest(http.MethodPut, "/api/budgets/2026/4", body)
	req = withUserAndURLParams(req, user, map[string]string{"year": "2026", "month": "4"})
	rec := httptest.NewRecorder()

	h.handleSetBudget(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleSetBudget_NoAuth_Returns401(t *testing.T) {
	h := setupHandler(t)

	body := strings.NewReader(`{"amount":2500}`)
	req := httptest.NewRequest(http.MethodPut, "/api/budgets/2026/4", body)
	rctx := chiRouteContext(map[string]string{"year": "2026", "month": "4"})
	req = req.WithContext(context.WithValue(req.Context(), chiRouteCtxKey(), rctx))
	rec := httptest.NewRecorder()

	h.handleSetBudget(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// TestHandleSetBudget_ZeroAmount_DoesNotUnsetExistingBudget is the other half
// of the B6a contract. The status-only test above shows PUT 0 is rejected; this
// one shows the rejection actually PROTECTS a stored budget, so PUT-0 can never
// be used as a backdoor unset. Deletion is the unset path.
func TestHandleSetBudget_ZeroAmount_DoesNotUnsetExistingBudget(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "admin")

	seedBudget(t, q, 2026, 4, 2500)

	body := strings.NewReader(`{"amount":0}`)
	req := httptest.NewRequest(http.MethodPut, "/api/budgets/2026/4", body)
	req = withUserAndURLParams(req, user, map[string]string{"year": "2026", "month": "4"})
	rec := httptest.NewRecorder()

	h.handleSetBudget(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
	b, err := q.GetBudget(context.Background(), database.GetBudgetParams{Year: 2026, Month: 4})
	if err != nil {
		t.Fatalf("the rejected PUT must leave the stored budget intact, but it is gone: %v", err)
	}
	if b.AmountCents != dollarsToCents(2500) {
		t.Errorf("stored amount_cents = %d, want %d — a rejected PUT moved money",
			b.AmountCents, dollarsToCents(2500))
	}
}

// --- handleDeleteBudget (B6a) ---
//
// Clearing a month's budget is a DELETE. Every case below mirrors the
// category-budget delete tests: same auth matrix, same idempotency, same
// response shape.

// seedBudget writes a monthly budget straight through sqlc so the delete tests
// do not depend on the PUT handler they are not testing.
func seedBudget(t *testing.T, q *database.Queries, year, month int64, amount float64) {
	t.Helper()
	if err := q.UpsertBudget(context.Background(), database.UpsertBudgetParams{
		Year:        year,
		Month:       month,
		AmountCents: dollarsToCents(amount),
	}); err != nil {
		t.Fatalf("seed budget %d/%d: %v", year, month, err)
	}
}

// deleteBudgetRequest issues a DELETE for one month as the given user.
func deleteBudgetRequest(t *testing.T, h *Handler, user database.User, year, month string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/api/budgets/"+year+"/"+month, nil)
	req = withUserAndURLParams(req, user, map[string]string{"year": year, "month": month})
	rec := httptest.NewRecorder()
	h.handleDeleteBudget(rec, req)
	return rec
}

func TestHandleDeleteBudget_ClearsBudget(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "admin")

	seedBudget(t, q, 2026, 4, 2500)

	rec := deleteBudgetRequest(t, h, user, "2026", "4")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var body map[string]string
	decodeResponse(t, rec, &body)
	if body["status"] != "deleted" {
		t.Errorf("status = %q, want %q (same shape as the category-budget delete)", body["status"], "deleted")
	}

	// The row is gone, not zeroed: a zero row would mean "budgeted nothing",
	// which renders differently from "no budget of its own".
	if _, err := q.GetBudget(context.Background(), database.GetBudgetParams{Year: 2026, Month: 4}); err == nil {
		t.Error("GetBudget still returns a row after delete")
	}
	rows, err := q.ListBudgetsByYear(context.Background(), 2026)
	if err != nil {
		t.Fatalf("list budgets: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 budgets for 2026 after delete, got %d: %+v", len(rows), rows)
	}
}

// TestHandleDeleteBudget_OnlyDeletesTheTargetMonth gives the delete a rival to
// spare. Without a second month in the table, dropping the `month = ?` half of
// the WHERE clause would wipe the year and still pass every other test here.
func TestHandleDeleteBudget_OnlyDeletesTheTargetMonth(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "admin")

	seedBudget(t, q, 2026, 4, 2500)
	seedBudget(t, q, 2026, 5, 3100)
	seedBudget(t, q, 2025, 4, 1900) // same month, different year

	if rec := deleteBudgetRequest(t, h, user, "2026", "4"); rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	survivor, err := q.GetBudget(context.Background(), database.GetBudgetParams{Year: 2026, Month: 5})
	if err != nil {
		t.Fatalf("2026/5 must survive a delete of 2026/4: %v", err)
	}
	if survivor.AmountCents != dollarsToCents(3100) {
		t.Errorf("2026/5 amount_cents = %d, want %d", survivor.AmountCents, dollarsToCents(3100))
	}
	otherYear, err := q.GetBudget(context.Background(), database.GetBudgetParams{Year: 2025, Month: 4})
	if err != nil {
		t.Fatalf("2025/4 must survive a delete of 2026/4: %v", err)
	}
	if otherYear.AmountCents != dollarsToCents(1900) {
		t.Errorf("2025/4 amount_cents = %d, want %d", otherYear.AmountCents, dollarsToCents(1900))
	}
}

func TestHandleDeleteBudget_Idempotent(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "admin")

	// Delete a month that was never budgeted — must still 200, matching
	// handleDeleteCategoryBudget: the post-condition holds either way.
	rec := deleteBudgetRequest(t, h, user, "2026", "4")
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 (idempotent), got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleDeleteBudget_NonAdmin_Returns403(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	member := seedTestUser(t, q, "bob", "member")

	seedBudget(t, q, 2026, 4, 2500)

	rec := deleteBudgetRequest(t, h, member, "2026", "4")
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d; body: %s", rec.Code, rec.Body.String())
	}
	// The refusal must happen BEFORE the delete. Asserting only the status
	// would pass on a handler that deleted the row and then returned 403.
	if _, err := q.GetBudget(context.Background(), database.GetBudgetParams{Year: 2026, Month: 4}); err != nil {
		t.Errorf("a member's refused DELETE removed the budget anyway: %v", err)
	}
}

func TestHandleDeleteBudget_NoAuth_Returns401(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)

	seedBudget(t, q, 2026, 4, 2500)

	req := httptest.NewRequest(http.MethodDelete, "/api/budgets/2026/4", nil)
	rctx := chiRouteContext(map[string]string{"year": "2026", "month": "4"})
	req = req.WithContext(context.WithValue(req.Context(), chiRouteCtxKey(), rctx))
	rec := httptest.NewRecorder()

	h.handleDeleteBudget(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
	if _, err := q.GetBudget(context.Background(), database.GetBudgetParams{Year: 2026, Month: 4}); err != nil {
		t.Errorf("an unauthenticated DELETE removed the budget anyway: %v", err)
	}
}

func TestHandleDeleteBudget_InvalidPath_Returns400(t *testing.T) {
	cases := []struct {
		name  string
		year  string
		month string
	}{
		{"unparseable year", "abc", "4"},
		{"year below the planning window", "1899", "4"},
		{"unparseable month", "2026", "abc"},
		{"month above 12", "2026", "13"},
		{"month below 1", "2026", "0"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q, db := setupTestDB(t)
			h := NewHandler(q, db)
			user := seedTestUser(t, q, "alice", "admin")

			rec := deleteBudgetRequest(t, h, user, tc.year, tc.month)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestNewRouter_DeleteBudget_RouteIsRegistered drives the REAL router so the
// route registration is proven, not just the handler.
//
// It must be AUTHENTICATED to prove anything. The auth middleware sits in front
// of routing, so an anonymous request to any URL — including a path that does
// not exist at all — returns 401 (verified: DELETE /api/nonexistent-path-xyz
// also 401s). An anonymous-401 assertion here passed with the route deleted.
// With a valid admin session the request reaches the router, so an unregistered
// method surfaces as 405 and the missing wiring is visible.
func TestNewRouter_DeleteBudget_RouteIsRegistered(t *testing.T) {
	q, db := setupTestDB(t)
	router := NewRouter(q, db, nil)

	// The first registered user is the admin.
	regReq := httptest.NewRequest(http.MethodPost, "/api/auth/register",
		strings.NewReader(`{"username":"admin","password":"longpassword"}`))
	regRec := httptest.NewRecorder()
	router.ServeHTTP(regRec, regReq)
	if regRec.Code != http.StatusCreated {
		t.Fatalf("register admin: expected 201, got %d; body: %s", regRec.Code, regRec.Body.String())
	}
	var sessionCookie *http.Cookie
	for _, c := range regRec.Result().Cookies() {
		if c.Name == "session" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("no session cookie from register")
	}

	seedBudget(t, q, 2026, 4, 2500)

	req := httptest.NewRequest(http.MethodDelete, "/api/budgets/2026/4", nil)
	req.AddCookie(sessionCookie)
	// requireJSONContentType (router.go) 415s every cookie-authenticated
	// non-GET, DELETE included. The frontend's ApiClient sets this header on
	// every request, so omitting it here would test a client that does not
	// exist — and would mask the routing result behind a 415.
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code == http.StatusMethodNotAllowed || rec.Code == http.StatusNotFound {
		t.Fatalf("DELETE /api/budgets/{year}/{month} is not registered: got %d; body: %s",
			rec.Code, rec.Body.String())
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 through the real router, got %d; body: %s", rec.Code, rec.Body.String())
	}
	// End-to-end: the route reached the handler and the handler did the work.
	if _, err := q.GetBudget(context.Background(), database.GetBudgetParams{Year: 2026, Month: 4}); err == nil {
		t.Error("the routed DELETE returned 200 but the budget row is still there")
	}
}

// --- handleDefaultBudget ---

func TestHandleDefaultBudget_GET_ReturnsCurrentDefault(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	req := httptest.NewRequest(http.MethodGet, "/api/settings/default-budget", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleDefaultBudget(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	// Seed data has default_budget = "2000"
	if resp["amount"] == nil {
		t.Error("expected amount in response")
	}
}

func TestHandleDefaultBudget_PUT_UpdatesDefault(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "admin")

	body := strings.NewReader(`{"amount":3000}`)
	req := httptest.NewRequest(http.MethodPut, "/api/settings/default-budget", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleDefaultBudget(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	// Verify via GET
	getReq := httptest.NewRequest(http.MethodGet, "/api/settings/default-budget", nil)
	getReq = withUser(getReq, user)
	getRec := httptest.NewRecorder()
	h.handleDefaultBudget(getRec, getReq)

	var resp map[string]any
	decodeResponse(t, getRec, &resp)
	if resp["amount"].(float64) != 3000 {
		t.Errorf("expected amount 3000, got %v", resp["amount"])
	}
}

func TestHandleDefaultBudget_NoAuth_Returns401(t *testing.T) {
	h := setupHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/default-budget", nil)
	rec := httptest.NewRecorder()

	h.handleDefaultBudget(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}
