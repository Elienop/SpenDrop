-- Users

-- name: CreateUser :one
INSERT INTO users (username, password_hash, display_name, role)
VALUES (?, ?, ?, ?)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = ?;

-- name: GetUserByUsername :one
SELECT * FROM users WHERE username = ?;

-- name: ListUsers :many
SELECT * FROM users ORDER BY id;

-- name: UpdateUser :exec
UPDATE users
SET display_name = ?, role = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: DeleteUser :execresult
DELETE FROM users WHERE id = ?;

-- Sessions

-- name: CreateSession :exec
INSERT INTO sessions (token, user_id, expires_at)
VALUES (?, ?, ?);

-- name: GetSession :one
SELECT token, user_id, expires_at, created_at
FROM sessions
WHERE token = ?;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token = ?;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at < CURRENT_TIMESTAMP;

-- name: DeleteSessionsByUserID :exec
DELETE FROM sessions WHERE user_id = ?;

-- Categories

-- name: CreateCategory :one
INSERT INTO categories (name, type, sort_order)
VALUES (?, ?, ?)
RETURNING *;

-- name: GetCategoryByID :one
SELECT * FROM categories WHERE id = ?;

-- name: ListActiveCategories :many
SELECT * FROM categories WHERE is_active = 1 ORDER BY type, sort_order;

-- name: ListAllCategories :many
SELECT * FROM categories ORDER BY type, sort_order;

-- name: UpdateCategory :execresult
UPDATE categories
SET name = ?, icon = ?
WHERE id = ?;

-- name: UpdateCategorySortOrder :exec
UPDATE categories SET sort_order = ? WHERE id = ?;

-- name: UpdateCategoryActive :execresult
UPDATE categories SET is_active = ? WHERE id = ?;

-- name: DeleteCategory :execresult
DELETE FROM categories WHERE id = ?;

-- Currencies

-- name: ListCurrencies :many
SELECT * FROM currencies;

-- name: GetCurrency :one
SELECT * FROM currencies WHERE code = ?;

-- name: UpsertCurrency :exec
INSERT INTO currencies (code, name, symbol, rate_to_base, is_base)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(code) DO UPDATE SET
    name = excluded.name,
    symbol = excluded.symbol,
    rate_to_base = excluded.rate_to_base,
    is_base = excluded.is_base,
    updated_at = CURRENT_TIMESTAMP;

-- Transactions

-- name: CreateTransaction :one
INSERT INTO transactions (user_id, date, amount, original_amount, original_currency, description, category_id, tags, notes)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetTransactionByID :one
SELECT t.*, c.type AS category_type
FROM transactions t
JOIN categories c ON t.category_id = c.id
WHERE t.id = ?;

-- name: UpdateTransaction :exec
UPDATE transactions
SET date = ?, amount = ?, original_amount = ?, original_currency = ?, description = ?, category_id = ?, tags = ?, notes = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: DeleteTransaction :exec
DELETE FROM transactions WHERE id = ?;

-- Budgets

-- name: GetBudget :one
SELECT * FROM budgets WHERE year = ? AND month = ?;

-- name: UpsertBudget :exec
INSERT INTO budgets (year, month, amount)
VALUES (?, ?, ?)
ON CONFLICT(year, month) DO UPDATE SET
    amount = excluded.amount,
    updated_at = CURRENT_TIMESTAMP;

-- name: ListBudgetsByYear :many
SELECT * FROM budgets WHERE year = ? ORDER BY month;

-- Savings Goals

-- name: GetSavingsGoal :one
SELECT * FROM savings_goals WHERE year = ?;

-- name: UpsertSavingsGoal :exec
INSERT INTO savings_goals (year, target_amount)
VALUES (?, ?)
ON CONFLICT(year) DO UPDATE SET
    target_amount = excluded.target_amount,
    updated_at = CURRENT_TIMESTAMP;

-- name: ListSavingsGoals :many
SELECT * FROM savings_goals ORDER BY year DESC;

-- App Settings

-- name: GetSetting :one
SELECT * FROM app_settings WHERE key = ?;

