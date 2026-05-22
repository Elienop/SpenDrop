# Category Over-Budget Display Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show each budgeted expense category's spend against its per-category monthly limit on the Dashboard "Spending by Category" card, with a clear over-budget indicator.

**Architecture:** A single pure Go helper (`overBudgetByCategory`) owns the "spend > limit" rule in integer cents and is consumed by BOTH the existing Homepage widget count and the augmented `GET /api/dashboard/categories` endpoint (which gains `limit` in dollars + `over` bool). The Dashboard card renders a dedicated budget bar per budgeted category plus a passive "N over budget" header badge. No migration, no new endpoint.

**Tech Stack:** Go (chi, hand-maintained sqlc), SQLite, React + TypeScript (Vite, shadcn/ui), Vitest + Testing Library.

**Spec:** `docs/superpowers/specs/2026-05-22-category-over-budget-display-design.md`

---

## Test commands (this environment)

There is no host Go or Node toolchain; run both in containers. `web/node_modules` and the Go module/build caches are already populated.

- **GOTEST** — `docker run --rm -v "$PWD":/app -w /app -v spendrop-gomod:/go/pkg/mod -v spendrop-gobuild:/root/.cache/go-build -e CGO_ENABLED=1 golang:1.26 go test <args>`
- **WEBRUN** — `docker run --rm -v "$PWD/web":/app -w /app node:22-bookworm npx <args>`

(If a host Go 1.26 / Node toolchain is available, plain `go test <args>` and `npx <args>` from `web/` work too.)

## File structure

- `internal/api/category_budget_status.go` — **new.** The `categoryBudgetStatus` type + pure `overBudgetByCategory` helper.
- `internal/api/category_budget_status_test.go` — **new.** Unit tests for the helper.
- `internal/api/homepage_handlers.go` — **modify.** Refactor `countOverBudgetCategories` to use the helper.
- `internal/api/dashboard_handlers.go` — **modify.** Add `Limit`/`Over` to `categoryEntry`; annotate in `handleDashboardCategories`.
- `internal/api/dashboard_handlers_test.go` — **modify.** Add budget-status + tombstone tests.
- `web/src/api/types.ts` — **modify.** Add `limit`/`over` to `CategoryBreakdownItem`.
- `web/src/pages/Dashboard.tsx` — **modify.** Plumb `limit`/`over` into slices; render budget bar + header badge.
- `web/src/pages/Dashboard.test.tsx` — **modify.** Update fixture; add budget-bar tests.

---

### Task 1: Pure `overBudgetByCategory` helper

**Files:**
- Create: `internal/api/category_budget_status.go`
- Test: `internal/api/category_budget_status_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/api/category_budget_status_test.go`:

```go
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
```

- [ ] **Step 2: Run the test to verify it fails**

Run (see Test commands → GOTEST): `go test ./internal/api/ -run TestOverBudgetByCategory -v`
Expected: FAIL — compile error `undefined: overBudgetByCategory`.

- [ ] **Step 3: Write the implementation**

Create `internal/api/category_budget_status.go`:

```go
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
```

- [ ] **Step 4: Run the test to verify it passes**

Run (GOTEST): `go test ./internal/api/ -run TestOverBudgetByCategory -v`
Expected: PASS (both `TestOverBudgetByCategory` and `TestOverBudgetByCategory_NoLimits`).

- [ ] **Step 5: Commit**

```bash
git add internal/api/category_budget_status.go internal/api/category_budget_status_test.go
git commit -m "feat(api): add overBudgetByCategory helper for per-category budget status"
```

---

### Task 2: Refactor the Homepage count to use the helper

**Files:**
- Modify: `internal/api/homepage_handlers.go` (the `countOverBudgetCategories` function body)

- [ ] **Step 1: Confirm the existing guard tests pass before changing**

Run (GOTEST): `go test ./internal/api/ -run TestSummary_OverBudgetCategories -v`
Expected: PASS (5 tests: CountsHouseholdWideExceedances, NoLimits_IsZero, IgnoresMonthLevelBudgetExceedance, HidesTombstoned, SpendEqualToLimitIsNotCounted). These pin the behavior the refactor must preserve.

