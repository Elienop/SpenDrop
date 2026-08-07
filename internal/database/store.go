package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Sentinel errors returned by TransactionStore.UpdateTx so a batch caller can
// distinguish skip-OK conditions (not found, tombstoned, owned by someone
// else) from hard errors that must roll back the entire batch. Handlers use
// errors.Is to map these to per-row "skipped" counts vs. an HTTP 500.
//
// These mirror the placement of ErrTokenNotFound in store_api_token.go: same
// package, exported, callers reach them as database.ErrTombstoned etc.
var (
	ErrTombstoned = errors.New("transaction is tombstoned")
	ErrNotOwned   = errors.New("transaction is not owned by the actor")
	ErrNotFound   = errors.New("transaction not found")
)

// Actor identifies who is performing a mutation and what authority they carry.
//
// It is a struct rather than a pair of positional arguments specifically
// because IsAdmin gates an authorization check: `UpdateTx(ctx, tx, uid, true,
// id, patch)` would compile cleanly while silently granting a full ownership
// bypass, and a bare `true` greps as noise. `database.Actor{UserID: user.ID,
// IsAdmin: true}` names the escalation at the call site and is greppable.
type Actor struct {
	// UserID is the acting user. Every audit row is attributed to it —
	// including cross-user admin edits, where the row's own user_id is
	// deliberately left untouched so an admin edit never re-homes a
	// member's transaction.
	UserID int64

	// IsAdmin lifts the row-OWNERSHIP restriction and nothing else, per
	// design spec §3.9 ("Admin bypass matches existing transaction
	// handlers" — handleUpdateTransaction, handleBatchDeleteTransactions,
	// and update-by-filter all drop the user_id predicate for RoleAdmin).
	// Tombstoned and missing rows are checked FIRST and stay skip-worthy
	// for every role, so an admin can never resurrect a soft-deleted row
	// through this path.
	IsAdmin bool
}

// Audit actions recorded by TransactionStore. These string literals MUST
// match the CHECK constraint in migrations/009_transaction_audit.sql. Adding
// a new action requires editing both places in lockstep.
//
// AuditRestore is reserved for the trash/restore UI that follows the
// Phase 2.1 soft-delete migration. Phase 2.1 ships the soft-delete
// semantics on Delete / DeleteTx (this file) and the five new trash
// queries in queries.sql (ListDeleted, CountDeleted, Restore, Purge,
// CountAll), but does NOT yet add a TransactionStore.Restore method or
// a HTTP handler that would emit this action. Once the restore endpoint
// lands, the new code path will call qtx.RestoreTransaction and emit a
// row with this action.
const (
	AuditInsert  = "insert"
	AuditUpdate  = "update"
	AuditDelete  = "delete"
	AuditRestore = "restore"
)

// BulkAuditTransactionID is the sentinel transaction_id used for summary
// audit rows written by bulk operations (bulk rename, delete-by-filter).
// A single audit row with this ID carries {"bulk":true,"count":N,"filter":...}
// in before_json; per-row audit would balloon the table for endpoints that
// routinely touch tens of thousands of rows. Documented in the column
// comment on transaction_audit.transaction_id.
const BulkAuditTransactionID int64 = 0

// TransactionStore routes every mutation of the `transactions` table through
// a single chokepoint that appends a row to `transaction_audit` inside the
// same SQL transaction as the mutation. An audit row exists if and only if
// the mutation committed — a rollback of either rolls back both.
//
// The store holds a *sql.DB and a *Queries. Single-row methods (Create,
// Update, Delete) open and commit their own short-lived transaction.
// The *Tx suffixed variants (CreateTx, DeleteTx, RecordBulkTx) take an
// externally managed *sql.Tx so batch and bulk handlers can attach the
// audit row to an existing transaction they already own.
type TransactionStore struct {
	db *sql.DB
	q  *Queries
}

// NewTransactionStore constructs a chokepoint bound to the given database
// handle. Callers pass the same *sql.DB and *Queries that they use for
// everything else; the store never opens a second connection.
func NewTransactionStore(db *sql.DB, q *Queries) *TransactionStore {
	return &TransactionStore{db: db, q: q}
}

