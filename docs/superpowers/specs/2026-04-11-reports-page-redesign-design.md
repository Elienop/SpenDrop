# Reports Page Redesign — Design Spec

## Overview

Redesign the Reports page from a flat 4-section scroll into a tabbed layout with 12 report sections (4 existing, 8 new). All charts follow established shadcn/Recharts patterns. Data accuracy is the primary goal.

**Household model:** All data is shared across household members. No user_id filtering on reports — both users see the same aggregated household data.

---

## Page Structure

shadcn `Tabs` component with 4 tabs. Each tab is a **separate React component** — hooks are called at component mount, and lazy loading is achieved via conditional rendering (`{activeTab === 'overview' && <OverviewTab />}`). This avoids violating the Rules of Hooks while keeping data fetching per-tab. Default tab: **Overview**.

```
/reports
  ├─ [Tab] Overview    — big picture financials
  ├─ [Tab] Spending    — where money goes
  ├─ [Tab] Savings     — progress toward goals
  └─ [Tab] Patterns    — behavioral insights
```

Responsive: 2-column grid on desktop, 1-column on mobile (`md:grid-cols-2`).

---

## Tab 1: Overview

### 1.1 Income vs Expenses (existing — relocated)

- **Chart:** Grouped `BarChart` (income + expenses per month)
- **Controls:** Period selector (6m / 12m / 24m) via `Select`
- **Endpoint:** `GET /api/reports/income-expenses?months=N` (existing)
- **No changes** to chart logic or data

### 1.2 Net Cash Flow (new)

- **Chart:** `AreaChart` with single `Area` — cumulative `(income - expenses)` over time
- **Controls:** Period toggle (6m / 12m) via `Select`
- **Data source:** Reuse `GET /api/reports/income-expenses?months=N` (existing)
- **Frontend computation:** Running sum of `net` field from income-expenses response. Note: cumulative starts from 0 at the beginning of the selected period, not from any actual account balance — this is a relative trend indicator, not an absolute balance.
- **Colors:** `hsl(var(--primary))` stroke, gradient fill 0.8→0.05 opacity
- **Layout:** Half-width, beside Income vs Expenses

### 1.3 Budget vs Actual (new)

- **Chart:** Grouped `BarChart` — budget bar + actual spending bar per month
- **Controls:** Year selector via `Select`
- **Endpoint:** `GET /api/reports/budget-vs-actual?year=N` (new)
- **Response:** `{ data: [{ month, budget, actual }] }` — always returns 12 entries (Jan–Dec). `month` is a 1-indexed integer; frontend maps to display name via `MONTH_NAMES[month - 1]`.
- **Backend logic:** Compose `ListBudgetsByYear` + `SumByMonthRange` (existing queries). **Budget fallback:** For months with no explicit budget row, fall back to the `default_budget` app setting (same logic as `handleDashboardSummary`). If neither exists, budget is 0.
- **Colors:**
  - Budget bar: `hsl(var(--chart-3))` — verified as blue (206 100% 39%) in globals.css
  - Actual bar: conditional coloring via Recharts `<Cell>` component inside `<Bar>`. Each `<Cell>` receives `fill` based on whether `actual <= budget` (use `hsl(var(--chart-8))` — amber, 48 96% 53%) or `actual > budget` (use `hsl(var(--chart-10))` — red, 346 77% 50%). This is a different rendering pattern from standard static `fill` — the `Bar` component renders `{data.map((entry, i) => <Cell key={i} fill={...} />)}` children.
- **ChartConfig keys:** `budget`, `actual` (camelCase)
- **Layout:** Full-width, below the two half-width charts

---

## Tab 2: Spending

### 2.1 Category Breakdown (new)

- **Chart:** `PieChart` + `Pie` with `innerRadius`/`outerRadius` (donut)
- **Controls:** Month/year selectors via `Select`
- **Endpoint:** `GET /api/dashboard/categories?year=N&month=N` (existing)
- **Response key:** `{ categories: [...] }` — note the wrapper key is `categories`, not `data`. The hook must unwrap via `.then((res) => res.categories)`.
- **Frontend computation:** Percentage = `category.total / sum(all totals)`
- **Center label:** Total spending amount formatted as currency
- **Colors:** `getCategoryColorVar({ id })` per category
- **Tooltip:** Category name, amount, percentage
- **Layout:** Half-width

### 2.2 Category Trends (existing — relocated)

- **Chart:** `LineChart` with one `Line` per top 6 expense categories
- **Controls:** Period selector (6m / 12m / 24m) via `Select`
- **Endpoint:** `GET /api/reports/category-trends?months=N` (existing)
- **No changes** to chart logic or data
- **Layout:** Half-width

### 2.3 Top Merchants (existing — relocated)