- [ ] **Step 2: Replace the body of `countOverBudgetCategories`**

In `internal/api/homepage_handlers.go`, replace the existing `countOverBudgetCategories` implementation (it currently builds its own `limitByCategory` map and counts inline) with a version that delegates the rule to the shared helper:

```go
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
```

Keep the doc comment above the function. Do not change its signature or call sites.

- [ ] **Step 3: Run the guard tests to verify behavior is unchanged**

Run (GOTEST): `go test ./internal/api/ -run TestSummary_OverBudgetCategories -v`
Expected: PASS (all 5, unchanged).

- [ ] **Step 4: Commit**

```bash
git add internal/api/homepage_handlers.go
git commit -m "refactor(api): derive Homepage over-budget count from overBudgetByCategory"
```

---

### Task 3: Expose `limit` + `over` from `GET /api/dashboard/categories`

**Files:**
- Modify: `internal/api/dashboard_handlers.go` (`categoryEntry` struct + `handleDashboardCategories`)
- Test: `internal/api/dashboard_handlers_test.go` (append two tests)

- [ ] **Step 1: Write the failing tests**

Append to `internal/api/dashboard_handlers_test.go` (the file already imports `context`, `net/http`, `net/http/httptest`, `testing`, and `database`; reuse them):

```go
func TestHandleDashboardCategories_IncludesBudgetStatus(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	// Category 1 = Food (expense): $612 spend vs a $500 limit -> over.
	// Category 2 = Gifts (expense): $180 spend, NO limit -> limit null, over false.
	seedTestTransaction(t, q, user.ID, 1, "2026-04-06", 612.0, "groceries")
	seedTestTransaction(t, q, user.ID, 2, "2026-04-07", 180.0, "gift")
	if err := q.UpsertCategoryBudget(context.Background(), database.UpsertCategoryBudgetParams{
		Year: 2026, Month: 4, CategoryID: 1, AmountCents: 50000, // $500
	}); err != nil {
		t.Fatalf("seed limit: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/categories?year=2026&month=4", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleDashboardCategories(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Categories []map[string]any `json:"categories"`
	}
	decodeResponse(t, rec, &resp)

	byName := map[string]map[string]any{}
	for _, c := range resp.Categories {
		byName[c["name"].(string)] = c
	}

	food, ok := byName["Food"]
	if !ok {
		t.Fatalf("missing Food category in %+v", resp.Categories)
	}
	// limit must be DOLLARS (500), never the raw cents (50000) — Money Wire-Edge DTO discipline.
	if food["limit"].(float64) != 500.0 {
		t.Errorf("Food limit: want 500 (dollars), got %v", food["limit"])
	}
	if food["over"] != true {
		t.Errorf("Food over: want true ($612 > $500), got %v", food["over"])
	}
	if _, leaked := food["limit_cents"]; leaked {
		t.Error("response leaked limit_cents; wire contract is limit in dollars")
	}

	gifts, ok := byName["Gifts"]
	if !ok {
		t.Fatalf("missing Gifts category in %+v", resp.Categories)
	}
	if gifts["limit"] != nil {
		t.Errorf("Gifts limit: want null (no limit set), got %v", gifts["limit"])
	}
	if gifts["over"] != false {
		t.Errorf("Gifts over: want false (no limit), got %v", gifts["over"])
	}
}

func TestHandleDashboardCategories_BudgetStatus_HidesTombstoned(t *testing.T) {
	q, db := setupTestDB(t)
	h := NewHandler(q, db)
	user := seedTestUser(t, q, "alice", "member")

	// Food (cat 1): live $40 (under a $100 limit) + tombstoned $999 that would
	// flip it to over if the soft-delete filter leaked.
	seedTestTransaction(t, q, user.ID, 1, "2026-04-06", 40.0, "live")
	seedTombstonedTestTransaction(t, q, user.ID, 1, "2026-04-07", 999.0, "tombstoned")
	if err := q.UpsertCategoryBudget(context.Background(), database.UpsertCategoryBudgetParams{
		Year: 2026, Month: 4, CategoryID: 1, AmountCents: 10000, // $100
	}); err != nil {
		t.Fatalf("seed limit: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/categories?year=2026&month=4", nil)
	req = withUser(req, user)
	rec := httptest.NewRecorder()
	h.handleDashboardCategories(rec, req)

	var resp struct {
		Categories []map[string]any `json:"categories"`
	}
	decodeResponse(t, rec, &resp)

	if len(resp.Categories) != 1 {
		t.Fatalf("want 1 category, got %d", len(resp.Categories))
	}
	c := resp.Categories[0]
	if c["total"].(float64) != 40.0 {
		t.Errorf("total: want 40 (tombstoned 999 excluded), got %v", c["total"])
	}
	if c["over"] != false {
		t.Errorf("over: want false ($40 live < $100 limit; tombstoned $999 must not flip it), got %v", c["over"])
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run (GOTEST): `go test ./internal/api/ -run 'TestHandleDashboardCategories_IncludesBudgetStatus|TestHandleDashboardCategories_BudgetStatus_HidesTombstoned' -v`
Expected: FAIL — `IncludesBudgetStatus` fails because `limit` is absent (raw entry has only id/name/total), so `food["limit"]` is `nil` and the `.(float64)` assertion fails.

- [ ] **Step 3: Add the `Limit`/`Over` fields to `categoryEntry`**

In `internal/api/dashboard_handlers.go`, replace the `categoryEntry` struct:

```go
type categoryEntry struct {
	ID    int64    `json:"id"`
	Name  string   `json:"name"`
	Total float64  `json:"total"`
	Limit *float64 `json:"limit"` // dollars; null when no limit set
	Over  bool     `json:"over"`  // false when no limit
}
```

- [ ] **Step 4: Annotate categories in `handleDashboardCategories`**

In `internal/api/dashboard_handlers.go`, replace the block from the `SumByCategoryForMonth` call through `writeJSON` with:

```go
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
```

(`year` and `month` are the `int` values from `h.parseYearMonth(r)` already in scope; `centsToDollars` is the existing helper.)

- [ ] **Step 5: Run the new tests + the existing dashboard-categories tests**

Run (GOTEST): `go test ./internal/api/ -run TestHandleDashboardCategories -v`
Expected: PASS — the two new tests AND the pre-existing ones (`ReturnsCategoryBreakdown`, `DefaultsToCurrentMonth`, `NoAuth_Returns401`, `InvalidYear_Returns400`, `HidesTombstoned`).

- [ ] **Step 6: Commit**

```bash
git add internal/api/dashboard_handlers.go internal/api/dashboard_handlers_test.go
git commit -m "feat(api): expose per-category limit + over flag from dashboard/categories"
```

---

### Task 4: Frontend type + slice plumbing

**Files:**
- Modify: `web/src/api/types.ts` (`CategoryBreakdownItem`)
- Modify: `web/src/pages/Dashboard.tsx` (`gaugeData` mapping)
- Modify: `web/src/pages/Dashboard.test.tsx` (`defaultDashboardData.categories` fixture)

- [ ] **Step 1: Extend the `CategoryBreakdownItem` type**

In `web/src/api/types.ts`, replace the interface:

```ts
export interface CategoryBreakdownItem {
  id: number;
  name: string;
  total: number;
  limit: number | null;
  over: boolean;
}
```

- [ ] **Step 2: Plumb `limit`/`over` into `gaugeData` slices**

In `web/src/pages/Dashboard.tsx`, update the `gaugeData` `useMemo` map callback to carry the new fields:

```ts
    return visibleCats.map((cat) => ({
      id: cat.id,
      name: cat.name,
      value: cat.total,
      color: getCategoryColorVar({ id: cat.id }),
      limit: cat.limit,
      over: cat.over,
    }));