// Create inserts a transaction and writes its audit row atomically. The
// `actorID` parameter is the authenticated user performing the mutation; a
// zero or negative value maps to a NULL actor_user_id (used for internal
// or system-originated inserts like background imports).
//
// before_json is NULL (there is nothing before an insert). after_json is
// the freshly inserted row as JSON.
func (s *TransactionStore) Create(ctx context.Context, actorID int64, p CreateTransactionParams) (Transaction, error) {
	var created Transaction
	err := s.withTx(ctx, func(qtx *Queries) error {
		t, err := qtx.CreateTransaction(ctx, p)
		if err != nil {
			return fmt.Errorf("create transaction: %w", err)
		}
		created = t
		return writeInsertAudit(ctx, qtx, actorID, t)
	})
	return created, err
}

// CreateTx performs Create inside a caller-owned *sql.Tx. Used by the batch
// insert handler so that N data rows + N audit rows share one commit: a
// failure halfway through the batch rolls back everything, including every
// audit row written up to that point.
func (s *TransactionStore) CreateTx(ctx context.Context, tx *sql.Tx, actorID int64, p CreateTransactionParams) (Transaction, error) {
	qtx := s.q.WithTx(tx)
	t, err := qtx.CreateTransaction(ctx, p)
	if err != nil {
		return Transaction{}, fmt.Errorf("create transaction: %w", err)
	}
	if err := writeInsertAudit(ctx, qtx, actorID, t); err != nil {
		return Transaction{}, err
	}
	return t, nil
}

// foreignMoneyUnchanged reports whether an update merely restates the foreign
// money the row already carries — same currency code, same original amount to
// the cent — in which case the base-currency value stored on the row must be
// carried forward verbatim instead of being re-derived from today's rate.
//
// A foreign-currency row records the foreign amount and what it was worth in
// the base currency, but NOT the rate that produced that number; no migration
// defines such a column. The api layer's resolveCurrency divides by the CURRENT
// rate on every call, and PUT /transactions/{id} is a full replace — the inline
// row editor rebuilds original_amount / original_currency from the stored row
// and resends them on every save (web/src/lib/currency.ts, toEditDefaults +
// toCreatePayload). So once the rate moved, editing only the description of a
// 1,500,000 LBP row silently re-priced it: $16.85 became $15.00 while the LBP
// figure never changed. Saving an edit must never move money the user did not
// touch.
//
// It deliberately does not freeze everything. A corrected foreign amount
// (1,500,000 -> 1,600,000), a switch to a different foreign currency, and a
// switch to or from the base currency all keep the caller's recomputed amount,
// because each of those IS the user changing the money. Re-pricing a corrected
// foreign amount at today's rate is the only option available — the rate the
// row was booked at is recorded nowhere. Snapshotting it per row is backlog B1
// step 2, folded into the refunds migration; this is step 1.
//
// KNOWN LIMITATION, and it is one-way. Once a foreign row exists, no re-save
// can ever move its base value again while the foreign amount and currency code
// stay put. If a rate is typed wrong (89,000 entered as 8,900), every row
// booked under it holds a 10x wrong base value that correcting the rate will
// NOT repair. The escape hatch is to change the foreign amount, save, change it
// back, and save again — undiscoverable, and deliberately not surfaced as an
// affordance here: a "re-price these rows" action is an owner decision, and
// rebuilding it as an automatic sweep is precisely the effective-dated rate
// table that was rejected for creating a second source of truth.
//
// Why the decision lives in the store, on `before`, and not in the handler:
// handleUpdateTransaction reads the row OUTSIDE any transaction for its
// tombstone and ownership checks, so that copy can be stale about money by the
// time this transaction opens. Deriving the freeze there would let the store
// write a base value that corresponds to neither the request nor the row it
// just read inside the tx — and hashInputsMoved, which is derived from `before`,
// would then see a phantom move and clear content_hash. SetMaxOpenConns(1) does
// not close that window: the pool cap serialises statements, not a
// read-then-open-transaction sequence (the same reasoning the create-path hash
// probe documents in transaction_handlers.go). `before` is the only read that
// both decisions can safely share.
//
// Two details for a future reviewer:
//
//   - Only the last two comparisons are load-bearing. The Valid checks are
//     defensive: given the code comparison and the cents comparison that
//     follow, no mutation of them is killable by any test, because a NULL
//     column and a set one already disagree on one of those two. Do not record
//     them as covered.
//   - The currency code is compared EXACTLY and no writer normalizes its case
//     (the import path stores whatever the spreadsheet's currency column says,
//     verbatim). A sheet spelling "lbp" therefore yields a row this predicate
//     can never match, so every save of it re-prices. Normalizing here alone
//     would not fix that — the row would still fail to match on the next
//     import — so it belongs with a data cleanup, not here.
func foreignMoneyUnchanged(before GetTransactionByIDRow, after UpdateTransactionParams) bool {
	if !after.OriginalCurrency.Valid || !after.OriginalAmountCents.Valid {
		return false
	}
	if !before.OriginalCurrency.Valid || !before.OriginalAmountCents.Valid {
		return false
	}
	if after.OriginalCurrency.String != before.OriginalCurrency.String {
		return false
	}
	return after.OriginalAmountCents.Int64 == before.OriginalAmountCents.Int64
}

