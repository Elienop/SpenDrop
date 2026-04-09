# Transactions Toolbar Redesign Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the always-visible filter bar and entry form with a compact toolbar that hides complexity behind expandable panels.

**Architecture:** Two new components (`TransactionToolbar`, `FilterPanel`) replace `FilterBar`. The toolbar is always visible; the filter panel and entry form toggle via boolean state in `Transactions.tsx`. Filter state stays in the existing `useTransactions` hook, which gains a `clearPanelFilters` function and an exported `TransactionFilters` type.

**Tech Stack:** React 18, TypeScript, CSS Modules, Vitest + Testing Library, date-fns

**Spec:** `docs/superpowers/specs/2026-04-09-transactions-toolbar-design.md`

---

## File Structure

| File | Responsibility | Status |
|------|---------------|--------|
| `web/src/hooks/useTransactions.ts` | Export `TransactionFilters`, add `clearPanelFilters` | Modify |
| `web/src/components/TransactionToolbar.tsx` | Toolbar row: search, type toggle, filter/add buttons | Create |
| `web/src/components/FilterPanel.tsx` | Tabbed filter panel: Date, Category, Amount, Saved | Create |
| `web/src/styles/Transactions.module.css` | Remove old filter bar classes, add toolbar/panel/chip classes | Modify |
| `web/src/pages/Transactions.tsx` | Wire toolbar + conditional panels, compute activeFilterCount, active chips | Modify |
| `web/src/components/FilterBar.tsx` | No longer used | Delete |
| `web/src/pages/Transactions.test.tsx` | Rewrite saved-filter tests, add toolbar/panel tests | Modify |

---

## Chunk 1: Hook + Components

### Task 1: Export TransactionFilters and add clearPanelFilters

**Files:**
- Modify: `web/src/hooks/useTransactions.ts`

- [ ] **Step 1: Export the TransactionFilters interface**

In `web/src/hooks/useTransactions.ts`, change line 5 from:

```ts
interface TransactionFilters {
```

to:

```ts
export interface TransactionFilters {
```

- [ ] **Step 2: Add clearPanelFilters function**

In `web/src/hooks/useTransactions.ts`, after the `clearFilters` callback (after line 121), add:

```ts
  const clearPanelFilters = useCallback(() => {
    setFilters((prev) => ({
      ...prev,
      dateFrom: '',
      dateTo: '',
      categoryId: '',
      categoryIds: '',
      amountMin: '',
      amountMax: '',
      tags: '',
    }));
    setPage(1);
  }, []);
```

- [ ] **Step 3: Update UseTransactionsResult interface**

Add `clearPanelFilters` to the `UseTransactionsResult` interface (after line 39):

```ts
  clearPanelFilters: () => void;
```

- [ ] **Step 4: Add clearPanelFilters to the return object**

In the return statement (after line 155 `clearFilters,`), add:

```ts
    clearPanelFilters,
```

- [ ] **Step 5: Verify types compile**

Run: `cd web && npx tsc --noEmit`
Expected: no errors

- [ ] **Step 6: Commit**

```bash
git add web/src/hooks/useTransactions.ts
git commit -m "feat: export TransactionFilters and add clearPanelFilters to hook"
```

---

### Task 2: Create TransactionToolbar component

**Files:**
- Create: `web/src/components/TransactionToolbar.tsx`

- [ ] **Step 1: Create the toolbar component**

Create `web/src/components/TransactionToolbar.tsx`:

```tsx
import styles from '../styles/Transactions.module.css';

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

export function TransactionToolbar({
  search,
  onSearchChange,
  type,
  onTypeChange,
  activeFilterCount,
  showFilters,
  onToggleFilters,
  showEntry,
  onToggleEntry,
}: TransactionToolbarProps) {
  return (
    <div className={styles.toolbar}>
      {/* Search */}
      <div className={styles.toolbarSearch}>
        <span className={styles.toolbarSearchIcon} aria-hidden="true">
          &#128269;
        </span>
        <input
          type="text"
          className={styles.toolbarSearchInput}
          placeholder="Search transactions..."
          value={search}
          onChange={(e) => onSearchChange(e.target.value)}
          aria-label="Search transactions"
        />
      </div>

      {/* Divider */}
      <div className={styles.toolbarDivider} />

      {/* Type Toggle */}
      <div className={styles.typeToggle}>
        <button
          type="button"
          className={`${styles.typeButton} ${type === '' ? styles.typeActive : ''}`}
          onClick={() => onTypeChange('')}
        >
          All
        </button>
        <button
          type="button"
          className={`${styles.typeButton} ${type === 'expense' ? styles.typeActive : ''}`}
          onClick={() => onTypeChange('expense')}
        >
          Expenses
        </button>
        <button
          type="button"
          className={`${styles.typeButton} ${type === 'income' ? styles.typeActive : ''}`}
          onClick={() => onTypeChange('income')}
        >
          Income
        </button>
      </div>

      {/* Divider */}
      <div className={styles.toolbarDivider} />

      {/* Filters Button */}
      <button
        type="button"
        className={styles.filterButton}
        onClick={onToggleFilters}
        aria-expanded={showFilters}
      >
        {activeFilterCount > 0 && (
          <span className={styles.filterDot} aria-hidden="true" />
        )}
        Filters{activeFilterCount > 0 ? ` (${activeFilterCount})` : ''}
      </button>

      {/* + Add / Cancel Button */}
      <button
        type="button"
        className={styles.addButtonToolbar}
        onClick={onToggleEntry}
        aria-expanded={showEntry}
      >
        {showEntry ? 'Cancel' : '+ Add'}
      </button>
    </div>
  );
}
```

