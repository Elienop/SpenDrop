package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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
	"created_at":  "t.created_at",
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
// shape. Phase 3.1a/3.1b: reads t.AmountCents / t.OriginalAmountCents (the
// integer cents columns) and converts to dollars exactly once via
// centsToDollars. The legacy t.amount / t.original_amount REAL columns were
// dropped in migration 010 — integer cents are now the only money columns,
// so the handler path cannot touch a float sum and float drift is impossible
// by construction for every aggregation that flows through this function.
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
	// Round to 2 decimal places. This is redundant with dollarsToCents (the
	// single wire-edge rounding chokepoint, cents.go), which re-rounds *100 on
	// the same scale and always agrees — so it is provably a no-op on the
	// stored cents. Kept deliberately to avoid churning a money path for zero
	// behavior change; see audit item h-resolvecurrency-double-round.
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
	// Reads t.amount_cents (int64); the legacy REAL amount column was dropped
	// in migration 010 (Phase 3.1b), so cents is the only money column. The
	// list path consumes cents and converts to dollars exactly once at the
	// wire edge via centsToDollars — no per-row float round-trip, so no
	// IEEE-754 drift in the list endpoint.
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
		AmountCents:         dollarsToCents(amount),
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

	// Phase 3.3: re-verify every checkpoint on or after the new row's date.
	// Best-effort; logs but never fails the mutation. See the comment on
	// verifyAffectedCheckpoints for the error contract.
	h.verifyAffectedCheckpoints(r.Context(), txn.Date)

	// Phase C (Task 17): fire the over-budget alert hook for the cell the new
	// row lands in. Post-commit, best-effort — never blocks or errors the
	// already-committed mutation. Cell is derived from the row's own date.
	h.evaluateBudgetAlerts(r.Context(), cellsForCreate(txn.CategoryID, txn.Date))

	// Activity notification (post-commit, best-effort). notifyTxnAdded applies
	// large-txn precedence internally; both no-op when the type is disabled.
	h.notifyTxnAdded(r.Context(), txn, user.ID)

	// Live-updates broadcast (post-commit, best-effort, nil-safe): tell every
	// open household device to refetch. A new row changes the list, the
	// dashboard totals, the reports aggregates, and the budget cells.
	h.publishInvalidate("transactions", "dashboard", "reports", "budgets")

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
		AmountCents:         dollarsToCents(amount),
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

	// Phase 3.3: an update can change either the amount or the date, so
	// the earliest affected checkpoint is bounded by MIN(old_date, new_date).
	// existing.Date is the pre-edit value we already loaded for the TOCTOU
	// check; `date` is the post-edit value. The verifier walks every
	// checkpoint on or after that minimum.
	h.verifyAffectedCheckpoints(r.Context(), earliestDate(existing.Date, date))

	// Phase C (Task 17): an edit can move the row between categories and/or
	// months — evaluate both the OLD cell (its spend may have dropped under,
	// clearing the latch) and the NEW cell (it may have crossed over).
	// Post-commit, best-effort.
	h.evaluateBudgetAlerts(r.Context(),
		cellsForUpdate(existing.CategoryID, existing.Date, req.CategoryID, date))

	// Activity notification. Re-read the committed row so AmountCents reflects
	// the edit for large-txn precedence; a read miss simply drops the notify
	// (best-effort) and never fails the already-committed update.
	// GetTransactionByID returns GetTransactionByIDRow; the notify helpers take
	// database.Transaction, so lift the three fields they use.
	if updated, err := h.queries.GetTransactionByID(r.Context(), id); err == nil {
		h.notifyTxnEdited(r.Context(), database.Transaction{
			AmountCents: updated.AmountCents,
			CategoryID:  updated.CategoryID,
			Description: updated.Description,
		}, user.ID)
	}

	// Live-updates broadcast (post-commit, best-effort, nil-safe): an edit can
	// move amount/category/date, so list, dashboard totals, reports, and budget
	// cells may all change.
	h.publishInvalidate("transactions", "dashboard", "reports", "budgets")

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

	// Phase 3.3: the tombstoned row stops contributing to the live SUM,
	// so every checkpoint on or after its date needs to be re-verified.
	// existing.Date is the pre-delete copy we loaded for the TOCTOU check.
	h.verifyAffectedCheckpoints(r.Context(), existing.Date)

	// Phase C (Task 17): a delete drops the row's spend, so its cell may now be
	// back under the limit — evaluating clears the latch (and re-arms a future
	// re-cross). Post-commit, best-effort.
	h.evaluateBudgetAlerts(r.Context(), cellsForDelete(existing.CategoryID, existing.Date))

	// Activity notification. Use the pre-delete copy we already loaded so the
	// body still has the amount/category/description of the removed row.
	// existing is a GetTransactionByIDRow; lift the fields the helper uses.
	h.notifyTxnDeleted(r.Context(), database.Transaction{
		AmountCents: existing.AmountCents,
		CategoryID:  existing.CategoryID,
		Description: existing.Description,
	}, user.ID)

	// Live-updates broadcast (post-commit, best-effort, nil-safe): a delete
	// tombstones the row — it leaves the list/dashboard/reports/budgets and
	// appears in trash, so invalidate trash too.
	h.publishInvalidate("transactions", "dashboard", "reports", "budgets", "trash")

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
	// Track the earliest date across the batch for the post-commit
	// checkpoint hook; a zero value means "no rows committed yet" and
	// suppresses the hook call entirely.
	var minBatchDate time.Time
	// Distinct (category, month) cells touched by this batch, for the
	// post-commit over-budget hook (Phase C, Task 17).
	cellSet := map[budgetCell]struct{}{}

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
			AmountCents:         dollarsToCents(amount),
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

		minBatchDate = earliestDate(minBatchDate, txn.Date)
		cellSet[cellForDate(txn.CategoryID, txn.Date)] = struct{}{}
		results = append(results, toTransactionResponse(txn))
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit batch")
		return
	}

	// Phase 3.3: re-verify every checkpoint on or after the earliest
	// inserted date. The post-commit position is deliberate — the hook
	// must see the rows as they exist in the committed live set. A zero
	// minBatchDate means the batch produced zero successful inserts, in
	// which case the loop short-circuits with no query.
	if !minBatchDate.IsZero() {
		h.verifyAffectedCheckpoints(r.Context(), minBatchDate)
	}

	// Phase C (Task 17): evaluate the over-budget alert for every distinct
	// (category, month) cell the batch touched. Post-commit, best-effort.
	if len(cellSet) > 0 {
		cells := make([]budgetCell, 0, len(cellSet))
		for c := range cellSet {
			cells = append(cells, c)
		}
		h.evaluateBudgetAlerts(r.Context(), cells)
	}

	// Activity notification: ONE aggregate push for the whole batch, never
	// one-per-row. len(results) is the count of successful inserts.
	if n := len(results); n > 0 {
		h.notifyTxnBatch(r.Context(), "added", n, user.ID)
	}

	// Live-updates broadcast (post-commit, best-effort, nil-safe): ONE signal
	// for the whole batch, mirroring the aggregate notify. New rows change the
	// list, dashboard totals, reports, and budget cells.
	if len(results) > 0 {
		h.publishInvalidate("transactions", "dashboard", "reports", "budgets")
	}

	writeJSON(w, http.StatusCreated, results)
}

