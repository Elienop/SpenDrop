# Transactions Page Toolbar Redesign

Simplify the transactions page from 3 always-visible sections (filter bar, entry form, table) to a single compact toolbar with expandable panels.

## Context

The current transactions page shows ~12+ filter controls and a 5-field entry form at all times, pushing the actual transaction table far down the page. Most visits are for viewing or searching, not adding or applying complex filters.

## Design

### Toolbar (always visible)

A single row inside a glass card: `Search input | Type toggle | Filters button | + Add button`

- **Search input** — left-aligned, takes remaining space. Bound to `filters.search`. Magnifying glass icon prefix.
- **Type toggle** — segmented control (All / Expenses / Income). Bound to `filters.type`. Same logic as current type buttons.
- **Filters button** — toggles the filter panel open/closed. Shows a `var(--color-primary)` dot indicator when any non-search filter is active. Label changes to "Filters (N)" where N is the count of active filter groups (date = `dateFrom` or `dateTo` set, category = `categoryIds` or `categoryId` set, amount = `amountMin` or `amountMax` set, tags = `tags` set — 4 possible groups max).
- **+ Add button** — primary filled button. Toggles the entry form open/closed. Label changes to "Cancel" when the form is open.
- Dividers (1px vertical lines) separate search, type toggle, and the right-side buttons.

### Active Filter Chips (below toolbar, conditional)

When filters are applied and the filter panel is closed, show small dismissible chips summarizing active filters:

- Date chip: "Mar 1 - Apr 9" when both set. "From Mar 1" when only `dateFrom`. "Until Apr 9" when only `dateTo`.
- Category chip: active when `categoryIds` OR `categoryId` is non-empty. Displays comma-joined category names from the `categories` array using the IDs. Falls back to `categoryId` if `categoryIds` is empty.
- Amount chip: "$50 - $200" when both set. "Min $50" when only min. "Max $200" when only max.
- Tags chip: e.g. "groceries, dining"

Each chip has an "x" button that clears that specific filter group. This replaces the current "Clear" button with something more informative and targeted.

Chips only show when the filter panel is closed and at least one non-search filter is active.

### Filter Panel (expandable, below toolbar)

Slides down below the toolbar when "Filters" is clicked. Glass card styling. Contains 4 tabs:

**Date tab:**
- Date preset chips: This Month, Last Month, This Year
- Date range inputs: from/to date pickers

**Category tab:**
- Multi-select category chips (same as current, colored when active)
- Tags text input below the chips

**Amount tab:**
- Min/max amount range inputs

**Saved tab:**
- List of saved filter chips (click to load, x to delete)
- "+ Save current" button (dashed border, same as current)

Top-right of the panel: "Clear all" link button that resets all filters (including `categoryId`, `categoryIds`, `dateFrom`, `dateTo`, `amountMin`, `amountMax`, `tags`).

The save filter button retains the current `window.prompt('Name this filter:')` approach for naming. No change from current behavior.

### Entry Form (expandable, below toolbar)

The existing `TransactionEntry` component, hidden/shown below the toolbar when `showEntry` is toggled. Uses CSS `display: none` when hidden (not conditional rendering) to preserve form state — the user may have a half-typed entry when they close/reopen. No internal changes to the component.

### Transitions

No slide animations. Panels show/hide instantly via `display: none` / `display: block`. This matches the project pattern of removing transition animations (see commit `af49f82`).

### Page Structure (top to bottom)

1. Page header: "Transactions" title + "Export Excel" button
2. Toolbar (always visible)
3. Active filter chips (conditional — when filters active and panel closed)
4. Filter panel (conditional — when Filters button toggled)
5. Entry form (conditional — when + Add button toggled)
6. Transaction table with pagination

## State

Two new boolean states in `Transactions.tsx`:
- `showFilters` — toggles filter panel visibility
- `showEntry` — toggles entry form visibility

All filter state remains in `useTransactions` hook unchanged. No backend changes.

## Components

| Component | File | Status |
|-----------|------|--------|
| `TransactionToolbar` | `web/src/components/TransactionToolbar.tsx` | **New** — toolbar row with search, toggle, buttons |
| `FilterPanel` | `web/src/components/FilterPanel.tsx` | **New** — tabbed filter panel |
| `FilterBar` | `web/src/components/FilterBar.tsx` | **Delete** — replaced by FilterPanel |
| `TransactionEntry` | `web/src/components/TransactionEntry.tsx` | **No change** |
| `TransactionRow` | `web/src/components/TransactionRow.tsx` | **No change** |

## Component Props

