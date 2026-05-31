package api

import (
	"database/sql"
	"fmt"
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
		if parsed < MinYear || parsed > MaxYear {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("year must be between %d and %d", MinYear, MaxYear))
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
	if year < MinYear || year > MaxYear {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("year must be between %d and %d", MinYear, MaxYear))
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

	// Phase 3.1b: the legacy REAL amount column was dropped in migration 010;
	// only amount_cents is written. The cents value is derived once from the
	// client-supplied float at the wire edge.
	err = h.queries.UpsertBudget(r.Context(), database.UpsertBudgetParams{
		Year:        year,
		Month:       month,
		AmountCents: dollarsToCents(req.Amount),
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
