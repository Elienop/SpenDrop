package api

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/database"
)

// sortColumnWhitelist maps frontend sort_by values to safe SQL column
// expressions. Phase 3.1a: "amount" now routes to t.amount_cents so sort
// order is determined by exact int64 values; sorting on the legacy REAL
// column would occasionally invert two rows whose float representations
// differed by one ULP below the visible cent.
var sortColumnWhitelist = map[string]string{
	"date":        "t.date",
	"amount":      "t.amount_cents",
	"description": "t.description",
	"category":    "c.name",
	"tags":        "t.tags",
}

// parseSortParams extracts and validates sort_by and sort_dir from query params.
// Invalid or missing values fall back to "t.date" and "DESC".
func parseSortParams(q map[string][]string) (column, dir string) {
	column = "t.date"
	dir = "DESC"

	if vals, ok := q["sort_by"]; ok && len(vals) > 0 {
		if col, valid := sortColumnWhitelist[vals[0]]; valid {
			column = col
		}
	}

	if vals, ok := q["sort_dir"]; ok && len(vals) > 0 {
		switch strings.ToLower(vals[0]) {
		case "asc":
			dir = "ASC"
		case "desc":
			dir = "DESC"
		}
	}

	return column, dir
}

// transactionRequest is the JSON input for creating or updating a transaction.
type transactionRequest struct {
	Date             string   `json:"date"`
	Amount           float64  `json:"amount"`
	OriginalAmount   *float64 `json:"original_amount"`
	OriginalCurrency string   `json:"original_currency"`
	Description      string   `json:"description"`
	CategoryID       int64    `json:"category_id"`
	Tags             string   `json:"tags"`
	Notes            string   `json:"notes"`
}

// transactionResponse is the JSON output for a single transaction including
// joined category info.
type transactionResponse struct {
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
	CategoryName     string   `json:"category_name,omitempty"`
	CategoryType     string   `json:"category_type,omitempty"`
}

// transactionListResponse wraps a paginated list of transactions.
type transactionListResponse struct {
	Transactions []transactionResponse `json:"transactions"`
	Total        int                   `json:"total"`
	Page         int                   `json:"page"`
	PerPage      int                   `json:"per_page"`
}

// toTransactionResponse converts a database.Transaction into the JSON wire
// shape. Phase 3.1a: reads t.AmountCents / t.OriginalAmountCents (the new
// integer cents columns) and converts to dollars exactly once via
// centsToDollars. The legacy t.Amount / t.OriginalAmount REAL columns stay
// populated by every writer until migration 010, but we deliberately ignore
// them on the read side so the handler path never touches a float sum -
// the whole point of Phase 3.1a is to make float drift impossible by
// construction for every aggregation that flows through this function.
func toTransactionResponse(t database.Transaction) transactionResponse {
	resp := transactionResponse{
		ID:          t.ID,
		UserID:      t.UserID,
		Date:        t.Date.Format("2006-01-02"),
		Amount:      centsToDollars(t.AmountCents),
		Description: t.Description,
		CategoryID:  t.CategoryID,
		CreatedAt:   t.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   t.UpdatedAt.Format(time.RFC3339),
	}
	if t.OriginalAmountCents.Valid {
		amt := centsToDollars(t.OriginalAmountCents.Int64)
		resp.OriginalAmount = &amt
	}
	if t.OriginalCurrency.Valid {
		resp.OriginalCurrency = t.OriginalCurrency.String
	}
	if t.Tags.Valid {
		resp.Tags = t.Tags.String
	}
	if t.Notes.Valid {
		resp.Notes = t.Notes.String
	}
	return resp
}

