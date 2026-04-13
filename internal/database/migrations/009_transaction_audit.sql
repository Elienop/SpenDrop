-- 009_transaction_audit.sql
-- Append-only mutation log for the transactions table. Every row in this
-- table is written by the TransactionStore chokepoint in
-- internal/database/store.go inside the same SQL transaction as the
-- mutation, so an audit row exists if and only if the mutation committed.
-- NEVER UPDATEd or DELETEd by application code (apart from a future manual
-- `spendrop audit prune` utility).
--
-- Migration numbering leaves 005..008 reserved for Tier 2 (soft-delete,
-- restore endpoint, etc.) which is expected to ship between the current
-- Tier 1 / Phase 4.1 stack and this phase. The 'restore' CHECK value below
-- is dormant until Tier 2 ships the restore code path.

CREATE TABLE IF NOT EXISTS transaction_audit (
    -- transaction_id is the target row. For BULK operations it is set to 0,
    -- a sentinel meaning "one audit row summarises N transactions"; the
    -- count and filter live in before_json. Per-row audit would balloon
    -- the audit table for bulk rename / bulk delete endpoints which can
    -- touch tens of thousands of rows in a single call.
    -- There is NO FOREIGN KEY on this column on purpose: a hard delete
    -- must not be allowed to orphan its own audit history.
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    transaction_id INTEGER NOT NULL,
    action TEXT NOT NULL CHECK(action IN ('insert','update','delete','restore')),
    -- actor_user_id is ON DELETE SET NULL rather than CASCADE: the
    -- historical fact that somebody changed this row outlives the user
    -- account being purged. Audit history survives account deletion.
    actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    occurred_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    before_json TEXT,
    after_json TEXT
);

CREATE INDEX idx_transaction_audit_txn ON transaction_audit(transaction_id);
CREATE INDEX idx_transaction_audit_time ON transaction_audit(occurred_at);