// Update loads the before row, applies the UPDATE, loads the after row, and
// appends an audit row — all inside a single transaction. The before/after
// reads are issued against the tx so they see a consistent view even under
// concurrent writers; pulling the before row outside the tx would race with
// other mutators.
//
// Two fields of p are DERIVED here rather than accepted from the caller —
// p.ClearContentHash always, and p.AmountCents on the one path described below.
// The store is the only place that holds both the pre-edit row and the
// post-edit params inside one tx, so deciding either question anywhere else
// would race its own read. See hashInputsMoved and foreignMoneyUnchanged.
//
// The two derivations are ordered, and the order is load-bearing: the freeze
// runs FIRST so the hash decision sees the amount that will actually be
// stored. Reversed, a rate change alone would report "amount moved", clear
// content_hash, and drop the row out of import dedupe — the exact failure the
// freeze exists to prevent, reintroduced one line later.
func (s *TransactionStore) Update(ctx context.Context, actorID int64, p UpdateTransactionParams) error {
	return s.withTx(ctx, func(qtx *Queries) error {
		before, err := qtx.GetTransactionByID(ctx, p.ID)
		if err != nil {
			return fmt.Errorf("load before: %w", err)
		}
		if foreignMoneyUnchanged(before, p) {
			p.AmountCents = before.AmountCents
		}
		p.ClearContentHash = hashInputsMoved(before, p)
		if err := qtx.UpdateTransaction(ctx, p); err != nil {
			return fmt.Errorf("update transaction: %w", err)
		}
		after, err := qtx.GetTransactionByID(ctx, p.ID)
		if err != nil {
			return fmt.Errorf("load after: %w", err)
		}
		return writeUpdateAudit(ctx, qtx, actorID, p.ID, before, after)
	})
}

