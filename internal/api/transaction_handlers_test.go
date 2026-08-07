package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/database"
	"github.com/elienop/spendrop/internal/ratelimit"
)

// errSendBoom is a shared sentinel for tests that force every push Send to
// fail, asserting the (already-committed) mutation never errors as a result.
var errSendBoom = errors.New("send boom")

// withUser injects an authenticated user into the request context.
func withUser(r *http.Request, user database.User) *http.Request {
	ctx := context.WithValue(r.Context(), auth.UserContextKey, user)
	return r.WithContext(ctx)
}

// withURLParam sets a chi URL parameter on the request.
func withURLParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// withUserAndURLParam sets both user context and a chi URL param.
func withUserAndURLParam(r *http.Request, user database.User, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	ctx := context.WithValue(r.Context(), chi.RouteCtxKey, rctx)
	ctx = context.WithValue(ctx, auth.UserContextKey, user)
	return r.WithContext(ctx)
}

// chiRouteContext creates a chi route context with multiple URL params.
func chiRouteContext(params map[string]string) *chi.Context {
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return rctx
}

// chiRouteCtxKey returns the chi route context key for use with context.WithValue.
func chiRouteCtxKey() any {
	return chi.RouteCtxKey
}

// withUserAndURLParams sets both user context and multiple chi URL params.
func withUserAndURLParams(r *http.Request, user database.User, params map[string]string) *http.Request {
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	ctx := context.WithValue(r.Context(), chi.RouteCtxKey, rctx)
	ctx = context.WithValue(ctx, auth.UserContextKey, user)
	return r.WithContext(ctx)
}

// seedTestUser creates a test user directly in the DB.
func seedTestUser(t *testing.T, q *database.Queries, username, role string) database.User {
	t.Helper()
	hash, _ := auth.HashPassword("testpassword")
	user, err := q.CreateUser(context.Background(), database.CreateUserParams{
		Username:     username,
		PasswordHash: hash,
		DisplayName:  username,
		Role:         role,
	})
	if err != nil {
		t.Fatalf("seed user %s: %v", username, err)
	}
	return user
}

// seedTestCategory creates a test category.
func seedTestCategory(t *testing.T, q *database.Queries, name, catType string) database.Category {
	t.Helper()
	cat, err := q.CreateCategory(context.Background(), database.CreateCategoryParams{
		Name:      name,
		Type:      catType,
		SortOrder: 0,
	})
	if err != nil {
		t.Fatalf("seed category %s: %v", name, err)
	}
	return cat
}

// seedTestCurrency creates a test currency.
func seedTestCurrency(t *testing.T, q *database.Queries, code string, rate float64, isBase bool) {
	t.Helper()
	err := q.UpsertCurrency(context.Background(), database.UpsertCurrencyParams{
		Code:       code,
		Name:       code,
		Symbol:     "$",
		RateToBase: rate,
		IsBase:     isBase,
	})
	if err != nil {
		t.Fatalf("seed currency %s: %v", code, err)
	}
}

// seedTestTransaction creates a transaction directly via sqlc. Phase 3.1b:
// the legacy REAL amount column was dropped in migration 010, so the row
// carries amount_cents only — the same single money column the handler-path
// inserts produce in production.
func seedTestTransaction(t *testing.T, q *database.Queries, userID, categoryID int64, date string, amount float64, desc string) database.Transaction {
	t.Helper()
	d, _ := time.Parse("2006-01-02", date)
	txn, err := q.CreateTransaction(context.Background(), database.CreateTransactionParams{
		UserID:      userID,
		Date:        d,
		AmountCents: dollarsToCents(amount),
		Description: desc,
		CategoryID:  categoryID,
	})
	if err != nil {
		t.Fatalf("seed transaction: %v", err)
	}
	return txn
}

// seedTestTransactionWithTags creates a transaction with tags set. Phase
// 3.1b: writes amount_cents only (see seedTestTransaction).
func seedTestTransactionWithTags(t *testing.T, q *database.Queries, userID, categoryID int64, date string, amount float64, desc, tags string) database.Transaction {
	t.Helper()
	d, _ := time.Parse("2006-01-02", date)
	txn, err := q.CreateTransaction(context.Background(), database.CreateTransactionParams{
		UserID:      userID,
		Date:        d,
		AmountCents: dollarsToCents(amount),
		Description: desc,
		CategoryID:  categoryID,
		Tags:        sql.NullString{String: tags, Valid: tags != ""},
	})
	if err != nil {
		t.Fatalf("seed transaction with tags: %v", err)
	}
	return txn
}

// seedTombstonedTestTransaction creates a transaction and immediately soft-
// deletes it. Used by the soft-delete read-path tests: each aggregator /
// reporter / exporter / suggestion query must skip a row that exists in the
// DB with deleted_at set. Seeding a tombstone straight from tests avoids
// tying the tests to the handler-path delete behaviour they're trying to
// verify independently.
func seedTombstonedTestTransaction(t *testing.T, q *database.Queries, userID, categoryID int64, date string, amount float64, desc string) database.Transaction {
	t.Helper()
	txn := seedTestTransaction(t, q, userID, categoryID, date, amount, desc)
	if err := q.SoftDeleteTransaction(context.Background(), txn.ID); err != nil {
		t.Fatalf("soft-delete seeded transaction: %v", err)
	}
	return txn
}

// --- handleCreateTransaction ---