// validateDate parses YYYY-MM-DD and returns the canonical form. Mirrors
// the date check in validateTransactionRequest but isolated so bulk-edit
// can call it without forcing all-fields validation.
func validateDate(s string) (string, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return "", fmt.Errorf("date must be in YYYY-MM-DD format")
	}
	return t.Format("2006-01-02"), nil
}

// validateDescription trims, then length-checks the trimmed value against
// MaxDescriptionLength. This is the bulk-edit-only helper; the single-row
// validateTransactionRequest path inlines its own raw length check
// deliberately to preserve legacy behavior (whitespace-only descriptions
// stay accepted there). See spec §5.5b for the divergence rationale.
func validateDescription(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("description must not be empty")
	}
	if len(s) > MaxDescriptionLength {
		return "", fmt.Errorf("description must be %d characters or less", MaxDescriptionLength)
	}
	return s, nil
}

// validateCategoryID matches the existing single-row laxity — id > 0
// only. The is_active / existence check is deliberately NOT added here;
// doing so on bulk-edit alone would create a divergence with the
// single-row endpoint. See spec §5.5b.
func validateCategoryID(id int64) (int64, error) {
	if id <= 0 {
		return 0, fmt.Errorf("category_id is required")
	}
	return id, nil
}

// validateTagsField length-checks against MaxTagsLength. Tags storage is
// verbatim — no lowercase, no canonical normalization. See spec §3.3.
func validateTagsField(s string) (string, error) {
	if len(s) > MaxTagsLength {
		return "", fmt.Errorf("tags must be %d characters or less", MaxTagsLength)
	}
	return s, nil
}

