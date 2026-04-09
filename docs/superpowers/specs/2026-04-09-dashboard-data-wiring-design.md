# Dashboard Data Wiring Redesign

Fix incorrect data wiring, remove broken budget card, add savings progress, and make the dashboard honest to the data model.

## Context

The v3 dashboard design is visually complete but has several data wiring issues discovered during review:

1. **Category bar/percentage mismatch** — Bar width uses one denominator (largest category), percentage label uses another (total spending). Visually confusing.
2. **Monthly Budget card is architecturally broken** — Shows per-category spending vs the entire household budget with arbitrary color thresholds. There are no per-category budgets in the DB, so the UX is misleading.
3. **Recent Transactions ignores month/year filter** — Always shows the 6 most recent transactions globally, while everything else on the page responds to the time selector.
4. **Unused backend fields** — `savings_ytd`, `savings_goal`, `savings_goal_progress`, `savings_this_month` are returned by the API and typed in the frontend but never rendered.
5. **"1Y" cash flow toggle is misleading** — Groups 12 months into partial calendar-year bars instead of showing individual months.

## Changes

### 1. Remove Monthly Budget Card, Add Savings Progress Card

**Remove:**
- The Monthly Budget JSX block: comment `{/* ── Monthly Budget ── */}` through its closing `</div>` (~lines 585–639 in Dashboard.tsx)
- The `budgetGradient` function (lines 75–79)
- The `budgetTotal` derived variable (line 173)
- All budget-related CSS classes in Dashboard.module.css (`.budgetBar`, `.budgetLabel`, `.budgetRow`, etc. — grep for `budget` to find them all)

**Add:** A "Savings Progress" card in the same grid position (right column of `content-grid`, next to Cash Flow).

**Data source:** All fields already exist in `DashboardSummary` from `GET /api/dashboard/summary`:

| Field | What it means |
|-------|---------------|
| `savings_this_month` | `total_income - total_spent` for the selected month |
| `savings_goal` | Annual target from `savings_goals` table |
| `savings_ytd` | Cumulative `income - expenses` from Jan to selected month |
| `savings_goal_progress` | `savings_ytd / savings_goal * 100` (capped at 100) |

**UI structure:**