**TransactionToolbar:**
```ts
interface TransactionToolbarProps {
  search: string;
  onSearchChange: (value: string) => void;
  type: string;
  onTypeChange: (value: string) => void;
  activeFilterCount: number;
  showFilters: boolean;
  onToggleFilters: () => void;
  showEntry: boolean;
  onToggleEntry: () => void;
}
```

**FilterPanel:**
```ts
interface FilterPanelProps {
  filters: FilterValues;
  setFilter: (key: keyof FilterValues, value: string) => void;
  clearFilters: () => void;
  categories: Category[];
  savedFilters: SavedFilter[];
  onSaveFilter: (name: string) => void;
  onLoadFilter: (filter: SavedFilter) => void;
  onDeleteFilter: (id: number) => void;
}
```

## Styles

All new styles go in `web/src/styles/Transactions.module.css`. Remove old filter bar classes (`.filterBar`, `.filterRow`, `.datePresets`, `.presetButton`, `.dateInputs`, `.typeToggle`, `.typeButton`, `.typeActive`, `.filterSelect`, `.searchInput`, `.clearButton`, `.multiSelect`, `.categoryChip`, `.categoryChipActive`, `.amountRange`, `.savedFilters`, `.savedFilterChip`, `.savedFilterButton`, `.savedFilterDelete`, `.saveFilterButton`).

New classes needed:

**Toolbar:**
- `.toolbar` — glass card, flex row, align-items center, gap 8px, padding 10px 16px
- `.toolbarSearch` — flex 1, with icon positioning
- `.toolbarDivider` — 1px vertical separator
- `.filterButton` — raised surface button with icon
- `.filterDot` — small colored indicator dot
- `.addButtonToolbar` — primary filled, compact

**Active Filter Chips:**
- `.activeChips` — flex row, gap 6px, wrapping
- `.activeChip` — small pill with text and x button
- `.activeChipRemove` — x button inside chip

**Filter Panel:**
- `.filterPanel` — glass card, margin-top 8px
- `.filterTabs` — tab row with bottom border
- `.filterTab` — individual tab button
- `.filterTabActive` — active tab with accent underline
- `.filterTabContent` — content area with padding
- `.clearAllButton` — text link in top-right of panel

**Reused from current:** `.entryForm`, `.entryField`, `.entryLabel`, `.entryInput`, `.entrySelect`, `.addButton` (for entry form). All table, pagination, action button, badge, and tag input styles remain unchanged.

## Files to Modify

| File | Changes |
|------|---------|
| `web/src/components/TransactionToolbar.tsx` | Create: toolbar component |
| `web/src/components/FilterPanel.tsx` | Create: tabbed filter panel component |
| `web/src/components/FilterBar.tsx` | Delete: no longer used |
| `web/src/pages/Transactions.tsx` | Modify: replace FilterBar + always-visible entry with Toolbar + conditional panels |
| `web/src/styles/Transactions.module.css` | Modify: remove old filter styles, add toolbar/panel/chip styles |
| `web/src/pages/Transactions.test.tsx` | Modify: update for new component structure |

## Files NOT Modified

| File | Reason |
|------|--------|
| Backend Go code | No API changes needed |
| `TransactionEntry.tsx` | Component unchanged, just conditionally rendered |
| `TransactionRow.tsx` | No changes |
| `useTransactions.ts` | Hook unchanged, same filter state interface |
| `useSavedFilters.ts` | Hook unchanged |
| `tokens.css` | No new tokens needed |

## Testing

### Existing tests to rewrite

All 5 saved-filter tests in `Transactions.test.tsx` reference `FilterBar` markup that no longer exists. They must be rewritten to:
1. Click the "Filters" toolbar button first to open the panel
2. Switch to the "Saved" tab
3. Then assert on saved filter chips, save button, load/delete behavior

The "renders Export Excel button" and "export URL" tests remain valid — no changes needed.
The "renders Tags column" test remains valid — no changes needed.

### New tests

- Toolbar renders with search input, type toggle (All/Expenses/Income), Filters button, + Add button
- Clicking Filters button toggles filter panel visibility
- Clicking + Add button toggles entry form visibility
- + Add button label changes to "Cancel" when entry form is open
- Filter panel tabs switch content (Date/Category/Amount/Saved)
- Active filter chips appear when filters are set and panel is closed
- Clicking chip x clears that specific filter group
- Filters button shows "(N)" count when filters are active
- Accessibility: Filters button has `aria-expanded`, + Add button has `aria-expanded`
- Run: `vitest run`, `tsc --noEmit`, `stylelint`