// UpdateTx applies a partial-field update to a single transaction inside a
// caller-owned *sql.Tx. The handler holds the tx so it can drive a multi-row
// batch under one transactional boundary: if any row in the batch returns a
// hard error, the deferred Rollback reverts every successful update + audit
// row that came earlier in the same loop.
//
// Returns sentinel errors (ErrNotFound, ErrTombstoned, ErrNotOwned) for the
// three skip-OK conditions so the caller can decide whether to skip-and-
// continue or roll back. Anything else is wrapped with fmt.Errorf and is a
// hard error.
//
// Per-row audit row is written via writeUpdateAudit inside this same tx —
// the handler does not write audit separately. If UpdateTx returns nil
// (success), the audit row is committed alongside the data update; if it
// returns any error, both are rolled back together.
//
// The Actor carries the caller's authority across this surface because the
// ownership predicate is evaluated HERE, against the `before` row this method
// already loads — making the handler re-read every row just to pre-check
// ownership would double the per-row query count for no gain. (batch-delete
// does the reverse and checks in the handler; it already loads the row there
// for its own tombstone check, so neither path is doing redundant work.)
func (s *TransactionStore) UpdateTx(
	ctx context.Context,
	tx *sql.Tx,
	actor Actor,
	id int64,
	patch UpdatePatch,
) (before, after GetTransactionByIDRow, err error) {
	qtx := s.q.WithTx(tx)

	before, err = qtx.GetTransactionByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return before, after, ErrNotFound
	}
	if err != nil {
		return before, after, fmt.Errorf("load before: %w", err)
	}
	if before.DeletedAt.Valid {
		return before, after, ErrTombstoned
	}
	if before.UserID != actor.UserID && !actor.IsAdmin {
		return before, after, ErrNotOwned
	}

	// Merge patch onto the existing row → UpdateTransactionParams (full-
	// replace shape). Every field on UpdateTransactionParams is copied from
	// `before` first so unset patch fields cannot zero-wipe the row; only
	// the explicitly-set patch fields override the snapshot.
	//
	// CRITICAL: Date in UpdateTransactionParams is time.Time. The patch's
	// *string Date is parsed via time.Parse("2006-01-02", …); validateDate
	// (Task 1, in the api layer) already enforces the format at the handler
	// boundary, so a parse error here is "should never happen" but is still
	// surfaced as a wrapped error so the caller's tx rolls back.
	//
	// AmountCents / OriginalAmountCents / OriginalCurrency are copied through
	// unchanged because the v1 batch-update patch deliberately does not expose
	// them — bulk amount edits would require currency conversion and cents
	// recomputation that the patch shape does not carry. The legacy REAL
	// amount / original_amount columns were dropped in migration 010 (Phase
	// 3.1b), so only the cents columns flow through.
	//
	// content_hash is cleared CONDITIONALLY, via the ClearContentHash flag set
	// below once the merge is complete. A patch can move date, description or
	// category_id — all hash inputs — and then the edited row must leave dedupe
	// rather than keep claiming the identity of content it no longer holds,
	// which would make a later import of that content skip a genuinely new row.
	// But a patch can equally carry only tags (the common batch-update shape,
	// up to MaxBatchUpdateIDs rows at a time), and clearing THOSE would drop a
	// whole selection out of dedupe and let a re-import double all of it. See
	// the long note on the UpdateTransaction query. The startup backfill
	// re-anchors a genuinely cleared row on the next boot.
	params := UpdateTransactionParams{
		ID:                  id,
		Date:                before.Date,
		AmountCents:         before.AmountCents,
		OriginalAmountCents: before.OriginalAmountCents,
		OriginalCurrency:    before.OriginalCurrency,
		Description:         before.Description,
		CategoryID:          before.CategoryID,
		Tags:                before.Tags,
		Notes:               before.Notes,
	}
	if patch.Date != nil {
		d, perr := time.Parse("2006-01-02", *patch.Date)
		if perr != nil {
			return before, after, fmt.Errorf("invalid date in patch (validateDate should have caught this): %w", perr)
		}
		params.Date = d
	}
	if patch.Description != nil {
		params.Description = *patch.Description
	}
	if patch.CategoryID != nil {
		params.CategoryID = *patch.CategoryID
	}
	if patch.Tags != nil {
		// The handler computes the per-row "merged" tags string via
		// applyTagsMode (Add / Remove / Replace set arithmetic) BEFORE
		// calling UpdateTx. Here we just assign — the store stays dumb
		// about set arithmetic. An empty string maps to NULL via Valid:false,
		// matching the column's nullable contract.
		params.Tags = sql.NullString{String: *patch.Tags, Valid: *patch.Tags != ""}
	}

	// Derived from the merged params vs the tx-scoped `before` row — the same
	// single predicate Update uses, so the single-PUT and batch-update paths
	// cannot drift apart. Comparing VALUES rather than testing which patch keys
	// are present also means a patch that resets a field to what it already
	// held keeps the row anchored.
	params.ClearContentHash = hashInputsMoved(before, params)

	if err := qtx.UpdateTransaction(ctx, params); err != nil {
		return before, after, fmt.Errorf("update transaction: %w", err)
	}

	after, err = qtx.GetTransactionByID(ctx, id)
	if err != nil {
		return before, after, fmt.Errorf("load after: %w", err)
	}
	if err := writeUpdateAudit(ctx, qtx, actor.UserID, id, before, after); err != nil {
		return before, after, fmt.Errorf("write audit: %w", err)
	}
	return before, after, nil
}