// validateTransactionRequest checks required fields on a transaction request.
// Composes the per-field validators above for date / tags / category_id /
// notes — but inlines its own raw description length check because
// validateDescription trims before measuring (a bulk-edit semantic) and
// the single-row endpoint must keep accepting whitespace-only descriptions
// for legacy compatibility. See spec §5.5b.
func validateTransactionRequest(req transactionRequest) error {
	if req.Date == "" {
		return fmt.Errorf("date is required")
	}
	if _, err := validateDate(req.Date); err != nil {
		return err
	}
	if req.Description == "" {
		return fmt.Errorf("description is required")
	}
	// Inline raw length check — preserve the legacy single-row behavior.
	// The trimming validateDescription helper exists for the bulk-edit
	// path, not this one.
	if len(req.Description) > MaxDescriptionLength {
		return fmt.Errorf("description must be %d characters or less", MaxDescriptionLength)
	}
	if _, err := validateTagsField(req.Tags); err != nil {
		return err
	}
	if len(req.Notes) > MaxNotesLength {
		return fmt.Errorf("notes must be %d characters or less", MaxNotesLength)
	}
	if _, err := validateCategoryID(req.CategoryID); err != nil {
		return err
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

// patchRequest is the JSON wire shape for the bulk-edit `patch` field.
// Pointer fields mean "unset = do not touch" (json-omitempty semantics).
// buildUpdatePatch lifts this into database.UpdatePatch after per-field
// validation; the wire shape lives in api so the database package stays
// free of HTTP/JSON concerns.
type patchRequest struct {
	Date        *string `json:"date,omitempty"`
	Description *string `json:"description,omitempty"`
	CategoryID  *int64  `json:"category_id,omitempty"`
	Tags        *string `json:"tags,omitempty"`
}

// validTagsModes pins the three accepted tag-merge modes per spec §3.3.
// "add", "remove", and "replace" are the only legal values; case-sensitive
// and trimmed-equal — surrounding whitespace or different casing must
// 400 because that's almost certainly a client bug rather than a typo we
// can helpfully recover from.
var validTagsModes = map[string]struct{}{"add": {}, "remove": {}, "replace": {}}

// buildUpdatePatch validates the wire-format patchRequest and lifts each
// present field into database.UpdatePatch. Validators are the per-field
// validators above; no bulk-specific logic here. Empty patch is NOT
// rejected here — IsEmpty() exists for that, and the handler decides
// whether an empty patch is a 400 or a no-op (current spec: 400).
//
// tagsMode is paired with patch.Tags: required iff tags is set, rejected
// if set without tags. The cross-field check lives here so neither
// caller (batch or filter handler) has to repeat it.
func buildUpdatePatch(req patchRequest, tagsMode *string) (database.UpdatePatch, error) {
	var out database.UpdatePatch
	if req.Date != nil {
		d, err := validateDate(*req.Date)
		if err != nil {
			return out, err
		}
		out.Date = &d
	}
	if req.Description != nil {
		d, err := validateDescription(*req.Description)
		if err != nil {
			return out, err
		}
		out.Description = &d
	}
	if req.CategoryID != nil {
		c, err := validateCategoryID(*req.CategoryID)
		if err != nil {
			return out, err
		}
		out.CategoryID = &c
	}
	if req.Tags != nil {
		// Empty-string tags are rejected server-side per spec §3.3 line 81.
		// The frontend already strips empty `tags` from the patch object
		// before submitting; an empty value arriving here is either a
		// hand-crafted client or a bypass attempt at the v1-forbidden
		// "Replace with empty" bulk-clear-all-tags. Reject before any mode
		// or length check so the error is "tags must not be empty" rather
		// than the less-specific "tagsMode required ..." or a silent
		// length-pass.
		if *req.Tags == "" {
			return out, fmt.Errorf("tags must not be empty when set")
		}
		if tagsMode == nil {
			return out, fmt.Errorf("tagsMode required when tags is set")
		}
		if _, ok := validTagsModes[*tagsMode]; !ok {
			return out, fmt.Errorf("tagsMode must be one of: add, remove, replace")
		}
		t, err := validateTagsField(*req.Tags)
		if err != nil {
			return out, err
		}
		out.Tags = &t
		out.TagsMode = tagsMode
	} else if tagsMode != nil {
		return out, fmt.Errorf("tags required when tagsMode is set")
	}
	return out, nil
}

// summarizePatchMaxLen caps the audit-summary string so pathological input
// (e.g. a 100KB description) cannot bloat the audit row's `before_json`
// envelope. See spec §5.4.
const summarizePatchMaxLen = 1024

// summarizePatch renders the patch as a stable, human-readable string for
// audit-table greppability. Stable format pinned by the spec — see
// docs/superpowers/specs/2026-05-01-transactions-bulk-edit-design.md §5.4.
//
// Field order is fixed: date, description, category_id, tags. Embedded
// quotes in description are escaped \"; semicolons left raw (the audit
// consumer is human/grep, not a parser). Output is capped at
// summarizePatchMaxLen with a "…(truncated)" suffix on overflow.
func summarizePatch(p database.UpdatePatch) string {
	var parts []string
	if p.Date != nil {
		parts = append(parts, "date="+*p.Date)
	}
	if p.Description != nil {
		escaped := strings.ReplaceAll(*p.Description, `"`, `\"`)
		parts = append(parts, fmt.Sprintf(`description="%s"`, escaped))
	}
	if p.CategoryID != nil {
		parts = append(parts, fmt.Sprintf("category_id=%d", *p.CategoryID))
	}
	if p.Tags != nil {
		mode := ""
		if p.TagsMode != nil {
			mode = *p.TagsMode
		}
		parts = append(parts, fmt.Sprintf("tags=%s(%s)", *p.Tags, mode))
	}
	out := strings.Join(parts, "; ")
	if len(out) > summarizePatchMaxLen {
		const suffix = "…(truncated)"
		// Walk the cut index back to the nearest rune start so we never
		// slice mid-multibyte-UTF-8. Without this guard, a description
		// dominated by 3-byte runes (e.g. "…") slices on a continuation
		// byte and corrupts the audit row's before_json envelope when
		// the JSON encoder later encounters the broken rune.
		cut := summarizePatchMaxLen - len(suffix)
		for cut > 0 && !utf8.RuneStart(out[cut]) {
			cut--
		}
		out = out[:cut] + suffix
	}
	return out
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

	// Live-updates broadcast (post-commit, best-effort, nil-safe): a rename only
	// changes descriptions (no amount/date/category move), so the list and the
	// description-keyed report aggregates change; dashboard totals and budget
	// cells are untouched.
	if rowsAffected > 0 {
		h.publishInvalidate("transactions", "reports")
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
	// Earliest date among rows actually tombstoned by this batch.
	// Stays zero if every requested ID was skipped, which suppresses
	// the post-commit checkpoint hook (nothing changed, nothing to
	// re-verify).
	var minDeletedDate time.Time
	// Distinct (category, month) cells touched by this batch, for the
	// post-commit over-budget hook (Phase C, Task 17).
	cellSet := map[budgetCell]struct{}{}

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
		minDeletedDate = earliestDate(minDeletedDate, existing.Date)
		cellSet[cellForDate(existing.CategoryID, existing.Date)] = struct{}{}
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

	// Phase 3.3: reverify every checkpoint on or after the earliest
	// tombstoned row's date. A zero minDeletedDate means every requested
	// ID was skipped (missing / tombstoned / not owned) so no rows
	// actually changed and the hook is a no-op.
	if !minDeletedDate.IsZero() {
		h.verifyAffectedCheckpoints(r.Context(), minDeletedDate)
	}

	// Phase C (Task 17): evaluate the over-budget alert for every distinct
	// (category, month) cell the batch tombstoned. Post-commit, best-effort.
	if len(cellSet) > 0 {
		cells := make([]budgetCell, 0, len(cellSet))
		for c := range cellSet {
			cells = append(cells, c)
		}
		h.evaluateBudgetAlerts(r.Context(), cells)
	}

	// Activity notification: ONE aggregate push for the whole batch delete.
	if deleted > 0 {
		h.notifyTxnBatch(r.Context(), "deleted", deleted, user.ID)
	}

	// Live-updates broadcast (post-commit, best-effort, nil-safe): ONE signal
	// for the whole batch. Tombstoned rows leave the list/dashboard/reports/
	// budgets and appear in trash.
	if deleted > 0 {
		h.publishInvalidate("transactions", "dashboard", "reports", "budgets", "trash")
	}

	writeJSON(w, http.StatusOK, map[string]int{"deleted": deleted})
}

// batchUpdateRequest is the JSON input for /api/transactions/batch-update.
// Pointer fields on Patch make absent keys mean "no change"; the wire shape
// matches the patchRequest struct used by future filter-mode handlers.
//
// TagsMode lives at the top level (not nested inside patch) so the JSON
// envelope mirrors the spec §6.1 wire format and matches Lidarr's
// proven-in-production shape that the design is derived from.
type batchUpdateRequest struct {
	IDs      []int64      `json:"ids"`
	Patch    patchRequest `json:"patch"`
	TagsMode *string      `json:"tagsMode,omitempty"`
}

// bulkUpdateResponse is the success payload for both batch-update (always
// emits skipped) and update-by-filter (omits skipped via omitempty since
// filter-mode's WHERE clause already excluded skip-eligible rows).
type bulkUpdateResponse struct {
	Updated int64 `json:"updated"`
	Skipped int64 `json:"skipped,omitempty"`
}

// applyTagsMode is the per-row tag set-arithmetic. Existing tags are split
// on commas and trimmed of leading/trailing whitespace per item; new tags
// are split the same way. Set arithmetic is byte-for-byte case-sensitive —
// see spec §3.3 worked example: "Tax, receipt" + Add "tax" yields
// "Tax, receipt, tax" (three distinct entries because "Tax" and "tax" are
// different bytes).
//
// Mode = "replace" returns newTags verbatim — the spec deliberately does
// NOT canonicalize replace-mode input, so casing and whitespace inside the
// user's typed string survive the round-trip unchanged.
//
// Output for add/remove uses ", " (comma-space) as the join separator,
// matching the canonical inline-edit output in the existing codebase.
// Input parsing tolerates both "a,b" and "a, b" — the trim makes the
// distinction invisible to set arithmetic.
func applyTagsMode(existing, newTags, mode string) string {
	if mode == "replace" {
		return newTags
	}
	parse := func(s string) []string {
		if s == "" {
			return nil
		}
		parts := strings.Split(s, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			t := strings.TrimSpace(p)
			if t != "" {
				out = append(out, t)
			}
		}
		return out
	}
	cur := parse(existing)
	in := parse(newTags)
	seen := make(map[string]struct{}, len(cur))
	for _, t := range cur {
		seen[t] = struct{}{}
	}
	switch mode {
	case "add":
		for _, t := range in {
			if _, ok := seen[t]; !ok {
				cur = append(cur, t)
				seen[t] = struct{}{}
			}
		}
	case "remove":
		rm := make(map[string]struct{}, len(in))
		for _, t := range in {
			rm[t] = struct{}{}
		}
		out := cur[:0]
		for _, t := range cur {
			if _, drop := rm[t]; !drop {
				out = append(out, t)
			}
		}
		cur = out
	}
	return strings.Join(cur, ", ")
}

// handleBatchUpdateTransactions applies a partial-field patch to a list of
// transaction IDs in one tx. Tombstoned / non-owned / missing rows are
// skipped (consistent with handleBatchDeleteTransactions). Per-row audit
// rows commit alongside the data updates; a summary audit row records
// skipped count if any. Mid-batch SQL/constraint errors roll back the
// entire tx — caller sees no partial progress.
//
// The handler uses dec.DisallowUnknownFields() as the mass-assignment guard
// (spec §5.5b): an attacker-controlled body that injects user_id, deleted_at,
// or any future-added field that bulk-edit hasn't been reviewed for must
// 400 rather than be silently dropped onto the typed struct.
//
// Tag merge is computed per-row in this handler (NOT in the store): the
// store stays dumb about set arithmetic. We pre-load each row's current
// tags via h.queries.GetTransactionByID, run applyTagsMode, and pass the
// resolved string to UpdateTx. The N+1 GetTransactionByID is acceptable at
// MaxBatchUpdateIDs=500; the alternative (folding tag-merge into the store
// API) complicates the store for one caller. GetTransactionByID
// deliberately leaks tombstoned rows so we don't need to redo the live-row
// check here — UpdateTx returns ErrTombstoned for the tombstoned row and
// the switch below skips it.
//
// See spec docs/superpowers/specs/2026-05-01-transactions-bulk-edit-design.md
// §5.3 for the design rationale.
func (h *Handler) handleBatchUpdateTransactions(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req batchUpdateRequest
	r.Body = http.MaxBytesReader(w, r.Body, getMaxJSONBytes())
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields() // mass-assignment guard — spec §5.5b
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "at least one id is required")
		return
	}
	if len(req.IDs) > MaxBatchUpdateIDs {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("batch size cannot exceed %d", MaxBatchUpdateIDs))
		return
	}
	patch, err := buildUpdatePatch(req.Patch, req.TagsMode)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if patch.IsEmpty() {
		writeError(w, http.StatusBadRequest, "patch must not be empty")
		return
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin transaction")
		return
	}
	defer tx.Rollback()

	// qtx is used for the per-row tag pre-load when patch.Tags is set.
	// UpdateTx itself reloads inside the same tx, but the merge happens
	// HERE so we can pass an already-resolved Tags string into UpdateTx.
	qtx := h.queries.WithTx(tx)

	var updated, skipped int64
	var minDate time.Time
	// Distinct (category, month) cells touched by this batch, for the
	// post-commit over-budget hook (Phase C, Task 17). UpdateTx returns
	// before/after rows carrying CategoryID and Date, so both the OLD cell
	// (whose spend dropped) and the NEW cell (which may have crossed) are
	// enumerable per row — unlike the single-SQL filter-update path.
	cellSet := map[budgetCell]struct{}{}

	for _, id := range req.IDs {
		// Compute per-row patch. For non-replace tag modes we need the
		// row's current tags to perform set arithmetic; for "replace"
		// mode the new tags are passed through verbatim and no pre-load
		// is needed.
		rowPatch := patch
		if patch.Tags != nil && *patch.TagsMode != "replace" {
			existing, err := qtx.GetTransactionByID(r.Context(), id)
			switch {
			case errors.Is(err, sql.ErrNoRows):
				skipped++
				continue
			case err != nil:
				writeError(w, http.StatusInternalServerError, "failed to load existing tags")
				return
			}
			// GetTransactionByID deliberately leaks tombstoned rows; let
			// UpdateTx be the canonical place that maps tombstoned -> skip.
			// We still pre-compute merged tags either way — the merged
			// string is harmless if UpdateTx subsequently skips the row.
			curTags := ""
			if existing.Tags.Valid {
				curTags = existing.Tags.String
			}
			merged := applyTagsMode(curTags, *patch.Tags, *patch.TagsMode)
			rowPatch.Tags = &merged
		}

		before, after, err := h.txnStore.UpdateTx(r.Context(), tx, user.ID, id, rowPatch)
		switch {
		case errors.Is(err, database.ErrTombstoned),
			errors.Is(err, database.ErrNotOwned),
			errors.Is(err, database.ErrNotFound):
			skipped++
		case err != nil:
			// Any non-skip error rolls back the entire batch.
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		default:
			updated++
			if patch.Date != nil {
				// earliestDate is 2-arg; nest the call to fold three
				// values into one floor.
				minDate = earliestDate(minDate, earliestDate(before.Date, after.Date))
			}
			// Record both the old and new cells: a category/date patch
			// relocates the row's spend, dropping the old cell (latch may
			// clear) and possibly crossing the new cell over budget. When
			// neither changed, both map to the same cell and dedupe.
			cellSet[cellForDate(before.CategoryID, before.Date)] = struct{}{}
			cellSet[cellForDate(after.CategoryID, after.Date)] = struct{}{}
		}
	}

	if skipped > 0 {
		// Skip-summary RecordBulkTx is inside the data tx — rollback
		// rolls both the data updates and this audit row back together.
		// RecordBulkTx wraps the JSON envelope itself; pass the struct,
		// not a marshalled string.
		if err := h.txnStore.RecordBulkTx(r.Context(), tx, user.ID, database.AuditUpdate,
			database.BulkAuditSummary{
				Count:  skipped,
				Filter: fmt.Sprintf("skipped_during_batch_update:%d_of_%d", skipped, len(req.IDs)),
			}); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to record audit")
			return
		}
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit batch update")
		return
	}

	// Phase 3.3 checkpoint hook: only fires when patch.Date was set AND at
	// least one row was actually updated (minDate stays zero otherwise).
	if patch.Date != nil && !minDate.IsZero() {
		h.verifyAffectedCheckpoints(r.Context(), minDate)
	}

	// Phase C (Task 17): evaluate the over-budget alert for every distinct
	// (category, month) cell this batch's old/new row positions touched.
	// Post-commit, best-effort.
	if len(cellSet) > 0 {
		cells := make([]budgetCell, 0, len(cellSet))
		for c := range cellSet {
			cells = append(cells, c)
		}
		h.evaluateBudgetAlerts(r.Context(), cells)
	}

	// Live-updates broadcast (post-commit, best-effort, nil-safe): a patch can
	// move amount/category/date across rows, so list, dashboard totals, reports,
	// and budget cells may all change. Fire only when at least one row updated.
	if updated > 0 {
		h.publishInvalidate("transactions", "dashboard", "reports", "budgets")
	}

	writeJSON(w, http.StatusOK, bulkUpdateResponse{Updated: updated, Skipped: skipped})
}

