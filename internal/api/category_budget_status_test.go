package api

import (
	"testing"

	"github.com/elienop/spendrop/internal/database"
)

func TestOverBudgetByCategory(t *testing.T) {
	spend := []database.SumByCategoryForMonthRow{
		{ID: 1, Name: "Groceries", TotalCents: 61200}, // > $500 limit -> over
		{ID: 2, Name: "Dining", TotalCents: 18000},    // < $300 limit -> under
		{ID: 3, Name: "Utilities", TotalCents: 25000}, // == $250 limit -> NOT over (strict >)
		{ID: 4, Name: "Transport", TotalCents: 9500},  // spend, but no limit
	}
	limits := []database.CategoryBudget{
		{CategoryID: 1, AmountCents: 50000},
		{CategoryID: 2, AmountCents: 30000},
		{CategoryID: 3, AmountCents: 25000},
		{CategoryID: 5, AmountCents: 10000}, // limit, but no spend this month
	}

	got := overBudgetByCategory(spend, limits)

	if len(got) != 3 {
		t.Fatalf("want 3 entries (categories with BOTH spend and a limit), got %d: %+v", len(got), got)
	}
	if s, ok := got[1]; !ok || !s.Over || s.LimitCents != 50000 {
		t.Errorf("cat 1: want {LimitCents:50000, Over:true}, got %+v (ok=%v)", s, ok)
	}
	if s, ok := got[2]; !ok || s.Over || s.LimitCents != 30000 {
		t.Errorf("cat 2: want {LimitCents:30000, Over:false}, got %+v (ok=%v)", s, ok)
	}
	if s, ok := got[3]; !ok || s.Over {
		t.Errorf("cat 3: spend == limit must NOT be over (strict >), got %+v (ok=%v)", s, ok)
	}
	if _, ok := got[4]; ok {
		t.Error("cat 4: has spend but no limit — must be absent from the map")
	}
	if _, ok := got[5]; ok {
		t.Error("cat 5: has a limit but no spend — must be absent (not in spend rows)")
	}
}

func TestOverBudgetByCategory_NoLimits(t *testing.T) {
	spend := []database.SumByCategoryForMonthRow{{ID: 1, Name: "X", TotalCents: 99999}}
	if got := overBudgetByCategory(spend, nil); len(got) != 0 {
		t.Errorf("want empty map when there are no limits, got %+v", got)
	}
}
