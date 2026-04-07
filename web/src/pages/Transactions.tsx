import { useState, useEffect, useCallback } from 'react';
import { api } from '../api/client';
import type { Category, SavedFilter } from '../api/types';
import { useTransactions } from '../hooks/useTransactions';
import { useSavedFilters } from '../hooks/useSavedFilters';
import { FilterBar } from '../components/FilterBar';
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
    setPage,
    loading,
    error,
    createTransaction,
    updateTransaction,
    deleteTransaction,
  } = useTransactions();

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

  const totalPages = Math.max(1, Math.ceil(total / perPage));

  return (
    <div className={styles.page}>
      <div className={styles.pageHeader}>
        <h1>Transactions</h1>
        <button type="button" className={styles.exportButton} onClick={handleExport}>
          Export Excel
        </button>
      </div>

      <FilterBar
        filters={filters}
        setFilter={setFilter}
        categories={categories}
        onClear={clearFilters}
        savedFilters={savedFilters}
        onSaveFilter={handleSaveFilter}
        onLoadFilter={handleLoadFilter}
        onDeleteFilter={handleDeleteFilter}
      />

      <TransactionEntry
        categories={categories}
        onSubmit={createTransaction}
      />

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
