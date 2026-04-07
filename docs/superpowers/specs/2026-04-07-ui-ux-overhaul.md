# SpenDrop UI/UX Overhaul Specification

## Overview

A comprehensive frontend overhaul covering sidebar behavior, page layout constraints, chart theming, card/section styling, tab patterns, and a complete dashboard redesign. All changes build on the existing design system tokens and conventions from the frontend design system spec.

**Goal:** Transform SpenDrop from a functional prototype into a polished, fintech-grade dark-themed dashboard that feels professional and intentional.

**Design references:** Crextio, Twisty, Sequence.io, various financial dashboard patterns.

---

## 1. Sidebar — Toggle Pin (No Hover)

### Problem
The current sidebar auto-expands on hover, which is jarring and unintentional. Icon selection highlight is not centered when collapsed.

### Solution
Replace hover expand/collapse with a user-controlled toggle button. Default to collapsed. Persist state to `localStorage`.

### Behavior
- **Collapsed (default):** 64px width, icon-only, centered icons
- **Expanded:** 240px width, icon + text label
- **Toggle:** Pin/unpin button at the top of the sidebar (hamburger icon or chevron)
- **No hover behavior** — sidebar only changes state via the toggle button
- **Persist** `sidebar-expanded` boolean to `localStorage('spendrop-sidebar')`
- **Transition:** `--duration-slow` with `--ease-decelerate` (expand) / `--ease-accelerate` (collapse)

### Icon Alignment Fix
When collapsed, the active state highlight (40x40px pill) must be horizontally centered within the 64px sidebar width. Currently the `.navLink` container is 40px but not centered — fix by applying `margin: 0 auto` or centering via flex on the sidebar column.

### Files
- `web/src/components/Sidebar.tsx` — remove `onMouseEnter`/`onMouseLeave`, add toggle button, add `localStorage` read/write
- `web/src/styles/Sidebar.module.css` — fix icon centering, remove hover-expand styles, add toggle button styles

---

## 2. Page Max-Width Constraint

### Problem
Pages stretch full width on wide monitors, making content hard to scan.

### Solution
Add `max-width: 1400px` with centered alignment to the main content area.

### Implementation

The sidebar is `position: fixed`, so `.main` uses `padding-left` to offset content past the sidebar, then constrains width and centers:

```css
.main {
  padding-left: calc(64px + var(--space-10)); /* sidebar width + page padding */
  padding-right: var(--space-10);
  padding-top: var(--space-8);
  padding-bottom: var(--space-8);
  max-width: calc(1400px + 64px); /* content max + sidebar */
  margin: 0 auto;
  transition: padding-left var(--duration-slow) var(--ease-decelerate);
}

/* When sidebar is expanded */
.mainExpanded {
  padding-left: calc(240px + var(--space-10));
  max-width: calc(1400px + 240px);
}
```

The `transition` on `padding-left` keeps main content reflow in sync with the sidebar expand/collapse animation. Use `--ease-decelerate` for expand (sidebar growing), `--ease-accelerate` for collapse (sidebar shrinking) — toggled via a class or CSS variable.

### Files
- `web/src/styles/AppLayout.module.css` — add `max-width`, center alignment, sidebar-width-aware padding

---

## 3. Chart Theming — Recharts

### Problem
Charts use black text on dark backgrounds (axis labels invisible), unstyled white tooltips, no rounded bar corners.

### Solution
Create a `useChartTheme()` hook that resolves CSS custom properties to Recharts-compatible props, plus a reusable `<ChartTooltip>` component.

### useChartTheme Hook
Returns an object with resolved colors for:
- `axisStroke` — `--text-tertiary`
- `gridStroke` — `--border-muted` at low opacity
- `tooltipBg` — `--surface-overlay`
- `tooltipBorder` — `--border-default`
- `tooltipText` — `--text-primary`
- `hoverBg` — `--primary-a8` (used for bar chart cursor highlight)
- `incomeColor` — `--color-income`
- `expenseColor` — `--color-expense`
- `categoryColors` — ordered array of 6 colors for donut/pie segments:
  1. `--color-primary` (indigo) — largest category
  2. `--color-income` (green)
  3. `--color-expense` (red/rose)
  4. `--color-warning` (amber)
  5. `--color-info` (blue)
  6. `--text-tertiary` (gray) — "Other" bucket

### Recharts Configuration
```tsx
<XAxis
  tick={{ fill: theme.axisStroke, fontSize: 12, fontFamily: 'Inter Variable' }}
  axisLine={{ stroke: theme.gridStroke }}
  tickLine={false}
/>
<YAxis
  tick={{ fill: theme.axisStroke, fontSize: 12, fontFamily: 'Inter Variable' }}
  axisLine={false}
  tickLine={false}
/>
<CartesianGrid
  strokeDasharray="3 3"
  stroke={theme.gridStroke}
  strokeOpacity={0.4}
  vertical={false}
/>
<Bar radius={[4, 4, 0, 0]} />  /* Recharts auto-flips for negative bars */
<Tooltip content={<ChartTooltip />} cursor={{ fill: theme.hoverBg }} />
```

