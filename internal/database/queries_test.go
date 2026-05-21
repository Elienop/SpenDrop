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

// dollarsToCents converts a float dollar amount to int64 cents using
// half-away-from-zero rounding. Phase 3.1b: amount_cents is the only money
// column (the legacy REAL columns were dropped in migration 010); tests use
// this helper to seed the cents column the same way the api layer does at
// the wire edge. The api layer has an equivalent helper — keeping this one
// file-local avoids exposing a public money-conversion surface from the
// database package just for test scaffolding.
func dollarsToCents(d float64) int64 {
	if d < 0 {
		return -int64(-d*100 + 0.5)
	}
	return int64(d*100 + 0.5)
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
		AmountCents: dollarsToCents(42.50),
		Description: "Groceries",
		CategoryID:  1,
	})
	if err != nil {
		t.Fatalf("CreateTransaction: %v", err)
	}
	if txn.AmountCents != dollarsToCents(42.50) {
		t.Errorf("expected amount_cents %d, got %d", dollarsToCents(42.50), txn.AmountCents)
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
		AmountCents: dollarsToCents(50.00),
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
	if updated.AmountCents != dollarsToCents(50.00) {
		t.Errorf("expected amount_cents %d, got %d", dollarsToCents(50.00), updated.AmountCents)
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
		Year:        2026,
		Month:       4,
		AmountCents: dollarsToCents(3000.00),
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
	if budget.AmountCents != dollarsToCents(3000.00) {
		t.Errorf("expected amount_cents %d, got %d", dollarsToCents(3000.00), budget.AmountCents)
	}

	// Upsert updates existing
	err = q.UpsertBudget(ctx, UpsertBudgetParams{
		Year:        2026,
		Month:       4,
		AmountCents: dollarsToCents(3500.00),
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
	if budget2.AmountCents != dollarsToCents(3500.00) {
		t.Errorf("expected updated amount_cents %d, got %d", dollarsToCents(3500.00), budget2.AmountCents)
	}

	// ListBudgetsByYear
	err = q.UpsertBudget(ctx, UpsertBudgetParams{
		Year:        2026,
		Month:       5,
		AmountCents: dollarsToCents(2800.00),
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
		Year:              2026,
		TargetAmountCents: dollarsToCents(10000.00),
	})
	if err != nil {
		t.Fatalf("UpsertSavingsGoal: %v", err)
	}

	goal, err := q.GetSavingsGoal(ctx, 2026)
	if err != nil {
		t.Fatalf("GetSavingsGoal: %v", err)
	}
	if goal.TargetAmountCents != dollarsToCents(10000.00) {
		t.Errorf("expected target_amount_cents %d, got %d", dollarsToCents(10000.00), goal.TargetAmountCents)
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
		AmountCents: dollarsToCents(100.00),
		Description: "Groceries",
		CategoryID:  1, // Food
	})
	if err != nil {
		t.Fatalf("CreateTransaction (groceries): %v", err)
	}

	_, err = q.CreateTransaction(ctx, CreateTransactionParams{
		UserID:      user.ID,
		Date:        time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC),
		AmountCents: dollarsToCents(50.00),
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
		AmountCents: dollarsToCents(3000.00),
		Description: "April salary",
		CategoryID:  15, // Paycheck
	})
	if err != nil {
		t.Fatalf("CreateTransaction (salary): %v", err)
	}

	// SumExpensesByMonth — Phase 3.1a: returns int64 cents (15000 = $150.00)
	expenseSum, err := q.SumExpensesByMonth(ctx, SumExpensesByMonthParams{
		Year:  "2026",
		Month: "04",
	})
	if err != nil {
		t.Fatalf("SumExpensesByMonth: %v", err)
	}
	if expenseSum != 15000 {
		t.Errorf("expected expense sum 15000 cents ($150.00), got %d", expenseSum)
	}

	// SumIncomeByMonth — Phase 3.1a: returns int64 cents (300000 = $3000.00)
	incomeSum, err := q.SumIncomeByMonth(ctx, SumIncomeByMonthParams{
		Year:  "2026",
		Month: "04",
	})
	if err != nil {
		t.Fatalf("SumIncomeByMonth: %v", err)
	}
	if incomeSum != 300000 {
		t.Errorf("expected income sum 300000 cents ($3000.00), got %d", incomeSum)
	}

	// SumByCategoryForMonth — Phase 3.1a: Row.TotalCents is int64 cents
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
	// Results are ordered by total DESC, so Food (10000 cents = $100.00) should be first
	if len(byCat) >= 1 && byCat[0].TotalCents != 10000 {
		t.Errorf("expected first category total 10000 cents ($100.00), got %d", byCat[0].TotalCents)
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

	// No transactions exist — aggregation should return 0 cents
	expenseSum, err := q.SumExpensesByMonth(ctx, SumExpensesByMonthParams{
		Year:  "2026",
		Month: "01",
	})
	if err != nil {
		t.Fatalf("SumExpensesByMonth (empty): %v", err)
	}
	if expenseSum != 0 {
		t.Errorf("expected 0 cents for empty month, got %d", expenseSum)
	}

	incomeSum, err := q.SumIncomeByMonth(ctx, SumIncomeByMonthParams{
		Year:  "2026",
		Month: "01",
	})
	if err != nil {
		t.Fatalf("SumIncomeByMonth (empty): %v", err)
	}
	if incomeSum != 0 {
		t.Errorf("expected 0 cents for empty month, got %d", incomeSum)
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
		AmountCents: dollarsToCents(12.50),
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
		AmountCents: dollarsToCents(5.00),
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
		AmountCents: dollarsToCents(6.00),
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

// TestAmountCentsBackfillEqualsCents pins the contract of migration 006's
// backfill UPDATE. The migration rewrites every pre-existing row with
//
//	amount_cents = CAST(ROUND(amount * 100) AS INTEGER)
//
// and the rest of Phase 3.1a's aggregation switch assumes that expression
// produces the same integer value that dollarsToCents produces for a newly
// inserted row. If that equivalence ever drifts (a SQLite upgrade changes
// ROUND's half-away-from-zero semantics, or someone "optimizes" the
// migration to CAST(amount*100 AS INTEGER) and silently truncates), every
// historical row's cents column will be off by one compared to the
// dual-write path and dashboard totals will disagree with the exports.
//
// Strategy: migration 010 (Phase 3.1b) dropped the legacy REAL `amount`
// column, so we can no longer seed `amount` against the fully-migrated DB.
// Instead we stand up the schema at the PRE-006 version (through migration
// 005), where the REAL `amount` column still exists and `amount_cents` does
// not. We seed rows with `amount`, then apply migration 006 itself — which
// adds `amount_cents` and runs its backfill UPDATE — and compare the
// resulting cents against dollarsToCents. This exercises the actual 006
// migration SQL, not a hand-copied expression, so the test cannot drift from
// the migration it pins. The fixture values cover the drift-prone cases:
// .005 boundary rounds and amounts whose float64 representation is not exact.
func TestAmountCentsBackfillEqualsCents(t *testing.T) {
	db, _ := openTestDB(t)
	ctx := context.Background()

	// Stand up the schema at the pre-006 version (001..005). At this point
	// transactions has the REAL `amount` column and no `amount_cents`.
	applyMigrationsThrough(t, db, "005_transactions_soft_delete.sql")

	// Pristine user + the seeded Food category (id=1). Direct SQL keeps
	// the test decoupled from sqlc-generated writes.
	if _, err := db.ExecContext(ctx, `INSERT INTO users (username, password_hash, display_name, role)
		VALUES ('backfill_probe', '$2a$10$fake', 'Backfill Probe', 'member')`); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	var userID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM users WHERE username='backfill_probe'`).Scan(&userID); err != nil {
		t.Fatalf("lookup user: %v", err)
	}

	// Drift-prone fixture values. 19.99 and 89000.42 are the motivating
	// cases cited in the migration 006 comment; 0.1/0.2/0.3 are the
	// textbook "0.1 + 0.2 != 0.3" triad; 19.995 sits exactly on the
	// half-away-from-zero boundary and must round UP to 2000, not DOWN
	// to 1999.
	fixtures := []float64{
		19.99,
		89000.42,
		0.1,
		0.2,
		0.3,
		19.995,
		42.57,
		1.005,
		0.01,
		1234567.89,
	}

	// Insert each fixture into the pre-006 schema (REAL `amount` only, no
	// amount_cents column yet).
	for _, amt := range fixtures {
		if _, err := db.ExecContext(ctx, `INSERT INTO transactions (user_id, date, amount, description, category_id)
			VALUES (?, '2026-04-01', ?, 'backfill fixture', 1)`, userID, amt); err != nil {
			t.Fatalf("insert fixture %v: %v", amt, err)
		}
	}

	// Apply migration 006 itself: it adds amount_cents and runs the backfill
	// UPDATE over the rows we just inserted. Running the real migration SQL
	// (rather than a hand-copied expression) keeps this test pinned to the
	// migration it guards.
	if _, err := db.ExecContext(ctx, readMigration(t, "006_amount_cents_add.sql")); err != nil {
		t.Fatalf("apply migration 006: %v", err)
	}

	// Pull every backfilled row back and compare against dollarsToCents,
	// which is the expression every dual-writer uses going forward. Any
	// drift between these two signals a semantic mismatch at the
	// migration boundary.
	rows, err := db.QueryContext(ctx, `SELECT amount, amount_cents FROM transactions WHERE user_id = ? ORDER BY id`, userID)
	if err != nil {
		t.Fatalf("select backfilled rows: %v", err)
	}
	defer rows.Close()

	var i int
	for rows.Next() {
		var amount float64
		var cents int64
		if err := rows.Scan(&amount, &cents); err != nil {
			t.Fatalf("scan row %d: %v", i, err)
		}
		want := dollarsToCents(amount)
		if cents != want {
			t.Errorf("row %d amount=%v: backfill cents=%d, dollarsToCents=%d (expressions must agree)", i, amount, cents, want)
		}
		i++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate rows: %v", err)
	}
	if i != len(fixtures) {
		t.Errorf("scanned %d rows, want %d (fixture count)", i, len(fixtures))
	}
}

// TestSumExpensesByMonth_NoFloatDrift is the headline behavioural test for
// Phase 3.1a: it proves that switching aggregation from `SUM(amount)` to
// `SUM(amount_cents)` eliminates the accumulated float drift that existed
// on the legacy path. The previous dashboard code worked around drift with
// scattered `math.Round(x*100)/100` patches; the cents column makes the
// class of bug impossible.
//
// Construction: seed 2000 rows with the textbook drift triad 0.10 / 0.20 /
// 0.30 plus a pair of "real money" values (19.99 and 42.57) that are not
// exactly representable in binary64. The expected total is computed in
// int64 cents (which is exact by definition), and the assertion compares
// the int64 result of SumExpensesByMonth against it byte-for-byte. If
// someone ever rolls this back to the REAL column, the equivalent float
// assertion would fail with a sub-cent drift on most systems.
func TestSumExpensesByMonth_NoFloatDrift(t *testing.T) {
	q, db := setupTestDB(t)
	ctx := context.Background()

	user, err := q.CreateUser(ctx, CreateUserParams{
		Username:     "drift_probe",
		PasswordHash: "$2a$10$fakehash",
		DisplayName:  "Drift Probe",
		Role:         "member",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Five drift-prone amounts repeated 400 times each = 2000 rows.
	// Expected int64 total:
	//   400 * (10 + 20 + 30 + 1999 + 4257) = 400 * 6316 = 2,526,400 cents
	// which is $25,264.00 exactly. On the legacy float path, summing 2000
	// non-representable binary64 values reliably drifts by a fraction of
	// a cent on most x86-64 libc rounding modes - small enough to slip
	// past naive asserts, large enough to corrupt rolling aggregates.
	fixtures := []float64{0.10, 0.20, 0.30, 19.99, 42.57}
	const repeats = 400

	txnDate := time.Date(2026, 4, 6, 0, 0, 0, 0, time.UTC)

	// Use a sql transaction for the bulk insert so the 2000 rows land in
	// one commit; on the CGO driver, autocommit-per-insert is an order of
	// magnitude slower and the test becomes the long pole of the suite.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin bulk insert tx: %v", err)
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO transactions (user_id, date, amount_cents, description, category_id)
		VALUES (?, ?, ?, 'drift fixture', 1)`)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("prepare bulk insert: %v", err)
	}
	var wantCents int64
	for i := 0; i < repeats; i++ {
		for _, amt := range fixtures {
			cents := dollarsToCents(amt)
			if _, err := stmt.ExecContext(ctx, user.ID, txnDate, cents); err != nil {
				_ = stmt.Close()
				_ = tx.Rollback()
				t.Fatalf("exec bulk insert: %v", err)
			}
			wantCents += cents
		}
	}
	if err := stmt.Close(); err != nil {
		_ = tx.Rollback()
		t.Fatalf("close stmt: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit bulk insert: %v", err)
	}

	// Sanity-check the fixture math: 400 * (10+20+30+1999+4257) = 2,526,400.
	const expected int64 = 2_526_400
	if wantCents != expected {
		t.Fatalf("fixture arithmetic broke: wantCents=%d, expected=%d", wantCents, expected)
	}

	// The aggregation call under test. Phase 3.1a made this return int64
	// cents; if it ever regresses to float dollars the comparison below
	// will not compile.
	gotCents, err := q.SumExpensesByMonth(ctx, SumExpensesByMonthParams{
		Year:  "2026",
		Month: "04",
	})
	if err != nil {
		t.Fatalf("SumExpensesByMonth: %v", err)
	}
	if gotCents != expected {
		t.Errorf("SumExpensesByMonth over 2000 drift-prone rows = %d cents, want %d cents (float drift would produce an off-by-one)", gotCents, expected)
	}
}
