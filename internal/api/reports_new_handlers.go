package api

import (
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

// --- Recurring Expenses ---

type recurringEntry struct {
	Description string  `json:"description"`
	MonthlyAvg  float64 `json:"monthly_avg"`
	MonthCount  int64   `json:"month_count"`
	AnnualTotal float64 `json:"annual_total"`
}

func (h *Handler) handleRecurring(w http.ResponseWriter, r *http.Request) {
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
	rows, err := h.queries.RecurringDescriptions(ctx, fmt.Sprintf("%d", year))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query recurring")
		return
	}

	// Load dismissed list from app_settings
	dismissed := make(map[string]bool)
	key := fmt.Sprintf("dismissed_recurring_%d", year)
	setting, err := h.queries.GetSetting(ctx, key)
	if err == nil && setting.Value != "" {
		var list []string
		if json.Unmarshal([]byte(setting.Value), &list) == nil {
			for _, d := range list {
				dismissed[d] = true
			}
		}
	}

	data := make([]recurringEntry, 0, len(rows))
	for _, row := range rows {
		if dismissed[row.Description] {
			continue
		}
		data = append(data, recurringEntry{
			Description: row.Description,
			MonthlyAvg:  math.Round(row.AnnualTotal/float64(row.MonthCount)*100) / 100,
			MonthCount:  row.MonthCount,
			AnnualTotal: row.AnnualTotal,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"data": data})
}

type dismissRequest struct {
	Year        int    `json:"year"`
	Description string `json:"description"`
}

func (h *Handler) handleDismissRecurring(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.GetUser(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req dismissRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Year < 2000 || req.Year > 2100 || req.Description == "" {
		writeError(w, http.StatusBadRequest, "year and description required")
		return
	}
	if len(req.Description) > 500 {
		writeError(w, http.StatusBadRequest, "description too long")
		return
	}

	ctx := r.Context()
	key := fmt.Sprintf("dismissed_recurring_%d", req.Year)

	// Use a serializable transaction to prevent TOCTOU race on the dismissed list
	tx, err := h.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin transaction")
		return
	}
	defer tx.Rollback()
	qtx := h.queries.WithTx(tx)

	// Load existing dismissed list
	var list []string
	setting, err := qtx.GetSetting(ctx, key)
	if err == nil && setting.Value != "" {
		if unmarshalErr := json.Unmarshal([]byte(setting.Value), &list); unmarshalErr != nil {
			log.Printf("corrupt dismissed_recurring setting %q: %v", key, unmarshalErr)
			writeError(w, http.StatusInternalServerError, "corrupt dismissed list data")
			return
		}
	}

	// Check idempotency
	for _, d := range list {
		if d == req.Description {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
			return
		}
	}

	list = append(list, req.Description)
	encoded, err := json.Marshal(list)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode dismissed list")
		return
	}
	if err := qtx.UpsertSetting(ctx, database.UpsertSettingParams{
		Key: key, Value: string(encoded),
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save dismissed list")
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit dismissed list")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- Tag Breakdown ---

type tagEntry struct {
	Tag   string  `json:"tag"`
	Total float64 `json:"total"`
	Count int     `json:"count"`
}

func (h *Handler) handleTagBreakdown(w http.ResponseWriter, r *http.Request) {
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

	month := 0 // 0 = YTD
	if ms := r.URL.Query().Get("month"); ms != "" {
		parsed, err := strconv.Atoi(ms)
		if err != nil || parsed < 0 || parsed > 12 {
			writeError(w, http.StatusBadRequest, "invalid month")
			return
		}
		month = parsed
	}

	// Build date range
	var dateFrom, dateTo string
	if month == 0 {
		dateFrom = fmt.Sprintf("%d-01-01", year)
		dateTo = fmt.Sprintf("%d-12-31", year)
	} else {
		dateFrom = fmt.Sprintf("%d-%02d-01", year, month)
		dateTo = time.Date(year, time.Month(month+1), 0, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	}

	rows, err := h.queries.TransactionAmountsAndTags(r.Context(), database.TransactionAmountsAndTagsParams{
		DateFrom: dateFrom,
		DateTo:   dateTo,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query tags")
		return
	}

	// Go-side aggregation: split CSV tags, accumulate per tag
	type tagAgg struct {
		total float64
		count int
	}
	agg := make(map[string]*tagAgg)
	for _, row := range rows {
		if !row.Tags.Valid || row.Tags.String == "" {
			continue
		}
		for _, raw := range strings.Split(row.Tags.String, ",") {
			tag := strings.TrimSpace(raw)
			if tag == "" {
				continue
			}
			if _, ok := agg[tag]; !ok {
				agg[tag] = &tagAgg{}
			}
			agg[tag].total += row.Amount
			agg[tag].count++
		}
	}

	// Sort by total descending
	data := make([]tagEntry, 0, len(agg))
	for tag, a := range agg {
		data = append(data, tagEntry{
			Tag:   tag,
			Total: math.Round(a.total*100) / 100,
			Count: a.count,
		})
	}
	sort.Slice(data, func(i, j int) bool {
		return data[i].Total > data[j].Total
	})

	writeJSON(w, http.StatusOK, map[string]any{"data": data})
}
