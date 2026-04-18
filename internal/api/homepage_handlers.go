package api

import (
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
	// month-to-date spend exceeds their per-category budget.
	//
	// TODO(per-category-budgets): per-category budget rows do not exist yet
	// (the budgets table is month-level only). This field is stubbed to 0
	// until the schema gains a category_budgets table and the corresponding
	// query is written. Keeping the field in the response now lets the
	// Homepage widget template reference it without a schema break later.
	// Tracked in: feat/per-category-budgets (future milestone).
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

	currency := h.getBaseCurrency(ctx)

	payload := homepageSummaryResponse{
		MonthSpent:           centsToDollars(monthSpentCents),
		MonthBudget:          centsToDollars(budgetCents),
		MonthRemaining:       centsToDollars(budgetCents - monthSpentCents),
		TxnCount:             txnCount,
		Currency:             currency,
		OverBudgetCategories: 0, // TODO(per-category-budgets): see field comment
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
