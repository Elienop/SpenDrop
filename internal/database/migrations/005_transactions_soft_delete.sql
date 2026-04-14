-- Soft-delete for transactions (Phase 2.1 of the data-stewardship rollout).
--
-- We keep rows forever and mark them tombstoned via deleted_at instead of
-- DELETE FROM transactions. The audit log (migration 009) records the
-- deleted_at timestamp in after_json so operators can see who tombstoned
-- what, and a follow-up phase adds a trash view + restore endpoint.
--
-- Two partial indexes make the hot path fast without bloating the live
-- table:
--   - idx_transactions_deleted_at_null covers the overwhelmingly common
--     live-row scan (every list, export, and report query filters on
--     deleted_at IS NULL). It indexes only live rows so its size tracks
--     the live row count, not the total-rows-ever-written count.
--   - idx_transactions_deleted_at covers the trash view and purge job,
--     both of which sort by deleted_at DESC. It only indexes tombstoned
--     rows, so it stays tiny relative to the live table.
--
-- Forward-only: the Phase 4.1 pre-migration snapshot is the rollback
-- mechanism. Reverting this migration would require a manual ALTER TABLE
-- DROP COLUMN which SQLite does not support cleanly before 3.35, so the
-- snapshot is the supported recovery path.

ALTER TABLE transactions ADD COLUMN deleted_at DATETIME;

CREATE INDEX idx_transactions_deleted_at_null
    ON transactions(id) WHERE deleted_at IS NULL;

CREATE INDEX idx_transactions_deleted_at
    ON transactions(deleted_at) WHERE deleted_at IS NOT NULL;