// enumerateFilterCells returns the DISTINCT (category, calendar-month) cells
// occupied by the live rows a bulk-filter operation matches. It runs the SAME
// JOIN + liveClause the delete/update-by-filter handlers use to select rows,
// so the enumerated cells are exactly the rows about to be mutated. The caller
// passes the already-built liveClause (buildTransactionWhereClause +
// non-admin user_id append + appendLiveTransactionsFilter) and its args so the
// enumeration scope matches the mutation scope byte-for-byte.
//
// Soft-delete: liveClause carries "AND t.deleted_at IS NULL" via
// appendLiveTransactionsFilter, so a tombstoned sentinel row never enters the
// cell set (TestEnumerateFilterCells_HidesTombstoned). The filter family uses
// raw dynamic SQL in the handler because its WHERE is built from runtime query
// params and cannot be a static query — but the soft-delete predicate stays
// centralized in appendLiveTransactionsFilter.
func (h *Handler) enumerateFilterCells(ctx context.Context, liveClause string, args []any) ([]budgetCell, error) {
	query := `SELECT DISTINCT t.category_id,
		CAST(strftime('%Y', t.date) AS INTEGER) AS year,
		CAST(strftime('%m', t.date) AS INTEGER) AS month
		FROM transactions t
		JOIN categories c ON t.category_id = c.id` + liveClause
	rows, err := h.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("enumerate filter cells: %w", err)
	}
	defer rows.Close()
	var cells []budgetCell
	for rows.Next() {
		var cell budgetCell
		var year, month int64
		if err := rows.Scan(&cell.CategoryID, &year, &month); err != nil {
			return nil, fmt.Errorf("scan filter cell: %w", err)
		}
		cell.Year = int(year)
		cell.Month = int(month)
		cells = append(cells, cell)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("filter cell rows: %w", err)
	}
	return cells, nil
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

	// Phase 3.3: filter-delete doesn't enumerate per-row dates, and pulling
	// MIN(date) up-front would require a second round trip to the same
	// subquery. Since this endpoint exists specifically for wipe-and-reimport
	// workflows that already touch tens of thousands of rows, conservatively
	// reverify every checkpoint by passing a zero time.Time — the helper
	// treats that as "walk all checkpoints regardless of date".
	if deleted > 0 {
		h.verifyAffectedCheckpoints(r.Context(), time.Time{})
	}

	// Budget-alert hook intentionally skipped here: the bulk UPDATE does not
	// expose per-row (category, month), so there is no precise cell to
	// evaluate. Re-import / wipe workflows re-trip alerts on the next
	// single-row or batch-create. (Phase C, Task 17.)

	// Live-updates broadcast (post-commit, best-effort, nil-safe): tombstoned
	// rows leave the list/dashboard/reports and appear in trash. Budgets are
	// omitted to match the budget-alert skip above (no precise cell enumerated).
	if deleted > 0 {
		h.publishInvalidate("transactions", "dashboard", "reports", "trash")
	}

	writeJSON(w, http.StatusOK, map[string]int64{"deleted": deleted})
}

