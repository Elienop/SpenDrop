-- 015_notification_settings.sql
-- Household-wide Web Push notification preferences (single-row table).
--
-- This is ONE household row, not per-user: SpenDrop's push fan-out is a
-- household signal (same visibility model as transactions/categories/budget
-- alerts). id INTEGER PRIMARY KEY CHECK (id = 1) makes "single row" a
-- schema-enforced invariant — there is exactly one settings row and it lives
-- at id=1, so reads are an unconditional `WHERE id = 1` and never need a
-- lazy-create. We SEED that row here with the defaults so a fresh install and
-- every upgrade already have it: over_budget stays ON (matches the existing
-- migration-014 latch behaviour), the four activity types default OFF (opt-in,
-- so an upgrade does not suddenly start buzzing every device on every entry),
-- large_txn_threshold_cents defaults to 50000 (=$500.00). Purely additive, no
-- backfill (contrast migration 008). Migration numbering: 014 was
-- budget_alert_state.

CREATE TABLE notification_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    over_budget INTEGER NOT NULL DEFAULT 1,
    txn_added INTEGER NOT NULL DEFAULT 0,
    txn_deleted INTEGER NOT NULL DEFAULT 0,
    txn_edited INTEGER NOT NULL DEFAULT 0,
    large_txn INTEGER NOT NULL DEFAULT 0,
    large_txn_threshold_cents INTEGER NOT NULL DEFAULT 50000,
    updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

-- Seed the single household row so every read is a plain WHERE id = 1.
INSERT INTO notification_settings (id) VALUES (1);
