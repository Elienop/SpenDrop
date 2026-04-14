-- Phase 3.1a of the data-stewardship rollout: replace every monetary REAL
-- column with an INTEGER cents column, additively. The legacy REAL columns
-- stay in place for this release so rollback is a no-op binary revert; the
-- destructive drop is deferred to migration 010 (Phase 3.1b) at the very
-- end of the data-stewardship plan, after the Tier 3 test harness has
-- soaked for a full release cycle on the cents path.
--
-- Why this matters: SQLite REAL is IEEE-754 binary64, and values like 19.99
-- or 89000.42 cannot be represented exactly. Every SUM() across transactions
-- accumulates rounding error, which already shows up today as scattered
-- `math.Round(x*100)/100` patches in dashboard_handlers.go and
-- reports_new_handlers.go - each one a smell that floats are leaking through
-- aggregation paths. Integer cents make the entire class impossible by
-- construction: every aggregation is int64 + int64 and the conversion to
-- dollars happens exactly once, at the JSON wire edge.
--
-- Dual-write contract: between this migration and migration 010, every
-- writer populates BOTH the legacy REAL column and the new _cents column.
-- Every reader consumes _cents only. The wire format is unchanged - the
-- response serializer divides by 100 at the edge so the frontend sees no
-- diff and no frontend migration is in flight during this window.
--
-- NOT NULL DEFAULT 0 on amount_cents, budgets.amount_cents, and
-- savings_goals.target_amount_cents: SQLite's ADD COLUMN requires a
-- constant default when the column is NOT NULL. A default of 0 lets the
-- ALTER succeed on an already-populated table; the UPDATE statements
-- immediately below rewrite every row with the true backfilled value
-- inside the same migration transaction, so no reader ever observes the
-- zero state. The CHECK(amount_cents > 0) constraint is deliberately
-- deferred to migration 010 - adding it here would make the backfill
-- fragile if any legacy fixture row contained an amount that rounded to
-- zero cents (currently none, but we do not want the invariant to gate
-- the backfill itself).
--
-- original_amount_cents stays nullable because original_amount is
-- nullable today: only foreign-currency transactions populate it. The
-- CASE expression preserves the NULL pass-through.
--
-- Rounding: CAST(ROUND(x * 100) AS INTEGER) uses SQLite's round-half-away
-- -from-zero semantics, matching the way a human reads "$19.99 -> 1999
-- cents". Values like 19.995 round to 2000, not 1999 - which is the
-- behaviour we want for user-entered money.
--
-- Forward-only: reverting this migration requires a manual snapshot
-- restore. Phase 4.1 (shipped) takes a VACUUM INTO snapshot before
-- migrations run, so the recovery path exists. The snapshot filename
-- will embed this migration's version, matching migrate.go's targetVersion
-- derivation.

ALTER TABLE transactions ADD COLUMN amount_cents INTEGER NOT NULL DEFAULT 0;
ALTER TABLE transactions ADD COLUMN original_amount_cents INTEGER;

UPDATE transactions
SET amount_cents = CAST(ROUND(amount * 100) AS INTEGER),
    original_amount_cents = CASE
        WHEN original_amount IS NULL THEN NULL
        ELSE CAST(ROUND(original_amount * 100) AS INTEGER)
    END;

ALTER TABLE budgets ADD COLUMN amount_cents INTEGER NOT NULL DEFAULT 0;
UPDATE budgets SET amount_cents = CAST(ROUND(amount * 100) AS INTEGER);

ALTER TABLE savings_goals ADD COLUMN target_amount_cents INTEGER NOT NULL DEFAULT 0;
UPDATE savings_goals SET target_amount_cents = CAST(ROUND(target_amount * 100) AS INTEGER);