// Delete soft-deletes a transaction: it loads the live row, flips
// deleted_at via SoftDeleteTransaction, reloads the now-tombstoned row, and
// writes a single audit row carrying both pre-tombstone and post-tombstone
// state. The two reads are issued against the same tx so the after_json
// reflects exactly what the UPDATE wrote, even under concurrent mutators.
//
// Idempotency: if the row is already tombstoned when Delete is called
// (racing caller, retry after a dropped response), Delete returns nil
// without writing another audit row. The tombstone was already recorded
// by whichever call actually flipped deleted_at. Emitting a second
// "delete" audit row for an already-tombstoned row would be noise that
// makes forensic reads harder, not easier. Not-found rows still error
// out via GetTransactionByID's sql.ErrNoRows return — idempotency is
// scoped to tombstone races, not missing IDs.
func (s *TransactionStore) Delete(ctx context.Context, actorID int64, id int64) error {
	return s.withTx(ctx, func(qtx *Queries) error {
		before, err := qtx.GetTransactionByID(ctx, id)
		if err != nil {
			return fmt.Errorf("load before: %w", err)
		}
		if before.DeletedAt.Valid {
			return nil
		}
		if err := qtx.SoftDeleteTransaction(ctx, id); err != nil {
			return fmt.Errorf("soft-delete transaction: %w", err)
		}
		after, err := qtx.GetTransactionByID(ctx, id)
		if err != nil {
			return fmt.Errorf("load after: %w", err)
		}
		return writeDeleteAudit(ctx, qtx, actorID, id, before, after)
	})
}

// DeleteTx performs Delete inside a caller-owned *sql.Tx. Used by the batch
// delete handler so that up to MaxBatchDeleteIDs deletes + N audit rows all
// share one commit. Same idempotency contract as Delete: already-tombstoned
// rows are a silent no-op inside the caller's tx.
func (s *TransactionStore) DeleteTx(ctx context.Context, tx *sql.Tx, actorID int64, id int64) error {
	qtx := s.q.WithTx(tx)
	before, err := qtx.GetTransactionByID(ctx, id)
	if err != nil {
		return fmt.Errorf("load before: %w", err)
	}
	if before.DeletedAt.Valid {
		return nil
	}
	if err := qtx.SoftDeleteTransaction(ctx, id); err != nil {
		return fmt.Errorf("soft-delete transaction: %w", err)
	}
	after, err := qtx.GetTransactionByID(ctx, id)
	if err != nil {
		return fmt.Errorf("load after: %w", err)
	}
	return writeDeleteAudit(ctx, qtx, actorID, id, before, after)
}

// Restore reverses a prior soft-delete: it loads the tombstoned row,
// calls RestoreTransaction to clear deleted_at, reloads the now-live
// row, and writes a paired "restore" audit row — all inside one SQL
// transaction. The two reads are issued against the same tx so the
// after_json reflects exactly what the UPDATE wrote, even under
// concurrent mutators.
//
// Race contract: if the row is not tombstoned when Restore is called
// (racing admin, retry after a dropped response that already succeeded,
// purge slipping in between the handler's TOCTOU check and the store
// call), Restore returns sql.ErrNoRows. The handler maps that to a
// 404, which matches the contract from the user's perspective: "the
// id you asked to restore is not in the trash." A sentinel error is
// correct here because a silent no-op would leave the client's trash
// view stale and the user would keep seeing a row that is actually
// live.
func (s *TransactionStore) Restore(ctx context.Context, actorID int64, id int64) error {
	return s.withTx(ctx, func(qtx *Queries) error {
		before, err := qtx.GetTransactionByID(ctx, id)
		if err != nil {
			return fmt.Errorf("load before: %w", err)
		}
		if !before.DeletedAt.Valid {
			return sql.ErrNoRows
		}
		if err := qtx.RestoreTransaction(ctx, id); err != nil {
			return fmt.Errorf("restore transaction: %w", err)
		}
		after, err := qtx.GetTransactionByID(ctx, id)
		if err != nil {
			return fmt.Errorf("load after: %w", err)
		}
		return writeRestoreAudit(ctx, qtx, actorID, id, before, after)
	})
}

