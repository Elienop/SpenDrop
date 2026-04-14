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
		year = int64(time.Now().Year())
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

	writeJSON(w, http.StatusOK, budgets)
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

	// Phase 3.1a: dual-write amount_cents alongside the legacy REAL amount.
	// The cents value is derived once from the client-supplied float so the
	// two columns stay in lockstep on every upsert.
	err = h.queries.UpsertBudget(r.Context(), database.UpsertBudgetParams{
		Year:        year,
		Month:       month,
		Amount:      req.Amount,
		AmountCents: dollarsToCents(req.Amount),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to set budget")
		return
	}

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
		writeJSON(w, http.StatusOK, map[string]any{"amount": req.Amount})

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
