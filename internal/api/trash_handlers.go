package api

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/database"
)

// deletedTransactionResponse is the JSON shape returned by the trash-view
// endpoints. It is the same as transactionResponse plus a DeletedAt
// timestamp so the frontend can render "deleted <N> minutes ago" next
// to each row. We do not reuse transactionResponse because omitempty
// on a zero string would hide the deleted_at field for any row that
// happens to carry an exact zero-value timestamp, which is unsettling
// behaviour on a recovery surface where the user expects to see the
// exact moment each row was tombstoned.
type deletedTransactionResponse struct {
	ID               int64    `json:"id"`
	UserID           int64    `json:"user_id"`
	Date             string   `json:"date"`
	Amount           float64  `json:"amount"`
	OriginalAmount   *float64 `json:"original_amount,omitempty"`
	OriginalCurrency string   `json:"original_currency,omitempty"`
	Description      string   `json:"description"`
	CategoryID       int64    `json:"category_id"`
	Tags             string   `json:"tags,omitempty"`
	Notes            string   `json:"notes,omitempty"`
	CreatedAt        string   `json:"created_at"`
	UpdatedAt        string   `json:"updated_at"`
	DeletedAt        string   `json:"deleted_at"`
	CategoryName     string   `json:"category_name,omitempty"`
	CategoryType     string   `json:"category_type,omitempty"`
}

// deletedTransactionListResponse wraps a paginated trash view. Same
// shape as transactionListResponse so the frontend list code can reuse
// its pagination controls; only the row type differs.
type deletedTransactionListResponse struct {
	Transactions []deletedTransactionResponse `json:"transactions"`
	Total        int                          `json:"total"`
	Page         int                          `json:"page"`
	PerPage      int                          `json:"per_page"`
}

