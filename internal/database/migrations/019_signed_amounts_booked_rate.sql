-- 019_signed_amounts_booked_rate.sql
-- B10: signed amounts (refunds) + the per-row booked exchange rate.
--
-- Two changes ride ONE rebuild of transactions, deliberately: both alter the
-- table's shape, and a second rebuild is a second chance to lose a column.
--
-- 1. amount_cents becomes SIGNED. A refund is a NEGATIVE amount on an EXPENSE
--    row — not a new category type, not a separate table. categories.type
--    stays two-valued ('expense','income') and is untouched by this file.
--    A negative income row is likewise defined: it is an income reversal, and
--    it nets in income sums the same way. Reports sum signed cents, so a
--    refund offsets the category it was booked against, which is the whole
--    point (a $50 grocery purchase and a $20 refund read as $30 spent).
--
-- 2. booked_rate REAL NULL records the divisor that priced a foreign-currency
--    row at booking time. currencies.rate_to_base is overwritten in place by
--    the admin currency editor and no history table exists, so until this
--    column landed the rate a row was booked at was recorded NOWHERE — see
--    the freeze-on-edit note in internal/database/store.go, which exists
--    solely because that number was unrecoverable.
--
-- ZERO STAYS ILLEGAL, and the new CHECK says so explicitly rather than by
-- omission. Note `amount_cents INTEGER NOT NULL DEFAULT 0` below: today an
-- INSERT that forgets the amount takes the default, fails CHECK(> 0) and
-- errors loudly. Relaxing to a signed column WITHOUT a constraint — or with
-- one that admits 0 — would turn that same caller bug into a silently
-- persisted zero-amount row that every aggregate happily sums as nothing.
-- So the constraint moves from "> 0" to "!= 0": every value the old CHECK
-- accepted is still accepted (the new one is strictly weaker), no existing
-- row can be rejected by the copy below, and the omitted-amount bug stays a
-- hard error. original_amount_cents follows the same rule so a foreign-
-- currency refund is representable: unset (NULL) or non-zero, never 0.
--
-- Sign agreement between amount_cents and original_amount_cents is NOT a
-- schema constraint. On the conversion path it holds by arithmetic (dividing
-- by a positive rate preserves sign); a pair that disagrees can only arrive
-- from a source that supplies both numbers independently, which makes it a
-- boundary-validation question rather than a storage one. Encoding it as a
-- CHECK would also make correcting one column alone impossible in a single
-- statement.
--
-- NOT touched by this migration, and each for a reason:
--   * category_budgets.amount_cents CHECK(amount_cents > 0) (migration 012) —
--     the identical constraint text on a DIFFERENT table. A budget LIMIT of
--     zero or less is a write error and must stay rejected. A search-and-
--     relax pass keyed on the string `amount_cents > 0` hits it; this one
--     names its table.
--   * categories.type — see above, refunds need no new type.
--   * balance_checkpoints.expected_amount_cents — already unconstrained and
--     already sign-tolerant end to end.
--
-- Why a full table REBUILD (not ALTER TABLE): SQLite cannot add, drop or
-- alter a CHECK constraint in place. The rebuild is the established pattern
-- in this schema for any constraint change on transactions (migrations 002
-- and 010 both did it); this file follows 010's sequence exactly.
--
-- THE COPY LIST IS EXPLICIT AND IT IS THE DANGEROUS PART. `SELECT *` is not
-- used, and 010's list must not be reused: it names 14 columns, the table now
-- has 15 (migration 017 added idempotency_key), and the new table has 16.
-- A copy list that omits idempotency_key NULLs every key with NO error —
-- the partial unique index ignores NULLs — and silently retires the retry
-- double-post protection that index exists to provide. Count the names
-- against PRAGMA table_info before changing anything here.
--
-- content_hash values are copied VERBATIM and never recomputed. The digest
-- covers amount_cents, so recomputing over now-signed rows would mint new
-- identities, break the earliest-id-wins anchors future imports dedupe
-- against, and can collide with a live row mid-migration.
--
-- INHERITED, ACCEPTED: DROP TABLE deletes the table's sqlite_sequence row, so
-- the rebuilt table's AUTOINCREMENT high-water mark restarts at max(copied id).
-- Ids hard-purged from the trash before this upgrade become reissuable, and
-- transaction_audit keeps a purged row's history with no FK (009 header), so a
-- re-minted id would inherit an unrelated audit trail. Same property as the
-- 010 rebuild it copies; purge is a rare manual action at household scale, so
-- this is accepted with this note rather than a sqlite_sequence carry-forward
-- (which has its own empty-table and fully-purged edge cases).
--
-- booked_rate is NULL for every copied row and there is NO backfill. A legacy
-- row's rate is only lossily derivable from original_amount_cents/amount_cents
-- (cent-rounded, and simply wrong for imported rows, which were never priced
-- by any rate lookup at all — import stores both sheet columns verbatim).
-- NULL means "no rate recorded", which is the honest value for every row that
-- predates the column, for every imported row, and for every base-currency
-- row. Only the conversion path writes a rate, and only from the same call
-- that performed the division.
--
-- ADDENDUM (2026-08-15, import per-row rate stage): the paragraph above is
-- history, not current behaviour, and is left as written because a migration
-- records what was true when it ran. As of that stage an imported row DOES
-- book a rate — the one its own sheet quoted, in a `Rate` column — and stores
-- the household's canonical currency code rather than the sheet's spelling.
-- NULL remains the value for a label-only row (a foreign pair with no rate
-- quoted), for a base-currency row, and for every row that predates the
-- column; and it is still true that no rate is ever backfilled or inferred
-- from the stored pair.
--
-- Why NO `PRAGMA foreign_keys` toggle (and why adding one would be worse than
-- useless): this file runs inside the single transaction the migration runner
-- wraps every migration in (applyPendingMigrations in migrate.go), and the
-- pragma is a silent no-op inside an active transaction — it would read as a
-- safeguard while doing nothing. It is not needed either, because nothing
-- holds an INBOUND foreign key to transactions: transaction_audit deliberately
-- has none (migration 009), and balance_checkpoints references only users and
-- categories (migration 007). Re-verified against every migration file as of
-- this one. The table's own OUTBOUND FKs are copied verbatim below.
--
-- Correction to 010's header: it claims the copied FKs "are validated by
-- foreign_key_check after the rename". They are not — no `PRAGMA
-- foreign_key_check` exists in migrate.go or in any migration. That check
-- runs only in tests; see migration_019_test.go, which is the only guard
-- this rebuild has.
--
-- Forward-only. There is no DOWN migration: a signed row cannot be expressed
-- under the old CHECK, so reverting means restoring the pre-migration
-- snapshot the runner takes automatically before this file applies.

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
    amount_cents INTEGER NOT NULL DEFAULT 0 CHECK(amount_cents != 0),
    original_amount_cents INTEGER CHECK(original_amount_cents IS NULL OR original_amount_cents != 0),
    booked_rate REAL,
    content_hash TEXT,
    idempotency_key TEXT
);

-- 16 names on both sides, in the same order. booked_rate is the only one the
-- source table cannot supply.
INSERT INTO transactions_new (
    id, user_id, date, original_currency, description, category_id, tags, notes,
    created_at, updated_at, deleted_at, amount_cents, original_amount_cents,
    booked_rate, content_hash, idempotency_key
)
SELECT
    id, user_id, date, original_currency, description, category_id, tags, notes,
    created_at, updated_at, deleted_at, amount_cents, original_amount_cents,
    NULL, content_hash, idempotency_key
FROM transactions;

DROP TABLE transactions;
ALTER TABLE transactions_new RENAME TO transactions;

-- Recreate every index the rebuild dropped with the old table. That is now
-- SEVEN: the six migration 010 recreated (from migrations 002, 005 and 008)
-- plus migration 017's idempotency index, which did not exist when 010 was
-- written. A missing index here is a silent correctness bug for the two
-- unique ones and a silent performance bug for the rest.
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

-- Reproduced verbatim from migration 017, INCLUDING the absence of a
-- deleted_at clause. That divergence from the content_hash index above is
-- deliberate: a retry whose original row has since been trashed must still
-- collide so the replay creates nothing. Adding `AND deleted_at IS NULL`
-- here to "match" the index above resurrects deleted content under a new id.
CREATE UNIQUE INDEX idx_transactions_idempotency_key
    ON transactions(user_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
