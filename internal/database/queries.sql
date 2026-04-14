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
--
-- Every read query in this section (and every other transactions read in
-- this file) MUST filter t.deleted_at IS NULL so soft-deleted rows stay
-- hidden from the live app. The only exceptions are:
--   * GetTransactionByID: mutation-only caller (TransactionStore.Update /
--     Delete) needs to see the tombstone row to emit the audit before/after.
--   * ListDeletedTransactions / CountDeletedTransactions: the trash view
--     specifically wants tombstoned rows.
--   * CountAllTransactions: the live/deleted split used by operator tools.
-- When adding a new transactions read, place it in queries.sql (not raw
-- SQL in a handler) and add AND t.deleted_at IS NULL by default.

-- name: CreateTransaction :one
-- Dual-write contract (Phase 3.1a): writers populate BOTH the legacy REAL
-- columns (amount, original_amount) AND the new INTEGER _cents columns until
-- migration 010 drops the legacy columns. Keep the caller math local -
-- amount_cents = int64(math.Round(amount*100)) at the call site.
INSERT INTO transactions (user_id, date, amount, amount_cents, original_amount, original_amount_cents, original_currency, description, category_id, tags, notes)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetTransactionByID :one
-- Mutation-only caller: used by TransactionStore.Update/Delete to emit the
-- audit before/after rows. Deliberately leaks tombstoned rows so Delete can
-- load the live row before marking it deleted_at; the handlers that serve
-- user-facing reads go through ListTransactions / sqlc aggregation queries
-- which all filter deleted_at IS NULL.
SELECT t.*, c.type AS category_type
FROM transactions t
JOIN categories c ON t.category_id = c.id
WHERE t.id = ?;

-- name: UpdateTransaction :exec
-- Dual-write contract (Phase 3.1a): see CreateTransaction above. Both the
-- legacy REAL column and the new INTEGER cents column are rewritten on every
-- edit. The caller computes cents from the float amount before invoking.
UPDATE transactions
SET date = ?, amount = ?, amount_cents = ?, original_amount = ?, original_amount_cents = ?, original_currency = ?, description = ?, category_id = ?, tags = ?, notes = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: SoftDeleteTransaction :exec
-- Tombstones a live row. The AND deleted_at IS NULL guard makes this
-- idempotent: tombstoning an already-tombstoned row is a no-op and leaves
-- the original deleted_at value intact so the audit trail stays stable.
UPDATE transactions
SET deleted_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND deleted_at IS NULL;

-- name: RestoreTransaction :exec
-- Clears the tombstone on a previously soft-deleted row. The
-- AND deleted_at IS NOT NULL guard prevents a restore from silently
-- touching a live row (which would still be a legal UPDATE without the
-- guard, but carries no semantic meaning).
UPDATE transactions
SET deleted_at = NULL, updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND deleted_at IS NOT NULL;

-- name: PurgeTransaction :exec
-- Hard delete, used only by the trash purge worker and by operator tools.
-- The AND deleted_at IS NOT NULL guard makes it impossible to purge a
-- live row via this query: the only code path that permanently removes a
-- live row is a full-retention soft-delete-then-purge sequence.
DELETE FROM transactions WHERE id = ? AND deleted_at IS NOT NULL;

-- name: ListDeletedTransactions :many
SELECT t.*, c.name AS category_name, c.type AS category_type
FROM transactions t
JOIN categories c ON t.category_id = c.id
WHERE t.deleted_at IS NOT NULL
ORDER BY t.deleted_at DESC, t.id DESC
LIMIT ? OFFSET ?;

-- name: CountDeletedTransactions :one
SELECT COUNT(*) FROM transactions WHERE deleted_at IS NOT NULL;

-- name: CountAllTransactions :one
SELECT
    CAST(COALESCE(SUM(CASE WHEN deleted_at IS NULL THEN 1 ELSE 0 END), 0) AS INTEGER) AS live,
    CAST(COALESCE(SUM(CASE WHEN deleted_at IS NOT NULL THEN 1 ELSE 0 END), 0) AS INTEGER) AS deleted
FROM transactions;

-- Budgets

-- name: GetBudget :one
SELECT * FROM budgets WHERE year = ? AND month = ?;

-- name: UpsertBudget :exec
-- Dual-write contract (Phase 3.1a): see CreateTransaction. Both the legacy
-- REAL column and the new INTEGER cents column are populated on every
-- upsert. Caller computes cents from the float amount.
INSERT INTO budgets (year, month, amount, amount_cents)
VALUES (?, ?, ?, ?)
ON CONFLICT(year, month) DO UPDATE SET
    amount = excluded.amount,
    amount_cents = excluded.amount_cents,
    updated_at = CURRENT_TIMESTAMP;