- [ ] **Step 2: Verify types compile**

Run: `cd web && npx tsc --noEmit`
Expected: will fail because new CSS module classes don't exist yet — that's expected at this stage.

- [ ] **Step 3: Commit**

```bash
git add web/src/components/TransactionToolbar.tsx
git commit -m "feat: create TransactionToolbar component"
```

---

### Task 3: Create FilterPanel component

**Files:**
- Create: `web/src/components/FilterPanel.tsx`

- [ ] **Step 1: Create the filter panel component**

Create `web/src/components/FilterPanel.tsx`:

```tsx
import { useState } from 'react';
import {
  startOfMonth,
  endOfMonth,
  subMonths,
  startOfYear,
  format,
} from 'date-fns';
import type { TransactionFilters } from '../hooks/useTransactions';
import type { Category, SavedFilter } from '../api/types';
import styles from '../styles/Transactions.module.css';

type FilterTab = 'date' | 'category' | 'amount' | 'saved';

interface FilterPanelProps {
  filters: TransactionFilters;
  setFilter: (key: keyof TransactionFilters, value: string) => void;
  clearPanelFilters: () => void;
  categories: Category[];
  savedFilters: SavedFilter[];
  onSaveFilter: (name: string) => void;
  onLoadFilter: (filter: SavedFilter) => void;
  onDeleteFilter: (id: number) => void;
}

export function FilterPanel({
  filters,
  setFilter,
  clearPanelFilters,
  categories,
  savedFilters,
  onSaveFilter,
  onLoadFilter,
  onDeleteFilter,
}: FilterPanelProps) {
  const [activeTab, setActiveTab] = useState<FilterTab>('date');

  const now = new Date();

  function setDatePreset(preset: 'thisMonth' | 'lastMonth' | 'thisYear') {
    let from: Date;
    let to: Date;
    switch (preset) {
      case 'thisMonth':
        from = startOfMonth(now);
        to = endOfMonth(now);
        break;
      case 'lastMonth': {
        const last = subMonths(now, 1);
        from = startOfMonth(last);
        to = endOfMonth(last);
        break;
      }
      case 'thisYear':
        from = startOfYear(now);
        to = now;
        break;
    }
    setFilter('dateFrom', format(from, 'yyyy-MM-dd'));
    setFilter('dateTo', format(to, 'yyyy-MM-dd'));
  }

  function handleSaveFilter() {
    const name = window.prompt('Name this filter:');
    if (name) {
      onSaveFilter(name);
    }
  }

  return (
    <div className={styles.filterPanel}>
      {/* Tabs + Clear all */}
      <div className={styles.filterTabsRow}>
        <div className={styles.filterTabs}>
          {(['date', 'category', 'amount', 'saved'] as const).map((tab) => (
            <button
              key={tab}
              type="button"
              className={`${styles.filterTab} ${activeTab === tab ? styles.filterTabActive : ''}`}
              onClick={() => setActiveTab(tab)}
            >
              {tab.charAt(0).toUpperCase() + tab.slice(1)}
            </button>
          ))}
        </div>
        <button
          type="button"
          className={styles.clearAllButton}
          onClick={clearPanelFilters}
        >
          Clear all
        </button>
      </div>

      {/* Tab Content */}
      <div className={styles.filterTabContent}>
        {activeTab === 'date' && (
          <>
            <div className={styles.datePresets}>
              <button
                type="button"
                className={styles.presetButton}
                onClick={() => setDatePreset('thisMonth')}
              >
                This Month
              </button>
              <button
                type="button"
                className={styles.presetButton}
                onClick={() => setDatePreset('lastMonth')}
              >
                Last Month
              </button>
              <button
                type="button"
                className={styles.presetButton}
                onClick={() => setDatePreset('thisYear')}
              >
                This Year
              </button>
            </div>
            <div className={styles.dateInputs}>
              <input
                type="date"
                className={styles.filterInput}
                value={filters.dateFrom}
                onChange={(e) => setFilter('dateFrom', e.target.value)}
                aria-label="Date from"
              />
              <span className={styles.dateSeparator}>to</span>
              <input
                type="date"
                className={styles.filterInput}
                value={filters.dateTo}
                onChange={(e) => setFilter('dateTo', e.target.value)}
                aria-label="Date to"
              />
            </div>
          </>
        )}

        {activeTab === 'category' && (
          <>
            <div className={styles.multiSelect}>
              {categories.map((cat) => {
                const ids = filters.categoryIds
                  ? filters.categoryIds.split(',')
                  : [];
                const selected = ids.includes(String(cat.id));
                return (
                  <button
                    key={cat.id}
                    type="button"
                    className={`${styles.categoryChip} ${selected ? styles.categoryChipActive : ''}`}
                    style={
                      selected
                        ? { backgroundColor: cat.color, borderColor: cat.color }
                        : {}
                    }
                    onClick={() => {
                      const next = selected
                        ? ids.filter((id) => id !== String(cat.id))
                        : [...ids, String(cat.id)];
                      setFilter('categoryIds', next.join(','));
                    }}
                  >
                    {cat.name}
                  </button>
                );
              })}
            </div>
            <input
              type="text"
              className={styles.filterInput}
              placeholder="Filter by tags..."
              value={filters.tags}
              onChange={(e) => setFilter('tags', e.target.value)}
              aria-label="Filter by tags"
              style={{ marginTop: 'var(--space-2)' }}
            />
          </>
        )}

        {activeTab === 'amount' && (
          <div className={styles.amountRange}>
            <input
              type="number"
              className={styles.filterInput}
              placeholder="Min $"
              value={filters.amountMin}
              onChange={(e) => setFilter('amountMin', e.target.value)}
              step="0.01"
              min="0"
              aria-label="Minimum amount"
            />
            <span className={styles.dateSeparator}>to</span>
            <input
              type="number"
              className={styles.filterInput}
              placeholder="Max $"
              value={filters.amountMax}
              onChange={(e) => setFilter('amountMax', e.target.value)}
              step="0.01"
              min="0"
              aria-label="Maximum amount"
            />
          </div>
        )}

        {activeTab === 'saved' && (
          <div className={styles.savedFilters}>
            {savedFilters.map((sf) => (
              <div key={sf.id} className={styles.savedFilterChip}>
                <button
                  type="button"
                  className={styles.savedFilterButton}
                  onClick={() => onLoadFilter(sf)}
                >
                  {sf.name}
                </button>
                <button
                  type="button"
                  className={styles.savedFilterDelete}
                  onClick={() => onDeleteFilter(sf.id)}
                  aria-label={`Delete saved filter ${sf.name}`}
                >
                  x
                </button>
              </div>
            ))}
            <button
              type="button"
              className={styles.saveFilterButton}
              onClick={handleSaveFilter}
            >
              + Save Filter
            </button>
          </div>
        )}
      </div>
    </div>
  );
}
```