// resolveCurrency applies currency conversion logic. If originalCurrency is
// a non-base currency, it divides originalAmount by the rate_to_base to get
// the converted amount. Returns (finalAmount, originalAmount as NullFloat64,
// originalCurrency as NullString, error).
// The queries parameter allows callers to pass either h.queries (normal) or
// a transactional qtx (batch) to ensure consistent reads within a transaction.
func resolveCurrency(ctx context.Context, q *database.Queries, req transactionRequest) (float64, sql.NullFloat64, sql.NullString, error) {
	origAmt := sql.NullFloat64{}
	origCur := sql.NullString{}

	if req.OriginalCurrency == "" {
		// No foreign currency — use amount directly
		if req.Amount <= 0 {
			return 0, origAmt, origCur, fmt.Errorf("amount must be positive")
		}
		return req.Amount, origAmt, origCur, nil
	}

	currency, err := q.GetCurrency(ctx, req.OriginalCurrency)
	if err != nil {
		return 0, origAmt, origCur, fmt.Errorf("unknown currency %q", req.OriginalCurrency)
	}

	if currency.IsBase {
		// It's the base currency — use amount directly, no conversion needed
		if req.Amount <= 0 {
			return 0, origAmt, origCur, fmt.Errorf("amount must be positive")
		}
		return req.Amount, origAmt, origCur, nil
	}

	// Foreign currency: must have original_amount
	if req.OriginalAmount == nil || *req.OriginalAmount <= 0 {
		return 0, origAmt, origCur, fmt.Errorf("original_amount is required for non-base currency")
	}

	if currency.RateToBase == 0 {
		return 0, origAmt, origCur, fmt.Errorf("currency %q has zero rate", req.OriginalCurrency)
	}

	converted := *req.OriginalAmount / currency.RateToBase
	// Round to 2 decimal places
	converted = math.Round(converted*100) / 100

	origAmt = sql.NullFloat64{Float64: *req.OriginalAmount, Valid: true}
	origCur = sql.NullString{String: req.OriginalCurrency, Valid: true}

	return converted, origAmt, origCur, nil
}

// handleListTransactions returns a filtered, paginated list of transactions.
func (h *Handler) handleListTransactions(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	_ = user // Transactions are visible to all authenticated users

	q := r.URL.Query()

	// Pagination
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

	whereClause, args := buildTransactionWhereClause(q)

	// Soft-delete filter: list endpoints only ever surface live rows.
	// buildTransactionWhereClause is shared with the trash view, so the
	// deleted_at predicate is applied here by the caller instead of inside
	// the helper. Every live-transactions read path in this file does the
	// same: append "AND t.deleted_at IS NULL" (or the "WHERE" form when the
	// helper returned an empty clause).
	liveClause := appendLiveTransactionsFilter(whereClause)

	// Count query
	countQuery := "SELECT COUNT(*) FROM transactions t JOIN categories c ON t.category_id = c.id" + liveClause
	var total int
	if err := h.db.QueryRowContext(r.Context(), countQuery, args...).Scan(&total); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count transactions")
		return
	}

	// Sorting
	sortCol, sortDir := parseSortParams(q)
	orderClause := fmt.Sprintf(" ORDER BY %s %s, t.id %s", sortCol, sortDir, sortDir)

	// Data query
	//
	// Phase 3.1a: reads t.amount_cents (int64) rather than t.amount (float64).
	// The legacy REAL column is still populated by every writer (dual-write
	// contract in queries.sql), but the aggregation/list path consumes cents
	// only and converts to dollars exactly once at the wire edge via
	// centsToDollars. This eliminates the per-row float round-trip that
	// would otherwise reintroduce IEEE-754 drift into the list endpoint.
	offset := (page - 1) * perPage
	dataQuery := `SELECT t.id, t.user_id, t.date, t.amount_cents, t.original_amount_cents, t.original_currency,
		t.description, t.category_id, t.tags, t.notes, t.created_at, t.updated_at,
		c.name AS category_name, c.type AS category_type
		FROM transactions t
		JOIN categories c ON t.category_id = c.id` + liveClause + orderClause + ` LIMIT ? OFFSET ?`

	dataArgs := make([]any, len(args))
	copy(dataArgs, args)
	dataArgs = append(dataArgs, perPage, offset)

	rows, err := h.db.QueryContext(r.Context(), dataQuery, dataArgs...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query transactions")
		return
	}
	defer rows.Close()

	transactions := make([]transactionResponse, 0)
	for rows.Next() {
		var (
			tr           transactionResponse
			amountCents  int64
			origAmtCents sql.NullInt64
			origCur      sql.NullString
			tags         sql.NullString
			notes        sql.NullString
			date         time.Time
			createdAt    time.Time
			updatedAt    time.Time
			categoryName string
			categoryType string
		)
		if err := rows.Scan(
			&tr.ID, &tr.UserID, &date, &amountCents, &origAmtCents, &origCur,
			&tr.Description, &tr.CategoryID, &tags, &notes, &createdAt, &updatedAt,
			&categoryName, &categoryType,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to scan transaction")
			return
		}
		tr.Amount = centsToDollars(amountCents)
		tr.Date = date.Format("2006-01-02")
		tr.CreatedAt = createdAt.Format(time.RFC3339)
		tr.UpdatedAt = updatedAt.Format(time.RFC3339)
		tr.CategoryName = categoryName
		tr.CategoryType = categoryType
		if origAmtCents.Valid {
			v := centsToDollars(origAmtCents.Int64)
			tr.OriginalAmount = &v
		}
		if origCur.Valid {
			tr.OriginalCurrency = origCur.String
		}
		if tags.Valid {
			tr.Tags = tags.String
		}
		if notes.Valid {
			tr.Notes = notes.String
		}
		transactions = append(transactions, tr)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to iterate transactions")
		return
	}

	writeJSON(w, http.StatusOK, transactionListResponse{
		Transactions: transactions,
		Total:        total,
		Page:         page,
		PerPage:      perPage,
	})
}