-- name: ListBudgetsByYear :many
SELECT * FROM budgets WHERE year = ? ORDER BY month;

-- Savings Goals

-- name: GetSavingsGoal :one
SELECT * FROM savings_goals WHERE year = ?;

-- name: UpsertSavingsGoal :exec
-- Dual-write contract (Phase 3.1a): see CreateTransaction. Both the legacy
-- REAL column and the new INTEGER cents column are populated on every
-- upsert. Caller computes cents from the float target amount.
INSERT INTO savings_goals (year, target_amount, target_amount_cents)
VALUES (?, ?, ?)
ON CONFLICT(year) DO UPDATE SET
    target_amount = excluded.target_amount,
    target_amount_cents = excluded.target_amount_cents,
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
--
-- Phase 3.1a: every aggregation below sums t.amount_cents (int64) instead of
-- t.amount (float64). The result alias is renamed to `*_cents` so the
-- generated Go field is self-documenting and so every consumer at the
-- handler boundary is forced to decide "do I need cents or dollars here?"
-- at the call site rather than trusting float arithmetic implicitly. The
-- CAST(... AS INTEGER) pins the return type - without it sqlc can get
-- confused by SUM() over a nullable integer column.

-- name: SumExpensesByMonth :one
SELECT CAST(COALESCE(SUM(t.amount_cents), 0) AS INTEGER) AS total_cents
FROM transactions t
JOIN categories c ON t.category_id = c.id
WHERE c.type = 'expense'
    AND t.deleted_at IS NULL
    AND strftime('%Y', t.date) = CAST(sqlc.arg(year) AS TEXT)
    AND strftime('%m', t.date) = CAST(sqlc.arg(month) AS TEXT);

-- name: SumIncomeByMonth :one
SELECT CAST(COALESCE(SUM(t.amount_cents), 0) AS INTEGER) AS total_cents
FROM transactions t
JOIN categories c ON t.category_id = c.id
WHERE c.type = 'income'
    AND t.deleted_at IS NULL
    AND strftime('%Y', t.date) = CAST(sqlc.arg(year) AS TEXT)
    AND strftime('%m', t.date) = CAST(sqlc.arg(month) AS TEXT);

-- name: SumByCategoryForMonth :many
SELECT c.id, c.name, CAST(COALESCE(SUM(t.amount_cents), 0) AS INTEGER) AS total_cents
FROM transactions t
JOIN categories c ON t.category_id = c.id
WHERE c.type = 'expense'
    AND t.deleted_at IS NULL
    AND strftime('%Y', t.date) = CAST(sqlc.arg(year) AS TEXT)
    AND strftime('%m', t.date) = CAST(sqlc.arg(month) AS TEXT)
GROUP BY c.id
ORDER BY total_cents DESC;

-- name: SumByMonthRange :many
SELECT
    CAST(strftime('%Y', t.date) AS INTEGER) AS year,
    CAST(strftime('%m', t.date) AS INTEGER) AS month,
    CAST(COALESCE(SUM(CASE WHEN c.type = 'expense' THEN t.amount_cents ELSE 0 END), 0) AS INTEGER) AS expenses_cents,
    CAST(COALESCE(SUM(CASE WHEN c.type = 'income' THEN t.amount_cents ELSE 0 END), 0) AS INTEGER) AS income_cents
FROM transactions t
JOIN categories c ON t.category_id = c.id
WHERE t.deleted_at IS NULL
    AND t.date >= CAST(sqlc.arg(date_from) AS TEXT) AND t.date <= CAST(sqlc.arg(date_to) AS TEXT)
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
    CAST(COALESCE(SUM(t.amount_cents), 0) AS INTEGER) AS total_cents
FROM transactions t
JOIN categories c ON t.category_id = c.id
WHERE t.deleted_at IS NULL
    AND t.date >= CAST(sqlc.arg(date_from) AS TEXT) AND t.date <= CAST(sqlc.arg(date_to) AS TEXT)
GROUP BY c.id, year, month
ORDER BY c.name, year, month;

-- name: TopDescriptions :many
SELECT
    t.description,
    c.type AS category_type,
    COUNT(*) AS tx_count,
    CAST(COALESCE(SUM(t.amount_cents), 0) AS INTEGER) AS total_cents
FROM transactions t
JOIN categories c ON t.category_id = c.id
WHERE c.type = 'expense'
    AND t.deleted_at IS NULL
    AND t.date >= CAST(sqlc.arg(date_from) AS TEXT) AND t.date <= CAST(sqlc.arg(date_to) AS TEXT)