- [ ] **Step 2: Commit**

Note: `tsc --noEmit` will fail at this point because new CSS module classes (`.filterPanel`, `.filterTabsRow`, etc.) don't exist yet. They are added in Task 4. Skip type-checking until Task 5.

```bash
git add web/src/components/FilterPanel.tsx
git commit -m "feat: create FilterPanel component with tabbed layout"
```

---

### Task 4: Update CSS — remove old filter bar styles, add toolbar/panel/chip styles

**Files:**
- Modify: `web/src/styles/Transactions.module.css`

- [ ] **Step 1: Remove unused filter bar classes**

Remove **only** these CSS classes from `web/src/styles/Transactions.module.css`. All other filter-related classes (`.datePresets`, `.presetButton`, `.dateInputs`, `.dateSeparator`, `.filterInput`, `.typeToggle`, `.typeButton`, `.typeActive`, `.multiSelect`, `.categoryChip`, `.categoryChipActive`, `.amountRange`, `.savedFilters`, `.savedFilterChip`, `.savedFilterButton`, `.savedFilterDelete`, `.saveFilterButton`) are reused by the new components and must be kept.

Remove these 5 class blocks:
- `.filterBar` and `.filterBar:hover` (lines 10–27, replaced by `.toolbar`)
- `.filterRow` (lines 29–34, no longer needed)
- `.filterSelect` (lines 103–108, not used anywhere)
- `.searchInput` (lines 110–116, replaced by `.toolbarSearchInput`)
- `.clearButton` and `.clearButton:hover` (lines 118–131, replaced by `.clearAllButton`)

- [ ] **Step 2: Add toolbar styles**

Add after the Page Header section (after `.exportButton:hover`), before the closing of the file:

```css
/* ===== Toolbar ===== */
.toolbar {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  background: var(--glass-bg);
  backdrop-filter: blur(var(--glass-blur));
  /* stylelint-disable-next-line property-no-vendor-prefix */
  -webkit-backdrop-filter: blur(var(--glass-blur));
  border: 1px solid var(--glass-border);
  border-radius: 14px;
  padding: 10px var(--space-4);
  box-shadow: var(--shadow-sm);
}

.toolbarSearch {
  flex: 1;
  position: relative;
  min-width: 200px;
}

.toolbarSearchIcon {
  position: absolute;
  left: 10px;
  top: 50%;
  transform: translateY(-50%);
  color: var(--text-tertiary);
  font-size: 14px;
  pointer-events: none;
}

.toolbarSearchInput {
  width: 100%;
  background-color: var(--surface-raised);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  padding: var(--space-2) var(--space-2) var(--space-2) 32px;
  color: var(--text-primary);
  font-size: 14px;
  min-height: 36px;
  box-sizing: border-box;
}

.toolbarSearchInput:focus {
  border-color: var(--color-primary);
  outline: none;
}

.toolbarDivider {
  width: 1px;
  height: 28px;
  background-color: var(--border-default);
  flex-shrink: 0;
}

.filterButton {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: var(--space-2) var(--space-3);
  background-color: var(--surface-raised);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  color: var(--text-secondary);
  font-size: 13px;
  font-weight: 500;
  flex-shrink: 0;
  transition: all var(--duration-fast) var(--ease-standard);
}

.filterButton:hover {
  background-color: var(--primary-a15);
  color: var(--color-primary);
  border-color: var(--color-primary);
}

.filterDot {
  width: 6px;
  height: 6px;
  border-radius: var(--radius-full);
  background-color: var(--color-primary);
}

.addButtonToolbar {
  padding: var(--space-2) 18px;
  background-color: var(--color-primary);
  color: var(--text-inverse);
  border: none;
  border-radius: var(--radius-md);
  font-size: 13px;
  font-weight: 500;
  flex-shrink: 0;
  white-space: nowrap;
  transition: opacity var(--duration-fast) var(--ease-standard);
}

.addButtonToolbar:hover {
  opacity: 0.8;
}

/* ===== Active Filter Chips ===== */
.activeChips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.activeChip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 4px 10px;
  background-color: var(--primary-a15);
  color: var(--color-primary);
  border-radius: var(--radius-full);
  font-size: 12px;
  font-weight: 500;
}

.activeChipRemove {
  background: none;
  border: none;
  color: var(--color-primary);
  cursor: pointer;
  font-size: 14px;
  padding: 0;
  line-height: 1;
  opacity: 0.7;
}

.activeChipRemove:hover {
  opacity: 1;
}

/* ===== Filter Panel ===== */
.filterPanel {
  background: var(--glass-bg);
  backdrop-filter: blur(var(--glass-blur));
  /* stylelint-disable-next-line property-no-vendor-prefix */
  -webkit-backdrop-filter: blur(var(--glass-blur));
  border: 1px solid var(--glass-border);
  border-radius: 14px;
  box-shadow: var(--shadow-sm);
  overflow: hidden;
}

.filterTabsRow {
  display: flex;
  align-items: center;
  justify-content: space-between;
  border-bottom: 1px solid var(--border-default);
  padding: 0 var(--space-4);
}

.filterTabs {
  display: flex;
}

.filterTab {
  padding: var(--space-3) var(--space-4);
  color: var(--text-secondary);
  font-size: 13px;
  font-weight: 500;
  border: none;
  background: none;
  border-bottom: 2px solid transparent;
  cursor: pointer;
  transition: color var(--duration-fast) var(--ease-standard);
}

.filterTab:hover {
  color: var(--text-primary);
}

.filterTabActive {
  color: var(--color-primary);
  font-weight: 600;
  border-bottom-color: var(--color-primary);
}

.filterTabContent {
  padding: var(--space-4);
}

.clearAllButton {
  padding: var(--space-1) var(--space-2);
  color: var(--text-secondary);
  font-size: 13px;
  font-weight: 500;
  background: none;
  border: none;
  cursor: pointer;
  transition: color var(--duration-fast) var(--ease-standard);
}

.clearAllButton:hover {
  color: var(--color-error);
}

/* ===== Entry Form Wrapper ===== */
.entryFormHidden {
  display: none;
}
```

- [ ] **Step 3: Verify stylelint passes**

Run: `cd web && npx stylelint "src/**/*.css"`
Expected: no errors

- [ ] **Step 4: Commit**

```bash
git add web/src/styles/Transactions.module.css
git commit -m "feat: add toolbar, filter panel, and active chip styles"
```

---

### Task 5: Rewrite Transactions page — wire toolbar + conditional panels

**Files:**
- Modify: `web/src/pages/Transactions.tsx`
- Delete: `web/src/components/FilterBar.tsx`

- [ ] **Step 1: Rewrite Transactions.tsx**

Replace the entire content of `web/src/pages/Transactions.tsx` with:

```tsx
import { useState, useEffect, useCallback, useMemo } from 'react';
import { format } from 'date-fns';
import { api } from '../api/client';
import type { Category, SavedFilter } from '../api/types';
import { useTransactions } from '../hooks/useTransactions';
import { useSavedFilters } from '../hooks/useSavedFilters';
import { TransactionToolbar } from '../components/TransactionToolbar';
import { FilterPanel } from '../components/FilterPanel';
import { TransactionEntry } from '../components/TransactionEntry';
import { TransactionRow } from '../components/TransactionRow';
import styles from '../styles/Transactions.module.css';

export function Transactions() {
  const {
    transactions,
    total,
    page,
    perPage,
    filters,
    setFilter,
    clearFilters,
    clearPanelFilters,
    setPage,
    loading,
    error,
    createTransaction,
    updateTransaction,
    deleteTransaction,
  } = useTransactions();

  const [showFilters, setShowFilters] = useState(false);
  const [showEntry, setShowEntry] = useState(false);

  const handleExport = useCallback(() => {
    const params = new URLSearchParams();
    if (filters.dateFrom) params.set('date_from', filters.dateFrom);
    if (filters.dateTo) params.set('date_to', filters.dateTo);
    if (filters.categoryIds) {
      params.set('category_ids', filters.categoryIds);
    } else if (filters.categoryId) {
      params.set('category_id', filters.categoryId);
    }
    if (filters.type) params.set('type', filters.type);
    if (filters.search) params.set('search', filters.search);
    if (filters.amountMin) params.set('amount_min', filters.amountMin);
    if (filters.amountMax) params.set('amount_max', filters.amountMax);
    if (filters.tags) params.set('tags', filters.tags);

    const query = params.toString();
    const url = `/api/export/transactions${query ? `?${query}` : ''}`;
    window.open(url, '_blank');
  }, [filters]);

  const {
    savedFilters,
    saveFilter,
    deleteFilter: deleteSavedFilter,
  } = useSavedFilters();

  const [categories, setCategories] = useState<Category[]>([]);

  useEffect(() => {
    api
      .get<Category[]>('categories')
      .then(setCategories)
      .catch(() => {
        /* non-critical */
      });
  }, []);

  const handleSaveFilter = useCallback(
    (name: string) => {
      saveFilter(name, JSON.stringify(filters));
    },
    [saveFilter, filters],
  );

  const handleLoadFilter = useCallback(
    (sf: SavedFilter) => {
      try {
        const parsed = JSON.parse(sf.filter_json) as Record<string, string>;
        clearFilters();
        for (const [key, value] of Object.entries(parsed)) {
          setFilter(key as keyof typeof filters, value);
        }
      } catch {
        /* invalid JSON — ignore */
      }
    },
    [setFilter, clearFilters],
  );

  const handleDeleteFilter = useCallback(
    (id: number) => {
      deleteSavedFilter(id);
    },
    [deleteSavedFilter],
  );

  // Count active filter groups (excluding search and type — they're in the toolbar)
  const activeFilterCount = useMemo(() => {
    let count = 0;
    if (filters.dateFrom || filters.dateTo) count++;
    if (filters.categoryIds || filters.categoryId) count++;
    if (filters.amountMin || filters.amountMax) count++;
    if (filters.tags) count++;
    return count;
  }, [filters]);

  // Build active filter chips data
  const activeChips = useMemo(() => {
    if (showFilters) return [];
    const chips: { label: string; onClear: () => void }[] = [];

    // Date chip
    if (filters.dateFrom || filters.dateTo) {
      let label: string;
      if (filters.dateFrom && filters.dateTo) {
        label = `${format(new Date(filters.dateFrom), 'MMM d')} - ${format(new Date(filters.dateTo), 'MMM d')}`;
      } else if (filters.dateFrom) {
        label = `From ${format(new Date(filters.dateFrom), 'MMM d')}`;
      } else {
        label = `Until ${format(new Date(filters.dateTo), 'MMM d')}`;
      }
      chips.push({
        label,
        onClear: () => {
          setFilter('dateFrom', '');
          setFilter('dateTo', '');
        },
      });
    }

    // Category chip
    if (filters.categoryIds || filters.categoryId) {
      const ids = filters.categoryIds
        ? filters.categoryIds.split(',')
        : [filters.categoryId];
      const names = ids
        .map((id) => categories.find((c) => String(c.id) === id)?.name)
        .filter(Boolean);
      chips.push({
        label: names.join(', ') || 'Categories',
        onClear: () => {
          setFilter('categoryIds', '');
          setFilter('categoryId', '');
        },
      });
    }

    // Amount chip
    if (filters.amountMin || filters.amountMax) {
      let label: string;
      if (filters.amountMin && filters.amountMax) {
        label = `$${filters.amountMin} - $${filters.amountMax}`;
      } else if (filters.amountMin) {
        label = `Min $${filters.amountMin}`;
      } else {
        label = `Max $${filters.amountMax}`;
      }
      chips.push({
        label,
        onClear: () => {
          setFilter('amountMin', '');
          setFilter('amountMax', '');
        },
      });
    }

    // Tags chip
    if (filters.tags) {
      chips.push({
        label: filters.tags,
        onClear: () => setFilter('tags', ''),
      });
    }

    return chips;
  }, [filters, showFilters, categories, setFilter]);

  const totalPages = Math.max(1, Math.ceil(total / perPage));

  return (
    <div className={styles.page}>
      <div className={styles.pageHeader}>
        <h1>Transactions</h1>
        <button type="button" className={styles.exportButton} onClick={handleExport}>
          Export Excel
        </button>
      </div>

      <TransactionToolbar
        search={filters.search}
        onSearchChange={(v) => setFilter('search', v)}
        type={filters.type}
        onTypeChange={(v) => setFilter('type', v)}
        activeFilterCount={activeFilterCount}
        showFilters={showFilters}
        onToggleFilters={() => setShowFilters((p) => !p)}
        showEntry={showEntry}
        onToggleEntry={() => setShowEntry((p) => !p)}
      />

      {/* Active filter chips — only when filters active and panel closed */}
      {activeChips.length > 0 && (
        <div className={styles.activeChips}>
          {activeChips.map((chip) => (
            <span key={chip.label} className={styles.activeChip}>
              {chip.label}
              <button
                type="button"
                className={styles.activeChipRemove}
                onClick={chip.onClear}
                aria-label={`Clear ${chip.label} filter`}
              >
                &times;
              </button>
            </span>
          ))}
        </div>
      )}

      {/* Filter panel — conditional rendering */}
      {showFilters && (
        <FilterPanel
          filters={filters}
          setFilter={setFilter}
          clearPanelFilters={clearPanelFilters}
          categories={categories}
          savedFilters={savedFilters}
          onSaveFilter={handleSaveFilter}
          onLoadFilter={handleLoadFilter}
          onDeleteFilter={handleDeleteFilter}
        />
      )}

      {/* Entry form — display:none to preserve state */}
      <div className={showEntry ? undefined : styles.entryFormHidden}>
        <TransactionEntry
          categories={categories}
          onSubmit={createTransaction}
        />
      </div>

      {error && (
        <div className={styles.error} role="alert">
          {error}
        </div>
      )}

      {loading ? (
        <div className={styles.loading}>Loading transactions...</div>
      ) : transactions.length === 0 ? (
        <div className={styles.emptyState}>
          No transactions found. Add one above to get started.
        </div>
      ) : (
        <div className={styles.tableWrapper}>
          <table className={styles.table}>
            <thead>
              <tr>
                <th>Date</th>
                <th>Description</th>
                <th>Category</th>
                <th>Tags</th>
                <th>Amount</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {transactions.map((tx) => (
                <TransactionRow
                  key={tx.id}
                  transaction={tx}
                  categories={categories}
                  onUpdate={updateTransaction}
                  onDelete={deleteTransaction}
                />
              ))}
            </tbody>
          </table>

          {totalPages > 1 && (
            <div className={styles.pagination}>
              <button
                type="button"
                className={styles.pageButton}
                disabled={page <= 1}
                onClick={() => setPage(page - 1)}
              >
                Previous
              </button>
              <span className={styles.pageInfo}>
                Page {page} of {totalPages}
              </span>
              <button
                type="button"
                className={styles.pageButton}
                disabled={page >= totalPages}
                onClick={() => setPage(page + 1)}
              >
                Next
              </button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
```