- **Chart:** Ranked list with numbered items
- **Controls:** Month/year selectors via `Select`
- **Endpoint:** `GET /api/reports/top-merchants?year=N&month=N&limit=10` (existing)
- **No changes** to list logic or data
- **Layout:** Half-width

### 2.4 Expense Velocity (new)

- **Chart:** `LineChart` with three `Line` elements:
  1. **Current month** (solid, `--primary`, `strokeWidth={2}`): cumulative daily spending
  2. **Budget pace** (dashed via `strokeDasharray="5 5"`, `--chart-3`): linear line from 0 to monthly budget
  3. **Previous month** (dotted via `strokeDasharray="2 2"`, `--primary/0.35`): previous month's cumulative daily spending as overlay
- **Controls:** Month/year selector via `Select`
- **Endpoint:** `GET /api/reports/expense-velocity?year=N&month=N` (new)
- **Response:**
  ```json
  {
    "days_in_month": 30,
    "budget": 5000,
    "current": [{ "day": 1, "daily_total": 120.50 }, ...],
    "previous": [{ "day": 1, "daily_total": 85.00 }, ...]
  }
  ```
- **Backend returns raw daily totals** (`daily_total`). **Frontend computes cumulative** running sum for display. This keeps the backend query simple (`SumExpensesByDayInMonth`) and gives the frontend control over the cumulative calculation.
- **Budget pace line:** Generated on the frontend as `[{ day: 1, pace: budget/days_in_month }, { day: days_in_month, pace: budget }]`.
- **Budget resolution:** Same fallback as §1.3 — monthly budget row → `default_budget` setting → 0. If 0, the pace line is hidden.
- **New SQL query:** `SumExpensesByDayInMonth` — `SELECT CAST(strftime('%d', t.date) AS INTEGER) AS day, CAST(COALESCE(SUM(t.amount), 0) AS REAL) AS daily_total FROM transactions t JOIN categories c ON t.category_id = c.id WHERE c.type = 'expense' AND strftime('%Y', t.date) = CAST(sqlc.arg(year) AS TEXT) AND strftime('%m', t.date) = CAST(sqlc.arg(month) AS TEXT) GROUP BY day ORDER BY day`. **Important:** The Go handler must zero-pad the month parameter as `fmt.Sprintf("%02d", month)` before passing to the query, matching the existing pattern for `SumExpensesByMonth`.
- **Layout:** Half-width

---

## Tab 3: Savings

### 3.1 Savings Goal Progress (new)

- **Chart:** `RadialBarChart` + `RadialBar` showing YTD savings as percentage of yearly goal
- **Inner display:** Dollar amount saved + percentage text
- **Below radial:** Small `AreaChart` showing cumulative monthly savings (income - expenses) from Jan through current month
- **Controls:** Year selector via `Select`
- **Data sources (all existing):**
  - `GetSavingsGoal(year)` → target amount
  - `SumByMonthRange(Jan 1 → Dec 31)` → monthly income/expense breakdown
- **Frontend computation:** Cumulative savings = running sum of `(income - expenses)` per month, progress = `ytd_saved / target * 100`, clamped to 0%
- **No goal set:** If `GetSavingsGoal` returns `sql.ErrNoRows`, the radial chart shows a disabled/empty state with text: `"No savings goal set for [year]. Set one in Settings."` The cumulative area chart below still renders (savings data exists regardless of whether a goal is set).
- **Colors:** `hsl(var(--primary))` for radial bar, `hsl(var(--muted))` for unfilled track
- **Layout:** Half-width

### 3.2 Year-over-Year (existing — relocated)

- **Chart:** Grouped `BarChart` comparing current vs previous year expenses
- **Controls:** Year selector via `Select`
- **Endpoint:** `GET /api/reports/year-over-year?year=N` (existing)
- **No changes** to chart logic or data
- **Layout:** Half-width

---

## Tab 4: Patterns

### 4.1 Spending Heatmap (new)

- **Chart:** Custom CSS grid (not Recharts). GitHub contribution graph style.
- **Structure:** 7 columns (Mon–Sun) × ~52 rows per year. Each cell = one day.
- **Controls:** Year selector via `Select`
- **Endpoint:** `GET /api/reports/spending-heatmap?year=N` (new)
- **Response:** `{ data: [{ date: "2026-01-15", total: 245.50 }, ...] }`
- **New SQL query:** `SumExpensesByDay` — `SELECT t.date, CAST(COALESCE(SUM(t.amount), 0) AS REAL) AS total FROM transactions t JOIN categories c ON t.category_id = c.id WHERE c.type = 'expense' AND strftime('%Y', t.date) = CAST(sqlc.arg(year) AS TEXT) GROUP BY t.date ORDER BY t.date`
- **Color intensity:** 5-level scale based on percentile distribution of daily totals. Use inline `style={{ opacity }}` on each cell div, with a base `backgroundColor` of `hsl(var(--chart-5))`:
  - No spending: `className="bg-muted"` (no inline style)
  - P0–P25: `style={{ backgroundColor: 'hsl(var(--chart-5))', opacity: 0.3 }}`
  - P25–P50: `style={{ backgroundColor: 'hsl(var(--chart-5))', opacity: 0.5 }}`
  - P50–P75: `style={{ backgroundColor: 'hsl(var(--chart-5))', opacity: 0.75 }}`
  - P75–P100: `style={{ backgroundColor: 'hsl(var(--chart-5))', opacity: 1 }}`
