package api

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/database"
)

// budgetSetRequest is the JSON input for upserting a monthly budget.
type budgetSetRequest struct {
	Amount float64 `json:"amount"`
}

// defaultBudgetRequest is the JSON input for updating the default budget setting.
type defaultBudgetRequest struct {
	Amount float64 `json:"amount"`
}

// handleGetBudgets returns budgets for a given year (defaults to current year).
func (h *Handler) handleGetBudgets(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.GetUser(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	yearStr := r.URL.Query().Get("year")
	var year int64
	if yearStr == "" {
		year = int64(h.clock.Now().Year())
	} else {
		parsed, err := strconv.ParseInt(yearStr, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid year")
			return
		}
		if parsed < PlanningMinYear || parsed > PlanningMaxYear {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("year must be between %d and %d", PlanningMinYear, PlanningMaxYear))
			return
		}
		year = parsed
	}

	budgets, err := h.queries.ListBudgetsByYear(r.Context(), year)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list budgets")
		return
	}

	// Emit a DTO with `amount` in DOLLARS. The raw database.Budget row carries
	// `amount_cents`, which the frontend does not understand (it expects
	// `amount` in dollars like every other money endpoint). Returning the row
	// directly leaked amount_cents under the wrong key.
	out := make([]budgetDTO, 0, len(budgets))
	for _, b := range budgets {
		out = append(out, budgetDTO{
			ID:        b.ID,
			Year:      b.Year,
			Month:     b.Month,
			Amount:    centsToDollars(b.AmountCents),
			UpdatedAt: b.UpdatedAt,
		})
	}

	writeJSON(w, http.StatusOK, out)
}

// budgetDTO is the wire shape for GET /api/budgets: `amount` in dollars, never
// the raw amount_cents column.
type budgetDTO struct {
	ID        int64     `json:"id"`
	Year      int64     `json:"year"`
	Month     int64     `json:"month"`
	Amount    float64   `json:"amount"`
	UpdatedAt time.Time `json:"updated_at"`
}

// handleSetBudget upserts a monthly budget for a given year and month. Admin only.
func (h *Handler) handleSetBudget(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if user.Role != RoleAdmin {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	yearStr := chi.URLParam(r, "year")
	monthStr := chi.URLParam(r, "month")

	year, err := strconv.ParseInt(yearStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid year")
		return
	}
	if year < PlanningMinYear || year > PlanningMaxYear {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("year must be between %d and %d", PlanningMinYear, PlanningMaxYear))
		return
	}

	month, err := strconv.ParseInt(monthStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid month")
		return
	}
	if month < 1 || month > 12 {
		writeError(w, http.StatusBadRequest, "month must be between 1 and 12")
		return
	}

	var req budgetSetRequest
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

	// Phase 3.1b: the legacy REAL amount column was dropped in migration 010;
	// only amount_cents is written. The cents value is derived once from the
	// client-supplied float at the wire edge.
	err = h.queries.UpsertBudget(r.Context(), database.UpsertBudgetParams{
		Year:        year,
		Month:       month,
		AmountCents: amountCents,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to set budget")
		return
	}

	// Live-updates: a changed monthly budget re-colors budget cells and the
	// reports budget surface. Post-commit, best-effort, nil-safe.
	h.publishInvalidate("budgets", "reports")

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// defaultBudgetCents parses the default_budget app_settings value into cents.
// It reports false when the stored string is not a budget this application can
// represent, and every caller then falls back to the documented default of 0.
//
// app_settings.value is free text. handleDefaultBudget's PUT is its only
// writer and enforces 0 < amount <= MaxTransactionAmount, but the read side
// cannot assume the column only ever passed through that gate: a hand-edited
// database, a restored backup or an older build can all leave something else
// there. strconv.ParseFloat accepts "NaN", "Inf" and "-Inf", none of which the
// JSON decoder on the write path can produce, and int64(NaN*100) is undefined
// in Go — on amd64 it lands on int64 minimum, so the budget surfaced as about
// -9.2e16 dollars and every figure derived from it followed.
//
// Negative values are refused for the same reason the PUT refuses them: a
// negative budget is not a state this application defines, and letting one
// through here would make the read side accept what the write side rejects.
func defaultBudgetCents(value string) (int64, bool) {
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, false
	}
	cents, ok := safeDollarsToCents(parsed)
	if !ok || cents < 0 {
		// Logged, not silent: this is a stored value the operator has to fix,
		// and the symptom (every budget reading zero) gives them nothing to go
		// on by itself.
		log.Printf("default_budget: refusing unrepresentable setting value %q; using 0", value)
		return 0, false
	}
	return cents, true
}

// handleDefaultBudget handles both GET and PUT for the default budget setting.
// GET returns the current value; PUT updates it (admin only).
func (h *Handler) handleDefaultBudget(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if r.Method == http.MethodPut && user.Role != RoleAdmin {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	switch r.Method {
	case http.MethodGet:
		setting, err := h.queries.GetSetting(r.Context(), SettingDefaultBudget)
		if err != nil {
			if err == sql.ErrNoRows {
				writeJSON(w, http.StatusOK, map[string]any{"amount": 0})
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to get default budget")
			return
		}
		amount, err := strconv.ParseFloat(setting.Value, 64)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "invalid default budget value")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"amount": amount})

	case http.MethodPut:
		var req defaultBudgetRequest
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
		err := h.queries.UpsertSetting(r.Context(), database.UpsertSettingParams{
			Key:   SettingDefaultBudget,
			Value: strconv.FormatFloat(req.Amount, 'f', -1, 64),
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update default budget")
			return
		}
		// Live-updates: the default budget feeds every month without an
		// explicit budget, so it re-colors budget cells and the reports
		// budget surface. Post-commit, best-effort, nil-safe.
		h.publishInvalidate("budgets", "reports")
		writeJSON(w, http.StatusOK, map[string]any{"amount": req.Amount})

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