GROUP BY t.description
ORDER BY total_cents DESC
LIMIT sqlc.arg(limit);

-- Spending Heatmap

-- name: SumExpensesByDay :many
SELECT t.date, CAST(COALESCE(SUM(t.amount_cents), 0) AS INTEGER) AS total_cents
FROM transactions t
JOIN categories c ON t.category_id = c.id
WHERE c.type = 'expense'
    AND t.deleted_at IS NULL
    AND strftime('%Y', t.date) = CAST(sqlc.arg(year) AS TEXT)
GROUP BY t.date
ORDER BY t.date;

-- Expense Velocity

-- name: SumExpensesByDayInMonth :many
SELECT CAST(strftime('%d', t.date) AS INTEGER) AS day,
       CAST(COALESCE(SUM(t.amount_cents), 0) AS INTEGER) AS daily_total_cents
FROM transactions t
JOIN categories c ON t.category_id = c.id
WHERE c.type = 'expense'
    AND t.deleted_at IS NULL
    AND strftime('%Y', t.date) = CAST(sqlc.arg(year) AS TEXT)
    AND strftime('%m', t.date) = CAST(sqlc.arg(month) AS TEXT)
GROUP BY day
ORDER BY day;

-- Recurring Expenses

-- name: RecurringDescriptions :many
SELECT t.description,
       COUNT(DISTINCT strftime('%Y-%m', t.date)) AS month_count,
       CAST(COALESCE(SUM(t.amount_cents), 0) AS INTEGER) AS annual_total_cents
FROM transactions t
JOIN categories c ON t.category_id = c.id
WHERE c.type = 'expense'
    AND t.deleted_at IS NULL
    AND strftime('%Y', t.date) = CAST(sqlc.arg(year) AS TEXT)
GROUP BY t.description
HAVING COUNT(DISTINCT strftime('%Y-%m', t.date)) >= 3
ORDER BY annual_total_cents DESC;

-- Tag Breakdown (raw data for Go-side aggregation)

-- name: TransactionAmountsAndTags :many
-- Returns int64 amount_cents instead of float64 amount - Go-side tag
-- aggregation sums cents to avoid float drift, then the handler converts
-- the per-tag totals to dollars at the JSON wire edge.
SELECT t.amount_cents, t.tags
FROM transactions t
JOIN categories c ON t.category_id = c.id
WHERE c.type = 'expense'
    AND t.deleted_at IS NULL
    AND t.tags IS NOT NULL AND t.tags != ''
    AND t.date >= CAST(sqlc.arg(date_from) AS TEXT)
    AND t.date <= CAST(sqlc.arg(date_to) AS TEXT);

-- Transaction Audit Log

-- name: InsertTransactionAudit :exec
INSERT INTO transaction_audit (transaction_id, action, actor_user_id, before_json, after_json)
VALUES (?, ?, ?, ?, ?);

-- name: ListTransactionAuditByID :many
SELECT * FROM transaction_audit
WHERE transaction_id = sqlc.arg(transaction_id)
ORDER BY occurred_at ASC, id ASC
LIMIT sqlc.arg(limit);

-- name: ListRecentTransactionAudit :many
SELECT * FROM transaction_audit
WHERE occurred_at >= CAST(sqlc.arg(since) AS TEXT)
ORDER BY occurred_at DESC, id DESC
LIMIT sqlc.arg(limit);

-- Balance Checkpoints
--
-- Beancount-style assertions of the form: on date D, the scope X summed
-- to Y cents. The verification query is the load-bearing one: it computes
-- the actual sum over live transactions (t.deleted_at IS NULL, Phase 2.1
-- invariant) up to and including the checkpoint date and returns int64
-- cents so the handler can compare against expected_amount_cents without
-- any float coercion. The scope switch is done inline in the WHERE clause
-- so the same query serves all three scope types and sqlc generates a
-- single Go helper.
--
-- Tag-scope note: tags are CSV-encoded in transactions.tags, so tag match
-- uses the canonical CSV-token LIKE pattern. See the long comment on
-- migration 007_balance_checkpoints.sql for the rationale and the exact
-- form of the LIKE expression.

-- name: CreateCheckpoint :one
INSERT INTO balance_checkpoints (
    user_id,
    scope_type,
    scope_id,
    scope_label,
    date,
    expected_amount_cents,
    note,
    last_verification_status
) VALUES (?, ?, ?, ?, ?, ?, ?, 'pending')
RETURNING *;

-- name: GetCheckpoint :one
SELECT * FROM balance_checkpoints WHERE id = ?;