// RestoreTx performs Restore inside a caller-owned *sql.Tx. Used by
// the batch restore handler so that up to MaxBatchDeleteIDs restores
// + N audit rows all share one commit. Same race contract as Restore:
// a not-tombstoned row returns sql.ErrNoRows and the caller decides
// whether to abort the whole batch or continue with the remaining IDs.
func (s *TransactionStore) RestoreTx(ctx context.Context, tx *sql.Tx, actorID int64, id int64) error {
	qtx := s.q.WithTx(tx)
	before, err := qtx.GetTransactionByID(ctx, id)
	if err != nil {
		return fmt.Errorf("load before: %w", err)
	}
	if !before.DeletedAt.Valid {
		return sql.ErrNoRows
	}
	if err := qtx.RestoreTransaction(ctx, id); err != nil {
		return fmt.Errorf("restore transaction: %w", err)
	}
	after, err := qtx.GetTransactionByID(ctx, id)
	if err != nil {
		return fmt.Errorf("load after: %w", err)
	}
	return writeRestoreAudit(ctx, qtx, actorID, id, before, after)
}

// Purge hard-deletes a tombstoned row from the transactions table.
// This is the only path in the codebase that physically removes a
// transaction; every other delete flows through Delete / DeleteTx
// which flips deleted_at.
//
// Purge does NOT write an audit row. The original soft-delete audit
// row already captures who tombstoned the data and when, the row's
// pre-tombstone state is preserved in that audit row's before_json,
// and the audit table has no FK to transactions (see 009_transaction_audit.sql)
// specifically so that the purge cannot orphan its own delete history.
// From the auditability standpoint, a purge is the continuation of
// the soft-delete that already lives in the log — a second audit row
// would require extending the CHECK constraint to allow 'purge',
// which needs a full table rewrite migration in SQLite. The plan
// explicitly scopes that to a future phase; this one inherits the
// existing four-action constraint.
//
// Race contract (identical to Restore): if the row is not tombstoned,
// Purge returns sql.ErrNoRows so the handler can return 404. A
// not-found row (no row with that id at all) also returns sql.ErrNoRows
// via GetTransactionByID.
//
// We do the load-then-delete inside a single tx so a concurrent
// restore racing with a purge cannot leave the transaction in a
// half-state: the tx's snapshot of deleted_at is what PurgeTransaction's
// `AND deleted_at IS NOT NULL` guard sees, and either the purge
// succeeds (row is gone) or the whole tx rolls back.
func (s *TransactionStore) Purge(ctx context.Context, id int64) error {
	return s.withTx(ctx, func(qtx *Queries) error {
		before, err := qtx.GetTransactionByID(ctx, id)
		if err != nil {
			return fmt.Errorf("load before: %w", err)
		}
		if !before.DeletedAt.Valid {
			return sql.ErrNoRows
		}
		if err := qtx.PurgeTransaction(ctx, id); err != nil {
			return fmt.Errorf("purge transaction: %w", err)
		}
		return nil
	})
}

// BulkAuditSummary describes a bulk mutation for the single summary audit
// row written by RecordBulkTx. Count is the number of data rows affected;
// Filter is a human-readable description of which rows (the LIKE pattern, a
// serialised filter-query-param set, etc.) for operator forensics.
type BulkAuditSummary struct {
	Count  int64
	Filter string
}

// UpdatePatch is the bulk-edit patch shape consumed by TransactionStore.UpdateTx.
// Pointer fields mean "unset = do not touch" (matching JSON-omitempty
// semantics on the wire). Both batch-update and update-by-filter use this
// struct; the api-layer patchRequest is the wire-format shape that
// buildUpdatePatch lifts into UpdatePatch.
type UpdatePatch struct {
	Date        *string
	Description *string
	CategoryID  *int64
	Tags        *string
	TagsMode    *string // required iff Tags != nil ("add" / "remove" / "replace")
}