### Custom Tooltip Component
- Dark background (`--surface-overlay`), border (`--border-default`), `--radius-md` corners
- Compact: month title, income line (green dot + value), expense line (red dot + value)
- Font: `--type-body-sm`, `tabular-nums` for amounts
- No arrow/caret needed

### Donut Chart (Pie)
```tsx
<Pie
  innerRadius={55}
  outerRadius={70}
  cornerRadius={3}
  paddingAngle={2}
  dataKey="value"
/>
```

### Files
- `web/src/hooks/useChartTheme.ts` — new hook
- `web/src/components/ChartTooltip.tsx` — new reusable tooltip
- `web/src/styles/ChartTooltip.module.css` — tooltip styles
- `web/src/pages/Dashboard.tsx` — apply theme to all charts
- `web/src/pages/Reports.tsx` — apply theme to all charts

---

## 4. Card & Section Style — Border-Only Flat Grouped

### Problem
Current cards use `--surface-raised` background which creates a "whitish" look on dark theme that doesn't feel elegant.

### Solution
Switch to border-only cards: transparent background, `1px solid --border-muted` border, inner content separated by divider lines.

### Card Pattern
```css
.card {
  background: transparent;
  border: 1px solid var(--border-muted);
  border-radius: var(--radius-lg);
  padding: var(--space-5);
}
```

No box-shadow. No background tint. Clean border delineation only.

### Grouped Sections (Settings, etc.)
For settings and form sections:
- No outer card wrapper
- Items separated by `1px solid var(--border-muted)` horizontal dividers
- First/last items have no top/bottom border respectively
- Consistent padding between items: `--space-4`

### Files
- `web/src/styles/Dashboard.module.css` — update card styles
- `web/src/styles/Settings.module.css` — replace card backgrounds with divider pattern
- `web/src/styles/Categories.module.css` — same treatment
- Any other page using card containers

---

## 5. Tab Style — Overlapping Underline

### Problem
Selected tab has a colored underline, and there's a separate gray separator line below the tab row. They don't connect, creating a visual gap.

### Solution
The separator line sits at the bottom of the tab container. The selected tab's colored underline (2px) overlaps/replaces the separator at that position, making it look like the separator line becomes thicker/colored under the active tab.

### Implementation
```css
.tabRow {
  display: flex;
  gap: var(--space-6);
  border-bottom: 1px solid var(--border-muted);
  position: relative;
}

.tab {
  padding: var(--space-2) 0 var(--space-3) 0;
  color: var(--text-secondary);
  font-size: var(--type-label-lg-size);
  font-weight: var(--type-label-lg-weight);
  border-bottom: 2px solid transparent;
  margin-bottom: -1px; /* overlap the container border */
  cursor: pointer;
  transition: color var(--duration-fast) var(--ease-standard);
}

.tab.active {
  color: var(--text-primary);
  border-bottom-color: var(--color-primary);
}
```

The key is `margin-bottom: -1px` which pulls the tab's bottom border down to overlap the container's border-bottom.

### Shared Tabs Component

Since both Settings and Categories use tabs, create a reusable component:

- `web/src/components/Tabs.tsx` — `<Tabs>` component accepting `tabs: { key: string, label: string }[]`, `activeKey`, `onTabChange`
- `web/src/styles/Tabs.module.css` — shared tab row + tab item styles using the overlapping pattern above

### Files
- `web/src/components/Tabs.tsx` — new shared component
- `web/src/styles/Tabs.module.css` — new shared styles
- `web/src/pages/Settings.tsx` — replace inline tab implementation with `<Tabs>`
- `web/src/pages/Categories.tsx` — replace inline tab implementation with `<Tabs>`

---

## 6. Dashboard Redesign

### Layout Structure

