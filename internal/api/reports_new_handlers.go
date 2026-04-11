package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/database"
)

// --- Budget vs Actual ---

type budgetVsActualEntry struct {
	Month  int     `json:"month"`
	Budget float64 `json:"budget"`
	Actual float64 `json:"actual"`
}

func (h *Handler) handleBudgetVsActual(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.GetUser(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	year := time.Now().Year()
	if ys := r.URL.Query().Get("year"); ys != "" {
		parsed, err := strconv.Atoi(ys)
		if err != nil || parsed < 2000 || parsed > 2100 {
			writeError(w, http.StatusBadRequest, "invalid year")
			return
		}
		year = parsed
	}

	ctx := r.Context()

	// Get all explicit budgets for the year
	budgets, err := h.queries.ListBudgetsByYear(ctx, int64(year))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list budgets")
		return
	}
	budgetMap := make(map[int]float64)
	for _, b := range budgets {
		budgetMap[int(b.Month)] = b.Amount
	}

	// Fallback: default_budget setting
	var defaultBudget float64
	setting, err := h.queries.GetSetting(ctx, "default_budget")
	if err == nil {
		parsed, parseErr := strconv.ParseFloat(setting.Value, 64)
		if parseErr == nil {
			defaultBudget = parsed
		}
	}

	// Get actual spending per month
	dateFrom := fmt.Sprintf("%d-01-01", year)
	dateTo := fmt.Sprintf("%d-12-31", year)
	rows, err := h.queries.SumByMonthRange(ctx, database.SumByMonthRangeParams{
		DateFrom: dateFrom,
		DateTo:   dateTo,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to sum expenses")
		return
	}
	actualMap := make(map[int]float64)
	for _, row := range rows {
		actualMap[int(row.Month)] = row.Expenses
	}

	// Build 12-month response
	data := make([]budgetVsActualEntry, 12)
	for m := 1; m <= 12; m++ {
		budget, ok := budgetMap[m]
		if !ok {
			budget = defaultBudget
		}
		data[m-1] = budgetVsActualEntry{
			Month:  m,
			Budget: budget,
			Actual: actualMap[m],
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"data": data})
}