// IsEmpty reports whether the patch carries no field changes. Used as the
// final "is the request actually doing anything?" gate before we dispatch.
func (p UpdatePatch) IsEmpty() bool {
	return p.Date == nil && p.Description == nil && p.CategoryID == nil && p.Tags == nil
}

// RecordBulkTx writes exactly one summary audit row for a bulk mutation
// that touched N rows. The row has transaction_id = BulkAuditTransactionID
// and its before_json carries {"bulk":true,"count":N,"filter":...}. This is
// the deliberate fidelity/perf trade documented in the plan: per-row diffs
// for an endpoint that can rename tens of thousands of rows would balloon
// the audit table and slow the operation the endpoint exists to serve.
//
// The caller owns the *sql.Tx and is responsible for Commit / Rollback;
// this function only issues the INSERT.
func (s *TransactionStore) RecordBulkTx(ctx context.Context, tx *sql.Tx, actorID int64, action string, summary BulkAuditSummary) error {
	payload, err := json.Marshal(map[string]any{
		"bulk":   true,
		"count":  summary.Count,
		"filter": summary.Filter,
	})
	if err != nil {
		return fmt.Errorf("marshal bulk summary: %w", err)
	}
	qtx := s.q.WithTx(tx)
	return qtx.InsertTransactionAudit(ctx, InsertTransactionAuditParams{
		TransactionID: BulkAuditTransactionID,
		Action:        action,
		ActorUserID:   actorNullInt64(actorID),
		BeforeJson:    sql.NullString{String: string(payload), Valid: true},
		AfterJson:     sql.NullString{},
	})
}

// withTx is the single place where tx lifecycle lives for the single-row
// methods. It opens a tx, defers Rollback (a no-op after a successful
// commit), runs fn with the scoped Queries, and commits on success.
func (s *TransactionStore) withTx(ctx context.Context, fn func(*Queries) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()
	if err := fn(s.q.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit()
}

// writeInsertAudit marshals an inserted row as after_json and writes a
// single insert audit row. Failing to marshal is a bug-class error (not an
// operator-facing error), but we still surface it so the caller's tx rolls
// back rather than committing a data row without its audit.
func writeInsertAudit(ctx context.Context, qtx *Queries, actorID int64, t Transaction) error {
	afterJSON, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("marshal insert audit after: %w", err)
	}
	return qtx.InsertTransactionAudit(ctx, InsertTransactionAuditParams{
		TransactionID: t.ID,
		Action:        AuditInsert,
		ActorUserID:   actorNullInt64(actorID),
		BeforeJson:    sql.NullString{},
		AfterJson:     sql.NullString{String: string(afterJSON), Valid: true},
	})
}

// writeUpdateAudit marshals the before and after rows returned by
// GetTransactionByID (which include the joined category_type column) and
// writes a single update audit row.
func writeUpdateAudit(ctx context.Context, qtx *Queries, actorID int64, id int64, before, after GetTransactionByIDRow) error {
	beforeJSON, err := json.Marshal(before)
	if err != nil {
		return fmt.Errorf("marshal update audit before: %w", err)
	}
	afterJSON, err := json.Marshal(after)
	if err != nil {
		return fmt.Errorf("marshal update audit after: %w", err)
	}
	return qtx.InsertTransactionAudit(ctx, InsertTransactionAuditParams{
		TransactionID: id,
		Action:        AuditUpdate,
		ActorUserID:   actorNullInt64(actorID),
		BeforeJson:    sql.NullString{String: string(beforeJSON), Valid: true},
		AfterJson:     sql.NullString{String: string(afterJSON), Valid: true},
	})
}