// updateByFilterRequest is the JSON wire shape for
// /api/transactions/update-by-filter. Patch fields use the same
// patchRequest shape as batch-update; TagsMode lives at the top level
// (not nested in patch) so the JSON envelope mirrors the batch-update
// wire format and matches Lidarr's proven-in-production shape.
type updateByFilterRequest struct {
	Patch    patchRequest `json:"patch"`
	TagsMode *string      `json:"tagsMode,omitempty"`
}

// handleUpdateTransactionsByFilter applies a partial-field patch to every
// transaction matching the filter querystring. It exists for the "Select all
// X across pages" UI action where the user wants to bulk-edit thousands of
// rows in one atomic operation — the ID-based batch-update endpoint would
// require chunking and expose the user to partial-failure states.
//
// Atomicity: one *sql.Tx wraps the data update and the summary audit row,
// so the audit row commits if and only if the data updates commit. Per spec
// §5.4 the summary row is emitted **unconditionally** — even when the filter
// matched zero rows. The audit table records the user's *attempt* against
// filter X, not just successful work.
//
// Two SQL paths, mutually exclusive at the handler level:
//   - patch.Tags == nil  → single SQL UPDATE (no-tags fast path; this task)
//   - patch.Tags != nil  → enumerate-then-write loop (tags branch; Task 5)
//
// Mass-assignment guard: dec.DisallowUnknownFields() so an attacker-crafted
// body carrying user_id, deleted_at, or any future-added field that
// bulk-edit hasn't been reviewed for must 400 rather than be silently
// dropped onto the typed struct (spec §5.5b).
//
// Soft-delete invariant (CLAUDE.md): both the inner appendLiveTransactionsFilter
// and the outer "AND deleted_at IS NULL" predicate on the UPDATE are
// load-bearing. The inner filter scopes the subquery to live rows; the outer
// is defense-in-depth so a previously-tombstoned row cannot have its
// deleted_at re-stamped. SQLite's snapshot isolation within a single
// statement makes the inner sufficient in practice, but the outer guard
// makes the invariant explicit at the SQL level for future maintainers.
//
// Race note: a deliberate TOCTOU window exists between the UI's `total`
// read and this UPDATE — rows inserted into the filter window in that
// interval are also updated. That's acceptable for the bulk-edit use case
// this endpoint exists to serve (and matches handleDeleteTransactionsByFilter's
// equivalent acceptance). The response echoes the real count.
//
// See spec docs/superpowers/specs/2026-05-01-transactions-bulk-edit-design.md
// §3.6 (audit unconditional), §3.10 (checkpoint hook), §5.4 (handler shape),
// §5.6 (filter SQL scaffolding).
func (h *Handler) handleUpdateTransactionsByFilter(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req updateByFilterRequest
	r.Body = http.MaxBytesReader(w, r.Body, getMaxJSONBytes())
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields() // mass-assignment guard — spec §5.5b
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	patch, err := buildUpdatePatch(req.Patch, req.TagsMode)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if patch.IsEmpty() {
		writeError(w, http.StatusBadRequest, "patch must not be empty")
		return
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin transaction")
		return
	}
	defer tx.Rollback()

	var updated int64
	var minDate time.Time

	if patch.Tags != nil {
		// Tags read-then-write path: enumerate matching rows, compute new
		// tags per row via applyTagsMode, and write them back. Mutually
		// exclusive with the no-tags fast path at this gate — only one
		// branch runs per request, so the summary audit row + checkpoint
		// hook below fire exactly once.
		updated, minDate, err = h.runUpdateByFilterTags(r, tx, user, patch)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update transactions")
			return
		}
	} else {
		updated, minDate, err = h.runUpdateByFilterNoTags(r, tx, user, patch)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update transactions")
			return
		}
	}

	// Summary audit row — emitted unconditionally (count=0 is valid; spec §5.4).
	// The summary row carries BOTH the raw query string (so an operator
	// replaying the audit log can rerun the filter) AND the patch summary
	// (so the operator knows what change was attempted) in a single
	// human-greppable Filter field. The raw URL.RawQuery is %q-quoted so
	// trailing whitespace, embedded quotes, and escape characters survive
	// a round-trip.
	patchSummary := summarizePatch(patch)
	filterDesc := fmt.Sprintf("update-by-filter query=%q patch=%s", r.URL.RawQuery, patchSummary)
	if err := h.txnStore.RecordBulkTx(r.Context(), tx, user.ID, database.AuditUpdate, database.BulkAuditSummary{
		Count:  updated,
		Filter: filterDesc,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record audit")
		return
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit update-by-filter")
		return
	}

	// Phase 3.3 checkpoint hook (spec §3.10): only fires when patch.Date was
	// set. The no-tags fast path doesn't enumerate per-row dates, so the
	// hook is invoked with a zero time.Time — the verifier treats that as
	// "walk all checkpoints regardless of date" (matches
	// handleDeleteTransactionsByFilter's conservative reverify).
	if patch.Date != nil {
		h.verifyAffectedCheckpoints(r.Context(), minDate)
	}

	// Budget-alert hook intentionally skipped here: the no-tags fast path is a
	// single SQL UPDATE that does not enumerate per-row (category, month), so
	// there is no precise old/new cell to evaluate (unlike the ID-list
	// handleBatchUpdateTransactions, which does enumerate). Wipe-and-reimport
	// workflows re-trip alerts on the next single-row, batch-create, or
	// import. (Phase C, Task 17.)

	// Live-updates broadcast (post-commit, best-effort, nil-safe): a filter
	// update can move amount/category/date, so list, dashboard totals, and
	// reports may change. Budgets are omitted to match the budget-alert skip
	// above (no precise cell enumerated). Fire only when rows actually updated.
	if updated > 0 {
		h.publishInvalidate("transactions", "dashboard", "reports")
	}

	writeJSON(w, http.StatusOK, map[string]int64{"updated": updated})
}