// handleListDeletedTransactions serves GET /api/transactions/deleted for
// admin users. It returns a paginated list of tombstoned transactions
// ordered by deleted_at DESC so the most-recent accident is at the top —
// the common recovery use case is "I just nuked the wrong filter, give
// me back the last thing I did."
//
// The query goes through sqlc (ListDeletedTransactions) so the chokepoint
// rule in CLAUDE.md — "all transactions reads live in queries.sql so the
// deleted_at filter is reviewable in one place" — is respected. The
// generated row projects both c.name and c.type so this handler has no
// raw SQL.
//
// Pagination is identical to handleListTransactions so the frontend
// trash view can reuse the same pager component — page + per_page
// query params, MaxPerPage cap, Total count from a separate query.
func (h *Handler) handleListDeletedTransactions(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.GetUser(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	q := r.URL.Query()

	page := 1
	if v := q.Get("page"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			page = p
		}
	}
	if page > MaxPage {
		page = MaxPage
	}
	perPage := DefaultPerPage
	if v := q.Get("per_page"); v != "" {
		if pp, err := strconv.Atoi(v); err == nil && pp > 0 {
			perPage = pp
		}
	}
	if perPage > MaxPerPage {
		perPage = MaxPerPage
	}

	ctx := r.Context()

	// CountDeletedTransactions is :one and returns a bare int64. It is
	// deliberately a separate query from ListDeletedTransactions so
	// the Total shown in the pager reflects the full trash, not just
	// the current page's window.
	total, err := h.queries.CountDeletedTransactions(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count deleted transactions")
		return
	}

	offset := (page - 1) * perPage
	rows, err := h.queries.ListDeletedTransactions(ctx, database.ListDeletedTransactionsParams{
		Limit:  int64(perPage),
		Offset: int64(offset),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query deleted transactions")
		return
	}

	transactions := make([]deletedTransactionResponse, 0, len(rows))
	for _, row := range rows {
		tr := deletedTransactionResponse{
			ID:           row.ID,
			UserID:       row.UserID,
			Date:         row.Date.Format("2006-01-02"),
			Amount:       centsToDollars(row.AmountCents),
			Description:  row.Description,
			CategoryID:   row.CategoryID,
			CreatedAt:    row.CreatedAt.Format(time.RFC3339),
			UpdatedAt:    row.UpdatedAt.Format(time.RFC3339),
			CategoryName: row.CategoryName,
			CategoryType: row.CategoryType,
		}
		// deleted_at is only reached via WHERE t.deleted_at IS NOT NULL,
		// so the sql.NullTime should always be valid here; the guard
		// below is a belt-and-braces check that avoids an ugly zero
		// timestamp in the JSON if SQLite ever surfaces a row with a
		// NULL value (which would itself indicate a query bug).
		if row.DeletedAt.Valid {
			tr.DeletedAt = row.DeletedAt.Time.Format(time.RFC3339)
		}
		if row.OriginalAmountCents.Valid {
			v := centsToDollars(row.OriginalAmountCents.Int64)
			tr.OriginalAmount = &v
		}
		if row.OriginalCurrency.Valid {
			tr.OriginalCurrency = row.OriginalCurrency.String
		}
		if row.Tags.Valid {
			tr.Tags = row.Tags.String
		}
		if row.Notes.Valid {
			tr.Notes = row.Notes.String
		}
		transactions = append(transactions, tr)
	}

	writeJSON(w, http.StatusOK, deletedTransactionListResponse{
		Transactions: transactions,
		Total:        int(total),
		Page:         page,
		PerPage:      perPage,
	})
}

// handleRestoreTransaction serves POST /api/transactions/{id}/restore
// for admin users. It clears the tombstone (deleted_at → NULL) on a
// previously soft-deleted row and writes a "restore" audit row inside
// the same SQL transaction. A restore of a row that is already live,
// or of a non-existent row, returns 404: from the UX perspective both
// cases mean "the id you asked to restore is not in the trash."
func (h *Handler) handleRestoreTransaction(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid transaction ID")
		return
	}

	// TOCTOU pre-check: load the row outside the store tx so we can
	// reject not-found and not-tombstoned rows with 404 before
	// touching the chokepoint. The store's own in-tx reload is the
	// authoritative check — a row that slips from tombstoned to
	// live between this read and the store call surfaces as
	// sql.ErrNoRows from the store, which we map to 404 below.
	existing, err := h.queries.GetTransactionByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "transaction not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get transaction")
		return
	}
	if !existing.DeletedAt.Valid {
		writeError(w, http.StatusNotFound, "transaction not found")
		return
	}

	if err := h.txnStore.Restore(r.Context(), user.ID, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "transaction not found")
			return
		}
		if database.IsContentHashUniqueViolation(err) {
			// A newer live row was re-imported with identical content while
			// this row sat in the trash; flipping deleted_at back to NULL
			// re-enters the partial unique index and collides. Surface an
			// actionable 409 instead of an opaque 500 — the user can purge
			// this trashed copy or edit the live copy to distinguish them,
			// then retry. Nothing changed, so skip checkpoint reverification.
			writeError(w, http.StatusConflict, "cannot restore: a newer transaction with identical date, amount, description, and category already exists. Purge this trashed copy or edit the existing one, then retry.")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to restore transaction")
		return
	}

	// Phase 3.3: reversing a soft-delete puts the row's cents back into
	// the live SUM, so every checkpoint on or after existing.Date needs
	// to be re-verified. existing.Date is the pre-restore copy loaded by
	// the TOCTOU pre-check above — the store's Restore call doesn't
	// mutate the date column so that value is still authoritative.
	h.verifyAffectedCheckpoints(r.Context(), existing.Date)

	// Phase C (Task 17): a restore re-adds the row's spend to the live SUM,
	// so its cell may now cross over budget — evaluate it (and re-arm a
	// future re-cross). cellsForCreate is the correct shape: a restore, like
	// a create, adds spend to a single cell. existing.Date/CategoryID are the
	// pre-restore copy from the TOCTOU read; Restore doesn't mutate either.
	// Post-commit, best-effort.
	h.evaluateBudgetAlerts(r.Context(), cellsForCreate(existing.CategoryID, existing.Date))

	// Live-updates: a restore re-adds a row to the live ledger, so every open
	// device's transactions list, dashboard, reports, and budget cells may now
	// be stale, and the trash view lost a row. Post-commit, best-effort,
	// nil-safe — never affects the response.
	h.publishInvalidate("trash", "transactions", "dashboard", "reports", "budgets")

	writeJSON(w, http.StatusOK, map[string]string{"status": "restored"})
}