func TestHandleCreateTransaction_ValidInput_Returns201(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)

	user := seedTestUser(t, q, "alice", "member")
	// Use a seed category (Food, id=1 from migrations)

	body := strings.NewReader(`{
		"date": "2026-04-06",
		"amount": 50.00,
		"description": "Groceries",
		"category_id": 1,
		"notes": "Weekly shopping"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/transactions", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleCreateTransaction(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	if resp["description"] != "Groceries" {
		t.Errorf("expected description 'Groceries', got %v", resp["description"])
	}
	if resp["amount"].(float64) != 50.0 {
		t.Errorf("expected amount 50, got %v", resp["amount"])
	}
	// Money Wire-Edge DTO discipline: the response emits dollars, never *_cents.
	// A typed-struct decode would zero-fill a renamed field and hide a future
	// raw-row regression, so assert the cents keys are absent on the raw map.
	if _, leaked := resp["amount_cents"]; leaked {
		t.Error("amount_cents must NOT leak in transaction response — frontend reads amount (dollars)")
	}
	if _, leaked := resp["original_amount_cents"]; leaked {
		t.Error("original_amount_cents must NOT leak in transaction response")
	}
}

func TestHandleListTransactions_DoesNotLeakAmountCents(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	// One base-currency row via the handler so the DTO path is exercised.
	body := strings.NewReader(`{"date":"2026-04-06","amount":50.00,"description":"USD","category_id":1}`)
	create := httptest.NewRequest(http.MethodPost, "/api/transactions", body)
	create = withUser(create, user)
	h.handleCreateTransaction(httptest.NewRecorder(), create)

	// One foreign-currency row so original_amount(_cents) is populated.
	// LBP rate 89000 from seed data → 89000/89000 = $1.00, original_amount=89000.
	fx := strings.NewReader(`{"date":"2026-04-06","original_amount":89000,"original_currency":"LBP","description":"LBP","category_id":1}`)
	createFx := httptest.NewRequest(http.MethodPost, "/api/transactions", fx)
	createFx = withUser(createFx, user)
	h.handleCreateTransaction(httptest.NewRecorder(), createFx)

	req := httptest.NewRequest(http.MethodGet, "/api/transactions", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleListTransactions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Decode into []map[string]any: a typed-struct decode would zero-fill a
	// renamed/missing field and hide a *_cents leak. This is the exact decode
	// the Money Wire-Edge DTO discipline mandates for every money read.
	var resp struct {
		Transactions []map[string]any `json:"transactions"`
	}
	decodeResponse(t, rec, &resp)
	if len(resp.Transactions) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(resp.Transactions))
	}
	for _, tx := range resp.Transactions {
		if _, ok := tx["amount"].(float64); !ok {
			t.Errorf("row missing dollar `amount` float: %v", tx)
		}
		if _, leaked := tx["amount_cents"]; leaked {
			t.Errorf("amount_cents leaked in list response: %v", tx)
		}
		if _, leaked := tx["original_amount_cents"]; leaked {
			t.Errorf("original_amount_cents leaked in list response: %v", tx)
		}
	}
}

func TestHandleCreateTransaction_MissingDate_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	body := strings.NewReader(`{"amount": 50.00, "description": "Groceries", "category_id": 1}`)
	req := httptest.NewRequest(http.MethodPost, "/api/transactions", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleCreateTransaction(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleCreateTransaction_MissingDescription_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	body := strings.NewReader(`{"date": "2026-04-06", "amount": 50.00, "category_id": 1}`)
	req := httptest.NewRequest(http.MethodPost, "/api/transactions", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleCreateTransaction(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleCreateTransaction_ZeroAmount_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	body := strings.NewReader(`{"date": "2026-04-06", "amount": 0, "description": "Zero", "category_id": 1}`)
	req := httptest.NewRequest(http.MethodPost, "/api/transactions", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleCreateTransaction(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleCreateTransaction_InvalidJSON_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	body := strings.NewReader(`not json`)
	req := httptest.NewRequest(http.MethodPost, "/api/transactions", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleCreateTransaction(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleCreateTransaction_NoAuth_Returns401(t *testing.T) {
	h := setupHandler(t)

	body := strings.NewReader(`{"date": "2026-04-06", "amount": 50, "description": "Test", "category_id": 1}`)
	req := httptest.NewRequest(http.MethodPost, "/api/transactions", body)
	rec := httptest.NewRecorder()

	h.handleCreateTransaction(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestHandleCreateTransaction_CurrencyConversion(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	// LBP rate is 89000 from seed data
	body := strings.NewReader(`{
		"date": "2026-04-06",
		"original_amount": 89000,
		"original_currency": "LBP",
		"description": "LBP purchase",
		"category_id": 1
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/transactions", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleCreateTransaction(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	// 89000 / 89000 = 1.0
	amount := resp["amount"].(float64)
	if amount < 0.99 || amount > 1.01 {
		t.Errorf("expected converted amount ~1.0, got %v", amount)
	}
	if resp["original_currency"] != "LBP" {
		t.Errorf("expected original_currency 'LBP', got %v", resp["original_currency"])
	}
}

func TestHandleCreateTransaction_BaseCurrency_UsesAmountDirectly(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	// USD is the base currency
	body := strings.NewReader(`{
		"date": "2026-04-06",
		"amount": 25.50,
		"original_currency": "USD",
		"description": "USD purchase",
		"category_id": 1
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/transactions", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleCreateTransaction(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	if resp["amount"].(float64) != 25.50 {
		t.Errorf("expected amount 25.50, got %v", resp["amount"])
	}
}

func TestHandleCreateTransaction_InvalidCurrency_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	body := strings.NewReader(`{
		"date": "2026-04-06",
		"original_amount": 100,
		"original_currency": "INVALID",
		"description": "Bad currency",
		"category_id": 1
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/transactions", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleCreateTransaction(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

// --- handleUpdateTransaction ---

func TestHandleUpdateTransaction_OwnerCanEdit(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	txn := seedTestTransaction(t, q, user.ID, 1, "2026-04-06", 50.0, "Original")

	body := strings.NewReader(`{
		"date": "2026-04-07",
		"amount": 75.00,
		"description": "Updated",
		"category_id": 1
	}`)
	req := httptest.NewRequest(http.MethodPut, "/api/transactions/"+fmt.Sprintf("%d", txn.ID), body)
	req = withUserAndURLParam(req, user, "id", fmt.Sprintf("%d", txn.ID))
	rec := httptest.NewRecorder()

	h.handleUpdateTransaction(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleUpdateTransaction_NonOwnerMember_Returns403(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	owner := seedTestUser(t, q, "alice", "member")
	other := seedTestUser(t, q, "bob", "member")
	txn := seedTestTransaction(t, q, owner.ID, 1, "2026-04-06", 50.0, "Alice's txn")

	body := strings.NewReader(`{
		"date": "2026-04-07",
		"amount": 75.00,
		"description": "Hacked",
		"category_id": 1
	}`)
	req := httptest.NewRequest(http.MethodPut, "/api/transactions/"+fmt.Sprintf("%d", txn.ID), body)
	req = withUserAndURLParam(req, other, "id", fmt.Sprintf("%d", txn.ID))
	rec := httptest.NewRecorder()

	h.handleUpdateTransaction(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleUpdateTransaction_AdminCanEditAny(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	owner := seedTestUser(t, q, "alice", "member")
	admin := seedTestUser(t, q, "admin", "admin")
	txn := seedTestTransaction(t, q, owner.ID, 1, "2026-04-06", 50.0, "Alice's txn")

	body := strings.NewReader(`{
		"date": "2026-04-07",
		"amount": 100.00,
		"description": "Admin edited",
		"category_id": 1
	}`)
	req := httptest.NewRequest(http.MethodPut, "/api/transactions/"+fmt.Sprintf("%d", txn.ID), body)
	req = withUserAndURLParam(req, admin, "id", fmt.Sprintf("%d", txn.ID))
	rec := httptest.NewRecorder()

	h.handleUpdateTransaction(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleUpdateTransaction_NotFound_Returns404(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	body := strings.NewReader(`{
		"date": "2026-04-07",
		"amount": 75.00,
		"description": "Updated",
		"category_id": 1
	}`)
	req := httptest.NewRequest(http.MethodPut, "/api/transactions/99999", body)
	req = withUserAndURLParam(req, user, "id", "99999")
	rec := httptest.NewRecorder()

	h.handleUpdateTransaction(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleUpdateTransaction_InvalidID_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	body := strings.NewReader(`{"date":"2026-04-07","amount":50,"description":"x","category_id":1}`)
	req := httptest.NewRequest(http.MethodPut, "/api/transactions/abc", body)
	req = withUserAndURLParam(req, user, "id", "abc")
	rec := httptest.NewRecorder()

	h.handleUpdateTransaction(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// --- handleDeleteTransaction ---

func TestHandleDeleteTransaction_OwnerCanDelete(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	txn := seedTestTransaction(t, q, user.ID, 1, "2026-04-06", 50.0, "To delete")

	req := httptest.NewRequest(http.MethodDelete, "/api/transactions/"+fmt.Sprintf("%d", txn.ID), nil)
	req = withUserAndURLParam(req, user, "id", fmt.Sprintf("%d", txn.ID))
	rec := httptest.NewRecorder()

	h.handleDeleteTransaction(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	// Post-soft-delete: the physical row survives with deleted_at set.
	// GetTransactionByID deliberately leaks tombstoned rows (mutation-only
	// caller), so the row is still visible here; user-facing list queries
	// filter it out via AND t.deleted_at IS NULL.
	tombstoned, err := q.GetTransactionByID(context.Background(), txn.ID)
	if err != nil {
		t.Fatalf("GetTransactionByID after soft-delete: %v", err)
	}
	if !tombstoned.DeletedAt.Valid {
		t.Errorf("expected deleted_at to be set, got NULL")
	}
	if got := countAllTransactions(t, db); got != 1 {
		t.Errorf("expected row to survive soft-delete (1 physical row), got %d", got)
	}
	if got := countTransactions(t, db); got != 0 {
		t.Errorf("expected 0 live rows after soft-delete, got %d", got)
	}
}

func TestHandleDeleteTransaction_NonOwnerMember_Returns403(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	owner := seedTestUser(t, q, "alice", "member")
	other := seedTestUser(t, q, "bob", "member")
	txn := seedTestTransaction(t, q, owner.ID, 1, "2026-04-06", 50.0, "Alice's")

	req := httptest.NewRequest(http.MethodDelete, "/api/transactions/"+fmt.Sprintf("%d", txn.ID), nil)
	req = withUserAndURLParam(req, other, "id", fmt.Sprintf("%d", txn.ID))
	rec := httptest.NewRecorder()

	h.handleDeleteTransaction(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestHandleDeleteTransaction_AdminCanDeleteAny(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	owner := seedTestUser(t, q, "alice", "member")
	admin := seedTestUser(t, q, "admin", "admin")
	txn := seedTestTransaction(t, q, owner.ID, 1, "2026-04-06", 50.0, "Alice's")

	req := httptest.NewRequest(http.MethodDelete, "/api/transactions/"+fmt.Sprintf("%d", txn.ID), nil)
	req = withUserAndURLParam(req, admin, "id", fmt.Sprintf("%d", txn.ID))
	rec := httptest.NewRecorder()

	h.handleDeleteTransaction(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleDeleteTransaction_NotFound_Returns404(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	req := httptest.NewRequest(http.MethodDelete, "/api/transactions/99999", nil)
	req = withUserAndURLParam(req, user, "id", "99999")
	rec := httptest.NewRecorder()

	h.handleDeleteTransaction(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

// --- handleListTransactions ---

func TestHandleListTransactions_ReturnsPagedResponse(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	// Create 3 transactions
	for i := 1; i <= 3; i++ {
		seedTestTransaction(t, q, user.ID, 1, fmt.Sprintf("2026-04-%02d", i), float64(i*10), fmt.Sprintf("Txn %d", i))
	}

	req := httptest.NewRequest(http.MethodGet, "/api/transactions", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleListTransactions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Transactions []map[string]any `json:"transactions"`
		Total        int              `json:"total"`
		Page         int              `json:"page"`
		PerPage      int              `json:"per_page"`
	}
	decodeResponse(t, rec, &resp)

	if resp.Total != 3 {
		t.Errorf("expected total=3, got %d", resp.Total)
	}
	if resp.Page != 1 {
		t.Errorf("expected page=1, got %d", resp.Page)
	}
	if resp.PerPage != 25 {
		t.Errorf("expected per_page=25, got %d", resp.PerPage)
	}
	if len(resp.Transactions) != 3 {
		t.Errorf("expected 3 transactions, got %d", len(resp.Transactions))
	}
}

func TestHandleListTransactions_IncludesCategoryInfo(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	// Category 1 = "Food" (expense) from seed
	seedTestTransaction(t, q, user.ID, 1, "2026-04-06", 50.0, "Groceries")

	req := httptest.NewRequest(http.MethodGet, "/api/transactions", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleListTransactions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Transactions []map[string]any `json:"transactions"`
	}
	decodeResponse(t, rec, &resp)

	if len(resp.Transactions) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(resp.Transactions))
	}
	txn := resp.Transactions[0]
	if txn["category_name"] != "Food" {
		t.Errorf("expected category_name 'Food', got %v", txn["category_name"])
	}
	if txn["category_type"] != "expense" {
		t.Errorf("expected category_type 'expense', got %v", txn["category_type"])
	}
}

func TestHandleListTransactions_DateRangeFilter(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	seedTestTransaction(t, q, user.ID, 1, "2026-01-15", 10.0, "Jan")
	seedTestTransaction(t, q, user.ID, 1, "2026-02-15", 20.0, "Feb")
	seedTestTransaction(t, q, user.ID, 1, "2026-03-15", 30.0, "Mar")

	req := httptest.NewRequest(http.MethodGet, "/api/transactions?date_from=2026-02-01&date_to=2026-02-28", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleListTransactions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Transactions []map[string]any `json:"transactions"`
		Total        int              `json:"total"`
	}
	decodeResponse(t, rec, &resp)

	if resp.Total != 1 {
		t.Errorf("expected total=1 (only Feb), got %d", resp.Total)
	}
}

func TestHandleListTransactions_CategoryFilter(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	// Category 1 = Food, Category 2 = Gifts (from seed)
	seedTestTransaction(t, q, user.ID, 1, "2026-04-06", 10.0, "Food item")
	seedTestTransaction(t, q, user.ID, 2, "2026-04-06", 20.0, "Gift item")

	req := httptest.NewRequest(http.MethodGet, "/api/transactions?category_id=1", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleListTransactions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Transactions []map[string]any `json:"transactions"`
		Total        int              `json:"total"`
	}
	decodeResponse(t, rec, &resp)

	if resp.Total != 1 {
		t.Errorf("expected total=1 (only Food), got %d", resp.Total)
	}
}

func TestHandleListTransactions_TypeFilter(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	// Category 1 = Food (expense), Category 15 = Paycheck (income) from seed
	seedTestTransaction(t, q, user.ID, 1, "2026-04-06", 50.0, "Expense item")
	seedTestTransaction(t, q, user.ID, 15, "2026-04-06", 1000.0, "Income item")

	req := httptest.NewRequest(http.MethodGet, "/api/transactions?type=income", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleListTransactions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Transactions []map[string]any `json:"transactions"`
		Total        int              `json:"total"`
	}
	decodeResponse(t, rec, &resp)

	if resp.Total != 1 {
		t.Errorf("expected total=1 (only income), got %d", resp.Total)
	}
}

func TestHandleListTransactions_SearchFilter(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	seedTestTransaction(t, q, user.ID, 1, "2026-04-06", 10.0, "Weekly groceries")
	seedTestTransaction(t, q, user.ID, 1, "2026-04-06", 20.0, "Dinner out")

	req := httptest.NewRequest(http.MethodGet, "/api/transactions?search=groceries", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleListTransactions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Transactions []map[string]any `json:"transactions"`
		Total        int              `json:"total"`
	}
	decodeResponse(t, rec, &resp)

	if resp.Total != 1 {
		t.Errorf("expected total=1, got %d", resp.Total)
	}
}

func TestHandleListTransactions_Pagination(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	for i := 1; i <= 5; i++ {
		seedTestTransaction(t, q, user.ID, 1, fmt.Sprintf("2026-04-%02d", i), float64(i*10), fmt.Sprintf("Txn %d", i))
	}

	req := httptest.NewRequest(http.MethodGet, "/api/transactions?page=2&per_page=2", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleListTransactions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Transactions []map[string]any `json:"transactions"`
		Total        int              `json:"total"`
		Page         int              `json:"page"`
		PerPage      int              `json:"per_page"`
	}
	decodeResponse(t, rec, &resp)

	if resp.Total != 5 {
		t.Errorf("expected total=5, got %d", resp.Total)
	}
	if resp.Page != 2 {
		t.Errorf("expected page=2, got %d", resp.Page)
	}
	if resp.PerPage != 2 {
		t.Errorf("expected per_page=2, got %d", resp.PerPage)
	}
	if len(resp.Transactions) != 2 {
		t.Errorf("expected 2 transactions on page 2, got %d", len(resp.Transactions))
	}
}

func TestHandleListTransactions_PerPageMaxCapped(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	req := httptest.NewRequest(http.MethodGet, "/api/transactions?per_page=500", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleListTransactions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		PerPage int `json:"per_page"`
	}
	decodeResponse(t, rec, &resp)

	if resp.PerPage != 100 {
		t.Errorf("expected per_page capped at 100, got %d", resp.PerPage)
	}
}

func TestHandleListTransactions_NoAuth_Returns401(t *testing.T) {
	h := setupHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/transactions", nil)
	rec := httptest.NewRecorder()

	h.handleListTransactions(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestHandleListTransactions_OrderByDateDesc(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	seedTestTransaction(t, q, user.ID, 1, "2026-04-01", 10.0, "First")
	seedTestTransaction(t, q, user.ID, 1, "2026-04-03", 30.0, "Third")
	seedTestTransaction(t, q, user.ID, 1, "2026-04-02", 20.0, "Second")

	req := httptest.NewRequest(http.MethodGet, "/api/transactions", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleListTransactions(rec, req)

	var resp struct {
		Transactions []map[string]any `json:"transactions"`
	}
	decodeResponse(t, rec, &resp)

	if len(resp.Transactions) != 3 {
		t.Fatalf("expected 3 transactions, got %d", len(resp.Transactions))
	}
	// Should be ordered by date DESC: Third, Second, First
	if resp.Transactions[0]["description"] != "Third" {
		t.Errorf("expected first result 'Third', got %v", resp.Transactions[0]["description"])
	}
	if resp.Transactions[2]["description"] != "First" {
		t.Errorf("expected last result 'First', got %v", resp.Transactions[2]["description"])
	}
}

// --- handleBatchCreateTransactions ---

func TestHandleBatchCreateTransactions_ValidInput_Returns201(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	body := strings.NewReader(`[
		{"date": "2026-04-06", "amount": 10.00, "description": "Item 1", "category_id": 1},
		{"date": "2026-04-07", "amount": 20.00, "description": "Item 2", "category_id": 1}
	]`)
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/batch", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleBatchCreateTransactions(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp) != 2 {
		t.Errorf("expected 2 transactions, got %d", len(resp))
	}
}

func TestHandleBatchCreateTransactions_EmptyArray_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	body := strings.NewReader(`[]`)
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/batch", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleBatchCreateTransactions(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleBatchCreateTransactions_OneInvalid_RollsBack(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	// Second item has no description — should fail validation
	body := strings.NewReader(`[
		{"date": "2026-04-06", "amount": 10.00, "description": "Item 1", "category_id": 1},
		{"date": "2026-04-07", "amount": 20.00, "description": "", "category_id": 1}
	]`)
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/batch", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleBatchCreateTransactions(rec, req)

	// Should fail
	if rec.Code == http.StatusCreated {
		t.Errorf("expected failure status, got 201")
	}

	// Verify nothing was committed — list should be empty
	listReq := httptest.NewRequest(http.MethodGet, "/api/transactions", nil)
	listReq = withUser(listReq, user)
	listRec := httptest.NewRecorder()
	h.handleListTransactions(listRec, listReq)

	var resp struct {
		Total int `json:"total"`
	}
	decodeResponse(t, listRec, &resp)
	if resp.Total != 0 {
		t.Errorf("expected 0 transactions after rollback, got %d", resp.Total)
	}
}

func TestHandleBatchCreateTransactions_WithCurrencyConversion(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	body := strings.NewReader(`[
		{"date": "2026-04-06", "original_amount": 178000, "original_currency": "LBP", "description": "LBP item", "category_id": 1},
		{"date": "2026-04-06", "amount": 25.50, "description": "USD item", "category_id": 1}
	]`)
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/batch", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleBatchCreateTransactions(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp) != 2 {
		t.Fatalf("expected 2 transactions, got %d", len(resp))
	}

	// LBP item: 178000 / 89000 = 2.0
	amount := resp[0]["amount"].(float64)
	if amount < 1.99 || amount > 2.01 {
		t.Errorf("expected converted amount ~2.0, got %v", amount)
	}
}

func TestHandleBatchCreateTransactions_NoAuth_Returns401(t *testing.T) {
	h := setupHandler(t)

	body := strings.NewReader(`[{"date":"2026-04-06","amount":10,"description":"Test","category_id":1}]`)
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/batch", body)
	rec := httptest.NewRecorder()

	h.handleBatchCreateTransactions(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// --- handleListTransactions: new filters ---

func TestHandleListTransactions_AmountMinFilter(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	seedTestTransaction(t, q, user.ID, 1, "2026-04-06", 10.0, "Small")
	seedTestTransaction(t, q, user.ID, 1, "2026-04-06", 50.0, "Medium")
	seedTestTransaction(t, q, user.ID, 1, "2026-04-06", 100.0, "Large")

	req := httptest.NewRequest(http.MethodGet, "/api/transactions?amount_min=50", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleListTransactions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp transactionListResponse
	decodeResponse(t, rec, &resp)

	if resp.Total != 2 {
		t.Errorf("expected total=2 (>=50), got %d", resp.Total)
	}
}

func TestHandleListTransactions_AmountMaxFilter(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	seedTestTransaction(t, q, user.ID, 1, "2026-04-06", 10.0, "Small")
	seedTestTransaction(t, q, user.ID, 1, "2026-04-06", 50.0, "Medium")
	seedTestTransaction(t, q, user.ID, 1, "2026-04-06", 100.0, "Large")

	req := httptest.NewRequest(http.MethodGet, "/api/transactions?amount_max=50", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleListTransactions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp transactionListResponse
	decodeResponse(t, rec, &resp)

	if resp.Total != 2 {
		t.Errorf("expected total=2 (<=50), got %d", resp.Total)
	}
}

func TestHandleListTransactions_AmountRangeFilter(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	seedTestTransaction(t, q, user.ID, 1, "2026-04-06", 10.0, "Small")
	seedTestTransaction(t, q, user.ID, 1, "2026-04-06", 50.0, "Medium")
	seedTestTransaction(t, q, user.ID, 1, "2026-04-06", 100.0, "Large")

	req := httptest.NewRequest(http.MethodGet, "/api/transactions?amount_min=20&amount_max=80", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleListTransactions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp transactionListResponse
	decodeResponse(t, rec, &resp)

	if resp.Total != 1 {
		t.Errorf("expected total=1 (only 50), got %d", resp.Total)
	}
}

func TestHandleListTransactions_MultiCategoryFilter(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	// Category 1 = Food, Category 2 = Gifts, Category 3 = Health/medical
	seedTestTransaction(t, q, user.ID, 1, "2026-04-06", 10.0, "Food item")
	seedTestTransaction(t, q, user.ID, 2, "2026-04-06", 20.0, "Gift item")
	seedTestTransaction(t, q, user.ID, 3, "2026-04-06", 30.0, "Health item")

	// Filter for categories 1 and 3 only
	req := httptest.NewRequest(http.MethodGet, "/api/transactions?category_ids=1,3", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleListTransactions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp transactionListResponse
	decodeResponse(t, rec, &resp)

	if resp.Total != 2 {
		t.Errorf("expected total=2 (Food + Health), got %d", resp.Total)
	}
}

func TestHandleListTransactions_MultiCategoryOverridesSingleCategory(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	seedTestTransaction(t, q, user.ID, 1, "2026-04-06", 10.0, "Food item")
	seedTestTransaction(t, q, user.ID, 2, "2026-04-06", 20.0, "Gift item")
	seedTestTransaction(t, q, user.ID, 3, "2026-04-06", 30.0, "Health item")

	// Both category_id and category_ids set; category_ids should win
	req := httptest.NewRequest(http.MethodGet, "/api/transactions?category_id=1&category_ids=2,3", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleListTransactions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp transactionListResponse
	decodeResponse(t, rec, &resp)

	// category_ids=2,3 should override category_id=1, giving us Gift + Health
	if resp.Total != 2 {
		t.Errorf("expected total=2 (Gift + Health, category_ids wins), got %d", resp.Total)
	}
}

func TestHandleListTransactions_TagsFilter(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	seedTestTransactionWithTags(t, q, user.ID, 1, "2026-04-06", 10.0, "Tagged food", "groceries,weekly")
	seedTestTransactionWithTags(t, q, user.ID, 1, "2026-04-06", 20.0, "Tagged restaurant", "restaurant,dinner")
	seedTestTransaction(t, q, user.ID, 1, "2026-04-06", 30.0, "No tags")

	req := httptest.NewRequest(http.MethodGet, "/api/transactions?tags=groceries", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleListTransactions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp transactionListResponse
	decodeResponse(t, rec, &resp)

	if resp.Total != 1 {
		t.Errorf("expected total=1 (only groceries tagged), got %d", resp.Total)
	}
}

func TestHandleListTransactions_TagsFilterWithSpecialChars(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	seedTestTransactionWithTags(t, q, user.ID, 1, "2026-04-06", 10.0, "Special", "100%_off")
	seedTestTransactionWithTags(t, q, user.ID, 1, "2026-04-06", 20.0, "Normal", "discount")

	// Search for "100%" which contains SQL LIKE wildcard chars; should be escaped
	req := httptest.NewRequest(http.MethodGet, "/api/transactions?tags=100%25", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleListTransactions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp transactionListResponse
	decodeResponse(t, rec, &resp)

	if resp.Total != 1 {
		t.Errorf("expected total=1 (only '100%%_off' tagged), got %d", resp.Total)
	}
}

func TestHandleListTransactions_InvalidAmountFilterIgnored(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	seedTestTransaction(t, q, user.ID, 1, "2026-04-06", 50.0, "Test")

	// Invalid amount_min should be silently ignored, returning all
	req := httptest.NewRequest(http.MethodGet, "/api/transactions?amount_min=notanumber", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleListTransactions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp transactionListResponse
	decodeResponse(t, rec, &resp)

	if resp.Total != 1 {
		t.Errorf("expected total=1 (invalid filter ignored), got %d", resp.Total)
	}
}

// --- handleListTransactions: sort params ---

func TestSortDefaultIsDateDesc(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	// Create transactions on different dates
	seedTestTransaction(t, q, user.ID, 1, "2026-04-01", 10.0, "First")
	seedTestTransaction(t, q, user.ID, 1, "2026-04-03", 30.0, "Third")
	seedTestTransaction(t, q, user.ID, 1, "2026-04-02", 20.0, "Second")

	req := httptest.NewRequest(http.MethodGet, "/api/transactions", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleListTransactions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Transactions []transactionResponse `json:"transactions"`
	}
	decodeResponse(t, rec, &resp)

	if len(resp.Transactions) != 3 {
		t.Fatalf("expected 3 transactions, got %d", len(resp.Transactions))
	}
	// Default sort: date DESC — Third (Apr 3), Second (Apr 2), First (Apr 1)
	if resp.Transactions[0].Description != "Third" {
		t.Errorf("expected first result 'Third' (date DESC default), got %q", resp.Transactions[0].Description)
	}
	if resp.Transactions[1].Description != "Second" {
		t.Errorf("expected second result 'Second', got %q", resp.Transactions[1].Description)
	}
	if resp.Transactions[2].Description != "First" {
		t.Errorf("expected third result 'First', got %q", resp.Transactions[2].Description)
	}
}

func TestSortByAmountAsc(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	seedTestTransaction(t, q, user.ID, 1, "2026-04-01", 50.0, "Medium")
	seedTestTransaction(t, q, user.ID, 1, "2026-04-02", 10.0, "Small")
	seedTestTransaction(t, q, user.ID, 1, "2026-04-03", 99.0, "Large")

	req := httptest.NewRequest(http.MethodGet, "/api/transactions?sort_by=amount&sort_dir=asc", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleListTransactions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Transactions []transactionResponse `json:"transactions"`
	}
	decodeResponse(t, rec, &resp)

	if len(resp.Transactions) != 3 {
		t.Fatalf("expected 3 transactions, got %d", len(resp.Transactions))
	}
	// Amount ASC: Small (10), Medium (50), Large (99)
	if resp.Transactions[0].Description != "Small" {
		t.Errorf("expected first result 'Small' (amount ASC), got %q", resp.Transactions[0].Description)
	}
	if resp.Transactions[1].Description != "Medium" {
		t.Errorf("expected second result 'Medium', got %q", resp.Transactions[1].Description)
	}
	if resp.Transactions[2].Description != "Large" {
		t.Errorf("expected third result 'Large', got %q", resp.Transactions[2].Description)
	}
}

func TestSortByInvalidColumnFallsBackToDate(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	seedTestTransaction(t, q, user.ID, 1, "2026-04-01", 10.0, "First")
	seedTestTransaction(t, q, user.ID, 1, "2026-04-03", 30.0, "Third")
	seedTestTransaction(t, q, user.ID, 1, "2026-04-02", 20.0, "Second")

	// "hacked" is not in the whitelist — should fall back to date DESC
	req := httptest.NewRequest(http.MethodGet, "/api/transactions?sort_by=hacked", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleListTransactions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Transactions []transactionResponse `json:"transactions"`
	}
	decodeResponse(t, rec, &resp)

	if len(resp.Transactions) != 3 {
		t.Fatalf("expected 3 transactions, got %d", len(resp.Transactions))
	}
	// Should fall back to date DESC
	if resp.Transactions[0].Description != "Third" {
		t.Errorf("expected first result 'Third' (fallback to date DESC), got %q", resp.Transactions[0].Description)
	}
}

func TestSortByInvalidDirFallsBackToDesc(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	seedTestTransaction(t, q, user.ID, 1, "2026-04-01", 10.0, "First")
	seedTestTransaction(t, q, user.ID, 1, "2026-04-03", 30.0, "Third")
	seedTestTransaction(t, q, user.ID, 1, "2026-04-02", 20.0, "Second")

	// "INVALID" is not asc/desc — should fall back to desc
	req := httptest.NewRequest(http.MethodGet, "/api/transactions?sort_by=date&sort_dir=INVALID", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleListTransactions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Transactions []transactionResponse `json:"transactions"`
	}
	decodeResponse(t, rec, &resp)

	if len(resp.Transactions) != 3 {
		t.Fatalf("expected 3 transactions, got %d", len(resp.Transactions))
	}
	// Should fall back to DESC
	if resp.Transactions[0].Description != "Third" {
		t.Errorf("expected first result 'Third' (fallback to DESC), got %q", resp.Transactions[0].Description)
	}
	if resp.Transactions[2].Description != "First" {
		t.Errorf("expected last result 'First', got %q", resp.Transactions[2].Description)
	}
}

func TestSortByCategoryName(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	// Category 1 = Food, Category 2 = Gifts, Category 3 = Health/medical (from seeds)
	seedTestTransaction(t, q, user.ID, 2, "2026-04-01", 20.0, "Gift item")
	seedTestTransaction(t, q, user.ID, 1, "2026-04-02", 10.0, "Food item")
	seedTestTransaction(t, q, user.ID, 3, "2026-04-03", 30.0, "Health item")

	req := httptest.NewRequest(http.MethodGet, "/api/transactions?sort_by=category&sort_dir=asc", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleListTransactions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Transactions []transactionResponse `json:"transactions"`
	}
	decodeResponse(t, rec, &resp)

	if len(resp.Transactions) != 3 {
		t.Fatalf("expected 3 transactions, got %d", len(resp.Transactions))
	}
	// Category ASC: Food, Gifts, Health/medical
	if resp.Transactions[0].CategoryName != "Food" {
		t.Errorf("expected first category 'Food' (category ASC), got %q", resp.Transactions[0].CategoryName)
	}
	if resp.Transactions[1].CategoryName != "Gifts" {
		t.Errorf("expected second category 'Gifts', got %q", resp.Transactions[1].CategoryName)
	}
}

func TestSortByDescriptionDesc(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	seedTestTransaction(t, q, user.ID, 1, "2026-04-01", 10.0, "Alpha")
	seedTestTransaction(t, q, user.ID, 1, "2026-04-02", 20.0, "Charlie")
	seedTestTransaction(t, q, user.ID, 1, "2026-04-03", 30.0, "Bravo")

	req := httptest.NewRequest(http.MethodGet, "/api/transactions?sort_by=description&sort_dir=desc", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleListTransactions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Transactions []transactionResponse `json:"transactions"`
	}
	decodeResponse(t, rec, &resp)

	if len(resp.Transactions) != 3 {
		t.Fatalf("expected 3 transactions, got %d", len(resp.Transactions))
	}
	// Description DESC: Charlie, Bravo, Alpha
	if resp.Transactions[0].Description != "Charlie" {
		t.Errorf("expected first result 'Charlie' (description DESC), got %q", resp.Transactions[0].Description)
	}
	if resp.Transactions[1].Description != "Bravo" {
		t.Errorf("expected second result 'Bravo', got %q", resp.Transactions[1].Description)
	}
	if resp.Transactions[2].Description != "Alpha" {
		t.Errorf("expected third result 'Alpha', got %q", resp.Transactions[2].Description)
	}
}

func TestSortByTagsAsc(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	seedTestTransactionWithTags(t, q, user.ID, 1, "2026-04-01", 10.0, "Item C", "zebra")
	seedTestTransactionWithTags(t, q, user.ID, 1, "2026-04-02", 20.0, "Item A", "alpha")
	seedTestTransactionWithTags(t, q, user.ID, 1, "2026-04-03", 30.0, "Item B", "middle")

	req := httptest.NewRequest(http.MethodGet, "/api/transactions?sort_by=tags&sort_dir=asc", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleListTransactions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Transactions []transactionResponse `json:"transactions"`
	}
	decodeResponse(t, rec, &resp)

	if len(resp.Transactions) != 3 {
		t.Fatalf("expected 3 transactions, got %d", len(resp.Transactions))
	}
	// Tags ASC: alpha, middle, zebra
	if resp.Transactions[0].Tags != "alpha" {
		t.Errorf("expected first result tags 'alpha' (tags ASC), got %q", resp.Transactions[0].Tags)
	}
	if resp.Transactions[1].Tags != "middle" {
		t.Errorf("expected second result tags 'middle', got %q", resp.Transactions[1].Tags)
	}
	if resp.Transactions[2].Tags != "zebra" {
		t.Errorf("expected third result tags 'zebra', got %q", resp.Transactions[2].Tags)
	}
}

// stampCreatedAt overwrites a transaction's created_at to a deterministic
// value. CreateTransaction has no created_at parameter (it defaults to
// CURRENT_TIMESTAMP), so seeding two rows back-to-back can collide on the
// timestamp. Setting it explicitly makes the created_at sort order
// deterministic and immune to wall-clock collisions. The stored layout
// matches what the driver writes elsewhere (mattn/go-sqlite3 stores a
// time.Time as RFC3339), so the column compares correctly as a date/time.
func stampCreatedAt(t *testing.T, db *sql.DB, id int64, createdAt time.Time) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(),
		"UPDATE transactions SET created_at = ? WHERE id = ?", createdAt, id); err != nil {
		t.Fatalf("stamp created_at on id=%d: %v", id, err)
	}
}

// TestSortByCreatedAt proves sorting by entry time (created_at) works
// end-to-end through handleListTransactions, independent of the transaction
// date. Both rows share the SAME date, so a date-sort cannot distinguish
// them — only a created_at-sort can. The row with the LATER created_at is
// deliberately given the LOWER id: under the secondary "t.id" tie-break a
// broken whitelist (created_at silently falling back to t.date) would order
// these rows by id and surface the EARLIER-entered row first on a desc sort,
// failing the assertion.
func TestSortByCreatedAt(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	// Same date for both rows; differ only by created_at.
	const sameDate = "2026-04-10"
	// "EnteredLater" is seeded first → lower id, but stamped with the LATER
	// created_at. "EnteredEarlier" is seeded second → higher id, stamped
	// EARLIER. This inversion of id vs. created_at is what makes the test
	// fail when created_at falls back to the t.date + t.id tie-break.
	enteredLaterID := seedTestTransaction(t, q, user.ID, 1, sameDate, 10.0, "EnteredLater").ID
	enteredEarlierID := seedTestTransaction(t, q, user.ID, 1, sameDate, 20.0, "EnteredEarlier").ID

	base := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	stampCreatedAt(t, db, enteredEarlierID, base)              // lower created_at, higher id
	stampCreatedAt(t, db, enteredLaterID, base.Add(time.Hour)) // higher created_at, lower id

	// DESC: the later-entered row (EnteredLater) must come first.
	reqDesc := httptest.NewRequest(http.MethodGet, "/api/transactions?sort_by=created_at&sort_dir=desc", nil)
	reqDesc = withUser(reqDesc, user)
	recDesc := httptest.NewRecorder()
	h.handleListTransactions(recDesc, reqDesc)

	if recDesc.Code != http.StatusOK {
		t.Fatalf("desc: expected 200, got %d; body: %s", recDesc.Code, recDesc.Body.String())
	}
	var respDesc struct {
		Transactions []transactionResponse `json:"transactions"`
	}
	decodeResponse(t, recDesc, &respDesc)
	if len(respDesc.Transactions) != 2 {
		t.Fatalf("desc: expected 2 transactions, got %d", len(respDesc.Transactions))
	}
	if respDesc.Transactions[0].Description != "EnteredLater" {
		t.Errorf("desc: expected first result 'EnteredLater' (created_at DESC), got %q", respDesc.Transactions[0].Description)
	}
	if respDesc.Transactions[1].Description != "EnteredEarlier" {
		t.Errorf("desc: expected second result 'EnteredEarlier', got %q", respDesc.Transactions[1].Description)
	}

	// ASC: the earlier-entered row must come first (reverse of desc).
	reqAsc := httptest.NewRequest(http.MethodGet, "/api/transactions?sort_by=created_at&sort_dir=asc", nil)
	reqAsc = withUser(reqAsc, user)
	recAsc := httptest.NewRecorder()
	h.handleListTransactions(recAsc, reqAsc)

	if recAsc.Code != http.StatusOK {
		t.Fatalf("asc: expected 200, got %d; body: %s", recAsc.Code, recAsc.Body.String())
	}
	var respAsc struct {
		Transactions []transactionResponse `json:"transactions"`
	}
	decodeResponse(t, recAsc, &respAsc)
	if len(respAsc.Transactions) != 2 {
		t.Fatalf("asc: expected 2 transactions, got %d", len(respAsc.Transactions))
	}
	if respAsc.Transactions[0].Description != "EnteredEarlier" {
		t.Errorf("asc: expected first result 'EnteredEarlier' (created_at ASC), got %q", respAsc.Transactions[0].Description)
	}
	if respAsc.Transactions[1].Description != "EnteredLater" {
		t.Errorf("asc: expected second result 'EnteredLater', got %q", respAsc.Transactions[1].Description)
	}
}

// TestParseSortParams_CreatedAt asserts the whitelist resolves the frontend
// "created_at" key to the safe fixed SQL column "t.created_at", and that an
// unknown key still falls back to "t.date" (the injection-safe whitelist
// property).
func TestParseSortParams_CreatedAt(t *testing.T) {
	col, dir := parseSortParams(map[string][]string{
		"sort_by":  {"created_at"},
		"sort_dir": {"asc"},
	})
	if col != "t.created_at" {
		t.Errorf("expected column 't.created_at', got %q", col)
	}
	if dir != "ASC" {
		t.Errorf("expected dir 'ASC', got %q", dir)
	}

	// Unknown key must still fall back to the default safe column.
	col, _ = parseSortParams(map[string][]string{"sort_by": {"created_at; DROP TABLE"}})
	if col != "t.date" {
		t.Errorf("expected unknown key to fall back to 't.date', got %q", col)
	}
}

// --- handleDeleteTransactionsByFilter ---

// countTransactions returns the total row count in the transactions table,
// used by delete-by-filter tests to verify atomic bulk deletes.
// countTransactions counts LIVE transactions only. The handlers that write
// to the database now soft-delete rather than hard-delete, so tests that
// want to assert "N rows are gone from the user's perspective" must filter
// tombstoned rows out. For asserting on the raw physical row count
// (including tombstones), use countAllTransactions below.
func countTransactions(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM transactions WHERE deleted_at IS NULL").Scan(&n); err != nil {
		t.Fatalf("count transactions: %v", err)
	}
	return n
}

// countAllTransactions counts every physical row including tombstones.
// Use this to assert that a soft-delete did NOT purge the row.
func countAllTransactions(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM transactions").Scan(&n); err != nil {
		t.Fatalf("count all transactions: %v", err)
	}
	return n
}

// countTombstonedTransactions counts only soft-deleted rows. Used by tests
// that assert a delete flipped deleted_at without hard-deleting.
func countTombstonedTransactions(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM transactions WHERE deleted_at IS NOT NULL").Scan(&n); err != nil {
		t.Fatalf("count tombstoned transactions: %v", err)
	}
	return n
}

func TestHandleDeleteTransactionsByFilter_NoFilter_DeletesAll(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	for i := 1; i <= 5; i++ {
		seedTestTransaction(t, q, admin.ID, 1, fmt.Sprintf("2026-04-%02d", i), 10.0, fmt.Sprintf("Txn %d", i))
	}

	req := httptest.NewRequest(http.MethodPost, "/api/transactions/delete-by-filter", nil)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()

	h.handleDeleteTransactionsByFilter(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Deleted int64 `json:"deleted"`
	}
	decodeResponse(t, rec, &resp)
	if resp.Deleted != 5 {
		t.Errorf("expected deleted=5, got %d", resp.Deleted)
	}
	if n := countTransactions(t, db); n != 0 {
		t.Errorf("expected 0 transactions remaining, got %d", n)
	}
}

func TestHandleDeleteTransactionsByFilter_DateRange_DeletesMatchingOnly(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	seedTestTransaction(t, q, admin.ID, 1, "2026-01-15", 10.0, "Jan")
	seedTestTransaction(t, q, admin.ID, 1, "2026-02-10", 20.0, "Feb A")
	seedTestTransaction(t, q, admin.ID, 1, "2026-02-25", 30.0, "Feb B")
	seedTestTransaction(t, q, admin.ID, 1, "2026-03-05", 40.0, "Mar")

	req := httptest.NewRequest(http.MethodPost,
		"/api/transactions/delete-by-filter?date_from=2026-02-01&date_to=2026-02-28", nil)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()

	h.handleDeleteTransactionsByFilter(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Deleted int64 `json:"deleted"`
	}
	decodeResponse(t, rec, &resp)
	if resp.Deleted != 2 {
		t.Errorf("expected deleted=2 (Feb A + Feb B), got %d", resp.Deleted)
	}
	if n := countTransactions(t, db); n != 2 {
		t.Errorf("expected 2 transactions remaining (Jan + Mar), got %d", n)
	}
}

func TestHandleDeleteTransactionsByFilter_NonAdminOnlyDeletesOwn(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	alice := seedTestUser(t, q, "alice", "member")
	bob := seedTestUser(t, q, "bob", "member")

	seedTestTransaction(t, q, alice.ID, 1, "2026-04-01", 10.0, "Alice 1")
	seedTestTransaction(t, q, alice.ID, 1, "2026-04-02", 20.0, "Alice 2")
	seedTestTransaction(t, q, bob.ID, 1, "2026-04-03", 30.0, "Bob 1")

	req := httptest.NewRequest(http.MethodPost, "/api/transactions/delete-by-filter", nil)
	req = withUser(req, alice)
	rec := httptest.NewRecorder()

	h.handleDeleteTransactionsByFilter(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Deleted int64 `json:"deleted"`
	}
	decodeResponse(t, rec, &resp)
	if resp.Deleted != 2 {
		t.Errorf("expected alice to delete only her 2 rows, got %d", resp.Deleted)
	}
	if n := countTransactions(t, db); n != 1 {
		t.Errorf("expected 1 transaction (Bob's) remaining, got %d", n)
	}
}

func TestHandleDeleteTransactionsByFilter_AdminDeletesEveryonesMatching(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	alice := seedTestUser(t, q, "alice", "member")
	bob := seedTestUser(t, q, "bob", "member")
	admin := seedTestUser(t, q, "admin", "admin")

	seedTestTransaction(t, q, alice.ID, 1, "2026-04-01", 10.0, "Alice")
	seedTestTransaction(t, q, bob.ID, 1, "2026-04-02", 20.0, "Bob")

	req := httptest.NewRequest(http.MethodPost, "/api/transactions/delete-by-filter", nil)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()

	h.handleDeleteTransactionsByFilter(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Deleted int64 `json:"deleted"`
	}
	decodeResponse(t, rec, &resp)
	if resp.Deleted != 2 {
		t.Errorf("expected admin to delete both rows, got %d", resp.Deleted)
	}
	if n := countTransactions(t, db); n != 0 {
		t.Errorf("expected 0 transactions remaining, got %d", n)
	}
}

func TestHandleDeleteTransactionsByFilter_CategoryFilter(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")

	// Seed categories from migration 001: id 1 = Food (expense),
	// id 2 = Gifts (expense). The point of this test is only that
	// category_id scopes the delete correctly — the specific names
	// don't matter, but the comment used to claim id 2 was "Transport"
	// which was wrong (Transportation is id 5).
	seedTestTransaction(t, q, admin.ID, 1, "2026-04-01", 10.0, "Food 1")
	seedTestTransaction(t, q, admin.ID, 1, "2026-04-02", 20.0, "Food 2")
	seedTestTransaction(t, q, admin.ID, 2, "2026-04-03", 30.0, "Gifts")

	req := httptest.NewRequest(http.MethodPost,
		"/api/transactions/delete-by-filter?category_id=1", nil)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()

	h.handleDeleteTransactionsByFilter(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Deleted int64 `json:"deleted"`
	}
	decodeResponse(t, rec, &resp)
	if resp.Deleted != 2 {
		t.Errorf("expected deleted=2 food rows, got %d", resp.Deleted)
	}
	if n := countTransactions(t, db); n != 1 {
		t.Errorf("expected 1 gifts row remaining, got %d", n)
	}
}

func TestHandleDeleteTransactionsByFilter_NoMatches_ReturnsZero(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")
	seedTestTransaction(t, q, admin.ID, 1, "2026-04-01", 10.0, "Txn")

	req := httptest.NewRequest(http.MethodPost,
		"/api/transactions/delete-by-filter?date_from=2099-01-01&date_to=2099-12-31", nil)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()

	h.handleDeleteTransactionsByFilter(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Deleted int64 `json:"deleted"`
	}
	decodeResponse(t, rec, &resp)
	if resp.Deleted != 0 {
		t.Errorf("expected deleted=0, got %d", resp.Deleted)
	}
	if n := countTransactions(t, db); n != 1 {
		t.Errorf("expected 1 row untouched, got %d", n)
	}
}

func TestHandleDeleteTransactionsByFilter_Unauthorized_Returns401(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)

	req := httptest.NewRequest(http.MethodPost, "/api/transactions/delete-by-filter", nil)
	rec := httptest.NewRecorder()

	h.handleDeleteTransactionsByFilter(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// Regression test: a non-admin deleting with a date filter must get BOTH
// constraints applied — ownership AND the date range. Previously untested
// because each constraint had its own isolated test, which would let a
// future refactor accidentally drop one of them without anything failing.
func TestHandleDeleteTransactionsByFilter_NonAdminWithDateFilter_BothApplied(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	alice := seedTestUser(t, q, "alice", "member")
	bob := seedTestUser(t, q, "bob", "member")

	// Alice has rows inside and outside the filter window.
	seedTestTransaction(t, q, alice.ID, 1, "2026-04-10", 10.0, "Alice in-range 1")
	seedTestTransaction(t, q, alice.ID, 1, "2026-04-15", 20.0, "Alice in-range 2")
	seedTestTransaction(t, q, alice.ID, 1, "2026-03-01", 30.0, "Alice out-of-range")
	// Bob also has a row inside the filter window — must be untouched
	// because ownership is enforced even when a filter matches it.
	seedTestTransaction(t, q, bob.ID, 1, "2026-04-12", 40.0, "Bob in-range")

	req := httptest.NewRequest(http.MethodPost,
		"/api/transactions/delete-by-filter?date_from=2026-04-01&date_to=2026-04-30", nil)
	req = withUser(req, alice)
	rec := httptest.NewRecorder()

	h.handleDeleteTransactionsByFilter(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Deleted int64 `json:"deleted"`
	}
	decodeResponse(t, rec, &resp)
	if resp.Deleted != 2 {
		t.Errorf("expected alice's 2 in-range rows deleted, got %d", resp.Deleted)
	}
	// Should leave: alice's out-of-range row + bob's in-range row = 2 rows.
	if n := countTransactions(t, db); n != 2 {
		t.Errorf("expected 2 rows remaining (alice out-of-range + bob in-range), got %d", n)
	}
}

// Regression test: the endpoint must never honor a crafted ?user_id= query
// parameter. buildTransactionWhereClause doesn't parse user_id at all, and
// ownership is enforced separately by the handler — but this test locks that
// in so a future "oh, let's add user_id filtering for admins" change can't
// silently open a cross-user delete hole.
func TestHandleDeleteTransactionsByFilter_IgnoresCraftedUserIDParam(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	alice := seedTestUser(t, q, "alice", "member")
	bob := seedTestUser(t, q, "bob", "member")

	seedTestTransaction(t, q, alice.ID, 1, "2026-04-01", 10.0, "Alice")
	seedTestTransaction(t, q, bob.ID, 1, "2026-04-02", 20.0, "Bob 1")
	seedTestTransaction(t, q, bob.ID, 1, "2026-04-03", 30.0, "Bob 2")

	// Alice tries to delete bob's rows by passing user_id=bob.ID.
	url := fmt.Sprintf("/api/transactions/delete-by-filter?user_id=%d", bob.ID)
	req := httptest.NewRequest(http.MethodPost, url, nil)
	req = withUser(req, alice)
	rec := httptest.NewRecorder()

	h.handleDeleteTransactionsByFilter(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Deleted int64 `json:"deleted"`
	}
	decodeResponse(t, rec, &resp)
	// The user_id param is ignored. The ownership clause still scopes
	// the delete to alice, so only her single row is removed.
	if resp.Deleted != 1 {
		t.Errorf("expected deleted=1 (alice's own row only), got %d", resp.Deleted)
	}
	// Bob's two rows must survive.
	if n := countTransactions(t, db); n != 2 {
		t.Errorf("expected 2 rows (both bob's) remaining, got %d", n)
	}
}

// --- Phase 2.1 soft-delete read-path invariant tests ---
//
// Every user-facing read must hide tombstoned rows. These tests pin the
// invariant in one place per read path, so a future change that drops
// AND t.deleted_at IS NULL from a query fails loudly instead of leaking
// trashed rows into production dashboards / reports / exports.

func TestHandleListTransactions_HidesTombstoned(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	seedTestTransaction(t, q, user.ID, 1, "2026-04-01", 10.0, "live 1")
	seedTombstonedTestTransaction(t, q, user.ID, 1, "2026-04-02", 20.0, "tombstoned")
	seedTestTransaction(t, q, user.ID, 1, "2026-04-03", 30.0, "live 2")

	req := httptest.NewRequest(http.MethodGet, "/api/transactions", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleListTransactions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Transactions []map[string]any `json:"transactions"`
		Total        int              `json:"total"`
	}
	decodeResponse(t, rec, &resp)

	if resp.Total != 2 {
		t.Errorf("total=%d, want 2 (tombstone excluded)", resp.Total)
	}
	if len(resp.Transactions) != 2 {
		t.Errorf("len(transactions)=%d, want 2", len(resp.Transactions))
	}
	for _, tx := range resp.Transactions {
		if tx["description"] == "tombstoned" {
			t.Errorf("list returned tombstoned row: %v", tx)
		}
	}
}

func TestHandleTransactionSuggestions_HidesTombstoned(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	seedTestTransactionWithTags(t, q, user.ID, 1, "2026-04-01", 10.0, "live desc", "livetag")
	tombstoned := seedTestTransactionWithTags(t, q, user.ID, 1, "2026-04-02", 20.0, "deleted desc", "deadtag")
	if err := q.SoftDeleteTransaction(context.Background(), tombstoned.ID); err != nil {
		t.Fatalf("soft-delete: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/transactions/suggestions", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleTransactionSuggestions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Descriptions []string `json:"descriptions"`
		Tags         []string `json:"tags"`
	}
	decodeResponse(t, rec, &resp)

	for _, d := range resp.Descriptions {
		if d == "deleted desc" {
			t.Errorf("suggestions include tombstoned description: %q", d)
		}
	}
	for _, tag := range resp.Tags {
		if tag == "deadtag" {
			t.Errorf("suggestions include tombstoned tag: %q", tag)
		}
	}
	// Sanity: the live row's tag/description must still appear.
	foundDesc := false
	for _, d := range resp.Descriptions {
		if d == "live desc" {
			foundDesc = true
		}
	}
	if !foundDesc {
		t.Errorf("live description missing from suggestions: %v", resp.Descriptions)
	}
}

func TestHandleDeleteTransaction_AlreadyTombstoned_Returns404(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	txn := seedTombstonedTestTransaction(t, q, user.ID, 1, "2026-04-01", 10.0, "gone")

	req := httptest.NewRequest(http.MethodDelete, "/api/transactions/"+fmt.Sprintf("%d", txn.ID), nil)
	req = withUserAndURLParam(req, user, "id", fmt.Sprintf("%d", txn.ID))
	rec := httptest.NewRecorder()
	h.handleDeleteTransaction(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for already-tombstoned row, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleUpdateTransaction_AlreadyTombstoned_Returns404(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	txn := seedTombstonedTestTransaction(t, q, user.ID, 1, "2026-04-01", 10.0, "gone")

	body := strings.NewReader(`{"date":"2026-04-01","amount":99,"description":"x","category_id":1}`)
	req := httptest.NewRequest(http.MethodPut, "/api/transactions/"+fmt.Sprintf("%d", txn.ID), body)
	req = withUserAndURLParam(req, user, "id", fmt.Sprintf("%d", txn.ID))
	rec := httptest.NewRecorder()
	h.handleUpdateTransaction(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for already-tombstoned row, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// --- Bulk-edit Task 1: pure helpers ----------------------------------------

func TestSummarizePatch_FormatsAllFieldKindsInFixedOrder(t *testing.T) {
	p := database.UpdatePatch{
		Date:        ptrString("2026-04-30"),
		Description: ptrString(`ATM card #4839 cash`),
		CategoryID:  ptrInt64(5),
		Tags:        ptrString("tax,receipt"),
		TagsMode:    ptrString("add"),
	}
	got := summarizePatch(p)
	want := `date=2026-04-30; description="ATM card #4839 cash"; category_id=5; tags=tax,receipt(add)`
	if got != want {
		t.Errorf("summarizePatch() = %q, want %q", got, want)
	}
}

func TestSummarizePatch_OmitsAbsentFields(t *testing.T) {
	p := database.UpdatePatch{CategoryID: ptrInt64(5)}
	if got := summarizePatch(p); got != "category_id=5" {
		t.Errorf("summarizePatch() = %q, want %q", got, "category_id=5")
	}
}

func TestSummarizePatch_EscapesQuotesInDescription(t *testing.T) {
	p := database.UpdatePatch{Description: ptrString(`he said "hi"`)}
	got := summarizePatch(p)
	want := `description="he said \"hi\""`
	if got != want {
		t.Errorf("summarizePatch() = %q, want %q", got, want)
	}
}

func TestSummarizePatch_GrepBoundaryRegression_EmbeddedQuoteSemicolon(t *testing.T) {
	p := database.UpdatePatch{Description: ptrString(`risky"; DROP TABLE`)}
	got := summarizePatch(p)
	want := `description="risky\"; DROP TABLE"`
	if got != want {
		t.Errorf("summarizePatch() = %q, want %q", got, want)
	}
}

func TestSummarizePatch_TruncatesAt1024Chars(t *testing.T) {
	long := strings.Repeat("x", 1100)
	p := database.UpdatePatch{Description: ptrString(long)}
	got := summarizePatch(p)
	if !strings.HasSuffix(got, "…(truncated)") {
		t.Errorf("expected truncation suffix, got tail %q", got[len(got)-20:])
	}
	if len(got) > 1024 {
		t.Errorf("expected len <= 1024, got %d", len(got))
	}
}

func TestSummarizePatch_TruncatesAtRuneBoundary_NotMidUTF8(t *testing.T) {
	long := strings.Repeat("…", 400) // 3-byte UTF-8 character × 400 = 1200 bytes
	p := database.UpdatePatch{Description: ptrString(long)}
	got := summarizePatch(p)
	if len(got) > 1024 {
		t.Errorf("len = %d, want <= 1024", len(got))
	}
	if !utf8.ValidString(got) {
		t.Errorf("output is not valid UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "…(truncated)") {
		t.Errorf("expected truncation suffix, got tail %q", got[len(got)-20:])
	}
}

func TestValidateDate_AcceptsCanonicalForm(t *testing.T) {
	got, err := validateDate("2026-04-30")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "2026-04-30" {
		t.Errorf("validateDate(\"2026-04-30\") = %q, want \"2026-04-30\"", got)
	}
}

func TestValidateDate_RejectsBadFormat(t *testing.T) {
	cases := []string{"2026/04/30", "30-04-2026", "April 30 2026", "", "2026-13-40"}
	for _, c := range cases {
		if _, err := validateDate(c); err == nil {
			t.Errorf("validateDate(%q) returned nil, want error", c)
		}
	}
}

func TestValidateDescription_TrimsAndCaps(t *testing.T) {
	got, err := validateDescription("  hello  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "hello" {
		t.Errorf("validateDescription trim = %q, want \"hello\"", got)
	}
	long := strings.Repeat("x", MaxDescriptionLength+1)
	if _, err := validateDescription(long); err == nil {
		t.Errorf("validateDescription oversized: nil, want error")
	}
}

func TestValidateCategoryID_PositiveOnly(t *testing.T) {
	if _, err := validateCategoryID(5); err != nil {
		t.Errorf("validateCategoryID(5): %v", err)
	}
	for _, n := range []int64{0, -1, -100} {
		if _, err := validateCategoryID(n); err == nil {
			t.Errorf("validateCategoryID(%d): nil, want error", n)
		}
	}
}

func TestValidateTagsField_LengthCheckOnly(t *testing.T) {
	if _, err := validateTagsField("Tax, receipt"); err != nil {
		t.Errorf("validateTagsField: %v", err)
	}
	long := strings.Repeat("a,", MaxTagsLength) // length > MaxTagsLength
	if _, err := validateTagsField(long); err == nil {
		t.Errorf("validateTagsField oversized: nil, want error")
	}
}

func TestBuildUpdatePatch_HappyPath(t *testing.T) {
	in := patchRequest{
		Date:       ptrString("2026-04-30"),
		CategoryID: ptrInt64(5),
		Tags:       ptrString("tax,receipt"),
	}
	p, err := buildUpdatePatch(in, ptrString("add"))
	if err != nil {
		t.Fatalf("buildUpdatePatch: %v", err)
	}
	if p.Date == nil || *p.Date != "2026-04-30" {
		t.Errorf("Date: got %v, want pointer to \"2026-04-30\"", p.Date)
	}
	if p.CategoryID == nil || *p.CategoryID != 5 {
		t.Errorf("CategoryID: got %v, want 5", p.CategoryID)
	}
	if p.Tags == nil || *p.Tags != "tax,receipt" {
		t.Errorf("Tags: got %v, want \"tax,receipt\"", p.Tags)
	}
	if p.TagsMode == nil || *p.TagsMode != "add" {
		t.Errorf("TagsMode: got %v, want \"add\"", p.TagsMode)
	}
}

func TestBuildUpdatePatch_RejectsTagsWithoutMode(t *testing.T) {
	in := patchRequest{Tags: ptrString("tax")}
	_, err := buildUpdatePatch(in, nil)
	if err == nil {
		t.Errorf("expected error when tags set but mode missing")
	}
}

func TestBuildUpdatePatch_RejectsModeWithoutTags(t *testing.T) {
	in := patchRequest{CategoryID: ptrInt64(5)}
	_, err := buildUpdatePatch(in, ptrString("add"))
	if err == nil {
		t.Errorf("expected error when mode set but tags missing")
	}
}

func TestBuildUpdatePatch_RejectsInvalidMode(t *testing.T) {
	in := patchRequest{Tags: ptrString("tax")}
	for _, m := range []string{"set", "merge", "ADD", "", "remove "} {
		_, err := buildUpdatePatch(in, ptrString(m))
		if err == nil {
			t.Errorf("buildUpdatePatch with mode %q: nil, want error", m)
		}
	}
}

func TestBuildUpdatePatch_RejectsEmptyTagsWithMode(t *testing.T) {
	in := patchRequest{Tags: ptrString("")}
	for _, mode := range []string{"add", "remove", "replace"} {
		if _, err := buildUpdatePatch(in, ptrString(mode)); err == nil {
			t.Errorf("buildUpdatePatch(empty tags, %s mode): nil, want error", mode)
		}
	}
}

func TestBuildUpdatePatch_RejectsEmpty(t *testing.T) {
	in := patchRequest{}
	p, err := buildUpdatePatch(in, nil)
	if err != nil {
		t.Fatalf("buildUpdatePatch on empty: %v", err)
	}
	if !p.IsEmpty() {
		t.Errorf("expected IsEmpty()=true on empty input")
	}
}

func TestBuildUpdatePatch_TrimsAndValidatesEachFieldSelectively(t *testing.T) {
	in := patchRequest{Description: ptrString("  hi  ")}
	p, err := buildUpdatePatch(in, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Description == nil || *p.Description != "hi" {
		t.Errorf("Description not trimmed: %v", p.Description)
	}
	bad := patchRequest{Date: ptrString("nope")}
	if _, err := buildUpdatePatch(bad, nil); err == nil {
		t.Errorf("bad date: nil, want error")
	}
}

// --- Bulk-edit Task 3: handleBatchUpdateTransactions -----------------------

// postBatchUpdate is a small helper that builds the JSON body, sets the
// authenticated user on the request, and invokes the handler directly.
// We construct the request in the same shape as every existing handler
// test in this file (no router wiring, just package-level Handler method
// dispatch) so the tests stay isolated from chi routing and middleware.
func postBatchUpdate(t *testing.T, h *Handler, user database.User, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/batch-update", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleBatchUpdateTransactions(rec, req)
	return rec
}

func TestBatchUpdate_HappyPath_AllRowsUpdated(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	catA := seedTestCategory(t, q, "A", "expense")
	catB := seedTestCategory(t, q, "B", "expense")
	t1 := seedTestTransaction(t, q, user.ID, catA.ID, "2026-04-01", 10.0, "T1")
	t2 := seedTestTransaction(t, q, user.ID, catA.ID, "2026-04-02", 20.0, "T2")
	t3 := seedTestTransaction(t, q, user.ID, catA.ID, "2026-04-03", 30.0, "T3")

	body := fmt.Sprintf(`{"ids":[%d,%d,%d],"patch":{"category_id":%d}}`, t1.ID, t2.ID, t3.ID, catB.ID)
	rec := postBatchUpdate(t, h, user, body)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Updated int64 `json:"updated"`
		Skipped int64 `json:"skipped"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Updated != 3 || resp.Skipped != 0 {
		t.Errorf("got updated=%d skipped=%d, want 3 0", resp.Updated, resp.Skipped)
	}
	for _, id := range []int64{t1.ID, t2.ID, t3.ID} {
		var got int64
		if err := db.QueryRow(`SELECT category_id FROM transactions WHERE id = ?`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != catB.ID {
			t.Errorf("id=%d category: %d, want %d", id, got, catB.ID)
		}
	}
}

// TestBatchUpdate_HidesTombstoned uses the CLAUDE.md canonical pattern:
// seed a tombstoned row with a sentinel amount of 999, then assert that the
// row is never touched. Per CLAUDE.md soft-delete invariant, the
// tombstoned row must remain at its original category and amount and must
// still have deleted_at set after the bulk update.
func TestBatchUpdate_HidesTombstoned(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	catA := seedTestCategory(t, q, "A", "expense")
	catB := seedTestCategory(t, q, "B", "expense")
	live := seedTestTransaction(t, q, user.ID, catA.ID, "2026-04-01", 10.0, "live")
	ts := seedTombstonedTestTransaction(t, q, user.ID, catA.ID, "2026-04-02", 999.0, "tombstoned-sentinel")

	body := fmt.Sprintf(`{"ids":[%d,%d],"patch":{"category_id":%d}}`, live.ID, ts.ID, catB.ID)
	rec := postBatchUpdate(t, h, user, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Updated int64 `json:"updated"`
		Skipped int64 `json:"skipped"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Updated != 1 || resp.Skipped != 1 {
		t.Errorf("got updated=%d skipped=%d, want 1 1", resp.Updated, resp.Skipped)
	}

	// Sentinel: tombstoned row must remain untouched.
	// Phase 3.1b: the legacy REAL amount column was dropped in migration 010,
	// so the sentinel is checked against amount_cents ($999.00 -> 99900 cents).
	var tsCat int64
	var tsAmountCents int64
	var tsDeletedAt sql.NullString
	if err := db.QueryRow(`SELECT category_id, amount_cents, deleted_at FROM transactions WHERE id = ?`, ts.ID).
		Scan(&tsCat, &tsAmountCents, &tsDeletedAt); err != nil {
		t.Fatal(err)
	}
	if tsCat != catA.ID {
		t.Errorf("tombstoned row's category got mutated: %d (want %d — must remain untouched)", tsCat, catA.ID)
	}
	if tsAmountCents != 99900 {
		t.Errorf("tombstoned row's amount_cents changed: %d (sentinel violated)", tsAmountCents)
	}
	if !tsDeletedAt.Valid {
		t.Errorf("tombstoned row got resurrected: deleted_at is null")
	}

	// And the live row must have been updated.
	var liveCat int64
	if err := db.QueryRow(`SELECT category_id FROM transactions WHERE id = ?`, live.ID).Scan(&liveCat); err != nil {
		t.Fatal(err)
	}
	if liveCat != catB.ID {
		t.Errorf("live row not updated: %d", liveCat)
	}
}

func TestBatchUpdate_PartialSkip_TombstonedAndNonOwnedAreSkipped(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	alice := seedTestUser(t, q, "alice", "member")
	bob := seedTestUser(t, q, "bob", "member")
	catA := seedTestCategory(t, q, "A", "expense")
	catB := seedTestCategory(t, q, "B", "expense")

	aliveAlice := seedTestTransaction(t, q, alice.ID, catA.ID, "2026-04-01", 10.0, "ok")
	tombstonedAlice := seedTombstonedTestTransaction(t, q, alice.ID, catA.ID, "2026-04-02", 10.0, "ts")
	aliveBob := seedTestTransaction(t, q, bob.ID, catA.ID, "2026-04-03", 10.0, "bob")

	body := fmt.Sprintf(`{"ids":[%d,%d,%d,99999],"patch":{"category_id":%d}}`,
		aliveAlice.ID, tombstonedAlice.ID, aliveBob.ID, catB.ID)
	rec := postBatchUpdate(t, h, alice, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Updated int64 `json:"updated"`
		Skipped int64 `json:"skipped"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Updated != 1 || resp.Skipped != 3 {
		t.Errorf("got updated=%d skipped=%d, want 1 3", resp.Updated, resp.Skipped)
	}

	// Verify alice's row was the only one mutated.
	var aliveAliceCat int64
	db.QueryRow(`SELECT category_id FROM transactions WHERE id = ?`, aliveAlice.ID).Scan(&aliveAliceCat)
	if aliveAliceCat != catB.ID {
		t.Errorf("alice's live row not updated: %d", aliveAliceCat)
	}
	// Bob's row must not have been touched.
	var bobCat int64
	db.QueryRow(`SELECT category_id FROM transactions WHERE id = ?`, aliveBob.ID).Scan(&bobCat)
	if bobCat != catA.ID {
		t.Errorf("bob's row was wrongly touched: %d", bobCat)
	}
}

// TestBatchUpdate_AdminUpdatesOtherMembersRows is the regression test for the
// reported bug: an admin bulk-editing a mixed-owner selection updated only the
// rows they had personally created and silently skipped the rest ("updated 1,
// skipped N" in the toast). The transactions list is household-wide
// (handleListTransactions: "Transactions are visible to all authenticated
// users"), so a normal admin selection spans several members' rows.
//
// Every sibling mutation path already grants the admin bypass — single-row
// update (`user.Role != RoleAdmin && existing.UserID != user.ID` → 403),
// batch-delete (same predicate → skip), update-by-filter (no user_id predicate
// appended for admins). Only the batch-update path enforced strict ownership,
// which is also what design spec §3.9 said it must not do.
func TestBatchUpdate_AdminUpdatesOtherMembersRows(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", RoleAdmin)
	bob := seedTestUser(t, q, "bob", RoleMember)
	catA := seedTestCategory(t, q, "A", "expense")
	catB := seedTestCategory(t, q, "B", "expense")

	own := seedTestTransaction(t, q, admin.ID, catA.ID, "2026-04-01", 10.0, "admin-own")
	bobs1 := seedTestTransaction(t, q, bob.ID, catA.ID, "2026-04-02", 20.0, "bob-1")
	bobs2 := seedTestTransaction(t, q, bob.ID, catA.ID, "2026-04-03", 30.0, "bob-2")

	body := fmt.Sprintf(`{"ids":[%d,%d,%d],"patch":{"category_id":%d}}`,
		own.ID, bobs1.ID, bobs2.ID, catB.ID)
	rec := postBatchUpdate(t, h, admin, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Updated int64 `json:"updated"`
		Skipped int64 `json:"skipped"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Updated != 3 || resp.Skipped != 0 {
		t.Errorf("got updated=%d skipped=%d, want 3 0", resp.Updated, resp.Skipped)
	}

	// All three rows moved to the new category...
	for _, tc := range []struct {
		id    int64
		owner int64
	}{{own.ID, admin.ID}, {bobs1.ID, bob.ID}, {bobs2.ID, bob.ID}} {
		var gotCat, gotOwner int64
		if err := db.QueryRow(
			`SELECT category_id, user_id FROM transactions WHERE id = ?`, tc.id,
		).Scan(&gotCat, &gotOwner); err != nil {
			t.Fatal(err)
		}
		if gotCat != catB.ID {
			t.Errorf("id=%d category: %d, want %d", tc.id, gotCat, catB.ID)
		}
		// ...and ownership is untouched: an admin edit must not re-home a
		// member's row onto the admin.
		if gotOwner != tc.owner {
			t.Errorf("id=%d user_id: %d, want %d (admin edit must not re-home the row)",
				tc.id, gotOwner, tc.owner)
		}
	}
}

// TestBatchUpdate_AdminStillSkipsTombstoned pins that the admin bypass loosens
// the OWNERSHIP predicate only. A tombstoned row stays untouchable for every
// role, so an admin bulk edit can never silently resurrect deleted rows into
// the ledger. The load-bearing assertions are that the row's category is
// unchanged and its tombstone intact — amount is not reachable from
// UpdatePatch at all, so this is a write-path skip test, not an instance of
// the CLAUDE.md sentinel-amount pattern (which guards reads leaking
// tombstoned rows into aggregates).
func TestBatchUpdate_AdminStillSkipsTombstoned(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", RoleAdmin)
	bob := seedTestUser(t, q, "bob", RoleMember)
	catA := seedTestCategory(t, q, "A", "expense")
	catB := seedTestCategory(t, q, "B", "expense")

	live := seedTestTransaction(t, q, bob.ID, catA.ID, "2026-04-01", 10.0, "bob-live")
	ts := seedTombstonedTestTransaction(t, q, bob.ID, catA.ID, "2026-04-02", 999.0, "tombstoned-sentinel")

	body := fmt.Sprintf(`{"ids":[%d,%d],"patch":{"category_id":%d}}`, live.ID, ts.ID, catB.ID)
	rec := postBatchUpdate(t, h, admin, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Updated int64 `json:"updated"`
		Skipped int64 `json:"skipped"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Updated != 1 || resp.Skipped != 1 {
		t.Errorf("got updated=%d skipped=%d, want 1 1", resp.Updated, resp.Skipped)
	}

	var tsCat int64
	var tsDeleted sql.NullString
	if err := db.QueryRow(
		`SELECT category_id, deleted_at FROM transactions WHERE id = ?`, ts.ID,
	).Scan(&tsCat, &tsDeleted); err != nil {
		t.Fatal(err)
	}
	if tsCat != catA.ID {
		t.Errorf("tombstoned row was mutated by an admin batch update: category %d, want %d", tsCat, catA.ID)
	}
	if !tsDeleted.Valid {
		t.Error("tombstoned row was resurrected by an admin batch update")
	}
}

// TestBatchUpdate_AdminTagsMode_MergesAgainstEachRowsOwnTags covers the branch
// where the admin bypass intersects the per-row tag pre-load. For non-replace
// tag modes the handler reads each row's CURRENT tags before calling UpdateTx,
// and with the bypass in place those reads now span other members' rows. Each
// row must merge against its OWN tags — a future refactor that hoists the
// pre-load out of the loop or caches curTags across iterations would smear one
// member's tags onto another's rows. (TestBatchUpdate_TagsAdd_AppendsAndDedup-
// licates already covers the multi-row merge for a single owner; this test adds
// the cross-owner axis the bypass newly makes reachable, and fails outright if
// the bypass is removed.)
func TestBatchUpdate_AdminTagsMode_MergesAgainstEachRowsOwnTags(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", RoleAdmin)
	bob := seedTestUser(t, q, "bob", RoleMember)
	cat := seedTestCategory(t, q, "A", "expense")

	adminRow := seedTestTransactionWithTags(t, q, admin.ID, cat.ID, "2026-04-01", 10.0, "admin-row", "adminonly")
	bobRow := seedTestTransactionWithTags(t, q, bob.ID, cat.ID, "2026-04-02", 20.0, "bob-row", "bobonly")

	body := fmt.Sprintf(`{"ids":[%d,%d],"patch":{"tags":"shared"},"tagsMode":"add"}`, adminRow.ID, bobRow.ID)
	rec := postBatchUpdate(t, h, admin, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	for _, tc := range []struct {
		id       int64
		wantTags string
	}{
		{adminRow.ID, "adminonly, shared"},
		{bobRow.ID, "bobonly, shared"},
	} {
		var got sql.NullString
		if err := db.QueryRow(`SELECT tags FROM transactions WHERE id = ?`, tc.id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got.String != tc.wantTags {
			t.Errorf("id=%d tags = %q, want %q (each row must merge against its own tags)",
				tc.id, got.String, tc.wantTags)
		}
	}
}

// TestBatchUpdate_AdminCrossUserEdit_AuditAttributesActorNotOwner pins the
// forensics contract for the newly-reachable cross-user edit: the audit row
// records the ADMIN who made the change, while the transaction itself stays
// owned by the member. Without both halves an operator reading the audit log
// could not tell who recategorized someone else's spending.
func TestBatchUpdate_AdminCrossUserEdit_AuditAttributesActorNotOwner(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", RoleAdmin)
	bob := seedTestUser(t, q, "bob", RoleMember)
	catA := seedTestCategory(t, q, "A", "expense")
	catB := seedTestCategory(t, q, "B", "expense")

	bobRow := seedTestTransaction(t, q, bob.ID, catA.ID, "2026-04-01", 10.0, "bob-row")

	body := fmt.Sprintf(`{"ids":[%d],"patch":{"category_id":%d}}`, bobRow.ID, catB.ID)
	rec := postBatchUpdate(t, h, admin, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	var actor sql.NullInt64
	if err := db.QueryRow(
		`SELECT actor_user_id FROM transaction_audit WHERE transaction_id = ? AND action = ?`,
		bobRow.ID, "update",
	).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	if !actor.Valid || actor.Int64 != admin.ID {
		t.Errorf("audit actor_user_id = %v, want %d (the acting admin)", actor, admin.ID)
	}

	var owner int64
	if err := db.QueryRow(`SELECT user_id FROM transactions WHERE id = ?`, bobRow.ID).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner != bob.ID {
		t.Errorf("transaction user_id = %d, want %d (an admin edit must not re-home the row)", owner, bob.ID)
	}
}

// registerAndSessionCookie registers a user through the real handler and
// returns their session cookie. The first user to register becomes admin;
// later ones become members (registration_enabled must be on for those).
func registerAndSessionCookie(t *testing.T, h *Handler, username string) *http.Cookie {
	t.Helper()
	body := strings.NewReader(fmt.Sprintf(`{"username":%q,"password":"longpassword"}`, username))
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register", body)
	// registerAttempts is a package-level map keyed by client IP that is never
	// reset between tests. Nothing increments it on a SUCCESSFUL register
	// today, so this helper is currently safe — but a future duplicate-username
	// or low-RATE_LIMIT_MAX test in this package would make it order-dependent
	// (a 429 instead of a 201, depending on test order). Clear our IP up front
	// so this test never becomes that flake.
	//
	// The key is clientIPForRateLimit's masked network prefix, not the bare
	// address — deleting under the bare address would silently stop clearing
	// anything and re-open the flake.
	rateLimitMu.Lock()
	delete(registerAttempts, clientIPForRateLimit(req))
	rateLimitMu.Unlock()
	rec := httptest.NewRecorder()
	h.handleRegister(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register %q: got %d, body: %s", username, rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == "session" {
			return c
		}
	}
	t.Fatalf("register %q: no session cookie set", username)
	return nil
}

// TestBatchUpdate_RoleComesFromTheDatabaseNotTheWire drives the endpoint
// through the REAL auth middleware instead of injecting a user into the
// request context the way every other handler test in this file does.
//
// The admin bypass is derived from user.Role, so the security question is
// whether a member can cause `user.Role == RoleAdmin` to evaluate true. The
// middleware resolves the session cookie to a user id and re-reads the row
// (including role) from the users table on every request, so it cannot — this
// test pins that end-to-end, in both directions, over the wire.
func TestBatchUpdate_RoleComesFromTheDatabaseNotTheWire(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)

	// First registered user becomes admin.
	adminCookie := registerAndSessionCookie(t, h, "adminuser")
	// Open registration so a second (member-role) user can be created.
	if err := q.UpsertSetting(context.Background(), database.UpsertSettingParams{
		Key:   "registration_enabled",
		Value: "true",
	}); err != nil {
		t.Fatalf("upsert setting: %v", err)
	}
	memberCookie := registerAndSessionCookie(t, h, "memberuser")

	var adminID, memberID int64
	if err := db.QueryRow(`SELECT id FROM users WHERE username = ?`, "adminuser").Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT id FROM users WHERE username = ?`, "memberuser").Scan(&memberID); err != nil {
		t.Fatal(err)
	}
	catA := seedTestCategory(t, q, "A", "expense")
	catB := seedTestCategory(t, q, "B", "expense")
	// Each arm targets a row the actor does NOT own, so both arms exercise the
	// bypass rather than plain ownership. (Pointing both at the admin's own row
	// would make the admin arm succeed via ownership and prove nothing — it
	// would stay green even if the bypass were deleted entirely.)
	adminRow := seedTestTransaction(t, q, adminID, catA.ID, "2026-04-01", 10.0, "admin-owned")
	memberRow := seedTestTransaction(t, q, memberID, catA.ID, "2026-04-02", 20.0, "member-owned")

	limiter := ratelimit.NewBucket(30, 10*time.Minute, h.clock)
	r := chi.NewRouter()
	r.Route("/api", func(r chi.Router) {
		r.Use(auth.RequireAuthOrAPIToken(h.queries, limiter))
		r.Post("/transactions/batch-update", h.handleBatchUpdateTransactions)
	})

	post := func(c *http.Cookie, targetID int64) (updated, skipped int64) {
		t.Helper()
		body := fmt.Sprintf(`{"ids":[%d],"patch":{"category_id":%d}}`, targetID, catB.ID)
		req := httptest.NewRequest(http.MethodPost, "/api/transactions/batch-update", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(c)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d, want 200; body: %s", rec.Code, rec.Body.String())
		}
		var resp struct {
			Updated int64 `json:"updated"`
			Skipped int64 `json:"skipped"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp.Updated, resp.Skipped
	}

	// A member over a genuine session must NOT get the bypass on a row they
	// don't own.
	if updated, skipped := post(memberCookie, adminRow.ID); updated != 0 || skipped != 1 {
		t.Errorf("member session: updated=%d skipped=%d, want 0 1 — a member reached the admin bypass", updated, skipped)
	}
	var gotCat int64
	if err := db.QueryRow(`SELECT category_id FROM transactions WHERE id = ?`, adminRow.ID).Scan(&gotCat); err != nil {
		t.Fatal(err)
	}
	if gotCat != catA.ID {
		t.Fatalf("member mutated a row they do not own: category %d, want %d", gotCat, catA.ID)
	}

	// The admin's session DOES get the bypass on a row they don't own. This
	// arm fails if the bypass is removed, so it is a real control, not a
	// restatement of ownership.
	if updated, skipped := post(adminCookie, memberRow.ID); updated != 1 || skipped != 0 {
		t.Errorf("admin session: updated=%d skipped=%d, want 1 0", updated, skipped)
	}
	if err := db.QueryRow(`SELECT category_id FROM transactions WHERE id = ?`, memberRow.ID).Scan(&gotCat); err != nil {
		t.Fatal(err)
	}
	if gotCat != catB.ID {
		t.Errorf("admin failed to edit a member's row: category %d, want %d", gotCat, catB.ID)
	}
}

func TestBatchUpdate_RejectsEmptyIDList(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	rec := postBatchUpdate(t, h, user, `{"ids":[],"patch":{"category_id":1}}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty ids: got %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestBatchUpdate_RejectsOversizedIDList(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	idsList := make([]string, MaxBatchUpdateIDs+1)
	for i := range idsList {
		idsList[i] = fmt.Sprintf("%d", i+1)
	}
	body := fmt.Sprintf(`{"ids":[%s],"patch":{"category_id":1}}`, strings.Join(idsList, ","))
	rec := postBatchUpdate(t, h, user, body)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("oversized ids: got %d, want 400", rec.Code)
	}
}

func TestBatchUpdate_RejectsEmptyPatch(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "Cat", "expense")
	txn := seedTestTransaction(t, q, user.ID, cat.ID, "2026-04-01", 10.0, "T")
	body := fmt.Sprintf(`{"ids":[%d],"patch":{}}`, txn.ID)
	rec := postBatchUpdate(t, h, user, body)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty patch: got %d, want 400", rec.Code)
	}
}

// TestBatchUpdate_DisallowUnknownFields_RejectsUserIDInjection pins the
// mass-assignment guard from spec §5.5b: extra patch keys (`user_id`,
// `deleted_at`, …) must error out with a 400, not be silently dropped onto
// the typed struct. The current typed UpdatePatch shields this incidentally,
// but DisallowUnknownFields makes the contract explicit so a future field
// addition cannot widen the attack surface unreviewed.
func TestBatchUpdate_DisallowUnknownFields_RejectsUserIDInjection(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "Cat", "expense")
	txn := seedTestTransaction(t, q, user.ID, cat.ID, "2026-04-01", 10.0, "T")
	body := fmt.Sprintf(`{"ids":[%d],"patch":{"user_id":99,"category_id":%d}}`, txn.ID, cat.ID)
	rec := postBatchUpdate(t, h, user, body)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unknown field; got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestBatchUpdate_TagsAdd_AppendsAndDeduplicates pins the spec §3.3 worked
// example for Add mode: existing "Tax, receipt" + Add "tax,receipt" yields
// "Tax, receipt, tax". Set arithmetic is byte-for-byte case-sensitive
// ("Tax" != "tax"), and items already present in the existing set dedupe
// ("receipt" stays once). The empty-existing case appends the input
// verbatim with the canonical ", " join.
func TestBatchUpdate_TagsAdd_AppendsAndDeduplicates(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "Cat", "expense")
	t1 := seedTestTransactionWithTags(t, q, user.ID, cat.ID, "2026-04-01", 10.0, "T1", "Tax, receipt")
	t2 := seedTestTransactionWithTags(t, q, user.ID, cat.ID, "2026-04-02", 10.0, "T2", "")

	body := fmt.Sprintf(`{"ids":[%d,%d],"patch":{"tags":"tax,receipt"},"tagsMode":"add"}`, t1.ID, t2.ID)
	rec := postBatchUpdate(t, h, user, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, body: %s", rec.Code, rec.Body.String())
	}

	var got1 string
	db.QueryRow(`SELECT tags FROM transactions WHERE id = ?`, t1.ID).Scan(&got1)
	if got1 != "Tax, receipt, tax" {
		t.Errorf("t1 tags: %q, want %q", got1, "Tax, receipt, tax")
	}
	var got2 string
	db.QueryRow(`SELECT tags FROM transactions WHERE id = ?`, t2.ID).Scan(&got2)
	if got2 != "tax, receipt" {
		t.Errorf("t2 tags: %q, want %q", got2, "tax, receipt")
	}
}

func TestBatchUpdate_TagsRemove_FiltersMatching(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "Cat", "expense")
	txn := seedTestTransactionWithTags(t, q, user.ID, cat.ID, "2026-04-01", 10.0, "T", "tax,personal,receipt")

	body := fmt.Sprintf(`{"ids":[%d],"patch":{"tags":"tax,personal"},"tagsMode":"remove"}`, txn.ID)
	rec := postBatchUpdate(t, h, user, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, body: %s", rec.Code, rec.Body.String())
	}

	var got string
	db.QueryRow(`SELECT tags FROM transactions WHERE id = ?`, txn.ID).Scan(&got)
	if got != "receipt" {
		t.Errorf("tags: %q, want %q", got, "receipt")
	}
}

func TestBatchUpdate_TagsReplace_OverwritesExisting(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "Cat", "expense")
	txn := seedTestTransactionWithTags(t, q, user.ID, cat.ID, "2026-04-01", 10.0, "T", "old1,old2,old3")

	body := fmt.Sprintf(`{"ids":[%d],"patch":{"tags":"fresh"},"tagsMode":"replace"}`, txn.ID)
	rec := postBatchUpdate(t, h, user, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, body: %s", rec.Code, rec.Body.String())
	}

	var got string
	db.QueryRow(`SELECT tags FROM transactions WHERE id = ?`, txn.ID).Scan(&got)
	if got != "fresh" {
		t.Errorf("tags: %q, want %q", got, "fresh")
	}
}

// TestBatchUpdate_TagsCaseSensitivePinning pins the exact case-sensitive
// worked example from spec §3.3: row has "Tax, receipt"; user adds "tax";
// result is "Tax, receipt, tax" (three distinct entries — "Tax" and "tax"
// are different bytes).
func TestBatchUpdate_TagsCaseSensitivePinning(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "Cat", "expense")
	txn := seedTestTransactionWithTags(t, q, user.ID, cat.ID, "2026-04-01", 10.0, "T", "Tax, receipt")

	body := fmt.Sprintf(`{"ids":[%d],"patch":{"tags":"tax"},"tagsMode":"add"}`, txn.ID)
	rec := postBatchUpdate(t, h, user, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, body: %s", rec.Code, rec.Body.String())
	}

	var got string
	db.QueryRow(`SELECT tags FROM transactions WHERE id = ?`, txn.ID).Scan(&got)
	if got != "Tax, receipt, tax" {
		t.Errorf("tags: %q, want %q", got, "Tax, receipt, tax")
	}
}

// ptrString / ptrInt64 are local test helpers used by the bulk-edit tests
// above to construct the pointer-shaped fields on database.UpdatePatch /
// patchRequest.
func ptrString(s string) *string { return &s }
func ptrInt64(n int64) *int64    { return &n }

// --- Bulk-edit Task 4: handleUpdateTransactionsByFilter --------------------

// postUpdateByFilter mirrors postBatchUpdate's shape but targets the filter
// endpoint. The querystring portion of the URL is what handleUpdateTransactionsByFilter
// reads via r.URL.Query() — buildTransactionWhereClause feeds off the same
// query params handleListTransactions accepts (date_from, date_to, category_id,
// type, search, amount_min/max, category_ids, tags). The body carries the
// patch + tagsMode in the same JSON envelope as batch-update.
func postUpdateByFilter(t *testing.T, h *Handler, user database.User, queryString, body string) *httptest.ResponseRecorder {
	t.Helper()
	url := "/api/transactions/update-by-filter"
	if queryString != "" {
		url = url + "?" + queryString
	}
	req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleUpdateTransactionsByFilter(rec, req)
	return rec
}

func TestUpdateByFilter_HappyPath(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	catCleaning := seedTestCategory(t, q, "Cleaning", "expense")
	catHousehold := seedTestCategory(t, q, "Household", "expense")
	seedTestTransaction(t, q, user.ID, catCleaning.ID, "2026-04-01", 10.0, "vacuum")
	seedTestTransaction(t, q, user.ID, catCleaning.ID, "2026-04-02", 5.0, "mop")
	seedTestTransaction(t, q, user.ID, catCleaning.ID, "2026-04-03", 3.0, "soap")
	// not in filter — must remain at catHousehold
	seedTestTransaction(t, q, user.ID, catHousehold.ID, "2026-04-04", 4.0, "lightbulb")

	body := fmt.Sprintf(`{"patch":{"category_id":%d}}`, catHousehold.ID)
	rec := postUpdateByFilter(t, h, user, fmt.Sprintf("category_id=%d", catCleaning.ID), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, body: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Updated int64 `json:"updated"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Updated != 3 {
		t.Errorf("updated=%d, want 3", resp.Updated)
	}

	// All three cleaning rows must now be at the household category.
	var movedToHousehold int
	if err := db.QueryRow(`SELECT COUNT(*) FROM transactions WHERE category_id = ? AND deleted_at IS NULL`, catHousehold.ID).Scan(&movedToHousehold); err != nil {
		t.Fatal(err)
	}
	if movedToHousehold != 4 { // 3 moved + 1 already there
		t.Errorf("household rows after move: %d, want 4", movedToHousehold)
	}
}

// TestUpdateByFilter_HidesTombstoned uses the canonical CLAUDE.md soft-delete
// sentinel pattern: seed one live row and one tombstoned row in the filter
// window, run update-by-filter, and assert the tombstoned row's category was
// NOT mutated. Sentinel amount of 999 makes any accidental write into the
// tombstoned row visually loud in failure output.
func TestUpdateByFilter_HidesTombstoned(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "Cat", "expense")
	catB := seedTestCategory(t, q, "CatB", "expense")
	live := seedTestTransaction(t, q, user.ID, cat.ID, "2026-04-01", 10.0, "live")
	ts := seedTombstonedTestTransaction(t, q, user.ID, cat.ID, "2026-04-02", 999.0, "tombstoned-sentinel")

	body := fmt.Sprintf(`{"patch":{"category_id":%d}}`, catB.ID)
	rec := postUpdateByFilter(t, h, user, fmt.Sprintf("category_id=%d", cat.ID), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, body: %s", rec.Code, rec.Body.String())
	}

	var liveCat, tsCat int64
	if err := db.QueryRow(`SELECT category_id FROM transactions WHERE id = ?`, live.ID).Scan(&liveCat); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT category_id FROM transactions WHERE id = ?`, ts.ID).Scan(&tsCat); err != nil {
		t.Fatal(err)
	}
	if liveCat != catB.ID {
		t.Errorf("live row not updated: cat=%d, want %d", liveCat, catB.ID)
	}
	if tsCat != cat.ID {
		t.Errorf("tombstoned row was touched! cat=%d, want %d (must remain untouched)", tsCat, cat.ID)
	}
	// And the row must still be tombstoned (not resurrected).
	var tsDel sql.NullString
	db.QueryRow(`SELECT deleted_at FROM transactions WHERE id = ?`, ts.ID).Scan(&tsDel)
	if !tsDel.Valid {
		t.Errorf("tombstoned row got resurrected: deleted_at is null")
	}
}

// TestUpdateByFilter_RespectsOwnershipForNonAdmin pins that a non-admin user
// can only update their own rows via update-by-filter — bob's row in the
// same filter window must remain at the original category.
func TestUpdateByFilter_RespectsOwnershipForNonAdmin(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	alice := seedTestUser(t, q, "alice", "member")
	bob := seedTestUser(t, q, "bob", "member")
	cat := seedTestCategory(t, q, "Cat", "expense")
	catB := seedTestCategory(t, q, "CatB", "expense")
	aliceTxn := seedTestTransaction(t, q, alice.ID, cat.ID, "2026-04-01", 10.0, "a")
	bobTxn := seedTestTransaction(t, q, bob.ID, cat.ID, "2026-04-02", 10.0, "b")

	body := fmt.Sprintf(`{"patch":{"category_id":%d}}`, catB.ID)
	rec := postUpdateByFilter(t, h, alice, fmt.Sprintf("category_id=%d", cat.ID), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, body: %s", rec.Code, rec.Body.String())
	}

	var aliceCat, bobCat int64
	db.QueryRow(`SELECT category_id FROM transactions WHERE id = ?`, aliceTxn.ID).Scan(&aliceCat)
	db.QueryRow(`SELECT category_id FROM transactions WHERE id = ?`, bobTxn.ID).Scan(&bobCat)
	if aliceCat != catB.ID {
		t.Errorf("alice's row not updated: %d, want %d", aliceCat, catB.ID)
	}
	if bobCat != cat.ID {
		t.Errorf("bob's row was wrongly touched: %d, want %d", bobCat, cat.ID)
	}
}

// TestUpdateByFilter_TagsPatch_RespectsOwnershipForNonAdmin mirrors
// TestUpdateByFilter_RespectsOwnershipForNonAdmin but drives the tags
// read-then-write branch (runUpdateByFilterTags) instead of the no-tags SQL
// UPDATE branch. That branch builds its own non-admin user_id scoping
// independently of runUpdateByFilterNoTags, and had zero coverage before
// this test — a full-suite mutation survivor found in deep review.
func TestUpdateByFilter_TagsPatch_RespectsOwnershipForNonAdmin(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	alice := seedTestUser(t, q, "alice", "member")
	bob := seedTestUser(t, q, "bob", "member")
	cat := seedTestCategory(t, q, "Cat", "expense")
	aliceTxn := seedTestTransactionWithTags(t, q, alice.ID, cat.ID, "2026-04-01", 10.0, "a", "old-a")
	bobTxn := seedTestTransactionWithTags(t, q, bob.ID, cat.ID, "2026-04-02", 10.0, "b", "old-b")

	body := `{"patch":{"tags":"b4tag"},"tagsMode":"replace"}`
	rec := postUpdateByFilter(t, h, alice, fmt.Sprintf("category_id=%d", cat.ID), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, body: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Updated int64 `json:"updated"`
	}
	decodeResponse(t, rec, &resp)
	if resp.Updated != 1 {
		t.Errorf("updated=%d, want 1", resp.Updated)
	}

	var aliceTags, bobTags string
	db.QueryRow(`SELECT tags FROM transactions WHERE id = ?`, aliceTxn.ID).Scan(&aliceTags)
	db.QueryRow(`SELECT tags FROM transactions WHERE id = ?`, bobTxn.ID).Scan(&bobTags)
	if aliceTags != "b4tag" {
		t.Errorf("alice's row not updated: %q, want %q", aliceTags, "b4tag")
	}
	if bobTags != "old-b" {
		t.Errorf("bob's row was wrongly touched: %q, want %q", bobTags, "old-b")
	}
}

func TestUpdateByFilter_RejectsEmptyPatch(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	body := `{"patch":{}}`
	rec := postUpdateByFilter(t, h, user, "", body)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty patch: got %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestUpdateByFilter_SearchAndCategoryFilter_DescriptionPatch is the B4
// backend pin: a description-only patch scoped by search PLUS another
// filter must rename only rows matching BOTH. Replace All rides this
// path; the retired rename endpoint it superseded dropped every filter
// but search.
func TestUpdateByFilter_SearchAndCategoryFilter_DescriptionPatch(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	catGroceries := seedTestCategory(t, q, "Groceries", "expense")
	catHousehold := seedTestCategory(t, q, "Household", "expense")
	inScope := seedTestTransaction(t, q, user.ID, catGroceries.ID, "2026-01-05", 10.0, "spinney dbayeh")
	wrongCat := seedTestTransaction(t, q, user.ID, catHousehold.ID, "2026-01-06", 20.0, "spinney household")
	wrongSearch := seedTestTransaction(t, q, user.ID, catGroceries.ID, "2026-01-07", 30.0, "carrefour")

	body := `{"patch":{"description":"Spinneys"}}`
	rec := postUpdateByFilter(t, h, user,
		fmt.Sprintf("search=spinney&category_id=%d", catGroceries.ID), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, body: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Updated int64 `json:"updated"`
	}
	decodeResponse(t, rec, &resp)
	if resp.Updated != 1 {
		t.Errorf("updated=%d, want 1 (search AND category must both apply)", resp.Updated)
	}
	for _, tc := range []struct {
		id   int64
		want string
	}{
		{inScope.ID, "Spinneys"},
		{wrongCat.ID, "spinney household"},
		{wrongSearch.ID, "carrefour"},
	} {
		var desc string
		if err := db.QueryRow(`SELECT description FROM transactions WHERE id = ?`, tc.id).Scan(&desc); err != nil {
			t.Fatal(err)
		}
		if desc != tc.want {
			t.Errorf("row %d: description=%q, want %q", tc.id, desc, tc.want)
		}
	}
}

// TestUpdateByFilter_SearchFilter_CaseInsensitive transfers the
// case-insensitive-search coverage from the retired bulk rename endpoint's
// test suite: SQLite LIKE is case-insensitive for ASCII, and Replace All's
// match set depends on it.
func TestUpdateByFilter_SearchFilter_CaseInsensitive(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "Cat", "expense")
	matched := seedTestTransaction(t, q, user.ID, cat.ID, "2026-04-01", 10.0, "MR BROWN COFFEE")
	control := seedTestTransaction(t, q, user.ID, cat.ID, "2026-04-02", 20.0, "carrefour")

	rec := postUpdateByFilter(t, h, user, "search=mr+brown", `{"patch":{"description":"Mr Brown"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, body: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Updated int64 `json:"updated"`
	}
	decodeResponse(t, rec, &resp)
	if resp.Updated != 1 {
		t.Errorf("updated=%d, want 1 (LIKE must match case-insensitively)", resp.Updated)
	}

	var matchedDesc, controlDesc string
	if err := db.QueryRow(`SELECT description FROM transactions WHERE id = ?`, matched.ID).Scan(&matchedDesc); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT description FROM transactions WHERE id = ?`, control.ID).Scan(&controlDesc); err != nil {
		t.Fatal(err)
	}
	if matchedDesc != "Mr Brown" {
		t.Errorf("matched row: description=%q, want %q", matchedDesc, "Mr Brown")
	}
	if controlDesc != "carrefour" {
		t.Errorf("control row wrongly touched: description=%q, want %q", controlDesc, "carrefour")
	}
}

// TestUpdateByFilter_SearchFilter_EscapesSQLWildcards transfers the
// wildcard-escaping coverage from the retired bulk rename endpoint's test
// suite: a literal % in the search term must not act as a wildcard. The
// term travels in the querystring, so it is URL-encoded here exactly as
// the browser sends it.
func TestUpdateByFilter_SearchFilter_EscapesSQLWildcards(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "Cat", "expense")
	seedTestTransaction(t, q, user.ID, cat.ID, "2026-04-01", 10.0, "100% discount store")
	seedTestTransaction(t, q, user.ID, cat.ID, "2026-04-02", 20.0, "100 percent store")

	rec := postUpdateByFilter(t, h, user,
		"search="+url.QueryEscape("100%"), `{"patch":{"description":"HUNDRED PERCENT"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, body: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Updated int64 `json:"updated"`
	}
	decodeResponse(t, rec, &resp)
	if resp.Updated != 1 {
		t.Errorf("updated=%d, want 1 (%% must match only the literal)", resp.Updated)
	}
}

// TestUpdateByFilter_AdminDescriptionPatch_UpdatesAllUsers transfers the
// admin-scope coverage from the retired bulk rename endpoint's test suite:
// an admin's description patch reaches other members' rows (Transaction
// Authorization Discipline — admins bypass row ownership on every
// mutation path).
func TestUpdateByFilter_AdminDescriptionPatch_UpdatesAllUsers(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "boss", "admin")
	member := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "Cat", "expense")
	adminTxn := seedTestTransaction(t, q, admin.ID, cat.ID, "2026-04-01", 10.0, "shared shop")
	memberTxn := seedTestTransaction(t, q, member.ID, cat.ID, "2026-04-02", 20.0, "shared shop too")

	rec := postUpdateByFilter(t, h, admin, "search=shared+shop", `{"patch":{"description":"Shared Shop"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, body: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Updated int64 `json:"updated"`
	}
	decodeResponse(t, rec, &resp)
	if resp.Updated != 2 {
		t.Errorf("updated=%d, want 2 (admin scope is household-wide)", resp.Updated)
	}
	for _, id := range []int64{adminTxn.ID, memberTxn.ID} {
		var desc string
		if err := db.QueryRow(`SELECT description FROM transactions WHERE id = ?`, id).Scan(&desc); err != nil {
			t.Fatal(err)
		}
		if desc != "Shared Shop" {
			t.Errorf("row %d: description=%q, want %q", id, desc, "Shared Shop")
		}
	}
}

// TestUpdateByFilter_DescriptionPatch_UpdatesTimestamp transfers the
// timestamp-bump coverage from the retired bulk rename endpoint's test
// suite, including its backdating technique: SQLite CURRENT_TIMESTAMP has
// second precision, so the seeded row is pushed 10s into the past instead
// of sleeping.
func TestUpdateByFilter_DescriptionPatch_UpdatesTimestamp(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "Cat", "expense")
	txn := seedTestTransaction(t, q, user.ID, cat.ID, "2026-04-01", 10.0, "mr brown")

	if _, err := db.ExecContext(context.Background(),
		"UPDATE transactions SET updated_at = datetime('now', '-10 seconds') WHERE id = ?",
		txn.ID); err != nil {
		t.Fatalf("backdate updated_at: %v", err)
	}
	reread, err := q.GetTransactionByID(context.Background(), txn.ID)
	if err != nil {
		t.Fatalf("reread transaction: %v", err)
	}
	origUpdatedAt := reread.UpdatedAt

	rec := postUpdateByFilter(t, h, user, "search=mr+brown", `{"patch":{"description":"MR BROWN"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, body: %s", rec.Code, rec.Body.String())
	}
	updated, err := q.GetTransactionByID(context.Background(), txn.ID)
	if err != nil {
		t.Fatalf("get transaction: %v", err)
	}
	if !updated.UpdatedAt.After(origUpdatedAt) {
		t.Errorf("expected updated_at to advance; before=%v, after=%v", origUpdatedAt, updated.UpdatedAt)
	}
}

// TestUpdateByFilter_EmptyDescription_Returns400 pins validateDescription
// on this path: a whitespace-only description in the patch must 400, not
// blank every matching row. No existing test covered this.
func TestUpdateByFilter_EmptyDescription_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "Cat", "expense")
	txn := seedTestTransaction(t, q, user.ID, cat.ID, "2026-04-01", 10.0, "keep me")

	rec := postUpdateByFilter(t, h, user, "search=keep", `{"patch":{"description":"   "}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	var desc string
	if err := db.QueryRow(`SELECT description FROM transactions WHERE id = ?`, txn.ID).Scan(&desc); err != nil {
		t.Fatal(err)
	}
	if desc != "keep me" {
		t.Errorf("description mutated to %q despite 400", desc)
	}
}

// TestUpdateByFilter_TagsAdd_PerRowReadThenWrite pins that the tags read-then-write
// path enumerates each matching row, computes Add-mode set arithmetic per row,
// and writes the merged tags back independently. Two rows seeded with different
// existing tags must end up with each row's existing tags ∪ "fresh" — proving
// the per-row read is happening (a single SQL UPDATE could not produce
// row-dependent output).
func TestUpdateByFilter_TagsAdd_PerRowReadThenWrite(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "Cat", "expense")
	t1 := seedTestTransactionWithTags(t, q, user.ID, cat.ID, "2026-04-01", 10.0, "T1", "old1")
	t2 := seedTestTransactionWithTags(t, q, user.ID, cat.ID, "2026-04-02", 10.0, "T2", "old2")

	body := `{"patch":{"tags":"fresh"},"tagsMode":"add"}`
	rec := postUpdateByFilter(t, h, user, fmt.Sprintf("category_id=%d", cat.ID), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, body: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Updated int64 `json:"updated"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Updated != 2 {
		t.Errorf("updated=%d, want 2", resp.Updated)
	}

	var got1, got2 string
	db.QueryRow(`SELECT tags FROM transactions WHERE id = ?`, t1.ID).Scan(&got1)
	db.QueryRow(`SELECT tags FROM transactions WHERE id = ?`, t2.ID).Scan(&got2)
	if got1 != "old1, fresh" {
		t.Errorf("t1 tags: %q, want %q", got1, "old1, fresh")
	}
	if got2 != "old2, fresh" {
		t.Errorf("t2 tags: %q, want %q", got2, "old2, fresh")
	}
}

// TestUpdateByFilter_TagsRemove_DropsMatchingItems pins that Remove mode
// performs set-difference on the existing tags. Seeded "tax,personal,receipt"
// minus ["personal"] must yield "tax, receipt".
func TestUpdateByFilter_TagsRemove_DropsMatchingItems(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "Cat", "expense")
	txn := seedTestTransactionWithTags(t, q, user.ID, cat.ID, "2026-04-01", 10.0, "T", "tax,personal,receipt")

	body := `{"patch":{"tags":"personal"},"tagsMode":"remove"}`
	rec := postUpdateByFilter(t, h, user, fmt.Sprintf("category_id=%d", cat.ID), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, body: %s", rec.Code, rec.Body.String())
	}

	var got string
	db.QueryRow(`SELECT tags FROM transactions WHERE id = ?`, txn.ID).Scan(&got)
	if got != "tax, receipt" {
		t.Errorf("tags: %q, want %q", got, "tax, receipt")
	}
}

// TestUpdateByFilter_TagsReplace_OverwritesAllRows pins that Replace mode
// unconditionally overwrites the tags column with the provided value on
// every matching row regardless of what was there before. Two rows with
// different prior values both end up with the same replacement.
func TestUpdateByFilter_TagsReplace_OverwritesAllRows(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "Cat", "expense")
	t1 := seedTestTransactionWithTags(t, q, user.ID, cat.ID, "2026-04-01", 10.0, "T1", "old1")
	t2 := seedTestTransactionWithTags(t, q, user.ID, cat.ID, "2026-04-02", 10.0, "T2", "old2,old3")

	body := `{"patch":{"tags":"fresh"},"tagsMode":"replace"}`
	rec := postUpdateByFilter(t, h, user, fmt.Sprintf("category_id=%d", cat.ID), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, body: %s", rec.Code, rec.Body.String())
	}

	var got1, got2 string
	db.QueryRow(`SELECT tags FROM transactions WHERE id = ?`, t1.ID).Scan(&got1)
	db.QueryRow(`SELECT tags FROM transactions WHERE id = ?`, t2.ID).Scan(&got2)
	if got1 != "fresh" {
		t.Errorf("t1 tags: %q, want %q", got1, "fresh")
	}
	if got2 != "fresh" {
		t.Errorf("t2 tags: %q, want %q", got2, "fresh")
	}
}

// TestUpdateByFilter_TagsPath_StillWritesOneSummary pins spec §3.6: the
// tags read-then-write loop must NOT emit per-row audit rows; it still
// emits exactly one summary row with transaction_id=BulkAuditTransactionID.
// Three matching rows touched + one summary audit = total 1 audit row.
func TestUpdateByFilter_TagsPath_StillWritesOneSummary(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "Cat", "expense")
	seedTestTransactionWithTags(t, q, user.ID, cat.ID, "2026-04-01", 10.0, "T1", "")
	seedTestTransactionWithTags(t, q, user.ID, cat.ID, "2026-04-02", 10.0, "T2", "")
	seedTestTransactionWithTags(t, q, user.ID, cat.ID, "2026-04-03", 10.0, "T3", "")

	body := `{"patch":{"tags":"fresh"},"tagsMode":"add"}`
	rec := postUpdateByFilter(t, h, user, fmt.Sprintf("category_id=%d", cat.ID), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, body: %s", rec.Code, rec.Body.String())
	}

	var summaries int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM transaction_audit WHERE transaction_id = ? AND action = ?`,
		database.BulkAuditTransactionID, database.AuditUpdate,
	).Scan(&summaries); err != nil {
		t.Fatal(err)
	}
	if summaries != 1 {
		t.Errorf("expected exactly 1 summary audit row, got %d", summaries)
	}

	// Defensive: also assert no per-row audit rows snuck in.
	var perRow int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM transaction_audit WHERE transaction_id != ? AND action = ?`,
		database.BulkAuditTransactionID, database.AuditUpdate,
	).Scan(&perRow); err != nil {
		t.Fatal(err)
	}
	if perRow != 0 {
		t.Errorf("expected 0 per-row update audit rows in tags-filter path, got %d", perRow)
	}
}

// TestUpdateByFilter_Tags_HidesTombstoned uses the canonical CLAUDE.md
// soft-delete sentinel pattern for the tags read-then-write SELECT path:
// seed one live row + one tombstoned row, run tags Add, and assert the
// tombstoned row is not enumerated nor mutated. The sentinel tag value
// "should-not-touch" makes any leak visually loud.
func TestUpdateByFilter_Tags_HidesTombstoned(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "Cat", "expense")
	live := seedTestTransactionWithTags(t, q, user.ID, cat.ID, "2026-04-01", 10.0, "live", "existing")
	ts := seedTestTransactionWithTags(t, q, user.ID, cat.ID, "2026-04-02", 999.0, "ts-sentinel", "should-not-touch")
	if err := q.SoftDeleteTransaction(context.Background(), ts.ID); err != nil {
		t.Fatalf("soft-delete sentinel: %v", err)
	}

	body := `{"patch":{"tags":"fresh"},"tagsMode":"add"}`
	rec := postUpdateByFilter(t, h, user, fmt.Sprintf("category_id=%d", cat.ID), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, body: %s", rec.Code, rec.Body.String())
	}

	var liveTags, tsTags string
	db.QueryRow(`SELECT tags FROM transactions WHERE id = ?`, live.ID).Scan(&liveTags)
	db.QueryRow(`SELECT tags FROM transactions WHERE id = ?`, ts.ID).Scan(&tsTags)
	if liveTags != "existing, fresh" {
		t.Errorf("live row tags: %q, want %q", liveTags, "existing, fresh")
	}
	if tsTags != "should-not-touch" {
		t.Errorf("tombstoned row was touched! tags: %q, want %q (must remain untouched)", tsTags, "should-not-touch")
	}
	// And the row must still be tombstoned (not resurrected).
	var tsDel sql.NullString
	db.QueryRow(`SELECT deleted_at FROM transactions WHERE id = ?`, ts.ID).Scan(&tsDel)
	if !tsDel.Valid {
		t.Errorf("tombstoned row got resurrected: deleted_at is null")
	}
}

// TestUpdateByFilter_TagsAndDate_FiresCheckpointHookOnce pins spec §3.10's
// mutual-exclusivity guarantee: a combined patch (tags Add + date) routes
// through ONLY the tags read-then-write path, lands both updates, and emits
// exactly one summary audit row (not one per branch). The checkpoint hook
// is invoked at most once per request — verified via the audit count, since
// double-fire would couple to a duplicate summary row in this code path.
func TestUpdateByFilter_TagsAndDate_FiresCheckpointHookOnce(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "Cat", "expense")
	txn := seedTestTransactionWithTags(t, q, user.ID, cat.ID, "2026-04-01", 10.0, "T", "old")

	body := `{"patch":{"tags":"fresh","date":"2026-05-01"},"tagsMode":"add"}`
	rec := postUpdateByFilter(t, h, user, fmt.Sprintf("category_id=%d", cat.ID), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, body: %s", rec.Code, rec.Body.String())
	}

	// Both fields must have landed.
	var gotTags, gotDate string
	db.QueryRow(`SELECT tags, DATE(date) FROM transactions WHERE id = ?`, txn.ID).Scan(&gotTags, &gotDate)
	if gotTags != "old, fresh" {
		t.Errorf("tags: %q, want %q", gotTags, "old, fresh")
	}
	if gotDate != "2026-05-01" {
		t.Errorf("date: %q, want %q", gotDate, "2026-05-01")
	}

	// And exactly one summary audit row — proves only the tags branch ran,
	// not both paths.
	var summaries int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM transaction_audit WHERE transaction_id = ? AND action = ?`,
		database.BulkAuditTransactionID, database.AuditUpdate,
	).Scan(&summaries); err != nil {
		t.Fatal(err)
	}
	if summaries != 1 {
		t.Errorf("expected exactly 1 summary audit row (mutual-exclusivity), got %d", summaries)
	}
}

// TestUpdateByFilter_DisallowUnknownFields_RejectsUserIDInjection pins the
// mass-assignment guard from spec §5.5b for the filter handler — same pattern
// as the batch-update test.
func TestUpdateByFilter_DisallowUnknownFields_RejectsUserIDInjection(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "Cat", "expense")
	body := fmt.Sprintf(`{"patch":{"user_id":99,"category_id":%d}}`, cat.ID)
	rec := postUpdateByFilter(t, h, user, "", body)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unknown field; got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestUpdateByFilter_Unauthorized_Returns401(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/update-by-filter",
		strings.NewReader(`{"patch":{"category_id":1}}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.handleUpdateTransactionsByFilter(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// --- Bulk-edit future-proofing regression tests ----------------------------
//
// These tests pin behavior at the boundaries (adversarial input, verbatim
// storage, single-writer concurrency) that future refactors are most likely
// to silently regress. They're not "ought-to-have" coverage — they're proof
// that the spec's stated guarantees still hold.

// TestUpdateByFilter_AdversarialQueryString pins SQL-injection safety on the
// hand-rolled filter UPDATE. buildTransactionWhereClause + appendLiveTransactionsFilter
// build the inner subquery from query params; values flow through `?`
// placeholders. A future "optimization" that string-concats a search value
// would break this test before it breaks production. The payload is the
// classic SQLi probe — the goal isn't to test that SQLite executes it (it
// won't), but to assert the bulk-edit path treats it as a literal search
// term and updates zero rows (no match).
func TestUpdateByFilter_AdversarialQueryString_NoSQLInjection(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "Cat", "expense")
	catB := seedTestCategory(t, q, "CatB", "expense")
	livID := seedTestTransaction(t, q, user.ID, cat.ID, "2026-04-01", 10.0, "innocent description").ID

	// Adversarial search payload — would be catastrophic if interpolated raw.
	adversarial := `'; DROP TABLE transactions; --`
	queryString := "search=" + url.QueryEscape(adversarial)
	body := fmt.Sprintf(`{"patch":{"category_id":%d}}`, catB.ID)
	rec := postUpdateByFilter(t, h, user, queryString, body)

	// The query is treated as a literal LIKE term that matches nothing →
	// updated=0, summary audit row still emitted (per spec §3.6).
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, body: %s", rec.Code, rec.Body.String())
	}
	var resp struct{ Updated int64 }
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Updated != 0 {
		t.Errorf("updated=%d, want 0 (adversarial search must match nothing)", resp.Updated)
	}

	// Critical assertion: the transactions table still exists with rows in it.
	// This is the regression guard — if SQL injection ever lands, this query
	// would fail with "no such table: transactions".
	var n int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM transactions WHERE id = ?`, livID).Scan(&n); err != nil {
		t.Fatalf("transactions table missing — SQL INJECTION REGRESSED: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 row in transactions, got %d", n)
	}
	// And the original row's category is unchanged (it didn't match the filter).
	var cat0 int64
	db.QueryRow(`SELECT category_id FROM transactions WHERE id = ?`, livID).Scan(&cat0)
	if cat0 != cat.ID {
		t.Errorf("untouched row got mutated: cat=%d, want %d (adversarial filter must match nothing)", cat0, cat.ID)
	}
}

// TestBatchUpdate_TagsWithEmbeddedCommaQuote_VerbatimStorage pins spec §3.3:
// tags are stored verbatim, no canonicalization. The split-on-comma parser in
// applyTagsMode is a known sharp edge — embedded commas inside a "quoted" tag
// are NOT handled (we don't have a CSV parser). This test pins that the
// edge-case-but-documented behavior of "embedded commas split" stays stable,
// AND that quotes round-trip verbatim through Replace mode.
//
// If a future refactor ever adds CSV-aware parsing to tags, this test will
// red-flag it so the spec can be updated deliberately rather than silently.
func TestBatchUpdate_TagsWithEmbeddedQuote_VerbatimStorage(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")
	cat := seedTestCategory(t, q, "Cat", "expense")
	txn := seedTestTransaction(t, q, user.ID, cat.ID, "2026-04-01", 10.0, "T")

	// Replace mode stores the value verbatim — no parsing, no escaping.
	// Embedded quotes round-trip 1:1.
	verbatim := `hello "world", with quote`
	body := fmt.Sprintf(`{"ids":[%d],"patch":{"tags":%q},"tagsMode":"replace"}`, txn.ID, verbatim)
	rec := postBatchUpdate(t, h, user, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, body: %s", rec.Code, rec.Body.String())
	}

	var got string
	db.QueryRow(`SELECT tags FROM transactions WHERE id = ?`, txn.ID).Scan(&got)
	if got != verbatim {
		t.Errorf("tags: %q, want %q (verbatim storage broken)", got, verbatim)
	}
}

// TestBatchUpdate_ConcurrentWithBatchDelete_NoStateCorruption was removed:
// it tested SQLite's single-writer + _busy_timeout serialization guarantee
// rather than any application-level invariant. Under CI's slower disk I/O
// the second tx occasionally errored before the busy-timeout window closed,
// causing the handler to 500 — a failure mode of SQLite (or its Go driver)
// under load, not of our code. The application-level invariants this test
// was meant to pin are already covered deterministically:
//
//   - "tombstoned row not mutated by bulk-update" → TestBatchUpdate_HidesTombstoned
//     (sentinel-amount 999) and TestBatchUpdate_PartialSkip_TombstonedAndNonOwnedAreSkipped
//   - "atomicity (no half-state under SQL error)" → TestAudit_BatchUpdate_OnRollback_LeavesNoOrphanRows
//     (FK-violation poison + rollback verification)
//   - "UpdateTx returns ErrTombstoned cleanly" → store_transaction_test.go's
//     TestUpdateTx_TombstonedRow_ReturnsErrTombstoned
//
// If concurrent writer behavior ever needs to be re-asserted, do it at the
// SQLite-driver level, not via parallel HTTP handlers.

// --- Activity notification triggers (Task 6) ---

func TestCreateTransaction_FiresTxnAdded(t *testing.T) {
	q, db := setupTestDB(t)
	rec := &recordingSender{}
	h := NewHandler(q, db)
	h.pushTesterForBudgetAlerts = rec
	user := seedTestUser(t, q, "alice", RoleMember)
	other := seedTestUser(t, q, "bob", RoleMember)
	cat := seedExpenseCat(t, q, "Groceries")
	// Actor's own device must be excluded; a second member's device must receive.
	seedPushSub(t, q, user.ID, "https://push.example/create-actor")
	seedPushSub(t, q, other.ID, "https://push.example/create-other")
	enableNotif(t, q, func(p *database.UpdateNotificationSettingsParams) { p.TxnAdded = true })

	body := strings.NewReader(`{"date":"2026-05-10","amount":12.34,"description":"milk","category_id":` + itoa(cat.ID) + `}`)
	req := httptest.NewRequest(http.MethodPost, "/api/transactions", body)
	req = withUser(req, user)
	rec2 := httptest.NewRecorder()
	h.handleCreateTransaction(rec2, req)

	if rec2.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d; body %s", rec2.Code, rec2.Body.String())
	}
	// Exactly one push — to the other member, never an echo to the actor.
	waitPush(t, h)
	if rec.count() != 1 {
		t.Fatalf("want 1 push (only the other member), got %d", rec.count())
	}
	var p pushAlertPayload
	_ = json.Unmarshal(rec.payloads[0], &p)
	if p.Type != "txn_added" {
		t.Errorf("type: got %q want txn_added", p.Type)
	}
	// Actor-first body: the push goes to OTHER members, so it must lead with
	// the actor's display name (seedTestUser sets DisplayName == username).
	if !strings.HasPrefix(p.Body, "alice ") {
		t.Errorf("body must start with actor display name, got %q", p.Body)
	}
}

// A push send failure must NEVER fail the (already-committed) mutation.
func TestCreateTransaction_SendFailureNeverErrors(t *testing.T) {
	q, db := setupTestDB(t)
	rec := &recordingSender{err: errSendBoom} // every Send returns an error
	h := NewHandler(q, db)
	h.pushTesterForBudgetAlerts = rec
	user := seedTestUser(t, q, "alice", RoleMember)
	cat := seedExpenseCat(t, q, "Groceries")
	seedPushSub(t, q, user.ID, "https://push.example/boom")
	enableNotif(t, q, func(p *database.UpdateNotificationSettingsParams) { p.TxnAdded = true })

	body := strings.NewReader(`{"date":"2026-05-10","amount":12.34,"description":"milk","category_id":` + itoa(cat.ID) + `}`)
	req := httptest.NewRequest(http.MethodPost, "/api/transactions", body)
	req = withUser(req, user)
	rec2 := httptest.NewRecorder()
	h.handleCreateTransaction(rec2, req)

	if rec2.Code != http.StatusCreated {
		t.Fatalf("send failure must not break the mutation: got %d", rec2.Code)
	}
	// Drain before the test's DB cleanup runs: delivery is asynchronous, and a
	// worker still querying a closed handle would log a spurious failure.
	waitPush(t, h)
}

func TestDeleteTransaction_FiresTxnDeleted(t *testing.T) {
	q, db := setupTestDB(t)
	rec := &recordingSender{}
	h := NewHandler(q, db)
	h.pushTesterForBudgetAlerts = rec
	user := seedTestUser(t, q, "alice", RoleMember)
	other := seedTestUser(t, q, "bob", RoleMember)
	cat := seedExpenseCat(t, q, "Groceries")
	// Actor's own device must be excluded; a second member's device must receive.
	seedPushSub(t, q, user.ID, "https://push.example/del-actor")
	seedPushSub(t, q, other.ID, "https://push.example/del-other")
	enableNotif(t, q, func(p *database.UpdateNotificationSettingsParams) { p.TxnDeleted = true })
	txn := seedExpenseRow(t, q, user.ID, cat.ID, "2026-05-10", 1234)

	req := httptest.NewRequest(http.MethodDelete, "/x", nil)
	req = withUserAndURLParam(req, user, "id", itoa(txn.ID))
	rec2 := httptest.NewRecorder()
	h.handleDeleteTransaction(rec2, req)

	if rec2.Code != http.StatusOK {
		t.Fatalf("delete: expected 200, got %d", rec2.Code)
	}
	var p pushAlertPayload
	waitPush(t, h)
	if rec.count() != 1 {
		t.Fatalf("want 1 push (only the other member), got %d", rec.count())
	}
	_ = json.Unmarshal(rec.payloads[0], &p)
	if p.Type != "txn_deleted" {
		t.Errorf("type: got %q want txn_deleted", p.Type)
	}
	if !strings.HasPrefix(p.Body, "alice ") {
		t.Errorf("body must start with actor display name, got %q", p.Body)
	}
}

// A batch create fires exactly ONE aggregate push, never one-per-row.
func TestBatchCreate_FiresSingleAggregatePush(t *testing.T) {
	q, db := setupTestDB(t)
	rec := &recordingSender{}
	h := NewHandler(q, db)
	h.pushTesterForBudgetAlerts = rec
	user := seedTestUser(t, q, "alice", RoleMember)
	other := seedTestUser(t, q, "bob", RoleMember)
	cat := seedExpenseCat(t, q, "Groceries")
	// Actor's own device must be excluded; a second member's device must receive.
	seedPushSub(t, q, user.ID, "https://push.example/batch-actor")
	seedPushSub(t, q, other.ID, "https://push.example/batch-other")
	enableNotif(t, q, func(p *database.UpdateNotificationSettingsParams) { p.TxnAdded = true })

	body := strings.NewReader(`[
		{"date":"2026-05-10","amount":1,"description":"a","category_id":` + itoa(cat.ID) + `},
		{"date":"2026-05-11","amount":2,"description":"b","category_id":` + itoa(cat.ID) + `},
		{"date":"2026-05-12","amount":3,"description":"c","category_id":` + itoa(cat.ID) + `}
	]`)
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/batch", body)
	req = withUser(req, user)
	rec2 := httptest.NewRecorder()
	h.handleBatchCreateTransactions(rec2, req)

	if rec2.Code != http.StatusCreated {
		t.Fatalf("batch create: expected 201, got %d; body %s", rec2.Code, rec2.Body.String())
	}
	// ONE aggregate push, to the other member only — never an echo to the actor.
	waitPush(t, h)
	if rec.count() != 1 {
		t.Fatalf("batch must fire ONE aggregate push (only the other member), got %d", rec.count())
	}
	var p pushAlertPayload
	_ = json.Unmarshal(rec.payloads[0], &p)
	if p.Type != "txn_added" {
		t.Errorf("type: got %q want txn_added", p.Type)
	}
	if !strings.HasPrefix(p.Body, "alice ") {
		t.Errorf("body must start with actor display name, got %q", p.Body)
	}
}

func TestEnumerateFilterCells_HidesTombstoned(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", RoleMember)

	liveCat := seedExpenseCategory(t, q, "Groceries")
	ghostCat := seedExpenseCategory(t, q, "Rent")

	// One live row in liveCat (50.00) and one tombstoned sentinel (999.99) in
	// ghostCat — distinct cells, so a leak is unambiguous.
	seedExpenseRow(t, q, user.ID, liveCat, "2026-05-10", 5000)
	ghost := seedExpenseRow(t, q, user.ID, ghostCat, "2026-05-11", 99900)
	if err := h.txnStore.Delete(context.Background(), user.ID, ghost.ID); err != nil {
		t.Fatalf("tombstone: %v", err)
	}

	cells, err := h.enumerateFilterCells(context.Background(),
		appendLiveTransactionsFilter(""), nil)
	if err != nil {
		t.Fatalf("enumerate: %v", err)
	}
	for _, c := range cells {
		if c.CategoryID == ghostCat {
			t.Fatalf("tombstoned 999 row leaked into cell set: %+v", c)
		}
	}
	want := budgetCell{CategoryID: liveCat, Year: 2026, Month: 5}
	found := false
	for _, c := range cells {
		if c == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("live cell %+v missing from %+v", want, cells)
	}
}

func TestHandleDeleteTransactionsByFilter_ClearsOverBudgetLatch(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")
	catID := seedExpenseCategory(t, q, "Groceries")

	// Budget 100.00; two rows summing to 120.00 -> over.
	if err := q.UpsertCategoryBudget(context.Background(), database.UpsertCategoryBudgetParams{
		Year: 2026, Month: 5, CategoryID: catID, AmountCents: 10000,
	}); err != nil {
		t.Fatalf("budget: %v", err)
	}
	seedExpenseRow(t, q, admin.ID, catID, "2026-05-10", 6000)
	seedExpenseRow(t, q, admin.ID, catID, "2026-05-12", 6000)

	// Pre-trip the latch as if a fresh cross already alerted.
	res, err := q.SetBudgetAlertState(context.Background(), database.SetBudgetAlertStateParams{
		CategoryID: catID, Year: 2026, Month: 5,
	})
	if err != nil {
		t.Fatalf("set latch: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("expected latch newly set, rows-affected=%d", n)
	}

	// Filter-delete every row in this category.
	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/transactions/delete-by-filter?category_id=%d", catID), nil)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()
	h.handleDeleteTransactionsByFilter(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	// Spend dropped to 0 < limit -> the post-commit re-eval must have cleared
	// the latch, so a follow-up clear removes 0 rows.
	cleared, err := q.ClearBudgetAlertState(context.Background(), database.ClearBudgetAlertStateParams{
		CategoryID: catID, Year: 2026, Month: 5,
	})
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if n, _ := cleared.RowsAffected(); n != 0 {
		t.Fatalf("latch should have been cleared by delete-by-filter re-eval; got %d rows still set", n)
	}
}

func cellsContain(cells []budgetCell, want budgetCell) bool {
	for _, c := range cells {
		if c == want {
			return true
		}
	}
	return false
}

func TestFilterUpdateCells_CategorySwapUnionsOldAndNew(t *testing.T) {
	old := []budgetCell{{CategoryID: 1, Year: 2026, Month: 5}}
	newCat := int64(2)
	got := filterUpdateCells(old, database.UpdatePatch{CategoryID: &newCat})

	if !cellsContain(got, budgetCell{CategoryID: 1, Year: 2026, Month: 5}) ||
		!cellsContain(got, budgetCell{CategoryID: 2, Year: 2026, Month: 5}) {
		t.Fatalf("want old cat 1 AND new cat 2, got %+v", got)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 deduped cells, got %d: %+v", len(got), got)
	}
}

func TestFilterUpdateCells_DateSwapMovesMonth(t *testing.T) {
	old := []budgetCell{{CategoryID: 1, Year: 2026, Month: 5}}
	d := "2026-06-15"
	got := filterUpdateCells(old, database.UpdatePatch{Date: &d})

	if !cellsContain(got, budgetCell{CategoryID: 1, Year: 2026, Month: 6}) {
		t.Fatalf("date swap must add the June cell; got %+v", got)
	}
	if !cellsContain(got, budgetCell{CategoryID: 1, Year: 2026, Month: 5}) {
		t.Fatalf("old May cell must be retained; got %+v", got)
	}
}

func TestFilterUpdateCells_DescriptionOnly_ReturnsOldOnly(t *testing.T) {
	old := []budgetCell{{CategoryID: 1, Year: 2026, Month: 5}}
	desc := "renamed"
	got := filterUpdateCells(old, database.UpdatePatch{Description: &desc})

	if len(got) != 1 || got[0] != old[0] {
		t.Fatalf("description-only patch must not add cells; got %+v", got)
	}
}

func TestHandleUpdateTransactionsByFilter_MoveOutClearsOldLatch(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	admin := seedTestUser(t, q, "admin", "admin")
	overCat := seedExpenseCategory(t, q, "Dining")
	otherCat := seedExpenseCategory(t, q, "Misc")

	// overCat: budget 100.00, two rows summing to 120.00 -> over + latched.
	if err := q.UpsertCategoryBudget(context.Background(), database.UpsertCategoryBudgetParams{
		Year: 2026, Month: 5, CategoryID: overCat, AmountCents: 10000,
	}); err != nil {
		t.Fatalf("budget: %v", err)
	}
	seedExpenseRow(t, q, admin.ID, overCat, "2026-05-10", 6000)
	seedExpenseRow(t, q, admin.ID, overCat, "2026-05-12", 6000)
	res, err := q.SetBudgetAlertState(context.Background(), database.SetBudgetAlertStateParams{
		CategoryID: overCat, Year: 2026, Month: 5,
	})
	if err != nil {
		t.Fatalf("set latch: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("expected latch newly set, rows-affected=%d", n)
	}

	// Move every Dining row into Misc via update-by-filter.
	body := fmt.Sprintf(`{"patch":{"category_id":%d}}`, otherCat)
	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/transactions/update-by-filter?category_id=%d", overCat),
		strings.NewReader(body))
	req = withUser(req, admin)
	rec := httptest.NewRecorder()
	h.handleUpdateTransactionsByFilter(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	// overCat now has 0 spend < 100 limit -> its latch must have re-armed.
	cleared, err := q.ClearBudgetAlertState(context.Background(), database.ClearBudgetAlertStateParams{
		CategoryID: overCat, Year: 2026, Month: 5,
	})
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if n, _ := cleared.RowsAffected(); n != 0 {
		t.Fatalf("old category latch should have cleared after move-out; got %d still set", n)
	}
}
