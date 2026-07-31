package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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
	// Tags is a pointer for exactly the same reason as Notes below: PUT is a
	// full replace, so a plain string made an absent key indistinguishable
	// from "" and silently wiped the stored tags. The web inline editor always
	// sends tags, but the API-token surface is exposed and its clients build
	// payloads field-by-field.
	//
	// nil           -> leave the stored tags unchanged (update) / no tags (create)
	// pointer to "" -> explicitly clear the tags
	Tags *string `json:"tags"`
	// Notes is a pointer so an ABSENT key is distinguishable from an empty
	// one. PUT /transactions/{id} is a full replace, and clients that build
	// their payload field-by-field (the inline row editor, the API-token
	// surface) legitimately omit notes because they have no notes editor. A
	// plain string decoded "" for those callers and silently NULLed a note
	// the user had imported — invisible loss, recoverable only from
	// transaction_audit.before_json.
	//
	// nil          -> leave the stored note unchanged (update) / no note (create)
	// pointer to "" -> explicitly clear the note
	Notes *string `json:"notes"`
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
		// No foreign currency — use amount directly.
		// `<= 0` alone lets 1e308 and NaN through (NaN compares false against
		// everything), and both convert to int64 minimum downstream — a
		// hugely NEGATIVE stored amount from a positive-looking request.
		if err := validateMoneyAmount(req.Amount, "amount"); err != nil {
			return 0, origAmt, origCur, err
		}
		return req.Amount, origAmt, origCur, nil
	}

	currency, err := q.GetCurrency(ctx, req.OriginalCurrency)
	if err != nil {
		return 0, origAmt, origCur, fmt.Errorf("unknown currency %q", req.OriginalCurrency)
	}

	if currency.IsBase {
		// It's the base currency — use amount directly, no conversion needed
		if err := validateMoneyAmount(req.Amount, "amount"); err != nil {
			return 0, origAmt, origCur, err
		}
		return req.Amount, origAmt, origCur, nil
	}

	// Foreign currency: must have original_amount
	if req.OriginalAmount == nil || *req.OriginalAmount <= 0 {
		return 0, origAmt, origCur, fmt.Errorf("original_amount is required for non-base currency")
	}
	if err := validateMoneyAmount(*req.OriginalAmount, "original_amount"); err != nil {
		return 0, origAmt, origCur, err
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

	// The division can carry an in-range original_amount out of range: a small
	// rate_to_base multiplies it up. Bound the converted value too, or
	// dollarsToCents launders it into int64 minimum at the storage edge.
	if err := validateMoneyAmount(converted, "converted amount"); err != nil {
		return 0, origAmt, origCur, err
	}

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

	if err := validateTransactionRequest(req, noStoredDate); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	date, _ := time.Parse("2006-01-02", req.Date) // already validated

	amount, origAmt, origCur, err := resolveCurrency(r.Context(), h.queries, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	amountCents := dollarsToCents(amount)
	params := database.CreateTransactionParams{
		UserID:              user.ID,
		Date:                date,
		AmountCents:         amountCents,
		OriginalAmountCents: nullableDollarsToCents(origAmt),
		OriginalCurrency:    origCur,
		Description:         req.Description,
		CategoryID:          req.CategoryID,
		Tags:                nullStringFromPtr(req.Tags),
		Notes:               nullStringFromPtr(req.Notes),
		// Manual entries used to store NULL here, so import dedupe could not
		// see them and re-imported them as duplicates.
		ContentHash: h.contentHashForManualEntry(
			r.Context(), h.queries, date, amountCents, req.Description, req.CategoryID),
	}
	txn, err := h.txnStore.Create(r.Context(), user.ID, params)
	// The pre-check inside contentHashForManualEntry cannot be atomic: it runs
	// on h.queries and hands its connection back to the pool before Create
	// opens its own transaction, so SetMaxOpenConns(1) does not fuse probe and
	// insert into one unit. Two concurrent identical creates therefore both
	// see "hash free", and the loser hits idx_transactions_content_hash. That
	// is the same legitimate-duplicate situation the pre-check handles, so
	// recover exactly as it would have: drop the hash and retry once, leaving
	// the winner as the canonical anchor (earliest-id-wins). The check stays
	// as the cheap common path; this is the correctness guarantee.
	//
	// IsContentHashUniqueViolation is scoped to that one index by design —
	// a users.username or categories.name violation must still surface.
	if err != nil && database.IsContentHashUniqueViolation(err) {
		params.ContentHash = sql.NullString{}
		txn, err = h.txnStore.Create(r.Context(), user.ID, params)
	}
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
	h.notifyTxnAdded(r.Context(), txn, user.DisplayName, user.ID)

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
	// The check stays HERE rather than moving into the store because this
	// handler must return a hard 403 (not a skip) and already reads the row
	// to do so; the store's own tx-scoped read then catches the one failure
	// mode (row vanished) that actually matters.
	//
	// The batch path resolves this differently and deliberately: UpdateTx
	// takes a database.Actor, because it is already loading `before` inside
	// the tx and a handler-side pre-check would mean a second read for each
	// of up to MaxBatchUpdateIDs rows. Actor carries authority, not HTTP
	// semantics — the api package maps user.Role to it at the call site — so
	// the coupling that argument was written to avoid does not apply.
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

	// PUT is a full replace, so the client resends the stored date on every
	// edit — including on rows dated outside the data window, which existed
	// long before validateDate had a bound. Passing the stored date here lets
	// such a row be described, recategorised, retagged or CORRECTED into range,
	// while still refusing to move it to any other out-of-window date. See
	// validateDateAllowingStored. `existing` was already loaded above for the
	// tombstone / ownership checks, so this costs no extra read.
	if err := validateTransactionRequest(req, existing.Date.Format("2006-01-02")); err != nil {
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
		// PUT is a full replace, so an absent tags or notes key must carry the
		// stored value forward rather than clearing it. `existing` was already
		// loaded above for the tombstone/ownership checks.
		Tags:  nullStringForUpdate(req.Tags, existing.Tags),
		Notes: nullStringForUpdate(req.Notes, existing.Notes),
		ID:    id,
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
		}, user.DisplayName, user.ID)
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
	}, user.DisplayName, user.ID)

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
		if err := validateTransactionRequest(req, noStoredDate); err != nil {
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
			Tags:                nullStringFromPtr(req.Tags),
			Notes:               nullStringFromPtr(req.Notes),
			// Same dedupe identity the single-create path assigns. Omitting it
			// here stored NULL, so every row added through batch create — which
			// is what the mobile quick-add and the offline queue drain into —
			// was invisible to import dedupe, and re-importing a spreadsheet
			// containing those entries silently duplicated them.
			//
			// qtx, not h.queries, and that is load-bearing twice over. It is the
			// transaction-scoped handle, so the uniqueness probe inside sees rows
			// created earlier in THIS batch: two identical items in one request
			// anchor the first and store the second with NULL, rather than
			// colliding on the partial UNIQUE index and rolling back everything
			// the user entered. And because the pool is SetMaxOpenConns(1), a read
			// through the outer handle while this transaction holds the single
			// connection DEADLOCKS — swapping qtx for h.queries hangs the request
			// until the client gives up.
			ContentHash: h.contentHashForManualEntry(
				r.Context(), qtx, date, dollarsToCents(amount), req.Description, req.CategoryID,
			),
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
		h.notifyTxnBatch(r.Context(), "added", n, user.DisplayName, user.ID)
	}

	// Live-updates broadcast (post-commit, best-effort, nil-safe): ONE signal
	// for the whole batch, mirroring the aggregate notify. New rows change the
	// list, dashboard totals, reports, and budget cells.
	if len(results) > 0 {
		h.publishInvalidate("transactions", "dashboard", "reports", "budgets")
	}

	writeJSON(w, http.StatusCreated, results)
}