```

- [ ] **Step 3: Update the test fixture so types + existing tests stay green**

In `web/src/pages/Dashboard.test.tsx`, update `defaultDashboardData.categories`:

```ts
  categories: [
    { id: 1, name: 'Food', total: 1200, limit: null, over: false },
    { id: 2, name: 'Transport', total: 800, limit: null, over: false },
  ],
```

- [ ] **Step 4: Typecheck to catch any other fixture missing the fields**

Run (see Test commands → WEBRUN): `tsc --noEmit`
Expected: PASS (exit 0). If it flags another test fixture building a category object (e.g. `web/src/hooks/useDashboard.test.ts`), add `limit: null, over: false` to those category literals until clean.

- [ ] **Step 5: Run the existing dashboard tests to confirm no regression**

Run (WEBRUN): `vitest run src/pages/Dashboard.test.tsx`
Expected: PASS (existing tests unaffected).

- [ ] **Step 6: Commit**

```bash
git add web/src/api/types.ts web/src/pages/Dashboard.tsx web/src/pages/Dashboard.test.tsx
git commit -m "feat(web): add limit/over to CategoryBreakdownItem and plumb into category slices"
```

---

### Task 5: Render the budget bar + "N over budget" header badge

**Files:**
- Modify: `web/src/pages/Dashboard.tsx` (imports, header badge, per-slice budget bar)
- Test: `web/src/pages/Dashboard.test.tsx` (append two tests; isolate the mock)

- [ ] **Step 1: Write the failing tests**

In `web/src/pages/Dashboard.test.tsx`, first make per-test overrides isolated by resetting the mock to the default in the existing `beforeEach`:

```ts
  beforeEach(() => {
    vi.clearAllMocks();
    mockUseDashboard.mockReturnValue(defaultDashboardData);
  });