Full-circle donut chart using Recharts `PieChart` with two data slices:
- Filled slice: value = `savings_goal_progress`, fill = `var(--color-primary)` (#5347CE)
- Remainder slice: value = `Math.max(0, 100 - savings_goal_progress)`, fill = `var(--border-default)`

Guard: clamp `savings_goal_progress` to 0–100 on the frontend even though the backend caps it, for safety.

Center text: `${Math.round(savings_goal_progress)}%` with "of goal" label below. Round to integer for clean display since the raw value is a float.

Below the ring, a 3-column stat row using the existing `formatCompact` helper for brevity:
- "Saved YTD" → `formatCompact(savings_ytd)`
- "Annual Goal" → `formatCompact(savings_goal)`
- "This Month" → `formatCompact(savings_this_month)`

**CSS classes to add in Dashboard.module.css:**
- `.savingsRing` — wrapper for the Recharts PieChart, centered, fixed size
- `.savingsCenter` — absolutely positioned center label over the ring
- `.savingsCenterPct` — the percentage number (large, bold, primary color)
- `.savingsCenterLabel` — "of goal" text (small, tertiary color)
- `.savingsStats` — 3-column flex row below the ring, with top border
- `.savingsStat` — individual stat column (centered text)
- `.savingsStatValue` — the number (15px, weight 600)
- `.savingsStatLabel` — the label (11px, tertiary color)

**Edge case — no savings goal set:** When `savings_goal` is 0 or null, show an empty ring with a message: "Set a savings goal in Settings to track progress." Link to `/settings`.

### 2. Fix Category Bar/Percentage Denominator Mismatch

**Current (broken):**
```ts
// Bar width — relative to LARGEST category
const barPct = totalCategorySpent > 0
  ? (cat.value / gaugeData[0].value) * 100  // denominator = max
  : 0;

// Percentage label — relative to TOTAL spending
const pct = (cat.value / totalCategorySpent * 100);  // denominator = total
```

**Fixed:** Both use total spending as the denominator:
```ts
const pct = totalCategorySpent > 0
  ? (cat.value / totalCategorySpent) * 100
  : 0;
// Use `pct` for both bar width and label
```

The bar for the largest category will no longer be 100% wide — it will be proportional to its share of total spending (e.g., 37% if Food is $1,200 out of $3,200). This is visually correct and matches what the percentage label says.

### 3. Fix Recent Transactions — Respect Month/Year Filter with Toggle

**Current:** Fetches `GET /api/transactions?per_page=6` on mount with empty dependency array `[]`. Never re-fetches, never filters by month.

**Fixed:**

Add two modes controlled by a boolean state `showLatest`:
- **Filtered mode (default):** `GET /api/transactions?per_page=6&date_from=YYYY-MM-01&date_to=YYYY-MM-{lastDay}`
- **Latest mode:** `GET /api/transactions?per_page=6` (no date filter)

The backend's `handleListTransactions` calls `buildTransactionWhereClause` (in `export_handlers.go`) which supports `date_from` and `date_to` query params in `YYYY-MM-DD` format. There are no `year`/`month` params — the frontend must compute month boundaries:

```ts
const startOfMonth = `${selectedYear}-${String(selectedMonth).padStart(2, '0')}-01`;
const lastDay = new Date(selectedYear, selectedMonth, 0).getDate();
const endOfMonth = `${selectedYear}-${String(selectedMonth).padStart(2, '0')}-${String(lastDay).padStart(2, '0')}`;
```

**UI:**
- Card subtitle shows `"{MONTHS[selectedMonth-1]} {selectedYear}"` in filtered mode, "Latest activity" in latest mode
- Toggle link in card header: `"Show latest →"` / `"Show {MONTHS[selectedMonth-1]} →"` depending on current mode
- Re-fetch when `selectedMonth`, `selectedYear`, or `showLatest` changes (add to useEffect dependency array)

### 4. Fix Cash Flow Toggle — 6M / 12M

**Current:** State type `CashFlowView = 'monthly' | 'yearly'` with variable `cashFlowView`. "monthly" shows 6 individual bars (correct). "yearly" aggregates by calendar year (broken).

**Fixed:**

Rename the state type and values:
```ts
type CashFlowView = '6m' | '12m';
const [cashFlowView, setCashFlowView] = useState<CashFlowView>('6m');
```

Remove the `yearlyChartData` computed value (lines 120–136) entirely. Replace `monthlyChartData` and `chartData` with a single computed value:

```ts
const chartData = (() => {
  const sorted = [...trend].reverse();
  const sliced = cashFlowView === '6m' ? sorted.slice(-6) : sorted;
  return sliced.map((item) => ({
    name: SHORT_MONTHS[item.month - 1],
    income: item.total_income,
    expense: -item.total_spent,
  }));
})();
```

Update the toggle buttons in JSX to show "6M" / "12M" labels and use the new state values.

If the trend array has fewer than 12 entries (new account, mid-year start), the 12M view simply shows fewer bars — no special handling needed since the map works on whatever data is available.

### 5. Minor: Use Server-Computed savings_this_month

The "Total Balance" KPI card currently recomputes `totalIncome - totalExpense` client-side. The backend already returns `savings_this_month` with the same value. No user-visible change, but reduces redundant computation. Optional cleanup — low priority.

## Files to Modify

| File | Changes |
|------|---------|
| `web/src/pages/Dashboard.tsx` | Remove budget section + `budgetGradient` + `budgetTotal`, add savings card, fix category bars, fix transactions filter, fix chart toggle type/values |
| `web/src/styles/Dashboard.module.css` | Remove budget-related styles, add savings card styles (`.savingsRing`, `.savingsCenter`, `.savingsCenterPct`, `.savingsCenterLabel`, `.savingsStats`, `.savingsStat`, `.savingsStatValue`, `.savingsStatLabel`) |
| `web/src/pages/Dashboard.test.tsx` | Update tests: add savings card assertions, update transactions mock for filtered fetch, remove budget card test assertions, update chart toggle test if any |

`web/src/App.test.tsx` needs no changes — the `useDashboard` mock already includes all savings fields in the summary object, and the App-level test only asserts that the heading renders.

## Files NOT Modified

| File | Reason |
|------|--------|
| Backend Go code | All required data is already returned by existing endpoints. `date_from`/`date_to` already supported on transactions. |
| `useDashboard.ts` | Hook already fetches all needed fields |
| `api/types.ts` | `DashboardSummary` already includes all savings fields |
| `tokens.css` | No new tokens needed — reuses `--color-primary`, `--border-default`, `--text-tertiary` |

## Testing

- Verify savings card renders with mock data including `savings_ytd`, `savings_goal`, `savings_goal_progress`, `savings_this_month`
- Verify savings card shows fallback message when `savings_goal` is 0
- Verify category bars and percentages use the same denominator
- Verify recent transactions re-fetches when month/year changes
- Verify "Show latest" toggle switches between filtered and global transactions
- Verify 12M chart shows 12 individual monthly bars
- Verify budget section is fully removed (no orphaned styles or components, no dead `budgetTotal` variable)
- Run full suite: `vitest run`, `tsc --noEmit`, `stylelint`
