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
                  ? filters.categoryIds.split(',').filter(Boolean)
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