// runUpdateByFilterNoTags issues a single UPDATE inside the caller's tx.
// SQL shape mirrors handleDeleteTransactionsByFilter — same helpers, same
// JOIN, same user_id append, same live-filter wrap.
//
// Returns the rows-affected count and a zero time.Time (the no-tags path
// does not enumerate per-row dates; the caller's checkpoint hook treats
// zero as "reverify all").
//
// Soft-delete invariant: appendLiveTransactionsFilter adds the inner
// "AND t.deleted_at IS NULL" predicate (load-bearing); the outer
// "AND deleted_at IS NULL" on the UPDATE is defense-in-depth. Both are
// required — see the long comment on handleUpdateTransactionsByFilter.
func (h *Handler) runUpdateByFilterNoTags(r *http.Request, tx *sql.Tx, user database.User, patch database.UpdatePatch) (int64, time.Time, error) {
	// Build the SET clause from the patch. Field order (date, description,
	// category_id) is fixed for query-plan stability; updated_at is always
	// stamped last so a future patch field added between date and
	// updated_at doesn't perturb the trailing comma.
	var setClauses []string
	var args []any
	if patch.Date != nil {
		// patch.Date is the canonical YYYY-MM-DD form returned by
		// validateDate. Re-parse to a time.Time so the driver writes the
		// same TEXT layout (ISO-8601 with timezone) that every other
		// handler produces — passing the bare YYYY-MM-DD string would
		// silently drift the storage format and break lex-ordered date
		// comparisons in the existing list/export queries.
		d, err := time.Parse("2006-01-02", *patch.Date)
		if err != nil {
			return 0, time.Time{}, fmt.Errorf("invalid date in patch: %w", err)
		}
		setClauses = append(setClauses, "date = ?")
		args = append(args, d)
	}
	if patch.Description != nil {
		setClauses = append(setClauses, "description = ?")
		args = append(args, *patch.Description)
	}
	if patch.CategoryID != nil {
		setClauses = append(setClauses, "category_id = ?")
		args = append(args, *patch.CategoryID)
	}
	setClauses = append(setClauses, "updated_at = CURRENT_TIMESTAMP")

	whereClause, whereArgs := buildTransactionWhereClause(r.URL.Query())
	if user.Role != RoleAdmin {
		if whereClause == "" {
			whereClause = " WHERE t.user_id = ?"
		} else {
			whereClause += " AND t.user_id = ?"
		}
		whereArgs = append(whereArgs, user.ID)
	}
	liveClause := appendLiveTransactionsFilter(whereClause)

	// SQLite does not allow UPDATE with a JOIN directly; the subquery form
	// matches handleDeleteTransactionsByFilter exactly. The categories JOIN
	// is needed because buildTransactionWhereClause emits c.type predicates
	// for the ?type=expense|income filter.
	query := fmt.Sprintf(
		`UPDATE transactions SET %s WHERE deleted_at IS NULL AND id IN (
			SELECT t.id FROM transactions t JOIN categories c ON t.category_id = c.id %s
		)`,
		strings.Join(setClauses, ", "),
		liveClause,
	)

	res, err := tx.ExecContext(r.Context(), query, append(args, whereArgs...)...)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("exec update: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("read update result: %w", err)
	}
	return n, time.Time{}, nil
}