```
┌─────────────────────────────────────────────────┐
│  Dashboard                    [April ▾] [2026 ▾]│
├───────────────────────┬─────────────────────────┤
│  Total Balance        │  Savings Goal           │
│  $12,480              │  62%    $3,100 / $5,000 │
│  +$1,952 from last mo │  ████████░░  $1,900 rem │
├───────────────────────┴─────────────────────────┤
│  Cash Flow    Income $3,200  Exp $1,248  [M][Y] │
│  ┌─┐ ┌─┐ ┌─┐ ┌─┐ ┌─┐ ┌─┐ ┌─┐ ┌─┐ ...        │
│  │█│ │█│ │█│ │█│ │█│ │█│ │█│ │█│              │
│  │█│ │█│ │ │ │█│ │█│ │ │ │█│ │█│   ← income   │
│  ─────────────────────────────────── 0 line      │
│  │█│ │█│ │█│ │█│ │█│ │█│ │█│ │█│   ← expense  │
│  └─┘ └─┘ └─┘ └─┘ └─┘ └─┘ └─┘ └─┘              │
│  Apr Mar Feb Jan Dec Nov Oct Sep                │
├──────────────────┬──────────────────────────────┤
│  Categories      │  Recent Transactions         │
│  ┌──────────┐    │  Name    Date    Cat   Amount│
│  │  Donut   │    │  ────────────────────────────│
│  │  $1,248  │    │  Supermarket  Apr 6  Groc -85│
│  └──────────┘    │  Moulin d'Or  Apr 5  Din  -42│
│  Groceries  $420 │  Salary dep   Apr 1  Inc +3.2│
│  Transport  $285 │  Electric     Mar 28 Util-165│
│  Dining     $199 │  Amazon       Mar 25 Shop -67│
│  Utilities  $165 │  Uber ride    Mar 22 Tran -18│
│  Shopping   $112 │                              │
│  Other       $67 │          [View All →]        │
└──────────────────┴──────────────────────────────┘
```

### 6.1 Hero Row — Total Balance + Savings Goal

Two equal-width border-only cards.

**Total Balance (left):**

Computed as `total_income - total_spent` from the existing `DashboardSummary` API response. No backend changes needed — the subtraction is done in the frontend component. Delta from last month is computed by comparing current and previous month summaries (both already available via the month/year selectors).

- Label: `--type-body-sm`, `--text-secondary`
- Value: `--type-heading-lg-size` scaled up to 36px (add `--type-display` token: 36px/700/40px), `--text-primary`, `tabular-nums`
- Sub: `--type-body-sm`, `--text-tertiary`, with `--color-income` colored `+$X` delta

**Savings Goal (right):**

Uses `savings_goal_progress` (percentage), `savings_ytd` (current), and `savings_goal` (target) from the existing `DashboardSummary` API response.

- Label: `--type-body-sm`, `--text-secondary`
- Row: percentage (`--type-display` 36px/700, color varies: `--color-income` if ≥75%, `--color-warning` if 25-74%, `--color-expense` if <25%) on left, `$current / $target` (`--text-secondary`) on right, same baseline
- Progress bar: `--space-1` height (4px), `--border-muted` track, fill color matches percentage color, `--radius-sm` corners
- Sub: `--type-body-sm`, `--text-tertiary`, remaining amount

### New Token

Add to `tokens.css`:
```css
:root {
  --type-display-size: 36px;
  --type-display-weight: 700;
  --type-display-line-height: 40px;
}
```

### 6.2 Cash Flow Section

Single border-only card containing everything.

**Top bar (single row):**
- Left: "Cash Flow" title (`--type-heading-sm`)
- Right: Income stat (green dot + label + green value) + Expense stat (red dot + label + red value) + Monthly/Yearly toggle
- The color-coded income/expense values **are** the legend — no separate legend below the chart
- Monthly/Yearly toggle: pill button group with `--primary-a15` active state

**Monthly/Yearly toggle behavior:**
- **Monthly (default):** Shows last 12 months of data. Each bar pair = one month. X-axis labels = 3-letter month abbreviations. Income/expense summary values = current selected month's totals.
- **Yearly:** Shows up to 5 years of data. Each bar pair = one year. X-axis labels = 4-digit years. Income/expense summary values = current selected year's totals.
- Both views use the same API data structure — monthly view filters by the selected year, yearly view aggregates across years. The `useDashboard` hook already accepts month/year params; yearly view passes `month=0` (or omits it) to get full-year aggregation. If the backend doesn't support yearly aggregation yet, the frontend computes it client-side from 12-month data.

**Chart area:**
- Recharts `<BarChart>` with bidirectional bars (income positive up, expenses negative down)
- Bar config: `radius={[4, 4, 0, 0]}`, solid colors (green for income, red for expenses), no gradients
- Tight bar spacing: `barGap={2}`, `barSize` auto-calculated to fill evenly without stretching
- X-axis: month labels, `--text-tertiary`
- Y-axis: currency shorthand (2k, 4k), `--text-tertiary`
- Grid: horizontal only, subtle dotted lines
- Hover: subtle column highlight via `cursor={{ fill: primaryA8 }}`
- Tooltip: compact dark tooltip (month, income value, expense value), follows cursor, small
- Chart container: `aspect-ratio: 3 / 1` to prevent stretching — chart should never distort

### 6.3 Bottom Grid — Categories + Transactions

`grid-template-columns: 2fr 3fr` with `--space-4` gap.