```

Then append inside the `describe('Dashboard', ...)` block:

```ts
  test('shows a budget bar and an over-budget header badge for an over category', async () => {
    mockUseDashboard.mockReturnValue({
      ...defaultDashboardData,
      categories: [
        { id: 1, name: 'Food', total: 612, limit: 500, over: true },
        { id: 2, name: 'Transport', total: 180, limit: 300, over: false },
      ],
    });
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    await waitFor(() => {
      // Passive header badge counts over-budget categories (1 of 2 here).
      expect(screen.getByText('1 over budget')).toBeInTheDocument();
      // Per-category budget percentage: 612/500 = 122%.
      expect(screen.getByText(/122%/)).toBeInTheDocument();
      // Under-budget category still shows its percentage: 180/300 = 60%.
      expect(screen.getByText(/60%/)).toBeInTheDocument();
    });
  });

  test('shows no over-budget badge when everything is within budget', async () => {
    mockUseDashboard.mockReturnValue({
      ...defaultDashboardData,
      categories: [
        { id: 1, name: 'Food', total: 180, limit: 300, over: false },
        { id: 2, name: 'Transport', total: 95, limit: null, over: false },
      ],
    });
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    await waitFor(() => {
      expect(screen.getByText(/60%/)).toBeInTheDocument(); // Food has a limit
    });
    expect(screen.queryByText(/over budget/)).not.toBeInTheDocument();
  });
```

- [ ] **Step 2: Run the tests to verify they fail**

Run (WEBRUN): `vitest run src/pages/Dashboard.test.tsx`
Expected: FAIL — `getByText('1 over budget')` and `getByText(/122%/)` are not found (nothing renders them yet).

- [ ] **Step 3: Add imports**

In `web/src/pages/Dashboard.tsx`, ensure these are imported (add what's missing):

```ts
import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';
```

- [ ] **Step 4: Compute the over-budget count**

In `web/src/pages/Dashboard.tsx`, just after the `const totalCategorySpent = ...` line, add (counts across ALL fetched categories, not just the visible/`gaugeData` ones):

```ts
  const overBudgetCount = categories.filter((cat) => cat.over).length;
```

- [ ] **Step 5: Add the header badge**

In the "Spending by Category" `CardHeader`, replace the standalone total `<span>` with a wrapper that shows the badge when `overBudgetCount > 0`:

```tsx
            <div className="flex items-center gap-2">
              {overBudgetCount > 0 && (
                <Badge variant="destructive" className="text-xs">
                  {overBudgetCount} over budget
                </Badge>
              )}
              <span className="font-mono text-lg font-semibold tabular-nums">
                {formatFull(totalCategorySpent)}
              </span>
            </div>
```

- [ ] **Step 6: Render the per-category budget bar**

In the `gaugeData.map((slice) => { ... })` body, add a `budgetPct` computation alongside the existing `pct`:

```tsx
                  const pct = totalCategorySpent > 0
                    ? (slice.value / totalCategorySpent) * 100
                    : 0;
                  const budgetPct = slice.limit != null && slice.limit > 0
                    ? Math.round((slice.value / slice.limit) * 100)
                    : 0;
