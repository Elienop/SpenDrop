package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/database"
)

// homepageSummaryResponse is the JSON payload returned by
// GET /api/homepage/summary. It is designed to be consumed directly by
// Gluetun's customapi widget without any client-side transformation.
type homepageSummaryResponse struct {
	// MonthSpent is the token owner's total expense spend for the current
	// calendar month, in dollars (two decimal places).
	MonthSpent float64 `json:"month_spent"`
	// MonthBudget is the configured monthly budget in dollars (monthly row →
	// default setting → 0).
	MonthBudget float64 `json:"month_budget"`
	// MonthRemaining is MonthBudget - MonthSpent. Negative means over budget.
	MonthRemaining float64 `json:"month_remaining"`
	// TxnCount is the number of non-deleted transactions (both expense and
	// income) the token owner has entered this calendar month.
	TxnCount int64 `json:"txn_count"`
	// Currency is the base currency code from app_settings (e.g. "USD").
	Currency string `json:"currency"`
	// OverBudgetCategories is the count of expense categories whose
	// household-wide month-to-date spend exceeds their per-category budget
	// limit (category_budgets table, migration 012). It is computed
	// household-wide — NOT scoped to the token owner — using
	// SumByCategoryForMonth (which already filters tombstoned rows) joined to
	// the limits for the current (year, month). Categories without a limit set
	// never contribute to the count.
	OverBudgetCategories int64 `json:"over_budget_categories"`
	// AsOf is the UTC instant at which the aggregation was computed. On a
	// cache hit this reflects the original computation time, NOT the time
	// the response was served — callers can use it to detect stale data.
	AsOf string `json:"as_of"`
}

// handleHomepageSummary handles GET /api/homepage/summary.
//
// The route is mounted under auth.RequireAPIToken middleware, so by the time
// this handler runs the bearer token has been validated and the token owner
// has been placed on the request context via auth.SetUser. Session cookies
// are explicitly NOT accepted here (the middleware rejects them with 401).
//
// Per-token response cache: the first call for a given token computes the
// payload and stores it for summaryCacheTTL (15 s). Subsequent calls within
// the TTL return the byte-identical cached payload so the DB is not hit on
// every scrape. The cache key is the SHA-256 hash of the bearer token, NOT
// the user ID, so two tokens owned by the same user have independent TTL
// slots — inserting a new transaction invalidates the next scrape for token
// T2 even while T1's slot is still warm.
func (h *Handler) handleHomepageSummary(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.GetUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// Extract the bearer token hash for cache keying. The Authorization
	// header has already been validated by RequireAPIToken; we re-read it
	// here only to derive the cache key — no second DB lookup.
	authz := r.Header.Get("Authorization")
	tokenHash := auth.HashAPIToken(strings.TrimPrefix(authz, "Bearer "))

	// Cache hit: return the pre-marshalled payload without touching the DB.
	if entry, ok := h.summaryCache.get(tokenHash); ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(entry.payload)
		return
	}

	// Call clock.Now() ONCE. This value is used for:
	//   1. The aggregation window (year/month)
	//   2. The cache entry's asOf timestamp
	//   3. The as_of field in the JSON response
	// Using a single instant ensures the DB query and the cache entry are
	// always coherent — a second call to Now() could straddle a month
	// boundary and produce a mismatched as_of vs. aggregation window.
	now := h.clock.Now()
	ctx := r.Context()

	yearStr := strconv.Itoa(now.Year())
	monthStr := fmt.Sprintf("%02d", int(now.Month()))

	// Resolve budget: monthly row → SettingDefaultBudget setting → 0.
	var budgetCents int64
	b, err := h.queries.GetBudget(ctx, database.GetBudgetParams{
		Year:  int64(now.Year()),
		Month: int64(now.Month()),
	})
	if err == nil {
		budgetCents = b.AmountCents
	} else if errors.Is(err, sql.ErrNoRows) {
		setting, settingErr := h.queries.GetSetting(ctx, SettingDefaultBudget)
		if settingErr == nil {
			parsed, parseErr := strconv.ParseFloat(setting.Value, 64)
			if parseErr == nil && parsed >= 0 {
				budgetCents = dollarsToCents(parsed)
			}
		}
		// If setting not found either, budget stays 0.
	} else {
		writeError(w, http.StatusInternalServerError, "failed to get budget")
		return
	}

	// User-scoped expense sum for the current month.
	monthSpentCents, err := h.queries.SumExpensesByMonthForUser(ctx, database.SumExpensesByMonthForUserParams{
		UserID: user.ID,
		Year:   yearStr,
		Month:  monthStr,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to sum expenses")
		return
	}

	// User-scoped transaction count for the current month (expense + income).
	txnCount, err := h.queries.CountMonthTransactionsForUser(ctx, database.CountMonthTransactionsForUserParams{
		UserID: user.ID,
		Year:   yearStr,
		Month:  monthStr,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count transactions")
		return
	}

	// over_budget_categories: count expense categories whose household-wide
	// month-to-date spend exceeds their per-category limit. Both inputs are
	// scoped to the current (year, month). SumByCategoryForMonth already
	// filters c.type='expense' and t.deleted_at IS NULL, so the spend side is
	// tombstone-safe; only categories with a limit row participate.
	overBudget, err := h.countOverBudgetCategories(ctx, int64(now.Year()), int64(now.Month()), yearStr, monthStr)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to compute over-budget categories")
		return
	}

	currency := h.getBaseCurrency(ctx)

	payload := homepageSummaryResponse{
		MonthSpent:           centsToDollars(monthSpentCents),
		MonthBudget:          centsToDollars(budgetCents),
		MonthRemaining:       centsToDollars(budgetCents - monthSpentCents),
		TxnCount:             txnCount,
		Currency:             currency,
		OverBudgetCategories: overBudget,
		AsOf:                 now.UTC().Format(time.RFC3339),
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode response")
		return
	}

	// Store in cache BEFORE writing to the client so a concurrent request
	// that arrives after the put() but before the Write() gets a cache hit
	// and returns the same bytes.
	h.summaryCache.put(tokenHash, encoded, now)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(encoded)
}

// countOverBudgetCategories returns how many expense categories have a
// per-category limit for (yearInt, monthInt) whose household-wide month-to-date
// spend strictly exceeds that limit. yearStr/monthStr are the same instant
// formatted for SumByCategoryForMonth (which keys on strftime text). Categories
// with no limit row are never counted; categories with a limit but no spend
// have spend 0 and therefore never exceed a positive limit.
func (h *Handler) countOverBudgetCategories(ctx context.Context, yearInt, monthInt int64, yearStr, monthStr string) (int64, error) {
	limits, err := h.queries.ListCategoryBudgetsByMonth(ctx, database.ListCategoryBudgetsByMonthParams{
		Year:  yearInt,
		Month: monthInt,
	})
	if err != nil {
		return 0, err
	}
	if len(limits) == 0 {
		return 0, nil
	}

	spend, err := h.queries.SumByCategoryForMonth(ctx, database.SumByCategoryForMonthParams{
		Year:  yearStr,
		Month: monthStr,
	})
	if err != nil {
		return 0, err
	}

	var count int64
	for _, s := range overBudgetByCategory(spend, limits) {
		if s.Over {
			count++
		}
	}
	return count, nil
}