// handleCreateTransaction creates a single new transaction.
func (h *Handler) handleCreateTransaction(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req transactionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := validateTransactionRequest(req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	date, _ := time.Parse("2006-01-02", req.Date) // already validated

	amount, origAmt, origCur, err := resolveCurrency(r.Context(), h.queries, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	txn, err := h.txnStore.Create(r.Context(), user.ID, database.CreateTransactionParams{
		UserID:              user.ID,
		Date:                date,
		Amount:              amount,
		AmountCents:         dollarsToCents(amount),
		OriginalAmount:      origAmt,
		OriginalAmountCents: nullableDollarsToCents(origAmt),
		OriginalCurrency:    origCur,
		Description:         req.Description,
		CategoryID:          req.CategoryID,
		Tags:                toNullString(req.Tags),
		Notes:               toNullString(req.Notes),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create transaction")
		return
	}

	writeJSON(w, http.StatusCreated, toTransactionResponse(txn))
}

// handleUpdateTransaction updates an existing transaction by ID.
func (h *Handler) handleUpdateTransaction(w http.ResponseWriter, r *http.Request) {
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

	// TOCTOU note: the 404 + ownership check below reads the row OUTSIDE
	// the TransactionStore's update tx, so in the microsecond gap between
	// this read and the chokepoint Update, a concurrent writer could have
	// deleted or mutated the row. The race is benign by construction:
	//
	//   - Row deleted: TransactionStore.Update reads the "before" row
	//     inside its own tx (store.go:97) and surfaces ErrNoRows, which
	//     rolls back cleanly — no ghost audit row, no orphaned update.
	//   - Fields mutated: the audit row's before_json captures the
	//     in-tx state, not the stale copy we read here, so the forensic
	//     log always reflects what actually committed.
	//   - user_id changed: impossible — no endpoint mutates user_id, so
	//     ownership cannot retroactively flip under us.
	//
	// Pushing the ownership check into the store would couple database
	// code to HTTP-level role semantics (member vs. admin) and force
	// every future chokepoint method to grow a scope parameter. We keep
	// the check here and rely on the store's own tx-scoped read to catch
	// the one failure mode (row vanished) that actually matters.
	existing, err := h.queries.GetTransactionByID(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "transaction not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get transaction")
		return
	}
	// GetTransactionByID deliberately leaks tombstoned rows so that
	// TransactionStore.Update/Delete can load the row inside its own tx
	// to emit audit before/after. From the user's perspective a
	// tombstoned row is not visible, so treat it as not-found here
	// (stale client, race with trash purge, bogus hand-crafted ID).
	if existing.DeletedAt.Valid {
		writeError(w, http.StatusNotFound, "transaction not found")
		return
	}

	// Ownership check: members can only edit their own
	if user.Role != RoleAdmin && existing.UserID != user.ID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	var req transactionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := validateTransactionRequest(req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	date, _ := time.Parse("2006-01-02", req.Date)

	amount, origAmt, origCur, err := resolveCurrency(r.Context(), h.queries, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	err = h.txnStore.Update(r.Context(), user.ID, database.UpdateTransactionParams{
		Date:                date,
		Amount:              amount,
		AmountCents:         dollarsToCents(amount),
		OriginalAmount:      origAmt,
		OriginalAmountCents: nullableDollarsToCents(origAmt),
		OriginalCurrency:    origCur,
		Description:         req.Description,
		CategoryID:          req.CategoryID,
		Tags:                toNullString(req.Tags),
		Notes:               toNullString(req.Notes),
		ID:                  id,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update transaction")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// handleDeleteTransaction deletes a transaction by ID.
func (h *Handler) handleDeleteTransaction(w http.ResponseWriter, r *http.Request) {
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

	// Same TOCTOU pattern as handleUpdateTransaction — see the long note
	// there for why reading the row outside the store tx is safe here.
	// Briefly: if the row is gone by the time TransactionStore.Delete
	// runs, its in-tx GetTransactionByID returns ErrNoRows and the whole
	// thing rolls back without producing a stray audit row.
	existing, err := h.queries.GetTransactionByID(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "transaction not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get transaction")
		return
	}
	// Already-tombstoned rows are not-found from the HTTP caller's
	// perspective; see the matching comment in handleUpdateTransaction.
	if existing.DeletedAt.Valid {
		writeError(w, http.StatusNotFound, "transaction not found")
		return
	}

	if user.Role != RoleAdmin && existing.UserID != user.ID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	if err := h.txnStore.Delete(r.Context(), user.ID, id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete transaction")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleBatchCreateTransactions creates multiple transactions in a single
// database transaction.
func (h *Handler) handleBatchCreateTransactions(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var reqs []transactionRequest
	if err := decodeJSON(w, r, &reqs); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if len(reqs) == 0 {
		writeError(w, http.StatusBadRequest, "at least one transaction is required")
		return
	}
	if len(reqs) > MaxBatchTransactions {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("batch size cannot exceed %d", MaxBatchTransactions))
		return
	}

	// Validate all items before starting the DB transaction
	for i, req := range reqs {
		if err := validateTransactionRequest(req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("item %d: %s", i, err.Error()))
			return
		}
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin transaction")
		return
	}
	defer tx.Rollback()

	// qtx is still used for the per-item currency lookup (a read the
	// chokepoint doesn't expose); CreateTx handles the write+audit pair
	// on the same *sql.Tx so the whole batch — data rows, audit rows,
	// currency reads — commits or rolls back atomically.
	qtx := h.queries.WithTx(tx)
	results := make([]transactionResponse, 0, len(reqs))

	for i, req := range reqs {
		date, _ := time.Parse("2006-01-02", req.Date)

		amount, origAmt, origCur, err := resolveCurrency(r.Context(), qtx, req)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("item %d: %s", i, err.Error()))
			return
		}

		txn, err := h.txnStore.CreateTx(r.Context(), tx, user.ID, database.CreateTransactionParams{
			UserID:              user.ID,
			Date:                date,
			Amount:              amount,
			AmountCents:         dollarsToCents(amount),
			OriginalAmount:      origAmt,
			OriginalAmountCents: nullableDollarsToCents(origAmt),
			OriginalCurrency:    origCur,
			Description:         req.Description,
			CategoryID:          req.CategoryID,
			Tags:                toNullString(req.Tags),
			Notes:               toNullString(req.Notes),
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("item %d: failed to create transaction", i))
			return
		}

		results = append(results, toTransactionResponse(txn))
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit batch")
		return
	}

	writeJSON(w, http.StatusCreated, results)
}

// validateTransactionRequest checks required fields on a transaction request.
func validateTransactionRequest(req transactionRequest) error {
	if req.Date == "" {
		return fmt.Errorf("date is required")
	}
	if _, err := time.Parse("2006-01-02", req.Date); err != nil {
		return fmt.Errorf("date must be in YYYY-MM-DD format")
	}
	if req.Description == "" {
		return fmt.Errorf("description is required")
	}
	if len(req.Description) > MaxDescriptionLength {
		return fmt.Errorf("description must be %d characters or less", MaxDescriptionLength)
	}
	if len(req.Tags) > MaxTagsLength {
		return fmt.Errorf("tags must be %d characters or less", MaxTagsLength)
	}
	if len(req.Notes) > MaxNotesLength {
		return fmt.Errorf("notes must be %d characters or less", MaxNotesLength)
	}
	if req.CategoryID <= 0 {
		return fmt.Errorf("category_id is required")
	}
	// Amount validation: if no foreign currency, amount must be positive.
	// If foreign currency is specified, original_amount is checked in resolveCurrency.
	if req.OriginalCurrency == "" && req.Amount <= 0 {
		return fmt.Errorf("amount must be positive")
	}
	if math.IsInf(req.Amount, 0) || math.IsNaN(req.Amount) || req.Amount > MaxTransactionAmount {
		return fmt.Errorf("amount exceeds maximum allowed value")
	}
	if req.OriginalAmount != nil && (math.IsInf(*req.OriginalAmount, 0) || math.IsNaN(*req.OriginalAmount) || *req.OriginalAmount > MaxTransactionAmount) {
		return fmt.Errorf("original_amount exceeds maximum allowed value")
	}
	return nil
}

// bulkRenameRequest is the JSON input for bulk-renaming transaction descriptions.
type bulkRenameRequest struct {
	Search         string `json:"search"`
	NewDescription string `json:"new_description"`
}

// handleBulkRename updates the description of all transactions matching a
// case-insensitive LIKE search. Non-admin users can only rename their own
// transactions; admins can rename across all users.
func (h *Handler) handleBulkRename(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req bulkRenameRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.Search = strings.TrimSpace(req.Search)
	req.NewDescription = strings.TrimSpace(req.NewDescription)

	if req.Search == "" {
		writeError(w, http.StatusBadRequest, "search is required")
		return
	}
	if req.NewDescription == "" {
		writeError(w, http.StatusBadRequest, "new_description is required")
		return
	}
	if len(req.NewDescription) > MaxDescriptionLength {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("new_description must be %d characters or less", MaxDescriptionLength))
		return
	}

	// Escape SQL LIKE wildcards (same pattern as buildTransactionWhereClause)
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(req.Search)

	// Wrap the bulk UPDATE + summary audit row in a single *sql.Tx so the
	// audit row commits if and only if the data rows commit. RecordBulkTx
	// writes a single summary row with transaction_id=BulkAuditTransactionID
	// rather than one-row-per-match; the plan documents this as the
	// deliberate fidelity/perf trade-off for an endpoint that can touch
	// tens of thousands of rows.
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin transaction")
		return
	}
	defer tx.Rollback()

	var result sql.Result
	// Soft-delete aware: the rename must skip tombstoned rows because
	// those are "no longer in the live set" and reviving them via an
	// incidental bulk rename would silently restore deleted data. If an
	// operator wants to rename tombstoned rows, they restore first.
	if user.Role == RoleAdmin {
		result, err = tx.ExecContext(r.Context(),
			`UPDATE transactions SET description = ?, updated_at = CURRENT_TIMESTAMP WHERE description LIKE ? ESCAPE '\' AND deleted_at IS NULL`,
			req.NewDescription, "%"+escaped+"%",
		)
	} else {
		result, err = tx.ExecContext(r.Context(),
			`UPDATE transactions SET description = ?, updated_at = CURRENT_TIMESTAMP WHERE description LIKE ? ESCAPE '\' AND user_id = ? AND deleted_at IS NULL`,
			req.NewDescription, "%"+escaped+"%", user.ID,
		)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update transactions")
		return
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get update count")
		return
	}

	// Record the summary audit row for the bulk rename. The filter string
	// embeds BOTH the raw user-supplied search term (quoted with %q so
	// trailing whitespace, quotes, and escape chars survive a round-trip)
	// AND the exact SQL LIKE pattern actually executed. Without the
	// executed pattern an operator replaying the audit log cannot tell
	// which `%` / `_` characters were literals vs. wildcards — critical
	// for reconstructing intent when the raw search contains either.
	scope := "own"
	if user.Role == RoleAdmin {
		scope = "all"
	}
	filter := fmt.Sprintf("rename scope=%s search=%q -> %q (SQL LIKE %q ESCAPE '\\')",
		scope, req.Search, req.NewDescription, "%"+escaped+"%")
	if err := h.txnStore.RecordBulkTx(r.Context(), tx, user.ID, database.AuditUpdate, database.BulkAuditSummary{
		Count:  rowsAffected,
		Filter: filter,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record audit")
		return
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit bulk rename")
		return
	}

	writeJSON(w, http.StatusOK, map[string]int64{"updated": rowsAffected})
}

// batchDeleteRequest is the JSON input for deleting multiple transactions.
type batchDeleteRequest struct {
	IDs []int64 `json:"ids"`
}

// handleBatchDeleteTransactions deletes multiple transactions in a single
// database transaction. IDs that don't exist or aren't owned by the caller
// are silently skipped. Returns the count of actually deleted rows.
func (h *Handler) handleBatchDeleteTransactions(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req batchDeleteRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if len(req.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "at least one id is required")
		return
	}
	if len(req.IDs) > MaxBatchDeleteIDs {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("batch size cannot exceed %d", MaxBatchDeleteIDs))
		return
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin transaction")
		return
	}
	defer tx.Rollback()

	// qtx handles the ownership-check read; DeleteTx performs the delete
	// + audit write on the same *sql.Tx so every deleted row gets its
	// own audit entry, and a failure rolls back the whole batch.
	qtx := h.queries.WithTx(tx)
	deleted := 0
	var skipped []int64

	for _, id := range req.IDs {
		existing, err := qtx.GetTransactionByID(r.Context(), id)
		if err != nil {
			skipped = append(skipped, id)
			continue
		}
		// Tombstoned rows are "skipped" just like missing rows: from
		// the caller's perspective they have already been deleted.
		if existing.DeletedAt.Valid {
			skipped = append(skipped, id)
			continue
		}

		if user.Role != RoleAdmin && existing.UserID != user.ID {
			skipped = append(skipped, id)
			continue
		}

		if err := h.txnStore.DeleteTx(r.Context(), tx, user.ID, id); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to delete transaction")
			return
		}
		deleted++
	}

	// If any requested IDs were skipped (missing or not owned), write an
	// additional summary audit row so operators can reconstruct operator
	// *intent* — the per-row audits only record rows that actually
	// committed, so without this the forensic log would silently lose the
	// fact that the user asked to delete ID 999 and it wasn't there. The
	// summary row uses the BULK sentinel transaction_id so it is easy to
	// filter out during single-row investigations.
	if len(skipped) > 0 {
		scope := "own"
		if user.Role == RoleAdmin {
			scope = "all"
		}
		filterDesc := fmt.Sprintf("batch-delete-skipped scope=%s requested=%d deleted=%d skipped_ids=%v",
			scope, len(req.IDs), deleted, skipped)
		if err := h.txnStore.RecordBulkTx(r.Context(), tx, user.ID, database.AuditDelete, database.BulkAuditSummary{
			Count:  int64(len(skipped)),
			Filter: filterDesc,
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to record audit")
			return
		}
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit batch delete")
		return
	}

	writeJSON(w, http.StatusOK, map[string]int{"deleted": deleted})
}

// handleDeleteTransactionsByFilter deletes every transaction matching the
// same filter query parameters accepted by handleListTransactions. It exists
// so the "Select all X across pages" UI action can delete tens of thousands
// of rows in a single atomic operation — the ID-based batch-delete endpoint
// would require chunking into MaxBatchDeleteIDs batches and expose the user
// to partial-failure states.
//
// Ownership is enforced at the SQL level: non-admin users may only delete
// rows where user_id matches their own. Admins may delete any. This mirrors
// the per-row skip behavior of handleBatchDeleteTransactions but expressed
// as a WHERE clause so the operation stays atomic.
//
// Race note: there's a deliberate TOCTOU window between when the UI reads
// `total` from GET /transactions and when the DELETE runs. Rows inserted
// into the filter window in that interval will also be deleted, so the
// actual `deleted` count can exceed the number the user saw at confirm
// time. That's acceptable for the "wipe-and-reimport" use case this
// endpoint exists to serve — the alternative (snapshot-then-delete) would
// require holding IDs client-side and defeats the whole point of the
// filter-based endpoint. The response echoes the real count so callers
// who care can compare.
func (h *Handler) handleDeleteTransactionsByFilter(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	whereClause, args := buildTransactionWhereClause(r.URL.Query())

	// Non-admins: restrict to their own rows. Admins can delete anything
	// matching the filter. We append to whereClause so the ownership check
	// happens inside the same subquery used to select rows for deletion.
	if user.Role != RoleAdmin {
		if whereClause == "" {
			whereClause = " WHERE t.user_id = ?"
		} else {
			whereClause += " AND t.user_id = ?"
		}
		args = append(args, user.ID)
	}

	// Add the live-only filter onto the inner subquery so we only tombstone
	// rows that are currently live. The outer UPDATE re-asserts the same
	// predicate as documentation that a previously-tombstoned row cannot
	// have its deleted_at (or audit trail) re-stamped — SQLite's snapshot
	// isolation within a single statement makes the inner filter sufficient
	// in practice, but the outer guard makes that invariant explicit at the
	// SQL level for future maintainers reviewing this query.
	liveClause := appendLiveTransactionsFilter(whereClause)

	// Phase 2.1 turns the DELETE into a soft-delete UPDATE. The outer
	// UPDATE sets deleted_at on every matching live row in one atomic step;
	// the inner subquery joins categories (same shape as the list handler)
	// so filter semantics stay in lockstep. SQLite does not allow UPDATE
	// with a JOIN directly, hence the subquery form.
	query := `UPDATE transactions
		SET deleted_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		WHERE deleted_at IS NULL AND id IN (
			SELECT t.id FROM transactions t
			JOIN categories c ON t.category_id = c.id` + liveClause + `)`

	// Wrap DELETE + summary audit row in a single *sql.Tx so the audit row
	// commits if and only if the deletion commits. The summary row carries
	// the raw query string as its filter for operator forensics — a full
	// per-row audit would defeat the point of this endpoint, which exists
	// precisely to delete tens of thousands of rows in one atomic shot.
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin transaction")
		return
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(r.Context(), query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete transactions")
		return
	}

	deleted, err := result.RowsAffected()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read delete result")
		return
	}

	// filterDesc encodes the raw query string and the admin/own scope so an
	// operator replaying the audit log can reconstruct exactly which rows
	// the DELETE targeted, even after the underlying rows are gone.
	scope := "own"
	if user.Role == RoleAdmin {
		scope = "all"
	}
	filterDesc := fmt.Sprintf("delete-by-filter scope=%s query=%q", scope, r.URL.RawQuery)
	if err := h.txnStore.RecordBulkTx(r.Context(), tx, user.ID, database.AuditDelete, database.BulkAuditSummary{
		Count:  deleted,
		Filter: filterDesc,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record audit")
		return
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit delete-by-filter")
		return
	}

	writeJSON(w, http.StatusOK, map[string]int64{"deleted": deleted})
}

// toNullString converts a string to sql.NullString, treating empty as NULL.
func toNullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func (h *Handler) handleTransactionSuggestions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Suggestions are shared across all household users — same visibility as
	// handleListTransactions. No per-user scoping needed. Both queries filter
	// deleted_at IS NULL so suggestions never leak data from the trash; a
	// soft-deleted row whose description or tag only appears in the trash
	// should not autocomplete for users creating new transactions.
	descRows, err := h.db.QueryContext(ctx,
		`SELECT DISTINCT description FROM transactions WHERE deleted_at IS NULL ORDER BY description LIMIT ?`,
		DescriptionSuggestionLimit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query descriptions")
		return
	}
	defer descRows.Close()

	descriptions := []string{}
	for descRows.Next() {
		var d string
		if err := descRows.Scan(&d); err == nil && d != "" {
			descriptions = append(descriptions, d)
		}
	}
	if err := descRows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read descriptions")
		return
	}

	tagRows, err := h.db.QueryContext(ctx,
		`SELECT DISTINCT tags FROM transactions WHERE deleted_at IS NULL AND tags != '' AND tags IS NOT NULL LIMIT ?`,
		TagSuggestionLimit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query tags")
		return
	}
	defer tagRows.Close()

	seen := map[string]bool{}
	for tagRows.Next() {
		var raw string
		if err := tagRows.Scan(&raw); err != nil {
			continue
		}
		for _, t := range strings.Split(raw, ",") {
			t = strings.TrimSpace(t)
			if t != "" && !seen[t] {
				seen[t] = true
			}
		}
	}

	if err := tagRows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read tags")
		return
	}

	tags := make([]string, 0, len(seen))
	for t := range seen {
		tags = append(tags, t)
	}
	sort.Strings(tags)

	w.Header().Set("Cache-Control", "private, max-age=60")
	writeJSON(w, http.StatusOK, struct {
		Descriptions []string `json:"descriptions"`
		Tags         []string `json:"tags"`
	}{
		Descriptions: descriptions,
		Tags:         tags,
	})
}
