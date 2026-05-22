# Category Over-Budget Display — Design Spec

- **Date:** 2026-05-22
- **Status:** Approved (pending spec review)
- **Branch:** `feat/category-over-budget-display`

## Problem

Per-category monthly limits (migration 012, set in **Settings → Category Limits**) have no in-app surface showing spend against them. A user who sets a limit cannot see whether they have exceeded it. The only consumer of the limits today is the external Homepage widget, which reports a household-wide count via `countOverBudgetCategories` (`internal/api/homepage_handlers.go`).

## Goal

On the Dashboard **"Spending by Category"** card, show each budgeted category's spend against its limit with a clear over-budget indication — without changing how limits are set and without disturbing the card's existing spend-ranked behavior.

## Agreed scope decisions

- **Placement:** Dashboard "Spending by Category" card. The existing per-category share-of-total bar is **untouched**.
- **Indicator:** a dedicated *budget* bar beneath the share bar, only for categories that have a limit. It fills toward the limit (100% = limit), renders the over-portion red, and shows an "over by $X" badge when exceeded.
- **Ranking:** the card stays **spend-ranked** (its source of truth). Budgeted categories are annotated wherever they fall; no pinning. Zero-spend budgeted categories do **not** appear (the card lists only categories with spend).
- **Header hint:** a passive `N over budget` badge in the card header, counted across **all** fetched categories (including those under "Show more"); hidden when `N = 0`; not clickable in v1.
- **Semantics:** month = the card's existing year/month selector; expense categories only; "over" = strict `spent > limit`, computed in integer cents.

## Architecture

### Single source of truth for "over budget"

The Dashboard card and the Homepage widget must agree on the rule. Extract one pure helper and have both consume it.

New pure helper in `internal/api/` (e.g. `category_budget_status.go`):

```go
type categoryBudgetStatus struct {
    LimitCents int64
    Over       bool
}

// overBudgetByCategory maps category id -> its limit and whether household
// month-to-date expense spend strictly exceeds it. Only categories present in
// BOTH spend and limits appear in the result. Pure: spend and limits are passed
// in (no query), so `Over` is computed against the exact spend the caller holds.
func overBudgetByCategory(
    spend  []database.SumByCategoryForMonthRow, // {ID, Name, TotalCents}
    limits []database.CategoryBudget,           // {CategoryID, AmountCents, ...}
) map[int64]categoryBudgetStatus
```

Rule: `Over = spentCents > limitCents` (strict; exactly-at-limit is **not** over). Integer cents only.

### Consumers

- **`handleDashboardCategories`** (`dashboard_handlers.go`): already calls `SumByCategoryForMonth`. Additionally call `ListCategoryBudgetsByMonth(year, month)`, pass both to the helper, and annotate each `categoryEntry`.
- **`countOverBudgetCategories`** (`homepage_handlers.go`): refactor to fetch spend + limits and return the count of `{Over}` entries from the helper. Behavior-preserving — still only spend categories that have a limit, still strict `>`.

### API change (additive, backward-compatible)

`GET /api/dashboard/categories` — `categoryEntry` gains:

```go
type categoryEntry struct {
    ID    int64    `json:"id"`
    Name  string   `json:"name"`
    Total float64  `json:"total"`
    Limit *float64 `json:"limit"` // dollars; null when no limit set
    Over  bool     `json:"over"`  // false when no limit
}
```

`Limit` is **dollars** on the wire (`centsToDollars`), per the Money Wire-Edge DTO Discipline — never the raw `*_cents`. Frontend `CategoryBreakdownItem` (`web/src/api/types.ts`) gains `limit: number | null` and `over: boolean`. Existing consumers (reports `useCategoryBreakdown`) ignore the new fields.

### Frontend — Dashboard card

`web/src/pages/Dashboard.tsx`, "Spending by Category":

- For each slice with `limit != null`, render a second thin bar under the existing share bar:
  - `pct = round((total / limit) * 100)` (guard `limit > 0`) — **display only**.
  - fill width = `min(pct, 100)%`. The bar caps at 100%; when over, the filled bar is rendered in the **destructive** token (the `⚠ over {total − limit}` badge conveys the overage magnitude, since width cannot exceed 100%).
  - caption: `{total} / {limit} · {pct}%`.
  - **The red/over state is driven by the API `over` flag (cents-exact), never a frontend `total > limit` recompute** — computing it once in Go is the entire point of this design.
- Categories with `limit == null`: rendered unchanged (share bar only).
- Card header: a small badge `{count} over budget` when `count = categories.filter(c => c.over).length > 0`; hidden at 0; **destructive** token; passive (not clickable).
- All money formatted via the existing `formatCurrency` + base currency.

## Data flow

1. Card mounts with `(year, month)` from its selector.
2. `useCategoryBreakdown(year, month)` → `GET /api/dashboard/categories` → `[{id, name, total, limit, over}]`, spend-ranked.
3. Render: share bar (`total / totalSpent`) + budget bar (`total / limit`, when `limit` present); header badge from the `over` count.

## Error handling / edge cases

- **No limit row** → `limit: null`, `over: false` (helper omits the category; handler emits null/false). No budget bar.
- **Limit but zero spend** → category absent from the spend rows → not shown (consistent with spend-ranked rule). For the Homepage count this is already a no-op (`0 > limit` is false).
- **Division by zero** → `category_budgets.amount_cents` has `CHECK(amount_cents > 0)`, so a stored limit is always positive; still guard `limit > 0` before computing `pct`.
- **Tombstoned transactions** → `SumByCategoryForMonth` already filters `t.deleted_at IS NULL`, so spend (and therefore `over`) excludes tombstoned rows.

## Testing (per the data-correctness discipline)

- **Go — pure helper:** over / under / exactly-at-limit (not over) / category with limit but no spend / category with spend but no limit / empty inputs.
- **Go — handler:** `dashboard/categories` returns correct `limit` (in **dollars**, not cents) and `over`; assert the wire field is `limit` not `limit_cents` (Money Wire-Edge DTO Discipline); preserve/extend the existing `*_HidesTombstoned` invariant (a tombstoned txn must not inflate spend or flip `over`).
- **Go — Homepage:** existing `countOverBudgetCategories` tests still pass after the refactor (count unchanged).
- **Frontend:** budget bar absent when `limit == null`; normal fill when under; red + "over by $X" badge when `over`; header badge shows the correct count and is hidden at 0; money rendered in base currency.

## Out of scope

- No migration (`category_budgets` exists since 012).
- No change to setting limits (Settings → Category Limits panel unchanged).
- Zero-spend budgeted categories are not shown.
- Header badge is passive in v1; click-to-expand is a possible later enhancement.
- No "approaching limit" (amber) warning state — the budget bar is binary: normal under the limit, destructive when over. An amber near-limit threshold is a possible later enhancement.
