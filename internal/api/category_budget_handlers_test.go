package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/elienop/spendrop/internal/database"
)

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// withURLParamsOnly attaches chi URL params without a user on the context,
// for the no-auth 401 path.
func withURLParamsOnly(r *http.Request, params map[string]string) *http.Request {
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// seedExpenseCat creates an expense category with a name suffixed by the test
// name to avoid colliding with the migration-seeded defaults (e.g. "Food").
func seedExpenseCat(t *testing.T, q *database.Queries, name string) database.Category {
	t.Helper()
	return seedTestCategory(t, q, name+"-"+t.Name(), CategoryTypeExpense)
}

// seedIncomeCat creates an income category with a test-name-suffixed name.
func seedIncomeCat(t *testing.T, q *database.Queries, name string) database.Category {
	t.Helper()
	return seedTestCategory(t, q, name+"-"+t.Name(), CategoryTypeIncome)
}

// --- handleSetCategoryBudget (PUT) ---

func TestHandleSetCategoryBudget_UpsertsLimit(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "admin")
	cat := seedExpenseCat(t, q, "Groceries")

	body := strings.NewReader(`{"amount":250.50}`)
	req := httptest.NewRequest(http.MethodPut, "/api/category-budgets/2026/4/1", body)
	req = withUserAndURLParams(req, user, map[string]string{
		"year": "2026", "month": "4", "categoryId": itoa(cat.ID),
	})
	rec := httptest.NewRecorder()

	h.handleSetCategoryBudget(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	rows, err := q.ListCategoryBudgetsByMonth(context.Background(), database.ListCategoryBudgetsByMonthParams{Year: 2026, Month: 4})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].CategoryID != cat.ID {
		t.Errorf("category_id: got %d want %d", rows[0].CategoryID, cat.ID)
	}
	if rows[0].AmountCents != dollarsToCents(250.50) {
		t.Errorf("amount_cents: got %d want %d", rows[0].AmountCents, dollarsToCents(250.50))
	}
}

func TestHandleSetCategoryBudget_OverwritesExisting(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "admin")
	cat := seedExpenseCat(t, q, "Groceries")

	put := func(amount string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/x", strings.NewReader(`{"amount":`+amount+`}`))
		req = withUserAndURLParams(req, user, map[string]string{
			"year": "2026", "month": "4", "categoryId": itoa(cat.ID),
		})
		rec := httptest.NewRecorder()
		h.handleSetCategoryBudget(rec, req)
		return rec
	}

	if rec := put("100"); rec.Code != http.StatusOK {
		t.Fatalf("first put: %d", rec.Code)
	}
	if rec := put("300"); rec.Code != http.StatusOK {
		t.Fatalf("second put: %d", rec.Code)
	}

	rows, _ := q.ListCategoryBudgetsByMonth(context.Background(), database.ListCategoryBudgetsByMonthParams{Year: 2026, Month: 4})
	if len(rows) != 1 {
		t.Fatalf("expected 1 row after overwrite, got %d", len(rows))
	}
	if rows[0].AmountCents != dollarsToCents(300) {
		t.Errorf("amount_cents: got %d want %d", rows[0].AmountCents, dollarsToCents(300))
	}
}

