package api

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/database"
)

// sortColumnWhitelist maps frontend sort_by values to safe SQL column expressions.
var sortColumnWhitelist = map[string]string{
	"date":        "t.date",
	"amount":      "t.amount",
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

func toTransactionResponse(t database.Transaction) transactionResponse {
	resp := transactionResponse{
		ID:          t.ID,
		UserID:      t.UserID,
		Date:        t.Date.Format("2006-01-02"),
		Amount:      t.Amount,
		Description: t.Description,
		CategoryID:  t.CategoryID,
		CreatedAt:   t.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   t.UpdatedAt.Format(time.RFC3339),
	}
	if t.OriginalAmount.Valid {
		amt := t.OriginalAmount.Float64
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
	if page > 1000000 {
		page = 1000000
	}
	perPage := 25
	if v := q.Get("per_page"); v != "" {
		if pp, err := strconv.Atoi(v); err == nil && pp > 0 {
			perPage = pp
		}
	}
	if perPage > 100 {
		perPage = 100
	}

	whereClause, args := buildTransactionWhereClause(q)

	// Count query
	countQuery := "SELECT COUNT(*) FROM transactions t JOIN categories c ON t.category_id = c.id" + whereClause
	var total int
	if err := h.db.QueryRowContext(r.Context(), countQuery, args...).Scan(&total); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count transactions")
		return
	}

	// Sorting
	sortCol, sortDir := parseSortParams(q)
	orderClause := fmt.Sprintf(" ORDER BY %s %s, t.id %s", sortCol, sortDir, sortDir)

	// Data query
	offset := (page - 1) * perPage
	dataQuery := `SELECT t.id, t.user_id, t.date, t.amount, t.original_amount, t.original_currency,
		t.description, t.category_id, t.tags, t.notes, t.created_at, t.updated_at,
		c.name AS category_name, c.type AS category_type
		FROM transactions t
		JOIN categories c ON t.category_id = c.id` + whereClause + orderClause + ` LIMIT ? OFFSET ?`

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
			origAmt      sql.NullFloat64
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
			&tr.ID, &tr.UserID, &date, &tr.Amount, &origAmt, &origCur,
			&tr.Description, &tr.CategoryID, &tags, &notes, &createdAt, &updatedAt,
			&categoryName, &categoryType,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to scan transaction")
			return
		}
		tr.Date = date.Format("2006-01-02")
		tr.CreatedAt = createdAt.Format(time.RFC3339)
		tr.UpdatedAt = updatedAt.Format(time.RFC3339)
		tr.CategoryName = categoryName
		tr.CategoryType = categoryType
		if origAmt.Valid {
			v := origAmt.Float64
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

	txn, err := h.queries.CreateTransaction(r.Context(), database.CreateTransactionParams{
		UserID:           user.ID,
		Date:             date,
		Amount:           amount,
		OriginalAmount:   origAmt,
		OriginalCurrency: origCur,
		Description:      req.Description,
		CategoryID:       req.CategoryID,
		Tags:             toNullString(req.Tags),
		Notes:            toNullString(req.Notes),
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

	existing, err := h.queries.GetTransactionByID(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "transaction not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get transaction")
		return
	}

	// Ownership check: members can only edit their own
	if user.Role != "admin" && existing.UserID != user.ID {
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

	err = h.queries.UpdateTransaction(r.Context(), database.UpdateTransactionParams{
		Date:             date,
		Amount:           amount,
		OriginalAmount:   origAmt,
		OriginalCurrency: origCur,
		Description:      req.Description,
		CategoryID:       req.CategoryID,
		Tags:             toNullString(req.Tags),
		Notes:            toNullString(req.Notes),
		ID:               id,
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

	existing, err := h.queries.GetTransactionByID(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "transaction not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get transaction")
		return
	}

	if user.Role != "admin" && existing.UserID != user.ID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	if err := h.queries.DeleteTransaction(r.Context(), id); err != nil {
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
	if len(reqs) > 500 {
		writeError(w, http.StatusBadRequest, "batch size cannot exceed 500")
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

	qtx := h.queries.WithTx(tx)
	results := make([]transactionResponse, 0, len(reqs))

	for i, req := range reqs {
		date, _ := time.Parse("2006-01-02", req.Date)

		amount, origAmt, origCur, err := resolveCurrency(r.Context(), qtx, req)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("item %d: %s", i, err.Error()))
			return
		}

		txn, err := qtx.CreateTransaction(r.Context(), database.CreateTransactionParams{
			UserID:           user.ID,
			Date:             date,
			Amount:           amount,
			OriginalAmount:   origAmt,
			OriginalCurrency: origCur,
			Description:      req.Description,
			CategoryID:       req.CategoryID,
			Tags:             toNullString(req.Tags),
			Notes:            toNullString(req.Notes),
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
	if len(req.Description) > 500 {
		return fmt.Errorf("description must be 500 characters or less")
	}
	if len(req.Tags) > 500 {
		return fmt.Errorf("tags must be 500 characters or less")
	}
	if len(req.Notes) > 2000 {
		return fmt.Errorf("notes must be 2000 characters or less")
	}
	if req.CategoryID <= 0 {
		return fmt.Errorf("category_id is required")
	}
	// Amount validation: if no foreign currency, amount must be positive.
	// If foreign currency is specified, original_amount is checked in resolveCurrency.
	if req.OriginalCurrency == "" && req.Amount <= 0 {
		return fmt.Errorf("amount must be positive")
	}
	if math.IsInf(req.Amount, 0) || math.IsNaN(req.Amount) || req.Amount > 1_000_000_000 {
		return fmt.Errorf("amount exceeds maximum allowed value")
	}
	if req.OriginalAmount != nil && (math.IsInf(*req.OriginalAmount, 0) || math.IsNaN(*req.OriginalAmount) || *req.OriginalAmount > 1_000_000_000) {
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
	if len(req.NewDescription) > 500 {
		writeError(w, http.StatusBadRequest, "new_description must be 500 characters or less")
		return
	}

	// Escape SQL LIKE wildcards (same pattern as buildTransactionWhereClause)
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(req.Search)

	var result sql.Result
	var err error
	if user.Role == "admin" {
		result, err = h.db.ExecContext(r.Context(),
			`UPDATE transactions SET description = ?, updated_at = CURRENT_TIMESTAMP WHERE description LIKE ? ESCAPE '\'`,
			req.NewDescription, "%"+escaped+"%",
		)
	} else {
		result, err = h.db.ExecContext(r.Context(),
			`UPDATE transactions SET description = ?, updated_at = CURRENT_TIMESTAMP WHERE description LIKE ? ESCAPE '\' AND user_id = ?`,
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
	if len(req.IDs) > 500 {
		writeError(w, http.StatusBadRequest, "batch size cannot exceed 500")
		return
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin transaction")
		return
	}
	defer tx.Rollback()

	qtx := h.queries.WithTx(tx)
	deleted := 0

	for _, id := range req.IDs {
		existing, err := qtx.GetTransactionByID(r.Context(), id)
		if err != nil {
			continue
		}

		if user.Role != "admin" && existing.UserID != user.ID {
			continue
		}

		if err := qtx.DeleteTransaction(r.Context(), id); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to delete transaction")
			return
		}
		deleted++
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit batch delete")
		return
	}

	writeJSON(w, http.StatusOK, map[string]int{"deleted": deleted})
}

// toNullString converts a string to sql.NullString, treating empty as NULL.
func toNullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