- **Tooltip:** Standard shadcn `Tooltip` (not chart tooltip) showing date + total
- **Layout:** Full-width

### 4.2 Recurring Expenses (new)

- **Chart:** shadcn `Table` component
- **Columns:** Description, Monthly Avg, Frequency (e.g., "10/12 months"), Annual Total, Dismiss (ghost `X` button)
- **Controls:** Year selector via `Select`
- **Endpoint:** `GET /api/reports/recurring?year=N` (new)
- **Response:** `{ data: [{ description, monthly_avg, month_count, annual_total }] }`
- **New SQL query:** `RecurringDescriptions` — `SELECT t.description, COUNT(DISTINCT strftime('%Y-%m', t.date)) AS month_count, CAST(COALESCE(SUM(t.amount), 0) AS REAL) AS annual_total FROM transactions t JOIN categories c ON t.category_id = c.id WHERE c.type = 'expense' AND strftime('%Y', t.date) = CAST(sqlc.arg(year) AS TEXT) GROUP BY t.description HAVING COUNT(DISTINCT strftime('%Y-%m', t.date)) >= 3 ORDER BY annual_total DESC`. Note: HAVING repeats the full expression instead of using the alias to ensure sqlc codegen compatibility. Note: uses `strftime('%Y-%m', ...)` to avoid cross-year false positives. `monthly_avg` is computed in Go as `annual_total / month_count`.
- **Dismiss:** `POST /api/reports/recurring/dismiss` with `{ year, description }`. Stored in `app_settings` as key `dismissed_recurring_<year>` with JSON array of dismissed description strings. Backend filters these out from the GET response. Dismiss is idempotent — dismissing an already-dismissed description is a no-op returning 200. Route registered in `router.go` inside the authenticated `/api` group.
- **Layout:** Full-width

### 4.3 Tag Analysis (new)

- **Chart:** Horizontal `BarChart` (bars left to right), one bar per tag
- **Controls:** Period selector — month/year or YTD via `Select`
- **Endpoint:** `GET /api/reports/tag-breakdown?year=N&month=N` (new), month=0 for YTD
- **Response:** `{ data: [{ tag, total, count }] }`
- **Backend implementation:** Tag parsing is done in **Go application code**, not SQL. The `tags` column stores comma-separated text (e.g., `"groceries,weekly"`). The handler queries all expense transactions for the period, then iterates in Go: split each `tags` string by comma, trim whitespace, and aggregate into a `map[string]{ total, count }`. This avoids complex SQLite string splitting and is more maintainable. No new sqlc query needed — reuse `ListTransactions` with date filters or write a simple raw query that returns `(amount, tags)` pairs.
- **Colors:** Cycle through `hsl(var(--chart-1))` to `hsl(var(--chart-11))` using index modulo
- **Empty state:** `"No tagged transactions yet. Add tags to your transactions to see breakdowns here."` in `text-muted-foreground`
- **Layout:** Full-width

---

## New Backend Endpoints

| Endpoint | Method | Params | Response |
|----------|--------|--------|----------|
| `/api/reports/budget-vs-actual` | GET | `year` | `{ data: [{ month, budget, actual }] }` |
| `/api/reports/expense-velocity` | GET | `year, month` | `{ days_in_month, budget, current: [{day, daily_total}], previous: [{day, daily_total}] }` |
| `/api/reports/spending-heatmap` | GET | `year` | `{ data: [{ date, total }] }` |
| `/api/reports/recurring` | GET | `year` | `{ data: [{ description, monthly_avg, month_count, annual_total }] }` |
| `/api/reports/recurring/dismiss` | POST | `{ year, description }` | `{ ok: true }` |
| `/api/reports/tag-breakdown` | GET | `year, month` (month=0 for YTD) | `{ data: [{ tag, total, count }] }` |

## New SQL Queries

| Query | Purpose |
|-------|---------|
| `SumExpensesByDay` | Daily expense totals for a year (heatmap) |
| `SumExpensesByDayInMonth` | Daily expense totals for a specific month (velocity) |
| `RecurringDescriptions` | Descriptions in 3+ distinct months within a year |

Note: `SumByTag` is handled in Go application code, not SQL (see §4.3).

## Reused Existing Endpoints (no changes)

