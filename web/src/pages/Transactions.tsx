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
      .catch((err) => {
        console.warn('Failed to load categories', err);
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
    const chips: { key: string; label: string; onClear: () => void }[] = [];

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
        key: 'date',
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
        key: 'category',
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
        key: 'amount',
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
        key: 'tags',
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
            <span key={chip.key} className={styles.activeChip}>
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