func TestHandleSetCategoryBudget_NonAdmin_Returns403(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "bob", "member")
	cat := seedExpenseCat(t, q, "Groceries")

	body := strings.NewReader(`{"amount":100}`)
	req := httptest.NewRequest(http.MethodPut, "/x", body)
	req = withUserAndURLParams(req, user, map[string]string{
		"year": "2026", "month": "4", "categoryId": itoa(cat.ID),
	})
	rec := httptest.NewRecorder()

	h.handleSetCategoryBudget(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestHandleSetCategoryBudget_NoAuth_Returns401(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	cat := seedExpenseCat(t, q, "Groceries")

	body := strings.NewReader(`{"amount":100}`)
	req := httptest.NewRequest(http.MethodPut, "/x", body)
	req = withURLParamsOnly(req, map[string]string{
		"year": "2026", "month": "4", "categoryId": itoa(cat.ID),
	})
	rec := httptest.NewRecorder()

	h.handleSetCategoryBudget(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestHandleSetCategoryBudget_ZeroAmount_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "admin")
	cat := seedExpenseCat(t, q, "Groceries")

	body := strings.NewReader(`{"amount":0}`)
	req := httptest.NewRequest(http.MethodPut, "/x", body)
	req = withUserAndURLParams(req, user, map[string]string{
		"year": "2026", "month": "4", "categoryId": itoa(cat.ID),
	})
	rec := httptest.NewRecorder()

	h.handleSetCategoryBudget(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleSetCategoryBudget_OverMax_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "admin")
	cat := seedExpenseCat(t, q, "Groceries")

	body := strings.NewReader(`{"amount":1000000001}`)
	req := httptest.NewRequest(http.MethodPut, "/x", body)
	req = withUserAndURLParams(req, user, map[string]string{
		"year": "2026", "month": "4", "categoryId": itoa(cat.ID),
	})
	rec := httptest.NewRecorder()

	h.handleSetCategoryBudget(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleSetCategoryBudget_UnknownCategory_Returns404(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "admin")

	body := strings.NewReader(`{"amount":100}`)
	req := httptest.NewRequest(http.MethodPut, "/x", body)
	req = withUserAndURLParams(req, user, map[string]string{
		"year": "2026", "month": "4", "categoryId": "999999",
	})
	rec := httptest.NewRecorder()

	h.handleSetCategoryBudget(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleSetCategoryBudget_IncomeCategory_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "admin")
	cat := seedIncomeCat(t, q, "Salary")

	body := strings.NewReader(`{"amount":100}`)
	req := httptest.NewRequest(http.MethodPut, "/x", body)
	req = withUserAndURLParams(req, user, map[string]string{
		"year": "2026", "month": "4", "categoryId": itoa(cat.ID),
	})
	rec := httptest.NewRecorder()

	h.handleSetCategoryBudget(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for income category, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleSetCategoryBudget_InvalidYear_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "admin")
	cat := seedExpenseCat(t, q, "Groceries")

	body := strings.NewReader(`{"amount":100}`)
	req := httptest.NewRequest(http.MethodPut, "/x", body)
	req = withUserAndURLParams(req, user, map[string]string{
		"year": "abc", "month": "4", "categoryId": itoa(cat.ID),
	})
	rec := httptest.NewRecorder()

	h.handleSetCategoryBudget(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleSetCategoryBudget_InvalidMonth_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "admin")
	cat := seedExpenseCat(t, q, "Groceries")

	body := strings.NewReader(`{"amount":100}`)
	req := httptest.NewRequest(http.MethodPut, "/x", body)
	req = withUserAndURLParams(req, user, map[string]string{
		"year": "2026", "month": "13", "categoryId": itoa(cat.ID),
	})
	rec := httptest.NewRecorder()

	h.handleSetCategoryBudget(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleSetCategoryBudget_InvalidCategoryID_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "admin")

	body := strings.NewReader(`{"amount":100}`)
	req := httptest.NewRequest(http.MethodPut, "/x", body)
	req = withUserAndURLParams(req, user, map[string]string{
		"year": "2026", "month": "4", "categoryId": "xyz",
	})
	rec := httptest.NewRecorder()

	h.handleSetCategoryBudget(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// --- handleGetCategoryBudgets (GET) ---

func TestHandleGetCategoryBudgets_ReturnsDollarsForMonth(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	cat := seedExpenseCat(t, q, "Groceries")

	if err := q.UpsertCategoryBudget(context.Background(), database.UpsertCategoryBudgetParams{
		Year: 2026, Month: 4, CategoryID: cat.ID, AmountCents: dollarsToCents(250.50),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// A limit in a different month must not leak.
	other := seedExpenseCat(t, q, "Other")
	if err := q.UpsertCategoryBudget(context.Background(), database.UpsertCategoryBudgetParams{
		Year: 2026, Month: 5, CategoryID: other.ID, AmountCents: dollarsToCents(99),
	}); err != nil {
		t.Fatalf("seed other: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/category-budgets?year=2026&month=4", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleGetCategoryBudgets(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp []map[string]any
	decodeResponse(t, rec, &resp)
	if len(resp) != 1 {
		t.Fatalf("expected 1 limit, got %d", len(resp))
	}
	if resp[0]["category_id"].(float64) != float64(cat.ID) {
		t.Errorf("category_id: got %v want %d", resp[0]["category_id"], cat.ID)
	}
	if resp[0]["amount"].(float64) != 250.50 {
		t.Errorf("amount: got %v want 250.50 (dollars)", resp[0]["amount"])
	}
	if _, leaked := resp[0]["amount_cents"]; leaked {
		t.Error("amount_cents must NOT leak in category-budgets response")
	}
}

func TestHandleGetCategoryBudgets_NoAuth_Returns401(t *testing.T) {
	h := setupHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/category-budgets?year=2026&month=4", nil)
	rec := httptest.NewRecorder()

	h.handleGetCategoryBudgets(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestHandleGetCategoryBudgets_MissingMonth_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	req := httptest.NewRequest(http.MethodGet, "/api/category-budgets?year=2026", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleGetCategoryBudgets(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// --- handleDeleteCategoryBudget (DELETE) ---

func TestHandleDeleteCategoryBudget_ClearsLimit(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "admin")
	cat := seedExpenseCat(t, q, "Groceries")

	if err := q.UpsertCategoryBudget(context.Background(), database.UpsertCategoryBudgetParams{
		Year: 2026, Month: 4, CategoryID: cat.ID, AmountCents: dollarsToCents(100),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/x", nil)
	req = withUserAndURLParams(req, user, map[string]string{
		"year": "2026", "month": "4", "categoryId": itoa(cat.ID),
	})
	rec := httptest.NewRecorder()

	h.handleDeleteCategoryBudget(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	rows, _ := q.ListCategoryBudgetsByMonth(context.Background(), database.ListCategoryBudgetsByMonthParams{Year: 2026, Month: 4})
	if len(rows) != 0 {
		t.Errorf("expected 0 rows after delete, got %d", len(rows))
	}
}

func TestHandleDeleteCategoryBudget_Idempotent(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "admin")
	cat := seedExpenseCat(t, q, "Groceries")

	// Delete a limit that was never set — must still 200.
	req := httptest.NewRequest(http.MethodDelete, "/x", nil)
	req = withUserAndURLParams(req, user, map[string]string{
		"year": "2026", "month": "4", "categoryId": itoa(cat.ID),
	})
	rec := httptest.NewRecorder()

	h.handleDeleteCategoryBudget(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 (idempotent), got %d", rec.Code)
	}
}

func TestHandleDeleteCategoryBudget_NonAdmin_Returns403(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "bob", "member")
	cat := seedExpenseCat(t, q, "Groceries")

	req := httptest.NewRequest(http.MethodDelete, "/x", nil)
	req = withUserAndURLParams(req, user, map[string]string{
		"year": "2026", "month": "4", "categoryId": itoa(cat.ID),
	})
	rec := httptest.NewRecorder()

	h.handleDeleteCategoryBudget(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}
