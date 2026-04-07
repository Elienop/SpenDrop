package database

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// setupTestDB creates an in-memory SQLite database with migrations applied
// and returns a Queries instance for testing.
func setupTestDB(t *testing.T) (*Queries, *sql.DB) {
	t.Helper()
	db := openTestDB(t)
	if err := RunMigrations(db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return New(db), db
}

func TestCreateUser_And_GetUserByID(t *testing.T) {
	q, _ := setupTestDB(t)
	ctx := context.Background()

	user, err := q.CreateUser(ctx, CreateUserParams{
		Username:     "alice",
		PasswordHash: "$2a$10$fakehash",
		DisplayName:  "Alice",
		Role:         "admin",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if user.Username != "alice" {
		t.Errorf("expected username 'alice', got %q", user.Username)
	}
	if user.DisplayName != "Alice" {
		t.Errorf("expected display_name 'Alice', got %q", user.DisplayName)
	}
	if user.Role != "admin" {
		t.Errorf("expected role 'admin', got %q", user.Role)
	}

	got, err := q.GetUserByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got.ID != user.ID {
		t.Errorf("expected id %d, got %d", user.ID, got.ID)
	}
	if got.Username != "alice" {
		t.Errorf("expected username 'alice', got %q", got.Username)
	}
}

func TestGetUserByUsername(t *testing.T) {
	q, _ := setupTestDB(t)
	ctx := context.Background()

	_, err := q.CreateUser(ctx, CreateUserParams{
		Username:     "bob",
		PasswordHash: "$2a$10$fakehash",
		DisplayName:  "Bob",
		Role:         "member",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	got, err := q.GetUserByUsername(ctx, "bob")
	if err != nil {
		t.Fatalf("GetUserByUsername: %v", err)
	}
	if got.Username != "bob" {
		t.Errorf("expected username 'bob', got %q", got.Username)
	}
}

func TestListUsers(t *testing.T) {
	q, _ := setupTestDB(t)
	ctx := context.Background()

	for _, name := range []string{"user1", "user2", "user3"} {
		_, err := q.CreateUser(ctx, CreateUserParams{
			Username:     name,
			PasswordHash: "$2a$10$fakehash",
			DisplayName:  name,
			Role:         "member",
		})
		if err != nil {
			t.Fatalf("CreateUser(%s): %v", name, err)
		}
	}

	users, err := q.ListUsers(ctx)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 3 {
		t.Errorf("expected 3 users, got %d", len(users))
	}
}

func TestUpdateUser(t *testing.T) {
	q, _ := setupTestDB(t)
	ctx := context.Background()

	user, err := q.CreateUser(ctx, CreateUserParams{
		Username:     "carol",
		PasswordHash: "$2a$10$fakehash",
		DisplayName:  "Carol",
		Role:         "member",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	err = q.UpdateUser(ctx, UpdateUserParams{
		ID:          user.ID,
		DisplayName: "Carolina",
		Role:        "admin",
	})
	if err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}

	got, err := q.GetUserByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got.DisplayName != "Carolina" {
		t.Errorf("expected display_name 'Carolina', got %q", got.DisplayName)
	}
	if got.Role != "admin" {
		t.Errorf("expected role 'admin', got %q", got.Role)
	}
}

func TestDeleteUser(t *testing.T) {
	q, _ := setupTestDB(t)
	ctx := context.Background()

	user, err := q.CreateUser(ctx, CreateUserParams{
		Username:     "dave",
		PasswordHash: "$2a$10$fakehash",
		DisplayName:  "Dave",
		Role:         "member",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	_, err = q.DeleteUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	_, err = q.GetUserByID(ctx, user.ID)
	if err != sql.ErrNoRows {
		t.Errorf("expected ErrNoRows after delete, got %v", err)
	}
}

func TestSessions(t *testing.T) {
	q, _ := setupTestDB(t)
	ctx := context.Background()

	user, err := q.CreateUser(ctx, CreateUserParams{
		Username:     "eve",
		PasswordHash: "$2a$10$fakehash",
		DisplayName:  "Eve",
		Role:         "member",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	expiresAt := time.Now().Add(24 * time.Hour).UTC()

	err = q.CreateSession(ctx, CreateSessionParams{
		Token:     "tok-123",
		UserID:    user.ID,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	sess, err := q.GetSession(ctx, "tok-123")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess.UserID != user.ID {
		t.Errorf("expected user_id %d, got %d", user.ID, sess.UserID)
	}
	if sess.Token != "tok-123" {
		t.Errorf("expected token 'tok-123', got %q", sess.Token)
	}

	err = q.DeleteSession(ctx, "tok-123")
	if err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	_, err = q.GetSession(ctx, "tok-123")
	if err != sql.ErrNoRows {
		t.Errorf("expected ErrNoRows after delete, got %v", err)
	}
}

func TestDeleteExpiredSessions(t *testing.T) {
	q, _ := setupTestDB(t)
	ctx := context.Background()

	user, err := q.CreateUser(ctx, CreateUserParams{
		Username:     "frank",
		PasswordHash: "$2a$10$fakehash",
		DisplayName:  "Frank",
		Role:         "member",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Expired session (year 2000)
	expiredAt := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	err = q.CreateSession(ctx, CreateSessionParams{
		Token:     "expired-tok",
		UserID:    user.ID,
		ExpiresAt: expiredAt,
	})
	if err != nil {
		t.Fatalf("CreateSession (expired): %v", err)
	}

	// Valid session (year 2099)
	validAt := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	err = q.CreateSession(ctx, CreateSessionParams{
		Token:     "valid-tok",
		UserID:    user.ID,
		ExpiresAt: validAt,
	})
	if err != nil {
		t.Fatalf("CreateSession (valid): %v", err)
	}

	err = q.DeleteExpiredSessions(ctx)
	if err != nil {
		t.Fatalf("DeleteExpiredSessions: %v", err)
	}

	// Expired should be gone
	_, err = q.GetSession(ctx, "expired-tok")
	if err != sql.ErrNoRows {
		t.Errorf("expected expired session to be deleted, got %v", err)
	}

	// Valid should still exist
	_, err = q.GetSession(ctx, "valid-tok")
	if err != nil {
		t.Errorf("expected valid session to still exist: %v", err)
	}
}

func TestCategories(t *testing.T) {
	q, _ := setupTestDB(t)
	ctx := context.Background()

	cat, err := q.CreateCategory(ctx, CreateCategoryParams{
		Name:      "TestCat",
		Type:      "expense",
		Color:     "#ff0000",
		SortOrder: 99,
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	if cat.Name != "TestCat" {
		t.Errorf("expected name 'TestCat', got %q", cat.Name)
	}

	got, err := q.GetCategoryByID(ctx, cat.ID)
	if err != nil {
		t.Fatalf("GetCategoryByID: %v", err)
	}
	if got.ID != cat.ID {
		t.Errorf("expected id %d, got %d", cat.ID, got.ID)
	}

	// ListActiveCategories should include seeded + new category
	active, err := q.ListActiveCategories(ctx)
	if err != nil {
		t.Fatalf("ListActiveCategories: %v", err)
	}
	// 19 seeded + 1 new = 20
	if len(active) != 20 {
		t.Errorf("expected 20 active categories, got %d", len(active))
	}

	// Deactivate our category
	_, err = q.UpdateCategoryActive(ctx, UpdateCategoryActiveParams{
		ID:       cat.ID,
		IsActive: false,
	})
	if err != nil {
		t.Fatalf("UpdateCategoryActive: %v", err)
	}

	active2, err := q.ListActiveCategories(ctx)
	if err != nil {
		t.Fatalf("ListActiveCategories after deactivate: %v", err)
	}
	if len(active2) != 19 {
		t.Errorf("expected 19 active categories after deactivation, got %d", len(active2))
	}

	// ListAllCategories still has 20
	all, err := q.ListAllCategories(ctx)
	if err != nil {
		t.Fatalf("ListAllCategories: %v", err)
	}
	if len(all) != 20 {
		t.Errorf("expected 20 total categories, got %d", len(all))
	}

	// Update category
	_, err = q.UpdateCategory(ctx, UpdateCategoryParams{
		ID:    cat.ID,
		Name:  "UpdatedCat",
		Color: "#00ff00",
	})
	if err != nil {
		t.Fatalf("UpdateCategory: %v", err)
	}

	updated, err := q.GetCategoryByID(ctx, cat.ID)
	if err != nil {
		t.Fatalf("GetCategoryByID after update: %v", err)
	}
	if updated.Name != "UpdatedCat" {
		t.Errorf("expected name 'UpdatedCat', got %q", updated.Name)
	}

	// Update sort order
	err = q.UpdateCategorySortOrder(ctx, UpdateCategorySortOrderParams{
		ID:        cat.ID,
		SortOrder: 42,
	})
	if err != nil {
		t.Fatalf("UpdateCategorySortOrder: %v", err)
	}
}

func TestCurrencies(t *testing.T) {
	q, _ := setupTestDB(t)
	ctx := context.Background()

	currencies, err := q.ListCurrencies(ctx)
	if err != nil {
		t.Fatalf("ListCurrencies: %v", err)
	}
	if len(currencies) != 3 {
		t.Errorf("expected 3 seeded currencies, got %d", len(currencies))
	}

	usd, err := q.GetCurrency(ctx, "USD")
	if err != nil {
		t.Fatalf("GetCurrency: %v", err)
	}
	if usd.Name != "US Dollar" {
		t.Errorf("expected 'US Dollar', got %q", usd.Name)
	}

	// Upsert a new currency
	err = q.UpsertCurrency(ctx, UpsertCurrencyParams{
		Code:       "GBP",
		Name:       "British Pound",
		Symbol:     "£",
		RateToBase: 0.79,
		IsBase:     false,
	})
	if err != nil {
		t.Fatalf("UpsertCurrency: %v", err)
	}

	currencies2, err := q.ListCurrencies(ctx)
	if err != nil {
		t.Fatalf("ListCurrencies after upsert: %v", err)
	}
	if len(currencies2) != 4 {
		t.Errorf("expected 4 currencies after upsert, got %d", len(currencies2))
	}
}

func TestTransactions(t *testing.T) {
	q, _ := setupTestDB(t)
	ctx := context.Background()

	user, err := q.CreateUser(ctx, CreateUserParams{
		Username:     "spender",
		PasswordHash: "$2a$10$fakehash",
		DisplayName:  "Spender",
		Role:         "member",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	txnDate := time.Date(2026, 4, 6, 0, 0, 0, 0, time.UTC)

	// Use the first seeded expense category (Food, id=1)
	txn, err := q.CreateTransaction(ctx, CreateTransactionParams{
		UserID:      user.ID,
		Date:        txnDate,
		Amount:      42.50,
		Description: "Groceries",
		CategoryID:  1,
	})
	if err != nil {
		t.Fatalf("CreateTransaction: %v", err)
	}
	if txn.Amount != 42.50 {
		t.Errorf("expected amount 42.50, got %f", txn.Amount)
	}

	got, err := q.GetTransactionByID(ctx, txn.ID)
	if err != nil {
		t.Fatalf("GetTransactionByID: %v", err)
	}
	if got.Description != "Groceries" {
		t.Errorf("expected description 'Groceries', got %q", got.Description)
	}
	if got.CategoryType != "expense" {
		t.Errorf("expected category_type 'expense', got %q", got.CategoryType)
	}

	// Update transaction
	updatedDate := time.Date(2026, 4, 7, 0, 0, 0, 0, time.UTC)
	err = q.UpdateTransaction(ctx, UpdateTransactionParams{
		ID:          txn.ID,
		Date:        updatedDate,
		Amount:      50.00,
		Description: "Updated groceries",
		CategoryID:  1,
	})
	if err != nil {
		t.Fatalf("UpdateTransaction: %v", err)
	}

	updated, err := q.GetTransactionByID(ctx, txn.ID)
	if err != nil {
		t.Fatalf("GetTransactionByID after update: %v", err)
	}
	if updated.Amount != 50.00 {
		t.Errorf("expected amount 50.00, got %f", updated.Amount)
	}

	// Delete transaction
	err = q.DeleteTransaction(ctx, txn.ID)
	if err != nil {
		t.Fatalf("DeleteTransaction: %v", err)
	}

	_, err = q.GetTransactionByID(ctx, txn.ID)
	if err != sql.ErrNoRows {
		t.Errorf("expected ErrNoRows after delete, got %v", err)
	}
}

func TestBudgets(t *testing.T) {
	q, _ := setupTestDB(t)
	ctx := context.Background()

	err := q.UpsertBudget(ctx, UpsertBudgetParams{
		Year:   2026,
		Month:  4,
		Amount: 3000.00,
	})
	if err != nil {
		t.Fatalf("UpsertBudget: %v", err)
	}

	budget, err := q.GetBudget(ctx, GetBudgetParams{
		Year:  2026,
		Month: 4,
	})
	if err != nil {
		t.Fatalf("GetBudget: %v", err)
	}
	if budget.Amount != 3000.00 {
		t.Errorf("expected amount 3000.00, got %f", budget.Amount)
	}

	// Upsert updates existing
	err = q.UpsertBudget(ctx, UpsertBudgetParams{
		Year:   2026,
		Month:  4,
		Amount: 3500.00,
	})
	if err != nil {
		t.Fatalf("UpsertBudget (update): %v", err)
	}

	budget2, err := q.GetBudget(ctx, GetBudgetParams{
		Year:  2026,
		Month: 4,
	})
	if err != nil {
		t.Fatalf("GetBudget after upsert: %v", err)
	}
	if budget2.Amount != 3500.00 {
		t.Errorf("expected updated amount 3500.00, got %f", budget2.Amount)
	}

	// ListBudgetsByYear
	err = q.UpsertBudget(ctx, UpsertBudgetParams{
		Year:   2026,
		Month:  5,
		Amount: 2800.00,
	})
	if err != nil {
		t.Fatalf("UpsertBudget (may): %v", err)
	}

	budgets, err := q.ListBudgetsByYear(ctx, 2026)
	if err != nil {
		t.Fatalf("ListBudgetsByYear: %v", err)
	}
	if len(budgets) != 2 {
		t.Errorf("expected 2 budgets for 2026, got %d", len(budgets))
	}
}

func TestSavingsGoals(t *testing.T) {
	q, _ := setupTestDB(t)
	ctx := context.Background()

	err := q.UpsertSavingsGoal(ctx, UpsertSavingsGoalParams{
		Year:         2026,
		TargetAmount: 10000.00,
	})
	if err != nil {
		t.Fatalf("UpsertSavingsGoal: %v", err)
	}

	goal, err := q.GetSavingsGoal(ctx, 2026)
	if err != nil {
		t.Fatalf("GetSavingsGoal: %v", err)
	}
	if goal.TargetAmount != 10000.00 {
		t.Errorf("expected target 10000.00, got %f", goal.TargetAmount)
	}

	goals, err := q.ListSavingsGoals(ctx)
	if err != nil {
		t.Fatalf("ListSavingsGoals: %v", err)
	}
	if len(goals) != 1 {
		t.Errorf("expected 1 savings goal, got %d", len(goals))
	}
}

func TestAppSettings(t *testing.T) {
	q, _ := setupTestDB(t)
	ctx := context.Background()

	// Seeded settings should exist
	setting, err := q.GetSetting(ctx, "base_currency")
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if setting.Value != "USD" {
		t.Errorf("expected 'USD', got %q", setting.Value)
	}

	// Upsert new setting
	err = q.UpsertSetting(ctx, UpsertSettingParams{
		Key:   "theme",
		Value: "dark",
	})
	if err != nil {
		t.Fatalf("UpsertSetting: %v", err)
	}

	theme, err := q.GetSetting(ctx, "theme")
	if err != nil {
		t.Fatalf("GetSetting (theme): %v", err)
	}
	if theme.Value != "dark" {
		t.Errorf("expected 'dark', got %q", theme.Value)
	}

	// Upsert overwrites
	err = q.UpsertSetting(ctx, UpsertSettingParams{
		Key:   "theme",
		Value: "light",
	})
	if err != nil {
		t.Fatalf("UpsertSetting (overwrite): %v", err)
	}

	theme2, err := q.GetSetting(ctx, "theme")
	if err != nil {
		t.Fatalf("GetSetting (theme overwrite): %v", err)
	}
	if theme2.Value != "light" {
		t.Errorf("expected 'light', got %q", theme2.Value)
	}
}

func TestDashboardAggregations(t *testing.T) {
	q, _ := setupTestDB(t)
	ctx := context.Background()

	user, err := q.CreateUser(ctx, CreateUserParams{
		Username:     "dash",
		PasswordHash: "$2a$10$fakehash",
		DisplayName:  "Dashboard User",
		Role:         "member",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Create some expenses (category 1 = Food/expense, category 2 = Gifts/expense)
	_, err = q.CreateTransaction(ctx, CreateTransactionParams{
		UserID:      user.ID,
		Date:        time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		Amount:      100.00,
		Description: "Groceries",
		CategoryID:  1, // Food
	})
	if err != nil {
		t.Fatalf("CreateTransaction (groceries): %v", err)
	}

	_, err = q.CreateTransaction(ctx, CreateTransactionParams{
		UserID:      user.ID,
		Date:        time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC),
		Amount:      50.00,
		Description: "Birthday gift",
		CategoryID:  2, // Gifts
	})
	if err != nil {
		t.Fatalf("CreateTransaction (gift): %v", err)
	}

	// Create income (category 15 = Paycheck/income)
	_, err = q.CreateTransaction(ctx, CreateTransactionParams{
		UserID:      user.ID,
		Date:        time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		Amount:      3000.00,
		Description: "April salary",
		CategoryID:  15, // Paycheck
	})
	if err != nil {
		t.Fatalf("CreateTransaction (salary): %v", err)
	}

	// SumExpensesByMonth
	expenseSum, err := q.SumExpensesByMonth(ctx, SumExpensesByMonthParams{
		Year:  "2026",
		Month: "04",
	})
	if err != nil {
		t.Fatalf("SumExpensesByMonth: %v", err)
	}
	if expenseSum != 150.00 {
		t.Errorf("expected expense sum 150.00, got %v", expenseSum)
	}

	// SumIncomeByMonth
	incomeSum, err := q.SumIncomeByMonth(ctx, SumIncomeByMonthParams{
		Year:  "2026",
		Month: "04",
	})
	if err != nil {
		t.Fatalf("SumIncomeByMonth: %v", err)
	}
	if incomeSum != 3000.00 {
		t.Errorf("expected income sum 3000.00, got %v", incomeSum)
	}

	// SumByCategoryForMonth
	byCat, err := q.SumByCategoryForMonth(ctx, SumByCategoryForMonthParams{
		Year:  "2026",
		Month: "04",
	})
	if err != nil {
		t.Fatalf("SumByCategoryForMonth: %v", err)
	}
	if len(byCat) != 2 {
		t.Errorf("expected 2 expense categories with transactions, got %d", len(byCat))
	}
	// Results are ordered by total DESC, so Food (100) should be first
	if len(byCat) >= 1 && byCat[0].Total != 100.00 {
		t.Errorf("expected first category total 100.00, got %v", byCat[0].Total)
	}
}

func TestSavedFilters(t *testing.T) {
	q, _ := setupTestDB(t)
	ctx := context.Background()

	user, err := q.CreateUser(ctx, CreateUserParams{
		Username:     "filterer",
		PasswordHash: "$2a$10$fakehash",
		DisplayName:  "Filterer",
		Role:         "member",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Create a saved filter
	filter, err := q.CreateSavedFilter(ctx, CreateSavedFilterParams{
		UserID:     user.ID,
		Name:       "Last 30 days",
		FilterJson: `{"days":30}`,
	})
	if err != nil {
		t.Fatalf("CreateSavedFilter: %v", err)
	}
	if filter.Name != "Last 30 days" {
		t.Errorf("expected name 'Last 30 days', got %q", filter.Name)
	}
	if filter.FilterJson != `{"days":30}` {
		t.Errorf("expected filter_json '{\"days\":30}', got %q", filter.FilterJson)
	}
	if filter.UserID != user.ID {
		t.Errorf("expected user_id %d, got %d", user.ID, filter.UserID)
	}

	// Create a second filter
	_, err = q.CreateSavedFilter(ctx, CreateSavedFilterParams{
		UserID:     user.ID,
		Name:       "Expenses only",
		FilterJson: `{"type":"expense"}`,
	})
	if err != nil {
		t.Fatalf("CreateSavedFilter (second): %v", err)
	}

	// List filters for user (should be ordered by name)
	filters, err := q.ListSavedFilters(ctx, user.ID)
	if err != nil {
		t.Fatalf("ListSavedFilters: %v", err)
	}
	if len(filters) != 2 {
		t.Fatalf("expected 2 filters, got %d", len(filters))
	}
	// Alphabetical: "Expenses only" < "Last 30 days"
	if filters[0].Name != "Expenses only" {
		t.Errorf("expected first filter 'Expenses only', got %q", filters[0].Name)
	}
	if filters[1].Name != "Last 30 days" {
		t.Errorf("expected second filter 'Last 30 days', got %q", filters[1].Name)
	}

	// Update filter
	_, err = q.UpdateSavedFilter(ctx, UpdateSavedFilterParams{
		Name:       "Last 7 days",
		FilterJson: `{"days":7}`,
		ID:         filter.ID,
		UserID:     user.ID,
	})
	if err != nil {
		t.Fatalf("UpdateSavedFilter: %v", err)
	}

	// Verify update via list
	updated, err := q.ListSavedFilters(ctx, user.ID)
	if err != nil {
		t.Fatalf("ListSavedFilters after update: %v", err)
	}
	found := false
	for _, f := range updated {
		if f.ID == filter.ID {
			found = true
			if f.Name != "Last 7 days" {
				t.Errorf("expected updated name 'Last 7 days', got %q", f.Name)
			}
			if f.FilterJson != `{"days":7}` {
				t.Errorf("expected updated filter_json, got %q", f.FilterJson)
			}
		}
	}
	if !found {
		t.Error("updated filter not found in list")
	}

	// Delete filter
	_, err = q.DeleteSavedFilter(ctx, DeleteSavedFilterParams{
		ID:     filter.ID,
		UserID: user.ID,
	})
	if err != nil {
		t.Fatalf("DeleteSavedFilter: %v", err)
	}

	// Verify delete
	remaining, err := q.ListSavedFilters(ctx, user.ID)
	if err != nil {
		t.Fatalf("ListSavedFilters after delete: %v", err)
	}
	if len(remaining) != 1 {
		t.Errorf("expected 1 filter after delete, got %d", len(remaining))
	}
}

func TestSavedFilters_UserIsolation(t *testing.T) {
	q, _ := setupTestDB(t)
	ctx := context.Background()

	user1, err := q.CreateUser(ctx, CreateUserParams{
		Username:     "user1",
		PasswordHash: "$2a$10$fakehash",
		DisplayName:  "User1",
		Role:         "member",
	})
	if err != nil {
		t.Fatalf("CreateUser (1): %v", err)
	}

	user2, err := q.CreateUser(ctx, CreateUserParams{
		Username:     "user2",
		PasswordHash: "$2a$10$fakehash",
		DisplayName:  "User2",
		Role:         "member",
	})
	if err != nil {
		t.Fatalf("CreateUser (2): %v", err)
	}

	_, err = q.CreateSavedFilter(ctx, CreateSavedFilterParams{
		UserID:     user1.ID,
		Name:       "User1 Filter",
		FilterJson: `{"owner":"user1"}`,
	})
	if err != nil {
		t.Fatalf("CreateSavedFilter for user1: %v", err)
	}

	_, err = q.CreateSavedFilter(ctx, CreateSavedFilterParams{
		UserID:     user2.ID,
		Name:       "User2 Filter",
		FilterJson: `{"owner":"user2"}`,
	})
	if err != nil {
		t.Fatalf("CreateSavedFilter for user2: %v", err)
	}

	// User1 should only see their filter
	filters1, err := q.ListSavedFilters(ctx, user1.ID)
	if err != nil {
		t.Fatalf("ListSavedFilters (user1): %v", err)
	}
	if len(filters1) != 1 {
		t.Errorf("expected 1 filter for user1, got %d", len(filters1))
	}
	if len(filters1) > 0 && filters1[0].Name != "User1 Filter" {
		t.Errorf("expected 'User1 Filter', got %q", filters1[0].Name)
	}

	// User2 cannot delete user1's filter
	_, err = q.DeleteSavedFilter(ctx, DeleteSavedFilterParams{
		ID:     filters1[0].ID,
		UserID: user2.ID,
	})
	if err != nil {
		t.Fatalf("DeleteSavedFilter: %v", err)
	}
	// Filter should still exist (WHERE clause prevents cross-user delete)
	stillThere, err := q.ListSavedFilters(ctx, user1.ID)
	if err != nil {
		t.Fatalf("ListSavedFilters after cross-user delete: %v", err)
	}
	if len(stillThere) != 1 {
		t.Errorf("expected filter to survive cross-user delete, got %d filters", len(stillThere))
	}
}

func TestDashboardAggregations_EmptyMonth(t *testing.T) {
	q, _ := setupTestDB(t)
	ctx := context.Background()

	// No transactions exist — aggregation should return 0
	expenseSum, err := q.SumExpensesByMonth(ctx, SumExpensesByMonthParams{
		Year:  "2026",
		Month: "01",
	})
	if err != nil {
		t.Fatalf("SumExpensesByMonth (empty): %v", err)
	}
	if expenseSum != 0.0 {
		t.Errorf("expected 0 for empty month, got %v", expenseSum)
	}

	incomeSum, err := q.SumIncomeByMonth(ctx, SumIncomeByMonthParams{
		Year:  "2026",
		Month: "01",
	})
	if err != nil {
		t.Fatalf("SumIncomeByMonth (empty): %v", err)
	}
	if incomeSum != 0.0 {
		t.Errorf("expected 0 for empty month, got %v", incomeSum)
	}

	byCat, err := q.SumByCategoryForMonth(ctx, SumByCategoryForMonthParams{
		Year:  "2026",
		Month: "01",
	})
	if err != nil {
		t.Fatalf("SumByCategoryForMonth (empty): %v", err)
	}
	if len(byCat) != 0 {
		t.Errorf("expected 0 categories for empty month, got %d", len(byCat))
	}
}