// writeDeleteAudit marshals the pre-tombstone and post-tombstone rows and
// writes a single delete audit row. before_json is the live row (deleted_at
// NULL), after_json is the same row with deleted_at populated — forensic
// readers can diff the two to confirm the only field that changed between
// the pre-delete snapshot and the tombstone was deleted_at (plus the
// updated_at touch that the soft-delete query also bumps).
func writeDeleteAudit(ctx context.Context, qtx *Queries, actorID int64, id int64, before, after GetTransactionByIDRow) error {
	// Guard against caller bugs: a delete audit must always describe the
	// transition live -> tombstoned. If before is already tombstoned, the
	// caller passed the wrong row; if after is still live, the soft-delete
	// SQL never ran (or ran against a different row). Either case would
	// produce an audit pair that silently misrepresents what happened, so
	// we fail loudly rather than write a misleading row.
	if before.DeletedAt.Valid {
		return fmt.Errorf("writeDeleteAudit: before snapshot already tombstoned (id=%d) — caller passed the wrong row", id)
	}
	if !after.DeletedAt.Valid {
		return fmt.Errorf("writeDeleteAudit: after snapshot still live (id=%d) — soft-delete did not run before the audit re-read", id)
	}
	beforeJSON, err := json.Marshal(before)
	if err != nil {
		return fmt.Errorf("marshal delete audit before: %w", err)
	}
	afterJSON, err := json.Marshal(after)
	if err != nil {
		return fmt.Errorf("marshal delete audit after: %w", err)
	}
	return qtx.InsertTransactionAudit(ctx, InsertTransactionAuditParams{
		TransactionID: id,
		Action:        AuditDelete,
		ActorUserID:   actorNullInt64(actorID),
		BeforeJson:    sql.NullString{String: string(beforeJSON), Valid: true},
		AfterJson:     sql.NullString{String: string(afterJSON), Valid: true},
	})
}

// writeRestoreAudit marshals the pre-restore (tombstoned) and post-restore
// (live) rows and writes a single restore audit row. before_json is the
// tombstoned row (deleted_at populated), after_json is the same row with
// deleted_at cleared — forensic readers can diff the two to confirm the
// only field that changed between the pre-restore snapshot and the live
// row was deleted_at (plus the updated_at touch that RestoreTransaction
// also bumps).
//
// Mirror of writeDeleteAudit: same paranoia guards, inverted — before
// MUST be tombstoned and after MUST be live. A misrouted call would
// otherwise produce an audit pair that silently misrepresents the
// transition, so we fail loudly rather than write a misleading row.
func writeRestoreAudit(ctx context.Context, qtx *Queries, actorID int64, id int64, before, after GetTransactionByIDRow) error {
	if !before.DeletedAt.Valid {
		return fmt.Errorf("writeRestoreAudit: before snapshot not tombstoned (id=%d) — caller passed the wrong row", id)
	}
	if after.DeletedAt.Valid {
		return fmt.Errorf("writeRestoreAudit: after snapshot still tombstoned (id=%d) — restore did not run before the audit re-read", id)
	}
	beforeJSON, err := json.Marshal(before)
	if err != nil {
		return fmt.Errorf("marshal restore audit before: %w", err)
	}
	afterJSON, err := json.Marshal(after)
	if err != nil {
		return fmt.Errorf("marshal restore audit after: %w", err)
	}
	return qtx.InsertTransactionAudit(ctx, InsertTransactionAuditParams{
		TransactionID: id,
		Action:        AuditRestore,
		ActorUserID:   actorNullInt64(actorID),
		BeforeJson:    sql.NullString{String: string(beforeJSON), Valid: true},
		AfterJson:     sql.NullString{String: string(afterJSON), Valid: true},
	})
}

// actorNullInt64 maps an actor user ID to the audit row's actor_user_id
// column. IDs <= 0 become NULL, which is how the table records "system" or
// "unknown actor" — the column is ON DELETE SET NULL so the historical fact
// that somebody (even if we no longer know who) performed the mutation
// survives account deletion.
//
// CALLER RESPONSIBILITY: handlers reached via an authenticated HTTP request
// MUST pass their own user.ID here, and the auth middleware already
// guarantees that value is a positive integer. This helper deliberately
// does NOT panic on a zero or negative actor because the NULL mapping is
// legitimate for system-originated writes (background imports, one-shot
// migrations that predate auth). The consequence: a handler that
// accidentally passes a zero-valued actor will silently produce an
// unattributable audit row instead of a loud failure. When adding a new
// call site for TransactionStore.* methods, assert your actor ID
// upstream; do not rely on the silent NULL mapping as a fallback.
func actorNullInt64(id int64) sql.NullInt64 {
	if id <= 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: id, Valid: true}
}