**Categories (left card):**
- Header: "Categories" + "All →" link
- SVG donut chart (centered): `innerRadius={55}`, `outerRadius={70}`, `cornerRadius={3}`, `paddingAngle={2}`
- Center label: total spent amount + "total" sub-label
- Category list below: colored dot + name + amount + mini progress bar
- Items separated by `--border-muted` dividers

**Recent Transactions (right card):**
- Header: "Recent Transactions" + "View All →" link
- **Table format** (not card list):
  - Columns: Name, Date, Category, Amount
  - Header: uppercase `--type-label-sm`, `--text-tertiary`
  - Rows: `--type-body-md`, separated by `--border-muted` dividers
  - Name: `--text-primary`, weight 500
  - Date: `--text-tertiary`, `--type-body-sm`
  - Category: colored pill badge (`color-mix()` 15% bg + full color text)
  - Amount: right-aligned, `tabular-nums`, expense in `--color-expense`, income in `--color-income`
  - **No icons** — we don't have per-category icons
- Shows 6 most recent transactions

---

## 7. Loading & Error States

### Loading
While `useDashboard` returns `loading: true`:
- Hero cards: show skeleton pulse placeholders (gray rectangles animating opacity) for value and sub-text areas
- Cash Flow chart: show skeleton placeholder matching chart aspect ratio (`3:1`)
- Bottom grid cards: show skeleton for donut chart area and table rows (6 rows of skeleton lines)
- Skeleton color: `--border-muted` background with `--surface-hover` pulse via CSS `@keyframes`

### Error
If API calls fail:
- Show a centered error message within the card: "Failed to load data" in `--text-tertiary` with a "Retry" button (`--color-primary` outlined style)
- Do not show empty charts or broken layouts

### Empty State
If no transactions exist for the selected month:
- Charts show empty state with "No data for this month" centered text
- Transaction table shows: "No transactions yet" in `--text-tertiary`

---

## 8. Responsive Behavior

| Breakpoint | Layout Changes |
|------------|---------------|
| > 1200px | Full layout as designed |
| 768-1200px | Hero row stays 2-col, bottom grid becomes single column (categories above transactions) |
| < 768px | Everything single column, sidebar always collapsed |

These breakpoints supersede the 1024px breakpoint from the design system spec. The 1200px threshold better accommodates the dashboard's 2fr/3fr bottom grid which needs more horizontal space than simpler layouts.

---

## 9. Superseded Sections

This spec supersedes the following sections of the frontend design system spec (`2026-04-07-frontend-design-system.md`):
- **Section 5.2 Cards** — replaced by border-only card pattern (Section 4 of this spec)
- **Section 7 Dashboard Layout** — entirely replaced by Section 6 of this spec. The 4-card KPI row, two-column chart section, and budget progress section are replaced with the hero row + cash flow + bottom grid layout. The existing `web/src/components/KPICard.tsx` component should be removed as it's no longer used.

---

## 10. Files Summary

### New Files
- `web/src/hooks/useChartTheme.ts` — chart theme hook resolving CSS vars
- `web/src/components/ChartTooltip.tsx` — reusable dark-themed chart tooltip
- `web/src/styles/ChartTooltip.module.css` — tooltip styles
- `web/src/components/Tabs.tsx` — shared tab component
- `web/src/styles/Tabs.module.css` — shared tab styles

### Modified Files
- `web/src/components/Sidebar.tsx` — toggle pin behavior, localStorage persistence
- `web/src/styles/Sidebar.module.css` — icon centering, toggle button, remove hover-expand
- `web/src/styles/AppLayout.module.css` — max-width 1400px, centered, sidebar-aware padding
- `web/src/pages/Dashboard.tsx` — complete rewrite: hero row, cash flow section, bottom grid, Recharts config
- `web/src/styles/Dashboard.module.css` — new layout styles, border-only cards
- `web/src/pages/Settings.tsx` — flat grouped sections, overlapping tabs
- `web/src/styles/Settings.module.css` — divider-based sections, tab overlap
- `web/src/pages/Categories.tsx` — flat grouped sections, overlapping tabs
- `web/src/styles/Categories.module.css` — matching treatment
- `web/src/pages/Reports.tsx` — chart theming applied
- `web/src/styles/Reports.module.css` — chart theming styles
- `docs/DESIGN_GUIDE.md` — update with new patterns (border-only cards, overlapping tabs, chart theming)
- `README.md` — update features section if needed

---

## 11. Non-Goals

- No light mode changes in this overhaul (dark-first, light follows later)
- No new pages or routes
- No backend changes — all new dashboard values (Total Balance, yearly aggregation) are computed client-side from existing API responses
- No new npm dependencies (Recharts already installed, Lucide already installed)
- No mobile-first responsive — desktop-first with reasonable tablet/mobile fallbacks
