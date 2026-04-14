-- 007_balance_checkpoints.sql
-- Phase 3.3 of the data-stewardship rollout: Beancount-style balance
-- checkpoints. A checkpoint is a user-asserted claim of the form "on date D,
-- the sum of <scope> equals Y cents". The application re-runs the underlying
-- SUM() query on demand and records whether the claim still holds. The
-- comparison is always integer-vs-integer (expected_amount_cents vs
-- SUM(amount_cents)) so there is no float tolerance to tune.
--
-- Why this lives next to soft-delete + cents (006): the verification query
-- filters t.deleted_at IS NULL so tombstoned rows are correctly ignored
-- (Phase 2.1 invariant), and it sums the int64 amount_cents column
-- introduced in migration 006 so aggregation is exact by construction.
-- Shipping this phase any earlier would have required either a float
-- tolerance band or a second audit pass against the cents columns.
--
-- scope_type / scope_id / scope_label encode one of three claim shapes:
--
--   * 'total'    — whole-ledger sum up to the date; scope_id and scope_label
--                  are both NULL. Matches every live transaction with
--                  t.date <= checkpoint.date.
--   * 'category' — per-category sum; scope_id is the target categories.id,
--                  scope_label is NULL. CASCADE delete ensures a deleted
--                  category takes its checkpoints with it — orphan
--                  checkpoints referencing a gone category have no
--                  reconstruction path and would just permanently mismatch.
--   * 'tag'      — per-tag sum; scope_label is the literal tag string
--                  (SQLite TEXT is byte-exact so case matters), scope_id is
--                  NULL. No FK on scope_label because tags are CSV-encoded
--                  in transactions.tags, not a normalized table — the
--                  verification query matches via
--                  `',' || t.tags || ',' LIKE '%,' || scope_label || ',%'`.
--                  Assumption: tag values do NOT contain embedded commas,
--                  which is enforced at the API write layer today and
--                  documented in the tags CSV contract. If that invariant
--                  is ever loosened, this phase's verification regresses
--                  silently into a superset match.
--
-- The CHECK constraint on scope_type pins the enum at the SQL layer so a
-- handler bug that sends a bogus scope_type cannot land in the DB. We do
-- NOT add a companion CHECK to enforce the "scope_id populated iff
-- scope_type='category'" invariant because SQLite CHECK on correlated
-- nullability is ugly and the handler always sets them in lockstep — the
-- handler tests cover the missing-scope-id case explicitly.
--
-- expected_amount_cents is INTEGER NOT NULL — Phase 3.1a discipline. No
-- REAL column, no dollars-at-rest, no CAST(ROUND(...)) patches in reader
-- paths. The value arrives from the POST body already converted at the
-- wire edge and never touches a float again.
--
-- last_verified_at / last_verification_status are nullable because a
-- freshly-inserted row that hits a transient DB error during its initial
-- verification should still exist so the user's UX sees "pending" and a
-- manual re-verify button, not a vanished save. The CHECK pins the three
-- legal states; a NULL status means "created but verification has not
-- run to completion even once", which is the legitimate state between
-- INSERT and UpdateCheckpointVerification on the hot creation path.
--
-- Indexes:
--   * idx_balance_checkpoints_user covers the trivial "list my
--     checkpoints" query and the user-scoped dashboard banner query.
--   * idx_balance_checkpoints_date covers the hot "which checkpoints
--     does this mutation or import potentially affect?" path — the
--     verification hook after transaction CRUD and after import confirm
--     runs a SELECT id FROM balance_checkpoints WHERE date >= MIN(...)
--     which is the whole point of keeping the index on `date`.

CREATE TABLE balance_checkpoints (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    scope_type TEXT NOT NULL CHECK(scope_type IN ('total', 'category', 'tag')),
    scope_id INTEGER REFERENCES categories(id) ON DELETE CASCADE,
    scope_label TEXT,
    date DATE NOT NULL,
    expected_amount_cents INTEGER NOT NULL,
    note TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_verified_at DATETIME,
    last_verification_status TEXT CHECK(last_verification_status IN ('ok', 'mismatch', 'pending'))
);

CREATE INDEX idx_balance_checkpoints_user ON balance_checkpoints(user_id);
CREATE INDEX idx_balance_checkpoints_date ON balance_checkpoints(date);