// runUpdateByFilterTags is the tags read-then-write branch of update-by-filter.
// A single SQL UPDATE cannot express "for each row, compute new tags as old
// ∪ new" (or set-difference / replace), so this enumerates matching live rows,
// computes the merged tag string per row via applyTagsMode, and writes each
// row's update inside the caller's tx. Rollback covers the whole batch.
//
// minDate is the earliest of (each row's pre-edit date, patch.Date) across
// every enumerated row. The caller passes that floor into
// verifyAffectedCheckpoints when patch.Date is set, so the post-commit hook
// can scope its work precisely instead of conservatively reverifying every
// checkpoint (the no-tags path's behavior).
//
// SQL scaffolding mirrors runUpdateByFilterNoTags exactly: same
// buildTransactionWhereClause + non-admin user_id append +
// appendLiveTransactionsFilter + JOIN categories. The inner statement is a
// SELECT instead of an UPDATE; per-row UPDATEs follow.
//
// Path selection at the handler level (`if patch.Tags != nil`) makes this
// MUTUALLY EXCLUSIVE with runUpdateByFilterNoTags — never both per request.
// The audit summary fires exactly once per request as a result, and the
// checkpoint hook fires at most once per request.
func (h *Handler) runUpdateByFilterTags(r *http.Request, tx *sql.Tx, user database.User, patch database.UpdatePatch) (int64, time.Time, error) {
	whereClause, whereArgs := buildTransactionWhereClause(r.URL.Query())
	if user.Role != RoleAdmin {
		if whereClause == "" {
			whereClause = " WHERE t.user_id = ?"
		} else {
			whereClause += " AND t.user_id = ?"
		}
		whereArgs = append(whereArgs, user.ID)
	}
	liveClause := appendLiveTransactionsFilter(whereClause)

	// SELECT t.id, t.tags, t.date for the read leg. The categories JOIN is
	// required because buildTransactionWhereClause emits c.type predicates
	// for the ?type=expense|income filter — skipping the JOIN would surface
	// as `no such column: c.type` at runtime.
	selectQuery := fmt.Sprintf(
		`SELECT t.id, t.tags, t.date FROM transactions t JOIN categories c ON t.category_id = c.id %s`,
		liveClause,
	)

	rows, err := tx.QueryContext(r.Context(), selectQuery, whereArgs...)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("select matching: %w", err)
	}

	type matchedRow struct {
		id   int64
		tags sql.NullString
		date time.Time
	}
	var matched []matchedRow
	for rows.Next() {
		var rr matchedRow
		if err := rows.Scan(&rr.id, &rr.tags, &rr.date); err != nil {
			rows.Close()
			return 0, time.Time{}, fmt.Errorf("scan: %w", err)
		}
		matched = append(matched, rr)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, time.Time{}, fmt.Errorf("rows.Err: %w", err)
	}
	// Explicit close so the next ExecContext doesn't fight for the same
	// connection — SQLite serializes per-conn and an unfinished SELECT can
	// stall a write under WAL.
	rows.Close()

	// Pre-parse patch.Date once so the per-row UPDATE doesn't re-parse on
	// every iteration. validateDate already ran in buildUpdatePatch, but
	// the canonical YYYY-MM-DD must be re-typed to time.Time so the driver
	// writes the same TEXT layout every other handler produces.
	var afterDate time.Time
	if patch.Date != nil {
		d, err := time.Parse("2006-01-02", *patch.Date)
		if err != nil {
			return 0, time.Time{}, fmt.Errorf("invalid date in patch: %w", err)
		}
		afterDate = d
	}

	var minDate time.Time
	for _, m := range matched {
		existing := ""
		if m.tags.Valid {
			existing = m.tags.String
		}
		// patch.TagsMode is guaranteed non-nil here: buildUpdatePatch
		// rejects patch.Tags != nil with TagsMode == nil at the wire-decode
		// boundary, so by the time we reach this point both pointers are
		// set together.
		merged := applyTagsMode(existing, *patch.Tags, *patch.TagsMode)

		// Build the per-row UPDATE — also touch date / description /
		// category_id when they're in the patch (combined patches are
		// legal even though tags-only patches are the common case).
		var setClauses []string
		var setArgs []any
		if patch.Date != nil {
			setClauses = append(setClauses, "date = ?")
			setArgs = append(setArgs, afterDate)
		}
		if patch.Description != nil {
			setClauses = append(setClauses, "description = ?")
			setArgs = append(setArgs, *patch.Description)
		}
		if patch.CategoryID != nil {
			setClauses = append(setClauses, "category_id = ?")
			setArgs = append(setArgs, *patch.CategoryID)
		}
		setClauses = append(setClauses, "tags = ?", "updated_at = CURRENT_TIMESTAMP")
		setArgs = append(setArgs, merged, m.id)

		// Defense-in-depth deleted_at IS NULL: the inner SELECT already
		// scoped to live rows via appendLiveTransactionsFilter, but
		// re-asserting on the per-row UPDATE makes the soft-delete
		// invariant explicit at the SQL level — a row tombstoned in the
		// microseconds between SELECT and UPDATE (impossible inside a
		// single tx, but cheap to gate against) cannot be silently
		// resurrected by this path.
		upd := fmt.Sprintf(
			"UPDATE transactions SET %s WHERE id = ? AND deleted_at IS NULL",
			strings.Join(setClauses, ", "),
		)
		if _, err := tx.ExecContext(r.Context(), upd, setArgs...); err != nil {
			return 0, time.Time{}, fmt.Errorf("update id=%d: %w", m.id, err)
		}

		if patch.Date != nil {
			// Fold three values into one floor: previous minDate, this
			// row's pre-edit date, and the patched date.
			minDate = earliestDate(minDate, earliestDate(m.date, afterDate))
		}
	}

	return int64(len(matched)), minDate, nil
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