// handlePurgeTransaction serves DELETE /api/transactions/{id}/purge
// for admin users. It hard-deletes a tombstoned row — the only place
// in the codebase where a transaction is physically removed. Live
// rows return 404 so the trash view cannot be used to bypass the
// normal soft-delete flow; a live row that should be removed must
// go through the regular DELETE /api/transactions/{id} endpoint
// first to become tombstoned, then purge.
//
// No audit row is written for the purge itself — see the long
// comment on TransactionStore.Purge for the rationale.
func (h *Handler) handlePurgeTransaction(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.GetUser(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid transaction ID")
		return
	}

	// Same TOCTOU pattern as handleRestoreTransaction: load outside
	// the store tx to return 404 early for live or missing rows.
	existing, err := h.queries.GetTransactionByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "transaction not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get transaction")
		return
	}
	if !existing.DeletedAt.Valid {
		writeError(w, http.StatusNotFound, "transaction not found")
		return
	}

	if err := h.txnStore.Purge(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "transaction not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to purge transaction")
		return
	}

	// Live-updates: a purge only changes the trash view (the row was already
	// tombstoned and out of every live aggregate). Post-commit, best-effort,
	// nil-safe — never affects the response.
	h.publishInvalidate("trash")

	writeJSON(w, http.StatusOK, map[string]string{"status": "purged"})
}

// batchRestoreRequest is the JSON input for POST /api/transactions/restore-batch.
type batchRestoreRequest struct {
	IDs []int64 `json:"ids"`
}

// batchRestoreResponse is the JSON output: the count of rows that were
// actually restored. IDs that were already live or missing from the
// trash are silently skipped — this matches the delete-batch contract
// where the caller cannot always know the exact state of every id it
// submitted and should not have the whole batch fail because of stragglers.
//
// Conflicted counts ids that could not be restored because a newer live
// row was re-imported with the same content_hash while the id sat in the
// trash. Those ids are skipped (the rest of the batch still commits) so
// the frontend can surface "restored N, M could not be restored (a newer
// copy already exists)". The field is additive JSON — older clients that
// don't read it are unaffected.
type batchRestoreResponse struct {
	Restored   int `json:"restored"`
	Conflicted int `json:"conflicted"`
}

