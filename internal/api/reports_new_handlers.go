package api

import (
	"database/sql"
	"errors"
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

// --- Expense Velocity ---

type dailyEntry struct {
	Day        int     `json:"day"`
	DailyTotal float64 `json:"daily_total"`
}

func (h *Handler) handleExpenseVelocity(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.GetUser(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	year, month, err := parseYearMonth(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx := r.Context()
	yearStr := fmt.Sprintf("%d", year)
	monthStr := fmt.Sprintf("%02d", month)

	// Current month daily totals
	currentRows, err := h.queries.SumExpensesByDayInMonth(ctx, database.SumExpensesByDayInMonthParams{
		Year:  yearStr,
		Month: monthStr,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query current month")
		return
	}
	current := make([]dailyEntry, len(currentRows))
	for i, row := range currentRows {
		current[i] = dailyEntry{Day: int(row.Day), DailyTotal: row.DailyTotal}
	}

	// Previous month daily totals
	prevTime := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	prevYearStr := fmt.Sprintf("%d", prevTime.Year())
	prevMonthStr := fmt.Sprintf("%02d", prevTime.Month())
	prevRows, err := h.queries.SumExpensesByDayInMonth(ctx, database.SumExpensesByDayInMonthParams{
		Year:  prevYearStr,
		Month: prevMonthStr,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query previous month")
		return
	}
	previous := make([]dailyEntry, len(prevRows))
	for i, row := range prevRows {
		previous[i] = dailyEntry{Day: int(row.Day), DailyTotal: row.DailyTotal}
	}

	// Budget resolution (same fallback as budget-vs-actual)
	var budget float64
	b, err := h.queries.GetBudget(ctx, database.GetBudgetParams{
		Year: int64(year), Month: int64(month),
	})
	if err == nil {
		budget = b.Amount
	} else if errors.Is(err, sql.ErrNoRows) {
		setting, settingErr := h.queries.GetSetting(ctx, "default_budget")
		if settingErr == nil {
			parsed, parseErr := strconv.ParseFloat(setting.Value, 64)
			if parseErr == nil {
				budget = parsed
			}
		}
	} else {
		writeError(w, http.StatusInternalServerError, "failed to get budget")
		return
	}

	daysInMonth := time.Date(year, time.Month(month+1), 0, 0, 0, 0, 0, time.UTC).Day()

	writeJSON(w, http.StatusOK, map[string]any{
		"days_in_month": daysInMonth,
		"budget":        budget,
		"current":       current,
		"previous":      previous,
	})
}

// --- Spending Heatmap ---

type heatmapEntry struct {
	Date  string  `json:"date"`
	Total float64 `json:"total"`
}

func (h *Handler) handleSpendingHeatmap(w http.ResponseWriter, r *http.Request) {
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

	rows, err := h.queries.SumExpensesByDay(r.Context(), fmt.Sprintf("%d", year))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query heatmap data")
		return
	}

	data := make([]heatmapEntry, len(rows))
	for i, row := range rows {
		data[i] = heatmapEntry{Date: row.Date.Format("2006-01-02"), Total: row.Total}
	}

	writeJSON(w, http.StatusOK, map[string]any{"data": data})
}
