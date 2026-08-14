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

// budgetVsActualEntry pairs a month's budget with what was actually spent.
//
// B10: `actual` is the SIGNED net of the month's expense rows and may be
// negative when refunds outweigh purchases. `budget` cannot — category and
// monthly budget limits keep their own CHECK(amount_cents > 0).
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

	year := h.clock.Now().Year()
	if ys := r.URL.Query().Get("year"); ys != "" {
		parsed, err := strconv.Atoi(ys)
		if err != nil || parsed < MinDataYear || parsed > MaxDataYear {
			writeError(w, http.StatusBadRequest, "invalid year")
			return
		}
		year = parsed
	}

	ctx := r.Context()

	// Get all explicit budgets for the year. Phase 3.1a: consume the new
	// AmountCents column and keep the budgetMap in int64 cents so the
	// fallback / default arithmetic is exact.
	budgets, err := h.queries.ListBudgetsByYear(ctx, int64(year))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list budgets")
		return
	}
	budgetMap := make(map[int]int64)
	for _, b := range budgets {
		budgetMap[int(b.Month)] = b.AmountCents
	}

	// Fallback: default_budget setting - stored as a user-entered string, so
	// parse once and immediately convert to cents. A value the application
	// cannot represent leaves the fallback at 0.
	var fallbackBudgetCents int64
	setting, err := h.queries.GetSetting(ctx, SettingDefaultBudget)
	if err == nil {
		if cents, ok := defaultBudgetCents(setting.Value); ok {
			fallbackBudgetCents = cents
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
	actualMap := make(map[int]int64)
	for _, row := range rows {
		actualMap[int(row.Month)] = row.ExpensesCents
	}

	// Build 12-month response. Conversion to float dollars happens once
	// per month here, at the wire edge.
	data := make([]budgetVsActualEntry, 12)
	for m := 1; m <= 12; m++ {
		budgetCents, ok := budgetMap[m]
		if !ok {
			budgetCents = fallbackBudgetCents
		}
		data[m-1] = budgetVsActualEntry{
			Month:  m,
			Budget: centsToDollars(budgetCents),
			Actual: centsToDollars(actualMap[m]),
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"data": data})
}

// --- Expense Velocity ---

// dailyEntry is one day of the expense-velocity series.
//
// B10: `daily_total` is a SIGNED net and may be negative, so a cumulative
// spend line built from these entries can legitimately dip rather than rise
// monotonically.
type dailyEntry struct {
	Day        int     `json:"day"`
	DailyTotal float64 `json:"daily_total"`
}

func (h *Handler) handleExpenseVelocity(w http.ResponseWriter, r *http.Request) {
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

	// Current month daily totals. Phase 3.1a: sqlc returns DailyTotalCents
	// (int64); convert to float dollars per row at the wire edge.
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
		current[i] = dailyEntry{Day: int(row.Day), DailyTotal: centsToDollars(row.DailyTotalCents)}
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
		previous[i] = dailyEntry{Day: int(row.Day), DailyTotal: centsToDollars(row.DailyTotalCents)}
	}

	// Budget resolution (same fallback as budget-vs-actual). Phase 3.1a:
	// consume AmountCents from the row; parse the default-budget setting
	// once then convert to cents immediately.
	var budgetCents int64
	b, err := h.queries.GetBudget(ctx, database.GetBudgetParams{
		Year: int64(year), Month: int64(month),
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
	} else {
		writeError(w, http.StatusInternalServerError, "failed to get budget")
		return
	}

	daysInMonth := time.Date(year, time.Month(month+1), 0, 0, 0, 0, 0, time.UTC).Day()

	writeJSON(w, http.StatusOK, map[string]any{
		"days_in_month": daysInMonth,
		"budget":        centsToDollars(budgetCents),
		"current":       current,
		"previous":      previous,
	})
}

// --- Spending Heatmap ---

// heatmapEntry is one day of the spending heatmap.
//
// B10 wire contract: `total` is a SIGNED net in dollars and may be zero or
// negative when a day's refunds meet or outweigh its purchases. A consumer must
// therefore key "does this day have activity?" on `txn_count`, never on
// `total > 0` — the two answers diverge on exactly the days a refund landed,
// and those are the days whose rows a user most wants to open. `txn_count` is
// the number of live expense rows that fed `total`, so it is >= 1 for every day
// present in the response.
type heatmapEntry struct {
	Date     string  `json:"date"`
	Total    float64 `json:"total"`
	TxnCount int64   `json:"txn_count"`
}

func (h *Handler) handleSpendingHeatmap(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.GetUser(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	year := h.clock.Now().Year()
	if ys := r.URL.Query().Get("year"); ys != "" {
		parsed, err := strconv.Atoi(ys)
		if err != nil || parsed < MinDataYear || parsed > MaxDataYear {
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
		data[i] = heatmapEntry{
			Date:     row.Date.Format("2006-01-02"),
			Total:    centsToDollars(row.TotalCents),
			TxnCount: row.TxnCount,
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"data": data})
}

// --- Recurring Expenses ---

// recurringEntry is one recurring description's yearly rollup.
//
// B10: `annual_total` and therefore `monthly_avg` are SIGNED and may be
// negative for a description whose refunds outweigh its charges. The division
// stays safe regardless: RecurringDescriptions' HAVING requires month_count
// >= 3, so the denominator can never be zero.
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

	year := h.clock.Now().Year()
	if ys := r.URL.Query().Get("year"); ys != "" {
		parsed, err := strconv.Atoi(ys)
		if err != nil || parsed < MinDataYear || parsed > MaxDataYear {
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
	key := dismissedRecurringKey(year)
	setting, err := h.queries.GetSetting(ctx, key)
	if err == nil && setting.Value != "" {
		var list []string
		if json.Unmarshal([]byte(setting.Value), &list) == nil {
			for _, d := range list {
				dismissed[d] = true
			}
		}
	}

	// Phase 3.1a: AnnualTotalCents (int64) flows out of sqlc; convert to
	// float dollars at the wire edge. MonthlyAvg does a float division
	// because it is an average, but the int64 numerator/denominator are
	// themselves exact so the only float step is the final rounding, which
	// matches pre-cents behaviour exactly.
	data := make([]recurringEntry, 0, len(rows))
	for _, row := range rows {
		if dismissed[row.Description] {
			continue
		}
		annualTotal := centsToDollars(row.AnnualTotalCents)
		data = append(data, recurringEntry{
			Description: row.Description,
			MonthlyAvg:  math.Round(annualTotal/float64(row.MonthCount)*100) / 100,
			MonthCount:  row.MonthCount,
			AnnualTotal: annualTotal,
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
	if req.Year < MinDataYear || req.Year > MaxDataYear || req.Description == "" {
		writeError(w, http.StatusBadRequest, "year and description required")
		return
	}
	if charLen(req.Description) > MaxDescriptionLength {
		writeError(w, http.StatusBadRequest, "description too long")
		return
	}

	ctx := r.Context()
	key := dismissedRecurringKey(req.Year)

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

// tagEntry is one tag's rollup over the requested window.
//
// B10: `total` is a SIGNED net and may be negative; `count` is the number of
// rows carrying the tag and is always >= 1, so the two disagree about "is
// there anything here" exactly on refund-dominated tags. The DESC sort on
// `total` is a signed sort — a refunded tag ranks below a zero one.
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

	year := h.clock.Now().Year()
	if ys := r.URL.Query().Get("year"); ys != "" {
		parsed, err := strconv.Atoi(ys)
		if err != nil || parsed < MinDataYear || parsed > MaxDataYear {
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

	// Go-side aggregation: split CSV tags, accumulate per tag. Phase 3.1a:
	// the per-tag running total is int64 cents (not float64 dollars) so the
	// accumulation across thousands of rows is exact. The previous version
	// accumulated row.Amount (float64) and wore a math.Round(*100)/100
	// patch at the end - that is the exact pattern Phase 3.1a exists to
	// eliminate by making float drift impossible by construction.
	type tagAgg struct {
		totalCents int64
		count      int
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
			agg[tag].totalCents += row.AmountCents
			agg[tag].count++
		}
	}

	// Sort by total descending. Convert to dollars at the wire edge; the
	// rounding patch is gone because the int64 sum is already exact.
	data := make([]tagEntry, 0, len(agg))
	for tag, a := range agg {
		data = append(data, tagEntry{
			Tag:   tag,
			Total: centsToDollars(a.totalCents),
			Count: a.count,
		})
	}
	sort.Slice(data, func(i, j int) bool {
		return data[i].Total > data[j].Total
	})

	writeJSON(w, http.StatusOK, map[string]any{"data": data})
}