-- name: ListCheckpoints :many
SELECT * FROM balance_checkpoints
ORDER BY date DESC, id DESC;

-- name: ListCheckpointsByUser :many
SELECT * FROM balance_checkpoints
WHERE user_id = ?
ORDER BY date DESC, id DESC;

-- name: DeleteCheckpoint :execresult
DELETE FROM balance_checkpoints WHERE id = ?;

-- name: UpdateCheckpointVerification :exec
UPDATE balance_checkpoints
SET last_verified_at = CURRENT_TIMESTAMP,
    last_verification_status = ?
WHERE id = ?;

-- name: ListCheckpointsAffectedByDate :many
-- Returns every checkpoint whose date is on or after the supplied date.
-- Used by the post-mutation/post-import hook: a transaction with date D
-- can only change the sum-as-of-date for dates greater than or equal to
-- D, so earlier checkpoints are untouchable by definition and skipped.
SELECT * FROM balance_checkpoints
WHERE date >= CAST(sqlc.arg(date) AS TEXT)
ORDER BY date ASC, id ASC;

-- name: ListStaleCheckpoints :many
-- Returns checkpoints whose last_verified_at is older than
-- sqlc.arg(threshold) (RFC3339) or NULL. Consumed by /healthz/data to
-- re-verify everything that has drifted beyond the freshness window, so
-- the monitoring scraper carries the cost of periodic re-verification.
SELECT * FROM balance_checkpoints
WHERE last_verified_at IS NULL
   OR last_verified_at < CAST(sqlc.arg(threshold) AS TEXT)
ORDER BY id ASC;

-- name: CountCheckpointsByStatus :one
-- One-row, three-column count used by /healthz/data. NULL status is
-- bucketed into pending so the counts always sum to the total row
-- count of the table.
SELECT
    CAST(COALESCE(SUM(CASE WHEN last_verification_status = 'ok' THEN 1 ELSE 0 END), 0) AS INTEGER) AS ok,
    CAST(COALESCE(SUM(CASE WHEN last_verification_status = 'mismatch' THEN 1 ELSE 0 END), 0) AS INTEGER) AS mismatch,
    CAST(COALESCE(SUM(CASE WHEN last_verification_status = 'pending' OR last_verification_status IS NULL THEN 1 ELSE 0 END), 0) AS INTEGER) AS pending
FROM balance_checkpoints;

-- name: CountStaleMismatchCheckpoints :one
-- Counts mismatched checkpoints whose last_verified_at is older than the
-- supplied threshold. /healthz/data flips to 503 when this is greater than
-- zero, matching the degradation trigger documented in the phase plan.
SELECT CAST(COALESCE(SUM(1), 0) AS INTEGER) AS n
FROM balance_checkpoints
WHERE last_verification_status = 'mismatch'
  AND last_verified_at IS NOT NULL
  AND last_verified_at < CAST(sqlc.arg(threshold) AS TEXT);

-- name: VerifyCheckpointTotal :one
-- The load-bearing verification query. Returns an int64 cents sum over
-- live transactions up to and including the checkpoint date, for the
-- scope matching the supplied arguments. Handler compares the returned
-- actual_cents against the checkpoint expected_amount_cents.
--
-- Phase 2.1: t.deleted_at IS NULL filters tombstoned rows out of the
-- aggregate so a soft-deleted transaction cannot silently flip a
-- previously-verified checkpoint.
-- Phase 3.1a: SUM(t.amount_cents) keeps the aggregate in int64 cents; the
-- COALESCE pins the empty-match case to 0 (not NULL), and the CAST locks
-- sqlc into an int64 return type.
--
-- Every sqlc.arg is wrapped in an explicit CAST so sqlc infers a concrete
-- Go type for every parameter. Without the casts, scope_type lands as
-- interface{} because it is only compared against string literals and
-- scope_id lands as int64 (non-null) which obscures the intent that it
-- is meaningful only when scope_type = 'category'.
SELECT CAST(COALESCE(SUM(t.amount_cents), 0) AS INTEGER) AS actual_cents
FROM transactions t
WHERE t.deleted_at IS NULL
  AND t.date <= CAST(sqlc.arg(date) AS TEXT)
  AND (
    CAST(sqlc.arg(scope_type) AS TEXT) = 'total'
    OR (CAST(sqlc.arg(scope_type) AS TEXT) = 'category' AND t.category_id = CAST(sqlc.arg(scope_id) AS INTEGER))
    OR (CAST(sqlc.arg(scope_type) AS TEXT) = 'tag' AND ',' || COALESCE(t.tags, '') || ',' LIKE '%,' || CAST(sqlc.arg(scope_label) AS TEXT) || ',%')
  );
