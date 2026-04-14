package database

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// setupTestDB creates a file-backed SQLite database in t.TempDir() with
// migrations applied and returns a Queries instance for testing. Phase 4.1
// forced the switch from `:memory:` to on-disk so SnapshotForMigration
// (which opens its own read-only connection by dbPath) has a real file to
// snapshot — for `:memory:` each connection gets an independent empty
// database, making the snapshot step a meaningless no-op.
func setupTestDB(t *testing.T) (*Queries, *sql.DB) {
	t.Helper()
	db, dbPath := openTestDB(t)
	if err := RunMigrations(db, defaultMigrationOptions(t, dbPath)); err != nil {
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
		ID:   cat.ID,
		Name: "UpdatedCat",
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

	// Soft-delete transaction: row survives, deleted_at is set, and the
	// mutation-only GetTransactionByID read (which deliberately leaks
	// tombstoned rows) still returns the row so TransactionStore can emit
	// the before/after audit pair.
	err = q.SoftDeleteTransaction(ctx, txn.ID)
	if err != nil {
		t.Fatalf("SoftDeleteTransaction: %v", err)
	}

	tombstoned, err := q.GetTransactionByID(ctx, txn.ID)
	if err != nil {
		t.Fatalf("GetTransactionByID after soft-delete: %v", err)
	}
	if !tombstoned.DeletedAt.Valid {
		t.Errorf("expected deleted_at to be set after soft-delete, got NULL")
	}
	if tombstoned.Description != "Updated groceries" {
		t.Errorf("expected payload to survive soft-delete, got description %q", tombstoned.Description)
	}

	// SoftDelete is idempotent: second call is a no-op because of the
	// AND deleted_at IS NULL guard.
	firstDeletedAt := tombstoned.DeletedAt.Time
	if err := q.SoftDeleteTransaction(ctx, txn.ID); err != nil {
		t.Fatalf("second SoftDeleteTransaction: %v", err)
	}
	reread, err := q.GetTransactionByID(ctx, txn.ID)
	if err != nil {
		t.Fatalf("GetTransactionByID after idempotent soft-delete: %v", err)
	}
	if !reread.DeletedAt.Time.Equal(firstDeletedAt) {
		t.Errorf("expected deleted_at unchanged by second SoftDelete, got %v → %v", firstDeletedAt, reread.DeletedAt.Time)
	}

	// Restore clears the tombstone.
	if err := q.RestoreTransaction(ctx, txn.ID); err != nil {
		t.Fatalf("RestoreTransaction: %v", err)
	}
	restored, err := q.GetTransactionByID(ctx, txn.ID)
	if err != nil {
		t.Fatalf("GetTransactionByID after restore: %v", err)
	}
	if restored.DeletedAt.Valid {
		t.Errorf("expected deleted_at cleared after restore, got %v", restored.DeletedAt.Time)
	}

	// Purge only works on tombstoned rows — re-tombstone then purge.
	if err := q.SoftDeleteTransaction(ctx, txn.ID); err != nil {
		t.Fatalf("SoftDeleteTransaction before purge: %v", err)
	}
	if err := q.PurgeTransaction(ctx, txn.ID); err != nil {
		t.Fatalf("PurgeTransaction: %v", err)
	}
	if _, err := q.GetTransactionByID(ctx, txn.ID); err != sql.ErrNoRows {
		t.Errorf("expected ErrNoRows after purge, got %v", err)
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

// TestListRecentTransactionAudit_SmokeTest exercises the recent-rows
// query that `spendrop audit` (no --transaction-id flag) is built on.
// It exists specifically because Phase 4.2's first cut shipped with a
// sqlc-numbering bug that went undetected: the query mixed a named
// `sqlc.arg(since)` with a bare `?` for LIMIT, which generated
// `CAST(?2 AS TEXT) ... LIMIT ?` — where the bare `?` became `?3` at
// the SQLite layer while the Go caller only passed 2 positional args,
// crashing every `spendrop audit` invocation on first use. No test
// covered the recent-rows mode at the time, so the bind mismatch
// slipped all the way through to the reviewer. This test closes that
// hole: it runs the actual query against a real DB and asserts the
// smoke — if the placeholder numbering regresses, SQLite will return
// "wrong number of bind parameters" and this test fails loudly.
//
// Uses TransactionStore.Create so the audit row is written by the same
// chokepoint production uses, not a hand-rolled INSERT. The test is
// resilient to future column additions because it asserts on the
// fields the CLI actually displays (action, transaction_id, actor).
func TestListRecentTransactionAudit_SmokeTest(t *testing.T) {
	q, db := setupTestDB(t)
	ctx := context.Background()

	user, err := q.CreateUser(ctx, CreateUserParams{
		Username:     "auditor",
		PasswordHash: "$2a$10$fakehash",
		DisplayName:  "Auditor",
		Role:         "member",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	store := NewTransactionStore(db, q)
	txn, err := store.Create(ctx, user.ID, CreateTransactionParams{
		UserID:      user.ID,
		Date:        time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		Amount:      12.50,
		Description: "audit smoke probe",
		CategoryID:  1,
	})
	if err != nil {
		t.Fatalf("store.Create: %v", err)
	}

	// Since = far past so the insert row is always in range. The
	// datetime layout matches internal/api/audit.go's sqliteDatetimeFormat
	// ("2006-01-02 15:04:05") — the CLI has to use the same layout
	// because transaction_audit.occurred_at is compared lexicographically.
	rows, err := q.ListRecentTransactionAudit(ctx, ListRecentTransactionAuditParams{
		Since: "2000-01-01 00:00:00",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("ListRecentTransactionAudit: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected at least 1 audit row after store.Create, got 0")
	}

	var found bool
	for _, r := range rows {
		if r.TransactionID == txn.ID && r.Action == AuditInsert {
			found = true
			if !r.ActorUserID.Valid || r.ActorUserID.Int64 != user.ID {
				t.Errorf("smoke row actor_user_id=%+v, want %d", r.ActorUserID, user.ID)
			}
			break
		}
	}
	if !found {
		t.Errorf("expected audit row for transaction_id=%d action=insert, got %d rows without a match", txn.ID, len(rows))
	}
}

// TestListTransactionAuditByID_SmokeTest is the sibling to the recent-
// rows smoke test: it exercises the transaction-id-filtered query used
// by `spendrop audit --transaction-id N`. The Phase 4.2 first cut
// initially took its limit as a bare `int64` rather than a named param,
// which forced the CLI to slice the result in Go and could have
// produced the same binding regression if the query definition drifted.
// This test pins the contract: `ListTransactionAuditByIDParams` with a
// TransactionID and a Limit must come back with chronological rows,
// oldest first.
func TestListTransactionAuditByID_SmokeTest(t *testing.T) {
	q, db := setupTestDB(t)
	ctx := context.Background()

	user, err := q.CreateUser(ctx, CreateUserParams{
		Username:     "auditor2",
		PasswordHash: "$2a$10$fakehash",
		DisplayName:  "Auditor2",
		Role:         "member",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	store := NewTransactionStore(db, q)
	txn, err := store.Create(ctx, user.ID, CreateTransactionParams{
		UserID:      user.ID,
		Date:        time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		Amount:      5.00,
		Description: "by-id smoke probe",
		CategoryID:  1,
	})
	if err != nil {
		t.Fatalf("store.Create: %v", err)
	}
	// Second mutation so there's more than one audit row for this txn
	// and we can assert on chronological ordering.
	if err := store.Update(ctx, user.ID, UpdateTransactionParams{
		ID:          txn.ID,
		Date:        time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		Amount:      6.00,
		Description: "by-id smoke probe (updated)",
		CategoryID:  1,
	}); err != nil {
		t.Fatalf("store.Update: %v", err)
	}

	rows, err := q.ListTransactionAuditByID(ctx, ListTransactionAuditByIDParams{
		TransactionID: txn.ID,
		Limit:         10,
	})
	if err != nil {
		t.Fatalf("ListTransactionAuditByID: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 audit rows (insert + update), got %d", len(rows))
	}
	if rows[0].Action != AuditInsert {
		t.Errorf("row[0].action=%q, want insert (chronological order)", rows[0].Action)
	}
	if rows[1].Action != AuditUpdate {
		t.Errorf("row[1].action=%q, want update (chronological order)", rows[1].Action)
	}
}