-- name: UpsertSetting :exec
INSERT INTO app_settings (key, value)
VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET
    value = excluded.value,
    updated_at = CURRENT_TIMESTAMP;

-- Dashboard aggregation queries

-- name: SumExpensesByMonth :one
SELECT CAST(COALESCE(SUM(t.amount), 0) AS REAL) AS total
FROM transactions t
JOIN categories c ON t.category_id = c.id
WHERE c.type = 'expense'
    AND strftime('%Y', t.date) = CAST(sqlc.arg(year) AS TEXT)
    AND strftime('%m', t.date) = CAST(sqlc.arg(month) AS TEXT);

-- name: SumIncomeByMonth :one
SELECT CAST(COALESCE(SUM(t.amount), 0) AS REAL) AS total
FROM transactions t
JOIN categories c ON t.category_id = c.id
WHERE c.type = 'income'
    AND strftime('%Y', t.date) = CAST(sqlc.arg(year) AS TEXT)
    AND strftime('%m', t.date) = CAST(sqlc.arg(month) AS TEXT);

-- name: SumByCategoryForMonth :many
SELECT c.id, c.name, CAST(COALESCE(SUM(t.amount), 0) AS REAL) AS total
FROM transactions t
JOIN categories c ON t.category_id = c.id
WHERE c.type = 'expense'
    AND strftime('%Y', t.date) = CAST(sqlc.arg(year) AS TEXT)
    AND strftime('%m', t.date) = CAST(sqlc.arg(month) AS TEXT)
GROUP BY c.id
ORDER BY total DESC;

-- name: SumByMonthRange :many
SELECT
    CAST(strftime('%Y', t.date) AS INTEGER) AS year,
    CAST(strftime('%m', t.date) AS INTEGER) AS month,
    CAST(COALESCE(SUM(CASE WHEN c.type = 'expense' THEN t.amount ELSE 0 END), 0) AS REAL) AS expenses,
    CAST(COALESCE(SUM(CASE WHEN c.type = 'income' THEN t.amount ELSE 0 END), 0) AS REAL) AS income
FROM transactions t
JOIN categories c ON t.category_id = c.id
WHERE t.date >= CAST(sqlc.arg(date_from) AS TEXT) AND t.date <= CAST(sqlc.arg(date_to) AS TEXT)
GROUP BY year, month
ORDER BY year, month;

-- Saved Filters

-- name: CreateSavedFilter :one
INSERT INTO saved_filters (user_id, name, filter_json)
VALUES (?, ?, ?)
RETURNING *;

-- name: ListSavedFilters :many
SELECT * FROM saved_filters WHERE user_id = ? ORDER BY name;

-- name: UpdateSavedFilter :execresult
UPDATE saved_filters SET name = ?, filter_json = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND user_id = ?;

-- name: DeleteSavedFilter :execresult
DELETE FROM saved_filters WHERE id = ? AND user_id = ?;

-- Reports

-- name: SumByCategoryForRange :many
SELECT
    c.id,
    c.name,
    c.type AS category_type,
    CAST(strftime('%Y', t.date) AS INTEGER) AS year,
    CAST(strftime('%m', t.date) AS INTEGER) AS month,
    CAST(COALESCE(SUM(t.amount), 0) AS REAL) AS total
FROM transactions t
JOIN categories c ON t.category_id = c.id
WHERE t.date >= CAST(sqlc.arg(date_from) AS TEXT) AND t.date <= CAST(sqlc.arg(date_to) AS TEXT)
GROUP BY c.id, year, month
ORDER BY c.name, year, month;

-- name: TopDescriptions :many
SELECT
    t.description,
    c.type AS category_type,
    COUNT(*) AS tx_count,
    CAST(COALESCE(SUM(t.amount), 0) AS REAL) AS total
FROM transactions t
JOIN categories c ON t.category_id = c.id
WHERE c.type = 'expense'
    AND t.date >= CAST(sqlc.arg(date_from) AS TEXT) AND t.date <= CAST(sqlc.arg(date_to) AS TEXT)
GROUP BY t.description
ORDER BY total DESC
LIMIT sqlc.arg(limit);
