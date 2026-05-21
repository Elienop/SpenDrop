-- 012_category_budgets.sql
-- Gap 2: per-category monthly budget limits.
--
-- Design decision (decided): per-category limits are INDEPENDENT sub-caps.
-- The existing month-level `budgets` table and all of the dashboard / reports
-- / velocity budget math remain UNCHANGED. A category_budgets row does NOT
-- partition or reconcile against the overall monthly budget — it is a separate
-- ceiling the user can optionally pin to an expense category, evaluated on its
-- own. The Homepage widget's `over_budget_categories` count is computed
-- household-wide: for the current month, count expense categories whose
-- household-wide month-to-date spend exceeds their per-category limit.
--
-- This is a purely additive CREATE TABLE. There is NO backfill and NO
-- collision/dedup machinery (contrast migration 008's content_hash sweep):
-- the table starts empty and is only populated by explicit admin PUTs through
-- the /api/category-budgets endpoint. First-boot-after-upgrade therefore has
-- nothing to scan and cannot enter the boot-loop failure mode that motivated
-- the Phase 3.4 backfill discipline. Migration numbering: 011 was API tokens;
-- this feature is 012.
--
-- Money is stored as integer cents (amount_cents), matching every other money
-- column post-migration-010. The float dollar amount the client supplies is
-- converted to cents once at the wire edge by the handler; no REAL column is
-- ever written. CHECK(amount_cents > 0) makes a zero/negative limit a write-
-- time failure rather than a silently-stored no-op — "no limit" is the ABSENCE
-- of a row (cleared via DELETE), never a row with amount_cents = 0.
--
-- category_id REFERENCES categories(id) ON DELETE CASCADE so deleting a
-- category transparently drops any limits pinned to it (consistent with the
-- cascade behaviour established in migration 002). The handler additionally
-- guards that the category is type='expense' before upserting, but the FK is
-- the durable invariant.
--
-- UNIQUE(year, month, category_id) makes the upsert target a single row per
-- (month, category) and lets ON CONFLICT(year, month, category_id) DO UPDATE
-- replace the amount in place. The supporting (year, month) index covers the
-- ListCategoryBudgetsByMonth read (the only list query) without scanning the
-- whole table.

CREATE TABLE category_budgets (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    year INTEGER NOT NULL,
    month INTEGER NOT NULL,
    category_id INTEGER NOT NULL REFERENCES categories(id) ON DELETE CASCADE,
    amount_cents INTEGER NOT NULL CHECK(amount_cents > 0),
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(year, month, category_id)
);

CREATE INDEX idx_category_budgets_year_month ON category_budgets(year, month);
