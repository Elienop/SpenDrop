package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/database"
)

// --- Year-over-Year ---

type yoyMonthEntry struct {
	Month    int     `json:"month"`
	Expenses float64 `json:"expenses"`
	Income   float64 `json:"income"`
}

type yoyResponse struct {
	CurrentYear  int             `json:"current_year"`
	PreviousYear int             `json:"previous_year"`
	Current      []yoyMonthEntry `json:"current"`
	Previous     []yoyMonthEntry `json:"previous"`
}

func (h *Handler) handleReportYoY(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.GetUser(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	year := time.Now().Year()
	if v := r.URL.Query().Get("year"); v != "" {
		parsed, err := strconv.Atoi(v)
		if err != nil || parsed < MinYear || parsed > MaxYear {
			writeError(w, http.StatusBadRequest, "invalid year")
			return
		}
		year = parsed
	}

	ctx := r.Context()
	prevYear := year - 1

	curFrom := fmt.Sprintf("%d-01-01", year)
	curTo := fmt.Sprintf("%d-12-31", year)
	curRows, err := h.queries.SumByMonthRange(ctx, database.SumByMonthRangeParams{
		DateFrom: curFrom, DateTo: curTo,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query current year")
		return
	}

	prevFrom := fmt.Sprintf("%d-01-01", prevYear)
	prevTo := fmt.Sprintf("%d-12-31", prevYear)
	prevRows, err := h.queries.SumByMonthRange(ctx, database.SumByMonthRangeParams{
		DateFrom: prevFrom, DateTo: prevTo,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query previous year")
		return
	}

	toEntries := func(rows []database.SumByMonthRangeRow) []yoyMonthEntry {
		lookup := make(map[int]database.SumByMonthRangeRow, len(rows))
		for _, r := range rows {
			lookup[int(r.Month)] = r
		}
		entries := make([]yoyMonthEntry, 12)
		for m := 1; m <= 12; m++ {
			entries[m-1] = yoyMonthEntry{Month: m}
			if row, ok := lookup[m]; ok {
				entries[m-1].Expenses = centsToDollars(row.ExpensesCents)
				entries[m-1].Income = centsToDollars(row.IncomeCents)
			}
		}
		return entries
	}

	writeJSON(w, http.StatusOK, yoyResponse{
		CurrentYear:  year,
		PreviousYear: prevYear,
		Current:      toEntries(curRows),
		Previous:     toEntries(prevRows),
	})
}

// --- Category Trends ---

type categoryTrendEntry struct {
	ID   int64        `json:"id"`
	Name string       `json:"name"`
	Type string       `json:"type"`
	Data []monthTotal `json:"data"`
}

type monthTotal struct {
	Year  int     `json:"year"`
	Month int     `json:"month"`
	Total float64 `json:"total"`
}

func (h *Handler) handleReportCategoryTrends(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.GetUser(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	months := 12
	if v := r.URL.Query().Get("months"); v != "" {
		parsed, err := strconv.Atoi(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid months")
			return
		}
		if parsed < 1 {
			parsed = 1
		}
		if parsed > MaxCategoryTrendMonths {
			parsed = MaxCategoryTrendMonths
		}
		months = parsed
	}

	now := time.Now()
	earliest := now.AddDate(0, -(months - 1), 0)
	dateFrom := fmt.Sprintf("%d-%02d-01", earliest.Year(), earliest.Month())
	dateTo := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, time.UTC).Format("2006-01-02")

	rows, err := h.queries.SumByCategoryForRange(r.Context(), database.SumByCategoryForRangeParams{
		DateFrom: dateFrom, DateTo: dateTo,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query category trends")
		return
	}

	catMap := make(map[int64]*categoryTrendEntry)
	var catOrder []int64
	for _, row := range rows {
		entry, exists := catMap[row.ID]
		if !exists {
			entry = &categoryTrendEntry{
				ID: row.ID, Name: row.Name, Type: row.CategoryType,
			}
			catMap[row.ID] = entry
			catOrder = append(catOrder, row.ID)
		}
		entry.Data = append(entry.Data, monthTotal{
			Year: int(row.Year), Month: int(row.Month), Total: centsToDollars(row.TotalCents),
		})
	}

	result := make([]categoryTrendEntry, 0, len(catOrder))
	for _, id := range catOrder {
		result = append(result, *catMap[id])
	}

	writeJSON(w, http.StatusOK, map[string]any{"categories": result})
}

// --- Income vs Expenses ---

type incomeExpenseEntry struct {
	Year     int     `json:"year"`
	Month    int     `json:"month"`
	Income   float64 `json:"income"`
	Expenses float64 `json:"expenses"`
	Net      float64 `json:"net"`
}

func (h *Handler) handleReportIncomeExpenses(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.GetUser(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	months := 12
	if v := r.URL.Query().Get("months"); v != "" {
		parsed, err := strconv.Atoi(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid months")
			return
		}
		if parsed < 1 {
			parsed = 1
		}
		if parsed > MaxTrendMonths {
			parsed = MaxTrendMonths
		}
		months = parsed
	}

	now := time.Now()
	earliest := now.AddDate(0, -(months - 1), 0)
	dateFrom := fmt.Sprintf("%d-%02d-01", earliest.Year(), earliest.Month())
	dateTo := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, time.UTC).Format("2006-01-02")

	rows, err := h.queries.SumByMonthRange(r.Context(), database.SumByMonthRangeParams{
		DateFrom: dateFrom, DateTo: dateTo,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query income/expenses")
		return
	}

	type mk struct{ y, m int }
	lookup := make(map[mk]database.SumByMonthRangeRow, len(rows))
	for _, row := range rows {
		lookup[mk{int(row.Year), int(row.Month)}] = row
	}

	entries := make([]incomeExpenseEntry, 0, months)
	for i := months - 1; i >= 0; i-- {
		t := now.AddDate(0, -i, 0)
		y, m := t.Year(), int(t.Month())
		entry := incomeExpenseEntry{Year: y, Month: m}
		if row, ok := lookup[mk{y, m}]; ok {
			// Phase 3.1a: compute net in cents (int64) before converting
			// so the subtraction is exact, then convert once at the edge.
			netCents := row.IncomeCents - row.ExpensesCents
			entry.Income = centsToDollars(row.IncomeCents)
			entry.Expenses = centsToDollars(row.ExpensesCents)
			entry.Net = centsToDollars(netCents)
		}
		entries = append(entries, entry)
	}

	writeJSON(w, http.StatusOK, map[string]any{"data": entries})
}

// --- Top Merchants ---

type topMerchantEntry struct {
	Description string  `json:"description"`
	TxCount     int64   `json:"tx_count"`
	Total       float64 `json:"total"`
}

func (h *Handler) handleReportTopMerchants(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.GetUser(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	year, month, err := parseYearMonth(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	limit := int64(DefaultTopMerchantsLimit)
	if v := r.URL.Query().Get("limit"); v != "" {
		parsed, parseErr := strconv.ParseInt(v, 10, 64)
		if parseErr != nil || parsed < 1 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		if parsed > MaxTopMerchantsLimit {
			parsed = MaxTopMerchantsLimit
		}
		limit = parsed
	}

	dateFrom := fmt.Sprintf("%d-%02d-01", year, month)
	dateTo := time.Date(year, time.Month(month+1), 0, 0, 0, 0, 0, time.UTC).Format("2006-01-02")

	rows, err := h.queries.TopDescriptions(r.Context(), database.TopDescriptionsParams{
		DateFrom: dateFrom, DateTo: dateTo, Limit: limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query top merchants")
		return
	}

	merchants := make([]topMerchantEntry, len(rows))
	for i, row := range rows {
		merchants[i] = topMerchantEntry{
			Description: row.Description,
			TxCount:     row.TxCount,
			Total:       centsToDollars(row.TotalCents),
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"year":      year,
		"month":     month,
		"merchants": merchants,
	})
}