// noStoredDate is the storedDate argument for any path that is not replacing
// an existing row: a create, or a bulk patch where the date is by definition a
// new value the user just typed. Nothing is grandfathered, so the window is
// enforced strictly.
const noStoredDate = ""

// validateDate parses YYYY-MM-DD, bounds the year to the data window and
// returns the canonical form. Mirrors the date check in
// validateTransactionRequest but isolated so bulk-edit can call it without
// forcing all-fields validation.
//
// The year bound was absent for the app's whole life — 0001-01-01 and
// 3021-01-02 both parsed and stored — while every report endpoint rejected
// years outside its window, so the ledger could hold rows no report would
// ever display.
func validateDate(s string) (string, error) {
	return validateDateAllowingStored(s, noStoredDate)
}

// validateDateAllowingStored is validateDate with one exception: `storedDate`
// (the canonical YYYY-MM-DD already on the row being replaced) is accepted
// even when it falls outside the data window.
//
// Why the exception exists. The bound is new; the data is not. Any deployment
// may already hold rows outside the window, and PUT /api/transactions/{id} is
// a FULL REPLACE — the client resends the row's own date on every edit. A
// strict bound would therefore 400 a description-only edit of a legacy 3021
// row, locking the user out of correcting, recategorising, or even retagging
// their own data. That trap is a worse failure than the hole it closes.
//
// The rule is "the bound applies to the date you are MOVING to":
//
//   - a row already outside the window may stay exactly where it is;
//   - it may always be corrected INTO the window (the escape hatch);
//   - it may not be moved to a DIFFERENT out-of-window date, so the hole
//     cannot be kept alive through the update path;
//   - nothing may be created outside the window at all.
//
// Exact-match, not year-match: "this row is legacy so anything goes" would let
// a 3021 row wander freely above MaxDataYear forever.
func validateDateAllowingStored(s, storedDate string) (string, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return "", fmt.Errorf("date must be in YYYY-MM-DD format")
	}
	canonical := t.Format("2006-01-02")
	if y := t.Year(); y < MinDataYear || y > MaxDataYear {
		if storedDate != "" && canonical == storedDate {
			return canonical, nil
		}
		if storedDate != "" {
			return "", fmt.Errorf(
				"date must be between %d-01-01 and %d-12-31 (this row is dated %s, from before that limit existed — "+
					"keep it unchanged or correct it into range)",
				MinDataYear, MaxDataYear, storedDate)
		}
		return "", fmt.Errorf("date must be between %d-01-01 and %d-12-31", MinDataYear, MaxDataYear)
	}
	return canonical, nil
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
//
// storedDate is the canonical YYYY-MM-DD already on the row this request
// replaces, or noStoredDate on a create. It is a required argument rather than
// an optional variant so every new caller has to decide, in review, whether it
// is replacing a row — see validateDateAllowingStored for what it licenses.
func validateTransactionRequest(req transactionRequest, storedDate string) error {
	if req.Date == "" {
		return fmt.Errorf("date is required")
	}
	if _, err := validateDateAllowingStored(req.Date, storedDate); err != nil {
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
	if _, err := validateTagsField(optionalStringValue(req.Tags)); err != nil {
		return err
	}
	if req.Notes != nil && len(*req.Notes) > MaxNotesLength {
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
	//
	// content_hash = NULL for the same reason UpdateTransaction clears it:
	// description is a dedupe-identity input, so every renamed row would
	// otherwise keep claiming the identity of the description it no longer
	// has — and a later import of that original content would be rejected as
	// a duplicate of a row that no longer matches it. The startup backfill
	// re-anchors the renamed rows to their current content on the next boot.
	if user.Role == RoleAdmin {
		result, err = tx.ExecContext(r.Context(),
			`UPDATE transactions SET description = ?, content_hash = NULL, updated_at = CURRENT_TIMESTAMP WHERE description LIKE ? ESCAPE '\' AND deleted_at IS NULL`,
			req.NewDescription, "%"+escaped+"%",
		)
	} else {
		result, err = tx.ExecContext(r.Context(),
			`UPDATE transactions SET description = ?, content_hash = NULL, updated_at = CURRENT_TIMESTAMP WHERE description LIKE ? ESCAPE '\' AND user_id = ? AND deleted_at IS NULL`,
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

		// Ownership is checked HERE rather than in the store because this
		// loop already loads the row for its own tombstone check above.
		// batch-update does the reverse — it passes a database.Actor into
		// UpdateTx, which is already loading `before` inside the tx — so
		// neither path pays for a redundant read. Same rule, same admin
		// bypass, two placements for the same reason.
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
		h.notifyTxnBatch(r.Context(), "deleted", deleted, user.DisplayName, user.ID)
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
// transaction IDs in one tx. Tombstoned / missing rows are skipped for every
// role; non-owned rows are skipped for members only, since admins bypass
// ownership (consistent with handleBatchDeleteTransactions — see the
// database.Actor construction at the call site below). Per-row audit
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

		// Admins may patch any household row (design spec §3.9) — the same
		// bypass handleUpdateTransaction, handleBatchDeleteTransactions and
		// update-by-filter already grant. Without it an admin bulk-editing a
		// mixed-owner selection out of the household-wide list silently
		// skipped every row they had not personally created.
		actor := database.Actor{UserID: user.ID, IsAdmin: user.Role == RoleAdmin}
		before, after, err := h.txnStore.UpdateTx(r.Context(), tx, actor, id, rowPatch)
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
			// Accumulate the date floor for EVERY updated row, not just
			// date patches. balance_checkpoints.scope_type is total |
			// category | tag, and VerifyCheckpointTotal filters on
			// t.category_id / t.tags for the latter two — so a category-
			// or tags-only patch moves a scoped checkpoint's real total
			// exactly like a date patch moves a total-scoped one. The
			// affected checkpoints are those on or after the earliest date
			// this batch touched, which is a tighter (and cheaper) floor
			// than the zero-time "reverify everything" fallback.
			// earliestDate is 2-arg; nest the call to fold three values
			// into one floor.
			minDate = earliestDate(minDate, earliestDate(before.Date, after.Date))
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
		// scope= mirrors batch-delete and bulk-rename: now that admins bypass
		// ownership, a skip count alone no longer tells an operator whether a
		// member hit the ownership wall or an admin hit tombstones.
		scope := "own"
		if user.Role == RoleAdmin {
			scope = "all"
		}
		if err := h.txnStore.RecordBulkTx(r.Context(), tx, user.ID, database.AuditUpdate,
			database.BulkAuditSummary{
				Count:  skipped,
				Filter: fmt.Sprintf("skipped_during_batch_update:%d_of_%d scope=%s", skipped, len(req.IDs), scope),
			}); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to record audit")
			return
		}
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit batch update")
		return
	}

	// Phase 3.3 checkpoint hook: fires when at least one row was actually
	// updated (minDate stays zero otherwise) AND the patch touched a field
	// that can move a checkpoint's computed sum. Date moves rows across a
	// total-scoped checkpoint's cutoff; CategoryID and Tags move them in and
	// out of category-/tag-scoped checkpoints (see VerifyCheckpointTotal).
	// A description-only patch cannot change any scope's sum, so it is
	// deliberately excluded.
	if !minDate.IsZero() && (patch.Date != nil || patch.CategoryID != nil || patch.Tags != nil) {
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

// filterUpdateCells derives the union of OLD and NEW over-budget cells a
// filter-update touches. oldCells are the (category, month) cells of the rows
// matched BEFORE the update; the patch relocates every matched row uniformly,
// so each row's NEW cell swaps category_id when patch.CategoryID is set and/or
// swaps year/month when patch.Date is set. Returns OLD ∪ NEW deduped so a row
// moved OUT of an over category clears its old latch and a row moved INTO a
// category re-evaluates the new one. Date is parsed UTC to match cellForDate.
func filterUpdateCells(oldCells []budgetCell, patch database.UpdatePatch) []budgetCell {
	set := make(map[budgetCell]struct{}, len(oldCells)*2)
	for _, c := range oldCells {
		set[c] = struct{}{}
	}
	if patch.CategoryID != nil || patch.Date != nil {
		for _, c := range oldCells {
			nc := c
			if patch.CategoryID != nil {
				nc.CategoryID = *patch.CategoryID
			}
			if patch.Date != nil {
				if d, err := time.Parse("2006-01-02", *patch.Date); err == nil {
					du := d.UTC()
					nc.Year = du.Year()
					nc.Month = int(du.Month())
				}
			}
			set[nc] = struct{}{}
		}
	}
	cells := make([]budgetCell, 0, len(set))
	for c := range set {
		cells = append(cells, c)
	}
	return cells
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

	// FilterLatchFix: enumerate the (category, month) cells the about-to-be-
	// tombstoned rows occupy BEFORE the soft-delete UPDATE — afterwards
	// liveClause filters them out. Post-commit we re-evaluate each so a latch
	// left set by a now-deleted over-budget row re-arms. Best-effort: an
	// enumeration error must not block the delete, so we log and proceed with an
	// empty cell set (mirrors the post-commit hook error contract).
	affectedCells, err := h.enumerateFilterCells(r.Context(), liveClause, args)
	if err != nil {
		log.Printf("delete-by-filter: enumerate affected cells: %v", err)
		affectedCells = nil
	}

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

	// FilterLatchFix: the bulk soft-delete drops spend out of every (category,
	// month) the deleted rows occupied, so a latch set by a now-deleted
	// over-budget row must re-arm. affectedCells was enumerated BEFORE the
	// UPDATE; evaluate them post-commit (best-effort — never blocks the
	// response, mirrors verifyAffectedCheckpoints' contract).
	if deleted > 0 && len(affectedCells) > 0 {
		h.evaluateBudgetAlerts(r.Context(), affectedCells)
	}

	// Live-updates broadcast (post-commit, best-effort, nil-safe): tombstoned
	// rows leave the list/dashboard/reports and appear in trash. Budgets are
	// invalidated too now that affected cells are re-evaluated above.
	if deleted > 0 {
		h.publishInvalidate("transactions", "dashboard", "reports", "budgets", "trash")
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

	// FilterLatchFix: enumerate the OLD (category, month) cells the matched live
	// rows occupy BEFORE the update; T18b derives the NEW cells the patch moves
	// them to post-commit. Rebuild liveClause here exactly as the branch helpers
	// do (buildTransactionWhereClause + non-admin user_id append +
	// appendLiveTransactionsFilter) so the enumeration scope matches the rows
	// the UPDATE will touch. Best-effort — an enumeration error must not block
	// the update.
	enumWhere, enumArgs := buildTransactionWhereClause(r.URL.Query())
	if user.Role != RoleAdmin {
		if enumWhere == "" {
			enumWhere = " WHERE t.user_id = ?"
		} else {
			enumWhere += " AND t.user_id = ?"
		}
		enumArgs = append(enumArgs, user.ID)
	}
	oldCells, err := h.enumerateFilterCells(r.Context(), appendLiveTransactionsFilter(enumWhere), enumArgs)
	if err != nil {
		log.Printf("update-by-filter: enumerate affected cells: %v", err)
		oldCells = nil
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
	// scope= completes the set: batch-delete, bulk-rename, delete-by-filter and
	// batch-update all record it. This path needs it MOST — a member's filter
	// is scoped to their own rows in SQL while an admin's runs household-wide,
	// and unlike batch-update there is no skipped count to hint at which ran.
	scope := "own"
	if user.Role == RoleAdmin {
		scope = "all"
	}
	patchSummary := summarizePatch(patch)
	filterDesc := fmt.Sprintf("update-by-filter scope=%s query=%q patch=%s", scope, r.URL.RawQuery, patchSummary)
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

	// Phase 3.3 checkpoint hook (spec §3.10). Fires for any patch that can
	// move a checkpoint's computed sum: Date (total-scoped checkpoints) plus
	// CategoryID and Tags, which move rows in and out of category-/tag-scoped
	// checkpoints via VerifyCheckpointTotal's scope predicates.
	//
	// DO NOT "unify" this with batch-update's !minDate.IsZero() gate. The
	// no-tags fast path is a single SQL UPDATE with no per-row dates to
	// enumerate, so it ALWAYS returns a zero minDate and the verifier treats
	// that as "walk every checkpoint regardless of date" (the same
	// conservative reverify handleDeleteTransactionsByFilter does). Gating
	// this path on a non-zero minDate would therefore silently disable the
	// checkpoint hook for every no-tags bulk filter edit. updated > 0 is the
	// correct liveness signal HERE; minDate is the correct one in
	// batch-update, where every updated row contributes a real date.
	if updated > 0 && (patch.Date != nil || patch.CategoryID != nil || patch.Tags != nil) {
		h.verifyAffectedCheckpoints(r.Context(), minDate)
	}

	// FilterLatchFix: a filter update relocates spend across (category, month)
	// cells — a category a row LEFT may drop back under budget (latch clears) and
	// a category it ENTERED may cross over. Evaluate the union of OLD and NEW
	// cells post-commit, best-effort (never blocks the response).
	if updated > 0 {
		cells := filterUpdateCells(oldCells, patch)
		if len(cells) > 0 {
			h.evaluateBudgetAlerts(r.Context(), cells)
		}
	}

	// Live-updates broadcast (post-commit, best-effort, nil-safe): a filter
	// update can move amount/category/date, so list, dashboard totals, and
	// reports may change. Budgets may change now that affected cells are
	// re-evaluated above. Fire only when rows actually updated.
	if updated > 0 {
		h.publishInvalidate("transactions", "dashboard", "reports", "budgets")
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
	// This branch only runs for tags-free patches, and buildUpdatePatch
	// rejects an empty patch, so at least one dedupe-identity input (date,
	// description, category) always changes here. Clear the stale identity —
	// see the note on the UpdateTransaction query for what keeping it costs.
	setClauses = append(setClauses, "content_hash = NULL")
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
// every enumerated row, accumulated for every row this branch touches. The
// caller passes that floor into verifyAffectedCheckpoints for any patch that
// can move a checkpoint sum (Date, CategoryID or Tags — and this branch always
// carries Tags), so the post-commit hook scopes its work precisely instead of
// conservatively reverifying every checkpoint (the no-tags path's behavior,
// which has no per-row dates to offer).
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

	// The read leg pulls t.id and t.tags (the merge inputs), t.date (the
	// checkpoint floor) AND every ComputeContentHash input, so the loop below
	// can ask database.HashInputsMoved whether the patch actually changes each
	// row's dedupe identity rather than guessing from which keys the patch
	// carries. Measured cost of the three extra columns on a full-ledger match:
	// +1.8 ms at 5k rows and +12 ms at 50k, against a per-row UPDATE loop of
	// 23 ms and 228 ms respectively — inside the noise at household scale and
	// ~5% of the whole statement at ten times that. Cheap enough to buy
	// precision; see the note at the clear decision below for what precision
	// buys.
	//
	// The categories JOIN is required because buildTransactionWhereClause emits
	// c.type predicates for the ?type=expense|income filter — skipping the JOIN
	// would surface as `no such column: c.type` at runtime.
	selectQuery := fmt.Sprintf(
		`SELECT t.id, t.tags, t.date, t.description, t.amount_cents, t.category_id `+
			`FROM transactions t JOIN categories c ON t.category_id = c.id %s`,
		liveClause,
	)

	rows, err := tx.QueryContext(r.Context(), selectQuery, whereArgs...)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("select matching: %w", err)
	}

	type matchedRow struct {
		id     int64
		tags   sql.NullString
		date   time.Time
		before database.HashInputs
	}
	var matched []matchedRow
	for rows.Next() {
		var rr matchedRow
		if err := rows.Scan(&rr.id, &rr.tags, &rr.date,
			&rr.before.Description, &rr.before.AmountCents, &rr.before.CategoryID); err != nil {
			rows.Close()
			return 0, time.Time{}, fmt.Errorf("scan: %w", err)
		}
		rr.before.Date = rr.date
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
		// Clear the dedupe identity only when the patch actually MOVES one of
		// ComputeContentHash's inputs, decided per row by the same
		// database.HashInputsMoved predicate TransactionStore derives
		// UpdateTransactionParams.ClearContentHash from. One predicate, so the
		// single-row PUT and this bulk path cannot drift apart.
		//
		// Both directions are load-bearing. Keeping a stale hash lets a row
		// claim the identity of content it no longer holds, which silently
		// rejects a genuinely new import as a duplicate. Clearing one that did
		// not move de-anchors the row from dedupe until the next boot's
		// BackfillContentHashes re-anchors it — and this branch always carries
		// tags, so a bulk tagging pass can match the entire ledger and would
		// de-anchor all of it. A patch that re-asserts a value a row already
		// holds (a saved filter replayed over its own output, a client that
		// resends every field) is exactly that case, and testing key PRESENCE
		// instead of a value change could not tell the two apart.
		//
		// amount is absent from UpdatePatch, so it can never move here; it is
		// carried in `before` anyway so the predicate stays whole rather than
		// depending on that staying true.
		after := m.before
		if patch.Date != nil {
			after.Date = afterDate
		}
		if patch.Description != nil {
			after.Description = *patch.Description
		}
		if patch.CategoryID != nil {
			after.CategoryID = *patch.CategoryID
		}
		if database.HashInputsMoved(m.before, after) {
			setClauses = append(setClauses, "content_hash = NULL")
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

		// Accumulate the floor for EVERY row this branch touches, not just
		// date patches. This path always runs with patch.Tags != nil, and
		// tags move tag-scoped checkpoints exactly like dates move
		// total-scoped ones — so the caller's hook now fires here even when
		// patch.Date is nil, and it needs a real floor to scope the walk.
		// Gating this on patch.Date left tags-only filter edits reverifying
		// every checkpoint in the table via the zero-time fallback.
		// afterDate == m.date when patch.Date is nil, so folding all three
		// stays correct in both cases.
		minDate = earliestDate(minDate, earliestDate(m.date, afterDate))
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

// validateMoneyAmount rejects the float values that cannot survive conversion
// to int64 cents: NaN, +/-Inf, and anything beyond MaxTransactionAmount. A
// bare `<= 0` check is NOT sufficient — NaN compares false against every
// operator, and 1e308 is comfortably positive right up until int64 conversion
// turns it into -9223372036854775808.
func validateMoneyAmount(d float64, field string) error {
	if math.IsNaN(d) || math.IsInf(d, 0) {
		return fmt.Errorf("%s must be a finite number", field)
	}
	if d <= 0 {
		return fmt.Errorf("%s must be positive", field)
	}
	if d > MaxTransactionAmount {
		return fmt.Errorf("%s exceeds the maximum allowed value", field)
	}
	return nil
}

// contentHashForManualEntry computes the dedupe identity for a hand-entered
// transaction, or returns NULL when that identity is already taken.
//
// This probe is advisory, NOT a guarantee: it runs on a pooled connection that
// is released before the caller's INSERT opens its own transaction, so a
// concurrent create can claim the identity in between. handleCreateTransaction
// therefore catches IsContentHashUniqueViolation and retries with a NULL hash;
// any new caller must do the same.
//
// Manual rows previously stored content_hash = NULL unconditionally, so import
// dedupe could not see them: importing a spreadsheet containing a transaction
// the user had already typed in created a second copy.
//
// Collisions must NOT be an error here. Real household data contains
// legitimate duplicates — two identical coffees on the same day, a recurring
// subscription entered twice on purpose — and the partial UNIQUE index
// idx_transactions_content_hash would reject the second one. This follows the
// same earliest-id-wins rule the migration-008 backfill uses: the first row to
// claim a hash keeps it as the canonical anchor, later identical rows are
// stored with NULL and remain fully intact in the ledger. The consequence,
// deliberately accepted: a later import of that content dedupes against the
// canonical row only.
func (h *Handler) contentHashForManualEntry(ctx context.Context, q *database.Queries, date time.Time, amountCents int64, description string, categoryID int64) sql.NullString {
	cat, err := q.GetCategoryByID(ctx, categoryID)
	if err != nil {
		// Category already validated upstream; if it vanished underneath us,
		// fall back to the previous behaviour rather than failing the write.
		return sql.NullString{}
	}
	hash := database.ComputeContentHash(date, amountCents, description, cat.Name)
	if _, err := q.GetTransactionByContentHash(ctx, sql.NullString{String: hash, Valid: true}); err == nil {
		// Identity already anchored by an earlier row.
		return sql.NullString{}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return sql.NullString{}
	}
	return sql.NullString{String: hash, Valid: true}
}

// nullStringFromPtr converts an optional wire field to its stored form on a
// CREATE path. A nil pointer (key absent from the JSON body) means "no value";
// update paths must not call this and should keep the existing value instead.
// Shared by notes and tags — both are optional free-text columns whose absent
// and empty cases must stay distinguishable.
func nullStringFromPtr(p *string) sql.NullString {
	if p == nil {
		return sql.NullString{}
	}
	return toNullString(*p)
}

// nullStringForUpdate resolves an optional column for a full-replace update:
// an absent key (nil) keeps whatever is already stored, an explicit value
// (including "") overwrites it.
func nullStringForUpdate(p *string, existing sql.NullString) sql.NullString {
	if p == nil {
		return existing
	}
	return toNullString(*p)
}

// optionalStringValue reads an optional wire field for validation, treating an
// absent key as the empty string. Only for validators — never for deciding
// what to store, where absent and "" must stay distinguishable.
func optionalStringValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
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
