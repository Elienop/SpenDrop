package api

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/database"
)

// dashboardSummaryResponse is the JSON response for GET /api/dashboard/summary.
type dashboardSummaryResponse struct {
	Year               int     `json:"year"`
	Month              int     `json:"month"`
	Budget             float64 `json:"budget"`
	TotalSpent         float64 `json:"total_spent"`
	TotalIncome        float64 `json:"total_income"`
	Remaining          float64 `json:"remaining"`
	SavingsThisMonth   float64 `json:"savings_this_month"`
	SavingsGoal        float64 `json:"savings_goal"`
	SavingsYTD         float64 `json:"savings_ytd"`
	SavingsGoalProgress float64 `json:"savings_goal_progress"`
}

// trendEntry is a single month entry in the trend response.
type trendEntry struct {
	Year        int     `json:"year"`
	Month       int     `json:"month"`
	TotalSpent  float64 `json:"total_spent"`
	TotalIncome float64 `json:"total_income"`
}

// categoryEntry is a single category in the categories response.
type categoryEntry struct {
	ID    int64   `json:"id"`
	Name  string  `json:"name"`
	Total float64 `json:"total"`
}

// parseYearMonth extracts year and month from query params, defaulting to current date.
func parseYearMonth(r *http.Request) (int, int, error) {
	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	yearStr := r.URL.Query().Get("year")
	if yearStr != "" {
		parsed, err := strconv.Atoi(yearStr)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid year")
		}
		year = parsed
	}

	monthStr := r.URL.Query().Get("month")
	if monthStr != "" {
		parsed, err := strconv.Atoi(monthStr)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid month")
		}
		month = parsed
	}

	if year < 2000 || year > 2100 {
		return 0, 0, fmt.Errorf("year must be between 2000 and 2100")
	}
	if month < 1 || month > 12 {
		return 0, 0, fmt.Errorf("month must be between 1 and 12")
	}

	return year, month, nil
}

// handleDashboardSummary returns KPI data for a given month.
func (h *Handler) handleDashboardSummary(w http.ResponseWriter, r *http.Request) {
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

	// Resolve budget: monthly budget -> default setting -> 0
	var budget float64
	b, err := h.queries.GetBudget(ctx, database.GetBudgetParams{
		Year:  int64(year),
		Month: int64(month),
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
		// If setting not found either, budget stays 0
	} else {
		writeError(w, http.StatusInternalServerError, "failed to get budget")
		return
	}

	// Total expenses for the month
	totalSpent, err := h.queries.SumExpensesByMonth(ctx, database.SumExpensesByMonthParams{
		Year:  yearStr,
		Month: monthStr,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to sum expenses")
		return
	}

	// Total income for the month
	totalIncome, err := h.queries.SumIncomeByMonth(ctx, database.SumIncomeByMonthParams{
		Year:  yearStr,
		Month: monthStr,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to sum income")
		return
	}

	remaining := budget - totalSpent
	savingsThisMonth := totalIncome - totalSpent

	// Savings YTD: single aggregate query for Jan through end of current month
	ytdFrom := fmt.Sprintf("%d-01-01", year)
	ytdTo := time.Date(year, time.Month(month+1), 0, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	ytdRows, err := h.queries.SumByMonthRange(ctx, database.SumByMonthRangeParams{
		DateFrom: ytdFrom,
		DateTo:   ytdTo,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to calculate savings ytd")
		return
	}
	var savingsYTD float64
	for _, row := range ytdRows {
		savingsYTD += row.Income - row.Expenses
	}

	// Savings goal
	var savingsGoal float64
	goal, err := h.queries.GetSavingsGoal(ctx, int64(year))
	if err == nil {
		savingsGoal = goal.TargetAmount
	} else if !errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to get savings goal")
		return
	}

	// Savings goal progress
	var savingsGoalProgress float64
	if savingsGoal > 0 {
		savingsGoalProgress = math.Round(savingsYTD/savingsGoal*10000) / 100
	}

	writeJSON(w, http.StatusOK, dashboardSummaryResponse{
		Year:               year,
		Month:              month,
		Budget:             budget,
		TotalSpent:         totalSpent,
		TotalIncome:        totalIncome,
		Remaining:          remaining,
		SavingsThisMonth:   savingsThisMonth,
		SavingsGoal:        savingsGoal,
		SavingsYTD:         savingsYTD,
		SavingsGoalProgress: savingsGoalProgress,
	})
}

// handleDashboardTrend returns monthly totals for the last N months.
func (h *Handler) handleDashboardTrend(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.GetUser(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	months := 12
	monthsStr := r.URL.Query().Get("months")
	if monthsStr != "" {
		parsed, err := strconv.Atoi(monthsStr)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid months parameter")
			return
		}
		months = parsed
	}
	if months < 1 {
		months = 1
	}
	if months > 120 {
		months = 120
	}

	ctx := r.Context()

	// Use optional year/month to anchor the trend window, defaulting to now.
	anchor := time.Now()
	if y := r.URL.Query().Get("year"); y != "" {
		if m := r.URL.Query().Get("month"); m != "" {
			py, errY := strconv.Atoi(y)
			pm, errM := strconv.Atoi(m)
			if errY == nil && errM == nil && py >= 2000 && py <= 2100 && pm >= 1 && pm <= 12 {
				anchor = time.Date(py, time.Month(pm), 1, 0, 0, 0, 0, time.UTC)
			}
		}
	}

	// Calculate date range: from (months-1) months ago to the anchor month
	earliest := anchor.AddDate(0, -(months-1), 0)
	dateFrom := fmt.Sprintf("%d-%02d-01", earliest.Year(), earliest.Month())
	dateTo := time.Date(anchor.Year(), anchor.Month()+1, 0, 0, 0, 0, 0, time.UTC).Format("2006-01-02")

	rows, err := h.queries.SumByMonthRange(ctx, database.SumByMonthRangeParams{
		DateFrom: dateFrom,
		DateTo:   dateTo,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query trend data")
		return
	}

	// Build lookup from query results
	type monthKey struct{ y, m int }
	lookup := make(map[monthKey]database.SumByMonthRangeRow, len(rows))
	for _, row := range rows {
		lookup[monthKey{int(row.Year), int(row.Month)}] = row
	}

	// Build trend entries walking backwards from anchor month
	trend := make([]trendEntry, 0, months)
	for i := 0; i < months; i++ {
		t := anchor.AddDate(0, -i, 0)
		y := t.Year()
		m := int(t.Month())
		entry := trendEntry{Year: y, Month: m}
		if row, ok := lookup[monthKey{y, m}]; ok {
			entry.TotalSpent = row.Expenses
			entry.TotalIncome = row.Income
		}
		trend = append(trend, entry)
	}

	writeJSON(w, http.StatusOK, map[string]any{"trend": trend})
}

// handleDashboardCategories returns category breakdown for a given month.
func (h *Handler) handleDashboardCategories(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.GetUser(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	year, month, err := parseYearMonth(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	yearStr := fmt.Sprintf("%d", year)
	monthStr := fmt.Sprintf("%02d", month)

	rows, err := h.queries.SumByCategoryForMonth(r.Context(), database.SumByCategoryForMonthParams{
		Year:  yearStr,
		Month: monthStr,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to sum by category")
		return
	}

	categories := make([]categoryEntry, len(rows))
	for i, row := range rows {
		categories[i] = categoryEntry{
			ID:    row.ID,
			Name:  row.Name,
			Total: row.Total,
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"categories": categories})
}