// handleBatchRestoreTransactions serves POST /api/transactions/restore-batch.
// It restores up to MaxBatchRestoreIDs tombstoned rows in a single SQL
// transaction. The cap is aliased to MaxBatchDeleteIDs in limits.go so
// "undo the last batch delete" is always possible in a single request —
// operators who just nuked 500 rows by accident shouldn't need a
// multi-step dance to recover. IDs that are already live (or missing)
// are silently skipped rather than aborting the whole batch, because
// the common case is "somebody pasted a list of ids from a report" and
// the operator shouldn't have to filter out stragglers by hand.
func (h *Handler) handleBatchRestoreTransactions(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req batchRestoreRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if len(req.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "at least one id is required")
		return
	}
	if len(req.IDs) > MaxBatchRestoreIDs {
		writeError(w, http.StatusBadRequest, "batch size cannot exceed "+strconv.Itoa(MaxBatchRestoreIDs))
		return
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin transaction")
		return
	}
	// Rollback is deferred as a safety net for every non-commit exit.
	// On the success path tx.Commit() runs first and this rollback
	// observes sql.ErrTxDone (the standard "already committed" no-op)
	// which we deliberately ignore. On the failure path, a non-ErrTxDone
	// rollback error is rare but worth surfacing: it means SQLite
	// rejected the rollback itself, which is the kind of edge case that
	// leaves orphaned writes behind — the operator should see it in the
	// logs rather than have it vanish into a silent `_ = tx.Rollback()`.
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			log.Printf("batch restore rollback: %v", rbErr)
		}
	}()

	// Distinct (category, month) cells touched by this batch, for the
	// post-commit over-budget hook (Phase C, Task 17). RestoreTx returns
	// only error, so we read each row's (category, date) off the same tx
	// before restoring it — only rows that actually restore are recorded.
	qtx := h.queries.WithTx(tx)
	cellSet := map[budgetCell]struct{}{}

	restored := 0
	conflicted := 0
	for _, id := range req.IDs {
		existing, loadErr := qtx.GetTransactionByID(r.Context(), id)
		err := h.txnStore.RestoreTx(r.Context(), tx, user.ID, id)
		if err == nil {
			restored++
			if loadErr == nil {
				cellSet[cellForDate(existing.CategoryID, existing.Date)] = struct{}{}
			}
			continue
		}
		if errors.Is(err, sql.ErrNoRows) {
			// Already live / missing — skip and continue the batch.
			continue
		}
		if database.IsContentHashUniqueViolation(err) {
			// A live row was re-imported with the same content_hash while
			// this id sat in the trash, so restoring it would re-enter the
			// partial unique index and collide. SQLite aborts only the
			// failing UPDATE (default ON CONFLICT ABORT), not the
			// surrounding tx, so the *sql.Tx stays usable for the remaining
			// ids and the final Commit. We skip this one straggler and keep
			// restoring the rest — never roll back the whole undo batch for
			// a single recoverable collision (the CLAUDE.md collision-
			// tolerance invariant). RestoreTx writes its audit row only
			// after a successful RestoreTransaction, so no partial audit
			// row leaks for the skipped id.
			conflicted++
			continue
		}
		writeError(w, http.StatusInternalServerError, "failed to restore transactions")
		return
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit batch")
		return
	}

	// Phase 3.3: batch restore doesn't pre-load row dates (the IDs arrive
	// straight from the UI list and adding a per-row GetTransactionByID
	// would double the query count for no forensic benefit), so pass a
	// zero time.Time to reverify every checkpoint. This is conservative
	// but correct — restoring a row always re-adds cents to the live SUM,
	// so every red/green state is potentially affected.
	if restored > 0 {
		h.verifyAffectedCheckpoints(r.Context(), time.Time{})
	}

	// Phase C (Task 17): evaluate the over-budget alert for every distinct
	// (category, month) cell the batch restored spend into. Post-commit,
	// best-effort.
	if len(cellSet) > 0 {
		cells := make([]budgetCell, 0, len(cellSet))
		for c := range cellSet {
			cells = append(cells, c)
		}
		h.evaluateBudgetAlerts(r.Context(), cells)
	}

	// Live-updates: one signal per batch (mirrors notifyTxnBatch aggregation).
	// A batch restore re-adds rows to the live ledger and empties part of the
	// trash. Post-commit, best-effort, nil-safe — never affects the response.
	if restored > 0 {
		h.publishInvalidate("trash", "transactions", "dashboard", "reports", "budgets")
	}

	writeJSON(w, http.StatusOK, batchRestoreResponse{Restored: restored, Conflicted: conflicted})
}

// restoreAllResponse is the JSON output for POST /api/transactions/restore-all.
// Mirrors batchRestoreResponse so the frontend can share a render path for
// both the "selected" and "all pages" bulk-restore results — including the
// Conflicted count for ids whose content_hash now collides with a re-imported
// live row (skipped rather than failing the whole call).
type restoreAllResponse struct {
	Restored   int `json:"restored"`
	Conflicted int `json:"conflicted"`
}

