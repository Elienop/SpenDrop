package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/database"
)

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

// seedTestTransaction creates a transaction directly via sqlc.
func seedTestTransaction(t *testing.T, q *database.Queries, userID, categoryID int64, date string, amount float64, desc string) database.Transaction {
	t.Helper()
	d, _ := time.Parse("2006-01-02", date)
	txn, err := q.CreateTransaction(context.Background(), database.CreateTransactionParams{
		UserID:      userID,
		Date:        d,
		Amount:      amount,
		Description: desc,
		CategoryID:  categoryID,
	})
	if err != nil {
		t.Fatalf("seed transaction: %v", err)
	}
	return txn
}

// seedTestTransactionWithTags creates a transaction with tags set.
func seedTestTransactionWithTags(t *testing.T, q *database.Queries, userID, categoryID int64, date string, amount float64, desc, tags string) database.Transaction {
	t.Helper()
	d, _ := time.Parse("2006-01-02", date)
	txn, err := q.CreateTransaction(context.Background(), database.CreateTransactionParams{
		UserID:      userID,
		Date:        d,
		Amount:      amount,
		Description: desc,
		CategoryID:  categoryID,
		Tags:        sql.NullString{String: tags, Valid: tags != ""},
	})
	if err != nil {
		t.Fatalf("seed transaction with tags: %v", err)
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

	// Verify it's actually deleted
	_, err := q.GetTransactionByID(context.Background(), txn.ID)
	if err != sql.ErrNoRows {
		t.Errorf("expected transaction to be deleted, got err: %v", err)
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

// --- handleBulkRename ---

func TestHandleBulkRename_RenamesMatchingTransactions(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	seedTestTransaction(t, q, user.ID, 1, "2026-04-01", 10.0, "mr brown coffee")
	seedTestTransaction(t, q, user.ID, 1, "2026-04-02", 20.0, "mr brown bakery")
	seedTestTransaction(t, q, user.ID, 1, "2026-04-03", 30.0, "starbucks")

	body := strings.NewReader(`{"search": "mr brown", "new_description": "MR BROWN"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/transactions/bulk-rename", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleBulkRename(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	if resp["updated"] != float64(2) {
		t.Errorf("expected updated=2, got %v", resp["updated"])
	}

	// Verify the descriptions were actually changed in the DB
	listReq := httptest.NewRequest(http.MethodGet, "/api/transactions?search=MR+BROWN", nil)
	listReq = withUser(listReq, user)
	listRec := httptest.NewRecorder()
	h.handleListTransactions(listRec, listReq)

	var listResp transactionListResponse
	decodeResponse(t, listRec, &listResp)
	if listResp.Total != 2 {
		t.Errorf("expected 2 renamed transactions, got %d", listResp.Total)
	}
	for _, txn := range listResp.Transactions {
		if txn.Description != "MR BROWN" {
			t.Errorf("expected description 'MR BROWN', got %q", txn.Description)
		}
	}
}

func TestHandleBulkRename_CaseInsensitiveSearch(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	seedTestTransaction(t, q, user.ID, 1, "2026-04-01", 10.0, "MR BROWN COFFEE")
	seedTestTransaction(t, q, user.ID, 1, "2026-04-02", 20.0, "Mr Brown Bakery")

	body := strings.NewReader(`{"search": "mr brown", "new_description": "Mr. Brown"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/transactions/bulk-rename", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleBulkRename(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	if resp["updated"] != float64(2) {
		t.Errorf("expected updated=2 (case-insensitive), got %v", resp["updated"])
	}
}

func TestHandleBulkRename_NoMatches_ReturnsZero(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	seedTestTransaction(t, q, user.ID, 1, "2026-04-01", 10.0, "starbucks")

	body := strings.NewReader(`{"search": "mr brown", "new_description": "MR BROWN"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/transactions/bulk-rename", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleBulkRename(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	if resp["updated"] != float64(0) {
		t.Errorf("expected updated=0, got %v", resp["updated"])
	}
}

func TestHandleBulkRename_EmptySearch_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	body := strings.NewReader(`{"search": "", "new_description": "MR BROWN"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/transactions/bulk-rename", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleBulkRename(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleBulkRename_EmptyNewDescription_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	body := strings.NewReader(`{"search": "mr brown", "new_description": ""}`)
	req := httptest.NewRequest(http.MethodPut, "/api/transactions/bulk-rename", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleBulkRename(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleBulkRename_NewDescriptionTooLong_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	longDesc := strings.Repeat("x", 501)
	body := strings.NewReader(`{"search": "mr brown", "new_description": "` + longDesc + `"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/transactions/bulk-rename", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleBulkRename(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleBulkRename_InvalidJSON_Returns400(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	body := strings.NewReader(`not json`)
	req := httptest.NewRequest(http.MethodPut, "/api/transactions/bulk-rename", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleBulkRename(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleBulkRename_NoAuth_Returns401(t *testing.T) {
	h := setupHandler(t)

	body := strings.NewReader(`{"search": "mr brown", "new_description": "MR BROWN"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/transactions/bulk-rename", body)
	rec := httptest.NewRecorder()

	h.handleBulkRename(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestHandleBulkRename_MemberOnlyRenamesOwnTransactions(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	alice := seedTestUser(t, q, "alice", "member")
	bob := seedTestUser(t, q, "bob", "member")

	// Both users have "mr brown" transactions
	seedTestTransaction(t, q, alice.ID, 1, "2026-04-01", 10.0, "mr brown coffee")
	seedTestTransaction(t, q, bob.ID, 1, "2026-04-02", 20.0, "mr brown bakery")

	// Alice renames — should only affect her own transaction
	body := strings.NewReader(`{"search": "mr brown", "new_description": "MR BROWN"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/transactions/bulk-rename", body)
	req = withUser(req, alice)
	rec := httptest.NewRecorder()

	h.handleBulkRename(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	if resp["updated"] != float64(1) {
		t.Errorf("expected updated=1 (only alice's), got %v", resp["updated"])
	}

	// Verify bob's transaction is unchanged
	bobTxn, err := q.GetTransactionByID(context.Background(), 2) // bob's txn
	if err != nil {
		t.Fatalf("get bob txn: %v", err)
	}
	if bobTxn.Description != "mr brown bakery" {
		t.Errorf("expected bob's transaction unchanged, got %q", bobTxn.Description)
	}
}

func TestHandleBulkRename_AdminRenamesAllTransactions(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	alice := seedTestUser(t, q, "alice", "member")
	admin := seedTestUser(t, q, "admin", "admin")

	seedTestTransaction(t, q, alice.ID, 1, "2026-04-01", 10.0, "mr brown coffee")
	seedTestTransaction(t, q, admin.ID, 1, "2026-04-02", 20.0, "mr brown bakery")

	// Admin renames — should affect all transactions
	body := strings.NewReader(`{"search": "mr brown", "new_description": "MR BROWN"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/transactions/bulk-rename", body)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()

	h.handleBulkRename(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	if resp["updated"] != float64(2) {
		t.Errorf("expected updated=2 (admin renames all), got %v", resp["updated"])
	}
}

func TestHandleBulkRename_EscapesSQLWildcards(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	// Create transactions with SQL LIKE wildcards in the description
	seedTestTransaction(t, q, user.ID, 1, "2026-04-01", 10.0, "100% discount store")
	seedTestTransaction(t, q, user.ID, 1, "2026-04-02", 20.0, "100 percent store")

	// Searching for "100%" should only match the first one (% is escaped)
	body := strings.NewReader(`{"search": "100%", "new_description": "HUNDRED PERCENT"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/transactions/bulk-rename", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleBulkRename(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	decodeResponse(t, rec, &resp)
	if resp["updated"] != float64(1) {
		t.Errorf("expected updated=1 (only exact '100%%' match), got %v", resp["updated"])
	}
}

func TestHandleBulkRename_UpdatesTimestamp(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	txn := seedTestTransaction(t, q, user.ID, 1, "2026-04-01", 10.0, "mr brown")
	origUpdatedAt := txn.UpdatedAt

	// SQLite CURRENT_TIMESTAMP has second-level precision
	time.Sleep(1100 * time.Millisecond)

	body := strings.NewReader(`{"search": "mr brown", "new_description": "MR BROWN"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/transactions/bulk-rename", body)
	req = withUser(req, user)
	rec := httptest.NewRecorder()

	h.handleBulkRename(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	// Verify updated_at changed
	updated, err := q.GetTransactionByID(context.Background(), txn.ID)
	if err != nil {
		t.Fatalf("get transaction: %v", err)
	}
	if !updated.UpdatedAt.After(origUpdatedAt) {
		t.Errorf("expected updated_at to advance; before=%v, after=%v", origUpdatedAt, updated.UpdatedAt)
	}
}
