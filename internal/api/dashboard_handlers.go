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
	Year                int     `json:"year"`
	Month               int     `json:"month"`
	Budget              float64 `json:"budget"`
	TotalSpent          float64 `json:"total_spent"`
	TotalIncome         float64 `json:"total_income"`
	Remaining           float64 `json:"remaining"`
	SavingsThisMonth    float64 `json:"savings_this_month"`
	SavingsGoal         float64 `json:"savings_goal"`
	SavingsYTD          float64 `json:"savings_ytd"`
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
	ID    int64    `json:"id"`
	Name  string   `json:"name"`
	Total float64  `json:"total"`
	Limit *float64 `json:"limit"` // dollars; null when no limit set
	Over  bool     `json:"over"`  // false when no limit
}

// parseYearMonth extracts year and month from query params, defaulting to
// the Handler's current clock instant. Phase 3.2 moved this from a
// package-level function to a method on *Handler so every caller picks up
// the injected clock automatically - the alternative (adding a `now
// time.Time` parameter at every call site) touched four handlers and made
// the test signature incompatible with the rest of the codebase.
func (h *Handler) parseYearMonth(r *http.Request) (int, int, error) {
	now := h.clock.Now()
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

	if year < MinDataYear || year > MaxDataYear {
		return 0, 0, fmt.Errorf("year must be between %d and %d", MinDataYear, MaxDataYear)
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

	year, month, err := h.parseYearMonth(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx := r.Context()
	yearStr := fmt.Sprintf("%d", year)
	monthStr := fmt.Sprintf("%02d", month)

	// Resolve budget: monthly budget -> default setting -> 0.
	//
	// Phase 3.1a: the handler tracks budgetCents (int64) through the whole
	// computation and only converts to dollars at the JSON wire edge. The
	// default-budget setting comes in as a user-entered string, so we parse
	// it as a float once and immediately convert to cents with
	// dollarsToCents - after that point, nothing in this handler touches a
	// float sum, so float drift across SUM() / subtraction / division
	// becomes impossible by construction.
	var budgetCents int64
	b, err := h.queries.GetBudget(ctx, database.GetBudgetParams{
		Year:  int64(year),
		Month: int64(month),
	})
	if err == nil {
		budgetCents = b.AmountCents
	} else if errors.Is(err, sql.ErrNoRows) {
		setting, settingErr := h.queries.GetSetting(ctx, SettingDefaultBudget)
		if settingErr == nil {
			if cents, ok := defaultBudgetCents(setting.Value); ok {
				budgetCents = cents
			}
		}
		// If setting not found, or it holds a value this application cannot
		// represent, budget stays 0.
	} else {
		writeError(w, http.StatusInternalServerError, "failed to get budget")
		return
	}

	// Total expenses for the month
	totalSpentCents, err := h.queries.SumExpensesByMonth(ctx, database.SumExpensesByMonthParams{
		Year:  yearStr,
		Month: monthStr,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to sum expenses")
		return
	}

	// Total income for the month
	totalIncomeCents, err := h.queries.SumIncomeByMonth(ctx, database.SumIncomeByMonthParams{
		Year:  yearStr,
		Month: monthStr,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to sum income")
		return
	}

	remainingCents := budgetCents - totalSpentCents
	savingsThisMonthCents := totalIncomeCents - totalSpentCents

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
	var savingsYTDCents int64
	for _, row := range ytdRows {
		savingsYTDCents += row.IncomeCents - row.ExpensesCents
	}

	// Savings goal
	var savingsGoalCents int64
	goal, err := h.queries.GetSavingsGoal(ctx, int64(year))
	if err == nil {
		savingsGoalCents = goal.TargetAmountCents
	} else if !errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to get savings goal")
		return
	}

	// Savings goal progress: percentage with 2 decimal places. Computed in
	// float because the output is inherently a ratio, not a money amount -
	// the input operands are integer cents so the numerator/denominator
	// themselves are exact, and the final math.Round to 0.01% matches the
	// pre-cents behaviour exactly.
	var savingsGoalProgress float64
	if savingsGoalCents > 0 {
		savingsGoalProgress = math.Round(float64(savingsYTDCents)/float64(savingsGoalCents)*10000) / 100
	}

	writeJSON(w, http.StatusOK, dashboardSummaryResponse{
		Year:                year,
		Month:               month,
		Budget:              centsToDollars(budgetCents),
		TotalSpent:          centsToDollars(totalSpentCents),
		TotalIncome:         centsToDollars(totalIncomeCents),
		Remaining:           centsToDollars(remainingCents),
		SavingsThisMonth:    centsToDollars(savingsThisMonthCents),
		SavingsGoal:         centsToDollars(savingsGoalCents),
		SavingsYTD:          centsToDollars(savingsYTDCents),
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
	if months > MaxTrendMonths {
		months = MaxTrendMonths
	}

	ctx := r.Context()

	// Use optional year/month to anchor the trend window, defaulting to now.
	anchor := h.clock.Now()
	if y := r.URL.Query().Get("year"); y != "" {
		if m := r.URL.Query().Get("month"); m != "" {
			py, errY := strconv.Atoi(y)
			pm, errM := strconv.Atoi(m)
			if errY == nil && errM == nil && py >= MinDataYear && py <= MaxDataYear && pm >= 1 && pm <= 12 {
				anchor = time.Date(py, time.Month(pm), 1, 0, 0, 0, 0, time.UTC)
			}
		}
	}

	// Normalize to first-of-month so AddDate cannot overflow a short month
	// (e.g. 2026-03-31 minus 1 month -> 2026-02-31 -> normalized 2026-03-03,
	// which would drop February and duplicate March in the walk below).
	base := time.Date(anchor.Year(), anchor.Month(), 1, 0, 0, 0, 0, time.UTC)

	// Calculate date range: from (months-1) months ago to the anchor month
	earliest := base.AddDate(0, -(months - 1), 0)
	dateFrom := fmt.Sprintf("%d-%02d-01", earliest.Year(), earliest.Month())
	dateTo := time.Date(base.Year(), base.Month()+1, 0, 0, 0, 0, 0, time.UTC).Format("2006-01-02")

	rows, err := h.queries.SumByMonthRange(ctx, database.SumByMonthRangeParams{
		DateFrom: dateFrom,
		DateTo:   dateTo,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query trend data")
		return
	}

	// Build lookup from query results. Phase 3.1a: SumByMonthRangeRow now
	// carries ExpensesCents/IncomeCents (int64), converted to float dollars
	// at the wire edge below.
	type monthKey struct{ y, m int }
	lookup := make(map[monthKey]database.SumByMonthRangeRow, len(rows))
	for _, row := range rows {
		lookup[monthKey{int(row.Year), int(row.Month)}] = row
	}

	// Build trend entries walking backwards from anchor month
	trend := make([]trendEntry, 0, months)
	for i := 0; i < months; i++ {
		t := base.AddDate(0, -i, 0)
		y := t.Year()
		m := int(t.Month())
		entry := trendEntry{Year: y, Month: m}
		if row, ok := lookup[monthKey{y, m}]; ok {
			entry.TotalSpent = centsToDollars(row.ExpensesCents)
			entry.TotalIncome = centsToDollars(row.IncomeCents)
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

	year, month, err := h.parseYearMonth(r)
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

	limits, err := h.queries.ListCategoryBudgetsByMonth(r.Context(), database.ListCategoryBudgetsByMonthParams{
		Year:  int64(year),
		Month: int64(month),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list category budgets")
		return
	}
	status := overBudgetByCategory(rows, limits)

	categories := make([]categoryEntry, len(rows))
	for i, row := range rows {
		entry := categoryEntry{
			ID:    row.ID,
			Name:  row.Name,
			Total: centsToDollars(row.TotalCents),
		}
		if s, ok := status[row.ID]; ok {
			limitDollars := centsToDollars(s.LimitCents)
			entry.Limit = &limitDollars
			entry.Over = s.Over
		}
		categories[i] = entry
	}

	writeJSON(w, http.StatusOK, map[string]any{"categories": categories})
}
