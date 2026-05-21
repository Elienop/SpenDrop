-- 010_amount_drop_legacy.sql
-- Phase 3.1b of the data-stewardship rollout: drop the legacy floating-point
-- money columns now that the integer-cents path (migration 006) has soaked
-- for a full release cycle. This is the destructive end of the dual-write
-- window opened by 006: every reader has consumed *_cents only since 006
-- shipped, every writer dual-populated both, and the wire format never
-- changed (the response serializers divide cents by 100 at the JSON edge).
-- Dropping the REAL columns makes float drift impossible by construction —
-- there is no longer a float column for a future query to accidentally SUM().
--
-- Pre-flight: this exact change was verified against the live production DB
-- (1589 transactions) before shipping — every amount_cents already equals
-- round(amount*100), every original_amount_cents equals round(original_amount
-- *100) (or NULL), and there are zero amount_cents <= 0 rows. So the
-- CHECK(amount_cents > 0) constraint added below cannot reject any existing
-- row; it only guards future inserts.
--
-- Why a full table REBUILD for transactions (not a bare DROP COLUMN):
--   * amount is NOT NULL with no default and SQLite's ALTER TABLE DROP COLUMN
--     cannot add the CHECK(amount_cents > 0) / CHECK(original_amount_cents ...)
--     constraints we want at the same time, and
--   * the rebuild is the established pattern in this schema (see migration
--     002_cascade_deletes.sql) for any change that must alter constraints on
--     transactions.
-- The new table is created with the cents columns and CHECK constraints,
-- every row is copied by explicit column list (NOT `SELECT *` — the old table
-- still has the dropped columns, so the column list must name only the
-- surviving columns), the old table is dropped, and the new one renamed into
-- place. Every index that existed on transactions is then recreated verbatim
-- (a rebuild drops all indexes with the old table) — see the CREATE INDEX
-- block below; missing any of these would be a silent correctness/perf bug.
--
-- Why NO `PRAGMA foreign_keys` toggle is needed (and why it would be wrong):
--   1. This migration runs INSIDE the single transaction the migration runner
--      wraps every file in (see applyPendingMigrations in migrate.go).
--      PRAGMA foreign_keys is a no-op inside an active transaction, so a
--      toggle here would be silently ignored — it must never be relied upon.
--   2. Nothing references transactions / budgets / savings_goals via an
--      INBOUND foreign key, so the drop-and-rename cannot orphan any child
--      row regardless of the foreign_keys setting:
--        - transaction_audit deliberately has NO FK to transactions (so a
--          hard delete cannot orphan its own audit history — see migration
--          009), and
--        - balance_checkpoints' only FKs point at users and categories, not
--          transactions (see migration 007).
--      The transactions table's own OUTBOUND FKs (user_id -> users ON DELETE
--      CASCADE, category_id -> categories) are copied verbatim onto the new
--      table below and are validated by foreign_key_check after the rename.
--
-- Forward-only: reverting requires the Phase 4.1 pre-migration snapshot
-- (VACUUM INTO copy taken before this runs). There is no DOWN migration.

-- Rebuild transactions without amount / original_amount.
CREATE TABLE transactions_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    date DATE NOT NULL,
    original_currency TEXT,
    description TEXT NOT NULL DEFAULT '',
    category_id INTEGER NOT NULL REFERENCES categories(id),
    tags TEXT,
    notes TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME,
    amount_cents INTEGER NOT NULL DEFAULT 0 CHECK(amount_cents > 0),
    original_amount_cents INTEGER CHECK(original_amount_cents IS NULL OR original_amount_cents > 0),
    content_hash TEXT
);

INSERT INTO transactions_new (
    id, user_id, date, original_currency, description, category_id, tags, notes,
    created_at, updated_at, deleted_at, amount_cents, original_amount_cents, content_hash
)
SELECT
    id, user_id, date, original_currency, description, category_id, tags, notes,
    created_at, updated_at, deleted_at, amount_cents, original_amount_cents, content_hash
FROM transactions;

DROP TABLE transactions;
ALTER TABLE transactions_new RENAME TO transactions;

-- Recreate every index that existed on transactions before the rebuild.
-- These reproduce, verbatim, the indexes from migrations 002 (date,
-- category_id, user_id), 005 (the two partial deleted_at indexes), and
-- 008 (the partial-unique content_hash index).
CREATE INDEX idx_transactions_date ON transactions(date);
CREATE INDEX idx_transactions_category_id ON transactions(category_id);
CREATE INDEX idx_transactions_user_id ON transactions(user_id);

CREATE INDEX idx_transactions_deleted_at_null
    ON transactions(id) WHERE deleted_at IS NULL;

CREATE INDEX idx_transactions_deleted_at
    ON transactions(deleted_at) WHERE deleted_at IS NOT NULL;

CREATE UNIQUE INDEX idx_transactions_content_hash
    ON transactions(content_hash)
    WHERE content_hash IS NOT NULL AND deleted_at IS NULL;

-- budgets / savings_goals: the legacy float is in no index and we are not
-- adding CHECK constraints to them, so a bare DROP COLUMN (SQLite 3.35+) is
-- sufficient. Their *_cents columns stay.
ALTER TABLE budgets DROP COLUMN amount;
ALTER TABLE savings_goals DROP COLUMN target_amount;