```

Then, immediately after the existing share-bar `<div className="h-5 w-full rounded-full bg-muted">…</div>`, still inside the per-slice `<div key={slice.id} …>`, add the budget bar:

```tsx
                      {slice.limit != null && (
                        <div className="flex flex-col gap-1">
                          <div className="flex items-center justify-between text-xs text-muted-foreground">
                            <span>
                              {formatFull(slice.value)} / {formatFull(slice.limit)} · {budgetPct}%
                            </span>
                            {slice.over && (
                              <span className="font-medium text-destructive">
                                ⚠ over {formatFull(slice.value - slice.limit)}
                              </span>
                            )}
                          </div>
                          <div className="h-1.5 w-full rounded-full bg-muted">
                            <div
                              className={cn(
                                'h-full rounded-full transition-all',
                                slice.over ? 'bg-destructive' : 'bg-primary',
                              )}
                              style={{ width: `${Math.min(budgetPct, 100)}%` }}
                            />
                          </div>
                        </div>
                      )}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run (WEBRUN): `vitest run src/pages/Dashboard.test.tsx`
Expected: PASS (the two new tests + all existing Dashboard tests).

- [ ] **Step 8: Typecheck**

Run (WEBRUN): `tsc --noEmit`
Expected: PASS (exit 0).

- [ ] **Step 9: Commit**

```bash
git add web/src/pages/Dashboard.tsx web/src/pages/Dashboard.test.tsx
git commit -m "feat(web): show per-category budget bar + over-budget badge on dashboard"
```

---

### Task 6: Full-suite verification

**Files:** none (verification only).

- [ ] **Step 1: Full Go race suite**

Run (GOTEST): `go test -race ./...`
Expected: PASS (all packages `ok`).

- [ ] **Step 2: Frontend tests + lint**

Run (WEBRUN): `vitest run`
Then run (WEBRUN): `tsc --noEmit && eslint .` (the `lint` script).
Expected: both PASS.

- [ ] **Step 3: Browser verification (manual / verify skill)**

Rebuild dev (`docker compose -f docker-compose.dev.yml up --build -d`), log in, set a category limit below current spend in **Settings → Category Limits**, and confirm the Dashboard "Spending by Category" card shows that category's budget bar in red with an "⚠ over $X" badge and a "N over budget" header badge. (No commit.)

---

## Self-review

**1. Spec coverage:**
- Shared Go rule (single source of truth) → Task 1 (helper) + Task 2 (Homepage consumes it) + Task 3 (dashboard consumes it). ✓
- Augment `dashboard/categories` with `limit` (dollars) + `over` → Task 3. ✓
- Dashboard card: untouched share bar + dedicated budget bar (caps 100%, destructive when over, "over $X" badge) → Task 5 Step 6. ✓
- Passive `N over budget` header badge, hidden at 0, counted across all categories → Task 5 Steps 4–5. ✓
- Red/over state driven by API `over` flag, not a float recompute → Task 5 uses `slice.over` for color/badge; `budgetPct` is display-only. ✓
- Tests: pure-helper cases → Task 1; handler `limit`-in-dollars + `*_HidesTombstoned` → Task 3; Homepage count unchanged → Task 2; frontend states → Task 5. ✓
- Out of scope (no migration, Settings unchanged, no zero-spend rows, no amber state) → respected; no task touches them. ✓

**2. Placeholder scan:** No TBD/TODO; every code step shows complete code; every run step has an exact command + expected result. ✓

**3. Type consistency:** `categoryBudgetStatus{LimitCents int64, Over bool}` and `overBudgetByCategory(spend []database.SumByCategoryForMonthRow, limits []database.CategoryBudget)` are used identically in Tasks 1, 2, 3. `categoryEntry.Limit *float64` (dollars) ↔ frontend `CategoryBreakdownItem.limit: number | null` ↔ slice `limit`/`over` ↔ test fixtures all agree. `parseYearMonth` returns `(int, int)`, cast to `int64` for `ListCategoryBudgetsByMonthParams`. ✓
