package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

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

// Update loads the before row, applies the UPDATE, loads the after row, and
// appends an audit row — all inside a single transaction. The before/after
// reads are issued against the tx so they see a consistent view even under
// concurrent writers; pulling the before row outside the tx would race with
// other mutators.
func (s *TransactionStore) Update(ctx context.Context, actorID int64, p UpdateTransactionParams) error {
	return s.withTx(ctx, func(qtx *Queries) error {
		before, err := qtx.GetTransactionByID(ctx, p.ID)
		if err != nil {
			return fmt.Errorf("load before: %w", err)
		}
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

// BulkAuditSummary describes a bulk mutation for the single summary audit
// row written by RecordBulkTx. Count is the number of data rows affected;
// Filter is a human-readable description of which rows (the LIKE pattern, a
// serialised filter-query-param set, etc.) for operator forensics.
type BulkAuditSummary struct {
	Count  int64
	Filter string
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
