import {
  startOfMonth,
  endOfMonth,
  subMonths,
  startOfYear,
  format,
} from 'date-fns';
import type { Category, SavedFilter } from '../api/types';
import styles from '../styles/Transactions.module.css';

interface FilterValues {
  dateFrom: string;
  dateTo: string;
  categoryId: string;
  categoryIds: string;
  amountMin: string;
  amountMax: string;
  tags: string;
  type: string;
  search: string;
}

interface FilterBarProps {
  filters: FilterValues;
  setFilter: (key: keyof FilterValues, value: string) => void;
  categories: Category[];
  onClear?: () => void;
  savedFilters: SavedFilter[];
  onSaveFilter: (name: string) => void;
  onLoadFilter: (filter: SavedFilter) => void;
  onDeleteFilter: (id: number) => void;
}

export function FilterBar({
  filters,
  setFilter,
  categories,
  onClear,
  savedFilters,
  onSaveFilter,
  onLoadFilter,
  onDeleteFilter,
}: FilterBarProps) {
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

  function handleClear() {
    setFilter('dateFrom', '');
    setFilter('dateTo', '');
    setFilter('categoryId', '');
    setFilter('categoryIds', '');
    setFilter('amountMin', '');
    setFilter('amountMax', '');
    setFilter('tags', '');
    setFilter('type', '');
    setFilter('search', '');
    onClear?.();
  }

  function handleSaveFilter() {
    const name = window.prompt('Name this filter:');
    if (name) {
      onSaveFilter(name);
    }
  }

  return (
    <div className={styles.filterBar}>
      {/* Row 1: Date presets and date range */}
      <div className={styles.filterRow}>
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
      </div>

      {/* Row 2: Type toggle, multi-category chips, amount range, tags, search, clear */}
      <div className={styles.filterRow}>
        <div className={styles.typeToggle}>
          <button
            type="button"
            className={`${styles.typeButton} ${filters.type === '' ? styles.typeActive : ''}`}
            onClick={() => setFilter('type', '')}
          >
            All
          </button>
          <button
            type="button"
            className={`${styles.typeButton} ${filters.type === 'expense' ? styles.typeActive : ''}`}
            onClick={() => setFilter('type', 'expense')}
          >
            Expenses
          </button>
          <button
            type="button"
            className={`${styles.typeButton} ${filters.type === 'income' ? styles.typeActive : ''}`}
            onClick={() => setFilter('type', 'income')}
          >
            Income
          </button>
        </div>

        <div className={styles.multiSelect}>
          {categories.map((cat) => {
            const ids = filters.categoryIds ? filters.categoryIds.split(',') : [];
            const selected = ids.includes(String(cat.id));
            return (
              <button
                key={cat.id}
                type="button"
                className={`${styles.categoryChip} ${selected ? styles.categoryChipActive : ''}`}
                style={selected ? { backgroundColor: cat.color, borderColor: cat.color } : {}}
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

        <input
          type="text"
          className={styles.filterInput}
          placeholder="Filter by tags..."
          value={filters.tags}
          onChange={(e) => setFilter('tags', e.target.value)}
          aria-label="Filter by tags"
        />

        <input
          type="text"
          className={styles.searchInput}
          placeholder="Search transactions..."
          value={filters.search}
          onChange={(e) => setFilter('search', e.target.value)}
          aria-label="Search transactions"
        />

        <button
          type="button"
          className={styles.clearButton}
          onClick={handleClear}
        >
          Clear
        </button>
      </div>

      {/* Row 3: Saved filters */}
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
    </div>
  );
}