- [ ] **Step 2: Delete FilterBar.tsx**

```bash
rm web/src/components/FilterBar.tsx
```

- [ ] **Step 3: Verify types compile**

Run: `cd web && npx tsc --noEmit`
Expected: no errors (all new CSS classes and component props should be resolved)

- [ ] **Step 4: Commit**

```bash
git add web/src/pages/Transactions.tsx
git rm web/src/components/FilterBar.tsx
git commit -m "feat: wire toolbar and filter panel into Transactions page

Replace FilterBar with TransactionToolbar + FilterPanel.
Entry form uses display:none to preserve form state.
Active filter chips show when panel is closed."
```

---

## Chunk 2: Tests + Quality

### Task 6: Rewrite tests for new component structure

**Files:**
- Modify: `web/src/pages/Transactions.test.tsx`

- [ ] **Step 1: Rewrite the test file**

Replace the entire content of `web/src/pages/Transactions.test.tsx` with:

```tsx
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, it, expect, vi, beforeEach } from 'vitest';

const mockSaveFilter = vi.fn();
const mockDeleteFilter = vi.fn();
const mockSetFilter = vi.fn();
const mockClearFilters = vi.fn();
const mockClearPanelFilters = vi.fn();

const defaultFilters = {
  dateFrom: '',
  dateTo: '',
  categoryId: '',
  categoryIds: '',
  amountMin: '',
  amountMax: '',
  tags: '',
  type: '',
  search: '',
};

const defaultTransaction = {
  id: 1,
  user_id: 1,
  date: '2026-04-01',
  amount: 25.5,
  original_amount: null,
  original_currency: null,
  description: 'Groceries',
  category_id: 1,
  category_name: 'Groceries',
  category_type: 'expense',
  category_color: '#e94560',
  tags: 'food,weekly',
  notes: null,
  created_at: '2026-04-01T00:00:00Z',
  updated_at: '2026-04-01T00:00:00Z',
};

const mockUseTransactions = vi.fn();

// Mock the hooks and api
vi.mock('../api/client', () => ({
  api: {
    get: vi.fn().mockResolvedValue([]),
  },
}));

vi.mock('../hooks/useTransactions', () => ({
  useTransactions: (...args: unknown[]) => mockUseTransactions(...args),
}));

vi.mock('../hooks/useSavedFilters', () => ({
  useSavedFilters: () => ({
    savedFilters: [
      {
        id: 10,
        user_id: 1,
        name: 'Big expenses',
        filter_json: '{"amountMin":"100","amountMax":"500"}',
        created_at: '',
        updated_at: '',
      },
    ],
    loading: false,
    saveFilter: mockSaveFilter,
    deleteFilter: mockDeleteFilter,
    refetch: vi.fn(),
  }),
}));

import { Transactions } from './Transactions';

function defaultHookReturn(overrides = {}) {
  return {
    transactions: [defaultTransaction],
    total: 1,
    page: 1,
    perPage: 20,
    filters: { ...defaultFilters },
    setFilter: mockSetFilter,
    clearFilters: mockClearFilters,
    clearPanelFilters: mockClearPanelFilters,
    setPage: vi.fn(),
    loading: false,
    error: '',
    createTransaction: vi.fn(),
    updateTransaction: vi.fn(),
    deleteTransaction: vi.fn(),
    ...overrides,
  };
}

describe('Transactions page', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockUseTransactions.mockReturnValue(defaultHookReturn());
  });

  describe('toolbar', () => {
    it('renders search input, type toggle, Filters button, and + Add button', () => {
      render(<Transactions />);
      expect(screen.getByLabelText('Search transactions')).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'All' })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Expenses' })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Income' })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Filters' })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: '+ Add' })).toBeInTheDocument();
    });

    it('clicking Filters button toggles filter panel visibility', async () => {
      const user = userEvent.setup();
      render(<Transactions />);

      // Panel not visible initially
      expect(screen.queryByRole('button', { name: 'This Month' })).not.toBeInTheDocument();

      // Open panel
      await user.click(screen.getByRole('button', { name: 'Filters' }));
      expect(screen.getByRole('button', { name: 'This Month' })).toBeInTheDocument();

      // Close panel
      await user.click(screen.getByRole('button', { name: 'Filters' }));
      expect(screen.queryByRole('button', { name: 'This Month' })).not.toBeInTheDocument();
    });

    it('clicking + Add button toggles entry form and changes label to Cancel', async () => {
      const user = userEvent.setup();
      render(<Transactions />);

      // Click + Add
      await user.click(screen.getByRole('button', { name: '+ Add' }));
      expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument();
      expect(screen.queryByRole('button', { name: '+ Add' })).not.toBeInTheDocument();

      // Click Cancel
      await user.click(screen.getByRole('button', { name: 'Cancel' }));
      expect(screen.getByRole('button', { name: '+ Add' })).toBeInTheDocument();
    });

    it('Filters button shows count when filters are active', () => {
      mockUseTransactions.mockReturnValue(
        defaultHookReturn({
          filters: { ...defaultFilters, dateFrom: '2026-01-01', amountMin: '50' },
        }),
      );
      render(<Transactions />);
      expect(screen.getByRole('button', { name: /Filters \(2\)/ })).toBeInTheDocument();
    });

    it('Filters button has aria-expanded attribute', async () => {
      const user = userEvent.setup();
      render(<Transactions />);

      const filtersBtn = screen.getByRole('button', { name: 'Filters' });
      expect(filtersBtn).toHaveAttribute('aria-expanded', 'false');

      await user.click(filtersBtn);
      expect(filtersBtn).toHaveAttribute('aria-expanded', 'true');
    });

    it('+ Add button has aria-expanded attribute', async () => {
      const user = userEvent.setup();
      render(<Transactions />);

      const addBtn = screen.getByRole('button', { name: '+ Add' });
      expect(addBtn).toHaveAttribute('aria-expanded', 'false');

      await user.click(addBtn);
      expect(screen.getByRole('button', { name: 'Cancel' })).toHaveAttribute(
        'aria-expanded',
        'true',
      );
    });
  });

  describe('filter panel tabs', () => {
    it('switches between Date, Category, Amount, and Saved tabs', async () => {
      const user = userEvent.setup();
      render(<Transactions />);

      // Open filter panel
      await user.click(screen.getByRole('button', { name: 'Filters' }));

      // Date tab is default — shows preset buttons
      expect(screen.getByRole('button', { name: 'This Month' })).toBeInTheDocument();

      // Switch to Category tab
      await user.click(screen.getByRole('button', { name: 'Category' }));
      expect(screen.getByLabelText('Filter by tags')).toBeInTheDocument();

      // Switch to Amount tab
      await user.click(screen.getByRole('button', { name: 'Amount' }));
      expect(screen.getByLabelText('Minimum amount')).toBeInTheDocument();

      // Switch to Saved tab
      await user.click(screen.getByRole('button', { name: 'Saved' }));
      expect(screen.getByRole('button', { name: 'Big expenses' })).toBeInTheDocument();
    });
  });

  describe('active filter chips', () => {
    it('shows chips when filters are set and panel is closed', () => {
      mockUseTransactions.mockReturnValue(
        defaultHookReturn({
          filters: { ...defaultFilters, amountMin: '50', amountMax: '200' },
        }),
      );
      render(<Transactions />);
      expect(screen.getByText('$50 - $200')).toBeInTheDocument();
    });

    it('hides chips when filter panel is open', async () => {
      const user = userEvent.setup();
      mockUseTransactions.mockReturnValue(
        defaultHookReturn({
          filters: { ...defaultFilters, amountMin: '50' },
        }),
      );
      render(<Transactions />);
      expect(screen.getByText('Min $50')).toBeInTheDocument();

      // Open panel — chips should hide
      await user.click(screen.getByRole('button', { name: /Filters/ }));
      expect(screen.queryByText('Min $50')).not.toBeInTheDocument();
    });

    it('clicking chip x clears that specific filter group', async () => {
      const user = userEvent.setup();
      mockUseTransactions.mockReturnValue(
        defaultHookReturn({
          filters: { ...defaultFilters, tags: 'groceries' },
        }),
      );
      render(<Transactions />);

      await user.click(screen.getByLabelText('Clear groceries filter'));
      expect(mockSetFilter).toHaveBeenCalledWith('tags', '');
    });
  });

  describe('saved filters integration', () => {
    it('renders saved filter chips in the Saved tab', async () => {
      const user = userEvent.setup();
      render(<Transactions />);

      // Open panel, go to Saved tab
      await user.click(screen.getByRole('button', { name: 'Filters' }));
      await user.click(screen.getByRole('button', { name: 'Saved' }));

      expect(screen.getByRole('button', { name: 'Big expenses' })).toBeInTheDocument();
    });

    it('renders a save filter button in the Saved tab', async () => {
      const user = userEvent.setup();
      render(<Transactions />);

      await user.click(screen.getByRole('button', { name: 'Filters' }));
      await user.click(screen.getByRole('button', { name: 'Saved' }));

      expect(screen.getByRole('button', { name: /save filter/i })).toBeInTheDocument();
    });

    it('clicking a saved filter chip loads its filters via setFilter', async () => {
      const user = userEvent.setup();
      render(<Transactions />);

      await user.click(screen.getByRole('button', { name: 'Filters' }));
      await user.click(screen.getByRole('button', { name: 'Saved' }));
      await user.click(screen.getByRole('button', { name: 'Big expenses' }));

      expect(mockSetFilter).toHaveBeenCalledWith('amountMin', '100');
      expect(mockSetFilter).toHaveBeenCalledWith('amountMax', '500');
    });

    it('calls saveFilter hook with name and current filter JSON on save', async () => {
      const user = userEvent.setup();
      const originalPrompt = window.prompt;
      window.prompt = vi.fn().mockReturnValue('My filter');
      render(<Transactions />);

      await user.click(screen.getByRole('button', { name: 'Filters' }));
      await user.click(screen.getByRole('button', { name: 'Saved' }));
      await user.click(screen.getByRole('button', { name: /save filter/i }));

      expect(mockSaveFilter).toHaveBeenCalledWith('My filter', expect.any(String));
      window.prompt = originalPrompt;
    });

    it('calls deleteFilter hook when delete button is clicked', async () => {
      const user = userEvent.setup();
      render(<Transactions />);

      await user.click(screen.getByRole('button', { name: 'Filters' }));
      await user.click(screen.getByRole('button', { name: 'Saved' }));

      const deleteBtn = screen.getByRole('button', { name: /delete saved filter/i });
      await user.click(deleteBtn);
      expect(mockDeleteFilter).toHaveBeenCalledWith(10);
    });
  });

  describe('export button', () => {
    it('renders an Export Excel button', () => {
      render(<Transactions />);
      expect(
        screen.getByRole('button', { name: /export excel/i }),
      ).toBeInTheDocument();
    });

    it('opens export URL in new tab when clicked with no filters', async () => {
      const user = userEvent.setup();
      const openSpy = vi.spyOn(window, 'open').mockImplementation(() => null);
      render(<Transactions />);

      await user.click(screen.getByRole('button', { name: /export excel/i }));

      expect(openSpy).toHaveBeenCalledTimes(1);
      const url = openSpy.mock.calls[0][0] as string;
      expect(url).toBe('/api/export/transactions');
      expect(openSpy.mock.calls[0][1]).toBe('_blank');
      openSpy.mockRestore();
    });

    it('includes filter params in the export URL when filters are active', async () => {
      mockUseTransactions.mockReturnValue(
        defaultHookReturn({
          filters: {
            dateFrom: '2026-01-01',
            dateTo: '2026-03-31',
            categoryId: '',
            categoryIds: '1,2',
            amountMin: '50',
            amountMax: '500',
            tags: 'food',
            type: 'expense',
            search: 'groceries',
          },
        }),
      );

      const user = userEvent.setup();
      const openSpy = vi.spyOn(window, 'open').mockImplementation(() => null);
      render(<Transactions />);

      await user.click(screen.getByRole('button', { name: /export excel/i }));

      expect(openSpy).toHaveBeenCalledTimes(1);
      const url = new URL(openSpy.mock.calls[0][0] as string, 'http://localhost');
      expect(url.pathname).toBe('/api/export/transactions');
      expect(url.searchParams.get('date_from')).toBe('2026-01-01');
      expect(url.searchParams.get('date_to')).toBe('2026-03-31');
      expect(url.searchParams.get('category_ids')).toBe('1,2');
      expect(url.searchParams.get('amount_min')).toBe('50');
      expect(url.searchParams.get('amount_max')).toBe('500');
      expect(url.searchParams.get('tags')).toBe('food');
      expect(url.searchParams.get('type')).toBe('expense');
      expect(url.searchParams.get('search')).toBe('groceries');
      openSpy.mockRestore();
    });

    it('uses categoryId when categoryIds is empty', async () => {
      mockUseTransactions.mockReturnValue(
        defaultHookReturn({
          filters: { ...defaultFilters, categoryId: '5', categoryIds: '' },
        }),
      );

      const user = userEvent.setup();
      const openSpy = vi.spyOn(window, 'open').mockImplementation(() => null);
      render(<Transactions />);

      await user.click(screen.getByRole('button', { name: /export excel/i }));

      const url = new URL(openSpy.mock.calls[0][0] as string, 'http://localhost');
      expect(url.searchParams.get('category_id')).toBe('5');
      expect(url.searchParams.has('category_ids')).toBe(false);
      openSpy.mockRestore();
    });
  });

  describe('tags column', () => {
    it('renders a Tags column header in the table', () => {
      render(<Transactions />);
      const headers = screen.getAllByRole('columnheader');
      const tagsHeader = headers.find((h) => h.textContent === 'Tags');
      expect(tagsHeader).toBeDefined();
    });
  });
});
```

- [ ] **Step 2: Run tests**

Run: `cd web && npx vitest run`
Expected: all tests pass

- [ ] **Step 3: Commit**

```bash
git add web/src/pages/Transactions.test.tsx
git commit -m "test: rewrite transaction tests for toolbar redesign

Rewrite saved-filter tests to navigate Filters > Saved tab.
Add toolbar, filter panel, active chip, and accessibility tests."
```

---

### Task 7: Quality checks

- [ ] **Step 1: TypeScript strict check**

Run: `cd web && npx tsc --noEmit`
Expected: no errors

- [ ] **Step 2: Stylelint check**

Run: `cd web && npx stylelint "src/**/*.css"`
Expected: no errors

- [ ] **Step 3: Full test suite**

Run: `cd web && npx vitest run`
Expected: all tests pass

- [ ] **Step 4: Fix any issues found**

If any of the above checks fail, fix the issues and re-run.

- [ ] **Step 5: Final commit (if fixes needed)**

Only if fixes were applied in Step 4.

```bash
git add -A
git commit -m "fix: address quality check issues"
```
