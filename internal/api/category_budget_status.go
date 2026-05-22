package api

import "github.com/elienop/spendrop/internal/database"

// categoryBudgetStatus pairs a category's per-category monthly limit with
// whether household month-to-date expense spend strictly exceeds it. Money is
// integer cents; Over is the canonical rule spendCents > limitCents.
type categoryBudgetStatus struct {
	LimitCents int64
	Over       bool
}

// overBudgetByCategory maps category id -> its limit and over-budget status for
// a single month. An entry exists only for categories present in BOTH spend and
// limits. Pure: spend and limits are passed in (no query) so the Over flag is
// computed against the exact spend the caller already holds. This is the single
// source of truth for "over budget" — both handleDashboardCategories and the
// Homepage widget's countOverBudgetCategories consume it.
func overBudgetByCategory(
	spend []database.SumByCategoryForMonthRow,
	limits []database.CategoryBudget,
) map[int64]categoryBudgetStatus {
	if len(limits) == 0 {
		return map[int64]categoryBudgetStatus{}
	}
	limitByCategory := make(map[int64]int64, len(limits))
	for _, l := range limits {
		limitByCategory[l.CategoryID] = l.AmountCents
	}
	out := make(map[int64]categoryBudgetStatus, len(limits))
	for _, row := range spend {
		limitCents, ok := limitByCategory[row.ID]
		if !ok {
			continue
		}
		out[row.ID] = categoryBudgetStatus{
			LimitCents: limitCents,
			Over:       row.TotalCents > limitCents,
		}
	}
	return out
}
