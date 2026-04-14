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
			Amount:       row.Amount,
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
		if row.OriginalAmount.Valid {
			v := row.OriginalAmount.Float64
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
		writeError(w, http.StatusInternalServerError, "failed to restore transaction")
		return
	}

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
type batchRestoreResponse struct {
	Restored int `json:"restored"`
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

	restored := 0
	for _, id := range req.IDs {
		err := h.txnStore.RestoreTx(r.Context(), tx, user.ID, id)
		if err == nil {
			restored++
			continue
		}
		if errors.Is(err, sql.ErrNoRows) {
			// Already live / missing — skip and continue the batch.
			continue
		}
		writeError(w, http.StatusInternalServerError, "failed to restore transactions")
		return
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit batch")
		return
	}

	writeJSON(w, http.StatusOK, batchRestoreResponse{Restored: restored})
}
