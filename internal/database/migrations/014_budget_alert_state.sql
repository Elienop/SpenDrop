-- 014_budget_alert_state.sql
-- Web Push over-budget alert latch.
--
-- A row in this table is a LATCH: its existence means "(category, year, month)
-- is currently in an alerted-over state — we have already pushed the
-- over-budget notification for this cell." The post-commit budget evaluator
-- inserts the row (ON CONFLICT DO NOTHING) the first time month-to-date spend
-- crosses the per-category limit and fans out a push; the DO-NOTHING means a
-- second mutation that leaves the cell still-over does NOT re-notify. When
-- spend later drops back under the limit the evaluator DELETEs the row,
-- re-arming the latch so a future re-crossing notifies again.
--
-- Purely additive CREATE TABLE, no backfill (contrast migration 008). Starts
-- empty; populated only by the in-process evaluator. category_id REFERENCES
-- categories(id) ON DELETE CASCADE so deleting a category drops its latches
-- (consistent with migration 012 category_budgets). CHECK(month BETWEEN 1 AND
-- 12) makes a bad month a write-time failure. UNIQUE(category_id, year, month)
-- is the latch key and the ON CONFLICT target. Migration numbering: 013 was
-- push_subscriptions.

CREATE TABLE budget_alert_state (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    category_id INTEGER NOT NULL REFERENCES categories(id) ON DELETE CASCADE,
    year INTEGER NOT NULL,
    month INTEGER NOT NULL CHECK(month BETWEEN 1 AND 12),
    notified_at DATETIME NOT NULL DEFAULT (datetime('now')),
    UNIQUE(category_id, year, month)
);
