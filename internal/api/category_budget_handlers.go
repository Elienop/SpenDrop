package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/database"
)

// categoryBudgetSetRequest is the JSON input for upserting a per-category
// monthly limit. Amount is in dollars; the handler converts to cents.
type categoryBudgetSetRequest struct {
	Amount float64 `json:"amount"`
}

// categoryBudgetDTO is the wire shape returned by GET /api/category-budgets.
// Amount is in DOLLARS (centsToDollars), never the raw amount_cents column —
// matching every other money-bearing endpoint.
type categoryBudgetDTO struct {
	CategoryID int64   `json:"category_id"`
	Amount     float64 `json:"amount"`
}

// handleGetCategoryBudgets returns the per-category limits set for a given
// (year, month). Any authenticated user may read. Only categories with a limit
// set appear in the response.
//
//	GET /api/category-budgets?year=&month=  ->  200 [{category_id, amount}]
func (h *Handler) handleGetCategoryBudgets(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.GetUser(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	year, ok := parseYearQuery(w, r)
	if !ok {
		return
	}
	month, ok := parseMonthQuery(w, r)
	if !ok {
		return
	}

	rows, err := h.queries.ListCategoryBudgetsByMonth(r.Context(), database.ListCategoryBudgetsByMonthParams{
		Year:  year,
		Month: month,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list category budgets")
		return
	}

	out := make([]categoryBudgetDTO, 0, len(rows))
	for _, row := range rows {
		out = append(out, categoryBudgetDTO{
			CategoryID: row.CategoryID,
			Amount:     centsToDollars(row.AmountCents),
		})
	}

	writeJSON(w, http.StatusOK, out)
}

// handleSetCategoryBudget upserts a per-category monthly limit. Admin only.
// The category must exist AND be type='expense'. Amount must be > 0 and within
// MaxTransactionAmount.
//
//	PUT /api/category-budgets/{year}/{month}/{categoryId}  body {amount}
func (h *Handler) handleSetCategoryBudget(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if user.Role != RoleAdmin {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	year, month, categoryID, ok := parseCategoryBudgetPath(w, r)
	if !ok {
		return
	}

	var req categoryBudgetSetRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Amount <= 0 {
		writeError(w, http.StatusBadRequest, "amount must be positive")
		return
	}
	if req.Amount > MaxTransactionAmount {
		writeError(w, http.StatusBadRequest, "amount exceeds maximum allowed value")
		return
	}

	// The category must exist (404) and be an expense category (400). The
	// ON DELETE CASCADE FK is the durable invariant for existence; this lookup
	// surfaces a clear status before the upsert and enforces the expense-only
	// rule the FK cannot express.
	cat, err := h.queries.GetCategoryByID(r.Context(), categoryID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "category not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to look up category")
		return
	}
	if cat.Type != CategoryTypeExpense {
		writeError(w, http.StatusBadRequest, "category budgets may only be set on expense categories")
		return
	}

	// safeDollarsToCents, not dollarsToCents: the two bounds above are a pair
	// of comparisons and NaN is false against both, so a non-finite amount
	// would pass validation and int64(NaN*100) stores int64 minimum. Defence in
	// depth — no JSON body can produce one today — kept at the conversion so it
	// survives a future decoder or validator change.
	amountCents, ok := safeDollarsToCents(req.Amount)
	if !ok {
		writeError(w, http.StatusBadRequest, "amount is not a representable money value")
		return
	}

	err = h.queries.UpsertCategoryBudget(r.Context(), database.UpsertCategoryBudgetParams{
		Year:        year,
		Month:       month,
		CategoryID:  categoryID,
		AmountCents: amountCents,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to set category budget")
		return
	}

	// Live-updates: a per-category limit re-colors that category's budget cell
	// and the reports budget surface. Post-commit, best-effort, nil-safe.
	h.publishInvalidate("budgets", "reports")

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// handleDeleteCategoryBudget clears a per-category limit. Admin only and
// idempotent: deleting a limit that was never set still returns 200 (the
// post-condition "no limit for this category/month" holds either way).
//
//	DELETE /api/category-budgets/{year}/{month}/{categoryId}
func (h *Handler) handleDeleteCategoryBudget(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if user.Role != RoleAdmin {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	year, month, categoryID, ok := parseCategoryBudgetPath(w, r)
	if !ok {
		return
	}

	err := h.queries.DeleteCategoryBudget(r.Context(), database.DeleteCategoryBudgetParams{
		Year:       year,
		Month:      month,
		CategoryID: categoryID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete category budget")
		return
	}

	// Live-updates: clearing a per-category limit changes that category's
	// budget cell and the reports budget surface. The op is idempotent, so we
	// always signal (a no-op-on-the-server delete still re-syncs viewers
	// cheaply under the client-side debounce). Post-commit, best-effort,
	// nil-safe.
	h.publishInvalidate("budgets", "reports")

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// parseYearQuery reads and validates the ?year= query param. Required.
func parseYearQuery(w http.ResponseWriter, r *http.Request) (int64, bool) {
	yearStr := r.URL.Query().Get("year")
	if yearStr == "" {
		writeError(w, http.StatusBadRequest, "year is required")
		return 0, false
	}
	year, err := strconv.ParseInt(yearStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid year")
		return 0, false
	}
	if year < PlanningMinYear || year > PlanningMaxYear {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("year must be between %d and %d", PlanningMinYear, PlanningMaxYear))
		return 0, false
	}
	return year, true
}

// parseMonthQuery reads and validates the ?month= query param. Required.
func parseMonthQuery(w http.ResponseWriter, r *http.Request) (int64, bool) {
	monthStr := r.URL.Query().Get("month")
	if monthStr == "" {
		writeError(w, http.StatusBadRequest, "month is required")
		return 0, false
	}
	month, err := strconv.ParseInt(monthStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid month")
		return 0, false
	}
	if month < 1 || month > 12 {
		writeError(w, http.StatusBadRequest, "month must be between 1 and 12")
		return 0, false
	}
	return month, true
}

// parseCategoryBudgetPath reads {year}/{month}/{categoryId} chi URL params and
// validates each. On any failure it writes a 400 and returns ok=false.
func parseCategoryBudgetPath(w http.ResponseWriter, r *http.Request) (year, month, categoryID int64, ok bool) {
	year, err := strconv.ParseInt(chi.URLParam(r, "year"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid year")
		return 0, 0, 0, false
	}
	if year < PlanningMinYear || year > PlanningMaxYear {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("year must be between %d and %d", PlanningMinYear, PlanningMaxYear))
		return 0, 0, 0, false
	}

	month, err = strconv.ParseInt(chi.URLParam(r, "month"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid month")
		return 0, 0, 0, false
	}
	if month < 1 || month > 12 {
		writeError(w, http.StatusBadRequest, "month must be between 1 and 12")
		return 0, 0, 0, false
	}

	categoryID, err = strconv.ParseInt(chi.URLParam(r, "categoryId"), 10, 64)
	if err != nil || categoryID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid category id")
		return 0, 0, 0, false
	}

	return year, month, categoryID, true
}