| Endpoint | Used by |
|----------|---------|
| `GET /api/reports/income-expenses` | Overview: Income vs Expenses + Net Cash Flow |
| `GET /api/reports/year-over-year` | Savings: Year-over-Year |
| `GET /api/reports/category-trends` | Spending: Category Trends |
| `GET /api/reports/top-merchants` | Spending: Top Merchants |
| `GET /api/dashboard/categories` | Spending: Category Breakdown (donut) — response key is `categories` |

---

## shadcn Chart Conventions (mandatory for all charts)

Every chart card follows this structure:

```tsx
<Card aria-labelledby="<id>-heading">
  <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
    <CardTitle id="<id>-heading" className="text-base font-semibold">Title</CardTitle>
    {/* Controls: Select, ButtonGroup */}
  </CardHeader>
  <CardContent>
    {loading && <Skeleton className="h-[300px] w-full" />}
    {error && <Alert variant="destructive"><AlertCircle /><AlertDescription>{error}</AlertDescription></Alert>}
    {data && (
      <ChartContainer config={config} className={cn("h-[300px] w-full transition-opacity duration-200", fetching && !loading && "opacity-60")}>
        {/* Recharts chart */}
      </ChartContainer>
    )}
  </CardContent>
</Card>
```

**Recharts conventions:**
- `ChartConfig` keys: camelCase identifiers only (no hyphens, spaces, or special characters)
- Colors: `hsl(var(--primary))`, `hsl(var(--primary) / 0.35)`, `hsl(var(--chart-N))`, or `getCategoryColorVar()`
- `BarChart`/`AreaChart`: `linearGradient` defs with `stopOpacity` 0.8→0.1
- `CartesianGrid vertical={false}`
- `XAxis tickLine={false} axisLine={false} tickMargin={10}`
- `ChartTooltip content={<ChartTooltipContent />}`
- `ChartLegend content={<ChartLegendContent />}`
- `Bar radius={[4, 4, 0, 0]}`
- `Line strokeWidth={2} dot={false}`
- Static `ChartConfig` objects hoisted outside component when possible
- **Exception:** Budget vs Actual uses `<Cell>` for conditional per-bar coloring (see §1.3)

---

## Frontend Architecture

### New Hooks (in `web/src/hooks/useReports.ts`)

Add to existing file, following the same pattern as existing hooks:

- `useBudgetVsActual(year: number)`
- `useExpenseVelocity(year: number, month: number)`
- `useSpendingHeatmap(year: number)`
- `useRecurring(year: number)`
- `useTagBreakdown(year: number, month: number)`

Each hook returns `{ data, loading, fetching, error }` matching existing hook pattern.

**Dismiss mutation:** `dismissRecurring` is a standalone exported async function (not part of the hook), following the existing pattern where mutations are direct `api.post(...)` calls. The component calls it in an event handler and manually triggers a refetch by bumping a state key.

### New Types (in `web/src/api/types.ts`)

```typescript
interface BudgetVsActualEntry {
  month: number        // 1-indexed, frontend maps via MONTH_NAMES[month - 1]
  budget: number
  actual: number
}

interface ExpenseVelocityData {
  days_in_month: number
  budget: number       // 0 if no budget set
  current: { day: number; daily_total: number }[]
  previous: { day: number; daily_total: number }[]
}

interface HeatmapEntry {
  date: string         // ISO date "YYYY-MM-DD"
  total: number
}

interface RecurringEntry {
  description: string
  monthly_avg: number
  month_count: number
  annual_total: number
}

interface TagBreakdownEntry {
  tag: string
  total: number
  count: number
}
```

### Component Structure

Each tab is a separate component to support lazy loading via conditional rendering:

```
web/src/pages/Reports.tsx (shell with Tabs)
  └─ Tabs (shadcn)
      ├─ OverviewTab.tsx
      │   ├─ IncomeExpensesChart (existing logic, relocated)
      │   ├─ NetCashFlowChart (new)
      │   └─ BudgetVsActualChart (new)
      ├─ SpendingTab.tsx
      │   ├─ CategoryBreakdownChart (new, donut)
      │   ├─ CategoryTrendsChart (existing logic, relocated)
      │   ├─ TopMerchantsList (existing logic, relocated)
      │   └─ ExpenseVelocityChart (new)
      ├─ SavingsTab.tsx
      │   ├─ SavingsProgressChart (new, radial + area)
      │   └─ YearOverYearChart (existing logic, relocated)
      └─ PatternsTab.tsx
          ├─ SpendingHeatmap (new, custom grid)
          ├─ RecurringExpensesTable (new)
          └─ TagAnalysisChart (new)
```

Tab components live in `web/src/components/reports/`. Individual chart components are extracted when they exceed ~150 lines; otherwise they remain inline within the tab component.