// handleRestoreAllTransactions serves POST /api/transactions/restore-all —
// the "oops, everything" escape hatch that flips every tombstoned row
// back to live in a single request. Unlike restore-batch this handler
// takes no body: the implicit scope is "the whole trash as it stands
// right now."
//
// Implementation mirrors handleBatchRestoreTransactions: load all
// tombstoned ids, open one SQL tx, call RestoreTx per id (tolerating
// sql.ErrNoRows so a race with a concurrent purge/restore doesn't kill
// the whole call), commit, then re-verify Phase 3.3 checkpoints. One
// "restore" audit row lands per id so the audit log is identical to
// what the user would see if they'd hit Restore on each row by hand.
func (h *Handler) handleRestoreAllTransactions(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	ctx := r.Context()

	// Snapshot the trash before we open the tx. A row that becomes
	// tombstoned between the snapshot and the tx body won't be restored
	// on this call — the operator can re-run restore-all to pick it up.
	// This is strictly weaker than a transactional snapshot but avoids
	// holding an open tx across the network round-trip from the user's
	// perspective.
	ids, err := h.queries.ListAllDeletedTransactionIDs(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list deleted transactions")
		return
	}

	if len(ids) == 0 {
		// Nothing to do — return 0 without opening a tx or writing audit
		// rows. Previous builds of the UI polled this endpoint from the
		// trash view, and writing audit rows on every poll would flood
		// the log.
		writeJSON(w, http.StatusOK, restoreAllResponse{Restored: 0})
		return
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin transaction")
		return
	}
	// Rollback is deferred for every non-commit exit. On the success
	// path tx.Commit() already returned and this rollback observes
	// sql.ErrTxDone which we deliberately ignore — surfacing any other
	// rollback error into the logs because it signals orphaned writes.
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			log.Printf("restore-all rollback: %v", rbErr)
		}
	}()

	// Distinct (category, month) cells restored, for the post-commit
	// over-budget hook (Phase C, Task 17). Same approach as batch-restore:
	// read each row's (category, date) off the tx before restoring it.
	qtx := h.queries.WithTx(tx)
	cellSet := map[budgetCell]struct{}{}

	restored := 0
	conflicted := 0
	for _, id := range ids {
		existing, loadErr := qtx.GetTransactionByID(ctx, id)
		err := h.txnStore.RestoreTx(ctx, tx, user.ID, id)
		if err == nil {
			restored++
			if loadErr == nil {
				cellSet[cellForDate(existing.CategoryID, existing.Date)] = struct{}{}
			}
			continue
		}
		if errors.Is(err, sql.ErrNoRows) {
			// Race: the row became live (or was purged) between the
			// snapshot and this inner call. Skip and keep going.
			continue
		}
		if database.IsContentHashUniqueViolation(err) {
			// Same recoverable collision as batch-restore: a re-imported
			// live row now owns this id's content_hash. SQLite aborts only
			// the failing UPDATE, so the tx stays usable; skip this id and
			// keep restoring the rest rather than rolling back the whole
			// undo (CLAUDE.md collision-tolerance invariant).
			conflicted++
			continue
		}
		writeError(w, http.StatusInternalServerError, "failed to restore transactions")
		return
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit restore-all")
		return
	}

	// Phase 3.3: same rationale as batch-restore — the per-id snapshot
	// doesn't pre-load dates, so re-verify every checkpoint.
	if restored > 0 {
		h.verifyAffectedCheckpoints(ctx, time.Time{})
	}

	// Phase C (Task 17): evaluate the over-budget alert for every distinct
	// (category, month) cell restore-all re-added spend into. Post-commit,
	// best-effort.
	if len(cellSet) > 0 {
		cells := make([]budgetCell, 0, len(cellSet))
		for c := range cellSet {
			cells = append(cells, c)
		}
		h.evaluateBudgetAlerts(ctx, cells)
	}

	// Live-updates: one signal for the whole restore-all op. Re-adds rows to
	// the live ledger and empties the trash. Post-commit, best-effort,
	// nil-safe — never affects the response.
	if restored > 0 {
		h.publishInvalidate("trash", "transactions", "dashboard", "reports", "budgets")
	}

	writeJSON(w, http.StatusOK, restoreAllResponse{Restored: restored, Conflicted: conflicted})
}

// purgeAllResponse is the JSON output for DELETE /api/transactions/trash.
// Purged is int64 because it is read straight from sql.Result.RowsAffected()
// and SQLite returns int64; narrowing to int would be a lossy cast for no
// benefit.
type purgeAllResponse struct {
	Purged int64 `json:"purged"`
}

// handlePurgeAllTransactions serves DELETE /api/transactions/trash —
// the "empty the trash" escape hatch that hard-deletes every tombstoned
// row in one shot. This is the only mass-delete code path in the
// system; every other removal goes through the soft-delete flow first.
//
// No audit row is written — same asymmetry documented on the per-row
// handlePurgeTransaction and on TransactionStore.Purge. The audit table's
// CHECK constraint doesn't even permit a 'purge' action today, so
// attempting to write one would surface as a constraint error; the
// paired test TestHandlePurgeAllTransactions_DoesNotWriteAuditRows
// guards the invariant explicitly.
func (h *Handler) handlePurgeAllTransactions(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.GetUser(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	result, err := h.queries.PurgeAllTombstonedTransactions(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to purge trash")
		return
	}
	purged, err := result.RowsAffected()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read purge result")
		return
	}

	// Live-updates: emptying the trash only changes the trash view. Post-commit,
	// best-effort, nil-safe — never affects the response.
	if purged > 0 {
		h.publishInvalidate("trash")
	}

	writeJSON(w, http.StatusOK, purgeAllResponse{Purged: purged})
}
